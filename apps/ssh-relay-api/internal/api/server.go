package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	store               *store.Store
	notifier            *notifier.Service
	relay               *relay.Manager
	domain              string
	allowedEmailDomains string
	portStart           int
	portEnd             int
	mux                 *http.ServeMux
	httpClient          *http.Client
	startedAt           time.Time
	stopMonitor         context.CancelFunc
}

func NewServer(s *store.Store, n *notifier.Service, domain, allowedEmailDomains string) *Server {
	portStart, _ := strconv.Atoi(envOr("SSHR_PORT_START", "40000"))
	portEnd, _ := strconv.Atoi(envOr("SSHR_PORT_END", "40999"))

	srv := &Server{
		store:               s,
		notifier:            n,
		relay:               relay.NewManager(s, portStart, portEnd, 8),
		domain:              domain,
		allowedEmailDomains: allowedEmailDomains,
		portStart:           portStart,
		portEnd:             portEnd,
		mux:                 http.NewServeMux(),
		httpClient:          &http.Client{Timeout: 5 * time.Second},
		startedAt:           time.Now().UTC(),
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
	s.mux.HandleFunc("/api/auth/send-code", s.handleSendCode)
	s.mux.HandleFunc("/api/auth/verify-code", s.handleVerifyCode)
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("/api/auth/me", s.handleAuthMe)
	s.mux.HandleFunc("/api/machines", s.handleMachines)
	s.mux.HandleFunc("/api/machines/", s.handleMachineByID)
	s.mux.HandleFunc("/api/forwards", s.handleForwards)
	s.mux.HandleFunc("/api/forwards/", s.handleForwardByID)
	s.mux.HandleFunc("/api/ports", s.handlePorts)
	s.mux.HandleFunc("/api/ports/available", s.handleAvailablePorts)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/events", s.handleEvents)
	s.mux.HandleFunc("/api/relay/reverse", s.relay.HandleReverseConnect)
	s.mux.HandleFunc("/api/users", s.handleUsers)
	s.mux.HandleFunc("/api/users/", s.handleUserByID)
	s.mux.HandleFunc("/api/me/password", s.handleChangePassword)
	s.mux.HandleFunc("/", s.handleDashboard)
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

// ─── Email domain check ───

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

func (s *Server) isEmailAllowed(email string) bool {
	if s.allowedEmailDomains == "" {
		return true
	}
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return false
	}
	for _, d := range strings.Split(s.allowedEmailDomains, ",") {
		if strings.TrimSpace(d) == parts[1] {
			return true
		}
	}
	return false
}

// ─── Role helpers ───

func (s *Server) requireRole(w http.ResponseWriter, r *http.Request, roles ...store.UserRole) *store.User {
	user := s.sessionUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return nil
	}
	for _, role := range roles {
		if user.Role == role || user.Role == store.RoleSuperAdmin {
			return user
		}
	}
	writeError(w, http.StatusForbidden, "insufficient permissions")
	return nil
}

func (s *Server) userCanManage(actor, target *store.User) bool {
	if actor.Role == store.RoleSuperAdmin {
		return true // super_admin can manage anyone
	}
	if actor.Role == store.RoleAdmin && target.Role == store.RoleUser {
		return true
	}
	return false
}

// ─── Port visibility for users ───

func (s *Server) userPortsVisible(ctx context.Context, userID string) bool {
	v, err := s.store.GetUserSetting(ctx, userID, "ports_visible")
	if err != nil {
		return true // default visible
	}
	return v != "false"
}

