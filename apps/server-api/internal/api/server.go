package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	mathrand "math/rand"
	"mime/multipart"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/25743/cloud-relay-platform/apps/server-api/internal/nginx"
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

type passwordChangeCodeRecord struct {
	CodeHash  string
	Email     string
	ExpiresAt time.Time
	SentAt    time.Time
}

type Server struct {
	version                string
	startedAt              time.Time
	store                  store.Store
	controlExecutor        controlExecutor
	relayTCPRuntimeURL     string
	nginxManager           *nginx.Manager
	acmeEmail              string
	httpClient             *http.Client
	mux                    *http.ServeMux
	accessSecret           string
	adminWebDir            string
	publicEntryHost        string
	accessTokenTTL         time.Duration
	refreshTokenTTL        time.Duration
	adminBootstrapSecret   string
	releaseUploadDir       string
	releasePublicBaseURL   string
	allowedOrigins         map[string]struct{}
	authCookiesSecure      bool
	controlExecuteMu       sync.Mutex
	controlExecuteActive   map[string]struct{}
	controlExecuteDone     map[string]controlExecutionRecord
	passwordChangeMu       sync.Mutex
	passwordChangeCodes    map[string]passwordChangeCodeRecord
	passwordChangeCodeTTL  time.Duration
	sendPasswordChangeCode func(to, code string) error
}

func NewServer(version string, backend store.Store, relayTCPRuntimeURL string) *Server {
	s := &Server{
		version:                version,
		startedAt:              time.Now().UTC(),
		store:                  backend,
		relayTCPRuntimeURL:     relayTCPRuntimeURL,
		httpClient:             &http.Client{Timeout: 5 * time.Second},
		mux:                    http.NewServeMux(),
		accessSecret:           envOrDefault("SERVER_API_ACCESS_SECRET", "cloud-relay-access-secret-dev"),
		adminWebDir:            envOrDefault("SERVER_API_ADMIN_WEB_DIR", "/opt/cloud-relay-platform/admin-web"),
		publicEntryHost:        envOrDefault("SERVER_API_PUBLIC_ENTRY_HOST", "publisher.manage.020309.top"),
		accessTokenTTL:         15 * time.Minute,
		refreshTokenTTL:        7 * 24 * time.Hour,
		adminBootstrapSecret:   strings.TrimSpace(os.Getenv("SERVER_API_ADMIN_BOOTSTRAP_SECRET")),
		releaseUploadDir:       strings.TrimSpace(os.Getenv("SERVER_API_RELEASE_UPLOAD_DIR")),
		releasePublicBaseURL:   strings.TrimRight(strings.TrimSpace(os.Getenv("SERVER_API_RELEASE_PUBLIC_BASE_URL")), "/"),
		allowedOrigins:         parseAllowedOrigins(os.Getenv("SERVER_API_ALLOWED_ORIGINS")),
		authCookiesSecure:      parseBoolEnv(os.Getenv("SERVER_API_AUTH_COOKIES_SECURE")),
		controlExecuteActive:   map[string]struct{}{},
		controlExecuteDone:     map[string]controlExecutionRecord{},
		passwordChangeCodes:    map[string]passwordChangeCodeRecord{},
		passwordChangeCodeTTL:  10 * time.Minute,
		sendPasswordChangeCode: newSMTPPasswordChangeCodeSenderFromEnv(),
	}
	s.controlExecutor = newStateMutationControlExecutor(backend, newConfiguredControlExecutor())
	s.routes()

	// Initialize nginx manager if config dir is set
	nginxConfigDir := envOrDefault("SERVER_API_NGINX_CONFIG_DIR", "")
	nginxCertDir := envOrDefault("SERVER_API_NGINX_CERT_DIR", "")
	nginxBin := envOrDefault("SERVER_API_NGINX_BIN", "")
	if nginxConfigDir != "" && nginxCertDir != "" {
		s.nginxManager = nginx.NewManager(nginxConfigDir, nginxCertDir, nginxBin, backend)
		os.MkdirAll(nginxConfigDir, 0755)
		os.MkdirAll(nginxCertDir, 0700)
		s.acmeEmail = envOrDefault("SERVER_API_ACME_EMAIL", "")
	}

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
	s.mux.HandleFunc("/internal/routes/udp", s.handleUDPRoutes)
	s.mux.HandleFunc("/internal/routes/http", s.handleHTTPRoutes)
	s.mux.HandleFunc("/internal/routes/https", s.handleHTTPSRoutes)

	s.mux.HandleFunc("/api/certificates", s.handleCertificates)

	s.mux.HandleFunc("/api/auth/bootstrap-status", s.handleBootstrapStatus)
	s.mux.HandleFunc("/api/auth/bootstrap", s.handleBootstrap)
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/register", s.handleRegisterUser)
	s.mux.HandleFunc("/api/auth/refresh", s.handleRefresh)
	s.mux.HandleFunc("/api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("/api/public/service-transport", s.handlePublicServiceTransport)
	s.mux.Handle("/api/auth/me", s.requireRole(types.UserRoleUser, http.HandlerFunc(s.handleAuthMe)))
	s.mux.Handle("/api/auth/password-change/send-code", s.requireRole(types.UserRoleUser, http.HandlerFunc(s.handleSendPasswordChangeCode)))
	s.mux.Handle("/api/auth/password-change/confirm", s.requireRole(types.UserRoleUser, http.HandlerFunc(s.handleConfirmPasswordChange)))
	s.mux.Handle("/api/user/services", s.requireRole(types.UserRoleUser, http.HandlerFunc(s.handleUserServices)))

	s.mux.Handle("/api/admin/auth-settings", s.requireRole(types.UserRoleAdmin, http.HandlerFunc(s.handleAdminAuthSettings)))
	s.mux.Handle("/api/users", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleUsers)))
	s.mux.Handle("/api/users/", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleUserByID)))
	s.mux.Handle("/api/nodes", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleNodes)))
	s.mux.Handle("/api/node-options", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleNodeOptions)))
	s.mux.Handle("/api/nodes/", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleNodeByID)))
	s.mux.Handle("/api/tunnels", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleTunnels)))
	s.mux.Handle("/api/tunnel-port-suggestion", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleTunnelPortSuggestion)))
	s.mux.Handle("/api/tunnels/", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleTunnelByID)))
	s.mux.Handle("/api/control-panels/node/", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleNodeControlPanel)))
	s.mux.Handle("/api/control-panels/tunnel/", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleTunnelControlPanel)))
	s.mux.Handle("/api/control-actions/node/", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleNodeControlActionOptions)))
	s.mux.Handle("/api/control-actions/tunnel/", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleTunnelControlActionOptions)))
	s.mux.Handle("/api/control-actions", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleControlActions)))
	s.mux.Handle("/api/server/metrics", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleServerMetrics)))
	s.mux.Handle("/api/admin/release-artifacts/upload", s.requireRole(types.UserRoleAdmin, http.HandlerFunc(s.handleReleaseArtifactUpload)))
	s.mux.Handle("/api/admin/release-artifacts", s.requireRole(types.UserRoleAdmin, http.HandlerFunc(s.handleReleaseArtifactList)))
	s.mux.HandleFunc("/api/releases/latest", s.handleLatestReleaseArtifacts)
	s.mux.Handle("/downloads/releases/", s.releaseArtifactDownloadHandler())
	s.mux.Handle("/api/managed-domains/https", s.requireRole(types.UserRoleManager, http.HandlerFunc(s.handleManagedHTTPSDomains)))
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

	// Probabilistically purge stale nodes (~1% of heartbeats)
	if mathrand.Intn(100) == 0 {
		if purged, err := s.store.PurgeStaleNodes(r.Context(), 7*24*time.Hour); err == nil && purged > 0 {
			log.Printf("purged %d stale nodes (unseen > 7d)", purged)
		}
	}
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

