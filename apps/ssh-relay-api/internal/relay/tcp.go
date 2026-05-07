package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/25743/cloud-relay-platform/apps/ssh-relay-api/internal/store"
)

const (
	reverseConnectPath = "/api/relay/reverse"
	relayUpgradeHeader = "sshr-reverse"
	keepaliveInterval  = 15 * time.Second
	dialTimeout        = 10 * time.Second
)

type Manager struct {
	store     *store.Store
	portStart int
	portEnd   int
	poolSize  int

	mu       sync.Mutex
	tunnels  map[int]*portTunnel
	stopSync context.CancelFunc
}

type portTunnel struct {
	publicPort int
	machineID  string
	targetHost string
	targetPort int
	listener   net.Listener
	pool       *reversePool
	cancel     context.CancelFunc
}

type reversePool struct {
	mu      sync.Mutex
	conns   []net.Conn
	waiters []chan net.Conn
	ctx     context.Context
}

func NewManager(st *store.Store, portStart, portEnd, poolSize int) *Manager {
	if poolSize < 1 {
		poolSize = 4
	}
	return &Manager{
		store:     st,
		portStart: portStart,
		portEnd:   portEnd,
		poolSize:  poolSize,
		tunnels:   map[int]*portTunnel{},
	}
}

func (m *Manager) StartSync(ctx context.Context) {
	syncCtx, cancel := context.WithCancel(ctx)
	m.stopSync = cancel
	go m.syncLoop(syncCtx)
}

func (m *Manager) Stop() {
	if m.stopSync != nil {
		m.stopSync()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tunnels {
		t.shutdown()
	}
	m.tunnels = map[int]*portTunnel{}
}

func (m *Manager) syncLoop(ctx context.Context) {
	log.Printf("relay sync started (ports %d-%d, pool=%d)", m.portStart, m.portEnd, m.poolSize)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	m.fullSync(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.fullSync(ctx)
		}
	}
}

func (m *Manager) fullSync(ctx context.Context) {
	fwds, err := m.store.ListForwards(ctx, "")
	if err != nil {
		log.Printf("relay sync error: %v", err)
		return
	}

	desired := map[int]store.Forward{}
	for _, f := range fwds {
		if f.Status == "active" {
			desired[f.PublicPort] = f
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove stale tunnels
	for port, t := range m.tunnels {
		if _, ok := desired[port]; !ok {
			log.Printf("relay: closing port %d (forward removed)", port)
			t.shutdown()
			delete(m.tunnels, port)
		}
	}

	// Add new tunnels
	for port, f := range desired {
		if _, ok := m.tunnels[port]; ok {
			continue
		}
		t := &portTunnel{
			publicPort: f.PublicPort,
			machineID:  f.MachineID,
			targetHost: f.TargetHost,
			targetPort: f.TargetPort,
		}
		if err := t.start(ctx, m.poolSize); err != nil {
			log.Printf("relay: failed to bind port %d: %v", port, err)
			continue
		}
		m.tunnels[port] = t
		log.Printf("relay: port %d listening -> %s:%s:%d", port, f.MachineID, f.TargetHost, f.TargetPort)
	}
}

// ─── Reverse connection endpoint ───

func (m *Manager) HandleReverseConnect(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") != relayUpgradeHeader {
		writeError(w, http.StatusBadRequest, "missing upgrade header")
		return
	}

	machineID := strings.TrimSpace(r.Header.Get("X-Machine-Id"))
	portStr := strings.TrimSpace(r.Header.Get("X-Public-Port"))
	if machineID == "" || portStr == "" {
		writeError(w, http.StatusBadRequest, "missing machine-id or public-port header")
		return
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	m.mu.Lock()
	t, ok := m.tunnels[port]
	m.mu.Unlock()

	if !ok || t.machineID != machineID {
		writeError(w, http.StatusNotFound, "no matching tunnel for this machine+port")
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "hijack not supported")
		return
	}

	conn, bufrw, err := hj.Hijack()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Write the 101 switching protocols response
	resp := "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + relayUpgradeHeader + "\r\n\r\n"
	if _, err := bufrw.WriteString(resp); err != nil {
		conn.Close()
		return
	}
	if err := bufrw.Flush(); err != nil {
		conn.Close()
		return
	}

	// Drain buffered reader before using as raw conn
	wrapped := &hijackedConn{Conn: conn, reader: bufrw.Reader}
	enableTCPKeepalive(wrapped)

	log.Printf("relay: reverse connection from machine=%s port=%d", machineID, port)
	t.pool.add(wrapped)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ─── portTunnel ───

func (t *portTunnel) start(ctx context.Context, poolSize int) error {
	l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", t.publicPort))
	if err != nil {
		return err
	}
	t.listener = l

	poolCtx, cancel := context.WithCancel(ctx)
	t.pool = &reversePool{ctx: poolCtx}
	t.cancel = cancel

	go t.acceptLoop(ctx)
	return nil
}

func (t *portTunnel) shutdown() {
	if t.cancel != nil {
		t.cancel()
	}
	if t.listener != nil {
		t.listener.Close()
	}
}

func (t *portTunnel) acceptLoop(ctx context.Context) {
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			log.Printf("relay: accept error on port %d: %v", t.publicPort, err)
			return
		}
		go t.handlePublicConn(ctx, conn)
	}
}

