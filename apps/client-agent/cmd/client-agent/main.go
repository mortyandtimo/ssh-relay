package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
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
	agentVersion              = "0.1.0"
	tunnelPollInterval        = 5 * time.Second
	defaultReversePoolSize    = 16
	defaultWebReversePoolSize = 32
	defaultRelayTimeout       = 10 * time.Second
)

type reverseManager struct {
	baseURL            string
	relayURL           string
	udpRelayURL        string
	relayWebURL        string
	httpClient         *http.Client
	poolSize           int
	webPoolSize        int
	connectTimout      time.Duration
	udpResponseTimeout time.Duration

	mu               sync.Mutex
	workers          map[string]managedTunnel
	httpProbeMetrics map[string]string
	tunnelTraffic    map[string]trafficCounter
	active           int64
	activeTunnels    int64
}

type managedTunnel struct {
	spec   types.TunnelSpec
	cancel context.CancelFunc
}

type trafficCounter struct {
	downBytes uint64
	upBytes   uint64
}

func main() {
	once := flag.Bool("once", false, "register and send a single heartbeat")
	flag.Parse()

	baseURL := config.GetEnv("CLOUD_RELAY_API_URL", "http://localhost:8080")
	relayURL := config.GetEnv("RELAY_TCP_CONNECT_URL", deriveRelayURL(baseURL))
	udpRelayURL := config.GetEnv("RELAY_UDP_CONNECT_URL", deriveUDPRelayURL(baseURL))
	relayWebURL := config.GetEnv("RELAY_WEB_CONNECT_URL", deriveRelayWebURL(baseURL))
	nodeName := config.GetEnv("CLIENT_NODE_NAME", defaultNodeName())
	nodeID := config.GetEnv("CLIENT_NODE_ID", "")
	deploymentMode := config.GetEnv("CLIENT_DEPLOYMENT_MODE", "managed")
	serviceUnit := config.GetEnv("CLIENT_SERVICE_UNIT", "")
	instanceProfile := config.GetEnv("CLIENT_INSTANCE_PROFILE", "")
	p2pConfig := loadP2PTelemetryConfig()
	heartbeatEvery := config.GetDurationEnvSeconds("AGENT_HEARTBEAT_INTERVAL", 30)
	reversePoolSize := config.GetIntEnv("AGENT_REVERSE_POOL_SIZE", defaultReversePoolSize)
	if reversePoolSize < 1 {
		reversePoolSize = 1
	}
	webReversePoolSize := config.GetIntEnv("AGENT_WEB_REVERSE_POOL_SIZE", defaultWebReversePoolSize)
	if webReversePoolSize < 1 {
		webReversePoolSize = 1
	}

	client := &http.Client{Timeout: 10 * time.Second}
	registeredID, err := register(client, baseURL, nodeID, nodeName, deploymentMode, serviceUnit, instanceProfile, p2pConfig)
	if err != nil {
		if *once {
			log.Fatal(err)
		}
		for {
			log.Printf("register failed: %v", err)
			time.Sleep(5 * time.Second)
			registeredID, err = register(client, baseURL, nodeID, nodeName, deploymentMode, serviceUnit, instanceProfile, p2pConfig)
			if err == nil {
				break
			}
		}
	}
	log.Printf("agent registered as %s", registeredID)
	if *once {
		if err := heartbeat(client, baseURL, registeredID, 0, map[string]string{}, p2pConfig); err != nil {
			log.Fatal(err)
		}
		log.Printf("heartbeat accepted for %s", registeredID)
		return
	}

	manager := &reverseManager{
		baseURL:            baseURL,
		relayURL:           relayURL,
		udpRelayURL:        udpRelayURL,
		relayWebURL:        relayWebURL,
		httpClient:         client,
		poolSize:           reversePoolSize,
		webPoolSize:        webReversePoolSize,
		connectTimout:      defaultRelayTimeout,
		workers:            make(map[string]managedTunnel),
		httpProbeMetrics:   make(map[string]string),
		tunnelTraffic:      make(map[string]trafficCounter),
		udpResponseTimeout: 3 * time.Second,
	}

	localAddr := config.GetEnv("AGENT_LOCAL_LISTEN", "127.0.0.1:5180")
	mux := http.NewServeMux()
	mux.HandleFunc("/agent/traffic", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(manager.TrafficSnapshot())
	})

	if err := manager.syncTunnels(context.Background(), registeredID); err != nil {
		log.Printf("initial tunnel sync failed: %v", err)
	}
	if err := heartbeat(client, baseURL, registeredID, manager.ActiveTunnelCount(), manager.HeartbeatMetrics(), p2pConfig); err != nil {
		log.Printf("initial heartbeat failed: %v", err)
	} else {
		log.Printf("heartbeat accepted for %s", registeredID)
	}

	localServer := &http.Server{Addr: localAddr, Handler: mux}
	go func() {
		if err := localServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("local traffic server error: %v", err)
		}
	}()
	log.Printf("local traffic endpoint listening on %s", localAddr)

	heartbeatTicker := time.NewTicker(heartbeatEvery)
	tunnelTicker := time.NewTicker(tunnelPollInterval)
	defer heartbeatTicker.Stop()
	defer tunnelTicker.Stop()

	for {
		select {
		case <-heartbeatTicker.C:
			if err := heartbeat(client, baseURL, registeredID, manager.ActiveTunnelCount(), manager.HeartbeatMetrics(), p2pConfig); err != nil {
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

func register(client *http.Client, baseURL, nodeID, nodeName, deploymentMode, serviceUnit, instanceProfile string, p2pConfig p2pTelemetryConfig) (string, error) {
	payload := types.NodeRegisterRequest{
		NodeID:       nodeID,
		NodeName:     nodeName,
		AgentVersion: agentVersion,
		Capabilities: types.NodeCapabilities{
			TCPRelay:      true,
			HTTPRelay:     true,
			HTTPSRelay:    true,
			UDPRelay:      true,
			P2PAssist:     p2pConfig.enabled,
			SOCKS5Connect: true,
		},
		Metadata: map[string]string{
			"hostname":        defaultNodeName(),
			"os":              runtime.GOOS,
			"arch":            runtime.GOARCH,
			"deploymentMode":  strings.TrimSpace(deploymentMode),
			"serviceUnit":     strings.TrimSpace(serviceUnit),
			"instanceProfile": strings.TrimSpace(instanceProfile),
			"instanceManaged": fmt.Sprintf("%t", strings.TrimSpace(serviceUnit) != ""),
		},
	}
	if p2pConfig.enabled {
		payload.Metadata["p2pRpcPortal"] = p2pConfig.rpcPortal
		payload.Metadata["p2pRuntime"] = "easytier"
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

func heartbeat(client *http.Client, baseURL, nodeID string, activeTunnels int, probeMetrics map[string]string, p2pConfig p2pTelemetryConfig) error {
	payload := types.NodeHeartbeatRequest{
		NodeID:        nodeID,
		ObservedAt:    time.Now().UTC(),
		ActiveTunnels: activeTunnels,
		Metrics: map[string]string{
			"goroutines": fmt.Sprintf("%d", runtime.NumGoroutine()),
			"pid":        fmt.Sprintf("%d", os.Getpid()),
		},
	}
	for key, value := range probeMetrics {
		payload.Metrics[key] = value
	}
	for key, value := range collectP2PMetrics(p2pConfig) {
		payload.Metrics[key] = value
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

func (m *reverseManager) HeartbeatMetrics() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for key, value := range m.httpProbeMetrics {
		out[key] = value
	}
	var totalDown uint64
	var totalUp uint64
	for tunnelID, counter := range m.tunnelTraffic {
		out["traffic:down:"+tunnelID] = fmt.Sprintf("%d", counter.downBytes)
		out["traffic:up:"+tunnelID] = fmt.Sprintf("%d", counter.upBytes)
		totalDown += counter.downBytes
		totalUp += counter.upBytes
	}
	out["traffic:down_total"] = fmt.Sprintf("%d", totalDown)
	out["traffic:up_total"] = fmt.Sprintf("%d", totalUp)
	return out
}

func (m *reverseManager) addTraffic(tunnelID string, downBytes uint64, upBytes uint64) {
	if tunnelID == "" || (downBytes == 0 && upBytes == 0) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	counter := m.tunnelTraffic[tunnelID]
	counter.downBytes += downBytes
	counter.upBytes += upBytes
	m.tunnelTraffic[tunnelID] = counter
}

type TunnelTrafficEntry struct {
	TunnelID  string `json:"tunnelId"`
	DownBytes uint64 `json:"downBytes"`
	UpBytes   uint64 `json:"upBytes"`
}

type TrafficSnapshot struct {
	Tunnels   []TunnelTrafficEntry `json:"tunnels"`
	DownTotal uint64               `json:"downTotal"`
	UpTotal   uint64               `json:"upTotal"`
	SampledAt int64                `json:"sampledAt"`
}

func (m *reverseManager) TrafficSnapshot() TrafficSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := TrafficSnapshot{
		SampledAt: time.Now().UnixMilli(),
	}
	for tunnelID, counter := range m.tunnelTraffic {
		snapshot.Tunnels = append(snapshot.Tunnels, TunnelTrafficEntry{
			TunnelID:  tunnelID,
			DownBytes: counter.downBytes,
			UpBytes:   counter.upBytes,
		})
		snapshot.DownTotal += counter.downBytes
		snapshot.UpTotal += counter.upBytes
	}
	return snapshot
}

func (m *reverseManager) resetTrafficForDesired(desired map[string]types.TunnelSpec) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for tunnelID := range m.tunnelTraffic {
		if _, ok := desired[tunnelID]; !ok {
			delete(m.tunnelTraffic, tunnelID)
		}
	}
}

func (m *reverseManager) refreshHTTPProbeMetrics(desired map[string]types.TunnelSpec) {
	results := map[string]string{}
	for _, tunnel := range desired {
		if tunnel.Type != "http" {
			continue
		}
		metricKey := httpReachabilityMetricKey(tunnel.NodeID, tunnel.PublicPort)
		if probeHTTPTarget(tunnel.TargetHost, tunnel.TargetPort, m.connectTimout) {
			results[metricKey] = string(types.TunnelHealthHealthy)
		} else {
			results[metricKey] = string(types.TunnelHealthTargetUnreachable)
		}
	}
	m.mu.Lock()
	m.httpProbeMetrics = results
	m.mu.Unlock()
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
		if item.Status != "active" {
			continue
		}
		isWebTunnel := item.Type == "http" || item.Type == "https"
		isPortTunnel := item.Type == "tcp" || item.Type == "udp" || item.Type == "socks5"
		if !isWebTunnel && !isPortTunnel {
			continue
		}
		if isPortTunnel && item.PublicPort == 0 {
			continue
		}
		desired[item.ID] = item
	}

	m.refreshHTTPProbeMetrics(desired)
	m.resetTrafficForDesired(desired)

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
		slotCount := m.poolSize
		if start.tunnel.Type == "http" || start.tunnel.Type == "https" {
			slotCount = m.webPoolSize
		}
		if start.tunnel.Type == "udp" {
			slotCount = 1
		}
		for slot := 0; slot < slotCount; slot++ {
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
	log.Printf("reverse tunnel worker started: tunnel=%s slot=%d publicPort=%d type=%s target=%s:%d", tunnel.ID, slot, tunnel.PublicPort, tunnel.Type, tunnel.TargetHost, tunnel.TargetPort)
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

	connectURL := m.relayURL
	if tunnel.Type == "udp" {
		connectURL = m.udpRelayURL
	} else if tunnel.Type == "http" || tunnel.Type == "https" {
		connectURL = m.relayWebURL
	}
	if tunnel.Type == "udp" {
		log.Printf("udp reverse session dialing: tunnel=%s publicPort=%d relay=%s", tunnel.ID, tunnel.PublicPort, connectURL)
	}
	if tunnel.Type == "http" || tunnel.Type == "https" {
		log.Printf("web reverse session dialing: tunnel=%s type=%s relay=%s", tunnel.ID, tunnel.Type, connectURL)
	}
	conn, err := dialRelayUpgrade(ctx, connectURL, tunnelUpgradeHello(tunnel))
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
	if tunnel.Type == "udp" {
		log.Printf("udp reverse session ready: tunnel=%s publicPort=%d", tunnel.ID, tunnel.PublicPort)
		defer log.Printf("udp reverse session closed: tunnel=%s publicPort=%d", tunnel.ID, tunnel.PublicPort)
	}

	if tunnel.Type == "socks5" {
		return serveSOCKS5(ctx, conn, tunnel.ID, m)
	}
	if tunnel.Type == "udp" {
		return serveUDPRelay(ctx, conn, tunnel.ID, tunnel.PublicPort, tunnel.TargetHost, tunnel.TargetPort, m.udpResponseTimeout, m)
	}

	targetConn, err := (&net.Dialer{Timeout: m.connectTimout}).DialContext(ctx, "tcp", net.JoinHostPort(tunnel.TargetHost, fmt.Sprintf("%d", tunnel.TargetPort)))
	if err != nil {
		return fmt.Errorf("dial target %s:%d: %w", tunnel.TargetHost, tunnel.TargetPort, err)
	}
	defer targetConn.Close()
	stopTargetCancel := closeConnOnCancel(ctx, targetConn)
	defer stopTargetCancel()

	proxyReverseConnection(conn, targetConn, tunnel.ID, m)
	return nil
}

func dialRelayUpgrade(ctx context.Context, relayURL string, hello any) (net.Conn, error) {
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
	upgradeHeader := types.AgentRelayUpgrade
	if strings.Contains(path, "reverse-udp") {
		upgradeHeader = types.AgentUDPRelayUpgrade
	} else if strings.Contains(path, "reverse-web") {
		upgradeHeader = types.AgentWebRelayUpgrade
	}
	request := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", path, parsed.Host, upgradeHeader, len(body), body)
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

func proxyReverseConnection(relayConn net.Conn, targetConn net.Conn, tunnelID string, manager *reverseManager) {
	buf1 := make([]byte, 32*1024)
	buf2 := make([]byte, 32*1024)

	errCh := make(chan error, 2)
	go func() {
		n, copyErr := io.CopyBuffer(targetConn, relayConn, buf1)
		if manager != nil && n > 0 {
			manager.addTraffic(tunnelID, uint64(n), 0)
		}
		errCh <- copyErr
		closeWrite(targetConn)
	}()
	go func() {
		n, copyErr := io.CopyBuffer(relayConn, targetConn, buf2)
		if manager != nil && n > 0 {
			manager.addTraffic(tunnelID, 0, uint64(n))
		}
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

func serveSOCKS5(ctx context.Context, relayConn net.Conn, tunnelID string, manager *reverseManager) error {
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
	proxyReverseConnection(&bufferedConn{Conn: relayConn, reader: reader}, targetConn, tunnelID, manager)
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
		_ = tcpConn.SetNoDelay(true)
	}
}

func httpReachabilityMetricKey(nodeID string, publicPort int) string {
	return "httpReachability:" + nodeID + ":" + fmt.Sprintf("%d", publicPort)
}

func probeHTTPTarget(host string, port int, timeout time.Duration) bool {
	if strings.TrimSpace(host) == "" || port <= 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func deriveUDPRelayURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "http://127.0.0.1:9093" + types.AgentUDPRelayConnectPath
	}
	host := parsed.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, "9093"), Path: types.AgentUDPRelayConnectPath}).String()
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

func deriveRelayWebURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "http://127.0.0.1:9094" + types.AgentWebRelayConnectPath
	}
	host := parsed.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	return (&url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, "9094"),
		Path:   types.AgentWebRelayConnectPath,
	}).String()
}

