#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:-all}"
SYNC_FROM_GITEE="${SYNC_FROM_GITEE:-1}"
SKIP_UPLOAD="${SKIP_UPLOAD:-0}"
ALLOW_DIRTY="${ALLOW_DIRTY:-0}"
CURRENT_BRANCH="$(git -C "$ROOT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")"
if [ "$CURRENT_BRANCH" = "HEAD" ]; then
  CURRENT_BRANCH=""
fi
BRANCH="${BRANCH:-$CURRENT_BRANCH}"

usage() {
  cat <<'EOF'
usage: scripts/packager_build_and_upload.sh [publisher|cert-keeper|all]

Packager responsibilities:
  1. Pull source changes from Gitee on the build machine.
  2. Build cache-heavy Windows artifacts locally.
  3. Upload finished artifacts back to the server release API.

Optional env:
  BRANCH           branch to pull before building
  SYNC_FROM_GITEE  1 to run git pull --ff-only, 0 to use current checkout
  SKIP_UPLOAD      1 to stop after build
  ALLOW_DIRTY      1 to bypass dirty-worktree protection

Required env for upload phase:
  SERVER_URL
  COOKIE_FILE
  VERSION          optional; forwarded to upload_windows_artifacts.sh
EOF
}

ensure_clean_checkout() {
  if [ "$ALLOW_DIRTY" = "1" ]; then
    return
  fi
  if [ -n "$(git -C "$ROOT_DIR" status --short)" ]; then
    echo "build machine worktree is dirty; commit/stash first or set ALLOW_DIRTY=1" >&2
    exit 1
  fi
}

sync_source() {
  if [ "$SYNC_FROM_GITEE" != "1" ]; then
    echo "Skipping Gitee sync; using current checkout."
    return
  fi
  ensure_clean_checkout
  if [ -z "$BRANCH" ]; then
    echo "BRANCH is required when the current checkout is detached" >&2
    exit 1
  fi
  echo "Syncing source from Gitee branch: $BRANCH"
  git -C "$ROOT_DIR" pull --ff-only origin "$BRANCH"
}

build_artifacts() {
  echo "Building Windows artifacts on packager: $TARGET"
  "$ROOT_DIR/scripts/build_windows_artifacts.sh" "$TARGET"
}

upload_artifacts() {
  if [ "$SKIP_UPLOAD" = "1" ]; then
    echo "Skipping upload; artifacts remain in outputs/."
    return
  fi
  "$ROOT_DIR/scripts/upload_windows_artifacts.sh" "$TARGET"
}

if [ "$TARGET" = "-h" ] || [ "$TARGET" = "--help" ]; then
  usage
  exit 0
fi

sync_source
build_artifacts
upload_artifacts
