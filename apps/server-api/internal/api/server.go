package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/25743/cloud-relay-platform/apps/server-api/internal/store"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

type Server struct {
	version            string
	startedAt          time.Time
	store              store.Store
	relayTCPRuntimeURL string
	adminToken         string
	httpClient         *http.Client
	mux                *http.ServeMux
}

func NewServer(version string, backend store.Store, relayTCPRuntimeURL string) *Server {
	s := &Server{
		version:            version,
		startedAt:          time.Now().UTC(),
		store:              backend,
		relayTCPRuntimeURL: relayTCPRuntimeURL,
		httpClient:         &http.Client{Timeout: 5 * time.Second},
		mux:                http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return withCORS(s.mux)
}

func (s *Server) SetAdminToken(token string) {
	s.adminToken = strings.TrimSpace(token)
}

func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/agent/register", s.handleRegister)
	s.mux.HandleFunc("/agent/heartbeat", s.handleHeartbeat)
	s.mux.HandleFunc("/agent/tunnels", s.handleAgentTunnels)
	s.mux.Handle("/api/nodes", s.requireAdmin(http.HandlerFunc(s.handleNodes)))
	s.mux.Handle("/api/tunnels", s.requireAdmin(http.HandlerFunc(s.handleTunnels)))
	s.mux.Handle("/api/tunnels/", s.requireAdmin(http.HandlerFunc(s.handleTunnelByID)))
	s.mux.Handle("/api/server/metrics", s.requireAdmin(http.HandlerFunc(s.handleServerMetrics)))
	s.mux.Handle("/api/relay/tcp/runtime", s.requireAdmin(http.HandlerFunc(s.handleRelayTCPRuntime)))
	s.mux.HandleFunc("/internal/routes/tcp", s.handleTCPRoutes)
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.authorizeRequest(r); err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cloud-relay-admin"`)
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorizeRequest(r *http.Request) error {
	if s.adminToken == "" {
		return nil
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.Fields(authHeader)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return errors.New("missing or invalid bearer token")
	}
	if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.adminToken)) != 1 {
		return errors.New("missing or invalid bearer token")
	}
	return nil
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

func (s *Server) handleAgentTunnels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	nodeID := strings.TrimSpace(r.URL.Query().Get("nodeId"))
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId is required")
		return
	}
	filter := store.TunnelFilter{
		NodeID: nodeID,
		Type:   r.URL.Query().Get("type"),
		Status: r.URL.Query().Get("status"),
	}
	if filter.Type == "" {
		filter.Type = "tcp"
	}
	if filter.Status == "" {
		filter.Status = "active"
	}
	items, err := s.store.ListTunnels(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
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
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrConflict) {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, tunnel)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (s *Server) handleTunnelByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")
	id = strings.TrimSpace(id)
	if id == "" {
		writeError(w, http.StatusBadRequest, "tunnel id is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		tunnel, err := s.store.GetTunnel(r.Context(), id)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, tunnel)
	case http.MethodPut:
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
		spec.ID = id
		spec.NodeID = req.NodeID
		if spec.Metadata == nil {
			spec.Metadata = map[string]string{}
		}
		spec.Metadata["nodeId"] = req.NodeID
		tunnel, err := s.store.UpdateTunnel(r.Context(), spec)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, store.ErrNotFound):
				status = http.StatusNotFound
			case errors.Is(err, store.ErrConflict):
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, tunnel)
	case http.MethodDelete:
		err := s.store.DeleteTunnel(r.Context(), id)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
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

func (s *Server) handleRelayTCPRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if s.relayTCPRuntimeURL == "" {
		writeJSON(w, http.StatusOK, types.RelayRuntimeSummary{
			Service:    "relay-tcp",
			ObservedAt: time.Now().UTC(),
			Pools:      []types.RelayPoolSummary{},
		})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.relayTCPRuntimeURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("relay-tcp runtime returned %s", resp.Status))
		return
	}
	var payload types.RelayRuntimeSummary
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payload)
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

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
