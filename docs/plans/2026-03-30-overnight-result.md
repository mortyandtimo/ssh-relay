# Overnight Result

## What Changed

- Tightened `relay-tcp` standby pool handling in `apps/relay-tcp/internal/runtime/runtime.go`.
- Added standby connection age tracking and bounded eviction behavior instead of treating a full queue as an unconditional hard reject.
- Implemented the first elastic standby pool shape on the cloud side only:
  - strict per-key hard cap
  - explicit `min/target/max` constants for the first version
  - atomic `totalStandby` accounting
  - `totalStandby` included in relay logs for real diagnostics
- Added clearer relay logs for:
  - standby admitted with `poolSize`
  - standby admitted with `totalStandby`
  - stale standby discarded during pairing
  - expired standby eviction
  - pairing lifecycle summaries
- Preserved the existing node-scoped matching key of `nodeId + publicPort` with strict tunnel hello validation.
- Kept the current control plane and native deployment layout unchanged:
  - `server-api` on `:7710`
  - env under `/etc/cloud-relay-platform`
  - binaries under `/opt/cloud-relay-platform/bin`
  - no Docker regression
- Added the minimum management loop on top of the working relay path:
  - tunnel CRUD in `server-api`
  - active TCP `publicPort` conflict validation
  - relay runtime summary API skeleton
  - minimal `admin-web` for nodes, tunnel list, tunnel create, tunnel enable/pause/delete

## Verification Commands

Cloud-side commands used tonight:

```bash
cd /root/cloud-relay-platform
git pull origin feat/reverse-tcp-channel

env -u GOOS -u GOARCH GOCACHE=/root/cloud-relay-platform/.gocache go test ./apps/relay-tcp/internal/runtime
env -u GOOS -u GOARCH GOCACHE=/root/cloud-relay-platform/.gocache CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o deploy/bin/linux-amd64/relay-tcp ./apps/relay-tcp/cmd/relay-tcp

install -m 0755 deploy/bin/linux-amd64/relay-tcp /opt/cloud-relay-platform/bin/relay-tcp
systemctl restart cloud-relay-tcp

curl -fsS http://127.0.0.1:7710/healthz
curl -fsS 'http://127.0.0.1:7710/api/nodes'
curl -fsS 'http://127.0.0.1:7710/api/tunnels?nodeId=node-1774805183388699102'
journalctl -u cloud-relay-tcp --since '2026-03-30 04:42:35' --no-pager
curl -sS -o /dev/null -w 'code=%{http_code} total=%{time_total}\n' http://82.156.236.104:10086

env -u GOOS -u GOARCH GOCACHE=/root/cloud-relay-platform/.gocache CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o deploy/bin/linux-amd64/server-api ./apps/server-api/cmd/server-api
install -m 0755 deploy/bin/linux-amd64/server-api /opt/cloud-relay-platform/bin/server-api
systemctl restart cloud-relay-server-api

cd /root/cloud-relay-platform/apps/admin-web
npm install
npm run build
mkdir -p /opt/cloud-relay-platform/admin-web
cp -r dist/* /opt/cloud-relay-platform/admin-web/
python3 -m http.server 18081 --directory /opt/cloud-relay-platform/admin-web
```

Windows-side evidence already available before this overnight cycle:

```powershell
Invoke-WebRequest -UseBasicParsing http://82.156.236.104:10086
1..10 | ForEach-Object { Invoke-WebRequest -UseBasicParsing http://82.156.236.104:10086 }
```

## Observed Results

- `server-api` stayed healthy on `:7710` with PostgreSQL store.
- Current Windows node remained present in `nodes` as `node-1774805183388699102`.
- Current active tunnel remained:
  - public port `10086`
  - target `127.0.0.1:16354`
- After redeploying the updated `relay-tcp`, the currently running Windows agent refilled the standby pool without requiring a Windows restart.
- New logs showed controlled standby admission instead of immediate rejection spam:

