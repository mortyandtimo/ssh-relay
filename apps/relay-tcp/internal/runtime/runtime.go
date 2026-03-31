package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

const (
	routeSyncInterval     = 5 * time.Second
	standbyPoolTargetSize = 8
	standbyPoolMaxSize    = 16
	globalMaxStandby      = 200
	standbyConnMaxAge     = 90 * time.Second
	defaultAcquireTimeout = 10 * time.Second
)

type standbyConn struct {
	conn         net.Conn
	hello        types.AgentRelayHello
	registeredAt time.Time
}

func (s standbyConn) age() time.Duration {
	return time.Since(s.registeredAt)
}

func (s standbyConn) expired() bool {
	return s.age() > standbyConnMaxAge
}

func (s standbyConn) prepareForStart() error {
	buf := make([]byte, 1)
	for {
		if err := s.conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
			return err
		}
		_, err := io.ReadFull(s.conn, buf)
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			_ = s.conn.SetReadDeadline(time.Time{})
			return nil
		}
		if err != nil {
			_ = s.conn.SetReadDeadline(time.Time{})
			return err
		}
		if buf[0] != types.AgentRelayKeepaliveByte {
			_ = s.conn.SetReadDeadline(time.Time{})
			return fmt.Errorf("unexpected pre-start byte %d", buf[0])
		}
	}
}

type standbyPool struct {
	mu     sync.Mutex
	items  []standbyConn
	closed bool
	key    string
	target int
	max    int
}

func newStandbyPool(key string, target, max int) *standbyPool {
	if target < 1 {
		target = 1
	}
	if max < target {
		max = target
	}
	return &standbyPool{key: key, target: target, max: max}
}

func (p *standbyPool) lenLocked() int {
	return len(p.items)
}

func (p *standbyPool) enqueue(item standbyConn) (int, []standbyConn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return len(p.items), nil, errors.New("pool closed")
	}
	var evicted []standbyConn
	for len(p.items) >= p.target {
		idx := p.oldestIndexLocked()
		evicted = append(evicted, p.items[idx])
		p.items = append(p.items[:idx], p.items[idx+1:]...)
	}
	p.items = append(p.items, item)
	return len(p.items), evicted, nil
}

func (p *standbyPool) acquire(route types.TunnelSpec) (standbyConn, int, []standbyConn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return standbyConn{}, len(p.items), nil, errors.New("pool closed")
	}
	if len(p.items) == 0 {
		return standbyConn{}, 0, nil, ErrPoolEmpty
	}
	var evicted []standbyConn
	for len(p.items) > 0 {
		item := p.items[0]
		p.items = p.items[1:]
		if item.expired() {
			evicted = append(evicted, item)
			continue
		}
		if !helloMatchesRoute(item.hello, route) {
			evicted = append(evicted, item)
			continue
		}
		return item, len(p.items), evicted, nil
	}
	return standbyConn{}, 0, evicted, ErrPoolEmpty
}

func (p *standbyPool) closeAndDrain() []standbyConn {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	drained := append([]standbyConn(nil), p.items...)
	p.items = nil
	return drained
}

func (p *standbyPool) oldestIndexLocked() int {
	if len(p.items) == 0 {
		return 0
	}
	oldestIdx := 0
	oldestAt := p.items[0].registeredAt
	for i := 1; i < len(p.items); i++ {
		if p.items[i].registeredAt.Before(oldestAt) {
			oldestAt = p.items[i].registeredAt
			oldestIdx = i
		}
	}
	return oldestIdx
}

var ErrPoolEmpty = errors.New("standby pool empty")

type Service struct {
	apiBaseURL string
	httpClient *http.Client

	mu        sync.Mutex
	listeners map[int]net.Listener
	routes    map[int]types.TunnelSpec
	pools     map[string]*standbyPool

	acquireTimeout time.Duration
}

var connectionCounter uint64
var totalStandby int64

func NewService(apiBaseURL string) *Service {
	return &Service{
		apiBaseURL:     apiBaseURL,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		listeners:      make(map[int]net.Listener),
		routes:         make(map[int]types.TunnelSpec),
		pools:          make(map[string]*standbyPool),
		acquireTimeout: defaultAcquireTimeout,
	}
}

