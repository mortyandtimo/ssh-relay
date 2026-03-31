package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
	"golang.org/x/crypto/bcrypt"
)

type InMemoryStore struct {
	mu          sync.RWMutex
	nodes       map[string]nodeRecord
	tunnels     map[string]types.TunnelSpec
	users       map[string]UserRecord
	userByEM    map[string]string
	sessions    map[string]WebSession
	auditLogs   []types.AuditLogEntry
	nextAuditID int64
}

type nodeRecord struct {
	Summary types.NodeSummary
	Metrics map[string]string
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		nodes:       make(map[string]nodeRecord),
		tunnels:     make(map[string]types.TunnelSpec),
		users:       make(map[string]UserRecord),
		userByEM:    make(map[string]string),
		sessions:    make(map[string]WebSession),
		auditLogs:   make([]types.AuditLogEntry, 0),
		nextAuditID: 1,
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
	if s.findPublicPortConflictLocked(tunnel.ID, tunnel.Type, tunnel.Status, tunnel.PublicPort) {
		s.mu.Unlock()
		return types.TunnelSpec{}, ErrConflict
	}
	s.tunnels[tunnel.ID] = tunnel
	if record, ok := s.nodes[tunnel.NodeID]; ok {
		record.Summary.ActiveTunnels = s.countActiveTunnelsForNode(record.Summary.NodeID)
		s.nodes[record.Summary.NodeID] = record
	}
	s.mu.Unlock()
	return tunnel, nil
}

func (s *InMemoryStore) GetTunnel(_ context.Context, id string) (types.TunnelSpec, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tunnel, ok := s.tunnels[id]
	if !ok {
		return types.TunnelSpec{}, ErrNotFound
	}
	return tunnel, nil
}

func (s *InMemoryStore) UpdateTunnel(_ context.Context, spec types.TunnelSpec) (types.TunnelSpec, error) {
	tunnel := normalizeTunnel(spec)
	if tunnel.ID == "" {
		return types.TunnelSpec{}, ErrNotFound
	}
	if tunnel.NodeID == "" {
		tunnel.NodeID = tunnel.Metadata["nodeId"]
	}
	if tunnel.Metadata == nil {
		tunnel.Metadata = map[string]string{}
	}
	tunnel.Metadata["nodeId"] = tunnel.NodeID

	s.mu.Lock()
	if _, ok := s.tunnels[tunnel.ID]; !ok {
		s.mu.Unlock()
		return types.TunnelSpec{}, ErrNotFound
	}
	if s.findPublicPortConflictLocked(tunnel.ID, tunnel.Type, tunnel.Status, tunnel.PublicPort) {
		s.mu.Unlock()
		return types.TunnelSpec{}, ErrConflict
	}
	s.tunnels[tunnel.ID] = tunnel
	for nodeID, record := range s.nodes {
		record.Summary.ActiveTunnels = s.countActiveTunnelsForNode(nodeID)
		s.nodes[nodeID] = record
	}
	s.mu.Unlock()
	return tunnel, nil
}

func (s *InMemoryStore) DeleteTunnel(_ context.Context, id string) error {
	s.mu.Lock()
	tunnel, ok := s.tunnels[id]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	delete(s.tunnels, id)
	if record, ok := s.nodes[tunnel.NodeID]; ok {
		record.Summary.ActiveTunnels = s.countActiveTunnelsForNode(record.Summary.NodeID)
		s.nodes[record.Summary.NodeID] = record
	}
	s.mu.Unlock()
	return nil
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

func (s *InMemoryStore) BootstrapStatus(_ context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users) == 0, nil
}

func (s *InMemoryStore) BootstrapAdmin(ctx context.Context, params CreateUserParams) (types.UserSummary, error) {
	required, err := s.BootstrapStatus(ctx)
	if err != nil {
		return types.UserSummary{}, err
	}
	if !required {
		return types.UserSummary{}, ErrConflict
	}
	params.Role = types.UserRoleAdmin
	return s.CreateUser(ctx, params)
}

func (s *InMemoryStore) AuthenticateUser(_ context.Context, params AuthenticateUserParams) (types.UserSummary, error) {
	email := normalizeEmail(params.Email)
	s.mu.RLock()
	userID, ok := s.userByEM[email]
	if !ok {
		s.mu.RUnlock()
		return types.UserSummary{}, ErrUnauthorized
	}
	record := s.users[userID]
	s.mu.RUnlock()
	if bcrypt.CompareHashAndPassword([]byte(record.PasswordHash), []byte(params.Password)) != nil {
		return types.UserSummary{}, ErrUnauthorized
	}
	return record.Summary, nil
}

func (s *InMemoryStore) ListUsers(_ context.Context) ([]types.UserSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]types.UserSummary, 0, len(s.users))
	for _, record := range s.users {
		items = append(items, record.Summary)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Email < items[j].Email })
	return items, nil
}

func (s *InMemoryStore) GetUser(_ context.Context, id string) (types.UserSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.users[id]
	if !ok {
		return types.UserSummary{}, ErrNotFound
	}
	return record.Summary, nil
}