```text
2026/03/30 04:42:36 standby reverse connection ready: ... publicPort=10086 poolSize=1
2026/03/30 04:42:36 standby reverse connection ready: ... publicPort=10086 poolSize=2
...
2026/03/30 04:42:38 standby reverse connection ready: ... publicPort=10086 poolSize=8
```

- A fresh cloud-side request still succeeded after the cloud redeploy:

```text
code=200 total=0.043278
```

- The earlier uncontrolled `standby pool full` spam observed before restart was not reproduced after deploying the updated cloud-side relay.
- Morning retest with the currently running Windows agent and the redeployed cloud relay showed the desired bounded standby pool behavior:

```text
2026/03/30 14:22:59 standby reverse connection ready: ... poolSize=1
...
2026/03/30 14:22:59 standby reverse connection ready: ... poolSize=8
2026/03/30 14:23:11 tcp connection 5 paired with standby reverse connection ... standbyAge=12.097925512s
2026/03/30 14:23:12 tcp connection 5 closed after 165.48628ms
2026/03/30 14:23:12 standby reverse connection ready: ... poolSize=8
```

- Morning retest from the client side also succeeded against the current online Windows agent without any Windows-side restart requirement:

```text
StatusCode : 200
Content    : <!DOCTYPE html>...
```

- Final elastic-pool retest with the currently running Windows agent and the redeployed cloud relay confirmed the first elastic version works without requiring a Windows-side restart or protocol change.
- Follow-up retest after tightening the first elastic pool implementation proved the cloud relay still works with the currently running Windows agent while enforcing a hard refill window back to `8` immediately after restart.

```text
2026/03/30 15:44:40 standby reverse connection ready: ... poolSize=1 totalStandby=1
2026/03/30 15:44:41 standby reverse connection ready: ... poolSize=2 totalStandby=2
...
2026/03/30 15:44:42 standby reverse connection ready: ... poolSize=8 totalStandby=8
```

- After a fresh redeploy of the tightened version, the current online Windows agent again repopulated the pool only up to the intended bound:

```text
2026/03/30 16:05:27 standby reverse connection ready: ... poolSize=1 totalStandby=1
...
2026/03/30 16:05:27 standby reverse connection ready: ... poolSize=8 totalStandby=8
```

- After refactoring relay standby storage from anonymous channel queues toward an explicit pool lifecycle model and redeploying again, the current online Windows agent continued to work without a protocol change, and the cloud relay kept the pool bounded at `8` while evicting the oldest standby before each new admission:

```text
2026/03/31 01:47:03 evict standby reverse connection ... poolSize=7 totalStandby=7 to keep target pool size=8
2026/03/31 01:47:03 standby reverse connection ready: ... poolSize=8 totalStandby=8
```

- A fresh cloud-side request against the current online Windows agent still succeeded after the elastic-pool relay deploy:

```text
code=200 total=0.068908
```

- A fresh cloud-side request still succeeded after the standby pool lifecycle refactor:

```text
code=200 total=0.052019
```

- The tunnel management API now supports a minimal CRUD loop without manual SQL:

```text
GET  /api/tunnels/{id}           -> 200
PUT  /api/tunnels/{id}           -> 200
DELETE /api/tunnels/{id}         -> 200
POST /api/tunnels (conflict)     -> 409
GET  /api/relay/tcp/runtime      -> 200
```

- A minimal admin web build now succeeds and static assets were staged to `/opt/cloud-relay-platform/admin-web` with a lightweight HTTP serve on `:18081` for immediate use.
- The latest cloud-side management loop verification also confirmed:

```text
GET /api/tunnels/tunnel-1774809656590623537 -> 200
GET /api/relay/tcp/runtime                    -> 200
GET http://82.156.236.104:18081/             -> 200
```

- The management API can now be used for create/update/delete and conflict-aware validation instead of manual SQL, and the lightweight web console can operate against the live cloud API.

## What Still Fails Or Remains Risky

