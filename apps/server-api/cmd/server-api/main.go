package main

import (
	"context"
	"log"
	"time"

	"github.com/25743/cloud-relay-platform/apps/server-api/internal/api"
	"github.com/25743/cloud-relay-platform/apps/server-api/internal/store"
	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

func main() {
	addr := config.GetEnv("SERVER_API_ADDR", ":8080")
	databaseURL := config.GetEnv("DATABASE_URL", "")

	var apiStore store.Store
	var err error
	if databaseURL != "" {
		apiStore, err = openPostgresStore(databaseURL)
		if err != nil {
			log.Fatalf("open postgres store: %v", err)
		}
	} else {
		apiStore = store.NewInMemoryStore()
	}
	defer apiStore.Close()

	server := api.NewServer("dev", apiStore)
	log.Printf("server-api listening on %s", addr)
	if err := server.ListenAndServe(addr); err != nil {
		log.Fatal(err)
	}
}

func openPostgresStore(databaseURL string) (store.Store, error) {
	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		backend, err := store.NewPostgresStore(context.Background(), databaseURL)
		if err == nil {
			return backend, nil
		}
		lastErr = err
		log.Printf("postgres not ready yet (attempt %d/15): %v", attempt, err)
		time.Sleep(2 * time.Second)
	}
	return nil, lastErr
}
