// Package server：HTTP + WebSocket 服务。
//
// 用 Gin 提供 REST API 管理项目/配置/备份，并提供 WebSocket 升级用于实时推送。
// 前端静态资源（web 构建产物）默认挂载在 /static，可由 Vite build 后注入。
package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/watchdog-ai/watchdog/internal/config"
	"github.com/watchdog-ai/watchdog/internal/notify"
	"github.com/watchdog-ai/watchdog/internal/watcher"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// resolveWebDist 定位前端产物目录 web/dist。
// 优先用当前工作目录（Systemd WorkingDirectory / Docker WORKDIR 均已正确设置），
// 找不到时回退到「可执行文件同级目录」，保证从任意位置手动启动也能加载前端。
func resolveWebDist() string {
	candidates := []string{"./web/dist"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "web", "dist"))
	}
	for _, dir := range candidates {
		if st, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !st.IsDir() {
			return dir
		}
	}
	return "./web/dist"
}

// Server 聚合 HTTP 服务依赖。
type Server struct {
	cfg  *config.Config
	db   *gorm.DB
	repo *watcher.Repository
	hub  *notify.Hub
	eng  *gin.Engine
}

// New 构造 Server。
func New(cfg *config.Config, db *gorm.DB, repo *watcher.Repository, hub *notify.Hub) *Server {
	s := &Server{cfg: cfg, db: db, repo: repo, hub: hub}
	s.eng = gin.New()
	s.eng.Use(gin.Logger(), gin.Recovery())
	s.setupRoutes()
	return s
}

// setupRoutes 注册全部路由。
func (s *Server) setupRoutes() {
	eng := s.eng

	// ---- 健康检查 ----
	eng.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// ---- 项目 CRUD ----
	proj := eng.Group("/api/projects")
	{
		proj.GET("", s.listProjects)
		proj.POST("", s.createProject)
		proj.GET("/:id", s.getProject)
		proj.PUT("/:id", s.updateProject)
		proj.DELETE("/:id", s.deleteProject) // 仅删配置，不删磁盘文件
		proj.POST("/:id/start", s.startProject)
		proj.POST("/:id/stop", s.stopProject)
		proj.POST("/:id/check", s.checkProject) // 一键检查单个项目
		proj.POST("/:id/fix", s.fixProject)     // AI 修复指定文件（面板「立即修复」按钮）
	}

	// ---- 一键检查全部项目 ----
	eng.POST("/api/check-all", s.checkAllProjects)

	// ---- AI 厂商信息（前端下拉框动态渲染）----
	eng.GET("/api/ai/providers", s.listAIProviders)

	// ---- 全局配置（写活）----
	conf := eng.Group("/api/config")
	{
		conf.GET("", s.listConfig)
		conf.PUT("/:key", s.setConfig)
	}

	// ---- 备份仓库（时间轴 + 一键回滚）----
	bak := eng.Group("/api/backups")
	{
		bak.GET("", s.listBackups)
		bak.POST("/:id/restore", s.restoreBackup)
	}

	// ---- 操作日志（AI 修复/备份/回滚等动作，倒序 200 条）----
	eng.GET("/api/audit", s.listAudit)

	// ---- WebSocket 实时推送 ----
	eng.GET("/ws", s.serveWS)

	// ---- 前端静态资源 ----
	// Vite 默认 base="/"，产物引用 /assets/*.js|css，
	// 因此除 /static 前缀外，还需在 NoRoute 中按文件兜底，否则前端会白屏。
	distDir := resolveWebDist()
	distFS := http.Dir(distDir)
	eng.StaticFS("/static", distFS)

	// 兜底：静态文件按路径直出；其余非 API 请求回退 index.html（SPA 路由）
	eng.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		// API / WebSocket 的未知路径返回 JSON 404，避免误吞前端资源
		if strings.HasPrefix(p, "/api") || strings.HasPrefix(p, "/ws") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		// 尝试作为静态文件响应（覆盖 /assets/** 等前端资源引用）
		if p != "/" && !strings.Contains(p, "..") {
			if f, err := distFS.Open(strings.TrimPrefix(p, "/")); err == nil {
				st, serr := f.Stat()
				f.Close()
				if serr == nil && !st.IsDir() {
					http.StripPrefix("/", http.FileServer(distFS)).ServeHTTP(c.Writer, c.Request)
					return
				}
			}
		}
		// SPA 路由兜底：返回 index.html 交给前端路由
		c.File(filepath.Join(distDir, "index.html"))
	})
}

// Run 启动 HTTP 服务。
func (s *Server) Run(addr string) error {
	return s.eng.Run(addr)
}