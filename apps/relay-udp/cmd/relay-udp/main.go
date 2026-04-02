package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/25743/cloud-relay-platform/apps/relay-udp/internal/runtime"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

func main() {
	addr := config.GetEnv("RELAY_UDP_ADDR", ":9093")
	apiBaseURL := config.GetEnv("RELAY_UDP_API_BASE_URL", "http://localhost:8080")
	service := runtime.NewService(apiBaseURL)
	go func() {
		if err := service.Run(context.Background()); err != nil {
			log.Fatal(err)
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc(types.AgentUDPRelayConnectPath, service.HandleAgentReverse)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"relay-udp"}`))
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"service": "relay-udp", "apiBaseUrl": apiBaseURL})
	})
	log.Printf("relay-udp control listener on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
