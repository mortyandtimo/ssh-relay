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
	globalMaxStandby      = 300
	standbyConnMaxAge     = 60 * time.Second
	defaultAcquireTimeout = 10 * time.Second
)

type standbyConn struct {
	conn         net.Conn
	hello        types.AgentRelayHello
	registeredAt time.Time
}

func (s standbyConn) age() time.Duration     { return time.Since(s.registeredAt) }
func (s standbyConn) expired() bool          { return s.age() > standbyConnMaxAge }

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

// ─── standby pool ───

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

var connectionCounter uint64
var totalStandby int64

// ─── Service ───

type Service struct {
	apiBaseURL string
	httpClient *http.Client

	mu     sync.Mutex
	routes map[string]types.TunnelSpec // key: lowercase domain
	pools  map[string]*standbyPool     // key: routePoolKey(nodeID, 0) — 0 because web routes don't use publicPort
}

func NewService(apiBaseURL string) *Service {
	return &Service{
		apiBaseURL: apiBaseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		routes:     make(map[string]types.TunnelSpec),
		pools:      make(map[string]*standbyPool),
	}
}

func (s *Service) Run(ctx context.Context) error {
	if err := s.syncRoutes(ctx); err != nil {
		log.Printf("initial web route sync failed: %v", err)
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
				log.Printf("sync web routes: %v", err)
			}
		}
	}
}

