// Package model：数据库 ORM 模型定义
//
// 三个核心表：
//   - Project        被监控的项目
//   - Config         全局键值配置（AI Key、开关等，全部“写活”）
//   - BackupRecord   备份与回滚记录（时间轴 + 一键回滚）
package model

import "time"

// Project 表示一个被 WatchDog 监控的项目。
// 删除项目时仅移除此记录，绝不删除磁盘上的源码文件。
type Project struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Name 项目名称
	Name string `gorm:"size:128;not null;index" json:"name"`

	// Path 源码磁盘绝对路径，如 /home/www/test
	Path string `gorm:"size:512;not null;uniqueIndex" json:"path"`

	// URL 项目访问地址（用于 HealthCheck 或回跳页面）
	URL string `gorm:"size:512" json:"url"`

	// TelegramBotPID 可选的 Telegram 机器人进程 PID（telebot 场景备用）
	TelegramBotPID int `json:"telegram_bot_pid"`

	// Enabled 是否启用监控，禁用后 Watcher 不工作
	Enabled bool `gorm:"default:false" json:"enabled"`

	// AutoFix 全自动无人干预开关：低风险错误直接修复，高风险仍需确认
	AutoFix bool `gorm:"default:false" json:"auto_fix"`

	// Status 实时状态：green=健康 / yellow=修复中 / red=错误
	// 该字段由 watcher 运行时更新，方便前端“状态指示灯”
	Status string `gorm:"size:16;default:green" json:"status"`
}

// TableName 指定表名
func (Project) TableName() string { return "projects" }

// Allowed 列举允许被监控的文件扩展名
var AllowedExts = []string{".php", ".js", ".html", ".css", ".vue", ".py"}

// IsMonitoredFile 判断一个文件是否属于监控范围
func IsMonitoredFile(filename string) bool {
	for _, ext := range AllowedExts {
		if len(filename) > len(ext) &&
			filename[len(filename)-len(ext):] == ext {
			return true
		}
	}
	return false
}