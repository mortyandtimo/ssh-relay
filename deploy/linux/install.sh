#!/usr/bin/env bash
# 驻阡陌 (Cloud Relay Platform) — 一键部署脚本
# 用法: sudo bash install.sh
set -euo pipefail

INSTALL_DIR="/opt/cloud-relay"
CONFIG_DIR="/etc/cloud-relay"
LOG_DIR="/var/log/cloud-relay"
BIN_DIR="$INSTALL_DIR/bin"
ENV_FILE="$CONFIG_DIR/cloud-relay.env"
SYSTEMD_DIR="/etc/systemd/system"

BOLD='\033[1m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
NC='\033[0m'

info()  { echo -e "${GREEN}[✓]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[✗]${NC} $*"; exit 1; }

# ─── 前置检查 ───
check_root() {
  [[ $EUID -eq 0 ]] || error "请使用 root 权限运行此脚本 (sudo bash install.sh)"
}

check_os() {
  if [[ ! -f /etc/os-release ]]; then error "无法识别操作系统"; fi
  source /etc/os-release
  case "$ID" in
    centos|rhel|rocky|alma|fedora|amzn) PKG_MGR="yum";;
    ubuntu|debian) PKG_MGR="apt";;
    *) warn "未测试的发行版: $ID，继续安装但可能需要手动调整";;
  esac
}

# ─── 依赖安装 ───
install_deps() {
  local needed=()
  command -v go    &>/dev/null || needed+=(go)
  command -v gcc   &>/dev/null || needed+=(gcc)
  command -v git   &>/dev/null || needed+=(git)
  command -v psql  &>/dev/null || needed+=(postgresql)

  if [[ ${#needed[@]} -eq 0 ]]; then
    info "系统依赖已满足"
    return
  fi

  warn "缺少依赖: ${needed[*]}"
  echo -n "是否自动安装? [Y/n] "
  read -r ans
  [[ "${ans,,}" == "n" ]] && error "请手动安装依赖后重试"

  case "$PKG_MGR" in
    yum)
      yum install -y golang gcc git postgresql postgresql-server 2>/dev/null || {
        warn "yum 安装失败，尝试手动安装 Go..."
        install_go_manually
      }
      ;;
    apt)
      apt-get update -qq
      apt-get install -y golang-go gcc git postgresql postgresql-contrib 2>/dev/null || {
        warn "apt 安装失败，尝试手动安装 Go..."
        install_go_manually
      }
      ;;
  esac
}

install_go_manually() {
  local GO_VER="1.23.6"
  if command -v go &>/dev/null; then return; fi
  warn "手动安装 Go $GO_VER..."
  curl -sL "https://go.dev/dl/go${GO_VER}.linux-amd64.tar.gz" | tar -C /usr/local -xzf -
  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
  source /etc/profile.d/go.sh
  export PATH=$PATH:/usr/local/go/bin
  command -v go &>/dev/null || error "Go 安装失败"
  info "Go $(go version) 安装成功"
}

# ─── PostgreSQL 设置 ───
setup_postgres() {
  if systemctl is-active postgresql &>/dev/null; then
    info "PostgreSQL 已运行"
  else
    warn "初始化 PostgreSQL..."
    case "$PKG_MGR" in
      yum) postgresql-setup initdb 2>/dev/null || true;;
      apt) true;;  # debian 自动初始化
    esac
    systemctl enable --now postgresql
    info "PostgreSQL 已启动"
  fi

  # 创建数据库和用户
  local DB_NAME="cloudrelay"
  local DB_USER="cloudrelay"
  local DB_PASS
  echo -n "设置 PostgreSQL 数据库密码 (直接回车使用随机密码): "
  read -r -s DB_PASS
  echo
  [[ -z "$DB_PASS" ]] && DB_PASS=$(head -c 24 /dev/urandom | base64 | tr -d '/+=' | head -c 24)

  su - postgres -c "psql -c \"CREATE USER ${DB_USER} WITH PASSWORD '${DB_PASS}';\"" 2>/dev/null || true
  su - postgres -c "psql -c \"CREATE DATABASE ${DB_NAME} OWNER ${DB_USER};\"" 2>/dev/null || true
  su - postgres -c "psql -c \"GRANT ALL PRIVILEGES ON DATABASE ${DB_NAME} TO ${DB_USER};\"" 2>/dev/null || true

  DATABASE_URL="postgres://${DB_USER}:${DB_PASS}@127.0.0.1:5432/${DB_NAME}?sslmode=disable"
  info "数据库创建完成: ${DB_NAME}"
}

