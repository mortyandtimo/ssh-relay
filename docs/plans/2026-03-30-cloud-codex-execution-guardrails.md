# Cloud Codex Execution Guardrails

## Purpose

This document is the authoritative execution boundary for the current overnight development cycle on branch `feat/reverse-tcp-channel`.

The cloud-side Codex must read this before continuing implementation or deployment work.

## Current Reality

1. Cloud control plane is healthy:
   - `server-api` is running natively on `:7710`
   - backing store is PostgreSQL
2. A real Windows node is already online:
   - node name: `my-windows-pc`
3. The Windows node's current `client-agent.exe` process is already running and registered.
4. There will be **no guaranteed human intervention tonight** on the Windows side.

## Hard Boundary

### The Windows agent process must be treated as fixed for this overnight run.

This means:

- Do not assume the operator will manually restart the Windows agent tonight.
- Do not assume the operator will replace the executable tonight.
- Do not require changing Windows environment variables tonight.
- Do not rely on a new agent binary unless the same currently running process would still remain compatible enough for validation.

## What This Means For Development

The cloud-side Codex may still change the protocol or implementation, but tonight's validation must respect this rule:

### Only validate against behavior that can work with the currently running Windows agent.

If a new protocol handshake or field becomes mandatory and would require restarting or replacing the Windows agent, that change may still be implemented, but:

- it must be clearly documented as **not verifiable tonight**
- it must not be falsely claimed as completed end-to-end
- the result note must explicitly state that a fresh Windows agent deployment is required tomorrow

## Allowed Validation Tonight

These are valid overnight outcomes:

1. Stabilize cloud-side reverse connection lifecycle management.
2. Eliminate or reduce `standby pool full` rejection spam.
3. Improve pairing, eviction, and standby reuse behavior on the cloud relay.
4. Add logs and metrics that make the reverse path diagnosable.
5. Prove that the cloud side correctly handles the currently running Windows agent as far as compatibility allows.

## Not Allowed As False Claims

Do not claim success for any of the following unless a real end-to-end test succeeds with the currently running Windows agent:

1. `10086 -> Windows local service` fully works.
2. Reverse TCP relay is fully complete.
3. Windows local service exposure is verified.

If these still require a Windows-side restart or binary replacement, the result note must say so plainly.

## Required Testing Standard

### Minimum acceptable verification tonight

1. `cloud-relay-server-api` remains healthy on `:7710`
2. `cloud-relay-tcp` starts cleanly
3. Current Windows node `my-windows-pc` remains present in `nodes`
4. Reverse standby connection management is measurably improved or clearly diagnosed
5. Final logs show the exact state of reverse pairing and failure mode, if not fully successful

### If full end-to-end success is not possible tonight

The cloud-side Codex must produce a precise result note including:

- whether the current running Windows agent is protocol-compatible with the latest cloud code
- whether a Windows agent restart is required tomorrow
- what exact next manual step is needed on Windows
- what exact SQL / service / log evidence supports that conclusion

## Required Deliverables Before Stopping

1. Commit code changes to `feat/reverse-tcp-channel`
2. Push to `origin/feat/reverse-tcp-channel`
3. Write `docs/plans/2026-03-30-overnight-result.md`

That result document must include:

- changed files
- verification commands
- observed results
- unresolved risks
- whether tomorrow requires Windows-side manual restart / redeploy

## Priority Order

1. Correctness of reverse connection lifecycle
2. Accurate logging and diagnosis
3. Safe cloud-side deployment
4. Honest result documentation
5. Only then any extra cleanup

## Final Instruction

If there is any conflict between "shipping a bigger change" and "keeping tonight's validation truthful", choose truthful validation.

