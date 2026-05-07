-- SSH Relay (sshr) independent schema
-- Uses sshr_ prefix to avoid collision with cloud-relay and cert-keeper tables

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