- The currently running Windows agent process is still an old runtime shape from earlier testing history. It can refill the pool and successfully serve traffic, but the cloud side has previously observed stale standby entries and long-lived queue buildup.
- Tonight's cloud-side change improves pool admission behavior and observability, but it does not yet introduce a fully adaptive or tunnel-specific standby pool policy.
- Because the Windows agent was treated as fixed tonight, this result should be considered a cloud-side stabilization step, not the final completed design for long-term pool management.
- A longer soak test is still useful, but the latest retest now proves the current online Windows agent can pair successfully with the first elastic standby pool implementation.
- A longer soak test is still useful, especially because the current Windows agent can later refill the pool above the initial target over time. The latest retest does, however, prove that the cloud-side implementation can restart cleanly, repopulate to the intended standby window, and serve traffic successfully with the currently running Windows agent.
- The relay runtime summary endpoint is intentionally minimal in this round. It exposes pool keys and configured bounds, but not yet the in-process live standby counts from `relay-tcp`.
- The temporary admin web is currently served by a lightweight Python HTTP process on `:18081`, not yet a formal systemd/nginx integration.
- Authentication is not yet enabled for the admin loop.
- HTTPS same-origin deployment for the admin web is intentionally deferred until a dedicated subdomain is chosen; the current temporary UI endpoint should be treated as a staging path only.

## Windows-Side Restart Requirement

- **Not required for tonight's cloud-side verification.**
- The currently running Windows agent was sufficient to verify that the updated cloud-side relay can restart, refill the standby pool to the bounded target, pair traffic successfully, and continue serving the existing tunnel.
- Tomorrow's manual Windows-side restart or redeploy is still recommended if a newer agent build is desired for additional noise reduction or future protocol work, but it was not required for the validation recorded here.

## Final Evidence Summary

- Verified tunnel path remains `82.156.236.104:10086 -> node-1774805183388699102 -> 127.0.0.1:16354`.
- Cloud-side relay restart did not break compatibility with the currently running Windows agent.
- Pool behavior after redeploy was measurable, bounded to the intended standby window during the latest retest, and diagnosable via logs including `totalStandby`.
- The current remaining risk is not protocol compatibility but policy quality: with the current fixed Windows agent process, the relay now bounds and evicts correctly, but smarter adaptive pool control is still future work.
- Management no longer depends on manual SQL for the basic tunnel lifecycle.


## 2026-03-31 Management Security Follow-Up

### What Changed In This Round

- Added minimal Bearer token protection to the management-side `server-api` endpoints without touching agent protocol compatibility.
- Protected these management endpoints:
  - `GET /api/nodes`
  - `GET/POST /api/tunnels`
  - `GET/PUT/DELETE /api/tunnels/{id}`
  - `GET /api/server/metrics`
  - `GET /api/relay/tcp/runtime`
- Kept these endpoints compatible and unauthenticated for the current online Windows agent:
  - `/agent/register`
  - `/agent/heartbeat`
  - `/agent/tunnels`
  - `/internal/routes/tcp`
- Kept `relay-tcp` protocol unchanged and verified that runtime summary now surfaces real live data already present in-process:
  - `totalStandby`
  - each pool `standbyCount`
- Updated `admin-web` to:
  - use Chinese management login text
  - store and send Bearer token on every management request
  - stop auto-refresh from overwriting tunnel form draft state and selected `nodeId`
  - show the live standby pool summary returned by the protected runtime API

### Verification In This Round

```bash
env -u GOOS -u GOARCH GOCACHE=/root/cloud-relay-platform/.gocache go test ./apps/server-api/internal/api ./apps/relay-tcp/internal/runtime
npm --prefix apps/admin-web run build

curl -s -o /tmp/api_nodes_unauth.json -w '%{http_code}' http://127.0.0.1:7710/api/nodes
# -> 401

curl -s -H 'Authorization: Bearer cloud-relay-admin-20260331' http://127.0.0.1:7710/api/relay/tcp/runtime
# -> totalStandby=8, standbyCount=8 for node-1774805183388699102:10086

curl -s -H 'Authorization: Bearer cloud-relay-admin-20260331' http://127.0.0.1:7710/api/nodes
curl -s -H 'Authorization: Bearer cloud-relay-admin-20260331' http://127.0.0.1:7710/api/tunnels
curl -s -o /dev/null -w 'code=%{http_code} total=%{time_total}
' http://82.156.236.104:10086
curl -s -o /dev/null -w '%{http_code}
' http://127.0.0.1:18081/
```

