# WatchDog AI

轻量级智能运维面板：上传源码后，自动接管**文件监控 → 语法检查 → AI 逻辑分析 → 变更摘要 → 告警/自动修复 → 覆盖前备份 → 一键回滚**整条流水线。开发者只需保存文件（Ctrl+S），剩下的交给它。

## 特性

- **实时监控**：递归监听项目目录树（含子目录），文件保存即触发检查，结果通过 WebSocket 实时推送面板，无需手动刷新。
- **一键检查**：项目卡片「检查」按钮或顶部「一键检查全部」，随时手动触发全量扫描。
- **零感知修复**：修改文件自动触发检查，页面实时刷新生效，无需 SSH 手动上传。
- **绝对安全**：任何覆盖前先备份；危险命令（`rm -rf`、`kill -9`、`chmod 777` 等）被强制拦截；出错可一键回滚。
- **配置写活**：AI 厂商/API Key/备份保留天数等全部在后台可编辑，保存即热生效，代码零硬编码。
- **多项目并行**：Go 协程隔离，支持 5+ 个项目同时监控互不干扰。
- **厂商全覆盖**：统一 LLM 适配层——国内（DeepSeek / 通义千问 / 智谱 GLM / 豆包 / MiniMax）+ 国际（OpenAI / Anthropic Claude）+ 自定义 OpenAI 兼容端点（中转网关 / Ollama / 私有化部署）。
- **Telegram 机器人**：配置 Token 后发送 `/start` 自动绑定通知目标；支持 `/status` 远程查看项目状态、`/check` 远程触发检查。
- **Telegram 风格 UI**：玻璃拟态（高斯模糊）+ 消息气泡式动画，动画仅用 transform/opacity（GPU 合成），中低端设备自动降级，兼顾性能。

## 技术栈

- 后端：Go + Gin + GORM + SQLite（编译为单二进制，Systemd 守护）
- 前端：Vue 3 + Vite + Element Plus（极简卡片式，重点状态指示灯）
- 监控：fsnotify + 内存任务队列 + 防抖

## 目录结构

```
watchdog-ai/
├── cmd/watchdog/         # 程序入口（装配各模块）
├── internal/
│   ├── config/           # 进程级配置（数据目录等）
│   ├── model/            # ORM 模型：Project / Config / BackupRecord
│   ├── database/         # SQLite 连接与迁移
│   ├── watcher/          # 核心监控（fsnotify 递归监听+防抖+队列+流水线，含 Lint/AI 接口）
│   ├── ai/               # LLM 适配层（8 家厂商 + 自定义端点）
│   ├── backup/           # 覆盖前备份 + 过期清理 + 回滚
│   ├── notify/           # WebSocket 总线 + Telegram Bot（推送与命令）
│   ├── server/           # Gin 路由 + WebSocket
│   └── audit/            # 操作日志
├── pkg/security/         # 危险命令过滤
├── web/                  # Vue3 前端
└── deploy/               # Systemd 服务模板
```

## 快速开始

### 方式一：一条命令安装（推荐，最省事）

仓库已内置构建好的前端产物 `web/dist`，因此**服务器无需安装 Node**。在服务器上，源码目录里执行：

```bash
sudo ./deploy/install.sh
```

或者「零克隆」远程直接装（需要 Go 环境，见下方说明）：

```bash
curl -fsSL https://raw.githubusercontent.com/watchdog-ai/watchdog/main/deploy/install.sh | sudo bash
```

脚本会自动完成：**检测并安装 Go → 编译后端 → 复用内置前端产物（无需 Node）→ 创建 watchdog 用户 → 写入 Systemd 服务 → 设置开机自启 → 启动面板**。默认安装到 `/opt/watchdog`，监听 `:9191`。

> 想用源码自建前端时，可在安装前于 `web/` 目录执行 `npm install && npm run build` 更新 `web/dist`；`install.sh` 会优先使用已存在的产物。

可自定义（环境变量）：

```bash
sudo WATCHDOG_BIND=:9000 WATCHDOG_INSTALL_DIR=/srv/watchdog ./deploy/install.sh
```

| 变量 | 说明 | 默认 |
|---|---|---|
| `WATCHDOG_INSTALL_DIR` | 安装目录 | `/opt/watchdog` |
| `WATCHDOG_BIND` | 监听地址 | `:9191` |
| `WATCHDOG_DATA_DIR` | 数据目录 | `{安装目录}/data` |
| `WATCHDOG_GO_VERSION` | Go 版本 | `1.22.5` |
| `WATCHDOG_SKIP_DEPS` | 设为 1 跳过依赖安装 | `0` |
| `WATCHDOG_SKIP_FRONTEND` | 设为 1 仅装后端 | `0` |

### 方式二：Docker 一键部署（零系统依赖）

```bash
# 方式 A：Compose（推荐）
docker compose -f deploy/docker-compose.yml up -d

# 方式 B：直接构建运行
docker build -t watchdog-ai .
docker run -d --name watchdog -p 9191:9191 -v watchdog-data:/app/data watchdog-ai
```