func tunnelUpgradeHello(tunnel types.TunnelSpec) any {
	if tunnel.Type == "udp" {
		return types.AgentUDPRelayHello{NodeID: tunnel.NodeID, TunnelID: tunnel.ID, PublicPort: tunnel.PublicPort, TargetHost: tunnel.TargetHost, TargetPort: tunnel.TargetPort}
	}
	return types.AgentRelayHello{NodeID: tunnel.NodeID, TunnelID: tunnel.ID, PublicPort: tunnel.PublicPort, TargetHost: tunnel.TargetHost, TargetPort: tunnel.TargetPort}
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

func serveUDPRelay(ctx context.Context, relayConn net.Conn, tunnelID string, publicPort int, targetHost string, targetPort int, responseTimeout time.Duration, manager *reverseManager) error {
	udpTarget, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(targetHost), Port: targetPort})
	if err != nil {
		resolved, resolveErr := net.ResolveUDPAddr("udp", net.JoinHostPort(targetHost, fmt.Sprintf("%d", targetPort)))
		if resolveErr != nil {
			return fmt.Errorf("resolve udp target %s:%d: %w", targetHost, targetPort, err)
		}
		udpTarget, err = net.DialUDP("udp", nil, resolved)
		if err != nil {
			return fmt.Errorf("dial udp target %s:%d: %w", targetHost, targetPort, err)
		}
	}
	defer udpTarget.Close()
	reader := bufio.NewReader(relayConn)
	for {
		if ctx.Err() != nil {
			return nil
		}
		frame, err := readUDPFrame(reader)
		if err != nil {
			return err
		}
		log.Printf("udp frame received from relay: tunnel=%s publicPort=%d session=%s bytes=%d", tunnelID, publicPort, frame.SessionID, len(frame.Payload))
		if manager != nil && len(frame.Payload) > 0 {
			manager.addTraffic(tunnelID, uint64(len(frame.Payload)), 0)
		}
		if len(frame.Payload) == 0 {
			if err := writeUDPFrame(relayConn, types.UDPDatagramFrame{SessionID: frame.SessionID}); err != nil {
				return err
			}
			continue
		}
		if _, err := udpTarget.Write(frame.Payload); err != nil {
			if err := writeUDPFrame(relayConn, types.UDPDatagramFrame{SessionID: frame.SessionID, Error: err.Error()}); err != nil {
				return err
			}
			continue
		}
		if err := udpTarget.SetReadDeadline(time.Now().Add(responseTimeout)); err != nil {
			return err
		}
		respBuf := make([]byte, 64*1024)
		n, _, err := udpTarget.ReadFromUDP(respBuf)
		if err != nil {
			if err := writeUDPFrame(relayConn, types.UDPDatagramFrame{SessionID: frame.SessionID, Error: err.Error()}); err != nil {
				return err
			}
			continue
		}
		log.Printf("udp response from target: tunnel=%s publicPort=%d session=%s bytes=%d", tunnelID, publicPort, frame.SessionID, n)
		if manager != nil && n > 0 {
			manager.addTraffic(tunnelID, 0, uint64(n))
		}
		if err := writeUDPFrame(relayConn, types.UDPDatagramFrame{SessionID: frame.SessionID, Payload: append([]byte(nil), respBuf[:n]...)}); err != nil {
			return err
		}
	}
}

func writeUDPFrame(w io.Writer, frame types.UDPDatagramFrame) error {
	body, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(body)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func readUDPFrame(r io.Reader) (types.UDPDatagramFrame, error) {
	var sizeBuf [4]byte
	if _, err := io.ReadFull(r, sizeBuf[:]); err != nil {
		return types.UDPDatagramFrame{}, err
	}
	size := binary.BigEndian.Uint32(sizeBuf[:])
	if size == 0 || size > 1<<20 {
		return types.UDPDatagramFrame{}, fmt.Errorf("invalid udp frame size %d", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return types.UDPDatagramFrame{}, err
	}
	var frame types.UDPDatagramFrame
	if err := json.Unmarshal(body, &frame); err != nil {
		return types.UDPDatagramFrame{}, err
	}
	return frame, nil
}