# ─── 交互式配置 ───
collect_config() {
  echo ""
  echo -e "${BOLD}═══════════════════════════════════════${NC}"
  echo -e "${BOLD}   驻阡陌 — 云端中转平台 配置向导${NC}"
  echo -e "${BOLD}═══════════════════════════════════════${NC}"
  echo ""

  # 管理员邮箱
  while [[ -z "$ADMIN_EMAIL" ]]; do
    echo -n "管理员邮箱: "
    read -r ADMIN_EMAIL
    [[ -z "$ADMIN_EMAIL" ]] && warn "邮箱不能为空"
  done

  # 管理员显示名
  echo -n "管理员显示名 [管理员]: "
  read -r ADMIN_DISPLAY_NAME
  ADMIN_DISPLAY_NAME="${ADMIN_DISPLAY_NAME:-管理员}"

  # 管理员密码
  while [[ -z "$ADMIN_PASSWORD" ]]; do
    echo -n "管理员密码: "
    read -r -s ADMIN_PASSWORD
    echo
    [[ -z "$ADMIN_PASSWORD" ]] && warn "密码不能为空"
  done

  # Bootstrap secret
  BOOTSTRAP_SECRET=$(head -c 32 /dev/urandom | base64 | tr -d '/+=' | head -c 32)

  # Access secret
  ACCESS_SECRET=$(head -c 32 /dev/urandom | base64 | tr -d '/+=' | head -c 32)

  # 服务监听端口
  echo -n "server-api 监听地址 [:7710]: "
  read -r SERVER_API_ADDR
  SERVER_API_ADDR="${SERVER_API_ADDR:-:7710}"

  echo -n "relay-tcp 监听地址 [:9090]: "
  read -r RELAY_TCP_ADDR
  RELAY_TCP_ADDR="${RELAY_TCP_ADDR:-:9090}"

  echo -n "relay-udp 监听地址 [:9093]: "
  read -r RELAY_UDP_ADDR
  RELAY_UDP_ADDR="${RELAY_UDP_ADDR:-:9093}"

  echo -n "relay-http 监听地址 [:9091]: "
  read -r RELAY_HTTP_ADDR
  RELAY_HTTP_ADDR="${RELAY_HTTP_ADDR:-:9091}"

  echo -n "本机公网IP或域名: "
  read -r PUBLIC_HOST
  PUBLIC_HOST="${PUBLIC_HOST:-}"

  echo ""
  info "配置收集完成"
}

# ─── 编译 ───
build_binaries() {
  warn "编译 Go 后端服务..."
  local SRC_DIR
  SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

  export CGO_ENABLED=0 GOOS=linux GOARCH=amd64

  mkdir -p "$BIN_DIR"
  go build -o "$BIN_DIR/server-api"  "$SRC_DIR/apps/server-api/cmd/server-api"
  go build -o "$BIN_DIR/relay-tcp"   "$SRC_DIR/apps/relay-tcp/cmd/relay-tcp"
  go build -o "$BIN_DIR/relay-udp"   "$SRC_DIR/apps/relay-udp/cmd/relay-udp"
  go build -o "$BIN_DIR/relay-web"   "$SRC_DIR/apps/relay-web/cmd/relay-web"
  go build -o "$BIN_DIR/relay-http"   "$SRC_DIR/apps/relay-http/cmd/relay-http"
  go build -o "$BIN_DIR/relay-https"  "$SRC_DIR/apps/relay-https/cmd/relay-https"
  go build -o "$BIN_DIR/client-agent" "$SRC_DIR/apps/client-agent/cmd/client-agent"

  info "编译完成: $(ls "$BIN_DIR/")"
}

