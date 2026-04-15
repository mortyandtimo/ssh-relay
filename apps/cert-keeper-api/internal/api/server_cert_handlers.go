package api

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/25743/cloud-relay-platform/apps/cert-keeper-api/internal/certbot"
	"github.com/25743/cloud-relay-platform/apps/cert-keeper-api/internal/dns"
	"github.com/25743/cloud-relay-platform/apps/cert-keeper-api/internal/store"
)

// ─── Certificate handlers ───

func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListCertificates(r.Context(), user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if items == nil {
			items = []store.Certificate{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var cert store.Certificate
		if err := json.NewDecoder(r.Body).Decode(&cert); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		cert.UserID = user.ID
		cert.Domain = strings.ToLower(strings.TrimSpace(cert.Domain))
		if cert.Domain == "" || cert.CertPEM == "" || cert.KeyPEM == "" {
			writeError(w, http.StatusBadRequest, "domain, certPem, and keyPem are required")
			return
		}
		if cert.ExpiresAt == nil {
			if exp := parseCertExpiry(cert.CertPEM); exp != nil {
				cert.ExpiresAt = exp
			}
		}
		result, err := s.store.CreateCertificate(r.Context(), cert)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "already has a certificate") {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		if err := s.applyNginxConfig(r.Context(), []store.Certificate{result}); err != nil {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if rollbackErr := s.store.DeleteCertificate(rollbackCtx, result.ID); rollbackErr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("activate certificate failed: %v (rollback failed: %v)", err, rollbackErr))
				return
			}
			if reconcileErr := s.reconcileNginx(context.Background()); reconcileErr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("activate certificate failed: %v (rollback reconcile failed: %v)", err, reconcileErr))
				return
			}
			writeError(w, http.StatusBadGateway, "activate certificate failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, result)
	default:
		writeMethodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (s *Server) handleCertificateByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/certificates/"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "certificate id is required")
		return
	}
	user, _ := userFromContext(r.Context())

	switch r.Method {
	case http.MethodGet:
		cert, err := s.store.GetCertificate(r.Context(), id)
		if err != nil {
			status := http.StatusInternalServerError
			if isNotFound(err) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		if cert.UserID != user.ID {
			writeError(w, http.StatusForbidden, "not your certificate")
			return
		}
		writeJSON(w, http.StatusOK, cert)

	case http.MethodPut:
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid payload")
			return
		}
		existing, err := s.store.GetCertificate(r.Context(), id)
		if err != nil {
			status := http.StatusInternalServerError
			if isNotFound(err) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		if existing.UserID != user.ID {
			writeError(w, http.StatusForbidden, "not your certificate")
			return
		}
		cert, err := mergeCertificateUpdate(existing, payload)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cert.ID = id
		cert.UserID = user.ID
		cert.Domain = existing.Domain
		if cert.CertPEM == "" || cert.KeyPEM == "" {
			writeError(w, http.StatusBadRequest, "certPem and keyPem are required")
			return
		}
		if cert.ExpiresAt == nil && cert.CertPEM != "" {
			if exp := parseCertExpiry(cert.CertPEM); exp != nil {
				cert.ExpiresAt = exp
			}
		}
		result, err := s.store.UpdateCertificate(r.Context(), cert)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.applyNginxConfig(r.Context(), []store.Certificate{result}); err != nil {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			existing.UpdatedAt = time.Now().UTC()
			if _, rollbackErr := s.store.UpdateCertificate(rollbackCtx, existing); rollbackErr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("activate certificate update failed: %v (rollback failed: %v)", err, rollbackErr))
				return
			}
			if reconcileErr := s.applyNginxConfig(context.Background(), []store.Certificate{existing}); reconcileErr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("activate certificate update failed: %v (rollback reconcile failed: %v)", err, reconcileErr))
				return
			}
			writeError(w, http.StatusBadGateway, "activate certificate update failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)

	case http.MethodDelete:
		cert, err := s.store.GetCertificate(r.Context(), id)
		if err != nil {
			status := http.StatusInternalServerError
			if isNotFound(err) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		if cert.UserID != user.ID {
			writeError(w, http.StatusForbidden, "not your certificate")
			return
		}
		if err := s.store.DeleteCertificate(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.reconcileNginx(r.Context()); err != nil {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			restored := cert
			restored.ID = ""
			if _, rollbackErr := s.store.CreateCertificate(rollbackCtx, restored); rollbackErr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("deactivate certificate failed: %v (rollback failed: %v)", err, rollbackErr))
				return
			}
			if reconcileErr := s.reconcileNginx(context.Background()); reconcileErr != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("deactivate certificate failed: %v (rollback reconcile failed: %v)", err, reconcileErr))
				return
			}
			writeError(w, http.StatusBadGateway, "deactivate certificate failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})

	default:
		writeMethodNotAllowed(w, "GET, PUT, DELETE")
	}
}

