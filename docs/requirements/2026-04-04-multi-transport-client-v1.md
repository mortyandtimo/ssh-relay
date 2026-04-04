# Multi-Transport Client V1

## Confirmed Architecture

1. Linux cloud host continues to own control-plane APIs and relay / coordination services.
2. The 24h Windows machine will later install a single Windows desktop application.
3. Management-side usage will also use the same desktop application, switching only into operator mode.
4. The existing `admin-web` remains in place as the current management plane and transition tool.
5. The existing `apps/client-agent/` remains the background runtime and is not renamed in this phase.

## Naming Correction

- Do not split into `provider-app/` and `operator-console/`.
- The future desktop application is a single codebase:
  - `apps/desktop-console/`
- The desktop application will later expose two modes:
  - `local-node mode`
  - `operator mode`
- `desktop-console` is expected to host/configure/visualize `client-agent`; it does not replace the runtime in this phase.

## Cloud Phase 1 Scope

This phase is cloud-side only.

Required in this phase:
- Add runtime path / runtime state / last failure reason fields to shared tunnel types.
- Expose those fields through `server-api` / store as real returnable control-plane fields.
- Keep configuration and runtime state explicitly separated.

Not in scope for this phase:
- No Windows desktop code yet.
- No P2P data-plane.
- No NAT traversal.
- No ICE / STUN / TURN.
- No complex peer handshake.

## Semantic Guardrails

- `type` expresses protocol class.
- `transportPolicy` expresses desired transport preference.
- `runtimePath` expresses the currently observed runtime path.
- `runtimeState` expresses whether runtime is active / pending / unavailable.
- `lastFailureReason` expresses the latest control-plane/runtime-level explanation, if any.

These fields must never imply that:
- `transportPolicy=p2p_preferred` means P2P data-plane is already active.
- A future-preferred path is the same thing as current runtime path.

## Expected Phase 1 Outcome

After this phase, the cloud control-plane should be able to return tunnel records where:
- configuration intent is visible
- current runtime path is visible
- current runtime state is visible
- the last known failure reason is visible

This is a control-plane / ops visibility phase only.
