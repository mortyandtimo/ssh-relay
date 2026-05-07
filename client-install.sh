#!/usr/bin/env bash
set -euo pipefail
#===============================================================================
# SSH Relay Client Installer
# 用于在需要转发 SSH 的 Linux 机器上一键注册并安装心跳守护
#===============================================================================

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
ok(){ echo -e "  ${GREEN}[OK]${NC} $*"; }
warn(){ echo -e "  ${YELLOW}[WARN]${NC} $*"; }
err(){ echo -e "  ${RED}[ERR]${NC} $*"; exit 1; }
ask(){ local def="$2"; [[ -n "$def" ]] && def="[$def]"; read -rep "  $1 $def: " val; echo "${val:-$2}"; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SSHR_BIN="$SCRIPT_DIR/sshr"
SSHR_INSTALL="/usr/local/bin/sshr"
CONFIG_DIR="$HOME/.config/sshr"
CONFIG_FILE="$CONFIG_DIR/machine.json"
VERSION="0.1.0"

echo -e "${CYAN}╔══════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║       SSH Relay Client Installer         ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════╝${NC}"
echo ""

# ─── 1. 安装二进制 ───
echo -e "${CYAN}[1/4]${NC} 安装 sshr 客户端..."

if [[ -f "$SSHR_BIN" ]]; then
    sudo install -m 0755 "$SSHR_BIN" "$SSHR_INSTALL"
    ok "sshr 已安装到 $SSHR_INSTALL"
elif command -v sshr &>/dev/null; then
    ok "sshr 已存在: $(which sshr)"
else
    warn "未找到本地 sshr 二进制，尝试从服务器下载..."
    SERVER=$(ask "服务器地址" "https://tunnel.020309.top")
    curl -fsSL "$SERVER/sshr" -o /tmp/sshr || err "下载失败，请手动将 sshr 放到 $SCRIPT_DIR"
    sudo install -m 0755 /tmp/sshr "$SSHR_INSTALL"
    ok "sshr 已下载并安装"
fi

# ─── 2. 注册 ───
echo ""
echo -e "${CYAN}[2/4]${NC} 注册本机到 SSH Relay..."

CURRENT_NAME=""
if [[ -f "$CONFIG_FILE" ]]; then
    CURRENT_NAME=$(python3 -c "import json; print(json.load(open('$CONFIG_FILE')).get('name',''))" 2>/dev/null || echo "")
    [[ -n "$CURRENT_NAME" ]] && echo -e "  当前已注册为: ${GREEN}$CURRENT_NAME${NC}"
    REREG=$(ask "  重新注册? (y/N)" "n")
    [[ "$REREG" != "y" && "$REREG" != "Y" ]] && { ok "保持现有注册"; }
fi

if [[ "$CURRENT_NAME" == "" ]] || [[ "${REREG:-n}" == "y" ]]; then
    sshr register || err "注册失败"
fi

# ─── 2.5 安装 ssh-to 辅助脚本 ───
SSH_TO="$SCRIPT_DIR/ssh-to"
if [[ -f "$SSH_TO" ]]; then
    sudo install -m 0755 "$SSH_TO" /usr/local/bin/ssh-to
    ok "ssh-to 已安装到 /usr/local/bin/ssh-to"
fi

# ─── 3. 心跳守护 ───
echo ""
echo -e "${CYAN}[3/4]${NC} 设置心跳守护..."

MODE=$(ask "  守护方式 (systemd/cron/none)" "systemd")

case "$MODE" in
    systemd)
        SYSTEMD_USER_DIR="$HOME/.config/systemd/user"
        mkdir -p "$SYSTEMD_USER_DIR"

        MACHINE_ID=$(python3 -c "import json; print(json.load(open('$CONFIG_FILE')).get('machineId',''))" 2>/dev/null || echo "")
        SERVER=$(ask "  服务器地址" "")

        cat > "$SYSTEMD_USER_DIR/sshrdaemon.service" <<SVC
[Unit]
Description=SSH Relay Heartbeat Daemon
After=network.target

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
        systemctl --user start sshrdaemon 2>/dev/null || warn "请运行: systemctl --user start sshrdaemon"
        sudo loginctl enable-linger "$USER" 2>/dev/null || true
        ok "systemd 用户服务已安装 (sshrdaemon)"
        ;;
    cron)
        # Write a lightweight heartbeat script
        HEARTBEAT_SCRIPT="$CONFIG_DIR/heartbeat.sh"
        MACHINE_ID=$(python3 -c "import json; print(json.load(open('$CONFIG_FILE')).get('machineId',''))" 2>/dev/null || echo "")
        SERVER=${SSHR_SERVER:-https://tunnel.020309.top}

        cat > "$HEARTBEAT_SCRIPT" <<CRONEOF
#!/bin/bash
curl -fsS -X POST "$SERVER/api/heartbeat" -H 'Content-Type: application/json' -d '{"machineId":"$MACHINE_ID","agentVersion":"$VERSION","metadata":{}}' > /dev/null 2>&1
CRONEOF
        chmod +x "$HEARTBEAT_SCRIPT"

        CRON_JOB="*/1 * * * * $HEARTBEAT_SCRIPT"
        (crontab -l 2>/dev/null || true) | grep -v "sshr" | { cat; echo "$CRON_JOB"; } | crontab -
        ok "cron 任务已添加（每分钟心跳一次）"
        ;;
    *)
        warn "跳过了守护设置，请手动运行: sshr daemon"
        ;;
esac

# ─── 4. 完成 ───
echo ""
echo -e "${CYAN}[4/4]${NC} 完成"
echo ""
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}客户端安装完成！${NC}"
echo ""
echo -e "  ${YELLOW}查看状态:${NC}  sshr status"
echo -e "  ${YELLOW}创建转发:${NC}  sshr forward"
echo -e "  ${YELLOW}查看转发:${NC}  sshr list"
echo -e "  ${YELLOW}SSH 到机器:${NC} sshr ssh <机器名>"
echo -e "  ${YELLOW}快捷方式:${NC}   ssh-to <机器名>"
echo -e "  ${YELLOW}删除转发:${NC}  sshr delete"
echo -e "  ${YELLOW}守护状态:${NC}  systemctl --user status sshrdaemon"
echo -e "  ${YELLOW}守护日志:${NC}  journalctl --user -u sshrdaemon -f"
echo ""
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
