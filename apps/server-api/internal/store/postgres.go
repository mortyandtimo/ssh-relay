package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
	"github.com/jackc/pgx/v5/pgxpool"
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
	meta, err := marshalMap(req.Metadata)
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
		Metadata:      req.Metadata,
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

func (s *PostgresStore) ListNodes(ctx context.Context) ([]types.NodeSummary, error) {
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
		order by n.id
	`)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		item.Capabilities, err = unmarshalCapabilities(capabilitiesJSON)
		if err != nil {
			return nil, err
		}
		item.Metadata, err = unmarshalMap(metadataJSON)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
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
