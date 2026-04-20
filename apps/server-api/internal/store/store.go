package store

import (
	"context"
	"errors"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var ErrUnauthorized = errors.New("unauthorized")
var ErrForbidden = errors.New("forbidden")
var ErrBootstrapRequired = errors.New("bootstrap required")

func TunnelPortBindingKey(tunnelType string) string {
	switch tunnelType {
	case "tcp", "socks5", "http":
		return "tcp"
	case "udp":
		return "udp"
	default:
		return ""
	}
}

type TunnelFilter struct {
	NodeID string
	Type   string
	Status string
}

type NodeFilter struct {
	NodeRole    string
	Environment string
	TrustLevel  string
	Owner       string
	Tag         string
	Limit       int
	Offset      int
}

type UpdateNodeParams struct {
	NodeID      string
	NodeRole    types.NodeRole
	Environment types.NodeEnvironment
	TrustLevel  types.NodeTrustLevel
	Owner       string
	Location    string
	Tags        []string
	Isolated    bool
}

type Counts struct {
	RegisteredNodes   int
	OnlineNodes       int
	ConfiguredTunnels int
}

type UserRecord struct {
	Summary      types.UserSummary
	PasswordHash string
}

type WebSession struct {
	SessionID        string
	UserID           string
	RefreshTokenHash string
	RefreshExpiresAt time.Time
	LastRefreshedAt  time.Time
	LastSeenAt       time.Time
	RemoteAddr       string
	UserAgent        string
	RevokedAt        *time.Time
	CreatedAt        time.Time
}

type AuthenticateUserParams struct {
	Email    string
	Password string
}

type CreateUserParams struct {
	Email       string
	DisplayName string
	Password    string
	Role        types.UserRole
}

type UpdateUserParams struct {
	ID          string
	DisplayName string
	Password    string
	Role        types.UserRole
	Disabled    *bool
}

type AuditLogFilter struct {
	Action          string
	ActionPrefix    string
	Outcome         string
	RejectionKind   string
	ExecutionMode   string
	PlaceholderOnly string
	ActorType       string
	ActorID         string
	ResourceType    string
	ResourceID      string
	StartAt         *time.Time
	EndAt           *time.Time
	Limit           int
	Offset          int
}

type AuditLogParams struct {
	ActorType    string
	ActorID      string
	Action       string
	ResourceType string
	ResourceID   string
	Payload      map[string]string
}

type CreateSessionParams struct {
	SessionID        string
	UserID           string
	RefreshTokenHash string
	RefreshExpiresAt time.Time
	RemoteAddr       string
	UserAgent        string
}

type Store interface {
	Kind() string
	RegisterNode(ctx context.Context, req types.NodeRegisterRequest) (types.NodeSummary, error)
	HeartbeatNode(ctx context.Context, req types.NodeHeartbeatRequest) (types.NodeSummary, error)
	ListNodes(ctx context.Context, filter NodeFilter) ([]types.NodeSummary, int, error)
	GetNode(ctx context.Context, nodeID string) (types.NodeSummary, error)
	UpdateNode(ctx context.Context, params UpdateNodeParams) (types.NodeSummary, error)
	CreateTunnel(ctx context.Context, spec types.TunnelSpec) (types.TunnelSpec, error)
	GetTunnel(ctx context.Context, id string) (types.TunnelSpec, error)
	UpdateTunnel(ctx context.Context, spec types.TunnelSpec) (types.TunnelSpec, error)
	DeleteTunnel(ctx context.Context, id string) error
	ListTunnels(ctx context.Context, filter TunnelFilter) ([]types.TunnelSpec, error)
	Counts(ctx context.Context) (Counts, error)
	PurgeStaleNodes(ctx context.Context, maxAge time.Duration) (int, error)

	BootstrapStatus(ctx context.Context) (bool, error)
	BootstrapAdmin(ctx context.Context, params CreateUserParams) (types.UserSummary, error)
	GetAuthSettings(ctx context.Context) (types.AuthSettings, error)
	UpdateAuthSettings(ctx context.Context, settings types.AuthSettings) (types.AuthSettings, error)
	AuthenticateUser(ctx context.Context, params AuthenticateUserParams) (types.UserSummary, error)
	ListUsers(ctx context.Context) ([]types.UserSummary, error)
	GetUser(ctx context.Context, id string) (types.UserSummary, error)
	CreateUser(ctx context.Context, params CreateUserParams) (types.UserSummary, error)
	UpdateUser(ctx context.Context, params UpdateUserParams) (types.UserSummary, error)
	DeleteUser(ctx context.Context, id string) error
	CreateWebSession(ctx context.Context, params CreateSessionParams) (WebSession, error)
	GetWebSessionByID(ctx context.Context, sessionID string) (WebSession, error)
	RotateWebSession(ctx context.Context, sessionID string, refreshTokenHash string, refreshExpiresAt time.Time, remoteAddr, userAgent string) (WebSession, error)
	DeleteWebSession(ctx context.Context, sessionID string) error
	DeleteUserSessions(ctx context.Context, userID string) error
	WriteAuditLog(ctx context.Context, params AuditLogParams) (types.AuditLogEntry, error)
	ListAuditLogs(ctx context.Context, filter AuditLogFilter) ([]types.AuditLogEntry, int, error)

	CreateCertificate(ctx context.Context, platformUserID string, spec types.CertificateSpec) (types.CertificateSpec, error)
	ListCertificates(ctx context.Context, platformUserID string) ([]types.CertificateSpec, error)
	FindCertificateByDomain(ctx context.Context, domain string) (*types.CertificateSpec, error)
	ListManagedHTTPSDomains(ctx context.Context, userID string) ([]types.ManagedHTTPSDomain, error)

	Close() error
}
