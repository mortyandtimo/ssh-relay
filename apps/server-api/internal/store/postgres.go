package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
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

func (s *PostgresStore) Kind() string {
	return "postgres"
}

func (s *PostgresStore) RegisterNode(ctx context.Context, req types.NodeRegisterRequest) (types.NodeSummary, error) {
	nodeID := req.NodeID
	if nodeID == "" {
		nodeID = fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	now := time.Now().UTC()
	caps, err := json.Marshal(req.Capabilities)
	if err != nil {
		return types.NodeSummary{}, err
	}
	existingMeta := map[string]string{}
	row := s.pool.QueryRow(ctx, `select metadata from nodes where id = $1`, nodeID)
	var existingJSON []byte
	if err := row.Scan(&existingJSON); err == nil {
		existingMeta, err = unmarshalMap(existingJSON)
		if err != nil {
			return types.NodeSummary{}, err
		}
	} else if !strings.Contains(err.Error(), "no rows") {
		return types.NodeSummary{}, err
	}
	mergedMeta := mergeAgentMetadata(existingMeta, req.Metadata)
	meta, err := marshalMap(mergedMeta)
	if err != nil {
		return types.NodeSummary{}, err
	}
	_, err = s.pool.Exec(ctx, `
		insert into nodes (id, name, status, agent_version, capabilities, metadata, last_seen_at)
		values ($1, $2, 'online', $3, $4, $5, $6)
		on conflict (id) do update set
			name = excluded.name,
			status = 'online',
			agent_version = excluded.agent_version,
			capabilities = excluded.capabilities,
			metadata = excluded.metadata,
			last_seen_at = excluded.last_seen_at,
			updated_at = now()
	`, nodeID, req.NodeName, req.AgentVersion, caps, meta, now)
	if err != nil {
		return types.NodeSummary{}, err
	}
	return types.NodeSummary{
		NodeID:        nodeID,
		NodeName:      req.NodeName,
		Status:        "online",
		AgentVersion:  req.AgentVersion,
		Capabilities:  req.Capabilities,
		ActiveTunnels: 0,
		LastSeenAt:    now,
		Metadata:      mergedMeta,
	}, nil
}

func (s *PostgresStore) HeartbeatNode(ctx context.Context, req types.NodeHeartbeatRequest) (types.NodeSummary, error) {
	now := time.Now().UTC()
	metrics, err := marshalMap(req.Metrics)
	if err != nil {
		return types.NodeSummary{}, err
	}
	row := s.pool.QueryRow(ctx, `
		update nodes
		set status = 'online', last_seen_at = $2, updated_at = now()
		where id = $1
		returning name, agent_version, capabilities, metadata
	`, req.NodeID, now)
	var name, agentVersion string
	var capabilitiesJSON, metadataJSON []byte
	if err := row.Scan(&name, &agentVersion, &capabilitiesJSON, &metadataJSON); err != nil {
		return types.NodeSummary{}, ErrNotFound
	}
	_, err = s.pool.Exec(ctx, `
		insert into node_metrics (node_id, payload, observed_at)
		values ($1, $2, $3)
	`, req.NodeID, metrics, now)
	if err != nil {
		return types.NodeSummary{}, err
	}
	capabilities, err := unmarshalCapabilities(capabilitiesJSON)
	if err != nil {
		return types.NodeSummary{}, err
	}
	metadata, err := unmarshalMap(metadataJSON)
	if err != nil {
		return types.NodeSummary{}, err
	}
	return types.NodeSummary{
		NodeID:        req.NodeID,
		NodeName:      name,
		Status:        "online",
		AgentVersion:  agentVersion,
		Capabilities:  capabilities,
		ActiveTunnels: req.ActiveTunnels,
		LastSeenAt:    now,
		Metadata:      metadata,
	}, nil
}

func (s *PostgresStore) ListNodes(ctx context.Context, filter NodeFilter) ([]types.NodeSummary, int, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	var total int
	err := s.pool.QueryRow(ctx, `
		select count(*)
		from nodes n
		where ($1 = '' or coalesce(n.metadata->>'nodeRole', '') = $1)
		  and ($2 = '' or coalesce(n.metadata->>'environment', '') = $2)
		  and ($3 = '' or coalesce(n.metadata->>'trustLevel', '') = $3)
		  and ($4 = '' or lower(coalesce(n.metadata->>'owner', '')) like '%' || lower($4) || '%')
		  and ($5 = '' or lower(coalesce(n.metadata->>'tags', '')) like '%' || lower($5) || '%')
	`, filter.NodeRole, filter.Environment, filter.TrustLevel, filter.Owner, filter.Tag).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		select
			n.id,
			n.name,
			n.status,
			n.agent_version,
			n.capabilities,
			n.metadata,
			coalesce(n.last_seen_at, now()),
			coalesce((select count(*) from tunnels t where t.node_id = n.id and t.status = 'active'), 0)
		from nodes n
		where ($1 = '' or coalesce(n.metadata->>'nodeRole', '') = $1)
		  and ($2 = '' or coalesce(n.metadata->>'environment', '') = $2)
		  and ($3 = '' or coalesce(n.metadata->>'trustLevel', '') = $3)
		  and ($4 = '' or lower(coalesce(n.metadata->>'owner', '')) like '%' || lower($4) || '%')
		  and ($5 = '' or lower(coalesce(n.metadata->>'tags', '')) like '%' || lower($5) || '%')
		order by n.id
		limit $6 offset $7
	`, filter.NodeRole, filter.Environment, filter.TrustLevel, filter.Owner, filter.Tag, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]types.NodeSummary, 0)
	for rows.Next() {
		var item types.NodeSummary
		var capabilitiesJSON, metadataJSON []byte
		if err := rows.Scan(
			&item.NodeID,
			&item.NodeName,
			&item.Status,
			&item.AgentVersion,
			&capabilitiesJSON,
			&metadataJSON,
			&item.LastSeenAt,
			&item.ActiveTunnels,
		); err != nil {
			return nil, 0, err
		}
		item.Capabilities, err = unmarshalCapabilities(capabilitiesJSON)
		if err != nil {
			return nil, 0, err
		}
		item.Metadata, err = unmarshalMap(metadataJSON)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, hydrateNodeSummary(item))
	}
	return items, total, rows.Err()
}

func (s *PostgresStore) GetNode(ctx context.Context, nodeID string) (types.NodeSummary, error) {
	row := s.pool.QueryRow(ctx, `
		select
			n.id,
			n.name,
			n.status,
			n.agent_version,
			n.capabilities,
			n.metadata,
			coalesce(n.last_seen_at, now()),
			coalesce((select count(*) from tunnels t where t.node_id = n.id and t.status = 'active'), 0)
		from nodes n
		where n.id = $1
	`, nodeID)
	var item types.NodeSummary
	var capabilitiesJSON, metadataJSON []byte
	if err := row.Scan(&item.NodeID, &item.NodeName, &item.Status, &item.AgentVersion, &capabilitiesJSON, &metadataJSON, &item.LastSeenAt, &item.ActiveTunnels); err != nil {
		return types.NodeSummary{}, ErrNotFound
	}
	var err error
	item.Capabilities, err = unmarshalCapabilities(capabilitiesJSON)
	if err != nil {
		return types.NodeSummary{}, err
	}
	item.Metadata, err = unmarshalMap(metadataJSON)
	if err != nil {
		return types.NodeSummary{}, err
	}
	return hydrateNodeSummary(item), nil
}

func (s *PostgresStore) UpdateNode(ctx context.Context, params UpdateNodeParams) (types.NodeSummary, error) {
	current, err := s.GetNode(ctx, params.NodeID)
	if err != nil {
		return types.NodeSummary{}, err
	}
	metadata := mergeNodeMetadata(current.Metadata, params)
	metaJSON, err := marshalMap(metadata)
	if err != nil {
		return types.NodeSummary{}, err
	}
	commandTag, err := s.pool.Exec(ctx, `
		update nodes
		set metadata = $2, updated_at = now()
		where id = $1
	`, params.NodeID, metaJSON)
	if err != nil {
		return types.NodeSummary{}, err
	}
	if commandTag.RowsAffected() == 0 {
		return types.NodeSummary{}, ErrNotFound
	}
	return s.GetNode(ctx, params.NodeID)
}

func (s *PostgresStore) CreateTunnel(ctx context.Context, spec types.TunnelSpec) (types.TunnelSpec, error) {
	tunnel := normalizeTunnel(spec)
	if tunnel.Metadata == nil {
		tunnel.Metadata = map[string]string{}
	}
	tunnel.Metadata["nodeId"] = tunnel.NodeID
	conflict, err := s.hasPublicPortConflict(ctx, tunnel.ID, tunnel.Type, tunnel.Status, tunnel.PublicPort)
	if err != nil {
		return types.TunnelSpec{}, err
	}
	if conflict {
		return types.TunnelSpec{}, ErrConflict
	}
	meta, err := marshalMap(tunnel.Metadata)
	if err != nil {
		return types.TunnelSpec{}, err
	}
	_, err = s.pool.Exec(ctx, `
		insert into tunnels (id, node_id, name, type, transport_policy, status, target_host, target_port, public_port, domain, tls_mode, metadata)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		on conflict (id) do update set
			node_id = excluded.node_id,
			name = excluded.name,
			type = excluded.type,
			transport_policy = excluded.transport_policy,
			status = excluded.status,
			target_host = excluded.target_host,
			target_port = excluded.target_port,
			public_port = excluded.public_port,
			domain = excluded.domain,
			tls_mode = excluded.tls_mode,
			metadata = excluded.metadata,
			updated_at = now()
	`, tunnel.ID, tunnel.NodeID, tunnel.Name, tunnel.Type, tunnel.TransportPolicy, tunnel.Status, tunnel.TargetHost, tunnel.TargetPort, tunnel.PublicPort, emptyStringToNil(tunnel.Domain), emptyStringToNil(tunnel.TLSMode), meta)
	if err != nil {
		return types.TunnelSpec{}, err
	}
	return tunnel, nil
}

func (s *PostgresStore) GetTunnel(ctx context.Context, id string) (types.TunnelSpec, error) {
	row := s.pool.QueryRow(ctx, `
		select id, coalesce(node_id, ''), name, type, transport_policy, status, target_host, target_port, coalesce(public_port, 0), coalesce(domain, ''), coalesce(tls_mode, ''), metadata
		from tunnels
		where id = $1
	`, id)
	var item types.TunnelSpec
	var nodeID string
	var metadataJSON []byte
	if err := row.Scan(&item.ID, &nodeID, &item.Name, &item.Type, &item.TransportPolicy, &item.Status, &item.TargetHost, &item.TargetPort, &item.PublicPort, &item.Domain, &item.TLSMode, &metadataJSON); err != nil {
		return types.TunnelSpec{}, ErrNotFound
	}
	item.NodeID = nodeID
	metadata, err := unmarshalMap(metadataJSON)
	if err != nil {
		return types.TunnelSpec{}, err
	}
	item.Metadata = metadata
	if item.Metadata == nil {
		item.Metadata = map[string]string{}
	}
	if nodeID != "" {
		item.Metadata["nodeId"] = nodeID
	}
	return item, nil
}

func (s *PostgresStore) UpdateTunnel(ctx context.Context, spec types.TunnelSpec) (types.TunnelSpec, error) {
	tunnel := normalizeTunnel(spec)
	if tunnel.ID == "" {
		return types.TunnelSpec{}, ErrNotFound
	}
	if tunnel.Metadata == nil {
		tunnel.Metadata = map[string]string{}
	}
	tunnel.Metadata["nodeId"] = tunnel.NodeID
	if _, err := s.GetTunnel(ctx, tunnel.ID); err != nil {
		return types.TunnelSpec{}, err
	}
	conflict, err := s.hasPublicPortConflict(ctx, tunnel.ID, tunnel.Type, tunnel.Status, tunnel.PublicPort)
	if err != nil {
		return types.TunnelSpec{}, err
	}
	if conflict {
		return types.TunnelSpec{}, ErrConflict
	}
	meta, err := marshalMap(tunnel.Metadata)
	if err != nil {
		return types.TunnelSpec{}, err
	}
	commandTag, err := s.pool.Exec(ctx, `
		update tunnels
		set node_id = $2,
			name = $3,
			type = $4,
			transport_policy = $5,
			status = $6,
			target_host = $7,
			target_port = $8,
			public_port = $9,
			domain = $10,
			tls_mode = $11,
			metadata = $12,
			updated_at = now()
		where id = $1
	`, tunnel.ID, tunnel.NodeID, tunnel.Name, tunnel.Type, tunnel.TransportPolicy, tunnel.Status, tunnel.TargetHost, tunnel.TargetPort, tunnel.PublicPort, emptyStringToNil(tunnel.Domain), emptyStringToNil(tunnel.TLSMode), meta)
	if err != nil {
		return types.TunnelSpec{}, err
	}
	if commandTag.RowsAffected() == 0 {
		return types.TunnelSpec{}, ErrNotFound
	}
	return tunnel, nil
}

func (s *PostgresStore) DeleteTunnel(ctx context.Context, id string) error {
	commandTag, err := s.pool.Exec(ctx, `delete from tunnels where id = $1`, id)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) ListTunnels(ctx context.Context, filter TunnelFilter) ([]types.TunnelSpec, error) {
	rows, err := s.pool.Query(ctx, `
		select id, coalesce(node_id, ''), name, type, transport_policy, status, target_host, target_port, coalesce(public_port, 0), coalesce(domain, ''), coalesce(tls_mode, ''), metadata
		from tunnels
		where ($1 = '' or node_id = $1)
		  and ($2 = '' or type = $2)
		  and ($3 = '' or status = $3)
		order by id
	`, filter.NodeID, filter.Type, filter.Status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]types.TunnelSpec, 0)
	for rows.Next() {
		var item types.TunnelSpec
		var nodeID string
		var metadataJSON []byte
		if err := rows.Scan(&item.ID, &nodeID, &item.Name, &item.Type, &item.TransportPolicy, &item.Status, &item.TargetHost, &item.TargetPort, &item.PublicPort, &item.Domain, &item.TLSMode, &metadataJSON); err != nil {
			return nil, err
		}
		item.NodeID = nodeID
		item.Metadata, err = unmarshalMap(metadataJSON)
		if err != nil {
			return nil, err
		}
		if item.Metadata == nil {
			item.Metadata = map[string]string{}
		}
		if nodeID != "" {
			item.Metadata["nodeId"] = nodeID
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) Counts(ctx context.Context) (Counts, error) {
	counts := Counts{}
	if err := s.pool.QueryRow(ctx, `
		select count(*), count(*) filter (where status = 'online') from nodes
	`).Scan(&counts.RegisteredNodes, &counts.OnlineNodes); err != nil {
		return Counts{}, err
	}
	if err := s.pool.QueryRow(ctx, `select count(*) from tunnels`).Scan(&counts.ConfiguredTunnels); err != nil {
		return Counts{}, err
	}
	return counts, nil
}

func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}

func marshalMap(input map[string]string) ([]byte, error) {
	if input == nil {
		input = map[string]string{}
	}
	return json.Marshal(input)
}

func unmarshalMap(input []byte) (map[string]string, error) {
	if len(input) == 0 {
		return map[string]string{}, nil
	}
	var out map[string]string
	if err := json.Unmarshal(input, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

func unmarshalCapabilities(input []byte) (types.NodeCapabilities, error) {
	if len(input) == 0 {
		return types.NodeCapabilities{}, nil
	}
	var out types.NodeCapabilities
	if err := json.Unmarshal(input, &out); err != nil {
		return types.NodeCapabilities{}, err
	}
	return out, nil
}

func emptyStringToNil(input string) any {
	if input == "" {
		return nil
	}
	return input
}

func (s *PostgresStore) hasPublicPortConflict(ctx context.Context, excludeID, tunnelType, status string, publicPort int) (bool, error) {
	if tunnelType != "tcp" || status != "active" || publicPort == 0 {
		return false, nil
	}
	var count int
	err := s.pool.QueryRow(ctx, `
		select count(*)
		from tunnels
		where id <> $1
		  and type = 'tcp'
		  and status = 'active'
		  and public_port = $2
	`, excludeID, publicPort).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *PostgresStore) BootstrapStatus(ctx context.Context) (bool, error) {
	var count int
	if err := s.pool.QueryRow(ctx, `select count(*) from users`).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

func (s *PostgresStore) BootstrapAdmin(ctx context.Context, params CreateUserParams) (types.UserSummary, error) {
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

func (s *PostgresStore) AuthenticateUser(ctx context.Context, params AuthenticateUserParams) (types.UserSummary, error) {
	email := normalizeEmail(params.Email)
	row := s.pool.QueryRow(ctx, `
		select id, email, display_name, role, password_hash, created_at, updated_at
		from users
		where lower(email) = $1
	`, email)
	var summary types.UserSummary
	var passwordHash string
	if err := row.Scan(&summary.ID, &summary.Email, &summary.DisplayName, &summary.Role, &passwordHash, &summary.CreatedAt, &summary.UpdatedAt); err != nil {
		return types.UserSummary{}, ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(params.Password)) != nil {
		return types.UserSummary{}, ErrUnauthorized
	}
	return summary, nil
}

func (s *PostgresStore) ListUsers(ctx context.Context) ([]types.UserSummary, error) {
	rows, err := s.pool.Query(ctx, `
		select id, email, display_name, role, created_at, updated_at
		from users
		order by email
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]types.UserSummary, 0)
	for rows.Next() {
		var item types.UserSummary
		if err := rows.Scan(&item.ID, &item.Email, &item.DisplayName, &item.Role, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetUser(ctx context.Context, id string) (types.UserSummary, error) {
	row := s.pool.QueryRow(ctx, `
		select id, email, display_name, role, created_at, updated_at
		from users
		where id = $1
	`, id)
	var item types.UserSummary
	if err := row.Scan(&item.ID, &item.Email, &item.DisplayName, &item.Role, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return types.UserSummary{}, ErrNotFound
	}
	return item, nil
}

func (s *PostgresStore) CreateUser(ctx context.Context, params CreateUserParams) (types.UserSummary, error) {
	email := normalizeEmail(params.Email)
	if email == "" || strings.TrimSpace(params.DisplayName) == "" || params.Password == "" {
		return types.UserSummary{}, ErrConflict
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
	if err != nil {
		return types.UserSummary{}, err
	}
	id := fmt.Sprintf("user-%d", time.Now().UnixNano())
	role := normalizeUserRole(params.Role)
	row := s.pool.QueryRow(ctx, `
		insert into users (id, email, display_name, password_hash, role)
		values ($1, $2, $3, $4, $5)
		returning id, email, display_name, role, created_at, updated_at
	`, id, email, strings.TrimSpace(params.DisplayName), string(hash), role)
	var summary types.UserSummary
	if err := row.Scan(&summary.ID, &summary.Email, &summary.DisplayName, &summary.Role, &summary.CreatedAt, &summary.UpdatedAt); err != nil {
		return types.UserSummary{}, ErrConflict
	}
	return summary, nil
}

func (s *PostgresStore) UpdateUser(ctx context.Context, params UpdateUserParams) (types.UserSummary, error) {
	current, err := s.GetUser(ctx, params.ID)
	if err != nil {
		return types.UserSummary{}, err
	}
	displayName := current.DisplayName
	if strings.TrimSpace(params.DisplayName) != "" {
		displayName = strings.TrimSpace(params.DisplayName)
	}
	role := current.Role
	if params.Role != "" {
		role = normalizeUserRole(params.Role)
	}
	passwordHashSQL := "password_hash"
	passwordArg := any(nil)
	if params.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
		if err != nil {
			return types.UserSummary{}, err
		}
		passwordHashSQL = "$4"
		passwordArg = string(hash)
		_, err = s.pool.Exec(ctx, `
			update users
			set display_name = $2, role = $3, password_hash = $4, updated_at = now()
			where id = $1
		`, params.ID, displayName, role, passwordArg)
		if err != nil {
			return types.UserSummary{}, err
		}
		return s.GetUser(ctx, params.ID)
	}
	_ = passwordHashSQL
	_ = passwordArg
	commandTag, err := s.pool.Exec(ctx, `
		update users
		set display_name = $2, role = $3, updated_at = now()
		where id = $1
	`, params.ID, displayName, role)
	if err != nil {
		return types.UserSummary{}, err
	}
	if commandTag.RowsAffected() == 0 {
		return types.UserSummary{}, ErrNotFound
	}
	return s.GetUser(ctx, params.ID)
}

func (s *PostgresStore) DeleteUser(ctx context.Context, id string) error {
	if err := s.DeleteUserSessions(ctx, id); err != nil {
		return err
	}
	commandTag, err := s.pool.Exec(ctx, `delete from users where id = $1`, id)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) CreateWebSession(ctx context.Context, params CreateSessionParams) (WebSession, error) {
	sessionID := params.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	now := time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		insert into web_sessions (id, user_id, refresh_token_hash, refresh_expires_at, remote_addr, user_agent, created_at, last_seen_at, last_refreshed_at)
		values ($1, $2, $3, $4, $5, $6, $7, $7, $7)
	`, sessionID, params.UserID, params.RefreshTokenHash, params.RefreshExpiresAt, params.RemoteAddr, params.UserAgent, now)
	if err != nil {
		return WebSession{}, err
	}
	return WebSession{SessionID: sessionID, UserID: params.UserID, RefreshTokenHash: params.RefreshTokenHash, RefreshExpiresAt: params.RefreshExpiresAt, LastRefreshedAt: now, LastSeenAt: now, RemoteAddr: params.RemoteAddr, UserAgent: params.UserAgent, CreatedAt: now}, nil
}

func (s *PostgresStore) GetWebSessionByID(ctx context.Context, sessionID string) (WebSession, error) {
	row := s.pool.QueryRow(ctx, `
		select id, user_id, refresh_token_hash, refresh_expires_at, remote_addr, user_agent, created_at, last_seen_at, last_refreshed_at, revoked_at
		from web_sessions
		where id = $1
	`, sessionID)
	var session WebSession
	if err := row.Scan(&session.SessionID, &session.UserID, &session.RefreshTokenHash, &session.RefreshExpiresAt, &session.RemoteAddr, &session.UserAgent, &session.CreatedAt, &session.LastSeenAt, &session.LastRefreshedAt, &session.RevokedAt); err != nil {
		return WebSession{}, ErrNotFound
	}
	if session.RevokedAt != nil {
		return WebSession{}, ErrUnauthorized
	}
	return session, nil
}

func (s *PostgresStore) RotateWebSession(ctx context.Context, sessionID string, refreshTokenHash string, refreshExpiresAt time.Time, remoteAddr, userAgent string) (WebSession, error) {
	now := time.Now().UTC()
	commandTag, err := s.pool.Exec(ctx, `
		update web_sessions
		set refresh_token_hash = $2, refresh_expires_at = $3, remote_addr = $4, user_agent = $5, last_seen_at = $6, last_refreshed_at = $6
		where id = $1 and revoked_at is null
	`, sessionID, refreshTokenHash, refreshExpiresAt, remoteAddr, userAgent, now)
	if err != nil {
		return WebSession{}, err
	}
	if commandTag.RowsAffected() == 0 {
		return WebSession{}, ErrNotFound
	}
	return s.GetWebSessionByID(ctx, sessionID)
}

func (s *PostgresStore) DeleteWebSession(ctx context.Context, sessionID string) error {
	commandTag, err := s.pool.Exec(ctx, `
		update web_sessions
		set revoked_at = now()
		where id = $1 and revoked_at is null
	`, sessionID)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `
		update web_sessions
		set revoked_at = now()
		where user_id = $1 and revoked_at is null
	`, userID)
	return err
}

func (s *PostgresStore) WriteAuditLog(ctx context.Context, params AuditLogParams) (types.AuditLogEntry, error) {
	payload, err := marshalMap(params.Payload)
	if err != nil {
		return types.AuditLogEntry{}, err
	}
	row := s.pool.QueryRow(ctx, `
		insert into audit_logs (actor_type, actor_id, action, resource_type, resource_id, payload)
		values ($1, $2, $3, $4, $5, $6)
		returning id, actor_type, coalesce(actor_id, ''), action, resource_type, coalesce(resource_id, ''), payload, created_at
	`, params.ActorType, emptyStringToNil(params.ActorID), params.Action, params.ResourceType, emptyStringToNil(params.ResourceID), payload)
	var entry types.AuditLogEntry
	var payloadJSON []byte
	if err := row.Scan(&entry.ID, &entry.ActorType, &entry.ActorID, &entry.Action, &entry.ResourceType, &entry.ResourceID, &payloadJSON, &entry.CreatedAt); err != nil {
		return types.AuditLogEntry{}, err
	}
	entry.Payload, err = unmarshalMap(payloadJSON)
	if err != nil {
		return types.AuditLogEntry{}, err
	}
	return entry, nil
}

func (s *PostgresStore) ListAuditLogs(ctx context.Context, filter AuditLogFilter) ([]types.AuditLogEntry, int, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	var total int
	err := s.pool.QueryRow(ctx, `
		select count(*)
		from audit_logs
		where ($1 = '' or action = $1)
		  and ($2 = '' or actor_type = $2)
		  and ($3 = '' or coalesce(actor_id, '') = $3)
		  and ($4 = '' or resource_type = $4)
		  and ($5 = '' or coalesce(resource_id, '') = $5)
		  and ($6::timestamptz is null or created_at >= $6)
		  and ($7::timestamptz is null or created_at <= $7)
	`, filter.Action, filter.ActorType, filter.ActorID, filter.ResourceType, filter.ResourceID, filter.StartAt, filter.EndAt).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		select id, actor_type, coalesce(actor_id, ''), action, resource_type, coalesce(resource_id, ''), payload, created_at
		from audit_logs
		where ($1 = '' or action = $1)
		  and ($2 = '' or actor_type = $2)
		  and ($3 = '' or coalesce(actor_id, '') = $3)
		  and ($4 = '' or resource_type = $4)
		  and ($5 = '' or coalesce(resource_id, '') = $5)
		  and ($6::timestamptz is null or created_at >= $6)
		  and ($7::timestamptz is null or created_at <= $7)
		order by created_at desc, id desc
		limit $8 offset $9
	`, filter.Action, filter.ActorType, filter.ActorID, filter.ResourceType, filter.ResourceID, filter.StartAt, filter.EndAt, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]types.AuditLogEntry, 0)
	for rows.Next() {
		var item types.AuditLogEntry
		var payloadJSON []byte
		if err := rows.Scan(&item.ID, &item.ActorType, &item.ActorID, &item.Action, &item.ResourceType, &item.ResourceID, &payloadJSON, &item.CreatedAt); err != nil {
			return nil, 0, err
		}
		item.Payload, err = unmarshalMap(payloadJSON)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}
