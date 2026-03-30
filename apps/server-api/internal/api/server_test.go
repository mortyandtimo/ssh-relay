package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/25743/cloud-relay-platform/apps/server-api/internal/store"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

func TestRegisterHeartbeatTunnelAndMetrics(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore())

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
	tunnelRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(tunnelRes, tunnelReq)
	if tunnelRes.Code != http.StatusCreated {
		t.Fatalf("expected tunnel status 201, got %d", tunnelRes.Code)
	}

	nodesReq := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
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
	server := NewServer("test", store.NewInMemoryStore())

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
	updateRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d", updateRes.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/tunnels/tunnel-a", nil)
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
	deleteRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("expected delete status 200, got %d", deleteRes.Code)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/api/tunnels/tunnel-a", nil)
	missingRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusNotFound {
		t.Fatalf("expected missing status 404, got %d", missingRes.Code)
	}
}
