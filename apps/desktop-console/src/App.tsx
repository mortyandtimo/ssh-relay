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
  TunnelTypeTab,
  UserSummary,
} from "../../../packages/desktop-core/src/types";
import {
  capabilitySummary,
  checkStateLabel,
  checkStateTone,
  controlOptionStateLabel,
  controlOptionTone,
  formatControlResultDisplay,
  formatDate,
  nodeAgentDeploymentLabel,
  publicEntry,
  resolveLocalNodeBinding,
  runtimeLabel,
  statusClass,
  tunnelTabs,
} from "../../../packages/desktop-core/src/utils";

const api = createDesktopApi(import.meta.env.VITE_API_BASE_URL || "");
const desktopNodeId = (import.meta.env.VITE_DESKTOP_NODE_ID || "").trim();

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
                      <div className="empty-inline">请先在左侧选择一个 tunnel。</div>
                    ) : (
                      <div className="detail-column">
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

function ControlResultBlock({ result }: { result: ControlActionResponse }) {
  const display = formatControlResultDisplay(result);
  return (
    <section className="panel result-panel">
      <div className="panel-head small">
        <h2>最近一次控制结果</h2>
        <span className={"status-chip " + display.tone}>{display.categoryLabel}</span>
      </div>
      <p className="copy">{display.message}</p>
      <p className="copy">result: <code>{result.result}</code> / executeOutcome: <code>{result.executeOutcome || "-"}</code> / rejectionKind: <code>{result.rejectionKind || "-"}</code></p>
      <p className="copy">executionMode: <code>{display.executionMode}</code> / dryRunOnly: <code>{display.dryRunOnly}</code> / placeholderOnly: <code>{String(display.placeholderOnly)}</code></p>
      <p className="copy">下一步：{display.nextStep}</p>
      {display.blockedReasons.length > 0 ? (
        <div className="check-list compact-check-list">
          {display.blockedReasons.map((reason) => (
            <div key={reason.code} className="check-item">
              <div className="check-head">
                <strong>{reason.code}</strong>
                <span className="status-chip danger">阻断</span>
              </div>
              <p className="copy">{reason.message}</p>
            </div>
          ))}
        </div>
      ) : null}
      {display.executionNotes.length > 0 ? (
        <div className="check-list compact-check-list">
          {display.executionNotes.map((note) => (
            <div key={note.code || note.message} className="check-item">
              <div className="check-head">
                <strong>{note.code || "note"}</strong>
                <span className="status-chip neutral">执行说明</span>
              </div>
              <p className="copy">{note.message}</p>
            </div>
          ))}
        </div>
      ) : null}
    </section>
  );
}
