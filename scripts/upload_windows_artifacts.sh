#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:-all}"
SERVER_URL="${SERVER_URL:-}"
COOKIE_FILE="${COOKIE_FILE:-}"
VERSION="${VERSION:-}"

usage() {
  cat <<'EOF'
usage: scripts/upload_windows_artifacts.sh [publisher|cert-keeper|user|all]

Required env:
  SERVER_URL   e.g. https://manage.020309.top
  COOKIE_FILE  curl cookie jar captured from an admin login session

Optional env:
  VERSION      release version path used by /downloads/releases/<product>/<version>/
               If omitted, a traceable default is derived from package version + git commit.
EOF
}

detect_default_version() {
  local app_version git_date git_sha
  app_version="$(sed -n 's/.*"version": "\([^"]*\)".*/\1/p' "$ROOT_DIR/apps/desktop-console/package.json" | head -n1)"
  git_date="$(git -C "$ROOT_DIR" log -1 --date=format:%Y%m%d-%H%M --format=%cd 2>/dev/null || date +%Y%m%d-%H%M)"
  git_sha="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo nogit)"
  if [ -n "$app_version" ]; then
    echo "${app_version}-${git_date}-${git_sha}"
    return
  fi
  echo "${git_date}-${git_sha}"
}

sha256_file() {
  local file_path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file_path" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file_path" | awk '{print $1}'
    return
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 -r "$file_path" | awk '{print $1}'
    return
  fi
  echo "missing sha256 tool: install sha256sum, shasum, or openssl" >&2
  exit 1
}

upload_one() {
  local product="$1"
  local channel="$2"
  local file_path="$3"
  if [ ! -f "$file_path" ]; then
    echo "skip missing artifact: $file_path" >&2
    return 0
  fi
  local file_hash
  file_hash="$(sha256_file "$file_path")"
  echo "Uploading $product/$channel -> $file_path"
  curl --fail --silent --show-error --location \
    -b "$COOKIE_FILE" \
    -F "product=$product" \
    -F "channel=$channel" \
    -F "version=$VERSION" \
    -F "sha256=$file_hash" \
    -F "file=@$file_path" \
    "$SERVER_URL/api/admin/release-artifacts/upload"
  echo
}

upload_publisher() {
  upload_one publisher setup "$ROOT_DIR/outputs/windows-publisher/final/CloudRelayPublisherSetup-x64.exe"
  upload_one publisher portable "$ROOT_DIR/outputs/windows-publisher/final/CloudRelayPublisher-x64-portable.zip"
}

upload_cert_keeper() {
  upload_one cert-keeper setup "$ROOT_DIR/outputs/windows-cert-keeper/final/CertKeeperSetup-x64.exe"
  upload_one cert-keeper portable "$ROOT_DIR/outputs/windows-cert-keeper/final/CertKeeper-x64-portable.zip"
}

upload_user() {
  upload_one user setup "$ROOT_DIR/outputs/windows-user/final/CloudRelayUserSetup-x64.exe"
  upload_one user portable "$ROOT_DIR/outputs/windows-user/final/CloudRelayUser-x64-portable.zip"
}

if [ "$TARGET" = "-h" ] || [ "$TARGET" = "--help" ]; then
  usage
  exit 0
fi

if [ -z "$SERVER_URL" ]; then
  echo "SERVER_URL is required, e.g. SERVER_URL=https://manage.020309.top" >&2
  exit 1
fi
if [ -z "$COOKIE_FILE" ]; then
  echo "COOKIE_FILE is required and should point to a curl cookie jar from an admin login session" >&2
  exit 1
fi
if [ ! -f "$COOKIE_FILE" ]; then
  echo "COOKIE_FILE does not exist: $COOKIE_FILE" >&2
  exit 1
fi

SERVER_URL="${SERVER_URL%/}"
VERSION="${VERSION:-$(detect_default_version)}"

echo "Release upload target: $SERVER_URL"
echo "Release version: $VERSION"

case "$TARGET" in
  publisher)
    upload_publisher
    ;;
  cert-keeper)
    upload_cert_keeper
    ;;
  user)
    upload_user
    ;;
  all)
    upload_publisher
    upload_cert_keeper
    upload_user
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
