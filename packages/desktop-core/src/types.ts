export type UserRole = "admin" | "manager" | "user";

export type UserSummary = {
  id: string;
  email: string;
  displayName: string;
  role: UserRole;
  createdAt: string;
  updatedAt: string;
};

export type NodeCapabilities = {
  tcpRelay: boolean;
  httpRelay: boolean;
  httpsRelay: boolean;
  udpRelay: boolean;
  p2pAssist: boolean;
  socks5Connect?: boolean;
};

export type NodeRuntimeSummary = {
  activeTunnelCount: number;
  relayPathCount: number;
  p2pPathCount: number;
  pendingStateCount: number;
  unavailableStateCount: number;
  failureReasonCount: number;
};

export type NodeSummary = {
  nodeId: string;
  nodeName: string;
  status: string;
  agentVersion: string;
  capabilities: NodeCapabilities;
  activeTunnels: number;
  runtimeSummary: NodeRuntimeSummary;
  lastSeenAt: string;
  metadata?: Record<string, string>;
  deploymentMode?: string;
  serviceUnit?: string;
  instanceProfile?: string;
  instanceManaged?: boolean;
  nodeRole?: "cloud" | "local" | "third_party" | "";
  environment?: "prod" | "test" | "dev" | "";
  trustLevel?: "trusted" | "limited" | "external" | "";
  owner?: string;
  location?: string;
  tags?: string[];
  isolated?: boolean;
  latestMetrics?: Record<string, string>;
};

export type NodeListResponse = {
  items: NodeSummary[];
  total: number;
  limit: number;
  offset: number;
};

export type TunnelSpec = {
  id: string;
  name: string;
  type: string;
  transportPolicy: string;
  runtimePath?: string;
  runtimeState?: string;
  lastFailureReason?: string;
  nodeId: string;
  targetHost: string;
  targetPort: number;
  publicPort: number;
  domain?: string;
  tlsMode?: string;
  probePath?: string;
  status: string;
  updatedAt?: string;
  healthStatus?: string;
  lastProbeSuccess?: boolean;
  lastProbeStatusCode?: number;
  lastProbeError?: string;
  lastProbedAt?: string;
  lastProbeTargetEntry?: string;
};

export type TunnelListResponse = {
  items: TunnelSpec[];
};

export type TunnelProbeResult = {
  tunnelId: string;
  success: boolean;
  statusCode?: number;
  error?: string;
  probedAt: string;
  targetEntry: string;
};

export type UserServiceEntry = {
  key: string;
  title: string;
  kind: string;
  summary?: string;
  registrationSource?: string;
  nodeId: string;
  nodeName?: string;
  nodeStatus?: string;
  tunnelId: string;
  tunnelName: string;
  tunnelType: string;
  tunnelStatus: string;
  transportPolicy: string;
  runtimePath?: string;
  runtimeState?: string;
  healthStatus?: string;
  publicUrl?: string;
  p2pUrl?: string;
  cloudAccess: string;
  p2pAccess: string;
  cloudAllowed: boolean;
  p2pAllowed: boolean;
  preferredPath?: string;
};

export type UserServiceCatalogResponse = {
  items: UserServiceEntry[];
};

export type ServerMetrics = {
  service: string;
  startedAt: string;
  registeredNodes: number;
  onlineNodes: number;
  configuredTunnels: number;
  protocolRelayCount: number;
};

export type ManagedHTTPSDomain = {
  domain: string;
  source: "platform" | "cert_keeper";
};

export type ManagedHTTPSDomainListResponse = {
  items: ManagedHTTPSDomain[];
};

export type AuthUserResponse = {
  user: UserSummary;
};

export type BootstrapStatusResponse = {
  required: boolean;
};

export type TunnelTypeTab = "tcp" | "udp" | "http" | "https" | "socks5";

export type ControlActionKind = "restart_agent" | "isolate_node" | "release_node" | "pause_tunnel" | "resume_tunnel";
export type ControlTargetKind = "node" | "tunnel";
export type ControlSurface = "node_console" | "operator_console";
export type ControlResult = "accepted" | "rejected" | "blocked" | "not_supported";
export type ControlExecutionMode = "placeholder" | "real";
export type ControlAvailabilityState = "available" | "blocked" | "placeholder_only";
export type ControlReadinessState = "ready" | "partial" | "blocked";
export type ControlCheckState = "pass" | "missing" | "blocked";

export type ControlCheckItem = {
  code: string;
  label: string;
  state: ControlCheckState;
  message: string;
};

export type ControlBlockedReason = {
  code: string;
  message: string;
};

export type ControlExecutionNote = {
  code: string;
  message: string;
};

export type ControlActionOption = {
  actionKind: ControlActionKind;
  targetKind: ControlTargetKind;
  targetId: string;
  sourceSurface: ControlSurface;
  contextVersion?: string;
  available: boolean;
  availabilityState: ControlAvailabilityState;
  label: string;
  message: string;
  summary?: string;
  nextStep?: string;
  primaryReasonCode?: string;
  reasonHints?: ControlBlockedReason[];
  executionMode: ControlExecutionMode;
  placeholderOnly?: boolean;
  executionNotes?: ControlExecutionNote[];
};

export type ControlActionOptionsResponse = {
  targetKind: ControlTargetKind;
  targetId: string;
  sourceSurface: ControlSurface;
  contextVersion?: string;
  executionMode: ControlExecutionMode;
  items: ControlActionOption[];
};

export type ControlPanelSummary = {
  targetKind: ControlTargetKind;
  targetId: string;
  sourceSurface: ControlSurface;
  contextVersion?: string;
  headline: string;
  summary: string;
  readinessState: ControlReadinessState;
  checks: ControlCheckItem[];
  primaryReasonCode?: string;
  nextStep?: string;
  recommendedAction?: ControlActionKind;
  executionMode: ControlExecutionMode;
  placeholderOnly?: boolean;
};

export type ControlPreflightSummary = {
  allowed: boolean;
  items: ControlCheckItem[];
  blockedReasons?: ControlBlockedReason[];
};

export type ControlActionRequest = {
  actionKind: ControlActionKind;
  targetKind: ControlTargetKind;
  targetId: string;
  sourceSurface: ControlSurface;
  dryRun: boolean;
  note?: string;
  requestedAt?: string;
};

export type ControlActionResponse = {
  result: ControlResult;
  actionKind: ControlActionKind;
  targetKind: ControlTargetKind;
  targetId: string;
  sourceSurface: ControlSurface;
  executeOutcome?: string;
  rejectionKind?: string;
  nextStep?: string;
  preflight: ControlPreflightSummary;
  humanMessage: string;
  dryRunOnly: boolean;
  executionMode: ControlExecutionMode;
  placeholderOnly?: boolean;
  executionNotes?: ControlExecutionNote[];
  facts?: Record<string, string>;
};

export type CertificateSpec = {
  id: string;
  userId: string;
  domain: string;
  certPem: string;
  expiresAt?: string;
  createdAt?: string;
  updatedAt?: string;
};
