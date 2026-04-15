#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../../.." && pwd)"
DIST_DIR="$ROOT_DIR/apps/desktop-console/dist"
OUT_DIR="$ROOT_DIR/outputs/windows-desktop-portable"
STAGE_DIR="$OUT_DIR/CloudRelayDesktopPortable"
SOURCE_AGENT="$ROOT_DIR/deploy/bin/windows-amd64/client-agent.exe"
SOURCE_AGENT_DIR="$(dirname "$SOURCE_AGENT")"
SOURCE_AGENT_PKG="./apps/client-agent/cmd/client-agent"

rm -rf "$STAGE_DIR"
mkdir -p "$STAGE_DIR"
mkdir -p "$STAGE_DIR/logs"
mkdir -p "$SOURCE_AGENT_DIR"

pushd "$ROOT_DIR" >/dev/null
GOOS=windows GOARCH=amd64 go build -o "$SOURCE_AGENT" "$SOURCE_AGENT_PKG"
npm --prefix apps/desktop-console run build
GOOS=windows GOARCH=amd64 go build -o "$STAGE_DIR/desktop-launcher.exe" ./apps/desktop-console/cmd/desktop-launcher
cp -r "$DIST_DIR" "$STAGE_DIR/desktop-dist"
cp "$SOURCE_AGENT" "$STAGE_DIR/client-agent.exe"
cp "$ROOT_DIR/deploy/windows/desktop/desktop-config.json.example" "$STAGE_DIR/desktop-config.json"
cp "$ROOT_DIR/deploy/windows/desktop/README.txt" "$STAGE_DIR/README.txt"
popd >/dev/null

pushd "$OUT_DIR" >/dev/null
rm -f CloudRelayDesktopPortable.zip
zip -qr CloudRelayDesktopPortable.zip CloudRelayDesktopPortable
popd >/dev/null

echo "$OUT_DIR/CloudRelayDesktopPortable.zip"
