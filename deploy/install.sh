#!/usr/bin/env bash
#
# WatchDog AI —— 一键安装脚本
#
# 用法（任选其一）：
#   sudo ./deploy/install.sh                        # 本地已有源码（在仓库内执行）
#   curl -fsSL <仓库地址>/deploy/install.sh | sudo bash   # 远程一键安装（开源分发推荐）
#
# 远程模式会自动 clone 源码（默认 github.com/watchdog-ai/watchdog，
# 可用环境变量 WATCHDOG_REPO_URL 覆盖），无需手动下载任何文件。
#
# 做什么：
#   1. 自动检测并安装依赖（Go、Node.js/npm、git、build 工具）
#   2. 编译 Go 后端 → 单二进制
#   3. 构建 Vue3 前端 → web/dist
#   4. 创建 watchdog 运行用户与 /opt/watchdog 目录
#   5. 动态生成并安装 Systemd 服务
#   6. 启动服务并打印访问地址
#
# 可配置的环境变量：
#   WATCHDOG_INSTALL_DIR   安装目录，默认 /opt/watchdog
#   WATCHDOG_BIND          HTTP 监听地址，默认 :9191
#   WATCHDOG_DATA_DIR      数据目录，默认 {安装目录}/data
#   WATCHDOG_SKIP_DEPS     1=跳过依赖安装（用于已装好环境）
#   WATCHDOG_SKIP_FRONTEND 1=跳过前端构建（仅后端）
#   WATCHDOG_GO_VERSION    Go 版本，默认 1.22.5
#
# 安全说明：脚本仅在 /tmp 与安装目录内操作，若需写入 /usr 会请求 sudo 权限。

set -euo pipefail

# ---------- 输出与工具函数（必须先定义、后使用） ----------
c() { printf '\033[36m%s\033[0m\n' "$1"; }   # 青色标题
ok() { printf '\033[32m✔ %s\033[0m\n' "$1"; }
warn() { printf '\033[33m⚠ %s\033[0m\n' "$1"; }
fail() { printf '\033[31m✘ %s\033[0m\n' "$1" >&2; exit 1; }

# ---------- 0. 权限检查 ----------
require_sudo() {
  if [[ $EUID -eq 0 ]]; then return; fi
  if ! command -v sudo >/dev/null; then
    fail "请用 root 运行，或安装 sudo 后改用： sudo $0"
  fi
}
maybe_sudo() {
  if [[ $EUID -eq 0 ]]; then "$@"; else sudo "$@"; fi
}
# 安全提取 go 版本号（如 1.22 → 122）。go 不存在 / 输出异常 / 解析失败时一律返回 0。
# 注意：不能把未校验的字符串直接放进 [[ -lt ]]，bash 会按算术表达式解析，
# 遇到裸单词（如 "go version" 里的 go）会被当成变量名，set -u 下直接报 unbound variable。
go_minor() {
  command -v go >/dev/null 2>&1 || { echo 0; return; }
  local v
  v="$(go version 2>/dev/null | sed -nE 's/.*go([0-9]+)\.([0-9]+).*/\1\2/p' | head -n1)"
  if [[ "$v" =~ ^[0-9]+$ ]]; then echo "$v"; else echo 0; fi
}

# ---------- 定位源码：本地模式 / 远程模式 ----------
# 远程一键安装时（curl | bash）脚本从 stdin 运行，源码并不在本地，需自动 clone。
# 判断依据是「脚本自身所在位置」（deploy/ 的上级是否有 go.mod），
# 而非当前工作目录——这样从任何目录用绝对路径执行也能正确识别本地仓库。
# 注意：管道模式下 BASH_SOURCE 未定义（set -u 会报 unbound variable），用 $0 兜底。
SELF="${BASH_SOURCE[0]:-$0}"
SELF_DIR=""
if [[ -n "$SELF" && "$SELF" != "bash" && "$SELF" != "sh" && -e "$SELF" ]]; then
  SELF_DIR="$(cd "$(dirname -- "$SELF")" 2>/dev/null && pwd || true)"
