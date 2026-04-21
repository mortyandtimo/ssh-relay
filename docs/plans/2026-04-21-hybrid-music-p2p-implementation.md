# Hybrid Music P2P Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a conservative hybrid music transport path across server manifest, Echo, and SPlayer.

**Architecture:** The server publishes transport defaults for music services. Echo and SPlayer keep control-plane HTTPS and choose data-plane P2P only when probes are healthy. Failure enters cooldown, while recovery is background-driven and affects future requests only.

**Tech Stack:** Go, Flutter/Dart, Vue 3, Electron, TypeScript

---

### Task 1: Server Manifest

**Files:**
- Modify: `apps/server-api/internal/api/user_services.go`
- Modify: `packages/protocol/types/types.go`
- Test: `apps/server-api/internal/api/server_test.go`

**Step 1:** Add transport manifest structs and `music` service inference.

**Step 2:** Populate default probe/recovery policy for music services.

**Step 3:** Extend tests for inferred music service and manifest payload.

### Task 2: Echo Hybrid Data Plane

**Files:**
- Modify: `_vendor/echo/lib/data/models/server_address.dart`
- Modify: `_vendor/echo/lib/features/auth/pages/login_page.dart`
- Modify: `_vendor/echo/lib/features/library/widgets/address_dialog.dart`
- Modify: `_vendor/echo/lib/widgets/app_drawer.dart`
- Modify: `_vendor/echo/lib/providers/api_provider.dart`
- Modify: `_vendor/echo/lib/data/sources/subsonic_api_client.dart`
- Modify: `_vendor/echo/lib/providers/player_provider.dart`
- Modify: `_vendor/echo/lib/core/services/download_service.dart`
- Create: `_vendor/echo/lib/core/network/data_plane_route_manager.dart`

**Step 1:** Split control-plane and data-plane routing.

**Step 2:** Add P2P candidate probing, cooldown, and recovery state.

**Step 3:** Wire stream/download/cover art URL builders to the data-plane resolver.

**Step 4:** Surface optional P2P address inputs in login and line management UI.

### Task 3: SPlayer Hybrid Streaming

**Files:**
- Modify: `_vendor/SPlayer/src/types/streaming.ts`
- Modify: `_vendor/SPlayer/src/stores/streaming.ts`
- Modify: `_vendor/SPlayer/src/api/streaming/subsonic.ts`
- Modify: `_vendor/SPlayer/src/components/Modal/Setting/StreamingServerConfig.vue`
- Modify: `_vendor/SPlayer/src/components/Setting/components/StreamingServerList.vue`
- Modify: `_vendor/SPlayer/src/core/player/PlayerController.ts`
- Create: `_vendor/SPlayer/src/api/streaming/hybrid.ts`

**Step 1:** Extend streaming server config with hybrid transport metadata.

**Step 2:** Add per-server probe, cooldown, and recovery logic.

**Step 3:** Use hybrid routing for stream URL generation.

**Step 4:** Report playback failures back into cooldown logic.

### Task 4: Verification

**Files:**
- N/A

**Step 1:** Run server tests for service catalog changes.

**Step 2:** Run Echo codegen and targeted analysis/tests.

**Step 3:** Run SPlayer lint/typecheck for touched files.
