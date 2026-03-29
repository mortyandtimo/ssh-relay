package types

import "time"

type NodeCapabilities struct {
	TCPRelay   bool `json:"tcpRelay"`
	HTTPRelay  bool `json:"httpRelay"`
	HTTPSRelay bool `json:"httpsRelay"`
	UDPRelay   bool `json:"udpRelay"`
	P2PAssist  bool `json:"p2pAssist"`
}

type NodeRegisterRequest struct {
	NodeID       string            `json:"nodeId"`
	NodeName     string            `json:"nodeName"`
	AgentVersion string            `json:"agentVersion"`
	Capabilities NodeCapabilities  `json:"capabilities"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type NodeRegisterResponse struct {
	NodeID            string    `json:"nodeId"`
	RegisteredAt      time.Time `json:"registeredAt"`
	RecommendedPeriod int       `json:"recommendedHeartbeatSec"`
}

type NodeHeartbeatRequest struct {
	NodeID        string            `json:"nodeId"`
	Metrics       map[string]string `json:"metrics,omitempty"`
	ObservedAt    time.Time         `json:"observedAt"`
	ActiveTunnels int               `json:"activeTunnels"`
}

type TunnelSpec struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	TransportPolicy string            `json:"transportPolicy"`
	NodeID          string            `json:"nodeId,omitempty"`
	TargetHost      string            `json:"targetHost"`
	TargetPort      int               `json:"targetPort"`
	PublicPort      int               `json:"publicPort,omitempty"`
	Domain          string            `json:"domain,omitempty"`
	TLSMode         string            `json:"tlsMode,omitempty"`
	Status          string            `json:"status"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type AgentRelayHello struct {
	NodeID     string `json:"nodeId"`
	TunnelID   string `json:"tunnelId"`
	PublicPort int    `json:"publicPort"`
	TargetHost string `json:"targetHost"`
	TargetPort int    `json:"targetPort"`
}

type NodeSummary struct {
	NodeID        string             `json:"nodeId"`
	NodeName      string             `json:"nodeName"`
	Status        string             `json:"status"`
	AgentVersion  string             `json:"agentVersion"`
	Capabilities  NodeCapabilities   `json:"capabilities"`
	ActiveTunnels int                `json:"activeTunnels"`
	LastSeenAt    time.Time          `json:"lastSeenAt"`
	Metadata      map[string]string  `json:"metadata,omitempty"`
}

type ServerMetrics struct {
	Service            string    `json:"service"`
	StartedAt          time.Time `json:"startedAt"`
	RegisteredNodes    int       `json:"registeredNodes"`
	OnlineNodes        int       `json:"onlineNodes"`
	ConfiguredTunnels  int       `json:"configuredTunnels"`
	ProtocolRelayCount int       `json:"protocolRelayCount"`
}

type HealthResponse struct {
	Status     string            `json:"status"`
	Service    string            `json:"service"`
	ObservedAt time.Time         `json:"observedAt"`
	Details    map[string]string `json:"details,omitempty"`
}
