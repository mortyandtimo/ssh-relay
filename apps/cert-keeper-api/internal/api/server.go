package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/25743/cloud-relay-platform/apps/cert-keeper-api/internal/certbot"
	"github.com/25743/cloud-relay-platform/apps/cert-keeper-api/internal/nginx"
	"github.com/25743/cloud-relay-platform/apps/cert-keeper-api/internal/store"
)

const (
	accessCookieName  = "ck_access"
	refreshCookieName = "ck_refresh"
	sessionCookieName = "ck_session"
)

type contextKey string

const authUserKey contextKey = "auth-user"
const certSyncContextKey contextKey = "cert-sync"

type authClaims struct {
	UserID string
	Role   string
	Expiry time.Time
}

// Config holds the server configuration.
type Config struct {
	DBURL           string
	ACMEEmail       string
	PublicIP        string
	CertDir         string
	NginxConfDir    string
	Webroot         string
	NginxBin        string
	AccessSecret    string
	AdminWebDir     string
	AllowedOrigins  string
	CookiesSecure   bool
	BootstrapSecret string
	CertSyncSecret  string
	DomainBackends  string
	SkipDomains     string
}

// Server is the cert-keeper API server.
type Server struct {
	store           store.Store
	nginxManager    *nginx.Manager
	acmeEmail       string
	publicIP        string
	webroot         string
	mux             *http.ServeMux
	accessSecret    string
	adminWebDir     string
	accessTTL       time.Duration
	refreshTTL      time.Duration
	bootstrapSecret string
	certSyncSecret  string
	allowedOrigins  map[string]struct{}
	cookiesSecure   bool
	renewCheckEvery time.Duration
	renewWithin     time.Duration
}

// NewServer creates a new cert-keeper API server.
func NewServer(cfg Config) (*Server, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	backend, err := store.NewPostgresStore(ctx, cfg.DBURL)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	s := &Server{
		store:           backend,
		acmeEmail:       strings.TrimSpace(cfg.ACMEEmail),
		publicIP:        cfg.PublicIP,
		webroot:         cfg.Webroot,
		mux:             http.NewServeMux(),
		accessSecret:    envOr(cfg.AccessSecret, "ck-access-secret-dev"),
		adminWebDir:     envOr(cfg.AdminWebDir, "/opt/cert-keeper/admin-web"),
		accessTTL:       15 * time.Minute,
		refreshTTL:      7 * 24 * time.Hour,
		bootstrapSecret: strings.TrimSpace(cfg.BootstrapSecret),
		certSyncSecret:  strings.TrimSpace(cfg.CertSyncSecret),
		allowedOrigins:  parseOrigins(cfg.AllowedOrigins),
		cookiesSecure:   cfg.CookiesSecure,
		renewCheckEvery: 6 * time.Hour,
		renewWithin:     30 * 24 * time.Hour,
	}

	os.MkdirAll(cfg.CertDir, 0755)
	os.MkdirAll(cfg.NginxConfDir, 0755)
	s.nginxManager = nginx.NewManager(cfg.NginxConfDir, cfg.CertDir, cfg.NginxBin, backend, s.publicIP)
	if cfg.DomainBackends != "" {
		s.nginxManager.SetDomainBackends(cfg.DomainBackends)
	}
	if cfg.SkipDomains != "" {
		s.nginxManager.SetSkipDomains(cfg.SkipDomains)
	}

	s.routes()
	if err := s.reconcileNginx(context.Background()); err != nil {
		log.Printf("cert-keeper startup nginx reconcile failed: %v", err)
	}
	s.startRenewWorker()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.withCORS(s.mux).ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)

	// Auth
	s.mux.HandleFunc("/api/auth/bootstrap-status", s.handleBootstrapStatus)
	s.mux.HandleFunc("/api/auth/bootstrap", s.handleBootstrap)
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/refresh", s.handleRefresh)
	s.mux.HandleFunc("/api/auth/logout", s.handleLogout)
	s.mux.Handle("/api/auth/me", s.requireAuth(http.HandlerFunc(s.handleAuthMe)))

	// Certificates
	s.mux.Handle("/api/certificates", s.requireCertificateAccess(http.HandlerFunc(s.handleCertificates)))
	s.mux.Handle("/api/certificates/auto-issue", s.requireAuth(http.HandlerFunc(s.handleAutoIssue)))
	s.mux.Handle("/api/certificates/dns-check", s.requireAuth(http.HandlerFunc(s.handleDNSCheck)))
	s.mux.Handle("/api/certificates/", s.requireCertificateAccess(http.HandlerFunc(s.handleCertificateByID)))
}

func (s *Server) startRenewWorker() {
	if s.acmeEmail == "" || s.renewCheckEvery <= 0 || s.renewWithin <= 0 {
		return
	}
	go func() {
		s.runRenewPass(context.Background())
		ticker := time.NewTicker(s.renewCheckEvery)
		defer ticker.Stop()
		for range ticker.C {
			s.runRenewPass(context.Background())
		}
	}()
}

func (s *Server) runRenewPass(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	certs, err := s.store.ListAllCertificates(ctx)
	if err != nil {
		log.Printf("cert renew scan failed: %v", err)
		return
	}
	for _, cert := range certs {
		if !s.shouldRenewCertificate(cert) {
			continue
		}
		renewCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		if err := s.renewCertificate(renewCtx, cert); err != nil {
			log.Printf("cert renew failed for %s: %v", cert.Domain, err)
		}
		cancel()
	}
}

