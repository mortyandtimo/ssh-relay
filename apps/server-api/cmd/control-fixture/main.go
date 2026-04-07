package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/25743/cloud-relay-platform/apps/server-api/internal/api"
	"github.com/25743/cloud-relay-platform/apps/server-api/internal/store"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

func main() {
	addr := config.GetEnv("SERVER_API_ADDR", "127.0.0.1:18080")
	relayTCPRuntimeURL := config.GetEnv("RELAY_TCP_RUNTIME_URL", "http://127.0.0.1:9090/runtime")
	mode := strings.TrimSpace(config.GetEnv("CONTROL_FIXTURE_MODE", "duplicate_inflight"))
	nodeID := strings.TrimSpace(config.GetEnv("CONTROL_FIXTURE_NODE_ID", "node-desktop-duplicate-live"))
	releaseDelay := time.Duration(parseIntEnv("CONTROL_FIXTURE_RELEASE_DELAY_MS", 600)) * time.Millisecond

	backend := store.NewInMemoryStore()
	defer backend.Close()
	prepareManagedNode(backend, nodeID)

	server := api.NewServer("fixture", backend, relayTCPRuntimeURL)
	var fixture *api.BlockingControlExecutionFixture
	switch mode {
	case "duplicate_inflight":
		fixture = server.UseBlockingControlExecutionFixture(types.ControlActionIsolateNode, types.ControlTargetNode, nodeID)
	case "duplicate_handled":
		// No extra hook is needed. The first accepted real execute writes into the
		// existing done-cache, and the second same-context request should then be
		// rejected as duplicate_handled by the normal server path.
	case "retryable_restart":
		server.UseRetryableRestartFailureFixture(nodeID, config.GetEnv("CONTROL_FIXTURE_RETRYABLE_DETAIL", "fixture transient failure"))
	default:
		log.Fatalf("unsupported CONTROL_FIXTURE_MODE: %s", mode)
	}

	if fixture != nil {
		go func() {
			for !fixture.Started() {
				time.Sleep(20 * time.Millisecond)
			}
			time.Sleep(releaseDelay)
			fixture.Release()
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/fixture/started", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		started := false
		if fixture != nil {
			started = fixture.Started()
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"started": started})
	})
	mux.Handle("/", server.Handler())

	httpServer := &http.Server{Addr: addr, Handler: mux}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		if fixture != nil {
			fixture.Release()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()

	log.Printf("control-fixture listening on %s for node %s in mode %s", addr, nodeID, mode)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func prepareManagedNode(backend store.Store, nodeID string) {
	ctx := context.Background()
	if _, err := backend.RegisterNode(ctx, types.NodeRegisterRequest{
		NodeID:       nodeID,
		NodeName:     nodeID,
		AgentVersion: "0.1.0",
		Capabilities: types.NodeCapabilities{TCPRelay: true, UDPRelay: true, HTTPRelay: true, HTTPSRelay: true, SOCKS5Connect: true},
		Metadata: map[string]string{
			"deploymentMode":  "managed",
			"serviceUnit":     "cloud-relay-client-agent@" + nodeID + ".service",
			"instanceProfile": nodeID,
			"instanceManaged": "true",
			"nodeRole":        "cloud",
		},
	}); err != nil {
		log.Fatalf("register fixture node: %v", err)
	}
	if _, err := backend.HeartbeatNode(ctx, types.NodeHeartbeatRequest{
		NodeID:        nodeID,
		ObservedAt:    time.Now().UTC(),
		ActiveTunnels: 0,
	}); err != nil {
		log.Fatalf("heartbeat fixture node: %v", err)
	}
}

func parseIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
