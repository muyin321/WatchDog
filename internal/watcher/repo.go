package watcher

import (
	"log"
	"sync"

	"github.com/watchdog-ai/watchdog/internal/ai"
	"github.com/watchdog-ai/watchdog/internal/model"
)

// Notifier 兼容别名：旧代码若引用 Notifier，语义等同 Reporter。
type Notifier = Reporter

// ConfigReader 读取全局配置键值；由 server 层注入，AI 配置变更时用于刷新厂商。
type ConfigReader func(key string) string

// Repository 管理多个项目监控实例（建议至少支持 5 个并行），互不干扰。
//
// 内部用 map[uint]*Watcher 索引；启动/停止按 ProjectID 路由，支持热更新。
type Repository struct {
	mu       sync.RWMutex
	wats     map[uint]*Watcher
	projects map[uint]*model.Project // 项目视图缓存，供创建 watcher 时使用
	reporter Reporter
	aiLib    *ai.Library
	cfgRead  ConfigReader // AI 刷新入口
	onStatus func(projectID uint, status string) // 状态落库回调
	fixDeps  FixDeps // 修复流水线依赖（备份+审计）
}

// NewRepository 创建仓库。
func NewRepository(reporter Reporter, aiLib *ai.Library) *Repository {
	return &Repository{
		wats:     make(map[uint]*Watcher),
		projects: make(map[uint]*model.Project),
		reporter: reporter,
		aiLib:    aiLib,
	}
}

// SetConfigReader 注入配置读取器，供 AI 厂商热切换。
func (r *Repository) SetConfigReader(fn ConfigReader) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfgRead = fn
}

// SetOnStatus 注入状态落库回调：每次项目状态变化时回调（写库+广播由上层决定）。
func (r *Repository) SetOnStatus(fn func(projectID uint, status string)) {
	r.mu.Lock()
	r.onStatus = fn
	r.mu.Unlock()
	// 对已运行的 watcher 同步生效
	for _, w := range r.wats {
		if w != nil {
			w.SetOnStatus(fn)
		}
	}
}

// SetFixDeps 注入修复流水线依赖（备份+审计），对已运行的 watcher 同步生效。
func (r *Repository) SetFixDeps(d FixDeps) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fixDeps = d
	for _, w := range r.wats {
		if w != nil {
			w.SetFixDeps(d)
		}
	}
}

// applyFixDepsLocked 在持锁前提下把修复依赖应用到指定 watcher。
func (r *Repository) applyFixDepsLocked(w *Watcher) {
	if r.fixDeps != nil {
		w.SetFixDeps(r.fixDeps)
	}
}

// ReloadAI 依据最新配置刷新 AI 适配层（配置写活的关键入口）。
func (r *Repository) ReloadAI() {
	r.mu.RLock()
	fn := r.cfgRead
	r.mu.RUnlock()
	if fn != nil {
		r.aiLib.BuildFromConfig(fn)
	}
}

// Upsert 登记/更新项目视图（不改变运行状态）。
func (r *Repository) Upsert(p *model.Project) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.projects[p.ID] = p
	// 若项目启停状态变化，需重建 watcher，交给 Reconcile 处理
}

// Reconcile 根据项目当前 Enabled 状态，补启动应运行而未运行的实例。
func (r *Repository) Reconcile() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, p := range r.projects {
		_, running := r.wats[id]
		if p.Enabled && !running {
			if w, err := NewWatcher(p, r.reporter, r.aiLib, nil, nil); err == nil {
				w.SetOnStatus(r.onStatus)
				r.applyFixDepsLocked(w)
				if err := w.Start(); err == nil {
					r.wats[id] = w
					log.Printf("[watcher] 已启动项目 %s 监控", p.Name)
				} else {
					log.Printf("[watcher] 启动 %s 失败: %v", p.Name, err)
				}
			}
		}
	}
}

// Start 为一个已登记的项目创建并启动监控。
func (r *Repository) Start(id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.projects[id]
	if !ok {
		return ErrProjectNotFound
	}
	return r.startLocked(p)
}

// startLocked 在已持锁的前提下创建 watcher 实例。
func (r *Repository) startLocked(p *model.Project) error {
	if w, running := r.wats[p.ID]; running && w != nil {
		return nil // 已在运行
	}
	w, err := NewWatcher(p, r.reporter, r.aiLib, nil, nil)
	if err != nil {
		return err
	}
	w.SetOnStatus(r.onStatus)
	r.applyFixDepsLocked(w)
	if err := w.Start(); err != nil {
		return err
	}
	r.wats[p.ID] = w
	log.Printf("[watcher] 已启动项目 %s 监控", p.Name)
	return nil
}

// Stop 停止某项目监控。
func (r *Repository) Stop(id uint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.wats[id]; ok && w != nil {
		w.Stop()
		delete(r.wats, id)
	}
}

// Restart 重启某项目（配置变更后调用）。
func (r *Repository) Restart(id uint) {
	r.Stop(id)
	_ = r.Start(id)
}

// CheckProject 一键检查：id=0 检查全部启用中的项目，否则只检查指定项目。
// 返回入队检查的文件总数；项目未运行时返回错误。
func (r *Repository) CheckProject(id uint) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	total := 0
	if id == 0 {
		for pid, w := range r.wats {
			if w != nil && r.projects[pid] != nil && r.projects[pid].Enabled {
				total += w.CheckAll()
			}
		}
		if total == 0 {
			return 0, errNoRunningProject
		}
		return total, nil
	}
	w, ok := r.wats[id]
	if !ok || w == nil {
		return 0, ErrProjectNotRunning
	}
	return w.CheckAll(), nil
}

// FixFile 对指定项目的指定文件执行 AI 修复（面板「立即修复」按钮调用）。
// 修复是耗时操作（AI 生成可达数十秒），调用方应异步等待结果，
// 进度与结果均通过 WebSocket 实时推送（fixing / fixed / rollback / error）。
func (r *Repository) FixFile(id uint, file string) error {
	r.mu.RLock()
	w, ok := r.wats[id]
	p := r.projects[id]
	r.mu.RUnlock()
	if !ok || w == nil {
		return ErrProjectNotRunning
	}
	// 路径安全校验：必须在项目目录内且属于监控类型，防越权写
	if p == nil || !IsFixableFile(p.Path, file) {
		return ErrFileNotMonitored
	}
	return w.FixFile(file)
}

// StopAll 停止全部监控（退出时调用）。
func (r *Repository) StopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, w := range r.wats {
		if w != nil {
			w.Stop()
		}
		delete(r.wats, id)
	}
	log.Print("[watcher] 全部项目监控已停止")
}

// 错误定义。
var (
	// ErrProjectNotFound 项目未在仓库登记。
	ErrProjectNotFound = &repoError{msg: "project not registered"}
	// ErrProjectNotRunning 项目未启动监控。
	ErrProjectNotRunning = &repoError{msg: "project watcher not running, please enable it first"}
	// errNoRunningProject 没有任何启用中的项目。
	errNoRunningProject = &repoError{msg: "no enabled project to check"}
)

type repoError struct{ msg string }

func (e *repoError) Error() string { return e.msg }
