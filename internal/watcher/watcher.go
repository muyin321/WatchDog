package watcher

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/watchdog-ai/watchdog/internal/ai"
	"github.com/watchdog-ai/watchdog/internal/model"
)

// 流水线阶段常量，用于状态与日志标识
const (
	StageLint = "lint"    // 硬语法检查
	StageAI   = "ai"      // AI 逻辑分析
	StageSum  = "summary" // 变更摘要
)

// ignoreDirs 监听时跳过的目录（依赖缓存/版本库/构建产物，变更无意义且量大）。
var ignoreDirs = map[string]bool{
	".git": true, ".svn": true, ".hg": true,
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	"__pycache__": true, ".idea": true, ".vscode": true, ".watchdog-data": true,
}

// Watcher 监控一个项目目录。
//
// 每个项目持有一个独立实例，互不干扰；内部由 fsnotify + debouncer + taskQueue + worker 组合。
// 关键行为：
//   - 递归监听整棵目录树（含运行期新建的子目录）
//   - 每次检查无论结果如何都会向前端推送消息（有错推 issue，无错推 summary）
//   - 状态变化经 onStatus 回调落库并广播，前端指示灯实时刷新
type Watcher struct {
	project *model.Project

	// fs 底层文件监听
	fs *fsnotify.Watcher

	// deb 每个文件事件的防抖器（窗口 1s，只对最后一次生效）
	deb *debouncer

	// queue 异步任务队列
	queue *taskQueue

	// reporter 通知接口（WebSocket / Telegram，由 notify.Hub 实现）
	reporter Reporter

	// aiLib AI 适配库
	aiLib *ai.Library

	// lintFn 语法检查函数（可注入自定义实现，nil 时用内置默认）
	lintFn LintFunc
	// analyzeFn AI 分析函数（可注入，nil 时用内置默认）
	analyzeFn AnalyzeFunc

	// onStatus 状态落库回调（server/main 层注入；nil 则仅改内存）
	onStatus func(projectID uint, status string)

	// fixDeps 修复流水线依赖（备份+审计；nil 时修复前不备份，仅内存还原兜底）
	fixDeps FixDeps

	// lastContent 文件内容快照（路径 -> 上次检查时内容），用于生成 diff。
	// 仅被 consumeLoop 单 goroutine 读写，无需加锁。
	lastContent map[string]string

	// cancel 停止循环的退出信号
	stop chan struct{}
	wg   sync.WaitGroup
}

// LintFunc 语法检查函数签名：输入项目与文件，返回错误列表或 nil。
// 允许替换为第二方的 linter 实现。
type LintFunc func(p *model.Project, file string) []string

// AnalyzeFunc AI 逻辑分析函数签名：输入项目、文件与 Diff 文本，
// 返回分析结论（错误摘要）、一句话变更总结，以及运行层错误。
// err 非 nil 表示“分析过程本身失败”（如 AI 接口调用失败），
// 会作为红色告警推送到前端，与代码问题（issues）严格区分。
type AnalyzeFunc func(p *model.Project, file, diff string) (issues []string, summary string, err error)

// Reporter 通知汇报接口：notify.Hub 实现全部方法。
// 比 Notifier 多了 summary（检查通过通知）、status（状态变化）、
// error（运行层错误）、fixing/fixed/rollback（修复三阶段）六个通道，
// 这是“监控在实时工作”与“修复进度可感知”的来源。
type Reporter interface {
	// Push 发现问题时推送（等待“允许修复/仅告警”决策）
	Push(p model.Project, issues []string, file string)
	// SendSummary 常规通知：文件检查通过/变更摘要
	SendSummary(projectID uint, projectName, file, summary string)
	// SendStatus 项目状态变化（green/yellow/red）
	SendStatus(projectID uint, projectName, status string)
	// SendError 运行层错误（AI 调用失败、文件不可读等）
	SendError(projectID uint, projectName, file, text string)
	// SendFixing 修复进行中（前端按钮转圈、状态提示）
	SendFixing(projectID uint, projectName, file, text string)
	// SendFixed 修复成功
	SendFixed(projectID uint, projectName, file, text string)
	// SendRollback 修复失败已回滚
	SendRollback(projectID uint, projectName, file, text string)
}

