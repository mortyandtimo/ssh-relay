package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

type desktopConfig struct {
	APIBaseURL      string `json:"apiBaseUrl"`
	PublicEntryHost string `json:"publicEntryHost"`
	ListenAddr      string `json:"listenAddr"`
	OpenBrowser     bool   `json:"openBrowser"`
}

func main() {
	baseDir, err := executableDir()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "logs"), 0o755); err != nil {
		log.Fatal(err)
	}
	logFile, err := os.OpenFile(filepath.Join(baseDir, "logs", "desktop-launcher.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()
	log.SetOutput(io.MultiWriter(os.Stdout, logFile))

	cfg, configPath, err := loadConfig(baseDir)
	if err != nil {
		log.Fatal(err)
	}
	listenAddr := strings.TrimSpace(cfg.ListenAddr)
	if listenAddr == "" {
		listenAddr = "127.0.0.1:5180"
	}
	apiBaseURL := strings.TrimSpace(cfg.APIBaseURL)
	if apiBaseURL == "" {
		apiBaseURL = "http://127.0.0.1:7710"
	}
	publicEntryHost := strings.TrimSpace(cfg.PublicEntryHost)
	if publicEntryHost == "" {
		if host, _, splitErr := net.SplitHostPort(listenAddr); splitErr == nil && host != "" {
			publicEntryHost = host
		} else {
			publicEntryHost = "127.0.0.1"
		}
	}

	distDir := filepath.Join(baseDir, "desktop-dist")
	if _, err := os.Stat(filepath.Join(distDir, "index.html")); err != nil {
		log.Fatalf("desktop-dist missing: %v", err)
	}

	apiURL, err := url.Parse(apiBaseURL)
	if err != nil {
		log.Fatalf("invalid apiBaseUrl %q: %v", apiBaseURL, err)
	}
	proxy := httputil.NewSingleHostReverseProxy(apiURL)
	originalDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		originalDirector(r)
		r.Host = apiURL.Host
	}

	fileServer := http.FileServer(http.Dir(distDir))
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":          "ok",
			"service":         "desktop-launcher",
			"configPath":      configPath,
			"apiBaseUrl":      apiBaseURL,
			"publicEntryHost": publicEntryHost,
			"listenAddr":      listenAddr,
		})
	})
	mux.Handle("/api/", proxy)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			fileServer.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			serveIndexHTML(w, distDir, apiBaseURL, publicEntryHost)
			return
		}
		if _, err := os.Stat(filepath.Join(distDir, strings.TrimPrefix(r.URL.Path, "/"))); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndexHTML(w, distDir, apiBaseURL, publicEntryHost)
	})

	log.Printf("desktop-launcher listening on http://%s (api=%s, config=%s)", listenAddr, apiBaseURL, configPath)
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatal(err)
	}
}

func executableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

func loadConfig(baseDir string) (desktopConfig, string, error) {
	path := filepath.Join(baseDir, "desktop-config.json")
	if _, err := os.Stat(path); err != nil {
		cfg := desktopConfig{
			APIBaseURL:      config.GetEnv("DESKTOP_API_BASE_URL", "http://127.0.0.1:7710"),
			PublicEntryHost: config.GetEnv("DESKTOP_PUBLIC_ENTRY_HOST", ""),
			ListenAddr:      config.GetEnv("DESKTOP_LISTEN_ADDR", "127.0.0.1:5180"),
			OpenBrowser:     false,
		}
		body, marshalErr := json.MarshalIndent(cfg, "", "  ")
		if marshalErr != nil {
			return desktopConfig{}, path, marshalErr
		}
		if writeErr := os.WriteFile(path, body, 0o644); writeErr != nil {
			return desktopConfig{}, path, writeErr
		}
		return cfg, path, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return desktopConfig{}, path, err
	}
	var cfg desktopConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return desktopConfig{}, path, fmt.Errorf("parse desktop-config.json: %w", err)
	}
	return cfg, path, nil
}

func serveIndexHTML(w http.ResponseWriter, distDir string, apiBaseURL string, publicEntryHost string) {
	body, err := os.ReadFile(filepath.Join(distDir, "index.html"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	content := string(body)
	inject := fmt.Sprintf("<script>window.__DESKTOP_ENV__={apiBaseUrl:%q,publicEntryHost:%q};</script>", apiBaseURL, publicEntryHost)
	content = strings.Replace(content, "</head>", inject+"</head>", 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, content)
}