fi
if [[ -n "$SELF_DIR" && -f "$SELF_DIR/../go.mod" && -d "$SELF_DIR/../web" ]]; then
  SCRIPT_SRC="$(cd "$SELF_DIR/.." && pwd)"
else
  REPO_URL="${WATCHDOG_REPO_URL:-https://github.com/watchdog-ai/watchdog.git}"
  c "==> 检测到远程安装模式，正在拉取源码 $REPO_URL ..."
  TMP_REPO="$(mktemp -d)"
  command -v git >/dev/null || fail "远程安装需要 git，请先安装：apt-get install -y git"
  git clone --depth 1 "$REPO_URL" "$TMP_REPO"
  SCRIPT_SRC="$TMP_REPO"
fi

# ---------- 常量与变量 ----------
INSTALL_DIR="${WATCHDOG_INSTALL_DIR:-/opt/watchdog}"
BIND="${WATCHDOG_BIND:-:9191}"
DATA_DIR="${WATCHDOG_DATA_DIR:-$INSTALL_DIR/data}"
SKIP_DEPS="${WATCHDOG_SKIP_DEPS:-0}"
SKIP_FRONTEND="${WATCHDOG_SKIP_FRONTEND:-0}"
GO_VER="${WATCHDOG_GO_VERSION:-1.22.5}"
RUN_USER="watchdog"

c "==> WatchDog AI 一键安装开始"

# ---------- 1. 依赖安装 ----------
install_deps() {
  if [[ "$SKIP_DEPS" == "1" ]]; then
    ok "已跳过依赖安装 (WATCHDOG_SKIP_DEPS=1)"
    return
  fi
  c "==> 检测系统并安装依赖 (Go / Node / git / 构建工具)"

  # 定位包管理器（直接记录命令名，后续统一用 "$PM" 调用）
  if   command -v apt-get >/dev/null; then PM="apt-get"
  elif command -v dnf     >/dev/null; then PM="dnf"
  elif command -v yum     >/dev/null; then PM="yum"
  elif command -v apt     >/dev/null; then PM="apt"
  else warn "未能识别包管理器，假定依赖已就绪"; return; fi

  # 刷新软件源索引（各家参数不同；失败不阻断安装）
  if [[ "$PM" == "apt-get" || "$PM" == "apt" ]]; then
    maybe_sudo "$PM" update -qq || true
  else
    maybe_sudo "$PM" makecache 2>/dev/null || maybe_sudo "$PM" update || true
  fi

  # Go：优先用系统包，检测版本是否足够新（>=1.20）
  if [[ "$(go_minor)" -lt 120 ]]; then
    if [[ "$PM" == "apt-get" || "$PM" == "apt" ]]; then
      maybe_sudo "$PM" install -y golang-go || true
    else
      maybe_sudo "$PM" install -y golang || true
    fi
  fi
  # 若系统 Go 仍过旧，改用官方二进制（最稳妥）
  if [[ "$(go_minor)" -lt 120 ]]; then
    c "   系统 Go 版本过旧，下载官方 Go $GO_VER ..."
    GO_TMP="$(mktemp -d)"
    curl -fsSL --connect-timeout 15 \
      -o "$GO_TMP/go.tgz" \
      "https://go.dev/dl/go${GO_VER}.linux-amd64.tar.gz"
    maybe_sudo tar -C /usr/local -xzf "$GO_TMP/go.tgz"
    rm -rf "$GO_TMP"
    export PATH="/usr/local/go/bin:$PATH"
    # 写入 profile 便于后续 shell 使用
    grep -q '/usr/local/go/bin' /etc/profile 2>/dev/null || \
      echo 'export PATH=$PATH:/usr/local/go/bin' | maybe_sudo tee -a /etc/profile >/dev/null
    export GOROOT=/usr/local/go
  fi
  ok "Go: $(go version 2>/dev/null || echo 已就绪)"

  # Node.js / npm（构建前端需要；仓库已内置 web/dist 时通常用不到，装上以备自建前端）
  if [[ "$SKIP_FRONTEND" != "1" ]] && ! command -v npm >/dev/null; then
    c "   安装 Node.js ..."
    if [[ "$PM" == "apt-get" || "$PM" == "apt" ]]; then
      maybe_sudo "$PM" install -y ca-certificates curl gnupg
      curl -fsSL https://deb.nodesource.com/setup_20.x | maybe_sudo bash - || true
      maybe_sudo "$PM" install -y nodejs || true
    else
      maybe_sudo "$PM" install -y nodejs npm || true
    fi
  fi
  command -v npm >/dev/null && ok "npm: $(npm -v)" || warn "npm 不可用（前端将跳过）"

  # 其它工具
  maybe_sudo "$PM" install -y git gcc wget curl 2>/dev/null || true
}

