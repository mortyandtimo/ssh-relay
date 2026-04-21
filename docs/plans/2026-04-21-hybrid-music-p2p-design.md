# Hybrid Music P2P Design

> Scope: `cloud-relay-platform` server-side manifest, `Echo` Android client, `SPlayer` desktop client.

## Goal

Build a conservative hybrid transport path for music playback:

- control plane stays on HTTPS
- data plane prefers P2P when healthy
- Android falls back quickly on network changes or overlay instability
- recovery only affects future requests

## Phase 1

Phase 1 only covers the most critical data-plane requests and recovery loop:

- `stream`
- `download`
- `coverArt` for Echo
- `stream` for SPlayer

It does not try to force full-session P2P.

## Server

`cloud-relay-platform` exposes a transport manifest in `UserServiceEntry` for `music` services.

The manifest contains:

- default control-plane mode
- default preferred path
- probe policy
- recovery policy
- capability flags for key data-plane requests

## Echo

Echo keeps the existing cloud address for control-plane requests and adds a dedicated data-plane route manager.

The data-plane route manager:

- probes P2P addresses only
- enters cooldown after consecutive failures
- recovers after repeated probe success
- never hot-switches the currently playing stream

The login flow and library address editor expose optional P2P data addresses.

## SPlayer

SPlayer keeps existing streaming control-plane requests on the configured server URL and adds per-server hybrid transport metadata.

The desktop client:

- uses P2P only for stream URLs in phase 1
- quickly falls back to cloud on playback failures
- probes P2P in the background and restores it for later requests

The entry point stays in `全局设置 -> 网络与链接`.

## Known Limits

- clients do not fully auto-bootstrap from a cloud-relay authenticated manifest in phase 1
- EasyTier process lifecycle integration is not completed in phase 1
- SPlayer cover-art transport remains cloud-first in phase 1