# ─── 写配置文件 ───
write_config() {
  mkdir -p "$CONFIG_DIR" "$LOG_DIR"
  chown root:root "$CONFIG_DIR" "$LOG_DIR"
  chmod 750 "$CONFIG_DIR"

  cat > "$ENV_FILE" <<EOF
# 驻阡陌 云端中转平台 — 服务配置
# 由 install.sh 自动生成，可手动修改后 systemctl restart cloud-relay-\*

# ─── server-api ───
SERVER_API_ADDR=${SERVER_API_ADDR}
DATABASE_URL=${DATABASE_URL}
SERVER_API_ACCESS_SECRET=${ACCESS_SECRET}
SERVER_API_ADMIN_BOOTSTRAP_SECRET=${BOOTSTRAP_SECRET}
SERVER_API_ADMIN_WEB_DIR=${INSTALL_DIR}/admin-web
SERVER_API_ALLOWED_ORIGINS=
SERVER_API_AUTH_COOKIES_SECURE=false

# ─── relay-tcp ───
RELAY_TCP_ADDR=${RELAY_TCP_ADDR}
RELAY_TCP_API_BASE_URL=http://127.0.0.1${SERVER_API_ADDR#*:}

# ─── relay-udp ───
RELAY_UDP_ADDR=${RELAY_UDP_ADDR}
RELAY_UDP_API_BASE_URL=http://127.0.0.1${SERVER_API_ADDR#*:}

# ─── relay-web ───
RELAY_WEB_ADDR=:9094
RELAY_WEB_HTTP_ADDR=:9095
RELAY_WEB_API_BASE_URL=http://127.0.0.1${SERVER_API_ADDR#*:}
RELAY_WEB_STANDBY_TARGET_SIZE=64
RELAY_WEB_STANDBY_MAX_SIZE=128
RELAY_WEB_GLOBAL_MAX_STANDBY=16384

# ─── server-api nginx ───
SERVER_API_NGINX_CONFIG_DIR=/etc/cloud-relay/nginx
SERVER_API_NGINX_CERT_DIR=/etc/cloud-relay/certs
SERVER_API_NGINX_BIN=/www/server/nginx/sbin/nginx
SERVER_API_ACME_EMAIL=webmaster@020309.top

# ─── relay-http ───
RELAY_HTTP_ADDR=${RELAY_HTTP_ADDR}
RELAY_HTTP_DOMAIN_SUFFIX=example.com

# ─── relay-https ───
RELAY_HTTPS_API_BASE_URL=http://127.0.0.1${SERVER_API_ADDR#*:}
EOF

  chmod 640 "$ENV_FILE"
  info "配置文件写入: $ENV_FILE"
}

# ─── Bootstrap 管理员 ───
bootstrap_admin() {
  warn "创建管理员账号..."
  local API_PORT="${SERVER_API_ADDR#:}"
  API_PORT="${API_PORT#:}"

  # 等待 server-api 启动
  for i in $(seq 1 30); do
    curl -sf "http://127.0.0.1:${API_PORT}/healthz" &>/dev/null && break
    sleep 1
  done

  local RESULT
  RESULT=$(curl -s -X POST "http://127.0.0.1:${API_PORT}/api/auth/bootstrap" \
    -H "Content-Type: application/json" \
    -H "X-Bootstrap-Secret: ${BOOTSTRAP_SECRET}" \
    -d "{\"email\":\"${ADMIN_EMAIL}\",\"displayName\":\"${ADMIN_DISPLAY_NAME}\",\"password\":\"${ADMIN_PASSWORD}\"}" 2>/dev/null || echo "FAILED")

  if [[ "$RESULT" == *"FAILED"* || "$RESULT" == *"error"* ]]; then
    warn "管理员创建可能失败，可稍后手动执行:"
    echo "  curl -X POST http://127.0.0.1:${API_PORT}/api/auth/bootstrap \\"
    echo "    -H 'Content-Type: application/json' \\"
    echo "    -H 'X-Bootstrap-Secret: ${BOOTSTRAP_SECRET}' \\"
    echo "    -d '{\"email\":\"${ADMIN_EMAIL}\",\"displayName\":\"${ADMIN_DISPLAY_NAME}\",\"password\":\"YOUR_PASSWORD\"}'"
  else
    info "管理员账号创建成功: ${ADMIN_EMAIL}"
  fi

  unset ADMIN_PASSWORD
}

# ─── systemd 服务 ───
install_systemd() {
  local SERVICES=(server-api relay-tcp relay-udp relay-web relay-http relay-https)

  for SVC in "${SERVICES[@]}"; do
    cat > "$SYSTEMD_DIR/cloud-relay-${SVC}.service" <<EOF
[Unit]
Description=Cloud Relay — ${SVC}
After=network.target postgresql.service
Wants=postgresql.service

[Service]
Type=simple
ExecStart=${BIN_DIR}/${SVC}
EnvironmentFile=${ENV_FILE}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardOutput=append:${LOG_DIR}/${SVC}.log
StandardError=append:${LOG_DIR}/${SVC}.err

[Install]
WantedBy=multi-user.target
EOF
  done

  systemctl daemon-reload
  info "systemd 服务文件已安装"

  # ─── nginx 配置 ───
  mkdir -p /etc/cloud-relay/nginx /etc/cloud-relay/certs
  if [[ -f /www/server/nginx/conf/nginx.conf ]]; then
    if ! grep -q 'cloud-relay/nginx' /www/server/nginx/conf/nginx.conf; then
      sed -i '/http {/a\\tinclude /etc/cloud-relay/nginx/*.conf;' /www/server/nginx/conf/nginx.conf
      info "nginx 已添加 cloud-relay 配置 include"
    fi
  fi
}

