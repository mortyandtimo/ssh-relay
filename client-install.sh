#!/usr/bin/env bash
set -euo pipefail
#===============================================================================
# SSH Relay Client Installer
# 用于在需要转发 SSH 的 Linux 机器上一键注册并安装心跳守护
# 卸载: sshr uninstall
#===============================================================================

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
ok(){ echo -e "  ${GREEN}[OK]${NC} $*"; }
warn(){ echo -e "  ${YELLOW}[WARN]${NC} $*"; }
err(){ echo -e "  ${RED}[ERR]${NC} $*"; exit 1; }
ask(){ local def="$2"; [[ -n "$def" ]] && def=" [$def]"; read -rep "  $1$def: " val; echo "${val:-$2}"; }

# Handle piped execution (curl | bash) - BASH_SOURCE is unbound in that case
if [[ -n "${BASH_SOURCE[0]:-}" ]]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    SCRIPT_DIR="$(pwd)"
fi
SSHR_BIN="$SCRIPT_DIR/sshr"
SSHR_INSTALL="/usr/local/bin/sshr"
CONFIG_DIR="$HOME/.config/sshr"
CONFIG_FILE="$CONFIG_DIR/machine.json"
VERSION="0.2.0"

echo -e "${CYAN}╔══════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║       SSH Relay Client Installer         ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════╝${NC}"
echo ""

# ─── 0. 服务器地址 ───
echo -e "${CYAN}[0/4]${NC} 中继服务器地址"
if [[ -n "${SSHR_SERVER:-}" ]]; then
    ok "从环境变量读取: $SSHR_SERVER"
    SERVER="$SSHR_SERVER"
else
    SERVER=$(ask "服务器地址" "")
    [[ -z "$SERVER" ]] && err "服务器地址不能为空"
fi
export SSHR_SERVER="$SERVER"

# ─── 1. 安装二进制 ───
echo ""
echo -e "${CYAN}[1/4]${NC} 安装 sshr 客户端..."

if [[ -f "$SSHR_BIN" ]]; then
    sudo install -m 0755 "$SSHR_BIN" "$SSHR_INSTALL"
    ok "sshr 已安装到 $SSHR_INSTALL"
elif command -v sshr &>/dev/null; then
    ok "sshr 已存在: $(which sshr)"
else
    warn "未找到本地 sshr，正在从 GitHub 下载..."
    curl -fsSL "https://raw.githubusercontent.com/mortyandtimo/ssh-relay/main/sshr" -o /tmp/sshr || {
        warn "下载失败。请检查网络连接，或手动安装:"
        warn "  git clone https://github.com/mortyandtimo/ssh-relay.git"
        warn "  cd ssh-relay && go build -o sshr ./apps/ssh-relay-cli/cmd/sshr/"
        err "无法获取 sshr 二进制"
    }
    sudo install -m 0755 /tmp/sshr "$SSHR_INSTALL"
    ok "sshr 已下载并安装"
fi

# ─── 2. 安装 ssh-to ───
SSH_TO="$SCRIPT_DIR/ssh-to"
if [[ -f "$SSH_TO" ]]; then
    sudo install -m 0755 "$SSH_TO" /usr/local/bin/ssh-to
    ok "ssh-to 已安装"
fi

# ─── 3. 注册 ───
echo ""
echo -e "${CYAN}[3/4]${NC} 注册本机到 SSH Relay..."

CURRENT_NAME=""
if [[ -f "$CONFIG_FILE" ]]; then
    CURRENT_NAME=$(python3 -c "import json; print(json.load(open('$CONFIG_FILE')).get('name',''))" 2>/dev/null || echo "")
    if [[ -n "$CURRENT_NAME" ]]; then
        echo -e "  当前已注册为: ${GREEN}$CURRENT_NAME${NC}"
        REREG=$(ask "重新注册?" "n")
        if [[ "$REREG" != "y" && "$REREG" != "Y" ]]; then
            ok "保持现有注册"
        fi
    fi
fi

if [[ "$CURRENT_NAME" == "" ]] || [[ "${REREG:-n}" == "y" ]]; then
    SSHR_SERVER="$SERVER" sshr register || err "注册失败"
fi

# ─── 4. 安装守护（开机自启）──
echo ""
echo -e "${CYAN}[4/4]${NC} 安装开机自启守护..."

MACHINE_ID=$(python3 -c "import json; print(json.load(open('$CONFIG_FILE')).get('machineId',''))" 2>/dev/null || echo "")
SYSTEMD_USER_DIR="$HOME/.config/systemd/user"
mkdir -p "$SYSTEMD_USER_DIR"

cat > "$SYSTEMD_USER_DIR/sshrdaemon.service" <<SVC
[Unit]
Description=SSH Relay Heartbeat Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$SSHR_INSTALL daemon
Environment=SSHR_SERVER=$SERVER
Environment=SSHR_MACHINE_ID=$MACHINE_ID
Restart=always
RestartSec=10
StandardOutput=journal

[Install]
WantedBy=default.target
SVC

systemctl --user daemon-reload
systemctl --user enable sshrdaemon
systemctl --user start sshrdaemon 2>/dev/null || warn "请手动启动: systemctl --user start sshrdaemon"

# Enable lingering so user services start at boot
sudo loginctl enable-linger "$USER" 2>/dev/null || true
ok "守护已安装并设为开机自启"

# ─── 完成 ───
echo ""
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}安装完成！${NC}"
echo ""
echo -e "  ${YELLOW}查看状态:${NC}     sshr status"
echo -e "  ${YELLOW}创建转发:${NC}     sshr forward"
echo -e "  ${YELLOW}查看转发:${NC}     sshr list"
echo -e "  ${YELLOW}远程 SSH:${NC}     ssh-to <机器名>"
echo -e "  ${YELLOW}守护状态:${NC}     systemctl --user status sshrdaemon"
echo -e "  ${YELLOW}守护日志:${NC}     journalctl --user -u sshrdaemon -f"
echo -e "  ${YELLOW}卸载:${NC}         sshr uninstall"
echo ""
echo -e "${CYAN}服务器地址已保存到 ${CONFIG_FILE}，重启后依然生效。${NC}"
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
