package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping pool: %w", err)
	}
	return pool, nil
}

func NewID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "m_" + hex.EncodeToString(b)
}

func NewForwardID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "f_" + hex.EncodeToString(b)
}

type Machine struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	GPUModel     string            `json:"gpuModel"`
	Status       string            `json:"status"`
	AgentVersion string            `json:"agentVersion"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	LastSeenAt   *time.Time        `json:"lastSeenAt"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

type Forward struct {
	ID         string            `json:"id"`
	MachineID  string            `json:"machineId"`
	Name       string            `json:"name"`
	TargetHost string            `json:"targetHost"`
	TargetPort int               `json:"targetPort"`
	PublicPort int               `json:"publicPort"`
	Status     string            `json:"status"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

type RegisterMachineRequest struct {
	Name            string            `json:"name"`
	GPUModel        string            `json:"gpuModel"`
	AgentVersion    string            `json:"agentVersion"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	NotifyEmails    []string          `json:"notifyEmails,omitempty"`
}

type HeartbeatRequest struct {
	MachineID   string            `json:"machineId"`
	AgentVersion string           `json:"agentVersion"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type CreateForwardRequest struct {
	MachineID  string `json:"machineId"`
	Name       string `json:"name"`
	TargetHost string `json:"targetHost"`
	TargetPort int    `json:"targetPort"`
	PublicPort int    `json:"publicPort"`
}

type HeartbeatEvent struct {
	ID         int64     `json:"id"`
	MachineID  string    `json:"machineId"`
	EventType  string    `json:"eventType"`
	Message    string    `json:"message"`
	ObservedAt time.Time `json:"observedAt"`
}

type Settings struct {
	NotifyEmails []string `json:"notifyEmails"`
}

type Store struct {
	pool *pgxpool.Pool
	mu   sync.RWMutex
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	schema := `
	create table if not exists sshr_machines (
	    id text primary key,
	    name text not null unique,
	    gpu_model text not null default '',
	    status text not null default 'online',
	    agent_version text not null default '0.1.0',
	    metadata jsonb not null default '{}'::jsonb,
	    last_seen_at timestamptz,
	    created_at timestamptz not null default now(),
	    updated_at timestamptz not null default now()
	);
	create table if not exists sshr_forwards (
	    id text primary key,
	    machine_id text not null references sshr_machines(id) on delete cascade,
	    name text not null,
	    target_host text not null default '127.0.0.1',
	    target_port integer not null default 22,
	    public_port integer not null unique,
	    status text not null default 'active',
	    metadata jsonb not null default '{}'::jsonb,
	    created_at timestamptz not null default now(),
	    updated_at timestamptz not null default now()
	);
	create table if not exists sshr_heartbeat_events (
	    id bigserial primary key,
	    machine_id text not null references sshr_machines(id) on delete cascade,
	    event_type text not null,
	    message text not null default '',
	    observed_at timestamptz not null default now()
	);
	create index if not exists idx_sshr_heartbeat_machine on sshr_heartbeat_events(machine_id, observed_at desc);
	create table if not exists sshr_settings (
	    key text primary key,
	    value text not null,
	    updated_at timestamptz not null default now()
	);
	`
	_, err := s.pool.Exec(ctx, schema)
	return err
}

// ─── Machine CRUD ───

func (s *Store) RegisterMachine(ctx context.Context, req RegisterMachineRequest) (Machine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check name uniqueness
	if strings.TrimSpace(req.Name) != "" {
		var existing string
		err := s.pool.QueryRow(ctx, `select id from sshr_machines where name = $1`, req.Name).Scan(&existing)
		if err == nil {
			return Machine{}, fmt.Errorf("machine name %q already exists", req.Name)
		}
	}

	m := Machine{
		ID:           NewID(),
		Name:         req.Name,
		GPUModel:     req.GPUModel,
		Status:       "online",
		AgentVersion: req.AgentVersion,
		Metadata:     req.Metadata,
	}
	now := time.Now().UTC()
	m.LastSeenAt = &now
	m.CreatedAt = now
	m.UpdatedAt = now

	if m.Metadata == nil {
		m.Metadata = map[string]string{}
	}
	if len(req.NotifyEmails) > 0 {
		m.Metadata["notifyEmails"] = strings.Join(req.NotifyEmails, ",")
	}

	metaJSON, _ := json.Marshal(m.Metadata)
	_, err := s.pool.Exec(ctx,
		`insert into sshr_machines (id, name, gpu_model, status, agent_version, metadata, last_seen_at, created_at, updated_at)
		 values ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		m.ID, m.Name, m.GPUModel, m.Status, m.AgentVersion, metaJSON, m.LastSeenAt, m.CreatedAt, m.UpdatedAt)
	if err != nil {
		return Machine{}, fmt.Errorf("insert machine: %w", err)
	}

	_, _ = s.pool.Exec(ctx,
		`insert into sshr_heartbeat_events (machine_id, event_type, message, observed_at) values ($1,'online','machine registered',$2)`,
		m.ID, now)

	return m, nil
}