func (s *Service) Run(ctx context.Context) error {
	if err := s.syncRoutes(ctx); err != nil {
		log.Printf("initial tcp route sync failed: %v", err)
	}
	ticker := time.NewTicker(routeSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.closeAll()
			return nil
		case <-ticker.C:
			if err := s.syncRoutes(ctx); err != nil {
				log.Printf("sync tcp routes: %v", err)
			}
		}
	}
}

func (s *Service) HandleAgentReverse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), types.AgentRelayUpgrade) {
		http.Error(w, "upgrade header required", http.StatusUpgradeRequired)
		return
	}

	var hello types.AgentRelayHello
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&hello); err != nil {
		http.Error(w, "invalid relay hello", http.StatusBadRequest)
		return
	}
	if hello.NodeID == "" || hello.TunnelID == "" || hello.PublicPort == 0 || hello.TargetPort == 0 || hello.TargetHost == "" {
		http.Error(w, "incomplete relay hello", http.StatusBadRequest)
		return
	}

	route, key, pool, ok := s.activeRouteAndPoolForHello(hello)
	if !ok {
		http.Error(w, "inactive tunnel", http.StatusNotFound)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	conn, bufrw, err := hijacker.Hijack()
	if err != nil {
		log.Printf("hijack reverse relay connection failed: %v", err)
		return
	}
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}
	if _, err := bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + types.AgentRelayUpgrade + "\r\n\r\n"); err != nil {
		_ = conn.Close()
		return
	}
	if err := bufrw.Flush(); err != nil {
		_ = conn.Close()
		return
	}

	if !helloMatchesRoute(hello, route) {
		_ = conn.Close()
		return
	}

	item := standbyConn{conn: conn, hello: hello, registeredAt: time.Now().UTC()}
	poolSize, totalStandbyCount, err := s.enqueueStandbyConn(pool, key, item)
	if err != nil {
		log.Printf("standby reverse connection rejected for %s tunnel %s: %v", key, hello.TunnelID, err)
		_ = conn.Close()
		return
	}
	log.Printf("standby reverse connection ready: node=%s tunnel=%s publicPort=%d poolSize=%d totalStandby=%d", hello.NodeID, hello.TunnelID, hello.PublicPort, poolSize, totalStandbyCount)
}

