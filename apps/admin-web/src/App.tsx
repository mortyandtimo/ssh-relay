import { FormEvent, Fragment, ReactNode, useEffect, useRef, useState } from "react";

type NodeCapabilities = {
  tcpRelay: boolean;
  httpRelay: boolean;
  httpsRelay: boolean;
  udpRelay: boolean;
  p2pAssist: boolean;
  socks5Connect?: boolean;
};

type NodeRole = "cloud" | "local" | "third_party" | "";
type NodeEnvironment = "prod" | "test" | "dev" | "";
type NodeTrustLevel = "trusted" | "limited" | "external" | "";

type NodeSummary = {
  nodeId: string;
  nodeName: string;
  status: string;
  agentVersion: string;
  capabilities: NodeCapabilities;
  activeTunnels: number;
  lastSeenAt: string;
  metadata?: Record<string, string>;
  nodeRole?: NodeRole;
  environment?: NodeEnvironment;
  trustLevel?: NodeTrustLevel;
  owner?: string;
  location?: string;
  tags?: string[];
  isolated?: boolean;
};

type NodeListResponse = {
  items: NodeSummary[];
  total: number;
  limit: number;
  offset: number;
};

type NodeOption = {
  nodeId: string;
  nodeName: string;
  status: string;
  supportsTCP?: boolean;
  supportsUDP?: boolean;
  supportsHTTP?: boolean;
  supportsHTTPS?: boolean;
  supportsSOCKS5?: boolean;
  isolated?: boolean;
};

type NodeOptionsResponse = {
  items: NodeOption[];
};

type NodeFilterState = {
  nodeRole: NodeRole;
  environment: NodeEnvironment;
  trustLevel: NodeTrustLevel;
  owner: string;
  tag: string;
  limit: number;
  offset: number;
};

type NodeStatusFilter = "all" | "online" | "offline";
type NodeCapabilityFilter = "all" | "tcp" | "udp" | "http" | "https" | "socks5";
type NodeSortMode = "ops_priority" | "last_seen_desc" | "active_tunnels_desc" | "name_asc";

type NodeEditForm = {
  nodeRole: NodeRole;
  environment: NodeEnvironment;
  trustLevel: NodeTrustLevel;
  owner: string;
  location: string;
  tags: string;
  isolated: boolean;
};

type TunnelHealthStatus = "healthy" | "node_offline" | "capability_missing" | "misconfigured" | "target_unreachable";
type TunnelHealthFilter = "all" | "healthy" | "unhealthy";
type TunnelTypeFilter = "all" | "tcp" | "udp" | "http" | "https" | "socks5";
type ProbeFreshnessState = "not_probed" | "recent_success" | "recent_failure" | "stale";
type ProbeStateFilter = "all" | ProbeFreshnessState;
type TunnelNodeStatusFilter = "all" | "online" | "offline";
type TunnelSortMode = "ops_priority" | "updated_desc" | "name_asc" | "health_priority" | "probe_desc";

type TunnelSpec = {
  id: string;
  name: string;
  type: string;
  transportPolicy: string;
  nodeId: string;
  targetHost: string;
  targetPort: number;
  publicPort: number;
  domain?: string;
  tlsMode?: string;
  probePath?: string;
  status: string;
  updatedAt?: string;
  healthStatus?: TunnelHealthStatus;
  lastProbeSuccess?: boolean;
  lastProbeStatusCode?: number;
  lastProbeError?: string;
  lastProbedAt?: string;
  lastProbeTargetEntry?: string;
};

type ServerMetrics = {
  service: string;
  startedAt: string;
  registeredNodes: number;
  onlineNodes: number;
  configuredTunnels: number;
  protocolRelayCount: number;
};

type RelayPoolSummary = {
  poolKey: string;
  nodeId: string;
  publicPort: number;
  standbyCount: number;
  targetSize: number;
  maxSize: number;
};

type RelayRuntimeSummary = {
  service: string;
  observedAt: string;
  totalStandby: number;
  pools: RelayPoolSummary[];
};

type TunnelProbeResult = {
  tunnelId: string;
  success: boolean;
  statusCode?: number;
  error?: string;
  probedAt: string;
  targetEntry: string;
};

type PortRangePlan = {
  type: string;
  label: string;
  rangeStart: number;
  rangeEnd: number;
  description: string;
};

type TunnelPortSuggestionResponse = {
  type: string;
  suggested: number;
  plan: PortRangePlan;
  compatible: boolean;
};

type TunnelForm = {
  nodeId: string;
  name: string;
  type: "tcp" | "udp" | "http" | "https" | "socks5";
  targetHost: string;
  targetPort: string;
  publicPort: string;
  domain: string;
  tlsMode: "" | "edge_terminate";
  probePath: string;
};

type TunnelEditForm = {
  id: string;
  nodeId: string;
  name: string;
  targetHost: string;
  targetPort: string;
  publicPort: string;
  domain: string;
  tlsMode: string;
  probePath: string;
  status: string;
  type: string;
  transportPolicy: string;
};

type UserRole = "admin" | "manager" | "user";

type UserSummary = {
  id: string;
  email: string;
  displayName: string;
  role: UserRole;
  createdAt: string;
  updatedAt: string;
};

type AuditLogEntry = {
  id: number;
  actorType: string;
  actorId: string;
  action: string;
  resourceType: string;
  resourceId: string;
  payload?: Record<string, string>;
  createdAt: string;
};

type AuditLogListResponse = {
  items: AuditLogEntry[];
  total: number;
  limit: number;
  offset: number;
};

type AuditFilterState = {
  action: string;
  actorType: string;
  resourceType: string;
  actorID: string;
  startAt: string;
  endAt: string;
  limit: number;
  offset: number;
};

type MainView = "overview" | "connections" | "audit" | "permissions";
type ConnectionView = "nodes" | "tunnels";

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || "";

const initialTunnelForm: TunnelForm = {
  nodeId: "",
  name: "",
  type: "tcp",
  targetHost: "127.0.0.1",
  targetPort: "",
  publicPort: "",
  domain: "",
  tlsMode: "",
  probePath: "/",
};

const initialAuditFilter: AuditFilterState = {
  action: "",
  actorType: "",
  resourceType: "",
  actorID: "",
  startAt: "",
  endAt: "",
  limit: 20,
  offset: 0,
};

const initialNodeFilter: NodeFilterState = {
  nodeRole: "",
  environment: "",
  trustLevel: "",
  owner: "",
  tag: "",
  limit: 10,
  offset: 0,
};

