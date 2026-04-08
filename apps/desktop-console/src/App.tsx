import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { createDesktopApi } from "../../../packages/desktop-core/src/api";
import type {
  ControlActionOption,
  ControlActionRequest,
  ControlActionResponse,
  ControlPanelSummary,
  ControlSurface,
  NodeSummary,
  TunnelSpec,
  TunnelProbeResult,
  TunnelTypeTab,
  UserSummary,
} from "../../../packages/desktop-core/src/types";
import {
  capabilitySummary,
  checkStateLabel,
  checkStateTone,
  controlOptionStateLabel,
  controlOptionTone,
  formatDate,
  nodeAgentDeploymentLabel,
  publicEntry,
  resolveLocalNodeBinding,
  runtimeLabel,
  statusClass,
  tunnelTabs,
} from "../../../packages/desktop-core/src/utils";
import { buildDesktopControlResultView } from "./controlResultView";
import { ControlResultBlock } from "./controlResultBlock";

const api = createDesktopApi(import.meta.env.VITE_API_BASE_URL || "");
const desktopNodeId = (import.meta.env.VITE_DESKTOP_NODE_ID || "").trim();
const desktopPublicHost = (() => {
  const configured = (import.meta.env.VITE_PUBLIC_ENTRY_HOST || "").trim();
  if (configured) return configured;
  if (typeof window !== "undefined") {
    const hostname = window.location.hostname.trim();
    if (hostname) return hostname;
  }
  return "82.156.236.104";
})();

type DesktopMode = "local-node" | "operator";

type TunnelEditForm = {
  name: string;
  targetHost: string;
  targetPort: string;
  publicPort: string;
  domain: string;
  probePath: string;
  transportPolicy: string;
};

