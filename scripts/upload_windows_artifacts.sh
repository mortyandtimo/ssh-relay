#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:-all}"
SERVER_URL="${SERVER_URL:-}"
COOKIE_FILE="${COOKIE_FILE:-}"
VERSION="${VERSION:-}"
UPLOAD_RETRY_COUNT="${UPLOAD_RETRY_COUNT:-4}"
UPLOAD_RETRY_DELAY="${UPLOAD_RETRY_DELAY:-2}"
ADMIN_EMAIL="${ADMIN_EMAIL:-}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"
UPLOAD_FALLBACK_URLS="${UPLOAD_FALLBACK_URLS:-}"

usage() {
  cat <<'EOF'
usage: scripts/upload_windows_artifacts.sh [publisher|cert-keeper|user|all]

Required env:
  SERVER_URL   e.g. https://manage.020309.top
  COOKIE_FILE  curl cookie jar captured from an admin login session

Optional env:
  VERSION      release version path used by /downloads/releases/<product>/<version>/
               If omitted, a traceable default is derived from package version + git commit.
  ADMIN_EMAIL / ADMIN_PASSWORD
               if provided, the script can refresh an expired admin cookie jar automatically after a 401
  UPLOAD_FALLBACK_URLS
               optional whitespace-separated alternate base URLs tried after SERVER_URL for transient upload failures
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

json_escape() {
  local value="${1:-}"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '%s' "$value"
}

login_admin_session() {
  local base_url="${1:-$SERVER_URL}"
  if [ -z "$ADMIN_EMAIL" ] || [ -z "$ADMIN_PASSWORD" ]; then
    return 1
  fi
  local payload response_file status_code
  payload="$(printf '{"email":"%s","password":"%s"}' "$(json_escape "$ADMIN_EMAIL")" "$(json_escape "$ADMIN_PASSWORD")")"
  response_file="$(mktemp)"
  status_code="$(curl --silent --show-error --location \
    -c "$COOKIE_FILE" \
    -H "Content-Type: application/json" \
    -d "$payload" \
    -o "$response_file" \
    -w '%{http_code}' \
    "$base_url/api/auth/login")"
  if [ "$status_code" -lt 200 ] || [ "$status_code" -ge 300 ]; then
    echo "admin login failed while refreshing upload session against $base_url: http $status_code" >&2
    cat "$response_file" >&2
    rm -f "$response_file"
    return 1
  fi
  rm -f "$response_file"
  return 0
}

append_unique_url() {
  local value="$1"
  if [ -z "$value" ]; then
    return
  fi
  local existing
  for existing in "${UPLOAD_BASE_URLS[@]:-}"; do
    if [ "$existing" = "$value" ]; then
      return
    fi
  done
  UPLOAD_BASE_URLS+=("$value")
}

derive_default_fallback_urls() {
  local primary="$1"
  case "$primary" in
    https://manage.020309.top)
      printf '%s\n' "https://publisher.manage.020309.top"
      ;;
    https://publisher.manage.020309.top)
      printf '%s\n' "https://manage.020309.top"
      ;;
  esac
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
  local response_file status_code curl_exit attempt max_attempts retry_delay relogin_attempted base_url
  response_file="$(mktemp)"
  max_attempts="$UPLOAD_RETRY_COUNT"
  retry_delay="$UPLOAD_RETRY_DELAY"

  for attempt in $(seq 1 "$max_attempts"); do
    relogin_attempted=0
    for base_url in "${UPLOAD_BASE_URLS[@]}"; do
      status_code=""
      curl_exit=0
      if [ "$base_url" != "$SERVER_URL" ]; then
        echo "warning: trying fallback upload endpoint $base_url" >&2
      fi
      if ! status_code="$(curl --silent --show-error --location \
        -b "$COOKIE_FILE" \
        -F "product=$product" \
        -F "channel=$channel" \
        -F "version=$VERSION" \
        -F "sha256=$file_hash" \
        -F "file=@$file_path" \
        -o "$response_file" \
        -w '%{http_code}' \
        "$base_url/api/admin/release-artifacts/upload")"; then
        curl_exit=$?
      fi

      if [ "$curl_exit" -eq 0 ] && [ "$status_code" -ge 200 ] && [ "$status_code" -lt 300 ]; then
        cat "$response_file"
        rm -f "$response_file"
        echo
        return 0
      fi

      if [ "$curl_exit" -eq 0 ] && [ "$status_code" = "401" ] && [ "$relogin_attempted" = "0" ]; then
        if login_admin_session "$base_url"; then
          relogin_attempted=1
          echo "warning: upload session expired for $base_url; refreshed admin login and retrying immediately" >&2
          if status_code="$(curl --silent --show-error --location \
            -b "$COOKIE_FILE" \
            -F "product=$product" \
            -F "channel=$channel" \
            -F "version=$VERSION" \
            -F "sha256=$file_hash" \
            -F "file=@$file_path" \
            -o "$response_file" \
            -w '%{http_code}' \
            "$base_url/api/admin/release-artifacts/upload")"; then
            curl_exit=0
          else
            curl_exit=$?
          fi
          if [ "$curl_exit" -eq 0 ] && [ "$status_code" -ge 200 ] && [ "$status_code" -lt 300 ]; then
            cat "$response_file"
            rm -f "$response_file"
            echo
            return 0
          fi
        fi
      fi

      if [ "$attempt" -lt "$max_attempts" ]; then
        if [ "$curl_exit" -ne 0 ] || [ "$status_code" = "408" ] || [ "$status_code" = "429" ] || [ "$status_code" = "500" ] || [ "$status_code" = "502" ] || [ "$status_code" = "503" ] || [ "$status_code" = "504" ]; then
          if [ -n "$status_code" ]; then
            echo "warning: upload attempt $attempt/$max_attempts via $base_url failed with http $status_code" >&2
          else
            echo "warning: upload attempt $attempt/$max_attempts via $base_url failed with curl exit $curl_exit" >&2
          fi
          continue
        fi
      fi

      if [ -n "$status_code" ]; then
        echo "upload failed via $base_url: http $status_code" >&2
      else
        echo "upload failed via $base_url: curl exit $curl_exit" >&2
      fi
      cat "$response_file" >&2
    done

    if [ "$attempt" -lt "$max_attempts" ]; then
      echo "warning: all upload endpoints failed for attempt $attempt/$max_attempts; retrying in ${retry_delay}s" >&2
      sleep "$retry_delay"
    fi
  done

  rm -f "$response_file"
  return 1
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
DETECTED_VERSION="$(detect_default_version)"
VERSION_SOURCE="auto"
if [ -n "$VERSION" ]; then
  VERSION_SOURCE="env"
else
  VERSION="$DETECTED_VERSION"
fi

UPLOAD_BASE_URLS=()
append_unique_url "$SERVER_URL"
for url in $UPLOAD_FALLBACK_URLS; do
  append_unique_url "${url%/}"
done
while IFS= read -r url; do
  append_unique_url "${url%/}"
done < <(derive_default_fallback_urls "$SERVER_URL")

echo "Release upload target: $SERVER_URL"
echo "Release version: $VERSION"
if [ "${#UPLOAD_BASE_URLS[@]}" -gt 1 ]; then
  printf 'Upload fallback endpoints:%s\n' "" >&2
  printf '  %s\n' "${UPLOAD_BASE_URLS[@]:1}" >&2
fi
if [ "$VERSION_SOURCE" = "env" ] && [ "$VERSION" != "$DETECTED_VERSION" ]; then
  echo "warning: VERSION came from environment; current checkout would default to $DETECTED_VERSION" >&2
fi

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
