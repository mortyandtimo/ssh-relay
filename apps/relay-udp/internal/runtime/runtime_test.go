package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

func TestUDPRelayServiceForwardsDatagramViaReverseAgentConnection(t *testing.T) {
	tunnel := types.TunnelSpec{ID: "udp-1", NodeID: "node-udp-1", Name: "udp-echo", Type: "udp", Status: "active", PublicPort: freeUDPPort(t), TargetHost: "127.0.0.1", TargetPort: 19001}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/routes/udp" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []types.TunnelSpec{tunnel}})
	}))
	defer api.Close()
	service := NewService(api.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = service.Run(ctx) }()

	relayHTTP := httptest.NewServer(http.HandlerFunc(service.HandleAgentReverse))
	defer relayHTTP.Close()
	requireEventuallyListening(t, service, tunnel.PublicPort)

	agentResult := make(chan error, 1)
	go func() {
		agentConn, err := dialUDPUpgrade(relayHTTP.URL+types.AgentUDPRelayConnectPath, types.AgentUDPRelayHello{NodeID: tunnel.NodeID, TunnelID: tunnel.ID, PublicPort: tunnel.PublicPort, TargetHost: tunnel.TargetHost, TargetPort: tunnel.TargetPort})
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
			agentResult <- fmt.Errorf("unexpected udp start byte %d", start[0])
			return
		}
		frame, err := readUDPFrame(bufio.NewReader(agentConn))
		if err != nil {
			agentResult <- err
			return
		}
		if string(frame.Payload) != "ping" {
			agentResult <- fmt.Errorf("unexpected udp payload %q", string(frame.Payload))
			return
		}
		if err := writeUDPFrame(agentConn, types.UDPDatagramFrame{SessionID: frame.SessionID, Payload: []byte("pong")}); err != nil {
			agentResult <- err
			return
		}
		agentResult <- nil
	}()

	requireEventuallySession(t, service, tunnel)
	clientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: tunnel.PublicPort})
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	if _, err := clientConn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	if err := clientConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, _, err := clientConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "pong" {
		t.Fatalf("unexpected udp relay response %q", string(buf[:n]))
	}
	if err := <-agentResult; err != nil {
		t.Fatal(err)
	}
}

func dialUDPUpgrade(relayURL string, hello types.AgentUDPRelayHello) (net.Conn, error) {
	parsed, err := neturlParse(relayURL)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("tcp", parsed.hostPort, 5*time.Second)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(hello)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	request := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", parsed.path, parsed.hostHeader, types.AgentUDPRelayUpgrade, len(body), body)
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
	t.Fatalf("udp listener on port %d did not become ready", port)
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).Port
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

func requireEventuallySession(t *testing.T, service *Service, tunnel types.TunnelSpec) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	key := routeKey(tunnel)
	for time.Now().Before(deadline) {
		service.mu.Lock()
		_, ok := service.sessions[key]
		service.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("udp session for tunnel %s did not become ready", tunnel.ID)
}
