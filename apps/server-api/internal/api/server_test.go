package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/25743/cloud-relay-platform/apps/server-api/internal/store"
	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

func TestRegisterHeartbeatTunnelAndMetrics(t *testing.T) {
	server := NewServer("test", store.NewInMemoryStore(), "")
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
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected bootstrap status 201, got %d", res.Code)
	}
	return res.Result().Cookies()
}
