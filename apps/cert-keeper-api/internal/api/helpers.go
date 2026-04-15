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
	"strings"
	"time"

	"github.com/25743/cloud-relay-platform/apps/cert-keeper-api/internal/store"
)

// ─── Auth middleware ───

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.authenticate(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), authUserKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) authenticate(r *http.Request) (store.User, error) {
	claims, err := s.readAccessClaims(r)
	if err != nil {
		return store.User{}, err
	}
	user, err := s.store.GetUser(r.Context(), claims.UserID)
	if err != nil {
		return store.User{}, errors.New("unauthorized")
	}
	if user.Role != claims.Role {
		return store.User{}, errors.New("unauthorized")
	}
	return user, nil
}

func userFromContext(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(authUserKey).(store.User)
	return u, ok
}

// ─── Session / Cookie management ───

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, user store.User) error {
	sessionID, err := randomToken(24)
	if err != nil {
		return err
	}
	refreshToken, err := randomToken(32)
	if err != nil {
		return err
	}
	refreshExpiry := time.Now().UTC().Add(s.refreshTTL)
	now := time.Now().UTC()
	_, err = s.store.CreateWebSession(r.Context(), store.WebSession{
		SessionID:        sessionID,
		UserID:           user.ID,
		RefreshTokenHash: hashToken(refreshToken),
		RefreshExpiresAt: refreshExpiry,
		RemoteAddr:       clientIP(r),
		UserAgent:        r.UserAgent(),
		CreatedAt:        now,
	})
	if err != nil {
		return err
	}
	return s.writeCookies(w, sessionID, refreshToken, refreshExpiry, user)
}

func (s *Server) rotateSession(w http.ResponseWriter, r *http.Request, session store.WebSession, user store.User) error {
	refreshToken, err := randomToken(32)
	if err != nil {
		return err
	}
	refreshExpiry := time.Now().UTC().Add(s.refreshTTL)
	_, err = s.store.RotateWebSession(r.Context(), session.SessionID, hashToken(refreshToken), refreshExpiry, clientIP(r), r.UserAgent())
	if err != nil {
		return err
	}
	return s.writeCookies(w, session.SessionID, refreshToken, refreshExpiry, user)
}

func (s *Server) readRefreshSession(r *http.Request) (store.WebSession, string, error) {
	sc, err := r.Cookie(sessionCookieName)
	if err != nil {
		return store.WebSession{}, "", errors.New("no session cookie")
	}
	rc, err := r.Cookie(refreshCookieName)
	if err != nil {
		return store.WebSession{}, "", errors.New("no refresh cookie")
	}
	session, err := s.store.GetWebSessionByID(r.Context(), sc.Value)
	if err != nil {
		return store.WebSession{}, "", errors.New("invalid session")
	}
	return session, rc.Value, nil
}

func (s *Server) writeCookies(w http.ResponseWriter, sessionID, refreshToken string, refreshExpiry time.Time, user store.User) error {
	accessToken, accessExpiry, err := s.createAccessToken(user)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: accessCookieName, Value: accessToken, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: accessExpiry, Secure: s.cookiesSecure})
	http.SetCookie(w, &http.Cookie{Name: refreshCookieName, Value: refreshToken, Path: "/api/auth", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: refreshExpiry, Secure: s.cookiesSecure})
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: sessionID, Path: "/api/auth", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: refreshExpiry, Secure: s.cookiesSecure})
	return nil
}

func (s *Server) clearCookies(w http.ResponseWriter) {
	for _, item := range []struct{ name, path string }{
		{accessCookieName, "/"},
		{refreshCookieName, "/api/auth"},
		{sessionCookieName, "/api/auth"},
	} {
		http.SetCookie(w, &http.Cookie{Name: item.name, Value: "", Path: item.path, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(0, 0), Secure: s.cookiesSecure})
	}
}

// ─── Access token (signed cookie) ───

func (s *Server) createAccessToken(user store.User) (string, time.Time, error) {
	expiry := time.Now().UTC().Add(s.accessTTL)
	payload := fmt.Sprintf("%s|%s|%d", user.ID, user.Role, expiry.Unix())
	sig := signValue(payload, s.accessSecret)
	token := base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + sig))
	return token, expiry, nil
}

func (s *Server) readAccessClaims(r *http.Request) (authClaims, error) {
	c, err := r.Cookie(accessCookieName)
	if err != nil {
		return authClaims{}, errors.New("unauthorized")
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return authClaims{}, errors.New("unauthorized")
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 {
		return authClaims{}, errors.New("unauthorized")
	}
	payload := strings.Join(parts[:3], "|")
	if signValue(payload, s.accessSecret) != parts[3] {
		return authClaims{}, errors.New("unauthorized")
	}
	var expUnix int64
	if _, err := fmt.Sscanf(parts[2], "%d", &expUnix); err != nil {
		return authClaims{}, errors.New("unauthorized")
	}
	expiry := time.Unix(expUnix, 0).UTC()
	if time.Now().UTC().After(expiry) {
		return authClaims{}, errors.New("unauthorized")
	}
	return authClaims{UserID: parts[0], Role: parts[1], Expiry: expiry}, nil
}

// ─── CORS ───

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if _, ok := s.allowedOrigins[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Bootstrap-Secret")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── Crypto helpers ───

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func signValue(value, secret string) string {
	sum := sha256.Sum256([]byte(secret + ":" + value))
	return hex.EncodeToString(sum[:])
}

// ─── HTTP helpers ───

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeMethodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func clientIP(r *http.Request) string {
	if fwd := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); fwd != "" {
		parts := strings.Split(fwd, ",")
		return strings.TrimSpace(parts[0])
	}
	return r.RemoteAddr
}

func envOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func parseOrigins(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(item)
		if origin != "" {
			out[origin] = struct{}{}
		}
	}
	return out
}
