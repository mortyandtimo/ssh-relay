package runtime

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

const routeSyncInterval = 5 * time.Second

type routeSession struct {
	tunnel   types.TunnelSpec
	conn     net.Conn
	reader   *bufio.Reader
	sessionM sync.Mutex
}

type Service struct {
	apiBaseURL string
	httpClient *http.Client

	mu        sync.Mutex
	routes    map[int]types.TunnelSpec
	listeners map[int]*net.UDPConn
	sessions  map[string]*routeSession
}

func NewService(apiBaseURL string) *Service {
	return &Service{
		apiBaseURL: apiBaseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		routes:     map[int]types.TunnelSpec{},
		listeners:  map[int]*net.UDPConn{},
		sessions:   map[string]*routeSession{},
	}
}

func (s *Service) Run(ctx context.Context) error {
	if err := s.syncRoutes(ctx); err != nil {
		log.Printf("initial udp route sync failed: %v", err)
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
				log.Printf("sync udp routes: %v", err)
			}
		}
	}
}

func (s *Service) syncRoutes(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiBaseURL+"/internal/routes/udp", nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("udp route query failed with status %s", resp.Status)
	}
	var payload struct {
		Items []types.TunnelSpec `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	desired := map[int]types.TunnelSpec{}
	for _, item := range payload.Items {
		if item.Type != "udp" || item.Status != "active" || item.PublicPort == 0 || item.NodeID == "" {
			continue
		}
		desired[item.PublicPort] = item
	}

	type listenerStart struct {
		conn  *net.UDPConn
		route types.TunnelSpec
	}
	starts := []listenerStart{}
	staleSessionKeys := []string{}

	s.mu.Lock()
	for port, conn := range s.listeners {
		route, ok := desired[port]
		if ok {
			existing := s.routes[port]
			if sameRoute(existing, route) {
				continue
			}
			staleSessionKeys = append(staleSessionKeys, routeKey(existing))
		} else {
			staleSessionKeys = append(staleSessionKeys, routeKey(s.routes[port]))
		}
		_ = conn.Close()
		delete(s.listeners, port)
		delete(s.routes, port)
	}
	for port, route := range desired {
		if existing, ok := s.routes[port]; ok && sameRoute(existing, route) {
			continue
		}
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.listeners[port] = conn
		s.routes[port] = route
		starts = append(starts, listenerStart{conn: conn, route: route})
	}
	s.mu.Unlock()

	for _, key := range staleSessionKeys {
		s.closeSession(key)
	}
	for _, item := range starts {
		go s.serveRoute(item.conn, item.route)
		log.Printf("udp route active: node=%s publicPort=%d tunnel=%s", item.route.NodeID, item.route.PublicPort, item.route.ID)
	}
	return nil
}

func (s *Service) HandleAgentReverse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Upgrade") != types.AgentUDPRelayUpgrade {
		http.Error(w, "upgrade header required", http.StatusUpgradeRequired)
		return
	}
	var hello types.AgentUDPRelayHello
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&hello); err != nil {
		http.Error(w, "invalid udp relay hello", http.StatusBadRequest)
		return
	}
	if hello.NodeID == "" || hello.TunnelID == "" || hello.PublicPort == 0 || hello.TargetHost == "" || hello.TargetPort <= 0 {
		http.Error(w, "incomplete udp relay hello", http.StatusBadRequest)
		return
	}
	if !s.routeMatchesHello(hello) {
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
		return
	}
	if _, err := bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + types.AgentUDPRelayUpgrade + "\r\n\r\n"); err != nil {
		_ = conn.Close()
		return
	}
	if err := bufrw.Flush(); err != nil {
		_ = conn.Close()
		return
	}
	if _, err := conn.Write([]byte{types.AgentRelayStartByte}); err != nil {
		_ = conn.Close()
		return
	}
	key := routeKey(types.TunnelSpec{ID: hello.TunnelID, NodeID: hello.NodeID, PublicPort: hello.PublicPort, TargetHost: hello.TargetHost, TargetPort: hello.TargetPort})
	session := &routeSession{tunnel: types.TunnelSpec{ID: hello.TunnelID, NodeID: hello.NodeID, PublicPort: hello.PublicPort, TargetHost: hello.TargetHost, TargetPort: hello.TargetPort, Type: "udp"}, conn: conn, reader: bufio.NewReader(conn)}
	s.replaceSession(key, session)
	log.Printf("udp reverse session ready: node=%s tunnel=%s publicPort=%d", hello.NodeID, hello.TunnelID, hello.PublicPort)
}

func (s *Service) serveRoute(conn *net.UDPConn, route types.TunnelSpec) {
	buf := make([]byte, 64*1024)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Printf("udp read failed on %d: %v", route.PublicPort, err)
			}
			return
		}
		payload := append([]byte(nil), buf[:n]...)
		log.Printf("udp packet received: tunnel=%s publicPort=%d from=%s bytes=%d", route.ID, route.PublicPort, addr.String(), len(payload))
		go s.handlePacket(conn, addr, route, payload)
	}
}

func (s *Service) handlePacket(listener *net.UDPConn, client *net.UDPAddr, route types.TunnelSpec, payload []byte) {
	session, err := s.sessionForRoute(route)
	if err != nil {
		log.Printf("udp packet dropped for tunnel=%s publicPort=%d: %v", route.ID, route.PublicPort, err)
		return
	}
	frame := types.UDPDatagramFrame{SessionID: client.String(), Payload: payload}
	log.Printf("udp packet forwarding: tunnel=%s session=%s bytes=%d", route.ID, client.String(), len(payload))
	response, err := session.exchange(frame)
	if err != nil {
		s.closeSession(routeKey(route))
		log.Printf("udp exchange failed for tunnel=%s session=%s: %v", route.ID, client.String(), err)
		return
	}
	if response.Error != "" {
		log.Printf("udp response error for tunnel=%s session=%s: %s", route.ID, client.String(), response.Error)
		return
	}
	if len(response.Payload) == 0 {
		log.Printf("udp empty response: tunnel=%s session=%s", route.ID, client.String())
		return
	}
	log.Printf("udp response received: tunnel=%s session=%s bytes=%d", route.ID, client.String(), len(response.Payload))
	if _, err := listener.WriteToUDP(response.Payload, client); err != nil {
		log.Printf("udp write back failed for tunnel=%s session=%s: %v", route.ID, client.String(), err)
		return
	}
}

func (s *Service) routeMatchesHello(hello types.AgentUDPRelayHello) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	route, ok := s.routes[hello.PublicPort]
	if !ok {
		return false
	}
	return route.ID == hello.TunnelID && route.NodeID == hello.NodeID && route.TargetHost == hello.TargetHost && route.TargetPort == hello.TargetPort && route.Type == "udp"
}

func (s *Service) sessionForRoute(route types.TunnelSpec) (*routeSession, error) {
	key := routeKey(route)
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[key]
	if !ok {
		return nil, errors.New("udp agent session unavailable")
	}
	return session, nil
}

func (s *Service) replaceSession(key string, next *routeSession) {
	s.mu.Lock()
	previous := s.sessions[key]
	s.sessions[key] = next
	s.mu.Unlock()
	if previous != nil {
		_ = previous.conn.Close()
	}
}

func (s *Service) closeSession(key string) {
	s.mu.Lock()
	session := s.sessions[key]
	delete(s.sessions, key)
	s.mu.Unlock()
	if session != nil {
		_ = session.conn.Close()
	}
}

func (s *Service) closeAll() {
	s.mu.Lock()
	listeners := s.listeners
	s.listeners = map[int]*net.UDPConn{}
	s.routes = map[int]types.TunnelSpec{}
	sessions := s.sessions
	s.sessions = map[string]*routeSession{}
	s.mu.Unlock()
	for _, conn := range listeners {
		_ = conn.Close()
	}
	for _, session := range sessions {
		_ = session.conn.Close()
	}
}

func (r *routeSession) exchange(frame types.UDPDatagramFrame) (types.UDPDatagramFrame, error) {
	r.sessionM.Lock()
	defer r.sessionM.Unlock()
	if err := writeUDPFrame(r.conn, frame); err != nil {
		return types.UDPDatagramFrame{}, err
	}
	return readUDPFrame(r.reader)
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

func routeKey(route types.TunnelSpec) string {
	return route.NodeID + ":" + route.ID + ":" + fmt.Sprintf("%d", route.PublicPort)
}

func sameRoute(left, right types.TunnelSpec) bool {
	return left.ID == right.ID && left.NodeID == right.NodeID && left.PublicPort == right.PublicPort && left.TargetHost == right.TargetHost && left.TargetPort == right.TargetPort && left.Type == right.Type && left.Status == right.Status
}
