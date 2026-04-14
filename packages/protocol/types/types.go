package types

import "time"

const (
	AgentRelayConnectPath    = "/agent/reverse-tcp"
	AgentRelayUpgrade        = "cloud-relay-tcp"
	AgentRelayKeepaliveByte  = byte(0x00)
	AgentRelayStartByte      = byte(0x01)
	AgentUDPRelayConnectPath = "/agent/reverse-udp"
	AgentUDPRelayUpgrade     = "cloud-relay-udp"
	AgentWebRelayConnectPath = "/agent/reverse-web"
	AgentWebRelayUpgrade     = "cloud-relay-web"
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
	Hostname        string            `json:"hostname,omitempty"`
	OS              string            `json:"os,omitempty"`
	Arch            string            `json:"arch,omitempty"`
	DeploymentMode  string            `json:"deploymentMode,omitempty"`
	ServiceUnit     string            `json:"serviceUnit,omitempty"`
	InstanceProfile string            `json:"instanceProfile,omitempty"`
	InstanceManaged bool              `json:"instanceManaged,omitempty"`
	NodeRole        NodeRole          `json:"nodeRole,omitempty"`
	Environment     NodeEnvironment   `json:"environment,omitempty"`
	TrustLevel      NodeTrustLevel    `json:"trustLevel,omitempty"`
	Owner           string            `json:"owner,omitempty"`
	Location        string            `json:"location,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	Isolated        bool              `json:"isolated,omitempty"`
	Extra           map[string]string `json:"-"`
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
	TunnelHealthTargetUnreachable TunnelHealthStatus = "target_unreachable"
)

type TunnelProbeResult struct {
	TunnelID    string    `json:"tunnelId"`
	Success     bool      `json:"success"`
	StatusCode  int       `json:"statusCode,omitempty"`
	Error       string    `json:"error,omitempty"`
	ProbedAt    time.Time `json:"probedAt"`
	TargetEntry string    `json:"targetEntry"`
}

type ControlActionKind string

const (
	ControlActionRestartAgent ControlActionKind = "restart_agent"
	ControlActionIsolateNode  ControlActionKind = "isolate_node"
	ControlActionReleaseNode  ControlActionKind = "release_node"
	ControlActionPauseTunnel  ControlActionKind = "pause_tunnel"
	ControlActionResumeTunnel ControlActionKind = "resume_tunnel"
)

type ControlTargetKind string

const (
	ControlTargetNode   ControlTargetKind = "node"
	ControlTargetTunnel ControlTargetKind = "tunnel"
)

type ControlSurface string

const (
	ControlSurfaceNodeConsole     ControlSurface = "node_console"
	ControlSurfaceOperatorConsole ControlSurface = "operator_console"
)

type ControlResult string

const (
	ControlResultAccepted     ControlResult = "accepted"
	ControlResultRejected     ControlResult = "rejected"
	ControlResultBlocked      ControlResult = "blocked"
	ControlResultNotSupported ControlResult = "not_supported"
)

type ControlExecutionMode string

const (
	ControlExecutionPlaceholder ControlExecutionMode = "placeholder"
	ControlExecutionReal        ControlExecutionMode = "real"
)

type ControlAvailabilityState string

const (
	ControlAvailabilityAvailable       ControlAvailabilityState = "available"
	ControlAvailabilityBlocked         ControlAvailabilityState = "blocked"
	ControlAvailabilityPlaceholderOnly ControlAvailabilityState = "placeholder_only"
)

type ControlReadinessState string

const (
	ControlReadinessReady   ControlReadinessState = "ready"
	ControlReadinessPartial ControlReadinessState = "partial"
	ControlReadinessBlocked ControlReadinessState = "blocked"
)

type ControlReasonCode string

const (
	ControlReasonNodeNotBound           ControlReasonCode = "node_not_bound"
	ControlReasonTargetNotFound         ControlReasonCode = "target_not_found"
	ControlReasonNodeNotSelected        ControlReasonCode = "node_not_selected"
	ControlReasonTunnelNotSelected      ControlReasonCode = "tunnel_not_selected"
	ControlReasonNodeOffline            ControlReasonCode = "node_offline"
	ControlReasonNodeIsolated           ControlReasonCode = "node_isolated"
	ControlReasonNodeNotIsolated        ControlReasonCode = "node_not_isolated"
	ControlReasonUnmanagedInstance      ControlReasonCode = "unmanaged_instance"
	ControlReasonMissingDeploymentMode  ControlReasonCode = "missing_deployment_mode"
	ControlReasonMissingServiceUnit     ControlReasonCode = "missing_service_unit"
	ControlReasonMissingInstanceProfile ControlReasonCode = "missing_instance_profile"
	ControlReasonUnsupportedSurface     ControlReasonCode = "unsupported_surface"
	ControlReasonUnsupportedAction      ControlReasonCode = "unsupported_action"
	ControlReasonPlaceholderOnly        ControlReasonCode = "placeholder_execution_only"
	ControlReasonTunnelStateConflict    ControlReasonCode = "tunnel_state_conflict"
)

type ControlCheckState string

const (
	ControlCheckPass    ControlCheckState = "pass"
	ControlCheckMissing ControlCheckState = "missing"
	ControlCheckBlocked ControlCheckState = "blocked"
)

type ControlCheckItem struct {
	Code    string            `json:"code"`
	Label   string            `json:"label"`
	State   ControlCheckState `json:"state"`
	Message string            `json:"message"`
}

type ControlBlockedReason struct {
	Code    ControlReasonCode `json:"code"`
	Message string            `json:"message"`
}

type ControlExecutionNote struct {
	Code    ControlReasonCode `json:"code"`
	Message string            `json:"message"`
}

type ControlActionOption struct {
	ActionKind        ControlActionKind        `json:"actionKind"`
	TargetKind        ControlTargetKind        `json:"targetKind"`
	TargetID          string                   `json:"targetId"`
	SourceSurface     ControlSurface           `json:"sourceSurface"`
	ContextVersion    string                   `json:"contextVersion,omitempty"`
	Available         bool                     `json:"available"`
	AvailabilityState ControlAvailabilityState `json:"availabilityState"`
	Label             string                   `json:"label"`
	Message           string                   `json:"message"`
	Summary           string                   `json:"summary,omitempty"`
	NextStep          string                   `json:"nextStep,omitempty"`
	PrimaryReasonCode ControlReasonCode        `json:"primaryReasonCode,omitempty"`
	ReasonHints       []ControlBlockedReason   `json:"reasonHints,omitempty"`
	ExecutionMode     ControlExecutionMode     `json:"executionMode"`
	PlaceholderOnly   bool                     `json:"placeholderOnly,omitempty"`
	ExecutionNotes    []ControlExecutionNote   `json:"executionNotes,omitempty"`
}

type ControlActionOptionsResponse struct {
	TargetKind     ControlTargetKind     `json:"targetKind"`
	TargetID       string                `json:"targetId"`
	SourceSurface  ControlSurface        `json:"sourceSurface"`
	ContextVersion string                `json:"contextVersion,omitempty"`
	ExecutionMode  ControlExecutionMode  `json:"executionMode"`
	Items          []ControlActionOption `json:"items"`
}

type ControlPanelSummary struct {
	TargetKind        ControlTargetKind     `json:"targetKind"`
	TargetID          string                `json:"targetId"`
	SourceSurface     ControlSurface        `json:"sourceSurface"`
	ContextVersion    string                `json:"contextVersion,omitempty"`
	Headline          string                `json:"headline"`
	Summary           string                `json:"summary"`
	ReadinessState    ControlReadinessState `json:"readinessState"`
	Checks            []ControlCheckItem    `json:"checks"`
	PrimaryReasonCode ControlReasonCode     `json:"primaryReasonCode,omitempty"`
	NextStep          string                `json:"nextStep,omitempty"`
	RecommendedAction ControlActionKind     `json:"recommendedAction,omitempty"`
	ExecutionMode     ControlExecutionMode  `json:"executionMode"`
	PlaceholderOnly   bool                  `json:"placeholderOnly,omitempty"`
}

type ControlPreflightSummary struct {
	Allowed        bool                   `json:"allowed"`
	Items          []ControlCheckItem     `json:"items"`
	BlockedReasons []ControlBlockedReason `json:"blockedReasons,omitempty"`
}

type ControlActionRequest struct {
	ActionKind    ControlActionKind `json:"actionKind"`
	TargetKind    ControlTargetKind `json:"targetKind"`
	TargetID      string            `json:"targetId"`
	SourceSurface ControlSurface    `json:"sourceSurface"`
	DryRun        bool              `json:"dryRun"`
	Note          string            `json:"note,omitempty"`
	RequestedAt   time.Time         `json:"requestedAt,omitempty"`
}

type ControlActionResponse struct {
	Result          ControlResult           `json:"result"`
	ActionKind      ControlActionKind       `json:"actionKind"`
	TargetKind      ControlTargetKind       `json:"targetKind"`
	TargetID        string                  `json:"targetId"`
	SourceSurface   ControlSurface          `json:"sourceSurface"`
	ExecuteOutcome  string                  `json:"executeOutcome,omitempty"`
	RejectionKind   string                  `json:"rejectionKind,omitempty"`
	NextStep        string                  `json:"nextStep,omitempty"`
	Preflight       ControlPreflightSummary `json:"preflight"`
	HumanMessage    string                  `json:"humanMessage"`
	DryRunOnly      bool                    `json:"dryRunOnly"`
	ExecutionMode   ControlExecutionMode    `json:"executionMode,omitempty"`
	PlaceholderOnly bool                    `json:"placeholderOnly,omitempty"`
	ExecutionNotes  []ControlExecutionNote  `json:"executionNotes,omitempty"`
	Facts           map[string]string       `json:"facts,omitempty"`
}

type TunnelSpec struct {
	ID                   string             `json:"id"`
	Name                 string             `json:"name"`
	Type                 string             `json:"type"`
	TransportPolicy      string             `json:"transportPolicy"`
	RuntimePath          string             `json:"runtimePath,omitempty"`
	RuntimeState         string             `json:"runtimeState,omitempty"`
	LastFailureReason    string             `json:"lastFailureReason,omitempty"`
	NodeID               string             `json:"nodeId,omitempty"`
	TargetHost           string             `json:"targetHost"`
	TargetPort           int                `json:"targetPort"`
	PublicPort           int                `json:"publicPort,omitempty"`
	Domain               string             `json:"domain,omitempty"`
	TLSMode              string             `json:"tlsMode,omitempty"`
	ProbePath            string             `json:"probePath,omitempty"`
	Status               string             `json:"status"`
	UpdatedAt            time.Time          `json:"updatedAt,omitempty"`
	HealthStatus         TunnelHealthStatus `json:"healthStatus,omitempty"`
	LastProbeSuccess     bool               `json:"lastProbeSuccess,omitempty"`
	LastProbeStatusCode  int                `json:"lastProbeStatusCode,omitempty"`
	LastProbeError       string             `json:"lastProbeError,omitempty"`
	LastProbedAt         time.Time          `json:"lastProbedAt,omitempty"`
	LastProbeTargetEntry string             `json:"lastProbeTargetEntry,omitempty"`
	Metadata             map[string]string  `json:"metadata,omitempty"`
}

const (
	TunnelTransportRelayOnly    = "relay_only"
	TunnelTransportP2PPreferred = "p2p_preferred"
)

const (
	TunnelRuntimePathRelay = "relay"
	TunnelRuntimePathP2P   = "p2p"

	TunnelRuntimeStatePending     = "pending"
	TunnelRuntimeStateActive      = "active"
	TunnelRuntimeStateUnavailable = "unavailable"
)

type AgentRelayHello struct {
	NodeID     string `json:"nodeId"`
	TunnelID   string `json:"tunnelId"`
	PublicPort int    `json:"publicPort"`
	TargetHost string `json:"targetHost"`
	TargetPort int    `json:"targetPort"`
}

type AgentUDPRelayHello struct {
	NodeID     string `json:"nodeId"`
	TunnelID   string `json:"tunnelId"`
	PublicPort int    `json:"publicPort"`
	TargetHost string `json:"targetHost"`
	TargetPort int    `json:"targetPort"`
}

type CertificateSpec struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	Domain    string    `json:"domain"`
	CertPEM   string    `json:"certPem"`
	KeyPEM    string    `json:"keyPem,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

type ManagedHTTPSDomain struct {
	Domain string `json:"domain"`
	Source string `json:"source"`
}

type ManagedHTTPSDomainListResponse struct {
	Items []ManagedHTTPSDomain `json:"items"`
}

type UDPDatagramFrame struct {
	SessionID string `json:"sessionId"`
	Payload   []byte `json:"payload,omitempty"`
	Error     string `json:"error,omitempty"`
}

type NodeSummary struct {
	NodeID          string             `json:"nodeId"`
	NodeName        string             `json:"nodeName"`
	Status          string             `json:"status"`
	AgentVersion    string             `json:"agentVersion"`
	Capabilities    NodeCapabilities   `json:"capabilities"`
	ActiveTunnels   int                `json:"activeTunnels"`
	RuntimeSummary  NodeRuntimeSummary `json:"runtimeSummary"`
	LastSeenAt      time.Time          `json:"lastSeenAt"`
	Metadata        map[string]string  `json:"metadata,omitempty"`
	DeploymentMode  string             `json:"deploymentMode,omitempty"`
	ServiceUnit     string             `json:"serviceUnit,omitempty"`
	InstanceProfile string             `json:"instanceProfile,omitempty"`
	InstanceManaged bool               `json:"instanceManaged,omitempty"`
	NodeRole        NodeRole           `json:"nodeRole,omitempty"`
	Environment     NodeEnvironment    `json:"environment,omitempty"`
	TrustLevel      NodeTrustLevel     `json:"trustLevel,omitempty"`
	Owner           string             `json:"owner,omitempty"`
	Location        string             `json:"location,omitempty"`
	Tags            []string           `json:"tags,omitempty"`
	Isolated        bool               `json:"isolated,omitempty"`
	LatestMetrics   map[string]string  `json:"latestMetrics,omitempty"`
}

type NodeRuntimeSummary struct {
	ActiveTunnelCount     int `json:"activeTunnelCount"`
	RelayPathCount        int `json:"relayPathCount"`
	P2PPathCount          int `json:"p2pPathCount"`
	PendingStateCount     int `json:"pendingStateCount"`
	UnavailableStateCount int `json:"unavailableStateCount"`
	FailureReasonCount    int `json:"failureReasonCount"`
}

type UpdateNodeRequest struct {
	NodeRole    NodeRole        `json:"nodeRole,omitempty"`
	Environment NodeEnvironment `json:"environment,omitempty"`
	TrustLevel  NodeTrustLevel  `json:"trustLevel,omitempty"`
	Owner       string          `json:"owner,omitempty"`
	Location    string          `json:"location,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	Isolated    bool            `json:"isolated,omitempty"`
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
	SupportsTCP    bool   `json:"supportsTCP"`
	SupportsUDP    bool   `json:"supportsUDP"`
	SupportsHTTP   bool   `json:"supportsHTTP"`
	SupportsHTTPS  bool   `json:"supportsHTTPS"`
	SupportsSOCKS5 bool   `json:"supportsSOCKS5"`
	SupportsP2P    bool   `json:"supportsP2P"`
	Isolated       bool   `json:"isolated"`
}

type NodeOptionsResponse struct {
	Items []NodeOption `json:"items"`
}

type PortRangePlan struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	RangeStart  int    `json:"rangeStart"`
	RangeEnd    int    `json:"rangeEnd"`
	Description string `json:"description"`
}

type TunnelPortSuggestionResponse struct {
	Type       string        `json:"type"`
	Suggested  int           `json:"suggested"`
	Plan       PortRangePlan `json:"plan"`
	Compatible bool          `json:"compatible"`
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
	PoolKey         string             `json:"poolKey"`
	NodeID          string             `json:"nodeId"`
	PublicPort      int                `json:"publicPort"`
	StandbyCount    int                `json:"standbyCount"`
	TargetSize      int                `json:"targetSize"`
	MaxSize         int                `json:"maxSize"`
	TargetHealth    TunnelHealthStatus `json:"targetHealth,omitempty"`
	TargetCheckedAt time.Time          `json:"targetCheckedAt,omitempty"`
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