export default function App() {
  const [bootstrapRequired, setBootstrapRequired] = useState<boolean | null>(null);
  const [currentUser, setCurrentUser] = useState<UserSummary | null>(null);
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [nodeTotal, setNodeTotal] = useState(0);
  const [allNodes, setAllNodes] = useState<NodeOption[]>([]);
  const [selectedNodeID, setSelectedNodeID] = useState<string | null>(null);
  const [nodeFilter, setNodeFilter] = useState<NodeFilterState>(initialNodeFilter);
  const [nodeStatusFilter, setNodeStatusFilter] = useState<NodeStatusFilter>("all");
  const [nodeCapabilityFilter, setNodeCapabilityFilter] = useState<NodeCapabilityFilter>("all");
  const [nodeSortMode, setNodeSortMode] = useState<NodeSortMode>("ops_priority");
  const [nodeEditForm, setNodeEditForm] = useState<NodeEditForm | null>(null);
  const [tunnels, setTunnels] = useState<TunnelSpec[]>([]);
  const [editingTunnelID, setEditingTunnelID] = useState<string | null>(null);
  const [tunnelEditForm, setTunnelEditForm] = useState<TunnelEditForm | null>(null);
  const [users, setUsers] = useState<UserSummary[]>([]);
  const [auditLogs, setAuditLogs] = useState<AuditLogEntry[]>([]);
  const [auditTotal, setAuditTotal] = useState(0);
  const [expandedAuditID, setExpandedAuditID] = useState<number | null>(null);
  const [metrics, setMetrics] = useState<ServerMetrics | null>(null);
  const [relayRuntime, setRelayRuntime] = useState<RelayRuntimeSummary | null>(null);
  const [probeResults, setProbeResults] = useState<Record<string, TunnelProbeResult>>({});
  const [tunnelForm, setTunnelForm] = useState<TunnelForm>(initialTunnelForm);
  const [tunnelPortSuggestion, setTunnelPortSuggestion] = useState<TunnelPortSuggestionResponse | null>(null);
  const [tunnelEditPortSuggestion, setTunnelEditPortSuggestion] = useState<TunnelPortSuggestionResponse | null>(null);
  const [tunnelHealthFilter, setTunnelHealthFilter] = useState<TunnelHealthFilter>("all");
  const [tunnelTypeFilter, setTunnelTypeFilter] = useState<TunnelTypeFilter>("all");
  const [probeStateFilter, setProbeStateFilter] = useState<ProbeStateFilter>("all");
  const [tunnelNodeStatusFilter, setTunnelNodeStatusFilter] = useState<TunnelNodeStatusFilter>("all");
  const [tunnelSortMode, setTunnelSortMode] = useState<TunnelSortMode>("ops_priority");
  const [loginForm, setLoginForm] = useState({ email: "", password: "" });
  const [bootstrapForm, setBootstrapForm] = useState({ email: "", displayName: "管理员", password: "", bootstrapSecret: "" });
  const [userForm, setUserForm] = useState({ email: "", displayName: "", password: "", role: "manager" as UserRole });
  const [auditFilter, setAuditFilter] = useState<AuditFilterState>(initialAuditFilter);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busyAction, setBusyAction] = useState("");
  const [hasInitializedNodeId, setHasInitializedNodeId] = useState(false);
  const [mainView, setMainView] = useState<MainView>("overview");
  const [connectionView, setConnectionView] = useState<ConnectionView>("nodes");
  const refreshInFlightRef = useRef<Promise<boolean> | null>(null);
  const auditFilterRef = useRef<AuditFilterState>(initialAuditFilter);
  const nodeFilterRef = useRef<NodeFilterState>(initialNodeFilter);

  useEffect(() => {
    let cancelled = false;

    async function init() {
      try {
        const status = await requestJSON<{ required: boolean }>("/api/auth/bootstrap-status");
        if (cancelled) return;
        setBootstrapRequired(status.required);
        if (status.required) return;

        const me = await requestJSON<{ user: UserSummary }>("/api/auth/me");
        if (cancelled) return;
        setCurrentUser(me.user);
        await refreshDashboard(false, me.user, cancelled, auditFilterRef.current, nodeFilterRef.current);
      } catch {
        if (!cancelled) {
          setCurrentUser(null);
        }
      }
    }

    void init();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    auditFilterRef.current = auditFilter;
  }, [auditFilter]);

  useEffect(() => {
    nodeFilterRef.current = nodeFilter;
  }, [nodeFilter]);

  useEffect(() => {
    if (!currentUser || currentUser.role === "user") {
      return;
    }
    if (connectionView !== "nodes") {
      return;
    }
    const timer = window.setTimeout(() => {
      void refreshDashboard(false, currentUser, false, auditFilterRef.current, nodeFilter);
    }, 150);
    return () => window.clearTimeout(timer);
  }, [connectionView, currentUser, nodeFilter]);

  useEffect(() => {
    if (nodes.length === 0) {
      setSelectedNodeID(null);
      setNodeEditForm(null);
      return;
    }
    setSelectedNodeID((current) => {
      if (current === null) {
        return null;
      }
      if (nodes.some((node) => node.nodeId === current)) {
        return current;
      }
      return current;
    });
  }, [nodes]);

  useEffect(() => {
    if (!selectedNodeID) {
      setNodeEditForm(null);
      return;
    }
    const selectedNode = nodes.find((node) => node.nodeId === selectedNodeID) ?? null;
    if (selectedNode) {
      setNodeEditForm(toNodeEditForm(selectedNode));
    }
  }, [nodes, selectedNodeID]);

  useEffect(() => {
    if (tunnels.length === 0) {
      setEditingTunnelID(null);
      setTunnelEditForm(null);
      return;
    }
    if (editingTunnelID && !tunnels.some((tunnel) => tunnel.id === editingTunnelID)) {
      setEditingTunnelID(null);
      setTunnelEditForm(null);
    }
  }, [editingTunnelID, tunnels]);

  useEffect(() => {
    if (!currentUser) {
      return;
    }
    let cancelled = false;
    const timer = window.setInterval(() => {
      void refreshDashboard(false, currentUser, cancelled, auditFilterRef.current, nodeFilterRef.current);
    }, 10000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [currentUser, hasInitializedNodeId]);

  useEffect(() => {
    if (!currentUser || currentUser.role === "user") {
      return;
    }
    let cancelled = false;
    void requestJSON<TunnelPortSuggestionResponse>("/api/tunnel-port-suggestion?type=" + encodeURIComponent(tunnelForm.type))
      .then((result) => {
        if (!cancelled) {
          setTunnelPortSuggestion(result);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setTunnelPortSuggestion(null);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [currentUser, tunnelForm.type]);

  useEffect(() => {
    if (!currentUser || currentUser.role === "user" || !tunnelEditForm) {
      setTunnelEditPortSuggestion(null);
      return;
    }
    let cancelled = false;
    void requestJSON<TunnelPortSuggestionResponse>("/api/tunnel-port-suggestion?type=" + encodeURIComponent(tunnelEditForm.type))
      .then((result) => {
        if (!cancelled) {
          setTunnelEditPortSuggestion(result);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setTunnelEditPortSuggestion(null);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [currentUser, tunnelEditForm]);

  async function requestJSON<T>(path: string, init?: RequestInit, allowRefresh = true): Promise<T> {
    const response = await fetch(apiBaseUrl + path, {
      credentials: "include",
      ...init,
      headers: {
        Accept: "application/json",
        ...(init?.body ? { "Content-Type": "application/json" } : {}),
        ...(init?.headers || {}),
      },
    });
    const payload = await response.json().catch(() => null);
    if (
      response.status === 401 &&
      allowRefresh &&
      path !== "/api/auth/login" &&
      path !== "/api/auth/bootstrap" &&
      path !== "/api/auth/refresh" &&
      path !== "/api/auth/bootstrap-status"
    ) {
      const refreshed = await refreshAuthSession();
      if (refreshed) {
        return requestJSON<T>(path, init, false);
      }
      resetConsoleState();
      setError("登录已失效，请重新登录。");
      throw new Error("登录已失效，请重新登录。");
    }
    if (!response.ok) {
      throw new Error(payload && typeof payload.error === "string" ? payload.error : "请求失败");
    }
    return payload as T;
  }

  async function refreshAuthSession(): Promise<boolean> {
    if (refreshInFlightRef.current) {
      return refreshInFlightRef.current;
    }
    const task = (async () => {
      const response = await fetch(apiBaseUrl + "/api/auth/refresh", {
        method: "POST",
        credentials: "include",
        headers: { Accept: "application/json" },
      });
      if (!response.ok) {
        return false;
      }
      const payload = (await response.json().catch(() => null)) as { user?: UserSummary } | null;
      if (payload?.user) {
        setCurrentUser(payload.user);
      }
      return true;
    })();
    refreshInFlightRef.current = task;
    try {
      return await task;
    } finally {
      refreshInFlightRef.current = null;
    }
  }

  function buildAuditQuery(filter: AuditFilterState) {
    const query = new URLSearchParams();
    query.set("limit", String(filter.limit));
    query.set("offset", String(filter.offset));
    if (filter.action) query.set("action", filter.action);
    if (filter.actorType) query.set("actorType", filter.actorType);
    if (filter.resourceType) query.set("resourceType", filter.resourceType);
    if (filter.actorID) query.set("actorID", filter.actorID);
    if (filter.startAt) query.set("startAt", new Date(filter.startAt).toISOString());
    if (filter.endAt) query.set("endAt", new Date(filter.endAt).toISOString());
    return query;
  }

  function buildNodeQuery(filter: NodeFilterState) {
    const query = new URLSearchParams();
    query.set("limit", String(filter.limit));
    query.set("offset", String(filter.offset));
    if (filter.nodeRole) query.set("nodeRole", filter.nodeRole);
    if (filter.environment) query.set("environment", filter.environment);
    if (filter.trustLevel) query.set("trustLevel", filter.trustLevel);
    if (filter.owner) query.set("owner", filter.owner);
    if (filter.tag) query.set("tag", filter.tag);
    return query;
  }

  async function refreshDashboard(
    showNotice: boolean,
    user = currentUser,
    cancelled = false,
    audit = auditFilterRef.current,
    nodeFilterValue = nodeFilterRef.current,
  ) {
    if (!user) {
      return;
    }

    try {
      const nodePath = "/api/nodes" + (() => {
        const query = buildNodeQuery(nodeFilterValue).toString();
        return query ? "?" + query : "";
      })();
      const allNodesPath = "/api/node-options";
      const overviewRequests =
        user.role !== "user"
          ? [
              requestJSON<NodeListResponse>(nodePath),
              requestJSON<NodeOptionsResponse>(allNodesPath),
              requestJSON<{ items: TunnelSpec[] }>("/api/tunnels"),
              requestJSON<ServerMetrics>("/api/server/metrics"),
              requestJSON<RelayRuntimeSummary>("/api/relay/tcp/runtime"),
            ]
          : [
              Promise.resolve({ items: [] as NodeSummary[], total: 0, limit: nodeFilterValue.limit, offset: nodeFilterValue.offset }),
              Promise.resolve({ items: [] as NodeOption[] }),
              Promise.resolve({ items: [] as TunnelSpec[] }),
              Promise.resolve(null as ServerMetrics | null),
              Promise.resolve(null as RelayRuntimeSummary | null),
            ];

      const userRequests =
        user.role === "admin"
          ? [requestJSON<{ items: UserSummary[] }>("/api/users")]
          : [Promise.resolve(undefined as { items: UserSummary[] } | undefined)];

      const auditRequests =
        user.role !== "user"
          ? [requestJSON<AuditLogListResponse>("/api/audit-logs?" + buildAuditQuery(audit).toString())]
          : [Promise.resolve(undefined as AuditLogListResponse | undefined)];

      const results = await Promise.all([...overviewRequests, ...userRequests, ...auditRequests]);
      if (cancelled) {
        return;
      }

      const [nodesPayload, allNodesPayload, tunnelsOnlyPayload, metricsOnlyPayload, relayOnlyPayload, usersPayloadFixed, auditPayloadFixed] = results as [
        NodeListResponse,
        NodeOptionsResponse,
        { items: TunnelSpec[] },
        ServerMetrics | null,
        RelayRuntimeSummary | null,
        { items: UserSummary[] } | undefined,
        AuditLogListResponse | undefined,
      ];

      setNodes(nodesPayload.items || []);
      setNodeTotal(nodesPayload.total || 0);
      setAllNodes(allNodesPayload.items || []);
      setTunnels(tunnelsOnlyPayload.items || []);
      setMetrics(metricsOnlyPayload);
      setRelayRuntime(relayOnlyPayload);
      setUsers(usersPayloadFixed?.items || []);
      setAuditLogs(auditPayloadFixed?.items || []);
      setAuditTotal(auditPayloadFixed?.total || 0);

      if (!hasInitializedNodeId) {
        const defaultNodeId = nodesPayload.items?.[0]?.nodeId || "";
        if (defaultNodeId) {
          setTunnelForm((current) => ({ ...current, nodeId: current.nodeId || defaultNodeId }));
          setHasInitializedNodeId(true);
        }
      }

      if (showNotice) {
        setMessage("管理数据已刷新。");
      }
      setError("");
    } catch (refreshError) {
      setError(refreshError instanceof Error ? refreshError.message : "加载管理数据失败");
    }
  }

  function resetConsoleState() {
    setCurrentUser(null);
    setNodes([]);
    setNodeTotal(0);
    setAllNodes([]);
    setSelectedNodeID(null);
    setNodeFilter(initialNodeFilter);
    setNodeEditForm(null);
    setTunnels([]);
    setEditingTunnelID(null);
    setTunnelEditForm(null);
    setUsers([]);
    setAuditLogs([]);
    setAuditTotal(0);
    setMetrics(null);
    setRelayRuntime(null);
    setProbeResults({});
    setTunnelForm(initialTunnelForm);
    setHasInitializedNodeId(false);
  }

  function beginTunnelEdit(tunnel: TunnelSpec) {
    setEditingTunnelID(tunnel.id);
    setTunnelEditForm(toTunnelEditForm(tunnel));
  }

  function clearTunnelEdit() {
    setEditingTunnelID(null);
    setTunnelEditForm(null);
  }

  async function suggestCreateTunnelPort() {
    const actionKey = "suggest-port:create:" + tunnelForm.type;
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      const result = await requestJSON<TunnelPortSuggestionResponse>("/api/tunnel-port-suggestion?type=" + encodeURIComponent(tunnelForm.type));
      setTunnelPortSuggestion(result);
      if (!result.suggested) {
        setError(result.plan.label + " 推荐端口段已无可用端口，请手工调整或清理占用。");
        return;
      }
      setTunnelForm((current) => ({ ...current, publicPort: String(result.suggested) }));
      setMessage("已填入推荐端口 " + result.suggested + "。旧 tunnel 保持兼容，不要求立即迁移。");
    } catch (suggestError) {
      setError(suggestError instanceof Error ? suggestError.message : "推荐端口失败");
    } finally {
      setBusyAction("");
    }
  }

  async function suggestEditTunnelPort() {
    if (!tunnelEditForm) {
      return;
    }
    const actionKey = "suggest-port:edit:" + tunnelEditForm.id;
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      const result = await requestJSON<TunnelPortSuggestionResponse>("/api/tunnel-port-suggestion?type=" + encodeURIComponent(tunnelEditForm.type));
      setTunnelEditPortSuggestion(result);
      if (!result.suggested) {
        setError(result.plan.label + " 推荐端口段已无可用端口，请手工调整或清理占用。");
        return;
      }
      setTunnelEditForm((current) => current ? { ...current, publicPort: String(result.suggested) } : current);
      setMessage("已填入推荐端口 " + result.suggested + "。旧 tunnel 保持兼容，不要求立即迁移。");
    } catch (suggestError) {
      setError(suggestError instanceof Error ? suggestError.message : "推荐端口失败");
    } finally {
      setBusyAction("");
    }
  }

  async function submitBootstrap(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusyAction("bootstrap");
    setError("");
    setMessage("");
    try {
      const payload = await requestJSON<{ user: UserSummary }>("/api/auth/bootstrap", {
        method: "POST",
        headers: { "X-Bootstrap-Secret": bootstrapForm.bootstrapSecret },
        body: JSON.stringify({
          email: bootstrapForm.email,
          displayName: bootstrapForm.displayName,
          password: bootstrapForm.password,
        }),
      });
      setCurrentUser(payload.user);
      setBootstrapRequired(false);
      setBootstrapForm({ email: "", displayName: "管理员", password: "", bootstrapSecret: "" });
      setMessage("管理员账户已初始化。");
      await refreshDashboard(false, payload.user, false, auditFilterRef.current, nodeFilterRef.current);
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : "初始化失败");
    } finally {
      setBusyAction("");
    }
  }

  async function submitLogin(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusyAction("login");
    setError("");
    setMessage("");
    try {
      const payload = await requestJSON<{ user: UserSummary }>("/api/auth/login", {
        method: "POST",
        body: JSON.stringify(loginForm),
      });
      setCurrentUser(payload.user);
      setMessage("登录成功。");
      setMainView(payload.user.role === "user" ? "overview" : "connections");
      void refreshDashboard(false, payload.user, false, auditFilterRef.current, nodeFilterRef.current);
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : "登录失败");
    } finally {
      setBusyAction("");
    }
  }

  async function logout() {
    setBusyAction("logout");
    setError("");
    setMessage("");
    try {
      await requestJSON<{ status: string }>("/api/auth/logout", { method: "POST" });
      resetConsoleState();
      setMessage("已退出登录。");
    } catch (logoutError) {
      setError(logoutError instanceof Error ? logoutError.message : "退出失败");
    } finally {
      setBusyAction("");
    }
  }

  async function submitNodeMetadata(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedNode || !nodeEditForm) {
      return;
    }
    const actionKey = "update-node:" + selectedNode.nodeId;
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      await requestJSON<NodeSummary>("/api/nodes/" + selectedNode.nodeId, {
        method: "PUT",
        body: JSON.stringify({
          nodeRole: nodeEditForm.nodeRole,
          environment: nodeEditForm.environment,
          trustLevel: nodeEditForm.trustLevel,
          owner: nodeEditForm.owner,
          location: nodeEditForm.location,
          tags: splitTagInput(nodeEditForm.tags),
          isolated: nodeEditForm.isolated,
        }),
      });
      setMessage("节点元数据已更新。");
      await refreshDashboard(false, currentUser, false, auditFilterRef.current, nodeFilterRef.current);
    } catch (updateError) {
      setError(updateError instanceof Error ? updateError.message : "更新节点元数据失败");
    } finally {
      setBusyAction("");
    }
  }

  async function setNodeIsolation(node: NodeSummary, isolated: boolean) {
    const actionKey = (isolated ? "isolate-node:" : "release-node:") + node.nodeId;
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      await requestJSON<NodeSummary>("/api/nodes/" + node.nodeId, {
        method: "PUT",
        body: JSON.stringify({
          nodeRole: node.nodeRole || "",
          environment: node.environment || "",
          trustLevel: node.trustLevel || "",
          owner: node.owner || "",
          location: node.location || "",
          tags: node.tags || [],
          isolated,
        }),
      });
      setMessage(isolated ? "节点已隔离。" : "节点已解除隔离。");
      await refreshDashboard(false, currentUser, false, auditFilterRef.current, nodeFilterRef.current);
    } catch (updateError) {
      setError(updateError instanceof Error ? updateError.message : "更新节点隔离状态失败");
    } finally {
      setBusyAction("");
    }
  }

  async function createTunnel(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusyAction("create-tunnel");
    setError("");
    setMessage("");
    try {
      await requestJSON<TunnelSpec>("/api/tunnels", {
        method: "POST",
        body: JSON.stringify({
          nodeId: tunnelForm.nodeId,
          name: tunnelForm.name,
          type: tunnelForm.type,
          transportPolicy: "relay_only",
          targetHost: tunnelForm.type === "socks5" ? "socks5" : tunnelForm.targetHost,
          targetPort: tunnelForm.type === "socks5" ? 1080 : Number(tunnelForm.targetPort),
          publicPort: Number(tunnelForm.publicPort),
          domain: tunnelForm.domain || undefined,
          tlsMode: tunnelForm.type === "https" ? (tunnelForm.tlsMode || "edge_terminate") : undefined,
          probePath: tunnelForm.type === "http" || tunnelForm.type === "https" ? tunnelForm.probePath : undefined,
          status: "active",
        }),
      });
      setTunnelForm((current) => ({ ...initialTunnelForm, nodeId: current.nodeId }));
      setMessage("隧道已创建。");
      await refreshDashboard(false, currentUser, false, auditFilterRef.current, nodeFilterRef.current);
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "创建隧道失败");
    } finally {
      setBusyAction("");
    }
  }

  async function submitTunnelEdit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!tunnelEditForm) {
      return;
    }
    const actionKey = "edit-tunnel:" + tunnelEditForm.id;
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      await requestJSON<TunnelSpec>("/api/tunnels/" + tunnelEditForm.id, {
        method: "PUT",
        body: JSON.stringify({
          nodeId: tunnelEditForm.nodeId,
          name: tunnelEditForm.name,
          type: tunnelEditForm.type,
          transportPolicy: tunnelEditForm.transportPolicy,
          targetHost: tunnelEditForm.targetHost,
          targetPort: Number(tunnelEditForm.targetPort),
          publicPort: Number(tunnelEditForm.publicPort),
          domain: tunnelEditForm.domain || undefined,
          tlsMode: tunnelEditForm.type === "https" ? (tunnelEditForm.tlsMode || "edge_terminate") : undefined,
          probePath: tunnelEditForm.type === "http" || tunnelEditForm.type === "https" ? tunnelEditForm.probePath : undefined,
          status: tunnelEditForm.status,
        }),
      });
      setMessage("隧道已更新。");
      await refreshDashboard(false, currentUser, false, auditFilterRef.current, nodeFilterRef.current);
    } catch (updateError) {
      setError(updateError instanceof Error ? updateError.message : "更新隧道失败");
    } finally {
      setBusyAction("");
    }
  }

  async function updateTunnelStatus(tunnel: TunnelSpec, status: "active" | "paused") {
    const actionKey = tunnel.id + ":" + status;
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      await requestJSON<TunnelSpec>("/api/tunnels/" + tunnel.id, {
        method: "PUT",
        body: JSON.stringify({
          nodeId: tunnel.nodeId,
          name: tunnel.name,
          type: tunnel.type,
          transportPolicy: tunnel.transportPolicy,
          targetHost: tunnel.targetHost,
          targetPort: tunnel.targetPort,
          publicPort: tunnel.publicPort,
          domain: tunnel.domain || undefined,
          tlsMode: tunnel.type === "https" ? (tunnel.tlsMode || "edge_terminate") : undefined,
          probePath: tunnel.type === "http" || tunnel.type === "https" ? tunnel.probePath || "/" : undefined,
          status,
        }),
      });
      setMessage(status === "active" ? "隧道已启用。" : "隧道已暂停。");
      await refreshDashboard(false, currentUser, false, auditFilterRef.current, nodeFilterRef.current);
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : "更新隧道失败");
    } finally {
      setBusyAction("");
    }
  }

  async function probeTunnel(tunnel: TunnelSpec) {
    const actionKey = tunnel.id + ":probe";
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      const result = await requestJSON<TunnelProbeResult>("/api/tunnels/" + tunnel.id + "/probe", { method: "POST" });
      setProbeResults((current) => ({ ...current, [tunnel.id]: result }));
      setMessage(result.success ? "探测完成：入口可访问。" : "探测完成：入口不可访问。");
      await refreshDashboard(false, currentUser, false, auditFilterRef.current, nodeFilterRef.current);
    } catch (probeError) {
      setError(probeError instanceof Error ? probeError.message : "探测失败");
    } finally {
      setBusyAction("");
    }
  }

  async function deleteTunnel(tunnel: TunnelSpec) {
    const actionKey = tunnel.id + ":delete";
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      await requestJSON<{ status: string }>("/api/tunnels/" + tunnel.id, { method: "DELETE" });
      if (editingTunnelID === tunnel.id) {
        clearTunnelEdit();
      }
      setMessage("隧道已删除。");
      await refreshDashboard(false, currentUser, false, auditFilterRef.current, nodeFilterRef.current);
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : "删除隧道失败");
    } finally {
      setBusyAction("");
    }
  }

  async function createUser(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusyAction("create-user");
    setError("");
    setMessage("");
    try {
      await requestJSON<UserSummary>("/api/users", {
        method: "POST",
        body: JSON.stringify(userForm),
      });
      setUserForm({ email: "", displayName: "", password: "", role: "manager" });
      setMessage("用户已创建。");
      await refreshDashboard(false, currentUser, false, auditFilterRef.current, nodeFilterRef.current);
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "创建用户失败");
    } finally {
      setBusyAction("");
    }
  }

  function submitNodeFilters(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextFilter = { ...nodeFilter, offset: 0 };
    setNodeFilter(nextFilter);
    nodeFilterRef.current = nextFilter;
    void refreshDashboard(false, currentUser, false, auditFilterRef.current, nextFilter);
  }

  function clearNodeFilters() {
    setNodeFilter(initialNodeFilter);
    nodeFilterRef.current = initialNodeFilter;
    void refreshDashboard(false, currentUser, false, auditFilterRef.current, initialNodeFilter);
  }

  function goToNodePage(nextOffset: number) {
    const nextFilter = { ...nodeFilter, offset: Math.max(0, nextOffset) };
    setNodeFilter(nextFilter);
    nodeFilterRef.current = nextFilter;
    void refreshDashboard(false, currentUser, false, auditFilterRef.current, nextFilter);
  }

  function submitAuditFilters(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextFilter = { ...auditFilter, offset: 0 };
    setAuditFilter(nextFilter);
    auditFilterRef.current = nextFilter;
    void refreshDashboard(false, currentUser, false, nextFilter, nodeFilterRef.current);
  }

  function clearAuditFilters() {
    const nextFilter = { ...initialAuditFilter };
    setAuditFilter(nextFilter);
    auditFilterRef.current = nextFilter;
    setExpandedAuditID(null);
    void refreshDashboard(false, currentUser, false, nextFilter, nodeFilterRef.current);
  }

  function goToAuditPage(nextOffset: number) {
    const nextFilter = { ...auditFilter, offset: nextOffset };
    setAuditFilter(nextFilter);
    auditFilterRef.current = nextFilter;
    void refreshDashboard(false, currentUser, false, nextFilter, nodeFilterRef.current);
  }

  const activeUser =
    currentUser ?? {
      id: "",
      email: "",
      displayName: "",
      role: "user" as UserRole,
      createdAt: "",
      updatedAt: "",
    };
  const selectedNode = selectedNodeID ? nodes.find((node) => node.nodeId === selectedNodeID) ?? null : null;
  const tunnelFormNode = allNodes.find((node) => node.nodeId === tunnelForm.nodeId) ?? null;
  const filteredNodes = sortNodes(nodes.filter((node) => matchesNodeOpsFilters(node, nodeStatusFilter, nodeFilter.nodeRole, nodeFilter.environment, nodeCapabilityFilter, nodeFilter.owner, nodeFilter.tag)), nodeSortMode);
  const selectedNodeVisibleInFilters = selectedNode ? filteredNodes.some((node) => node.nodeId === selectedNode.nodeId) : true;
  const selectedNodeTunnels = selectedNode ? sortNodeTunnels(tunnels.filter((tunnel) => tunnel.nodeId === selectedNode.nodeId)) : [];
  const selectedNodeProfile = selectedNode ? buildNodeLoadProfile(selectedNode, selectedNodeTunnels) : null;
  const selectedTunnel = editingTunnelID ? tunnels.find((tunnel) => tunnel.id === editingTunnelID) ?? null : null;
  const selectedTunnelPlacement = selectedTunnel ? explainTunnelPlacement(selectedTunnel, nodes, tunnels) : null;
  const selectedProbeResult = editingTunnelID ? probeResults[editingTunnelID] ?? null : null;
  const persistedProbeResult = selectedTunnel ? toProbeResult(selectedTunnel) : null;
  const selectedTunnelVisibleInFilters = selectedTunnel ? matchesTunnelFilters(selectedTunnel, nodes, tunnelHealthFilter, tunnelTypeFilter, probeStateFilter, tunnelNodeStatusFilter) : true;
  const onlineNodes = nodes.filter((node) => node.status === "online").length;
  const activeTunnels = tunnels.filter((tunnel) => tunnel.status === "active").length;
  const filteredTunnels = sortTunnels(
    tunnels.filter((tunnel) => matchesTunnelFilters(tunnel, nodes, tunnelHealthFilter, tunnelTypeFilter, probeStateFilter, tunnelNodeStatusFilter)),
    nodes,
    tunnelSortMode,
  );
  const unhealthyTunnelCount = tunnels.filter((tunnel) => (tunnel.healthStatus || "healthy") !== "healthy").length;
  const tcpTunnelCount = tunnels.filter((tunnel) => tunnel.type === "tcp").length;
  const httpTunnelCount = tunnels.filter((tunnel) => tunnel.type === "http").length;
  const httpsTunnelCount = tunnels.filter((tunnel) => tunnel.type === "https").length;
  const udpTunnelCount = tunnels.filter((tunnel) => tunnel.type === "udp").length;
  const socks5TunnelCount = tunnels.filter((tunnel) => tunnel.type === "socks5").length;
  const httpCapableNodeCount = allNodes.filter((node) => node.supportsHTTP).length;
  const httpsCapableNodeCount = allNodes.filter((node) => node.supportsHTTPS ?? node.supportsHTTP).length;
  const udpCapableNodeCount = allNodes.filter((node) => node.supportsUDP).length;
  const socks5CapableNodeCount = allNodes.filter((node) => node.supportsSOCKS5).length;
  const offlineNodeCount = nodes.filter((node) => node.status !== "online").length;
  const cloudNodeCount = nodes.filter((node) => node.nodeRole === "cloud").length;
  const localNodeCount = nodes.filter((node) => node.nodeRole === "local").length;
  const thirdPartyNodeCount = nodes.filter((node) => node.nodeRole === "third_party").length;
  const prodNodeCount = nodes.filter((node) => node.environment === "prod").length;
  const testNodeCount = nodes.filter((node) => node.environment === "test").length;
  const devNodeCount = nodes.filter((node) => node.environment === "dev").length;
  const highestLoadNode = nodes.length ? [...nodes].sort((left, right) => right.activeTunnels - left.activeTunnels)[0] : null;
  const unprobedTunnelCount = tunnels.filter((tunnel) => deriveProbeFreshnessState(tunnel) === "not_probed").length;
  const recentFailedTunnelCount = tunnels.filter((tunnel) => deriveProbeFreshnessState(tunnel) === "recent_failure").length;
  const staleTunnelCount = tunnels.filter((tunnel) => deriveProbeFreshnessState(tunnel) === "stale").length;
  const overloadedNodes = nodes.filter((node) => buildNodeLoadProfile(node, tunnels.filter((tunnel) => tunnel.nodeId === node.nodeId)).loadState === "high_load");
  const idleNodes = nodes.filter((node) => buildNodeLoadProfile(node, tunnels.filter((tunnel) => tunnel.nodeId === node.nodeId)).loadState === "idle");
  const nodesWithProblemTunnels = nodes.filter((node) => buildNodeLoadProfile(node, tunnels.filter((tunnel) => tunnel.nodeId === node.nodeId)).problemCount > 0);
  const canOperate = activeUser.role !== "user";
  const canManageUsers = activeUser.role === "admin";
  const nodeStart = nodes.length === 0 ? 0 : nodeFilter.offset + 1;
  const nodeEnd = Math.min(nodeFilter.offset + nodeFilter.limit, nodeTotal);
  const auditStart = auditLogs.length === 0 ? 0 : auditFilter.offset + 1;
  const auditEnd = Math.min(auditFilter.offset + auditFilter.limit, auditTotal);
  const groupedNodes = groupNodesByRole(nodes);

  if (bootstrapRequired === null) {
    return (
      <div className="workspace-shell auth-shell">
        <div className="auth-card">正在检查管理面初始化状态...</div>
      </div>
    );
  }

  if (bootstrapRequired) {
    return (
      <div className="workspace-shell auth-shell">
        <section className="auth-card">
          <p className="eyebrow">初始化</p>
          <h1>首次安装初始化</h1>
          <p className="summary">输入管理员信息和 bootstrap secret，初始化后直接进入控制台。</p>
          {error ? <div className="error">{error}</div> : null}
          <form className="form-grid" onSubmit={submitBootstrap}>
            <label><span>邮箱</span><input value={bootstrapForm.email} onChange={(event) => setBootstrapForm((current) => ({ ...current, email: event.target.value }))} required /></label>
            <label><span>显示名称</span><input value={bootstrapForm.displayName} onChange={(event) => setBootstrapForm((current) => ({ ...current, displayName: event.target.value }))} required /></label>
            <label><span>密码</span><input type="password" value={bootstrapForm.password} onChange={(event) => setBootstrapForm((current) => ({ ...current, password: event.target.value }))} required /></label>
            <label><span>初始化密钥</span><input type="password" value={bootstrapForm.bootstrapSecret} onChange={(event) => setBootstrapForm((current) => ({ ...current, bootstrapSecret: event.target.value }))} required /></label>
            <button type="submit" disabled={busyAction === "bootstrap"}>{busyAction === "bootstrap" ? "初始化中..." : "创建管理员"}</button>
          </form>
        </section>
      </div>
    );
  }

  if (!currentUser) {
    return (
      <div className="workspace-shell auth-shell">
        <section className="auth-card">
          <p className="eyebrow">登录</p>
          <h1>控制台登录</h1>
          <p className="summary">登录后进入真正的工作区切换，而不是锚点长页面。</p>
          {error ? <div className="error">{error}</div> : null}
          <form className="form-grid" onSubmit={submitLogin}>
            <label><span>邮箱</span><input value={loginForm.email} onChange={(event) => setLoginForm((current) => ({ ...current, email: event.target.value }))} required /></label>
            <label><span>密码</span><input type="password" value={loginForm.password} onChange={(event) => setLoginForm((current) => ({ ...current, password: event.target.value }))} required /></label>
            <button type="submit" disabled={busyAction === "login"}>{busyAction === "login" ? "登录中..." : "登录"}</button>
          </form>
        </section>
      </div>
    );
  }

  return (
    <div className="workspace-shell">
      <aside className="workspace-sidebar">
        <div className="sidebar-card brand-card">
          <p className="eyebrow">云中继控制台</p>
          <h1>控制台</h1>
          <p className="sidebar-copy">切换工作区而不是在同一页里反复滚动寻找目标模块。</p>
        </div>

        <div className="sidebar-card operator-card">
          <span className={roleClass(activeUser.role)}>{activeUser.role}</span>
          <strong>{activeUser.displayName}</strong>
          <span className="muted-line">{activeUser.email}</span>
          <div className="sidebar-actions">
            <button className="secondary" type="button" onClick={() => void refreshDashboard(true, currentUser, false, auditFilterRef.current, nodeFilterRef.current)} disabled={busyAction === "refresh"}>刷新当前工作区</button>
            <button type="button" onClick={() => void logout()} disabled={busyAction === "logout"}>{busyAction === "logout" ? "退出中..." : "退出登录"}</button>
          </div>
        </div>

        <nav className="sidebar-card workspace-nav">
          <button type="button" className={mainView === "overview" ? "nav-tab active" : "nav-tab"} onClick={() => setMainView("overview")}>总览</button>
          {canOperate ? <button type="button" className={mainView === "connections" ? "nav-tab active" : "nav-tab"} onClick={() => setMainView("connections")}>连接管理</button> : null}
          {canOperate ? <button type="button" className={mainView === "audit" ? "nav-tab active" : "nav-tab"} onClick={() => setMainView("audit")}>审计</button> : null}
          {canManageUsers ? <button type="button" className={mainView === "permissions" ? "nav-tab active" : "nav-tab"} onClick={() => setMainView("permissions")}>权限</button> : null}
        </nav>

        {activeUser.role !== "user" ? (
          <div className="sidebar-card mini-dashboard">
            <MiniStat label="在线节点" value={String(onlineNodes)} />
            <MiniStat label="活跃隧道" value={String(activeTunnels)} />
            <MiniStat label="待命池" value={String(relayRuntime?.pools.length ?? 0)} />
            <MiniStat label="隧道异常" value={String(unhealthyTunnelCount)} />
            <MiniStat label="审计总数" value={String(auditTotal)} />
          </div>
        ) : null}
      </aside>

      <main className="workspace-main">
        {error ? <div className="error">{error}</div> : null}
        {message ? <div className="notice">{message}</div> : null}

        {mainView === "overview" ? (
          <section className="workspace-panel">
            <div className="section-head">
              <div>
                <p className="eyebrow">总览</p>
                <h2>系统总览</h2>
              </div>
              <span className="muted-line">{activeUser.role === "user" ? "当前角色仅显示允许查看的摘要信息" : "统一观察节点、隧道、待命池和审计窗口"}</span>
            </div>

            {activeUser.role === "user" ? (
              <div className="restricted-state">
                <strong>当前角色为只读受限视角</strong>
                <p>你可以看到控制台的基础状态与身份信息，但节点、隧道、审计和权限工作区不会展示可误导的 0 值面板。</p>
              </div>
            ) : (
              <>
                <div className="overview-grid">
                  <MetricCard label="注册节点" value={String(metrics?.registeredNodes ?? nodes.length)} hint="控制平面已注册" />
                  <MetricCard label="在线节点" value={String(metrics?.onlineNodes ?? onlineNodes)} hint="当前可通信节点" />
                  <MetricCard label="活跃隧道" value={String(activeTunnels)} hint="当前为 active 的入口配置" />
                  <MetricCard label="待命连接" value={String(relayRuntime?.totalStandby ?? 0)} hint="反向 TCP 待命池" />
                </div>
                <div className="signal-strip">
                  <SignalCard label="服务" value={metrics?.service ?? "server-api"} />
                  <SignalCard label="启动时间" value={metrics ? formatDate(metrics.startedAt) : "-"} />
                  <SignalCard label="异常隧道" value={unhealthyTunnelCount === 0 ? "无" : String(unhealthyTunnelCount)} />
                  <SignalCard label="审计窗口" value={auditTotal === 0 ? "暂无" : auditStart + "-" + auditEnd + " / " + auditTotal} />
                  <SignalCard label="最新动作" value={auditLogs[0]?.action ?? "-"} />
                </div>
                <section className="subpanel">
                  <div className="section-head compact-head">
                    <div>
                      <h3>能力与入口总览</h3>
                      <span className="muted-line">区分当前有哪些入口类型、多少异常入口，以及哪些节点具备对应挂载能力。</span>
                    </div>
                  </div>
                  <div className="overview-grid">
                    <MetricCard label="隧道总数" value={String(tunnels.length)} hint="当前全部 tunnel 配置数" />
                    <MetricCard label="Active 数" value={String(activeTunnels)} hint="当前处于 active 的 tunnel" />
                    <MetricCard label="HTTP 入口" value={String(httpTunnelCount)} hint="http://IP:端口 发布 Web/API" />
                    <MetricCard label="HTTPS 入口" value={String(httpsTunnelCount)} hint="https://domain 标准 443 入口" />
                    <MetricCard label="UDP 最小版" value={String(udpTunnelCount)} hint="udp://IP:端口 最小公网闭环已验证" />
                    <MetricCard label="SOCKS5 入口" value={String(socks5TunnelCount)} hint="socks5://IP:端口 代理入口" />
                    <MetricCard label="TCP 入口" value={String(tcpTunnelCount)} hint="host:port 直连入口" />
                  </div>
                  <div className="signal-strip">
                    <SignalCard label="异常入口数" value={unhealthyTunnelCount === 0 ? "无" : String(unhealthyTunnelCount)} />
                    <SignalCard label="未探测" value={unprobedTunnelCount === 0 ? "无" : String(unprobedTunnelCount)} />
                    <SignalCard label="最近失败" value={recentFailedTunnelCount === 0 ? "无" : String(recentFailedTunnelCount)} />
                    <SignalCard label="结果较旧" value={staleTunnelCount === 0 ? "无" : String(staleTunnelCount)} />
                    <SignalCard label="支持 HTTP 的节点" value={String(httpCapableNodeCount)} />
                    <SignalCard label="支持 HTTPS 的节点" value={String(httpsCapableNodeCount)} />
                    <SignalCard label="支持 UDP 的节点" value={String(udpCapableNodeCount)} />
                    <SignalCard label="UDP 里程碑" value={udpTunnelCount > 0 ? "公网 echo 已验证" : "待创建 UDP tunnel"} />
                    <SignalCard label="支持 SOCKS5 的节点" value={String(socks5CapableNodeCount)} />
                  </div>
                </section>
                <section className="subpanel">
                  <div className="section-head compact-head">
                    <div>
                      <h3>节点负载与承载信号</h3>
                      <span className="muted-line">统一观察哪些节点高承载、哪些节点空闲、哪些节点挂了异常入口，为后续人工编排提供依据。</span>
                    </div>
                  </div>
                  <div className="signal-strip">
                    <SignalCard label="高承载节点" value={overloadedNodes.length === 0 ? "无" : String(overloadedNodes.length)} />
                    <SignalCard label="空闲节点" value={idleNodes.length === 0 ? "无" : String(idleNodes.length)} />
                    <SignalCard label="异常节点" value={nodesWithProblemTunnels.length === 0 ? "无" : String(nodesWithProblemTunnels.length)} />
                    <SignalCard label="最高承载节点" value={highestLoadNode ? highestLoadNode.nodeName + " / " + highestLoadNode.activeTunnels : "-"} />
                  </div>
                  <div className="spotlight-grid compact-cards load-signal-grid">
                    {overloadedNodes.length > 0 ? overloadedNodes.slice(0, 3).map((node) => {
                      const profile = buildNodeLoadProfile(node, tunnels.filter((tunnel) => tunnel.nodeId === node.nodeId));
                      return <article key={node.nodeId} className="spotlight-card compact-signal-card"><strong>{node.nodeName}</strong><span className="muted-line">高承载 / {profile.activeCount} active / {profile.protocolSummary}</span></article>;
                    }) : <article className="spotlight-card compact-signal-card"><strong>当前无高承载节点</strong><span className="muted-line">暂无节点达到高承载阈值。</span></article>}
                    {nodesWithProblemTunnels.length > 0 ? nodesWithProblemTunnels.slice(0, 3).map((node) => {
                      const profile = buildNodeLoadProfile(node, tunnels.filter((tunnel) => tunnel.nodeId === node.nodeId));
                      return <article key={node.nodeId + ":problem"} className="spotlight-card compact-signal-card problem-card"><strong>{node.nodeName}</strong><span className="muted-line">异常入口 {profile.problemCount} 个 / {profile.protocolSummary}</span></article>;
                    }) : <article className="spotlight-card compact-signal-card"><strong>当前无异常承载节点</strong><span className="muted-line">没有节点承载异常 tunnel。</span></article>}
                    {idleNodes.length > 0 ? idleNodes.slice(0, 3).map((node) => (
                      <article key={node.nodeId + ":idle"} className="spotlight-card compact-signal-card"><strong>{node.nodeName}</strong><span className="muted-line">空闲 / 暂无 active tunnel</span></article>
                    )) : <article className="spotlight-card compact-signal-card"><strong>当前无空闲节点</strong><span className="muted-line">所有节点都有一定承载。</span></article>}
                  </div>
                </section>
                {relayRuntime?.pools?.length ? (
                  <div className="pool-band">
                    {relayRuntime.pools.map((pool) => (
                      <div key={pool.poolKey} className="pool-chip">
                        <strong>{pool.poolKey}</strong>
                        <span>{pool.standbyCount} / {pool.maxSize}</span>
                      </div>
                    ))}
                  </div>
                ) : null}
              </>
            )}
          </section>
        ) : null}

        {mainView === "connections" && canOperate ? (
          <section className="workspace-panel">
            <div className="section-head">
              <div>
                <p className="eyebrow">连接</p>
                <h2>连接管理</h2>
              </div>
              <div className="inline-switches">
                <button type="button" className={connectionView === "nodes" ? "nav-tab active" : "nav-tab"} onClick={() => setConnectionView("nodes")}>节点</button>
                <button type="button" className={connectionView === "tunnels" ? "nav-tab active" : "nav-tab"} onClick={() => setConnectionView("tunnels")}>隧道</button>
              </div>
            </div>

            {connectionView === "nodes" ? (
              <div className="ops-layout node-ops-layout">
                <div className="ops-sidebar panel-stack">
                  <section className="subpanel workspace-column">
                    <div className="section-head compact-head">
                      <div>
                        <h3>节点运维筛选台</h3>
                        <span className="muted-line">左侧保留筛选与节点列表，右侧固定为节点运维工作区，避免详情继续被压成窄栏。</span>
                      </div>
                    </div>
                    <form className="form-grid" onSubmit={submitNodeFilters}>
                      <label>
                        <span>节点状态</span>
                        <select value={nodeStatusFilter} onChange={(event) => setNodeStatusFilter(event.target.value as NodeStatusFilter)}>
                          <option value="all">全部</option>
                          <option value="online">online</option>
                          <option value="offline">offline</option>
                        </select>
                      </label>
                      <label>
                        <span>节点角色</span>
                        <select value={nodeFilter.nodeRole} onChange={(event) => setNodeFilter((current) => ({ ...current, nodeRole: event.target.value as NodeRole, offset: 0 }))}>
                          <option value="">全部</option>
                          <option value="cloud">cloud</option>
                          <option value="local">local</option>
                          <option value="third_party">third_party</option>
                        </select>
                      </label>
                      <label>
                        <span>环境</span>
                        <select value={nodeFilter.environment} onChange={(event) => setNodeFilter((current) => ({ ...current, environment: event.target.value as NodeEnvironment, offset: 0 }))}>
                          <option value="">全部</option>
                          <option value="prod">prod</option>
                          <option value="test">test</option>
                          <option value="dev">dev</option>
                        </select>
                      </label>
                      <label>
                        <span>能力</span>
                        <select value={nodeCapabilityFilter} onChange={(event) => setNodeCapabilityFilter(event.target.value as NodeCapabilityFilter)}>
                          <option value="all">全部</option>
                          <option value="tcp">TCP</option>
                          <option value="http">HTTP</option>
                          <option value="https">HTTPS</option>
                          <option value="socks5">SOCKS5</option>
                        </select>
                      </label>
                      <label>
                        <span>排序</span>
                        <select value={nodeSortMode} onChange={(event) => setNodeSortMode(event.target.value as NodeSortMode)}>
                          <option value="ops_priority">默认：离线/高承载优先</option>
                          <option value="last_seen_desc">按最后在线时间</option>
                          <option value="active_tunnels_desc">按承载 tunnel 数</option>
                          <option value="name_asc">按名称</option>
                        </select>
                      </label>
                      <label>
                        <span>负责人</span>
                        <input value={nodeFilter.owner} onChange={(event) => setNodeFilter((current) => ({ ...current, owner: event.target.value, offset: 0 }))} />
                      </label>
                      <label>
                        <span>标签</span>
                        <input value={nodeFilter.tag} onChange={(event) => setNodeFilter((current) => ({ ...current, tag: event.target.value, offset: 0 }))} placeholder="例如 win 或 office" />
                      </label>
                      <div className="form-actions">
                        <button type="submit">应用筛选</button>
                        <button type="button" className="secondary" onClick={clearNodeFilters}>清空筛选</button>
                      </div>
                    </form>

                    {!selectedNodeVisibleInFilters && selectedNode ? <div className="empty-state list-selection-note"><strong>当前选中节点未命中筛选结果</strong><p>详情仍保留在右侧工作区，便于继续排障；如需在左侧列表重新看到它，请调整筛选条件。</p></div> : null}

                    <div className="section-head compact-head">
                      <h3>节点列表</h3>
                      <span className="muted-line">显示 {filteredNodes.length === 0 ? 0 : 1}-{filteredNodes.length} / {nodes.length}</span>
                    </div>
                    <div className="table-wrap compact-table">
                      <table>
                        <thead><tr><th>节点</th><th>状态</th><th>角色/环境</th><th>承载</th><th>能力</th><th>标签</th></tr></thead>
                        <tbody>
                          {filteredNodes.length === 0 ? <tr><td colSpan={6}>当前筛选条件下暂无节点。</td></tr> : filteredNodes.map((node) => (
                            <tr key={node.nodeId} className={selectedNodeID === node.nodeId ? "clickable-row selected-row" : "clickable-row"} onClick={() => setSelectedNodeID((current) => current === node.nodeId ? null : node.nodeId)}>
                              <td><strong>{node.nodeName}</strong><div className="muted">{node.nodeId}</div><div className="muted">{nodeAgentDeploymentLabel(node)}</div>{node.isolated ? <div><span className="status-pill tone-danger">已隔离</span></div> : null}</td>
                              <td><span className={statusPillClass(node.status)}>{node.status}</span><div className="muted">{formatDate(node.lastSeenAt)}</div></td>
                              <td><div>{node.nodeRole ? roleLabel(node.nodeRole) : "-"}</div><div className="muted">{node.environment || "-"} / {node.trustLevel || "-"}</div></td>
                              <td><div>{node.activeTunnels}</div><div className="muted">负责人 {node.owner || "-"}</div></td>
                              <td><div className="table-status-stack"><span className={capabilityPillClass(node.capabilities.tcpRelay)}>TCP</span><span className={capabilityPillClass(node.capabilities.udpRelay)}>UDP</span><span className={capabilityPillClass(node.capabilities.httpRelay)}>HTTP</span><span className={capabilityPillClass(node.capabilities.httpsRelay || node.capabilities.httpRelay)}>HTTPS</span><span className={capabilityPillClass(Boolean(node.capabilities.socks5Connect))}>SOCKS5</span></div></td>
                              <td>{formatTags(node.tags)}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>

                    <div className="actions-row audit-pager">
                      <span className="inline-note">当前节点工作区使用前端派生筛选/排序，便于快速运维判断；分页接口仍保留但本视图以当前已加载节点为准。</span>
                    </div>
                  </section>
                </div>

                <section className="subpanel detail-panel ops-workbench node-workbench">
                  <div className="section-head compact-head">
                    <div>
                      <h3>节点运维工作区</h3>
                      <span className="muted-line">右侧固定承载基础状态、节点属性、能力矩阵与节点承载入口，不再压成一条细长侧栏。</span>
                    </div>
                  </div>
                  {selectedNode && nodeEditForm ? (
                    <>
                      <div className="detail-hero workbench-hero">
                        <div>
                          <p className="eyebrow">当前节点</p>
                          <strong>{selectedNode.nodeName}</strong>
                          <span className="muted-line">{selectedNode.nodeId}</span>
                        </div>
                        <div className="hero-status-group">
                          <span className={statusPillClass(selectedNode.status)}>{selectedNode.status}</span>
                          {selectedNode.isolated ? <span className="status-pill tone-danger">已隔离</span> : <span className="status-pill tone-good">未隔离</span>}
                        </div>
                      </div>

                      <div className="workbench-grid workbench-grid-wide workbench-primary-grid">
                        <div className="workbench-card workbench-card-primary">
                          <div className="card-heading-row">
                            <div>
                              <span className="card-kicker">核心状态</span>
                              <strong>当前节点概况</strong>
                            </div>
                            <div className="card-status-rail">
                              <span className={statusPillClass(selectedNode.status)}>{selectedNode.status}</span>
                              {selectedNode.isolated ? <span className="status-pill tone-danger">已隔离</span> : <span className="status-pill tone-good">允许挂载</span>}
                            </div>
                          </div>
                          <div className="fact-list">
                            <FactRow label="最后在线" value={<code>{formatDate(selectedNode.lastSeenAt)}</code>} />
                            <FactRow label="当前承载" value={<code>{String(selectedNode.activeTunnels)} 个 tunnel</code>} />
                            <FactRow label="主机信息" value={<code>{nodeMeta(selectedNode, "hostname", selectedNode.nodeName)} / {nodeMeta(selectedNode, "os")} / {nodeMeta(selectedNode, "arch")}</code>} />
                            <FactRow label="agent 部署" value={<code>{nodeAgentDeploymentLabel(selectedNode)}</code>} />
                            <FactRow label="service unit" value={<code>{nodeMeta(selectedNode, "serviceUnit")}</code>} />
                          </div>
                          <div className="actions-row actions-row-strong">
                            <button type="button" className="secondary" disabled={busyAction === 'isolate-node:' + selectedNode.nodeId || selectedNode.isolated} onClick={() => void setNodeIsolation(selectedNode, true)}>隔离节点</button>
                            <button type="button" className="secondary" disabled={busyAction === 'release-node:' + selectedNode.nodeId || !selectedNode.isolated} onClick={() => void setNodeIsolation(selectedNode, false)}>解除隔离</button>
                            <button type="button" className="secondary" onClick={() => void refreshDashboard(true, currentUser, false, auditFilterRef.current, nodeFilterRef.current)} disabled={busyAction === 'refresh'}>刷新当前视图</button>
                          </div>
                        </div>

                        <div className="workbench-card workbench-card-aside">
                          <div className="card-heading-row">
                            <div>
                              <span className="card-kicker">运维提示</span>
                              <strong>优先判断项</strong>
                            </div>
                          </div>
                          <div className="ops-note-list">
                            <div className={selectedNode.isolated ? "ops-note tone-danger note-strong" : "ops-note tone-good"}>{selectedNode.isolated ? '已隔离：禁止新挂载 tunnel。' : '未隔离：允许正常挂载 tunnel。'}</div>
                            <div className={selectedNode.status !== 'online' ? "ops-note tone-danger note-strong" : "ops-note tone-info"}>{selectedNode.status !== 'online' ? 'offline：当前不可通信。' : 'online：控制面可通信。'}</div>
                            <div className={selectedNode.activeTunnels >= 3 ? "ops-note tone-warn" : "ops-note tone-neutral"}>{selectedNode.activeTunnels >= 3 ? '高承载：当前 activeTunnels 较多。' : '承载正常：当前 activeTunnels 处于较低水平。'}</div>
                          </div>
                        </div>
                      </div>

                      <section className="workbench-section">
                        <div className="section-head compact-head">
                          <div>
                            <h3>节点承载画像</h3>
                            <span className="muted-line">把当前节点的协议分布、异常入口和负载水平收敛成一个可读画像，而不是只看 tunnel 表。</span>
                          </div>
                        </div>
                        {selectedNodeProfile ? (
                          <>
                            <div className="signal-strip profile-strip">
                              <SignalCard label="当前负载" value={nodeLoadStateLabel(selectedNodeProfile.loadState)} />
                              <SignalCard label="异常入口" value={selectedNodeProfile.problemCount === 0 ? "无" : String(selectedNodeProfile.problemCount)} />
                              <SignalCard label="协议分布" value={selectedNodeProfile.protocolSummary} />
                              <SignalCard label="active tunnel" value={String(selectedNodeProfile.activeCount)} />
                            </div>
                            <div className="table-status-stack capability-matrix">
                              <span className={selectedNodeProfile.protocolCounts.tcp > 0 ? "status-pill tone-info" : "status-pill tone-neutral"}>TCP {selectedNodeProfile.protocolCounts.tcp}</span>
                              <span className={selectedNodeProfile.protocolCounts.http > 0 ? "status-pill tone-info" : "status-pill tone-neutral"}>HTTP {selectedNodeProfile.protocolCounts.http}</span>
                              <span className={selectedNodeProfile.protocolCounts.https > 0 ? "status-pill tone-info" : "status-pill tone-neutral"}>HTTPS {selectedNodeProfile.protocolCounts.https}</span>
                              <span className={selectedNodeProfile.protocolCounts.udp > 0 ? "status-pill tone-info" : "status-pill tone-neutral"}>UDP {selectedNodeProfile.protocolCounts.udp}</span>
                              <span className={selectedNodeProfile.protocolCounts.socks5 > 0 ? "status-pill tone-info" : "status-pill tone-neutral"}>SOCKS5 {selectedNodeProfile.protocolCounts.socks5}</span>
                            </div>
                            <div className="ops-note-list">
                              {selectedNodeProfile.notes.map((note) => <div key={note} className={note.includes("异常") || note.includes("高承载") ? "ops-note tone-warn" : note.includes("空闲") ? "ops-note tone-neutral" : "ops-note tone-info"}>{note}</div>)}
                            </div>
                          </>
                        ) : null}
                      </section>

                      <section className="workbench-section">
                        <div className="section-head compact-head">
                          <div>
                            <h3>节点属性</h3>
                            <span className="muted-line">按排障视角展示角色、环境、信任级别与运维归属信息。</span>
                          </div>
                        </div>
                        <div className="detail-grid">
                          <DetailItem label="role" value={selectedNode.nodeRole ? roleLabel(selectedNode.nodeRole) : "-"} />
                          <DetailItem label="environment" value={selectedNode.environment || "-"} />
                          <DetailItem label="trustLevel" value={selectedNode.trustLevel || "-"} />
                          <DetailItem label="owner" value={selectedNode.owner || "-"} />
                          <DetailItem label="location" value={selectedNode.location || "-"} />
                          <DetailItem label="tags" value={formatTags(selectedNode.tags)} />
                        </div>
                      </section>

                      <section className="workbench-section">
                        <div className="section-head compact-head">
                          <div>
                            <h3>节点能力矩阵</h3>
                            <span className="muted-line">直接判断该节点能否挂载 TCP / UDP / HTTP / HTTPS / SOCKS5；其中 UDP 当前已达到最小公网数据面 V1。</span>
                          </div>
                        </div>
                        <div className="table-status-stack capability-matrix">
                          <span className={capabilityPillClass(selectedNode.capabilities.tcpRelay)}>TCP relay {capabilityEnabledLabel(selectedNode.capabilities.tcpRelay)}</span>
                          <span className={capabilityPillClass(selectedNode.capabilities.udpRelay)}>UDP relay V1 {selectedNode.capabilities.udpRelay ? "已就绪" : "未启用"}</span>
                          <span className={capabilityPillClass(selectedNode.capabilities.httpRelay)}>HTTP relay {capabilityEnabledLabel(selectedNode.capabilities.httpRelay)}</span>
                          <span className={capabilityPillClass(selectedNode.capabilities.httpsRelay || selectedNode.capabilities.httpRelay)}>HTTPS relay {capabilityEnabledLabel(selectedNode.capabilities.httpsRelay || selectedNode.capabilities.httpRelay)}</span>
                          <span className={capabilityPillClass(Boolean(selectedNode.capabilities.socks5Connect))}>SOCKS5 connect {capabilityEnabledLabel(Boolean(selectedNode.capabilities.socks5Connect))}</span>
                          <span className={capabilityPillClass(selectedNode.capabilities.p2pAssist)}>P2P assist {capabilityEnabledLabel(selectedNode.capabilities.p2pAssist)}</span>
                        </div>
                      </section>

                      <section className="workbench-section">
                        <div className="section-head compact-head">
                          <div>
                            <h3>节点承载入口</h3>
                            <span className="muted-line">直接查看该节点当前挂载的 tunnel，并与上面的承载画像联动判断协议分布、异常入口和是否过载。</span>
                          </div>
                        </div>
                        <div className="table-wrap compact-table">
                          <table>
                            <thead><tr><th>隧道</th><th>类型</th><th>状态</th><th>健康</th><th>用户入口</th></tr></thead>
                            <tbody>
                              {selectedNodeTunnels.length === 0 ? <tr><td colSpan={5}>该节点当前没有挂载 tunnel。</td></tr> : selectedNodeTunnels.map((tunnel) => (
                                <tr key={tunnel.id} className={(tunnel.healthStatus || 'healthy') !== 'healthy' ? 'problem-row' : undefined}>
                                  <td><strong>{tunnel.name}</strong><div className="muted">{tunnel.id}</div></td>
                                  <td>{tunnelTypeLabel(tunnel.type)}{tunnel.type === "udp" ? <div className="muted">最小公网 echo 已验证</div> : null}</td>
                                  <td><span className={statusPillClass(tunnel.status)}>{tunnel.status}</span></td>
                                  <td><span className={tunnelHealthPillClass(tunnel.healthStatus)}>{tunnelHealthLabel(tunnel.healthStatus)}</span></td>
                                  <td>{tunnelPublicEntry(tunnel)}</td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </div>
                      </section>

                      <section className="workbench-section">
                        <div className="section-head compact-head">
                          <div>
                            <h3>节点元数据维护</h3>
                            <span className="muted-line">右侧工作区直接完成标签、角色、环境等元数据更新，不再来回跳转。</span>
                          </div>
                        </div>
                        <form className="form-grid" onSubmit={submitNodeMetadata}>
                          <label>
                            <span>节点角色</span>
                            <select value={nodeEditForm.nodeRole} onChange={(event) => setNodeEditForm((current) => current ? { ...current, nodeRole: event.target.value as NodeRole } : current)}>
                              <option value="">未设置</option>
                              <option value="cloud">cloud</option>
                              <option value="local">local</option>
                              <option value="third_party">third_party</option>
                            </select>
                          </label>
                          <label>
                            <span>环境</span>
                            <select value={nodeEditForm.environment} onChange={(event) => setNodeEditForm((current) => current ? { ...current, environment: event.target.value as NodeEnvironment } : current)}>
                              <option value="">未设置</option>
                              <option value="prod">prod</option>
                              <option value="test">test</option>
                              <option value="dev">dev</option>
                            </select>
                          </label>
                          <label>
                            <span>信任级别</span>
                            <select value={nodeEditForm.trustLevel} onChange={(event) => setNodeEditForm((current) => current ? { ...current, trustLevel: event.target.value as NodeTrustLevel } : current)}>
                              <option value="">未设置</option>
                              <option value="trusted">trusted</option>
                              <option value="limited">limited</option>
                              <option value="external">external</option>
                            </select>
                          </label>
                          <label><span>负责人</span><input value={nodeEditForm.owner} onChange={(event) => setNodeEditForm((current) => current ? { ...current, owner: event.target.value } : current)} /></label>
                          <label><span>位置</span><input value={nodeEditForm.location} onChange={(event) => setNodeEditForm((current) => current ? { ...current, location: event.target.value } : current)} /></label>
                          <label><span>标签</span><input value={nodeEditForm.tags} onChange={(event) => setNodeEditForm((current) => current ? { ...current, tags: event.target.value } : current)} placeholder="逗号分隔" /></label>
                          <div className="form-actions">
                            <button type="submit" disabled={busyAction === "update-node:" + selectedNode.nodeId}>{busyAction === "update-node:" + selectedNode.nodeId ? "保存中..." : "保存节点元数据"}</button>
                          </div>
                        </form>
                      </section>
                    </>
                  ) : <EmptyState title="未选择节点" body="请先在左侧列表中选中节点。右侧工作区会稳定承载节点状态、能力矩阵和承载入口，不再继续挤成一条窄栏。" />}
                </section>
              </div>
            ) : (
              <div className="ops-layout tunnel-ops-layout">
                <div className="ops-sidebar panel-stack">
                  <section className="subpanel tunnel-filter-panel">
                    <div className="section-head compact-head tunnel-filter-bar">
                      <div>
                        <h3>隧道运维筛选台</h3>
                        <span className="muted-line">左侧只保留筛选与隧道列表，右侧固定为 tunnel workbench，避免选中后整页上下突兀增高。</span>
                      </div>
                      <div className="inline-switches">
                        <button type="button" className={tunnelHealthFilter === "all" ? "nav-tab active" : "nav-tab"} onClick={() => setTunnelHealthFilter("all")}>全部</button>
                        <button type="button" className={tunnelHealthFilter === "healthy" ? "nav-tab active" : "nav-tab"} onClick={() => setTunnelHealthFilter("healthy")}>正常</button>
                        <button type="button" className={tunnelHealthFilter === "unhealthy" ? "nav-tab active" : "nav-tab"} onClick={() => setTunnelHealthFilter("unhealthy")}>异常</button>
                      </div>
                    </div>
                    <div className="form-grid">
                      <label><span>入口类型</span><select value={tunnelTypeFilter} onChange={(event) => setTunnelTypeFilter(event.target.value as TunnelTypeFilter)}><option value="all">全部</option><option value="tcp">TCP</option><option value="udp">UDP</option><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5">SOCKS5</option></select></label>
                      <label><span>Probe 状态</span><select value={probeStateFilter} onChange={(event) => setProbeStateFilter(event.target.value as ProbeStateFilter)}><option value="all">全部</option><option value="not_probed">未探测</option><option value="recent_success">最近成功</option><option value="recent_failure">最近失败</option><option value="stale">结果较旧</option></select></label>
                      <label><span>节点状态</span><select value={tunnelNodeStatusFilter} onChange={(event) => setTunnelNodeStatusFilter(event.target.value as TunnelNodeStatusFilter)}><option value="all">全部</option><option value="online">节点在线</option><option value="offline">节点离线</option></select></label>
                      <label><span>排序</span><select value={tunnelSortMode} onChange={(event) => setTunnelSortMode(event.target.value as TunnelSortMode)}><option value="ops_priority">默认：异常/失败/较旧优先</option><option value="updated_desc">按更新时间</option><option value="name_asc">按名称</option><option value="health_priority">按健康优先级</option><option value="probe_desc">按最近 probe 时间</option></select></label>
                    </div>
                  </section>

                  {!selectedTunnelVisibleInFilters && selectedTunnel ? <div className="empty-state list-selection-note"><strong>当前选中隧道未命中筛选结果</strong><p>详情仍保留在右侧工作台，便于继续排查；如需在列表中重新看到它，请调整筛选条件。</p></div> : null}

                  <section className="subpanel tunnel-list-panel">
                    <div className="section-head compact-head">
                      <div>
                        <h3>隧道列表</h3>
                        <span className="muted-line">点击左侧卡片后，只更新右侧工作台内容，不再在本页中下方追加大块详情。</span>
                      </div>
                      <span className="muted-line">显示 {filteredTunnels.length === 0 ? 0 : 1}-{filteredTunnels.length} / {tunnels.length}</span>
                    </div>
                    <div className="tunnel-card-list">
                      {filteredTunnels.length === 0 ? <EmptyState title="暂无匹配隧道" body="当前筛选条件下没有匹配结果，可以切换到全部查看。" /> : filteredTunnels.map((tunnel) => (
                        <article key={tunnel.id} className={editingTunnelID === tunnel.id ? tunnelCardClass(tunnel, true) : tunnelCardClass(tunnel, false)} onClick={() => {
                          if (editingTunnelID === tunnel.id) {
                            clearTunnelEdit();
                            return;
                          }
                          beginTunnelEdit(tunnel);
                        }}>
                          <div className="spotlight-head">
                            <div>
                              <strong>{tunnel.name}</strong>
                              <span className="muted-line">{tunnel.id}</span>
                            </div>
                            <span className={statusPillClass(tunnel.status)}>{tunnel.status}</span>
                          </div>
                          <div className="tunnel-route">{tunnelPublicEntry(tunnel)}</div>
                          <div className="health-pill-row">
                            <span className={tunnelHealthPillClass(tunnel.healthStatus)}>{tunnelHealthLabel(tunnel.healthStatus)}</span>
                            <span className={probeFreshnessPillClass(deriveProbeFreshnessState(tunnel))}>{probeFreshnessLabel(deriveProbeFreshnessState(tunnel))}</span>
                          </div>
                          <div className="muted-line">类型 {tunnelTypeLabel(tunnel.type)}{tunnel.type === "udp" ? ' / 最小公网 echo 已验证' : ''}</div>
                          <div className="muted-line">目标 {tunnelTargetLabel(tunnel)}</div>
                          <div className="muted-line">运行依赖 {tunnelRequirementSummary(tunnel, nodes)}</div>
                          <div className="muted-line">节点 {tunnel.nodeId}</div>
                        </article>
                      ))}
                    </div>
                    <div className="table-wrap compact-table tunnel-list-table">
                      <table>
                        <thead><tr><th>名称</th><th>类型</th><th>状态/健康</th><th>Probe</th><th>入口</th></tr></thead>
                        <tbody>
                          {filteredTunnels.length === 0 ? <tr><td colSpan={5}>当前筛选条件下暂无隧道。</td></tr> : filteredTunnels.map((tunnel) => (
                            <tr key={tunnel.id} className={editingTunnelID === tunnel.id ? tunnelRowClass(tunnel, true) : tunnelRowClass(tunnel, false)} onClick={() => {
                              if (editingTunnelID === tunnel.id) {
                                clearTunnelEdit();
                                return;
                              }
                              beginTunnelEdit(tunnel);
                            }}>
                              <td><strong>{tunnel.name}</strong><div className="muted">{tunnel.id}</div></td>
                              <td>{tunnelTypeLabel(tunnel.type)}</td>
                              <td><div className="table-status-stack"><span className={statusPillClass(tunnel.status)}>{tunnel.status}</span><span className={tunnelHealthPillClass(tunnel.healthStatus)}>{tunnelHealthLabel(tunnel.healthStatus)}</span></div></td>
                              <td><div className="table-status-stack"><span className={probeFreshnessPillClass(deriveProbeFreshnessState(tunnel))}>{probeFreshnessLabel(deriveProbeFreshnessState(tunnel))}</span></div><div className="muted">{tunnel.lastProbedAt ? formatDate(tunnel.lastProbedAt) : "-"}</div></td>
                              <td><div>{tunnelPublicEntry(tunnel)}</div><div className="muted">{tunnelTypeEntryHint(tunnel)}</div></td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </section>
                </div>

                <section className="subpanel ops-workbench tunnel-workbench-panel">
                  <div className="section-head compact-head">
                    <div>
                      <h3>隧道运维工作台</h3>
                      <span className="muted-line">右侧固定承载核心状态、入口信息、表单操作、probe 结果和运行说明，切换隧道时只更新这里的内容。</span>
                    </div>
                    <span className={selectedTunnel ? statusPillClass(selectedTunnel.status) : "status-pill tone-neutral"}>{selectedTunnel ? selectedTunnel.status : "新建模式"}</span>
                  </div>

                  {selectedTunnel ? (
                    <>
                      <div className="detail-hero workbench-hero tunnel-hero">
                        <div>
                          <p className="eyebrow">当前隧道</p>
                          <strong>{selectedTunnel.name}</strong>
                          <span className="muted-line">{selectedTunnel.id}</span>
                        </div>
                        <div className="hero-status-group hero-status-stack">
                          <span className={statusPillClass(selectedTunnel.status)}>{selectedTunnel.status}</span>
                          <span className={tunnelHealthPillClass(selectedTunnel.healthStatus)}>{tunnelHealthLabel(selectedTunnel.healthStatus)}</span>
                          <span className={probeFreshnessPillClass(deriveProbeFreshnessState(selectedTunnel))}>{probeFreshnessLabel(deriveProbeFreshnessState(selectedTunnel))}</span>
                        </div>
                      </div>

                      <div className="workbench-grid workbench-grid-wide tunnel-info-grid workbench-primary-grid">
                        <div className="workbench-card workbench-card-primary">
                          <div className="card-heading-row">
                            <div>
                              <span className="card-kicker">核心状态</span>
                              <strong>入口与运行条件</strong>
                            </div>
                            <div className="card-status-rail">
                              <span className={tunnelHealthPillClass(selectedTunnel.healthStatus)}>{tunnelHealthLabel(selectedTunnel.healthStatus)}</span>
                              <span className={probeFreshnessPillClass(deriveProbeFreshnessState(selectedTunnel))}>{probeFreshnessLabel(deriveProbeFreshnessState(selectedTunnel))}</span>
                            </div>
                          </div>
                          <div className="fact-list">
                            <FactRow label="用户入口" value={<code>{tunnelPublicEntry(selectedTunnel)}</code>} />
                            <FactRow label="入口类型" value={tunnelTypeEntryHint(selectedTunnel)} />
                            <FactRow label="目标地址" value={<code>{selectedTunnel.targetHost}:{selectedTunnel.targetPort}</code>} />
                            <FactRow label="运行依赖" value={tunnelRequirementSummary(selectedTunnel, nodes)} />
                            <FactRow label="当前归属" value={selectedTunnelPlacement ? selectedTunnelPlacement.summary : "-"} />
                            {(selectedTunnel.type === "http" || selectedTunnel.type === "https") ? <FactRow label="probePath" value={<code>{selectedTunnel.probePath || "/"}</code>} /> : null}
                            {selectedTunnel.type === "https" ? <FactRow label="publicPort" value={<span><code>{selectedTunnel.publicPort}</code> 仅作内部保留字段</span>} /> : null}
                          </div>
                          <div className="actions-row actions-row-strong">
                            {(selectedTunnel.type === "http" || selectedTunnel.type === "https") ? <button type="button" className="secondary" disabled={busyAction === selectedTunnel.id + ":probe"} onClick={() => void probeTunnel(selectedTunnel)}>{busyAction === selectedTunnel.id + ":probe" ? "探测中..." : "探测"}</button> : null}
                            <button type="button" disabled={busyAction === selectedTunnel.id + ":active" || selectedTunnel.status === "active"} onClick={() => void updateTunnelStatus(selectedTunnel, "active")}>启用</button>
                            <button type="button" disabled={busyAction === selectedTunnel.id + ":paused" || selectedTunnel.status === "paused"} onClick={() => void updateTunnelStatus(selectedTunnel, "paused")}>暂停</button>
                            <button type="button" className="danger" disabled={busyAction === selectedTunnel.id + ":delete"} onClick={() => void deleteTunnel(selectedTunnel)}>删除</button>
                          </div>
                        </div>

                        <div className="workbench-card workbench-card-aside">
                          <div className="card-heading-row">
                            <div>
                              <span className="card-kicker">辅助判断</span>
                              <strong>当前风险提示</strong>
                            </div>
                          </div>
                          <div className="ops-note-list">
                            <div className={(selectedTunnel.healthStatus || "healthy") !== "healthy" ? "ops-note tone-danger note-strong" : "ops-note tone-good"}>{tunnelAvailabilityText(selectedTunnel)}</div>
                            <div className={deriveProbeFreshnessState(selectedTunnel) === "recent_failure" ? "ops-note tone-danger note-strong" : deriveProbeFreshnessState(selectedTunnel) === "stale" ? "ops-note tone-warn" : "ops-note tone-info"}>Probe 状态：{probeFreshnessLabel(deriveProbeFreshnessState(selectedTunnel))}</div>
                            <div className="ops-note tone-neutral">节点：{selectedTunnel.nodeId}</div>
                            {selectedTunnelPlacement ? selectedTunnelPlacement.notes.map((note) => <div key={note} className={note.includes("不合适") || note.includes("离线") || note.includes("缺失") ? "ops-note tone-danger note-strong" : note.includes("高承载") ? "ops-note tone-warn" : "ops-note tone-info"}>{note}</div>) : null}
                          </div>
                        </div>
                      </div>

                      <section className="workbench-section">
                        <div className="section-head compact-head">
                          <div>
                            <h3>归属与替代判断</h3>
                            <span className="muted-line">解释当前为什么挂在这个节点上、这个节点是否合适，以及有没有更轻载的只读替代节点可供人工判断。</span>
                          </div>
                        </div>
                        {selectedTunnelPlacement ? (
                          <div className="empty-state placement-panel">
                            <strong>{selectedTunnelPlacement.summary}</strong>
                            <p>当前节点判断：<code>{selectedTunnelPlacement.currentNodeAssessment}</code></p>
                            <p>归属解释：<code>{selectedTunnelPlacement.requirementSummary}</code></p>
                            <p>只读替代建议：<code>{selectedTunnelPlacement.alternativeSummary}</code></p>
                          </div>
                        ) : <EmptyState title="暂无归属解释" body="选中 tunnel 后，这里会给出当前节点是否合适以及只读替代建议。" />}
                      </section>
                    </>
                  ) : <EmptyState title="尚未选择隧道" body="右侧当前保持稳定的新建工作台；在左侧选择隧道后，这里会切换成当前隧道的编辑与排障面板。" />}

                  <section className="workbench-section">
                    <div className="section-head compact-head">
                      <div>
                        <p className="eyebrow">{editingTunnelID !== null && tunnelEditForm ? "编辑" : "创建"}</p>
                        <h3>{editingTunnelID !== null && tunnelEditForm ? "编辑当前隧道" : "新建隧道"}</h3>
                      </div>
                      <span className="muted-line">{editingTunnelID !== null && tunnelEditForm ? "当前编辑 " + tunnelEditForm.id : "未选中隧道时，这里固定显示新建表单"}</span>
                    </div>

                    {editingTunnelID !== null && tunnelEditForm ? (
                      <form className="form-grid" onSubmit={submitTunnelEdit}>
                        <label><span>名称</span><input value={tunnelEditForm.name} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, name: event.target.value } : current)} required /></label>
                        {tunnelEditForm.type === "udp" ? <div className="form-note">UDP 当前为最小数据面 V1，已完成真实公网 echo 验证；当前仍不支持 UDP probe、复杂会话管理、生产级超时治理或 NAT 穿透。</div> : null}
                        <label><span>目标主机</span><input value={tunnelEditForm.targetHost} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, targetHost: event.target.value } : current)} required={tunnelEditForm.type !== "socks5"} disabled={tunnelEditForm.type === "socks5"} /></label>
                        <label><span>目标端口</span><input value={tunnelEditForm.targetPort} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, targetPort: event.target.value } : current)} inputMode="numeric" required={tunnelEditForm.type !== "socks5"} disabled={tunnelEditForm.type === "socks5"} /></label>
                        <label><span>{tunnelEditForm.type === "https" ? "内部端口（保留字段）" : "公网端口"}</span><input value={tunnelEditForm.publicPort} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, publicPort: event.target.value } : current)} inputMode="numeric" required /></label>
                        <PortPlanHint
                          type={tunnelEditForm.type}
                          suggestion={tunnelEditPortSuggestion}
                          currentPort={tunnelEditForm.publicPort}
                          actionLabel="推荐可用端口"
                          actionBusy={busyAction === "suggest-port:edit:" + tunnelEditForm.id}
                          onSuggest={() => void suggestEditTunnelPort()}
                        />
                        {(tunnelEditForm.type === "http" || tunnelEditForm.type === "https") ? <label><span>域名</span><input value={tunnelEditForm.domain} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, domain: event.target.value } : current)} placeholder="例如 app.example.com" /></label> : null}
                        {(tunnelEditForm.type === "http" || tunnelEditForm.type === "https") ? <label><span>probePath</span><input value={tunnelEditForm.probePath} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, probePath: event.target.value } : current)} placeholder="默认 /" /></label> : null}
                        {tunnelEditForm.type === "https" ? <label><span>TLS 模式</span><select value={tunnelEditForm.tlsMode} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, tlsMode: event.target.value } : current)}><option value="edge_terminate">edge_terminate</option></select></label> : null}
                        {tunnelEditForm.type === "http" ? <div className="form-note">HTTP relay 用于发布节点上的 Web/API 服务，访问方式为 <code>http://82.156.236.104:{tunnelEditForm.publicPort || "<公网端口>"}</code></div> : null}
                        {tunnelEditForm.type === "https" ? <div className="form-note">HTTPS 当前标准入口语义为 Nginx 在 443 终止 TLS，再转发到 relay-https 后端服务。正式访问入口是 <code>https://{tunnelEditForm.domain || "<你的域名>"}</code>；此处端口字段仅作内部保留字段，不作为标准用户入口。</div> : null}
                        {tunnelEditForm.type === "socks5" ? <div className="form-note">SOCKS5 使用节点侧内置代理语义，不需要手工填写目标主机和目标端口。</div> : null}
                        <div className="detail-grid readonly-grid">
                          <DetailItem label="nodeId" value={tunnelEditForm.nodeId} />
                          <DetailItem label="status" value={tunnelEditForm.status} />
                        </div>
                        <div className="form-actions">
                          <button type="submit" disabled={busyAction === "edit-tunnel:" + tunnelEditForm.id}>{busyAction === "edit-tunnel:" + tunnelEditForm.id ? "保存中..." : "保存修改"}</button>
                          <button type="button" className="secondary" onClick={clearTunnelEdit}>取消编辑</button>
                        </div>
                      </form>
                    ) : (
                      <form className="form-grid" onSubmit={createTunnel}>
                        <label>
                          <span>类型</span>
                          <select value={tunnelForm.type} onChange={(event) => setTunnelForm((current) => ({ ...current, type: event.target.value as "tcp" | "udp" | "http" | "https" | "socks5" }))}>
                            <option value="tcp">TCP</option>
                            <option value="udp">UDP（最小 V1）</option>
                            <option value="http">HTTP</option>
                            <option value="https">HTTPS</option>
                            <option value="socks5">SOCKS5</option>
                          </select>
                        </label>
                        <label>
                          <span>节点</span>
                          <select value={tunnelForm.nodeId} onChange={(event) => { setTunnelForm((current) => ({ ...current, nodeId: event.target.value })); setHasInitializedNodeId(true); }} required>
                            <option value="">选择节点</option>
                            {allNodes.filter((node) => {
                              if (tunnelForm.type === "socks5") return node.supportsSOCKS5;
                              if (tunnelForm.type === "http") return node.supportsHTTP;
                              if (tunnelForm.type === "https") return node.supportsHTTPS ?? node.supportsHTTP;
                              return true;
                            }).map((node) => <option key={node.nodeId} value={node.nodeId}>{node.nodeName} ({node.nodeId}){node.status !== "online" ? " / offline" : ""}</option>)}
                          </select>
                        </label>
                        <label><span>名称</span><input value={tunnelForm.name} onChange={(event) => setTunnelForm((current) => ({ ...current, name: event.target.value }))} required /></label>
                        <label><span>目标主机</span><input value={tunnelForm.targetHost} onChange={(event) => setTunnelForm((current) => ({ ...current, targetHost: event.target.value }))} required={tunnelForm.type !== "socks5"} disabled={tunnelForm.type === "socks5"} /></label>
                        <label><span>目标端口</span><input value={tunnelForm.targetPort} onChange={(event) => setTunnelForm((current) => ({ ...current, targetPort: event.target.value }))} inputMode="numeric" required={tunnelForm.type !== "socks5"} disabled={tunnelForm.type === "socks5"} /></label>
                        <label><span>{tunnelForm.type === "https" ? "内部端口（保留字段）" : "公网端口"}</span><input value={tunnelForm.publicPort} onChange={(event) => setTunnelForm((current) => ({ ...current, publicPort: event.target.value }))} inputMode="numeric" required /></label>
                        <PortPlanHint
                          type={tunnelForm.type}
                          suggestion={tunnelPortSuggestion}
                          currentPort={tunnelForm.publicPort}
                          actionLabel="推荐可用端口"
                          actionBusy={busyAction === "suggest-port:create:" + tunnelForm.type}
                          onSuggest={() => void suggestCreateTunnelPort()}
                        />
                        {(tunnelForm.type === "http" || tunnelForm.type === "https") ? <label><span>域名</span><input value={tunnelForm.domain} onChange={(event) => setTunnelForm((current) => ({ ...current, domain: event.target.value }))} placeholder="例如 app.example.com" /></label> : null}
                        {(tunnelForm.type === "http" || tunnelForm.type === "https") ? <label><span>probePath</span><input value={tunnelForm.probePath} onChange={(event) => setTunnelForm((current) => ({ ...current, probePath: event.target.value }))} placeholder="默认 /" /></label> : null}
                        {tunnelForm.type === "https" ? <label><span>TLS 模式</span><select value={tunnelForm.tlsMode} onChange={(event) => setTunnelForm((current) => ({ ...current, tlsMode: event.target.value as "" | "edge_terminate" }))}><option value="edge_terminate">edge_terminate</option></select></label> : null}
                        {tunnelForm.type === "udp" ? <div className="form-note">UDP（最小 V1）：当前已完成真实公网 echo 验证，可用于最小公网 UDP 单会话验证；仍不支持 UDP probe、复杂会话管理、生产级超时治理或 NAT 穿透。</div> : null}
                        {tunnelForm.type === "http" ? <div className="form-note">HTTP relay 用于发布节点上的 Web/API 服务，访问方式为 <code>http://82.156.236.104:{tunnelForm.publicPort || "<公网端口>"}</code></div> : null}
                        {tunnelForm.type === "https" ? <div className="form-note">HTTPS 当前标准入口语义为 Nginx 在 443 终止 TLS，再转发到 relay-https 后端服务。正式访问入口是 <code>https://{tunnelForm.domain || "<你的域名>"}</code>；此处端口字段仅作内部保留字段，不作为标准用户入口。</div> : null}
                        {tunnelFormNode?.status !== "online" ? <div className="form-note">当前选中节点 offline。按现有语义仍可查看或保留配置，但当前不可通信。</div> : null}
                        {tunnelFormNode?.isolated ? <div className="form-note">当前选中节点已隔离，后端会拒绝新建 tunnel。</div> : null}
                        {tunnelForm.type === "socks5" ? <div className="form-note">SOCKS5 使用节点侧内置代理语义，不需要手工填写目标主机和目标端口。</div> : null}
                        <button type="submit" disabled={busyAction === "create-tunnel"}>{busyAction === "create-tunnel" ? "创建中..." : "创建隧道"}</button>
                      </form>
                    )}
                  </section>

                  <section className="workbench-section">
                    <div className="section-head compact-head">
                      <div>
                        <h3>最近一次 Probe 结果</h3>
                        <span className="muted-line">当前只对 HTTP / HTTPS tunnel 展示最近一次 probe 的结果，不再把结果块散落在列表下方。</span>
                      </div>
                    </div>
                    {(selectedProbeResult || persistedProbeResult) ? (
                      <div className="empty-state">
                        <strong>最近一次探测结果</strong>
                        <p>完整探测地址：<code>{(selectedProbeResult || persistedProbeResult)?.targetEntry}</code></p>
                        <p>结果：<span className={(selectedProbeResult || persistedProbeResult)?.success ? "status-pill tone-good" : "status-pill tone-danger"}>{(selectedProbeResult || persistedProbeResult)?.success ? "成功" : "失败"}</span></p>
                        <p>状态码：<code>{(selectedProbeResult || persistedProbeResult)?.statusCode ? String((selectedProbeResult || persistedProbeResult)?.statusCode) : "-"}</code></p>
                        <p>错误：<code>{(selectedProbeResult || persistedProbeResult)?.error || "-"}</code></p>
                        <p>探测时间：<code>{formatDate((selectedProbeResult || persistedProbeResult)?.probedAt || "")}</code></p>
                        <p>结果状态：<span className={probeFreshnessPillClass(selectedTunnel ? deriveProbeFreshnessState(selectedTunnel) : "not_probed")}>{probeFreshnessLabel(selectedTunnel ? deriveProbeFreshnessState(selectedTunnel) : "not_probed")}</span></p>
                      </div>
                    ) : <EmptyState title="暂无 Probe 结果" body="HTTP / HTTPS tunnel 可在右侧工作台直接点击探测，结果会稳定显示在这里，不再把页面向下撑长。" />}
                  </section>

                  <section className="workbench-section">
                    <div className="section-head compact-head">
                      <div>
                        <h3>运行说明</h3>
                        <span className="muted-line">收敛原来按类型堆叠的 empty-state，统一在这里根据当前工作台类型切换说明。</span>
                      </div>
                    </div>
                    <TunnelRuntimeGuide
                      type={editingTunnelID !== null && tunnelEditForm ? tunnelEditForm.type : tunnelForm.type}
                      entry={editingTunnelID !== null && tunnelEditForm ? tunnelEntryPreview(tunnelEditForm.type, tunnelEditForm.publicPort, tunnelEditForm.domain) : tunnelEntryPreview(tunnelForm.type, tunnelForm.publicPort, tunnelForm.domain)}
                      publicPort={editingTunnelID !== null && tunnelEditForm ? tunnelEditForm.publicPort : tunnelForm.publicPort}
                      domain={editingTunnelID !== null && tunnelEditForm ? tunnelEditForm.domain : tunnelForm.domain}
                      probePath={editingTunnelID !== null && tunnelEditForm ? tunnelEditForm.probePath : tunnelForm.probePath}
                    />
                  </section>
                </section>
              </div>
            )}
          </section>
        ) : null}

        {mainView === "audit" && canOperate ? (
          <section className="workspace-panel">
            <div className="section-head">
              <div>
                <p className="eyebrow">审计</p>
                <h2>审计</h2>
              </div>
              <span className="muted-line">审计工作区独立保留筛选、分页与 payload 展开</span>
            </div>

            <section className="subpanel audit-filter-panel">
              <h3>筛选栏</h3>
              <form className="form-grid" onSubmit={submitAuditFilters}>
                <label><span>动作</span><input value={auditFilter.action} onChange={(event) => setAuditFilter((current) => ({ ...current, action: event.target.value }))} /></label>
                <label><span>执行者类型</span><input value={auditFilter.actorType} onChange={(event) => setAuditFilter((current) => ({ ...current, actorType: event.target.value }))} /></label>
                <label><span>资源类型</span><input value={auditFilter.resourceType} onChange={(event) => setAuditFilter((current) => ({ ...current, resourceType: event.target.value }))} /></label>
                <label><span>执行者 ID</span><input value={auditFilter.actorID} onChange={(event) => setAuditFilter((current) => ({ ...current, actorID: event.target.value }))} /></label>
                <label><span>开始时间</span><input type="datetime-local" value={auditFilter.startAt} onChange={(event) => setAuditFilter((current) => ({ ...current, startAt: event.target.value }))} /></label>
                <label><span>结束时间</span><input type="datetime-local" value={auditFilter.endAt} onChange={(event) => setAuditFilter((current) => ({ ...current, endAt: event.target.value }))} /></label>
                <div className="form-actions">
                  <button type="submit">应用筛选</button>
                  <button type="button" className="secondary" onClick={clearAuditFilters}>清空筛选</button>
                  <span className="inline-note">清空后回到第一页，自动刷新继续沿用当前筛选。</span>
                </div>
              </form>
            </section>

            <section className="subpanel audit-table-panel">
              <div className="audit-header-row">
                <h3>最近操作历史</h3>
                <span className="audit-page-status">显示 {auditStart === 0 ? 0 : auditStart}-{auditEnd} / {auditTotal}</span>
              </div>
              <div className="table-wrap compact-table dense-table">
                <table>
                  <thead><tr><th>时间</th><th>执行者</th><th>动作</th><th>资源类型</th><th>资源 ID</th></tr></thead>
                  <tbody>
                    {auditLogs.length === 0 ? <tr><td colSpan={5}>暂无审计日志。</td></tr> : auditLogs.map((entry) => (
                      <Fragment key={entry.id}>
                        <tr className={expandedAuditID === entry.id ? "audit-row selected-row" : "audit-row"} onClick={() => setExpandedAuditID((current) => current === entry.id ? null : entry.id)}>
                          <td>{formatDate(entry.createdAt)}</td>
                          <td>{entry.actorType}{entry.actorId ? ":" + entry.actorId : ""}</td>
                          <td><strong>{entry.action}</strong></td>
                          <td>{entry.resourceType}</td>
                          <td>{entry.resourceId || "-"}</td>
                        </tr>
                        {expandedAuditID === entry.id ? (
                          <tr className="audit-payload-row"><td colSpan={5}><pre className="payload-view">{JSON.stringify(entry.payload || {}, null, 2)}</pre></td></tr>
                        ) : null}
                      </Fragment>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="actions-row audit-pager">
                <button type="button" className="secondary" disabled={auditFilter.offset === 0} onClick={() => goToAuditPage(Math.max(0, auditFilter.offset - auditFilter.limit))}>上一页</button>
                <button type="button" className="secondary" disabled={auditFilter.offset + auditFilter.limit >= auditTotal} onClick={() => goToAuditPage(auditFilter.offset + auditFilter.limit)}>下一页</button>
                <span className="inline-note">limit {auditFilter.limit} / offset {auditFilter.offset}</span>
              </div>
            </section>
          </section>
        ) : null}

        {mainView === "permissions" && canManageUsers ? (
          <section className="workspace-panel">
            <div className="section-head">
              <div>
                <p className="eyebrow">权限</p>
                <h2>用户/权限</h2>
              </div>
              <span className="muted-line">管理员专属工作区</span>
            </div>
            <div className="split-layout">
              <section className="subpanel form-panel">
                <h3>创建用户</h3>
                <form className="form-grid" onSubmit={createUser}>
                  <label><span>邮箱</span><input value={userForm.email} onChange={(event) => setUserForm((current) => ({ ...current, email: event.target.value }))} required /></label>
                  <label><span>显示名称</span><input value={userForm.displayName} onChange={(event) => setUserForm((current) => ({ ...current, displayName: event.target.value }))} required /></label>
                  <label><span>密码</span><input type="password" value={userForm.password} onChange={(event) => setUserForm((current) => ({ ...current, password: event.target.value }))} required /></label>
                  <label><span>角色</span><select value={userForm.role} onChange={(event) => setUserForm((current) => ({ ...current, role: event.target.value as UserRole }))}><option value="manager">管理用户</option><option value="user">普通用户</option><option value="admin">管理员</option></select></label>
                  <button type="submit" disabled={busyAction === "create-user"}>{busyAction === "create-user" ? "创建中..." : "创建用户"}</button>
                </form>
              </section>
              <section className="subpanel">
                <h3>当前用户</h3>
                <div className="table-wrap compact-table">
                  <table>
                    <thead><tr><th>邮箱</th><th>显示名称</th><th>角色</th><th>创建时间</th></tr></thead>
                    <tbody>
                      {users.length === 0 ? <tr><td colSpan={4}>暂无用户。</td></tr> : users.map((user) => (
                        <tr key={user.id}><td>{user.email}</td><td>{user.displayName}</td><td><span className={roleClass(user.role)}>{user.role}</span></td><td>{formatDate(user.createdAt)}</td></tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </section>
            </div>
          </section>
        ) : null}
      </main>
    </div>
  );
}

function MetricCard({ label, value, hint }: { label: string; value: string; hint: string }) {
  return (
    <article className="metric-card">
      <span>{label}</span>
      <strong>{value}</strong>
      <p>{hint}</p>
    </article>
  );
}

function SignalCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="signal-card">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function MiniStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="mini-stat">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function DetailItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="detail-item">
      <span>{label}</span>
      <strong>{value || "-"}</strong>
    </div>
  );
}

function FactRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="fact-row">
      <span>{label}</span>
      <div className="fact-value">{value}</div>
    </div>
  );
}

