package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/25743/cloud-relay-platform/apps/server-api/internal/store"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

const (
	accessCookieName     = "crp_access"
	refreshCookieName    = "crp_refresh"
	sessionCookieName    = "crp_session"
	nodeOfflineThreshold = 90 * time.Second
)

type contextKey string

const authUserContextKey contextKey = "auth-user"

type authClaims struct {
	UserID string
	Role   types.UserRole
	Expiry time.Time
}

type Server struct {
	version              string
	startedAt            time.Time
	store                store.Store
	relayTCPRuntimeURL   string
	httpClient           *http.Client
	mux                  *http.ServeMux
	accessSecret         string
	adminWebDir          string
	accessTokenTTL       time.Duration
	refreshTokenTTL      time.Duration
	adminBootstrapSecret string
	allowedOrigins       map[string]struct{}
	authCookiesSecure    bool
}

func NewServer(version string, backend store.Store, relayTCPRuntimeURL string) *Server {
	s := &Server{
		version:              version,
		startedAt:            time.Now().UTC(),
		store:                backend,
		relayTCPRuntimeURL:   relayTCPRuntimeURL,
		httpClient:           &http.Client{Timeout: 5 * time.Second},
		mux:                  http.NewServeMux(),
		accessSecret:         envOrDefault("SERVER_API_ACCESS_SECRET", "cloud-relay-access-secret-dev"),
		adminWebDir:          envOrDefault("SERVER_API_ADMIN_WEB_DIR", "/opt/cloud-relay-platform/admin-web"),
		accessTokenTTL:       15 * time.Minute,
		refreshTokenTTL:      7 * 24 * time.Hour,
		adminBootstrapSecret: strings.TrimSpace(os.Getenv("SERVER_API_ADMIN_BOOTSTRAP_SECRET")),
		allowedOrigins:       parseAllowedOrigins(os.Getenv("SERVER_API_ALLOWED_ORIGINS")),
		authCookiesSecure:    parseBoolEnv(os.Getenv("SERVER_API_AUTH_COOKIES_SECURE")),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.withCORS(s.mux)
}

func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/agent/register", s.handleRegister)
	s.mux.HandleFunc("/agent/heartbeat", s.handleHeartbeat)
	s.mux.HandleFunc("/agent/tunnels", s.handleAgentTunnels)
	s.mux.HandleFunc("/internal/routes/tcp", s.handleTCPRoutes)
	s.mux.HandleFunc("/internal/routes/http", s.handleHTTPRoutes)
	s.mux.HandleFunc("/internal/routes/https", s.handleHTTPSRoutes)

	s.mux.HandleFunc("/api/auth/bootstrap-status", s.handleBootstrapStatus)
	s.mux.HandleFunc("/api/auth/bootstrap", s.handleBootstrap)
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/refresh", s.handleRefresh)
	s.mux.HandleFunc("/api/auth/logout", s.handleLogout)
	s.mux.Handle("/api/auth/me", s.requireRole(types.UserRoleUser, http.HandlerFunc(s.handleAuthMe)))

	s.mux.Handle("/api/users", s.requireRole(types.UserRoleAdmin, http.HandlerFunc(s.handleUsers)))
	s.mux.Handle("/api/users/", s.requireRole(types.UserRoleAdmin, http.HandlerFunc(s.handleUserByID)))
	s.mux.Handle("/api/nodes", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleNodes)))
	s.mux.Handle("/api/node-options", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleNodeOptions)))
	s.mux.Handle("/api/nodes/", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleNodeByID)))
	s.mux.Handle("/api/tunnels", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleTunnels)))
	s.mux.Handle("/api/tunnels/", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleTunnelByID)))
	s.mux.Handle("/api/server/metrics", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleServerMetrics)))
	s.mux.Handle("/api/relay/tcp/runtime", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleRelayTCPRuntime)))
	s.mux.Handle("/api/audit-logs", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleAuditLogs)))

	s.mux.Handle("/admin/", s.adminSPAHandler())
	s.mux.Handle("/admin", s.adminSPAHandler())
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
	writeJSON(w, http.StatusOK, types.NodeRegisterResponse{NodeID: summary.NodeID, RegisteredAt: summary.LastSeenAt, RecommendedPeriod: 30})
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
	writeJSON(w, http.StatusOK, map[string]any{"status": "accepted", "observedAt": time.Now().UTC()})
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
	filter := store.TunnelFilter{NodeID: nodeID, Type: r.URL.Query().Get("type"), Status: r.URL.Query().Get("status")}
	if filter.Type == "" {
		filter.Type = ""
	}
	if filter.Status == "" {
		filter.Status = "active"
	}
	items, err := s.store.ListTunnels(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	visible := make([]types.TunnelSpec, 0, len(items))
	for _, item := range items {
		visible = append(visible, agentVisibleTunnelSpec(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": visible})
}

func (s *Server) handleBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	required, err := s.store.BootstrapStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types.AuthBootstrapStatusResponse{Required: required})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if err := s.authorizeBootstrap(r); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	required, err := s.store.BootstrapStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !required {
		writeError(w, http.StatusConflict, "bootstrap already completed")
		return
	}
	var req types.BootstrapAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid bootstrap payload")
		return
	}
	user, err := s.store.BootstrapAdmin(r.Context(), store.CreateUserParams{Email: req.Email, DisplayName: req.DisplayName, Password: req.Password, Role: types.UserRoleAdmin})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.issueAuthSession(w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeAudit(r, "bootstrap_admin", "user", user.ID, map[string]string{"email": user.Email, "role": string(user.Role)})
	writeJSON(w, http.StatusCreated, types.AuthUserResponse{User: user})
}

func (s *Server) authorizeBootstrap(r *http.Request) error {
	if s.adminBootstrapSecret == "" {
		return errors.New("bootstrap is disabled until SERVER_API_ADMIN_BOOTSTRAP_SECRET is configured")
	}
	secret := strings.TrimSpace(r.Header.Get("X-Bootstrap-Secret"))
	if secret == "" {
		secret = strings.TrimSpace(r.URL.Query().Get("bootstrapSecret"))
	}
	if secret == "" || secret != s.adminBootstrapSecret {
		return errors.New("invalid bootstrap secret")
	}
	return nil
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req types.AuthLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid login payload")
		return
	}
	user, err := s.store.AuthenticateUser(r.Context(), store.AuthenticateUserParams{Email: req.Email, Password: req.Password})
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err := s.issueAuthSession(w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeAudit(r, "login", "session", "", map[string]string{"userId": user.ID, "email": user.Email})
	writeJSON(w, http.StatusOK, types.AuthUserResponse{User: user})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	session, refreshToken, err := s.requireRefreshSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if time.Now().UTC().After(session.RefreshExpiresAt) {
		_ = s.store.DeleteWebSession(r.Context(), session.SessionID)
		s.clearAuthCookies(w)
		writeError(w, http.StatusUnauthorized, "refresh token expired")
		return
	}
	if hashOpaqueToken(refreshToken) != session.RefreshTokenHash {
		_ = s.store.DeleteWebSession(r.Context(), session.SessionID)
		s.clearAuthCookies(w)
		writeError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	user, err := s.store.GetUser(r.Context(), session.UserID)
	if err != nil {
		s.clearAuthCookies(w)
		writeError(w, http.StatusUnauthorized, "session user not found")
		return
	}
	if err := s.rotateAuthSession(w, r, session, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types.AuthUserResponse{User: user})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	actorType := "system"
	actorID := ""
	if sessionCookie, err := r.Cookie(sessionCookieName); err == nil {
		if session, err := s.store.GetWebSessionByID(r.Context(), sessionCookie.Value); err == nil {
			actorType = "user"
			actorID = session.UserID
		}
		_ = s.store.DeleteWebSession(r.Context(), sessionCookie.Value)
	}
	s.clearAuthCookies(w)
	_, _ = s.store.WriteAuditLog(r.Context(), store.AuditLogParams{ActorType: actorType, ActorID: actorID, Action: "logout", ResourceType: "session"})
	writeJSON(w, http.StatusOK, map[string]any{"status": "logged_out"})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	user, ok := authUserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, types.AuthUserResponse{User: user})
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListUsers(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var req types.CreateUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid user payload")
			return
		}
		user, err := s.store.CreateUser(r.Context(), store.CreateUserParams{Email: req.Email, DisplayName: req.DisplayName, Password: req.Password, Role: req.Role})
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrConflict) {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		s.writeAudit(r, "create_user", "user", user.ID, map[string]string{"email": user.Email, "role": string(user.Role)})
		writeJSON(w, http.StatusCreated, user)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (s *Server) handleUserByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/users/"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "user id is required")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var req types.UpdateUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid user payload")
			return
		}
		user, err := s.store.UpdateUser(r.Context(), store.UpdateUserParams{ID: id, DisplayName: req.DisplayName, Password: req.Password, Role: req.Role})
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		s.writeAudit(r, "update_user", "user", user.ID, map[string]string{"email": user.Email, "role": string(user.Role)})
		writeJSON(w, http.StatusOK, user)
	case http.MethodDelete:
		user, err := s.store.GetUser(r.Context(), id)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		if err := s.store.DeleteUser(r.Context(), id); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		s.writeAudit(r, "delete_user", "user", id, map[string]string{"email": user.Email, "displayName": user.DisplayName, "role": string(user.Role)})
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	default:
		writeMethodNotAllowed(w, http.MethodPut+", "+http.MethodDelete)
	}
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := parsePositiveInt(r.URL.Query().Get("limit"), 20)
		offset := parseNonNegativeInt(r.URL.Query().Get("offset"), 0)
		items, total, err := s.store.ListNodes(r.Context(), store.NodeFilter{
			NodeRole:    strings.TrimSpace(r.URL.Query().Get("nodeRole")),
			Environment: strings.TrimSpace(r.URL.Query().Get("environment")),
			TrustLevel:  strings.TrimSpace(r.URL.Query().Get("trustLevel")),
			Owner:       strings.TrimSpace(r.URL.Query().Get("owner")),
			Tag:         strings.TrimSpace(r.URL.Query().Get("tag")),
			Limit:       limit,
			Offset:      offset,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, types.NodeListResponse{Items: items, Total: total, Limit: limit, Offset: offset})
	default:
		writeMethodNotAllowed(w, http.MethodGet)
	}
}