func (s *Server) handleReleaseArtifactUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if strings.TrimSpace(s.releaseUploadDir) == "" {
		writeError(w, http.StatusServiceUnavailable, "release upload is disabled until SERVER_API_RELEASE_UPLOAD_DIR is configured")
		return
	}
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload")
		return
	}

	product := strings.TrimSpace(r.FormValue("product"))
	channel := strings.TrimSpace(r.FormValue("channel"))
	version := strings.TrimSpace(r.FormValue("version"))
	clientSHA256 := strings.TrimSpace(r.FormValue("sha256"))
	if product == "" || channel == "" || version == "" {
		writeError(w, http.StatusBadRequest, "product, channel, and version are required")
		return
	}
	if !allowedReleaseProduct(product) {
		writeError(w, http.StatusBadRequest, "unsupported product")
		return
	}
	if !allowedReleaseChannel(channel) {
		writeError(w, http.StatusBadRequest, "unsupported channel")
		return
	}
	if strings.Contains(version, "..") || strings.ContainsAny(version, `/\\`) {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	if header.Size <= 0 {
		writeError(w, http.StatusBadRequest, "file is empty")
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !releaseChannelMatchesExt(channel, ext) {
		writeError(w, http.StatusBadRequest, "file extension does not match channel")
		return
	}

	// Read file into memory to compute sha256 before writing to disk
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read uploaded file")
		return
	}
	serverHash := sha256.Sum256(fileBytes)
	serverSHA256 := hex.EncodeToString(serverHash[:])

	// Verify client-provided sha256 if present
	if clientSHA256 != "" && !strings.EqualFold(clientSHA256, serverSHA256) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("sha256 mismatch: client=%s server=%s", clientSHA256, serverSHA256))
		return
	}

	fileName := releaseArtifactFileName(product, channel, version, ext)
	targetDir := filepath.Join(s.releaseUploadDir, product, version)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create release directory")
		return
	}
	targetPath := filepath.Join(targetDir, fileName)
	if err := os.WriteFile(targetPath, fileBytes, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store uploaded file")
		return
	}
	// Write sha256 sidecar file
	_ = os.WriteFile(targetPath+".sha256", []byte(serverSHA256), 0o644)

	downloadPath := "/downloads/releases/" + product + "/" + version + "/" + fileName
	resp := map[string]any{
		"product":      product,
		"channel":      channel,
		"version":      version,
		"fileName":     fileName,
		"size":         len(fileBytes),
		"sha256":       serverSHA256,
		"storedPath":   targetPath,
		"downloadPath": downloadPath,
	}
	if s.releasePublicBaseURL != "" {
		resp["downloadUrl"] = s.releasePublicBaseURL + downloadPath
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleReleaseArtifactList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.listReleaseArtifacts()})
}

func (s *Server) handleLatestReleaseArtifacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": latestReleaseArtifacts(s.listReleaseArtifacts())})
}

type releaseArtifactEntry struct {
	Product      string    `json:"product"`
	Version      string    `json:"version"`
	Channel      string    `json:"channel"`
	FileName     string    `json:"fileName"`
	Size         int64     `json:"size"`
	SHA256       string    `json:"sha256"`
	DownloadPath string    `json:"downloadPath"`
	DownloadURL  string    `json:"downloadUrl"`
	UploadedAt   string    `json:"uploadedAt"`
	uploadedAt   time.Time `json:"-"`
}

func (s *Server) listReleaseArtifacts() []releaseArtifactEntry {
	if strings.TrimSpace(s.releaseUploadDir) == "" {
		return []releaseArtifactEntry{}
	}
	items := make([]releaseArtifactEntry, 0)
	products := []string{"publisher", "cert-keeper", "user"}
	for _, product := range products {
		productDir := filepath.Join(s.releaseUploadDir, product)
		versions, err := os.ReadDir(productDir)
		if err != nil {
			continue
		}
		for _, vEntry := range versions {
			if !vEntry.IsDir() {
				continue
			}
			version := vEntry.Name()
			files, err := os.ReadDir(filepath.Join(productDir, version))
			if err != nil {
				continue
			}
			for _, fEntry := range files {
				if fEntry.IsDir() || strings.HasSuffix(fEntry.Name(), ".sha256") {
					continue
				}
				info, err := fEntry.Info()
				if err != nil {
					continue
				}
				filePath := filepath.Join(productDir, version, fEntry.Name())
				sha256Hex := ""
				if data, err := os.ReadFile(filePath + ".sha256"); err == nil {
					sha256Hex = strings.TrimSpace(string(data))
				}
				ext := strings.ToLower(filepath.Ext(fEntry.Name()))
				channel := "portable"
				if ext == ".exe" {
					channel = "setup"
				}
				downloadPath := "/downloads/releases/" + product + "/" + version + "/" + fEntry.Name()
				downloadURL := downloadPath
				if s.releasePublicBaseURL != "" {
					downloadURL = s.releasePublicBaseURL + downloadPath
				}
				items = append(items, releaseArtifactEntry{
					Product:      product,
					Version:      version,
					Channel:      channel,
					FileName:     fEntry.Name(),
					Size:         info.Size(),
					SHA256:       sha256Hex,
					DownloadPath: downloadPath,
					DownloadURL:  downloadURL,
					UploadedAt:   info.ModTime().UTC().Format(time.RFC3339),
					uploadedAt:   info.ModTime().UTC(),
				})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].uploadedAt.Equal(items[j].uploadedAt) {
			return items[i].uploadedAt.After(items[j].uploadedAt)
		}
		if items[i].Product != items[j].Product {
			return items[i].Product < items[j].Product
		}
		if items[i].Channel != items[j].Channel {
			return items[i].Channel < items[j].Channel
		}
		if items[i].Version != items[j].Version {
			return items[i].Version > items[j].Version
		}
		return items[i].FileName < items[j].FileName
	})
	return items
}

func latestReleaseArtifacts(items []releaseArtifactEntry) []releaseArtifactEntry {
	if len(items) == 0 {
		return []releaseArtifactEntry{}
	}
	out := make([]releaseArtifactEntry, 0, 4)
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		key := item.Product + ":" + item.Channel
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Product != out[j].Product {
			return out[i].Product < out[j].Product
		}
		return out[i].Channel < out[j].Channel
	})
	return out
}

func (s *Server) releaseArtifactDownloadHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(s.releaseUploadDir) == "" {
			http.NotFound(w, r)
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, "/downloads/releases/")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" || strings.Contains(rel, "..") {
			http.NotFound(w, r)
			return
		}
		target := filepath.Join(s.releaseUploadDir, filepath.FromSlash(rel))
		info, err := os.Stat(target)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, target)
	})
}

func allowedReleaseProduct(product string) bool {
	switch product {
	case "publisher", "cert-keeper", "user":
		return true
	default:
		return false
	}
}