function EmptyState({ title, body }: { title: string; body: string }) {
  return (
    <div className="empty-state">
      <strong>{title}</strong>
      <p>{body}</p>
    </div>
  );
}

function PortPlanHint({
  type,
  suggestion,
  currentPort,
  actionLabel,
  actionBusy,
  onSuggest,
}: {
  type: string;
  suggestion: TunnelPortSuggestionResponse | null;
  currentPort: string;
  actionLabel: string;
  actionBusy: boolean;
  onSuggest: () => void;
}) {
  const plan = suggestion?.plan || fallbackPortPlan(type);
  const current = Number(currentPort);
  const inRange = current > 0 && current >= plan.rangeStart && current <= plan.rangeEnd;
  return (
    <div className="form-note port-plan-note">
      <strong>{plan.label} 推荐端口段</strong>
      <p>
        推荐范围：<code>{plan.rangeStart}-{plan.rangeEnd}</code>。{plan.description}
      </p>
      <p>
        当前端口：<code>{currentPort || "<未填写>"}</code>
        {currentPort ? (inRange ? "，位于推荐范围内。" : "，不在推荐范围内，但旧 tunnel 仍保持兼容，不要求立即迁移。") : "，可直接使用推荐值。"}
      </p>
      <div className="form-actions compact-actions">
        <button type="button" className="secondary" disabled={actionBusy} onClick={onSuggest}>{actionBusy ? "推荐中..." : actionLabel}</button>
        <span className="inline-note">
          {suggestion?.suggested ? <>当前建议：<code>{suggestion.suggested}</code>，已避开当前已占用端口。</> : <>当前推荐段暂无空闲端口，需要手工调整或清理占用。</>}
        </span>
      </div>
    </div>
  );
}

