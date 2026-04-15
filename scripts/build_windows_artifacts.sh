#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:-all}"

need_cmd() {
  local cmd="$1"
  local hint="$2"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing required command: $cmd ($hint)" >&2
    exit 1
  fi
}

has_rust_target() {
  local triple="$1"
  rustup target list --installed 2>/dev/null | grep -Fx "$triple" >/dev/null 2>&1
}

check_publisher() {
  need_cmd npm "install Node.js/npm first"
  need_cmd cargo "install Rust toolchain first"
  need_cmd rustup "install rustup first"
  need_cmd go "install Go first"
  need_cmd zip "install zip first"
  if ! has_rust_target x86_64-pc-windows-gnu && ! has_rust_target x86_64-pc-windows-msvc; then
    echo "missing Rust Windows target: install x86_64-pc-windows-gnu or x86_64-pc-windows-msvc" >&2
    exit 1
  fi
  if ! command -v makensis >/dev/null 2>&1 && [ -z "${MAKENSIS_BIN:-}" ]; then
    echo "warning: makensis not found; publisher portable zip will build but setup exe will be skipped" >&2
  fi
}

check_cert_keeper() {
  need_cmd npm "install Node.js/npm first"
  need_cmd cargo "install Rust toolchain first"
  need_cmd rustup "install rustup first"
  need_cmd zip "install zip first"
  if ! has_rust_target x86_64-pc-windows-gnu; then
    echo "missing Rust Windows target: install x86_64-pc-windows-gnu" >&2
    exit 1
  fi
  if ! command -v makensis >/dev/null 2>&1 && [ -z "${MAKENSIS_BIN:-}" ]; then
    echo "warning: makensis not found; CertKeeper portable zip will build but setup exe will be skipped" >&2
  fi
}

build_publisher() {
  check_publisher
  "$ROOT_DIR/deploy/windows/publisher/build-windows-artifacts.sh"
}

build_cert_keeper() {
  check_cert_keeper
  "$ROOT_DIR/deploy/windows/cert-keeper/build-windows-artifacts.sh"
}

case "$TARGET" in
  publisher)
    build_publisher
    ;;
  cert-keeper)
    build_cert_keeper
    ;;
  all)
    build_publisher
    build_cert_keeper
    ;;
  *)
    echo "usage: $0 [publisher|cert-keeper|all]" >&2
    exit 1
    ;;
esac
