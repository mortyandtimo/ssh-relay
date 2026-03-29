package store

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

type InMemoryStore struct {
	mu      sync.RWMutex
	nodes   map[string]nodeRecord
	tunnels map[string]types.TunnelSpec
}

type nodeRecord struct {
	Summary types.NodeSummary
	Metrics map[string]string
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		nodes:   make(map[string]nodeRecord),
		tunnels: make(map[string]types.TunnelSpec),
	}
}

func (s *InMemoryStore) Kind() string {
	return "memory"
}

func (s *InMemoryStore) RegisterNode(_ context.Context, req types.NodeRegisterRequest) (types.NodeSummary, error) {
	nodeID := req.NodeID
	if nodeID == "" {
		nodeID = fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	now := time.Now().UTC()
	summary := types.NodeSummary{
		NodeID:        nodeID,
		NodeName:      req.NodeName,
		Status:        "online",
		AgentVersion:  req.AgentVersion,
		Capabilities:  req.Capabilities,
		ActiveTunnels: s.countActiveTunnelsForNode(nodeID),
		LastSeenAt:    now,
		Metadata:      req.Metadata,
	}

	s.mu.Lock()
	s.nodes[nodeID] = nodeRecord{Summary: summary, Metrics: map[string]string{}}
	s.mu.Unlock()
	return summary, nil
}

func (s *InMemoryStore) HeartbeatNode(_ context.Context, req types.NodeHeartbeatRequest) (types.NodeSummary, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.nodes[req.NodeID]
	if !ok {
		return types.NodeSummary{}, ErrNotFound
	}
	record.Summary.Status = "online"
	record.Summary.LastSeenAt = now
	record.Summary.ActiveTunnels = req.ActiveTunnels
	record.Metrics = req.Metrics
	s.nodes[req.NodeID] = record
	return record.Summary, nil
}

func (s *InMemoryStore) ListNodes(_ context.Context) ([]types.NodeSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]types.NodeSummary, 0, len(s.nodes))
	for _, record := range s.nodes {
		items = append(items, record.Summary)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].NodeID < items[j].NodeID })
	return items, nil
}

func (s *InMemoryStore) CreateTunnel(_ context.Context, spec types.TunnelSpec) (types.TunnelSpec, error) {
	tunnel := normalizeTunnel(spec)
	if tunnel.NodeID == "" {
		tunnel.NodeID = tunnel.Metadata["nodeId"]
	}
	if tunnel.Metadata == nil {
		tunnel.Metadata = map[string]string{}
	}
	tunnel.Metadata["nodeId"] = tunnel.NodeID
	s.mu.Lock()
	s.tunnels[tunnel.ID] = tunnel
	if record, ok := s.nodes[tunnel.NodeID]; ok {
		record.Summary.ActiveTunnels = s.countActiveTunnelsForNode(record.Summary.NodeID)
		s.nodes[record.Summary.NodeID] = record
	}
	s.mu.Unlock()
	return tunnel, nil
}

func (s *InMemoryStore) ListTunnels(_ context.Context, filter TunnelFilter) ([]types.TunnelSpec, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]types.TunnelSpec, 0, len(s.tunnels))
	for _, tunnel := range s.tunnels {
		if filter.NodeID != "" && tunnel.NodeID != filter.NodeID {
			continue
		}
		if filter.Type != "" && tunnel.Type != filter.Type {
			continue
		}
		if filter.Status != "" && tunnel.Status != filter.Status {
			continue
		}
		items = append(items, tunnel)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *InMemoryStore) Counts(_ context.Context) (Counts, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	online := 0
	for _, record := range s.nodes {
		if record.Summary.Status == "online" {
			online++
		}
	}
	return Counts{
		RegisteredNodes:   len(s.nodes),
		OnlineNodes:       online,
		ConfiguredTunnels: len(s.tunnels),
	}, nil
}

func (s *InMemoryStore) Close() error {
	return nil
}

func (s *InMemoryStore) countActiveTunnelsForNode(nodeID string) int {
	count := 0
	for _, tunnel := range s.tunnels {
		if tunnel.NodeID == nodeID && tunnel.Status == "active" {
			count++
		}
	}
	return count
}

func normalizeTunnel(spec types.TunnelSpec) types.TunnelSpec {
	tunnel := spec
	if tunnel.ID == "" {
		tunnel.ID = fmt.Sprintf("tunnel-%d", time.Now().UnixNano())
	}
	if tunnel.Type == "" {
		tunnel.Type = "tcp"
	}
	if tunnel.Status == "" {
		tunnel.Status = "active"
	}
	if tunnel.TransportPolicy == "" {
		tunnel.TransportPolicy = "relay_only"
	}
	if tunnel.Metadata == nil {
		tunnel.Metadata = map[string]string{}
	}
	return tunnel
}