# ---------- 2. 编译后端 ----------
build_backend() {
  c "==> 编译 Go 后端"
  cd "$SCRIPT_SRC"
  export GOFLAGS="${GOFLAGS:-}" GOTOOLCHAIN=auto
  export PATH="/usr/local/go/bin:$PATH"
  go build -o /tmp/watchdog-binary ./cmd/watchdog
  ok "后端编译完成"
}

# ---------- 3. 构建前端 ----------
build_frontend() {
  [[ "$SKIP_FRONTEND" == "1" ]] && { warn "已跳过前端构建，请确认 web/dist 已存在"; return; }

  c "==> 处理前端资源"

  # 情况一：已有构建产物，直接用（无需 npm）
  if [[ -f "$SCRIPT_SRC/web/dist/index.html" ]]; then
    ok "检测到已有前端产物（web/dist/index.html），直接使用，跳过 Node 构建"
    return
  fi

  # 情况二：有 npm，则构建
  if command -v npm >/dev/null; then
    c "   当前无前端产物，使用 npm 构建 ..."
    cd "$SCRIPT_SRC/web"

    # 安装依赖：失败不再静默跳过（否则 vite 会 command not found）
    # 策略：先用默认源，失败自动切换国内镜像 npmmirror.com 重试一次
    if [[ ! -d node_modules || ! -x node_modules/.bin/vite ]]; then
      c "   安装前端依赖 (npm install) ..."
      if ! npm install --no-audit --no-fund --loglevel=error; then
        warn "默认源安装失败，切换国内镜像 (npmmirror.com) 重试 ..."
        npm install --no-audit --no-fund --loglevel=error \
          --registry=https://registry.npmmirror.com \
          || fail "npm install 失败（默认源与国内镜像均不可用）。\n  排查：① 服务器网络出口 ② 手动执行: cd web && npm install --registry=https://registry.npmmirror.com"
      fi
    fi

    # 双保险：vite 仍不在（部分环境 PATH 不含 node_modules/.bin），补装一次
    if [[ ! -x node_modules/.bin/vite ]]; then
      warn "vite 未就绪，补装 vite ..."
      npm install -D vite --registry=https://registry.npmmirror.com \
        || fail "vite 安装失败"
    fi

    # 用本地 vite 跑构建，避免 PATH 问题
    if [[ -x node_modules/.bin/vite ]]; then
      ./node_modules/.bin/vite build || fail "前端构建失败"
    else
      npm run build || fail "前端构建失败"
    fi
    [[ -f dist/index.html ]] || fail "前端构建产物缺失 (web/dist/index.html)"
    ok "前端构建完成 → web/dist"
    return
  fi

  # 情况三：既无产物也无 npm —— 明确失败，避免部署后 404
  fail "缺少前端产物(web/dist) 且未安装 npm。请任选其一解决：\n  ① 下载发行包(含 dist)再运行；② 执行 apt-get install -y nodejs 后重试"
}

