# Control V1 Operator Guide

## Scope

This guide describes the current `control-v1` contract as implemented in `server-api`, `node-console`, `operator-console`, and `admin-web`.

- `snapshot`, `panel`, `options`, and `dryRun` remain side-effect free.
- Real side effects only happen on `dryRun=false` execute requests.
- `restart_agent` is the only shell/system command path.
- `isolate_node`, `release_node`, `pause_tunnel`, and `resume_tunnel` perform real store/control-plane state mutations instead of shell commands.

## Action Matrix

| Action | Target | Real execute path | Placeholder or reject boundary |
| --- | --- | --- | --- |
| `restart_agent` | `node` | `systemctl restart <serviceUnit>` after local managed-node policy passes | Falls back to `placeholder` preview or `policy_rejected` when local restart policy is not satisfied |
| `isolate_node` | `node` | Update node `isolated=true` in store | Rejected when preflight or context version blocks it |
| `release_node` | `node` | Update node `isolated=false` in store | Rejected when preflight or context version blocks it |
| `pause_tunnel` | `tunnel` | Update tunnel `status=paused` in store | Rejected when preflight or context version blocks it |
| `resume_tunnel` | `tunnel` | Update tunnel `status=active` in store | Rejected when preflight or context version blocks it |

## Execute Outcome Matrix

| `executeOutcome` | `result` | `rejectionKind` | Meaning | Audit action | Operator guidance |
| --- | --- | --- | --- | --- | --- |
| `accepted_real` | `accepted` | `""` | Real executor or real store mutation accepted the action | `control_execute_accepted` | Refresh target state and audit view to confirm the side effect |
| `accepted_placeholder` | `accepted` | `""` | Request stayed inside the placeholder boundary and did not perform a real side effect | `control_execute_placeholder_accepted` | Treat as contract preview only; do not assume the target changed |
| `blocked_preflight` | `blocked` | `""` | Execute was blocked by current preflight state before any executor call | no execute audit | Fix the blocking reason first |
| `policy_rejected` | `rejected` | `state_drift` | The target state changed after the operator captured the context version | `control_execute_policy_rejected` | Refresh panel or action options and retry from fresh context |
| `policy_rejected` | `rejected` | `duplicate_inflight` | Same target/action/source/context is already executing | no execute audit | Wait for the in-flight result; do not repeat the same click |
| `policy_rejected` | `rejected` | `duplicate_handled` | Same target/action/source/context was already handled and cached as complete | no execute audit | Refresh first, then decide whether a new context should be executed |
| `policy_rejected` | `rejected` | other or empty | Execution policy rejected the action for non-drift reasons | `control_execute_policy_rejected` | Read `preflight.blockedReasons`, `humanMessage`, and audit payload |
| `retryable_failure` | `rejected` | `""` | Execution reached the real or placeholder boundary but failed in a retryable way | `control_execute_retryable_failure` | Retry later; same context is intentionally not cached as done |
| `non_retryable_failure` | `rejected` | `""` | Execution failed and should not be retried without fixing environment or policy | `control_execute_non_retryable_failure` | Fix the environment or policy first |

Stable machine fields returned to UI layers:

- `result`
- `executionMode`
- `placeholderOnly`
- `executeOutcome`
- `rejectionKind`
- `nextStep`
- `preflight.blockedReasons`
- `facts.controlStateUpdatedAt`

## Context Version Contract

The control context version is a server-generated fact, not a client-local timestamp.

- `panel`, `options`, and `dryRun` responses expose the same server-side version through `contextVersion` and `facts.controlStateUpdatedAt`.
- Execute requests must echo that server-provided value back through `requestedAt`.
- The frontend must not substitute `new Date().toISOString()` for `requestedAt`.

Current server-side version rules:

- Node `controlStateUpdatedAt` does not advance on heartbeat or `lastSeen` refresh alone.
- Node `controlStateUpdatedAt` advances only on control-relevant state changes, such as isolation changes.
- Tunnel `controlStateUpdatedAt` comes from tunnel metadata, not `updatedAt`.
- Tunnel `controlStateUpdatedAt` currently advances on control-relevant tunnel status changes; non-status edits such as name, target, domain, or TLS configuration do not change the context version.

When execute sees a newer server-side context version than the submitted `requestedAt`, it returns:

- `executeOutcome=policy_rejected`
- `rejectionKind=state_drift`
- refresh-oriented `humanMessage`
- refresh-oriented `nextStep`

## Duplicate And Retry Semantics

Duplicate protection is keyed by:

