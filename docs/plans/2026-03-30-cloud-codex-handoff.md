# Cloud Codex Handoff

## Current State

- Local repo branch: `feat/reverse-tcp-channel`
- Gitee remote: `origin`
- Cloud server control plane is already deployed natively, not with Docker.
- `cloud-relay-server-api` is healthy on `:7710`.
- PostgreSQL database `cloud_relay` exists and schema is loaded.
- Windows `client-agent.exe -once` can register and heartbeat into `nodes`.
- Reverse TCP data channel is now implemented and verified against a real Windows local service.

## Confirmed Working Facts

1. `curl http://127.0.0.1:7710/healthz` on the cloud server returns `store=postgres`.
2. Windows can reach `http://82.156.236.104:7710`.
3. `nodes` contains a real Windows node named `my-windows-pc`.
4. `relay-tcp` no longer dials Windows local services from the cloud side. Public inbound TCP connections are paired with standby reverse connections from `client-agent`.
5. Verified working path: `82.156.236.104:10086 -> Windows node node-1774805183388699102 -> 127.0.0.1:16354`.
6. Baseline validation on the Windows side succeeded with about `98ms` single-request latency, `10/10` serial HTTP success, and `8/8` concurrent HTTP success.

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

### Windows client-agent test env

```env
CLOUD_RELAY_API_URL=http://82.156.236.104:7710
RELAY_TCP_CONNECT_URL=http://82.156.236.104:9090/agent/reverse-tcp
CLIENT_NODE_NAME=my-windows-pc
CLIENT_NODE_ID=node-1774805183388699102
AGENT_HEARTBEAT_INTERVAL=30
AGENT_REVERSE_POOL_SIZE=8
```

## Current Verified Test Mapping

- Public port: `10086`
- Windows target host: `127.0.0.1`
- Windows target port: `16354`
- Node ID: `node-1774805183388699102`

## Test Flow Guidance

1. Keep the Windows local service listening on `127.0.0.1:16354`.
2. Start exactly one `client-agent.exe` instance with `AGENT_REVERSE_POOL_SIZE=8` during the current test phase.
3. Validate the public port with `Invoke-WebRequest -UseBasicParsing http://82.156.236.104:10086`.
4. For lightweight availability checks, use serial `10` request loops and `8` concurrent jobs from Windows PowerShell.

## Why Pool Size Is 8 For Now

- `AGENT_REVERSE_POOL_SIZE` is per tunnel, not global.
- With one active TCP tunnel, `8` means `8` standby reverse connections for that one public port.
- Earlier smaller values such as `2` were enough for single requests but were too small for browser-style parallel HTTP fetches.
- Current validation proved `8` is enough for a lightweight `8`-concurrency check against the test service.
- Treat `8` as a temporary testing default, not as the final adaptive policy.

## Follow-up Engineering Guidance

- Keep the current test process using fixed `AGENT_REVERSE_POOL_SIZE=8` until stability and regression work is complete.
- Add smarter pool control later instead of hard-coding one value for every tunnel.
- The preferred future direction is tunnel-aware control, for example:
  - low default pool for generic TCP services
  - larger pool for browser-facing HTTP workloads carried over TCP relay
  - optional tunnel-level override in control-plane metadata
  - adaptive refill based on recent concurrency or queue pressure
- Management follow-up still needed:
  - minimal admin login flow using the existing `users` table
  - same-origin HTTPS deployment for admin-web instead of the temporary `:18081` HTTP preview
  - do not bind the final deployment to the main domain yet; wait for a dedicated subdomain choice

## Exact Next Goal

Stabilize the verified reverse TCP data channel implementation for long-running use and improve pool management beyond the current fixed testing value.

## Required Direction

1. Preserve the current verified reverse TCP path and do not regress to cloud-side direct dialing.
2. Keep node-scoped tunnel queries and reverse standby connection pooling intact.
3. Remove remaining agent-side panic and timeout noise from long-running idle standby connections.
4. Add smarter standby pool sizing so testing does not depend on one global fixed value.
5. Keep validating real tunnels through the control plane instead of manual database edits.

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
5. `Invoke-WebRequest -UseBasicParsing http://82.156.236.104:10086` returns the Windows local service from `127.0.0.1:16354`.
6. During the current test phase, `10/10` serial requests and `8/8` concurrent requests should succeed with `AGENT_REVERSE_POOL_SIZE=8`.

## Do Not Regress

- Do not switch cloud deployment back to Docker.
- Do not change the control plane off `:7710`.
- Do not rely on `target_host = 127.0.0.1` or hostnames like `agent`/`my-windows-node` for Windows local services.
- Do not use manual database tunnel inserts as a substitute for reverse channel support.
