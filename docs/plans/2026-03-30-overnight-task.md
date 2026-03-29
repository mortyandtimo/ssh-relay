# Overnight Task For Cloud Codex

## Goal

Continue autonomous development on branch eat/reverse-tcp-channel and finish a stable reverse TCP relay path so a Windows node can expose a local TCP service through the cloud relay.

## Current Known State

- Control plane is healthy on :7710.
- PostgreSQL backing store works.
- Windows client-agent.exe can register and heartbeat.
- Reverse TCP relay code exists but is not stable yet.
- Current log evidence shows a concrete issue:
  - standby reverse connection rejected ... standby pool full
- Cloud deployment is native Linux binaries + systemd, not Docker.

## Hard Constraints

1. Do not switch cloud deployment back to Docker.
2. Keep server-api on :7710.
3. Keep using /etc/cloud-relay-platform/*.env and /opt/cloud-relay-platform/bin.
4. Continue on branch eat/reverse-tcp-channel.
5. Commit and push all completed work to Gitee before stopping.
6. Do not leave the repo dirty unless blocked and explicitly documented.

## Primary Objectives

### 1. Stabilize reverse connection pool behavior

Investigate and fix why standby reverse connections are being rejected with standby pool full.

Expected outcome:
- standby connection pool is bounded correctly
- stale or duplicate reverse connections are evicted cleanly
- reconnect storms from agent side do not poison the pool

### 2. Complete end-to-end reverse TCP tunnel flow

Expected target behavior:
- Windows client-agent opens reverse relay connections back to cloud elay-tcp
- cloud public port accepts inbound client connections
- elay-tcp pairs inbound public connection with an available reverse agent connection
- traffic is forwarded to the Windows local TCP service
- connection closes cleanly without hanging sockets

### 3. Make relay selection and tunnel ownership deterministic

Expected outcome:
- tunnel queries are node-scoped where needed
- relay-tcp maps standby connections by 
odeId + publicPort or an equally strict key
- no accidental cross-node or cross-tunnel reuse

### 4. Improve observability

Add or refine logs so these phases are visible and easy to debug:
- reverse connection created
- reverse connection admitted / rejected
- standby pool size
- public connection accepted
- pairing succeeded / timed out
- bytes copied or connection lifecycle summary

### 5. Verify on the real cloud server workflow

Use the native deployment path only:
- build Linux binaries
- copy to /opt/cloud-relay-platform/bin
- restart cloud-relay-server-api and cloud-relay-tcp
- verify with current Windows node

## Suggested Work Sequence

1. Read these files first:
   - docs/plans/2026-03-30-cloud-codex-handoff.md
   - current reverse relay related files under pps/relay-tcp, pps/client-agent, pps/server-api, packages/protocol
2. Inspect current logs for reverse tunnel failures.
3. Fix pool admission / stale connection handling first.
4. Then verify pairing and byte-forwarding behavior.
5. Only after code is stable, rebuild and redeploy binaries on the cloud server.
6. Run a real validation against the current Windows node.

## Verification Targets

At minimum verify all of these before claiming success:

1. curl http://127.0.0.1:7710/healthz
2. systemctl status cloud-relay-server-api --no-pager
3. systemctl status cloud-relay-tcp --no-pager
4. Windows node remains visible in 
odes
5. A real tunnel entry exists for the Windows node
6. Inbound traffic to the chosen public port is forwarded successfully to the Windows local service
7. Logs no longer show uncontrolled standby pool full rejection spam

## Deployment Commands

Use this exact cycle after code changes:

`ash
cd /root/cloud-relay-platform
git pull --rebase origin feat/reverse-tcp-channel
chmod +x deploy/linux/scripts/*.sh
bash deploy/linux/scripts/build-linux-binaries.sh

systemctl stop cloud-relay-server-api
systemctl stop cloud-relay-tcp 2>/dev/null || true

cp /root/cloud-relay-platform/deploy/bin/linux-amd64/* /opt/cloud-relay-platform/bin/
chmod +x /opt/cloud-relay-platform/bin/*
chown -R relay:relay /opt/cloud-relay-platform

systemctl start cloud-relay-server-api
systemctl start cloud-relay-tcp
`

## Required Deliverables Before Stop

1. Code committed locally
2. Code pushed to origin/feat/reverse-tcp-channel
3. A short written status note saved to:
   - docs/plans/2026-03-30-overnight-result.md
4. The result note must include:
   - what changed
   - what was verified
   - what still fails or remains risky
   - exact commands/logs for the final verification

## If Fully Completed

If reverse TCP relay becomes stable tonight, then as a stretch goal:
- clean up temporary or invalid tunnel records
- document the exact minimal procedure for bringing a fresh Windows node online
- keep services in the working state

## If Blocked

If blocked, do not thrash.
Instead:
- capture the precise blocker
- save evidence in docs/plans/2026-03-30-overnight-result.md
- commit any useful instrumentation or partial fixes
- push the branch anyway

