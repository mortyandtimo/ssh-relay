package store

import (
	"context"
	"time"
)

// Certificate represents a managed SSL certificate.
type Certificate struct {
	ID             string     `json:"id"`
	UserID         string     `json:"userId"`
	Domain         string     `json:"domain"`
	CertPEM        string     `json:"certPem"`
	KeyPEM         string     `json:"keyPem,omitempty"`
	Issuer         string     `json:"issuer"`
	AutoRenew      bool       `json:"autoRenew"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	DNSVerifiedAt  *time.Time `json:"dnsVerifiedAt,omitempty"`
	LastRenewedAt  *time.Time `json:"lastRenewedAt,omitempty"`
	RenewError     string     `json:"renewError,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// User represents a cert-keeper user.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"displayName"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// WebSession represents an authenticated session.
type WebSession struct {
	SessionID        string     `json:"sessionId"`
	UserID           string     `json:"userId"`
	RefreshTokenHash string     `json:"-"`
	RefreshExpiresAt time.Time  `json:"refreshExpiresAt"`
	RemoteAddr       string     `json:"remoteAddr,omitempty"`
	UserAgent        string     `json:"userAgent,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	LastSeenAt       time.Time  `json:"lastSeenAt"`
	LastRefreshedAt  time.Time  `json:"lastRefreshedAt"`
	RevokedAt        *time.Time `json:"revokedAt,omitempty"`
}

// DNSCheckResult represents the result of a DNS resolution check.
type DNSCheckResult struct {
	Resolved bool     `json:"resolved"`
	IPs      []string `json:"ips"`
	Matches  bool     `json:"matches"`
}

// Store is the persistence interface for cert-keeper.
type Store interface {
	Kind() string
	Close() error

	// Auth
	BootstrapStatus(ctx context.Context) (bool, error)
	BootstrapAdmin(ctx context.Context, email, displayName, password string) (User, error)
	AuthenticateUser(ctx context.Context, email, password string) (User, error)
	GetUser(ctx context.Context, id string) (User, error)

	// Sessions
	CreateWebSession(ctx context.Context, session WebSession) (WebSession, error)
	GetWebSessionByID(ctx context.Context, sessionID string) (WebSession, error)
	RotateWebSession(ctx context.Context, sessionID, refreshTokenHash string, refreshExpiresAt time.Time, remoteAddr, userAgent string) (WebSession, error)
	DeleteWebSession(ctx context.Context, sessionID string) error
	DeleteUserSessions(ctx context.Context, userID string) error

	// Certificates
	CreateCertificate(ctx context.Context, cert Certificate) (Certificate, error)
	GetCertificate(ctx context.Context, id string) (Certificate, error)
	ListCertificates(ctx context.Context, userID string) ([]Certificate, error)
	ListAllCertificates(ctx context.Context) ([]Certificate, error)
	UpdateCertificate(ctx context.Context, cert Certificate) (Certificate, error)
	DeleteCertificate(ctx context.Context, id string) error
	FindCertificateByDomain(ctx context.Context, domain string) (*Certificate, error)
}
