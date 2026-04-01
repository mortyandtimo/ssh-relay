import { FormEvent, Fragment, useEffect, useRef, useState } from "react";

type NodeCapabilities = {
  tcpRelay: boolean;
  httpRelay: boolean;
  httpsRelay: boolean;
  udpRelay: boolean;
  p2pAssist: boolean;
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
  supportsHTTP?: boolean;
  supportsHTTPS?: boolean;
  supportsSOCKS5?: boolean;
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

type NodeEditForm = {
  nodeRole: NodeRole;
  environment: NodeEnvironment;
  trustLevel: NodeTrustLevel;
  owner: string;
  location: string;
  tags: string;
};

type TunnelHealthStatus = "healthy" | "node_offline" | "capability_missing" | "misconfigured" | "target_unreachable";
type TunnelHealthFilter = "all" | "healthy" | "unhealthy";

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
  status: string;
  healthStatus?: TunnelHealthStatus;
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

type TunnelForm = {
  nodeId: string;
  name: string;
  type: "tcp" | "http" | "https" | "socks5";
  targetHost: string;
  targetPort: string;
  publicPort: string;
  domain: string;
  tlsMode: "" | "edge_terminate";
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
  const [tunnelForm, setTunnelForm] = useState<TunnelForm>(initialTunnelForm);
  const [tunnelHealthFilter, setTunnelHealthFilter] = useState<TunnelHealthFilter>("all");
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
      return null;
    });
  }, [nodes]);

  useEffect(() => {
    const selectedNode = selectedNodeID ? nodes.find((node) => node.nodeId === selectedNodeID) ?? null : null;
    setNodeEditForm(selectedNode ? toNodeEditForm(selectedNode) : null);
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
  const onlineNodes = nodes.filter((node) => node.status === "online").length;
  const activeTunnels = tunnels.filter((tunnel) => tunnel.status === "active").length;
  const filteredTunnels = tunnels.filter((tunnel) => matchesTunnelHealthFilter(tunnel, tunnelHealthFilter));
  const unhealthyTunnelCount = tunnels.filter((tunnel) => (tunnel.healthStatus || "healthy") !== "healthy").length;
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
                  <MetricCard label="隧道数量" value={String(metrics?.configuredTunnels ?? tunnels.length)} hint="公网入口配置数" />
                  <MetricCard label="待命连接" value={String(relayRuntime?.totalStandby ?? 0)} hint="反向 TCP 待命池" />
                </div>
                <div className="signal-strip">
                  <SignalCard label="服务" value={metrics?.service ?? "server-api"} />
                  <SignalCard label="启动时间" value={metrics ? formatDate(metrics.startedAt) : "-"} />
                  <SignalCard label="隧道异常" value={unhealthyTunnelCount === 0 ? "无" : String(unhealthyTunnelCount)} />
                  <SignalCard label="审计窗口" value={auditTotal === 0 ? "暂无" : auditStart + "-" + auditEnd + " / " + auditTotal} />
                  <SignalCard label="最新动作" value={auditLogs[0]?.action ?? "-"} />
                </div>
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
              <div className="split-layout connections-layout">
                <section className="subpanel workspace-column">
                  <h3>节点筛选</h3>
                  <form className="form-grid" onSubmit={submitNodeFilters}>
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
                      <span>信任级别</span>
                      <select value={nodeFilter.trustLevel} onChange={(event) => setNodeFilter((current) => ({ ...current, trustLevel: event.target.value as NodeTrustLevel, offset: 0 }))}>
                        <option value="">全部</option>
                        <option value="trusted">trusted</option>
                        <option value="limited">limited</option>
                        <option value="external">external</option>
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

                  <div className="section-head compact-head">
                    <h3>节点列表</h3>
                    <span className="muted-line">显示 {nodeStart === 0 ? 0 : nodeStart}-{nodeEnd} / {nodeTotal}</span>
                  </div>
                  <div className="table-wrap compact-table">
                    <table>
                      <thead><tr><th>节点</th><th>角色</th><th>环境</th><th>信任级别</th><th>负责人</th><th>标签</th></tr></thead>
                      <tbody>
                        {nodes.length === 0 ? <tr><td colSpan={6}>暂无节点。</td></tr> : nodes.map((node) => (
                          <tr key={node.nodeId} className={selectedNodeID === node.nodeId ? "clickable-row selected-row" : "clickable-row"} onClick={() => setSelectedNodeID((current) => current === node.nodeId ? null : node.nodeId)}>
                            <td><strong>{node.nodeName}</strong><div className="muted">{node.nodeId}</div></td>
                            <td>{node.nodeRole ? roleLabel(node.nodeRole) : "-"}</td>
                            <td>{node.environment || "-"}</td>
                            <td>{node.trustLevel || "-"}</td>
                            <td>{node.owner || "-"}</td>
                            <td>{formatTags(node.tags)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>

                  <div className="actions-row audit-pager">
                    <button type="button" className="secondary" disabled={nodeFilter.offset === 0} onClick={() => goToNodePage(nodeFilter.offset - nodeFilter.limit)}>上一页</button>
                    <button type="button" className="secondary" disabled={nodeFilter.offset + nodeFilter.limit >= nodeTotal} onClick={() => goToNodePage(nodeFilter.offset + nodeFilter.limit)}>下一页</button>
                    <span className="inline-note">limit {nodeFilter.limit} / offset {nodeFilter.offset}</span>
                  </div>
                </section>

                <section className="subpanel detail-panel">
                  <h3>节点详情</h3>
                  {selectedNode && nodeEditForm ? (
                    <>
                      <div className="detail-hero">
                        <div>
                          <strong>{selectedNode.nodeName}</strong>
                          <span className="muted-line">{selectedNode.nodeId}</span>
                        </div>
                        <span className={statusPillClass(selectedNode.status)}>{selectedNode.status}</span>
                      </div>
                      <div className="detail-grid">
                        <DetailItem label="hostname" value={nodeMeta(selectedNode, "hostname", selectedNode.nodeName)} />
                        <DetailItem label="os" value={nodeMeta(selectedNode, "os")} />
                        <DetailItem label="arch" value={nodeMeta(selectedNode, "arch")} />
                        <DetailItem label="capabilities" value={capabilitySummary(selectedNode.capabilities)} />
                        <DetailItem label="lastSeenAt" value={formatDate(selectedNode.lastSeenAt)} />
                        <DetailItem label="activeTunnels" value={String(selectedNode.activeTunnels)} />
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
                    </>
                  ) : <EmptyState title="未选择节点" body="请先在左侧列表中选中节点。当前界面仅显示当前分页的数据，避免节点过多时页面不断拉长。" />}
                </section>
              </div>
            ) : (
              <>
                <div className="section-head compact-head tunnel-filter-bar">
                  <div>
                    <h3>隧道健康筛选</h3>
                    <span className="muted-line">区分配置停用与当前异常，不再只看 active / paused。</span>
                  </div>
                  <div className="inline-switches">
                    <button type="button" className={tunnelHealthFilter === "all" ? "nav-tab active" : "nav-tab"} onClick={() => setTunnelHealthFilter("all")}>全部</button>
                    <button type="button" className={tunnelHealthFilter === "healthy" ? "nav-tab active" : "nav-tab"} onClick={() => setTunnelHealthFilter("healthy")}>正常</button>
                    <button type="button" className={tunnelHealthFilter === "unhealthy" ? "nav-tab active" : "nav-tab"} onClick={() => setTunnelHealthFilter("unhealthy")}>异常</button>
                  </div>
                </div>
                <div className="split-layout tunnel-workspace">
                  <div className="workspace-column panel-stack">
                    {editingTunnelID === null ? (
                    <section className="subpanel form-panel">
                      <h3>创建隧道</h3>
                      <form className="form-grid" onSubmit={createTunnel}>
                        <label>
                          <span>类型</span>
                          <select value={tunnelForm.type} onChange={(event) => setTunnelForm((current) => ({ ...current, type: event.target.value as "tcp" | "http" | "https" | "socks5" }))}>
                            <option value="tcp">TCP</option>
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
                            }).map((node) => <option key={node.nodeId} value={node.nodeId}>{node.nodeName} ({node.nodeId})</option>)}
                          </select>
                        </label>
                        <label><span>名称</span><input value={tunnelForm.name} onChange={(event) => setTunnelForm((current) => ({ ...current, name: event.target.value }))} required /></label>
                        <label><span>目标主机</span><input value={tunnelForm.targetHost} onChange={(event) => setTunnelForm((current) => ({ ...current, targetHost: event.target.value }))} required={tunnelForm.type !== "socks5"} disabled={tunnelForm.type === "socks5"} /></label>
                        <label><span>目标端口</span><input value={tunnelForm.targetPort} onChange={(event) => setTunnelForm((current) => ({ ...current, targetPort: event.target.value }))} inputMode="numeric" required={tunnelForm.type !== "socks5"} disabled={tunnelForm.type === "socks5"} /></label>
                        <label><span>公网端口</span><input value={tunnelForm.publicPort} onChange={(event) => setTunnelForm((current) => ({ ...current, publicPort: event.target.value }))} inputMode="numeric" required /></label>
                        {(tunnelForm.type === "http" || tunnelForm.type === "https") ? <label><span>域名</span><input value={tunnelForm.domain} onChange={(event) => setTunnelForm((current) => ({ ...current, domain: event.target.value }))} placeholder="例如 app.example.com" /></label> : null}
                        {tunnelForm.type === "https" ? <label><span>TLS 模式</span><select value={tunnelForm.tlsMode} onChange={(event) => setTunnelForm((current) => ({ ...current, tlsMode: event.target.value as "" | "edge_terminate" }))}><option value="edge_terminate">edge_terminate</option></select></label> : null}
                        {tunnelForm.type === "http" ? <div className="form-note">HTTP relay 用于发布节点上的 Web/API 服务，访问方式为 <code>http://82.156.236.104:{tunnelForm.publicPort || "<公网端口>"}</code></div> : null}
                        {tunnelForm.type === "https" ? <div className="form-note">HTTPS 最小版使用单域名 + 边缘 TLS 终止。若已接好证书与 443 入口，访问方式为 <code>https://{tunnelForm.domain || "<你的域名>"}</code></div> : null}
                        {tunnelForm.type === "socks5" ? <div className="form-note">SOCKS5 使用节点侧内置代理语义，不需要手工填写目标主机和目标端口。</div> : null}
                        <button type="submit" disabled={busyAction === "create-tunnel"}>{busyAction === "create-tunnel" ? "创建中..." : "创建隧道"}</button>
                      </form>
                    </section>
                    ) : (
                    <section className="subpanel form-panel">
                      <h3>新建隧道</h3>
                      <p className="summary">当前已选中隧道，右侧正在显示其编辑表单。再次点击已选隧道可取消选中，取消后这里会恢复新建表单。</p>
                    </section>
                    )}

                    <section className="subpanel form-panel">
                      <div className="section-head compact-head">
                        <div>
                          <p className="eyebrow">编辑</p>
                          <h3>编辑隧道</h3>
                        </div>
                        {tunnelEditForm ? <span className="muted-line">当前编辑 {tunnelEditForm.id}</span> : null}
                      </div>
                      {editingTunnelID !== null && tunnelEditForm ? (
                        <form className="form-grid" onSubmit={submitTunnelEdit}>
                          <label><span>名称</span><input value={tunnelEditForm.name} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, name: event.target.value } : current)} required /></label>
                          <label><span>目标主机</span><input value={tunnelEditForm.targetHost} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, targetHost: event.target.value } : current)} required={tunnelEditForm.type !== "socks5"} disabled={tunnelEditForm.type === "socks5"} /></label>
                          <label><span>目标端口</span><input value={tunnelEditForm.targetPort} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, targetPort: event.target.value } : current)} inputMode="numeric" required={tunnelEditForm.type !== "socks5"} disabled={tunnelEditForm.type === "socks5"} /></label>
                          <label><span>公网端口</span><input value={tunnelEditForm.publicPort} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, publicPort: event.target.value } : current)} inputMode="numeric" required /></label>
                          {(tunnelEditForm.type === "http" || tunnelEditForm.type === "https") ? <label><span>域名</span><input value={tunnelEditForm.domain} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, domain: event.target.value } : current)} placeholder="例如 app.example.com" /></label> : null}
                          {tunnelEditForm.type === "https" ? <label><span>TLS 模式</span><select value={tunnelEditForm.tlsMode} onChange={(event) => setTunnelEditForm((current) => current ? { ...current, tlsMode: event.target.value } : current)}><option value="edge_terminate">edge_terminate</option></select></label> : null}
                          {tunnelEditForm.type === "http" ? <div className="form-note">HTTP relay 用于发布节点上的 Web/API 服务，访问方式为 <code>http://82.156.236.104:{tunnelEditForm.publicPort || "<公网端口>"}</code></div> : null}
                          {tunnelEditForm.type === "https" ? <div className="form-note">HTTPS 最小版使用单域名 + 边缘 TLS 终止。若已接好证书与 443 入口，访问方式为 <code>https://{tunnelEditForm.domain || "<你的域名>"}</code></div> : null}
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
                      ) : <EmptyState title="尚未选择隧道" body="点击右侧隧道卡片或表格中的编辑按钮后，在这里修改目标映射。" />}
                    </section>
                  </div>

                  <section className="subpanel">
                    <h3>重点隧道</h3>
                    <div className="spotlight-grid compact-cards">
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
                            <span className="muted-line">{tunnelAvailabilityText(tunnel)}</span>
                          </div>
                          <div className="muted-line">类型 {tunnelTypeLabel(tunnel.type)}</div>
                          <div className="muted-line">目标 {tunnelTargetLabel(tunnel)}</div>
                          <div className="muted-line">节点 {tunnel.nodeId}</div>
                        </article>
                      ))}
                    </div>

                    {editingTunnelID !== null && tunnelEditForm?.type === "http" ? (
                      <div className="empty-state">
                        <strong>HTTP relay 最小能力说明</strong>
                        <p>当前 HTTP relay 用于发布节点上的 Web/API 服务，通过公网 HTTP 入口访问本地目标地址。</p>
                        <p>适用场景：本地开发接口、内部 Web 管理页、轻量 API 发布。</p>
                        <p>当前不包含 HTTPS、域名绑定、TLS、复杂 header 重写或 ACL。</p>
                      </div>
                    ) : null}

                    {editingTunnelID !== null && tunnelEditForm?.type === "http" && editingTunnelID === tunnelEditForm.id ? (
                      <div className="empty-state">
                        <strong>最小使用示例</strong>
                        <p>浏览器/命令行：直接访问 <code>http://82.156.236.104:{tunnelEditForm.publicPort}</code></p>
                        <p>curl: <code>curl.exe http://82.156.236.104:{tunnelEditForm.publicPort}</code></p>
                      </div>
                    ) : null}

                    {editingTunnelID !== null && tunnelEditForm?.type === "socks5" ? (
                      <div className="empty-state">
                        <strong>SOCKS5 最小能力说明</strong>
                        <p>当前 SOCKS5 入口仅支持 CONNECT，不支持 UDP associate，也不提供高级认证、ACL 或链式代理。</p>
                        <p>适用场景：临时出口代理、浏览器/命令行经 SOCKS5 发起 TCP 连接。</p>
                        <p>不适用场景：需要 UDP、需要细粒度访问控制、需要多级代理链。</p>
                      </div>
                    ) : null}

                    {editingTunnelID !== null && tunnelEditForm?.type === "socks5" && editingTunnelID === tunnelEditForm.id ? (
                      <div className="empty-state">
                        <strong>最小使用示例</strong>
                        <p>curl: <code>curl.exe --proxy socks5h://82.156.236.104:{tunnelEditForm.publicPort} https://example.com -I</code></p>
                        <p>PowerShell: 可以直接调用上面的 <code>curl.exe</code> 命令；浏览器可把 SOCKS5 代理指向 <code>82.156.236.104:{tunnelEditForm.publicPort}</code>。</p>
                      </div>
                    ) : null}
                  </section>
                </div>

                <div className="table-wrap compact-table">
                  <table>
                    <thead><tr><th>名称</th><th>类型</th><th>节点</th><th>状态/健康</th><th>公网</th><th>目标</th><th>操作</th></tr></thead>
                    <tbody>
                      {filteredTunnels.length === 0 ? <tr><td colSpan={7}>当前筛选条件下暂无隧道。</td></tr> : filteredTunnels.map((tunnel) => (
                        <tr key={tunnel.id} className={editingTunnelID === tunnel.id ? tunnelRowClass(tunnel, true) : tunnelRowClass(tunnel, false)}>
                          <td><strong>{tunnel.name}</strong><div className="muted">{tunnel.id}</div></td>
                          <td>{tunnelTypeLabel(tunnel.type)}</td>
                          <td>{tunnel.nodeId}</td>
                          <td><div className="table-status-stack"><span className={statusPillClass(tunnel.status)}>{tunnel.status}</span><span className={tunnelHealthPillClass(tunnel.healthStatus)}>{tunnelHealthLabel(tunnel.healthStatus)}</span></div></td>
                          <td>{tunnelPublicEntry(tunnel)}</td>
                          <td>{tunnelTargetLabel(tunnel)}</td>
                          <td>
                            <div className="actions-row">
                              <button type="button" className="secondary" onClick={() => beginTunnelEdit(tunnel)}>编辑</button>
                              <button type="button" disabled={busyAction === tunnel.id + ":active" || tunnel.status === "active"} onClick={() => void updateTunnelStatus(tunnel, "active")}>启用</button>
                              <button type="button" disabled={busyAction === tunnel.id + ":paused" || tunnel.status === "paused"} onClick={() => void updateTunnelStatus(tunnel, "paused")}>暂停</button>
                              <button type="button" className="danger" disabled={busyAction === tunnel.id + ":delete"} onClick={() => void deleteTunnel(tunnel)}>删除</button>
                            </div>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
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

function EmptyState({ title, body }: { title: string; body: string }) {
  return (
    <div className="empty-state">
      <strong>{title}</strong>
      <p>{body}</p>
    </div>
  );
}

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

function tunnelTypeLabel(type: string) {
  if (type === "http") return "HTTP";
  if (type === "https") return "HTTPS";
  return type === "socks5" ? "SOCKS5" : "TCP";
}

function tunnelPublicEntry(tunnel: TunnelSpec) {
  if (tunnel.type === "http") {
    return "http://82.156.236.104:" + tunnel.publicPort;
  }
  if (tunnel.type === "https") {
    return tunnel.domain ? "https://" + tunnel.domain : "https://<待绑定域名>";
  }
  if (tunnel.type === "socks5") {
    return "socks5://82.156.236.104:" + tunnel.publicPort;
  }
  return "82.156.236.104:" + tunnel.publicPort;
}

function tunnelTargetLabel(tunnel: TunnelSpec) {
  if (tunnel.type === "socks5") {
    return "节点侧 SOCKS5 CONNECT";
  }
  if (tunnel.type === "http") {
    return "发布 " + tunnel.targetHost + ":" + tunnel.targetPort + " 的 Web/API 服务";
  }
  if (tunnel.type === "https") {
    return "TLS 终止后转发到 " + tunnel.targetHost + ":" + tunnel.targetPort + (tunnel.domain ? "（域名 " + tunnel.domain + "）" : "（待绑定域名）");
  }
  return tunnel.targetHost + ":" + tunnel.targetPort;
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
  if (capabilities.httpsRelay) active.push("HTTPS");
  if (capabilities.udpRelay) active.push("UDP");
  if (capabilities.p2pAssist) active.push("P2P");
  return active.length > 0 ? active.join(" / ") : "无";
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
  };
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
