# Reverse TCP Standby Pool Design (First Elastic Version)

## Background

We now have a working reverse TCP relay path:

- `server-api` exposes `/internal/routes/tcp` and `/agent/tunnels`.
- `relay-tcp` pulls `TunnelSpec` and exposes public TCP listeners.
- `client-agent` registers the node and opens reverse upgraded connections back to `relay-tcp`.
- Public clients connect to cloud `publicPort` and get proxied to the Windows local TCP service.

The current reverse standby pool implementation in `apps/relay-tcp/internal/runtime/runtime.go` is a **fixed-shape, best-effort** design:

- Each pool is keyed by `nodeId + publicPort` via `routePoolKey`.
- The underlying queue is a buffered `chan standbyConn` of size `standbyPoolBufferSize = 64`.
- There is a nominal `standbyPoolTargetSize = 8`.
- The pool performs some eviction when full and drops obviously expired items.

This has been enough to validate the protocol and basic path, but it is **not** yet a robust or elastic design for multi-tunnel, multi-user load.

## Current Problems (As Of 6a80607)

1. **Target vs. physical limit mismatch**

   - Code maintains a `standbyPoolTargetSize = 8` but the actual channel capacity is `64`.
   - `enqueueStandbyConn` uses `len(queue) >= standbyPoolTargetSize` as a soft signal but still has code paths where new items can be admitted beyond that target.
   - Under concurrent admission/consumption, the pool size can drift toward the full channel capacity instead of staying near 8.

2. **No per-tunnel or global budget awareness**

   - Each `nodeId + publicPort` key has its own pool.
   - If many tunnels are configured, total standby connections can grow linearly without a global cap.
   - There is no awareness of machine capacity or total relay budget.

3. **No real elasticity**

   - Agent-side worker count is fixed by `AGENT_REVERSE_POOL_SIZE` (default 2).
   - Relay-side pool tries to keep up to 8 items but does not:
     - Scale with observed traffic
     - Shrink gracefully during idle periods
   - The result is a static prewarm model, not an elastic pool.

4. **Insufficient test coverage for pool behavior**

   - Existing tests cover:
     - Basic forward path via a single reverse connection.
     - Rejection of inactive tunnels.
     - Failure behavior when no standby exists.
   - They do **not** cover:
     - Pool size caps under concurrent producers.
     - Eviction policy and age-based cleanup.
     - Route resync and pool draining behavior.

## Design Goals

We want an incremental, backwards-compatible evolution of the standby pool toward a more elastic model.

**Hard goals for this iteration:**

1. Keep the current protocol and public API stable.
2. Bound per-tunnel standby connections **strictly**, not just heuristically.
3. Avoid unbounded total standby growth when many tunnels are configured.
4. Preserve compatibility with the current Windows agent process (no breaking handshake changes).

**Stretch goals (can be partially implemented):**

5. Make the pool size adaptive to recent traffic per tunnel.
6. Provide explicit metrics/logs for pool occupancy and eviction reasons.

## Target Model

### Per-tunnel pool parameters

For each `(nodeId, publicPort)` we define:

- `minStandby`: minimum standby connections to keep warm.
- `targetStandby`: current desired steady-state size.
- `maxStandby`: hard upper bound for that pool.

Initial defaults (for the first elastic version):

- `minStandby = 1`
- `targetStandby = 2`
- `maxStandby = 16`

These numbers are deliberately conservative and can be tuned later.

### Global budget

We define a soft global budget for total standby connections on the relay process, e.g.:

- `globalMaxStandby = 200` (tunable via env, but not required in the first patch).

The runtime keeps a simple atomic counter of currently admitted standby connections across all pools. When this counter is at or above `globalMaxStandby`, new standby admissions should:

- Prefer to admit for hot tunnels (heavier recent traffic).
- Fall back to rejecting for cold tunnels.

In the first version we can implement a simpler behavior:

- Always admit if `globalStandbyCount < globalMaxStandby`.
- Otherwise, reject new standby with a clear log line.

### Elastic behavior

1. **Expansion:**

   - If a pool experiences frequent `no standby reverse connection` events for a given tunnel, we gradually increase `targetStandby` up to `maxStandby` for that tunnel.
   - The signal can be collected in `handlePublicConnection` when `acquireStandbyConn` times out.

2. **Contraction:**

   - Each standby connection already has an age via `registeredAt`.
   - We keep `standbyConnMaxAge` (e.g. 90s) as an upper bound.
   - In addition, if a pool is consistently above `targetStandby` and the extra items stay unused for a configurable idle window (e.g. 30s), we allow them to be evicted earlier.

3. **Route changes:**

   - When `/internal/routes/tcp` removes a route or changes the `nodeId/publicPort/target` combination, we drain and delete the associated pool via `drainPool`.
   - This is already in place; we only need to ensure it stays correct as we add more bookkeeping.

