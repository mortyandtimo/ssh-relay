# Cloud Codex Handoff

## Current State

- Local repo branch: `feat/reverse-tcp-channel`
- Gitee remote: `origin`
- Cloud server control plane is already deployed natively, not with Docker.
- `cloud-relay-server-api` is healthy on `:7710`.
- PostgreSQL database `cloud_relay` exists and schema is loaded.
- Windows `client-agent.exe -once` can register and heartbeat into `nodes`.
- `cloud-relay-tcp` should remain stopped until reverse data channel support is finished.

## Confirmed Working Facts

1. `curl http://127.0.0.1:7710/healthz` on the cloud server returns `store=postgres`.
2. Windows can reach `http://82.156.236.104:7710`.
3. `nodes` contains a real Windows node named `my-windows-pc`.
4. Current `relay-tcp` implementation only supports cloud-side direct dialing. It does **not** yet tunnel traffic back through `client-agent`.

## Deployment Layout On Cloud Server

- Git working tree: `/root/cloud-relay-platform`
- Binary install dir: `/opt/cloud-relay-platform/bin`
- Env dir: `/etc/cloud-relay-platform`
- systemd units:
  - `cloud-relay-server-api`
  - `cloud-relay-tcp`

## Important Env Values Already In Use

### `/etc/cloud-relay-platform/server-api.env`

```env
SERVER_API_ADDR=:7710
DATABASE_URL=postgres://postgres:wdblsw12138@127.0.0.1:5432/cloud_relay?sslmode=disable
```

### `/etc/cloud-relay-platform/relay-tcp.env`

```env
RELAY_TCP_ADDR=:9090
RELAY_TCP_API_BASE_URL=http://127.0.0.1:7710
```

## Exact Next Goal

Implement a real reverse TCP data channel so the cloud relay can expose a Windows local service such as `127.0.0.1:23546` without direct cloud-side dialing.

## Required Direction

1. `server-api` must support node-scoped tunnel queries.
2. `client-agent` must poll or fetch its own active TCP tunnels.
3. `client-agent` must open reverse connections back to the cloud relay and identify the tunnel/public port.
4. `relay-tcp` must maintain an agent connection pool keyed by node and public port.
5. Public inbound connections on the cloud server must be paired with an available reverse agent connection.
6. Only after this is implemented should `cloud-relay-tcp` be used to test ports like `10086`.

## Files Already Touched For This Direction

- `packages/protocol/types/types.go`
- `apps/server-api/internal/store/store.go`
- `apps/server-api/internal/store/memory.go`
- `apps/server-api/internal/store/postgres.go`
- `apps/server-api/internal/api/server.go`

These files now include the start of node-aware tunnel support and protocol additions for reverse relay work. Continue from there instead of re-planning from scratch.

## Cloud Pull / Rebuild Cycle

After pulling new code on the cloud server, use this sequence:

```bash
cd /root/cloud-relay-platform
git pull origin feat/reverse-tcp-channel
chmod +x deploy/linux/scripts/*.sh
bash deploy/linux/scripts/build-linux-binaries.sh

systemctl stop cloud-relay-server-api
systemctl stop cloud-relay-tcp 2>/dev/null || true

cp /root/cloud-relay-platform/deploy/bin/linux-amd64/* /opt/cloud-relay-platform/bin/
chmod +x /opt/cloud-relay-platform/bin/*
chown -R relay:relay /opt/cloud-relay-platform

systemctl start cloud-relay-server-api
systemctl start cloud-relay-tcp
```

## Verification Targets For Cloud Codex

1. `curl http://127.0.0.1:7710/healthz`
2. `systemctl status cloud-relay-server-api --no-pager`
3. `systemctl status cloud-relay-tcp --no-pager`
4. Windows client remains able to register and heartbeat.
5. A TCP tunnel from cloud public port to Windows local port succeeds only after reverse channel implementation is complete.

## Do Not Regress

- Do not switch cloud deployment back to Docker.
- Do not change the control plane off `:7710`.
- Do not rely on `target_host = 127.0.0.1` or hostnames like `agent`/`my-windows-node` for Windows local services.
- Do not use manual database tunnel inserts as a substitute for reverse channel support.

