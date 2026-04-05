import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { loadBootstrapStatus, loadCurrentUser, loadDesktopData, login, logout } from "./api";
import type { DesktopMode, NodeCapabilities, NodeSummary, TunnelSpec, TunnelTypeTab, UserSummary } from "./types";

const tunnelTabs: TunnelTypeTab[] = ["tcp", "udp", "http", "https", "socks5"];
const desktopNodeId = (import.meta.env.VITE_DESKTOP_NODE_ID || "").trim();

export default function App() {
  const [bootstrapRequired, setBootstrapRequired] = useState<boolean | null>(null);
  const [currentUser, setCurrentUser] = useState<UserSummary | null>(null);
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [tunnels, setTunnels] = useState<TunnelSpec[]>([]);
  const [mode, setMode] = useState<DesktopMode | null>(null);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
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
        const status = await loadBootstrapStatus();
        if (cancelled) return;
        setBootstrapRequired(status.required);
        if (status.required) {
          setError("当前仍需要先完成管理面 bootstrap，本轮桌面端骨架不处理 bootstrap 流程。");
          return;
        }
        const me = await loadCurrentUser();
        if (cancelled) return;
        setCurrentUser(me.user);
        const payload = await loadDesktopData();
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
    if (!currentUser) {
      return;
    }
    const timer = window.setInterval(() => {
      void refreshConsoleData(false, "auto");
    }, 10000);
    return () => window.clearInterval(timer);
  }, [currentUser]);

  const localBinding = useMemo(() => resolveLocalNodeBinding(nodes), [nodes]);

  useEffect(() => {
    if (!mode) {
      setSelectedNodeId(null);
      return;
    }
    if (mode === "local-node") {
      setSelectedNodeId(localBinding.node?.nodeId ?? null);
      return;
    }
    setSelectedNodeId((current) => {
      if (current && nodes.some((node) => node.nodeId === current)) {
        return current;
      }
      return nodes[0]?.nodeId ?? null;
    });
  }, [localBinding.node, mode, nodes]);

  const selectedNode = useMemo(
    () => (selectedNodeId ? nodes.find((node) => node.nodeId === selectedNodeId) ?? null : null),
    [nodes, selectedNodeId],
  );

  const selectedNodeTunnels = useMemo(() => {
    if (!selectedNode) return [];
    return tunnels.filter((tunnel) => tunnel.nodeId === selectedNode.nodeId);
  }, [selectedNode, tunnels]);

  const tabTunnels = useMemo(() => selectedNodeTunnels.filter((tunnel) => tunnel.type === activeTab), [selectedNodeTunnels, activeTab]);

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

  async function handleLogin(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy("login");
    setError("");
    setMessage("");
    try {
      const auth = await login(loginForm.email, loginForm.password);
      setCurrentUser(auth.user);
      const payload = await loadDesktopData();
      setNodes(payload.nodes);
      setTunnels(payload.tunnels);
      setLastRefreshAt(new Date().toISOString());
      setMessage("桌面端骨架已接入当前管理面数据。请选择模式继续。");
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
      await logout();
      setCurrentUser(null);
      setMode(null);
      setNodes([]);
      setTunnels([]);
      setSelectedNodeId(null);
      setSelectedTunnelId(null);
      setLastRefreshAt("");
      setMessage("已退出桌面端骨架会话。");
    } catch (logoutError) {
      setError(logoutError instanceof Error ? logoutError.message : "退出失败");
    } finally {
      setBusy("");
    }
  }

  async function refreshConsoleData(showNotice: boolean, source: "manual" | "auto") {
    if (!currentUser || refreshInFlightRef.current) {
      return;
    }
    refreshInFlightRef.current = true;
    setRefreshing(true);
    if (showNotice) {
      setMessage("");
    }
    try {
      const payload = await loadDesktopData();
      setNodes(payload.nodes);
      setTunnels(payload.tunnels);
      setLastRefreshAt(new Date().toISOString());
      setError("");
      if (showNotice) {
        setMessage("桌面端数据已刷新，当前模式和选择上下文已尽量保留。");
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
    return <Shell><StateCard title="桌面端骨架初始化中" body="正在读取现有管理面认证状态和后端基础数据。" /></Shell>;
  }

  if (bootstrapRequired) {
    return <Shell><StateCard title="当前环境仍需 bootstrap" body="这轮桌面端第一版骨架不处理 bootstrap 流程，请先通过现有管理面完成初始化。" /></Shell>;
  }

  if (!currentUser) {
    return (
      <Shell>
        <div className="login-card">
          <p className="eyebrow">Desktop Console V1</p>
          <h1>统一桌面端骨架</h1>
          <p className="copy">这轮只接现有管理面认证和真实后端数据，不实现 desktop-console 正式打包，也不实现 P2P 数据面。</p>
          <form className="login-form" onSubmit={handleLogin}>
            <label>
              <span>邮箱</span>
              <input value={loginForm.email} onChange={(event) => setLoginForm((current) => ({ ...current, email: event.target.value }))} required />
            </label>
            <label>
              <span>密码</span>
              <input type="password" value={loginForm.password} onChange={(event) => setLoginForm((current) => ({ ...current, password: event.target.value }))} required />
            </label>
            <button type="submit" disabled={busy === "login"}>{busy === "login" ? "登录中..." : "进入桌面端骨架"}</button>
          </form>
          {error ? <div className="banner error">{error}</div> : null}
          {message ? <div className="banner info">{message}</div> : null}
        </div>
      </Shell>
    );
  }

  if (!mode) {
    return (
      <Shell>
        <div className="mode-grid">
          <ModeCard
            title="本机模式"
            description="围绕当前机器工作，进入后默认直达当前机器首页，适合作为 24h Windows 机器上的 local-node mode 基线。"
            onClick={() => setMode("local-node")}
          />
          <ModeCard
            title="运维模式"
            description="先进入机器列表，再选定机器进入同一套工作流。相比本机模式，这轮唯一新增入口就是选择机器。"
            onClick={() => setMode("operator")}
          />
        </div>
      </Shell>
    );
  }

  return (
    <Shell>
      <div className="desktop-shell">
        <aside className="left-rail">
          <div className="panel brand-panel">
            <p className="eyebrow">Desktop Console</p>
            <h1>{mode === "local-node" ? "本机模式" : "运维模式"}</h1>
            <p className="copy">统一桌面端第一版骨架：共用同一套主工作流，operator mode 只多一个机器选择入口。</p>
          </div>

          <div className="panel user-panel">
            <strong>{currentUser.displayName}</strong>
            <span className="muted-line">{currentUser.email}</span>
            <span className="status-chip neutral">{currentUser.role}</span>
            <span className="muted-line">自动刷新 10s{lastRefreshAt ? " / 上次成功 " + formatDate(lastRefreshAt) : " / 尚无成功刷新"}</span>
            <div className="button-row">
              <button className="secondary" type="button" onClick={() => void refreshConsoleData(true, "manual")} disabled={refreshing}>{refreshing ? "刷新中..." : "手动刷新"}</button>
              <button className="secondary" type="button" onClick={() => setMode(null)}>切换模式</button>
              <button className="secondary" type="button" onClick={() => void handleLogout()} disabled={busy === "logout"}>{busy === "logout" ? "退出中..." : "退出"}</button>
            </div>
          </div>

          {mode === "operator" ? (
            <div className="panel machine-panel">
              <div className="panel-head">
                <h2>机器列表</h2>
                <span>{nodes.length} 台</span>
              </div>
              <div className="machine-list">
                {nodes.map((node) => (
                  <button
                    key={node.nodeId}
                    type="button"
                    className={selectedNodeId === node.nodeId ? "machine-item active" : "machine-item"}
                    onClick={() => setSelectedNodeId(node.nodeId)}
                  >
                    <div className="machine-topline">
                      <strong>{node.nodeName}</strong>
                      <span className={statusClass(node.status)}>{node.status}</span>
                    </div>
                    <div className="machine-subline">{node.nodeId}</div>
                    <div className="machine-subline">{node.isolated ? "已隔离" : "未隔离"} / {nodeAgentDeploymentLabel(node)}</div>
                    <div className="machine-subline">{capabilitySummary(node.capabilities)}</div>
                    <div className="machine-subline">active {node.activeTunnels} / relay {node.runtimeSummary?.relayPathCount ?? 0} / p2p {node.runtimeSummary?.p2pPathCount ?? 0}</div>
                  </button>
                ))}
              </div>
            </div>
          ) : null}
        </aside>

        <main className="main-stage">
          {error ? <div className="banner error">{error}</div> : null}
          {message ? <div className="banner info">{message}</div> : null}

          {mode === "local-node" && !localBinding.node ? (
            <StateCard title="尚未完成本机绑定" body={localBinding.reason + " 本机模式这轮不再 fallback 到 third_party、cloud 或首个节点。请配置 VITE_DESKTOP_NODE_ID，或让当前账号下只保留唯一受管 local 节点。"} />
          ) : !selectedNode ? (
            <StateCard title="当前没有可展示的机器" body={mode === "local-node" ? "本机模式尚未选中绑定机器。" : "请在左侧机器列表中选择一台机器。"} />
          ) : (
            <>
              <section className="panel hero-panel">
                <div className="hero-head">
                  <div>
                    <p className="eyebrow">当前机器首页</p>
                    <h2>{selectedNode.nodeName}</h2>
                    <p className="copy">{selectedNode.nodeId}</p>
                  </div>
                  <div className="hero-chips">
                    <span className={statusClass(selectedNode.status)}>{selectedNode.status}</span>
                    <span className={selectedNode.isolated ? "status-chip danger" : "status-chip good"}>{selectedNode.isolated ? "已隔离" : "未隔离"}</span>
                    <span className="status-chip neutral">{selectedNode.agentVersion || "agent 未上报"}</span>
                    {mode === "local-node" ? <span className="status-chip neutral">绑定来源: {localBinding.sourceLabel}</span> : null}
                  </div>
                </div>
                <div className="hero-grid">
                  <Metric label="能力摘要" value={capabilitySummary(selectedNode.capabilities)} />
                  <Metric label="active tunnel" value={String(selectedNode.activeTunnels)} />
                  <Metric label="deployment" value={selectedNode.deploymentMode || "尚未上报"} />
                  <Metric label="service unit" value={selectedNode.serviceUnit || "尚未上报"} />
                  <Metric label="instance profile" value={selectedNode.instanceProfile || "尚未上报"} />
                  <Metric label="instance managed" value={selectedNode.instanceManaged ? "true" : "未托管"} />
                  <Metric label="relay path" value={String(selectedNode.runtimeSummary?.relayPathCount ?? 0)} />
                  <Metric label="p2p path" value={String(selectedNode.runtimeSummary?.p2pPathCount ?? 0)} />
                  <Metric label="pending" value={String(selectedNode.runtimeSummary?.pendingStateCount ?? 0)} />
                  <Metric label="unavailable" value={String(selectedNode.runtimeSummary?.unavailableStateCount ?? 0)} />
                  <Metric label="last seen" value={formatDate(selectedNode.lastSeenAt)} />
                  <Metric label="失败原因数" value={String(selectedNode.runtimeSummary?.failureReasonCount ?? 0)} />
                </div>
              </section>

              <section className="panel workbench-panel">
                <div className="panel-head">
                  <div>
                    <h2>按隧道类型切换的工作区</h2>
                    <p className="copy">这轮先把 TCP / UDP / HTTP / HTTPS / SOCKS5 的真实列表和当前选中项工作区搭起来，不做每个类型的深度定制交互。</p>
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
                    <div className="panel-head small">
                      <h3>{activeTab.toUpperCase()} 列表</h3>
                      <span>{tabTunnels.length} 条</span>
                    </div>
                    <div className="tunnel-list">
                      {tabTunnels.length === 0 ? <div className="empty-inline">当前机器没有 {activeTab.toUpperCase()} tunnel。</div> : tabTunnels.map((tunnel) => (
                        <button
                          key={tunnel.id}
                          type="button"
                          className={selectedTunnelId === tunnel.id ? "tunnel-item active" : "tunnel-item"}
                          onClick={() => setSelectedTunnelId(tunnel.id)}
                        >
                          <strong>{tunnel.name}</strong>
                          <span>{tunnel.id}</span>
                          <span>{tunnel.transportPolicy || "relay_only"}</span>
                          <span>{runtimeLabel(tunnel)}</span>
                        </button>
                      ))}
                    </div>
                  </div>

                  <div className="panel tunnel-detail-panel">
                    <div className="panel-head small">
                      <h3>当前隧道详情 / 编辑区</h3>
                    </div>
                    {!selectedTunnel ? (
                      <div className="empty-inline">请先在左侧选择一个 tunnel。当前类型工作区只做最小真实字段展示，不扩大量新表单逻辑。</div>
                    ) : (
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
  return (
    <div className="panel state-card">
      <h1>{title}</h1>
      <p className="copy">{body}</p>
    </div>
  );
}

function ModeCard({ title, description, onClick }: { title: string; description: string; onClick: () => void }) {
  return (
    <button type="button" className="panel mode-card" onClick={onClick}>
      <p className="eyebrow">模式入口</p>
      <h2>{title}</h2>
      <p className="copy">{description}</p>
    </button>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="metric-box">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function resolveLocalNodeBinding(nodes: NodeSummary[]) {
  if (desktopNodeId) {
    const matched = nodes.find((node) => node.nodeId === desktopNodeId) ?? null;
    if (matched) {
      return {
        node: matched,
        sourceLabel: "VITE_DESKTOP_NODE_ID",
        reason: "已通过 VITE_DESKTOP_NODE_ID 明确绑定到本机节点 " + desktopNodeId + "。",
      };
    }
    return {
      node: null,
      sourceLabel: "VITE_DESKTOP_NODE_ID",
      reason: "当前配置了 VITE_DESKTOP_NODE_ID=" + desktopNodeId + "，但当前节点列表中没有命中该 nodeId。",
    };
  }

  const managedLocalNodes = nodes.filter((node) => node.nodeRole === "local" && node.instanceManaged);
  if (managedLocalNodes.length === 1) {
    return {
      node: managedLocalNodes[0],
      sourceLabel: "唯一受管 local 节点",
      reason: "当前账号下只检测到一个受管 local 节点，已可确定性绑定。",
    };
  }
  if (managedLocalNodes.length === 0) {
    return {
      node: null,
      sourceLabel: "尚无绑定来源",
      reason: "没有检测到明确可绑定的受管 local 节点。",
    };
  }
  return {
    node: null,
    sourceLabel: "绑定不唯一",
    reason: "当前检测到 " + managedLocalNodes.length + " 个受管 local 节点，无法确定本机归属。",
  };
}

function capabilitySummary(capabilities: NodeCapabilities) {
  const active = [] as string[];
  if (capabilities.tcpRelay) active.push("TCP");
  if (capabilities.udpRelay) active.push("UDP");
  if (capabilities.httpRelay) active.push("HTTP");
  if (capabilities.httpsRelay || capabilities.httpRelay) active.push("HTTPS");
  if (capabilities.socks5Connect) active.push("SOCKS5");
  if (capabilities.p2pAssist) active.push("P2P assist");
  return active.length > 0 ? active.join(" / ") : "无能力上报";
}

function nodeAgentDeploymentLabel(node: NodeSummary) {
  const mode = (node.deploymentMode || "").trim();
  const unit = (node.serviceUnit || "").trim();
  const managed = Boolean(node.instanceManaged);
  if (mode === "managed" || managed) {
    return unit ? "受管运行 / " + unit : "受管运行 / service unit 尚未上报";
  }
  if (mode) {
    return mode + " / 未托管";
  }
  return "尚未上报 / 未托管";
}

function statusClass(status: string) {
  return status === "online" ? "status-chip good" : "status-chip danger";
}

function formatDate(value: string) {
  if (!value) return "-";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString();
}

function publicEntry(tunnel: TunnelSpec) {
  if (tunnel.type === "http" || tunnel.type === "https") {
    return tunnel.domain || "尚未配置域名";
  }
  if (tunnel.publicPort) {
    return String(tunnel.publicPort);
  }
  return "-";
}

function runtimeLabel(tunnel: TunnelSpec) {
  if (tunnel.runtimePath || tunnel.runtimeState) {
    return [tunnel.runtimePath || "尚无", tunnel.runtimeState || "尚无"].join(" / ");
  }
  return "尚无运行态上报";
}