function TunnelRuntimeGuide({
  type,
  entry,
  publicPort,
  domain,
  probePath,
}: {
  type: string;
  entry: string;
  publicPort: string;
  domain: string;
  probePath: string;
}) {
  const normalizedProbePath = normalizeProbePath(probePath);

  if (type === "http") {
    return (
      <div className="empty-state runtime-guide">
        <strong>HTTP relay 说明</strong>
        <p>HTTP relay 用于发布节点上的 Web/API 服务，通过公网 HTTP 入口访问本地目标地址。</p>
        <p>访问入口：<code>{entry}</code></p>
        <p>最小验证：<code>curl.exe {entry}{normalizedProbePath === "/" ? "" : normalizedProbePath}</code></p>
        <p>当前不包含 HTTPS、域名绑定、TLS、复杂 header 重写或 ACL。</p>
      </div>
    );
  }

  if (type === "https") {
    return (
      <div className="empty-state runtime-guide">
        <strong>HTTPS relay 说明</strong>
        <p>标准入口：<code>{entry}</code></p>
        <p>当前标准生产语义为 Nginx 在 443 终止 TLS，再转发到 relay-https 后端链路，随后按 domain 命中对应 HTTPS tunnel。</p>
        <p>domain：<code>{domain || "<待绑定域名>"}</code> / tlsMode：<code>edge_terminate</code></p>
        <p>probePath 验证入口：<code>{entry}{normalizedProbePath === "/" ? "/" : normalizedProbePath}</code></p>
        <p>publicPort：<code>{publicPort || "<保留字段>"}</code>，仅作内部保留字段，不作为标准用户入口。</p>
      </div>
    );
  }

  if (type === "socks5") {
    return (
      <div className="empty-state runtime-guide">
        <strong>SOCKS5 relay 说明</strong>
        <p>当前 SOCKS5 入口仅支持 CONNECT，不支持 UDP associate，也不提供高级认证、ACL 或链式代理。</p>
        <p>访问入口：<code>{entry}</code></p>
        <p>最小验证：<code>curl.exe --proxy {entry} https://example.com -I</code></p>
        <p>适用场景：临时出口代理、浏览器或命令行经 SOCKS5 发起 TCP 连接。</p>
      </div>
    );
  }

  if (type === "udp") {
    return (
      <div className="empty-state runtime-guide">
        <strong>UDP relay 最小版说明</strong>
        <p>UDP 当前已完成真实公网 echo 验证，系统已具备最小公网 UDP 单会话闭环能力。</p>
        <p>当前入口：<code>{entry}</code></p>
        <p>健康语义：当前支持 healthy / node_offline / capability_missing / misconfigured；当前不提供可靠的 UDP target_unreachable 判定，也不提供 UDP probe。</p>
        <p>当前限制：不支持复杂会话管理、生产级超时治理、NAT 穿透或告警系统。</p>
      </div>
    );
  }

  return (
    <div className="empty-state runtime-guide">
      <strong>TCP relay 说明</strong>
      <p>TCP relay 提供原始端口映射入口，不做 HTTP 协议语义，也不是 SOCKS5 代理。</p>
      <p>访问入口：<code>{entry}</code></p>
      <p>适用场景：数据库、RDP、SSH 或其他自定义 TCP 服务映射。</p>
    </div>
  );
}

