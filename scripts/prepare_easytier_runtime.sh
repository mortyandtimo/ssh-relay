#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RUNTIME_DIR="$ROOT_DIR/deploy/windows/user-console/runtime"
TARGET_EXE="$RUNTIME_DIR/easytier-core.exe"
MARKER_FILE="$RUNTIME_DIR/.prepared-source.txt"
FORCE_PREPARE="${FORCE_EASYTIER_PREPARE:-0}"
EASYTIER_VERSION="${1:-${EASYTIER_VERSION:-}}"
EASYTIER_CORE_SOURCE="${EASYTIER_CORE_SOURCE:-}"
EASYTIER_DOWNLOAD_URL="${EASYTIER_DOWNLOAD_URL:-}"
LATEST_RELEASE_URL="https://github.com/EasyTier/EasyTier/releases/latest"

usage() {
  cat <<'EOF'
usage: scripts/prepare_easytier_runtime.sh [version]

Behavior:
  1. Reuse existing deploy/windows/user-console/runtime/easytier-core.exe by default.
  2. If missing, prepare the runtime from one of these sources:
     - EASYTIER_CORE_SOURCE=/path/to/easytier-windows-x86_64-vX.Y.Z.zip
     - EASYTIER_CORE_SOURCE=/path/to/easytier-core.exe
     - EASYTIER_CORE_SOURCE=/path/to/extracted/easytier/folder
     - EASYTIER_CORE_SOURCE=https://.../easytier-windows-x86_64-vX.Y.Z.zip
     - EASYTIER_DOWNLOAD_URL=https://.../easytier-windows-x86_64-vX.Y.Z.zip
     - EASYTIER_VERSION=vX.Y.Z
     - no args/env: auto-detect latest stable tag from the official GitHub release redirect

Optional env:
  FORCE_EASYTIER_PREPARE=1   overwrite existing runtime files
EOF
}

need_cmd() {
  local cmd="$1"
  local hint="$2"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing required command: $cmd ($hint)" >&2
    exit 1
  fi
}

is_url() {
  case "$1" in
    http://*|https://*) return 0 ;;
    *) return 1 ;;
  esac
}

resolve_latest_tag() {
  need_cmd curl "install curl first"
  curl --silent --show-error --location --head --output /dev/null --write-out '%{url_effective}' "$LATEST_RELEASE_URL" \
    | sed -n 's#.*/releases/tag/\([^/?#]*\).*#\1#p' \
    | head -n1
}

default_download_url() {
  local version="$1"
  printf 'https://github.com/EasyTier/EasyTier/releases/download/%s/easytier-windows-x86_64-%s.zip\n' "$version" "$version"
}

absolute_path() {
  local input_path="$1"
  (
    cd "$(dirname "$input_path")"
    printf '%s/%s\n' "$PWD" "$(basename "$input_path")"
  )
}

clean_runtime_payload() {
  rm -f \
    "$RUNTIME_DIR/easytier-core.exe" \
    "$RUNTIME_DIR/easytier-cli.exe" \
    "$RUNTIME_DIR/easytier-web.exe" \
    "$RUNTIME_DIR/easytier-web-embed.exe" \
    "$RUNTIME_DIR/Packet.dll" \
    "$RUNTIME_DIR/wintun.dll" \
    "$RUNTIME_DIR/.prepared-source.txt"
}

copy_from_directory() {
  local source_dir="$1"
  local copied=0
  local file
  for file in easytier-core.exe easytier-cli.exe easytier-web.exe easytier-web-embed.exe Packet.dll wintun.dll; do
    if [ -f "$source_dir/$file" ]; then
      cp "$source_dir/$file" "$RUNTIME_DIR/$file"
      copied=1
    fi
  done
  if [ "$copied" != "1" ]; then
    echo "no expected EasyTier runtime files found in directory: $source_dir" >&2
    exit 1
  fi
}

copy_from_exe() {
  local exe_path="$1"
  cp "$exe_path" "$TARGET_EXE"
  local sidecar_dir
  sidecar_dir="$(cd "$(dirname "$exe_path")" && pwd)"
  local file
  for file in Packet.dll wintun.dll easytier-cli.exe easytier-web.exe easytier-web-embed.exe; do
    if [ -f "$sidecar_dir/$file" ]; then
      cp "$sidecar_dir/$file" "$RUNTIME_DIR/$file"
    fi
  done
}

