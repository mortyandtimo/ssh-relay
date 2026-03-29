package main

import (
	"context"
	"log"
	"net/http"

	"github.com/25743/cloud-relay-platform/apps/relay-tcp/internal/runtime"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

func main() {
	addr := config.GetEnv("RELAY_TCP_ADDR", ":9090")
	apiBaseURL := config.GetEnv("RELAY_TCP_API_BASE_URL", "http://localhost:8080")

	service := runtime.NewService(apiBaseURL)
	go func() {
		if err := service.Run(context.Background()); err != nil {
			log.Fatal(err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc(types.AgentRelayConnectPath, service.HandleAgentReverse)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"relay-tcp"}`))
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"service":"relay-tcp","mode":"runtime","apiBaseUrl":"` + apiBaseURL + `"}`))
	})

	log.Printf("relay-tcp control listener on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
