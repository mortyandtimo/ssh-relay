#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../../.." && pwd)"
APP_DIR="$ROOT_DIR/apps/desktop-console"
REPACK_SCRIPT="$ROOT_DIR/scripts/repack_windows_publisher.sh"
TARGET="${TAURI_TARGET:-x86_64-pc-windows-gnu}"

pushd "$APP_DIR" >/dev/null
npm install
npm run build
npm run tauri:build -- --target "$TARGET" --no-bundle
popd >/dev/null

"$REPACK_SCRIPT"