func (s *Server) shouldRenewCertificate(cert store.Certificate) bool {
	if !cert.AutoRenew {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(cert.Issuer), "letsencrypt") {
		return false
	}
	if strings.TrimSpace(cert.Domain) == "" {
		return false
	}
	if cert.ExpiresAt == nil {
		return true
	}
	return time.Until(cert.ExpiresAt.UTC()) <= s.renewWithin
}

func (s *Server) renewCertificate(ctx context.Context, cert store.Certificate) error {
	result, err := certbot.Renew(ctx, cert.Domain, s.acmeEmail, s.webroot)
	now := time.Now().UTC()
	storeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err != nil {
		cert.RenewError = err.Error()
		_, updateErr := s.store.UpdateCertificate(storeCtx, cert)
		if updateErr != nil {
			return fmt.Errorf("record renew error: %w (original: %v)", updateErr, err)
		}
		return err
	}

	previous := cert
	cert.CertPEM = result.CertPEM
	cert.KeyPEM = result.KeyPEM
	cert.Issuer = "letsencrypt"
	cert.LastRenewedAt = &now
	cert.RenewError = ""
	if !result.ExpiresAt.IsZero() {
		cert.ExpiresAt = &result.ExpiresAt
	}
	if cert.DNSVerifiedAt == nil {
		cert.DNSVerifiedAt = &now
	}
	updated, err := s.store.UpdateCertificate(storeCtx, cert)
	if err != nil {
		return err
	}
	log.Printf("certificate renewed for %s (expires %s)", cert.Domain, result.ExpiresAt.Format("2006-01-02"))
	if err := s.applyNginxConfig(context.Background(), []store.Certificate{updated}); err != nil {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer rollbackCancel()
		if _, rollbackErr := s.store.UpdateCertificate(rollbackCtx, previous); rollbackErr != nil {
			return fmt.Errorf("reconcile nginx after renew: %w (rollback failed: %v)", err, rollbackErr)
		}
		if reconcileErr := s.applyNginxConfig(context.Background(), []store.Certificate{previous}); reconcileErr != nil {
			return fmt.Errorf("reconcile nginx after renew: %w (rollback reconcile failed: %v)", err, reconcileErr)
		}
		return fmt.Errorf("reconcile nginx after renew: %w", err)
	}
	return nil
}

func (s *Server) applyNginxConfig(ctx context.Context, changed []store.Certificate) error {
	if s.nginxManager == nil {
		return nil
	}
	applyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.nginxManager.RegenerateConfig(applyCtx); err != nil {
		return err
	}
	verifyCtx, verifyCancel := context.WithTimeout(ctx, 20*time.Second)
	defer verifyCancel()
	if err := s.nginxManager.VerifyCertificates(verifyCtx, changed); err != nil {
		return err
	}
	return nil
}

func (s *Server) reconcileNginx(ctx context.Context) error {
	if s.nginxManager == nil {
		return nil
	}
	applyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.nginxManager.RegenerateConfig(applyCtx); err != nil {
		return err
	}
	verifyCtx, verifyCancel := context.WithTimeout(ctx, 20*time.Second)
	defer verifyCancel()
	if err := s.nginxManager.VerifyCertificates(verifyCtx, nil); err != nil {
		return err
	}
	return nil
}

// ─── Health ───

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "cert-keeper-api",
	})
}

// ─── Auth handlers ───

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
	writeJSON(w, http.StatusOK, map[string]any{"required": required})
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
	var req struct {
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		Password    string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	user, err := s.store.BootstrapAdmin(r.Context(), req.Email, req.DisplayName, req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.issueSession(w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) authorizeBootstrap(r *http.Request) error {
	if s.bootstrapSecret == "" {
		return errors.New("bootstrap disabled: set CERT_KEEPER_BOOTSTRAP_SECRET")
	}
	secret := strings.TrimSpace(r.Header.Get("X-Bootstrap-Secret"))
	if secret == "" {
		secret = strings.TrimSpace(r.URL.Query().Get("bootstrapSecret"))
	}
	if secret != s.bootstrapSecret {
		return errors.New("invalid bootstrap secret")
	}
	return nil
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	user, err := s.store.AuthenticateUser(r.Context(), req.Email, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err := s.issueSession(w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	session, refreshToken, err := s.readRefreshSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if time.Now().UTC().After(session.RefreshExpiresAt) {
		_ = s.store.DeleteWebSession(r.Context(), session.SessionID)
		s.clearCookies(w)
		writeError(w, http.StatusUnauthorized, "refresh token expired")
		return
	}
	if hashToken(refreshToken) != session.RefreshTokenHash {
		_ = s.store.DeleteWebSession(r.Context(), session.SessionID)
		s.clearCookies(w)
		writeError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	user, err := s.store.GetUser(r.Context(), session.UserID)
	if err != nil {
		s.clearCookies(w)
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	if err := s.rotateSession(w, r, session, user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.store.DeleteWebSession(r.Context(), c.Value)
	}
	s.clearCookies(w)
	writeJSON(w, http.StatusOK, map[string]any{"status": "logged_out"})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, user)
}
