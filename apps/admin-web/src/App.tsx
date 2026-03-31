import { FormEvent, Fragment, useEffect, useRef, useState } from "react";

type NodeCapabilities = {
  tcpRelay: boolean;
  httpRelay: boolean;
  httpsRelay: boolean;
  udpRelay: boolean;
  p2pAssist: boolean;
};

type NodeSummary = {
  nodeId: string;
  nodeName: string;
  status: string;
  agentVersion: string;
  capabilities: NodeCapabilities;
  activeTunnels: number;
  lastSeenAt: string;
};

type TunnelSpec = {
  id: string;
  name: string;
  type: string;
  transportPolicy: string;
  nodeId: string;
  targetHost: string;
  targetPort: number;
  publicPort: number;
  status: string;
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
  targetHost: string;
  targetPort: string;
  publicPort: string;
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
  targetHost: "127.0.0.1",
  targetPort: "",
  publicPort: "",
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

export default function App() {
  const [bootstrapRequired, setBootstrapRequired] = useState<boolean | null>(null);
  const [currentUser, setCurrentUser] = useState<UserSummary | null>(null);
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [tunnels, setTunnels] = useState<TunnelSpec[]>([]);
  const [users, setUsers] = useState<UserSummary[]>([]);
  const [auditLogs, setAuditLogs] = useState<AuditLogEntry[]>([]);
  const [auditTotal, setAuditTotal] = useState(0);
  const [expandedAuditID, setExpandedAuditID] = useState<number | null>(null);
  const [metrics, setMetrics] = useState<ServerMetrics | null>(null);
  const [relayRuntime, setRelayRuntime] = useState<RelayRuntimeSummary | null>(null);
  const [tunnelForm, setTunnelForm] = useState<TunnelForm>(initialTunnelForm);
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
        await refreshDashboard(false, me.user, cancelled, auditFilterRef.current);
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
    if (!currentUser) {
      return;
    }
    let cancelled = false;
    const timer = window.setInterval(() => {
      void refreshDashboard(false, currentUser, cancelled, auditFilterRef.current);
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

  async function refreshDashboard(showNotice: boolean, user = currentUser, cancelled = false, filter = auditFilterRef.current) {
    if (!user) {
      return;
    }

    try {
      const overviewRequests =
        user.role !== "user"
          ? [
              requestJSON<{ items: NodeSummary[] }>("/api/nodes"),
              requestJSON<{ items: TunnelSpec[] }>("/api/tunnels"),
              requestJSON<ServerMetrics>("/api/server/metrics"),
              requestJSON<RelayRuntimeSummary>("/api/relay/tcp/runtime"),
            ]
          : [
              Promise.resolve({ items: [] as NodeSummary[] }),
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
          ? [requestJSON<AuditLogListResponse>("/api/audit-logs?" + buildAuditQuery(filter).toString())]
          : [Promise.resolve(undefined as AuditLogListResponse | undefined)];

      const results = await Promise.all([...overviewRequests, ...userRequests, ...auditRequests]);
      if (cancelled) {
        return;
      }

      const [nodesPayload, tunnelsPayload, metricsPayload, relayPayload, usersPayload, auditPayload] = results as [
        { items: NodeSummary[] },
        { items: TunnelSpec[] },
        ServerMetrics | null,
        RelayRuntimeSummary | null,
        { items: UserSummary[] } | undefined,
        AuditLogListResponse | undefined,
      ];

      setNodes(nodesPayload.items || []);
      setTunnels(tunnelsPayload.items || []);
      setMetrics(metricsPayload);
      setRelayRuntime(relayPayload);
      setUsers(usersPayload?.items || []);
      setAuditLogs(auditPayload?.items || []);
      setAuditTotal(auditPayload?.total || 0);

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
    setTunnels([]);
    setUsers([]);
    setAuditLogs([]);
    setAuditTotal(0);
    setMetrics(null);
    setRelayRuntime(null);
    setTunnelForm(initialTunnelForm);
    setHasInitializedNodeId(false);
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
      await refreshDashboard(false, payload.user, false, auditFilterRef.current);
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
      await refreshDashboard(false, payload.user, false, auditFilterRef.current);
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
          type: "tcp",
          transportPolicy: "relay_only",
          targetHost: tunnelForm.targetHost,
          targetPort: Number(tunnelForm.targetPort),
          publicPort: Number(tunnelForm.publicPort),
          status: "active",
        }),
      });
      setTunnelForm((current) => ({ ...initialTunnelForm, nodeId: current.nodeId }));
      setMessage("隧道已创建。");
      await refreshDashboard(false, currentUser, false, auditFilterRef.current);
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "创建隧道失败");
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
      await refreshDashboard(false, currentUser, false, auditFilterRef.current);
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
      setMessage("隧道已删除。");
      await refreshDashboard(false, currentUser, false, auditFilterRef.current);
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
      await refreshDashboard(false, currentUser, false, auditFilterRef.current);
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "创建用户失败");
    } finally {
      setBusyAction("");
    }
  }

  function submitAuditFilters(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextFilter = { ...auditFilter, offset: 0 };
    setAuditFilter(nextFilter);
    auditFilterRef.current = nextFilter;
    void refreshDashboard(false, currentUser, false, nextFilter);
  }

  function goToAuditPage(nextOffset: number) {
    const nextFilter = { ...auditFilter, offset: nextOffset };
    setAuditFilter(nextFilter);
    auditFilterRef.current = nextFilter;
    void refreshDashboard(false, currentUser, false, nextFilter);
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
  const onlineNodes = nodes.filter((node) => node.status === "online").length;
  const activeTunnels = tunnels.filter((tunnel) => tunnel.status === "active").length;
  const canOperate = activeUser.role !== "user";
  const canManageUsers = activeUser.role === "admin";
  const auditStart = auditLogs.length === 0 ? 0 : auditFilter.offset + 1;
  const auditEnd = Math.min(auditFilter.offset + auditFilter.limit, auditTotal);

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
          <p className="eyebrow">Bootstrap</p>
          <h1>首次安装初始化</h1>
          <p className="summary">输入管理员信息和 bootstrap secret，初始化后直接进入控制台。</p>
          {error ? <div className="error">{error}</div> : null}
          <form className="form-grid" onSubmit={submitBootstrap}>
            <label><span>邮箱</span><input value={bootstrapForm.email} onChange={(event) => setBootstrapForm((current) => ({ ...current, email: event.target.value }))} required /></label>
            <label><span>显示名称</span><input value={bootstrapForm.displayName} onChange={(event) => setBootstrapForm((current) => ({ ...current, displayName: event.target.value }))} required /></label>
            <label><span>密码</span><input type="password" value={bootstrapForm.password} onChange={(event) => setBootstrapForm((current) => ({ ...current, password: event.target.value }))} required /></label>
            <label><span>Bootstrap Secret</span><input type="password" value={bootstrapForm.bootstrapSecret} onChange={(event) => setBootstrapForm((current) => ({ ...current, bootstrapSecret: event.target.value }))} required /></label>
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
          <p className="eyebrow">Login</p>
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
          <p className="eyebrow">Cloud Relay Console</p>
          <h1>控制台</h1>
          <p className="sidebar-copy">切换工作区而不是在同一页里反复滚动寻找目标模块。</p>
        </div>

        <div className="sidebar-card operator-card">
          <span className={roleClass(activeUser.role)}>{activeUser.role}</span>
          <strong>{activeUser.displayName}</strong>
          <span className="muted-line">{activeUser.email}</span>
          <div className="sidebar-actions">
            <button className="secondary" type="button" onClick={() => void refreshDashboard(true, currentUser, false, auditFilterRef.current)} disabled={busyAction === "refresh"}>刷新当前工作区</button>
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
                <p className="eyebrow">Overview</p>
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
                  <SignalCard label="审计窗口" value={auditTotal === 0 ? "暂无" : `${auditStart}-${auditEnd} / ${auditTotal}`} />
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
                <p className="eyebrow">Connections</p>
                <h2>连接管理</h2>
              </div>
              <div className="inline-switches">
                <button type="button" className={connectionView === "nodes" ? "nav-tab active" : "nav-tab"} onClick={() => setConnectionView("nodes")}>节点</button>
                <button type="button" className={connectionView === "tunnels" ? "nav-tab active" : "nav-tab"} onClick={() => setConnectionView("tunnels")}>隧道</button>
              </div>
            </div>

            {connectionView === "nodes" ? (
              <>
                <div className="spotlight-grid">
                  {nodes.length === 0 ? <EmptyState title="暂无节点" body="当前没有可展示的节点状态。" /> : nodes.map((node) => (
                    <article key={node.nodeId} className="spotlight-card">
                      <div className="spotlight-head">
                        <div>
                          <strong>{node.nodeName}</strong>
                          <span className="muted-line">{node.nodeId}</span>
                        </div>
                        <span className={statusPillClass(node.status)}>{node.status}</span>
                      </div>
                      <div className="spotlight-meta">
                        <span>Agent {node.agentVersion}</span>
                        <span>{node.activeTunnels} 条隧道</span>
                      </div>
                      <div className="capability-row">{capabilitySummary(node.capabilities)}</div>
                      <div className="muted-line">最后心跳：{formatDate(node.lastSeenAt)}</div>
                    </article>
                  ))}
                </div>
                <div className="table-wrap compact-table">
                  <table>
                    <thead><tr><th>节点</th><th>状态</th><th>Agent</th><th>隧道数</th><th>能力</th><th>最后心跳</th></tr></thead>
                    <tbody>
                      {nodes.length === 0 ? <tr><td colSpan={6}>暂无节点。</td></tr> : nodes.map((node) => (
                        <tr key={node.nodeId}>
                          <td><strong>{node.nodeName}</strong><div className="muted">{node.nodeId}</div></td>
                          <td><span className={statusPillClass(node.status)}>{node.status}</span></td>
                          <td>{node.agentVersion}</td>
                          <td>{node.activeTunnels}</td>
                          <td>{capabilitySummary(node.capabilities)}</td>
                          <td>{formatDate(node.lastSeenAt)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
            ) : (
              <>
                <div className="split-layout">
                  <section className="subpanel form-panel">
                    <h3>创建隧道</h3>
                    <form className="form-grid" onSubmit={createTunnel}>
                      <label>
                        <span>节点</span>
                        <select value={tunnelForm.nodeId} onChange={(event) => { setTunnelForm((current) => ({ ...current, nodeId: event.target.value })); setHasInitializedNodeId(true); }} required>
                          <option value="">选择节点</option>
                          {nodes.map((node) => <option key={node.nodeId} value={node.nodeId}>{node.nodeName} ({node.nodeId})</option>)}
                        </select>
                      </label>
                      <label><span>名称</span><input value={tunnelForm.name} onChange={(event) => setTunnelForm((current) => ({ ...current, name: event.target.value }))} required /></label>
                      <label><span>目标主机</span><input value={tunnelForm.targetHost} onChange={(event) => setTunnelForm((current) => ({ ...current, targetHost: event.target.value }))} required /></label>
                      <label><span>目标端口</span><input value={tunnelForm.targetPort} onChange={(event) => setTunnelForm((current) => ({ ...current, targetPort: event.target.value }))} inputMode="numeric" required /></label>
                      <label><span>公网端口</span><input value={tunnelForm.publicPort} onChange={(event) => setTunnelForm((current) => ({ ...current, publicPort: event.target.value }))} inputMode="numeric" required /></label>
                      <button type="submit" disabled={busyAction === "create-tunnel"}>{busyAction === "create-tunnel" ? "创建中..." : "创建隧道"}</button>
                    </form>
                  </section>

                  <section className="subpanel">
                    <h3>重点隧道</h3>
                    <div className="spotlight-grid compact-cards">
                      {tunnels.length === 0 ? <EmptyState title="暂无隧道" body="创建后会在这里优先展示公网入口和目标映射。" /> : tunnels.map((tunnel) => (
                        <article key={tunnel.id} className="spotlight-card tunnel-card">
                          <div className="spotlight-head">
                            <div>
                              <strong>{tunnel.name}</strong>
                              <span className="muted-line">{tunnel.id}</span>
                            </div>
                            <span className={statusPillClass(tunnel.status)}>{tunnel.status}</span>
                          </div>
                          <div className="tunnel-route">公网 {tunnel.publicPort}</div>
                          <div className="muted-line">目标 {tunnel.targetHost}:{tunnel.targetPort}</div>
                          <div className="muted-line">节点 {tunnel.nodeId}</div>
                        </article>
                      ))}
                    </div>
                  </section>
                </div>

                <div className="table-wrap compact-table">
                  <table>
                    <thead><tr><th>名称</th><th>节点</th><th>状态</th><th>公网</th><th>目标</th><th>操作</th></tr></thead>
                    <tbody>
                      {tunnels.length === 0 ? <tr><td colSpan={6}>暂无隧道。</td></tr> : tunnels.map((tunnel) => (
                        <tr key={tunnel.id}>
                          <td><strong>{tunnel.name}</strong><div className="muted">{tunnel.id}</div></td>
                          <td>{tunnel.nodeId}</td>
                          <td><span className={statusPillClass(tunnel.status)}>{tunnel.status}</span></td>
                          <td>{tunnel.publicPort}</td>
                          <td>{tunnel.targetHost}:{tunnel.targetPort}</td>
                          <td>
                            <div className="actions-row">
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
                <p className="eyebrow">Audit</p>
                <h2>审计</h2>
              </div>
              <span className="muted-line">审计工作区独立保留筛选、分页与 payload 展开</span>
            </div>

            <section className="subpanel audit-filter-panel">
              <h3>筛选栏</h3>
              <form className="form-grid" onSubmit={submitAuditFilters}>
                <label><span>Action</span><input value={auditFilter.action} onChange={(event) => setAuditFilter((current) => ({ ...current, action: event.target.value }))} /></label>
                <label><span>Actor Type</span><input value={auditFilter.actorType} onChange={(event) => setAuditFilter((current) => ({ ...current, actorType: event.target.value }))} /></label>
                <label><span>Resource Type</span><input value={auditFilter.resourceType} onChange={(event) => setAuditFilter((current) => ({ ...current, resourceType: event.target.value }))} /></label>
                <label><span>Actor ID</span><input value={auditFilter.actorID} onChange={(event) => setAuditFilter((current) => ({ ...current, actorID: event.target.value }))} /></label>
                <label><span>开始时间</span><input type="datetime-local" value={auditFilter.startAt} onChange={(event) => setAuditFilter((current) => ({ ...current, startAt: event.target.value }))} /></label>
                <label><span>结束时间</span><input type="datetime-local" value={auditFilter.endAt} onChange={(event) => setAuditFilter((current) => ({ ...current, endAt: event.target.value }))} /></label>
                <button type="submit">应用筛选</button>
              </form>
            </section>

            <section className="subpanel audit-table-panel">
              <div className="audit-header-row">
                <h3>最近操作历史</h3>
                <span className="audit-page-status">显示 {auditStart === 0 ? 0 : auditStart}-{auditEnd} / {auditTotal}</span>
              </div>
              <div className="table-wrap compact-table dense-table">
                <table>
                  <thead><tr><th>时间</th><th>Actor</th><th>Action</th><th>资源类型</th><th>资源 ID</th></tr></thead>
                  <tbody>
                    {auditLogs.length === 0 ? <tr><td colSpan={5}>暂无审计日志。</td></tr> : auditLogs.map((entry) => (
                      <Fragment key={entry.id}>
                        <tr className="audit-row" onClick={() => setExpandedAuditID((current) => current === entry.id ? null : entry.id)}>
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
                <p className="eyebrow">Permissions</p>
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

function EmptyState({ title, body }: { title: string; body: string }) {
  return (
    <div className="empty-state">
      <strong>{title}</strong>
      <p>{body}</p>
    </div>
  );
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

function formatDate(value: string) {
  return new Date(value).toLocaleString();
}
