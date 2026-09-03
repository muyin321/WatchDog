// Package notify：变更/错误通知中心。
//
// 统一收口所有需要推送给开发者的消息：WebSocket 总线（网页端弹窗）为主，
// Telegram Bot 推送为可选（配置 token 后启用，支持 /start 等命令交互）。
package notify

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/watchdog-ai/watchdog/internal/model"
	"github.com/gorilla/websocket"
)

// MsgType 消息类型
const (
	TypeIssue    = "issue"    // 发现错误，等待“允许/仅告警”决策
	TypeFixed    = "fixed"    // 修复成功
	TypeFixing   = "fixing"   // 修复进行中（按钮转圈）
	TypeRollback = "rollback" // 修复失败已回滚
	TypeSummary  = "summary"  // 变更摘要（检查通过的常规通知）
	TypeStatus   = "status"   // 项目状态变化（前端指示灯实时刷新）
	TypeError    = "error"    // 运行层错误（AI 调用失败/文件不可读等），与代码问题区分
)

// Message 推送给前端的统一消息结构。
type Message struct {
	Type      string   `json:"type"`
	Project   string   `json:"project"`
	ProjectID uint     `json:"project_id,omitempty"`
	File      string   `json:"file"`
	Issues    []string `json:"issues,omitempty"`
	Text      string   `json:"text,omitempty"`
	Status    string   `json:"status,omitempty"`
	Time      string   `json:"time,omitempty"`
}

// Client 一个已连接的 WebSocket 客户端。
type Client struct {
	id   uint64
	conn *websocket.Conn
	send chan []byte
}

// NewClient 构造一个客户端（供 server 层使用）。
// conn 为已升级的 WebSocket 连接；客户端由 Hub 负责注册与广播写循环。
func NewClient(id uint64, conn *websocket.Conn) *Client {
	return &Client{id: id, conn: conn, send: make(chan []byte, 64)}
}

// SendChannel 返回该客户端的广播写通道。
func (c *Client) SendChannel() chan []byte { return c.send }

// Hub WebSocket 广播中心，同时实现 watcher.Notifier 接口。
// 内嵌一个 Telegram Bot：配置 token 后既负责推送也响应命令。
type Hub struct {
	mu         sync.RWMutex
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client

	// bot Telegram 机器人（token 为空时不启动）
	bot *TelegramBot
}

// NewHub 创建通知中心。
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// Push 实现 watcher.Notifier：向所有前端广播错误摘要。
func (h *Hub) Push(p model.Project, issues []string, file string) {
	h.Send(Message{Type: TypeIssue, Project: p.Name, ProjectID: p.ID, File: file, Issues: issues})
}

// SendSummary 推送“检查通过/变更摘要”类消息（让用户感知监控在工作）。
func (h *Hub) SendSummary(projectID uint, projectName, file, summary string) {
	h.Send(Message{
		Type: TypeSummary, Project: projectName, ProjectID: projectID,
		File: file, Text: summary,
	})
}

// SendStatus 推送项目状态变化（前端指示灯实时刷新）。
func (h *Hub) SendStatus(projectID uint, projectName, status string) {
	h.Send(Message{
		Type: TypeStatus, Project: projectName, ProjectID: projectID, Status: status,
	})
}

// SendError 推送运行层错误（AI 调用失败、文件不可读等）。
// 这类错误与“代码本身有问题”不同：必须让用户在面板上看到红色告警，
// 而不是被当作“检查通过”的绿色摘要静默吞掉。
func (h *Hub) SendError(projectID uint, projectName, file, text string) {
	h.Send(Message{
		Type: TypeError, Project: projectName, ProjectID: projectID,
		File: file, Text: text,
	})
}

// SendFixing 推送“修复进行中”（前端修复按钮转圈，AI 可能耗时数十秒）。
func (h *Hub) SendFixing(projectID uint, projectName, file, text string) {
	h.Send(Message{
		Type: TypeFixing, Project: projectName, ProjectID: projectID,
		File: file, Text: text,
	})
}