export default function App() {
  const [bootstrapRequired, setBootstrapRequired] = useState<boolean | null>(null);
  const [currentUser, setCurrentUser] = useState<UserSummary | null>(null);
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [tunnels, setTunnels] = useState<TunnelSpec[]>([]);
  const [mode, setMode] = useState<DesktopMode | null>(null);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<TunnelTypeTab>("tcp");
  const [selectedTunnelId, setSelectedTunnelId] = useState<string | null>(null);
  const [editForm, setEditForm] = useState<TunnelEditForm | null>(null);
  const [loginForm, setLoginForm] = useState({ email: "", password: "" });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [lastRefreshAt, setLastRefreshAt] = useState("");
  const [controlNote, setControlNote] = useState("");
  const [controlResult, setControlResult] = useState<ControlActionResponse | null>(null);
  const [probeResults, setProbeResults] = useState<Record<string, TunnelProbeResult>>({});
  const [nodeActionOptions, setNodeActionOptions] = useState<ControlActionOption[]>([]);
  const [tunnelActionOptions, setTunnelActionOptions] = useState<ControlActionOption[]>([]);
  const [nodeControlPanel, setNodeControlPanel] = useState<ControlPanelSummary | null>(null);
  const [tunnelControlPanel, setTunnelControlPanel] = useState<ControlPanelSummary | null>(null);
  const [nodeControlContextAt, setNodeControlContextAt] = useState("");
  const [tunnelControlContextAt, setTunnelControlContextAt] = useState("");
  const refreshInFlightRef = useRef(false);

  useEffect(() => {
    let cancelled = false;

    async function init() {
      try {
        const status = await api.loadBootstrapStatus();
        if (cancelled) return;
        setBootstrapRequired(status.required);
        if (status.required) {
          setError("当前仍需要先完成管理面 bootstrap，本轮桌面端不处理 bootstrap 流程。");
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
  const bindingTone = localBinding.status === "success" ? "good" : localBinding.status === "config_error" ? "danger" : localBinding.status === "ambiguous" ? "warn" : "neutral";
  const bindingStatusLabel = localBinding.status === "success" ? "成功" : localBinding.status === "config_error" ? "配置错误" : localBinding.status === "ambiguous" ? "不唯一" : "未完成";

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

  const activeSurface: ControlSurface = mode === "operator" ? "operator_console" : "node_console";
  const selectedNode = useMemo(() => {
    if (mode === "local-node") {
      return localBinding.node;
    }
    return selectedNodeId ? nodes.find((node) => node.nodeId === selectedNodeId) ?? null : null;
  }, [localBinding.node, mode, nodes, selectedNodeId]);

  const selectedNodeTunnels = useMemo(() => {
    if (!selectedNode) return [];
    return tunnels.filter((tunnel) => tunnel.nodeId === selectedNode.nodeId);
  }, [selectedNode, tunnels]);
  const protocolOverviewItems = useMemo(() => selectedNode ? buildProtocolOverview(selectedNode, selectedNodeTunnels) : [], [selectedNode, selectedNodeTunnels]);

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

  useEffect(() => {
    if (!selectedTunnel) {
      setEditForm(null);
      return;
    }
    setEditForm({
      name: selectedTunnel.name,
      targetHost: selectedTunnel.targetHost || "",
      targetPort: String(selectedTunnel.targetPort || ""),
      publicPort: String(selectedTunnel.publicPort || ""),
      domain: selectedTunnel.domain || "",
      probePath: selectedTunnel.probePath || "",
      transportPolicy: selectedTunnel.transportPolicy || "relay_only",
    });
  }, [selectedTunnel]);

  useEffect(() => {
    let cancelled = false;
    async function loadNodeControls() {
      if (!selectedNode || !currentUser) {
        setNodeActionOptions([]);
        setNodeControlPanel(null);
        return;
      }
      try {
        const [options, panel] = await Promise.all([
          api.loadNodeControlActionOptions(selectedNode.nodeId, activeSurface),
          api.loadNodeControlPanel(selectedNode.nodeId, activeSurface),
        ]);
        if (cancelled) return;
        setNodeActionOptions(options.items);
        setNodeControlPanel(panel);
        setNodeControlContextAt(panel.contextVersion || options.contextVersion || options.items[0]?.contextVersion || "");
      } catch (controlError) {
        if (!cancelled) {
          setNodeActionOptions([]);
          setNodeControlPanel(null);
          setError(controlError instanceof Error ? controlError.message : "读取节点控制摘要失败");
        }
      }
    }
    void loadNodeControls();
    return () => {
      cancelled = true;
    };
  }, [activeSurface, currentUser, selectedNode]);

  useEffect(() => {
    let cancelled = false;
    async function loadTunnelControls() {
      if (!selectedTunnel || !currentUser) {
        setTunnelActionOptions([]);
        setTunnelControlPanel(null);
        return;
      }
      try {
        const [options, panel] = await Promise.all([
          api.loadTunnelControlActionOptions(selectedTunnel.id, activeSurface),
          api.loadTunnelControlPanel(selectedTunnel.id, activeSurface),
        ]);
        if (cancelled) return;
        setTunnelActionOptions(options.items);
        setTunnelControlPanel(panel);
        setTunnelControlContextAt(panel.contextVersion || options.contextVersion || options.items[0]?.contextVersion || "");
      } catch (controlError) {
        if (!cancelled) {
          setTunnelActionOptions([]);
          setTunnelControlPanel(null);
          setError(controlError instanceof Error ? controlError.message : "读取 tunnel 控制摘要失败");
        }
      }
    }
    void loadTunnelControls();
    return () => {
      cancelled = true;
    };
  }, [activeSurface, currentUser, selectedTunnel]);

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
      setMessage("desktop-console 已接入当前管理面数据，可在本机模式和运维模式之间切换。");
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
      setMode(null);
      setNodes([]);
      setTunnels([]);
      setSelectedNodeId(null);
      setSelectedTunnelId(null);
      setNodeActionOptions([]);
      setTunnelActionOptions([]);
      setNodeControlPanel(null);
      setTunnelControlPanel(null);
      setControlResult(null);
      setControlNote("");
      setLastRefreshAt("");
      setMessage("已退出 desktop-console。");
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
        setMessage("desktop-console 数据已刷新，当前模式与控制上下文已尽量保留。");
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

  async function handleTunnelSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedTunnel || !selectedNode || !editForm) {
      return;
    }
    setBusy("save-tunnel");
    setError("");
    setMessage("");
    try {
      await api.updateTunnel(selectedTunnel.id, {
        id: selectedTunnel.id,
        nodeId: selectedNode.nodeId,
        name: editForm.name.trim(),
        type: selectedTunnel.type,
        status: selectedTunnel.status,
        targetHost: editForm.targetHost.trim(),
        targetPort: Number(editForm.targetPort),
        publicPort: Number(editForm.publicPort),
        domain: selectedTunnel.type === "http" || selectedTunnel.type === "https" ? editForm.domain.trim() : "",
        probePath: selectedTunnel.type === "http" || selectedTunnel.type === "https" ? editForm.probePath.trim() : "",
        transportPolicy: editForm.transportPolicy,
        tlsMode: selectedTunnel.tlsMode || "",
      });
      await refreshConsoleData(false, "manual");
      setMessage("当前 tunnel 的普通配置字段已提交，列表和详情已刷新。status 不会通过普通保存改变；active/paused 只能通过 pause_tunnel / resume_tunnel 控制动作切换。");
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "保存 tunnel 失败");
    } finally {
      setBusy("");
    }
  }

  async function runTunnelProbe(tunnel: TunnelSpec) {
    const busyKey = tunnel.id + ":probe";
    setBusy(busyKey);
    setError("");
    setMessage("");
    try {
      const result = await api.probeTunnel(tunnel.id);
      setProbeResults((current) => ({ ...current, [tunnel.id]: result }));
      setMessage(result.success ? "协议入口探测完成：当前入口可访问。" : "协议入口探测完成：当前入口不可访问。" );
      await refreshConsoleData(false, "manual");
    } catch (probeError) {
      setError(probeError instanceof Error ? probeError.message : "协议入口探测失败");
    } finally {
      setBusy("");
    }
  }

  async function copyToClipboard(value: string, copiedLabel: string) {
    try {
      if (!navigator?.clipboard?.writeText) {
        throw new Error("clipboard unavailable");
      }
      await navigator.clipboard.writeText(value);
      setMessage(copiedLabel + " 已复制到剪贴板。");
      setError("");
    } catch (copyError) {
      setError(copyError instanceof Error ? copyError.message : "复制失败");
    }
  }

  function openExternal(url: string, label: string) {
    try {
      const opened = window.open(url, "_blank", "noopener,noreferrer");
      if (!opened) {
        throw new Error("窗口被拦截，请允许当前桌面页打开新窗口。")
      }
      setMessage(label + " 已在新窗口打开。");
      setError("");
    } catch (openError) {
      setError(openError instanceof Error ? openError.message : "打开入口失败");
    }
  }

  function currentControlContextAt(targetKind: ControlActionRequest["targetKind"]) {
    if (targetKind === "tunnel") {
      return tunnelControlPanel?.contextVersion || tunnelActionOptions[0]?.contextVersion || tunnelControlContextAt || undefined;
    }
    return nodeControlPanel?.contextVersion || nodeActionOptions[0]?.contextVersion || nodeControlContextAt || undefined;
  }

  async function runControlAction(actionKind: ControlActionRequest["actionKind"], targetKind: ControlActionRequest["targetKind"], targetId: string, dryRun: boolean) {
    setBusy("control-action");
    setError("");
    setMessage("");
    try {
      const result = await api.controlAction({
        actionKind,
        targetKind,
        targetId,
        sourceSurface: activeSurface,
        dryRun,
        note: controlNote.trim(),
        requestedAt: currentControlContextAt(targetKind),
      });
      setControlResult(result);
      setMessage(result.humanMessage);
      await refreshConsoleData(false, "manual");
    } catch (controlError) {
      setError(controlError instanceof Error ? controlError.message : "控制动作请求失败");
    } finally {
      setBusy("");
    }
  }

  if (bootstrapRequired === null) {
    if (error) {
      return <Shell><StateCard title="桌面端骨架初始化失败" body={error} /></Shell>;
    }
    return <Shell><StateCard title="桌面端骨架初始化中" body="正在读取现有管理面认证状态和后端基础数据。" /></Shell>;
  }

  if (bootstrapRequired) {
    return <Shell><StateCard title="当前环境仍需 bootstrap" body="这轮桌面端不处理 bootstrap 流程，请先通过现有管理面完成初始化。" /></Shell>;
  }

  if (!currentUser) {
    return (
      <Shell>
        <div className="login-card">
          <p className="eyebrow">Desktop Console V1</p>
          <h1>统一桌面控制台</h1>
          <p className="copy">本轮接入与 node/operator 一致的 control-v1 合同，包括 contextVersion、稳定 executeOutcome 分类和真实刷新闭环。</p>
          <form className="login-form" onSubmit={handleLogin}>
            <label>
              <span>邮箱</span>
              <input value={loginForm.email} onChange={(event) => setLoginForm((current) => ({ ...current, email: event.target.value }))} required />
            </label>
            <label>
              <span>密码</span>
              <input type="password" value={loginForm.password} onChange={(event) => setLoginForm((current) => ({ ...current, password: event.target.value }))} required />
            </label>
            <button type="submit" disabled={busy === "login"}>{busy === "login" ? "登录中..." : "进入 desktop-console"}</button>
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
            description="映射到 node_console 语义：围绕当前机器和本机隧道工作，使用同一套 contextVersion / executeOutcome 合同。"
            onClick={() => setMode("local-node")}
          />
          <ModeCard
            title="运维模式"
            description="映射到 operator_console 语义：先选机器，再对节点和隧道执行同一套控制动作，不再形成独立合同。"
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
            <p className="copy">统一桌面控制台：本机模式使用 node-console 语义，运维模式使用 operator-console 语义，控制结果分类完全由稳定机器字段驱动。</p>
          </div>

          <div className="panel user-panel">
            <strong>{currentUser.displayName}</strong>
            <span className="muted-line">{currentUser.email}</span>
            <span className="status-chip neutral">{currentUser.role}</span>
            <span className="muted-line">surface: {activeSurface} / 自动刷新 10s{lastRefreshAt ? " / 上次成功 " + formatDate(lastRefreshAt) : " / 尚无成功刷新"}</span>
            <div className="button-row">
              <button className="secondary" type="button" onClick={() => void refreshConsoleData(true, "manual")} disabled={refreshing}>{refreshing ? "刷新中..." : "手动刷新"}</button>
              <button className="secondary" type="button" onClick={() => setMode(null)}>切换模式</button>
              <button className="secondary" type="button" onClick={() => void handleLogout()} disabled={busy === "logout"}>{busy === "logout" ? "退出中..." : "退出"}</button>
            </div>
          </div>

          {mode === "local-node" ? (
            <div className="panel binding-panel">
              <div className="panel-head small">
                <h2>本机绑定状态</h2>
                <span className={"status-chip " + bindingTone}>{bindingStatusLabel}</span>
              </div>
              <div className="detail-stack binding-grid">
                <Metric label="绑定来源" value={localBinding.sourceLabel} />
                <Metric label="当前配置 nodeId" value={desktopNodeId || "未配置"} />
                <Metric label="当前受管 local 节点数" value={String(nodes.filter((node) => node.nodeRole === "local" && node.instanceManaged).length)} />
                <Metric label="是否命中绑定节点" value={localBinding.node ? localBinding.node.nodeId : "未命中"} />
              </div>
              <div className="banner info binding-note">绑定原因：{localBinding.reason}</div>
              <div className="banner info binding-note">下一步建议：{localBinding.nextAction}</div>
            </div>
          ) : (
            <div className="panel machine-panel">
              <div className="panel-head small">
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
          )}
        </aside>

        <main className="main-stage">
          {error ? <div className="banner error">{error}</div> : null}
          {message ? <div className="banner info">{message}</div> : null}

          {mode === "local-node" && !localBinding.node ? (
            <StateCard title="尚未完成本机绑定" body={localBinding.reason + " " + localBinding.nextAction + " 本机模式不会 fallback 到 operator 机器列表。"} />
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
                    <span className="status-chip neutral">surface: {activeSurface}</span>
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

              <section className="panel protocol-panel">
                <div className="panel-head small">
                  <div>
                    <h2>协议入口总览</h2>
                    <p className="copy">把当前机器上 HTTP / HTTPS / UDP / SOCKS5 / TCP / P2P 的可用、空、需关注、partial 状态直接收敛成桌面入口判断，不再只在 tunnel 详情里被动拼接。</p>
                  </div>
                </div>
                <div className="protocol-grid">
                  {protocolOverviewItems.map((item) => (
                    <div key={item.key} className="check-item">
                      <div className="check-head">
                        <strong>{item.label}</strong>
                        <span className={"status-chip " + item.tone}>{item.state}</span>
                      </div>
                      <p className="copy">{item.summary}</p>
                      <p className="weak-note">{item.detail}</p>
                      {item.nextStep ? <p className="weak-note">下一步：{item.nextStep}</p> : null}
                    </div>
                  ))}
                </div>
              </section>

              <section className="panel preflight-panel">
                <div className="panel-head small">
                  <div>
                    <h2>节点控制区</h2>
                    <p className="copy">panel、options、dry-run、execute 都走与 node/operator 一致的 control-v1 主链，并使用同一个 contextVersion/requestedAt 合同。</p>
                  </div>
                </div>
                {nodeControlPanel ? <div className="banner info">{nodeControlPanel.headline} {nodeControlPanel.summary} 下一步：{nodeControlPanel.nextStep}</div> : null}
                <div className="check-list">
                  {(nodeControlPanel?.checks || []).map((item) => (
                    <div key={item.code} className="check-item">
                      <div className="check-head">
                        <strong>{item.label}</strong>
                        <span className={"status-chip " + checkStateTone(item.state)}>{checkStateLabel(item.state)}</span>
                      </div>
                      <p className="copy">{item.message}</p>
                    </div>
                  ))}
                </div>
                <label className="control-note-field">
                  <span>控制动作备注</span>
                  <textarea value={controlNote} onChange={(event) => setControlNote(event.target.value)} placeholder="可选：记录为什么要做这次控制动作" />
                </label>
                <div className="check-list compact-check-list">
                  {nodeActionOptions.map((option) => (
                    <div key={option.actionKind} className="check-item">
                      <div className="check-head">
                        <strong>{option.label}</strong>
                        <span className={"status-chip " + controlOptionTone(option)}>{controlOptionStateLabel(option)}</span>
                      </div>
                      {nodeControlPanel?.recommendedAction === option.actionKind ? <p className="copy">推荐动作</p> : null}
                      <p className="copy">{option.message}</p>
                      {option.summary ? <p className="copy">{option.summary}</p> : null}
                      {option.nextStep ? <p className="copy">下一步：{option.nextStep}</p> : null}
                      <p className="copy">executionMode: <code>{option.executionMode}</code> / placeholderOnly: <code>{String(Boolean(option.placeholderOnly))}</code> / contextVersion: <code>{option.contextVersion || nodeControlPanel?.contextVersion || nodeControlContextAt || "-"}</code></p>
                      <div className="button-row wrap-actions">
                        <button className="secondary" type="button" disabled={!option.available || busy === "control-action"} onClick={() => void runControlAction(option.actionKind, option.targetKind, option.targetId, true)}>{busy === "control-action" ? "处理中..." : "预检 " + option.label}</button>
                        <button className="secondary" type="button" disabled={!option.available || busy === "control-action"} onClick={() => void runControlAction(option.actionKind, option.targetKind, option.targetId, false)}>{busy === "control-action" ? "处理中..." : (option.placeholderOnly ? "占位执行 " : "执行 ") + option.label}</button>
                      </div>
                    </div>
                  ))}
                </div>
              </section>

              <section className="panel workbench-panel">
                <div className="panel-head">
                  <div>
                    <h2>按隧道类型切换的工作区</h2>
                    <p className="copy">当前选中隧道的详情、普通配置编辑和 tunnel control panel/options/result 全部在同一个工作区里闭环；status 切换只能走控制动作。</p>
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
                      <h3>当前隧道详情 / 控制区</h3>
                    </div>
                    {!selectedTunnel ? (
                      <WorkbenchEmptyState activeTab={activeTab} selectedNode={selectedNode} />
                    ) : (
                      <div className="detail-column">
                        {tunnelStateEvaluation(selectedTunnel).messages.map((item) => (
                          <div key={item.message} className={item.tone === "danger" ? "banner error" : "banner info"}>{item.message}</div>
                        ))}
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

                        <div className="panel access-panel">
                          <div className="panel-head small">
                            <div>
                              <h3>Tunnel Access Workbench</h3>
                              <p className="copy">基于当前已验证可用的 HTTP / HTTPS / UDP / SOCKS5 能力，给桌面端一个可直接操作的协议入口工作区。P2P 当前只做降级表达，不阻塞主线。</p>
                            </div>
                          </div>
                          <div className="detail-stack access-grid">
                            <Metric label="用户入口" value={tunnelPublicEntry(selectedTunnel)} />
                            <Metric label="入口语义" value={tunnelAccessLabel(selectedTunnel)} />
                            <Metric label="运行事实" value={runtimeFactSummary(selectedTunnel)} />
                            <Metric label="最近 probe" value={probeFreshnessLabel(selectedTunnel, probeResults[selectedTunnel.id])} />
                          </div>
                          <div className="badge-row access-badges">
                            {tunnelStateEvaluation(selectedTunnel).badges.map((badge) => (
                              <span key={badge.label} className={"status-chip " + badge.tone}>{badge.label}</span>
                            ))}
                          </div>
                          <div className="banner info">{tunnelAccessGuide(selectedTunnel)}</div>
                          {tunnelStateEvaluation(selectedTunnel).nextStep ? <div className="banner info">下一步：{tunnelStateEvaluation(selectedTunnel).nextStep}</div> : null}
                          <div className="button-row wrap-actions">
                            <button type="button" className="secondary" disabled={!supportsCopyEntry(selectedTunnel)} onClick={() => void copyToClipboard(tunnelPublicEntry(selectedTunnel), "用户入口")}>复制用户入口</button>
                            <button type="button" className="secondary" disabled={!supportsQuickCommand(selectedTunnel)} onClick={() => void copyToClipboard(tunnelQuickCommand(selectedTunnel), "协议示例命令")}>复制协议示例命令</button>
                            <button type="button" className="secondary" disabled={!supportsOpenEntry(selectedTunnel)} onClick={() => openExternal(tunnelPublicEntry(selectedTunnel), "用户入口")}>打开用户入口</button>
                            <button type="button" className="secondary" disabled={!supportsProbeTargetOpen(selectedTunnel)} onClick={() => openExternal(tunnelProbeTargetEntry(selectedTunnel, probeResults[selectedTunnel.id]), "Probe 目标")}>打开 probe 目标</button>
                          </div>
                          <div className="access-command-box">
                            <span>协议示例命令</span>
                            <code>{tunnelQuickCommand(selectedTunnel)}</code>
                          </div>
                          {(selectedTunnel.type === "http" || selectedTunnel.type === "https") ? (
                            <div className="button-row wrap-actions">
                              <button type="button" className="secondary" disabled={!supportsProbeAction(selectedTunnel) || busy === selectedTunnel.id + ":probe"} onClick={() => void runTunnelProbe(selectedTunnel)}>
                                {busy === selectedTunnel.id + ":probe" ? "探测中..." : "探测当前入口"}
                              </button>
                              <span className="weak-note">probePath: <code>{normalizeProbePath(selectedTunnel.probePath)}</code></span>
                            </div>
                          ) : null}
                          {renderProbeSummary(selectedTunnel, probeResults[selectedTunnel.id])}
                          <div className="weak-note runtime-note">{runtimeFactGuide(selectedTunnel)}</div>
                          {selectedTunnel.transportPolicy === "p2p_preferred" ? <div className="banner info">P2P 当前仍为降级占位：配置意图可见，但当前不作为桌面数据面前提，也不表达成已建立 P2P 路径。</div> : null}
                        </div>

                        <form className="edit-form" onSubmit={handleTunnelSave}>
                          <div className="panel-head small"><h3>最小编辑入口</h3></div>
                          <label><span>tunnel 名称</span><input value={editForm?.name || ""} onChange={(event) => setEditForm((current) => current ? { ...current, name: event.target.value } : current)} /></label>
                          <label><span>targetHost</span><input value={editForm?.targetHost || ""} onChange={(event) => setEditForm((current) => current ? { ...current, targetHost: event.target.value } : current)} /></label>
                          <label><span>targetPort</span><input value={editForm?.targetPort || ""} onChange={(event) => setEditForm((current) => current ? { ...current, targetPort: event.target.value } : current)} /></label>
                          <label><span>publicPort</span><input value={editForm?.publicPort || ""} onChange={(event) => setEditForm((current) => current ? { ...current, publicPort: event.target.value } : current)} /></label>
                          {(selectedTunnel.type === "http" || selectedTunnel.type === "https") ? <label><span>domain</span><input value={editForm?.domain || ""} onChange={(event) => setEditForm((current) => current ? { ...current, domain: event.target.value } : current)} /></label> : <div className="weak-note">当前类型不适用 domain。</div>}
                          {(selectedTunnel.type === "http" || selectedTunnel.type === "https") ? <label><span>probePath</span><input value={editForm?.probePath || ""} onChange={(event) => setEditForm((current) => current ? { ...current, probePath: event.target.value } : current)} /></label> : <div className="weak-note">当前类型不适用 probePath。</div>}
                          <label><span>transportPolicy</span><select value={editForm?.transportPolicy || "relay_only"} onChange={(event) => setEditForm((current) => current ? { ...current, transportPolicy: event.target.value } : current)}><option value="relay_only">relay_only</option><option value="p2p_preferred">p2p_preferred</option></select></label>
                          <div className="weak-note">transportPolicy 和目标配置只代表普通配置编辑。当前 status={selectedTunnel.status} 只能通过下方 pause_tunnel / resume_tunnel 控制动作改变，不会经由普通保存提交。</div>

                          {tunnelControlPanel ? <div className="banner info">{tunnelControlPanel.headline} {tunnelControlPanel.summary} 下一步：{tunnelControlPanel.nextStep}</div> : null}
                          <div className="check-list compact-check-list">
                            {(tunnelControlPanel?.checks || []).map((item) => (
                              <div key={item.code} className="check-item">
                                <div className="check-head">
                                  <strong>{item.label}</strong>
                                  <span className={"status-chip " + checkStateTone(item.state)}>{checkStateLabel(item.state)}</span>
                                </div>
                                <p className="copy">{item.message}</p>
                              </div>
                            ))}
                          </div>
                          <div className="check-list compact-check-list">
                            {tunnelActionOptions.map((option) => (
                              <div key={option.actionKind} className="check-item">
                                <div className="check-head">
                                  <strong>{option.label}</strong>
                                  <span className={"status-chip " + controlOptionTone(option)}>{controlOptionStateLabel(option)}</span>
                                </div>
                                {tunnelControlPanel?.recommendedAction === option.actionKind ? <p className="copy">推荐动作</p> : null}
                                <p className="copy">{option.message}</p>
                                {option.summary ? <p className="copy">{option.summary}</p> : null}
                                {option.nextStep ? <p className="copy">下一步：{option.nextStep}</p> : null}
                                <p className="copy">executionMode: <code>{option.executionMode}</code> / placeholderOnly: <code>{String(Boolean(option.placeholderOnly))}</code> / contextVersion: <code>{option.contextVersion || tunnelControlPanel?.contextVersion || tunnelControlContextAt || "-"}</code></p>
                                <div className="button-row wrap-actions">
                                  <button className="secondary" type="button" disabled={!option.available || busy === "control-action"} onClick={() => void runControlAction(option.actionKind, option.targetKind, option.targetId, true)}>{busy === "control-action" ? "处理中..." : "预检 " + option.label}</button>
                                  <button className="secondary" type="button" disabled={!option.available || busy === "control-action"} onClick={() => void runControlAction(option.actionKind, option.targetKind, option.targetId, false)}>{busy === "control-action" ? "处理中..." : (option.placeholderOnly ? "占位执行 " : "执行 ") + option.label}</button>
                                </div>
                              </div>
                            ))}
                          </div>
                          <button type="submit" disabled={busy === "save-tunnel"}>{busy === "save-tunnel" ? "保存中..." : "保存当前 tunnel"}</button>
                        </form>
                      </div>
                    )}
                  </div>
                </div>
              </section>

              {controlResult ? <ControlResultBlock result={controlResult} /> : null}
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

function WorkbenchEmptyState({ activeTab, selectedNode }: { activeTab: TunnelTypeTab; selectedNode: NodeSummary }) {
  const info = emptyStateForProtocol(activeTab, selectedNode);
  return (
    <div className="check-item empty-workbench-state">
      <div className="check-head">
        <strong>{info.title}</strong>
        <span className={"status-chip " + info.tone}>{info.state}</span>
      </div>
      <p className="copy">{info.summary}</p>
      <p className="weak-note">{info.detail}</p>
      {info.nextStep ? <p className="weak-note">下一步：{info.nextStep}</p> : null}
    </div>
  );
}

function tunnelPublicEntry(tunnel: TunnelSpec) {
  if (tunnel.type === "http") {
    return `http://${desktopPublicHost}:${tunnel.publicPort}`;
  }
  if (tunnel.type === "https") {
    return tunnel.domain ? `https://${tunnel.domain}` : "https://<待绑定域名>";
  }
  if (tunnel.type === "udp") {
    return `udp://${desktopPublicHost}:${tunnel.publicPort}`;
  }
  if (tunnel.type === "socks5") {
    return `socks5://${desktopPublicHost}:${tunnel.publicPort}`;
  }
  return `${desktopPublicHost}:${tunnel.publicPort}`;
}

function tunnelAccessLabel(tunnel: TunnelSpec) {
  if (tunnel.type === "http") return "HTTP Web/API 入口";
  if (tunnel.type === "https") return "HTTPS 标准入口";
  if (tunnel.type === "udp") return "UDP 最小数据面入口";
  if (tunnel.type === "socks5") return "SOCKS5 CONNECT 代理入口";
  return "TCP 端口直连入口";
}

function tunnelAccessGuide(tunnel: TunnelSpec) {
  const state = tunnelStateEvaluation(tunnel);
  if (tunnel.type === "http") {
    if (!state.entryUsable && !state.domainReady) {
      return `HTTP 当前入口配置不完整，暂时不能作为桌面入口使用。`;
    }
    if (!state.entryUsable) {
      return `HTTP 当前处于不可直接使用状态，入口动作已按产品交互降级。`;
    }
    return `HTTP 已可作为当前桌面开发前提。建议先访问 ${tunnelPublicEntry(tunnel)}${normalizeProbePath(tunnel.probePath) === "/" ? "" : normalizeProbePath(tunnel.probePath)}，再按需执行 probe。`;
  }
  if (tunnel.type === "https") {
    if (!state.domainReady) {
      return `HTTPS 当前还不能直接打开，因为还没有配置 domain。`;
    }
    if (!state.entryUsable) {
      return `HTTPS 当前处于不可直接使用状态，入口动作已按产品交互降级。`;
    }
    return `HTTPS 已可作为当前桌面开发前提。标准入口使用域名 ${tunnel.domain || "<待绑定域名>"}，当前仍需环境侧证书与域名配置配合。`;
  }
  if (tunnel.type === "udp") {
    if (!state.entryUsable) {
      return `UDP 当前处于不可直接使用状态，入口命令仅保留为参考。`;
    }
    return `UDP 已可作为当前桌面开发前提。当前只确认最小数据面闭环，不提供 UDP probe、复杂会话治理或 NAT 穿透。`;
  }
  if (tunnel.type === "socks5") {
    if (!state.entryUsable) {
      return `SOCKS5 当前处于不可直接使用状态，入口动作已按产品交互降级。`;
    }
    return `SOCKS5 已可作为当前桌面开发前提。当前只支持 CONNECT，不支持 UDP associate，也不提供高级认证或 ACL。`;
  }
  if (tunnel.transportPolicy === "p2p_preferred") {
    return `当前 tunnel 配置偏好为 p2p_preferred，但 P2P 仍是 partial 能力；桌面端只展示配置意图和运行事实，不把它当作已可用的数据面。`;
  }
  return `TCP 端口映射当前可直接使用。适合 SSH / RDP / 数据库等原始 TCP 服务。`;
}

function tunnelQuickCommand(tunnel: TunnelSpec) {
  const entry = tunnelPublicEntry(tunnel);
  if (tunnel.type === "http") {
    const path = normalizeProbePath(tunnel.probePath);
    return `curl ${entry}${path === "/" ? "" : path}`;
  }
  if (tunnel.type === "https") {
    const path = normalizeProbePath(tunnel.probePath);
    return `curl ${entry}${path === "/" ? "" : path}`;
  }
  if (tunnel.type === "udp") {
    return `echo -n "ping" | nc -u ${desktopPublicHost} ${tunnel.publicPort}`;
  }
  if (tunnel.type === "socks5") {
    return `curl --proxy ${entry} https://example.com -I`;
  }
  return `nc ${desktopPublicHost} ${tunnel.publicPort}`;
}

function supportsCopyEntry(tunnel: TunnelSpec) {
  return hasUsableEntry(tunnel);
}

function supportsQuickCommand(tunnel: TunnelSpec) {
  return hasUsableEntry(tunnel);
}

function supportsOpenEntry(tunnel: TunnelSpec) {
  return (tunnel.type === "http" || tunnel.type === "https") && tunnelStateEvaluation(tunnel).entryUsable;
}

function supportsProbeTargetOpen(tunnel: TunnelSpec) {
  return (tunnel.type === "http" || tunnel.type === "https") && tunnelStateEvaluation(tunnel).entryUsable;
}

function supportsProbeAction(tunnel: TunnelSpec) {
  return (tunnel.type === "http" || tunnel.type === "https") && tunnelStateEvaluation(tunnel).entryUsable;
}

function hasUsableEntry(tunnel: TunnelSpec) {
  return tunnelStateEvaluation(tunnel).entryUsable;
}

function tunnelProbeTargetEntry(tunnel: TunnelSpec, probe: TunnelProbeResult | null | undefined) {
  if (probe?.targetEntry) {
    return probe.targetEntry;
  }
  const entry = tunnelPublicEntry(tunnel);
  const path = normalizeProbePath(tunnel.probePath);
  return path === "/" ? entry + "/" : entry + path;
}

function runtimeFactSummary(tunnel: TunnelSpec) {
  const path = tunnel.runtimePath || "尚无路径上报";
  const state = tunnel.runtimeState || "尚无状态上报";
  return `${path} / ${state}`;
}

function buildProtocolOverview(node: NodeSummary, tunnels: TunnelSpec[]) {
  return [
    protocolOverviewItem("tcp", "TCP", Boolean(node.capabilities.tcpRelay), tunnels.filter((item) => item.type === "tcp")),
    protocolOverviewItem("http", "HTTP", Boolean(node.capabilities.httpRelay), tunnels.filter((item) => item.type === "http")),
    protocolOverviewItem("https", "HTTPS", Boolean(node.capabilities.httpsRelay || node.capabilities.httpRelay), tunnels.filter((item) => item.type === "https")),
    protocolOverviewItem("udp", "UDP", Boolean(node.capabilities.udpRelay), tunnels.filter((item) => item.type === "udp")),
    protocolOverviewItem("socks5", "SOCKS5", Boolean(node.capabilities.socks5Connect), tunnels.filter((item) => item.type === "socks5")),
    p2pOverviewItem(node, tunnels),
  ];
}

function protocolOverviewItem(key: string, label: string, supported: boolean, tunnels: TunnelSpec[]) {
  const states = tunnels.map((item) => tunnelStateEvaluation(item));
  const activeCount = states.filter((item) => item.active).length;
  const attentionCount = states.filter((item) => item.attention).length;
  if (!supported) {
    return { key, label, tone: "danger", state: "不可用", summary: "当前节点未上报对应能力。", detail: "当前协议不应作为这台机器的桌面入口前提。", nextStep: "继续使用其它已可用协议，不要让这个能力阻塞桌面主路径。" };
  }
  if (tunnels.length === 0) {
    return { key, label, tone: "neutral", state: "空", summary: "当前节点还没有对应 tunnel。", detail: "这不阻塞其它已可用协议；如需接入该协议，可后续补 tunnel。", nextStep: "如需使用这一协议，先补 tunnel，而不是中断当前桌面主线。" };
  }
  if (attentionCount > 0) {
    return { key, label, tone: "danger", state: "需关注", summary: `${tunnels.length} 条 tunnel，${attentionCount} 条处于失败、非 active 或配置不完整状态。`, detail: `当前 active=${activeCount}。桌面工作区会按不可用态禁用入口动作。`, nextStep: "优先处理 active/domain/failure 等阻塞条件，再回到入口操作。" };
  }
  return { key, label, tone: "good", state: "可用", summary: `${tunnels.length} 条 tunnel 已可作为当前桌面入口区前提。`, detail: `当前 active=${activeCount}，可直接进入当前协议的入口操作路径。`, nextStep: "直接进入 tunnel workbench，使用 copy/open/probe 等入口动作。" };
}

function p2pOverviewItem(node: NodeSummary, tunnels: TunnelSpec[]) {
  const preferredCount = tunnels.filter((item) => item.transportPolicy === "p2p_preferred").length;
  if (!node.capabilities.p2pAssist && preferredCount === 0) {
    return { key: "p2p", label: "P2P", tone: "neutral", state: "未启用", summary: "当前节点没有 P2P assist，也没有 p2p_preferred tunnel。", detail: "桌面主路径继续建立在 HTTP/HTTPS/UDP/SOCKS5 上。", nextStep: "当前无需为 P2P 停下主线开发。" };
  }
  return { key: "p2p", label: "P2P", tone: "neutral", state: "Partial", summary: `当前只展示配置意图与能力可见性；p2p_preferred tunnel=${preferredCount}。`, detail: "P2P 仍不是当前桌面数据面前提，不提供 live data-plane 入口动作。", nextStep: "继续沿 HTTP/HTTPS/UDP/SOCKS5 主路径做桌面产品，不等待 P2P。" };
}

function emptyStateForProtocol(activeTab: TunnelTypeTab, node: NodeSummary) {
  const label = activeTab.toUpperCase();
  const support = activeTab === "tcp" ? Boolean(node.capabilities.tcpRelay)
    : activeTab === "udp" ? Boolean(node.capabilities.udpRelay)
      : activeTab === "http" ? Boolean(node.capabilities.httpRelay)
        : activeTab === "https" ? Boolean(node.capabilities.httpsRelay || node.capabilities.httpRelay)
          : Boolean(node.capabilities.socks5Connect);
  if (!support) {
    return {
      title: `${label} 当前不可用`,
      state: "不可用",
      tone: "danger",
      summary: `当前节点还没有上报 ${label} 对应能力，当前协议不应作为这台机器的桌面入口主路径。`,
      detail: "可以继续使用其它已可用协议，不需要等待这个协议补齐后再继续桌面开发。",
      nextStep: "切到其它已可用协议，或先补节点能力再回到这里。",
    };
  }
  return {
    title: `${label} 当前为空`,
    state: "空",
    tone: "neutral",
    summary: `当前机器还没有 ${label} tunnel，因此 workbench 暂无可操作入口。`,
    detail: activeTab === "https"
      ? "如果后续要使用 HTTPS，先补 tunnel 和 domain；P2P 仍不阻塞这条主路径。"
      : activeTab === "udp"
        ? "UDP 当前已验证可用，但这台机器还没有对应 tunnel。"
        : activeTab === "socks5"
          ? "SOCKS5 当前已验证可用，但这台机器还没有对应 tunnel。"
          : "当前协议能力并不缺失，只是还没有对应 tunnel。",
    nextStep: "如需这个协议，先补 tunnel；否则继续沿当前已可用协议推进桌面主路径。",
  };
}

function runtimeFactGuide(tunnel: TunnelSpec) {
  const state = tunnelStateEvaluation(tunnel);
  const failure = tunnel.lastFailureReason || "尚无失败原因";
  if (!state.active) {
    return `当前 tunnel 状态是 ${tunnel.status}。在非 active 状态下，打开入口和 probe 等动作会被降级或禁用；lastFailureReason=${failure}。`;
  }
  if (tunnel.runtimePath || tunnel.runtimeState) {
    return `当前运行事实来自 tunnel runtime 字段：runtimePath=${tunnel.runtimePath || "-"} / runtimeState=${tunnel.runtimeState || "-"} / lastFailureReason=${failure}。`;
  }
  return `当前还没有 runtimePath/runtimeState 回传。桌面端不把 transportPolicy 误当作运行事实；lastFailureReason=${failure}。`;
}

function tunnelStateEvaluation(tunnel: TunnelSpec) {
  const domainReady = tunnel.type !== "https" || Boolean((tunnel.domain || "").trim());
  const active = tunnel.status === "active";
  const hasPortEntry = tunnel.type === "http" || tunnel.type === "udp" || tunnel.type === "socks5" || tunnel.type === "tcp";
  const entryConfigured = tunnel.type === "https" ? domainReady : !hasPortEntry || tunnel.publicPort > 0;
  const runtimeUnavailable = tunnel.runtimeState === "unavailable";
  const hasFailure = Boolean(tunnel.lastFailureReason);
  const entryUsable = active && entryConfigured;
  const attention = !active || !entryConfigured || runtimeUnavailable || hasFailure;
  const badges = [
    {
      label: runtimeUnavailable ? "运行不可用" : tunnel.runtimeState === "pending" ? "运行待定" : tunnel.runtimeState === "active" ? `运行中 ${tunnel.runtimePath || "reported"}` : "运行事实未上报",
      tone: runtimeUnavailable ? "danger" : tunnel.runtimeState === "active" ? "good" : "neutral",
    },
    {
      label: !tunnel.healthStatus ? "health 未上报" : `health ${tunnel.healthStatus}`,
      tone: !tunnel.healthStatus || tunnel.healthStatus === "healthy" ? "good" : "danger",
    },
    {
      label: `transportPolicy ${tunnel.transportPolicy || "relay_only"}`,
      tone: "neutral",
    },
  ];
  const messages = [] as Array<{ tone: "danger" | "info"; message: string }>;
  if (!active) {
    messages.push({ tone: "danger", message: `当前 tunnel 处于非 active 状态，入口打开、示例命令和 probe 已按产品交互降级或禁用。请先通过控制动作恢复状态。` });
  }
  if (hasFailure) {
    messages.push({ tone: "danger", message: `最近失败原因：${tunnel.lastFailureReason}` });
  }
  if (tunnel.type === "https" && !domainReady) {
    messages.push({ tone: "info", message: "HTTPS 仍缺少 domain，打开入口和 probe 目标会继续保持禁用，直到补齐域名。" });
  }
  let nextStep = "直接使用当前入口动作，继续验证或访问当前 tunnel。";
  if (!active) {
    nextStep = "先通过 pause_tunnel / resume_tunnel 控制动作把 tunnel 恢复到 active。";
  } else if (tunnel.type === "https" && !domainReady) {
    nextStep = "先在普通配置区补齐 domain，再使用打开入口或 probe。";
  } else if (runtimeUnavailable) {
    nextStep = "先结合 runtimeState 和 lastFailureReason 排查，再决定是否继续访问当前入口。";
  } else if (hasFailure) {
    nextStep = "先处理最近失败原因，再决定是否继续访问或重新探测当前入口。";
  }
  return {
    active,
    domainReady,
    entryUsable,
    attention,
    badges,
    messages,
    nextStep,
  };
}

function normalizeProbePath(pathValue?: string) {
  const value = (pathValue || "/").trim();
  if (!value) return "/";
  return value.startsWith("/") ? value : "/" + value;
}

function probeFreshnessLabel(tunnel: TunnelSpec, probe: TunnelProbeResult | null | undefined) {
  const probedAt = probe?.probedAt || tunnel.lastProbedAt || "";
  if (!probedAt || probedAt.startsWith("0001-01-01")) return "尚未探测";
  const parsed = Date.parse(probedAt);
  if (Number.isNaN(parsed)) return "探测结果时间未知";
  const success = probe?.success ?? Boolean(tunnel.lastProbeSuccess);
  return success ? `最近成功 / ${formatDate(probedAt)}` : `最近失败 / ${formatDate(probedAt)}`;
}

function renderProbeSummary(tunnel: TunnelSpec, probe: TunnelProbeResult | null | undefined) {
  if (tunnel.type !== "http" && tunnel.type !== "https") {
    return null;
  }
  const result = probe || (tunnel.lastProbedAt ? {
    success: Boolean(tunnel.lastProbeSuccess),
    statusCode: tunnel.lastProbeStatusCode,
    error: tunnel.lastProbeError,
    targetEntry: tunnel.lastProbeTargetEntry || tunnelPublicEntry(tunnel),
    probedAt: tunnel.lastProbedAt,
  } : null);
  if (!result) {
    return <div className="weak-note">当前还没有 probe 结果。可直接探测当前入口验证可达性。</div>;
  }
  return (
    <div className={result.success ? "banner info" : "banner error"}>
      {result.success ? "Probe 成功" : "Probe 失败"}
      {result.statusCode ? ` / status=${result.statusCode}` : ""}
      {result.targetEntry ? ` / target=${result.targetEntry}` : ""}
      {result.error ? ` / error=${result.error}` : ""}
    </div>
  );
}