## Implementation Plan

This iteration will **not** introduce full-blown controller logic. Instead, we will:

1. Keep `standbyPoolBufferSize = 64` as the channel capacity.
2. Treat `standbyPoolTargetSize` as `maxStandby` for now and enforce it strictly per key.
3. Add a simple global counter for total standby connections.
4. Tighten `enqueueStandbyConn` so that:
   - `len(queue)` never grows beyond `standbyPoolTargetSize` for a given key (modulo in-flight operations).
   - global standby count never grows beyond a configured limit if we have one.

### Step 1: Per-key hard cap

We will adjust `enqueueStandbyConn` to follow a simpler, stricter rule:

- Before admitting a new `standbyConn` into the queue, check:
  - If `len(queue) >= standbyPoolTargetSize`, we **must** evict something or reject.
- Eviction policy when full:
  - Prefer evicting an expired connection if available.
  - If no expired item is available, evict the oldest one.
- After eviction, if the queue is still full (due to races), we reject the new item.

The key requirements for this step:

- After `enqueueStandbyConn` returns success, `len(queue)` should be `<= standbyPoolTargetSize` for that key.
- Logging must distinguish between:
  - eviction of expired standby
  - eviction of non-expired standby due to capacity pressure
  - outright rejection of new standby because the pool is still full

### Step 2: Global counter (optional but recommended)

We introduce a package-level `int64` counter `totalStandby` updated atomically:

- On successful enqueue: `atomic.AddInt64(&totalStandby, 1)`.
- On dequeue or eviction: `atomic.AddInt64(&totalStandby, -1)`.

We also define a constant or env-driven value `globalMaxStandby` (e.g. 200).

Admission rule becomes:

- If `globalMaxStandby > 0` and `atomic.LoadInt64(&totalStandby) >= globalMaxStandby`, reject new standby for cold tunnels with a specific log message.
- For the first iteration we can keep the behavior simple and conservative (e.g. always reject when above global max).

### Step 3: Telemetry hooks

We extend logging in `enqueueStandbyConn`, `acquireStandbyConn`, and `handlePublicConnection` to include:

- pool key (`nodeId:publicPort`)
- pool length after admission/eviction
- `totalStandby` value (sampled, not necessarily exact)

This will allow us to see, in real logs, whether the pool is respecting the configured bounds.

## Testing Plan

We will extend `apps/relay-tcp/internal/runtime/runtime_test.go` with additional tests. At minimum:

1. **Per-key cap enforcement**

   - Construct a `Service` with a synthetic pool key.
   - Inject N standby connections greater than `standbyPoolTargetSize` from multiple goroutines.
   - Assert that `len(queue)` for that key never exceeds `standbyPoolTargetSize` after the dust settles.

2. **Expired eviction preference**

   - Insert a mix of expired and fresh `standbyConn` entries.
   - Force an admission when the pool is full.
   - Assert via logs or mocks that expired items are evicted first.

3. **Route drain behavior**

   - Create a route, admit some standby connections, then remove the route via `syncRoutes`.
   - Verify that `drainPool` is called and all associated standby connections are closed.

4. **Global counter sanity** (if implemented in this iteration)

   - Start with zero pools.
   - Admit and drain connections across multiple keys.
   - Assert that `totalStandby` returns to zero at the end of the test.

We intentionally keep these tests focused on local behavior of `enqueueStandbyConn` and pool management, not on full end-to-end TCP forwarding.

## Compatibility and Rollout

- The protocol between `client-agent` and `relay-tcp` does not change in this iteration.
- No changes are made to `server-api` or the PostgreSQL schema for this step.
- The currently running Windows agent process remains compatible, as its behavior (how many reverse connections it opens) is unchanged.
- Rollout steps remain the same as in the existing docs:
  - build Linux binaries
  - stop `cloud-relay-server-api` and `cloud-relay-tcp`
  - install updated `relay-tcp`
  - restart services

## Future Work (Beyond This Iteration)

If this iteration proves stable, the next steps can include:

1. **Truly adaptive `targetStandby` per tunnel**

   - Track recent request rate and concurrency per tunnel.
   - Adjust `targetStandby` periodically based on these metrics.

2. **Agent-side coordination**

   - Allow `server-api` or `relay-tcp` to expose a recommended pool size per tunnel.
   - Let `client-agent` adjust `AGENT_REVERSE_POOL_SIZE` dynamically to align supply with demand.

3. **More precise lifetime management**

   - Track idle time vs. absolute age for standby connections.
   - Evict long-idle but non-expired connections more aggressively when under pressure.

4. **Persistent configuration**

   - Store per-tunnel pool hints (e.g. min/target/max) in PostgreSQL via `tunnels.metadata`.

For now, this document defines the **first elastic version** scope: strict per-key caps and an optional global budget, without overcomplicating the implementation.