func (s *Store) Heartbeat(ctx context.Context, req HeartbeatRequest) (Machine, error) {
	now := time.Now().UTC()

	s.mu.RLock()
	// Check if transitioning from offline to online
	var prevStatus string
	_ = s.pool.QueryRow(ctx, `select status from sshr_machines where id = $1`, req.MachineID).Scan(&prevStatus)
	s.mu.RUnlock()

	metaJSON, _ := json.Marshal(req.Metadata)
	tag, err := s.pool.Exec(ctx,
		`update sshr_machines set status='online', agent_version=$1, metadata=$2, last_seen_at=$3, updated_at=$4 where id=$5`,
		req.AgentVersion, metaJSON, now, now, req.MachineID)
	if err != nil {
		return Machine{}, fmt.Errorf("heartbeat update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Machine{}, fmt.Errorf("machine not found: %s", req.MachineID)
	}

	if prevStatus == "offline" {
		_, _ = s.pool.Exec(ctx,
			`insert into sshr_heartbeat_events (machine_id, event_type, message, observed_at) values ($1,'online','machine recovered',$2)`,
			req.MachineID, now)
	}

	var m Machine
	var metaBytes []byte
	err = s.pool.QueryRow(ctx,
		`select id, name, gpu_model, status, agent_version, metadata, last_seen_at, created_at, updated_at from sshr_machines where id=$1`,
		req.MachineID).Scan(&m.ID, &m.Name, &m.GPUModel, &m.Status, &m.AgentVersion, &metaBytes, &m.LastSeenAt, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return Machine{}, err
	}
	_ = json.Unmarshal(metaBytes, &m.Metadata)
	return m, nil
}

func (s *Store) GetMachine(ctx context.Context, id string) (Machine, error) {
	var m Machine
	var metaBytes []byte
	err := s.pool.QueryRow(ctx,
		`select id, name, gpu_model, status, agent_version, metadata, last_seen_at, created_at, updated_at from sshr_machines where id=$1`,
		id).Scan(&m.ID, &m.Name, &m.GPUModel, &m.Status, &m.AgentVersion, &metaBytes, &m.LastSeenAt, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return Machine{}, err
	}
	_ = json.Unmarshal(metaBytes, &m.Metadata)
	return m, nil
}

func (s *Store) ListMachines(ctx context.Context, status string) ([]Machine, error) {
	var rows pgxRows
	var err error
	if status != "" {
		rows, err = s.pool.Query(ctx,
			`select id, name, gpu_model, status, agent_version, metadata, last_seen_at, created_at, updated_at from sshr_machines where status=$1 order by created_at desc`, status)
	} else {
		rows, err = s.pool.Query(ctx,
			`select id, name, gpu_model, status, agent_version, metadata, last_seen_at, created_at, updated_at from sshr_machines order by created_at desc`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var machines []Machine
	for rows.Next() {
		var m Machine
		var metaBytes []byte
		if err := rows.Scan(&m.ID, &m.Name, &m.GPUModel, &m.Status, &m.AgentVersion, &metaBytes, &m.LastSeenAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(metaBytes, &m.Metadata)
		machines = append(machines, m)
	}
	if machines == nil {
		machines = []Machine{}
	}
	return machines, nil
}

func (s *Store) UpdateMachineName(ctx context.Context, id, name string) (Machine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Check uniqueness
	var existing string
	err := s.pool.QueryRow(ctx, `select id from sshr_machines where name=$1 and id!=$2`, name, id).Scan(&existing)
	if err == nil {
		return Machine{}, fmt.Errorf("machine name %q already exists", name)
	}
	now := time.Now().UTC()
	_, err = s.pool.Exec(ctx, `update sshr_machines set name=$1, updated_at=$2 where id=$3`, name, now, id)
	if err != nil {
		return Machine{}, err
	}
	return s.GetMachine(ctx, id)
}

func (s *Store) DeleteMachine(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `delete from sshr_machines where id=$1`, id)
	return err
}

// ─── Forwards ───

func (s *Store) CreateForward(ctx context.Context, req CreateForwardRequest) (Forward, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check port uniqueness
	var existingFwd string
	err := s.pool.QueryRow(ctx, `select id from sshr_forwards where public_port=$1`, req.PublicPort).Scan(&existingFwd)
	if err == nil {
		return Forward{}, fmt.Errorf("public port %d is already in use", req.PublicPort)
	}

	f := Forward{
		ID:         NewForwardID(),
		MachineID:  req.MachineID,
		Name:       req.Name,
		TargetHost: req.TargetHost,
		TargetPort: req.TargetPort,
		PublicPort: req.PublicPort,
		Status:     "active",
	}
	now := time.Now().UTC()
	f.CreatedAt = now
	f.UpdatedAt = now

	_, err = s.pool.Exec(ctx,
		`insert into sshr_forwards (id, machine_id, name, target_host, target_port, public_port, status, created_at, updated_at)
		 values ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		f.ID, f.MachineID, f.Name, f.TargetHost, f.TargetPort, f.PublicPort, f.Status, f.CreatedAt, f.UpdatedAt)
	if err != nil {
		return Forward{}, fmt.Errorf("insert forward: %w", err)
	}
	return f, nil
}

func (s *Store) ListForwards(ctx context.Context, machineID string) ([]Forward, error) {
	var rows pgxRows
	var err error
	if machineID != "" {
		rows, err = s.pool.Query(ctx,
			`select id, machine_id, name, target_host, target_port, public_port, status, created_at, updated_at from sshr_forwards where machine_id=$1 order by public_port`, machineID)
	} else {
		rows, err = s.pool.Query(ctx,
			`select id, machine_id, name, target_host, target_port, public_port, status, created_at, updated_at from sshr_forwards order by public_port`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fwds []Forward
	for rows.Next() {
		var f Forward
		if err := rows.Scan(&f.ID, &f.MachineID, &f.Name, &f.TargetHost, &f.TargetPort, &f.PublicPort, &f.Status, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		fwds = append(fwds, f)
	}
	if fwds == nil {
		fwds = []Forward{}
	}
	return fwds, nil
}

func (s *Store) GetForward(ctx context.Context, id string) (Forward, error) {
	var f Forward
	err := s.pool.QueryRow(ctx,
		`select id, machine_id, name, target_host, target_port, public_port, status, created_at, updated_at from sshr_forwards where id=$1`,
		id).Scan(&f.ID, &f.MachineID, &f.Name, &f.TargetHost, &f.TargetPort, &f.PublicPort, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return Forward{}, err
	}
	return f, nil
}

func (s *Store) DeleteForward(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `delete from sshr_forwards where id=$1`, id)
	return err
}

func (s *Store) UsedPorts(ctx context.Context) (map[int]bool, error) {
	rows, err := s.pool.Query(ctx, `select public_port from sshr_forwards`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	used := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		used[p] = true
	}
	return used, nil
}

// ─── Heartbeat Events ───

func (s *Store) AddHeartbeatEvent(ctx context.Context, machineID, eventType, message string) error {
	_, err := s.pool.Exec(ctx,
		`insert into sshr_heartbeat_events (machine_id, event_type, message, observed_at) values ($1,$2,$3,$4)`,
		machineID, eventType, message, time.Now().UTC())
	return err
}

func (s *Store) ListHeartbeatEvents(ctx context.Context, machineID string, limit int) ([]HeartbeatEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx,
		`select id, machine_id, event_type, message, observed_at from sshr_heartbeat_events where machine_id=$1 order by observed_at desc limit $2`,
		machineID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []HeartbeatEvent
	for rows.Next() {
		var e HeartbeatEvent
		if err := rows.Scan(&e.ID, &e.MachineID, &e.EventType, &e.Message, &e.ObservedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if events == nil {
		events = []HeartbeatEvent{}
	}
	return events, nil
}

// ─── Settings ───

func (s *Store) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `select key, value from sshr_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		settings[k] = v
	}
	return settings, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx,
		`insert into sshr_settings (key, value, updated_at) values ($1,$2,$3)
		 on conflict (key) do update set value=$2, updated_at=$3`,
		key, value, time.Now().UTC())
	return err
}

// ─── Offline detection ───

func (s *Store) MarkStaleMachinesOffline(ctx context.Context, threshold time.Duration) ([]Machine, error) {
	cutoff := time.Now().UTC().Add(-threshold)

	// Find machines that were online but haven't been seen
	rows, err := s.pool.Query(ctx,
		`select id, name, gpu_model, status, agent_version, metadata, last_seen_at, created_at, updated_at
		 from sshr_machines where status='online' and (last_seen_at is null or last_seen_at < $1)`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var gone []Machine
	for rows.Next() {
		var m Machine
		var metaBytes []byte
		if err := rows.Scan(&m.ID, &m.Name, &m.GPUModel, &m.Status, &m.AgentVersion, &metaBytes, &m.LastSeenAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(metaBytes, &m.Metadata)
		gone = append(gone, m)
	}
	rows.Close()

	if len(gone) == 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var reallyGone []Machine
	for _, m := range gone {
		tag, err := s.pool.Exec(ctx,
			`update sshr_machines set status='offline', updated_at=$1 where id=$2 and status='online'`,
			time.Now().UTC(), m.ID)
		if err != nil || tag.RowsAffected() == 0 {
			continue
		}
		_, _ = s.pool.Exec(ctx,
			`insert into sshr_heartbeat_events (machine_id, event_type, message, observed_at) values ($1,'offline','heartbeat lost',$2)`,
			m.ID, time.Now().UTC())
		reallyGone = append(reallyGone, m)
	}
	return reallyGone, nil
}

type pgxRows interface {
	Close()
	Next() bool
	Scan(dest ...interface{}) error
}
