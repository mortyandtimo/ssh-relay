#!/usr/bin/env bash
set -euo pipefail
#===============================================================================
# SSH Relay Server Installer
# 一键安装脚本 - 引导安装 ssh-relay-api 到云服务器
#===============================================================================

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
logo(){ echo -e "${CYAN}  ██████╗ ███████╗██╗  ██╗    ██████╗ ███████╗██╗      █████╗ ██╗   ██╗${NC}"; echo -e "${CYAN}  ██╔════╝ ██╔════╝██║  ██║    ██╔══██╗██╔════╝██║     ██╔══██╗╚██╗ ██╔╝${NC}"; echo -e "${CYAN}  ╚█████╗  ███████╗███████║    ██████╔╝█████╗  ██║     ███████║ ╚████╔╝ ${NC}"; echo -e "${CYAN}   ╚═══██╗ ╚════██║██╔══██║    ██╔══██╗██╔══╝  ██║     ██╔══██║  ╚██╔╝  ${NC}"; echo -e "${CYAN}  ██████╔╝ ███████║██║  ██║    ██║  ██║███████╗███████╗██║  ██║   ██║   ${NC}"; echo -e "${CYAN}  ╚═════╝  ╚══════╝╚═╝  ╚═╝    ╚═╝  ╚═╝╚══════╝╚══════╝╚═╝  ╚═╝   ╚═╝   ${NC}"; echo ""
}
ok(){ echo -e "  ${GREEN}[OK]${NC} $*"; }
warn(){ echo -e "  ${YELLOW}[WARN]${NC} $*"; }
err(){ echo -e "  ${RED}[ERR]${NC} $*"; exit 1; }
ask(){ local def="$2"; [[ -n "$def" ]] && def="[$def]"; read -rep "  $1 $def: " val; echo "${val:-$2}"; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_BIN="/opt/cloud-relay-platform/bin"
INSTALL_CONF="/etc/cloud-relay-platform"

logo
echo -e "${CYAN}SSH Relay Server Installer${NC}"
echo -e "此脚本将引导你完成 ssh-relay-api 服务端的安装。"
echo ""

# ─── 0. 检测 ───
echo -e "${CYAN}[1/5]${NC} 环境检测..."
command -v systemctl >/dev/null || err "需要 systemd，不支持当前系统"
[[ -f "$SCRIPT_DIR/ssh-relay-api" ]] || err "未找到 ssh-relay-api 二进制，请将此脚本与二进制放在同一目录"

# ─── 1. 配置收集 ───
echo ""
echo -e "${CYAN}[2/5]${NC} 配置参数"

DOMAIN=$(ask "子域名" "")
DB_URL=$(ask "PostgreSQL 连接串" "postgresql://cloud_relay:password@127.0.0.1:5432/cloud_relay?sslmode=disable")
PORT_START=$(ask "端口范围起始" "40000")
PORT_END=$(ask "端口范围结束" "40999")
LISTEN_ADDR=$(ask "API 监听地址" ":7722")

echo ""
echo -e "${CYAN}SMTP 邮件通知${NC} (留空跳过，掉线告警将无法发送)"
SMTP_HOST=$(ask "  SMTP 服务器地址" "")
SMTP_PORT=""; SMTP_USER=""; SMTP_PASS=""; SMTP_FROM=""
if [[ -n "$SMTP_HOST" ]]; then
    SMTP_PORT=$(ask "  SMTP 端口" "587")
    SMTP_USER=$(ask "  SMTP 用户名" "")
    SMTP_PASS=$(ask "  SMTP 密码" "")
    SMTP_FROM=$(ask "  发件人地址" "$SMTP_USER")
fi

echo ""
echo -e "${CYAN}[3/5]${NC} 安装二进制和配置..."

# ─── 2. 安装文件 ───
sudo install -d -m 0755 "$INSTALL_BIN"
sudo install -d -m 0755 "$INSTALL_CONF"
sudo install -m 0755 "$SCRIPT_DIR/ssh-relay-api" "$INSTALL_BIN/ssh-relay-api"
ok "二进制已安装到 $INSTALL_BIN/ssh-relay-api"

# ─── 3. 生成环境文件 ───
ENV_FILE="$INSTALL_CONF/ssh-relay-api.env"
sudo tee "$ENV_FILE" > /dev/null <<ENVEOF
SSHR_LISTEN_ADDR=$LISTEN_ADDR
SSHR_DATABASE_URL=$DB_URL
SSHR_DOMAIN=$DOMAIN
SSHR_PORT_START=$PORT_START
SSHR_PORT_END=$PORT_END
SSHR_SMTP_HOST=$SMTP_HOST
SSHR_SMTP_PORT=$SMTP_PORT
SSHR_SMTP_USERNAME=$SMTP_USER
SSHR_SMTP_PASSWORD=$SMTP_PASS
SSHR_SMTP_FROM=$SMTP_FROM
ENVEOF
sudo chmod 600 "$ENV_FILE"
ok "环境文件已生成 $ENV_FILE"

# ─── 4. 初始化数据库 ───
echo ""
echo -e "${CYAN}[4/5]${NC} 初始化数据库..."
DB_SCHEMA="$SCRIPT_DIR/ssh-relay-schema.sql"
if [[ -f "$DB_SCHEMA" ]]; then
    if psql "$DB_URL" -f "$DB_SCHEMA" > /dev/null 2>&1; then
        ok "数据库表已创建"
    else
        warn "数据库初始化失败，请手动执行: psql $DB_URL -f $DB_SCHEMA"
    fi
else
    # 内联 schema
    psql "$DB_URL" -c "create table if not exists sshr_machines (id text primary key, name text not null unique, gpu_model text not null default '', status text not null default 'online', agent_version text not null default '0.1.0', metadata jsonb not null default '{}'::jsonb, last_seen_at timestamptz, created_at timestamptz not null default now(), updated_at timestamptz not null default now())" 2>/dev/null || warn "无法连接数据库，请检查 DATABASE_URL"
    psql "$DB_URL" -c "create table if not exists sshr_forwards (id text primary key, machine_id text not null references sshr_machines(id) on delete cascade, name text not null, target_host text not null default '127.0.0.1', target_port integer not null default 22, public_port integer not null unique, status text not null default 'active', metadata jsonb not null default '{}'::jsonb, created_at timestamptz not null default now(), updated_at timestamptz not null default now())" 2>/dev/null
    psql "$DB_URL" -c "create table if not exists sshr_heartbeat_events (id bigserial primary key, machine_id text not null references sshr_machines(id) on delete cascade, event_type text not null, message text not null default '', observed_at timestamptz not null default now())" 2>/dev/null
    psql "$DB_URL" -c "create index if not exists idx_sshr_heartbeat_machine on sshr_heartbeat_events(machine_id, observed_at desc)" 2>/dev/null
    psql "$DB_URL" -c "create table if not exists sshr_settings (key text primary key, value text not null, updated_at timestamptz not null default now())" 2>/dev/null
    ok "数据库表已创建（内联 schema）"
fi

# ─── 5. 创建 systemd 服务 ───
echo ""
echo -e "${CYAN}[5/5]${NC} 安装 systemd 服务..."

SERVICE_FILE="/etc/systemd/system/cloud-relay-ssh-relay.service"
sudo tee "$SERVICE_FILE" > /dev/null <<SVC
[Unit]
Description=Cloud Relay SSH Relay API
After=network.target postgresql.service

[Service]
Type=simple
User=root
WorkingDirectory=/opt/cloud-relay-platform
EnvironmentFile=$ENV_FILE
ExecStart=$INSTALL_BIN/ssh-relay-api
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
SVC

sudo systemctl daemon-reload
sudo systemctl enable cloud-relay-ssh-relay
sudo systemctl start cloud-relay-ssh-relay
ok "systemd 服务已安装并启动"

# ─── 6. Nginx 反代提示 ───
echo ""
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}安装完成！${NC}"
echo ""
echo -e "  ${YELLOW}服务状态:${NC}  systemctl status cloud-relay-ssh-relay"
echo -e "  ${YELLOW}查看日志:${NC}  journalctl -u cloud-relay-ssh-relay -f"
echo -e "  ${YELLOW}健康检查:${NC}  curl http://127.0.0.1${LISTEN_ADDR#:}/healthz"
echo ""
echo -e "${CYAN}接下来的步骤:${NC}"
echo "  1. 确保 Nginx/Caddy 已反代 $DOMAIN -> 127.0.0.1${LISTEN_ADDR#:}"
echo "     (包括 /api/ 和 /api/relay/reverse 的 WebSocket 升级)"
echo "  2. 证书管家已托管 $DOMAIN 的 SSL 证书"
echo "  3. 云防火墙已放行 TCP $PORT_START-$PORT_END"
echo "  4. 设置全局通知邮箱:"
echo "     curl -X PUT https://$DOMAIN/api/settings -H 'Content-Type: application/json' -d '{\"notifyEmails\":\"admin@example.com\"}'"
echo ""
echo -e "${CYAN}客户端机器上执行:${NC}"
echo "  bash client-install.sh   # 一键安装"
echo "  sshr register             # 注册本机"
echo "  sshr forward              # 查询端口并创建转发"
echo "  sshr daemon               # 启动心跳+反向隧道守护"
echo ""
echo -e "${CYAN}从任意机器 SSH 到已注册机器:${NC}"
echo "  ssh-to <机器名>            # 使用 bash 辅助脚本"
echo "  sshr ssh <机器名>          # 使用 CLI"
echo "  ssh -p <端口> user@$DOMAIN # 直接 SSH"
echo ""
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
