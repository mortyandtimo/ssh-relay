import { FormEvent, useEffect, useRef, useState } from "react";

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
  createdAt: string;
};

type DashboardPayload = {
  nodes: NodeSummary[];
  tunnels: TunnelSpec[];
  metrics: ServerMetrics;
  relayRuntime: RelayRuntimeSummary;
  users: UserSummary[];
  auditLogs: AuditLogEntry[];
};

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || "";

const initialTunnelForm: TunnelForm = {
  nodeId: "",
  name: "",
  targetHost: "127.0.0.1",
  targetPort: "",
  publicPort: "",
};

export default function App() {
  const [bootstrapRequired, setBootstrapRequired] = useState<boolean | null>(null);
  const [currentUser, setCurrentUser] = useState<UserSummary | null>(null);
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [tunnels, setTunnels] = useState<TunnelSpec[]>([]);
  const [users, setUsers] = useState<UserSummary[]>([]);
  const [auditLogs, setAuditLogs] = useState<AuditLogEntry[]>([]);
  const [metrics, setMetrics] = useState<ServerMetrics | null>(null);
  const [relayRuntime, setRelayRuntime] = useState<RelayRuntimeSummary | null>(null);
  const [tunnelForm, setTunnelForm] = useState<TunnelForm>(initialTunnelForm);
  const [loginForm, setLoginForm] = useState({ email: "", password: "" });
  const [bootstrapForm, setBootstrapForm] = useState({ email: "", displayName: "管理员", password: "", bootstrapSecret: "" });
  const [userForm, setUserForm] = useState({ email: "", displayName: "", password: "", role: "manager" as UserRole });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busyAction, setBusyAction] = useState("");
  const [hasInitializedNodeId, setHasInitializedNodeId] = useState(false);
  const refreshInFlightRef = useRef<Promise<boolean> | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function init() {
      try {
        const status = await requestJSON<{ required: boolean }>("/api/auth/bootstrap-status");
        if (cancelled) {
          return;
        }
        setBootstrapRequired(status.required);
        if (status.required) {
          return;
        }
        const me = await requestJSON<{ user: UserSummary }>("/api/auth/me");
        if (cancelled) {
          return;
        }
        setCurrentUser(me.user);
        await refreshDashboard(false, me.user, cancelled);
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
    if (!currentUser) {
      return;
    }
    let cancelled = false;
    const timer = window.setInterval(() => {
      void refreshDashboard(false, currentUser, cancelled);
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
    if (response.status === 401 && allowRefresh && path !== "/api/auth/login" && path !== "/api/auth/bootstrap" && path !== "/api/auth/refresh" && path !== "/api/auth/bootstrap-status") {
      const refreshed = await refreshAuthSession();
      if (refreshed) {
        return requestJSON<T>(path, init, false);
      }
      setCurrentUser(null);
      setNodes([]);
      setTunnels([]);
      setUsers([]);
      setAuditLogs([]);
      setMetrics(null);
      setRelayRuntime(null);
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
      const payload = await response.json().catch(() => null) as { user?: UserSummary } | null;
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

  async function refreshDashboard(showNotice: boolean, user = currentUser, cancelled = false) {
    if (!user) {
      return;
    }
    try {
      const requests = [
        requestJSON<{ items: NodeSummary[] }>("/api/nodes"),
        requestJSON<{ items: TunnelSpec[] }>("/api/tunnels"),
        requestJSON<ServerMetrics>("/api/server/metrics"),
        requestJSON<RelayRuntimeSummary>("/api/relay/tcp/runtime"),
      ] as const;
      const userRequests = user.role === "admin" ? [requestJSON<{ items: UserSummary[] }>("/api/users")] : [];
      const auditRequests = user.role !== "user" ? [requestJSON<{ items: AuditLogEntry[] }>("/api/audit-logs?limit=50")] : [];
      const results = await Promise.all([...requests, ...userRequests, ...auditRequests]);
      if (cancelled) {
        return;
      }
      const [nodesPayload, tunnelsPayload, metricsPayload, relayPayload, usersPayload, auditPayload] = results as unknown as [
        { items: NodeSummary[] },
        { items: TunnelSpec[] },
        ServerMetrics,
        RelayRuntimeSummary,
        { items: UserSummary[] } | undefined,
        { items: AuditLogEntry[] } | undefined,
      ];
      setNodes(nodesPayload.items || []);
      setTunnels(tunnelsPayload.items || []);
      setMetrics(metricsPayload);
      setRelayRuntime(relayPayload);
      setUsers(usersPayload?.items || []);
      setAuditLogs(auditPayload?.items || []);
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

  async function submitBootstrap(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusyAction("bootstrap");
    setError("");
    setMessage("");
    try {
      const payload = await requestJSON<{ user: UserSummary }>("/api/auth/bootstrap", {
        method: "POST",
        headers: { "X-Bootstrap-Secret": bootstrapForm.bootstrapSecret },
        body: JSON.stringify({ email: bootstrapForm.email, displayName: bootstrapForm.displayName, password: bootstrapForm.password }),
      });
      setCurrentUser(payload.user);
      setBootstrapRequired(false);
      setBootstrapForm({ email: "", displayName: "管理员", password: "", bootstrapSecret: "" });
      setMessage("管理员账户已初始化。");
      await refreshDashboard(false, payload.user);
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
      await refreshDashboard(false, payload.user);
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
      setCurrentUser(null);
      setNodes([]);
      setTunnels([]);
      setUsers([]);
      setAuditLogs([]);
      setMetrics(null);
      setRelayRuntime(null);
      setTunnelForm(initialTunnelForm);
      setHasInitializedNodeId(false);
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
      await refreshDashboard(false);
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
      await refreshDashboard(false);
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
      await refreshDashboard(false);
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
      await refreshDashboard(false);
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "创建用户失败");
    } finally {
      setBusyAction("");
    }
  }

  if (bootstrapRequired === null) {
    return <div className="shell"><div className="panel">正在检查管理面初始化状态...</div></div>;
  }

  if (bootstrapRequired) {
    return (
      <div className="shell auth-shell">
        <section className="panel auth-panel">
          <p className="eyebrow">初始化</p>
          <h1>创建首个管理员账户</h1>
          {error ? <div className="error">{error}</div> : null}
          <form className="tunnel-form" onSubmit={submitBootstrap}>
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
      <div className="shell auth-shell">
        <section className="panel auth-panel">
          <p className="eyebrow">登录</p>
          <h1>管理面登录</h1>
          {error ? <div className="error">{error}</div> : null}
          <form className="tunnel-form" onSubmit={submitLogin}>
            <label><span>邮箱</span><input value={loginForm.email} onChange={(event) => setLoginForm((current) => ({ ...current, email: event.target.value }))} required /></label>
            <label><span>密码</span><input type="password" value={loginForm.password} onChange={(event) => setLoginForm((current) => ({ ...current, password: event.target.value }))} required /></label>
            <button type="submit" disabled={busyAction === "login"}>{busyAction === "login" ? "登录中..." : "登录"}</button>
          </form>
        </section>
      </div>
    );
  }

  return (
    <div className="shell">
      <header className="hero">
        <div>
          <p className="eyebrow">云中继平台</p>
          <h1>管理中心</h1>
          <p className="summary">已通过会话登录保护管理面，支持管理员、管理用户、普通用户三级权限。</p>
          <p className="inline-note">当前登录：{currentUser.displayName} / {currentUser.role}</p>
        </div>
        <div className="hero-actions">
          <button className="secondary" type="button" onClick={() => void refreshDashboard(true)} disabled={busyAction === "refresh"}>刷新</button>
          <button type="button" onClick={() => void logout()} disabled={busyAction === "logout"}>{busyAction === "logout" ? "退出中..." : "退出登录"}</button>
        </div>
        {metrics ? (
          <div className="metrics-grid">
            <MetricCard label="注册节点" value={String(metrics.registeredNodes)} />
            <MetricCard label="在线节点" value={String(metrics.onlineNodes)} />
            <MetricCard label="隧道数量" value={String(metrics.configuredTunnels)} />
            <MetricCard label="中继服务" value={String(metrics.protocolRelayCount)} />
            <MetricCard label="待命连接" value={String(relayRuntime?.totalStandby ?? 0)} />
          </div>
        ) : null}
      </header>

      {error ? <div className="error">{error}</div> : null}
      {message ? <div className="notice">{message}</div> : null}

      {currentUser.role !== "user" ? (
        <section className="panel">
          <div className="panel-header"><div><p className="eyebrow">创建隧道</p><h2>暴露节点本地服务</h2></div></div>
          <form className="tunnel-form" onSubmit={createTunnel}>
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
      ) : null}

      <section className="panel">
        <div className="panel-header"><div><p className="eyebrow">中继状态</p><h2>待命池摘要</h2></div></div>
        <div className="table-wrap">
          <table>
            <thead><tr><th>池键</th><th>节点</th><th>公网端口</th><th>待命数</th><th>目标值</th><th>上限</th></tr></thead>
            <tbody>
              {relayRuntime?.pools?.length ? relayRuntime.pools.map((pool) => (
                <tr key={pool.poolKey}><td>{pool.poolKey}</td><td>{pool.nodeId}</td><td>{pool.publicPort}</td><td>{pool.standbyCount}</td><td>{pool.targetSize}</td><td>{pool.maxSize}</td></tr>
              )) : <tr><td colSpan={6}>暂无中继池状态。</td></tr>}
            </tbody>
          </table>
        </div>
      </section>

      {currentUser.role !== "user" ? (
        <section className="panel">
          <div className="panel-header"><div><p className="eyebrow">隧道</p><h2>当前隧道列表</h2></div></div>
          <div className="table-wrap">
            <table>
              <thead><tr><th>名称</th><th>节点</th><th>状态</th><th>公网</th><th>目标</th><th>操作</th></tr></thead>
              <tbody>
                {tunnels.length === 0 ? <tr><td colSpan={6}>暂无隧道。</td></tr> : tunnels.map((tunnel) => (
                  <tr key={tunnel.id}>
                    <td><strong>{tunnel.name}</strong><div className="muted">{tunnel.id}</div></td>
                    <td>{tunnel.nodeId}</td>
                    <td>{tunnel.status}</td>
                    <td>{tunnel.publicPort}</td>
                    <td>{tunnel.targetHost}:{tunnel.targetPort}</td>
                    <td><div className="actions-row">
                      <button type="button" disabled={busyAction === tunnel.id + ":active" || tunnel.status === "active"} onClick={() => void updateTunnelStatus(tunnel, "active")}>启用</button>
                      <button type="button" disabled={busyAction === tunnel.id + ":paused" || tunnel.status === "paused"} onClick={() => void updateTunnelStatus(tunnel, "paused")}>暂停</button>
                      <button type="button" className="danger" disabled={busyAction === tunnel.id + ":delete"} onClick={() => void deleteTunnel(tunnel)}>删除</button>
                    </div></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}

      <section className="panel">
        <div className="panel-header"><div><p className="eyebrow">节点</p><h2>节点在线状态</h2></div></div>
        <div className="table-wrap">
          <table>
            <thead><tr><th>节点</th><th>状态</th><th>Agent</th><th>隧道数</th><th>能力</th><th>最后心跳</th></tr></thead>
            <tbody>
              {nodes.length === 0 ? <tr><td colSpan={6}>暂无节点。</td></tr> : nodes.map((node) => (
                <tr key={node.nodeId}>
                  <td><strong>{node.nodeName}</strong><div className="muted">{node.nodeId}</div></td>
                  <td>{node.status}</td>
                  <td>{node.agentVersion}</td>
                  <td>{node.activeTunnels}</td>
                  <td>{capabilitySummary(node.capabilities)}</td>
                  <td>{new Date(node.lastSeenAt).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>



      {currentUser.role !== "user" ? (
        <section className="panel">
          <div className="panel-header"><div><p className="eyebrow">审计</p><h2>最近操作历史</h2></div></div>
          <div className="table-wrap">
            <table>
              <thead><tr><th>时间</th><th>Actor</th><th>Action</th><th>资源类型</th><th>资源 ID</th></tr></thead>
              <tbody>
                {auditLogs.length === 0 ? <tr><td colSpan={5}>暂无审计日志。</td></tr> : auditLogs.map((entry) => (
                  <tr key={entry.id}>
                    <td>{new Date(entry.createdAt).toLocaleString()}</td>
                    <td>{entry.actorType}{entry.actorId ? ':' + entry.actorId : ''}</td>
                    <td>{entry.action}</td>
                    <td>{entry.resourceType}</td>
                    <td>{entry.resourceId || '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}

      {currentUser.role === "admin" ? (
        <section className="panel">
          <div className="panel-header"><div><p className="eyebrow">用户</p><h2>用户与角色</h2></div></div>
          <form className="tunnel-form" onSubmit={createUser}>
            <label><span>邮箱</span><input value={userForm.email} onChange={(event) => setUserForm((current) => ({ ...current, email: event.target.value }))} required /></label>
            <label><span>显示名称</span><input value={userForm.displayName} onChange={(event) => setUserForm((current) => ({ ...current, displayName: event.target.value }))} required /></label>
            <label><span>密码</span><input type="password" value={userForm.password} onChange={(event) => setUserForm((current) => ({ ...current, password: event.target.value }))} required /></label>
            <label><span>角色</span><select value={userForm.role} onChange={(event) => setUserForm((current) => ({ ...current, role: event.target.value as UserRole }))}><option value="manager">管理用户</option><option value="user">普通用户</option><option value="admin">管理员</option></select></label>
            <button type="submit" disabled={busyAction === "create-user"}>{busyAction === "create-user" ? "创建中..." : "创建用户"}</button>
          </form>
          <div className="table-wrap">
            <table>
              <thead><tr><th>邮箱</th><th>显示名称</th><th>角色</th><th>创建时间</th></tr></thead>
              <tbody>
                {users.length === 0 ? <tr><td colSpan={4}>暂无用户。</td></tr> : users.map((user) => (
                  <tr key={user.id}><td>{user.email}</td><td>{user.displayName}</td><td>{user.role}</td><td>{new Date(user.createdAt).toLocaleString()}</td></tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}
    </div>
  );
}

function MetricCard({ label, value }: { label: string; value: string }) {
  return <div className="metric-card"><span>{label}</span><strong>{value}</strong></div>;
}

function capabilitySummary(capabilities: NodeCapabilities) {
  const active = [] as string[];
  if (capabilities.tcpRelay) active.push("TCP");
  if (capabilities.httpRelay) active.push("HTTP");
  if (capabilities.httpsRelay) active.push("HTTPS");
  if (capabilities.udpRelay) active.push("UDP");
  if (capabilities.p2pAssist) active.push("P2P");
  return active.length > 0 ? active.join(", ") : "无";
}