const PROBE_STALE_MS = 15 * 60 * 1000;

function matchesTunnelHealthFilter(tunnel: TunnelSpec, filter: TunnelHealthFilter) {
  const health = tunnel.healthStatus || "healthy";
  if (filter === "healthy") {
    return health === "healthy";
  }
  if (filter === "unhealthy") {
    return health !== "healthy";
  }
  return true;
}

function deriveProbeFreshnessState(tunnel: TunnelSpec): ProbeFreshnessState {
  if (!tunnel.lastProbedAt || tunnel.lastProbedAt.startsWith("0001-01-01")) {
    return "not_probed";
  }
  const parsed = new Date(tunnel.lastProbedAt).getTime();
  if (Number.isNaN(parsed)) {
    return "stale";
  }
  if (Date.now() - parsed > PROBE_STALE_MS) {
    return "stale";
  }
  return tunnel.lastProbeSuccess ? "recent_success" : "recent_failure";
}

function probeFreshnessLabel(state: ProbeFreshnessState) {
  switch (state) {
    case "recent_success":
      return "最近成功";
    case "recent_failure":
      return "最近失败";
    case "stale":
      return "结果较旧";
    default:
      return "尚未探测";
  }
}

function probeFreshnessPillClass(state: ProbeFreshnessState) {
  switch (state) {
    case "recent_success":
      return "status-pill tone-good";
    case "recent_failure":
      return "status-pill tone-danger";
    case "stale":
      return "status-pill tone-warn";
    default:
      return "status-pill tone-neutral";
  }
}

