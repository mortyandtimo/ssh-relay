package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/25743/cloud-relay-platform/apps/relay-web/internal/runtime"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

func main() {
	controlAddr := config.GetEnv("RELAY_WEB_ADDR", ":9094")
	proxyAddr := config.GetEnv("RELAY_WEB_HTTP_ADDR", ":9095")
	apiBaseURL := config.GetEnv("RELAY_WEB_API_BASE_URL", "http://127.0.0.1:7710")
	poolTarget := config.GetIntEnv("RELAY_WEB_STANDBY_TARGET_SIZE", 64)
	poolMax := config.GetIntEnv("RELAY_WEB_STANDBY_MAX_SIZE", 128)
	globalMax := int64(config.GetIntEnv("RELAY_WEB_GLOBAL_MAX_STANDBY", 16384))

	service := runtime.NewService(apiBaseURL, poolTarget, poolMax, globalMax)
	go func() {
		if err := service.Run(context.Background()); err != nil {
			log.Fatal(err)
		}
	}()

	// Control port: receives agent reverse connections
	controlMux := http.NewServeMux()
	controlMux.HandleFunc(types.AgentWebRelayConnectPath, service.HandleAgentReverse)
	controlMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.HealthResponse{Status: "ok", Service: "relay-web", ObservedAt: time.Now().UTC(), Details: map[string]string{"controlAddr": controlAddr, "proxyAddr": proxyAddr, "apiBaseUrl": apiBaseURL}})
	})
	controlMux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"service":           "relay-web",
			"controlAddr":       controlAddr,
			"proxyAddr":         proxyAddr,
			"apiBaseUrl":        apiBaseURL,
			"standbyTargetSize": poolTarget,
			"standbyMaxSize":    poolMax,
			"globalMaxStandby":  globalMax,
		})
	})
	controlMux.HandleFunc("/runtime", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(service.RuntimeSummary())
	})

	go func() {
		log.Printf("relay-web control listener on %s", controlAddr)
		if err := http.ListenAndServe(controlAddr, controlMux); err != nil {
			log.Fatal(err)
		}
	}()

	// Proxy port: receives nginx reverse proxy requests, routes by Host header
	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/", service.HandleProxyRequest)

	log.Printf("relay-web proxy listener on %s", proxyAddr)
	if err := http.ListenAndServe(proxyAddr, proxyMux); err != nil {
		log.Fatal(err)
	}
}