# ─── 管理脚本 ───
install_cli() {
  local SCRIPT_SRC
  SCRIPT_SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/scripts/cloud-relay-cli.sh"
  if [[ ! -f "$SCRIPT_SRC" ]]; then
    error "CLI 脚本不存在: $SCRIPT_SRC"
  fi
  cp "$SCRIPT_SRC" /usr/local/bin/cloud-relay
  chmod +x /usr/local/bin/cloud-relay
  local AUTOSTART_SCRIPT_SRC
  AUTOSTART_SCRIPT_SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/scripts/cloud-relay-server-api-autostart.sh"
  if [[ ! -f "$AUTOSTART_SCRIPT_SRC" ]]; then
    error "server-api 自启脚本不存在: $AUTOSTART_SCRIPT_SRC"
  fi
  cp "$AUTOSTART_SCRIPT_SRC" /usr/local/bin/cloud-relay-server-api-autostart
  chmod +x /usr/local/bin/cloud-relay-server-api-autostart
  info "管理命令已安装: cloud-relay start|stop|restart|status|ports|log|enable|disable|config"
  info "server-api 自启命令已安装: cloud-relay-server-api-autostart enable|disable|status|verify"
}

# ─── 启动服务 ───
start_services() {
  warn "启动 server-api..."
  systemctl enable --now cloud-relay-server-api
  sleep 2
  bootstrap_admin
  warn "启动中继服务..."
  systemctl enable --now cloud-relay-relay-tcp cloud-relay-relay-udp cloud-relay-relay-http cloud-relay-relay-https
  info "所有服务已启动"
}

# ─── 完成 ───
print_summary() {
  echo ""
  echo -e "${BOLD}═══════════════════════════════════════${NC}"
  echo -e "${GREEN}  驻阡陌 部署完成！${NC}"
  echo -e "${BOLD}═══════════════════════════════════════${NC}"
  echo ""
  echo "  安装目录:   $INSTALL_DIR"
  echo "  配置文件:   $ENV_FILE"
  echo "  日志目录:   $LOG_DIR"
  echo "  管理员:     $ADMIN_EMAIL"
  echo ""
  echo -e "  ${BOLD}服务端口:${NC}"
  local API_PORT="${SERVER_API_ADDR#:}"
  local TCP_PORT="${RELAY_TCP_ADDR#:}"
  local UDP_PORT="${RELAY_UDP_ADDR#:}"
  local HTTP_PORT="${RELAY_HTTP_ADDR#:}"
  echo "    server-api  ${API_PORT}"
  echo "    relay-tcp   ${TCP_PORT}"
  echo "    relay-udp   ${UDP_PORT}"
  echo "    relay-http  ${HTTP_PORT}"
  [[ -n "$PUBLIC_HOST" ]] && echo ""
  [[ -n "$PUBLIC_HOST" ]] && echo "  公网入口:   $PUBLIC_HOST"
  echo ""
  echo "  常用命令:"
  echo "    cloud-relay status     — 查看服务状态"
  echo "    cloud-relay ports      — 查看服务端口"
  echo "    cloud-relay restart    — 重启所有服务"
  echo "    cloud-relay log        — 查看日志"
  echo "    cloud-relay config     — 修改配置"
  echo "    cloud-relay-server-api-autostart enable|disable|status|verify"
  echo ""
  echo -e "  ${YELLOW}重要: 请妥善保管 bootstrap secret 和数据库密码${NC}"
  echo -e "  ${YELLOW}它们保存在 $ENV_FILE 中${NC}"
  echo ""
}

# ─── 主流程 ───
main() {
  check_root
  check_os
  install_deps
  setup_postgres
  collect_config
  build_binaries
  write_config
  install_systemd
  install_cli
  start_services
  print_summary
}

main "$@"