// ─── User management ───

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	actor := s.requireRole(w, r, store.RoleSuperAdmin, store.RoleAdmin)
	if actor == nil {
		return
	}

	switch r.Method {
	case http.MethodGet:
		users, err := s.store.ListUsers(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Filter by role hierarchy
		var filtered []store.User
		for _, u := range users {
			if s.userCanManage(actor, &u) {
				filtered = append(filtered, u)
			}
		}
		if filtered == nil {
			filtered = []store.User{}
		}
		// Attach user settings
		type userWithSettings struct {
			store.User
			Settings map[string]string `json:"settings"`
		}
		var result []userWithSettings
		for _, u := range filtered {
			u.PasswordHash = "" // never leak password hash
			settings, _ := s.store.GetAllUserSettings(r.Context(), u.ID)
			result = append(result, userWithSettings{User: u, Settings: settings})
		}
		if result == nil {
			result = []userWithSettings{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": result})

	case http.MethodPost:
		var req struct {
			Username string        `json:"username"`
			Email    string        `json:"email"`
			Password string        `json:"password"`
			Role     store.UserRole `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Password == "" {
			writeError(w, http.StatusBadRequest, "username and password are required")
			return
		}
		if req.Role == "" {
			req.Role = store.RoleUser
		}

		// Check role permission
		if req.Role == store.RoleAdmin && actor.Role != store.RoleSuperAdmin {
			writeError(w, http.StatusForbidden, "only super_admin can create admin accounts")
			return
		}
		if req.Role == store.RoleSuperAdmin {
			writeError(w, http.StatusForbidden, "cannot create super_admin accounts")
			return
		}

		// Validate email domain
		if req.Email != "" && !s.isEmailAllowed(req.Email) {
			writeError(w, http.StatusForbidden, "email domain not allowed")
			return
		}

		u, err := s.store.CreateUser(r.Context(), req.Username, req.Email, req.Password, req.Role)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "create user") {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		log.Printf("user created: id=%s username=%s role=%s by=%s", u.ID, u.Username, u.Role, actor.Username)
		writeJSON(w, http.StatusCreated, u)

	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (s *Server) handleUserByID(w http.ResponseWriter, r *http.Request) {
	actor := s.requireRole(w, r, store.RoleSuperAdmin, store.RoleAdmin)
	if actor == nil {
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/users/")
	parts := strings.Split(path, "/")
	id := parts[0]

	if id == "" {
		writeError(w, http.StatusBadRequest, "user id is required")
		return
	}

	// /api/users/{id}/settings
	if len(parts) == 2 && parts[1] == "settings" {
		s.handleUserSettings(w, r, actor, id)
		return
	}
	// /api/users/{id}/promote
	if len(parts) == 2 && parts[1] == "promote" {
		s.handlePromoteUser(w, r, actor, id)
		return
	}

	target, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if !s.userCanManage(actor, &target) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	switch r.Method {
	case http.MethodGet:
		target.PasswordHash = ""
		settings, _ := s.store.GetAllUserSettings(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]any{"user": target, "settings": settings})

	case http.MethodPut:
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		// Only super_admin can change role
		if _, ok := req["role"]; ok && actor.Role != store.RoleSuperAdmin {
			writeError(w, http.StatusForbidden, "only super_admin can change role")
			return
		}
		if email, ok := req["email"].(string); ok && email != "" && !s.isEmailAllowed(email) {
			writeError(w, http.StatusForbidden, "email domain not allowed")
			return
		}
		if err := s.store.UpdateUser(r.Context(), id, req); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated, _ := s.store.GetUser(r.Context(), id)
		updated.PasswordHash = ""
		log.Printf("user updated: id=%s username=%s by=%s", id, updated.Username, actor.Username)
		writeJSON(w, http.StatusOK, updated)

	case http.MethodDelete:
		if target.ID == actor.ID {
			writeError(w, http.StatusBadRequest, "cannot delete yourself")
			return
		}
		if err := s.store.DeleteUser(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		log.Printf("user deleted: id=%s username=%s by=%s", id, target.Username, actor.Username)
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})

	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func (s *Server) handleUserSettings(w http.ResponseWriter, r *http.Request, actor *store.User, targetID string) {
	target, err := s.store.GetUser(r.Context(), targetID)
	if err != nil || !s.userCanManage(actor, &target) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	switch r.Method {
	case http.MethodGet:
		settings, _ := s.store.GetAllUserSettings(r.Context(), targetID)
		writeJSON(w, http.StatusOK, settings)

	case http.MethodPut:
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		for k, v := range req {
			if k == "ports_visible" || k == "role" {
				if err := s.store.SetUserSetting(r.Context(), targetID, k, v); err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
		}
		settings, _ := s.store.GetAllUserSettings(r.Context(), targetID)
		writeJSON(w, http.StatusOK, settings)

	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}


// ─── Promote guest user ───

func (s *Server) handlePromoteUser(w http.ResponseWriter, r *http.Request, actor *store.User, targetID string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if actor.Role == store.RoleUser {
		writeError(w, http.StatusForbidden, "权限不足")
		return
	}

	target, err := s.store.GetUser(r.Context(), targetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if actor.Role == store.RoleAdmin && target.Role != store.RoleUser {
		writeError(w, http.StatusForbidden, "管理员只能提升普通用户")
		return
	}

	if err := s.store.PromoteUser(r.Context(), targetID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("user promoted: %s by %s", target.Username, actor.Username)
	writeJSON(w, http.StatusOK, map[string]any{"status": "promoted", "id": targetID})
}
// ─── Change own password ───

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeMethodNotAllowed(w, http.MethodPut)
		return
	}
	user := s.sessionUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OldPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "oldPassword and newPassword are required")
		return
	}
	// Verify old password
	_, err := s.store.AuthenticateUser(r.Context(), user.Username, req.OldPassword)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if err := s.store.UpdateUser(r.Context(), user.ID, map[string]any{"password": req.NewPassword}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "password_changed"})
}

// ─── Dashboard ───

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Check if user is logged in via session cookie
	user := s.sessionUser(r)
	if user == nil {
		// Serve login page
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(loginHTML))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

func (s *Server) sessionUser(r *http.Request) *store.User {
	cookie, err := r.Cookie("sshr_session")
	if err != nil {
		return nil
	}
	u, err := s.store.ValidateSession(r.Context(), cookie.Value)
	if err != nil {
		return nil
	}
	return &u
}

// ─── Auth Handlers ───

func (s *Server) handleSendCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}

	// Check email domain is allowed
	if !s.isEmailAllowed(req.Email) {
		writeError(w, http.StatusForbidden, "email domain not allowed")
		return
	}

	// Allowed domains: auto-register, no need for pre-existing account
	if s.isEmailAllowed(req.Email) {
		code, err := s.store.CreateVerificationCode(r.Context(), req.Email)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create code")
			return
		}
		if err := sendVerificationEmail(s.domain, req.Email, code); err != nil {
			log.Printf("send code email failed: %v", err)
		}
		log.Printf("verification code sent to %s", req.Email)
		writeJSON(w, http.StatusOK, map[string]any{"status": "sent", "expiresIn": 600})
		return
	}

	// Non-whitelist domains: must have existing account
	_, err := s.store.FindUserByEmail(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	code, err := s.store.CreateVerificationCode(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create code")
		return
	}

	// Send email
	if err := sendVerificationEmail(s.domain, req.Email, code); err != nil {
		log.Printf("send code email failed: %v", err)
		// Still return success to not leak info
	}

	log.Printf("verification code sent to %s", req.Email)
	writeJSON(w, http.StatusOK, map[string]any{"status": "sent", "expiresIn": 600})
}

func (s *Server) handleVerifyCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" || req.Code == "" {
		writeError(w, http.StatusBadRequest, "email and code required")
		return
	}

	ok, err := s.store.VerifyCode(r.Context(), req.Email, req.Code)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid or expired code")
		return
	}

	// Find user or auto-create for allowed domains
	user, err := s.store.FindUserByEmail(r.Context(), req.Email)
	if err != nil {
		if s.isEmailAllowed(req.Email) {
			user, err = s.store.CreateAutoUser(r.Context(), req.Email)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "auto-register failed")
				return
			}
			log.Printf("auto-registered guest: %s (email=%s)", user.Username, req.Email)
		} else {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
	}

	// Check login limit for auto users
	if err := s.store.CheckLoginAllowed(r.Context(), &user); err != nil {
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}

	_, token, err := s.store.CreateSession(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session error")
		return
	}

	// Shorter session for auto-registered guests (2 hours vs 24 hours)
	maxAge := 86400
	if user.Source == "auto" {
		maxAge = 7200
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "sshr_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
		Secure:   true,
	})

	s.store.IncrementLoginCount(r.Context(), user.ID)

	log.Printf("user %s logged in via verification code", user.Username)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "verified",
		"username": user.Username,
		"role":     user.Role,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Login == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "login and password required")
		return
	}

	user, err := s.store.AuthenticateUser(r.Context(), req.Login, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Check login limit for auto users
	if err := s.store.CheckLoginAllowed(r.Context(), &user); err != nil {
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}

	_, token, err := s.store.CreateSession(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session error")
		return
	}

	maxAge := 86400
	if user.Source == "auto" {
		maxAge = 7200
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "sshr_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
		Secure:   true,
	})

	s.store.IncrementLoginCount(r.Context(), user.ID)

	log.Printf("user %s logged in via password", user.Username)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"username": user.Username,
		"role":     user.Role,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	cookie, err := r.Cookie("sshr_session")
	if err == nil {
		s.store.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "sshr_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "logged_out"})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	user := s.sessionUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	settings, _ := s.store.GetAllUserSettings(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"username":    user.Username,
		"displayName": user.DisplayName,
		"email":       user.Email,
		"role":        user.Role,
		"settings":    settings,
	})
}

// ─── Email helpers ───

func sendVerificationEmail(domain, to, code string) error {
	host := strings.TrimSpace(os.Getenv("SSHR_SMTP_HOST"))
	port := strings.TrimSpace(os.Getenv("SSHR_SMTP_PORT"))
	user := strings.TrimSpace(os.Getenv("SSHR_SMTP_USERNAME"))
	pass := strings.TrimSpace(os.Getenv("SSHR_SMTP_PASSWORD"))
	from := strings.TrimSpace(os.Getenv("SSHR_SMTP_FROM"))
	if host == "" || user == "" || pass == "" {
		return fmt.Errorf("smtp not configured")
	}
	if port == "" {
		port = "587"
	}
	if from == "" {
		from = user
	}
	subject := "SSH Relay - 登录验证码"
	body := fmt.Sprintf("您的 SSH Relay 登录验证码是: %s\r\n有效期 10 分钟，请勿泄露给他人。\r\n\r\n-- SSH Relay", code)
	return notifier.SendMail(host, host+":"+port, user, pass, from, to, subject, body)
}

const loginHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>SSH Relay - 登录</title>
<style>
  :root { --bg:#0d1117; --card:#161b22; --border:#30363d; --green:#3fb950; --red:#f85149; --blue:#58a6ff; --text:#c9d1d9; --muted:#8b949e; --input-bg:#0d1117; --btn-bg:#238636; }
  * { margin:0; padding:0; box-sizing:border-box; }
  body { background:var(--bg); color:var(--text); font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif; min-height:100vh; display:flex; align-items:center; justify-content:center; }
  .login-box { background:var(--card); border:1px solid var(--border); border-radius:12px; padding:40px; width:380px; max-width:90vw; }
  .login-box h1 { font-size:22px; text-align:center; margin-bottom:8px; }
  .login-box .subtitle { color:var(--muted); font-size:13px; text-align:center; margin-bottom:28px; }
  .field { margin-bottom:16px; }
  .field label { display:block; font-size:13px; margin-bottom:6px; color:var(--muted); }
  .field input { width:100%; padding:10px 14px; background:var(--input-bg); border:1px solid var(--border); border-radius:6px; color:var(--text); font-size:14px; outline:none; }
  .field input:focus { border-color:var(--blue); }
  .btn { width:100%; padding:10px; border:none; border-radius:6px; font-size:14px; cursor:pointer; font-weight:600; color:#fff; }
  .btn-primary { background:var(--btn-bg); }
  .btn-primary:hover { opacity:0.9; }
  .btn-secondary { background:var(--border); margin-top:12px; }
  .btn:disabled { opacity:0.5; cursor:not-allowed; }
  .tabs { display:flex; gap:0; margin-bottom:24px; border-bottom:1px solid var(--border); }
  .tab { flex:1; text-align:center; padding:10px; cursor:pointer; color:var(--muted); font-size:14px; border-bottom:2px solid transparent; }
  .tab.active { color:var(--blue); border-bottom-color:var(--blue); }
  .error { color:var(--red); font-size:12px; margin-top:8px; display:none; }
  .success { color:var(--green); font-size:12px; margin-top:8px; display:none; }
  .footer { text-align:center; margin-top:20px; font-size:12px; color:var(--muted); }
  .footer a { color:var(--blue); text-decoration:none; }
  .footer a:hover { text-decoration:underline; }
</style>
</head>
<body>
<div class="login-box">
  <h1>SSH Relay</h1>
  <p class="subtitle">远程 SSH 中继管理面板</p>

  <div class="tabs">
    <div class="tab active" onclick="switchTab('password')">密码登录</div>
    <div class="tab" onclick="switchTab('code')">验证码登录</div>
  </div>

  <!-- Password login -->
  <div id="tab-password">
    <div class="field"><label>用户名 / 邮箱</label><input id="pw-login" placeholder="用户名或邮箱" autocomplete="username"></div>
    <div class="field"><label>密码</label><input id="pw-pass" type="password" placeholder="密码" autocomplete="current-password"></div>
    <button class="btn btn-primary" onclick="doPasswordLogin()">登 录</button>
    <div class="error" id="pw-err"></div>
  </div>

  <!-- Code login -->
  <div id="tab-code" style="display:none">
    <div class="field"><label>邮箱</label><input id="code-email" type="email" placeholder="请输入注册邮箱"></div>
    <button class="btn btn-primary" id="send-btn" onclick="sendCode()">发送验证码</button>
    <div class="field" style="margin-top:14px"><label>验证码</label><input id="code-input" placeholder="6位验证码"></div>
    <button class="btn btn-primary" onclick="doCodeLogin()">验证并登录</button>
    <div class="success" id="code-sent"></div>
    <div class="error" id="code-err"></div>
  </div>

  <div class="footer">
    没有账号？<a href="https://github.com/mortyandtimo/ssh-relay" target="_blank">GitHub 仓库</a>
    &middot; 联系管理员开通账号
  </div>
</div>
<script>
function switchTab(t){
  document.querySelectorAll('.tab').forEach(function(el){ el.classList.remove('active'); });
  event.target.classList.add('active');
  document.getElementById('tab-password').style.display = t==='password'?'block':'none';
  document.getElementById('tab-code').style.display = t==='code'?'block':'none';
}
function doPasswordLogin(){
  var l=document.getElementById('pw-login').value.trim(), p=document.getElementById('pw-pass').value;
  if(!l||!p){ showErr('pw-err','请填写用户名和密码'); return; }
  fetch('/api/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({login:l,password:p})})
  .then(function(r){ return r.json().then(function(d){ return {ok:r.ok,d:d}; }); })
  .then(function(r){ if(r.ok){ location.reload(); } else { showErr('pw-err',r.d.error); } });
}
function sendCode(){
  var e=document.getElementById('code-email').value.trim();
  if(!e){ showErr('code-err','请填写邮箱'); return; }
  var btn=document.getElementById('send-btn'); btn.disabled=true; btn.textContent='发送中...';
  fetch('/api/auth/send-code',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({email:e})})
  .then(function(r){ return r.json().then(function(d){ return {ok:r.ok,d:d}; }); })
  .then(function(r){
    btn.disabled=false;
    if(r.ok){
      document.getElementById('code-sent').textContent='验证码已发送，10分钟内有效'; document.getElementById('code-sent').style.display='block';
      document.getElementById('code-err').style.display='none';
      btn.textContent='60秒后可重发'; btn.disabled=true;
      var s=60; var t=setInterval(function(){ s--; btn.textContent=s+'秒后可重发'; if(s<=0){ clearInterval(t); btn.textContent='重新发送'; btn.disabled=false; } },1000);
    } else if(r.d.error){ showErr('code-err',r.d.error); btn.textContent='发送验证码'; }
  });
}
function doCodeLogin(){
  var e=document.getElementById('code-email').value.trim(), c=document.getElementById('code-input').value.trim();
  if(!e||!c){ showErr('code-err','请填写邮箱和验证码'); return; }
  fetch('/api/auth/verify-code',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({email:e,code:c})})
  .then(function(r){ return r.json().then(function(d){ return {ok:r.ok,d:d}; }); })
  .then(function(r){ if(r.ok){ location.reload(); } else { showErr('code-err',r.d.error); } });
}
function showErr(id,msg){ var el=document.getElementById(id); el.textContent=msg; el.style.display='block'; }
document.getElementById('pw-pass').addEventListener('keydown',function(e){ if(e.key==='Enter') doPasswordLogin(); });
document.getElementById('code-input').addEventListener('keydown',function(e){ if(e.key==='Enter') doCodeLogin(); });
</script>
</body>
</html>`

const dashboardHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>SSH Relay - 机器仪表盘</title>
<style>
  :root { --bg:#0d1117; --card:#161b22; --border:#30363d; --green:#3fb950; --red:#f85149; --yellow:#d2991d; --blue:#58a6ff; --text:#c9d1d9; --muted:#8b949e; }
  * { margin:0; padding:0; box-sizing:border-box; }
  body { background:var(--bg); color:var(--text); font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif; min-height:100vh; }
  header { background:var(--card); border-bottom:1px solid var(--border); padding:16px 32px; display:flex; justify-content:space-between; align-items:center; flex-wrap:wrap; gap:12px; }
  header h1 { font-size:20px; }
  header .right { display:flex; align-items:center; gap:16px; font-size:13px; }
  header a { color:var(--blue); text-decoration:none; }
  header a:hover { text-decoration:underline; }
  .logout-btn { background:var(--border); color:var(--text); border:none; padding:6px 14px; border-radius:4px; cursor:pointer; font-size:12px; }
  .logout-btn:hover { background:#484f58; }
  .container { max-width:960px; margin:0 auto; padding:24px 16px; }
  .summary { display:flex; gap:16px; margin-bottom:24px; flex-wrap:wrap; }
  .summary-box { background:var(--card); border:1px solid var(--border); border-radius:8px; padding:16px 20px; flex:1; min-width:100px; }
  .summary-box .num { font-size:28px; font-weight:700; }
  .summary-box .label { color:var(--muted); font-size:12px; margin-top:2px; }
  .online .num { color:var(--green); } .offline .num { color:var(--red); } .ports .num { color:var(--blue); }
  .machine-card { background:var(--card); border:1px solid var(--border); border-radius:8px; padding:20px; margin-bottom:16px; }
  .machine-card.offline { opacity:0.6; }
  .machine-header { display:flex; justify-content:space-between; align-items:center; margin-bottom:12px; flex-wrap:wrap; gap:8px; }
  .machine-name { font-size:16px; font-weight:600; }
  .machine-meta { color:var(--muted); font-size:12px; }
  .status-dot { display:inline-block; width:8px; height:8px; border-radius:50%; margin-right:6px; }
  .status-dot.online { background:var(--green); box-shadow:0 0 6px var(--green); }
  .status-dot.offline { background:var(--red); }
  table { width:100%; border-collapse:collapse; font-size:13px; }
  th { text-align:left; color:var(--muted); font-weight:500; padding:8px 12px; border-bottom:1px solid var(--border); }
  td { padding:8px 12px; border-bottom:1px solid var(--border); }
  td code { background:#0d1117; padding:3px 8px; border-radius:4px; font-size:12px; color:var(--blue); white-space:nowrap; }
  .copy-btn { background:var(--border); color:var(--text); border:none; padding:3px 10px; border-radius:4px; cursor:pointer; font-size:11px; }
  .copy-btn:hover { background:#484f58; }
  .empty { text-align:center; color:var(--muted); padding:40px; }
  .empty code { background:#0d1117; padding:3px 8px; border-radius:4px; color:var(--blue); }
  .gpu-tag { background:#1a2332; color:var(--yellow); font-size:11px; padding:2px 8px; border-radius:4px; }
  .refresh { color:var(--muted); font-size:11px; text-align:center; margin-top:16px; }
  .install-box { background:var(--card); border:1px solid var(--border); border-radius:8px; padding:20px; margin-top:24px; }
  .install-box h3 { font-size:15px; margin-bottom:8px; }
  .install-box code { background:#0d1117; padding:4px 10px; border-radius:4px; font-size:12px; color:var(--green); display:block; margin:6px 0; overflow-x:auto; white-space:pre-wrap; word-break:break-all; }
  .install-box p { color:var(--muted); font-size:13px; margin:8px 0; }
  @media(max-width:640px){ .summary{flex-direction:column;} table{font-size:11px;} td code{font-size:10px;padding:2px 4px;} }
  .role-badge { font-size:10px; padding:2px 8px; border-radius:10px; font-weight:600; text-transform:uppercase; }
  .role-badge.super_admin { background:#da3633; color:#fff; }
  .role-badge.admin { background:#d2991d; color:#000; }
  .role-badge.user { background:#30363d; color:var(--muted); }
  .section-title { font-size:15px; font-weight:600; margin:24px 0 12px; display:flex; align-items:center; gap:8px; }
  .section-title .count { color:var(--muted); font-size:12px; font-weight:400; }
  .btn-sm { padding:4px 12px; border-radius:4px; border:none; cursor:pointer; font-size:11px; font-weight:600; }
  .btn-green { background:#238636; color:#fff; }
  .btn-red { background:#da3633; color:#fff; }
  .btn-gray { background:var(--border); color:var(--text); }
  .btn-blue { background:#1f6feb; color:#fff; }
  .btn-sm:hover { opacity:0.85; }
  .toggle-track { width:36px; height:20px; background:var(--border); border-radius:10px; cursor:pointer; position:relative; display:inline-block; vertical-align:middle; }
  .toggle-track.on { background:#238636; }
  .toggle-track .knob { width:16px; height:16px; background:#fff; border-radius:50%; position:absolute; top:2px; left:2px; transition:transform 0.15s; }
  .toggle-track.on .knob { transform:translateX(16px); }
  .modal-overlay { display:none; position:fixed; inset:0; background:rgba(0,0,0,0.6); z-index:100; align-items:center; justify-content:center; }
  .modal-overlay.active { display:flex; }
  .modal-box { background:var(--card); border:1px solid var(--border); border-radius:12px; padding:28px; width:400px; max-width:90vw; }
  .modal-box h3 { font-size:17px; margin-bottom:18px; }
  .modal-box .field { margin-bottom:14px; }
  .modal-box .field label { display:block; font-size:12px; color:var(--muted); margin-bottom:4px; }
  .modal-box .field input, .modal-box .field select { width:100%; padding:8px 12px; background:var(--bg); border:1px solid var(--border); border-radius:6px; color:var(--text); font-size:13px; outline:none; }
  .modal-box .field input:focus, .modal-box .field select:focus { border-color:var(--blue); }
  .modal-actions { display:flex; gap:8px; justify-content:flex-end; margin-top:20px; }
  .modal-box .msg { font-size:12px; margin-top:8px; display:none; }
  .modal-box .msg.error { color:var(--red); }
  .modal-box .msg.success { color:var(--green); }
  .pass-box { display:none; margin-top:20px; padding:16px; background:var(--bg); border:1px solid var(--border); border-radius:8px; }
  .pass-box h4 { font-size:13px; color:var(--muted); margin-bottom:8px; }
  .pass-box input { background:var(--bg); border:1px solid var(--border); color:var(--green); padding:8px; width:100%; border-radius:4px; font-size:14px; }
</style>
</head>
<body>
<header>
  <h1>&#x26A1; SSH Relay 仪表盘</h1>
  <div class="right">
    <span id="login-user"></span>
    <a href="https://github.com/mortyandtimo/ssh-relay" target="_blank">GitHub</a>
    <button class="logout-btn" onclick="logout()">退出登录</button>
  </div>
</header>
<div class="container">
  <div class="summary">
    <div class="summary-box online"><div class="num" id="cnt-online">-</div><div class="label">&#x25CF; 在线</div></div>
    <div class="summary-box offline"><div class="num" id="cnt-offline">-</div><div class="label">&#x25CB; 离线</div></div>
    <div class="summary-box ports"><div class="num" id="cnt-forwards">-</div><div class="label">&#x2194; 转发端口</div></div>
  </div>
  <div id="machines"></div>
  <div class="refresh">每 30 秒自动刷新 &middot; <span id="last-update"></span></div>

  <div class="install-box">
    <h3>&#x1F4E6; 客户端安装</h3>
    <p>在被控端 Linux 机器上执行：</p>
    <code>curl -fsSL https://raw.githubusercontent.com/mortyandtimo/ssh-relay/main/client-install.sh | bash</code>
    <p>或访问 <a href="https://github.com/mortyandtimo/ssh-relay" target="_blank" style="color:var(--blue)">GitHub 仓库</a> 查看完整文档</p>
  </div>

  <div id="admin-panel" style="display:none">
    <div class="section-title">
      &#x1F464; 用户管理 <span class="count" id="user-count"></span>
      <button class="btn-sm btn-green" onclick="openUserModal()" style="margin-left:auto">+ 创建用户</button>
    </div>
    <div id="user-list" class="machine-card" style="padding:16px"></div>
  </div>
  <div class="modal-overlay" id="user-modal">
    <div class="modal-box">
      <h3 id="modal-title">创建用户</h3>
      <div class="field"><label>用户名</label><input id="mu-username" placeholder="用户名"></div>
      <div class="field"><label>邮箱（可选，用于验证码登录）</label><input id="mu-email" type="email" placeholder="user@hrbeu.edu.cn"></div>
      <div class="field"><label>密码</label><input id="mu-password" type="password" placeholder="密码"></div>
      <div class="field"><label>角色</label><select id="mu-role"><option value="user">普通用户</option><option value="admin">管理员</option></select></div>
      <div class="field" id="mu-edit-role" style="display:none"><label>修改角色（仅超级管理员）</label><select id="mu-newrole"><option value="">不改</option><option value="user">普通用户</option><option value="admin">管理员</option></select></div>
      <input type="hidden" id="mu-id">
      <div class="modal-actions">
        <button class="btn-sm btn-gray" onclick="closeUserModal()">取消</button>
        <button class="btn-sm btn-green" id="mu-submit" onclick="saveUser()">创建</button>
      </div>
      <div class="msg error" id="mu-err"></div>
      <div class="msg success" id="mu-ok"></div>
      <div class="pass-box" id="new-pwd-box">
        <h4>新密码</h4>
        <input readonly id="new-pwd-value" onclick="this.select();document.execCommand('copy')">
        <p style="font-size:11px;color:var(--muted);margin-top:4px">点击复制，关闭后将无法再次查看</p>
      </div>
    </div>
  </div>
</div>
<script>
var host = window.location.host;
var currentUser = {};

function load(){
  fetch('/api/auth/me').then(function(r){ return r.json(); }).then(function(d){
    currentUser = d;
    var badge='';
    if(d.role==='super_admin') badge=' <span class="role-badge super_admin">Super Admin</span>';
    else if(d.role==='admin') badge=' <span class="role-badge admin">Admin</span>';
    else badge=' <span class="role-badge user">User</span>';
    document.getElementById('login-user').innerHTML = (d.displayName||d.username) + badge;
    // Show/hide admin panel
    if(d.role==='super_admin' || d.role==='admin'){
      document.getElementById('admin-panel').style.display='block';
      document.getElementById('mu-role').parentElement.style.display=(d.role==='super_admin')?'block':'none';
      loadUsers();
    }
    // Users can see their own ports_visible setting
    var hidePorts = d.role==='user' && d.settings && d.settings.ports_visible==='false';
    loadMachines(hidePorts);
  }).catch(function(){});
}

function loadMachines(hidePorts){
  Promise.all([
    fetch('/api/machines').then(function(r){return r.json();}),
    fetch('/api/forwards').then(function(r){return r.json();})
  ]).then(function(results){
    var machines = results[0].items;
    var allFwds = results[1].items;
    var fwdMap = {};
    allFwds.forEach(function(f){ if(!fwdMap[f.machineId]) fwdMap[f.machineId]=[]; fwdMap[f.machineId].push(f); });
    var online=0, offline=0;
    machines.forEach(function(m){ if(m.status==='online') online++; else offline++; });
    document.getElementById('cnt-online').textContent = online;
    document.getElementById('cnt-offline').textContent = offline;
    document.getElementById('cnt-forwards').textContent = allFwds.length;
    var html = '';
    if(machines.length===0){
      html='<div class="empty">暂无注册机器<br><code>sshr register</code> 在被控 Linux 机器上注册即可在此查看</div>';
    }
    machines.forEach(function(m){
      var isOff=m.status!=='online';
      html+='<div class="machine-card'+(isOff?' offline':'')+'">';
      html+='<div class="machine-header">';
      html+='<div><span class="status-dot '+(m.status==='online'?'online':'offline')+'"></span><span class="machine-name">'+esc(m.name)+'</span></div>';
      html+='<div class="machine-meta">';
      if(m.gpuModel) html+='<span class="gpu-tag">'+esc(m.gpuModel)+'</span> ';
      html+='ID: '+esc(m.id.substring(0,16))+' &middot; '+(m.lastSeenAt?timeAgo(m.lastSeenAt):'从未');
      html+='</div></div>';
      var fwds = fwdMap[m.id] || [];
      if(fwds.length>0){
        if(hidePorts){
          html+='<div style="color:var(--muted);font-size:13px;margin-top:8px;">'+fwds.length+' 个转发（权限受限，请联系管理员查看详情）</div>';
        }else{
          html+='<table><thead><tr><th>端口</th><th>目标</th><th>SSH 连接命令</th><th></th></tr></thead><tbody>';
          fwds.forEach(function(f){
            var cmd = 'ssh -p '+f.publicPort+' &lt;用户名&gt;@'+host;
            html+='<tr><td><code>'+f.publicPort+'</code></td><td>'+esc(f.targetHost)+':'+f.targetPort+'</td><td><code>'+cmd+'</code></td><td><button class="copy-btn" onclick="copy(this,\''+cmd+'\')">复制</button></td></tr>';
          });
          html+='</tbody></table>';
        }
      }else{
        html+='<div style="color:var(--muted);font-size:13px;margin-top:8px;">暂无转发，在该机器上运行 <code>sshr forward</code></div>';
      }
      html+='</div>';
    });
    document.getElementById('machines').innerHTML = html;
    document.getElementById('last-update').textContent = '更新于 ' + new Date().toLocaleTimeString();
  }).catch(function(e){ console.error(e); });
}
function esc(s){ var d=document.createElement('div'); d.textContent=s; return d.innerHTML; }
function timeAgo(ts){ var s=(Date.now()-new Date(ts).getTime())/1000; if(s<60) return Math.floor(s)+'秒前'; if(s<3600) return Math.floor(s/60)+'分钟前'; if(s<86400) return Math.floor(s/3600)+'小时前'; return Math.floor(s/86400)+'天前'; }
function copy(btn,text){ navigator.clipboard.writeText(text).then(function(){ btn.textContent='已复制!'; setTimeout(function(){ btn.textContent='复制'; },1500); }); }
function logout(){ fetch('/api/auth/logout',{method:'POST'}).then(function(){ location.reload(); }); }

// ─── User management JS ───

function loadUsers(){
  fetch('/api/users').then(function(r){ return r.json(); }).then(function(d){
    var items = d.items;
    document.getElementById('user-count').textContent = '('+items.length+'个用户)';
    var html = '';
    if(items.length===0){
      html='<div style="color:var(--muted);font-size:13px;text-align:center;padding:12px">暂无用户</div>';
    }else{
      html+='<table><thead><tr><th>用户名</th><th>邮箱</th><th>角色</th><th>端口可见</th><th>操作</th></tr></thead><tbody>';
      items.forEach(function(u){
        var roleLabel = u.role==='super_admin'?'Super Admin':(u.role==='admin'?'Admin':'User');
        var portsOn = u.settings && u.settings.ports_visible!=='false';
        html+='<tr>';
        html+='<td>'+esc(u.username)+'</td>';
        html+='<td style="color:var(--muted)">'+esc(u.email||'-')+'</td>';
        html+='<td><span class="role-badge '+esc(u.role)+'">'+roleLabel+'</span></td>';
        if(u.role==='user'){
          html+='<td><div class="toggle-track'+(portsOn?' on':'')+'" onclick="togglePorts(\''+u.id+'\',this)"><div class="knob"></div></div></td>';
        }else{
          html+='<td style="color:var(--muted);font-size:11px">-</td>';
        }
        html+='<td>';
        html+='<button class="btn-sm btn-blue" onclick="editUser(\''+u.id+'\',\''+escJs(u.username)+'\',\''+escJs(u.email||'')+'\',\''+escJs(u.role)+'\')">编辑</button> ';
        html+='<button class="btn-sm btn-red" onclick="deleteUser(\''+u.id+'\',\''+escJs(u.username)+'\')">删除</button>';
        html+='</td></tr>';
      });
      html+='</tbody></table>';
    }
    document.getElementById('user-list').innerHTML = html;
  }).catch(function(e){ console.error(e); });
}

function escJs(s){ return s.replace(/\\/g,'\\\\').replace(/'/g,"\\'").replace(/"/g,'\\"'); }

function openUserModal(){
  document.getElementById('modal-title').textContent='创建用户';
  document.getElementById('mu-username').value='';
  document.getElementById('mu-email').value='';
  document.getElementById('mu-password').value='';
  document.getElementById('mu-password').parentElement.style.display='block';
  document.getElementById('mu-role').value='user';
  document.getElementById('mu-id').value='';
  document.getElementById('mu-submit').textContent='创建';
  document.getElementById('mu-err').style.display='none';
  document.getElementById('mu-ok').style.display='none';
  document.getElementById('new-pwd-box').style.display='none';
  document.getElementById('mu-edit-role').style.display='none';
  // Show role selector only for super_admin
  if(currentUser.role==='super_admin'){
    document.getElementById('mu-role').parentElement.style.display='block';
  }else{
    document.getElementById('mu-role').parentElement.style.display='none';
  }
  document.getElementById('user-modal').classList.add('active');
}

function editUser(id, username, email, role){
  document.getElementById('modal-title').textContent='编辑用户: '+username;
  document.getElementById('mu-username').value=username;
  document.getElementById('mu-email').value=email;
  document.getElementById('mu-password').value='';
  document.getElementById('mu-password').parentElement.style.display='block';
  document.getElementById('mu-role').parentElement.style.display='none';
  document.getElementById('mu-id').value=id;
  document.getElementById('mu-submit').textContent='保存';
  document.getElementById('mu-err').style.display='none';
  document.getElementById('mu-ok').style.display='none';
  document.getElementById('new-pwd-box').style.display='none';
  // Show role edit for super_admin
  if(currentUser.role==='super_admin' && role!=='super_admin'){
    document.getElementById('mu-edit-role').style.display='block';
    document.getElementById('mu-newrole').value='';
  }else{
    document.getElementById('mu-edit-role').style.display='none';
  }
  document.getElementById('user-modal').classList.add('active');
}

function closeUserModal(){
  document.getElementById('user-modal').classList.remove('active');
  document.getElementById('new-pwd-box').style.display='none';
}

function saveUser(){
  var id = document.getElementById('mu-id').value;
  var username = document.getElementById('mu-username').value.trim();
  var email = document.getElementById('mu-email').value.trim();
  var password = document.getElementById('mu-password').value;
  var role = document.getElementById('mu-role').value;
  var errEl = document.getElementById('mu-err');
  var okEl = document.getElementById('mu-ok');
  errEl.style.display='none';
  okEl.style.display='none';
  document.getElementById('new-pwd-box').style.display='none';

  if(!username){ errEl.textContent='用户名不能为空'; errEl.style.display='block'; return; }

  if(id){
    // Update
    var body = {};
    if(email) body.email = email;
    if(password) body.password = password;
    if(currentUser.role==='super_admin'){
      var nr = document.getElementById('mu-newrole').value;
      if(nr) body.role = nr;
    }
    fetch('/api/users/'+id, {method:'PUT', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body)})
    .then(function(r){ return r.json().then(function(d){ return {ok:r.ok, d:d}; }); })
    .then(function(r){
      if(r.ok){
        okEl.textContent='保存成功';
        okEl.style.display='block';
        if(password){
          document.getElementById('new-pwd-value').value=password;
          document.getElementById('new-pwd-box').style.display='block';
        }
        loadUsers();
      }else{
        errEl.textContent=r.d.error||'保存失败';
        errEl.style.display='block';
      }
    });
  }else{
    // Create
    if(!password){ errEl.textContent='密码不能为空'; errEl.style.display='block'; return; }
    fetch('/api/users', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({username:username, email:email, password:password, role:role})})
    .then(function(r){ return r.json().then(function(d){ return {ok:r.ok, d:d}; }); })
    .then(function(r){
      if(r.ok){
        okEl.textContent='创建成功';
        okEl.style.display='block';
        document.getElementById('new-pwd-value').value=password;
        document.getElementById('new-pwd-box').style.display='block';
        loadUsers();
      }else{
        errEl.textContent=r.d.error||'创建失败';
        errEl.style.display='block';
      }
    });
  }
}

function deleteUser(id, name){
  if(!confirm('确定要删除用户 \''+name+'\' 吗？此操作不可恢复。')) return;
  fetch('/api/users/'+id, {method:'DELETE'})
  .then(function(r){ return r.json().then(function(d){ return {ok:r.ok, d:d}; }); })
  .then(function(r){
    if(r.ok){ loadUsers(); }
    else { alert(r.d.error||'删除失败'); }
  });
}

function togglePorts(userId, track){
  var isOn = track.classList.contains('on');
  var newVal = isOn?'false':'true';
  fetch('/api/users/'+userId+'/settings', {method:'PUT', headers:{'Content-Type':'application/json'}, body:JSON.stringify({ports_visible:newVal})})
  .then(function(r){ return r.json(); })
  .then(function(){
    if(newVal==='true'){ track.classList.add('on'); }else{ track.classList.remove('on'); }
  });
}

load();
setInterval(load, 30000);
</script>
</body>
</html>`