func (s *Server) handleNodeOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	items, _, err := s.store.ListNodes(r.Context(), store.NodeFilter{Limit: 100000, Offset: 0})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	options := make([]types.NodeOption, 0, len(items))
	for _, item := range items {
		options = append(options, types.NodeOption{NodeID: item.NodeID, NodeName: item.NodeName, Status: item.Status, SupportsTCP: item.Capabilities.TCPRelay, SupportsUDP: item.Capabilities.UDPRelay, SupportsHTTP: item.Capabilities.HTTPRelay, SupportsHTTPS: item.Capabilities.HTTPSRelay || item.Capabilities.HTTPRelay, SupportsSOCKS5: item.Capabilities.SOCKS5Connect, Isolated: item.Isolated})
	}
	writeJSON(w, http.StatusOK, types.NodeOptionsResponse{Items: options})
}

func (s *Server) handleNodeByID(w http.ResponseWriter, r *http.Request) {
	nodeID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/nodes/"))
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "node id is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		node, err := s.store.GetNode(r.Context(), nodeID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, node)
	case http.MethodPut:
		var req types.UpdateNodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid node payload")
			return
		}
		node, err := s.store.UpdateNode(r.Context(), store.UpdateNodeParams{
			NodeID:      nodeID,
			NodeRole:    req.NodeRole,
			Environment: req.Environment,
			TrustLevel:  req.TrustLevel,
			Owner:       req.Owner,
			Location:    req.Location,
			Tags:        req.Tags,
			Isolated:    req.Isolated,
		})
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		s.writeAudit(r, "update_node", "node", node.NodeID, map[string]string{
			"nodeRole":    string(node.NodeRole),
			"environment": string(node.Environment),
			"trustLevel":  string(node.TrustLevel),
			"owner":       node.Owner,
			"location":    node.Location,
			"tags":        strings.Join(node.Tags, ","),
			"isolated":    fmt.Sprintf("%t", node.Isolated),
		})
		writeJSON(w, http.StatusOK, node)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}

