# Cloud Relay Platform Requirement Document

## Goal

Build a self-hosted cloud relay platform that lets users expose local services through a cloud server while managing nodes, tunnels, and server health through a unified control plane.

## Deliverables

- A cloud control plane service (`server-api`)
- Protocol-specific relay services (`relay-tcp`, `relay-http`, `relay-https`)
- A local node client (`client-agent`)
- A web management console (`admin-web`)
- PostgreSQL schema and deployment scaffolding
- Governed runtime artifacts for future implementation waves

## Functional Requirements

1. Nodes must be able to register against the cloud server with stable identifiers and capability metadata.
2. Nodes must periodically send heartbeats and expose basic machine metrics.
3. The control plane must list online nodes and aggregate server status.
4. Tunnel objects must support `tcp`, `http`, `https`, and reserve `udp` for later implementation.
5. Tunnel definitions must include ingress settings, target settings, TLS mode, and transport policy.
6. The management API must be reusable by both the web console and a future desktop or CLI manager.
7. Relay services must remain physically separate per protocol even if some runtime behavior is still stubbed.

## Non-Goals For This Phase

- Full TCP/HTTP/HTTPS forwarding implementation
- UDP relay implementation
- P2P hole punching, STUN, TURN, or automatic relay fallback
- Multi-tenant billing
- Large-scale horizontal sharding

## Constraints

- Core backend services use Go.
- The first management client uses React.
- Persistent metadata uses PostgreSQL.
- The first release targets a small number of trusted users but must preserve extensibility.
- The architecture must support future cloud-side Codex-assisted debugging and coordination.

## Acceptance Criteria

- `server-api` exposes working register, heartbeat, node list, tunnel list, and server metrics endpoints.
- `client-agent` can register and heartbeat against `server-api`.
- Relay services start and expose health endpoints with protocol-specific identity.
- Admin web bootstrap can fetch and render the API surface after dependencies are installed.
- The project layout clearly separates control plane, data plane, protocol contracts, and shared utilities.

## Inferred Assumptions

- The first deployment uses a single cloud VM.
- TLS termination for HTTPS will happen on the cloud side.
- Future desktop management clients will reuse the same admin API and authentication model.
- Relay services will eventually publish events back into the control plane instead of writing directly to the database.

