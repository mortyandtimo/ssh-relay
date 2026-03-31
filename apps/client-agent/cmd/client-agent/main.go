package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

const (
	agentVersion           = "0.1.0"
	tunnelPollInterval     = 5 * time.Second
	defaultReversePoolSize = 2
	defaultRelayTimeout    = 10 * time.Second
)

type reverseManager struct {
	baseURL       string
	relayURL      string
	httpClient    *http.Client
	poolSize      int
	connectTimout time.Duration

	mu            sync.Mutex
	workers       map[string]managedTunnel
	active        int64
	activeTunnels int64
}

type managedTunnel struct {
	spec   types.TunnelSpec
	cancel context.CancelFunc
}

func main() {
	once := flag.Bool("once", false, "register and send a single heartbeat")
	flag.Parse()

	baseURL := config.GetEnv("CLOUD_RELAY_API_URL", "http://localhost:8080")
	relayURL := config.GetEnv("RELAY_TCP_CONNECT_URL", deriveRelayURL(baseURL))
	nodeName := config.GetEnv("CLIENT_NODE_NAME", defaultNodeName())
	nodeID := config.GetEnv("CLIENT_NODE_ID", "")
	heartbeatEvery := config.GetDurationEnvSeconds("AGENT_HEARTBEAT_INTERVAL", 30)
	reversePoolSize := config.GetIntEnv("AGENT_REVERSE_POOL_SIZE", defaultReversePoolSize)
	if reversePoolSize < 1 {
		reversePoolSize = 1
	}

	client := &http.Client{Timeout: 10 * time.Second}
	registeredID, err := register(client, baseURL, nodeID, nodeName)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("agent registered as %s", registeredID)
	if *once {
		if err := heartbeat(client, baseURL, registeredID, 0); err != nil {
			log.Fatal(err)
		}
		log.Printf("heartbeat accepted for %s", registeredID)
		return
	}

	manager := &reverseManager{
		baseURL:       baseURL,
		relayURL:      relayURL,
		httpClient:    client,
		poolSize:      reversePoolSize,
		connectTimout: defaultRelayTimeout,
		workers:       make(map[string]managedTunnel),
	}

	if err := manager.syncTunnels(context.Background(), registeredID); err != nil {
		log.Printf("initial tunnel sync failed: %v", err)
	}
	if err := heartbeat(client, baseURL, registeredID, manager.ActiveTunnelCount()); err != nil {
		log.Fatal(err)
	}
	log.Printf("heartbeat accepted for %s", registeredID)

	heartbeatTicker := time.NewTicker(heartbeatEvery)
	tunnelTicker := time.NewTicker(tunnelPollInterval)
	defer heartbeatTicker.Stop()
	defer tunnelTicker.Stop()

	for {
		select {
		case <-heartbeatTicker.C:
			if err := heartbeat(client, baseURL, registeredID, manager.ActiveTunnelCount()); err != nil {
				log.Printf("heartbeat failed: %v", err)
				continue
			}
			log.Printf("heartbeat accepted for %s", registeredID)
		case <-tunnelTicker.C:
			if err := manager.syncTunnels(context.Background(), registeredID); err != nil {
				log.Printf("sync tunnels failed: %v", err)
			}
		}
	}
}