extract_zip() {
  local archive_path="$1"
  need_cmd unzip "install unzip first"
  local temp_dir extracted_exe extracted_dir
  temp_dir="$(mktemp -d)"
  unzip -oq "$archive_path" -d "$temp_dir"
  extracted_exe="$(find "$temp_dir" -type f \( -name 'easytier-core.exe' -o -name 'EASYTIER-CORE.EXE' \) | head -n1)"
  if [ -z "$extracted_exe" ]; then
    echo "no easytier-core.exe found in archive: $archive_path" >&2
    rm -rf "$temp_dir"
    exit 1
  fi
  extracted_dir="$(dirname "$extracted_exe")"
  copy_from_directory "$extracted_dir"
  rm -rf "$temp_dir"
}

prepare_from_path() {
  local source_path="$1"
  if [ ! -e "$source_path" ]; then
    echo "EASYTIER_CORE_SOURCE path not found: $source_path" >&2
    exit 1
  fi
  if [ -d "$source_path" ]; then
    copy_from_directory "$source_path"
    return
  fi
  case "$source_path" in
    *.zip|*.ZIP)
      extract_zip "$source_path"
      ;;
    *.exe|*.EXE)
      copy_from_exe "$source_path"
      ;;
    *)
      echo "unsupported EASYTIER_CORE_SOURCE path: $source_path" >&2
      exit 1
      ;;
  esac
}

prepare_from_url() {
  local url="$1"
  need_cmd curl "install curl first"
  local temp_dir temp_file
  temp_dir="$(mktemp -d)"
  trap 'rm -rf "$temp_dir"' EXIT
  temp_file="$temp_dir/easytier-download"
  echo "Downloading EasyTier runtime from: $url"
  curl --fail --silent --show-error --location "$url" --output "$temp_file"
  if printf '%s' "$url" | grep -E '\.exe([?#].*)?$' >/dev/null 2>&1; then
    mv "$temp_file" "$temp_dir/easytier-core.exe"
    copy_from_exe "$temp_dir/easytier-core.exe"
    return
  fi
  mv "$temp_file" "$temp_dir/easytier-runtime.zip"
  extract_zip "$temp_dir/easytier-runtime.zip"
}

ensure_target_ready() {
  if [ ! -f "$TARGET_EXE" ]; then
    echo "easytier-core.exe was not prepared successfully into $RUNTIME_DIR" >&2
    exit 1
  fi
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

mkdir -p "$RUNTIME_DIR"

if [ "$FORCE_PREPARE" != "1" ] && [ -f "$TARGET_EXE" ]; then
  echo "Reusing existing EasyTier runtime in $RUNTIME_DIR"
  exit 0
fi

clean_runtime_payload

if [ -n "$EASYTIER_CORE_SOURCE" ]; then
  if is_url "$EASYTIER_CORE_SOURCE"; then
    prepare_from_url "$EASYTIER_CORE_SOURCE"
    printf '%s\n' "$EASYTIER_CORE_SOURCE" >"$MARKER_FILE"
  else
    prepare_from_path "$EASYTIER_CORE_SOURCE"
    printf '%s\n' "$(absolute_path "$EASYTIER_CORE_SOURCE")" >"$MARKER_FILE"
  fi
  ensure_target_ready
  echo "Prepared EasyTier runtime into $RUNTIME_DIR"
  exit 0
fi

if [ -z "$EASYTIER_DOWNLOAD_URL" ]; then
  if [ -z "$EASYTIER_VERSION" ]; then
    EASYTIER_VERSION="$(resolve_latest_tag)"
  fi
  if [ -z "$EASYTIER_VERSION" ]; then
    echo "failed to resolve latest EasyTier release tag" >&2
    exit 1
  fi
  EASYTIER_DOWNLOAD_URL="$(default_download_url "$EASYTIER_VERSION")"
fi

prepare_from_url "$EASYTIER_DOWNLOAD_URL"
printf '%s\n' "$EASYTIER_DOWNLOAD_URL" >"$MARKER_FILE"
ensure_target_ready
echo "Prepared EasyTier runtime into $RUNTIME_DIR"
