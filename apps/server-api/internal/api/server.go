package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/25743/cloud-relay-platform/apps/server-api/internal/store"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

type Server struct {
	version   string
	startedAt time.Time
	store     store.Store
	mux       *http.ServeMux
}

func NewServer(version string, backend store.Store) *Server {
	s := &Server{
		version:   version,
		startedAt: time.Now().UTC(),
		store:     backend,
		mux:       http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/agent/register", s.handleRegister)
	s.mux.HandleFunc("/agent/heartbeat", s.handleHeartbeat)
	s.mux.HandleFunc("/api/nodes", s.handleNodes)
	s.mux.HandleFunc("/api/tunnels", s.handleTunnels)
	s.mux.HandleFunc("/api/server/metrics", s.handleServerMetrics)
	s.mux.HandleFunc("/internal/routes/tcp", s.handleTCPRoutes)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, types.HealthResponse{
		Status:     "ok",
		Service:    "server-api",
		ObservedAt: time.Now().UTC(),
		Details: map[string]string{
			"version": s.version,
			"store":   s.store.Kind(),
		},
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req types.NodeRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid register payload")
		return
	}
	if strings.TrimSpace(req.NodeName) == "" {
		writeError(w, http.StatusBadRequest, "nodeName is required")
		return
	}

	summary, err := s.store.RegisterNode(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types.NodeRegisterResponse{
		NodeID:            summary.NodeID,
		RegisteredAt:      summary.LastSeenAt,
		RecommendedPeriod: 30,
	})
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req types.NodeHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid heartbeat payload")
		return
	}
	if strings.TrimSpace(req.NodeID) == "" {
		writeError(w, http.StatusBadRequest, "nodeId is required")
		return
	}

	_, err := s.store.HeartbeatNode(r.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "accepted",
		"observedAt": time.Now().UTC(),
	})
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	items, err := s.store.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleTunnels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListTunnels(r.Context(), store.TunnelFilter{NodeID: r.URL.Query().Get("nodeId"), Type: r.URL.Query().Get("type"), Status: r.URL.Query().Get("status")})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var req struct {
			NodeID string `json:"nodeId"`
			types.TunnelSpec
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid tunnel payload")
			return
		}
		if req.NodeID == "" {
			writeError(w, http.StatusBadRequest, "nodeId is required")
			return
		}
		spec := req.TunnelSpec
		spec.NodeID = req.NodeID
		if spec.Metadata == nil {
			spec.Metadata = map[string]string{}
		}
		spec.Metadata["nodeId"] = req.NodeID
		tunnel, err := s.store.CreateTunnel(r.Context(), spec)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, tunnel)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (s *Server) handleServerMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	counts, err := s.store.Counts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types.ServerMetrics{
		Service:            "server-api",
		StartedAt:          s.startedAt,
		RegisteredNodes:    counts.RegisteredNodes,
		OnlineNodes:        counts.OnlineNodes,
		ConfiguredTunnels:  counts.ConfiguredTunnels,
		ProtocolRelayCount: 3,
	})
}

func (s *Server) handleTCPRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	items, err := s.store.ListTunnels(r.Context(), store.TunnelFilter{Type: "tcp", Status: "active"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeMethodNotAllowed(w http.ResponseWriter, method string) {
	w.Header().Set("Allow", method)
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
