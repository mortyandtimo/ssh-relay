package types

import "time"

const (
	AgentRelayConnectPath   = "/agent/reverse-tcp"
	AgentRelayUpgrade       = "cloud-relay-tcp"
	AgentRelayKeepaliveByte = byte(0x00)
	AgentRelayStartByte     = byte(0x01)
)

type NodeCapabilities struct {
	TCPRelay      bool `json:"tcpRelay"`
	HTTPRelay     bool `json:"httpRelay"`
	HTTPSRelay    bool `json:"httpsRelay"`
	UDPRelay      bool `json:"udpRelay"`
	P2PAssist     bool `json:"p2pAssist"`
	SOCKS5Connect bool `json:"socks5Connect"`
}

type NodeRole string

const (
	NodeRoleCloud      NodeRole = "cloud"
	NodeRoleLocal      NodeRole = "local"
	NodeRoleThirdParty NodeRole = "third_party"
)

type NodeEnvironment string

const (
	NodeEnvironmentProd NodeEnvironment = "prod"
	NodeEnvironmentTest NodeEnvironment = "test"
	NodeEnvironmentDev  NodeEnvironment = "dev"
)

type NodeTrustLevel string

const (
	NodeTrustTrusted  NodeTrustLevel = "trusted"
	NodeTrustLimited  NodeTrustLevel = "limited"
	NodeTrustExternal NodeTrustLevel = "external"
)

type NodeMetadata struct {
	Hostname    string            `json:"hostname,omitempty"`
	OS          string            `json:"os,omitempty"`
	Arch        string            `json:"arch,omitempty"`
	NodeRole    NodeRole          `json:"nodeRole,omitempty"`
	Environment NodeEnvironment   `json:"environment,omitempty"`
	TrustLevel  NodeTrustLevel    `json:"trustLevel,omitempty"`
	Owner       string            `json:"owner,omitempty"`
	Location    string            `json:"location,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Extra       map[string]string `json:"-"`
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

type TunnelHealthStatus string

const (
	TunnelHealthHealthy           TunnelHealthStatus = "healthy"
	TunnelHealthNodeOffline       TunnelHealthStatus = "node_offline"
	TunnelHealthCapabilityMissing TunnelHealthStatus = "capability_missing"
	TunnelHealthMisconfigured     TunnelHealthStatus = "misconfigured"
)

type TunnelSpec struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Type            string             `json:"type"`
	TransportPolicy string             `json:"transportPolicy"`
	NodeID          string             `json:"nodeId,omitempty"`
	TargetHost      string             `json:"targetHost"`
	TargetPort      int                `json:"targetPort"`
	PublicPort      int                `json:"publicPort,omitempty"`
	Domain          string             `json:"domain,omitempty"`
	TLSMode         string             `json:"tlsMode,omitempty"`
	Status          string             `json:"status"`
	HealthStatus    TunnelHealthStatus `json:"healthStatus,omitempty"`
	Metadata        map[string]string  `json:"metadata,omitempty"`
}

type AgentRelayHello struct {
	NodeID     string `json:"nodeId"`
	TunnelID   string `json:"tunnelId"`
	PublicPort int    `json:"publicPort"`
	TargetHost string `json:"targetHost"`
	TargetPort int    `json:"targetPort"`
}

type NodeSummary struct {
	NodeID        string            `json:"nodeId"`
	NodeName      string            `json:"nodeName"`
	Status        string            `json:"status"`
	AgentVersion  string            `json:"agentVersion"`
	Capabilities  NodeCapabilities  `json:"capabilities"`
	ActiveTunnels int               `json:"activeTunnels"`
	LastSeenAt    time.Time         `json:"lastSeenAt"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	NodeRole      NodeRole          `json:"nodeRole,omitempty"`
	Environment   NodeEnvironment   `json:"environment,omitempty"`
	TrustLevel    NodeTrustLevel    `json:"trustLevel,omitempty"`
	Owner         string            `json:"owner,omitempty"`
	Location      string            `json:"location,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
}

type UpdateNodeRequest struct {
	NodeRole    NodeRole        `json:"nodeRole,omitempty"`
	Environment NodeEnvironment `json:"environment,omitempty"`
	TrustLevel  NodeTrustLevel  `json:"trustLevel,omitempty"`
	Owner       string          `json:"owner,omitempty"`
	Location    string          `json:"location,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
}

type NodeListResponse struct {
	Items  []NodeSummary `json:"items"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

type NodeOption struct {
	NodeID         string `json:"nodeId"`
	NodeName       string `json:"nodeName"`
	Status         string `json:"status"`
	SupportsHTTP   bool   `json:"supportsHTTP"`
	SupportsSOCKS5 bool   `json:"supportsSOCKS5"`
}

type NodeOptionsResponse struct {
	Items []NodeOption `json:"items"`
}

type ServerMetrics struct {
	Service            string    `json:"service"`
	StartedAt          time.Time `json:"startedAt"`
	RegisteredNodes    int       `json:"registeredNodes"`
	OnlineNodes        int       `json:"onlineNodes"`
	ConfiguredTunnels  int       `json:"configuredTunnels"`
	ProtocolRelayCount int       `json:"protocolRelayCount"`
}

type RelayPoolSummary struct {
	PoolKey      string `json:"poolKey"`
	NodeID       string `json:"nodeId"`
	PublicPort   int    `json:"publicPort"`
	StandbyCount int    `json:"standbyCount"`
	TargetSize   int    `json:"targetSize"`
	MaxSize      int    `json:"maxSize"`
}

type RelayRuntimeSummary struct {
	Service      string             `json:"service"`
	ObservedAt   time.Time          `json:"observedAt"`
	TotalStandby int                `json:"totalStandby"`
	Pools        []RelayPoolSummary `json:"pools"`
}

type UserRole string

const (
	UserRoleAdmin   UserRole = "admin"
	UserRoleManager UserRole = "manager"
	UserRoleUser    UserRole = "user"
)

type UserSummary struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	Role        UserRole  `json:"role"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type AuthLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AuthUserResponse struct {
	User UserSummary `json:"user"`
}

type AuthBootstrapStatusResponse struct {
	Required bool `json:"required"`
}

type CreateUserRequest struct {
	Email       string   `json:"email"`
	DisplayName string   `json:"displayName"`
	Password    string   `json:"password,omitempty"`
	Role        UserRole `json:"role"`
}

type UpdateUserRequest struct {
	DisplayName string   `json:"displayName,omitempty"`
	Password    string   `json:"password,omitempty"`
	Role        UserRole `json:"role,omitempty"`
}

type BootstrapAdminRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
}

type AuditLogEntry struct {
	ID           int64             `json:"id"`
	ActorType    string            `json:"actorType"`
	ActorID      string            `json:"actorId,omitempty"`
	Action       string            `json:"action"`
	ResourceType string            `json:"resourceType"`
	ResourceID   string            `json:"resourceId,omitempty"`
	Payload      map[string]string `json:"payload,omitempty"`
	CreatedAt    time.Time         `json:"createdAt"`
}

type AuditLogListResponse struct {
	Items  []AuditLogEntry `json:"items"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

type HealthResponse struct {
	Status     string            `json:"status"`
	Service    string            `json:"service"`
	ObservedAt time.Time         `json:"observedAt"`
	Details    map[string]string `json:"details,omitempty"`
}
