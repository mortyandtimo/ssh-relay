#!/usr/bin/env bash
set -euo pipefail

UNIT="cloud-relay-server-api.service"
QUIET=0

usage() {
  cat <<'EOF'
用法:
  cloud-relay-server-api-autostart enable [--no-start] [--quiet]
  cloud-relay-server-api-autostart disable [--quiet]
  cloud-relay-server-api-autostart status [--quiet]
  cloud-relay-server-api-autostart verify [--quiet]

说明:
  - `enable` 会注册 server-api 开机自启，默认同时立即启动。
  - `disable` 会取消开机自启并停止当前服务。
  - `status` 输出当前 enabled / active 状态。
  - `verify` 会校验 unit 是否已注册且正在运行。
  - systemd 本身就是后台静默启动；这里的 `--quiet` 仅抑制脚本成功输出。
EOF
}

info() {
  if [[ $QUIET -eq 0 ]]; then
    echo "$@"
  fi
}

fail() {
  echo "$@" >&2
  exit 1
}

run_systemctl() {
  if [[ ${EUID:-$(id -u)} -eq 0 ]]; then
    systemctl "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo systemctl "$@"
  else
    fail "需要 root 或 sudo 权限来执行 systemctl $*"
  fi
}

unit_enabled_state() {
  systemctl is-enabled "$UNIT" 2>/dev/null || echo disabled
}

unit_active_state() {
  systemctl is-active "$UNIT" 2>/dev/null || echo inactive
}

verify_enabled() {
  local enabled active
  enabled="$(unit_enabled_state)"
  active="$(unit_active_state)"
  [[ "$enabled" == "enabled" ]] || fail "校验失败：$UNIT 当前不是 enabled，而是 $enabled"
  [[ "$active" == "active" ]] || fail "校验失败：$UNIT 当前不是 active，而是 $active"
}

verify_disabled() {
  local enabled active
  enabled="$(unit_enabled_state)"
  active="$(unit_active_state)"
  [[ "$enabled" == "disabled" ]] || fail "校验失败：$UNIT 当前仍是 $enabled"
  [[ "$active" != "active" ]] || fail "校验失败：$UNIT 仍在运行"
}

ensure_unit_exists() {
  systemctl cat "$UNIT" >/dev/null 2>&1 || fail "未找到 $UNIT，请先安装 systemd unit 文件"
}

action="${1:-status}"
shift || true
start_now=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    --quiet)
      QUIET=1
      ;;
    --no-start)
      start_now=0
      ;;
    -h|--help|help)
      usage
      exit 0
      ;;
    *)
      fail "未知参数：$1"
      ;;
  esac
  shift
done

case "$action" in
  enable)
    ensure_unit_exists
    run_systemctl daemon-reload
    if [[ $start_now -eq 1 ]]; then
      run_systemctl enable --now "$UNIT"
      verify_enabled
      info "$UNIT 已注册为开机自启并已静默启动。"
    else
      run_systemctl enable "$UNIT"
      [[ "$(unit_enabled_state)" == "enabled" ]] || fail "校验失败：$UNIT 未成功注册为 enabled"
      info "$UNIT 已注册为开机自启，当前未立即启动。"
    fi
    ;;
  disable)
    ensure_unit_exists
    run_systemctl disable --now "$UNIT"
    verify_disabled
    info "$UNIT 已取消开机自启并停止。"
    ;;
  status)
    ensure_unit_exists
    info "$UNIT"
    info "  enabled: $(unit_enabled_state)"
    info "  active:  $(unit_active_state)"
    ;;
  verify)
    ensure_unit_exists
    verify_enabled
    info "$UNIT 开机自启与运行状态校验通过。"
    ;;
  help|-h|--help)
    usage
    ;;
  *)
    usage
    fail "未知动作：$action"
    ;;
esac
