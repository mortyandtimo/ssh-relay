package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/25743/cloud-relay-platform/apps/cert-keeper-api/internal/api"
)

func main() {
	port := envOr("CERT_KEEPER_PORT", "7720")
	dbURL := envOr("CERT_KEEPER_DATABASE_URL", "postgres://cloudrelay:CloudRelay2024!@127.0.0.1:5432/cloudrelay?sslmode=disable")
	acmeEmail := envOr("CERT_KEEPER_ACME_EMAIL", "")
	publicIP := envOr("CERT_KEEPER_PUBLIC_IP", "82.156.236.104")
	certDir := envOr("CERT_KEEPER_CERT_DIR", "/etc/cloud-relay/cert-keeper/certs")
	nginxConfDir := envOr("CERT_KEEPER_NGINX_CONF_DIR", "/etc/cloud-relay/cert-keeper/nginx")
	webroot := envOr("CERT_KEEPER_WEBROOT", "/www/server/nginx/html")
	nginxBin := envOr("CERT_KEEPER_NGINX_BIN", "")
	accessSecret := envOr("CERT_KEEPER_ACCESS_SECRET", "")
	adminWebDir := envOr("CERT_KEEPER_ADMIN_WEB_DIR", "")
	allowedOrigins := envOr("CERT_KEEPER_ALLOWED_ORIGINS", "")
	bootstrapSecret := envOr("CERT_KEEPER_BOOTSTRAP_SECRET", "")
	cookiesSecure := envOrBool("CERT_KEEPER_COOKIES_SECURE", false)

	srv, err := api.NewServer(api.Config{
		DBURL:           dbURL,
		ACMEEmail:       acmeEmail,
		PublicIP:        publicIP,
		CertDir:         certDir,
		NginxConfDir:    nginxConfDir,
		Webroot:         webroot,
		NginxBin:        nginxBin,
		AccessSecret:    accessSecret,
		AdminWebDir:     adminWebDir,
		AllowedOrigins:  allowedOrigins,
		CookiesSecure:   cookiesSecure,
		BootstrapSecret: bootstrapSecret,
	})
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: srv,
	}

	go func() {
		log.Printf("cert-keeper-api starting on :%s", port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envOrBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
