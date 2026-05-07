package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/25743/cloud-relay-platform/apps/ssh-relay-api/internal/notifier"
	"github.com/25743/cloud-relay-platform/apps/ssh-relay-api/internal/relay"
	"github.com/25743/cloud-relay-platform/apps/ssh-relay-api/internal/store"
)

const offlineThreshold = 90 * time.Second
const monitorInterval = 30 * time.Second

type Server struct {
	store       *store.Store
	notifier    *notifier.Service
	relay       *relay.Manager
	domain      string
	portStart   int
	portEnd     int
	mux         *http.ServeMux
	httpClient  *http.Client
	startedAt   time.Time
	stopMonitor context.CancelFunc
}

func NewServer(s *store.Store, n *notifier.Service, domain string) *Server {
	portStart, _ := strconv.Atoi(envOr("SSHR_PORT_START", "40000"))
	portEnd, _ := strconv.Atoi(envOr("SSHR_PORT_END", "40999"))

	srv := &Server{
		store:      s,
		notifier:   n,
		relay:      relay.NewManager(s, portStart, portEnd, 4),
		domain:     domain,
		portStart:  portStart,
		portEnd:    portEnd,
		mux:        http.NewServeMux(),
		httpClient: &http.Client{Timeout: 5 * time.Second},
		startedAt:  time.Now().UTC(),
	}
	srv.routes()
	return srv
}

func (s *Server) Handler() http.Handler {
	return s.withCORS(s.mux)
}

func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) StartMonitor(ctx context.Context) {
	monCtx, cancel := context.WithCancel(ctx)
	s.stopMonitor = cancel
	s.relay.StartSync(monCtx)
	go s.runMonitor(monCtx)
}

func (s *Server) StopMonitor() {
	if s.stopMonitor != nil {
		s.stopMonitor()
	}
	s.relay.Stop()
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/api/register", s.handleRegister)
	s.mux.HandleFunc("/api/heartbeat", s.handleHeartbeat)
	s.mux.HandleFunc("/api/machines", s.handleMachines)
	s.mux.HandleFunc("/api/machines/", s.handleMachineByID)
	s.mux.HandleFunc("/api/forwards", s.handleForwards)
	s.mux.HandleFunc("/api/forwards/", s.handleForwardByID)
	s.mux.HandleFunc("/api/ports", s.handlePorts)
	s.mux.HandleFunc("/api/ports/available", s.handleAvailablePorts)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/events", s.handleEvents)
	s.mux.HandleFunc("/api/relay/reverse", s.relay.HandleReverseConnect)
}

// ─── Health ───

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"service":    "ssh-relay-api",
		"domain":     s.domain,
		"startedAt":  s.startedAt,
		"observedAt": time.Now().UTC(),
	})
}