func (s *Service) syncRoutes(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiBaseURL+"/internal/routes/tcp", nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("route query failed with status %s", resp.Status)
	}

	var payload struct {
		Items []types.TunnelSpec `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}

	desired := make(map[int]types.TunnelSpec)
	for _, item := range payload.Items {
		if item.PublicPort == 0 || item.NodeID == "" {
			continue
		}
		if existing, ok := desired[item.PublicPort]; ok {
			log.Printf("duplicate tcp public port detected: publicPort=%d existingTunnel=%s existingNode=%s conflictingTunnel=%s conflictingNode=%s", item.PublicPort, existing.ID, existing.NodeID, item.ID, item.NodeID)
			return fmt.Errorf("duplicate public port %d", item.PublicPort)
		}
		desired[item.PublicPort] = item
	}

	staleKeys := make([]string, 0)
	type listenerStart struct {
		listener net.Listener
		route    types.TunnelSpec
	}
	starts := make([]listenerStart, 0)

	s.mu.Lock()
	for port, listener := range s.listeners {
		if _, ok := desired[port]; ok {
			continue
		}
		_ = listener.Close()
		if route, ok := s.routes[port]; ok {
			staleKeys = append(staleKeys, routePoolKey(route.NodeID, route.PublicPort))
		}
		delete(s.listeners, port)
		delete(s.routes, port)
		log.Printf("tcp route removed: %d", port)
	}

	for port, route := range desired {
		current, ok := s.routes[port]
		if ok && sameRoute(current, route) {
			continue
		}
		if ok {
			_ = s.listeners[port].Close()
			staleKeys = append(staleKeys, routePoolKey(current.NodeID, current.PublicPort))
			delete(s.listeners, port)
			delete(s.routes, port)
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", itoa(port)))
		if err != nil {
			s.mu.Unlock()
			for _, key := range staleKeys {
				s.drainPool(key)
			}
			return err
		}
		s.listeners[port] = listener
		s.routes[port] = route
		key := routePoolKey(route.NodeID, route.PublicPort)
		if _, ok := s.pools[key]; !ok {
			s.pools[key] = newStandbyPool(key, standbyPoolTargetSize, standbyPoolTargetSize)
		}
		starts = append(starts, listenerStart{listener: listener, route: route})
		log.Printf("%s route active: node=%s publicPort=%d tunnel=%s", route.Type, route.NodeID, route.PublicPort, route.ID)
	}
	s.mu.Unlock()

	for _, key := range staleKeys {
		s.drainPool(key)
	}
	for _, start := range starts {
		if start.route.Type == "http" {
			go s.serveHTTPRoute(start.listener, start.route)
			continue
		}
		go s.acceptLoop(start.listener, start.route)
	}
	return nil
}

func (s *Service) serveHTTPRoute(listener net.Listener, route types.TunnelSpec) {
	server := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handleHTTPRequest(w, r, route) }),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	server.SetKeepAlivesEnabled(false)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		log.Printf("http serve failed on %s: %v", listener.Addr().String(), err)
	}
}

func (s *Service) handleHTTPRequest(w http.ResponseWriter, r *http.Request, route types.TunnelSpec) {
	reqID := atomic.AddUint64(&connectionCounter, 1)
	startedAt := time.Now()
	log.Printf("http request %d accepted from %s method=%s host=%s path=%s for node=%s publicPort=%d", reqID, r.RemoteAddr, r.Method, r.Host, r.URL.RequestURI(), route.NodeID, route.PublicPort)
	standby, poolSize, totalStandbyCount, err := s.acquireStandbyConn(r.Context(), route)
	if err != nil {
		log.Printf("http request %d no standby reverse connection for node=%s publicPort=%d: %v", reqID, route.NodeID, route.PublicPort, err)
		http.Error(w, "http relay standby unavailable", http.StatusBadGateway)
		return
	}
	if err := standby.prepareForStart(); err != nil {
		log.Printf("http request %d discarded stale standby reverse connection for tunnel %s poolSize=%d totalStandby=%d: %v", reqID, route.ID, poolSize, totalStandbyCount, err)
		_ = standby.conn.Close()
		http.Error(w, "http relay standby stale", http.StatusBadGateway)
		return
	}
	if _, err := standby.conn.Write([]byte{types.AgentRelayStartByte}); err != nil {
		log.Printf("http request %d failed to start reverse session for tunnel %s poolSize=%d totalStandby=%d: %v", reqID, route.ID, poolSize, totalStandbyCount, err)
		_ = standby.conn.Close()
		http.Error(w, "http relay start failed", http.StatusBadGateway)
		return
	}
	defer standby.conn.Close()
	upstreamReq := cloneHTTPRequestForRelay(r, route)
	if err := upstreamReq.Write(standby.conn); err != nil {
		log.Printf("http request %d write upstream request failed for tunnel %s: %v", reqID, route.ID, err)
		http.Error(w, "http relay write failed", http.StatusBadGateway)
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(standby.conn), upstreamReq)
	if err != nil {
		log.Printf("http request %d read upstream response failed for tunnel %s: %v", reqID, route.ID, err)
		http.Error(w, "http relay response failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyHTTPResponse(w, resp)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("http request %d copy response body failed for tunnel %s: %v", reqID, route.ID, err)
		return
	}
	log.Printf("http request %d completed with status=%d after %s", reqID, resp.StatusCode, time.Since(startedAt))
}

func cloneHTTPRequestForRelay(r *http.Request, route types.TunnelSpec) *http.Request {
	out := r.Clone(r.Context())
	out.URL = &url.URL{Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery, ForceQuery: r.URL.ForceQuery}
	out.RequestURI = ""
	out.Host = net.JoinHostPort(route.TargetHost, itoa(route.TargetPort))
	out.Close = true
	return out
}

func copyHTTPResponse(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
}

func isHopByHopHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func (s *Service) acceptLoop(listener net.Listener, route types.TunnelSpec) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Printf("accept failed on %s: %v", listener.Addr().String(), err)
			}
			return
		}
		go s.handlePublicConnection(conn, route)
	}
}

func (s *Service) handlePublicConnection(src net.Conn, route types.TunnelSpec) {
	connID := atomic.AddUint64(&connectionCounter, 1)
	start := time.Now()
	defer src.Close()
	log.Printf("tcp connection %d accepted from %s for node=%s publicPort=%d", connID, src.RemoteAddr().String(), route.NodeID, route.PublicPort)

	ctx, cancel := context.WithTimeout(context.Background(), s.acquireTimeout)
	defer cancel()
	for {
		standby, poolSize, totalStandbyCount, err := s.acquireStandbyConn(ctx, route)
		if err != nil {
			log.Printf("tcp connection %d no standby reverse connection for node=%s publicPort=%d: %v", connID, route.NodeID, route.PublicPort, err)
			return
		}
		if err := standby.prepareForStart(); err != nil {
			log.Printf("tcp connection %d discarded stale standby reverse connection for tunnel %s age=%s poolSize=%d totalStandby=%d: %v", connID, route.ID, standby.age(), poolSize, totalStandbyCount, err)
			_ = standby.conn.Close()
			continue
		}
		if _, err := standby.conn.Write([]byte{types.AgentRelayStartByte}); err != nil {
			log.Printf("tcp connection %d failed to start reverse session for tunnel %s poolSize=%d totalStandby=%d: %v", connID, route.ID, poolSize, totalStandbyCount, err)
			_ = standby.conn.Close()
			continue
		}
		log.Printf("tcp connection %d paired with standby reverse connection for tunnel %s standbyAge=%s poolSize=%d totalStandby=%d", connID, route.ID, standby.age(), poolSize, totalStandbyCount)
		defer standby.conn.Close()
		proxyConnections(connID, src, standby.conn, start)
		return
	}
}

func proxyConnections(connID uint64, left net.Conn, right net.Conn, startedAt time.Time) {
	errCh := make(chan error, 2)
	go func() {
		_, copyErr := io.Copy(right, left)
		errCh <- copyErr
		if tcpConn, ok := right.(*net.TCPConn); ok {
			_ = tcpConn.CloseWrite()
		}
	}()
	go func() {
		_, copyErr := io.Copy(left, right)
		errCh <- copyErr
		if tcpConn, ok := left.(*net.TCPConn); ok {
			_ = tcpConn.CloseWrite()
		}
	}()

	firstErr := <-errCh
	secondErr := <-errCh
	if firstErr != nil && !errors.Is(firstErr, io.EOF) {
		log.Printf("tcp connection %d first copy ended with error: %v", connID, firstErr)
	}
	if secondErr != nil && !errors.Is(secondErr, io.EOF) {
		log.Printf("tcp connection %d second copy ended with error: %v", connID, secondErr)
	}
	log.Printf("tcp connection %d closed after %s", connID, time.Since(startedAt))
}

func (s *Service) activeRouteAndPoolForHello(hello types.AgentRelayHello) (types.TunnelSpec, string, *standbyPool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	route, ok := s.routes[hello.PublicPort]
	if !ok || !helloMatchesRoute(hello, route) {
		return types.TunnelSpec{}, "", nil, false
	}
	key := routePoolKey(route.NodeID, route.PublicPort)
	pool, ok := s.pools[key]
	if !ok {
		pool = newStandbyPool(key, standbyPoolTargetSize, standbyPoolTargetSize)
		s.pools[key] = pool
	}
	return route, key, pool, true
}

func (s *Service) poolForRoute(route types.TunnelSpec) (*standbyPool, string, bool) {
	key := routePoolKey(route.NodeID, route.PublicPort)
	s.mu.Lock()
	pool, ok := s.pools[key]
	s.mu.Unlock()
	return pool, key, ok
}

func (s *Service) acquireStandbyConn(ctx context.Context, route types.TunnelSpec) (standbyConn, int, int64, error) {
	for {
		pool, _, ok := s.poolForRoute(route)
		if !ok {
			return standbyConn{}, 0, atomic.LoadInt64(&totalStandby), ErrPoolEmpty
		}
		item, poolSize, drained, err := pool.acquire(route)
		if len(drained) > 0 {
			s.closeStandbyItems(drained)
		}
		if err == nil {
			totalStandbyCount := atomic.AddInt64(&totalStandby, -1)
			return item, poolSize, totalStandbyCount, nil
		}
		if errors.Is(err, ErrPoolEmpty) {
			select {
			case <-ctx.Done():
				return standbyConn{}, poolSize, atomic.LoadInt64(&totalStandby), ctx.Err()
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}
		return standbyConn{}, poolSize, atomic.LoadInt64(&totalStandby), err
	}
}

func (s *Service) enqueueStandbyConn(pool *standbyPool, key string, item standbyConn) (int, int64, error) {
	if globalMaxStandby > 0 && atomic.LoadInt64(&totalStandby) >= globalMaxStandby {
		return pool.lenLocked(), atomic.LoadInt64(&totalStandby), errors.New("global standby pool full")
	}
	poolSize, evicted, err := pool.enqueue(item)
	if err != nil {
		return poolSize, atomic.LoadInt64(&totalStandby), err
	}
	for _, item := range evicted {
		totalAfterEvict := atomic.AddInt64(&totalStandby, -1)
		if item.expired() {
			log.Printf("evict expired standby reverse connection for key=%s tunnel=%s age=%s totalStandby=%d to keep target pool size=%d", key, item.hello.TunnelID, item.age(), totalAfterEvict, pool.target)
		} else {
			log.Printf("evict standby reverse connection for key=%s tunnel=%s age=%s totalStandby=%d to keep target pool size=%d", key, item.hello.TunnelID, item.age(), totalAfterEvict, pool.target)
		}
		_ = item.conn.Close()
	}
	totalAfterAdd := atomic.AddInt64(&totalStandby, 1)
	return poolSize, totalAfterAdd, nil
}

func (s *Service) drainPool(key string) {
	s.mu.Lock()
	pool := s.pools[key]
	delete(s.pools, key)
	s.mu.Unlock()
	if pool == nil {
		return
	}
	s.closeStandbyItems(pool.closeAndDrain())
}

func (s *Service) closeAll() {
	s.mu.Lock()
	listeners := make([]net.Listener, 0, len(s.listeners))
	pools := make([]*standbyPool, 0, len(s.pools))
	for _, listener := range s.listeners {
		listeners = append(listeners, listener)
	}
	for _, pool := range s.pools {
		pools = append(pools, pool)
	}
	s.listeners = make(map[int]net.Listener)
	s.routes = make(map[int]types.TunnelSpec)
	s.pools = make(map[string]*standbyPool)
	s.mu.Unlock()

	for _, listener := range listeners {
		_ = listener.Close()
	}
	for _, pool := range pools {
		s.closeStandbyItems(pool.closeAndDrain())
	}
}

func (s *Service) closeStandbyItems(items []standbyConn) {
	for _, item := range items {
		_ = atomic.AddInt64(&totalStandby, -1)
		_ = item.conn.Close()
	}
}

func (s *Service) RuntimeSummary() types.RelayRuntimeSummary {
	s.mu.Lock()
	pools := make([]types.RelayPoolSummary, 0, len(s.pools))
	for key, pool := range s.pools {
		pool.mu.Lock()
		standbyCount := len(pool.items)
		targetSize := pool.target
		maxSize := pool.max
		pool.mu.Unlock()
		nodeID, publicPort := parseRoutePoolKey(key)
		pools = append(pools, types.RelayPoolSummary{
			PoolKey:      key,
			NodeID:       nodeID,
			PublicPort:   publicPort,
			StandbyCount: standbyCount,
			TargetSize:   targetSize,
			MaxSize:      maxSize,
		})
	}
	s.mu.Unlock()
	return types.RelayRuntimeSummary{
		Service:      "relay-tcp",
		ObservedAt:   time.Now().UTC(),
		TotalStandby: int(atomic.LoadInt64(&totalStandby)),
		Pools:        pools,
	}
}

func helloMatchesRoute(hello types.AgentRelayHello, route types.TunnelSpec) bool {
	return hello.NodeID == route.NodeID &&
		hello.TunnelID == route.ID &&
		hello.PublicPort == route.PublicPort &&
		hello.TargetHost == route.TargetHost &&
		hello.TargetPort == route.TargetPort
}

func sameRoute(left, right types.TunnelSpec) bool {
	return left.ID == right.ID &&
		left.Type == right.Type &&
		left.NodeID == right.NodeID &&
		left.PublicPort == right.PublicPort &&
		left.TargetHost == right.TargetHost &&
		left.TargetPort == right.TargetPort
}

func routePoolKey(nodeID string, publicPort int) string {
	return nodeID + ":" + itoa(publicPort)
}

func parseRoutePoolKey(key string) (string, int) {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return key, 0
	}
	var publicPort int
	_, _ = fmt.Sscanf(parts[1], "%d", &publicPort)
	return parts[0], publicPort
}

func itoa(v int) string {
	return fmt.Sprintf("%d", v)
}