func (t *portTunnel) handlePublicConn(ctx context.Context, public net.Conn) {
	defer public.Close()

	// Get a reverse connection from the pool
	rev, err := t.pool.get(ctx, 30*time.Second)
	if err != nil {
		log.Printf("relay: port %d: no reverse connection available: %v", t.publicPort, err)
		return
	}
	defer rev.Close()

	// Write a start signal to the reverse connection
	hello := map[string]any{
		"targetHost": t.targetHost,
		"targetPort": t.targetPort,
	}
	helloBytes, _ := json.Marshal(hello)
	header := fmt.Sprintf("SSHR-PROXY %d\n", len(helloBytes))
	if _, err := rev.Write([]byte(header)); err != nil {
		log.Printf("relay: port %d: write proxy header: %v", t.publicPort, err)
		return
	}
	if _, err := rev.Write(helloBytes); err != nil {
		return
	}

	// Bidirectional copy
	errCh := make(chan error, 2)
	go func() { _, e := io.Copy(rev, public); errCh <- e }()
	go func() { _, e := io.Copy(public, rev); errCh <- e }()
	<-errCh
	<-errCh
}

// ─── reversePool ───

func (p *reversePool) add(conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Give to waiting public side first
	if len(p.waiters) > 0 {
		waiter := p.waiters[0]
		p.waiters = p.waiters[1:]
		select {
		case waiter <- conn:
			return
		default:
		}
	}

	p.conns = append(p.conns, conn)
	// Trim excess
	for len(p.conns) > 16 {
		oldest := p.conns[0]
		p.conns = p.conns[1:]
		oldest.Close()
	}
}

func (p *reversePool) get(ctx context.Context, timeout time.Duration) (net.Conn, error) {
	p.mu.Lock()
	if len(p.conns) > 0 {
		conn := p.conns[0]
		p.conns = p.conns[1:]
		p.mu.Unlock()
		return conn, nil
	}

	// No connection ready, wait
	ch := make(chan net.Conn, 1)
	p.waiters = append(p.waiters, ch)
	p.mu.Unlock()

	select {
	case conn := <-ch:
		return conn, nil
	case <-time.After(timeout):
		p.mu.Lock()
		for i, w := range p.waiters {
			if w == ch {
				p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
				break
			}
		}
		p.mu.Unlock()
		return nil, fmt.Errorf("timeout waiting for reverse connection")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ─── hijackedConn hijacked by any HTTP upgrades ───

type hijackedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *hijackedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func enableTCPKeepalive(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.SetKeepAlive(true)
		tcp.SetKeepAlivePeriod(30 * time.Second)
	}
}
