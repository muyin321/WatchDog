// Package audit：操作日志记录。
//
// 记录关键动作（启用/禁用项目、备份、回滚、AI 修复、高危拦截等），
// 便于“操作日志”页面审计与排障。
package audit

import (
	"time"

	"gorm.io/gorm"
)

// Entry 一条操作日志。
type Entry struct {
	ID       uint      `gorm:"primaryKey" json:"id"`
	Time     time.Time `json:"time"`
	Action   string    `gorm:"size:64" json:"action"`   // 动作类型
	Target   string    `gorm:"size:256" json:"target"`  // 对象（项目/文件）
	Detail   string    `gorm:"size:1024" json:"detail"` // 详情
	Level    string    `gorm:"size:16;default:info" json:"level"` // info / warn / error
}

func (Entry) TableName() string { return "audit_entries" }

// Logger 审计日志写入器。
type Logger struct {
	db *gorm.DB
}

// New 创建日志器。
func New(db *gorm.DB) *Logger { return &Logger{db: db} }

// Log 写入一条审计日志。
func (l *Logger) Log(action, target, detail, level string) {
	l.db.Create(&Entry{Time: time.Now(), Action: action, Target: target, Detail: detail, Level: level})
}

// List 分页查询日志（倒序）。
func (l *Logger) List(limit int) []Entry {
	if limit <= 0 {
		limit = 100
	}
	var list []Entry
	l.db.Order("time desc").Limit(limit).Find(&list)
	return list
}