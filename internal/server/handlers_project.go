package server

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/watchdog-ai/watchdog/internal/model"
	"github.com/watchdog-ai/watchdog/internal/watcher"
	"github.com/gin-gonic/gin"
)

// createProject 新增项目：只需名称、磁盘绝对路径、URL 或 TelegramBotPID。
// 仅登记配置并 Upsert 到监控仓库，不启动（由调用方 start）。
func (s *Server) createProject(c *gin.Context) {
	var in struct {
		Name          string `json:"name"`
		Path          string `json:"path"`
		URL           string `json:"url"`
		TelegramBotPID int    `json:"telegram_bot_pid"`
		Enabled       bool   `json:"enabled"`
		AutoFix       bool   `json:"auto_fix"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if in.Name == "" || in.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称与路径必填"})
		return
	}

	p := model.Project{
		Name:          in.Name,
		Path:          in.Path,
		URL:           in.URL,
		TelegramBotPID: in.TelegramBotPID,
		Enabled:       in.Enabled,
		AutoFix:       in.AutoFix,
		Status:        "green",
	}
	// 落库
	if err := s.db.Create(&p).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 登记进监控仓库
	s.repo.Upsert(&p)
	if p.Enabled {
		_ = s.repo.Start(p.ID)
	}
	c.JSON(http.StatusOK, p)
}

// listProjects 列出全部项目（含状态指示灯字段）。
func (s *Server) listProjects(c *gin.Context) {
	var list []model.Project
	s.db.Order("id desc").Find(&list)
	c.JSON(http.StatusOK, list)
}

// getProject 查询单个项目。
func (s *Server) getProject(c *gin.Context) {
	id := mustID(c)
	var p model.Project
	if err := s.db.First(&p, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// updateProject 更新项目（路径/URL/开关等），仅改配置不改磁盘。
func (s *Server) updateProject(c *gin.Context) {
	id := mustID(c)
	var p model.Project
	if err := s.db.First(&p, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	var in model.Project
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	// 仅允许修改这些字段（禁止篡改 ID）
	p.Name = in.Name
	p.Path = in.Path
	p.URL = in.URL
	p.TelegramBotPID = in.TelegramBotPID
	p.Enabled = in.Enabled
	p.AutoFix = in.AutoFix

	if err := s.db.Save(&p).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 同步仓库视图并重建 watcher
	s.repo.Upsert(&p)
	if p.Enabled {
		_ = s.repo.Start(p.ID)
	} else {
		s.repo.Stop(p.ID)
	}
	c.JSON(http.StatusOK, p)
}

// deleteProject 删除项目：仅移除配置记录，绝不删除磁盘源码。
func (s *Server) deleteProject(c *gin.Context) {
	id := mustID(c)
	s.repo.Stop(id)
	// 只删记录，不触磁盘
	if err := s.db.Delete(&model.Project{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// startProject 手动启动某项目监控。
func (s *Server) startProject(c *gin.Context) {
	id := mustID(c)
	if err := s.repo.Start(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"started": true})
}

// stopProject 手动停止某项目监控。
func (s *Server) stopProject(c *gin.Context) {
	id := mustID(c)
	s.repo.Stop(id)
	c.JSON(http.StatusOK, gin.H{"stopped": true})
}

// checkProject 一键检查：立即对该项目全部监控文件执行检查流水线。
// 结果通过 WebSocket 实时推送（summary / issue），无需轮询。
func (s *Server) checkProject(c *gin.Context) {
	id := mustID(c)
	n, err := s.repo.CheckProject(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"queued": n, "message": "已触发检查，结果将实时推送到面板"})
}

// checkAllProjects 一键检查全部启用中的项目。
func (s *Server) checkAllProjects(c *gin.Context) {
	n, err := s.repo.CheckProject(0)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"queued": n, "message": "已触发全部项目检查，结果将实时推送到面板"})
}

// fixProject AI 修复：对指定文件执行“备份 -> AI 生成 -> 写入 -> 复检 -> 失败回滚”流水线。
// 修复耗时可达数十秒，因此立即返回受理结果；进度与结果经 WebSocket 推送
// （fixing / fixed / rollback / error 四种消息），前端按钮实时感知。
func (s *Server) fixProject(c *gin.Context) {
	id := mustID(c)
	var in struct {
		File string `json:"file"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.File == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供要修复的文件路径"})
		return
	}

	// 先校验项目存在（给出明确错误，而不是笼统的“未运行”）
	var p model.Project
	if err := s.db.First(&p, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	if !p.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "项目未启用监控，请先开启"})
		return
	}
	// 路径安全校验（同步做，受理前即拒绝）：必须在项目目录内且属于监控类型
	if !watcher.IsFixableFile(p.Path, in.File) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件不在项目目录内或不属于受监控的文件类型"})
		return
	}

	// 异步执行：HTTP 立即返回“已受理”，结果走 WS（避免网关/axios 30s 超时）。
	// 错误推送去重：rollback 终态已由 FixFile 推送，这里只推其余错误。
	go func() {
		if err := s.repo.FixFile(id, in.File); err != nil {
			log.Printf("[fix] 修复结束 project=%d file=%s: %v", id, in.File, err)
			if !errors.Is(err, watcher.ErrRolledBack) {
				s.hub.SendError(id, p.Name, in.File, "修复未完成："+err.Error())
			}
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{
		"accepted": true,
		"message":  "修复任务已受理，进度与结果将实时推送到面板",
	})
}

// mustID 从路由参数解析 uint ID。
func mustID(c *gin.Context) uint {
	v, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	return uint(v)
}