package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

const (
	routeSyncInterval     = 5 * time.Second
	standbyPoolBufferSize = 64
	standbyPoolTargetSize = 8
	standbyConnMaxAge     = 90 * time.Second
	defaultAcquireTimeout = 10 * time.Second
)

type standbyConn struct {
	conn         net.Conn
	hello        types.AgentRelayHello
	registeredAt time.Time
}

type Service struct {
	apiBaseURL string
	httpClient *http.Client

	mu        sync.Mutex
	listeners map[int]net.Listener
	routes    map[int]types.TunnelSpec
	pools     map[string]chan standbyConn

	acquireTimeout time.Duration
}

var connectionCounter uint64

func NewService(apiBaseURL string) *Service {
	return &Service{
		apiBaseURL:     apiBaseURL,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		listeners:      make(map[int]net.Listener),
		routes:         make(map[int]types.TunnelSpec),
		pools:          make(map[string]chan standbyConn),
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

	route, ok := s.activeRouteForHello(hello)
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

	key := routePoolKey(route.NodeID, route.PublicPort)
	item := standbyConn{conn: conn, hello: hello, registeredAt: time.Now().UTC()}
	poolSize, err := s.enqueueStandbyConn(key, item)
	if err != nil {
		log.Printf("standby reverse connection rejected for %s tunnel %s: %v", key, hello.TunnelID, err)
		_ = conn.Close()
		return
	}
	log.Printf("standby reverse connection ready: node=%s tunnel=%s publicPort=%d poolSize=%d", hello.NodeID, hello.TunnelID, hello.PublicPort, poolSize)
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
		starts = append(starts, listenerStart{listener: listener, route: route})
		log.Printf("tcp route active: node=%s publicPort=%d tunnel=%s", route.NodeID, route.PublicPort, route.ID)
	}
	s.mu.Unlock()

	for _, key := range staleKeys {
		s.drainPool(key)
	}
	for _, start := range starts {
		go s.acceptLoop(start.listener, start.route)
	}
	return nil
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
		standby, err := s.acquireStandbyConn(ctx, route)
		if err != nil {
			log.Printf("tcp connection %d no standby reverse connection for node=%s publicPort=%d: %v", connID, route.NodeID, route.PublicPort, err)
			return
		}

		if err := standby.prepareForStart(); err != nil {
			log.Printf("tcp connection %d discarded stale standby reverse connection for tunnel %s age=%s: %v", connID, route.ID, standby.age(), err)
			_ = standby.conn.Close()
			continue
		}
		if _, err := standby.conn.Write([]byte{types.AgentRelayStartByte}); err != nil {
			log.Printf("tcp connection %d failed to start reverse session for tunnel %s: %v", connID, route.ID, err)
			_ = standby.conn.Close()
			continue
		}
		log.Printf("tcp connection %d paired with standby reverse connection for tunnel %s standbyAge=%s", connID, route.ID, standby.age())
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

func (s *Service) activeRouteForHello(hello types.AgentRelayHello) (types.TunnelSpec, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	route, ok := s.routes[hello.PublicPort]
	if !ok {
		return types.TunnelSpec{}, false
	}
	if !helloMatchesRoute(hello, route) {
		return types.TunnelSpec{}, false
	}
	return route, true
}

func (s *Service) acquireStandbyConn(ctx context.Context, route types.TunnelSpec) (standbyConn, error) {
	queue := s.poolForKey(routePoolKey(route.NodeID, route.PublicPort))
	for {
		select {
		case item := <-queue:
			if item.expired() {
				log.Printf("evict expired standby reverse connection for node=%s publicPort=%d tunnel=%s age=%s", route.NodeID, route.PublicPort, item.hello.TunnelID, item.age())
				_ = item.conn.Close()
				continue
			}
			if !helloMatchesRoute(item.hello, route) {
				_ = item.conn.Close()
				continue
			}
			return item, nil
		case <-ctx.Done():
			return standbyConn{}, ctx.Err()
		}
	}
}

func (s *Service) enqueueStandbyConn(key string, item standbyConn) (int, error) {
	queue := s.poolForKey(key)
	if len(queue) >= standbyPoolTargetSize {
		select {
		case evicted := <-queue:
			log.Printf("evict standby reverse connection for key=%s tunnel=%s age=%s to keep target pool size=%d", key, evicted.hello.TunnelID, evicted.age(), standbyPoolTargetSize)
			_ = evicted.conn.Close()
		case queue <- item:
			return len(queue), nil
		default:
		}
	}
	select {
	case queue <- item:
		return len(queue), nil
	default:
		select {
		case evicted := <-queue:
			if evicted.expired() {
				log.Printf("evict expired standby reverse connection for key=%s tunnel=%s age=%s to admit new standby", key, evicted.hello.TunnelID, evicted.age())
				_ = evicted.conn.Close()
				select {
				case queue <- item:
					return len(queue), nil
				default:
					_ = item.conn.Close()
					return len(queue), errors.New("standby pool full after expired eviction")
				}
			}
			select {
			case queue <- evicted:
			default:
				_ = evicted.conn.Close()
			}
		default:
		}
		return len(queue), errors.New("standby pool full")
	}
}

func (s *Service) poolForKey(key string) chan standbyConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	queue, ok := s.pools[key]
	if ok {
		return queue
	}
	queue = make(chan standbyConn, standbyPoolBufferSize)
	s.pools[key] = queue
	return queue
}

func (s *Service) drainPool(key string) {
	s.mu.Lock()
	queue := s.pools[key]
	delete(s.pools, key)
	s.mu.Unlock()
	drainStandbyQueue(queue)
}

func (s *Service) closeAll() {
	s.mu.Lock()
	listeners := make([]net.Listener, 0, len(s.listeners))
	pools := make([]chan standbyConn, 0, len(s.pools))
	for _, listener := range s.listeners {
		listeners = append(listeners, listener)
	}
	for _, queue := range s.pools {
		pools = append(pools, queue)
	}
	s.listeners = make(map[int]net.Listener)
	s.routes = make(map[int]types.TunnelSpec)
	s.pools = make(map[string]chan standbyConn)
	s.mu.Unlock()

	for _, listener := range listeners {
		_ = listener.Close()
	}
	for _, queue := range pools {
		drainStandbyQueue(queue)
	}
}

func drainStandbyQueue(queue chan standbyConn) {
	if queue == nil {
		return
	}
	for {
		select {
		case item := <-queue:
			_ = item.conn.Close()
		default:
			return
		}
	}
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

func (s standbyConn) age() time.Duration {
	return time.Since(s.registeredAt)
}

func (s standbyConn) expired() bool {
	return s.age() > standbyConnMaxAge
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
		left.NodeID == right.NodeID &&
		left.PublicPort == right.PublicPort &&
		left.TargetHost == right.TargetHost &&
		left.TargetPort == right.TargetPort
}

func routePoolKey(nodeID string, publicPort int) string {
	return nodeID + ":" + itoa(publicPort)
}

func itoa(v int) string {
	return fmt.Sprintf("%d", v)
}
