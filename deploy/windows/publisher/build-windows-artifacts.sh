#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../../.." && pwd)"
APP_DIR="$ROOT_DIR/apps/desktop-console"
REPACK_SCRIPT="$ROOT_DIR/scripts/repack_windows_publisher.sh"
ENSURE_NPM_DEPS_SCRIPT="$ROOT_DIR/scripts/ensure_npm_dependencies.sh"
PREPARE_EASYTIER_SCRIPT="$ROOT_DIR/scripts/prepare_easytier_runtime.sh"
TARGET="${TAURI_TARGET:-x86_64-pc-windows-gnu}"
SKIP_EASYTIER_PREPARE="${SKIP_EASYTIER_PREPARE:-0}"

if [ "$SKIP_EASYTIER_PREPARE" != "1" ]; then
  if [ -n "${EASYTIER_VERSION:-}" ]; then
    "$PREPARE_EASYTIER_SCRIPT" "$EASYTIER_VERSION"
  else
    "$PREPARE_EASYTIER_SCRIPT"
  fi
else
  echo "Skipping EasyTier runtime preparation because SKIP_EASYTIER_PREPARE=1"
fi

pushd "$APP_DIR" >/dev/null
"$ENSURE_NPM_DEPS_SCRIPT" "$APP_DIR"
npm run build
npm run tauri:build -- --target "$TARGET" --no-bundle
popd >/dev/null

"$REPACK_SCRIPT"
