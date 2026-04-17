#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SOURCE_FILE="$ROOT_DIR/deploy/manage-status/index.html"
TARGET_FILE="${1:-/var/www/personal/manage-status/index.html}"

if [ ! -f "$SOURCE_FILE" ]; then
  echo "source file not found: $SOURCE_FILE" >&2
  exit 1
fi

install -m 0644 "$SOURCE_FILE" "$TARGET_FILE"
echo "deployed $SOURCE_FILE -> $TARGET_FILE"