func allowedReleaseChannel(channel string) bool {
	switch channel {
	case "setup", "portable":
		return true
	default:
		return false
	}
}

func releaseChannelMatchesExt(channel, ext string) bool {
	switch channel {
	case "setup":
		return ext == ".exe"
	case "portable":
		return ext == ".zip"
	default:
		return false
	}
}

func releaseArtifactFileName(product, channel, version, ext string) string {
	base := version
	if product == "publisher" {
		if channel == "setup" {
			base = "CloudRelayPublisherSetup-" + version
		} else {
			base = "CloudRelayPublisherPortable-" + version
		}
	} else if product == "cert-keeper" {
		if channel == "setup" {
			base = "CertKeeperSetup-" + version
		} else {
			base = "CertKeeperPortable-" + version
		}
	} else {
		if channel == "setup" {
			base = "CloudRelayUserSetup-" + version
		} else {
			base = "CloudRelayUserPortable-" + version
		}
	}
	return base + ext
}

func writeUploadedFile(targetPath string, src multipart.File, mode os.FileMode) error {
	out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, src)
	return err
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
	settings, err := s.store.GetAuthSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types.AuthBootstrapStatusResponse{
		Required:                  required,
		PublicRegistrationEnabled: settings.PublicRegistrationEnabled,
	})
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
		if errors.Is(err, store.ErrForbidden) {
			writeError(w, http.StatusForbidden, "user is disabled")
			return
		}
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

func (s *Server) handleRegisterUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	required, err := s.store.BootstrapStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if required {
		writeError(w, http.StatusConflict, "bootstrap required before public registration")
		return
	}
	settings, err := s.store.GetAuthSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !settings.PublicRegistrationEnabled {
		writeError(w, http.StatusForbidden, "public registration is disabled")
		return
	}
	var req types.AuthRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid register payload")
		return
	}
	if strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.DisplayName) == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email, displayName and password are required")
		return
	}
	user, err := s.store.CreateUser(r.Context(), store.CreateUserParams{
		Email:       req.Email,
		DisplayName: req.DisplayName,
		Password:    req.Password,
		Role:        types.UserRoleUser,
	})
	if err != nil {
		status := http.StatusInternalServerError
		message := err.Error()
		if errors.Is(err, store.ErrConflict) {
			status = http.StatusConflict
			message = "user already exists or registration payload is invalid"
		}
		writeError(w, status, message)
		return
	}
	if err := s.issueAuthSession(w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeAudit(r, "register_user", "user", user.ID, map[string]string{"email": user.Email, "role": string(user.Role)})
	writeJSON(w, http.StatusCreated, types.AuthUserResponse{User: user})
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
	if user.Disabled {
		_ = s.store.DeleteWebSession(r.Context(), session.SessionID)
		s.clearAuthCookies(w)
		writeError(w, http.StatusForbidden, "user is disabled")
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

func (s *Server) handleSendPasswordChangeCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	user, ok := authUserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if user.Role == types.UserRoleAdmin {
		writeError(w, http.StatusConflict, "super admin password must be changed manually outside self-service flow")
		return
	}
	if strings.TrimSpace(user.Email) == "" {
		writeError(w, http.StatusBadRequest, "current account does not have an email address")
		return
	}
	if s.sendPasswordChangeCode == nil {
		writeError(w, http.StatusServiceUnavailable, "password change email verification is not configured")
		return
	}

	now := time.Now().UTC()
	s.passwordChangeMu.Lock()
	current := s.passwordChangeCodes[user.ID]
	if !current.SentAt.IsZero() && now.Before(current.SentAt.Add(60*time.Second)) {
		retryAfter := int(current.SentAt.Add(60 * time.Second).Sub(now).Seconds())
		s.passwordChangeMu.Unlock()
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("verification code was already sent, retry after %d seconds", retryAfter))
		return
	}
	s.passwordChangeMu.Unlock()

	code, err := numericCode(6)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate verification code")
		return
	}
	if err := s.sendPasswordChangeCode(user.Email, code); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	s.passwordChangeMu.Lock()
	s.passwordChangeCodes[user.ID] = passwordChangeCodeRecord{
		CodeHash:  hashOpaqueToken(code),
		Email:     user.Email,
		ExpiresAt: now.Add(s.passwordChangeCodeTTL),
		SentAt:    now,
	}
	s.passwordChangeMu.Unlock()

	s.writeAudit(r, "send_password_change_code", "user", user.ID, map[string]string{"email": user.Email})
	writeJSON(w, http.StatusOK, map[string]any{"status": "sent", "expiresInSec": int(s.passwordChangeCodeTTL.Seconds()), "email": user.Email})
}

