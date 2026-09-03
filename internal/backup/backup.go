// Package backup：覆盖前强制备份与过期清理、回滚还原。
//
// 目录约定：{BackupsRoot}/{项目名}/{YYYY-MM-DD}/{备份文件名}。
// 每次覆盖原文件前，先把原文件副本落盘并写一条 BackupRecord，出错可一键还原。
package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/watchdog-ai/watchdog/internal/model"
	"gorm.io/gorm"
)

// Service 备份服务。
type Service struct {
	root string // 备份根目录
	db   *gorm.DB
}

// New 创建备份服务。
func New(root string, db *gorm.DB) *Service {
	return &Service{root: root, db: db}
}

// BackupFile 在覆盖前复制原文件到备份目录，并写入记录，返回记录 ID。
// 返回的错误表示备份失败（此时调用方应中止覆盖）。
func (s *Service) BackupFile(project model.Project, src string, reason string) (uint, error) {
	info, err := os.Stat(src)
	if err != nil {
		return 0, err
	}

	dateDir := time.Now().Format("2006-01-02")
	targetDir := filepath.Join(s.root, safeName(project.Name), dateDir)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return 0, err
	}

	dest := filepath.Join(targetDir, fmt.Sprintf("%d_%s",
		time.Now().UnixNano(), filepath.Base(src)))
	// 复制文件内容
	data, err := os.ReadFile(src)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(dest, data, info.Mode()); err != nil {
		return 0, err
	}

	rec := model.BackupRecord{
		ProjectID:   project.ID,
		ProjectName: project.Name,
		FilePath:    src,
		BackupPath:  dest,
		Reason:      reason,
	}
	if err := s.db.Create(&rec).Error; err != nil {
		return 0, err
	}
	return rec.ID, nil
}

// Restore 依据记录把备份副本还原到原路径（一键回滚）。
func (s *Service) Restore(recordID uint) error {
	var rec model.BackupRecord
	if err := s.db.First(&rec, recordID).Error; err != nil {
		return err
	}
	data, err := os.ReadFile(rec.BackupPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(rec.FilePath, data, 0o644); err != nil {
		return err
	}
	now := time.Now()
	rec.RolledBack = true
	rec.RolledBackAt = &now
	return s.db.Save(&rec).Error
}

// ListByProject 按时间轴列出某项目的备份记录（倒序）。
func (s *Service) ListByProject(projectID uint) ([]model.BackupRecord, error) {
	var recs []model.BackupRecord
	err := s.db.Where("project_id = ?", projectID).Order("created_at desc").Find(&recs).Error
	return recs, err
}

// CleanupExpired 清理超过保留天数的备份文件与记录（默认 7 天）。
// 返回被删除的记录数，方便记操作日志。
func (s *Service) CleanupExpired(retainDays int) (int, error) {
	if retainDays <= 0 {
		retainDays = 7
	}
	cutoff := time.Now().AddDate(0, 0, -retainDays)

	var ids []uint
	if err := s.db.Model(&model.BackupRecord{}).
		Where("created_at < ? AND rolled_back = ?", cutoff, false).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}

	// 物理删除备份文件
	var recs []model.BackupRecord
	s.db.Where("id IN ?", ids).Find(&recs)
	for _, r := range recs {
		_ = os.Remove(r.BackupPath)
	}
	// 删除记录
	if len(ids) > 0 {
		if err := s.db.Where("id IN ?", ids).Delete(&model.BackupRecord{}).Error; err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// safeName 把项目名里的路径分隔符替换掉，避免目录逃逸。
func safeName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '/' || r == '\\' || r == ':' {
			r = '_'
		}
		out = append(out, r)
	}
	return string(out)
}

// WalkDirs 遍历备份目录树（供“备份仓库”页面按日期文件夹展示）。
func (s *Service) WalkDirs() []string {
	var dirs []string
	_ = filepath.Walk(s.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && path != s.root {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs
}