// SendFixed 推送修复成功（含结果摘要）。
func (h *Hub) SendFixed(projectID uint, projectName, file, text string) {
	h.Send(Message{
		Type: TypeFixed, Project: projectName, ProjectID: projectID,
		File: file, Text: text,
	})
}

// SendRollback 推送修复失败已回滚（含原因）。
func (h *Hub) SendRollback(projectID uint, projectName, file, text string) {
	h.Send(Message{
		Type: TypeRollback, Project: projectName, ProjectID: projectID,
		File: file, Text: text,
	})
}

// Send 序列化并广播一条消息。
func (h *Hub) Send(m Message) {
	if m.Time == "" {
		m.Time = nowRFC3339()
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	select {
	case h.broadcast <- data:
	default:
		log.Print("[notify] 广播通道已满，消息被丢弃")
	}
}

// ConfigureTelegram 设置 Telegram Bot 并按需（重）启动命令轮询。
// token 从无到有 / 发生变化时重启 Bot；仅 chatID 变化则热更新即可。
func (h *Hub) ConfigureTelegram(token, chatID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.bot != nil && h.bot.Token() == token {
		h.bot.UpdateConfig(token, chatID) // 仅 chat_id 变化，无需重启
		return
	}
	// token 变化（含清空）：停旧起新
	if h.bot != nil {
		h.bot.Stop()
	}
	if token == "" {
		h.bot = nil
		log.Print("[telegram] Bot 已停用（token 已清空）")
		return
	}
	h.bot = NewTelegramBot(token, chatID)
	h.bot.Start()
	log.Print("[telegram] Bot 已启动")
}

// Bot 返回当前 Bot 实例（可能为 nil），供装配回调。
func (h *Hub) Bot() *TelegramBot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.bot
}

// RunLoop 常驻事件循环，处理注册/注销/广播。
func (h *Hub) RunLoop() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = true
			h.mu.Unlock()

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()

		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// 慢消费者丢弃，避免整体阻塞
				}
			}
			h.mu.RUnlock()

			// 同步推送到 Telegram（issue 类才推，摘要类避免刷屏）
			h.pushTelegram(msg)
		}
	}
}

// Register / Unregister 供 server 层调用。
func (h *Hub) Register(c *Client)          { h.register <- c }
func (h *Hub) Unregister(c *Client)        { h.unregister <- c }
func (h *Hub) BroadcastChannel() chan []byte { return h.broadcast }

// pushTelegram 把广播消息转发到 Telegram（仅 issue / rollback 类）。
func (h *Hub) pushTelegram(msg []byte) {
	h.mu.RLock()
	bot := h.bot
	h.mu.RUnlock()
	if bot == nil {
		return
	}

	var m Message
	if err := json.Unmarshal(msg, &m); err != nil {
		return
	}
	switch m.Type {
	case TypeIssue:
		var sb string
		sb = "🚨 WatchDog AI 告警\n"
		sb += "项目：" + m.Project + "\n"
		sb += "文件：" + m.File + "\n"
		sb += "发现 " + jsonNumber(len(m.Issues)) + " 处问题：\n"
		for i, is := range m.Issues {
			if i >= 5 {
				sb += "...（其余省略，详见 Web 面板）\n"
				break
			}
			sb += jsonNumber(i+1) + ". " + is + "\n"
		}
		sb += "\n到 Web 面板确认是否允许 AI 修复。"
		bot.SendToBound(sb)
	case TypeRollback:
		bot.SendToBound("↩️ 修复失败已自动回滚\n项目：" + m.Project + "\n文件：" + m.File)
	case TypeFixed:
		bot.SendToBound("✅ AI 修复成功\n项目：" + m.Project + "\n文件：" + m.File)
	case TypeError:
		bot.SendToBound("⚠️ 运行错误\n项目：" + m.Project + "\n文件：" + m.File + "\n" + m.Text)
	}
	// summary/status 不推送 Telegram，避免刷屏；网页端可见
}

// jsonNumber 数字转字符串（避免直接引入 strconv 多处）。
func jsonNumber(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// nowRFC3339 当前时间的 RFC3339 字符串。
func nowRFC3339() string { return time.Now().Format(time.RFC3339) }
