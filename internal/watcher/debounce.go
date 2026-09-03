// Package watcher：核心文件监控模块
//
// 职责：为一个或多个项目提供 fsnotify 文件监听 + 防抖 + 异步任务队列 +
// “Lint->AI分析->变更摘要”流水线，并联动备份/回滚。
// 全部对外接口均为 Go 接口/函数签名，便于按项目二次实现。
package watcher

import (
	"sync"
	"time"
)

// debouncer 实现对单文件的“最后一次触发生效”防抖。
//
// 行为：窗口期内（默认 1s）同一文件多次触发只保留最后一次变化时间，
// 窗口结束后通过回调 emit 通知一次，避免 Ctrl+S 引起的连环事件浪费资源。
type debouncer struct {
	mu      sync.Mutex
	window  time.Duration
	pending map[string]time.Time // 文件路径 -> 最近一次变更时间
	timer   *time.Timer
	emit    func(path string)
}

// newDebouncer 创建防抖器。
// window 为防抖窗口；emit 在“窗口结束且存在待处理变更”时被调用。
func newDebouncer(window time.Duration, emit func(path string)) *debouncer {
	if window <= 0 {
		window = time.Second
	}
	return &debouncer{
		window:  window,
		pending: make(map[string]time.Time),
		emit:    emit,
	}
}

// Tap 记录一次变更事件到达。
func (d *debouncer) Tap(path string) {
	d.mu.Lock()
	d.pending[path] = time.Now()
	// 每来一次事件就重置计时器：只要事件比窗口间隔更频繁，就永远不触发
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.window, d.flush)
	d.mu.Unlock()
}

// flush 窗口结束后扫描所有待处理变更并逐个回调。
func (d *debouncer) flush() {
	d.mu.Lock()
	pending := d.pending
	d.pending = make(map[string]time.Time)
	d.timer = nil
	d.mu.Unlock()

	// 在锁外回调，避免长时间持锁
	for path := range pending {
		if d.emit != nil {
			d.emit(path)
		}
	}
}

// Close 清理残留计时器。
func (d *debouncer) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
}