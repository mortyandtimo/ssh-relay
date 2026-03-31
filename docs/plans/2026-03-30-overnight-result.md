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
- Management console is same-origin and served by `server-api` itself:
  - `http://82.156.236.104:7710/admin/`
- Management authentication is cookie-backed Web session auth with three roles:
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
- Access token implementation is currently a signed opaque payload, acceptable for current self-hosted scope but still a lightweight session design.
- HTTPS and dedicated subdomain deployment are still pending and remain the next hardening step before wider exposure.
- Bootstrap requires explicit secret distribution and manual operator knowledge during first install.
- Current CORS policy is explicit allowlist based; any future cross-origin development flow must be configured deliberately.

## Latest Deployment Verification

### Bootstrap And First Install Flow

- Current bootstrap secret is configured through:
  - `SERVER_API_ADMIN_BOOTSTRAP_SECRET`
- Current cloud value is set in `/etc/cloud-relay-platform/server-api.env`.
- `admin-web` bootstrap page now includes a Bootstrap Secret input.
- Current first-install flow is:
  1. Open `http://82.156.236.104:7710/admin/`
  2. If `bootstrap-status` returns `required=true`, fill:
     - email
     - display name
     - password
     - bootstrap secret
  3. Submit bootstrap
  4. On success, the page clears the bootstrap secret field and enters the logged-in management state
- Runtime bootstrap validation rules are now:
  - secret correct and bootstrap required -> `201`
  - secret missing or wrong -> `401`
  - secret correct but bootstrap already completed -> `409`

### Session Cookies

- Auth cookies now support configurable `Secure` behavior through:
  - `SERVER_API_AUTH_COOKIES_SECURE`
- Current cloud deployment keeps this disabled so existing HTTP deployment is not broken.
- Future HTTPS deployment can enable it without code changes.

### CORS

- CORS is no longer open by default and does not reflect arbitrary origin.
- Allowed origins are now configured through:
  - `SERVER_API_ALLOWED_ORIGINS`
- Current cloud allowlist is:
  - `http://82.156.236.104:7710`
  - `http://127.0.0.1:7710`
- Same-origin `/admin` continues to work without permissive cross-origin behavior.
- Non-allowlisted origin preflight no longer receives `Access-Control-Allow-Origin`.

### Admin Web Session Behavior

- Login issues Web session cookies:
  - `crp_access`
  - `crp_refresh`
  - `crp_session`
- Management API without login returns `401`.
- `admin-web` request layer now supports:
  - request returns `401`
  - frontend automatically `POST /api/auth/refresh`
  - refresh succeeds -> original request is retried automatically
  - refresh fails -> UI returns to login state
- Existing tunnel form state logic remains intact and is not overwritten by the refresh path.

### Reverse TCP Runtime

- Reverse TCP public path still succeeds after these auth/initialization changes:

```text
code=200 total=0.042746
```

- Live relay runtime summary remains available through authenticated management API and still reports real standby values.
- Current admin-page standby pool values are live runtime data and currently reflect the fixed `8/8` window for the active tunnel.

### Local Validation Commands Used For This Round

```bash
env -u GOOS -u GOARCH GOCACHE=/tmp/cloud-relay-gocache-round2c go test ./apps/server-api/internal/api ./apps/server-api/internal/store ./apps/relay-tcp/internal/runtime
npm --prefix apps/admin-web run build
```

### Cloud Validation Commands Used For This Round

```bash
curl -s -o /tmp/bootstrap_no_secret.json -w '%{http_code}' -H 'Content-Type: application/json' -d '{...}' http://127.0.0.1:7710/api/auth/bootstrap
cat /tmp/bootstrap_no_secret.json
curl -i -s -X OPTIONS -H 'Origin: http://evil.example.com' -H 'Access-Control-Request-Method: POST' http://127.0.0.1:7710/api/auth/login
curl -i -s -X OPTIONS -H 'Origin: http://82.156.236.104:7710' -H 'Access-Control-Request-Method: POST' http://127.0.0.1:7710/api/auth/login
curl -s http://127.0.0.1:7710/api/auth/bootstrap-status
curl -s -o /dev/null -w 'code=%{http_code} total=%{time_total}
' http://82.156.236.104:10086
```

### Admin Web Blank Page Fix (2026-03-31)

- Symptom on `http://82.156.236.104:7710/admin/`: background loaded but the console body stayed blank.
- Root cause: the admin SPA still had a login-state null dereference in `apps/admin-web/src/App.tsx`, where role-dependent rendering could touch `currentUser.role` before the unauthenticated guard returned the login screen.
- Fix: moved role-dependent rendering to use a safe `activeUser` fallback until `currentUser` is confirmed, so the login screen renders instead of crashing the React root.
- Deployment verification after rebuild:
  - `npm --prefix apps/admin-web run build` passed
  - `/admin/` now serves `dist/index.html` referencing `index-BHqO471Q.js` and `index-DluHM-uB.css`
  - local `curl http://127.0.0.1:7710/admin/` confirmed the updated entry HTML
- Operator note: browsers that cached the old HTML or old bundle such as `index-D713E6AO.js` may still show the old error until a hard refresh is performed.

### Node Metadata Enhancement (2026-03-31)

