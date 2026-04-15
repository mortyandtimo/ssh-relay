#!/usr/bin/env bash
# 驻阡陌 (Cloud Relay Platform) — 卸载脚本
# 用法: sudo bash uninstall.sh
set -euo pipefail

BOLD='\033[1m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
NC='\033[0m'

info()  { echo -e "${GREEN}[✓]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[✗]${NC} $*"; exit 1; }

[[ $EUID -eq 0 ]] || error "请使用 root 权限运行 (sudo bash uninstall.sh)"

INSTALL_DIR="/opt/cloud-relay"
CONFIG_DIR="/etc/cloud-relay"
LOG_DIR="/var/log/cloud-relay"
CLI="/usr/local/bin/cloud-relay"
SERVICES=(server-api relay-tcp relay-udp relay-http relay-https)

echo -e "${BOLD}═══════════════════════════════════════${NC}"
echo -e "${BOLD}   驻阡陌 — 卸载向导${NC}"
echo -e "${BOLD}═══════════════════════════════════════${NC}"
echo ""
echo "此操作将:"
echo "  1. 停止并禁用所有 cloud-relay 服务"
echo "  2. 删除 systemd 服务文件"
echo "  3. 删除安装目录 ($INSTALL_DIR)"
echo "  4. 删除配置目录 ($CONFIG_DIR)"
echo "  5. 删除日志目录 ($LOG_DIR)"
echo "  6. 删除管理命令 ($CLI)"
echo ""
echo -e "${RED}PostgreSQL 数据库不会被自动删除。${NC}"
echo "  如需删除数据库，请手动执行:"
echo "  sudo -u postgres psql -c 'DROP DATABASE cloudrelay;'"
echo "  sudo -u postgres psql -c 'DROP USER cloudrelay;'"
echo ""

echo -n "确认卸载? 输入 YES 继续: "
read -r CONFIRM
[[ "$CONFIRM" == "YES" ]] || { echo "已取消"; exit 0; }

# 停止服务
warn "停止服务..."
for SVC in "${SERVICES[@]}"; do
  systemctl stop "cloud-relay-${SVC}" 2>/dev/null || true
  systemctl disable "cloud-relay-${SVC}" 2>/dev/null || true
done

# 删除 systemd 文件
warn "删除 systemd 服务文件..."
for SVC in "${SERVICES[@]}"; do
  rm -f "/etc/systemd/system/cloud-relay-${SVC}.service"
done
systemctl daemon-reload

# 删除安装目录
warn "删除安装目录..."
rm -rf "$INSTALL_DIR"

# 删除配置
warn "删除配置目录..."
rm -rf "$CONFIG_DIR"

# 删除日志
warn "删除日志目录..."
rm -rf "$LOG_DIR"

# 删除 CLI
warn "删除管理命令..."
rm -f "$CLI"

info "卸载完成"
echo ""
echo "如需清除数据库，执行:"
echo "  sudo -u postgres psql -c 'DROP DATABASE cloudrelay;'"
echo "  sudo -u postgres psql -c 'DROP USER cloudrelay;'"
