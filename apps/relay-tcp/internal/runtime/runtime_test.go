package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

func TestTCPRelayServiceForwardsTrafficViaReverseAgentConnection(t *testing.T) {
	tunnel := types.TunnelSpec{
		ID:         "t-1",
		NodeID:     "node-1",
		Name:       "echo",
		Type:       "tcp",
		Status:     "active",
		PublicPort: freePort(t),
		TargetHost: "127.0.0.1",
		TargetPort: 23546,
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/routes/tcp" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []types.TunnelSpec{tunnel}})
	}))
	defer api.Close()

	service := NewService(api.URL)
	service.acquireTimeout = 2 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = service.Run(ctx)
	}()

	relayHTTP := httptest.NewServer(http.HandlerFunc(service.HandleAgentReverse))
	defer relayHTTP.Close()

	requireEventuallyListening(t, service, tunnel.PublicPort)

	agentResult := make(chan error, 1)
	go func() {
		agentConn, err := dialTestUpgrade(relayHTTP.URL+types.AgentRelayConnectPath, types.AgentRelayHello{
			NodeID:     tunnel.NodeID,
			TunnelID:   tunnel.ID,
			PublicPort: tunnel.PublicPort,
			TargetHost: tunnel.TargetHost,
			TargetPort: tunnel.TargetPort,
		})
		if err != nil {
			agentResult <- err
			return
		}
		defer agentConn.Close()

		start := make([]byte, 1)
		if _, err := io.ReadFull(agentConn, start); err != nil {
			agentResult <- err
			return
		}
		if start[0] != types.AgentRelayStartByte {
			agentResult <- fmt.Errorf("unexpected start byte %d", start[0])
			return
		}

		buf := make([]byte, 64)
		n, err := agentConn.Read(buf)
		if err != nil {
			agentResult <- err
			return
		}
		if string(buf[:n]) != "hello\n" {
			agentResult <- fmt.Errorf("unexpected relay payload %q", string(buf[:n]))
			return
		}
		if _, err := io.WriteString(agentConn, "echo:hello\n"); err != nil {
			agentResult <- err
			return
		}
		agentResult <- nil
	}()

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", tunnel.PublicPort)), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "hello\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "echo:hello\n" {
		t.Fatalf("unexpected relay response: %q", string(buf[:n]))
	}
	if err := <-agentResult; err != nil {
		t.Fatal(err)
	}
}

func TestHandleAgentReverseRejectsInactiveTunnel(t *testing.T) {
	service := NewService("http://127.0.0.1:1")
	req := httptest.NewRequest(http.MethodPost, types.AgentRelayConnectPath, bytes.NewReader([]byte(`{"nodeId":"node-1","tunnelId":"t-1","publicPort":10086,"targetHost":"127.0.0.1","targetPort":23546}`)))
	req.Header.Set("Upgrade", types.AgentRelayUpgrade)
	res := httptest.NewRecorder()

	service.HandleAgentReverse(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.Code)
	}
}

func TestPublicConnectionFailsWithoutStandbyAgentConnection(t *testing.T) {
	tunnel := types.TunnelSpec{
		ID:         "t-2",
		NodeID:     "node-2",
		Type:       "tcp",
		Status:     "active",
		PublicPort: freePort(t),
		TargetHost: "127.0.0.1",
		TargetPort: 29999,
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []types.TunnelSpec{tunnel}})
	}))
	defer api.Close()

	service := NewService(api.URL)
	service.acquireTimeout = 300 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = service.Run(ctx)
	}()

	requireEventuallyListening(t, service, tunnel.PublicPort)

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", tunnel.PublicPort)), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "hello\n"); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("expected read failure when no standby agent connection exists")
	}
}

func TestEnqueueStandbyConnMaintainsStrictPerKeyCap(t *testing.T) {
	service := NewService("http://127.0.0.1:1")
	key := routePoolKey("node-1", 10086)
	var maxObserved int64
	var wg sync.WaitGroup

	for i := 0; i < standbyPoolTargetSize*3; i++ {
		serverConn, clientConn := net.Pipe()
		defer clientConn.Close()
		wg.Add(1)
		go func(idx int, conn net.Conn) {
			defer wg.Done()
			_, _, err := service.enqueueStandbyConn(key, standbyConn{
				conn: conn,
				hello: types.AgentRelayHello{
					NodeID:     "node-1",
					TunnelID:   fmt.Sprintf("t-%d", idx),
					PublicPort: 10086,
					TargetHost: "127.0.0.1",
					TargetPort: 80,
				},
				registeredAt: time.Now().UTC(),
			})
			if err != nil {
				t.Errorf("enqueue standby %d: %v", idx, err)
				return
			}
			queueLen := len(service.poolForKey(key))
			for {
				current := atomic.LoadInt64(&maxObserved)
				if int64(queueLen) <= current {
					break
				}
				if atomic.CompareAndSwapInt64(&maxObserved, current, int64(queueLen)) {
					break
				}
			}
		}(i, serverConn)
	}

	wg.Wait()
	if got := len(service.poolForKey(key)); got > standbyPoolTargetSize {
		t.Fatalf("expected queue length <= %d, got %d", standbyPoolTargetSize, got)
	}
	if maxObserved > int64(standbyPoolTargetSize) {
		t.Fatalf("expected max observed queue <= %d, got %d", standbyPoolTargetSize, maxObserved)
	}
	drainStandbyQueue(service.poolForKey(key))
}

func dialTestUpgrade(relayURL string, hello types.AgentRelayHello) (net.Conn, error) {
	parsedURL, err := neturlParse(relayURL)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("tcp", parsedURL.hostPort, 5*time.Second)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(hello)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	request := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", parsedURL.path, parsedURL.hostHeader, types.AgentRelayUpgrade, len(body), body)
	if _, err := io.WriteString(conn, request); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodPost})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = conn.Close()
		return nil, fmt.Errorf("upgrade failed with status %s: %s", resp.Status, string(payload))
	}
	return &testBufferedConn{Conn: conn, reader: reader}, nil
}

type parsedNetURL struct {
	hostPort   string
	hostHeader string
	path       string
}

func neturlParse(raw string) (parsedNetURL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return parsedNetURL{}, err
	}
	hostPort := parsed.Host
	if _, _, err := net.SplitHostPort(hostPort); err != nil {
		hostPort = net.JoinHostPort(parsed.Hostname(), "80")
	}
	path := parsed.Path
	if path == "" {
		path = "/"
	}
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	return parsedNetURL{hostPort: hostPort, hostHeader: parsed.Host, path: path}, nil
}

type testBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *testBufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func requireEventuallyListening(t *testing.T, service *Service, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		service.mu.Lock()
		_, ok := service.listeners[port]
		service.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("listener on port %d did not become ready in service state", port)
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var out int
	if _, err := fmt.Sscanf(port, "%d", &out); err != nil {
		t.Fatal(err)
	}
	return out
}
