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

type TunnelFilter struct {
	NodeID string
	Type   string
	Status string
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
	ListNodes(ctx context.Context) ([]types.NodeSummary, error)
	CreateTunnel(ctx context.Context, spec types.TunnelSpec) (types.TunnelSpec, error)
	GetTunnel(ctx context.Context, id string) (types.TunnelSpec, error)
	UpdateTunnel(ctx context.Context, spec types.TunnelSpec) (types.TunnelSpec, error)
	DeleteTunnel(ctx context.Context, id string) error
	ListTunnels(ctx context.Context, filter TunnelFilter) ([]types.TunnelSpec, error)
	Counts(ctx context.Context) (Counts, error)

	BootstrapStatus(ctx context.Context) (bool, error)
	BootstrapAdmin(ctx context.Context, params CreateUserParams) (types.UserSummary, error)
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

	Close() error
}
