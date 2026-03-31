# Overnight Result

## Current State

- Branch remains `feat/reverse-tcp-channel`.
- Native cloud deployment remains in use:
  - `server-api` on `:7710`
  - `relay-tcp` on `:9090`
  - env files under `/etc/cloud-relay-platform`
  - binaries under `/opt/cloud-relay-platform/bin`
- Reverse TCP production path remains verified and unchanged at protocol level:
  - `82.156.236.104:10086 -> node-1774805183388699102 -> 127.0.0.1:16354`
- Management console is now same-origin and served by `server-api` itself:
  - `http://82.156.236.104:7710/admin/`
- Management authentication is now cookie-backed Web session auth with three roles:
  - `admin`
  - `manager`
  - `user`
- First admin has already been bootstrapped in PostgreSQL.
- Relay runtime summary is live data, not placeholder data.
- Current standby pool policy is still fixed-window at runtime for the active tunnel:
  - current live behavior is effectively `target=8`, `max=8`
  - this is not yet the final elastic pool policy

## Current Risk

- Reverse TCP pool lifecycle is stabilized, but pool sizing policy is still fixed-window rather than fully elastic.
- Access token implementation is currently a signed opaque payload, which is acceptable for the current self-hosted phase but still a lightweight session design.
- HTTPS and dedicated subdomain deployment are still pending and remain the next hardening step before wider exposure.
- Bootstrap endpoint security now depends on explicit `SERVER_API_ADMIN_BOOTSTRAP_SECRET`; missing or incorrect configuration will correctly block bootstrap.
- CORS is no longer open by default; any future cross-origin development workflow must be added explicitly through allowlist configuration.

## Latest Deployment Verification

### Auth And Management Plane

- `GET /api/auth/bootstrap-status` works on cloud and currently returns `required=false` after admin bootstrap.
- Same-origin admin page is live:
  - `GET http://82.156.236.104:7710/admin/ -> 200`
- Same-origin static assets are live:
  - `GET http://82.156.236.104:7710/admin/assets/... -> 200`
- Login issues Web session cookies:
  - `crp_access`
  - `crp_refresh`
  - `crp_session`
- Management API without login returns `401`.
- Admin session can access `/api/users`, `/api/nodes`, `/api/tunnels`, `/api/server/metrics`, `/api/relay/tcp/runtime`.
- Manager/user role behavior is covered by local tests.
- Admin web request layer now supports `401 -> POST /api/auth/refresh -> retry original request`; refresh failure returns the UI to login state.

### Bootstrap Security

- `/api/auth/bootstrap` is no longer meant to be publicly usable without bootstrap secret.
- Expected behavior now:
  - missing secret -> `401`
  - wrong secret -> `401`
  - correct secret -> bootstrap allowed only while bootstrap is still required
- Current cloud state already has an initialized admin, so bootstrap now returns `required=false` from status and normal bootstrap retry returns conflict after initialization.

### CORS

- CORS is no longer raw-reflection for arbitrary origin.
- Allowed origins are now driven by explicit allowlist configuration via `SERVER_API_ALLOWED_ORIGINS`.
- Same-origin `/admin` continues to work without depending on permissive cross-origin behavior.

### Reverse TCP Runtime

- Reverse TCP public path still succeeds after the auth and admin changes:

```text
code=200 total=0.040986
```

- Live relay runtime summary remains available through authenticated management API and still reports real standby values.
- Current admin-page standby pool values are live runtime data and currently reflect the fixed `8/8` window for the active tunnel.

### Local Validation Commands Used For Current Round

```bash
env -u GOOS -u GOARCH GOCACHE=/tmp/cloud-relay-gocache-final go test ./apps/server-api/internal/api ./apps/server-api/internal/store ./apps/relay-tcp/internal/runtime
npm --prefix apps/admin-web run build
psql postgres://postgres:wdblsw12138@127.0.0.1:5432/cloud_relay?sslmode=disable -f db/schema.sql
curl http://127.0.0.1:7710/api/auth/bootstrap-status
curl http://127.0.0.1:7710/admin/
curl http://127.0.0.1:7710/admin/assets/index-DH-GNW08.js
curl -s -o /dev/null -w 'code=%{http_code} total=%{time_total}
' http://82.156.236.104:10086
```
