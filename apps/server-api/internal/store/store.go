package store

import (
	"context"
	"errors"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

var ErrNotFound = errors.New("not found")

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

type Store interface {
	Kind() string
	RegisterNode(ctx context.Context, req types.NodeRegisterRequest) (types.NodeSummary, error)
	HeartbeatNode(ctx context.Context, req types.NodeHeartbeatRequest) (types.NodeSummary, error)
	ListNodes(ctx context.Context) ([]types.NodeSummary, error)
	CreateTunnel(ctx context.Context, spec types.TunnelSpec) (types.TunnelSpec, error)
	ListTunnels(ctx context.Context, filter TunnelFilter) ([]types.TunnelSpec, error)
	Counts(ctx context.Context) (Counts, error)
	Close() error
}