function nodeOnlineStatus(nodeId: string, nodes: NodeSummary[]) {
  const node = nodes.find((item) => item.nodeId === nodeId) ?? null;
  return node?.status === "online" ? "online" : "offline";
}

function matchesTunnelFilters(
  tunnel: TunnelSpec,
  nodes: NodeSummary[],
  healthFilter: TunnelHealthFilter,
  typeFilter: TunnelTypeFilter,
  probeFilter: ProbeStateFilter,
  nodeStatusFilter: TunnelNodeStatusFilter,
) {
  if (!matchesTunnelHealthFilter(tunnel, healthFilter)) {
    return false;
  }
  if (typeFilter !== "all" && tunnel.type !== typeFilter) {
    return false;
  }
  if (probeFilter !== "all" && deriveProbeFreshnessState(tunnel) !== probeFilter) {
    return false;
  }
  if (nodeStatusFilter !== "all" && nodeOnlineStatus(tunnel.nodeId, nodes) !== nodeStatusFilter) {
    return false;
  }
  return true;
}

function healthPriority(tunnel: TunnelSpec) {
  switch (tunnel.healthStatus) {
    case "target_unreachable":
      return 0;
    case "node_offline":
      return 1;
    case "capability_missing":
      return 2;
    case "misconfigured":
      return 3;
    default:
      return 4;
  }
}

