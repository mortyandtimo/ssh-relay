import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { createDesktopApi } from "../../../packages/desktop-core/src/api";
import type { ControlActionOption, ControlActionRequest, ControlActionResponse, NodeSummary, TunnelSpec, TunnelTypeTab, UserSummary } from "../../../packages/desktop-core/src/types";
import { capabilitySummary, checkStateLabel, checkStateTone, controlOptionStateLabel, controlOptionTone, formatControlResultDisplay, formatDate, nodeAgentDeploymentLabel, publicEntry, runtimeLabel, statusClass, tunnelTabs, type SafetyCheckItem } from "../../../packages/desktop-core/src/utils";

const api = createDesktopApi(import.meta.env.VITE_API_BASE_URL || "");
type RuntimeStateFilter = "all" | "pending" | "unavailable" | "reported";
type RuntimePathFilter = "all" | "relay" | "p2p" | "reported";
type AttentionFilter = "all" | "needs_attention" | "failure_reason" | "p2p_fallback";

type TunnelEditForm = {
  name: string;
  status: "active" | "paused";
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
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<TunnelTypeTab>("tcp");
  const [selectedTunnelId, setSelectedTunnelId] = useState<string | null>(null);
  const [editForm, setEditForm] = useState<TunnelEditForm | null>(null);
  const [runtimeStateFilter, setRuntimeStateFilter] = useState<RuntimeStateFilter>("all");
  const [runtimePathFilter, setRuntimePathFilter] = useState<RuntimePathFilter>("all");
  const [attentionFilter, setAttentionFilter] = useState<AttentionFilter>("all");
  const [loginForm, setLoginForm] = useState({ email: "", password: "" });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState("");
  const [controlNote, setControlNote] = useState("");
  const [controlResult, setControlResult] = useState<ControlActionResponse | null>(null);
  const [nodeActionOptions, setNodeActionOptions] = useState<ControlActionOption[]>([]);
  const [tunnelActionOptions, setTunnelActionOptions] = useState<ControlActionOption[]>([]);
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

  useEffect(() => {
    if (!currentUser) {
      setSelectedNodeId(null);
      return;
    }
    setSelectedNodeId((current) => {
      if (current && nodes.some((node) => node.nodeId === current)) {
        return current;
      }
      return nodes[0]?.nodeId ?? null;
    });
  }, [currentUser, nodes]);

  const selectedNode = useMemo(
    () => (selectedNodeId ? nodes.find((node) => node.nodeId === selectedNodeId) ?? null : null),
    [nodes, selectedNodeId],
  );

  const selectedNodeTunnels = useMemo(() => {
    if (!selectedNode) return [];
    return tunnels.filter((tunnel) => tunnel.nodeId === selectedNode.nodeId);
  }, [selectedNode, tunnels]);
  const tabTunnels = useMemo(() => {
    return selectedNodeTunnels.filter((tunnel) => {
      if (tunnel.type !== activeTab) {
        return false;
      }
      if (runtimeStateFilter === "pending" && tunnel.runtimeState !== "pending") {
        return false;
      }
      if (runtimeStateFilter === "unavailable" && tunnel.runtimeState !== "unavailable") {
        return false;
      }
      if (runtimeStateFilter === "reported" && !tunnel.runtimeState) {
        return false;
      }
      if (runtimePathFilter === "relay" && tunnel.runtimePath !== "relay") {
        return false;
      }
      if (runtimePathFilter === "p2p" && tunnel.runtimePath !== "p2p") {
        return false;
      }
      if (runtimePathFilter === "reported" && !tunnel.runtimePath) {
        return false;
      }
      if (attentionFilter === "failure_reason" && !tunnel.lastFailureReason) {
        return false;
      }
      if (attentionFilter === "p2p_fallback" && !isP2PFallbackTunnel(tunnel)) {
        return false;
      }
      if (attentionFilter === "needs_attention" && !needsAttention(tunnel)) {
        return false;
      }
      return true;
    });
  }, [activeTab, attentionFilter, runtimePathFilter, runtimeStateFilter, selectedNodeTunnels]);

  const unavailableCount = useMemo(() => selectedNodeTunnels.filter((tunnel) => tunnel.runtimeState === "unavailable").length, [selectedNodeTunnels]);
  const failureReasonCount = useMemo(() => selectedNodeTunnels.filter((tunnel) => Boolean(tunnel.lastFailureReason)).length, [selectedNodeTunnels]);
  const p2pFallbackCount = useMemo(() => selectedNodeTunnels.filter((tunnel) => isP2PFallbackTunnel(tunnel)).length, [selectedNodeTunnels]);
  const remoteControlChecks = useMemo<SafetyCheckItem[]>(() => {
    const items: SafetyCheckItem[] = [
      {
        label: "已选中机器",
        state: selectedNode ? "pass" : "blocked",
        detail: selectedNode ? "当前已选中机器 " + selectedNode.nodeId + "。" : "当前还没有选中机器，无法进入远程控制预检。",
      },
      {
        label: "当前机器 online",
        state: selectedNode?.status === "online" ? "pass" : selectedNode ? "blocked" : "blocked",
        detail: selectedNode?.status === "online" ? "当前机器在线。" : "当前机器不在线，未来远程动作应阻断。",
      },
      {
        label: "当前机器未隔离",
        state: selectedNode && !selectedNode.isolated ? "pass" : selectedNode ? "blocked" : "blocked",
        detail: selectedNode ? (selectedNode.isolated ? "当前机器已隔离，未来远程动作应阻断。" : "当前机器未隔离。") : "当前还没有选中机器。",
      },
      {
        label: "deploymentMode",
        state: selectedNode?.deploymentMode ? "pass" : selectedNode ? "missing" : "blocked",
        detail: selectedNode?.deploymentMode || "当前还没有上报 deploymentMode。",
      },
      {
        label: "serviceUnit 已上报",
        state: selectedNode?.serviceUnit ? "pass" : selectedNode ? "missing" : "blocked",
        detail: selectedNode?.serviceUnit || "当前还没有上报 serviceUnit。",
      },
      {
        label: "instanceManaged",
        state: selectedNode?.instanceManaged ? "pass" : selectedNode ? "missing" : "blocked",
        detail: selectedNode?.instanceManaged ? "当前机器为受管实例。" : "当前机器不是受管实例，未来远程控制前提不完整。",
      },
      {
        label: "满足未来远程控制前提",
        state: selectedNode && selectedNode.status === "online" && !selectedNode.isolated && selectedNode.instanceManaged && Boolean(selectedNode.serviceUnit) ? "pass" : selectedNode ? "blocked" : "blocked",
        detail: selectedNode && selectedNode.status === "online" && !selectedNode.isolated && selectedNode.instanceManaged && Boolean(selectedNode.serviceUnit)
          ? "当前机器已满足最小远程控制前提，但这轮仍未实现真实命令。"
          : "当前至少有一项关键前提未满足，因此未来远程动作仍应阻断。",
      },
    ];
    return items;
  }, [selectedNode]);

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
    let cancelled = false;
    async function loadNodeActionOptions() {
      if (!selectedNode || !currentUser) {
        setNodeActionOptions([]);
        return;
      }
      try {
        const response = await api.loadNodeControlActionOptions(selectedNode.nodeId, "operator_console");
        if (!cancelled) {
          setNodeActionOptions(response.items);
        }
      } catch (actionError) {
        if (!cancelled) {
          setNodeActionOptions([]);
          setError(actionError instanceof Error ? actionError.message : "读取节点动作摘要失败");
        }
      }
    }
    void loadNodeActionOptions();
    return () => {
      cancelled = true;
    };
  }, [selectedNode, currentUser]);

  useEffect(() => {
    let cancelled = false;
    async function loadTunnelActionOptions() {
      if (!selectedTunnel || !currentUser) {
        setTunnelActionOptions([]);
        return;
      }
      try {
        const response = await api.loadTunnelControlActionOptions(selectedTunnel.id, "operator_console");
        if (!cancelled) {
          setTunnelActionOptions(response.items);
        }
      } catch (actionError) {
        if (!cancelled) {
          setTunnelActionOptions([]);
          setError(actionError instanceof Error ? actionError.message : "读取 tunnel 动作摘要失败");
        }
      }
    }
    void loadTunnelActionOptions();
    return () => {
      cancelled = true;
    };
  }, [selectedTunnel, currentUser]);

  useEffect(() => {
    if (!selectedTunnel) {
      setEditForm(null);
      return;
    }
    setEditForm({
      name: selectedTunnel.name,
      status: selectedTunnel.status === "paused" ? "paused" : "active",
      targetHost: selectedTunnel.targetHost || "",
      targetPort: String(selectedTunnel.targetPort || ""),
      publicPort: String(selectedTunnel.publicPort || ""),
      domain: selectedTunnel.domain || "",
      probePath: selectedTunnel.probePath || "",
      transportPolicy: selectedTunnel.transportPolicy || "relay_only",
    });
  }, [selectedTunnel]);

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
      setMessage("operator-console 已接入当前管理面数据，请选择机器继续。");
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
      setSelectedNodeId(null);
      setSelectedTunnelId(null);
      setLastRefreshAt("");
      setMessage("已退出 operator-console。");
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
        setMessage("operator-console 数据已刷新，当前选择上下文已尽量保留。");
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
        status: editForm.status,
        targetHost: editForm.targetHost.trim(),
        targetPort: Number(editForm.targetPort),
        publicPort: Number(editForm.publicPort),
        domain: selectedTunnel.type === "http" || selectedTunnel.type === "https" ? editForm.domain.trim() : "",
        probePath: selectedTunnel.type === "http" || selectedTunnel.type === "https" ? editForm.probePath.trim() : "",
        transportPolicy: editForm.transportPolicy,
        tlsMode: selectedTunnel.tlsMode || "",
      });
      await refreshConsoleData(false, "manual");
      setMessage("当前 tunnel 的最小配置字段已提交，列表和详情已刷新。transportPolicy 仍只代表配置意图，不代表当前 runtime facts 已改变。");
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "保存 tunnel 失败");
    } finally {
      setBusy("");
    }
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
        sourceSurface: "operator_console",
        dryRun,
        note: controlNote.trim(),
        requestedAt: new Date().toISOString(),
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
    if (error) return <Shell><StateCard title="operator-console 初始化失败" body={error} /></Shell>;
    return <Shell><StateCard title="operator-console 初始化中" body="正在读取现有管理面认证状态和后端基础数据。" /></Shell>;
  }

  if (bootstrapRequired) {
    return <Shell><StateCard title="当前环境仍需 bootstrap" body="请先通过现有管理面完成初始化。" /></Shell>;
  }

  if (!currentUser) {
    return (
      <Shell>
        <div className="login-card panel">
          <p className="eyebrow">operator-console</p>
          <h1>运维控制台</h1>
          <p className="copy">先展示机器列表，再进入某台机器后复用按类型切换的工作区。不做控制命令和 P2P 数据面。</p>
          <form className="login-form" onSubmit={handleLogin}>
            <label><span>邮箱</span><input value={loginForm.email} onChange={(event) => setLoginForm((current) => ({ ...current, email: event.target.value }))} required /></label>
            <label><span>密码</span><input type="password" value={loginForm.password} onChange={(event) => setLoginForm((current) => ({ ...current, password: event.target.value }))} required /></label>
            <button type="submit" disabled={busy === "login"}>{busy === "login" ? "登录中..." : "进入 operator-console"}</button>
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
            <p className="eyebrow">operator-console</p>
            <h1>运维控制台</h1>
            <p className="copy">先进入机器列表，再进入某台机器的工作区；不承担本机服务托管职责。</p>
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
          <div className="panel machine-panel">
            <div className="panel-head"><h2>机器列表</h2><span>{nodes.length} 台</span></div>
            <div className="machine-list">
              {nodes.map((node) => (
                <button key={node.nodeId} type="button" className={selectedNodeId === node.nodeId ? "machine-item active" : "machine-item"} onClick={() => setSelectedNodeId(node.nodeId)}>
                  <div className="machine-topline"><strong>{node.nodeName}</strong><span className={statusClass(node.status)}>{node.status}</span></div>
                  <div className="machine-subline">{node.nodeId}</div>
                  <div className="machine-subline">{node.isolated ? "已隔离" : "未隔离"} / {nodeAgentDeploymentLabel(node)}</div>
                  <div className="machine-subline">{capabilitySummary(node.capabilities)}</div>
                  <div className="machine-subline">active {node.activeTunnels} / relay {node.runtimeSummary?.relayPathCount ?? 0} / p2p {node.runtimeSummary?.p2pPathCount ?? 0}</div>
                </button>
              ))}
            </div>
          </div>
        </aside>
        <main className="main-stage">
          {error ? <div className="banner error">{error}</div> : null}
          {message ? <div className="banner info">{message}</div> : null}
          {!selectedNode ? <StateCard title="当前没有可展示的机器" body="请先在左侧机器列表中选择一台机器。" /> : (
            <>
              <section className="panel hero-panel">
                <div className="hero-head">
                  <div>
                    <p className="eyebrow">当前机器</p>
                    <h2>{selectedNode.nodeName}</h2>
                    <p className="copy">{selectedNode.nodeId}</p>
                  </div>
                  <div className="hero-chips">
                    <span className={statusClass(selectedNode.status)}>{selectedNode.status}</span>
                    <span className={selectedNode.isolated ? "status-chip danger" : "status-chip good"}>{selectedNode.isolated ? "已隔离" : "未隔离"}</span>
                    <span className="status-chip neutral">{selectedNode.agentVersion || "agent 未上报"}</span>
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
                    <h3>远程控制预检区</h3>
                    <p className="copy">这里是未来远程危险操作前的预检/确认层。当前还没有执行任何真实控制命令，只是在冻结远程危险动作的人机交互边界。</p>
                  </div>
                </div>
                <div className="check-list">
                  {remoteControlChecks.map((item) => (
                    <div key={item.label} className="check-item">
                      <div className="check-head">
                        <strong>{item.label}</strong>
                        <span className={"status-chip " + checkStateTone(item.state)}>{checkStateLabel(item.state)}</span>
                      </div>
                      <p className="copy">{item.detail}</p>
                    </div>
                  ))}
                </div>
                <label className="control-note-field">
                  <span>远程动作备注</span>
                  <textarea value={controlNote} onChange={(event) => setControlNote(event.target.value)} placeholder="可选：记录为什么要做远程动作预检/占位执行" />
                </label>
                <div className="check-list compact-check-list">
                  {nodeActionOptions.map((option) => (
                    <div key={option.actionKind} className="check-item">
                      <div className="check-head">
                        <strong>{option.label}</strong>
                        <span className={"status-chip " + controlOptionTone(option)}>{controlOptionStateLabel(option)}</span>
                      </div>
                      <p className="copy">{option.message}</p>
                      {option.primaryReasonCode ? <p className="copy">primaryReason: <code>{option.primaryReasonCode}</code></p> : null}
                      <div className="button-row wrap-actions">
                        <button className="secondary" type="button" disabled={!option.available || busy === "control-action"} onClick={() => void runControlAction(option.actionKind, option.targetKind, option.targetId, true)}>{busy === "control-action" ? "处理中..." : "预检 " + option.label}</button>
                        {option.placeholderOnly ? <button className="secondary" type="button" disabled={!option.available || busy === "control-action"} onClick={() => void runControlAction(option.actionKind, option.targetKind, option.targetId, false)}>{busy === "control-action" ? "处理中..." : "占位执行 " + option.label}</button> : null}
                      </div>
                    </div>
                  ))}
                </div>
                <div className="banner info">当前为什么还不能执行未来远程控制动作：只要机器未选中、offline、isolated、未受管、未上报 serviceUnit 中任一成立，就应继续阻断。</div>
                <div className="banner info">下一步建议：先补齐机器在线性、受管实例信息和 serviceUnit 上报，再进入真正控制命令实现阶段；当前这轮只做确认层，不执行动作。</div>
                {controlResult ? <ControlResultBlock result={controlResult} /> : null}
              </section>
              <section className="panel workbench-panel">
                <div className="panel-head"><div><h2>按隧道类型切换的工作区</h2><p className="copy">进入某台机器后，复用按类型切换的工作区与 tunnel 详情区。</p></div></div>
                <div className="hero-grid focus-grid">
                  <Metric label="待处理 unavailable" value={String(unavailableCount)} />
                  <Metric label="failureReason 非空" value={String(failureReasonCount)} />
                  <Metric label="p2p 预期但仍走 relay" value={String(p2pFallbackCount)} />
                </div>
                <div className="filter-strip">
                  <label>
                    <span>runtimeState</span>
                    <select value={runtimeStateFilter} onChange={(event) => setRuntimeStateFilter(event.target.value as RuntimeStateFilter)}>
                      <option value="all">全部</option>
                      <option value="pending">pending</option>
                      <option value="unavailable">unavailable</option>
                      <option value="reported">已上报</option>
                    </select>
                  </label>
                  <label>
                    <span>runtimePath</span>
                    <select value={runtimePathFilter} onChange={(event) => setRuntimePathFilter(event.target.value as RuntimePathFilter)}>
                      <option value="all">全部</option>
                      <option value="relay">relay</option>
                      <option value="p2p">p2p</option>
                      <option value="reported">已上报</option>
                    </select>
                  </label>
                  <label>
                    <span>快速聚焦</span>
                    <select value={attentionFilter} onChange={(event) => setAttentionFilter(event.target.value as AttentionFilter)}>
                      <option value="all">全部</option>
                      <option value="needs_attention">待处理项</option>
                      <option value="failure_reason">failureReason 非空</option>
                      <option value="p2p_fallback">p2p 预期但仍走 relay</option>
                    </select>
                  </label>
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
                      {tabTunnels.length === 0 ? <div className="empty-inline">当前机器没有 {activeTab.toUpperCase()} tunnel。</div> : tabTunnels.map((tunnel) => (
                        <button key={tunnel.id} type="button" className={selectedTunnelId === tunnel.id ? tunnelItemClass(tunnel, true) : tunnelItemClass(tunnel, false)} onClick={() => setSelectedTunnelId(tunnel.id)}>
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
                          <label><span>status</span><select value={editForm?.status || "active"} onChange={(event) => setEditForm((current) => current ? { ...current, status: event.target.value as "active" | "paused" } : current)}><option value="active">active</option><option value="paused">paused</option></select></label>
                          <label><span>targetHost</span><input value={editForm?.targetHost || ""} onChange={(event) => setEditForm((current) => current ? { ...current, targetHost: event.target.value } : current)} /></label>
                          <label><span>targetPort</span><input value={editForm?.targetPort || ""} onChange={(event) => setEditForm((current) => current ? { ...current, targetPort: event.target.value } : current)} /></label>
                          <label><span>publicPort</span><input value={editForm?.publicPort || ""} onChange={(event) => setEditForm((current) => current ? { ...current, publicPort: event.target.value } : current)} /></label>
                          {(selectedTunnel.type === "http" || selectedTunnel.type === "https") ? <label><span>domain</span><input value={editForm?.domain || ""} onChange={(event) => setEditForm((current) => current ? { ...current, domain: event.target.value } : current)} /></label> : <div className="weak-note">当前类型不适用 domain。</div>}
                          {(selectedTunnel.type === "http" || selectedTunnel.type === "https") ? <label><span>probePath</span><input value={editForm?.probePath || ""} onChange={(event) => setEditForm((current) => current ? { ...current, probePath: event.target.value } : current)} /></label> : <div className="weak-note">当前类型不适用 probePath。</div>}
                          <label><span>transportPolicy</span><select value={editForm?.transportPolicy || "relay_only"} onChange={(event) => setEditForm((current) => current ? { ...current, transportPolicy: event.target.value } : current)}><option value="relay_only">relay_only</option><option value="p2p_preferred">p2p_preferred</option></select></label>
                          <div className="weak-note">transportPolicy 只代表配置意图。当前 runtimePath / runtimeState / lastFailureReason 仍然是运行事实，这轮编辑不会把它们伪装成已经改变。</div>
                          <div className="check-list compact-check-list">
                            {tunnelActionOptions.map((option) => (
                              <div key={option.actionKind} className="check-item">
                                <div className="check-head">
                                  <strong>{option.label}</strong>
                                  <span className={"status-chip " + controlOptionTone(option)}>{controlOptionStateLabel(option)}</span>
                                </div>
                                <p className="copy">{option.message}</p>
                                {option.primaryReasonCode ? <p className="copy">primaryReason: <code>{option.primaryReasonCode}</code></p> : null}
                                <div className="button-row wrap-actions">
                                  <button className="secondary" type="button" disabled={!option.available || busy === "control-action"} onClick={() => void runControlAction(option.actionKind, option.targetKind, option.targetId, true)}>{busy === "control-action" ? "处理中..." : "预检 " + option.label}</button>
                                  {option.placeholderOnly ? <button className="secondary" type="button" disabled={!option.available || busy === "control-action"} onClick={() => void runControlAction(option.actionKind, option.targetKind, option.targetId, false)}>{busy === "control-action" ? "处理中..." : "占位执行 " + option.label}</button> : null}
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

function ControlResultBlock({ result }: { result: ControlActionResponse }) {
  const display = formatControlResultDisplay(result);
  return (
    <div className="control-result-card">
      <div className="check-head">
        <strong>最近一次控制契约结果</strong>
        <span className={"status-chip " + display.tone}>{result.result}</span>
      </div>
      <p className="copy">{display.message}</p>
      <p className="copy">executionMode: <code>{display.executionMode}</code> / dryRunOnly: <code>{display.dryRunOnly}</code></p>
      {display.placeholderOnly ? <div className="banner info">当前仅为占位执行提示，尚未接入真实系统执行器。</div> : null}
      <div className="check-list compact-check-list">
        {display.checks.map((item) => (
          <div key={item.code} className="check-item">
            <div className="check-head">
              <strong>{item.label}</strong>
              <span className={"status-chip " + item.stateTone}>{item.stateLabel}</span>
            </div>
            <p className="copy">{item.message}</p>
          </div>
        ))}
      </div>
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
            <div key={note.code} className="check-item">
              <div className="check-head">
                <strong>{note.code}</strong>
                <span className="status-chip neutral">占位执行提示</span>
              </div>
              <p className="copy">{note.message}</p>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function needsAttention(tunnel: TunnelSpec) {
  return tunnel.runtimeState === "unavailable" || Boolean(tunnel.lastFailureReason) || isP2PFallbackTunnel(tunnel);
}

function isP2PFallbackTunnel(tunnel: TunnelSpec) {
  return tunnel.transportPolicy === "p2p_preferred" && tunnel.runtimePath === "relay";
}

function tunnelItemClass(tunnel: TunnelSpec, selected: boolean) {
  const classes = [selected ? "tunnel-item active" : "tunnel-item"];
  if (!selected && needsAttention(tunnel)) {
    classes.push("attention-item");
  }
  return classes.join(" ");
}
