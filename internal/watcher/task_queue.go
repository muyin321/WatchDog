package watcher

import "sync"

// CheckTask 一条待执行的检查流水线任务。
type CheckTask struct {
	// ProjectID 所属项目
	ProjectID uint
	// ProjectName 项目名
	ProjectName string
	// FilePath 发生变更的文件
	FilePath string
}

// taskQueue 内存任务队列。
//
// 文件系统（fsnotify 回调）只负责往队列里 push，真正执行由后台 worker
// 异步消费，从而保证监听不会阻塞在耗时的检查流水线上。
type taskQueue struct {
	mu     sync.Mutex
	ch     chan CheckTask
	closed bool
	once   sync.Once
}

// newTaskQueue 创建一个容量固定的有界队列。
func newTaskQueue(capacity int) *taskQueue {
	if capacity <= 0 {
		capacity = 256
	}
	return &taskQueue{ch: make(chan CheckTask, capacity)}
}

// Push 非阻塞式入队；队列满时丢弃并返回 false，避免拖慢文件回调。
func (q *taskQueue) Push(t CheckTask) bool {
	q.mu.Lock()
	closed := q.closed
	q.mu.Unlock()
	if closed {
		return false
	}
	select {
	case q.ch <- t:
		return true
	default:
		return false // 队列已满，直接丢弃本次，可记录日志
	}
}

// Consume 返回只读消费通道，供 worker 循环读取。
func (q *taskQueue) Consume() <-chan CheckTask { return q.ch }

// Close 关闭通道。
func (q *taskQueue) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.once.Do(func() { close(q.ch) })
}