#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="${ROOT_DIR}/bin/linux-amd64"

mkdir -p "${OUT_DIR}"

export CGO_ENABLED=0
export GOOS=linux
export GOARCH=amd64

go build -o "${OUT_DIR}/server-api" ./apps/server-api/cmd/server-api
go build -o "${OUT_DIR}/relay-tcp" ./apps/relay-tcp/cmd/relay-tcp
go build -o "${OUT_DIR}/relay-udp" ./apps/relay-udp/cmd/relay-udp
go build -o "${OUT_DIR}/relay-http" ./apps/relay-http/cmd/relay-http
go build -o "${OUT_DIR}/relay-https" ./apps/relay-https/cmd/relay-https
go build -o "${OUT_DIR}/client-agent" ./apps/client-agent/cmd/client-agent
go build -o "${OUT_DIR}/ssh-relay-api" ./apps/ssh-relay-api/cmd/ssh-relay-api
go build -o "${OUT_DIR}/sshr" ./apps/ssh-relay-cli/cmd/sshr

echo "linux binaries built into ${OUT_DIR}"

