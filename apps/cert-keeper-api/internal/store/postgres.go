package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Kind() string { return "postgres" }
func (s *PostgresStore) Close() error { s.pool.Close(); return nil }

// ─── Auth ───

func (s *PostgresStore) BootstrapStatus(ctx context.Context) (bool, error) {
	var count int
	err := s.pool.QueryRow(ctx, `select count(*) from ck_users`).Scan(&count)
	if err != nil {
		return true, err
	}
	return count == 0, nil
}

func (s *PostgresStore) BootstrapAdmin(ctx context.Context, email, displayName, password string) (User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	id := fmt.Sprintf("cku-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	var u User
	err = s.pool.QueryRow(ctx,
		`insert into ck_users (id, email, display_name, password_hash, role, created_at, updated_at)
		 values ($1, $2, $3, $4, 'admin', $5, $5) returning id, email, display_name, role, created_at, updated_at`,
		id, email, displayName, string(hash), now,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func (s *PostgresStore) AuthenticateUser(ctx context.Context, email, password string) (User, error) {
	var u User
	var hash string
	err := s.pool.QueryRow(ctx,
		`select id, email, display_name, password_hash, role, created_at, updated_at from ck_users where email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &hash, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, fmt.Errorf("invalid credentials")
		}
		return User{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return User{}, fmt.Errorf("invalid credentials")
	}
	u.PasswordHash = hash
	return u, nil
}

func (s *PostgresStore) GetUser(ctx context.Context, id string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`select id, email, display_name, role, created_at, updated_at from ck_users where id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// ─── Sessions ───

func (s *PostgresStore) CreateWebSession(ctx context.Context, session WebSession) (WebSession, error) {
	_, err := s.pool.Exec(ctx,
		`insert into ck_web_sessions (id, user_id, refresh_token_hash, refresh_expires_at, remote_addr, user_agent, created_at, last_seen_at, last_refreshed_at)
		 values ($1, $2, $3, $4, $5, $6, $7, $7, $7)`,
		session.SessionID, session.UserID, session.RefreshTokenHash, session.RefreshExpiresAt,
		session.RemoteAddr, session.UserAgent, session.CreatedAt,
	)
	return session, err
}

func (s *PostgresStore) GetWebSessionByID(ctx context.Context, sessionID string) (WebSession, error) {
	var ws WebSession
	err := s.pool.QueryRow(ctx,
		`select id, user_id, refresh_token_hash, refresh_expires_at, remote_addr, user_agent, created_at, last_seen_at, last_refreshed_at, revoked_at
		 from ck_web_sessions where id = $1 and revoked_at is null and refresh_expires_at > now()`,
		sessionID,
	).Scan(&ws.SessionID, &ws.UserID, &ws.RefreshTokenHash, &ws.RefreshExpiresAt,
		&ws.RemoteAddr, &ws.UserAgent, &ws.CreatedAt, &ws.LastSeenAt, &ws.LastRefreshedAt, &ws.RevokedAt)
	if err != nil {
		return WebSession{}, err
	}
	return ws, nil
}

func (s *PostgresStore) RotateWebSession(ctx context.Context, sessionID, refreshTokenHash string, refreshExpiresAt time.Time, remoteAddr, userAgent string) (WebSession, error) {
	var ws WebSession
	err := s.pool.QueryRow(ctx,
		`update ck_web_sessions set refresh_token_hash = $2, refresh_expires_at = $3, remote_addr = $4, user_agent = $5, last_refreshed_at = now()
		 where id = $1 and revoked_at is null returning id, user_id, refresh_token_hash, refresh_expires_at, remote_addr, user_agent, created_at, last_seen_at, last_refreshed_at, revoked_at`,
		sessionID, refreshTokenHash, refreshExpiresAt, remoteAddr, userAgent,
	).Scan(&ws.SessionID, &ws.UserID, &ws.RefreshTokenHash, &ws.RefreshExpiresAt,
		&ws.RemoteAddr, &ws.UserAgent, &ws.CreatedAt, &ws.LastSeenAt, &ws.LastRefreshedAt, &ws.RevokedAt)
	return ws, err
}

func (s *PostgresStore) DeleteWebSession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `update ck_web_sessions set revoked_at = now() where id = $1`, sessionID)
	return err
}

func (s *PostgresStore) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `update ck_web_sessions set revoked_at = now() where user_id = $1`, userID)
	return err
}

// ─── Certificates ───

func (s *PostgresStore) CreateCertificate(ctx context.Context, cert Certificate) (Certificate, error) {
	if cert.ID == "" {
		cert.ID = fmt.Sprintf("ckc-%d", time.Now().UnixNano())
	}
	now := time.Now().UTC()
	cert.CreatedAt = now
	cert.UpdatedAt = now
	err := s.pool.QueryRow(ctx,
		`insert into ck_certificates (id, user_id, domain, cert_pem, key_pem, issuer, auto_renew, expires_at, dns_verified_at, last_renewed_at, renew_error, created_at, updated_at)
		 values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)
		 returning created_at, updated_at`,
		cert.ID, cert.UserID, cert.Domain, cert.CertPEM, cert.KeyPEM, cert.Issuer, cert.AutoRenew,
		cert.ExpiresAt, cert.DNSVerifiedAt, cert.LastRenewedAt, cert.RenewError, now,
	).Scan(&cert.CreatedAt, &cert.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "idx_ck_certs_domain") {
		return Certificate{}, fmt.Errorf("domain %s already has a certificate", cert.Domain)
	}
	return cert, err
}

func (s *PostgresStore) GetCertificate(ctx context.Context, id string) (Certificate, error) {
	var c Certificate
	err := s.pool.QueryRow(ctx,
		`select id, user_id, domain, cert_pem, key_pem, issuer, auto_renew, expires_at, dns_verified_at, last_renewed_at, renew_error, created_at, updated_at
		 from ck_certificates where id = $1`, id,
	).Scan(&c.ID, &c.UserID, &c.Domain, &c.CertPEM, &c.KeyPEM, &c.Issuer, &c.AutoRenew,
		&c.ExpiresAt, &c.DNSVerifiedAt, &c.LastRenewedAt, &c.RenewError, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Certificate{}, err
	}
	return c, nil
}

func (s *PostgresStore) ListCertificates(ctx context.Context, userID string) ([]Certificate, error) {
	rows, err := s.pool.Query(ctx,
		`select id, user_id, domain, cert_pem, key_pem, issuer, auto_renew, expires_at, dns_verified_at, last_renewed_at, renew_error, created_at, updated_at
		 from ck_certificates where user_id = $1 order by domain`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var certs []Certificate
	for rows.Next() {
		var c Certificate
		if err := rows.Scan(&c.ID, &c.UserID, &c.Domain, &c.CertPEM, &c.KeyPEM, &c.Issuer, &c.AutoRenew,
			&c.ExpiresAt, &c.DNSVerifiedAt, &c.LastRenewedAt, &c.RenewError, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	if certs == nil {
		certs = []Certificate{}
	}
	return certs, nil
}

func (s *PostgresStore) ListAllCertificates(ctx context.Context) ([]Certificate, error) {
	rows, err := s.pool.Query(ctx,
		`select id, user_id, domain, cert_pem, key_pem, issuer, auto_renew, expires_at, dns_verified_at, last_renewed_at, renew_error, created_at, updated_at
		 from ck_certificates order by domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var certs []Certificate
	for rows.Next() {
		var c Certificate
		if err := rows.Scan(&c.ID, &c.UserID, &c.Domain, &c.CertPEM, &c.KeyPEM, &c.Issuer, &c.AutoRenew,
			&c.ExpiresAt, &c.DNSVerifiedAt, &c.LastRenewedAt, &c.RenewError, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	if certs == nil {
		certs = []Certificate{}
	}
	return certs, nil
}

func (s *PostgresStore) UpdateCertificate(ctx context.Context, cert Certificate) (Certificate, error) {
	cert.UpdatedAt = time.Now().UTC()
	err := s.pool.QueryRow(ctx,
		`update ck_certificates set cert_pem = $2, key_pem = $3, issuer = $4, auto_renew = $5, expires_at = $6, dns_verified_at = $7, last_renewed_at = $8, renew_error = $9, updated_at = $10
		 where id = $1 returning created_at, updated_at`,
		cert.ID, cert.CertPEM, cert.KeyPEM, cert.Issuer, cert.AutoRenew,
		cert.ExpiresAt, cert.DNSVerifiedAt, cert.LastRenewedAt, cert.RenewError, cert.UpdatedAt,
	).Scan(&cert.CreatedAt, &cert.UpdatedAt)
	return cert, err
}

func (s *PostgresStore) DeleteCertificate(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `delete from ck_certificates where id = $1`, id)
	return err
}

func (s *PostgresStore) FindCertificateByDomain(ctx context.Context, domain string) (*Certificate, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	var c Certificate
	err := s.pool.QueryRow(ctx,
		`select id, user_id, domain, cert_pem, key_pem, issuer, auto_renew, expires_at, dns_verified_at, last_renewed_at, renew_error, created_at, updated_at
		 from ck_certificates where domain = $1 limit 1`, domain,
	).Scan(&c.ID, &c.UserID, &c.Domain, &c.CertPEM, &c.KeyPEM, &c.Issuer, &c.AutoRenew,
		&c.ExpiresAt, &c.DNSVerifiedAt, &c.LastRenewedAt, &c.RenewError, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}