# ---------- 4. 安装到目标目录 ----------
install_to_dir() {
  c "==> 安装到 $INSTALL_DIR"

  # 先停旧服务：正在运行的二进制无法被覆盖（cp 会报 Text file busy）
  if command -v systemctl >/dev/null && systemctl is-active --quiet watchdog 2>/dev/null; then
    c "   停止旧版服务（升级模式）..."
    maybe_sudo systemctl stop watchdog 2>/dev/null || true
  fi
  # 兜底：杀掉所有残留的 watchdog 进程（非 systemd 启动的）
  if pgrep -x watchdog >/dev/null 2>&1 || pgrep -f "$INSTALL_DIR/watchdog" >/dev/null 2>&1; then
    warn "检测到残留 watchdog 进程，正在停止 ..."
    maybe_sudo pkill -x watchdog 2>/dev/null || true
    maybe_sudo pkill -f "$INSTALL_DIR/watchdog" 2>/dev/null || true
    sleep 1
  fi

  # 运行用户
  if ! id "$RUN_USER" &>/dev/null; then
    maybe_sudo useradd -r -s /bin/false -M "$RUN_USER" 2>/dev/null || true
    ok "创建运行用户 $RUN_USER"
  fi

  maybe_sudo mkdir -p "$INSTALL_DIR" "$DATA_DIR" "$INSTALL_DIR/web/dist"
  # 若二进制仍被占用（极端情况），先删再拷（unlink 运行中的文件是允许的）
  if [[ -f "$INSTALL_DIR/watchdog" ]] && ! maybe_sudo cp /tmp/watchdog-binary "$INSTALL_DIR/watchdog" 2>/dev/null; then
    warn "二进制被占用，先移除旧文件再写入 ..."
    maybe_sudo rm -f "$INSTALL_DIR/watchdog"
    maybe_sudo cp /tmp/watchdog-binary "$INSTALL_DIR/watchdog"
  fi
  maybe_sudo chmod 755 "$INSTALL_DIR/watchdog"

  if [[ -d "$SCRIPT_SRC/web/dist" ]]; then
    maybe_sudo cp -r "$SCRIPT_SRC/web/dist/." "$INSTALL_DIR/web/dist/"
  fi
  maybe_sudo chown -R "$RUN_USER":"$RUN_USER" "$INSTALL_DIR" "$DATA_DIR"
  ok "二进制与前端已就位"
}

# ---------- 5. 生成并安装 Systemd 服务 ----------
install_service() {
  c "==> 写入 Systemd 服务"
  if ! command -v systemctl >/dev/null; then
    warn "未检测到 systemd，请手动运行：$INSTALL_DIR/watchdog 启动"
    return
  fi
  UNIT="$(cat <<EOF
[Unit]
Description=WatchDog AI 智能运维面板
After=network-online.target
Wants=network-online.target

[Service]
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/watchdog
Environment=WATCHDOG_BIND=$BIND
Environment=WATCHDOG_DATA_DIR=$DATA_DIR

ProtectSystem=full
ReadWritePaths=$INSTALL_DIR
PrivateTmp=true
NoNewPrivileges=true

Restart=on-failure
RestartSec=3

User=$RUN_USER
Group=$RUN_USER

[Install]
WantedBy=multi-user.target
EOF
)"
  maybe_sudo tee /etc/systemd/system/watchdog.service >/dev/null <<<"$UNIT"
  maybe_sudo systemctl daemon-reload
  maybe_sudo systemctl enable --now watchdog
  ok "服务已安装并设置开机自启"
}

# ---------- 6. 收尾 ----------
finish() {
  rm -f /tmp/watchdog-binary
  c "==> 安装完成"
  PORT="${BIND#*:}"
  if command -v systemctl >/dev/null; then
    maybe_sudo systemctl restart watchdog
  else
    maybe_sudo nohup "$INSTALL_DIR/watchdog" >/dev/null 2>&1 &
  fi
  ok "面板已启动，请访问： http://服务器IP:$PORT"
  c "首次使用：打开面板 → 配置中心填 AI API Key → 项目总览添加项目"
}

main() {
  require_sudo
  install_deps
  build_backend
  build_frontend
  install_to_dir
  install_service
  finish
}

# 默认自动执行；被其它脚本 source 复用或纯测试时，设置 WATCHDOG_SOURCE_ONLY=1 不运行。
if [[ "${WATCHDOG_SOURCE_ONLY:-0}" != "1" ]]; then
  main "$@"
fi