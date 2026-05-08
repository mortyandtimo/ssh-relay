package nginx

import (
	"strings"
	"testing"
	"time"

	"github.com/25743/cloud-relay-platform/apps/cert-keeper-api/internal/store"
)

func TestBuildConfigAddsForwardedHeadersForProxyDomains(t *testing.T) {
	manager := NewManager("/tmp/nginx-conf", "/tmp/certs", "", nil, "")
	now := time.Unix(1, 0).UTC()
	config := manager.buildConfig([]store.Certificate{
		{
			ID:        "cert-1",
			Domain:    "img.020309.top",
			CreatedAt: now,
			UpdatedAt: now,
		},
	})

	required := []string{
		"proxy_set_header Host $host;",
		"proxy_set_header X-Forwarded-Host $host;",
		"proxy_set_header X-Forwarded-Proto https;",
		"proxy_set_header X-Forwarded-Port 443;",
		"proxy_set_header Forwarded \"for=$remote_addr;proto=https;host=$host\";",
		"proxy_http_version 1.1;",
		"proxy_set_header Upgrade $http_upgrade;",
		"proxy_set_header Connection \"upgrade\";",
		"proxy_pass http://127.0.0.1:9095;",
	}
	for _, snippet := range required {
		if !strings.Contains(config, snippet) {
			t.Fatalf("expected generated config to contain %q\nconfig:\n%s", snippet, config)
		}
	}
}
