package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

const routeSyncInterval = 5 * time.Second

type service struct {
	apiBaseURL string
	httpClient *http.Client

	mu     sync.RWMutex
	routes map[string]types.TunnelSpec
}

func newService(apiBaseURL string) *service {
	return &service{
		apiBaseURL: apiBaseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		routes:     make(map[string]types.TunnelSpec),
	}
}

func (s *service) run(ctx context.Context) error {
	if err := s.syncRoutes(ctx); err != nil {
		log.Printf("initial http route sync failed: %v", err)
	}
	ticker := time.NewTicker(routeSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.syncRoutes(ctx); err != nil {
				log.Printf("sync http routes: %v", err)
			}
		}
	}
}

func (s *service) syncRoutes(ctx context.Context) error {
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
			return fmt.Errorf("http route query (%s) failed with status %s", routeType, resp.Status)
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
	s.routes = desired
	s.mu.Unlock()
	return nil
}

func (s *service) routeForHost(host string) (types.TunnelSpec, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	s.mu.RLock()
	route, ok := s.routes[host]
	s.mu.RUnlock()
	return route, ok
}

func (s *service) serveHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := s.routeForHost(r.Host)
	if !ok {
		http.NotFound(w, r)
		return
	}
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%d", route.TargetHost, route.TargetPort)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		http.Error(rw, "http relay upstream error: "+err.Error(), http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func main() {
	addr := config.GetEnv("RELAY_HTTP_ADDR", ":9091")
	apiBaseURL := config.GetEnv("RELAY_HTTP_API_BASE_URL", "http://127.0.0.1:7710")
	domainSuffix := config.GetEnv("RELAY_HTTP_DOMAIN_SUFFIX", "")

	svc := newService(apiBaseURL)
	go func() {
		if err := svc.run(context.Background()); err != nil {
			log.Fatal(err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.HealthResponse{
			Status:     "ok",
			Service:    "relay-http",
			ObservedAt: time.Now().UTC(),
			Details: map[string]string{
				"apiBaseUrl":    apiBaseURL,
				"addr":          addr,
				"domainSuffix":  domainSuffix,
			},
		})
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"service":      "relay-http",
			"mode":         "runtime",
			"apiBaseUrl":   apiBaseURL,
			"addr":         addr,
			"domainSuffix": domainSuffix,
		})
	})
	mux.HandleFunc("/", svc.serveHTTP)

	log.Printf("relay-http listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
