package model

import "time"

// BackupRecord 记录一次“覆盖前备份”或人工回滚。
//
// 所有覆盖原文件的操作必须先 Create 一条记录并物理复制原文件到
// {BackupsRoot}/{项目名}/{日期}/ 下，出错时可据记录执行还原。
type BackupRecord struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `json:"created_at"`

	// ProjectID 所属项目
	ProjectID uint `gorm:"index" json:"project_id"`
	// ProjectName 冗余项目名，方便显示与路径组织
	ProjectName string `gorm:"size:128" json:"project_name"`

	// FilePath 被覆盖的原始文件绝对路径
	FilePath string `gorm:"size:1024" json:"file_path"`
	// BackupPath 备份副本存放路径
	BackupPath string `gorm:"size:1024" json:"backup_path"`

	// Reason 备份原因：auto_fix / manual 等
	Reason string `gorm:"size:64" json:"reason"`
	// RolledBack 是否已被回滚（true 表示该备份已用于还原）
	RolledBack bool `gorm:"default:false" json:"rolled_back"`
	// RolledBackAt 回滚时间，nil 表示未回滚
	RolledBackAt *time.Time `json:"rolled_back_at"`
}

func (BackupRecord) TableName() string { return "backup_records" }