func (s *Server) handleAutoIssue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	user, _ := userFromContext(r.Context())

	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" {
		writeError(w, http.StatusBadRequest, "domain is required")
		return
	}

	// Check DNS first
	check := dns.Check(domain, s.publicIP)
	if !check.Resolved || !check.Matches {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("DNS not resolved to this server (resolved=%v, matches=%v, ips=%v)", check.Resolved, check.Matches, check.IPs))
		return
	}

	result, err := certbot.Issue(r.Context(), domain, s.acmeEmail, s.webroot)
	if err != nil {
		writeError(w, http.StatusBadGateway, "certbot issue failed: "+err.Error())
		return
	}

	cert := store.Certificate{
		UserID:    user.ID,
		Domain:    domain,
		CertPEM:   result.CertPEM,
		KeyPEM:    result.KeyPEM,
		Issuer:    "letsencrypt",
		AutoRenew: true,
	}
	if !result.ExpiresAt.IsZero() {
		cert.ExpiresAt = &result.ExpiresAt
	}
	now := time.Now().UTC()
	cert.DNSVerifiedAt = &now

	created, err := s.store.CreateCertificate(r.Context(), cert)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.applyNginxConfig(r.Context(), []store.Certificate{created}); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if rollbackErr := s.store.DeleteCertificate(rollbackCtx, created.ID); rollbackErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("activate auto-issued certificate failed: %v (rollback failed: %v)", err, rollbackErr))
			return
		}
		if reconcileErr := s.reconcileNginx(context.Background()); reconcileErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("activate auto-issued certificate failed: %v (rollback reconcile failed: %v)", err, reconcileErr))
			return
		}
		writeError(w, http.StatusBadGateway, "activate auto-issued certificate failed: "+err.Error())
		return
	}
	log.Printf("auto-issued certificate for %s (expires %s)", domain, result.ExpiresAt.Format("2006-01-02"))
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleDNSCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if domain == "" {
		writeError(w, http.StatusBadRequest, "domain query param is required")
		return
	}
	result := dns.Check(domain, s.publicIP)
	writeJSON(w, http.StatusOK, result)
}

func mergeCertificateUpdate(existing store.Certificate, payload map[string]json.RawMessage) (store.Certificate, error) {
	updated := existing
	for key, raw := range payload {
		switch key {
		case "id", "userId", "createdAt", "updatedAt":
			continue
		case "domain":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return store.Certificate{}, fmt.Errorf("invalid domain")
			}
			value = strings.ToLower(strings.TrimSpace(value))
			if value != "" && value != existing.Domain {
				return store.Certificate{}, fmt.Errorf("domain cannot be changed")
			}
		case "certPem":
			if err := json.Unmarshal(raw, &updated.CertPEM); err != nil {
				return store.Certificate{}, fmt.Errorf("invalid certPem")
			}
		case "keyPem":
			if err := json.Unmarshal(raw, &updated.KeyPEM); err != nil {
				return store.Certificate{}, fmt.Errorf("invalid keyPem")
			}
		case "issuer":
			if err := json.Unmarshal(raw, &updated.Issuer); err != nil {
				return store.Certificate{}, fmt.Errorf("invalid issuer")
			}
		case "autoRenew":
			if err := json.Unmarshal(raw, &updated.AutoRenew); err != nil {
				return store.Certificate{}, fmt.Errorf("invalid autoRenew")
			}
		case "expiresAt":
			value, err := decodeOptionalTime(raw)
			if err != nil {
				return store.Certificate{}, fmt.Errorf("invalid expiresAt")
			}
			updated.ExpiresAt = value
		case "dnsVerifiedAt":
			value, err := decodeOptionalTime(raw)
			if err != nil {
				return store.Certificate{}, fmt.Errorf("invalid dnsVerifiedAt")
			}
			updated.DNSVerifiedAt = value
		case "lastRenewedAt":
			value, err := decodeOptionalTime(raw)
			if err != nil {
				return store.Certificate{}, fmt.Errorf("invalid lastRenewedAt")
			}
			updated.LastRenewedAt = value
		case "renewError":
			if err := json.Unmarshal(raw, &updated.RenewError); err != nil {
				return store.Certificate{}, fmt.Errorf("invalid renewError")
			}
		}
	}
	return updated, nil
}

func decodeOptionalTime(raw json.RawMessage) (*time.Time, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" {
		return nil, nil
	}
	var value time.Time
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	utc := value.UTC()
	return &utc, nil
}

func parseCertExpiry(certPEM string) *time.Time {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || cert.NotAfter.IsZero() {
		return nil
	}
	t := cert.NotAfter.UTC()
	return &t
}

func isNotFound(err error) bool {
	return err != nil && (errors.Is(err, context.Canceled) == false && strings.Contains(err.Error(), "no rows"))
}