- Node management now has fixed classification fields exposed through the existing node API:
  - `nodeRole`: `cloud | local | third_party`
  - `environment`: `prod | test | dev`
  - `trustLevel`: `trusted | limited | external`
  - `owner`
  - `location`
  - `tags: string[]`
- These fields are currently persisted in `nodes.metadata` so the deployment does not require a PostgreSQL schema migration.
- Existing Windows agent compatibility remains unchanged:
  - agent register / heartbeat payload format is unchanged
  - existing three-machine联调 roles are not remapped by code
  - agent-reported base metadata such as `hostname / os / arch` still updates normally
- Important runtime rule:
  - management-defined node fields are no longer overwritten by a later agent re-register
  - agent register continues to refresh agent-owned metadata keys like `hostname / os / arch`
- Management API now supports:
  - `GET /api/nodes?nodeRole=...&environment=...&trustLevel=...&owner=...&tag=...`
  - `GET /api/nodes/{id}`
  - `PUT /api/nodes/{id}` for updating fixed classification fields and tags
- Admin web node workspace now supports:
  - nodeRole / environment / trustLevel / owner / tags filtering
  - visible grouped display by role without introducing a hard group tree
  - node detail editing for the fixed fields and tags

### Node Pagination And Tunnel Selection Refinement (2026-03-31)

- `/api/nodes` now supports server-side pagination through:
  - `limit`
  - `offset`
  - response fields: `items / total / limit / offset`
- Admin web node page now uses that pagination instead of rendering all nodes into one long page.
- Current admin-web node behavior:
  - default page size is `10`
  - node list shows current page range and total
  - node list uses previous / next page controls
  - node detail panel only shows when a node is selected
  - clicking the same selected node again clears the selection
- Current admin-web tunnel behavior:
  - unselected state shows the create-tunnel form
  - selected state shows tunnel editing context instead of keeping create/edit stacked as equal-weight forms
  - clicking the same selected tunnel again clears the selection and returns to create mode

### SOCKS5 Minimal Design (2026-03-31)

- SOCKS5 is implemented as a new tunnel type: `socks5`
- It is not modeled as a special `tcp` mode, because the control plane, audit path, node binding, public port allocation and admin UI can directly reuse the existing tunnel model with less ambiguity
- Current minimal runtime shape:
  - management plane creates a `socks5` tunnel bound to a node and a public port
  - TCP relay still provides the public TCP entrypoint
  - after reverse session start, the client-agent handles SOCKS5 on that stream instead of dialing a fixed target first
  - current SOCKS5 implementation supports CONNECT only
- Current non-goals for this version:
  - no UDP associate
  - no advanced authentication
  - no ACL / policy engine
  - no proxy chaining / transparent mode

### SOCKS5 Minimal Implementation Status (2026-03-31)

- Current tunnel semantics are now locked for `type=socks5`:
  - server-side create/update normalizes `targetHost=socks5`
  - server-side create/update normalizes `targetPort=1080`
  - admin-web no longer exposes normal TCP target editing fields for `socks5`
- Current client-agent SOCKS5 behavior:
  - supports version 5 CONNECT
  - rejects non-CONNECT commands
  - still does not implement UDP associate
- Current verification coverage:
  - server-api tests cover socks5 create/update normalization
  - server-api tests cover socks5 tunnel visibility in agent path and TCP route export
  - client-agent tests cover CONNECT success and non-CONNECT rejection
- Real cloud validation still requires replacing the running Windows `client-agent.exe` with the newly built binary:
  - `/root/cloud-relay-platform/deploy/bin/windows-amd64/client-agent.exe`

### SOCKS5 Real CONNECT Validation (2026-03-31)

- Windows client-agent was replaced with the newly built binary and reconnected successfully.
- Real public SOCKS5 CONNECT validation succeeded through the live cloud path:
  - public entry: `82.156.236.104:11080`
  - test command: `curl.exe --proxy socks5h://82.156.236.104:11080 https://example.com -I`
  - observed result: `HTTP/1.1 200 OK`
- This confirms the current minimal SOCKS5 chain is working for CONNECT:
  - admin-created `socks5` tunnel
  - server-api control plane
  - relay-tcp public TCP entry
  - updated Windows client-agent SOCKS5 handler
  - outbound target connect through SOCKS5 CONNECT

### SOCKS5 Usage Guidance (2026-03-31)

- Current suitable scenarios:
  - temporary outbound proxy access through TCP destinations
  - browser / curl / command-line tools that can use SOCKS5 CONNECT
  - lightweight operator-facing proxy entry managed from the existing tunnel console
- Current unsuitable scenarios:
  - UDP-based applications
  - environments requiring fine-grained ACLs or identity-aware authorization
  - advanced enterprise proxy features such as multi-hop chaining or transparent proxying
- Minimal usage examples:
  - curl:
    - `curl.exe --proxy socks5h://82.156.236.104:11080 https://example.com -I`
  - PowerShell:
    - directly run the same `curl.exe` command in PowerShell
  - Browser:
    - configure a SOCKS5 proxy pointing to `82.156.236.104:<publicPort>`
    - current expectation is TCP web access only; UDP-based browser features are outside scope
