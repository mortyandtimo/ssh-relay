# Multi-Transport Client V1 Plan

## Phase Layout

### Phase 0
- Keep existing Linux cloud control-plane and relays running.
- Keep `admin-web` as the current operations surface.
- Keep `client-agent` as the current background runtime.

### Phase 1
- Cloud-side only.
- Add tunnel runtime-path state fields into shared types and control-plane responses.
- Keep runtime and configuration semantics separate.

### Later Phase
- Introduce a single Windows desktop application under `apps/desktop-console/`.
- Support both `local-node mode` and `operator mode` in the same desktop application.
- Let the desktop application host/configure/visualize `client-agent` instead of replacing it.

## Phase 1 Tasks

1. Shared protocol types
- Add `runtimePath`.
- Add `runtimeState`.
- Add `lastFailureReason`.

2. Store hydration
- Persist / hydrate these fields through tunnel metadata.
- Return them from memory and postgres stores.

3. Server API behavior
- Return these fields from existing tunnel endpoints.
- Preserve the current rule that `transportPolicy` is only desired configuration.

4. Tests
- Verify `type` and `transportPolicy` remain independent.
- Verify `transportPolicy` and `runtimePath` remain independent.
- Verify a tunnel configured with `p2p_preferred` is not falsely reported as currently running over P2P unless runtime fields say so.

## Non-Goals In This Round

- No desktop implementation.
- No relay runtime rewrite.
- No P2P data-plane.
- No NAT traversal.
- No full peer orchestration engine.

## Exit Criteria For Phase 1

- Tunnel responses can show desired transport policy.
- Tunnel responses can separately show current runtime path/state.
- Tunnel responses can separately show the last failure reason.
- Tests prove configuration intent is not confused with runtime fact.