- `targetKind`
- `targetId`
- `actionKind`
- `sourceSurface`
- server-provided context version

Current behavior:

- Same context while execution is still active returns `duplicate_inflight`.
- Same context after a cached completed result returns `duplicate_handled`.
- Cached done results currently include `accepted_real`, `accepted_placeholder`, `policy_rejected`, and `non_retryable_failure`.
- `retryable_failure` is intentionally not cached as done, so the same context can retry the real path again.

## Restart Environment Requirements

Real `restart_agent` requires these `server-api` environment variables:

```env
SERVER_API_CONTROL_REAL_RESTART_ENABLED=true
SERVER_API_CONTROL_LOCAL_NODE_ID=<node-id>
SERVER_API_CONTROL_RESTART_SERVICE_PREFIX=cloud-relay-client-agent@
SERVER_API_CONTROL_RESTART_TIMEOUT_SEC=15
```

If any restart policy requirement fails, the system does not widen shell access. It stays inside `placeholder` preview semantics or returns `policy_rejected`.

## Audit Contract

Current execute audit actions:

- `control_execute_accepted`
- `control_execute_placeholder_accepted`
- `control_execute_policy_rejected`
- `control_execute_retryable_failure`
- `control_execute_non_retryable_failure`

Stable audit payload fields used for triage:

- `actionKind`
- `sourceSurface`
- `executionMode`
- `placeholderOnly`
- `result`
- `outcome`
- `rejectionKind`
- `nextStep`
- `recommendedAction`
- `readinessState`
- `targetStateBefore`
- `targetStateAfter`
- `primaryReasonCode`
- `humanMessage`

Current `/api/audit-logs` filter parameters relevant to control execute triage:

- `action`
- `actionPrefix`
- `outcome`
- `rejectionKind`
- `executionMode`
- `placeholderOnly`
- `resourceType`
- `resourceID`
- `actorType`
- `actorID`

Useful audit queries:

```text
/api/audit-logs?actionPrefix=control_execute_&outcome=policy_rejected&rejectionKind=state_drift&resourceID=node-123
/api/audit-logs?actionPrefix=control_execute_&outcome=accepted_placeholder&executionMode=placeholder&placeholderOnly=true
/api/audit-logs?actionPrefix=control_execute_&outcome=retryable_failure&resourceID=tunnel-abc
```

`admin-web` now exposes the three stable triage filters directly:

- `rejectionKind`
- `executionMode`
- `placeholderOnly`

The audit table also prefers payload `nextStep` over falling back to `humanMessage`.

## Troubleshooting

### Placeholder accepted

- Check `executeOutcome=accepted_placeholder`.
- Confirm `executionMode=placeholder` and `placeholderOnly=true`.
- This means the request was accepted contractually, but no real side effect happened.

### Stale context rejection

- Check `executeOutcome=policy_rejected` and `rejectionKind=state_drift`.
- Refresh panel or action options first.
- Re-execute only after the UI receives a newer `contextVersion`.

### Duplicate in-flight rejection

- Check `rejectionKind=duplicate_inflight`.
- Wait for the first request to complete.
- Do not spam the same action under the same context version.

### Duplicate handled rejection

- Check `rejectionKind=duplicate_handled`.
- The same context already completed and was cached as done.
- Refresh first before deciding whether another action is still needed.

### Retryable failure

- Check `executeOutcome=retryable_failure`.
- Review `executionNotes`, `humanMessage`, and matching audit rows.
- Same context can retry again because this outcome is not cached as handled.

### Non-retryable failure

- Check `executeOutcome=non_retryable_failure`.
- Review environment or policy prerequisites before retrying.
- If it is a restart path, confirm the service unit and restart policy inputs first.

### Preflight blocked

- Check `result=blocked` and `preflight.blockedReasons`.
- This path does not call the executor and does not write execute audit rows.

## Standard Validation Commands

Use the native Linux defaults from the current shell:

```bash
go test ./apps/server-api/internal/api
npm --prefix apps/node-console run build
npm --prefix apps/operator-console run build
npm --prefix apps/admin-web run build
```

If a round changes `packages/desktop-core`, also run:

```bash
npm --prefix packages/desktop-core run test
```

## Known Boundaries And Unfinished Items

- `restart_agent` remains the only shell/system command path.
- `apps/desktop-console` is still outside the current hardening pass.
- No batch execute flow exists yet.
- No persistent control history timeline exists beyond the current audit log.
- Audit triage is now mostly contract-driven, but some admin-web display labels still keep local short-form mappings for readability.
