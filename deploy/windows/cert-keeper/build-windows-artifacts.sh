#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../../.." && pwd)"
APP_DIR="$ROOT_DIR/apps/cert-keeper-desktop"
OUT_DIR="$ROOT_DIR/outputs/windows-cert-keeper"
FINAL_DIR="$OUT_DIR/final"
PORTABLE_DIR="$OUT_DIR/CertKeeperPortable"
PAYLOAD_DIR="$OUT_DIR/CertKeeperPayload"
INSTALLER_SCRIPT="$ROOT_DIR/deploy/windows/cert-keeper/installer.nsi"
INSTALLER_HELPER="$ROOT_DIR/scripts/build_windows_nsis_installer.sh"
ICON_FILE="$APP_DIR/src-tauri/icons/icon.ico"
README_FILE="$ROOT_DIR/deploy/windows/cert-keeper/README.txt"
APP_VERSION="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$APP_DIR/package.json")"
TARGET="${TAURI_TARGET:-x86_64-pc-windows-gnu}"
TARGET_EXE="$APP_DIR/src-tauri/target/$TARGET/release/cert-keeper-desktop.exe"
WEBVIEW2_LOADER="$APP_DIR/src-tauri/target/$TARGET/release/WebView2Loader.dll"

mkdir -p "$OUT_DIR" "$FINAL_DIR"
rm -rf "$PORTABLE_DIR" "$PAYLOAD_DIR"
rm -f "$FINAL_DIR/CertKeeperSetup-x64.exe"
mkdir -p "$PORTABLE_DIR/logs" "$PAYLOAD_DIR"

pushd "$APP_DIR" >/dev/null
npm install
npm run build
npm run tauri -- build --target "$TARGET" --no-bundle
popd >/dev/null

cp "$TARGET_EXE" "$PORTABLE_DIR/CertKeeper.exe"
cp "$TARGET_EXE" "$PAYLOAD_DIR/CertKeeper.exe"
if [ -f "$WEBVIEW2_LOADER" ]; then
  cp "$WEBVIEW2_LOADER" "$PORTABLE_DIR/WebView2Loader.dll"
  cp "$WEBVIEW2_LOADER" "$PAYLOAD_DIR/WebView2Loader.dll"
fi
cp "$README_FILE" "$PAYLOAD_DIR/README.txt"

pushd "$OUT_DIR" >/dev/null
rm -f CertKeeper-x64-portable.zip
zip -qr CertKeeper-x64-portable.zip CertKeeperPortable
popd >/dev/null

cp "$OUT_DIR/CertKeeper-x64-portable.zip" "$FINAL_DIR/CertKeeper-x64-portable.zip"
"$INSTALLER_HELPER" \
  "CertKeeper" \
  "$PAYLOAD_DIR" \
  "$INSTALLER_SCRIPT" \
  "$ICON_FILE" \
  "$APP_VERSION" \
  "$FINAL_DIR" \
  "CertKeeperSetup-x64.exe"

echo "$FINAL_DIR"
