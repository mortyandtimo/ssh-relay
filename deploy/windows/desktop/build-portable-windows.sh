#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../../.." && pwd)"
DIST_DIR="$ROOT_DIR/apps/desktop-console/dist"
OUT_DIR="$ROOT_DIR/outputs/windows-desktop-portable"
STAGE_DIR="$OUT_DIR/CloudRelayDesktopPortable"

rm -rf "$STAGE_DIR"
mkdir -p "$STAGE_DIR"
mkdir -p "$STAGE_DIR/logs"

pushd "$ROOT_DIR" >/dev/null
npm --prefix apps/desktop-console run build
GOOS=windows GOARCH=amd64 go build -o "$STAGE_DIR/desktop-launcher.exe" ./apps/desktop-console/cmd/desktop-launcher
cp -r "$DIST_DIR" "$STAGE_DIR/desktop-dist"
cp "$ROOT_DIR/deploy/bin/windows-amd64/client-agent.exe" "$STAGE_DIR/client-agent.exe"
cp "$ROOT_DIR/deploy/windows/desktop/desktop-config.json.example" "$STAGE_DIR/desktop-config.json"
cp "$ROOT_DIR/deploy/windows/desktop/README.txt" "$STAGE_DIR/README.txt"
popd >/dev/null

pushd "$OUT_DIR" >/dev/null
rm -f CloudRelayDesktopPortable.zip
zip -qr CloudRelayDesktopPortable.zip CloudRelayDesktopPortable
popd >/dev/null

echo "$OUT_DIR/CloudRelayDesktopPortable.zip"
