#!/usr/bin/env bash
# 驻阡陌 命令行管理工具
set -euo pipefail

SERVICES=(server-api relay-tcp relay-udp relay-web relay-http relay-https)
ALL_UNITS() { for s in "${SERVICES[@]}"; do echo "cloud-relay-${s}"; done; }
verify_enabled() {
  local failed=0
  for u in $(ALL_UNITS); do
    if [[ "$(systemctl is-enabled "$u" 2>/dev/null || echo disabled)" != "enabled" ]]; then
      echo "校验失败: $u 未成功注册开机自启" >&2
      failed=1
    fi
  done
  [[ $failed -eq 0 ]]
}

verify_disabled() {
  local failed=0
  for u in $(ALL_UNITS); do
    if [[ "$(systemctl is-enabled "$u" 2>/dev/null || echo disabled)" != "disabled" ]]; then
      echo "校验失败: $u 仍然处于已启用状态" >&2
      failed=1
    fi
  done
  [[ $failed -eq 0 ]]
}

case "${1:-help}" in
  start)
    systemctl start $(ALL_UNITS)
    echo "已启动所有服务"
    ;;
  stop)
    systemctl stop $(ALL_UNITS)
    echo "已停止所有服务"
    ;;
  restart)
    systemctl restart $(ALL_UNITS)
    echo "已重启所有服务"
    ;;
  status)
    for u in $(ALL_UNITS); do
      printf "%-30s active=%-10s enabled=%s\n" "$u" "$(systemctl is-active "$u" 2>/dev/null || echo inactive)" "$(systemctl is-enabled "$u" 2>/dev/null || echo disabled)"
    done
    ;;
  log|logs)
    local SVC="${2:-server-api}"
    journalctl -u "cloud-relay-${SVC}" -n 50 --no-pager -f
    ;;
  enable)
    systemctl enable $(ALL_UNITS)
    verify_enabled || exit 1
    echo "已设置开机自启并完成校验"
    ;;
  disable)
    systemctl disable $(ALL_UNITS)
    verify_disabled || exit 1
    echo "已取消开机自启并完成校验"
    ;;
  config)
    ${EDITOR:-vi} /etc/cloud-relay/cloud-relay.env
    echo "配置已修改，执行 cloud-relay restart 使其生效"
    ;;
  ports)
    if [[ ! -f /etc/cloud-relay/cloud-relay.env ]]; then
      echo "配置文件不存在"; exit 1
    fi
    echo "驻阡陌 服务端口"
    echo ""
    grep -E '^(SERVER_API_ADDR|RELAY_TCP_ADDR|RELAY_UDP_ADDR|RELAY_HTTP_ADDR)=' /etc/cloud-relay/cloud-relay.env | while IFS='=' read -r key val; do
      case "$key" in
        SERVER_API_ADDR)  label="server-api";;
        RELAY_TCP_ADDR)   label="relay-tcp  ";;
        RELAY_UDP_ADDR)   label="relay-udp  ";;
        RELAY_HTTP_ADDR)  label="relay-http ";;
        *)                label="$key       ";;
      esac
      port="${val#:}"
      printf "  %s  %s\n" "$label" "$port"
    done
    ;;
  help|*)
    echo "驻阡陌 命令行管理工具"
    echo ""
    echo "用法: cloud-relay <命令>"
    echo ""
    echo "  start      启动所有服务"
    echo "  stop       停止所有服务"
    echo "  restart    重启所有服务"
    echo "  status     查看服务状态"
    echo "  ports      查看服务端口"
    echo "  log [服务] 查看日志 (默认 server-api, ctrl+c 退出)"
    echo "  enable     设置开机自启"
    echo "  disable    取消开机自启"
    echo "  config     编辑配置文件"
    echo "  help       显示帮助"
    echo ""
    echo "单服务操作: systemctl restart cloud-relay-server-api"
    echo "server-api 自启: cloud-relay-server-api-autostart enable|disable|status|verify"
    ;;
esac