function sortTunnels(tunnels: TunnelSpec[], nodes: NodeSummary[], mode: TunnelSortMode) {
  const items = [...tunnels];
  items.sort((left, right) => {
    if (mode === "name_asc") {
      return left.name.localeCompare(right.name, "zh-CN");
    }
    if (mode === "updated_desc") {
      return (Date.parse(right.updatedAt || "") || 0) - (Date.parse(left.updatedAt || "") || 0);
    }
    if (mode === "health_priority") {
      return healthPriority(left) - healthPriority(right);
    }
    if (mode === "probe_desc") {
      return (Date.parse(right.lastProbedAt || "") || 0) - (Date.parse(left.lastProbedAt || "") || 0);
    }
    const leftProbe = deriveProbeFreshnessState(left);
    const rightProbe = deriveProbeFreshnessState(right);
    const freshnessRank = (value: ProbeFreshnessState) => {
      switch (value) {
        case "recent_failure":
          return 0;
        case "stale":
          return 1;
        case "not_probed":
          return 2;
        default:
          return 3;
      }
    };
    const probeDelta = freshnessRank(leftProbe) - freshnessRank(rightProbe);
    if (probeDelta !== 0) {
      return probeDelta;
    }
    const healthDelta = healthPriority(left) - healthPriority(right);
    if (healthDelta != 0) {
      return healthDelta;
    }
    const nodeDelta = nodeOnlineStatus(left.nodeId, nodes).localeCompare(nodeOnlineStatus(right.nodeId, nodes));
    if (nodeDelta !== 0) {
      return nodeDelta;
    }
    return left.name.localeCompare(right.name, "zh-CN");
  });
  return items;
}

function tunnelTypeLabel(type: string) {
  if (type === "http") return "HTTP";
  if (type === "https") return "HTTPS";
  if (type === "udp") return "UDP";
  return type === "socks5" ? "SOCKS5" : "TCP";
}

function fallbackPortPlan(type: string): PortRangePlan {
  if (type === "udp") {
    return { type: "udp", label: "UDP", rangeStart: 21000, rangeEnd: 21999, description: "UDP 推荐端口段" };
  }
  if (type === "http" || type === "https") {
    return { type, label: type.toUpperCase(), rangeStart: 22000, rangeEnd: 22999, description: "HTTP/HTTPS 共享推荐端口段" };
  }
  if (type === "socks5") {
    return { type: "socks5", label: "SOCKS5", rangeStart: 23000, rangeEnd: 23999, description: "SOCKS5 推荐端口段" };
  }
  return { type: "tcp", label: "TCP", rangeStart: 20000, rangeEnd: 20999, description: "TCP 推荐端口段" };
}

function tunnelPublicEntry(tunnel: TunnelSpec) {
  if (tunnel.type === "http") {
    return "http://82.156.236.104:" + tunnel.publicPort;
  }
  if (tunnel.type === "https") {
    return tunnel.domain ? "https://" + tunnel.domain : "https://<待绑定域名>";
  }
  if (tunnel.type === "udp") {
    return "udp://82.156.236.104:" + tunnel.publicPort;
  }
  if (tunnel.type === "socks5") {
    return "socks5://82.156.236.104:" + tunnel.publicPort;
  }
  return "82.156.236.104:" + tunnel.publicPort;
}

function tunnelEntryPreview(type: string, publicPort: string, domain: string) {
  if (type === "http") {
    return "http://82.156.236.104:" + (publicPort || "<公网端口>");
  }
  if (type === "https") {
    return domain ? "https://" + domain : "https://<待绑定域名>";
  }
  if (type === "udp") {
    return "udp://82.156.236.104:" + (publicPort || "<公网端口>");
  }
  if (type === "socks5") {
    return "socks5://82.156.236.104:" + (publicPort || "<公网端口>");
  }
  return "82.156.236.104:" + (publicPort || "<公网端口>");
}

function normalizeProbePath(pathValue?: string) {
  const value = (pathValue || "/").trim();
  if (!value) {
    return "/";
  }
  return value.startsWith("/") ? value : "/" + value;
}

function tunnelTargetLabel(tunnel: TunnelSpec) {
  if (tunnel.type === "socks5") {
    return "节点侧 SOCKS5 CONNECT";
  }
  if (tunnel.type === "udp") {
    return "UDP 最小数据面 V1（公网 echo 已验证）";
  }
  if (tunnel.type === "http") {
    return "发布 " + tunnel.targetHost + ":" + tunnel.targetPort + " 的 Web/API 服务";
  }
  if (tunnel.type === "https") {
    return "标准 443 入口由 Nginx 终止 TLS，再转发到 relay-https，最终到达 " + tunnel.targetHost + ":" + tunnel.targetPort + (tunnel.domain ? "（域名 " + tunnel.domain + "）" : "（待绑定域名）");
  }
  return tunnel.targetHost + ":" + tunnel.targetPort;
}

