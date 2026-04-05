package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/25743/cloud-relay-platform/apps/server-api/internal/store"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

func TestRegisterHeartbeatTunnelAndMetrics(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, err := json.Marshal(types.NodeRegisterRequest{
		NodeName:     "edge-a",
		AgentVersion: "0.1.0",
		Capabilities: types.NodeCapabilities{TCPRelay: true, HTTPRelay: true, HTTPSRelay: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register status 200, got %d", registerRes.Code)
	}

	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	heartbeatBody, err := json.Marshal(types.NodeHeartbeatRequest{
		NodeID:        registerOut.NodeID,
		ObservedAt:    time.Now().UTC(),
		ActiveTunnels: 0,
		Metrics: map[string]string{
			"cpu": "5",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	heartbeatReq := httptest.NewRequest(http.MethodPost, "/agent/heartbeat", bytes.NewReader(heartbeatBody))
	heartbeatRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusOK {
		t.Fatalf("expected heartbeat status 200, got %d", heartbeatRes.Code)
	}

	tunnelBody, err := json.Marshal(map[string]any{
		"nodeId":     registerOut.NodeID,
		"name":       "ssh-edge-a",
		"type":       "tcp",
		"targetHost": "127.0.0.1",
		"targetPort": 22,
		"publicPort": 20022,
		"status":     "active",
	})
	if err != nil {
		t.Fatal(err)
	}

	tunnelReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(tunnelBody))
	applyCookies(tunnelReq, adminCookies)
	tunnelRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(tunnelRes, tunnelReq)
	if tunnelRes.Code != http.StatusCreated {
		t.Fatalf("expected tunnel status 201, got %d", tunnelRes.Code)
	}

	nodesReq := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	applyCookies(nodesReq, adminCookies)
	nodesRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(nodesRes, nodesReq)
	if nodesRes.Code != http.StatusOK {
		t.Fatalf("expected nodes status 200, got %d", nodesRes.Code)
	}

	var nodesOut struct {
		Items []types.NodeSummary `json:"items"`
	}
	if err := json.NewDecoder(nodesRes.Body).Decode(&nodesOut); err != nil {
		t.Fatal(err)
	}
	if len(nodesOut.Items) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodesOut.Items))
	}
	if nodesOut.Items[0].NodeID != registerOut.NodeID {
		t.Fatalf("expected node id %s, got %s", registerOut.NodeID, nodesOut.Items[0].NodeID)
	}

	routesReq := httptest.NewRequest(http.MethodGet, "/internal/routes/tcp", nil)
	routesRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(routesRes, routesReq)
	if routesRes.Code != http.StatusOK {
		t.Fatalf("expected routes status 200, got %d", routesRes.Code)
	}

	var routesOut struct {
		Items []types.TunnelSpec `json:"items"`
	}
	if err := json.NewDecoder(routesRes.Body).Decode(&routesOut); err != nil {
		t.Fatal(err)
	}
	if len(routesOut.Items) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routesOut.Items))
	}
	if routesOut.Items[0].PublicPort != 20022 {
		t.Fatalf("expected public port 20022, got %d", routesOut.Items[0].PublicPort)
	}

	agentRoutesReq := httptest.NewRequest(http.MethodGet, "/agent/tunnels?nodeId="+registerOut.NodeID, nil)
	agentRoutesRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(agentRoutesRes, agentRoutesReq)
	if agentRoutesRes.Code != http.StatusOK {
		t.Fatalf("expected agent routes status 200, got %d", agentRoutesRes.Code)
	}

	var agentRoutesOut struct {
		Items []types.TunnelSpec `json:"items"`
	}
	if err := json.NewDecoder(agentRoutesRes.Body).Decode(&agentRoutesOut); err != nil {
		t.Fatal(err)
	}
	if len(agentRoutesOut.Items) != 1 {
		t.Fatalf("expected 1 agent route, got %d", len(agentRoutesOut.Items))
	}
	if agentRoutesOut.Items[0].NodeID != registerOut.NodeID {
		t.Fatalf("expected node id %s, got %s", registerOut.NodeID, agentRoutesOut.Items[0].NodeID)
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/api/server/metrics", nil)
	applyCookies(metricsReq, adminCookies)
	metricsRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(metricsRes, metricsReq)
	if metricsRes.Code != http.StatusOK {
		t.Fatalf("expected metrics status 200, got %d", metricsRes.Code)
	}

	var metricsOut types.ServerMetrics
	if err := json.NewDecoder(metricsRes.Body).Decode(&metricsOut); err != nil {
		t.Fatal(err)
	}
	if metricsOut.RegisteredNodes != 1 {
		t.Fatalf("expected 1 registered node, got %d", metricsOut.RegisteredNodes)
	}
	if metricsOut.ConfiguredTunnels != 1 {
		t.Fatalf("expected 1 configured tunnel, got %d", metricsOut.ConfiguredTunnels)
	}
}

func TestTunnelCRUDAndConflictHandling(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, err := json.Marshal(types.NodeRegisterRequest{
		NodeName:     "edge-a",
		AgentVersion: "0.1.0",
		Capabilities: types.NodeCapabilities{TCPRelay: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register status 200, got %d", registerRes.Code)
	}
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	createBody, err := json.Marshal(map[string]any{
		"id":         "tunnel-a",
		"nodeId":     registerOut.NodeID,
		"name":       "svc-a",
		"type":       "tcp",
		"targetHost": "127.0.0.1",
		"targetPort": 16354,
		"publicPort": 10086,
		"status":     "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(createReq, adminCookies)
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d", createRes.Code)
	}

	conflictBody, err := json.Marshal(map[string]any{
		"id":         "tunnel-b",
		"nodeId":     registerOut.NodeID,
		"name":       "svc-b",
		"type":       "tcp",
		"targetHost": "127.0.0.1",
		"targetPort": 18081,
		"publicPort": 10086,
		"status":     "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	conflictReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(conflictBody))
	applyCookies(conflictReq, adminCookies)
	conflictRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(conflictRes, conflictReq)
	if conflictRes.Code != http.StatusConflict {
		t.Fatalf("expected conflict status 409, got %d", conflictRes.Code)
	}

	updateBody, err := json.Marshal(map[string]any{
		"nodeId":     registerOut.NodeID,
		"name":       "svc-a-paused",
		"type":       "tcp",
		"targetHost": "127.0.0.1",
		"targetPort": 16354,
		"publicPort": 10086,
		"status":     "paused",
	})
	if err != nil {
		t.Fatal(err)
	}
	updateReq := httptest.NewRequest(http.MethodPut, "/api/tunnels/tunnel-a", bytes.NewReader(updateBody))
	applyCookies(updateReq, adminCookies)
	updateRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d", updateRes.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/tunnels/tunnel-a", nil)
	applyCookies(getReq, adminCookies)
	getRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected get status 200, got %d", getRes.Code)
	}
	var tunnelOut types.TunnelSpec
	if err := json.NewDecoder(getRes.Body).Decode(&tunnelOut); err != nil {
		t.Fatal(err)
	}
	if tunnelOut.Status != "paused" {
		t.Fatalf("expected paused tunnel, got %s", tunnelOut.Status)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/tunnels/tunnel-a", nil)
	applyCookies(deleteReq, adminCookies)
	deleteRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("expected delete status 200, got %d", deleteRes.Code)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/api/tunnels/tunnel-a", nil)
	applyCookies(missingReq, adminCookies)
	missingRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusNotFound {
		t.Fatalf("expected missing status 404, got %d", missingRes.Code)
	}
}

func TestTunnelProbeSupportsHTTPAndHTTPS(t *testing.T) {
	httpUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("http-ok"))
	}))
	defer httpUpstream.Close()
	httpsUpstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer httpsUpstream.Close()

	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	server.httpClient = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: httpsUpstream.Client().Transport.(*http.Transport).TLSClientConfig}}
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	httpURL, _ := url.Parse(httpUpstream.URL)
	httpsURL, _ := url.Parse(httpsUpstream.URL)
	httpPort, _ := strconv.Atoi(strings.Split(httpURL.Host, ":")[1])
	httpsPort, _ := strconv.Atoi(strings.Split(httpsURL.Host, ":")[1])

	httpTunnel := types.TunnelSpec{ID: "probe-http", NodeID: "node-a", Name: "probe-http", Type: "http", Status: "active", PublicPort: httpPort, ProbePath: "", TargetHost: "127.0.0.1", TargetPort: 80}
	httpsTunnel := types.TunnelSpec{ID: "probe-https", NodeID: "node-a", Name: "probe-https", Type: "https", Status: "active", Domain: httpsURL.Hostname(), TLSMode: "edge_terminate", ProbePath: "/admin/", PublicPort: httpsPort, TargetHost: "127.0.0.1", TargetPort: 443}
	if _, err := server.store.CreateTunnel(context.Background(), httpTunnel); err != nil {
		_ = err
	}
	if _, err := server.store.CreateTunnel(context.Background(), httpsTunnel); err != nil {
		_ = err
	}

	httpEntry, err := probeTunnelEntry(httpTunnel)
	if err != nil {
		t.Fatal(err)
	}
	httpsEntry, err := probeTunnelEntry(httpsTunnel)
	if err != nil {
		t.Fatal(err)
	}
	server.httpClient = &http.Client{Timeout: 5 * time.Second, Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == httpEntry {
			return httpUpstream.Client().Transport.RoundTrip(mustCloneRequest(req, httpUpstream.URL))
		}
		if req.URL.String() == httpsEntry {
			return httpsUpstream.Client().Transport.RoundTrip(mustCloneRequest(req, httpsUpstream.URL))
		}
		return nil, fmt.Errorf("unexpected probe target %s", req.URL.String())
	})}

	httpProbeReq := httptest.NewRequest(http.MethodPost, "/api/tunnels/probe-http/probe", nil)
	applyCookies(httpProbeReq, adminCookies)
	httpProbeRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(httpProbeRes, httpProbeReq)
	if httpProbeRes.Code != http.StatusOK {
		t.Fatalf("expected http probe 200, got %d", httpProbeRes.Code)
	}
	var httpOut types.TunnelProbeResult
	if err := json.NewDecoder(httpProbeRes.Body).Decode(&httpOut); err != nil {
		t.Fatal(err)
	}
	if !httpOut.Success || httpOut.StatusCode != http.StatusOK {
		t.Fatalf("unexpected http probe result: %+v", httpOut)
	}
	if httpOut.TargetEntry != fmt.Sprintf("http://82.156.236.104:%d/", httpPort) {
		t.Fatalf("expected default probePath / in http targetEntry, got %q", httpOut.TargetEntry)
	}
	httpGetReq := httptest.NewRequest(http.MethodGet, "/api/tunnels/probe-http", nil)
	applyCookies(httpGetReq, adminCookies)
	httpGetRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(httpGetRes, httpGetReq)
	if httpGetRes.Code != http.StatusOK {
		t.Fatalf("expected http tunnel get 200, got %d", httpGetRes.Code)
	}
	var httpTunnelOut types.TunnelSpec
	if err := json.NewDecoder(httpGetRes.Body).Decode(&httpTunnelOut); err != nil {
		t.Fatal(err)
	}
	if !httpTunnelOut.LastProbeSuccess || httpTunnelOut.LastProbeStatusCode != http.StatusOK || httpTunnelOut.LastProbeTargetEntry != httpOut.TargetEntry {
		t.Fatalf("unexpected persisted http last probe result: %+v", httpTunnelOut)
	}

	httpsProbeReq := httptest.NewRequest(http.MethodPost, "/api/tunnels/probe-https/probe", nil)
	applyCookies(httpsProbeReq, adminCookies)
	httpsProbeRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(httpsProbeRes, httpsProbeReq)
	if httpsProbeRes.Code != http.StatusOK {
		t.Fatalf("expected https probe 200, got %d", httpsProbeRes.Code)
	}
	var httpsOut types.TunnelProbeResult
	if err := json.NewDecoder(httpsProbeRes.Body).Decode(&httpsOut); err != nil {
		t.Fatal(err)
	}
	if !httpsOut.Success || httpsOut.StatusCode != http.StatusOK {
		t.Fatalf("unexpected https probe result: %+v", httpsOut)
	}
	if !strings.HasSuffix(httpsOut.TargetEntry, "/admin/") {
		t.Fatalf("expected https probe targetEntry to include /admin/, got %q", httpsOut.TargetEntry)
	}
	httpsGetReq := httptest.NewRequest(http.MethodGet, "/api/tunnels/probe-https", nil)
	applyCookies(httpsGetReq, adminCookies)
	httpsGetRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(httpsGetRes, httpsGetReq)
	if httpsGetRes.Code != http.StatusOK {
		t.Fatalf("expected https tunnel get 200, got %d", httpsGetRes.Code)
	}
	var httpsTunnelOut types.TunnelSpec
	if err := json.NewDecoder(httpsGetRes.Body).Decode(&httpsTunnelOut); err != nil {
		t.Fatal(err)
	}
	if !httpsTunnelOut.LastProbeSuccess || httpsTunnelOut.LastProbeStatusCode != http.StatusOK || httpsTunnelOut.LastProbeTargetEntry != httpsOut.TargetEntry {
		t.Fatalf("unexpected persisted https last probe result: %+v", httpsTunnelOut)
	}
}

