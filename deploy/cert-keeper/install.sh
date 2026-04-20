#!/bin/bash
set -e

echo "=== CertKeeper API Deploy ==="

ENV_DIR=/etc/cloud-relay/cert-keeper
ENV_FILE="$ENV_DIR/cert-keeper.env"

mkdir -p "$ENV_DIR"

if [ -f "$ENV_FILE" ]; then
    set -a
    # shellcheck disable=SC1090
    . "$ENV_FILE"
    set +a
fi

: "${CERT_KEEPER_DATABASE_URL:?Set CERT_KEEPER_DATABASE_URL in the environment or $ENV_FILE before running this installer}"

if [ ! -f "$ENV_FILE" ]; then
    : "${CERT_KEEPER_ACME_EMAIL:?Set CERT_KEEPER_ACME_EMAIL before running this installer}"
    : "${CERT_KEEPER_PUBLIC_IP:?Set CERT_KEEPER_PUBLIC_IP before running this installer}"
    umask 077
    cat >"$ENV_FILE" <<EOF
CERT_KEEPER_DATABASE_URL=$CERT_KEEPER_DATABASE_URL
CERT_KEEPER_ACME_EMAIL=$CERT_KEEPER_ACME_EMAIL
CERT_KEEPER_PUBLIC_IP=$CERT_KEEPER_PUBLIC_IP
CERT_KEEPER_ACCESS_SECRET=${CERT_KEEPER_ACCESS_SECRET:-}
CERT_KEEPER_BOOTSTRAP_SECRET=${CERT_KEEPER_BOOTSTRAP_SECRET:-}
CERT_KEEPER_ALLOWED_ORIGINS=${CERT_KEEPER_ALLOWED_ORIGINS:-}
CERT_KEEPER_DOMAIN_BACKENDS=${CERT_KEEPER_DOMAIN_BACKENDS:-}
CERT_KEEPER_SKIP_DOMAINS=${CERT_KEEPER_SKIP_DOMAINS:-}
EOF
    chmod 600 "$ENV_FILE"
    echo "Wrote $ENV_FILE from current shell environment"
fi

# Build
cd /root/cloud-relay-platform/apps/cert-keeper-api
CGO_ENABLED=0 go build -o /opt/cert-keeper/cert-keeper-api ./cmd/cert-keeper-api/
echo "Built cert-keeper-api"

# DB schema
echo "Applying database schema..."
psql "$CERT_KEEPER_DATABASE_URL" -f /root/cloud-relay-platform/db/cert-keeper-schema.sql

# Nginx include
NGINX_CONF=/www/server/nginx/conf/nginx.conf
NGINX_BIN=/www/server/nginx/sbin/nginx
INCLUDE_LINE="include /etc/cloud-relay/cert-keeper/nginx/*.conf;"
if ! grep -Fq "$INCLUDE_LINE" "$NGINX_CONF" 2>/dev/null; then
    if grep -Fq 'include /www/server/panel/vhost/nginx/*.conf;' "$NGINX_CONF"; then
        sed -i '/include \/www\/server\/panel\/vhost\/nginx\/\*\.conf;/a\    include /etc/cloud-relay/cert-keeper/nginx/*.conf;' "$NGINX_CONF"
    elif grep -Fq 'include /etc/cloud-relay/nginx/*.conf;' "$NGINX_CONF"; then
        sed -i '/include \/etc\/cloud-relay\/nginx\/\*\.conf;/i\    include /etc/cloud-relay/cert-keeper/nginx/*.conf;' "$NGINX_CONF"
    else
        echo "Failed to find nginx include anchor in $NGINX_CONF" >&2
        exit 1
    fi
    echo "Added nginx include"
fi

# Directories
mkdir -p /etc/cloud-relay/cert-keeper/certs
mkdir -p /etc/cloud-relay/cert-keeper/nginx
mkdir -p /opt/cert-keeper

# Systemd
cp /root/cloud-relay-platform/deploy/cert-keeper/cert-keeper-api.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable cert-keeper-api
systemctl restart cert-keeper-api
echo "cert-keeper-api service started"

# Health check
sleep 2
curl -sf http://127.0.0.1:7720/healthz && echo " — health OK" || echo " — health check failed"

# Live nginx verification
$NGINX_BIN -t
if ! $NGINX_BIN -T 2>/dev/null | grep -Fq "$INCLUDE_LINE"; then
    echo "CertKeeper nginx include is not active in live nginx config" >&2
    exit 1
fi
echo "CertKeeper nginx include verified in live config"
