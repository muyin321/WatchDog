// telegram.go：真实 Telegram Bot 实现。
//
// 两个职责：
//  1. 推送：告警/摘要消息发送到已绑定的 chat_id（sendMessage）
//  2. 交互：getUpdates 长轮询接收命令（/start /help /status /check /ping），
//     /start 时可自动把发消息的 chat_id 写入配置完成绑定。
//
// 说明：Bot 与用户“对话”不需要轮询网页，发 /start 就会有回应——
// 这是此前“发 start 没反应”的根因：旧实现只有占位日志，没有 Bot 循环。
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/watchdog-ai/watchdog/internal/model"
)

// TelegramBot 一个 Telegram 机器人实例（推送 + 命令长轮询）。
type TelegramBot struct {
	mu     sync.Mutex
	token  string
	chatID string

	client *http.Client
	stop   chan struct{}
	wg     sync.WaitGroup
	offset int64

	// ---- 回调（由 main/server 装配，避免反向依赖）----

	// SaveChatID 把 /start 发起者的 chat_id 持久化到配置表（自动绑定）。
	SaveChatID func(chatID string) error
	// ListProjects 查询项目列表（/status 用）。
	ListProjects func() []model.Project
	// CheckProject 触发某项目一键检查（/check 用），id=0 表示全部。
	CheckProject func(id uint) error
}

// NewTelegramBot 创建 Bot（未启动；调用 Start 后开始长轮询）。
func NewTelegramBot(token, chatID string) *TelegramBot {
	return &TelegramBot{
		token:  token,
		chatID: chatID,
		client: &http.Client{Timeout: 35 * time.Second},
		stop:   make(chan struct{}),
	}
}

// UpdateConfig 热更新 token/chatID（配置中心保存后调用）。
// token 变化需要重启轮询（由 Hub 负责）。
func (b *TelegramBot) UpdateConfig(token, chatID string) {
	b.mu.Lock()
	b.token, b.chatID = token, chatID
	b.mu.Unlock()
}

// Token 返回当前 token（Hub 判断是否需要重启轮询）。
func (b *TelegramBot) Token() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.token
}

// ChatID 返回当前绑定的 chat_id。
func (b *TelegramBot) ChatID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.chatID
}

// Start 启动命令长轮询 goroutine。
func (b *TelegramBot) Start() {
	b.wg.Add(1)
	go b.runLoop()
	log.Printf("[telegram] Bot 命令监听已启动")
}

// Stop 停止轮询。
func (b *TelegramBot) Stop() {
	select {
	case <-b.stop:
	default:
		close(b.stop)
	}
	b.wg.Wait()
}

// runLoop getUpdates 长轮询主循环。
func (b *TelegramBot) runLoop() {
	defer b.wg.Done()
	for {
		select {
		case <-b.stop:
			return
		default:
		}
		b.pollOnce()
	}
}

// tgUpdate / tgMessage Telegram API 数据结构（仅取所需字段）。
type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

type tgMessage struct {
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Text string `json:"text"`
}

