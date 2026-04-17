#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../../.." && pwd)"
APP_DIR="$ROOT_DIR/apps/user-console"
OUT_DIR="$ROOT_DIR/outputs/windows-user"
FINAL_DIR="$OUT_DIR/final"
PORTABLE_DIR="$OUT_DIR/CloudRelayUserPortable"
PAYLOAD_DIR="$OUT_DIR/CloudRelayUserPayload"
INSTALLER_SCRIPT="$ROOT_DIR/deploy/windows/user-console/installer.nsi"
INSTALLER_HELPER="$ROOT_DIR/scripts/build_windows_nsis_installer.sh"
ENSURE_NPM_DEPS_SCRIPT="$ROOT_DIR/scripts/ensure_npm_dependencies.sh"
PREPARE_EASYTIER_SCRIPT="$ROOT_DIR/scripts/prepare_easytier_runtime.sh"
OPTIONAL_RUNTIME_DIR="$ROOT_DIR/deploy/windows/user-console/runtime"
ICON_FILE="$APP_DIR/src-tauri/icons/icon.ico"
README_FILE="$ROOT_DIR/deploy/windows/user-console/README.txt"
APP_VERSION="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$APP_DIR/package.json")"
TARGET="${TAURI_TARGET:-x86_64-pc-windows-gnu}"
TARGET_EXE="$APP_DIR/src-tauri/target/$TARGET/release/cloud-relay-user.exe"
WEBVIEW2_LOADER="$APP_DIR/src-tauri/target/$TARGET/release/WebView2Loader.dll"
SKIP_EASYTIER_PREPARE="${SKIP_EASYTIER_PREPARE:-0}"

mkdir -p "$OUT_DIR" "$FINAL_DIR"
rm -rf "$PORTABLE_DIR" "$PAYLOAD_DIR"
rm -f "$FINAL_DIR/CloudRelayUserSetup-x64.exe"
mkdir -p "$PORTABLE_DIR/logs" "$PAYLOAD_DIR"

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
npm run tauri -- build --target "$TARGET" --no-bundle
popd >/dev/null

cp "$TARGET_EXE" "$PORTABLE_DIR/CloudRelayUser.exe"
cp "$TARGET_EXE" "$PAYLOAD_DIR/CloudRelayUser.exe"
if [ -f "$WEBVIEW2_LOADER" ]; then
  cp "$WEBVIEW2_LOADER" "$PORTABLE_DIR/WebView2Loader.dll"
  cp "$WEBVIEW2_LOADER" "$PAYLOAD_DIR/WebView2Loader.dll"
fi
cp "$README_FILE" "$PAYLOAD_DIR/README.txt"
if [ -d "$OPTIONAL_RUNTIME_DIR" ]; then
  mkdir -p "$PORTABLE_DIR/runtime" "$PAYLOAD_DIR/runtime"
  cp -R "$OPTIONAL_RUNTIME_DIR"/. "$PORTABLE_DIR/runtime/"
  cp -R "$OPTIONAL_RUNTIME_DIR"/. "$PAYLOAD_DIR/runtime/"
fi

pushd "$OUT_DIR" >/dev/null
rm -f CloudRelayUser-x64-portable.zip
zip -qr CloudRelayUser-x64-portable.zip CloudRelayUserPortable
popd >/dev/null

cp "$OUT_DIR/CloudRelayUser-x64-portable.zip" "$FINAL_DIR/CloudRelayUser-x64-portable.zip"
"$INSTALLER_HELPER" \
  "CloudRelayUser" \
  "$PAYLOAD_DIR" \
  "$INSTALLER_SCRIPT" \
  "$ICON_FILE" \
  "$APP_VERSION" \
  "$FINAL_DIR" \
  "CloudRelayUserSetup-x64.exe"

echo "$FINAL_DIR"
