package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
	"github.com/25743/cloud-relay-platform/packages/shared/config"
)

const agentVersion = "0.1.0"

func main() {
	once := flag.Bool("once", false, "register and send a single heartbeat")
	flag.Parse()

	baseURL := config.GetEnv("CLOUD_RELAY_API_URL", "http://localhost:8080")
	nodeName := config.GetEnv("CLIENT_NODE_NAME", defaultNodeName())
	nodeID := config.GetEnv("CLIENT_NODE_ID", "")
	heartbeatEvery := config.GetDurationEnvSeconds("AGENT_HEARTBEAT_INTERVAL", 30)

	client := &http.Client{Timeout: 10 * time.Second}
	registeredID, err := register(client, baseURL, nodeID, nodeName)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("agent registered as %s", registeredID)

	if err := heartbeat(client, baseURL, registeredID); err != nil {
		log.Fatal(err)
	}
	log.Printf("heartbeat accepted for %s", registeredID)

	if *once {
		return
	}

	ticker := time.NewTicker(heartbeatEvery)
	defer ticker.Stop()

	for range ticker.C {
		if err := heartbeat(client, baseURL, registeredID); err != nil {
			log.Printf("heartbeat failed: %v", err)
			continue
		}
		log.Printf("heartbeat accepted for %s", registeredID)
	}
}

func register(client *http.Client, baseURL, nodeID, nodeName string) (string, error) {
	payload := types.NodeRegisterRequest{
		NodeID:       nodeID,
		NodeName:     nodeName,
		AgentVersion: agentVersion,
		Capabilities: types.NodeCapabilities{
			TCPRelay:   true,
			HTTPRelay:  true,
			HTTPSRelay: true,
		},
		Metadata: map[string]string{
			"hostname": defaultNodeName(),
			"os":       runtime.GOOS,
			"arch":     runtime.GOARCH,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	resp, err := client.Post(baseURL+"/agent/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("register failed with status %s", resp.Status)
	}

	var out types.NodeRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.NodeID, nil
}

func heartbeat(client *http.Client, baseURL, nodeID string) error {
	payload := types.NodeHeartbeatRequest{
		NodeID:        nodeID,
		ObservedAt:    time.Now().UTC(),
		ActiveTunnels: 0,
		Metrics: map[string]string{
			"goroutines": fmt.Sprintf("%d", runtime.NumGoroutine()),
			"pid":        fmt.Sprintf("%d", os.Getpid()),
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := client.Post(baseURL+"/agent/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("heartbeat failed with status %s", resp.Status)
	}
	return nil
}

func defaultNodeName() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return "local-node"
	}
	return hostname
}