func (s *Service) syncRoutes(ctx context.Context) error {
	desired := make(map[string]types.TunnelSpec)
	for _, routeType := range []string{"http", "https"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiBaseURL+"/internal/routes/"+routeType, nil)
		if err != nil {
			return err
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("web route query (%s) failed with status %s", routeType, resp.Status)
		}
		var payload struct {
			Items []types.TunnelSpec `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			resp.Body.Close()
			return err
		}
		resp.Body.Close()
		for _, item := range payload.Items {
			domain := strings.ToLower(strings.TrimSpace(item.Domain))
			if domain == "" {
				continue
			}
			desired[domain] = item
		}
	}

	s.mu.Lock()
	// Remove stale routes
	for domain := range s.routes {
		if _, ok := desired[domain]; !ok {
			delete(s.routes, domain)
			log.Printf("web route removed: %s", domain)
		}
	}
	// Add/update routes
	for domain, route := range desired {
		s.routes[domain] = route
		key := webPoolKey(route.NodeID, route.ID)
		if _, ok := s.pools[key]; !ok {
			s.pools[key] = newStandbyPool(key, standbyPoolTargetSize, standbyPoolMaxSize)
		}
		log.Printf("web route active: domain=%s node=%s tunnel=%s", domain, route.NodeID, route.ID)
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) routeForHost(host string) (types.TunnelSpec, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	s.mu.Lock()
	route, ok := s.routes[host]
	s.mu.Unlock()
	return route, ok
}

// HandleAgentReverse receives agent reverse connections on the control port.
func (s *Service) HandleAgentReverse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), types.AgentWebRelayUpgrade) {
		http.Error(w, "upgrade header required", http.StatusUpgradeRequired)
		return
	}

	var hello types.AgentRelayHello
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&hello); err != nil {
		http.Error(w, "invalid relay hello", http.StatusBadRequest)
		return
	}
	if hello.NodeID == "" || hello.TunnelID == "" || hello.TargetHost == "" || hello.TargetPort == 0 {
		http.Error(w, "incomplete relay hello", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	// Find route by tunnel ID
	var route types.TunnelSpec
	var found bool
	for _, r := range s.routes {
		if r.ID == hello.TunnelID && r.NodeID == hello.NodeID {
			route = r
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		http.Error(w, "unknown tunnel", http.StatusNotFound)
		return
	}
	key := webPoolKey(route.NodeID, route.ID)
	pool, ok := s.pools[key]
	if !ok {
		pool = newStandbyPool(key, standbyPoolTargetSize, standbyPoolMaxSize)
		s.pools[key] = pool
	}
	s.mu.Unlock()

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
	if _, err := bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + types.AgentWebRelayUpgrade + "\r\n\r\n"); err != nil {
		_ = conn.Close()
		return
	}
	if err := bufrw.Flush(); err != nil {
		_ = conn.Close()
		return
	}

	item := standbyConn{conn: conn, hello: hello, registeredAt: time.Now().UTC()}
	poolSize, totalStandbyCount, err := s.enqueueStandbyConn(pool, key, item)
	if err != nil {
		log.Printf("standby reverse connection rejected for tunnel %s: %v", hello.TunnelID, err)
		_ = conn.Close()
		return
	}
	log.Printf("standby reverse connection ready: node=%s tunnel=%s poolSize=%d totalStandby=%d", hello.NodeID, hello.TunnelID, poolSize, totalStandbyCount)
}

// HandleProxyRequest handles incoming requests from nginx on the proxy port.
func (s *Service) HandleProxyRequest(w http.ResponseWriter, r *http.Request) {
	route, ok := s.routeForHost(r.Host)
	if !ok {
		http.NotFound(w, r)
		return
	}

	reqID := atomic.AddUint64(&connectionCounter, 1)
	startedAt := time.Now()
	log.Printf("web request %d accepted from %s method=%s host=%s path=%s for node=%s tunnel=%s", reqID, r.RemoteAddr, r.Method, r.Host, r.URL.RequestURI(), route.NodeID, route.ID)

	standby, poolSize, totalStandbyCount, err := s.acquireStandbyConn(r.Context(), route)
	if err != nil {
		log.Printf("web request %d no standby reverse connection for tunnel %s poolSize=%d totalStandby=%d: %v", reqID, route.ID, poolSize, totalStandbyCount, err)
		http.Error(w, "web relay standby unavailable", http.StatusBadGateway)
		return
	}
	if err := standby.prepareForStart(); err != nil {
		log.Printf("web request %d discarded stale standby for tunnel %s: %v", reqID, route.ID, err)
		_ = standby.conn.Close()
		http.Error(w, "web relay standby stale", http.StatusBadGateway)
		return
	}
	if _, err := standby.conn.Write([]byte{types.AgentRelayStartByte}); err != nil {
		log.Printf("web request %d failed to start reverse session for tunnel %s: %v", reqID, route.ID, err)
		_ = standby.conn.Close()
		http.Error(w, "web relay start failed", http.StatusBadGateway)
		return
	}
	defer standby.conn.Close()

	upstreamReq := cloneHTTPRequestForRelay(r, route)
	log.Printf("web request %d forwarding host=%s proto=%s port=%s xff=%q target=%s:%d", reqID, strings.TrimSpace(upstreamReq.Header.Get("X-Forwarded-Host")), strings.TrimSpace(upstreamReq.Header.Get("X-Forwarded-Proto")), strings.TrimSpace(upstreamReq.Header.Get("X-Forwarded-Port")), strings.TrimSpace(upstreamReq.Header.Get("X-Forwarded-For")), route.TargetHost, route.TargetPort)
	if err := upstreamReq.Write(standby.conn); err != nil {
		log.Printf("web request %d write upstream request failed for tunnel %s: %v", reqID, route.ID, err)
		http.Error(w, "web relay write failed", http.StatusBadGateway)
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(standby.conn), upstreamReq)
	if err != nil {
		log.Printf("web request %d read upstream response failed for tunnel %s: %v", reqID, route.ID, err)
		http.Error(w, "web relay response failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	rewriteReport := copyHTTPResponse(w, r, route, resp)
	if rewriteReport.LocationOriginal != "" || rewriteReport.RefreshOriginal != "" {
		log.Printf("web request %d response rewrite location=%q -> %q refresh=%q -> %q", reqID, rewriteReport.LocationOriginal, rewriteReport.LocationRewritten, rewriteReport.RefreshOriginal, rewriteReport.RefreshRewritten)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("web request %d copy response body failed for tunnel %s: %v", reqID, route.ID, err)
		return
	}
	log.Printf("web request %d completed with status=%d after %s", reqID, resp.StatusCode, time.Since(startedAt))
}

// ─── standby pool management ───

func (s *Service) acquireStandbyConn(ctx context.Context, route types.TunnelSpec) (standbyConn, int, int64, error) {
	key := webPoolKey(route.NodeID, route.ID)
	for {
		s.mu.Lock()
		pool, ok := s.pools[key]
		s.mu.Unlock()
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
	for _, e := range evicted {
		atomic.AddInt64(&totalStandby, -1)
		_ = e.conn.Close()
	}
	totalAfterAdd := atomic.AddInt64(&totalStandby, 1)
	return poolSize, totalAfterAdd, nil
}

func (s *Service) closeStandbyItems(items []standbyConn) {
	for _, item := range items {
		_ = atomic.AddInt64(&totalStandby, -1)
		_ = item.conn.Close()
	}
}

func (s *Service) closeAll() {
	s.mu.Lock()
	pools := make([]*standbyPool, 0, len(s.pools))
	for _, pool := range s.pools {
		pools = append(pools, pool)
	}
	s.routes = make(map[string]types.TunnelSpec)
	s.pools = make(map[string]*standbyPool)
	s.mu.Unlock()
	for _, pool := range pools {
		s.closeStandbyItems(pool.closeAndDrain())
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
		pools = append(pools, types.RelayPoolSummary{
			PoolKey:      key,
			StandbyCount: standbyCount,
			TargetSize:   targetSize,
			MaxSize:      maxSize,
		})
	}
	s.mu.Unlock()
	return types.RelayRuntimeSummary{
		Service:      "relay-web",
		ObservedAt:   time.Now().UTC(),
		TotalStandby: int(atomic.LoadInt64(&totalStandby)),
		Pools:        pools,
	}
}

// ─── helpers ───

func helloMatchesRoute(hello types.AgentRelayHello, route types.TunnelSpec) bool {
	return hello.NodeID == route.NodeID &&
		hello.TunnelID == route.ID &&
		hello.TargetHost == route.TargetHost &&
		hello.TargetPort == route.TargetPort
}

func webPoolKey(nodeID, tunnelID string) string {
	return nodeID + ":" + tunnelID
}

func cloneHTTPRequestForRelay(r *http.Request, route types.TunnelSpec) *http.Request {
	out := r.Clone(r.Context())
	out.URL = &url.URL{Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery, ForceQuery: r.URL.ForceQuery}
	out.RequestURI = ""
	out.Host = fmt.Sprintf("%s:%d", route.TargetHost, route.TargetPort)
	out.Close = true
	out.Header = cloneHeader(r.Header)
	out.Header.Set("Host", out.Host)
	out.Header.Set("Connection", "close")
	out.Header.Del("Proxy-Connection")
	out.Header.Del("Upgrade")
	setForwardedHeaders(out.Header, r)
	return out
}

type responseRewriteReport struct {
	LocationOriginal  string
	LocationRewritten string
	RefreshOriginal   string
	RefreshRewritten  string
}

func copyHTTPResponse(w http.ResponseWriter, originalReq *http.Request, route types.TunnelSpec, resp *http.Response) responseRewriteReport {
	report := responseRewriteReport{}
	for key, values := range resp.Header {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			rewritten := rewriteResponseHeaderValue(key, value, originalReq, route)
			if strings.EqualFold(key, "Location") && value != rewritten {
				report.LocationOriginal = value
				report.LocationRewritten = rewritten
			}
			if strings.EqualFold(key, "Refresh") && value != rewritten {
				report.RefreshOriginal = value
				report.RefreshRewritten = rewritten
			}
			w.Header().Add(key, rewritten)
		}
	}
	w.WriteHeader(resp.StatusCode)
	return report
}

func cloneHeader(header http.Header) http.Header {
	cloned := make(http.Header, len(header))
	for key, values := range header {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func setForwardedHeaders(header http.Header, r *http.Request) {
	proto := requestScheme(r)
	host := canonicalForwardedHost(r.Host)
	port := forwardedPort(proto, r.Host)
	clientIP := clientIPFromRequest(r)

	appendForwardedHeaderValues(header, clientIP)
	header.Set("X-Forwarded-Proto", proto)
	header.Set("X-Forwarded-Host", host)
	header.Set("X-Forwarded-Port", port)
	header.Set("Forwarded", buildForwardedHeader(clientIP, proto, host))
}

func appendForwardedHeaderValues(header http.Header, clientIP string) {
	existing := strings.TrimSpace(header.Get("X-Forwarded-For"))
	switch {
	case existing == "":
		header.Set("X-Forwarded-For", clientIP)
	case clientIP == "":
		header.Set("X-Forwarded-For", existing)
	default:
		header.Set("X-Forwarded-For", existing+", "+clientIP)
	}
}

func requestScheme(r *http.Request) string {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") || r.TLS != nil {
		return "https"
	}
	return "http"
}

func canonicalForwardedHost(host string) string {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return ""
	}
	if parsed, err := url.Parse("http://" + trimmed); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return trimmed
}

func forwardedPort(proto, host string) string {
	if parsed, err := url.Parse("http://" + strings.TrimSpace(host)); err == nil {
		if port := parsed.Port(); port != "" {
			return port
		}
	}
	if proto == "https" {
		return "443"
	}
	return "80"
}

func clientIPFromRequest(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			return strings.TrimSpace(parts[0])
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func buildForwardedHeader(clientIP, proto, host string) string {
	parts := make([]string, 0, 3)
	if clientIP != "" {
		parts = append(parts, fmt.Sprintf("for=%q", clientIP))
	}
	if proto != "" {
		parts = append(parts, fmt.Sprintf("proto=%q", proto))
	}
	if host != "" {
		parts = append(parts, fmt.Sprintf("host=%q", host))
	}
	return strings.Join(parts, ";")
}

func rewriteResponseHeaderValue(name, value string, originalReq *http.Request, route types.TunnelSpec) string {
	trimmed := strings.TrimSpace(value)
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "location":
		return rewriteAbsoluteURLToOriginalOrigin(trimmed, originalReq, route)
	case "refresh":
		return rewriteRefreshHeader(trimmed, originalReq, route)
	default:
		return value
	}
}

func rewriteAbsoluteURLToOriginalOrigin(value string, originalReq *http.Request, route types.TunnelSpec) string {
	if value == "" {
		return value
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() {
		return value
	}
	if !sameUpstreamAuthority(parsed, route) {
		return value
	}
	rewritten := *parsed
	rewritten.Scheme = requestScheme(originalReq)
	rewritten.Host = originalReq.Host
	return rewritten.String()
}

func rewriteRefreshHeader(value string, originalReq *http.Request, route types.TunnelSpec) string {
	if value == "" {
		return value
	}
	lower := strings.ToLower(value)
	idx := strings.Index(lower, "url=")
	if idx < 0 {
		return value
	}
	prefix := value[:idx+4]
	target := strings.TrimSpace(value[idx+4:])
	return prefix + rewriteAbsoluteURLToOriginalOrigin(target, originalReq, route)
}

func sameUpstreamAuthority(parsed *url.URL, route types.TunnelSpec) bool {
	if parsed == nil {
		return false
	}
	if !strings.EqualFold(parsed.Hostname(), strings.TrimSpace(route.TargetHost)) {
		return false
	}
	parsedPort := parsed.Port()
	if parsedPort == "" {
		if strings.EqualFold(parsed.Scheme, "https") {
			parsedPort = "443"
		} else {
			parsedPort = "80"
		}
	}
	return parsedPort == fmt.Sprintf("%d", route.TargetPort)
}

func isHopByHopHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

// standbyPool.lenLocked needs the pool to expose this
func (p *standbyPool) lenLocked() int {
	return len(p.items)
}
