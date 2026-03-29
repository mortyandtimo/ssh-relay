package main

import (
	"log"

	"github.com/25743/cloud-relay-platform/packages/shared/bootstrap"
	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

func main() {
	addr := config.GetEnv("RELAY_HTTP_ADDR", ":9091")
	domainSuffix := config.GetEnv("RELAY_HTTP_DOMAIN_SUFFIX", "example.com")
	if err := bootstrap.Run(bootstrap.Descriptor{
		Service: "relay-http",
		Addr:    addr,
		Config: map[string]string{
			"domainSuffix": domainSuffix,
			"mode":         "bootstrap",
		},
	}); err != nil {
		log.Fatal(err)
	}
}