func (s *Server) handleTunnels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListTunnels(r.Context(), store.TunnelFilter{NodeID: r.URL.Query().Get("nodeId"), Type: r.URL.Query().Get("type"), Status: r.URL.Query().Get("status")})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": s.withTunnelHealth(r.Context(), items)})
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
		normalized, err := normalizeManagedTunnelSpec(spec)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		spec = normalized
		if err := s.ensureTunnelNodeCapability(r.Context(), req.NodeID, spec.Type); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		if err := s.ensureHTTPSDomainUnique(r.Context(), spec, ""); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrConflict) {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
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
		tunnel = s.withTunnelHealthOne(r.Context(), tunnel)
		s.writeAudit(r, "create_tunnel", "tunnel", tunnel.ID, map[string]string{"nodeId": tunnel.NodeID, "publicPort": fmt.Sprintf("%d", tunnel.PublicPort)})
		writeJSON(w, http.StatusCreated, tunnel)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (s *Server) handleTunnelByID(w http.ResponseWriter, r *http.Request) {
	pathValue := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/tunnels/"))
	if pathValue == "" {
		writeError(w, http.StatusBadRequest, "tunnel id is required")
		return
	}
	parts := strings.Split(strings.Trim(pathValue, "/"), "/")
	id := strings.TrimSpace(parts[0])
	if id == "" {
		writeError(w, http.StatusBadRequest, "tunnel id is required")
		return
	}
	if len(parts) == 2 && parts[1] == "probe" {
		s.handleTunnelProbe(w, r, id)
		return
	}
	if len(parts) > 1 {
		writeError(w, http.StatusNotFound, "not found")
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
		writeJSON(w, http.StatusOK, s.withTunnelHealthOne(r.Context(), tunnel))
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
		normalized, err := normalizeManagedTunnelSpec(spec)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		spec = normalized
		if err := s.ensureTunnelNodeCapability(r.Context(), req.NodeID, spec.Type); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		if err := s.ensureHTTPSDomainUnique(r.Context(), spec, id); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrConflict) {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
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
		tunnel = s.withTunnelHealthOne(r.Context(), tunnel)
		s.writeAudit(r, "update_tunnel", "tunnel", tunnel.ID, map[string]string{"nodeId": tunnel.NodeID, "publicPort": fmt.Sprintf("%d", tunnel.PublicPort), "status": tunnel.Status})
		writeJSON(w, http.StatusOK, tunnel)
	case http.MethodDelete:
		tunnel, err := s.store.GetTunnel(r.Context(), id)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		if err := s.store.DeleteTunnel(r.Context(), id); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		s.writeAudit(r, "delete_tunnel", "tunnel", id, map[string]string{"nodeId": tunnel.NodeID, "publicPort": fmt.Sprintf("%d", tunnel.PublicPort), "targetHost": tunnel.TargetHost, "targetPort": fmt.Sprintf("%d", tunnel.TargetPort)})
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func (s *Server) handleTunnelProbe(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	tunnel, err := s.store.GetTunnel(r.Context(), id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	result, err := s.probeTunnel(r.Context(), tunnel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updatedTunnel := tunnel
	if updatedTunnel.Metadata == nil {
		updatedTunnel.Metadata = map[string]string{}
	}
	updatedTunnel.LastProbeSuccess = result.Success
	updatedTunnel.LastProbeStatusCode = result.StatusCode
	updatedTunnel.LastProbeError = result.Error
	updatedTunnel.LastProbedAt = result.ProbedAt
	updatedTunnel.LastProbeTargetEntry = result.TargetEntry
	updatedTunnel.Metadata["lastProbeSuccess"] = fmt.Sprintf("%t", result.Success)
	updatedTunnel.Metadata["lastProbeStatusCode"] = fmt.Sprintf("%d", result.StatusCode)
	updatedTunnel.Metadata["lastProbeError"] = result.Error
	updatedTunnel.Metadata["lastProbedAt"] = result.ProbedAt.Format(time.RFC3339Nano)
	updatedTunnel.Metadata["lastProbeTargetEntry"] = result.TargetEntry
	if _, err := s.store.UpdateTunnel(r.Context(), updatedTunnel); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) probeTunnel(ctx context.Context, tunnel types.TunnelSpec) (types.TunnelProbeResult, error) {
	entry, err := probeTunnelEntry(tunnel)
	if err != nil {
		return types.TunnelProbeResult{}, err
	}
	result := types.TunnelProbeResult{TunnelID: tunnel.ID, ProbedAt: time.Now().UTC(), TargetEntry: entry}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry, nil)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	result.Success = resp.StatusCode >= 200 && resp.StatusCode < 400
	if !result.Success {
		result.Error = resp.Status
	}
	return result, nil
}

func probeTunnelEntry(tunnel types.TunnelSpec) (string, error) {
	probePath := normalizeProbePath(tunnel.ProbePath)
	switch tunnel.Type {
	case "http":
		return fmt.Sprintf("http://82.156.236.104:%d%s", tunnel.PublicPort, probePath), nil
	case "https":
		if strings.TrimSpace(tunnel.Domain) == "" {
			return "", errors.New("https tunnel requires domain for probe")
		}
		return "https://" + strings.TrimSpace(tunnel.Domain) + probePath, nil
	default:
		return "", errors.New("probe only supports http and https tunnels")
	}
}

func normalizeProbePath(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "/"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return value
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
	writeJSON(w, http.StatusOK, types.ServerMetrics{Service: "server-api", StartedAt: s.startedAt, RegisteredNodes: counts.RegisteredNodes, OnlineNodes: counts.OnlineNodes, ConfiguredTunnels: counts.ConfiguredTunnels, ProtocolRelayCount: 3})
}

func (s *Server) handleTCPRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	items, err := s.store.ListTunnels(r.Context(), store.TunnelFilter{Status: "active"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleHTTPRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	items, err := s.store.ListTunnels(r.Context(), store.TunnelFilter{Type: "http", Status: "active"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.withTunnelHealth(r.Context(), items)})
}

func (s *Server) handleHTTPSRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	items, err := s.store.ListTunnels(r.Context(), store.TunnelFilter{Type: "https", Status: "active"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.withTunnelHealth(r.Context(), items)})
}

func (s *Server) handleAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	filter := store.AuditLogFilter{
		Action:       strings.TrimSpace(r.URL.Query().Get("action")),
		ActorType:    strings.TrimSpace(r.URL.Query().Get("actorType")),
		ActorID:      strings.TrimSpace(r.URL.Query().Get("actorID")),
		ResourceType: strings.TrimSpace(r.URL.Query().Get("resourceType")),
		ResourceID:   strings.TrimSpace(r.URL.Query().Get("resourceID")),
		Limit:        50,
		Offset:       0,
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := parseInt64(raw); err == nil && parsed > 0 {
			filter.Limit = int(parsed)
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if parsed, err := parseInt64(raw); err == nil && parsed >= 0 {
			filter.Offset = int(parsed)
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("startAt")); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			filter.StartAt = &parsed
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("endAt")); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			filter.EndAt = &parsed
		}
	}
	items, total, err := s.store.ListAuditLogs(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types.AuditLogListResponse{Items: items, Total: total, Limit: filter.Limit, Offset: filter.Offset})
}

func (s *Server) handleRelayTCPRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if s.relayTCPRuntimeURL == "" {
		writeJSON(w, http.StatusOK, types.RelayRuntimeSummary{Service: "relay-tcp", ObservedAt: time.Now().UTC(), Pools: []types.RelayPoolSummary{}})
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

type cachedTunnelNode struct {
	summary types.NodeSummary
	found   bool
}

func (s *Server) withTunnelHealth(ctx context.Context, items []types.TunnelSpec) []types.TunnelSpec {
	if len(items) == 0 {
		return items
	}
	cache := map[string]cachedTunnelNode{}
	reachability := s.httpReachabilityState(ctx)
	out := make([]types.TunnelSpec, 0, len(items))
	for _, item := range items {
		out = append(out, s.withTunnelHealthCached(ctx, item, cache, reachability))
	}
	return out
}

func (s *Server) withTunnelHealthOne(ctx context.Context, item types.TunnelSpec) types.TunnelSpec {
	return s.withTunnelHealthCached(ctx, item, map[string]cachedTunnelNode{}, s.httpReachabilityState(ctx))
}

func (s *Server) withTunnelHealthCached(ctx context.Context, item types.TunnelSpec, cache map[string]cachedTunnelNode, reachability map[string]types.TunnelHealthStatus) types.TunnelSpec {
	item.HealthStatus = s.deriveTunnelHealth(ctx, item, cache, reachability)
	return item
}

func (s *Server) deriveTunnelHealth(ctx context.Context, tunnel types.TunnelSpec, cache map[string]cachedTunnelNode, reachability map[string]types.TunnelHealthStatus) types.TunnelHealthStatus {
	if isTunnelMisconfigured(tunnel) {
		return types.TunnelHealthMisconfigured
	}
	nodeID := strings.TrimSpace(tunnel.NodeID)
	if nodeID == "" {
		return types.TunnelHealthMisconfigured
	}
	node, ok := cache[nodeID]
	if !ok {
		summary, err := s.store.GetNode(ctx, nodeID)
		if err != nil {
			cache[nodeID] = cachedTunnelNode{found: false}
			return types.TunnelHealthNodeOffline
		}
		node = cachedTunnelNode{summary: summary, found: true}
		cache[nodeID] = node
	}
	if !node.found || isNodeOffline(node.summary) {
		return types.TunnelHealthNodeOffline
	}
	if tunnel.Type == "socks5" && !node.summary.Capabilities.SOCKS5Connect {
		return types.TunnelHealthCapabilityMissing
	}
	if (tunnel.Type == "http" || tunnel.Type == "https") && !node.summary.Capabilities.HTTPRelay {
		return types.TunnelHealthCapabilityMissing
	}
	if tunnel.Type == "http" || tunnel.Type == "https" {
		if status, ok := reachability[httpReachabilityMetricKey(tunnel.NodeID, tunnel.PublicPort)]; ok && status == types.TunnelHealthTargetUnreachable {
			return types.TunnelHealthTargetUnreachable
		}
	}
	return types.TunnelHealthHealthy
}

func isTunnelMisconfigured(tunnel types.TunnelSpec) bool {
	if strings.TrimSpace(tunnel.NodeID) == "" || tunnel.PublicPort <= 0 {
		return true
	}
	switch tunnel.Type {
	case "", "tcp", "http":
		return strings.TrimSpace(tunnel.TargetHost) == "" || tunnel.TargetPort <= 0
	case "https":
		return strings.TrimSpace(tunnel.TargetHost) == "" || tunnel.TargetPort <= 0 || strings.TrimSpace(tunnel.Domain) == "" || strings.TrimSpace(tunnel.TLSMode) != "edge_terminate"
	case "socks5":
		return strings.TrimSpace(tunnel.TargetHost) != "socks5" || tunnel.TargetPort != 1080
	default:
		return true
	}
}

func isNodeOffline(node types.NodeSummary) bool {
	if strings.TrimSpace(node.Status) != "online" {
		return true
	}
	if node.LastSeenAt.IsZero() {
		return true
	}
	return time.Since(node.LastSeenAt.UTC()) > nodeOfflineThreshold
}

func httpReachabilityMetricKey(nodeID string, publicPort int) string {
	return "httpReachability:" + nodeID + ":" + fmt.Sprintf("%d", publicPort)
}

func (s *Server) httpReachabilityState(ctx context.Context) map[string]types.TunnelHealthStatus {
	out := map[string]types.TunnelHealthStatus{}
	if s.store.Kind() != "postgres" {
		return out
	}
	postgresStore, ok := s.store.(*store.PostgresStore)
	if !ok {
		return out
	}
	rows, err := postgresStore.DB().Query(ctx, `
		select node_id, payload
		from (
			select node_id, payload, row_number() over (partition by node_id order by observed_at desc) as rn
			from node_metrics
		) latest
		where rn = 1
	`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var nodeID string
		var payloadJSON []byte
		if err := rows.Scan(&nodeID, &payloadJSON); err != nil {
			continue
		}
		metrics := map[string]string{}
		if err := json.Unmarshal(payloadJSON, &metrics); err != nil {
			continue
		}
		for key, value := range metrics {
			if !strings.HasPrefix(key, "httpReachability:") {
				continue
			}
			out[key] = types.TunnelHealthStatus(value)
		}
	}
	return out
}

func agentVisibleTunnelSpec(item types.TunnelSpec) types.TunnelSpec {
	return item
}

func (s *Server) currentActor(r *http.Request) (string, string) {
	if user, ok := authUserFromContext(r.Context()); ok {
		return "user", user.ID
	}
	return "system", ""
}

func (s *Server) writeAudit(r *http.Request, action, resourceType, resourceID string, payload map[string]string) {
	actorType, actorID := s.currentActor(r)
	_, _ = s.store.WriteAuditLog(r.Context(), store.AuditLogParams{
		ActorType:    actorType,
		ActorID:      actorID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Payload:      payload,
	})
}

func (s *Server) requireRole(minRole types.UserRole, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.authenticateRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if !roleAllowed(user.Role, minRole) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authUserContextKey, user)))
	})
}

