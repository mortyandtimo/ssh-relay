import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { createDesktopApi } from "../../../packages/desktop-core/src/api";
import type { NodeSummary, TunnelSpec, TunnelTypeTab, UserSummary } from "../../../packages/desktop-core/src/types";
import { capabilitySummary, formatDate, nodeAgentDeploymentLabel, publicEntry, resolveLocalNodeBinding, runtimeLabel, statusClass, tunnelTabs } from "../../../packages/desktop-core/src/utils";

const api = createDesktopApi(import.meta.env.VITE_API_BASE_URL || "");
const desktopNodeId = (import.meta.env.VITE_DESKTOP_NODE_ID || "").trim();

export default function App() {
  const [bootstrapRequired, setBootstrapRequired] = useState<boolean | null>(null);
  const [currentUser, setCurrentUser] = useState<UserSummary | null>(null);
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [tunnels, setTunnels] = useState<TunnelSpec[]>([]);
  const [activeTab, setActiveTab] = useState<TunnelTypeTab>("tcp");
  const [selectedTunnelId, setSelectedTunnelId] = useState<string | null>(null);
  const [loginForm, setLoginForm] = useState({ email: "", password: "" });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [lastRefreshAt, setLastRefreshAt] = useState("");
  const refreshInFlightRef = useRef(false);

  useEffect(() => {
    let cancelled = false;
    async function init() {
      try {
        const status = await api.loadBootstrapStatus();
        if (cancelled) return;
        setBootstrapRequired(status.required);
        if (status.required) {
          setError("当前仍需要先完成管理面 bootstrap，本轮不处理 bootstrap 流程。");
          return;
        }
        const me = await api.loadCurrentUser();
        if (cancelled) return;
        setCurrentUser(me.user);
        const payload = await api.loadDesktopData();
        if (cancelled) return;
        setNodes(payload.nodes);
        setTunnels(payload.tunnels);
        setLastRefreshAt(new Date().toISOString());
      } catch (initError) {
        if (!cancelled) {
          setError(initError instanceof Error ? initError.message : "初始化失败");
        }
      }
    }
    void init();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!currentUser) return;
    const timer = window.setInterval(() => {
      void refreshConsoleData(false, "auto");
    }, 10000);
    return () => window.clearInterval(timer);
  }, [currentUser]);

  const localBinding = useMemo(() => resolveLocalNodeBinding(nodes, desktopNodeId), [nodes]);
  const selectedNode = localBinding.node;
  const selectedNodeTunnels = useMemo(() => {
    if (!selectedNode) return [];
    return tunnels.filter((tunnel) => tunnel.nodeId === selectedNode.nodeId);
  }, [selectedNode, tunnels]);
  const tabTunnels = useMemo(() => selectedNodeTunnels.filter((tunnel) => tunnel.type === activeTab), [activeTab, selectedNodeTunnels]);

  useEffect(() => {
    if (!tabTunnels.length) {
      setSelectedTunnelId(null);
      return;
    }
    if (!selectedTunnelId || !tabTunnels.some((tunnel) => tunnel.id === selectedTunnelId)) {
      setSelectedTunnelId(tabTunnels[0].id);
    }
  }, [selectedTunnelId, tabTunnels]);

  const selectedTunnel = useMemo(
    () => (selectedTunnelId ? tabTunnels.find((tunnel) => tunnel.id === selectedTunnelId) ?? null : null),
    [selectedTunnelId, tabTunnels],
  );
  const boundNode = selectedNode;

  async function handleLogin(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy("login");
    setError("");
    setMessage("");
    try {
      const auth = await api.login(loginForm.email, loginForm.password);
      setCurrentUser(auth.user);
      const payload = await api.loadDesktopData();
      setNodes(payload.nodes);
      setTunnels(payload.tunnels);
      setLastRefreshAt(new Date().toISOString());
      setMessage("node-console 已接入当前管理面数据，正在尝试绑定本机。");
    } catch (loginError) {
      setError(loginError instanceof Error ? loginError.message : "登录失败");
    } finally {
      setBusy("");
    }
  }

  async function handleLogout() {
    setBusy("logout");
    setError("");
    setMessage("");
    try {
      await api.logout();
      setCurrentUser(null);
      setNodes([]);
      setTunnels([]);
      setSelectedTunnelId(null);
      setLastRefreshAt("");
      setMessage("已退出 node-console。");
    } catch (logoutError) {
      setError(logoutError instanceof Error ? logoutError.message : "退出失败");
    } finally {
      setBusy("");
    }
  }

  async function refreshConsoleData(showNotice: boolean, source: "manual" | "auto") {
    if (!currentUser || refreshInFlightRef.current) return;
    refreshInFlightRef.current = true;
    setRefreshing(true);
    if (showNotice) setMessage("");
    try {
      const payload = await api.loadDesktopData();
      setNodes(payload.nodes);
      setTunnels(payload.tunnels);
      setLastRefreshAt(new Date().toISOString());
      setError("");
      if (showNotice) {
        setMessage("node-console 数据已刷新，当前本机上下文已尽量保留。");
      }
    } catch (refreshError) {
      const prefix = source === "auto" ? "自动刷新失败，已保留当前页面数据。" : "手动刷新失败，已保留当前页面数据。";
      const detail = refreshError instanceof Error ? refreshError.message : "刷新失败";
      setError(prefix + " " + detail);
    } finally {
      refreshInFlightRef.current = false;
      setRefreshing(false);
    }
  }

  if (bootstrapRequired === null) {
    if (error) return <Shell><StateCard title="node-console 初始化失败" body={error} /></Shell>;
    return <Shell><StateCard title="node-console 初始化中" body="正在读取现有管理面认证状态和后端基础数据。" /></Shell>;
  }

  if (bootstrapRequired) {
    return <Shell><StateCard title="当前环境仍需 bootstrap" body="请先通过现有管理面完成初始化。" /></Shell>;
  }

  if (!currentUser) {
    return (
      <Shell>
        <div className="login-card panel">
          <p className="eyebrow">node-console</p>
          <h1>本机节点控制台</h1>
          <p className="copy">只围绕当前机器工作，不提供机器选择入口，不做控制命令和 P2P 数据面。</p>
          <form className="login-form" onSubmit={handleLogin}>
            <label><span>邮箱</span><input value={loginForm.email} onChange={(event) => setLoginForm((current) => ({ ...current, email: event.target.value }))} required /></label>
            <label><span>密码</span><input type="password" value={loginForm.password} onChange={(event) => setLoginForm((current) => ({ ...current, password: event.target.value }))} required /></label>
            <button type="submit" disabled={busy === "login"}>{busy === "login" ? "登录中..." : "进入 node-console"}</button>
          </form>
          {error ? <div className="banner error">{error}</div> : null}
          {message ? <div className="banner info">{message}</div> : null}
        </div>
      </Shell>
    );
  }

  return (
    <Shell>
      <div className="desktop-shell">
        <aside className="left-rail">
          <div className="panel brand-panel">
            <p className="eyebrow">node-console</p>
            <h1>本机节点控制台</h1>
            <p className="copy">只围绕本机工作，不再提供模式入口页，也不允许出现机器列表入口。</p>
          </div>
          <div className="panel user-panel">
            <strong>{currentUser.displayName}</strong>
            <span className="muted-line">{currentUser.email}</span>
            <span className="status-chip neutral">{currentUser.role}</span>
            <span className="muted-line">自动刷新 10s{lastRefreshAt ? " / 上次成功 " + formatDate(lastRefreshAt) : " / 尚无成功刷新"}</span>
            <div className="button-row">
              <button className="secondary" type="button" onClick={() => void refreshConsoleData(true, "manual")} disabled={refreshing}>{refreshing ? "刷新中..." : "手动刷新"}</button>
              <button className="secondary" type="button" onClick={() => void handleLogout()} disabled={busy === "logout"}>{busy === "logout" ? "退出中..." : "退出"}</button>
            </div>
          </div>
        </aside>
        <main className="main-stage">
          {error ? <div className="banner error">{error}</div> : null}
          {message ? <div className="banner info">{message}</div> : null}
          {!localBinding.node || !boundNode ? (
            <StateCard title="尚未完成本机绑定" body={localBinding.reason + " node-console 不允许出现机器选择器，请配置 VITE_DESKTOP_NODE_ID，或让当前账号下只保留唯一受管 local 节点。"} />
          ) : (
            <>
              <section className="panel hero-panel">
                <div className="hero-head">
                  <div>
                    <p className="eyebrow">本机首页</p>
                    <h2>{boundNode.nodeName}</h2>
                    <p className="copy">{boundNode.nodeId}</p>
                  </div>
                  <div className="hero-chips">
                    <span className={statusClass(boundNode.status)}>{boundNode.status}</span>
                    <span className={boundNode.isolated ? "status-chip danger" : "status-chip good"}>{boundNode.isolated ? "已隔离" : "未隔离"}</span>
                    <span className="status-chip neutral">{boundNode.agentVersion || "agent 未上报"}</span>
                    <span className="status-chip neutral">绑定来源: {localBinding.sourceLabel}</span>
                  </div>
                </div>
                <div className="hero-grid">
                  <Metric label="能力摘要" value={capabilitySummary(boundNode.capabilities)} />
                  <Metric label="active tunnel" value={String(boundNode.activeTunnels)} />
                  <Metric label="deployment" value={boundNode.deploymentMode || "尚未上报"} />
                  <Metric label="service unit" value={boundNode.serviceUnit || "尚未上报"} />
                  <Metric label="instance profile" value={boundNode.instanceProfile || "尚未上报"} />
                  <Metric label="instance managed" value={boundNode.instanceManaged ? "true" : "未托管"} />
                  <Metric label="relay path" value={String(boundNode.runtimeSummary?.relayPathCount ?? 0)} />
                  <Metric label="p2p path" value={String(boundNode.runtimeSummary?.p2pPathCount ?? 0)} />
                  <Metric label="pending" value={String(boundNode.runtimeSummary?.pendingStateCount ?? 0)} />
                  <Metric label="unavailable" value={String(boundNode.runtimeSummary?.unavailableStateCount ?? 0)} />
                  <Metric label="last seen" value={formatDate(boundNode.lastSeenAt)} />
                  <Metric label="失败原因数" value={String(boundNode.runtimeSummary?.failureReasonCount ?? 0)} />
                </div>
              </section>
              <section className="panel workbench-panel">
                <div className="panel-head">
                  <div>
                    <h2>按隧道类型切换的工作区</h2>
                    <p className="copy">围绕本机节点展示 TCP / UDP / HTTP / HTTPS / SOCKS5 的真实列表和当前选中项工作区。</p>
                  </div>
                </div>
                <div className="tab-row">
                  {tunnelTabs.map((tab) => (
                    <button key={tab} type="button" className={activeTab === tab ? "tab active" : "tab"} onClick={() => setActiveTab(tab)}>
                      {tab.toUpperCase()} ({selectedNodeTunnels.filter((item) => item.type === tab).length})
                    </button>
                  ))}
                </div>
                <div className="workspace-grid">
                  <div className="panel tunnel-list-panel">
                    <div className="panel-head small"><h3>{activeTab.toUpperCase()} 列表</h3><span>{tabTunnels.length} 条</span></div>
                    <div className="tunnel-list">
                      {tabTunnels.length === 0 ? <div className="empty-inline">当前本机没有 {activeTab.toUpperCase()} tunnel。</div> : tabTunnels.map((tunnel) => (
                        <button key={tunnel.id} type="button" className={selectedTunnelId === tunnel.id ? "tunnel-item active" : "tunnel-item"} onClick={() => setSelectedTunnelId(tunnel.id)}>
                          <strong>{tunnel.name}</strong>
                          <span>{tunnel.id}</span>
                          <span>{tunnel.transportPolicy || "relay_only"}</span>
                          <span>{runtimeLabel(tunnel)}</span>
                        </button>
                      ))}
                    </div>
                  </div>
                  <div className="panel tunnel-detail-panel">
                    <div className="panel-head small"><h3>当前隧道详情</h3></div>
                    {!selectedTunnel ? <div className="empty-inline">请先在左侧选择一个 tunnel。</div> : (
                      <div className="detail-stack">
                        <Metric label="name" value={selectedTunnel.name} />
                        <Metric label="type" value={selectedTunnel.type} />
                        <Metric label="transportPolicy" value={selectedTunnel.transportPolicy || "relay_only"} />
                        <Metric label="runtimePath" value={selectedTunnel.runtimePath || "尚无运行态上报"} />
                        <Metric label="runtimeState" value={selectedTunnel.runtimeState || "尚无运行态上报"} />
                        <Metric label="lastFailureReason" value={selectedTunnel.lastFailureReason || "尚无运行态上报"} />
                        <Metric label="public entry" value={publicEntry(selectedTunnel)} />
                        <Metric label="target" value={selectedTunnel.targetHost + ":" + selectedTunnel.targetPort} />
                        <Metric label="status" value={selectedTunnel.status} />
                      </div>
                    )}
                  </div>
                </div>
              </section>
            </>
          )}
        </main>
      </div>
    </Shell>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return <div className="desktop-root">{children}</div>;
}

function StateCard({ title, body }: { title: string; body: string }) {
  return <div className="panel state-card"><h1>{title}</h1><p className="copy">{body}</p></div>;
}

function Metric({ label, value }: { label: string; value: string }) {
  return <div className="metric-box"><span>{label}</span><strong>{value}</strong></div>;
}
