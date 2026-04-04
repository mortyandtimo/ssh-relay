package store

import (
	"context"
	"fmt"
	"sort"
	"strconv"
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
	s.mu.Lock()
	existing := s.nodes[nodeID]
	summary := types.NodeSummary{
		NodeID:        nodeID,
		NodeName:      req.NodeName,
		Status:        "online",
		AgentVersion:  req.AgentVersion,
		Capabilities:  req.Capabilities,
		ActiveTunnels: s.countActiveTunnelsForNode(nodeID),
		LastSeenAt:    now,
		Metadata:      mergeAgentMetadata(existing.Summary.Metadata, req.Metadata),
	}
	summary = hydrateNodeSummary(summary)
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
	record.Summary = hydrateNodeSummary(record.Summary)
	s.nodes[req.NodeID] = record
	return record.Summary, nil
}

func (s *InMemoryStore) ListNodes(_ context.Context, filter NodeFilter) ([]types.NodeSummary, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]types.NodeSummary, 0, len(s.nodes))
	for _, record := range s.nodes {
		summary := hydrateNodeSummary(record.Summary)
		if !matchesNodeFilter(summary, filter) {
			continue
		}
		items = append(items, summary)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].NodeID < items[j].NodeID })
	total := len(items)
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []types.NodeSummary{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return items[offset:end], total, nil
}

func (s *InMemoryStore) GetNode(_ context.Context, nodeID string) (types.NodeSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.nodes[nodeID]
	if !ok {
		return types.NodeSummary{}, ErrNotFound
	}
	return hydrateNodeSummary(record.Summary), nil
}

func (s *InMemoryStore) UpdateNode(_ context.Context, params UpdateNodeParams) (types.NodeSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.nodes[params.NodeID]
	if !ok {
		return types.NodeSummary{}, ErrNotFound
	}
	record.Summary.Metadata = mergeNodeMetadata(record.Summary.Metadata, params)
	record.Summary = hydrateNodeSummary(record.Summary)
	s.nodes[params.NodeID] = record
	return record.Summary, nil
}

func (s *InMemoryStore) CreateTunnel(_ context.Context, spec types.TunnelSpec) (types.TunnelSpec, error) {
	tunnel := normalizeTunnel(spec)
	tunnel.UpdatedAt = time.Now().UTC()
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
	tunnel.UpdatedAt = time.Now().UTC()
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
	bindingKey := TunnelPortBindingKey(tunnelType)
	if bindingKey == "" || status != "active" || publicPort == 0 {
		return false
	}
	for id, tunnel := range s.tunnels {
		if id == excludeID {
			continue
		}
		if tunnel.Status != "active" || tunnel.PublicPort != publicPort {
			continue
		}
		if TunnelPortBindingKey(tunnel.Type) == bindingKey {
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
	if tunnel.RuntimePath == "" {
		tunnel.RuntimePath = tunnel.Metadata["runtimePath"]
	} else {
		tunnel.Metadata["runtimePath"] = tunnel.RuntimePath
	}
	if tunnel.RuntimeState == "" {
		tunnel.RuntimeState = tunnel.Metadata["runtimeState"]
	} else {
		tunnel.Metadata["runtimeState"] = tunnel.RuntimeState
	}
	if tunnel.LastFailureReason == "" {
		tunnel.LastFailureReason = tunnel.Metadata["lastFailureReason"]
	} else {
		tunnel.Metadata["lastFailureReason"] = tunnel.LastFailureReason
	}
	if tunnel.UpdatedAt.IsZero() {
		tunnel.UpdatedAt = time.Now().UTC()
	}
	if tunnel.ProbePath == "" {
		tunnel.ProbePath = tunnel.Metadata["probePath"]
	} else {
		tunnel.Metadata["probePath"] = tunnel.ProbePath
	}
	if tunnel.Metadata["lastProbeSuccess"] == "true" {
		tunnel.LastProbeSuccess = true
	}
	if value := tunnel.Metadata["lastProbeStatusCode"]; value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			tunnel.LastProbeStatusCode = parsed
		}
	}
	tunnel.LastProbeError = tunnel.Metadata["lastProbeError"]
	tunnel.LastProbeTargetEntry = tunnel.Metadata["lastProbeTargetEntry"]
	tunnel.RuntimePath = tunnel.Metadata["runtimePath"]
	tunnel.RuntimeState = tunnel.Metadata["runtimeState"]
	tunnel.LastFailureReason = tunnel.Metadata["lastFailureReason"]
	if value := tunnel.Metadata["lastProbedAt"]; value != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			tunnel.LastProbedAt = parsed
		}
	}
	return tunnel
}

