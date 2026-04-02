#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SYSTEMD_DIR="${ROOT_DIR}/deploy/linux/systemd"

sudo install -d -m 0755 /opt/cloud-relay-platform/bin
sudo install -d -m 0755 /etc/cloud-relay-platform
sudo install -d -m 0755 /etc/cloud-relay-platform/client-agent

sudo install -m 0644 "${SYSTEMD_DIR}/cloud-relay-server-api.service" /etc/systemd/system/cloud-relay-server-api.service
sudo install -m 0644 "${SYSTEMD_DIR}/cloud-relay-tcp.service" /etc/systemd/system/cloud-relay-tcp.service
sudo install -m 0644 "${SYSTEMD_DIR}/cloud-relay-udp.service" /etc/systemd/system/cloud-relay-udp.service
sudo install -m 0644 "${SYSTEMD_DIR}/cloud-relay-client-agent.service" /etc/systemd/system/cloud-relay-client-agent.service
sudo install -m 0644 "${SYSTEMD_DIR}/cloud-relay-client-agent@.service" /etc/systemd/system/cloud-relay-client-agent@.service

sudo systemctl daemon-reload

echo "systemd unit files installed."
echo "Single-instance compatibility env: /etc/cloud-relay-platform/client-agent.env"
echo "Multi-instance env dir: /etc/cloud-relay-platform/client-agent/<node-id>.env"
echo "Example: systemctl enable --now cloud-relay-client-agent@node-linux-udp-v1.service"