func (s *InMemoryStore) CreateUser(_ context.Context, params CreateUserParams) (types.UserSummary, error) {
	role := normalizeUserRole(params.Role)
	email := normalizeEmail(params.Email)
	if email == "" || strings.TrimSpace(params.DisplayName) == "" || params.Password == "" {
		return types.UserSummary{}, ErrConflict
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
	if err != nil {
		return types.UserSummary{}, err
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("user-%d", now.UnixNano())
	summary := types.UserSummary{ID: id, Email: email, DisplayName: strings.TrimSpace(params.DisplayName), Role: role, CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.userByEM[email]; exists {
		return types.UserSummary{}, ErrConflict
	}
	s.users[id] = UserRecord{Summary: summary, PasswordHash: string(hash)}
	s.userByEM[email] = id
	return summary, nil
}

func (s *InMemoryStore) UpdateUser(_ context.Context, params UpdateUserParams) (types.UserSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.users[params.ID]
	if !ok {
		return types.UserSummary{}, ErrNotFound
	}
	if strings.TrimSpace(params.DisplayName) != "" {
		record.Summary.DisplayName = strings.TrimSpace(params.DisplayName)
	}
	if params.Role != "" {
		record.Summary.Role = normalizeUserRole(params.Role)
	}
	if params.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
		if err != nil {
			return types.UserSummary{}, err
		}
		record.PasswordHash = string(hash)
	}
	record.Summary.UpdatedAt = time.Now().UTC()
	s.users[params.ID] = record
	return record.Summary, nil
}

func (s *InMemoryStore) DeleteUser(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.userByEM, record.Summary.Email)
	delete(s.users, id)
	for sid, sess := range s.sessions {
		if sess.UserID == id {
			delete(s.sessions, sid)
		}
	}
	return nil
}

func (s *InMemoryStore) CreateWebSession(_ context.Context, params CreateSessionParams) (WebSession, error) {
	now := time.Now().UTC()
	sessionID := params.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("sess-%d", now.UnixNano())
	}
	session := WebSession{
		SessionID:        sessionID,
		UserID:           params.UserID,
		RefreshTokenHash: params.RefreshTokenHash,
		RefreshExpiresAt: params.RefreshExpiresAt,
		LastRefreshedAt:  now,
		LastSeenAt:       now,
		RemoteAddr:       params.RemoteAddr,
		UserAgent:        params.UserAgent,
		CreatedAt:        now,
	}
	s.mu.Lock()
	s.sessions[session.SessionID] = session
	s.mu.Unlock()
	return session, nil
}

func (s *InMemoryStore) GetWebSessionByID(_ context.Context, sessionID string) (WebSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return WebSession{}, ErrNotFound
	}
	if session.RevokedAt != nil {
		return WebSession{}, ErrUnauthorized
	}
	return session, nil
}

func (s *InMemoryStore) RotateWebSession(_ context.Context, sessionID string, refreshTokenHash string, refreshExpiresAt time.Time, remoteAddr, userAgent string) (WebSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return WebSession{}, ErrNotFound
	}
	if session.RevokedAt != nil {
		return WebSession{}, ErrUnauthorized
	}
	now := time.Now().UTC()
	session.RefreshTokenHash = refreshTokenHash
	session.RefreshExpiresAt = refreshExpiresAt
	session.LastRefreshedAt = now
	session.LastSeenAt = now
	session.RemoteAddr = remoteAddr
	session.UserAgent = userAgent
	s.sessions[sessionID] = session
	return session, nil
}

func (s *InMemoryStore) DeleteWebSession(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionID]; !ok {
		return ErrNotFound
	}
	delete(s.sessions, sessionID)
	return nil
}

func (s *InMemoryStore) DeleteUserSessions(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sid, session := range s.sessions {
		if session.UserID == userID {
			delete(s.sessions, sid)
		}
	}
	return nil
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

func (s *InMemoryStore) findPublicPortConflictLocked(excludeID, tunnelType, status string, publicPort int) bool {
	if tunnelType != "tcp" || status != "active" || publicPort == 0 {
		return false
	}
	for id, tunnel := range s.tunnels {
		if id == excludeID {
			continue
		}
		if tunnel.Type == "tcp" && tunnel.Status == "active" && tunnel.PublicPort == publicPort {
			return true
		}
	}
	return false
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

func normalizeEmail(input string) string {
	return strings.ToLower(strings.TrimSpace(input))
}

func normalizeUserRole(role types.UserRole) types.UserRole {
	switch role {
	case types.UserRoleAdmin, types.UserRoleManager, types.UserRoleUser:
		return role
	default:
		return types.UserRoleUser
	}
}

func (s *InMemoryStore) WriteAuditLog(_ context.Context, params AuditLogParams) (types.AuditLogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := types.AuditLogEntry{
		ID:           s.nextAuditID,
		ActorType:    params.ActorType,
		ActorID:      params.ActorID,
		Action:       params.Action,
		ResourceType: params.ResourceType,
		ResourceID:   params.ResourceID,
		Payload:      params.Payload,
		CreatedAt:    time.Now().UTC(),
	}
	s.nextAuditID++
	s.auditLogs = append([]types.AuditLogEntry{entry}, s.auditLogs...)
	return entry, nil
}

func (s *InMemoryStore) ListAuditLogs(_ context.Context, limit int) ([]types.AuditLogEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	if limit > len(s.auditLogs) {
		limit = len(s.auditLogs)
	}
	items := make([]types.AuditLogEntry, limit)
	copy(items, s.auditLogs[:limit])
	return items, nil
}