func (s *Server) authenticateRequest(r *http.Request) (types.UserSummary, error) {
	claims, err := s.readAccessClaims(r)
	if err != nil {
		return types.UserSummary{}, err
	}
	user, err := s.store.GetUser(r.Context(), claims.UserID)
	if err != nil {
		return types.UserSummary{}, store.ErrUnauthorized
	}
	if user.Role != claims.Role {
		return types.UserSummary{}, store.ErrUnauthorized
	}
	return user, nil
}

func (s *Server) issueAuthSession(w http.ResponseWriter, r *http.Request, user types.UserSummary) error {
	sessionID, err := randomToken(24)
	if err != nil {
		return err
	}
	refreshToken, err := randomToken(32)
	if err != nil {
		return err
	}
	refreshExpiry := time.Now().UTC().Add(s.refreshTokenTTL)
	_, err = s.store.CreateWebSession(r.Context(), store.CreateSessionParams{SessionID: sessionID, UserID: user.ID, RefreshTokenHash: hashOpaqueToken(refreshToken), RefreshExpiresAt: refreshExpiry, RemoteAddr: clientIP(r), UserAgent: r.UserAgent()})
	if err != nil {
		return err
	}
	return s.writeAuthCookies(w, sessionID, refreshToken, refreshExpiry, user)
}