func (s *Server) handleConfirmPasswordChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	user, ok := authUserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if user.Role == types.UserRoleAdmin {
		writeError(w, http.StatusConflict, "super admin password must be changed manually outside self-service flow")
		return
	}
	var req types.ConfirmPasswordChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid password change payload")
		return
	}
	if strings.TrimSpace(req.Code) == "" || strings.TrimSpace(req.Password) == "" {
		writeError(w, http.StatusBadRequest, "code and password are required")
		return
	}
	if err := s.consumePasswordChangeCode(user.ID, user.Email, req.Code); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updatedUser, err := s.store.UpdateUser(r.Context(), store.UpdateUserParams{
		ID:       user.ID,
		Password: req.Password,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeAudit(r, "change_password", "user", user.ID, map[string]string{"email": updatedUser.Email, "verifiedBy": "email_code"})
	writeJSON(w, http.StatusOK, types.AuthUserResponse{User: updatedUser})
}

func (s *Server) handleAdminAuthSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.store.GetAuthSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, settings)
	case http.MethodPut:
		var req types.UpdateAuthSettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid auth settings payload")
			return
		}
		settings, err := s.store.UpdateAuthSettings(r.Context(), types.AuthSettings{
			PublicRegistrationEnabled: req.PublicRegistrationEnabled,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeAudit(r, "update_auth_settings", "auth_settings", "public_registration", map[string]string{
			"publicRegistrationEnabled": strconv.FormatBool(settings.PublicRegistrationEnabled),
		})
		writeJSON(w, http.StatusOK, settings)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
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
		actor, ok := authUserFromContext(r.Context())
		if !ok || actor.Role != types.UserRoleAdmin {
			writeError(w, http.StatusForbidden, "only super admin can create users")
			return
		}
		var req types.CreateUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid user payload")
			return
		}
		if normalizeManagedUserRole(req.Role) == types.UserRoleAdmin {
			writeError(w, http.StatusConflict, "super admin account is reserved and cannot be created here")
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
		s.writeAudit(r, "create_user", "user", user.ID, map[string]string{"email": user.Email, "role": string(user.Role), "disabled": strconv.FormatBool(user.Disabled)})
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
	actor, ok := authUserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var req types.UpdateUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid user payload")
			return
		}
		if actor.Role != types.UserRoleAdmin {
			if req.Role != "" || req.Password != "" || strings.TrimSpace(req.DisplayName) != "" || req.Disabled == nil {
				writeError(w, http.StatusForbidden, "ordinary admin can only ban or unban normal users")
				return
			}
			targetUser, err := s.store.GetUser(r.Context(), id)
			if err != nil {
				status := http.StatusInternalServerError
				if errors.Is(err, store.ErrNotFound) {
					status = http.StatusNotFound
				}
				writeError(w, status, err.Error())
				return
			}
			if targetUser.Role != types.UserRoleUser {
				writeError(w, http.StatusForbidden, "ordinary admin can only manage normal users")
				return
			}
		}
		if normalizeManagedUserRole(req.Role) == types.UserRoleAdmin && req.Role != "" {
			writeError(w, http.StatusConflict, "super admin account is reserved and cannot be assigned here")
			return
		}
		if err := s.ensureUserManagementBoundary(r.Context(), r, id, req.Role, req.Disabled, req.Password != "", false); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		user, err := s.store.UpdateUser(r.Context(), store.UpdateUserParams{ID: id, DisplayName: req.DisplayName, Password: req.Password, Role: req.Role, Disabled: req.Disabled})
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		if user.Disabled {
			_ = s.store.DeleteUserSessions(r.Context(), user.ID)
		}
		s.writeAudit(r, "update_user", "user", user.ID, map[string]string{"email": user.Email, "role": string(user.Role), "disabled": strconv.FormatBool(user.Disabled)})
		writeJSON(w, http.StatusOK, user)
	case http.MethodDelete:
		if actor.Role != types.UserRoleAdmin {
			writeError(w, http.StatusForbidden, "only super admin can delete users")
			return
		}
		if err := s.ensureUserManagementBoundary(r.Context(), r, id, "", nil, false, true); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
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
		s.writeAudit(r, "delete_user", "user", id, map[string]string{"email": user.Email, "displayName": user.DisplayName, "role": string(user.Role), "disabled": strconv.FormatBool(user.Disabled)})
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
		options = append(options, types.NodeOption{NodeID: item.NodeID, NodeName: item.NodeName, Status: item.Status, SupportsTCP: item.Capabilities.TCPRelay, SupportsUDP: item.Capabilities.UDPRelay, SupportsHTTP: item.Capabilities.HTTPRelay, SupportsHTTPS: item.Capabilities.HTTPSRelay || item.Capabilities.HTTPRelay, SupportsSOCKS5: item.Capabilities.SOCKS5Connect, SupportsP2P: item.Capabilities.P2PAssist, Isolated: item.Isolated})
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

func portRangePlanForType(tunnelType string) types.PortRangePlan {
	switch strings.TrimSpace(tunnelType) {
	case "udp":
		return types.PortRangePlan{Type: "udp", Label: "UDP", RangeStart: 21000, RangeEnd: 21999, Description: "UDP 推荐端口段"}
	case "http", "https":
		return types.PortRangePlan{Type: tunnelType, Label: strings.ToUpper(tunnelType), RangeStart: 22000, RangeEnd: 22999, Description: "HTTP/HTTPS 共享推荐端口段"}
	case "socks5":
		return types.PortRangePlan{Type: "socks5", Label: "SOCKS5", RangeStart: 23000, RangeEnd: 23999, Description: "SOCKS5 推荐端口段"}
	default:
		return types.PortRangePlan{Type: "tcp", Label: "TCP", RangeStart: 20000, RangeEnd: 20999, Description: "TCP 推荐端口段"}
	}
}

func portRangePlanGroupForType(tunnelType string) string {
	switch strings.TrimSpace(tunnelType) {
	case "http", "https":
		return "http_family"
	case "udp":
		return "udp"
	case "socks5":
		return "socks5"
	default:
		return "tcp"
	}
}

func portConflictGroupForSuggestedType(tunnelType string) string {
	switch strings.TrimSpace(tunnelType) {
	case "https":
		return ""
	default:
		return store.TunnelPortBindingKey(strings.TrimSpace(tunnelType))
	}
}

func suggestedPortForType(ctx context.Context, backend store.Store, tunnelType string) (types.TunnelPortSuggestionResponse, error) {
	plan := portRangePlanForType(tunnelType)
	items, err := backend.ListTunnels(ctx, store.TunnelFilter{})
	if err != nil {
		return types.TunnelPortSuggestionResponse{}, err
	}
	rangeUsed := map[int]bool{}
	conflictUsed := map[int]bool{}
	planGroup := portRangePlanGroupForType(plan.Type)
	conflictGroup := portConflictGroupForSuggestedType(plan.Type)
	for _, item := range items {
		if item.PublicPort <= 0 {
			continue
		}
		if portRangePlanGroupForType(item.Type) != planGroup {
		} else {
			rangeUsed[item.PublicPort] = true
		}
		if conflictGroup == "" {
			continue
		}
		if store.TunnelPortBindingKey(item.Type) != conflictGroup {
			continue
		}
		conflictUsed[item.PublicPort] = true
	}
	for port := plan.RangeStart; port <= plan.RangeEnd; port++ {
		if !rangeUsed[port] && !conflictUsed[port] {
			return types.TunnelPortSuggestionResponse{Type: plan.Type, Suggested: port, Plan: plan, Compatible: true}, nil
		}
	}
	return types.TunnelPortSuggestionResponse{Type: plan.Type, Suggested: 0, Plan: plan, Compatible: true}, nil
}

func (s *Server) handleTunnelPortSuggestion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	tunnelType := strings.TrimSpace(r.URL.Query().Get("type"))
	result, err := suggestedPortForType(r.Context(), s.store, tunnelType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
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
		s.triggerNginxRegen()
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
		currentTunnel, err := s.store.GetTunnel(r.Context(), id)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		spec := req.TunnelSpec
		spec.ID = id
		spec.NodeID = req.NodeID
		if spec.Metadata == nil {
			spec.Metadata = make(map[string]string, len(currentTunnel.Metadata))
			for key, value := range currentTunnel.Metadata {
				spec.Metadata[key] = value
			}
		}
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
		s.triggerNginxRegen()
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
		s.triggerNginxRegen()
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
	entry, err := s.probeTunnelEntry(tunnel)
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

func (s *Server) probeTunnelEntry(tunnel types.TunnelSpec) (string, error) {
	probePath := normalizeProbePath(tunnel.ProbePath)
	switch tunnel.Type {
	case "http":
		publicHost := strings.TrimSpace(s.publicEntryHost)
		if publicHost == "" {
			return "", errors.New("http tunnel requires public entry host for probe")
		}
		return fmt.Sprintf("http://%s:%d%s", publicHost, tunnel.PublicPort, probePath), nil
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

func (s *Server) handleManagedHTTPSDomains(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	user, ok := authUserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	items, err := s.store.ListManagedHTTPSDomains(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []types.ManagedHTTPSDomain{}
	}
	writeJSON(w, http.StatusOK, types.ManagedHTTPSDomainListResponse{Items: items})
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
	visible := make([]types.TunnelSpec, 0, len(items))
	for _, item := range items {
		if item.Type == "udp" {
			continue
		}
		visible = append(visible, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": visible})
}

func (s *Server) handleUDPRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	items, err := s.store.ListTunnels(r.Context(), store.TunnelFilter{Type: "udp", Status: "active"})
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
		Action:          strings.TrimSpace(r.URL.Query().Get("action")),
		ActionPrefix:    strings.TrimSpace(r.URL.Query().Get("actionPrefix")),
		Outcome:         strings.TrimSpace(r.URL.Query().Get("outcome")),
		RejectionKind:   strings.TrimSpace(r.URL.Query().Get("rejectionKind")),
		ExecutionMode:   strings.TrimSpace(r.URL.Query().Get("executionMode")),
		PlaceholderOnly: strings.TrimSpace(r.URL.Query().Get("placeholderOnly")),
		ActorType:       strings.TrimSpace(r.URL.Query().Get("actorType")),
		ActorID:         strings.TrimSpace(r.URL.Query().Get("actorID")),
		ResourceType:    strings.TrimSpace(r.URL.Query().Get("resourceType")),
		ResourceID:      strings.TrimSpace(r.URL.Query().Get("resourceID")),
		Limit:           50,
		Offset:          0,
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
	if tunnel.Type == "udp" && !node.summary.Capabilities.UDPRelay {
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
	if strings.TrimSpace(tunnel.NodeID) == "" {
		return true
	}
	switch tunnel.Type {
	case "", "tcp", "http", "udp":
		return tunnel.PublicPort <= 0 || strings.TrimSpace(tunnel.TargetHost) == "" || tunnel.TargetPort <= 0
	case "https":
		return strings.TrimSpace(tunnel.TargetHost) == "" || tunnel.TargetPort <= 0 || strings.TrimSpace(tunnel.Domain) == "" || strings.TrimSpace(tunnel.TLSMode) != "edge_terminate"
	case "socks5":
		return tunnel.PublicPort <= 0 || strings.TrimSpace(tunnel.TargetHost) != "socks5" || tunnel.TargetPort != 1080
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
			status := http.StatusUnauthorized
			if errors.Is(err, store.ErrForbidden) {
				status = http.StatusForbidden
			}
			writeError(w, status, err.Error())
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
	if user.Disabled {
		return types.UserSummary{}, store.ErrForbidden
	}
	if user.Role != claims.Role {
		return types.UserSummary{}, store.ErrUnauthorized
	}
	return user, nil
}

func (s *Server) ensureUserManagementBoundary(ctx context.Context, r *http.Request, targetUserID string, requestedRole types.UserRole, requestedDisabled *bool, selfPasswordChange bool, deleting bool) error {
	actor, ok := authUserFromContext(r.Context())
	if ok && deleting && actor.ID == targetUserID {
		return errors.New("cannot delete the current account")
	}
	if ok && actor.ID == targetUserID {
		if selfPasswordChange {
			if actor.Role == types.UserRoleAdmin {
				return errors.New("super admin password must be changed manually outside self-service flow")
			}
			return errors.New("current account password must be changed through email verification")
		}
		if requestedDisabled != nil && *requestedDisabled {
			return errors.New("cannot disable the current account")
		}
		if requestedRole != "" && normalizeManagedUserRole(requestedRole) != types.UserRoleAdmin {
			return errors.New("cannot change the current account role")
		}
	}

	targetUser, err := s.store.GetUser(ctx, targetUserID)
	if err != nil {
		return err
	}

	nextRole := targetUser.Role
	if requestedRole != "" {
		nextRole = normalizeManagedUserRole(requestedRole)
	}
	nextDisabled := targetUser.Disabled
	if requestedDisabled != nil {
		nextDisabled = *requestedDisabled
	}

	if deleting {
		nextDisabled = true
	}
	if targetUser.Role == types.UserRoleAdmin && !targetUser.Disabled && (deleting || nextRole != types.UserRoleAdmin || nextDisabled) {
		users, err := s.store.ListUsers(ctx)
		if err != nil {
			return err
		}
		enabledAdminCount := 0
		for _, user := range users {
			if user.Role == types.UserRoleAdmin && !user.Disabled {
				enabledAdminCount++
			}
		}
		if enabledAdminCount <= 1 {
			return errors.New("cannot remove the last enabled admin")
		}
	}

	return nil
}

func normalizeManagedUserRole(role types.UserRole) types.UserRole {
	switch role {
	case types.UserRoleAdmin, types.UserRoleManager, types.UserRoleUser:
		return role
	default:
		return types.UserRoleUser
	}
}

func (s *Server) consumePasswordChangeCode(userID, email, code string) error {
	s.passwordChangeMu.Lock()
	defer s.passwordChangeMu.Unlock()

	record, ok := s.passwordChangeCodes[userID]
	if !ok {
		return errors.New("verification code has not been requested")
	}
	if !strings.EqualFold(strings.TrimSpace(record.Email), strings.TrimSpace(email)) {
		delete(s.passwordChangeCodes, userID)
		return errors.New("verification email no longer matches the current account")
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		delete(s.passwordChangeCodes, userID)
		return errors.New("verification code has expired")
	}
	if hashOpaqueToken(strings.TrimSpace(code)) != record.CodeHash {
		return errors.New("invalid verification code")
	}
	delete(s.passwordChangeCodes, userID)
	return nil
}

func numericCode(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("invalid code length")
	}
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	var builder strings.Builder
	builder.Grow(length)
	for _, value := range bytes {
		builder.WriteByte(byte('0' + (value % 10)))
	}
	return builder.String(), nil
}

func newSMTPPasswordChangeCodeSenderFromEnv() func(to, code string) error {
	host := strings.TrimSpace(os.Getenv("SERVER_API_SMTP_HOST"))
	port := strings.TrimSpace(os.Getenv("SERVER_API_SMTP_PORT"))
	username := strings.TrimSpace(os.Getenv("SERVER_API_SMTP_USERNAME"))
	password := strings.TrimSpace(os.Getenv("SERVER_API_SMTP_PASSWORD"))
	from := strings.TrimSpace(os.Getenv("SERVER_API_SMTP_FROM"))
	if port == "" {
		port = "587"
	}
	if from == "" {
		from = username
	}

	return func(to, code string) error {
		if host == "" || username == "" || password == "" || from == "" {
			return errors.New("password change email verification is not configured")
		}
		subject := "Cloud Relay password change code"
		body := fmt.Sprintf("Your Cloud Relay password change code is %s.\r\nThis code expires in 10 minutes.\r\n", code)
		return sendSMTPMail(host, port, username, password, from, to, subject, body)
	}
}

func sendSMTPMail(host, port, username, password, from, to, subject, body string) error {
	addr := net.JoinHostPort(host, port)
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial failed: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("smtp starttls failed: %w", err)
		}
	}
	if ok, _ := client.Extension("AUTH"); ok {
		if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			return fmt.Errorf("smtp auth failed: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL FROM failed: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO failed: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA failed: %w", err)
	}
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", from, to, subject, body)
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp body write failed: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp body close failed: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp quit failed: %w", err)
	}
	return nil
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
	if spec.PublicPort < 0 {
		return types.TunnelSpec{}, errors.New("publicPort must be zero or greater")
	}
	spec.TransportPolicy = strings.TrimSpace(spec.TransportPolicy)
	if spec.TransportPolicy == "" {
		spec.TransportPolicy = types.TunnelTransportRelayOnly
	}
	if spec.TransportPolicy != types.TunnelTransportRelayOnly && spec.TransportPolicy != types.TunnelTransportP2PPreferred {
		return types.TunnelSpec{}, fmt.Errorf("unsupported transportPolicy %s", spec.TransportPolicy)
	}
	switch spec.Type {
	case "tcp":
		if spec.PublicPort <= 0 {
			return types.TunnelSpec{}, errors.New("tcp tunnel requires publicPort")
		}
		if strings.TrimSpace(spec.TargetHost) == "" || spec.TargetPort <= 0 {
			return types.TunnelSpec{}, errors.New("tcp tunnel requires targetHost and targetPort")
		}
	case "http":
		if spec.PublicPort <= 0 {
			return types.TunnelSpec{}, errors.New("http tunnel requires publicPort")
		}
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
		if spec.PublicPort <= 0 {
			return types.TunnelSpec{}, errors.New("socks5 tunnel requires publicPort")
		}
		spec.TargetHost = "socks5"
		spec.TargetPort = 1080
	case "udp":
		if spec.PublicPort <= 0 {
			return types.TunnelSpec{}, errors.New("udp tunnel requires publicPort")
		}
		if strings.TrimSpace(spec.TargetHost) == "" || spec.TargetPort <= 0 {
			return types.TunnelSpec{}, errors.New("udp tunnel requires targetHost and targetPort")
		}
		spec.Domain = ""
		spec.TLSMode = ""
		spec.ProbePath = ""
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
	if tunnelType == "udp" && !node.Capabilities.UDPRelay {
		return errors.New("selected node does not support udp relay")
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

// ─── Certificate API ───

func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
	user, err := s.authenticateRequest(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListCertificates(r.Context(), user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if items == nil {
			items = []types.CertificateSpec{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var spec types.CertificateSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			writeError(w, http.StatusBadRequest, "invalid certificate payload")
			return
		}
		spec.Domain = strings.ToLower(strings.TrimSpace(spec.Domain))
		if spec.Domain == "" || spec.CertPEM == "" || spec.KeyPEM == "" {
			writeError(w, http.StatusBadRequest, "domain, certPem, and keyPem are required")
			return
		}
		// Parse expiration from the PEM certificate
		if spec.ExpiresAt.IsZero() {
			if block, _ := pem.Decode([]byte(spec.CertPEM)); block != nil {
				if cert, err := x509.ParseCertificate(block.Bytes); err == nil && !cert.NotAfter.IsZero() {
					spec.ExpiresAt = cert.NotAfter.UTC()
				}
			}
		}
		result, err := s.store.CreateCertificate(r.Context(), user.ID, spec)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.triggerNginxRegen()
		writeJSON(w, http.StatusCreated, result)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (s *Server) triggerNginxRegen() {
	if s.nginxManager == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.nginxManager.RegenerateConfig(ctx); err != nil {
			log.Printf("nginx regen failed: %v", err)
		}
	}()
}
