package server

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/watchdog-ai/watchdog/internal/notify"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// upgrader WebSocket 升级器。
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // 开发期放行，生产按域名校验
}

// 客户端自增 ID，用于区分连接（普通互斥保护，兼容 Go 1.18）。
var (
	clientSeqMu sync.Mutex
	clientSeq   uint64
)

// nextClientID 返回下一个客户端自增 ID。
func nextClientID() uint64 {
	clientSeqMu.Lock()
	defer clientSeqMu.Unlock()
	clientSeq++
	return clientSeq
}

// serveWS 升级为 WebSocket，注册客户端并启动读写循环。
func (s *Server) serveWS(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[ws] 升级失败: %v", err)
		return
	}
	client := notify.NewClient(nextClientID(), conn)

	s.hub.Register(client)

	// 写循环：把后台广播推送出去
	go func() {
		defer func() { s.hub.Unregister(client); _ = conn.Close() }()
		for msg := range client.SendChannel() {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	// 读循环：接收前端决策（“允许修复/仅告警”），当前先透传日志
	conn.SetReadLimit(4096)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		// TODO(二次开发): 解析 data 中的 {action:"allow_fix"|"alert_only", ...}
		// 并路由到对应 Project 的 AutoFix 流程。
		time.Sleep(time.Millisecond)
		_ = data
	}
}

// writePump 常量保留给 future 心跳控制。
const writeWait = 10 * time.Second