func (s *Server) rotateAuthSession(w http.ResponseWriter, r *http.Request, session store.WebSession, user types.UserSummary) error {
	refreshToken, err := randomToken(32)
	if err != nil {
		return err
	}
	refreshExpiry := time.Now().UTC().Add(s.refreshTokenTTL)
	_, err = s.store.RotateWebSession(r.Context(), session.SessionID, hashOpaqueToken(refreshToken), refreshExpiry, clientIP(r), r.UserAgent())
	if err != nil {
		return err
	}
	return s.writeAuthCookies(w, session.SessionID, refreshToken, refreshExpiry, user)
}

func (s *Server) requireRefreshSession(r *http.Request) (store.WebSession, string, error) {
	sessionCookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return store.WebSession{}, "", store.ErrUnauthorized
	}
	refreshCookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		return store.WebSession{}, "", store.ErrUnauthorized
	}
	session, err := s.store.GetWebSessionByID(r.Context(), sessionCookie.Value)
	if err != nil {
		return store.WebSession{}, "", err
	}
	return session, refreshCookie.Value, nil
}

func (s *Server) writeAuthCookies(w http.ResponseWriter, sessionID, refreshToken string, refreshExpiry time.Time, user types.UserSummary) error {
	accessToken, accessExpiry, err := s.createAccessToken(user)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: accessCookieName, Value: accessToken, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: accessExpiry, Secure: s.authCookiesSecure})
	http.SetCookie(w, &http.Cookie{Name: refreshCookieName, Value: refreshToken, Path: "/api/auth", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: refreshExpiry, Secure: s.authCookiesSecure})
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: sessionID, Path: "/api/auth", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: refreshExpiry, Secure: s.authCookiesSecure})
	return nil
}