// NewWatcher 构造一个项目监控实例。
// reporter 负责推送（可为空实现）；lintFn/analyzeFn 允许外部注入，nil 则用内置。
func NewWatcher(p *model.Project, reporter Reporter, aiLib *ai.Library,
	lintFn LintFunc, analyzeFn AnalyzeFunc) (*Watcher, error) {

	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		project:     p,
		fs:          fs,
		reporter:    reporter,
		aiLib:       aiLib,
		lintFn:      lintFn,
		analyzeFn:   analyzeFn,
		stop:        make(chan struct{}),
		lastContent: make(map[string]string),
	}
	w.deb = newDebouncer(time.Second, func(path string) {
		// 防抖结束后，把任务交给异步队列（非阻塞）
		w.enqueue(path)
	})
	w.queue = newTaskQueue(256)

	// 默认实现：内置 linter + AI 库（在 pipeline 内按需初始化）
	if lintFn == nil {
		w.lintFn = defaultLint
	}
	if analyzeFn == nil {
		w.analyzeFn = w.defaultAnalyze
	}
	return w, nil
}

// SetOnStatus 注入状态落库回调。
func (w *Watcher) SetOnStatus(fn func(projectID uint, status string)) { w.onStatus = fn }

// SetFixDeps 注入修复流水线依赖（备份+审计）。
func (w *Watcher) SetFixDeps(d FixDeps) { w.fixDeps = d }

// enqueue 把变更文件放入内存队列（非阻塞，不拖累文件系统）。
func (w *Watcher) enqueue(path string) {
	ok := w.queue.Push(CheckTask{
		ProjectID:   w.project.ID,
		ProjectName: w.project.Name,
		FilePath:    path,
	})
	if !ok {
		log.Printf("[watcher] %s 队列已满，丢弃检查任务: %s", w.project.Name, path)
	}
}

// Start 启动 fsnotify 监听与 worker 消费循环。
// 递归添加项目目录树下所有子目录（fsnotify 原生不支持递归，需手动展开）。
func (w *Watcher) Start() error {
	if err := w.addTree(w.project.Path); err != nil {
		return err
	}
	w.wg.Add(2)
	go w.watchLoop()
	go w.consumeLoop()
	log.Printf("[watcher] %s 开始递归监听 %s", w.project.Name, w.project.Path)
	return nil
}

// addTree 递归把 root 及其全部子目录加入监听（跳过 ignoreDirs）。
func (w *Watcher) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 单个目录不可读不影响整体
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && ignoreDirs[d.Name()] {
			return filepath.SkipDir
		}
		if err := w.fs.Add(path); err != nil {
			log.Printf("[watcher] %s 无法监听目录 %s: %v", w.project.Name, path, err)
		}
		return nil
	})
}

// watchLoop 处理 fsnotify 事件：只关心 Write/Create，仅监控白名单扩展名。
// 新建目录时动态补挂其子树，保证运行期新增的目录也能被监控。
func (w *Watcher) watchLoop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.stop:
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			// 目录事件：Create 补挂子树；Remove 忽略
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = w.addTree(ev.Name)
					continue
				}
			}
			// 只处理写入/创建事件；删除、重命名暂不触发检查
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				// 文件被删除时清理内容快照，避免泄漏
				if ev.Op&fsnotify.Remove != 0 {
					delete(w.lastContent, ev.Name)
				}
				continue
			}
			// 扩展名过滤
			if !model.IsMonitoredFile(ev.Name) {
				continue
			}
			// 交给防抖器（1s 窗口）
			w.deb.Tap(ev.Name)
		case err := <-w.fs.Errors:
			if err != nil {
				log.Printf("[watcher] %s 监听错误: %v", w.project.Name, err)
			}
		}
	}
}

// consumeLoop 消费队列并执行流水线。
func (w *Watcher) consumeLoop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.stop:
			return
		case task := <-w.queue.Consume():
			w.process(task)
		}
	}
}

