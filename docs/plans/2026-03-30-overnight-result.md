# Overnight Result

## What Changed

- Tightened `relay-tcp` standby pool handling in `apps/relay-tcp/internal/runtime/runtime.go`.
- Added standby connection age tracking and bounded eviction behavior instead of treating a full queue as an unconditional hard reject.
- Added clearer relay logs for:
  - standby admitted with `poolSize`
  - stale standby discarded during pairing
  - expired standby eviction
  - pairing lifecycle summaries
- Preserved the existing node-scoped matching key of `nodeId + publicPort` with strict tunnel hello validation.
- Kept the current control plane and native deployment layout unchanged:
  - `server-api` on `:7710`
  - env under `/etc/cloud-relay-platform`
  - binaries under `/opt/cloud-relay-platform/bin`
  - no Docker regression

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

## What Still Fails Or Remains Risky

- The currently running Windows agent process is still an old runtime shape from earlier testing history. It can refill the pool and successfully serve traffic, but the cloud side has previously observed stale standby entries and long-lived queue buildup.
- Tonight's cloud-side change improves pool admission behavior and observability, but it does not yet introduce a fully adaptive or tunnel-specific standby pool policy.
- Because the Windows agent was treated as fixed tonight, this result should be considered a cloud-side stabilization step, not the final completed design for long-term pool management.
- A longer soak test is still useful, but the morning retest now proves the current online Windows agent can pair successfully with the bounded standby pool implementation.

## Windows-Side Restart Requirement

- **Not required for tonight's cloud-side verification.**
- The currently running Windows agent was sufficient to verify that the updated cloud-side relay can restart, refill the standby pool to the bounded target, pair traffic successfully, and continue serving the existing tunnel.
- Tomorrow's manual Windows-side restart or redeploy is still recommended if a newer agent build is desired for additional noise reduction or future protocol work, but it was not required for the validation recorded here.

## Final Evidence Summary

- Verified tunnel path remains `82.156.236.104:10086 -> node-1774805183388699102 -> 127.0.0.1:16354`.
- Cloud-side relay restart did not break compatibility with the currently running Windows agent.
- Pool behavior after redeploy was measurable, bounded to the intended standby window during the morning retest, and diagnosable via logs.
