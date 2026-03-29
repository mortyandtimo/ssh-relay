# Cloud Relay Platform Design

## Overview

The platform is split into a stable control plane and protocol-specific data-plane relays.

- `server-api` owns identity, tunnel configuration, node status, and management APIs.
- `relay-tcp`, `relay-http`, and `relay-https` own ingress behavior per protocol.
- `client-agent` maintains node identity and later terminates data-plane streams back to local targets.
- `admin-web` is only a client of the management API, not a privileged backend.

This design intentionally keeps protocol relays physically distinct so that later additions such as UDP relay or P2P coordination do not force a rewrite of the control plane.

## Architecture

### Control Plane

The control plane uses a simple REST surface for phase one.

- Agent registration and heartbeat land on `server-api`.
- Node state is kept in memory for the bootstrap and mirrored to PostgreSQL in a later phase.
- Tunnel definitions already model `type`, `tls_mode`, `transport_policy`, `public_port`, and `domain` so HTTP/HTTPS and future UDP/P2P do not distort the schema.

### Data Plane

Each relay service has its own process boundary.

- `relay-tcp` will accept port-based traffic.
- `relay-http` will own HTTP host/path routing.
- `relay-https` will own TLS edge behavior and HTTPS-specific policy.

During the bootstrap these services expose health/config endpoints only. That preserves deployment, configuration, and observability boundaries before implementing actual stream forwarding.

### Client Plane

`client-agent` registers itself, advertises capabilities, and sends heartbeats. That gives us a stable node lifecycle model before long-lived control streams and relay workers are added.

### Management Plane

`admin-web` consumes the public management API. This is important because a future desktop or CLI manager can reuse exactly the same API without backend duplication.

## Data Model

Core entities:

- `nodes`
- `node_tokens`
- `node_sessions`
- `tunnels`
- `tunnel_sessions`
- `server_metrics`
- `node_metrics`
- `audit_logs`

Tunnel records are the most important extensibility point. They already reserve fields for TCP, HTTP/HTTPS, UDP, and future transport policy.

## Operational Notes

- The bootstrap uses standard library Go HTTP servers to keep the first compile surface small.
- Docker scaffolding is included so cloud and local Codex sessions can share the same service topology.
- PostgreSQL is modeled now even though the first API implementation uses an in-memory registry. This avoids redesigning records later.

