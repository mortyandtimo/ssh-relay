package main

import (
	"log"

	"github.com/25743/cloud-relay-platform/packages/shared/bootstrap"
	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

func main() {
	addr := config.GetEnv("RELAY_HTTPS_ADDR", ":9092")
	tlsMode := config.GetEnv("RELAY_HTTPS_TLS_MODE", "edge-terminate")
	if err := bootstrap.Run(bootstrap.Descriptor{
		Service: "relay-https",
		Addr:    addr,
		Config: map[string]string{
			"tlsMode": tlsMode,
			"mode":    "bootstrap",
		},
	}); err != nil {
		log.Fatal(err)
	}
}

