# Cloud Relay Platform Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the first governed bootstrap of the cloud relay platform with a working control plane, protocol-specific relay processes, a minimal node agent, and a React management shell.

**Architecture:** Start with a single Go module and a physically separated service layout. Implement the control plane first, then add relay service entry points and the admin UI shell, while keeping PostgreSQL persistence and actual data forwarding as the next wave.

**Tech Stack:** Go 1.22, React 18, TypeScript, Vite, PostgreSQL, Docker Compose.

---

### Task 1: Freeze governed runtime artifacts

**Files:**
- Create: `docs/requirements/2026-03-29-cloud-relay-platform.md`
- Create: `docs/plans/2026-03-29-cloud-relay-platform-design.md`
- Create: `docs/plans/2026-03-29-cloud-relay-platform-execution-plan.md`
- Create: `outputs/runtime/vibe-sessions/2026-03-29-cloud-relay-platform/*.json`

**Steps:**
1. Write the requirement document.
2. Write the approved design document.
3. Write the execution plan.
4. Emit bootstrap runtime receipts.

### Task 2: Establish shared contracts and utilities

**Files:**
- Create: `go.mod`
- Create: `packages/protocol/types/types.go`
- Create: `packages/shared/config/env.go`
- Create: `packages/shared/bootstrap/simple_service.go`

**Steps:**
1. Define node, heartbeat, tunnel, and server metric contracts.
2. Add small shared configuration helpers.
3. Add a reusable health/config bootstrap server for relay processes.
4. Run `go test ./...` to ensure the module still compiles.

### Task 3: Implement bootstrap control plane

**Files:**
- Create: `apps/server-api/cmd/server-api/main.go`
- Create: `apps/server-api/internal/api/server.go`
- Create: `apps/server-api/internal/api/server_test.go`

**Steps:**
1. Implement the in-memory node registry and HTTP handlers.
2. Add tests for register, heartbeat, list nodes, and server metrics.
3. Run `go test ./apps/server-api/...`.

### Task 4: Add client and relay service entry points

**Files:**
- Create: `apps/client-agent/cmd/client-agent/main.go`
- Create: `apps/relay-tcp/cmd/relay-tcp/main.go`
- Create: `apps/relay-http/cmd/relay-http/main.go`
- Create: `apps/relay-https/cmd/relay-https/main.go`

**Steps:**
1. Build the bootstrap node agent for register/heartbeat.
2. Create protocol relay placeholders with health/config endpoints.
3. Run `go test ./...` and `go build` for all Go commands.

### Task 5: Add management web shell and deployment scaffolding

**Files:**
- Create: `apps/admin-web/*`
- Create: `db/schema.sql`
- Create: `deploy/docker/*`

**Steps:**
1. Scaffold the React admin app against the management API.
2. Write the first PostgreSQL schema.
3. Add Dockerfiles and compose topology.
4. Document local startup in `README.md`.