func TestTunnelProbeRejectsUnsupportedType(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)
	if _, err := server.store.CreateTunnel(context.Background(), types.TunnelSpec{ID: "probe-tcp", NodeID: "node-a", Name: "probe-tcp", Type: "tcp", Status: "active", PublicPort: 10086, TargetHost: "127.0.0.1", TargetPort: 22}); err != nil {
		_ = err
	}
	probeReq := httptest.NewRequest(http.MethodPost, "/api/tunnels/probe-tcp/probe", nil)
	applyCookies(probeReq, adminCookies)
	probeRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(probeRes, probeReq)
	if probeRes.Code != http.StatusBadRequest {
		t.Fatalf("expected tcp probe reject 400, got %d", probeRes.Code)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func mustCloneRequest(req *http.Request, target string) *http.Request {
	clone := req.Clone(req.Context())
	parsed, err := url.Parse(target)
	if err != nil {
		panic(err)
	}
	clone.URL.Scheme = parsed.Scheme
	clone.URL.Host = parsed.Host
	clone.Host = parsed.Host
	return clone
}

func TestTunnelHealthStatusDerivedFromNodeAndConfig(t *testing.T) {
	backend := store.NewInMemoryStore()
	server := NewServer("test", backend, "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerHealthyBody, _ := json.Marshal(types.NodeRegisterRequest{
		NodeID:       "node-healthy",
		NodeName:     "node-healthy",
		AgentVersion: "0.1.0",
		Capabilities: types.NodeCapabilities{TCPRelay: true, SOCKS5Connect: true},
	})
	registerHealthyReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerHealthyBody))
	registerHealthyRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerHealthyRes, registerHealthyReq)
	if registerHealthyRes.Code != http.StatusOK {
		t.Fatalf("expected healthy node register 200, got %d", registerHealthyRes.Code)
	}

	registerOfflineBody, _ := json.Marshal(types.NodeRegisterRequest{
		NodeID:       "node-offline",
		NodeName:     "node-offline",
		AgentVersion: "0.1.0",
		Capabilities: types.NodeCapabilities{TCPRelay: true},
	})
	registerOfflineReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerOfflineBody))
	registerOfflineRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerOfflineRes, registerOfflineReq)
	if registerOfflineRes.Code != http.StatusOK {
		t.Fatalf("expected offline node register 200, got %d", registerOfflineRes.Code)
	}

	createHealthyBody, _ := json.Marshal(map[string]any{
		"id":         "tunnel-healthy",
		"nodeId":     "node-healthy",
		"name":       "healthy",
		"type":       "tcp",
		"targetHost": "127.0.0.1",
		"targetPort": 8080,
		"publicPort": 10101,
		"status":     "active",
	})
	createHealthyReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createHealthyBody))
	applyCookies(createHealthyReq, adminCookies)
	createHealthyRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createHealthyRes, createHealthyReq)
	if createHealthyRes.Code != http.StatusCreated {
		t.Fatalf("expected healthy tunnel create 201, got %d", createHealthyRes.Code)
	}

	createOfflineBody, _ := json.Marshal(map[string]any{
		"id":         "tunnel-offline",
		"nodeId":     "node-offline",
		"name":       "offline",
		"type":       "tcp",
		"targetHost": "127.0.0.1",
		"targetPort": 8081,
		"publicPort": 10102,
		"status":     "active",
	})
	createOfflineReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createOfflineBody))
	applyCookies(createOfflineReq, adminCookies)
	createOfflineRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createOfflineRes, createOfflineReq)
	if createOfflineRes.Code != http.StatusCreated {
		t.Fatalf("expected offline tunnel create 201, got %d", createOfflineRes.Code)
	}

	createCapBody, _ := json.Marshal(map[string]any{
		"id":         "tunnel-capability-missing",
		"nodeId":     "node-healthy",
		"name":       "capability-missing",
		"type":       "socks5",
		"publicPort": 10103,
		"status":     "active",
	})
	createCapReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createCapBody))
	applyCookies(createCapReq, adminCookies)
	createCapRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createCapRes, createCapReq)
	if createCapRes.Code != http.StatusCreated {
		t.Fatalf("expected socks5 tunnel create 201, got %d", createCapRes.Code)
	}

	misconfigured, err := backend.CreateTunnel(context.Background(), types.TunnelSpec{
		ID:         "tunnel-misconfigured",
		NodeID:     "node-healthy",
		Name:       "misconfigured",
		Type:       "tcp",
		Status:     "active",
		PublicPort: 10104,
		TargetHost: "",
		TargetPort: 0,
		Metadata:   map[string]string{"nodeId": "node-healthy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := server.withTunnelHealthOne(context.Background(), misconfigured).HealthStatus; got != types.TunnelHealthMisconfigured {
		t.Fatalf("expected direct misconfigured health, got %q", got)
	}

	offlineNode, err := backend.GetNode(context.Background(), "node-offline")
	if err != nil {
		t.Fatal(err)
	}
	offlineNode.LastSeenAt = time.Now().UTC().Add(-2 * time.Minute)

	healthyNode, err := backend.GetNode(context.Background(), "node-healthy")
	if err != nil {
		t.Fatal(err)
	}
	healthyNode.Capabilities.SOCKS5Connect = false

	server.store = nodeOverrideStore{
		Store: backend,
		overrides: map[string]types.NodeSummary{
			"node-offline": offlineNode,
			"node-healthy": healthyNode,
		},
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/tunnels", nil)
	applyCookies(listReq, adminCookies)
	listRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected list tunnels 200, got %d", listRes.Code)
	}
	var listOut struct {
		Items []types.TunnelSpec `json:"items"`
	}
	if err := json.NewDecoder(listRes.Body).Decode(&listOut); err != nil {
		t.Fatal(err)
	}

	got := map[string]types.TunnelHealthStatus{}
	for _, item := range listOut.Items {
		got[item.ID] = item.HealthStatus
	}
	if got["tunnel-healthy"] != types.TunnelHealthHealthy {
		t.Fatalf("expected healthy health status, got %q", got["tunnel-healthy"])
	}
	if got["tunnel-offline"] != types.TunnelHealthNodeOffline {
		t.Fatalf("expected node_offline health status, got %q", got["tunnel-offline"])
	}
	if got["tunnel-capability-missing"] != types.TunnelHealthCapabilityMissing {
		t.Fatalf("expected capability_missing health status, got %q", got["tunnel-capability-missing"])
	}
	if got["tunnel-misconfigured"] != types.TunnelHealthMisconfigured {
		t.Fatalf("expected misconfigured health status, got %q", got["tunnel-misconfigured"])
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/tunnels/tunnel-offline", nil)
	applyCookies(getReq, adminCookies)
	getRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected get tunnel 200, got %d", getRes.Code)
	}
	var tunnelOut types.TunnelSpec
	if err := json.NewDecoder(getRes.Body).Decode(&tunnelOut); err != nil {
		t.Fatal(err)
	}
	if tunnelOut.HealthStatus != types.TunnelHealthNodeOffline {
		t.Fatalf("expected detail node_offline, got %q", tunnelOut.HealthStatus)
	}
}

func TestIsolatedNodeRejectsTunnelCreationUntilReleased(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeID: "node-isolated", NodeName: "node-isolated", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, HTTPRelay: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register 200, got %d", registerRes.Code)
	}

	isolateBody, _ := json.Marshal(types.UpdateNodeRequest{Isolated: true})
	isolateReq := httptest.NewRequest(http.MethodPut, "/api/nodes/node-isolated", bytes.NewReader(isolateBody))
	applyCookies(isolateReq, adminCookies)
	isolateRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(isolateRes, isolateReq)
	if isolateRes.Code != http.StatusOK {
		t.Fatalf("expected isolate 200, got %d", isolateRes.Code)
	}

	createBody, _ := json.Marshal(map[string]any{"nodeId": "node-isolated", "name": "blocked-tunnel", "type": "tcp", "targetHost": "127.0.0.1", "targetPort": 8080, "publicPort": 18090, "status": "active"})
	createReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(createReq, adminCookies)
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusBadRequest {
		t.Fatalf("expected isolated create reject 400, got %d", createRes.Code)
	}

	releaseBody, _ := json.Marshal(types.UpdateNodeRequest{Isolated: false})
	releaseReq := httptest.NewRequest(http.MethodPut, "/api/nodes/node-isolated", bytes.NewReader(releaseBody))
	applyCookies(releaseReq, adminCookies)
	releaseRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(releaseRes, releaseReq)
	if releaseRes.Code != http.StatusOK {
		t.Fatalf("expected release 200, got %d", releaseRes.Code)
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(retryReq, adminCookies)
	retryRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(retryRes, retryReq)
	if retryRes.Code != http.StatusCreated {
		t.Fatalf("expected create after release 201, got %d", retryRes.Code)
	}
}

func TestNodeMetadataFilterAndUpdate(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, err := json.Marshal(types.NodeRegisterRequest{
		NodeName:     "node-local-a",
		AgentVersion: "0.1.0",
		Capabilities: types.NodeCapabilities{TCPRelay: true, HTTPRelay: true},
		Metadata: map[string]string{
			"hostname":        "host-a",
			"os":              "windows",
			"arch":            "amd64",
			"deploymentMode":  "managed",
			"serviceUnit":     "cloud-relay-client-agent@node-local-a.service",
			"instanceProfile": "node-local-a",
			"instanceManaged": "true",
			"nodeRole":        "local",
			"environment":     "test",
			"trustLevel":      "trusted",
			"owner":           "alice",
			"location":        "shanghai",
			"tags":            "desk,win",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register status 200, got %d", registerRes.Code)
	}
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/nodes?nodeRole=local&environment=test&trustLevel=trusted&owner=ali&tag=desk", nil)
	applyCookies(listReq, adminCookies)
	listRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d", listRes.Code)
	}
	var listOut struct {
		Items []types.NodeSummary `json:"items"`
	}
	if err := json.NewDecoder(listRes.Body).Decode(&listOut); err != nil {
		t.Fatal(err)
	}
	if len(listOut.Items) != 1 {
		t.Fatalf("expected 1 filtered node, got %d", len(listOut.Items))
	}
	if listOut.Items[0].NodeRole != types.NodeRoleLocal || listOut.Items[0].Environment != types.NodeEnvironmentTest || listOut.Items[0].TrustLevel != types.NodeTrustTrusted {
		t.Fatalf("expected metadata fields to be hydrated, got %+v", listOut.Items[0])
	}
	if len(listOut.Items[0].Tags) != 2 {
		t.Fatalf("expected tags to be present, got %+v", listOut.Items[0].Tags)
	}
	if listOut.Items[0].DeploymentMode != "managed" || listOut.Items[0].ServiceUnit != "cloud-relay-client-agent@node-local-a.service" || listOut.Items[0].InstanceProfile != "node-local-a" || !listOut.Items[0].InstanceManaged {
		t.Fatalf("expected deployment metadata fields to be hydrated, got %+v", listOut.Items[0])
	}

	updateBody, err := json.Marshal(types.UpdateNodeRequest{
		NodeRole:    types.NodeRoleThirdParty,
		Environment: types.NodeEnvironmentProd,
		TrustLevel:  types.NodeTrustExternal,
		Owner:       "bob",
		Location:    "beijing",
		Tags:        []string{"third", "edge"},
	})
	if err != nil {
		t.Fatal(err)
	}
	updateReq := httptest.NewRequest(http.MethodPut, "/api/nodes/"+registerOut.NodeID, bytes.NewReader(updateBody))
	applyCookies(updateReq, adminCookies)
	updateRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d", updateRes.Code)
	}
	var updated types.NodeSummary
	if err := json.NewDecoder(updateRes.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.NodeRole != types.NodeRoleThirdParty || updated.Environment != types.NodeEnvironmentProd || updated.TrustLevel != types.NodeTrustExternal {
		t.Fatalf("expected updated node metadata, got %+v", updated)
	}
	if updated.Owner != "bob" || updated.Location != "beijing" {
		t.Fatalf("expected updated owner/location, got %+v", updated)
	}
	if len(updated.Tags) != 2 || updated.Tags[0] != "edge" || updated.Tags[1] != "third" {
		t.Fatalf("expected normalized tags, got %+v", updated.Tags)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/nodes/"+registerOut.NodeID, nil)
	applyCookies(getReq, adminCookies)
	getRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected get status 200, got %d", getRes.Code)
	}
	var got types.NodeSummary
	if err := json.NewDecoder(getRes.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.DeploymentMode != listOut.Items[0].DeploymentMode || got.ServiceUnit != listOut.Items[0].ServiceUnit || got.InstanceProfile != listOut.Items[0].InstanceProfile || got.InstanceManaged != listOut.Items[0].InstanceManaged {
		t.Fatalf("expected get/list node management fields to match, got get=%+v list=%+v", got, listOut.Items[0])
	}
}

func TestNodeListPagination(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	for i := 0; i < 25; i++ {
		registerBody, err := json.Marshal(types.NodeRegisterRequest{
			NodeID:       fmt.Sprintf("node-page-%02d", i),
			NodeName:     fmt.Sprintf("node-%02d", i),
			AgentVersion: "0.1.0",
			Capabilities: types.NodeCapabilities{TCPRelay: true},
			Metadata: map[string]string{
				"nodeRole":    "local",
				"environment": "test",
				"trustLevel":  "trusted",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("expected register status 200, got %d", res.Code)
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/nodes?nodeRole=local&limit=10&offset=10", nil)
	applyCookies(listReq, adminCookies)
	listRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d", listRes.Code)
	}
	var listOut types.NodeListResponse
	if err := json.NewDecoder(listRes.Body).Decode(&listOut); err != nil {
		t.Fatal(err)
	}
	if listOut.Total != 25 {
		t.Fatalf("expected total 25, got %d", listOut.Total)
	}
	if listOut.Limit != 10 || listOut.Offset != 10 {
		t.Fatalf("expected limit/offset 10/10, got %d/%d", listOut.Limit, listOut.Offset)
	}
	if len(listOut.Items) != 10 {
		t.Fatalf("expected 10 items, got %d", len(listOut.Items))
	}
}

func TestHTTPTunnelLifecycleVisibleToAgentAndRoutes(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)
	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "http-node", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, HTTPRelay: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	createBody, _ := json.Marshal(map[string]any{
		"id":         "tunnel-http-a",
		"nodeId":     registerOut.NodeID,
		"name":       "http-api",
		"type":       "http",
		"targetHost": "127.0.0.1",
		"targetPort": 18080,
		"publicPort": 18081,
		"status":     "active",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(createReq, adminCookies)
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d", createRes.Code)
	}
	var created types.TunnelSpec
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Type != "http" {
		t.Fatalf("expected http type, got %s", created.Type)
	}

	agentReq := httptest.NewRequest(http.MethodGet, "/agent/tunnels?nodeId="+registerOut.NodeID, nil)
	agentRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(agentRes, agentReq)
	if agentRes.Code != http.StatusOK {
		t.Fatalf("expected agent tunnels status 200, got %d", agentRes.Code)
	}
	var agentOut struct {
		Items []types.TunnelSpec `json:"items"`
	}
	if err := json.NewDecoder(agentRes.Body).Decode(&agentOut); err != nil {
		t.Fatal(err)
	}
	if len(agentOut.Items) != 1 || agentOut.Items[0].Type != "http" {
		t.Fatalf("expected http tunnel in agent view, got %+v", agentOut.Items)
	}

	httpRoutesReq := httptest.NewRequest(http.MethodGet, "/internal/routes/http", nil)
	httpRoutesRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(httpRoutesRes, httpRoutesReq)
	if httpRoutesRes.Code != http.StatusOK {
		t.Fatalf("expected http routes status 200, got %d", httpRoutesRes.Code)
	}
	var httpRoutesOut struct {
		Items []types.TunnelSpec `json:"items"`
	}
	if err := json.NewDecoder(httpRoutesRes.Body).Decode(&httpRoutesOut); err != nil {
		t.Fatal(err)
	}
	if len(httpRoutesOut.Items) != 1 || httpRoutesOut.Items[0].Type != "http" {
		t.Fatalf("expected http tunnel in http routes, got %+v", httpRoutesOut.Items)
	}
	if httpRoutesOut.Items[0].HealthStatus != types.TunnelHealthHealthy {
		t.Fatalf("expected healthy http route, got %q", httpRoutesOut.Items[0].HealthStatus)
	}
}

func TestHTTPTunnelRequiresHTTPRelayCapableNode(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	unsupportedBody, _ := json.Marshal(types.NodeRegisterRequest{NodeID: "node-no-http", NodeName: "node-no-http", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true}})
	unsupportedReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(unsupportedBody))
	unsupportedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(unsupportedRes, unsupportedReq)
	if unsupportedRes.Code != http.StatusOK {
		t.Fatalf("expected unsupported node register 200, got %d", unsupportedRes.Code)
	}

	createBody, _ := json.Marshal(map[string]any{"nodeId": "node-no-http", "name": "http-blocked", "type": "http", "targetHost": "127.0.0.1", "targetPort": 8080, "publicPort": 18082, "status": "active"})
	createReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(createReq, adminCookies)
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusBadRequest {
		t.Fatalf("expected http tunnel reject 400, got %d", createRes.Code)
	}
}

func TestSOCKS5TunnelLifecycleVisibleToAgentAndRoutes(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)
	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "socks-node", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, HTTPRelay: true, HTTPSRelay: true, SOCKS5Connect: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	createBody, err := json.Marshal(map[string]any{
		"id":         "tunnel-socks5-a",
		"nodeId":     registerOut.NodeID,
		"name":       "socks-entry",
		"type":       "socks5",
		"targetHost": "socks5",
		"targetPort": 1080,
		"publicPort": 11080,
		"status":     "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(createReq, adminCookies)
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d", createRes.Code)
	}

	agentReq := httptest.NewRequest(http.MethodGet, "/agent/tunnels?nodeId="+registerOut.NodeID, nil)
	agentRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(agentRes, agentReq)
	if agentRes.Code != http.StatusOK {
		t.Fatalf("expected agent tunnels status 200, got %d", agentRes.Code)
	}
	var agentOut struct {
		Items []types.TunnelSpec `json:"items"`
	}
	if err := json.NewDecoder(agentRes.Body).Decode(&agentOut); err != nil {
		t.Fatal(err)
	}
	if len(agentOut.Items) != 1 || agentOut.Items[0].Type != "socks5" {
		t.Fatalf("expected socks5 tunnel from agent view, got %+v", agentOut.Items)
	}

	routesReq := httptest.NewRequest(http.MethodGet, "/internal/routes/tcp", nil)
	routesRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(routesRes, routesReq)
	if routesRes.Code != http.StatusOK {
		t.Fatalf("expected routes status 200, got %d", routesRes.Code)
	}
	var routesOut struct {
		Items []types.TunnelSpec `json:"items"`
	}
	if err := json.NewDecoder(routesRes.Body).Decode(&routesOut); err != nil {
		t.Fatal(err)
	}
	if len(routesOut.Items) != 1 || routesOut.Items[0].Type != "socks5" {
		t.Fatalf("expected socks5 tunnel in tcp routes, got %+v", routesOut.Items)
	}
}

func TestSOCKS5TunnelCreateUpdateValidation(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)
	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "socks-validate-node", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, SOCKS5Connect: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	createBody, _ := json.Marshal(map[string]any{
		"id":         "tunnel-socks5-v",
		"nodeId":     registerOut.NodeID,
		"name":       "socks-validate",
		"type":       "socks5",
		"targetHost": "should-be-overridden",
		"targetPort": 9999,
		"publicPort": 12080,
		"status":     "active",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(createReq, adminCookies)
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d", createRes.Code)
	}
	var created types.TunnelSpec
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.TargetHost != "socks5" || created.TargetPort != 1080 {
		t.Fatalf("expected normalized socks5 target, got %s:%d", created.TargetHost, created.TargetPort)
	}

	updateBody, _ := json.Marshal(map[string]any{
		"nodeId":     registerOut.NodeID,
		"name":       "socks-validate-updated",
		"type":       "socks5",
		"targetHost": "still-ignored",
		"targetPort": 7,
		"publicPort": 12080,
		"status":     "active",
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/tunnels/"+created.ID, bytes.NewReader(updateBody))
	applyCookies(updateReq, adminCookies)
	updateRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d", updateRes.Code)
	}
	var updated types.TunnelSpec
	if err := json.NewDecoder(updateRes.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.TargetHost != "socks5" || updated.TargetPort != 1080 {
		t.Fatalf("expected normalized socks5 target after update, got %s:%d", updated.TargetHost, updated.TargetPort)
	}
}

func TestSOCKS5TunnelRequiresSOCKS5CapableNode(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	unsupportedBody, _ := json.Marshal(types.NodeRegisterRequest{
		NodeID:       "node-no-socks5",
		NodeName:     "node-no-socks5",
		AgentVersion: "0.1.0",
		Capabilities: types.NodeCapabilities{TCPRelay: true},
	})
	unsupportedReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(unsupportedBody))
	unsupportedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(unsupportedRes, unsupportedReq)
	if unsupportedRes.Code != http.StatusOK {
		t.Fatalf("expected unsupported node register 200, got %d", unsupportedRes.Code)
	}

	createBody, _ := json.Marshal(map[string]any{
		"nodeId":     "node-no-socks5",
		"name":       "socks-blocked",
		"type":       "socks5",
		"publicPort": 13080,
		"status":     "active",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(createReq, adminCookies)
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusBadRequest {
		t.Fatalf("expected create reject 400, got %d", createRes.Code)
	}

	supportedBody, _ := json.Marshal(types.NodeRegisterRequest{
		NodeID:       "node-has-socks5",
		NodeName:     "node-has-socks5",
		AgentVersion: "0.1.0",
		Capabilities: types.NodeCapabilities{TCPRelay: true, SOCKS5Connect: true},
	})
	supportedReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(supportedBody))
	supportedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(supportedRes, supportedReq)
	if supportedRes.Code != http.StatusOK {
		t.Fatalf("expected supported node register 200, got %d", supportedRes.Code)
	}

	allowedBody, _ := json.Marshal(map[string]any{
		"nodeId":     "node-has-socks5",
		"name":       "socks-allowed",
		"type":       "socks5",
		"publicPort": 13081,
		"status":     "active",
	})
	allowedReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(allowedBody))
	applyCookies(allowedReq, adminCookies)
	allowedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(allowedRes, allowedReq)
	if allowedRes.Code != http.StatusCreated {
		t.Fatalf("expected create allowed 201, got %d", allowedRes.Code)
	}
}

func TestTunnelRuntimeFieldsStaySeparateFromTransportPolicy(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "p2p-phase1-node", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, P2PAssist: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	createBody, _ := json.Marshal(map[string]any{
		"id":              "tunnel-p2p-phase1-a",
		"nodeId":          registerOut.NodeID,
		"name":            "p2p-phase1-a",
		"type":            "tcp",
		"transportPolicy": types.TunnelTransportP2PPreferred,
		"targetHost":      "127.0.0.1",
		"targetPort":      18090,
		"publicPort":      18091,
		"status":          "active",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(createReq, adminCookies)
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d", createRes.Code)
	}

	created, err := server.store.GetTunnel(context.Background(), "tunnel-p2p-phase1-a")
	if err != nil {
		t.Fatal(err)
	}
	created.RuntimePath = types.TunnelRuntimePathRelay
	created.RuntimeState = types.TunnelRuntimeStatePending
	created.LastFailureReason = "p2p data-plane not enabled yet"
	if created.Metadata == nil {
		created.Metadata = map[string]string{}
	}
	created.Metadata["runtimePath"] = created.RuntimePath
	created.Metadata["runtimeState"] = created.RuntimeState
	created.Metadata["lastFailureReason"] = created.LastFailureReason
	if _, err := server.store.UpdateTunnel(context.Background(), created); err != nil {
		t.Fatal(err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/tunnels/tunnel-p2p-phase1-a", nil)
	applyCookies(getReq, adminCookies)
	getRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected get status 200, got %d", getRes.Code)
	}
	var out types.TunnelSpec
	if err := json.NewDecoder(getRes.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Type != "tcp" {
		t.Fatalf("expected type tcp, got %s", out.Type)
	}
	if out.TransportPolicy != types.TunnelTransportP2PPreferred {
		t.Fatalf("expected transportPolicy p2p_preferred, got %s", out.TransportPolicy)
	}
	if out.RuntimePath != types.TunnelRuntimePathRelay {
		t.Fatalf("expected runtimePath relay, got %s", out.RuntimePath)
	}
	if out.RuntimeState != types.TunnelRuntimeStatePending {
		t.Fatalf("expected runtimeState pending, got %s", out.RuntimeState)
	}
	if out.LastFailureReason != "p2p data-plane not enabled yet" {
		t.Fatalf("expected lastFailureReason to round-trip, got %q", out.LastFailureReason)
	}
	if out.TransportPolicy == out.RuntimePath {
		t.Fatalf("transportPolicy and runtimePath must remain separate semantics")
	}
}

func TestTunnelRuntimeFieldsWithNilMetadataDoNotPanic(t *testing.T) {
	backend := store.NewInMemoryStore()
	created, err := backend.CreateTunnel(context.Background(), types.TunnelSpec{
		ID:                "runtime-nil-meta-a",
		Name:              "runtime-nil-meta-a",
		Type:              "tcp",
		TransportPolicy:   types.TunnelTransportP2PPreferred,
		RuntimePath:       types.TunnelRuntimePathRelay,
		RuntimeState:      types.TunnelRuntimeStatePending,
		LastFailureReason: "p2p data-plane not enabled yet",
		NodeID:            "node-a",
		TargetHost:        "127.0.0.1",
		TargetPort:        19090,
		PublicPort:        19091,
		Status:            "active",
		Metadata:          nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.RuntimePath != types.TunnelRuntimePathRelay || created.RuntimeState != types.TunnelRuntimeStatePending || created.LastFailureReason != "p2p data-plane not enabled yet" {
		t.Fatalf("unexpected create runtime fields: %+v", created)
	}
	got, err := backend.GetTunnel(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RuntimePath != types.TunnelRuntimePathRelay || got.RuntimeState != types.TunnelRuntimeStatePending || got.LastFailureReason != "p2p data-plane not enabled yet" {
		t.Fatalf("unexpected get runtime fields: %+v", got)
	}
	items, err := backend.ListTunnels(context.Background(), store.TunnelFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(items))
	}
	if items[0].RuntimePath != types.TunnelRuntimePathRelay || items[0].RuntimeState != types.TunnelRuntimeStatePending || items[0].LastFailureReason != "p2p data-plane not enabled yet" {
		t.Fatalf("unexpected list runtime fields: %+v", items[0])
	}
	got.RuntimePath = types.TunnelRuntimePathP2P
	got.RuntimeState = types.TunnelRuntimeStateActive
	got.LastFailureReason = ""
	got.Metadata = nil
	updated, err := backend.UpdateTunnel(context.Background(), got)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RuntimePath != types.TunnelRuntimePathP2P || updated.RuntimeState != types.TunnelRuntimeStateActive || updated.LastFailureReason != "" {
		t.Fatalf("unexpected update runtime fields: %+v", updated)
	}
	again, err := backend.GetTunnel(context.Background(), got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.RuntimePath != types.TunnelRuntimePathP2P || again.RuntimeState != types.TunnelRuntimeStateActive || again.LastFailureReason != "" {
		t.Fatalf("unexpected get after update runtime fields: %+v", again)
	}
}

func TestNodeRuntimeSummaryReflectsTunnelRuntimeFacts(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{
		NodeID:       "node-runtime-summary-a",
		NodeName:     "node-runtime-summary-a",
		AgentVersion: "0.1.0",
		Capabilities: types.NodeCapabilities{TCPRelay: true, UDPRelay: true, P2PAssist: true},
	})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register 200, got %d", registerRes.Code)
	}

	items := []types.TunnelSpec{
		{
			ID:                "node-runtime-summary-relay",
			Name:              "node-runtime-summary-relay",
			Type:              "tcp",
			TransportPolicy:   types.TunnelTransportP2PPreferred,
			RuntimePath:       types.TunnelRuntimePathRelay,
			RuntimeState:      types.TunnelRuntimeStatePending,
			LastFailureReason: "awaiting relay path",
			NodeID:            "node-runtime-summary-a",
			TargetHost:        "127.0.0.1",
			TargetPort:        20001,
			PublicPort:        21001,
			Status:            "active",
		},
		{
			ID:              "node-runtime-summary-p2p",
			Name:            "node-runtime-summary-p2p",
			Type:            "udp",
			TransportPolicy: types.TunnelTransportP2PPreferred,
			RuntimePath:     types.TunnelRuntimePathP2P,
			RuntimeState:    types.TunnelRuntimeStateUnavailable,
			NodeID:          "node-runtime-summary-a",
			TargetHost:      "127.0.0.1",
			TargetPort:      20002,
			PublicPort:      21002,
			Status:          "active",
		},
		{
			ID:              "node-runtime-summary-inactive",
			Name:            "node-runtime-summary-inactive",
			Type:            "tcp",
			TransportPolicy: types.TunnelTransportP2PPreferred,
			RuntimePath:     types.TunnelRuntimePathP2P,
			RuntimeState:    types.TunnelRuntimeStatePending,
			NodeID:          "node-runtime-summary-a",
			TargetHost:      "127.0.0.1",
			TargetPort:      20003,
			PublicPort:      21003,
			Status:          "paused",
		},
	}
	for _, item := range items {
		if _, err := server.store.CreateTunnel(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	applyCookies(listReq, adminCookies)
	listRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected list nodes 200, got %d", listRes.Code)
	}
	var listOut types.NodeListResponse
	if err := json.NewDecoder(listRes.Body).Decode(&listOut); err != nil {
		t.Fatal(err)
	}
	if len(listOut.Items) != 1 {
		t.Fatalf("expected 1 node, got %d", len(listOut.Items))
	}
	summary := listOut.Items[0].RuntimeSummary
	if summary.ActiveTunnelCount != 2 {
		t.Fatalf("expected 2 active tunnels in runtime summary, got %d", summary.ActiveTunnelCount)
	}
	if summary.RelayPathCount != 1 || summary.P2PPathCount != 1 {
		t.Fatalf("expected relay=1 and p2p=1, got %+v", summary)
	}
	if summary.PendingStateCount != 1 || summary.UnavailableStateCount != 1 {
		t.Fatalf("expected pending=1 and unavailable=1, got %+v", summary)
	}
	if summary.FailureReasonCount != 1 {
		t.Fatalf("expected failureReasonCount=1, got %+v", summary)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/nodes/node-runtime-summary-a", nil)
	applyCookies(getReq, adminCookies)
	getRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected get node 200, got %d", getRes.Code)
	}
	var nodeOut types.NodeSummary
	if err := json.NewDecoder(getRes.Body).Decode(&nodeOut); err != nil {
		t.Fatal(err)
	}
	if nodeOut.RuntimeSummary != summary {
		t.Fatalf("expected get node runtime summary to match list summary, got %+v vs %+v", nodeOut.RuntimeSummary, summary)
	}
	if nodeOut.DeploymentMode != "" || nodeOut.ServiceUnit != "" || nodeOut.InstanceProfile != "" || nodeOut.InstanceManaged {
		t.Fatalf("expected runtime summary test node to keep management fields empty unless explicitly hydrated, got %+v", nodeOut)
	}
	if nodeOut.RuntimeSummary.P2PPathCount != 1 && nodeOut.RuntimeSummary.ActiveTunnelCount == 2 {
		t.Fatalf("runtime summary must come from runtimePath facts, not transportPolicy intent: %+v", nodeOut.RuntimeSummary)
	}
}

func TestPublicPortConflictSemanticsByProtocol(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "edge-port-semantics", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, UDPRelay: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register status 200, got %d", registerRes.Code)
	}
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	createTCPBody, _ := json.Marshal(map[string]any{"id": "tunnel-tcp-a", "nodeId": registerOut.NodeID, "name": "tcp-a", "type": "tcp", "targetHost": "127.0.0.1", "targetPort": 18080, "publicPort": 19090, "status": "active"})
	createTCPReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createTCPBody))
	applyCookies(createTCPReq, adminCookies)
	createTCPRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createTCPRes, createTCPReq)
	if createTCPRes.Code != http.StatusCreated {
		t.Fatalf("expected tcp create 201, got %d", createTCPRes.Code)
	}

	tcpConflictBody, _ := json.Marshal(map[string]any{"id": "tunnel-tcp-b", "nodeId": registerOut.NodeID, "name": "tcp-b", "type": "tcp", "targetHost": "127.0.0.1", "targetPort": 18081, "publicPort": 19090, "status": "active"})
	tcpConflictReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(tcpConflictBody))
	applyCookies(tcpConflictReq, adminCookies)
	tcpConflictRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(tcpConflictRes, tcpConflictReq)
	if tcpConflictRes.Code != http.StatusConflict {
		t.Fatalf("expected tcp/tcp conflict 409, got %d", tcpConflictRes.Code)
	}

	tcpUDPConflictAllowedBody, _ := json.Marshal(map[string]any{"id": "tunnel-udp-reserved-same-port", "nodeId": registerOut.NodeID, "name": "udp-same-port", "type": "udp", "targetHost": "127.0.0.1", "targetPort": 18082, "publicPort": 19090, "status": "active"})
	tcpUDPConflictAllowedReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(tcpUDPConflictAllowedBody))
	applyCookies(tcpUDPConflictAllowedReq, adminCookies)
	tcpUDPConflictAllowedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(tcpUDPConflictAllowedRes, tcpUDPConflictAllowedReq)
	if tcpUDPConflictAllowedRes.Code != http.StatusCreated {
		t.Fatalf("expected tcp/udp same-port create 201, got %d", tcpUDPConflictAllowedRes.Code)
	}

	memoryStore := store.NewInMemoryStore()
	if _, err := memoryStore.CreateTunnel(context.Background(), types.TunnelSpec{ID: "tcp-store-a", Type: "tcp", Status: "active", PublicPort: 20000, TargetHost: "127.0.0.1", TargetPort: 80}); err != nil {
		t.Fatalf("expected tcp store create success, got %v", err)
	}
	if _, err := memoryStore.CreateTunnel(context.Background(), types.TunnelSpec{ID: "udp-store-a", Type: "udp", Status: "active", PublicPort: 20000, TargetHost: "127.0.0.1", TargetPort: 53}); err != nil {
		t.Fatalf("expected tcp/udp same port to coexist in store, got %v", err)
	}
	if _, err := memoryStore.CreateTunnel(context.Background(), types.TunnelSpec{ID: "udp-store-b", Type: "udp", Status: "active", PublicPort: 20000, TargetHost: "127.0.0.1", TargetPort: 54}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected udp/udp conflict, got %v", err)
	}
}

func TestNodeOptionsExposeSOCKS5Capability(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	bodyA, _ := json.Marshal(types.NodeRegisterRequest{NodeID: "node-opt-a", NodeName: "node-opt-a", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true}})
	reqA := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(bodyA))
	resA := httptest.NewRecorder()
	server.Handler().ServeHTTP(resA, reqA)

	bodyB, _ := json.Marshal(types.NodeRegisterRequest{NodeID: "node-opt-b", NodeName: "node-opt-b", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, SOCKS5Connect: true}})
	reqB := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(bodyB))
	resB := httptest.NewRecorder()
	server.Handler().ServeHTTP(resB, reqB)

	optionsReq := httptest.NewRequest(http.MethodGet, "/api/node-options", nil)
	applyCookies(optionsReq, adminCookies)
	optionsRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(optionsRes, optionsReq)
	if optionsRes.Code != http.StatusOK {
		t.Fatalf("expected node options status 200, got %d", optionsRes.Code)
	}
	var payload types.NodeOptionsResponse
	if err := json.NewDecoder(optionsRes.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range payload.Items {
		seen[item.NodeID] = item.SupportsSOCKS5
	}
	if seen["node-opt-a"] {
		t.Fatal("expected node-opt-a to not support socks5")
	}
	if !seen["node-opt-b"] {
		t.Fatal("expected node-opt-b to support socks5")
	}
}

func TestHTTPSTunnelRequiresUniqueDomainOnCreate(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "https-node", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, HTTPRelay: true, HTTPSRelay: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register 200, got %d", registerRes.Code)
	}
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	createBody, _ := json.Marshal(map[string]any{
		"id":         "tunnel-https-a",
		"nodeId":     registerOut.NodeID,
		"name":       "https-a",
		"type":       "https",
		"targetHost": "127.0.0.1",
		"targetPort": 7710,
		"publicPort": 10443,
		"domain":     "WWW.EXAMPLE.COM",
		"tlsMode":    "edge_terminate",
		"status":     "active",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(createReq, adminCookies)
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected create 201, got %d", createRes.Code)
	}
	var created types.TunnelSpec
	if err := json.NewDecoder(createRes.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Domain != "www.example.com" {
		t.Fatalf("expected normalized lowercase domain, got %q", created.Domain)
	}

	duplicateBody, _ := json.Marshal(map[string]any{
		"id":         "tunnel-https-b",
		"nodeId":     registerOut.NodeID,
		"name":       "https-b",
		"type":       "https",
		"targetHost": "127.0.0.1",
		"targetPort": 7711,
		"publicPort": 10444,
		"domain":     "www.example.com",
		"tlsMode":    "edge_terminate",
		"status":     "active",
	})
	duplicateReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(duplicateBody))
	applyCookies(duplicateReq, adminCookies)
	duplicateRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(duplicateRes, duplicateReq)
	if duplicateRes.Code != http.StatusConflict {
		t.Fatalf("expected duplicate create 409, got %d", duplicateRes.Code)
	}
}

func TestHTTPSTunnelRequiresUniqueDomainOnUpdate(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "https-node", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, HTTPRelay: true, HTTPSRelay: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register 200, got %d", registerRes.Code)
	}
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	for _, item := range []struct {
		id         string
		name       string
		domain     string
		publicPort int
	}{
		{id: "tunnel-https-a", name: "https-a", domain: "alpha.example.com", publicPort: 10443},
		{id: "tunnel-https-b", name: "https-b", domain: "beta.example.com", publicPort: 10444},
	} {
		body, _ := json.Marshal(map[string]any{
			"id":         item.id,
			"nodeId":     registerOut.NodeID,
			"name":       item.name,
			"type":       "https",
			"targetHost": "127.0.0.1",
			"targetPort": 7710,
			"publicPort": item.publicPort,
			"domain":     item.domain,
			"tlsMode":    "edge_terminate",
			"status":     "active",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(body))
		applyCookies(req, adminCookies)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusCreated {
			t.Fatalf("expected create 201, got %d", res.Code)
		}
	}

	updateBody, _ := json.Marshal(map[string]any{
		"nodeId":     registerOut.NodeID,
		"name":       "https-b",
		"type":       "https",
		"targetHost": "127.0.0.1",
		"targetPort": 7710,
		"publicPort": 10444,
		"domain":     "alpha.example.com",
		"tlsMode":    "edge_terminate",
		"status":     "active",
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/tunnels/tunnel-https-b", bytes.NewReader(updateBody))
	applyCookies(updateReq, adminCookies)
	updateRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusConflict {
		t.Fatalf("expected duplicate update 409, got %d", updateRes.Code)
	}
}

func TestHTTPSTunnelDeleteReleasesDomainForRecreate(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "https-delete-node", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, HTTPRelay: true, HTTPSRelay: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register 200, got %d", registerRes.Code)
	}
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	createBody, _ := json.Marshal(map[string]any{
		"id":         "tunnel-https-release-a",
		"nodeId":     registerOut.NodeID,
		"name":       "https-release-a",
		"type":       "https",
		"targetHost": "127.0.0.1",
		"targetPort": 7710,
		"publicPort": 11443,
		"domain":     "release.example.com",
		"tlsMode":    "edge_terminate",
		"status":     "active",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(createReq, adminCookies)
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected create 201, got %d", createRes.Code)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/tunnels/tunnel-https-release-a", nil)
	applyCookies(deleteReq, adminCookies)
	deleteRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("expected delete 200, got %d", deleteRes.Code)
	}

	recreateBody, _ := json.Marshal(map[string]any{
		"id":         "tunnel-https-release-b",
		"nodeId":     registerOut.NodeID,
		"name":       "https-release-b",
		"type":       "https",
		"targetHost": "127.0.0.1",
		"targetPort": 7711,
		"publicPort": 11444,
		"domain":     "release.example.com",
		"tlsMode":    "edge_terminate",
		"status":     "active",
	})
	recreateReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(recreateBody))
	applyCookies(recreateReq, adminCookies)
	recreateRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(recreateRes, recreateReq)
	if recreateRes.Code != http.StatusCreated {
		t.Fatalf("expected recreate 201, got %d", recreateRes.Code)
	}
}

func TestHTTPSTunnelUpdateReturnsNormalizedDomainAndTLSMode(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "https-update-node", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, HTTPRelay: true, HTTPSRelay: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register 200, got %d", registerRes.Code)
	}
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	createBody, _ := json.Marshal(map[string]any{"id": "tunnel-https-update", "nodeId": registerOut.NodeID, "name": "https-update", "type": "https", "targetHost": "127.0.0.1", "targetPort": 7710, "publicPort": 12443, "domain": "old.example.com", "tlsMode": "edge_terminate", "status": "active"})
	createReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createBody))
	applyCookies(createReq, adminCookies)
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected create 201, got %d", createRes.Code)
	}

	updateBody, _ := json.Marshal(map[string]any{"nodeId": registerOut.NodeID, "name": "https-update-renamed", "type": "https", "targetHost": "127.0.0.2", "targetPort": 8800, "publicPort": 12443, "domain": "NEW.EXAMPLE.COM", "tlsMode": "edge_terminate", "status": "active"})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/tunnels/tunnel-https-update", bytes.NewReader(updateBody))
	applyCookies(updateReq, adminCookies)
	updateRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("expected update 200, got %d", updateRes.Code)
	}
	var updated types.TunnelSpec
	if err := json.NewDecoder(updateRes.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Domain != "new.example.com" || updated.TLSMode != "edge_terminate" {
		t.Fatalf("expected normalized domain/tlsMode, got %+v", updated)
	}
	if updated.TargetHost != "127.0.0.2" || updated.TargetPort != 8800 {
		t.Fatalf("expected updated target, got %+v", updated)
	}
}

