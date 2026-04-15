#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:-all}"

remove_path() {
  local path="$1"
  if [ -e "$path" ]; then
    echo "[clean] removing $path"
    rm -rf "$path"
  else
    echo "[clean] skip missing $path"
  fi
}

clean_publisher() {
  remove_path "$ROOT_DIR/apps/desktop-console/node_modules"
  remove_path "$ROOT_DIR/apps/desktop-console/src-tauri/target"
  remove_path "$ROOT_DIR/outputs/windows-publisher"
}

clean_cert_keeper() {
  remove_path "$ROOT_DIR/apps/cert-keeper-desktop/node_modules"
  remove_path "$ROOT_DIR/apps/cert-keeper-desktop/src-tauri/target"
  remove_path "$ROOT_DIR/outputs/windows-cert-keeper"
}

case "$TARGET" in
  publisher)
    clean_publisher
    ;;
  cert-keeper)
    clean_cert_keeper
    ;;
  all)
    clean_publisher
    clean_cert_keeper
    ;;
  *)
    echo "usage: $0 [publisher|cert-keeper|all]" >&2
    exit 1
    ;;
esac
