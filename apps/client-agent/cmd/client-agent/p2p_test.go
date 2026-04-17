package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadP2PTelemetryConfigAndCollectMetrics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}

	scriptPath := writeFakeEasyTierCLI(t)

	t.Setenv("CLIENT_P2P_ASSIST", "true")
	t.Setenv("CLIENT_P2P_CLI", scriptPath)
	t.Setenv("CLIENT_P2P_RPC_PORTAL", "127.0.0.1:29888")
	t.Setenv("CLIENT_P2P_TIMEOUT_SEC", "2")

	cfg := loadP2PTelemetryConfig()
	if !cfg.enabled {
		t.Fatal("expected p2p telemetry to be enabled")
	}
	if cfg.rpcPortal != "127.0.0.1:29888" {
		t.Fatalf("expected rpc portal 127.0.0.1:29888, got %q", cfg.rpcPortal)
	}

	metrics := collectP2PMetrics(cfg)
	if metrics[p2pMetricRunningKey] != "true" {
		t.Fatalf("expected p2p running true, got %q", metrics[p2pMetricRunningKey])
	}
	if metrics[p2pMetricIPv4Key] != "10.126.0.20" {
		t.Fatalf("expected p2p ipv4 10.126.0.20, got %q", metrics[p2pMetricIPv4Key])
	}
	if metrics[p2pMetricPeerCountKey] != "2" {
		t.Fatalf("expected peer count 2 after filtering Local peer, got %q", metrics[p2pMetricPeerCountKey])
	}
	if metrics[p2pMetricInstanceIDKey] != "inst-demo" {
		t.Fatalf("expected instance id inst-demo, got %q", metrics[p2pMetricInstanceIDKey])
	}
}

func TestRegisterAndHeartbeatExposeP2PAssistAndMetrics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is unix-only")
	}

	var registerPayload any
	var heartbeatPayload any
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/agent/register":
			if err := json.NewDecoder(r.Body).Decode(&registerPayload); err != nil {
				return nil, err
			}
			body, err := json.Marshal(map[string]any{
				"nodeId":                  "node-p2p-a",
				"registeredAt":            "2026-04-18T00:00:00Z",
				"recommendedHeartbeatSec": 30,
			})
			if err != nil {
				return nil, err
			}
			return jsonHTTPResponse(http.StatusOK, body), nil
		case "/agent/heartbeat":
			if err := json.NewDecoder(r.Body).Decode(&heartbeatPayload); err != nil {
				return nil, err
			}
			return jsonHTTPResponse(http.StatusOK, []byte(`{}`)), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, []byte(`{"error":"not found"}`)), nil
		}
	})}

	cfg := p2pTelemetryConfig{
		enabled:      true,
		cliPath:      writeFakeEasyTierCLI(t),
		rpcPortal:    "127.0.0.1:15888",
		queryTimeout: 2 * time.Second,
	}
	nodeID, err := register(client, "http://cloud-relay.test", "", "service-node-a", "managed", "cloud-relay-client-agent@svc.service", "svc", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if nodeID != "node-p2p-a" {
		t.Fatalf("expected registered node id node-p2p-a, got %q", nodeID)
	}

	payloadBytes, err := json.Marshal(registerPayload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(payloadBytes, []byte(`"p2pAssist":true`)) {
		t.Fatalf("expected register payload to advertise p2pAssist, got %s", payloadBytes)
	}
	if !bytes.Contains(payloadBytes, []byte(`"p2pRpcPortal":"127.0.0.1:15888"`)) {
		t.Fatalf("expected register payload to include p2pRpcPortal, got %s", payloadBytes)
	}

	if err := heartbeat(client, "http://cloud-relay.test", nodeID, 1, map[string]string{"traffic:down_total": "100"}, cfg); err != nil {
		t.Fatal(err)
	}

	heartbeatBytes, err := json.Marshal(heartbeatPayload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(heartbeatBytes, []byte(`"p2p:ipv4":"10.126.0.20"`)) {
		t.Fatalf("expected heartbeat payload to include p2p ipv4 metric, got %s", heartbeatBytes)
	}
	if !bytes.Contains(heartbeatBytes, []byte(`"traffic:down_total":"100"`)) {
		t.Fatalf("expected heartbeat payload to preserve probe metrics, got %s", heartbeatBytes)
	}
}

func writeFakeEasyTierCLI(t *testing.T) string {
	t.Helper()
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "easytier-cli")
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *"node info"* ]]; then
  printf '%s\n' '{"hostname":"service-node-a","ipv4_addr":"10.126.0.20","inst_id":"inst-demo"}'
  exit 0
fi
if [[ "$*" == *"peer list"* ]]; then
  printf '%s\n' '[{"cost":"Local"},{"cost":"5"},{"cost":"9"}]'
  exit 0
fi
printf '%s\n' '{"error":"unexpected args"}' >&2
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return scriptPath
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func jsonHTTPResponse(statusCode int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     strings.TrimSpace(http.StatusText(statusCode)),
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}
}