func register(client *http.Client, baseURL, nodeID, nodeName string) (string, error) {
	payload := types.NodeRegisterRequest{
		NodeID:       nodeID,
		NodeName:     nodeName,
		AgentVersion: agentVersion,
		Capabilities: types.NodeCapabilities{
			TCPRelay:   true,
			HTTPRelay:  true,
			HTTPSRelay: true,
		},
		Metadata: map[string]string{
			"hostname": defaultNodeName(),
			"os":       runtime.GOOS,
			"arch":     runtime.GOARCH,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	resp, err := client.Post(baseURL+"/agent/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("register failed with status %s", resp.Status)
	}

	var out types.NodeRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.NodeID, nil
}

func heartbeat(client *http.Client, baseURL, nodeID string, activeTunnels int) error {
	payload := types.NodeHeartbeatRequest{
		NodeID:        nodeID,
		ObservedAt:    time.Now().UTC(),
		ActiveTunnels: activeTunnels,
		Metrics: map[string]string{
			"goroutines": fmt.Sprintf("%d", runtime.NumGoroutine()),
			"pid":        fmt.Sprintf("%d", os.Getpid()),
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := client.Post(baseURL+"/agent/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("heartbeat failed with status %s", resp.Status)
	}
	return nil
}

func (m *reverseManager) ActiveCount() int {
	return int(atomic.LoadInt64(&m.active))
}

func (m *reverseManager) ActiveTunnelCount() int {
	return int(atomic.LoadInt64(&m.activeTunnels))
}

func (m *reverseManager) StopAll() {
	m.mu.Lock()
	workers := m.workers
	m.workers = make(map[string]managedTunnel)
	atomic.StoreInt64(&m.activeTunnels, 0)
	m.mu.Unlock()
	for _, worker := range workers {
		worker.cancel()
	}
}

func (m *reverseManager) syncTunnels(ctx context.Context, nodeID string) error {
	items, err := m.fetchTunnels(ctx, nodeID)
	if err != nil {
		return err
	}
	desired := make(map[string]types.TunnelSpec, len(items))
	for _, item := range items {
	if (item.Type != "tcp" && item.Type != "socks5") || item.Status != "active" || item.PublicPort == 0 {
			continue
		}
		desired[item.ID] = item
	}

	toStart := make([]struct {
		ctx    context.Context
		tunnel types.TunnelSpec
	}, 0)
	toStop := make([]managedTunnel, 0)

	m.mu.Lock()
	atomic.StoreInt64(&m.activeTunnels, int64(len(desired)))
	for id, worker := range m.workers {
		tunnel, ok := desired[id]
		if ok && sameTunnel(worker.spec, tunnel) {
			continue
		}
		delete(m.workers, id)
		toStop = append(toStop, worker)
	}
	for id, tunnel := range desired {
		if _, ok := m.workers[id]; ok {
			continue
		}
		workerCtx, cancel := context.WithCancel(context.Background())
		m.workers[id] = managedTunnel{spec: tunnel, cancel: cancel}
		toStart = append(toStart, struct {
			ctx    context.Context
			tunnel types.TunnelSpec
		}{ctx: workerCtx, tunnel: tunnel})
	}
	m.mu.Unlock()

	for _, worker := range toStop {
		worker.cancel()
	}
	for _, start := range toStart {
		for slot := 0; slot < m.poolSize; slot++ {
			go m.runTunnelWorker(start.ctx, start.tunnel, slot)
		}
	}
	return nil
}

func (m *reverseManager) fetchTunnels(ctx context.Context, nodeID string) ([]types.TunnelSpec, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+"/agent/tunnels?nodeId="+url.QueryEscape(nodeID), nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent tunnel query failed with status %s", resp.Status)
	}
	var payload struct {
		Items []types.TunnelSpec `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Items, nil
}

func (m *reverseManager) runTunnelWorker(ctx context.Context, tunnel types.TunnelSpec, slot int) {
	log.Printf("reverse tunnel worker started: tunnel=%s slot=%d publicPort=%d target=%s:%d", tunnel.ID, slot, tunnel.PublicPort, tunnel.TargetHost, tunnel.TargetPort)
	for {
		if ctx.Err() != nil {
			log.Printf("reverse tunnel worker stopped: tunnel=%s slot=%d", tunnel.ID, slot)
			return
		}
		if err := m.openReverseSession(ctx, tunnel); err != nil {
			if ctx.Err() != nil {
				log.Printf("reverse tunnel worker stopped: tunnel=%s slot=%d", tunnel.ID, slot)
				return
			}
			log.Printf("reverse session failed for tunnel=%s slot=%d publicPort=%d: %v", tunnel.ID, slot, tunnel.PublicPort, err)
			select {
			case <-ctx.Done():
				log.Printf("reverse tunnel worker stopped: tunnel=%s slot=%d", tunnel.ID, slot)
				return
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (m *reverseManager) openReverseSession(ctx context.Context, tunnel types.TunnelSpec) error {
	atomic.AddInt64(&m.active, 1)
	defer atomic.AddInt64(&m.active, -1)

	conn, err := dialRelayUpgrade(ctx, m.relayURL, types.AgentRelayHello{
		NodeID:     tunnel.NodeID,
		TunnelID:   tunnel.ID,
		PublicPort: tunnel.PublicPort,
		TargetHost: tunnel.TargetHost,
		TargetPort: tunnel.TargetPort,
	})
	if err != nil {
		return err
	}
	defer conn.Close()
	enableTCPKeepalive(conn)
	stopRelayCancel := closeConnOnCancel(ctx, conn)
	defer stopRelayCancel()
	stopKeepalive := startRelayKeepalive(ctx, conn)
	defer stopKeepalive()

	if err := waitForStart(ctx, conn); err != nil {
		return err
	}
	stopKeepalive()

	if tunnel.Type == "socks5" {
		return serveSOCKS5(ctx, conn)
	}

	targetConn, err := (&net.Dialer{Timeout: m.connectTimout}).DialContext(ctx, "tcp", net.JoinHostPort(tunnel.TargetHost, fmt.Sprintf("%d", tunnel.TargetPort)))
	if err != nil {
		return fmt.Errorf("dial target %s:%d: %w", tunnel.TargetHost, tunnel.TargetPort, err)
	}
	defer targetConn.Close()
	stopTargetCancel := closeConnOnCancel(ctx, targetConn)
	defer stopTargetCancel()

	proxyReverseConnection(conn, targetConn)
	return nil
}

func dialRelayUpgrade(ctx context.Context, relayURL string, hello types.AgentRelayHello) (net.Conn, error) {
	parsed, err := url.Parse(relayURL)
	if err != nil {
		return nil, err
	}
	host := parsed.Host
	if !strings.Contains(host, ":") {
		if parsed.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	body, err := json.Marshal(hello)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	path := parsed.Path
	if path == "" {
		path = types.AgentRelayConnectPath
	}
	request := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", path, parsed.Host, types.AgentRelayUpgrade, len(body), body)
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
		return nil, fmt.Errorf("relay upgrade failed with status %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

func waitForStart(ctx context.Context, conn net.Conn) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}
	defer conn.SetReadDeadline(time.Time{})
	buf := make([]byte, 1)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if buf[0] != types.AgentRelayStartByte {
		return fmt.Errorf("unexpected reverse start byte %d", buf[0])
	}
	return nil
}

func proxyReverseConnection(relayConn net.Conn, targetConn net.Conn) {
	errCh := make(chan error, 2)
	go func() {
		_, copyErr := io.Copy(targetConn, relayConn)
		errCh <- copyErr
		closeWrite(targetConn)
	}()
	go func() {
		_, copyErr := io.Copy(relayConn, targetConn)
		errCh <- copyErr
		closeWrite(relayConn)
	}()
	firstErr := <-errCh
	secondErr := <-errCh
	if firstErr != nil && !errors.Is(firstErr, io.EOF) {
		log.Printf("reverse relay first copy ended with error: %v", firstErr)
	}
	if secondErr != nil && !errors.Is(secondErr, io.EOF) {
		log.Printf("reverse relay second copy ended with error: %v", secondErr)
	}
}

func serveSOCKS5(ctx context.Context, relayConn net.Conn) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = relayConn.SetDeadline(deadline)
	}
	reader := bufio.NewReader(relayConn)

	version, err := reader.ReadByte()
	if err != nil {
		return err
	}
	if version != 0x05 {
		return fmt.Errorf("unsupported socks version %d", version)
	}
	methodCount, err := reader.ReadByte()
	if err != nil {
		return err
	}
	methods := make([]byte, int(methodCount))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	if _, err := relayConn.Write([]byte{0x05, 0x00}); err != nil {
		return err
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return fmt.Errorf("invalid request version %d", header[0])
	}
	if header[1] != 0x01 {
		_, _ = relayConn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return fmt.Errorf("unsupported socks command %d", header[1])
	}

	targetHost, err := readSOCKSAddress(reader, header[3])
	if err != nil {
		_, _ = relayConn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return err
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return err
	}
	targetPort := int(portBytes[0])<<8 | int(portBytes[1])

	dialer := &net.Dialer{Timeout: defaultRelayTimeout}
	targetConn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(targetHost, fmt.Sprintf("%d", targetPort)))
	if err != nil {
		_, _ = relayConn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return fmt.Errorf("dial socks target %s:%d: %w", targetHost, targetPort, err)
	}
	defer targetConn.Close()

	if _, err := relayConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	proxyReverseConnection(&bufferedConn{Conn: relayConn, reader: reader}, targetConn)
	return nil
}

func readSOCKSAddress(reader *bufio.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	case 0x03:
		length, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		buf := make([]byte, int(length))
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return string(buf), nil
	case 0x04:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	default:
		return "", fmt.Errorf("unsupported socks address type %d", atyp)
	}
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *bufferedConn) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return nil
}

func closeWrite(conn net.Conn) {
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
}

func closeConnOnCancel(ctx context.Context, conn net.Conn) func() {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
			_ = conn.Close()
		case <-stop:
		}
	}()
	return func() {
		close(stop)
	}
}

func startRelayKeepalive(ctx context.Context, conn net.Conn) func() {
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				if _, err := conn.Write([]byte{types.AgentRelayKeepaliveByte}); err != nil {
					return
				}
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(stop)
		})
	}
}

func enableTCPKeepalive(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}
}

func deriveRelayURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "http://127.0.0.1:9090" + types.AgentRelayConnectPath
	}
	host := parsed.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	return (&url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, "9090"),
		Path:   types.AgentRelayConnectPath,
	}).String()
}

func sameTunnel(left, right types.TunnelSpec) bool {
	return left.ID == right.ID &&
		left.NodeID == right.NodeID &&
		left.PublicPort == right.PublicPort &&
		left.TargetHost == right.TargetHost &&
		left.TargetPort == right.TargetPort &&
		left.Status == right.Status
}

func defaultNodeName() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return "local-node"
	}
	return hostname
}
