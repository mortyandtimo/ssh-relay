create table if not exists users (
    id text primary key,
    email text not null unique,
    display_name text not null,
    password_hash text not null,
    role text not null default 'admin',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table if not exists nodes (
    id text primary key,
    name text not null,
    status text not null,
    agent_version text not null,
    capabilities jsonb not null default '{}'::jsonb,
    metadata jsonb not null default '{}'::jsonb,
    last_seen_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table if not exists node_tokens (
    id text primary key,
    node_id text not null references nodes(id) on delete cascade,
    token_hash text not null,
    last_used_at timestamptz,
    created_at timestamptz not null default now(),
    revoked_at timestamptz
);

create table if not exists node_sessions (
    id text primary key,
    node_id text not null references nodes(id) on delete cascade,
    status text not null,
    connected_at timestamptz not null default now(),
    disconnected_at timestamptz,
    metadata jsonb not null default '{}'::jsonb
);

create table if not exists tunnels (
    id text primary key,
    node_id text not null references nodes(id) on delete cascade,
    name text not null,
    type text not null,
    transport_policy text not null default 'relay_only',
    status text not null,
    target_host text not null,
    target_port integer not null,
    public_port integer,
    domain text,
    tls_mode text,
    metadata jsonb not null default '{}'::jsonb,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table if not exists tunnel_sessions (
    id text primary key,
    tunnel_id text not null references tunnels(id) on delete cascade,
    relay_service text not null,
    status text not null,
    opened_at timestamptz not null default now(),
    closed_at timestamptz,
    bytes_in bigint not null default 0,
    bytes_out bigint not null default 0,
    metadata jsonb not null default '{}'::jsonb
);

create table if not exists server_metrics (
    id bigserial primary key,
    service text not null,
    payload jsonb not null,
    observed_at timestamptz not null default now()
);

create table if not exists node_metrics (
    id bigserial primary key,
    node_id text not null references nodes(id) on delete cascade,
    payload jsonb not null,
    observed_at timestamptz not null default now()
);

create table if not exists audit_logs (
    id bigserial primary key,
    actor_type text not null,
    actor_id text,
    action text not null,
    resource_type text not null,
    resource_id text,
    payload jsonb not null default '{}'::jsonb,
    created_at timestamptz not null default now()
);


create table if not exists web_sessions (
    id text primary key,
    user_id text not null references users(id) on delete cascade,
    refresh_token_hash text not null,
    refresh_expires_at timestamptz not null,
    remote_addr text,
    user_agent text,
    created_at timestamptz not null default now(),
    last_seen_at timestamptz not null default now(),
    last_refreshed_at timestamptz not null default now(),
    revoked_at timestamptz
);

create index if not exists idx_web_sessions_user_id on web_sessions(user_id);

create table if not exists user_certificates (
    id text primary key,
    user_id text not null references users(id) on delete cascade,
    domain text not null,
    cert_pem text not null,
    key_pem text not null,
    expires_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create unique index if not exists idx_user_certs_user_domain on user_certificates(user_id, domain);
