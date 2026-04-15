#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 7 ]; then
  echo "usage: $0 <app-name> <payload-dir> <installer-script> <icon-file> <app-version> <out-dir> <out-exe-name>" >&2
  exit 1
fi

APP_NAME="$1"
PAYLOAD_DIR="$2"
INSTALLER_SCRIPT="$3"
ICON_FILE="$4"
APP_VERSION="$5"
OUT_DIR="$6"
OUT_EXE_NAME="$7"
OUT_FILE="$OUT_DIR/$OUT_EXE_NAME"
MAKENSIS_BIN="${MAKENSIS_BIN:-}"

if [ -n "$MAKENSIS_BIN" ]; then
  if [ ! -x "$MAKENSIS_BIN" ]; then
    echo "MAKENSIS_BIN is set but not executable: $MAKENSIS_BIN" >&2
    exit 1
  fi
else
  MAKENSIS_BIN="$(command -v makensis || true)"
fi

if [ -z "$MAKENSIS_BIN" ]; then
  echo "makensis not found for $APP_NAME; set MAKENSIS_BIN=/path/to/makensis and rerun packaging" >&2
  exit 0
fi

mkdir -p "$OUT_DIR"
rm -f "$OUT_FILE"
"$MAKENSIS_BIN" \
  -V2 \
  -DPAYLOAD_DIR="$PAYLOAD_DIR" \
  -DICON_FILE="$ICON_FILE" \
  -DAPP_VERSION="$APP_VERSION" \
  -DOUT_FILE="$OUT_FILE" \
  "$INSTALLER_SCRIPT"

if [ ! -f "$OUT_FILE" ]; then
  echo "NSIS did not produce expected installer: $OUT_FILE" >&2
  exit 1
fi

echo "$OUT_FILE"