// ─── Register ───

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		Name         string            `json:"name"`
		GPUModel     string            `json:"gpuModel"`
		AgentVersion string            `json:"agentVersion"`
		Metadata     map[string]string `json:"metadata,omitempty"`
		NotifyEmails []string          `json:"notifyEmails,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if req.AgentVersion == "" {
		req.AgentVersion = "0.1.0"
	}
	m, err := s.store.RegisterMachine(r.Context(), store.RegisterMachineRequest{
		Name:         req.Name,
		GPUModel:     req.GPUModel,
		AgentVersion: req.AgentVersion,
		Metadata:     req.Metadata,
		NotifyEmails: req.NotifyEmails,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	log.Printf("machine registered: id=%s name=%s gpu=%s", m.ID, m.Name, m.GPUModel)
	writeJSON(w, http.StatusOK, m)
}

// ─── Heartbeat ───

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req store.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if req.MachineID == "" {
		writeError(w, http.StatusBadRequest, "machineId is required")
		return
	}
	m, err := s.store.Heartbeat(r.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "accepted",
		"machineId":   m.ID,
		"observedAt":  time.Now().UTC(),
	})
}

// ─── Machine list ───

func (s *Server) handleMachines(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status := r.URL.Query().Get("status")
		machines, err := s.store.ListMachines(r.Context(), status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": machines})
	default:
		writeMethodNotAllowed(w, http.MethodGet)
	}
}

// ─── Machine by ID ───

func (s *Server) handleMachineByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/machines/")
	parts := strings.Split(path, "/")
	id := parts[0]

	if id == "" {
		writeError(w, http.StatusBadRequest, "machine id is required")
		return
	}

	// /api/machines/{id}/events
	if len(parts) == 2 && parts[1] == "events" {
		s.handleMachineEvents(w, r, id)
		return
	}
	// /api/machines/{id}/forwards
	if len(parts) == 2 && parts[1] == "forwards" {
		s.handleMachineForwards(w, r, id)
		return
	}

	switch r.Method {
	case http.MethodGet:
		m, err := s.store.GetMachine(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "machine not found")
			return
		}
		writeJSON(w, http.StatusOK, m)
	case http.MethodPut:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		m, err := s.store.UpdateMachineName(r.Context(), id, req.Name)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "already exists") {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, m)
	case http.MethodDelete:
		if err := s.store.DeleteMachine(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func (s *Server) handleMachineEvents(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.store.ListHeartbeatEvents(r.Context(), id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events})
}

func (s *Server) handleMachineForwards(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	fwds, err := s.store.ListForwards(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": fwds})
}

// ─── Forwards ───

func (s *Server) handleForwards(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		machineID := r.URL.Query().Get("machineId")
		fwds, err := s.store.ListForwards(r.Context(), machineID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": fwds})
	case http.MethodPost:
		var req store.CreateForwardRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		if req.MachineID == "" || req.Name == "" {
			writeError(w, http.StatusBadRequest, "machineId and name are required")
			return
		}
		if req.TargetHost == "" {
			req.TargetHost = "127.0.0.1"
		}
		if req.TargetPort == 0 {
			req.TargetPort = 22
		}
		if req.PublicPort < s.portStart || req.PublicPort > s.portEnd {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("public port must be in range %d-%d", s.portStart, s.portEnd))
			return
		}
		f, err := s.store.CreateForward(r.Context(), req)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "already in use") {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		// Write audit event
		_ = s.store.AddHeartbeatEvent(r.Context(), f.MachineID, "forward_created",
			fmt.Sprintf("forward %s created: %d -> %s:%d", f.Name, f.PublicPort, f.TargetHost, f.TargetPort))

		log.Printf("forward created: id=%s machine=%s port=%d target=%s:%d", f.ID, f.MachineID, f.PublicPort, f.TargetHost, f.TargetPort)
		writeJSON(w, http.StatusCreated, f)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (s *Server) handleForwardByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/forwards/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "forward id is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		f, err := s.store.GetForward(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "forward not found")
			return
		}
		writeJSON(w, http.StatusOK, f)
	case http.MethodDelete:
		f, err := s.store.GetForward(r.Context(), id)
		if err == nil {
			_ = s.store.AddHeartbeatEvent(r.Context(), f.MachineID, "forward_deleted",
				fmt.Sprintf("forward %s deleted: port %d released", f.Name, f.PublicPort))
		}
		if err := s.store.DeleteForward(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		log.Printf("forward deleted: id=%s", id)
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodDelete)
	}
}

// ─── Ports ───

func (s *Server) handlePorts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	type portInfo struct {
		Port       int    `json:"port"`
		Status     string `json:"status"`
		MachineID  string `json:"machineId,omitempty"`
		MachineName string `json:"machineName,omitempty"`
		TargetHost string `json:"targetHost,omitempty"`
		TargetPort int    `json:"targetPort,omitempty"`
	}

	var ports []portInfo
	for p := s.portStart; p <= s.portEnd; p++ {
		ports = append(ports, portInfo{Port: p, Status: "free"})
	}

	allFwds, _ := s.store.ListForwards(r.Context(), "")
	usedByPort := map[int]store.Forward{}
	for _, f := range allFwds {
		usedByPort[f.PublicPort] = f
	}
	machineCache := map[string]store.Machine{}

	for i, p := range ports {
		if f, ok := usedByPort[p.Port]; ok {
			ports[i].Status = "used"
			ports[i].MachineID = f.MachineID
			ports[i].TargetHost = f.TargetHost
			ports[i].TargetPort = f.TargetPort
			if m, ok := machineCache[f.MachineID]; ok {
				ports[i].MachineName = m.Name
			} else {
				m, err := s.store.GetMachine(r.Context(), f.MachineID)
				if err == nil {
					ports[i].MachineName = m.Name
					machineCache[f.MachineID] = m
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": ports, "range": map[string]int{"start": s.portStart, "end": s.portEnd}})
}

func (s *Server) handleAvailablePorts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	used, err := s.store.UsedPorts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var available []int
	for p := s.portStart; p <= s.portEnd; p++ {
		if !used[p] {
			available = append(available, p)
			if len(available) >= 10 {
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ports": available,
		"total": len(available),
	})
}

// ─── Settings ───

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.store.GetSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, settings)
	case http.MethodPut:
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		for k, v := range req {
			if err := s.store.SetSetting(r.Context(), k, v); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		settings, _ := s.store.GetSettings(r.Context())
		writeJSON(w, http.StatusOK, settings)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}

// ─── Events ───

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	machineID := r.URL.Query().Get("machineId")
	if machineID == "" {
		writeError(w, http.StatusBadRequest, "machineId is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.store.ListHeartbeatEvents(r.Context(), machineID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events})
}

// ─── Monitor ───

func (s *Server) runMonitor(ctx context.Context) {
	log.Printf("offline monitor started (threshold=%s, interval=%s)", offlineThreshold, monitorInterval)
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("offline monitor stopped")
			return
		case <-ticker.C:
			gone, err := s.store.MarkStaleMachinesOffline(ctx, offlineThreshold)
			if err != nil {
				log.Printf("monitor error: %v", err)
				continue
			}
			for _, m := range gone {
				log.Printf("machine offline: id=%s name=%s lastSeen=%s", m.ID, m.Name, m.LastSeenAt)
				// Send alerts to machine-specific notify emails and global settings
				emails := s.collectNotifyEmails(m)
				if err := s.notifier.SendOfflineAlert(emails, m.Name, m.ID, *m.LastSeenAt); err != nil {
					log.Printf("alert send error: %v", err)
				}
			}
		}
	}
}

func (s *Server) collectNotifyEmails(m store.Machine) []string {
	seen := map[string]bool{}

	// Machine-level notify emails
	if m.Metadata != nil {
		if raw := m.Metadata["notifyEmails"]; raw != "" {
			for _, e := range strings.Split(raw, ",") {
				e = strings.TrimSpace(e)
				if e != "" {
					seen[e] = true
				}
			}
		}
	}

	// Global settings
	settings, err := s.store.GetSettings(context.Background())
	if err == nil {
		if raw := settings["notifyEmails"]; raw != "" {
			for _, e := range strings.Split(raw, ",") {
				e = strings.TrimSpace(e)
				if e != "" {
					seen[e] = true
				}
			}
		}
	}

	var out []string
	for e := range seen {
		out = append(out, e)
	}
	return out
}

// ─── Helpers ───

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