func (s *Server) clearAuthCookies(w http.ResponseWriter) {
	for _, item := range []struct{ name, path string }{{accessCookieName, "/"}, {refreshCookieName, "/api/auth"}, {sessionCookieName, "/api/auth"}} {
		http.SetCookie(w, &http.Cookie{Name: item.name, Value: "", Path: item.path, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(0, 0), Secure: s.authCookiesSecure})
	}
}

func (s *Server) createAccessToken(user types.UserSummary) (string, time.Time, error) {
	expiry := time.Now().UTC().Add(s.accessTokenTTL)
	payload := fmt.Sprintf("%s|%s|%d", user.ID, user.Role, expiry.Unix())
	sig := signValue(payload, s.accessSecret)
	token := base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + sig))
	return token, expiry, nil
}

func (s *Server) readAccessClaims(r *http.Request) (authClaims, error) {
	cookie, err := r.Cookie(accessCookieName)
	if err != nil {
		return authClaims{}, store.ErrUnauthorized
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return authClaims{}, store.ErrUnauthorized
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 {
		return authClaims{}, store.ErrUnauthorized
	}
	payload := strings.Join(parts[:3], "|")
	if signValue(payload, s.accessSecret) != parts[3] {
		return authClaims{}, store.ErrUnauthorized
	}
	expUnix, err := parseInt64(parts[2])
	if err != nil {
		return authClaims{}, store.ErrUnauthorized
	}
	expiry := time.Unix(expUnix, 0).UTC()
	if time.Now().UTC().After(expiry) {
		return authClaims{}, store.ErrUnauthorized
	}
	return authClaims{UserID: parts[0], Role: types.UserRole(parts[1]), Expiry: expiry}, nil
}

func (s *Server) adminSPAHandler() http.Handler {
	fileServer := http.FileServer(http.Dir(s.adminWebDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(s.adminWebDir); err != nil {
			http.NotFound(w, r)
			return
		}
		cleanPath := strings.TrimPrefix(r.URL.Path, "/admin")
		cleanPath = strings.TrimPrefix(cleanPath, "/")
		if cleanPath == "" {
			http.ServeFile(w, r, filepath.Join(s.adminWebDir, "index.html"))
			return
		}
		target := filepath.Join(s.adminWebDir, cleanPath)
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			http.StripPrefix("/admin/", fileServer).ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(s.adminWebDir, "index.html"))
	})
}

