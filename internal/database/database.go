// Package database：SQLite 连接与自动建表
//
// 使用 GORM + glebarez/sqlite（纯 Go 实现，无需 CGO），因此：
//   - 单二进制可静态编译（Docker 多阶段构建、无 glibc 依赖均可运行）
//   - 也可以设置 CGO_ENABLED=1 使用原生 sqlite 获得更好并发（可选）
package database

import (
	"path/filepath"

	"github.com/watchdog-ai/watchdog/internal/audit"
	"github.com/watchdog-ai/watchdog/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Init 打开（必要时创建）SQLite 数据库，并执行自动迁移。
// dbPath 为存放 watchdog.db 的目录。
func Init(dbDir string) (*gorm.DB, error) {
	dsn := filepath.Join(dbDir, "watchdog.db")

	// 开发期把日志级别调低，生产可改为 logger.Error
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}

	// 自动迁移核心表，字段新增可在此追加
	if err := db.AutoMigrate(
		&model.Project{},
		&model.Config{},
		&model.BackupRecord{},
		&audit.Entry{}, // 操作日志（AI 修复/备份/回滚等动作记录）
	); err != nil {
		return nil, err
	}

	return db, nil
}