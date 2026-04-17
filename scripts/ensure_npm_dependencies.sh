#!/usr/bin/env bash
set -euo pipefail

APP_DIR="${1:-}"

if [ -z "$APP_DIR" ]; then
  echo "usage: $0 <app-dir>" >&2
  exit 1
fi

if [ ! -d "$APP_DIR" ]; then
  echo "app directory not found: $APP_DIR" >&2
  exit 1
fi

PACKAGE_JSON="$APP_DIR/package.json"
LOCK_FILE="$APP_DIR/package-lock.json"
NODE_MODULES_DIR="$APP_DIR/node_modules"
STAMP_FILE="$NODE_MODULES_DIR/.package-lock.fingerprint"
FORCE_NPM_INSTALL="${FORCE_NPM_INSTALL:-0}"

if [ ! -f "$PACKAGE_JSON" ]; then
  echo "package.json not found: $PACKAGE_JSON" >&2
  exit 1
fi

detect_hash_cmd() {
  if command -v sha256sum >/dev/null 2>&1; then
    echo "sha256sum"
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    echo "shasum"
    return
  fi
  if command -v openssl >/dev/null 2>&1; then
    echo "openssl"
    return
  fi
  echo "missing checksum tool: install sha256sum, shasum, or openssl" >&2
  exit 1
}

HASH_CMD="$(detect_hash_cmd)"

fingerprint_files() {
  if [ "$HASH_CMD" = "sha256sum" ]; then
    sha256sum "$@" | sha256sum | awk '{print $1}'
    return
  fi
  if [ "$HASH_CMD" = "shasum" ]; then
    shasum -a 256 "$@" | shasum -a 256 | awk '{print $1}'
    return
  fi
  local temp_file
  temp_file="$(mktemp)"
  trap 'rm -f "$temp_file"' EXIT
  cat "$@" >"$temp_file"
  openssl dgst -sha256 -r "$temp_file" | awk '{print $1}'
}

run_install() {
  echo "Installing npm dependencies in $APP_DIR"
  pushd "$APP_DIR" >/dev/null
  if [ -f "$LOCK_FILE" ]; then
    npm ci --no-audit --fund false
  else
    npm install --no-audit --fund false
  fi
  popd >/dev/null
}

fingerprint_inputs=("$PACKAGE_JSON")
if [ -f "$LOCK_FILE" ]; then
  fingerprint_inputs+=("$LOCK_FILE")
fi
CURRENT_FINGERPRINT="$(fingerprint_files "${fingerprint_inputs[@]}")"

if [ "$FORCE_NPM_INSTALL" = "1" ]; then
  run_install
elif [ ! -d "$NODE_MODULES_DIR" ]; then
  run_install
elif [ ! -f "$STAMP_FILE" ]; then
  echo "Reusing existing npm dependencies in $APP_DIR (adopting pre-existing node_modules without fingerprint stamp)"
elif [ "$(cat "$STAMP_FILE")" != "$CURRENT_FINGERPRINT" ]; then
  run_install
else
  echo "Reusing existing npm dependencies in $APP_DIR"
fi

mkdir -p "$NODE_MODULES_DIR"
printf '%s\n' "$CURRENT_FINGERPRINT" >"$STAMP_FILE"
