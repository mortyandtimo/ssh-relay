package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

const (
	defaultP2PCLICommand   = "easytier-cli"
	defaultP2PRPCPortal    = "127.0.0.1:15888"
	defaultP2PTimeoutSec   = 3
	p2pMetricEnabledKey    = "p2p:enabled"
	p2pMetricRunningKey    = "p2p:running"
	p2pMetricRuntimeKey    = "p2p:runtime"
	p2pMetricRPCPortalKey  = "p2p:rpc_portal"
	p2pMetricHostnameKey   = "p2p:hostname"
	p2pMetricIPv4Key       = "p2p:ipv4"
	p2pMetricInstanceIDKey = "p2p:instance_id"
	p2pMetricPeerCountKey  = "p2p:peer_count"
	p2pMetricErrorKey      = "p2p:error"
)

type p2pTelemetryConfig struct {
	enabled      bool
	cliPath      string
	rpcPortal    string
	queryTimeout time.Duration
}

type easyTierNodeInfo struct {
	Hostname string `json:"hostname"`
	IPv4Addr string `json:"ipv4_addr"`
	InstID   string `json:"inst_id"`
}

type easyTierPeerInfo struct {
	Cost string `json:"cost"`
}

type p2pRuntimeSnapshot struct {
	running    bool
	hostname   string
	ipv4       string
	instanceID string
	peerCount  int
	err        string
}

func loadP2PTelemetryConfig() p2pTelemetryConfig {
	if !envBool("CLIENT_P2P_ASSIST", false) {
		return p2pTelemetryConfig{}
	}

	cliCandidate := strings.TrimSpace(config.GetEnv("CLIENT_P2P_CLI", defaultP2PCLICommand))
	resolvedCLI, err := exec.LookPath(cliCandidate)
	if err != nil {
		log.Printf("p2p telemetry disabled: easytier cli %q not found: %v", cliCandidate, err)
		return p2pTelemetryConfig{}
	}

	timeoutSec := config.GetIntEnv("CLIENT_P2P_TIMEOUT_SEC", defaultP2PTimeoutSec)
	if timeoutSec < 1 {
		timeoutSec = defaultP2PTimeoutSec
	}

	return p2pTelemetryConfig{
		enabled:      true,
		cliPath:      resolvedCLI,
		rpcPortal:    strings.TrimSpace(config.GetEnv("CLIENT_P2P_RPC_PORTAL", defaultP2PRPCPortal)),
		queryTimeout: time.Duration(timeoutSec) * time.Second,
	}
}

func collectP2PMetrics(cfg p2pTelemetryConfig) map[string]string {
	metrics := map[string]string{
		p2pMetricEnabledKey: fmt.Sprintf("%t", cfg.enabled),
	}
	if strings.TrimSpace(cfg.rpcPortal) != "" {
		metrics[p2pMetricRPCPortalKey] = cfg.rpcPortal
	}
	if !cfg.enabled {
		return metrics
	}

	snapshot := queryP2PRuntimeSnapshot(cfg)
	metrics[p2pMetricRuntimeKey] = "easytier"
	metrics[p2pMetricRunningKey] = fmt.Sprintf("%t", snapshot.running)
	metrics[p2pMetricPeerCountKey] = fmt.Sprintf("%d", snapshot.peerCount)
	if snapshot.hostname != "" {
		metrics[p2pMetricHostnameKey] = snapshot.hostname
	}
	if snapshot.ipv4 != "" {
		metrics[p2pMetricIPv4Key] = snapshot.ipv4
	}
	if snapshot.instanceID != "" {
		metrics[p2pMetricInstanceIDKey] = snapshot.instanceID
	}
	if snapshot.err != "" {
		metrics[p2pMetricErrorKey] = snapshot.err
	}
	return metrics
}

func queryP2PRuntimeSnapshot(cfg p2pTelemetryConfig) p2pRuntimeSnapshot {
	if !cfg.enabled {
		return p2pRuntimeSnapshot{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.queryTimeout)
	defer cancel()

	var node easyTierNodeInfo
	if err := runP2PJSONCommand(ctx, cfg, &node, "node", "info"); err != nil {
		return p2pRuntimeSnapshot{err: err.Error()}
	}

	snapshot := p2pRuntimeSnapshot{
		running:    true,
		hostname:   strings.TrimSpace(node.Hostname),
		ipv4:       strings.TrimSpace(node.IPv4Addr),
		instanceID: strings.TrimSpace(node.InstID),
	}

	var peers []easyTierPeerInfo
	if err := runP2PJSONCommand(ctx, cfg, &peers, "peer", "list"); err != nil {
		snapshot.err = err.Error()
		return snapshot
	}

	for _, peer := range peers {
		if strings.EqualFold(strings.TrimSpace(peer.Cost), "Local") {
			continue
		}
		snapshot.peerCount++
	}

	return snapshot
}

func runP2PJSONCommand(ctx context.Context, cfg p2pTelemetryConfig, target any, args ...string) error {
	if !cfg.enabled {
		return nil
	}

	commandArgs := []string{"-p", cfg.rpcPortal, "-o", "json"}
	commandArgs = append(commandArgs, args...)
	cmd := exec.CommandContext(ctx, cfg.cliPath, commandArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("easytier-cli %s failed: %s", strings.Join(args, " "), detail)
	}
	if err := json.Unmarshal(output, target); err != nil {
		return fmt.Errorf("decode easytier-cli %s output: %w", strings.Join(args, " "), err)
	}
	return nil
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(config.GetEnv(key, "")))
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