### Observed Results In This Round

- Unauthenticated management API access now returns `401`.
- Authenticated management API access succeeds with Bearer token.
- Live relay runtime summary now returns real standby values instead of skeleton placeholders:

```json
{
  "totalStandby": 8,
  "pools": [
    {
      "poolKey": "node-1774805183388699102:10086",
      "standbyCount": 8
    }
  ]
}
```

- `admin-web` static site remains reachable on `http://82.156.236.104:18081/` and can now be used by entering the current Bearer token in-page.
- Existing reverse TCP tunnel remained healthy after redeploy:

```text
code=200 total=0.039750
```

### Remaining Risk

- The current Bearer token is a minimal management guard, not a full user system or HTTPS same-origin hardening solution.
- Admin token is currently static env configuration and should later move behind dedicated domain + TLS + stronger credential lifecycle.


## 2026-03-31 Web Session Auth Upgrade

### What Changed In This Round

- Replaced the temporary Bearer-token management access pattern with a real Web session model.
- Added server-side auth/session endpoints:
  - `GET /api/auth/bootstrap-status`
  - `POST /api/auth/bootstrap`
  - `POST /api/auth/login`
  - `POST /api/auth/refresh`
  - `POST /api/auth/logout`
  - `GET /api/auth/me`
- Added role-aware user management endpoints:
  - `GET /api/users`
  - `POST /api/users`
  - `PUT /api/users/{id}`
  - `DELETE /api/users/{id}`
- Added three role levels:
  - `admin`
  - `manager`
  - `user`
- Moved management authentication to cookie-backed Web sessions with dedicated `web_sessions` persistence.
- Switched admin UI from temporary token-entry page to a real same-origin management application under `/admin/`.
- Added bootstrap flow for first admin creation, login page, logout, and role-based page behavior.
- Preserved current reverse TCP protocol and current Windows agent compatibility.

### Live Verification In This Round

```bash
psql postgres://postgres:wdblsw12138@127.0.0.1:5432/cloud_relay?sslmode=disable -f db/schema.sql

env -u GOOS -u GOARCH GOCACHE=/root/cloud-relay-platform/.gocache go test ./apps/server-api/internal/api ./apps/server-api/internal/store ./apps/relay-tcp/internal/runtime
npm --prefix apps/admin-web run build

curl http://127.0.0.1:7710/api/auth/bootstrap-status
curl -i -H 'Content-Type: application/json' -d '{...}' http://127.0.0.1:7710/api/auth/bootstrap
curl -i -H 'Content-Type: application/json' -d '{...}' http://127.0.0.1:7710/api/auth/login
curl http://127.0.0.1:7710/admin/
curl http://127.0.0.1:7710/admin/assets/index-DH-GNW08.js
curl -s -o /dev/null -w 'code=%{http_code} total=%{time_total}
' http://82.156.236.104:10086
```

### Observed Results In This Round

- Same-origin admin page is now served from `server-api` itself:
  - `GET http://82.156.236.104:7710/admin/ -> 200`
- Static admin assets are also same-origin and load from `/admin/assets/...`.
- First admin bootstrap succeeded and persisted in PostgreSQL.
- Login now issues session cookies:
  - `crp_access`
  - `crp_refresh`
  - `crp_session`
- Management API without login now returns `401`.
- Reverse TCP production path still remained healthy after the auth/system changes:

```text
code=200 total=0.040986
```

### Remaining Risk

- Current access token is a signed opaque payload rather than a full JWT stack, which is acceptable for the current self-hosted scope but still a lightweight implementation.
- HTTPS and dedicated subdomain deployment still remain the next required hardening step before exposing the management plane more broadly.