func authUserFromContext(ctx context.Context) (types.UserSummary, bool) {
	user, ok := ctx.Value(authUserContextKey).(types.UserSummary)
	return user, ok
}

func roleAllowed(actual, required types.UserRole) bool {
	rank := func(role types.UserRole) int {
		switch role {
		case types.UserRoleAdmin:
			return 3
		case types.UserRoleManager:
			return 2
		case types.UserRoleUser:
			return 1
		default:
			return 0
		}
	}
	return rank(actual) >= rank(required)
}

func randomToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashOpaqueToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func signValue(value, secret string) string {
	sum := sha256.Sum256([]byte(secret + ":" + value))
	return hex.EncodeToString(sum[:])
}

func parseInt64(raw string) (int64, error) {
	var value int64
	_, err := fmt.Sscanf(raw, "%d", &value)
	return value, err
}

func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	return r.RemoteAddr
}

func parseBoolEnv(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parsePositiveInt(raw string, fallback int) int {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseNonNegativeInt(raw string, fallback int) int {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func normalizeManagedTunnelSpec(spec types.TunnelSpec) (types.TunnelSpec, error) {
	spec.Type = strings.TrimSpace(spec.Type)
	if spec.Type == "" {
		spec.Type = "tcp"
	}
	switch spec.Type {
	case "tcp":
		if strings.TrimSpace(spec.TargetHost) == "" || spec.TargetPort <= 0 {
			return types.TunnelSpec{}, errors.New("tcp tunnel requires targetHost and targetPort")
		}
	case "http":
		if strings.TrimSpace(spec.TargetHost) == "" || spec.TargetPort <= 0 {
			return types.TunnelSpec{}, errors.New("http tunnel requires targetHost and targetPort")
		}
		spec.Domain = strings.TrimSpace(spec.Domain)
		spec.TLSMode = ""
		spec.ProbePath = normalizeProbePath(spec.ProbePath)
	case "https":
		if strings.TrimSpace(spec.TargetHost) == "" || spec.TargetPort <= 0 {
			return types.TunnelSpec{}, errors.New("https tunnel requires targetHost and targetPort")
		}
		spec.Domain = strings.TrimSpace(spec.Domain)
		if spec.Domain == "" {
			return types.TunnelSpec{}, errors.New("https tunnel requires domain")
		}
		spec.Domain = strings.ToLower(spec.Domain)
		spec.TLSMode = strings.TrimSpace(spec.TLSMode)
		if spec.TLSMode == "" {
			spec.TLSMode = "edge_terminate"
		}
		if spec.TLSMode != "edge_terminate" {
			return types.TunnelSpec{}, errors.New("unsupported https tlsMode")
		}
		spec.ProbePath = normalizeProbePath(spec.ProbePath)
	case "socks5":
		spec.TargetHost = "socks5"
		spec.TargetPort = 1080
	case "udp":
		return types.TunnelSpec{}, errors.New("udp tunnel is reserved and not enabled yet")
	default:
		return types.TunnelSpec{}, fmt.Errorf("unsupported tunnel type %s", spec.Type)
	}
	return spec, nil
}

func (s *Server) ensureTunnelNodeCapability(ctx context.Context, nodeID, tunnelType string) error {
	node, err := s.store.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	if node.Isolated {
		return errors.New("selected node is isolated and cannot accept new tunnels")
	}
	if tunnelType == "socks5" && !node.Capabilities.SOCKS5Connect {
		return errors.New("selected node does not support socks5 connect")
	}
	if (tunnelType == "http" || tunnelType == "https") && !node.Capabilities.HTTPRelay {
		return errors.New("selected node does not support http relay")
	}
	return nil
}

func (s *Server) ensureHTTPSDomainUnique(ctx context.Context, spec types.TunnelSpec, excludeID string) error {
	if spec.Type != "https" {
		return nil
	}
	domain := strings.ToLower(strings.TrimSpace(spec.Domain))
	if domain == "" {
		return errors.New("https tunnel requires domain")
	}
	items, err := s.store.ListTunnels(ctx, store.TunnelFilter{Type: "https"})
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == excludeID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Domain), domain) {
			return fmt.Errorf("%w: https domain %s is already used by tunnel %s", store.ErrConflict, domain, item.ID)
		}
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
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

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" && s.isAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Bootstrap-Secret")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) isAllowedOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	if _, ok := s.allowedOrigins[origin]; ok {
		return true
	}
	return false
}

func parseAllowedOrigins(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(item)
		if origin == "" {
			continue
		}
		out[origin] = struct{}{}
	}
	return out
}
