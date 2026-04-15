#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
APP_DIR="$ROOT_DIR/apps/desktop-console"
OUT_DIR="$ROOT_DIR/outputs/windows-publisher"
FINAL_DIR="$OUT_DIR/final"
PORTABLE_DIR="$OUT_DIR/CloudRelayPublisherPortable"
PAYLOAD_DIR="$OUT_DIR/CloudRelayPublisherPayload"
INSTALLER_SCRIPT="$ROOT_DIR/deploy/windows/publisher/installer.nsi"
INSTALLER_HELPER="$ROOT_DIR/scripts/build_windows_nsis_installer.sh"
ICON_FILE="$APP_DIR/src-tauri/icons/icon.ico"
APP_VERSION="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$APP_DIR/package.json")"
TARGET_EXE_GNU="$APP_DIR/src-tauri/target/x86_64-pc-windows-gnu/release/cloud-relay-publisher.exe"
TARGET_EXE_MSVC="$APP_DIR/src-tauri/target/x86_64-pc-windows-msvc/release/cloud-relay-publisher.exe"
SOURCE_AGENT="$ROOT_DIR/deploy/bin/windows-amd64/client-agent.exe"
SOURCE_AGENT_DIR="$(dirname "$SOURCE_AGENT")"
SOURCE_AGENT_PKG="./apps/client-agent/cmd/client-agent"
BUNDLED_AGENT="$APP_DIR/src-tauri/runtime/client-agent.exe"

if [ -f "$TARGET_EXE_GNU" ]; then
  TARGET_EXE="$TARGET_EXE_GNU"
elif [ -f "$TARGET_EXE_MSVC" ]; then
  TARGET_EXE="$TARGET_EXE_MSVC"
else
  echo "cloud-relay-publisher.exe not found under GNU or MSVC release targets" >&2
  exit 1
fi

mkdir -p "$OUT_DIR" "$FINAL_DIR" "$SOURCE_AGENT_DIR"
rm -rf "$PORTABLE_DIR" "$PAYLOAD_DIR"
rm -f "$FINAL_DIR/CloudRelayPublisherSetup-x64.exe"
mkdir -p "$PORTABLE_DIR/runtime" "$PORTABLE_DIR/logs" "$PAYLOAD_DIR/runtime"

pushd "$ROOT_DIR" >/dev/null
GOOS=windows GOARCH=amd64 go build -o "$SOURCE_AGENT" "$SOURCE_AGENT_PKG"
popd >/dev/null

cp "$SOURCE_AGENT" "$BUNDLED_AGENT"
cp "$TARGET_EXE" "$PORTABLE_DIR/CloudRelayPublisher.exe"
cp "$TARGET_EXE" "$PAYLOAD_DIR/CloudRelayPublisher.exe"

WEBVIEW2_LOADER="$(dirname "$TARGET_EXE")/WebView2Loader.dll"
if [ -f "$WEBVIEW2_LOADER" ]; then
  cp "$WEBVIEW2_LOADER" "$PORTABLE_DIR/WebView2Loader.dll"
  cp "$WEBVIEW2_LOADER" "$PAYLOAD_DIR/WebView2Loader.dll"
fi

cp "$SOURCE_AGENT" "$PORTABLE_DIR/runtime/client-agent.exe"
cp "$SOURCE_AGENT" "$PAYLOAD_DIR/runtime/client-agent.exe"
cp "$ROOT_DIR/deploy/windows/desktop/desktop-config.json.example" "$PORTABLE_DIR/desktop-config.json"
cp "$ROOT_DIR/deploy/windows/desktop/desktop-config.json.example" "$PAYLOAD_DIR/desktop-config.json"
cp "$ROOT_DIR/deploy/windows/desktop/README.txt" "$PORTABLE_DIR/README.txt"
cp "$ROOT_DIR/deploy/windows/publisher/README.txt" "$PAYLOAD_DIR/README.txt"

pushd "$OUT_DIR" >/dev/null
rm -f CloudRelayPublisher-x64-portable.zip
zip -qr CloudRelayPublisher-x64-portable.zip CloudRelayPublisherPortable
popd >/dev/null

cp "$TARGET_EXE" "$FINAL_DIR/CloudRelayPublisher.exe"
cp "$OUT_DIR/CloudRelayPublisher-x64-portable.zip" "$FINAL_DIR/CloudRelayPublisher-x64-portable.zip"
"$INSTALLER_HELPER" \
  "Cloud Relay Publisher" \
  "$PAYLOAD_DIR" \
  "$INSTALLER_SCRIPT" \
  "$ICON_FILE" \
  "$APP_VERSION" \
  "$FINAL_DIR" \
  "CloudRelayPublisherSetup-x64.exe"

echo "$FINAL_DIR"
