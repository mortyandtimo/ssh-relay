# Control V1 Productization Hardening

Operational reference: see [`docs/operations/control-v1-operator-guide.md`](../operations/control-v1-operator-guide.md).

## Action Matrix

| Action          | Target | Execute mode                                                                           | Real side effect                          |
| --------------- | ------ | -------------------------------------------------------------------------------------- | ----------------------------------------- |
| `restart_agent` | node   | `real` when local managed restart policy passes, otherwise `placeholder` or `rejected` | `systemctl restart <serviceUnit>`         |
| `isolate_node`  | node   | `real`                                                                                 | update node isolated state in store       |
| `release_node`  | node   | `real`                                                                                 | update node isolated state in store       |
| `pause_tunnel`  | tunnel | `real`                                                                                 | update tunnel status to `paused` in store |
| `resume_tunnel` | tunnel | `real`                                                                                 | update tunnel status to `active` in store |

## Preview Boundary

- `snapshot`, `panel`, `options`, and `dryRun` stay side-effect free.
- They only preview whether a later execute would be `real`, `placeholder`, `blocked`, or likely rejected.
- Real runner or store mutation only happens on `dryRun=false` execute requests.

## Restart Environment

Real `restart_agent` needs these server-api environment variables:

- `SERVER_API_CONTROL_REAL_RESTART_ENABLED=true`
- `SERVER_API_CONTROL_LOCAL_NODE_ID=<node-id>`
- `SERVER_API_CONTROL_RESTART_SERVICE_PREFIX=cloud-relay-client-agent@`
- `SERVER_API_CONTROL_RESTART_TIMEOUT_SEC=15`

If any policy requirement is not satisfied, restart falls back to policy rejection or placeholder preview semantics instead of widening shell access.

## Execute Safety Contract

- `dryRun=true` never calls the executor.
- `preflight blocked` execute never calls the executor.
- `requestedAt` is treated as the control-context timestamp that the user is acting on.
- Execute rejects stale control context with `policy_rejected` when target control state changed after that timestamp.
- Drift rejection returns a refresh-oriented human message and execution note.

## Audit Contract

Control execute audit actions:

- `control_execute_accepted`
- `control_execute_placeholder_accepted`
- `control_execute_policy_rejected`
- `control_execute_retryable_failure`
- `control_execute_non_retryable_failure`

Stable payload fields used for filtering and diagnosis:

- `actionKind`
- `sourceSurface`
- `executionMode`
- `placeholderOnly`
- `result`
- `outcome`
- `rejectionKind`
- `recommendedAction`
- `readinessState`
- `targetStateBefore`
- `targetStateAfter`
- `primaryReasonCode`
- `humanMessage`

Audit query helpers exposed by `/api/audit-logs`:

- `action=<exact action>`
- `actionPrefix=<prefix match>`
- `outcome=<payload.outcome>`
- `resourceType=<type>`
- `resourceID=<id>`
- `actorType=<type>`
- `actorID=<id>`

## Validation Commands

Use native Linux defaults from the current shell:

```bash
go test ./apps/server-api/internal/api
npm --prefix apps/node-console run build
npm --prefix apps/operator-console run build
npm --prefix apps/admin-web run build
```

## Known Boundaries

- `restart_agent` is still the only shell/system command path.
- `isolate_node`, `release_node`, `pause_tunnel`, and `resume_tunnel` are real store/control-plane mutations, not shell commands.
- Drift rejection currently relies on reusing the latest control-context timestamp in execute requests.
- `apps/desktop-console` is intentionally outside this hardening pass.