数据通过卷 `watchdog-data` 持久化，重启不丢。适合不想装 Go/Node 的环境。

### 方式三：手动部署

```bash
# 后端：编译单二进制（纯 Go SQLite，无需 CGO）
go build -o watchdog ./cmd/watchdog

# 前端：构建静态资源到 web/dist
cd web && npm install && npm run build && cd ..

# 安装为服务
sudo useradd -r -s /bin/false watchdog
sudo mkdir -p /opt/watchdog
sudo cp watchdog /opt/watchdog/
sudo cp -r web/dist /opt/watchdog/web/
sudo cp deploy/watchdog.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now watchdog
```

> 源码放在 `/home/www/test` 等目录即可，`watchdog` 用户需对其有读写权限。建议监控以下目录：`/home/www` 等。

### 打开面板

浏览器访问 `http://<服务器IP>:9191`：
- 首次进入「配置中心」填写 AI 厂商与 API Key；
- 在「项目总览」添加项目：仅需填写**项目名称**、**磁盘绝对路径**、**访问 URL（或 TeleBot PID）**。

## 核心流程

文件变更 → 防抖(1s) → 入内存队列 → 异步执行：

1. **硬语法检查**：`php -l` / `eslint` / `stylelint` / 内置 HTML 标签闭合检查（无需外部工具）
2. **AI 逻辑分析**：Diff 送大模型，查逻辑错误、数组越界、SQL 注入/XSS、性能瓶颈
3. **变更摘要**：一句话总结本次改动

无论有无问题，结果都会通过 WebSocket 实时推送到面板（右下角气泡通知 + 项目状态灯变化）。

发现问题 → WebSocket 弹窗「发现 X 处错误，是否允许 AI 一键修复？」→「允许修复」→ AI 生成补丁 → **覆盖前备份到** `data/backups/{项目}/{日期}/` → 写入 → **再次检查验证** → 通过则结束，失败自动还原备份并告警。

## 支持的 AI 厂商

| 分类 | 厂商 | 默认模型 | 说明 |
|---|---|---|---|
| 国内 | DeepSeek | `deepseek-chat` | |
| 国内 | 通义千问（阿里） | `qwen-plus` | OpenAI 兼容模式 |
| 国内 | 智谱 GLM | `glm-4-flash` | |
| 国内 | 豆包（字节火山方舟） | `doubao-1.5-pro-32k` | 模型名也可填推理接入点 ID（`ep-xxxx`） |
| 国内 | MiniMax | `MiniMax-Text-01` | |
| 国际 | OpenAI | `gpt-4o-mini` | 国内服务器可能需代理 |
| 国际 | Anthropic Claude | `claude-sonnet-4-20250514` | 独立 Messages 协议，国内服务器可能需代理 |
| 自定义 | 任意 OpenAI 兼容端点 | 必填 | 中转网关 / Ollama（`http://localhost:11434/v1`）/ vLLM 等私有化部署 |

在「配置中心」切换厂商并保存即热生效，无需重启。所有厂商均可用 `ai.base_url` 覆盖官方端点。

## Telegram 机器人

1. 在 Telegram 找 `@BotFather` 发送 `/newbot` 创建机器人，拿到 Token；
2. 面板「配置中心 → Telegram 通知」填入 Bot Token，保存；
3. 在 Telegram 里给你的机器人发送 `/start`，Chat ID 自动绑定（无需手动查找）。

可用命令：

| 命令 | 作用 |
|---|---|
| `/start` | 绑定当前会话为通知目标 |
| `/status` | 查看全部项目状态 |
| `/check` | 远程触发全部项目检查 |
| `/help` | 查看帮助 |

绑定后，文件检查结果（问题与通过摘要）会同步推送到 Telegram。

## 二次开发指引（第一轮已交付）

当前为项目**基础骨架**，可直接编译运行。以下为后续迭代挂接点（均已留接口/注释标注 `TODO(二次开发)`）：

- `internal/watcher/pipeline.go#defaultAnalyze`：把 Diff 接上真实 AI 调用。
- `internal/watcher/watcher.go#fixWithBackup`：接入 AI 修复补丁的“备份→写入→再验证→失败还原”闭环。
- `internal/server/ws.go`：解析前端「允许修复 / 仅告警」决策并路由到对应用项目的修复流程。
- `internal/backup/backup.go`：可加定时任务自动 `CleanupExpired`。
- `internal/audit/audit.go`：接入数据库迁移并按页面展示。

## 安全说明

- 所有覆盖操作前强制备份，记录于「备份仓库」，支持一键回滚。
- AI 生成指令经 `pkg/security` 正则过滤，拦截 `rm -rf`、`kill`、`chmod 777`、fork 炸弹等。
- Systemd 服务默认以低权限账号运行并启用 ProtectSystem 加固。

## License

MIT