function tunnelTypeEntryHint(tunnel: TunnelSpec) {
  if (tunnel.type === "http") {
    return "HTTP 入口，直接发布 Web/API";
  }
  if (tunnel.type === "https") {
    return "HTTPS 标准入口，依赖 Nginx 443 terminate";
  }
  if (tunnel.type === "udp") {
    return "UDP 最小数据面入口（无 UDP probe）";
  }
  if (tunnel.type === "socks5") {
    return "SOCKS5 CONNECT 代理入口";
  }
  return "TCP 端口直连入口";
}

function tunnelHealthLabel(status?: TunnelHealthStatus) {
  switch (status) {
    case "node_offline":
      return "节点离线";
    case "capability_missing":
      return "能力缺失";
    case "misconfigured":
      return "配置异常";
    case "target_unreachable":
      return "目标不可达";
    default:
      return "正常";
  }
}

function tunnelAvailabilityText(tunnel: TunnelSpec) {
  if (tunnel.status !== "active") {
    return "当前为停用配置";
  }
  switch (tunnel.healthStatus) {
    case "node_offline":
      return "节点当前不可达";
    case "capability_missing":
      return "节点不满足能力要求";
    case "misconfigured":
      return "配置不完整或不合法";
    case "target_unreachable":
      return "节点在线，但目标 Web 服务不可达";
    default:
      return "当前满足运行条件";
  }
}

function tunnelHealthPillClass(status?: TunnelHealthStatus) {
  switch (status) {
    case "node_offline":
    case "capability_missing":
    case "misconfigured":
    case "target_unreachable":
      return "status-pill tone-danger";
    default:
      return "status-pill tone-good";
  }
}

function tunnelCardClass(tunnel: TunnelSpec, selected: boolean) {
  const base = ["spotlight-card", "tunnel-card", "interactive-card"];
  if ((tunnel.healthStatus || "healthy") !== "healthy") {
    base.push("problem-card");
  }
  if (selected) {
    base.push("selected-card");
  }
  return base.join(" ");
}

function tunnelRowClass(tunnel: TunnelSpec, selected: boolean) {
  const base = [] as string[];
  if ((tunnel.healthStatus || "healthy") !== "healthy") {
    base.push("problem-row");
  }
  if (selected) {
    base.push("selected-row");
  }
  return base.join(" ") || undefined;
}

function capabilitySummary(capabilities: NodeCapabilities) {
  const active = [] as string[];
  if (capabilities.tcpRelay) active.push("TCP");
  if (capabilities.httpRelay) active.push("HTTP");
  if (capabilities.httpsRelay || capabilities.httpRelay) active.push("HTTPS");
  if (capabilities.socks5Connect) active.push("SOCKS5");
  if (capabilities.udpRelay) active.push("UDP");
  if (capabilities.p2pAssist) active.push("P2P");
  return active.length > 0 ? active.join(" / ") : "无";
}

function capabilityEnabledLabel(enabled: boolean) {
  return enabled ? "可用" : "不可用";
}

function capabilityPillClass(enabled: boolean) {
  return enabled ? "status-pill tone-good" : "status-pill tone-neutral";
}

function toProbeResult(tunnel: TunnelSpec): TunnelProbeResult | null {
  if (!tunnel.lastProbedAt) {
    return null;
  }
  return {
    tunnelId: tunnel.id,
    success: Boolean(tunnel.lastProbeSuccess),
    statusCode: tunnel.lastProbeStatusCode,
    error: tunnel.lastProbeError,
    probedAt: tunnel.lastProbedAt,
    targetEntry: tunnel.lastProbeTargetEntry || "",
  };
}

function tunnelRequirementSummary(tunnel: TunnelSpec, nodes: NodeSummary[]) {
  const node = nodes.find((item) => item.nodeId === tunnel.nodeId) ?? null;
  const requirements = [] as string[];
  requirements.push(node && node.status === "online" ? "节点在线" : "节点离线");
  if (tunnel.type === "http") {
    requirements.push(node?.capabilities.httpRelay ? "HTTP 能力满足" : "HTTP 能力缺失");
  } else if (tunnel.type === "https") {
    requirements.push((node?.capabilities.httpsRelay || node?.capabilities.httpRelay) ? "HTTPS 能力满足" : "HTTPS 能力缺失");
    requirements.push("443 入口依赖 Nginx terminate");
  } else if (tunnel.type === "udp") {
    requirements.push(node?.capabilities.udpRelay ? "UDP 能力满足" : "UDP 能力缺失");
    requirements.push("UDP 最小数据面 V1（公网 echo 已验证）");
  } else if (tunnel.type === "socks5") {
    requirements.push(node?.capabilities.socks5Connect ? "SOCKS5 能力满足" : "SOCKS5 能力缺失");
  } else {
    requirements.push(node?.capabilities.tcpRelay ? "TCP 能力满足" : "TCP 能力缺失");
  }
  requirements.push((tunnel.healthStatus || "healthy") === "healthy" ? "当前健康" : "当前异常");
  return requirements.join(" / ");
}

function statusPillClass(status: string) {
  return status === "online" || status === "active" ? "status-pill tone-good" : "status-pill tone-warn";
}

function roleClass(role: UserRole) {
  switch (role) {
    case "admin":
      return "status-pill tone-admin";
    case "manager":
      return "status-pill tone-info";
    default:
      return "status-pill tone-neutral";
  }
}

function toTunnelEditForm(tunnel: TunnelSpec): TunnelEditForm {
  return {
    id: tunnel.id,
    nodeId: tunnel.nodeId,
    name: tunnel.name,
    targetHost: tunnel.targetHost,
    targetPort: String(tunnel.targetPort),
    publicPort: String(tunnel.publicPort),
    domain: tunnel.domain || "",
    tlsMode: tunnel.tlsMode || "",
    probePath: tunnel.probePath || "/",
    status: tunnel.status,
    type: tunnel.type,
    transportPolicy: tunnel.transportPolicy,
  };
}

function toNodeEditForm(node: NodeSummary): NodeEditForm {
  return {
    nodeRole: node.nodeRole || "",
    environment: node.environment || "",
    trustLevel: node.trustLevel || "",
    owner: node.owner || "",
    location: node.location || "",
    tags: (node.tags || []).join(", "),
    isolated: Boolean(node.isolated),
  };
}

function matchesNodeOpsFilters(
  node: NodeSummary,
  statusFilter: NodeStatusFilter,
  roleFilter: NodeRole,
  environmentFilter: NodeEnvironment,
  capabilityFilter: NodeCapabilityFilter,
  ownerFilter: string,
  tagFilter: string,
) {
  if (statusFilter !== "all" && (node.status === "online" ? "online" : "offline") !== statusFilter) {
    return false;
  }
  if (roleFilter && node.nodeRole !== roleFilter) {
    return false;
  }
  if (environmentFilter && node.environment !== environmentFilter) {
    return false;
  }
  if (ownerFilter && !(node.owner || "").toLowerCase().includes(ownerFilter.toLowerCase())) {
    return false;
  }
  if (tagFilter && !(node.tags || []).join(",").toLowerCase().includes(tagFilter.toLowerCase())) {
    return false;
  }
  if (capabilityFilter !== "all") {
    const match =
      capabilityFilter === "tcp" ? node.capabilities.tcpRelay :
      capabilityFilter === "udp" ? node.capabilities.udpRelay :
      capabilityFilter === "http" ? node.capabilities.httpRelay :
      capabilityFilter === "https" ? (node.capabilities.httpsRelay || node.capabilities.httpRelay) :
      Boolean(node.capabilities.socks5Connect);
    if (!match) {
      return false;
    }
  }
  return true;
}

function sortNodes(nodes: NodeSummary[], mode: NodeSortMode) {
  const items = [...nodes];
  items.sort((left, right) => {
    if (mode === "name_asc") {
      return left.nodeName.localeCompare(right.nodeName, "zh-CN");
    }
    if (mode === "last_seen_desc") {
      return (Date.parse(right.lastSeenAt || "") || 0) - (Date.parse(left.lastSeenAt || "") || 0);
    }
    if (mode === "active_tunnels_desc") {
      return right.activeTunnels - left.activeTunnels;
    }
    const leftOffline = left.status === "online" ? 0 : 1;
    const rightOffline = right.status === "online" ? 0 : 1;
    if (leftOffline !== rightOffline) {
      return rightOffline - leftOffline;
    }
    if (left.activeTunnels !== right.activeTunnels) {
      return right.activeTunnels - left.activeTunnels;
    }
    return left.nodeName.localeCompare(right.nodeName, "zh-CN");
  });
  return items;
}

function sortNodeTunnels(items: TunnelSpec[]) {
  return [...items].sort((left, right) => {
    const leftProblem = (left.healthStatus || "healthy") === "healthy" ? 1 : 0;
    const rightProblem = (right.healthStatus || "healthy") === "healthy" ? 1 : 0;
    if (leftProblem !== rightProblem) {
      return leftProblem - rightProblem;
    }
    return left.name.localeCompare(right.name, "zh-CN");
  });
}

function buildNodeLoadProfile(node: NodeSummary, items: TunnelSpec[]) {
  const protocolCounts = { tcp: 0, http: 0, https: 0, udp: 0, socks5: 0 };
  let activeCount = 0;
  let problemCount = 0;
  for (const tunnel of items) {
    if (tunnel.type === "http") protocolCounts.http += 1;
    else if (tunnel.type === "https") protocolCounts.https += 1;
    else if (tunnel.type === "udp") protocolCounts.udp += 1;
    else if (tunnel.type === "socks5") protocolCounts.socks5 += 1;
    else protocolCounts.tcp += 1;
    if (tunnel.status === "active") {
      activeCount += 1;
    }
    if ((tunnel.healthStatus || "healthy") !== "healthy") {
      problemCount += 1;
    }
  }
  const protocolSummary = [
    protocolCounts.tcp > 0 ? "TCP " + protocolCounts.tcp : "",
    protocolCounts.http > 0 ? "HTTP " + protocolCounts.http : "",
    protocolCounts.https > 0 ? "HTTPS " + protocolCounts.https : "",
    protocolCounts.udp > 0 ? "UDP " + protocolCounts.udp : "",
    protocolCounts.socks5 > 0 ? "SOCKS5 " + protocolCounts.socks5 : "",
  ].filter(Boolean).join(" / ") || "暂无入口";
  let loadState: "idle" | "normal" | "high_load" = "normal";
  if (activeCount === 0) {
    loadState = "idle";
  } else if (activeCount >= 3 || problemCount >= 2) {
    loadState = "high_load";
  }
  const notes = [] as string[];
  if (loadState === "idle") {
    notes.push("当前空闲：暂无 active tunnel，可作为后续编排候选节点。");
  } else if (loadState === "high_load") {
    notes.push("当前高承载：active tunnel 较多，后续新增入口应谨慎挂载。");
  } else {
    notes.push("当前承载处于正常区间。");
  }
  if (problemCount > 0) {
    notes.push("存在异常入口：" + problemCount + " 个 tunnel 当前健康异常。");
  }
  if (node.status !== "online") {
    notes.push("节点当前 offline，承载画像仅供排查，不适合作为新增挂载目标。");
  }
  return { node, protocolCounts, protocolSummary, activeCount, problemCount, loadState, notes };
}

function nodeLoadStateLabel(state: "idle" | "normal" | "high_load") {
  switch (state) {
    case "idle":
      return "空闲";
    case "high_load":
      return "高承载";
    default:
      return "承载正常";
  }
}

function supportsTunnelType(node: NodeSummary, tunnelType: string) {
  if (tunnelType === "udp") return node.capabilities.udpRelay;
  if (tunnelType === "http") return node.capabilities.httpRelay;
  if (tunnelType === "https") return node.capabilities.httpsRelay || node.capabilities.httpRelay;
  if (tunnelType === "socks5") return Boolean(node.capabilities.socks5Connect);
  return node.capabilities.tcpRelay;
}

function explainTunnelPlacement(tunnel: TunnelSpec, nodes: NodeSummary[], allTunnels: TunnelSpec[]) {
  const currentNode = nodes.find((node) => node.nodeId === tunnel.nodeId) ?? null;
  const currentProfile = currentNode ? buildNodeLoadProfile(currentNode, allTunnels.filter((item) => item.nodeId === currentNode.nodeId)) : null;
  const notes = [] as string[];
  if (!currentNode) {
    notes.push("当前绑定节点不存在或未加载，归属不合适。");
  } else {
    if (currentNode.status !== "online") {
      notes.push("当前绑定节点离线，归属不合适。");
    }
    if (!supportsTunnelType(currentNode, tunnel.type)) {
      notes.push("当前绑定节点 capability 缺失，归属不合适。");
    }
    if (currentProfile?.loadState === "high_load") {
      notes.push("当前绑定节点高承载，后续可考虑人工分散。");
    }
    if ((tunnel.healthStatus || "healthy") !== "healthy") {
      notes.push("当前 tunnel 自身存在异常，需要结合节点状态一起判断。");
    }
  }
  if (notes.length === 0) {
    notes.push("当前节点满足 capability 且状态正常，归属基本合理。");
  }

  const alternatives = nodes
    .filter((node) => node.nodeId !== tunnel.nodeId)
    .filter((node) => node.status === "online")
    .filter((node) => supportsTunnelType(node, tunnel.type))
    .map((node) => ({ node, profile: buildNodeLoadProfile(node, allTunnels.filter((item) => item.nodeId === node.nodeId)) }))
    .sort((left, right) => {
      const rank = (state: "idle" | "normal" | "high_load") => state === "idle" ? 0 : state === "normal" ? 1 : 2;
      const loadDelta = rank(left.profile.loadState) - rank(right.profile.loadState);
      if (loadDelta !== 0) {
        return loadDelta;
      }
      return left.profile.activeCount - right.profile.activeCount;
    });

  const bestAlternative = alternatives[0] || null;
  const currentNodeAssessment = currentNode
    ? nodeAgentDeploymentLabel(currentNode) + " / " + nodeLoadStateLabel(currentProfile?.loadState || "normal") + " / " + (supportsTunnelType(currentNode, tunnel.type) ? "capability 满足" : "capability 不匹配")
    : "未找到当前节点";
  const requirementSummary = tunnelRequirementSummary(tunnel, nodes);
  const alternativeSummary = bestAlternative
    ? bestAlternative.node.nodeName + "（" + bestAlternative.node.nodeId + "，" + nodeLoadStateLabel(bestAlternative.profile.loadState) + "，" + bestAlternative.profile.protocolSummary + "）"
    : "暂无更合适的在线替代节点";
  const summary = currentNode
    ? tunnel.name + " 当前绑定在 " + currentNode.nodeName + "（" + currentNode.nodeId + "）"
    : tunnel.name + " 当前绑定节点不可用";
  return { summary, currentNodeAssessment, requirementSummary, alternativeSummary, notes };
}

function splitTagInput(input: string) {
  return input.split(",").map((item) => item.trim()).filter(Boolean);
}

function formatTags(tags?: string[]) {
  return tags && tags.length > 0 ? tags.join(", ") : "-";
}

function groupNodesByRole(nodes: NodeSummary[]) {
  const order: Array<{ key: string; label: string }> = [
    { key: "cloud", label: "云机节点" },
    { key: "local", label: "本地节点" },
    { key: "third_party", label: "第三方节点" },
    { key: "unassigned", label: "未分类节点" },
  ];
  return order
    .map((entry) => ({
      key: entry.key,
      label: entry.label,
      items: nodes.filter((node) => (node.nodeRole || "unassigned") === entry.key),
    }))
    .filter((entry) => entry.items.length > 0);
}

function roleLabel(role: string) {
  switch (role) {
    case "cloud":
      return "云机"
    case "local":
      return "本地"
    case "third_party":
      return "第三方"
    default:
      return role || "未分类"
  }
}

function nodeMeta(node: NodeSummary, key: string, fallback = "-") {
  const value = node.metadata?.[key];
  return value && value.trim() ? value : fallback;
}

function nodeAgentDeploymentLabel(node: NodeSummary) {
  const mode = nodeMeta(node, "deploymentMode", "manual");
  const unit = nodeMeta(node, "serviceUnit", "-");
  if (mode === "managed") {
    return unit !== "-" ? "systemd 常驻: " + unit : "systemd 常驻";
  }
  return "手工/临时 agent";
}

function formatDate(value: string) {
  if (!value) {
    return "-";
  }
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return value;
  }
  return parsed.toLocaleString();
}
