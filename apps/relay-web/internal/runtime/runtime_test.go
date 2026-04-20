package runtime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

func TestCloneHTTPRequestForRelayAddsForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://img.020309.top/admin/images?page=2", nil)
	req.RemoteAddr = "10.20.30.40:54321"
	req.Host = "img.020309.top"

	route := types.TunnelSpec{TargetHost: "127.0.0.1", TargetPort: 8181}
	upstream := cloneHTTPRequestForRelay(req, route)

	if got := upstream.Host; got != "127.0.0.1:8181" {
		t.Fatalf("expected upstream host to target app, got %q", got)
	}
	if got := upstream.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Fatalf("expected forwarded proto https, got %q", got)
	}
	if got := upstream.Header.Get("X-Forwarded-Host"); got != "img.020309.top" {
		t.Fatalf("expected forwarded host img.020309.top, got %q", got)
	}
	if got := upstream.Header.Get("X-Forwarded-Port"); got != "443" {
		t.Fatalf("expected forwarded port 443, got %q", got)
	}
	if got := upstream.Header.Get("X-Forwarded-For"); got != "10.20.30.40" {
		t.Fatalf("expected forwarded for 10.20.30.40, got %q", got)
	}
	if got := upstream.Header.Get("Forwarded"); !strings.Contains(got, "proto=\"https\"") || !strings.Contains(got, "host=\"img.020309.top\"") {
		t.Fatalf("expected RFC forwarded header, got %q", got)
	}
}

func TestCloneHTTPRequestForRelayAppendsOnlyNewPeerToForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://img.020309.top/admin/images?page=2", nil)
	req.RemoteAddr = "10.20.30.40:54321"
	req.Host = "img.020309.top"
	req.Header.Set("X-Forwarded-For", "198.51.100.10, 203.0.113.8")

	route := types.TunnelSpec{TargetHost: "127.0.0.1", TargetPort: 8181}
	upstream := cloneHTTPRequestForRelay(req, route)

	if got := upstream.Header.Get("X-Forwarded-For"); got != "198.51.100.10, 203.0.113.8, 10.20.30.40" {
		t.Fatalf("expected forwarded chain with appended peer ip, got %q", got)
	}
}

func TestCloneHTTPRequestForRelayDoesNotDuplicateExistingPeerInForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://img.020309.top/admin/images?page=2", nil)
	req.RemoteAddr = "10.20.30.40:54321"
	req.Host = "img.020309.top"
	req.Header.Set("X-Forwarded-For", "198.51.100.10, 10.20.30.40")

	route := types.TunnelSpec{TargetHost: "127.0.0.1", TargetPort: 8181}
	upstream := cloneHTTPRequestForRelay(req, route)

	if got := upstream.Header.Get("X-Forwarded-For"); got != "198.51.100.10, 10.20.30.40" {
		t.Fatalf("expected forwarded chain without duplicate peer ip, got %q", got)
	}
}

func TestCopyHTTPResponseRewritesLocationAndRefreshOnly(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://img.020309.top/admin/settings", nil)
	req.Host = "img.020309.top"
	route := types.TunnelSpec{TargetHost: "127.0.0.1", TargetPort: 8181}
	resp := &http.Response{
		StatusCode: http.StatusFound,
		Header: http.Header{
			"Location":   {"http://127.0.0.1:8181/login"},
			"Refresh":    {"0; url=http://127.0.0.1:8181/login"},
			"Set-Cookie": {"laravel_session=test; Path=/; HttpOnly; Secure"},
		},
		Body: io.NopCloser(strings.NewReader("")),
	}

	recorder := httptest.NewRecorder()
	report := copyHTTPResponse(recorder, req, route, resp)
	result := recorder.Result()
	defer result.Body.Close()

	if result.StatusCode != http.StatusFound {
		t.Fatalf("expected status 302, got %d", result.StatusCode)
	}
	if got := result.Header.Get("Location"); got != "https://img.020309.top/login" {
		t.Fatalf("expected rewritten location, got %q", got)
	}
	if got := result.Header.Get("Refresh"); got != "0; url=https://img.020309.top/login" {
		t.Fatalf("expected rewritten refresh, got %q", got)
	}
	if got := result.Header.Get("Set-Cookie"); got != "laravel_session=test; Path=/; HttpOnly; Secure" {
		t.Fatalf("expected relay-web to preserve set-cookie, got %q", got)
	}
	if report.LocationOriginal != "http://127.0.0.1:8181/login" || report.LocationRewritten != "https://img.020309.top/login" {
		t.Fatalf("unexpected location rewrite report: %#v", report)
	}
	if report.RefreshOriginal != "0; url=http://127.0.0.1:8181/login" || report.RefreshRewritten != "0; url=https://img.020309.top/login" {
		t.Fatalf("unexpected refresh rewrite report: %#v", report)
	}
}

func TestStandbyPoolEnqueueUsesMaxCapacity(t *testing.T) {
	pool := newStandbyPool("node:tunnel", 2, 4)
	base := time.Unix(1_700_000_000, 0).UTC()

	for i := 0; i < 4; i++ {
		poolSize, evicted, err := pool.enqueue(standbyConn{registeredAt: base.Add(time.Duration(i) * time.Second)})
		if err != nil {
			t.Fatalf("enqueue %d failed: %v", i, err)
		}
		if len(evicted) != 0 {
			t.Fatalf("enqueue %d unexpectedly evicted %d items before max capacity", i, len(evicted))
		}
		if want := i + 1; poolSize != want {
			t.Fatalf("enqueue %d pool size = %d, want %d", i, poolSize, want)
		}
	}

	poolSize, evicted, err := pool.enqueue(standbyConn{registeredAt: base.Add(5 * time.Second)})
	if err != nil {
		t.Fatalf("enqueue overflow failed: %v", err)
	}
	if poolSize != 4 {
		t.Fatalf("overflow pool size = %d, want 4", poolSize)
	}
	if len(evicted) != 1 {
		t.Fatalf("overflow evicted %d items, want 1", len(evicted))
	}
	if !evicted[0].registeredAt.Equal(base) {
		t.Fatalf("overflow evicted wrong item: got %s want %s", evicted[0].registeredAt, base)
	}
}