// CheckAll 一键检查：遍历项目目录，把全部监控文件立即入队（绕过防抖等待）。
// 返回入队文件数。供“一键检查”按钮与 Telegram /check 命令调用。
func (w *Watcher) CheckAll() int {
	count := 0
	_ = filepath.WalkDir(w.project.Path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != w.project.Path && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if model.IsMonitoredFile(path) {
			w.enqueue(path)
			count++
		}
		return nil
	})
	return count
}

// process 串行执行完整检测流水线（Lint -> AI 分析 -> 摘要）。
// 无论有无问题都会产生一条前端可见的消息：
//   - 代码有问题   -> issue（红）
//   - 全部通过     -> summary（绿）
//   - 运行层错误   -> error（AI 调用失败等，黄），绝不被“检查通过”掩盖
func (w *Watcher) process(task CheckTask) {
	w.setStatus("yellow") // 处理中

	// 第零步：文件可达性检查（刚写入就被删除/权限变更的场景）
	if _, err := os.Stat(task.FilePath); err != nil {
		if w.reporter != nil {
			w.reporter.SendError(w.project.ID, w.project.Name, task.FilePath,
				"文件无法读取："+err.Error())
		}
		return
	}

	// 读取当前内容并生成 diff 快照（供 AI 分析）
	diff := w.diffOf(task.FilePath)

	// 第一步：硬语法检查
	issues := w.lintFn(w.project, task.FilePath)

	// 第二步：AI 逻辑分析（含变更摘要；可能返回运行层错误）
	aiIssues, summary, aiErr := w.analyzeFn(w.project, task.FilePath, diff)
	issues = append(issues, aiIssues...)

	// 运行层错误（如 AI 调用失败）：独立推送红色告警，保证用户可见。
	// 此前这里被当作“检查通过”的绿色摘要静默吞掉，用户完全感知不到。
	if aiErr != nil {
		log.Printf("[watcher] %s 分析出错: %s: %v", w.project.Name, task.FilePath, aiErr)
		if w.reporter != nil {
			w.reporter.SendError(w.project.ID, w.project.Name, task.FilePath, aiErr.Error())
		}
	}

	// 第三步：汇总结论并推送
	if len(issues) > 0 {
		w.setStatus("red")
		w.notifyIssue(task, issues)
		return
	}

	// 代码本身没问题：
	//   - AI 正常 -> 绿 + 摘要
	//   - AI 挂了 -> 保持黄（不误报健康），error 告警已推送
	if aiErr != nil {
		w.setStatus("yellow")
		return
	}
	w.setStatus("green")
	if summary == "" {
		summary = "检查通过，未发现问题"
	}
	if w.reporter != nil {
		w.reporter.SendSummary(w.project.ID, w.project.Name, task.FilePath, summary)
	}
	log.Printf("[watcher] %s 检查通过: %s (%s)", w.project.Name, task.FilePath, summary)
}

// diffOf 读取文件当前内容，与上次快照对比生成差异文本，然后更新快照。
// 首次见到的文件返回提示语；超大文件只取前缀参与对比。
func (w *Watcher) diffOf(file string) string {
	cur := readHead(file, maxContentBytes)
	old, ok := w.lastContent[file]
	w.lastContent[file] = cur
	if !ok {
		return "（首次检查该文件，无历史快照，以下为当前内容）\n" + cur
	}
	if old == cur {
		return "（内容与上次检查一致）"
	}
	return simpleDiff(old, cur)
}

// notifyIssue 通过通知接口推送错误摘要到前端弹窗 / Telegram。
func (w *Watcher) notifyIssue(task CheckTask, issues []string) {
	if w.reporter != nil {
		w.reporter.Push(model.Project{ID: task.ProjectID, Name: task.ProjectName}, issues, task.FilePath)
	}
}

// setStatus 更新项目状态：内存 + 落库回调 + 前端广播。
func (w *Watcher) setStatus(s string) {
	if w.project.Status == s {
		return
	}
	w.project.Status = s
	if w.onStatus != nil {
		w.onStatus(w.project.ID, s)
	}
	if w.reporter != nil {
		w.reporter.SendStatus(w.project.ID, w.project.Name, s)
	}
}

// Stop 停止监听、队列与 worker。
func (w *Watcher) Stop() {
	close(w.stop)
	_ = w.fs.Close()
	w.deb.Close()
	w.queue.Close()
	w.wg.Wait()
}
