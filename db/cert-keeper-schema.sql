-- CertKeeper (证书管家) independent schema
-- Uses ck_ prefix to avoid collision with cloud-relay tables in the same database

create table if not exists ck_users (
    id text primary key,
    email text not null unique,
    display_name text not null,
    password_hash text not null,
    role text not null default 'admin',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table if not exists ck_web_sessions (
    id text primary key,
    user_id text not null references ck_users(id) on delete cascade,
    refresh_token_hash text not null,
    refresh_expires_at timestamptz not null,
    remote_addr text,
    user_agent text,
    created_at timestamptz not null default now(),
    last_seen_at timestamptz not null default now(),
    last_refreshed_at timestamptz not null default now(),
    revoked_at timestamptz
);

create index if not exists idx_ck_sessions_user_id on ck_web_sessions(user_id);

create table if not exists ck_certificates (
    id text primary key,
    user_id text not null references ck_users(id) on delete cascade,
    domain text not null,
    cert_pem text not null,
    key_pem text not null,
    issuer text not null default '',
    auto_renew boolean not null default false,
    expires_at timestamptz,
    dns_verified_at timestamptz,
    last_renewed_at timestamptz,
    renew_error text,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

-- Domain is globally unique (one domain = one valid certificate)
create unique index if not exists idx_ck_certs_domain on ck_certificates(domain);
