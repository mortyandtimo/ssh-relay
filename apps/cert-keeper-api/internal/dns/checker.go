package dns

import (
	"net"
	"strings"

	"github.com/25743/cloud-relay-platform/apps/cert-keeper-api/internal/store"
)

// Check resolves the A records for domain and compares them against publicIP.
func Check(domain, publicIP string) store.DNSCheckResult {
	result := store.DNSCheckResult{}
	ips, err := net.LookupHost(domain)
	if err != nil {
		return result
	}
	result.Resolved = true
	result.IPs = ips
	publicIP = strings.TrimSpace(publicIP)
	for _, ip := range ips {
		if strings.TrimSpace(ip) == publicIP {
			result.Matches = true
			break
		}
	}
	return result
}