func hydrateNodeSummary(summary types.NodeSummary) types.NodeSummary {
	meta := parseNodeMetadata(summary.Metadata)
	summary.Metadata = meta.Extra
	summary.NodeRole = meta.NodeRole
	summary.Environment = meta.Environment
	summary.TrustLevel = meta.TrustLevel
	summary.Owner = meta.Owner
	summary.Location = meta.Location
	summary.Tags = meta.Tags
	summary.Isolated = meta.Isolated
	return summary
}

func parseNodeMetadata(input map[string]string) types.NodeMetadata {
	extra := map[string]string{}
	for key, value := range input {
		extra[key] = value
	}
	meta := types.NodeMetadata{Extra: extra}
	meta.Hostname = strings.TrimSpace(extra["hostname"])
	meta.OS = strings.TrimSpace(extra["os"])
	meta.Arch = strings.TrimSpace(extra["arch"])
	meta.NodeRole = types.NodeRole(strings.TrimSpace(extra["nodeRole"]))
	meta.Environment = types.NodeEnvironment(strings.TrimSpace(extra["environment"]))
	meta.TrustLevel = types.NodeTrustLevel(strings.TrimSpace(extra["trustLevel"]))
	meta.Owner = strings.TrimSpace(extra["owner"])
	meta.Location = strings.TrimSpace(extra["location"])
	meta.Tags = splitTags(extra["tags"])
	meta.Isolated = strings.TrimSpace(extra["isolated"]) == "true"
	return meta
}

func mergeNodeMetadata(existing map[string]string, params UpdateNodeParams) map[string]string {
	merged := map[string]string{}
	for key, value := range existing {
		merged[key] = value
	}
	merged["nodeRole"] = string(params.NodeRole)
	merged["environment"] = string(params.Environment)
	merged["trustLevel"] = string(params.TrustLevel)
	merged["owner"] = strings.TrimSpace(params.Owner)
	merged["location"] = strings.TrimSpace(params.Location)
	merged["tags"] = strings.Join(normalizeTags(params.Tags), ",")
	merged["isolated"] = fmt.Sprintf("%t", params.Isolated)
	return merged
}

func mergeAgentMetadata(existing, incoming map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range existing {
		merged[key] = value
	}
	reserved := map[string]struct{}{
		"nodeRole":    {},
		"environment": {},
		"trustLevel":  {},
		"owner":       {},
		"location":    {},
		"tags":        {},
	}
	for key, value := range incoming {
		if _, ok := reserved[key]; ok {
			if current := strings.TrimSpace(merged[key]); current != "" {
				continue
			}
		}
		merged[key] = value
	}
	return merged
}

func matchesNodeFilter(summary types.NodeSummary, filter NodeFilter) bool {
	if filter.NodeRole != "" && string(summary.NodeRole) != filter.NodeRole {
		return false
	}
	if filter.Environment != "" && string(summary.Environment) != filter.Environment {
		return false
	}
	if filter.TrustLevel != "" && string(summary.TrustLevel) != filter.TrustLevel {
		return false
	}
	if filter.Owner != "" && !strings.Contains(strings.ToLower(summary.Owner), strings.ToLower(filter.Owner)) {
		return false
	}
	if filter.Tag != "" {
		needle := strings.ToLower(strings.TrimSpace(filter.Tag))
		matched := false
		for _, tag := range summary.Tags {
			if strings.Contains(strings.ToLower(tag), needle) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func splitTags(input string) []string {
	if strings.TrimSpace(input) == "" {
		return []string{}
	}
	return normalizeTags(strings.Split(input, ","))
}

func normalizeTags(input []string) []string {
	seen := map[string]struct{}{}
	items := make([]string, 0, len(input))
	for _, raw := range input {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, tag)
	}
	sort.Strings(items)
	return items
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

func (s *InMemoryStore) ListAuditLogs(_ context.Context, filter AuditLogFilter) ([]types.AuditLogEntry, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]types.AuditLogEntry, 0, len(s.auditLogs))
	for _, item := range s.auditLogs {
		if filter.Action != "" && item.Action != filter.Action {
			continue
		}
		if filter.ActorType != "" && item.ActorType != filter.ActorType {
			continue
		}
		if filter.ActorID != "" && item.ActorID != filter.ActorID {
			continue
		}
		if filter.ResourceType != "" && item.ResourceType != filter.ResourceType {
			continue
		}
		if filter.ResourceID != "" && item.ResourceID != filter.ResourceID {
			continue
		}
		if filter.StartAt != nil && item.CreatedAt.Before(*filter.StartAt) {
			continue
		}
		if filter.EndAt != nil && item.CreatedAt.After(*filter.EndAt) {
			continue
		}
		items = append(items, item)
	}
	total := len(items)
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []types.AuditLogEntry{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	out := make([]types.AuditLogEntry, end-offset)
	copy(out, items[offset:end])
	return out, total, nil
}