// pollOnce 拉取一批更新并逐条处理命令。
func (b *TelegramBot) pollOnce() {
	b.mu.Lock()
	token := b.token
	offset := b.offset
	b.mu.Unlock()
	if token == "" {
		// 未配置 token：休眠等待配置（Hub 更新后会重启）
		select {
		case <-b.stop:
		case <-time.After(5 * time.Second):
		}
		return
	}

	api := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=25&offset=%d", token, offset)
	ctx, cancel := context.WithTimeout(context.Background(), 32*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return
	}
	resp, err := b.client.Do(req)
	if err != nil {
		// 网络/超时错误：稍等后重试，避免死循环打爆日志
		select {
		case <-b.stop:
		case <-time.After(3 * time.Second):
		}
		return
	}
	defer resp.Body.Close()

	var out struct {
		OK     bool        `json:"ok"`
		Result []tgUpdate  `json:"result"`
		Error  *struct { //nolint
			Description string `json:"description"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || !out.OK {
		if out.Error != nil {
			log.Printf("[telegram] getUpdates 失败: %s", out.Error.Description)
		}
		select {
		case <-b.stop:
		case <-time.After(5 * time.Second):
		}
		return
	}

	for _, u := range out.Result {
		b.mu.Lock()
		b.offset = u.UpdateID + 1
		b.mu.Unlock()
		if u.Message != nil && u.Message.Text != "" {
			go b.handleCommand(strconv.FormatInt(u.Message.Chat.ID, 10), u.Message.Text)
		}
	}
}

// handleCommand 解析并处理一条 Bot 命令。
func (b *TelegramBot) handleCommand(chatID, text string) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return
	}
	cmd := strings.ToLower(fields[0])

	switch cmd {
	case "/start":
		b.cmdStart(chatID)
	case "/help", "help":
		b.reply(chatID, "WatchDog AI 智能运维机器人\n\n"+
			"可用命令：\n"+
			"/status - 查看全部项目监控状态\n"+
			"/check - 对所有项目执行一次立即检查\n"+
			"/check <项目ID> - 只检查指定项目\n"+
			"/ping - 存活测试\n\n"+
			"绑定说明：告警消息会自动推送到本对话。")
	case "/status":
		b.cmdStatus(chatID)
	case "/check":
		if len(fields) >= 2 {
			if id, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				b.cmdCheck(chatID, uint(id))
				return
			}
		}
		b.cmdCheck(chatID, 0)
	case "/ping":
		b.reply(chatID, "pong ✓ WatchDog AI 运行中")
	default:
		// 非命令消息：简单回应，避免用户以为 Bot 离线
		b.reply(chatID, "收到。发送 /help 查看可用命令。")
	}
}

// cmdStart 处理 /start：欢迎语 + 自动绑定 chat_id。
func (b *TelegramBot) cmdStart(chatID string) {
	b.reply(chatID, "👋 欢迎使用 WatchDog AI！\n\n"+
		"我已开始为你服务：\n"+
		"• 文件变更告警会推送到本对话\n"+
		"• 发送 /status 查看项目状态\n"+
		"• 发送 /check 立即执行一次全量检查")

	// 若尚未绑定 chat_id，自动保存（配置写活）
	if b.ChatID() == "" && b.SaveChatID != nil {
		if err := b.SaveChatID(chatID); err == nil {
			b.mu.Lock()
			b.chatID = chatID
			b.mu.Unlock()
			b.reply(chatID, "✓ 已自动绑定本对话为告警接收方（Chat ID: "+chatID+"）")
			log.Printf("[telegram] 已自动绑定 chat_id=%s", chatID)
		}
	}
}

// cmdStatus 处理 /status：列出项目状态。
func (b *TelegramBot) cmdStatus(chatID string) {
	if b.ListProjects == nil {
		b.reply(chatID, "暂无项目数据")
		return
	}
	ps := b.ListProjects()
	if len(ps) == 0 {
		b.reply(chatID, "还没有添加任何项目。请到 Web 面板「项目总览」添加。")
		return
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("共 %d 个项目：\n\n", len(ps)))
	icons := map[string]string{"green": "🟢", "yellow": "🟡", "red": "🔴"}
	for _, p := range ps {
		ic := icons[p.Status]
		if ic == "" {
			ic = "⚪"
		}
		state := "监控中"
		if !p.Enabled {
			state = "已停用"
		}
		sb.WriteString(fmt.Sprintf("%s #%d %s（%s）\n    %s\n", ic, p.ID, p.Name, state, p.Path))
	}
	sb.WriteString("\n/check <ID> 立即检查某项目")
	b.reply(chatID, sb.String())
}

// cmdCheck 处理 /check：触发一键检查。
func (b *TelegramBot) cmdCheck(chatID string, id uint) {
	if b.CheckProject == nil {
		b.reply(chatID, "检查功能未就绪")
		return
	}
	if err := b.CheckProject(id); err != nil {
		b.reply(chatID, "检查触发失败："+err.Error())
		return
	}
	if id == 0 {
		b.reply(chatID, "✓ 已触发全部项目的检查，结果稍后推送到本对话与 Web 面板。")
	} else {
		b.reply(chatID, "✓ 已触发项目检查，结果稍后推送。")
	}
}

// reply 向指定 chat 发送纯文本消息。
func (b *TelegramBot) reply(chatID, text string) { b.sendTo(chatID, text) }

// SendToBound 向已绑定的 chat_id 推送消息（供 Hub 告警推送使用）。
// 未绑定时静默跳过（前端 WebSocket 仍可收到）。
func (b *TelegramBot) SendToBound(text string) {
	id := b.ChatID()
	if id == "" {
		return
	}
	b.sendTo(id, text)
}

// sendTo 调用 Telegram sendMessage API。
func (b *TelegramBot) sendTo(chatID, text string) {
	token := b.Token()
	if token == "" || chatID == "" {
		return
	}
	// Telegram 单条消息上限 4096 字符，超出截断
	if len(text) > 4000 {
		text = text[:4000] + "\n...(内容过长已截断)"
	}

	api := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api,
		strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := b.client.Do(req)
	if err != nil {
		log.Printf("[telegram] 发送失败(chat=%s): %v", chatID, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Description string `json:"description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		log.Printf("[telegram] 发送被拒(chat=%s): %s", chatID, e.Description)
	}
}
