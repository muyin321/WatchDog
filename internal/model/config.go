package model

import "time"

// Config 全局键值配置。
//
// “配置写活”落地点：所有 Key 都保存到表里，后台可改，代码里不硬编码任何密钥/路径。
// 常见 Key（常量见下方）：
//   - ai.provider      AI 厂商：deepseek / openai / qwen
//   - ai.api_key       AI 服务 API Key
//   - ai.model         模型名
//   - ai.base_url      可选，自定义网关地址
//   - backup.retain_days 备份保留天数（默认 7）
//   - notify.telegram_bot_token  Telegram Bot Token（可选）
//   - notify.telegram_chat_id     Telegram 目标 chat_id（可选）
type Config struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Key 配置键，如 "ai.api_key"
	Key string `gorm:"size:128;not null;uniqueIndex" json:"key"`
	// Value 配置值
	Value string `gorm:"size:4096" json:"value"`
}

func (Config) TableName() string { return "configs" }

// 常用配置键常量（避免字符串散落各处）
const (
	CfgAIProvider      = "ai.provider"
	CfgAIAPIKey        = "ai.api_key"
	CfgAIModel         = "ai.model"
	CfgAIBaseURL       = "ai.base_url"
	CfgBackupRetainDay = "backup.retain_days"
	CfgNotifyTgToken   = "notify.telegram_bot_token"
	CfgNotifyTgChatID  = "notify.telegram_chat_id"
)