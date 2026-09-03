package server

import (
	"net/http"
	"strconv"

	"github.com/watchdog-ai/watchdog/internal/ai"
	"github.com/watchdog-ai/watchdog/internal/audit"
	"github.com/watchdog-ai/watchdog/internal/backup"
	"github.com/watchdog-ai/watchdog/internal/model"
	"github.com/gin-gonic/gin"
)

// listConfig 返回全部全局配置（键值，含 AI Key 等，后台可编辑）。
func (s *Server) listConfig(c *gin.Context) {
	var list []model.Config
	s.db.Find(&list)
	kv := make(map[string]string)
	for _, it := range list {
		kv[it.Key] = it.Value
	}
	c.JSON(http.StatusOK, kv)
}

// setConfig 设置单个配置项（upsert）。
// AI 配置变更 -> 刷新 AI 适配层；Telegram 配置变更 -> 重启 Bot。全部即时生效。
func (s *Server) setConfig(c *gin.Context) {
	key := c.Param("key")
	var in struct {
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	var it model.Config
	if err := s.db.Where("key = ?", key).First(&it).Error; err != nil {
		// 不存在则新建
		it = model.Config{Key: key, Value: in.Value}
	} else {
		it.Value = in.Value
	}
	if err := s.db.Save(&it).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 若改的是 AI 配置，通知监控仓库刷新 AI 适配层（配置即时生效）
	if isAIKey(key) {
		s.repo.ReloadAI()
	}
	// 若改的是 Telegram 配置，重启/更新 Bot（保存 token 后立即可用 /start）
	if isTelegramKey(key) {
		s.hub.ConfigureTelegram(s.readCfg(model.CfgNotifyTgToken), s.readCfg(model.CfgNotifyTgChatID))
	}
	c.JSON(http.StatusOK, gin.H{"key": key, "value": in.Value})
}

// isAIKey 判断配置键是否属于 AI 范畴。
func isAIKey(key string) bool {
	return key == model.CfgAIProvider || key == model.CfgAIAPIKey ||
		key == model.CfgAIModel || key == model.CfgAIBaseURL
}

// isTelegramKey 判断配置键是否属于 Telegram 范畴。
func isTelegramKey(key string) bool {
	return key == model.CfgNotifyTgToken || key == model.CfgNotifyTgChatID
}

// readCfg 从库中读一个配置值（不存在返回空串）。
func (s *Server) readCfg(key string) string {
	var c model.Config
	if err := s.db.Where("key = ?", key).First(&c).Error; err == nil {
		return c.Value
	}
	return ""
}

// listAIProviders 返回支持的 AI 厂商清单（含默认模型），供前端下拉动态渲染。
func (s *Server) listAIProviders(c *gin.Context) {
	type prov struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Region string `json:"region"` // cn / global
		Model  string `json:"default_model"`
		NeedBaseURL bool `json:"need_base_url"`
	}
	list := []prov{
		{ID: ai.ProviderDeepSeek, Name: "DeepSeek", Region: "cn", Model: ai.DefaultModel(ai.ProviderDeepSeek)},
		{ID: ai.ProviderQwen, Name: "通义千问（阿里）", Region: "cn", Model: ai.DefaultModel(ai.ProviderQwen)},
		{ID: ai.ProviderZhipu, Name: "智谱 GLM", Region: "cn", Model: ai.DefaultModel(ai.ProviderZhipu)},
		{ID: ai.ProviderDoubao, Name: "豆包（字节火山方舟）", Region: "cn", Model: ai.DefaultModel(ai.ProviderDoubao)},
		{ID: ai.ProviderMiniMax, Name: "MiniMax", Region: "cn", Model: ai.DefaultModel(ai.ProviderMiniMax)},
		{ID: ai.ProviderOpenAI, Name: "OpenAI", Region: "global", Model: ai.DefaultModel(ai.ProviderOpenAI)},
		{ID: ai.ProviderAnthropic, Name: "Anthropic Claude", Region: "global", Model: ai.DefaultModel(ai.ProviderAnthropic)},
		{ID: ai.ProviderCustom, Name: "自定义（OpenAI 兼容）", Region: "custom", Model: "", NeedBaseURL: true},
	}
	c.JSON(http.StatusOK, list)
}

// listBackups 列出备份仓库（可选按项目过滤）。
func (s *Server) listBackups(c *gin.Context) {
	svc := backup.New(s.cfg.BackupsRoot, s.db)
	pidQ := c.Query("project_id")
	if pidQ != "" {
		pid, _ := strconv.ParseUint(pidQ, 10, 64)
		recs, err := svc.ListByProject(uint(pid))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, recs)
		return
	}
	// 全部按时间轴倒序
	var recs []model.BackupRecord
	s.db.Order("created_at desc").Find(&recs)
	c.JSON(http.StatusOK, recs)
}

// restoreBackup 一键回滚到指定备份。
func (s *Server) restoreBackup(c *gin.Context) {
	idV := c.Param("id")
	id, _ := strconv.ParseUint(idV, 10, 64)
	svc := backup.New(s.cfg.BackupsRoot, s.db)
	if err := svc.Restore(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"restored": true})
}

// listAudit 操作日志（倒序，最近 200 条）。
// 记录来源：AI 修复（fix.start/fix.done/fix.rollback）、备份、高危拦截等关键动作。
func (s *Server) listAudit(c *gin.Context) {
	var list []audit.Entry
	s.db.Order("time desc").Limit(200).Find(&list)
	c.JSON(http.StatusOK, list)
}