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
	"sync/atomic"
	"sync"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

type Service struct {
	apiBaseURL string
	httpClient *http.Client
	mu         sync.Mutex
	listeners  map[int]net.Listener
	routes     map[int]types.TunnelSpec
}

var connectionCounter uint64

func NewService(apiBaseURL string) *Service {
	return &Service{
		apiBaseURL: apiBaseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		listeners:  make(map[int]net.Listener),
		routes:     make(map[int]types.TunnelSpec),
	}
}

func (s *Service) Run(ctx context.Context) error {
	if err := s.syncRoutes(ctx); err != nil {
		log.Printf("initial tcp route sync failed: %v", err)
	}
	ticker := time.NewTicker(5 * time.Second)
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

	var payload struct {
		Items []types.TunnelSpec `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}

	desired := make(map[int]types.TunnelSpec)
	for _, item := range payload.Items {
		if item.PublicPort == 0 {
			continue
		}
		desired[item.PublicPort] = item
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for port, listener := range s.listeners {
		if _, ok := desired[port]; ok {
			continue
		}
		_ = listener.Close()
		delete(s.listeners, port)
		delete(s.routes, port)
		log.Printf("tcp route removed: %d", port)
	}

	for port, route := range desired {
		current, ok := s.routes[port]
		if ok && current.TargetHost == route.TargetHost && current.TargetPort == route.TargetPort {
			continue
		}
		if ok {
			_ = s.listeners[port].Close()
			delete(s.listeners, port)
			delete(s.routes, port)
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", itoa(port)))
		if err != nil {
			return err
		}
		s.listeners[port] = listener
		s.routes[port] = route
		go s.acceptLoop(listener, route)
		log.Printf("tcp route active: %d -> %s:%d", port, route.TargetHost, route.TargetPort)
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
		go handleConnection(conn, route)
	}
}

func handleConnection(src net.Conn, route types.TunnelSpec) {
	connID := atomic.AddUint64(&connectionCounter, 1)
	start := time.Now()
	defer src.Close()
	log.Printf("tcp connection %d accepted from %s for public port %d", connID, src.RemoteAddr().String(), route.PublicPort)

	dst, err := net.DialTimeout("tcp", net.JoinHostPort(route.TargetHost, itoa(route.TargetPort)), 5*time.Second)
	if err != nil {
		log.Printf("tcp connection %d dial target %s:%d failed: %v", connID, route.TargetHost, route.TargetPort, err)
		return
	}
	defer dst.Close()
	log.Printf("tcp connection %d connected to target %s", connID, dst.RemoteAddr().String())

	errCh := make(chan error, 2)
	go func() {
		_, copyErr := io.Copy(dst, src)
		errCh <- copyErr
		if tcpConn, ok := dst.(*net.TCPConn); ok {
			_ = tcpConn.CloseWrite()
		}
	}()
	go func() {
		_, copyErr := io.Copy(src, dst)
		errCh <- copyErr
		if tcpConn, ok := src.(*net.TCPConn); ok {
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
	log.Printf("tcp connection %d closed after %s", connID, time.Since(start))
}

func (s *Service) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for port, listener := range s.listeners {
		_ = listener.Close()
		delete(s.listeners, port)
		delete(s.routes, port)
	}
}

func itoa(v int) string {
	return fmt.Sprintf("%d", v)
}
