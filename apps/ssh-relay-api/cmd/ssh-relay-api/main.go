package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/25743/cloud-relay-platform/apps/ssh-relay-api/internal/api"
	"github.com/25743/cloud-relay-platform/apps/ssh-relay-api/internal/notifier"
	"github.com/25743/cloud-relay-platform/apps/ssh-relay-api/internal/store"
)

func main() {
	addr := envOr("SSHR_LISTEN_ADDR", ":7722")
	databaseURL := requiredEnv("SSHR_DATABASE_URL")
	domain := requiredEnv("SSHR_DOMAIN")
	allowedEmailDomains := strings.TrimSpace(os.Getenv("SSHR_ALLOWED_EMAIL_DOMAINS"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := store.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	st := store.New(pool)
	if err := st.EnsureSchema(ctx); err != nil {
		log.Fatalf("ensure schema: %v", err)
	}
	if err := st.InitUsers(ctx); err != nil {
		log.Fatalf("init users: %v", err)
	}

	notifierSvc := notifier.New(notifier.Config{
		SMTPHost:     strings.TrimSpace(os.Getenv("SSHR_SMTP_HOST")),
		SMTPPort:     strings.TrimSpace(os.Getenv("SSHR_SMTP_PORT")),
		SMTPUsername: strings.TrimSpace(os.Getenv("SSHR_SMTP_USERNAME")),
		SMTPPassword: strings.TrimSpace(os.Getenv("SSHR_SMTP_PASSWORD")),
		SMTPFrom:     strings.TrimSpace(os.Getenv("SSHR_SMTP_FROM")),
	})

	srv := api.NewServer(st, notifierSvc, domain, allowedEmailDomains)
	srv.StartMonitor(context.Background())

	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv.Handler(),
	}

	go func() {
		log.Printf("ssh-relay-api starting on %s (domain: %s)", addr, domain)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	srv.StopMonitor()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func requiredEnv(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		log.Fatalf("missing required env: %s", key)
	}
	return v
}
