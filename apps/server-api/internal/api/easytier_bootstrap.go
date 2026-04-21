package api

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
)

type easyTierBootstrapConfig struct {
	networkName   string
	networkSecret string
	hostname      string
	instanceName  string
	privateMode   bool
	peers         []string
}

func loadEasyTierBootstrapConfig() *easyTierBootstrapConfig {
	envFile := strings.TrimSpace(envOrDefault("SERVER_API_EASYTIER_ENV_FILE", "/etc/cloud-relay-platform/easytier.env"))
	values, err := parseSimpleEnvFile(envFile)
	if err != nil {
		return nil
	}

	networkName := strings.TrimSpace(values["ET_NETWORK_NAME"])
	networkSecret := strings.TrimSpace(values["ET_NETWORK_SECRET"])
	hostname := strings.TrimSpace(values["ET_HOSTNAME"])
	instanceName := strings.TrimSpace(values["ET_INSTANCE_NAME"])
	privateMode := parseBoolEnv(values["ET_PRIVATE_MODE"])

	peers := parseCSV(envOrDefault("SERVER_API_EASYTIER_PEERS", ""))
	if len(peers) == 0 && hostname != "" {
		port := parsePositiveInt(envOrDefault("SERVER_API_EASYTIER_PORT", "11010"), 11010)
		peers = []string{
			fmt.Sprintf("tcp://%s:%d", hostname, port),
			fmt.Sprintf("udp://%s:%d", hostname, port),
		}
	}

	if networkName == "" || networkSecret == "" || len(peers) == 0 {
		return nil
	}

	return &easyTierBootstrapConfig{
		networkName:   networkName,
		networkSecret: networkSecret,
		hostname:      hostname,
		instanceName:  instanceName,
		privateMode:   privateMode,
		peers:         peers,
	}
}

func (c *easyTierBootstrapConfig) build(kind, overlayBaseURL string) *types.ServiceP2PBootstrap {
	if c == nil {
		return nil
	}
	if strings.TrimSpace(strings.ToLower(kind)) != "music" {
		return nil
	}
	if strings.TrimSpace(overlayBaseURL) == "" {
		return nil
	}
	peers := make([]string, len(c.peers))
	copy(peers, c.peers)
	return &types.ServiceP2PBootstrap{
		Provider:       "easytier",
		NetworkName:    c.networkName,
		NetworkSecret:  c.networkSecret,
		Peers:          peers,
		Hostname:       c.hostname,
		InstanceName:   c.instanceName,
		PrivateMode:    c.privateMode,
		OverlayBaseURL: strings.TrimSpace(overlayBaseURL),
	}
}

func parseSimpleEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}