func TestBootstrapLoginAndRoleProtectedManagementFlow(t *testing.T) {
	runtimeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(types.RelayRuntimeSummary{
			Service:      "relay-tcp",
			ObservedAt:   time.Now().UTC(),
			TotalStandby: 3,
			Pools: []types.RelayPoolSummary{{
				PoolKey:      "node-1:10086",
				NodeID:       "node-1",
				PublicPort:   10086,
				StandbyCount: 3,
				TargetSize:   8,
				MaxSize:      16,
			}},
		})
	}))
	defer runtimeUpstream.Close()

	backend := store.NewInMemoryStore()
	server := NewServer("test", backend, runtimeUpstream.URL)
	server.adminBootstrapSecret = "bootstrap-secret"

	statusReq := httptest.NewRequest(http.MethodGet, "/api/auth/bootstrap-status", nil)
	statusRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(statusRes, statusReq)
	if statusRes.Code != http.StatusOK {
		t.Fatalf("expected bootstrap status 200, got %d", statusRes.Code)
	}

	bootstrapBody, err := json.Marshal(types.BootstrapAdminRequest{
		Email:       "admin@example.com",
		DisplayName: "管理员",
		Password:    "AdminPass#2026",
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrapReq := httptest.NewRequest(http.MethodPost, "/api/auth/bootstrap", bytes.NewReader(bootstrapBody))
	bootstrapReq.Header.Set("X-Bootstrap-Secret", "bootstrap-secret")
	bootstrapRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(bootstrapRes, bootstrapReq)
	if bootstrapRes.Code != http.StatusCreated {
		t.Fatalf("expected bootstrap 201, got %d", bootstrapRes.Code)
	}
	adminCookies := bootstrapRes.Result().Cookies()
	if len(adminCookies) < 3 {
		t.Fatalf("expected auth cookies after bootstrap, got %d", len(adminCookies))
	}

	registerOut := registerNodeThroughAgent(t, server, "edge-secure")
	if _, err := backend.CreateUser(context.Background(), store.CreateUserParams{Email: "manager@example.com", DisplayName: "管理用户", Password: "ManagerPass#2026", Role: types.UserRoleManager}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.CreateUser(context.Background(), store.CreateUserParams{Email: "user@example.com", DisplayName: "普通用户", Password: "UserPass#2026", Role: types.UserRoleUser}); err != nil {
		t.Fatal(err)
	}

	managerCookies := loginAndCollectCookies(t, server, "manager@example.com", "ManagerPass#2026")
	userCookies := loginAndCollectCookies(t, server, "user@example.com", "UserPass#2026")

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	unauthorizedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedRes, unauthorizedReq)
	if unauthorizedRes.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized nodes status 401, got %d", unauthorizedRes.Code)
	}

	managerCreateTunnelBody, err := json.Marshal(map[string]any{
		"id":         "tunnel-auth",
		"nodeId":     registerOut.NodeID,
		"name":       "secure-svc",
		"type":       "tcp",
		"targetHost": "127.0.0.1",
		"targetPort": 16354,
		"publicPort": 10086,
		"status":     "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	managerCreateReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(managerCreateTunnelBody))
	applyCookies(managerCreateReq, managerCookies)
	managerCreateRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(managerCreateRes, managerCreateReq)
	if managerCreateRes.Code != http.StatusCreated {
		t.Fatalf("expected manager create tunnel 201, got %d", managerCreateRes.Code)
	}

	managerNodesReq := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	applyCookies(managerNodesReq, managerCookies)
	managerNodesRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(managerNodesRes, managerNodesReq)
	if managerNodesRes.Code != http.StatusOK {
		t.Fatalf("expected manager nodes status 200, got %d", managerNodesRes.Code)
	}

	managerRuntimeReq := httptest.NewRequest(http.MethodGet, "/api/relay/tcp/runtime", nil)
	applyCookies(managerRuntimeReq, managerCookies)
	managerRuntimeRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(managerRuntimeRes, managerRuntimeReq)
	if managerRuntimeRes.Code != http.StatusOK {
		t.Fatalf("expected manager runtime status 200, got %d", managerRuntimeRes.Code)
	}
	var runtimeOut types.RelayRuntimeSummary
	if err := json.NewDecoder(managerRuntimeRes.Body).Decode(&runtimeOut); err != nil {
		t.Fatal(err)
	}
	if runtimeOut.TotalStandby != 3 {
		t.Fatalf("expected runtime total standby 3, got %d", runtimeOut.TotalStandby)
	}

	userNodesReq := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	applyCookies(userNodesReq, userCookies)
	userNodesRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(userNodesRes, userNodesReq)
	if userNodesRes.Code != http.StatusForbidden {
		t.Fatalf("expected basic user nodes status 403, got %d", userNodesRes.Code)
	}

	adminUsersReq := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	applyCookies(adminUsersReq, adminCookies)
	adminUsersRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(adminUsersRes, adminUsersReq)
	if adminUsersRes.Code != http.StatusOK {
		t.Fatalf("expected admin users status 200, got %d", adminUsersRes.Code)
	}

	managerUsersReq := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	applyCookies(managerUsersReq, managerCookies)
	managerUsersRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(managerUsersRes, managerUsersReq)
	if managerUsersRes.Code != http.StatusForbidden {
		t.Fatalf("expected manager users status 403, got %d", managerUsersRes.Code)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	applyCookies(meReq, userCookies)
	meRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(meRes, meReq)
	if meRes.Code != http.StatusOK {
		t.Fatalf("expected me status 200, got %d", meRes.Code)
	}

	refreshReq := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	applyCookies(refreshReq, managerCookies)
	refreshRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(refreshRes, refreshReq)
	if refreshRes.Code != http.StatusOK {
		t.Fatalf("expected refresh status 200, got %d", refreshRes.Code)
	}
	if len(refreshRes.Result().Cookies()) == 0 {
		t.Fatal("expected refreshed cookies")
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	applyCookies(logoutReq, managerCookies)
	logoutRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(logoutRes, logoutReq)
	if logoutRes.Code != http.StatusOK {
		t.Fatalf("expected logout status 200, got %d", logoutRes.Code)
	}
}

func TestAgentEndpointsRemainAccessibleWithSessionAuthEnabled(t *testing.T) {
	backend := store.NewInMemoryStore()
	server := NewServer("test", backend, "")

	registerOut := registerNodeThroughAgent(t, server, "edge-open-agent")

	heartbeatBody, err := json.Marshal(types.NodeHeartbeatRequest{NodeID: registerOut.NodeID, ObservedAt: time.Now().UTC(), ActiveTunnels: 1})
	if err != nil {
		t.Fatal(err)
	}
	heartbeatReq := httptest.NewRequest(http.MethodPost, "/agent/heartbeat", bytes.NewReader(heartbeatBody))
	heartbeatRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(heartbeatRes, heartbeatReq)
	if heartbeatRes.Code != http.StatusOK {
		t.Fatalf("expected heartbeat status 200, got %d", heartbeatRes.Code)
	}

	if _, err := backend.CreateTunnel(context.Background(), types.TunnelSpec{ID: "tunnel-agent", NodeID: registerOut.NodeID, Name: "agent-visible", Type: "tcp", Status: "active", PublicPort: 10086, TargetHost: "127.0.0.1", TargetPort: 16354, Metadata: map[string]string{"nodeId": registerOut.NodeID}}); err != nil {
		t.Fatal(err)
	}

	agentTunnelsReq := httptest.NewRequest(http.MethodGet, "/agent/tunnels?nodeId="+registerOut.NodeID, nil)
	agentTunnelsRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(agentTunnelsRes, agentTunnelsReq)
	if agentTunnelsRes.Code != http.StatusOK {
		t.Fatalf("expected agent tunnels status 200, got %d", agentTunnelsRes.Code)
	}

	internalRoutesReq := httptest.NewRequest(http.MethodGet, "/internal/routes/tcp", nil)
	internalRoutesRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(internalRoutesRes, internalRoutesReq)
	if internalRoutesRes.Code != http.StatusOK {
		t.Fatalf("expected internal routes status 200, got %d", internalRoutesRes.Code)
	}
}

type nodeOverrideStore struct {
	store.Store
	overrides map[string]types.NodeSummary
}

func (s nodeOverrideStore) GetNode(ctx context.Context, nodeID string) (types.NodeSummary, error) {
	if item, ok := s.overrides[nodeID]; ok {
		return item, nil
	}
	return s.Store.GetNode(ctx, nodeID)
}

func loginAndCollectCookies(t *testing.T, server *Server, email, password string) []*http.Cookie {
	t.Helper()
	body, err := json.Marshal(types.AuthLoginRequest{Email: email, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected login 200 for %s, got %d", email, res.Code)
	}
	return res.Result().Cookies()
}

func applyCookies(req *http.Request, cookies []*http.Cookie) {
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
}

func registerNodeThroughAgent(t *testing.T, server *Server, nodeName string) types.NodeRegisterResponse {
	t.Helper()
	registerBody, err := json.Marshal(types.NodeRegisterRequest{
		NodeName:     nodeName,
		AgentVersion: "0.1.0",
		Capabilities: types.NodeCapabilities{TCPRelay: true, HTTPRelay: true, HTTPSRelay: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register status 200, got %d", registerRes.Code)
	}
	var out types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func bootstrapAdminAndCollectCookies(t *testing.T, server *Server) []*http.Cookie {
	t.Helper()
	body, err := json.Marshal(types.BootstrapAdminRequest{Email: "admin-bootstrap@example.com", DisplayName: "管理员", Password: "AdminPass#2026"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/bootstrap", bytes.NewReader(body))
	req.Header.Set("X-Bootstrap-Secret", "bootstrap-secret")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected bootstrap status 201, got %d", res.Code)
	}
	return res.Result().Cookies()
}

func TestBootstrapRequiresSecretWhenConfigured(t *testing.T) {
	backend := store.NewInMemoryStore()
	server := NewServer("test", backend, "")
	server.adminBootstrapSecret = "bootstrap-secret"

	body, err := json.Marshal(types.BootstrapAdminRequest{Email: "admin@example.com", DisplayName: "管理员", Password: "AdminPass#2026"})
	if err != nil {
		t.Fatal(err)
	}

	missingReq := httptest.NewRequest(http.MethodPost, "/api/auth/bootstrap", bytes.NewReader(body))
	missingRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing secret bootstrap 401, got %d", missingRes.Code)
	}

	invalidReq := httptest.NewRequest(http.MethodPost, "/api/auth/bootstrap", bytes.NewReader(body))
	invalidReq.Header.Set("X-Bootstrap-Secret", "wrong")
	invalidRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(invalidRes, invalidReq)
	if invalidRes.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalid secret bootstrap 401, got %d", invalidRes.Code)
	}

	okReq := httptest.NewRequest(http.MethodPost, "/api/auth/bootstrap", bytes.NewReader(body))
	okReq.Header.Set("X-Bootstrap-Secret", "bootstrap-secret")
	okRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(okRes, okReq)
	if okRes.Code != http.StatusCreated {
		t.Fatalf("expected bootstrap with secret 201, got %d", okRes.Code)
	}
}

func TestBootstrapRejectedWhenSecretNotConfigured(t *testing.T) {
	backend := store.NewInMemoryStore()
	server := NewServer("test", backend, "")
	server.adminBootstrapSecret = ""

	body, err := json.Marshal(types.BootstrapAdminRequest{Email: "admin@example.com", DisplayName: "管理员", Password: "AdminPass#2026"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/bootstrap", bytes.NewReader(body))
	req.Header.Set("X-Bootstrap-Secret", "bootstrap-secret")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected bootstrap disabled 401, got %d", res.Code)
	}
}

func TestCORSAllowsOnlyConfiguredOrigins(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.allowedOrigins = map[string]struct{}{
		"http://127.0.0.1:7710": {},
		"http://localhost:5173": {},
	}

	allowedReq := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	allowedReq.Header.Set("Origin", "http://127.0.0.1:7710")
	allowedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(allowedRes, allowedReq)
	if allowedRes.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:7710" {
		t.Fatalf("expected allowed origin reflected, got %q", allowedRes.Header().Get("Access-Control-Allow-Origin"))
	}

	rejectedReq := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	rejectedReq.Header.Set("Origin", "http://evil.example.com")
	rejectedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(rejectedRes, rejectedReq)
	if rejectedRes.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("expected rejected origin to get no allow-origin header, got %q", rejectedRes.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestDeleteTunnelAuditIncludesDeletedTunnelPayload(t *testing.T) {
	backend := store.NewInMemoryStore()
	server := NewServer("test", backend, "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerOut := registerNodeThroughAgent(t, server, "audit-node")
	createTunnelBody, _ := json.Marshal(map[string]any{"id": "audit-tunnel", "nodeId": registerOut.NodeID, "name": "audit", "type": "tcp", "targetHost": "127.0.0.1", "targetPort": 80, "publicPort": 10090, "status": "active"})
	createTunnelReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(createTunnelBody))
	applyCookies(createTunnelReq, adminCookies)
	createTunnelRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createTunnelRes, createTunnelReq)
	if createTunnelRes.Code != http.StatusCreated {
		t.Fatalf("expected create tunnel 201, got %d", createTunnelRes.Code)
	}

	deleteTunnelReq := httptest.NewRequest(http.MethodDelete, "/api/tunnels/audit-tunnel", nil)
	applyCookies(deleteTunnelReq, adminCookies)
	deleteTunnelRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleteTunnelRes, deleteTunnelReq)
	if deleteTunnelRes.Code != http.StatusOK {
		t.Fatalf("expected delete tunnel 200, got %d", deleteTunnelRes.Code)
	}

	auditReq := httptest.NewRequest(http.MethodGet, "/api/audit-logs?limit=20", nil)
	applyCookies(auditReq, adminCookies)
	auditRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(auditRes, auditReq)
	if auditRes.Code != http.StatusOK {
		t.Fatalf("expected audit logs 200, got %d", auditRes.Code)
	}
	var auditOut struct {
		Items []types.AuditLogEntry `json:"items"`
	}
	if err := json.NewDecoder(auditRes.Body).Decode(&auditOut); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, item := range auditOut.Items {
		if item.Action == "delete_tunnel" && item.ResourceType == "tunnel" && item.ResourceID == "audit-tunnel" {
			found = true
			if item.Payload["nodeId"] != registerOut.NodeID || item.Payload["publicPort"] != "10090" || item.Payload["targetHost"] != "127.0.0.1" || item.Payload["targetPort"] != "80" {
				t.Fatalf("unexpected delete_tunnel payload: %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("expected delete_tunnel audit log with exact resource and payload")
	}
}

func TestDeleteUserAuditDoesNotEmitDeleteTunnel(t *testing.T) {
	backend := store.NewInMemoryStore()
	server := NewServer("test", backend, "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	createUserBody, _ := json.Marshal(types.CreateUserRequest{Email: "audit-user@example.com", DisplayName: "审计用户", Password: "UserPass#2026", Role: types.UserRoleUser})
	createUserReq := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewReader(createUserBody))
	applyCookies(createUserReq, adminCookies)
	createUserRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createUserRes, createUserReq)
	if createUserRes.Code != http.StatusCreated {
		t.Fatalf("expected create user 201, got %d", createUserRes.Code)
	}
	var createdUser types.UserSummary
	if err := json.NewDecoder(createUserRes.Body).Decode(&createdUser); err != nil {
		t.Fatal(err)
	}

	deleteUserReq := httptest.NewRequest(http.MethodDelete, "/api/users/"+createdUser.ID, nil)
	applyCookies(deleteUserReq, adminCookies)
	deleteUserRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleteUserRes, deleteUserReq)
	if deleteUserRes.Code != http.StatusOK {
		t.Fatalf("expected delete user 200, got %d", deleteUserRes.Code)
	}

	auditReq := httptest.NewRequest(http.MethodGet, "/api/audit-logs?limit=20", nil)
	applyCookies(auditReq, adminCookies)
	auditRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(auditRes, auditReq)
	if auditRes.Code != http.StatusOK {
		t.Fatalf("expected audit logs 200, got %d", auditRes.Code)
	}
	var auditOut struct {
		Items []types.AuditLogEntry `json:"items"`
	}
	if err := json.NewDecoder(auditRes.Body).Decode(&auditOut); err != nil {
		t.Fatal(err)
	}

	foundDeleteUser := false
	for _, item := range auditOut.Items {
		if item.Action == "delete_user" && item.ResourceType == "user" && item.ResourceID == createdUser.ID {
			foundDeleteUser = true
			if item.Payload["email"] != "audit-user@example.com" || item.Payload["displayName"] != "审计用户" || item.Payload["role"] != string(types.UserRoleUser) {
				t.Fatalf("unexpected delete_user payload: %+v", item)
			}
		}
		if item.ResourceID == createdUser.ID && (item.Action == "delete_tunnel" || item.ResourceType == "tunnel") {
			t.Fatalf("unexpected tunnel deletion audit for deleted user: %+v", item)
		}
	}
	if !foundDeleteUser {
		t.Fatal("expected delete_user audit log with exact resource and payload")
	}
}

func TestLogoutAuditUsesRealActor(t *testing.T) {
	backend := store.NewInMemoryStore()
	server := NewServer("test", backend, "")
	server.adminBootstrapSecret = "bootstrap-secret"
	if _, err := backend.BootstrapAdmin(context.Background(), store.CreateUserParams{Email: "admin@example.com", DisplayName: "管理员", Password: "AdminPass#2026", Role: types.UserRoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.CreateUser(context.Background(), store.CreateUserParams{Email: "basic@example.com", DisplayName: "普通用户", Password: "UserPass#2026", Role: types.UserRoleUser}); err != nil {
		t.Fatal(err)
	}
	adminCookies := loginAndCollectCookies(t, server, "admin@example.com", "AdminPass#2026")
	userCookies := loginAndCollectCookies(t, server, "basic@example.com", "UserPass#2026")
	basicUser, err := backend.AuthenticateUser(context.Background(), store.AuthenticateUserParams{Email: "basic@example.com", Password: "UserPass#2026"})
	if err != nil {
		t.Fatal(err)
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	applyCookies(logoutReq, userCookies)
	logoutRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(logoutRes, logoutReq)
	if logoutRes.Code != http.StatusOK {
		t.Fatalf("expected logout 200, got %d", logoutRes.Code)
	}

	adminAuditReq := httptest.NewRequest(http.MethodGet, "/api/audit-logs?limit=20", nil)
	applyCookies(adminAuditReq, adminCookies)
	adminAuditRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(adminAuditRes, adminAuditReq)
	if adminAuditRes.Code != http.StatusOK {
		t.Fatalf("expected admin audit logs 200, got %d", adminAuditRes.Code)
	}
	var auditOut struct {
		Items []types.AuditLogEntry `json:"items"`
	}
	if err := json.NewDecoder(adminAuditRes.Body).Decode(&auditOut); err != nil {
		t.Fatal(err)
	}

	foundLogout := false
	for _, item := range auditOut.Items {
		if item.Action == "logout" {
			foundLogout = true
			if item.ResourceType != "session" || item.ActorType != "user" || item.ActorID != basicUser.ID {
				t.Fatalf("unexpected logout audit log: %+v", item)
			}
		}
	}
	if !foundLogout {
		t.Fatal("expected logout audit log")
	}

	userAuditReq := httptest.NewRequest(http.MethodGet, "/api/audit-logs", nil)
	applyCookies(userAuditReq, userCookies)
	userAuditRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(userAuditRes, userAuditReq)
	if userAuditRes.Code != http.StatusForbidden {
		t.Fatalf("expected basic user audit logs 403, got %d", userAuditRes.Code)
	}
}

func TestTunnelPortSuggestionFollowsProtocolRanges(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "port-plan-node", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, UDPRelay: true, HTTPRelay: true, SOCKS5Connect: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	for _, body := range []map[string]any{
		{"id": "tcp-plan-a", "nodeId": registerOut.NodeID, "name": "tcp-a", "type": "tcp", "targetHost": "127.0.0.1", "targetPort": 8080, "publicPort": 20000, "status": "active"},
		{"id": "udp-plan-a", "nodeId": registerOut.NodeID, "name": "udp-a", "type": "udp", "targetHost": "127.0.0.1", "targetPort": 19002, "publicPort": 21000, "status": "active"},
	} {
		payload, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(payload))
		applyCookies(req, adminCookies)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusCreated {
			t.Fatalf("expected create status 201, got %d", res.Code)
		}
	}

	tcpReq := httptest.NewRequest(http.MethodGet, "/api/tunnel-port-suggestion?type=tcp", nil)
	applyCookies(tcpReq, adminCookies)
	tcpRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(tcpRes, tcpReq)
	if tcpRes.Code != http.StatusOK {
		t.Fatalf("expected tcp suggestion 200, got %d", tcpRes.Code)
	}
	var tcpOut types.TunnelPortSuggestionResponse
	if err := json.NewDecoder(tcpRes.Body).Decode(&tcpOut); err != nil {
		t.Fatal(err)
	}
	if tcpOut.Suggested != 20001 {
		t.Fatalf("expected tcp suggestion 20001, got %d", tcpOut.Suggested)
	}

	udpReq := httptest.NewRequest(http.MethodGet, "/api/tunnel-port-suggestion?type=udp", nil)
	applyCookies(udpReq, adminCookies)
	udpRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(udpRes, udpReq)
	if udpRes.Code != http.StatusOK {
		t.Fatalf("expected udp suggestion 200, got %d", udpRes.Code)
	}
	var udpOut types.TunnelPortSuggestionResponse
	if err := json.NewDecoder(udpRes.Body).Decode(&udpOut); err != nil {
		t.Fatal(err)
	}
	if udpOut.Suggested != 21001 {
		t.Fatalf("expected udp suggestion 21001, got %d", udpOut.Suggested)
	}
}

func TestTunnelPortSuggestionSharesHTTPFamilyRange(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "http-family-node", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{HTTPRelay: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	httpBody, _ := json.Marshal(map[string]any{"id": "http-plan-a", "nodeId": registerOut.NodeID, "name": "http-a", "type": "http", "targetHost": "127.0.0.1", "targetPort": 8080, "publicPort": 22000, "status": "active"})
	httpReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(httpBody))
	applyCookies(httpReq, adminCookies)
	httpRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(httpRes, httpReq)
	if httpRes.Code != http.StatusCreated {
		t.Fatalf("expected http create 201, got %d", httpRes.Code)
	}

	httpsReq := httptest.NewRequest(http.MethodGet, "/api/tunnel-port-suggestion?type=https", nil)
	applyCookies(httpsReq, adminCookies)
	httpsRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(httpsRes, httpsReq)
	if httpsRes.Code != http.StatusOK {
		t.Fatalf("expected https suggestion 200, got %d", httpsRes.Code)
	}
	var httpsOut types.TunnelPortSuggestionResponse
	if err := json.NewDecoder(httpsRes.Body).Decode(&httpsOut); err != nil {
		t.Fatal(err)
	}
	if httpsOut.Suggested != 22001 {
		t.Fatalf("expected https suggestion 22001, got %d", httpsOut.Suggested)
	}
	if httpsOut.Plan.RangeStart != 22000 || httpsOut.Plan.RangeEnd != 22999 {
		t.Fatalf("unexpected https plan range: %+v", httpsOut.Plan)
	}
}

func TestTunnelPortSuggestionAvoidsActualConflictGroup(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeName: "conflict-plan-node", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true, SOCKS5Connect: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	var registerOut types.NodeRegisterResponse
	if err := json.NewDecoder(registerRes.Body).Decode(&registerOut); err != nil {
		t.Fatal(err)
	}

	tcpBody, _ := json.Marshal(map[string]any{"id": "tcp-plan-b", "nodeId": registerOut.NodeID, "name": "tcp-b", "type": "tcp", "targetHost": "127.0.0.1", "targetPort": 8081, "publicPort": 23000, "status": "active"})
	tcpReq := httptest.NewRequest(http.MethodPost, "/api/tunnels", bytes.NewReader(tcpBody))
	applyCookies(tcpReq, adminCookies)
	tcpRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(tcpRes, tcpReq)
	if tcpRes.Code != http.StatusCreated {
		t.Fatalf("expected tcp create 201, got %d", tcpRes.Code)
	}

	socksReq := httptest.NewRequest(http.MethodGet, "/api/tunnel-port-suggestion?type=socks5", nil)
	applyCookies(socksReq, adminCookies)
	socksRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(socksRes, socksReq)
	if socksRes.Code != http.StatusOK {
		t.Fatalf("expected socks5 suggestion 200, got %d", socksRes.Code)
	}
	var socksOut types.TunnelPortSuggestionResponse
	if err := json.NewDecoder(socksRes.Body).Decode(&socksOut); err != nil {
		t.Fatal(err)
	}
	if socksOut.Suggested != 23001 {
		t.Fatalf("expected socks5 suggestion 23001 to avoid tcp conflict, got %d", socksOut.Suggested)
	}
}

func TestControlActionNodePreflightBranches(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerNode := func(nodeID, nodeName string, metadata map[string]string) {
		body, _ := json.Marshal(types.NodeRegisterRequest{NodeID: nodeID, NodeName: nodeName, AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true}, Metadata: metadata})
		req := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(body))
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("expected register 200, got %d", res.Code)
		}
	}

	registerNode("node-managed", "node-managed", map[string]string{"deploymentMode": "managed", "serviceUnit": "cloud-relay-client-agent@node-managed.service", "instanceProfile": "node-managed", "instanceManaged": "true"})
	registerNode("node-unmanaged", "node-unmanaged", map[string]string{"deploymentMode": "manual", "instanceManaged": "false"})
	registerNode("node-missing-service", "node-missing-service", map[string]string{"deploymentMode": "managed", "instanceProfile": "node-missing-service", "instanceManaged": "true"})
	registerNode("node-missing-profile", "node-missing-profile", map[string]string{"deploymentMode": "managed", "serviceUnit": "cloud-relay-client-agent@node-missing-profile.service", "instanceManaged": "true"})
	registerNode("node-missing-deploy", "node-missing-deploy", map[string]string{"serviceUnit": "cloud-relay-client-agent@node-missing-deploy.service", "instanceProfile": "node-missing-deploy", "instanceManaged": "true"})
	registerNode("node-isolated", "node-isolated", map[string]string{"deploymentMode": "managed", "serviceUnit": "cloud-relay-client-agent@node-isolated.service", "instanceProfile": "node-isolated", "instanceManaged": "true", "isolated": "true"})

	offline, err := server.store.GetNode(context.Background(), "node-managed")
	if err != nil {
		t.Fatal(err)
	}
	offline.NodeID = "node-offline"
	offline.Status = "offline"
	server.store = nodeOverrideStore{Store: server.store, overrides: map[string]types.NodeSummary{"node-offline": offline}}

	assertAction := func(name string, payload map[string]any, wantResult types.ControlResult, wantReason types.ControlReasonCode, wantMissingCode string, wantBlockedReasons bool) {
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/control-actions", bytes.NewReader(body))
		applyCookies(req, adminCookies)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK && res.Code != http.StatusNotFound {
			t.Fatalf("%s: unexpected status %d", name, res.Code)
		}
		var out types.ControlActionResponse
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("%s: decode failed: %v", name, err)
		}
		if out.Result != wantResult {
			t.Fatalf("%s: expected result %s, got %s", name, wantResult, out.Result)
		}
		if wantBlockedReasons && len(out.Preflight.BlockedReasons) == 0 {
			t.Fatalf("%s: expected blocked reasons, got none", name)
		}
		if !wantBlockedReasons && len(out.Preflight.BlockedReasons) != 0 {
			t.Fatalf("%s: expected no blocked reasons, got %+v", name, out.Preflight.BlockedReasons)
		}
		if wantReason != "" {
			found := false
			for _, item := range out.Preflight.BlockedReasons {
				if item.Code == wantReason {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s: expected blocked reason %s, got %+v", name, wantReason, out.Preflight.BlockedReasons)
			}
		}
		if wantMissingCode != "" {
			found := false
			for _, item := range out.Preflight.Items {
				if item.Code == wantMissingCode && item.State == types.ControlCheckMissing {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s: expected missing check for %s, got %+v", name, wantMissingCode, out.Preflight.Items)
			}
		}
	}

	assertAction("restart accepted dry-run", map[string]any{"actionKind": "restart_agent", "targetKind": "node", "targetId": "node-managed", "sourceSurface": "node_console", "dryRun": true}, types.ControlResultAccepted, "", "", false)
	assertAction("isolate blocked on node console surface", map[string]any{"actionKind": "isolate_node", "targetKind": "node", "targetId": "node-managed", "sourceSurface": "node_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonUnsupportedSurface, "", true)
	assertAction("release blocked on node console surface", map[string]any{"actionKind": "release_node", "targetKind": "node", "targetId": "node-isolated", "sourceSurface": "node_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonUnsupportedSurface, "", true)
	assertAction("restart offline blocked", map[string]any{"actionKind": "restart_agent", "targetKind": "node", "targetId": "node-offline", "sourceSurface": "node_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonNodeOffline, "", true)
	assertAction("restart unmanaged blocked with missing signal", map[string]any{"actionKind": "restart_agent", "targetKind": "node", "targetId": "node-unmanaged", "sourceSurface": "node_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonUnmanagedInstance, "managed", true)
	assertAction("missing service unit blocked with missing signal", map[string]any{"actionKind": "restart_agent", "targetKind": "node", "targetId": "node-missing-service", "sourceSurface": "operator_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonMissingServiceUnit, "serviceUnit", true)
	assertAction("missing profile blocked with missing signal", map[string]any{"actionKind": "restart_agent", "targetKind": "node", "targetId": "node-missing-profile", "sourceSurface": "operator_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonMissingInstanceProfile, "instanceProfile", true)
	assertAction("missing deployment blocked with missing signal", map[string]any{"actionKind": "restart_agent", "targetKind": "node", "targetId": "node-missing-deploy", "sourceSurface": "operator_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonMissingDeploymentMode, "deployment", true)
	assertAction("unsupported surface blocked", map[string]any{"actionKind": "restart_agent", "targetKind": "node", "targetId": "node-managed", "sourceSurface": "bad_surface", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonUnsupportedSurface, "", true)
	assertAction("unsupported action blocked", map[string]any{"actionKind": "delete_node", "targetKind": "node", "targetId": "node-managed", "sourceSurface": "operator_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonUnsupportedAction, "", true)
	assertAction("isolate blocked when isolated", map[string]any{"actionKind": "isolate_node", "targetKind": "node", "targetId": "node-isolated", "sourceSurface": "operator_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonNodeIsolated, "", true)
	assertAction("release blocked when not isolated", map[string]any{"actionKind": "release_node", "targetKind": "node", "targetId": "node-managed", "sourceSurface": "operator_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonNodeNotIsolated, "", true)
	assertAction("release accepted when isolated", map[string]any{"actionKind": "release_node", "targetKind": "node", "targetId": "node-isolated", "sourceSurface": "operator_console", "dryRun": true}, types.ControlResultAccepted, "", "", false)
}

func TestControlActionTunnelPreflightAndPlaceholderExecute(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerBody, _ := json.Marshal(types.NodeRegisterRequest{NodeID: "node-control", NodeName: "node-control", AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true}})
	registerReq := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(registerBody))
	registerRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerRes, registerReq)
	if registerRes.Code != http.StatusOK {
		t.Fatalf("expected register 200, got %d", registerRes.Code)
	}

	for _, item := range []types.TunnelSpec{
		{ID: "tunnel-active", NodeID: "node-control", Name: "tunnel-active", Type: "tcp", Status: "active", TargetHost: "127.0.0.1", TargetPort: 8080, PublicPort: 20010, Metadata: map[string]string{"nodeId": "node-control"}},
		{ID: "tunnel-paused", NodeID: "node-control", Name: "tunnel-paused", Type: "tcp", Status: "paused", TargetHost: "127.0.0.1", TargetPort: 8081, PublicPort: 20011, Metadata: map[string]string{"nodeId": "node-control"}},
	} {
		if _, err := server.store.CreateTunnel(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}

	assertTunnel := func(name string, payload map[string]any, wantResult types.ControlResult, wantReason types.ControlReasonCode, wantExecutionMode types.ControlExecutionMode, wantDryRun bool, wantPlaceholderOnly bool, wantExecutionNote bool, wantBlockedReasons bool) {
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/control-actions", bytes.NewReader(body))
		applyCookies(req, adminCookies)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK && res.Code != http.StatusNotFound {
			t.Fatalf("%s: unexpected status %d", name, res.Code)
		}
		var out types.ControlActionResponse
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("%s: decode failed: %v", name, err)
		}
		if out.Result != wantResult {
			t.Fatalf("%s: expected result %s, got %s", name, wantResult, out.Result)
		}
		if out.ExecutionMode != wantExecutionMode {
			t.Fatalf("%s: expected execution mode %s, got %s", name, wantExecutionMode, out.ExecutionMode)
		}
		if out.DryRunOnly != wantDryRun {
			t.Fatalf("%s: expected dryRunOnly=%t, got %t", name, wantDryRun, out.DryRunOnly)
		}
		if out.PlaceholderOnly != wantPlaceholderOnly {
			t.Fatalf("%s: expected placeholderOnly=%t, got %t", name, wantPlaceholderOnly, out.PlaceholderOnly)
		}
		if wantExecutionNote && len(out.ExecutionNotes) == 0 {
			t.Fatalf("%s: expected execution notes, got none", name)
		}
		if !wantExecutionNote && len(out.ExecutionNotes) != 0 {
			t.Fatalf("%s: expected no execution notes, got %+v", name, out.ExecutionNotes)
		}
		if wantBlockedReasons && len(out.Preflight.BlockedReasons) == 0 {
			t.Fatalf("%s: expected blocked reasons, got none", name)
		}
		if !wantBlockedReasons && len(out.Preflight.BlockedReasons) != 0 {
			t.Fatalf("%s: expected no blocked reasons, got %+v", name, out.Preflight.BlockedReasons)
		}
		if wantReason != "" {
			found := false
			for _, item := range out.Preflight.BlockedReasons {
				if item.Code == wantReason {
					found = true
					break
				}
			}
			if !found {
				for _, item := range out.ExecutionNotes {
					if item.Code == wantReason {
						found = true
						break
					}
				}
			}
			if !found {
				t.Fatalf("%s: expected reason %s, got blocked=%+v execution=%+v", name, wantReason, out.Preflight.BlockedReasons, out.ExecutionNotes)
			}
		}
	}

	assertTunnel("tunnel target missing", map[string]any{"actionKind": "pause_tunnel", "targetKind": "tunnel", "targetId": "missing-tunnel", "sourceSurface": "operator_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonTargetNotFound, types.ControlExecutionPlaceholder, true, false, false, true)
	assertTunnel("active pause accepted", map[string]any{"actionKind": "pause_tunnel", "targetKind": "tunnel", "targetId": "tunnel-active", "sourceSurface": "operator_console", "dryRun": true}, types.ControlResultAccepted, "", types.ControlExecutionPlaceholder, true, false, false, false)
	assertTunnel("paused resume accepted", map[string]any{"actionKind": "resume_tunnel", "targetKind": "tunnel", "targetId": "tunnel-paused", "sourceSurface": "node_console", "dryRun": true}, types.ControlResultAccepted, "", types.ControlExecutionPlaceholder, true, false, false, false)
	assertTunnel("paused pause blocked", map[string]any{"actionKind": "pause_tunnel", "targetKind": "tunnel", "targetId": "tunnel-paused", "sourceSurface": "operator_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonTunnelStateConflict, types.ControlExecutionPlaceholder, true, false, false, true)
	assertTunnel("active resume blocked", map[string]any{"actionKind": "resume_tunnel", "targetKind": "tunnel", "targetId": "tunnel-active", "sourceSurface": "operator_console", "dryRun": true}, types.ControlResultBlocked, types.ControlReasonTunnelStateConflict, types.ControlExecutionPlaceholder, true, false, false, true)
	assertTunnel("execute placeholder accepted", map[string]any{"actionKind": "pause_tunnel", "targetKind": "tunnel", "targetId": "tunnel-active", "sourceSurface": "operator_console", "dryRun": false}, types.ControlResultAccepted, types.ControlReasonPlaceholderOnly, types.ControlExecutionPlaceholder, false, true, true, false)
	assertTunnel("execute blocked when conflict", map[string]any{"actionKind": "pause_tunnel", "targetKind": "tunnel", "targetId": "tunnel-paused", "sourceSurface": "operator_console", "dryRun": false}, types.ControlResultBlocked, types.ControlReasonTunnelStateConflict, types.ControlExecutionPlaceholder, false, false, false, true)
}

func TestControlActionOptionsNodeAndTunnel(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
	server.adminBootstrapSecret = "bootstrap-secret"
	adminCookies := bootstrapAdminAndCollectCookies(t, server)

	registerNode := func(nodeID string, metadata map[string]string) {
		body, _ := json.Marshal(types.NodeRegisterRequest{NodeID: nodeID, NodeName: nodeID, AgentVersion: "0.1.0", Capabilities: types.NodeCapabilities{TCPRelay: true}, Metadata: metadata})
		req := httptest.NewRequest(http.MethodPost, "/agent/register", bytes.NewReader(body))
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("expected register 200, got %d", res.Code)
		}
	}

	registerNode("node-managed", map[string]string{"deploymentMode": "managed", "serviceUnit": "cloud-relay-client-agent@node-managed.service", "instanceProfile": "node-managed", "instanceManaged": "true"})
	registerNode("node-isolated", map[string]string{"deploymentMode": "managed", "serviceUnit": "cloud-relay-client-agent@node-isolated.service", "instanceProfile": "node-isolated", "instanceManaged": "true", "isolated": "true"})
	registerNode("node-missing-service", map[string]string{"deploymentMode": "managed", "instanceProfile": "node-missing-service", "instanceManaged": "true"})

	for _, item := range []types.TunnelSpec{
		{ID: "tunnel-active-options", NodeID: "node-managed", Name: "tunnel-active-options", Type: "tcp", Status: "active", TargetHost: "127.0.0.1", TargetPort: 8080, PublicPort: 21010, Metadata: map[string]string{"nodeId": "node-managed"}},
		{ID: "tunnel-paused-options", NodeID: "node-managed", Name: "tunnel-paused-options", Type: "tcp", Status: "paused", TargetHost: "127.0.0.1", TargetPort: 8081, PublicPort: 21011, Metadata: map[string]string{"nodeId": "node-managed"}},
	} {
		if _, err := server.store.CreateTunnel(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}

	assertOptions := func(path string, wantStatus int, check func(types.ControlActionOptionsResponse)) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		applyCookies(req, adminCookies)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != wantStatus {
			t.Fatalf("%s: expected status %d, got %d", path, wantStatus, res.Code)
		}
		if wantStatus != http.StatusOK {
			return
		}
		var out types.ControlActionOptionsResponse
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("%s: decode failed: %v", path, err)
		}
		check(out)
	}

	assertOptions("/api/control-actions/node/node-managed/node_console/options", http.StatusOK, func(out types.ControlActionOptionsResponse) {
		if len(out.Items) != 3 {
			t.Fatalf("expected 3 node action options, got %d", len(out.Items))
		}
		if out.Items[0].ActionKind != types.ControlActionRestartAgent || !out.Items[0].Available || out.Items[0].AvailabilityState != types.ControlAvailabilityPlaceholderOnly {
			t.Fatalf("unexpected restart_agent option: %+v", out.Items[0])
		}
		if !out.Items[0].PlaceholderOnly || len(out.Items[0].ExecutionNotes) == 0 {
			t.Fatalf("expected placeholder-only restart option, got %+v", out.Items[0])
		}
		if out.Items[0].Summary == "" || out.Items[0].NextStep == "" {
			t.Fatalf("expected placeholder-only restart option to include summary and nextStep, got %+v", out.Items[0])
		}
		if out.Items[1].ActionKind != types.ControlActionIsolateNode || out.Items[1].Available || out.Items[1].AvailabilityState != types.ControlAvailabilityBlocked || out.Items[1].PrimaryReasonCode != types.ControlReasonUnsupportedSurface {
			t.Fatalf("unexpected isolate_node option: %+v", out.Items[1])
		}
		if out.Items[1].PlaceholderOnly || len(out.Items[1].ExecutionNotes) != 0 {
			t.Fatalf("expected blocked isolate option without placeholder fields, got %+v", out.Items[1])
		}
		if out.Items[1].Summary == "" || out.Items[1].NextStep == "" {
			t.Fatalf("expected blocked isolate option to include summary and nextStep, got %+v", out.Items[1])
		}
	})

	assertOptions("/api/control-actions/node/node-isolated/operator_console/options", http.StatusOK, func(out types.ControlActionOptionsResponse) {
		var releaseOpt types.ControlActionOption
		for _, item := range out.Items {
			if item.ActionKind == types.ControlActionReleaseNode {
				releaseOpt = item
			}
		}
		if !releaseOpt.Available || releaseOpt.AvailabilityState != types.ControlAvailabilityPlaceholderOnly {
			t.Fatalf("expected release_node available on isolated node, got %+v", releaseOpt)
		}
	})

	assertOptions("/api/control-actions/node/node-missing-service/operator_console/options", http.StatusOK, func(out types.ControlActionOptionsResponse) {
		var restartOpt types.ControlActionOption
		for _, item := range out.Items {
			if item.ActionKind == types.ControlActionRestartAgent {
				restartOpt = item
			}
		}
		if restartOpt.AvailabilityState != types.ControlAvailabilityBlocked || restartOpt.PrimaryReasonCode != types.ControlReasonMissingServiceUnit {
			t.Fatalf("expected missing service unit to block restart_agent, got %+v", restartOpt)
		}
		if restartOpt.PlaceholderOnly || len(restartOpt.ExecutionNotes) != 0 {
			t.Fatalf("expected blocked restart option without placeholder fields, got %+v", restartOpt)
		}
		if restartOpt.Summary == "" || restartOpt.NextStep == "" {
			t.Fatalf("expected blocked restart option to include summary and nextStep, got %+v", restartOpt)
		}
	})

	assertOptions("/api/control-actions/tunnel/tunnel-active-options/operator_console/options", http.StatusOK, func(out types.ControlActionOptionsResponse) {
		if len(out.Items) != 2 {
			t.Fatalf("expected 2 tunnel action options, got %d", len(out.Items))
		}
		if out.Items[0].ActionKind != types.ControlActionPauseTunnel || !out.Items[0].Available || out.Items[0].AvailabilityState != types.ControlAvailabilityPlaceholderOnly {
			t.Fatalf("unexpected pause_tunnel option: %+v", out.Items[0])
		}
		if !out.Items[0].PlaceholderOnly || len(out.Items[0].ExecutionNotes) == 0 {
			t.Fatalf("expected placeholder-only pause option, got %+v", out.Items[0])
		}
		if out.Items[0].Summary == "" || out.Items[0].NextStep == "" {
			t.Fatalf("expected placeholder-only pause option to include summary and nextStep, got %+v", out.Items[0])
		}
		if out.Items[1].ActionKind != types.ControlActionResumeTunnel || out.Items[1].Available || out.Items[1].AvailabilityState != types.ControlAvailabilityBlocked || out.Items[1].PrimaryReasonCode != types.ControlReasonTunnelStateConflict {
			t.Fatalf("unexpected resume_tunnel option: %+v", out.Items[1])
		}
		if out.Items[1].PlaceholderOnly || len(out.Items[1].ExecutionNotes) != 0 {
			t.Fatalf("expected blocked resume option without placeholder fields, got %+v", out.Items[1])
		}
		if out.Items[1].Summary == "" || out.Items[1].NextStep == "" {
			t.Fatalf("expected blocked resume option to include summary and nextStep, got %+v", out.Items[1])
		}
	})

	assertOptions("/api/control-actions/tunnel/tunnel-paused-options/node_console/options", http.StatusOK, func(out types.ControlActionOptionsResponse) {
		var resumeOpt types.ControlActionOption
		for _, item := range out.Items {
			if item.ActionKind == types.ControlActionResumeTunnel {
				resumeOpt = item
			}
		}
		if !resumeOpt.Available || resumeOpt.AvailabilityState != types.ControlAvailabilityPlaceholderOnly {
			t.Fatalf("expected resume_tunnel available, got %+v", resumeOpt)
		}
		if !resumeOpt.PlaceholderOnly || len(resumeOpt.ExecutionNotes) == 0 {
			t.Fatalf("expected placeholder-only resume option, got %+v", resumeOpt)
		}
		if resumeOpt.Summary == "" || resumeOpt.NextStep == "" {
			t.Fatalf("expected placeholder-only resume option to include summary and nextStep, got %+v", resumeOpt)
		}
	})
}
