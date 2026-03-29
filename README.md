# Cloud Relay Platform

Cloud Relay Platform is a self-hosted reverse tunneling platform for exposing local services through a cloud relay while keeping management and visibility centralized.

## Scope

- `server-api`: control plane, node management, status aggregation, admin API
- `relay-tcp`: TCP relay runtime
- `relay-http`: HTTP relay runtime
- `relay-https`: HTTPS relay runtime with TLS termination reserved at the edge
- `client-agent`: node agent that registers, heartbeats, and later maintains data channels
- `admin-web`: React management console for nodes, tunnels, and server health

## Repository Layout

```text
apps/
  server-api/
  relay-tcp/
  relay-http/
  relay-https/
  client-agent/
  admin-web/
packages/
  protocol/
  shared/
db/
deploy/docker/
docs/
outputs/runtime/
```

## Current Status

This bootstrap establishes:

- requirement and design artifacts required by the governed runtime
- the initial Go module and shared protocol definitions
- a working `server-api` with register, heartbeat, node listing, and server metrics endpoints
- a PostgreSQL-backed `server-api` with tunnel persistence and internal TCP route discovery
- a `relay-tcp` service that polls active TCP routes and binds public ports dynamically
- a `client-agent` that can register itself and send heartbeats
- a React admin shell ready to consume the management API

HTTP and HTTPS relays still remain bootstrap services in this phase. The first real data-plane path implemented here is TCP relay driven by persisted routes from `server-api`.

## Quick Start

### Local Docker rehearsal

```powershell
docker build -f apps/server-api/Dockerfile -t cloud-relay-platform/server-api:dev .
docker build -f apps/relay-tcp/Dockerfile -t cloud-relay-platform/relay-tcp:dev .
docker compose -f deploy/docker/docker-compose.yml up -d postgres server-api relay-tcp
```

### Admin web

```powershell
cd apps/admin-web
npm install
npm run dev
```

By default the admin UI expects the API at `http://localhost:8080`.

### Lightweight cloud deployment

For a small cloud VM, prefer native binaries instead of Docker. The repository now includes `deploy/linux/` for this path.

Recommended shape on the VM:

- binaries under `/opt/cloud-relay-platform/bin`
- environment files under `/etc/cloud-relay-platform`
- `server-api` and `relay-tcp` managed by `systemd`
- PostgreSQL installed natively from the distro package manager

Build Linux binaries from a machine with Go installed:

```bash
./deploy/linux/scripts/build-linux-binaries.sh
```

Then copy these files to the VM:

- `bin/linux-amd64/server-api`
- `bin/linux-amd64/relay-tcp`
- `bin/linux-amd64/client-agent`
- `db/schema.sql`
- `deploy/linux/systemd/*.service`
- `deploy/linux/env/*.env.example`

Install the systemd units on the VM:

```bash
sudo bash deploy/linux/scripts/install-systemd.sh
```

Create real env files on the VM:

- `/etc/cloud-relay-platform/server-api.env`
- `/etc/cloud-relay-platform/relay-tcp.env`
- `/etc/cloud-relay-platform/client-agent.env`

Then enable services:

```bash
sudo systemctl enable --now cloud-relay-server-api
sudo systemctl enable --now cloud-relay-tcp
```

Check health:

```bash
curl http://127.0.0.1:8080/healthz
journalctl -u cloud-relay-server-api -n 100 --no-pager
journalctl -u cloud-relay-tcp -n 100 --no-pager
```

### Cloud server rehearsal

If you still want a disposable all-in-one rehearsal on a stronger machine, you can use Docker locally. On the lightweight cloud VM, prefer the native binary path above.

```powershell
docker build -f apps/server-api/Dockerfile -t cloud-relay-platform/server-api:dev .
docker build -f apps/relay-tcp/Dockerfile -t cloud-relay-platform/relay-tcp:dev .
docker compose -f deploy/docker/docker-compose.yml up -d postgres server-api relay-tcp
Invoke-RestMethod http://127.0.0.1:8080/healthz
```

Then create a test node and tunnel from the VM itself:

```powershell
$node = Invoke-RestMethod -Uri 'http://127.0.0.1:8080/agent/register' -Method Post -ContentType 'application/json' -Body '{"nodeName":"cloud-test","agentVersion":"0.2.0","capabilities":{"tcpRelay":true,"httpRelay":true,"httpsRelay":true,"udpRelay":false,"p2pAssist":false}}'
$tunnel = @{ nodeId = $node.nodeId; name = 'vm-test'; type = 'tcp'; transportPolicy = 'relay_only'; targetHost = '127.0.0.1'; targetPort = 22; publicPort = 20022; status = 'active' } | ConvertTo-Json
Invoke-RestMethod -Uri 'http://127.0.0.1:8080/api/tunnels' -Method Post -ContentType 'application/json' -Body $tunnel
```

The current relay runtime will then expose `20022` on the cloud server and forward it to the configured local target. This is enough to rehearse route persistence, route polling, and TCP socket proxying on the cloud VM before the reverse data channel from `client-agent` is added.

## Key Endpoints

- `GET /healthz`
- `POST /agent/register`
- `POST /agent/heartbeat`
- `GET /api/nodes`
- `GET /api/tunnels`
- `GET /api/server/metrics`
