package bootstrap

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

type Descriptor struct {
	Service string
	Addr    string
	Config  map[string]string
}

func Run(desc Descriptor) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, types.HealthResponse{
			Status:     "ok",
			Service:    desc.Service,
			ObservedAt: time.Now().UTC(),
			Details:    desc.Config,
		})
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"service": desc.Service,
			"addr":    desc.Addr,
			"config":  desc.Config,
		})
	})

	log.Printf("%s listening on %s", desc.Service, desc.Addr)
	return http.ListenAndServe(desc.Addr, mux)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

