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

export type AuthUserResponse = {
  user: UserSummary;
};

export type BootstrapStatusResponse = {
  required: boolean;
};

export type TunnelTypeTab = "tcp" | "udp" | "http" | "https" | "socks5";
