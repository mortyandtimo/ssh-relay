import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { createDesktopApi } from "../../../packages/desktop-core/src/api";
import type { ControlActionRequest, ControlActionResponse, NodeSummary, TunnelSpec, TunnelTypeTab, UserSummary } from "../../../packages/desktop-core/src/types";
import { capabilitySummary, checkStateLabel, checkStateTone, formatControlResultDisplay, formatDate, nodeAgentDeploymentLabel, publicEntry, resolveLocalNodeBinding, runtimeLabel, statusClass, tunnelTabs, type SafetyCheckItem } from "../../../packages/desktop-core/src/utils";

const api = createDesktopApi(import.meta.env.VITE_API_BASE_URL || "");
const desktopNodeId = (import.meta.env.VITE_DESKTOP_NODE_ID || "").trim();

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
  const [activeTab, setActiveTab] = useState<TunnelTypeTab>("tcp");
  const [selectedTunnelId, setSelectedTunnelId] = useState<string | null>(null);
  const [editForm, setEditForm] = useState<TunnelEditForm | null>(null);
  const [loginForm, setLoginForm] = useState({ email: "", password: "" });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState("");
  const [controlNote, setControlNote] = useState("");
  const [controlResult, setControlResult] = useState<ControlActionResponse | null>(null);
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
  const boundNode = selectedNode;
  const bindingTone = localBinding.status === "success" ? "good" : localBinding.status === "config_error" ? "danger" : localBinding.status === "ambiguous" ? "warn" : "neutral";
  const bindingStatusLabel = localBinding.status === "success" ? "成功" : localBinding.status === "config_error" ? "配置错误" : localBinding.status === "ambiguous" ? "不唯一" : "未完成";
  const localControlChecks = useMemo<SafetyCheckItem[]>(() => {
    const items: SafetyCheckItem[] = [
      {
        label: "本机绑定状态",
        state: localBinding.status === "success" ? "pass" : localBinding.status === "config_error" ? "blocked" : localBinding.status === "ambiguous" ? "blocked" : "missing",
        detail: localBinding.reason,
      },
      {
        label: "已明确命中本机节点",
        state: localBinding.node ? "pass" : "blocked",
        detail: localBinding.node ? "当前已命中 nodeId=" + localBinding.node.nodeId : "当前还没有命中明确本机节点。",
      },
      {
        label: "当前为受管实例",
        state: boundNode?.instanceManaged ? "pass" : boundNode ? "missing" : "blocked",
        detail: boundNode?.instanceManaged ? "instanceManaged=true。" : "当前实例未标记为受管实例。",
      },
      {
        label: "deploymentMode",
        state: boundNode?.deploymentMode ? "pass" : boundNode ? "missing" : "blocked",
        detail: boundNode?.deploymentMode ? boundNode.deploymentMode : "当前还没有上报 deploymentMode。",
      },
      {
        label: "serviceUnit 已上报",
        state: boundNode?.serviceUnit ? "pass" : boundNode ? "missing" : "blocked",
        detail: boundNode?.serviceUnit || "当前还没有上报 serviceUnit。",
      },
      {
        label: "instanceProfile 已上报",
        state: boundNode?.instanceProfile ? "pass" : boundNode ? "missing" : "blocked",
        detail: boundNode?.instanceProfile || "当前还没有上报 instanceProfile。",
      },
      {
        label: "当前节点 online",
        state: boundNode?.status === "online" ? "pass" : boundNode ? "blocked" : "blocked",
        detail: boundNode?.status === "online" ? "当前节点在线。" : "当前节点不在线，未来本机控制动作应阻断。",
      },
      {
        label: "当前节点未隔离",
        state: boundNode && !boundNode.isolated ? "pass" : boundNode ? "blocked" : "blocked",
        detail: boundNode ? (boundNode.isolated ? "当前节点已隔离，未来危险动作应阻断。" : "当前节点未隔离。") : "当前没有命中本机节点。",
      },
    ];
    return items;
  }, [boundNode, localBinding]);

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

  async function handleTunnelSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedTunnel || !boundNode || !editForm) {
      return;
    }
    setBusy("save-tunnel");
    setError("");
    setMessage("");
    try {
      await api.updateTunnel(selectedTunnel.id, {
        id: selectedTunnel.id,
        nodeId: boundNode.nodeId,
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
        sourceSurface: "node_console",
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
        </aside>
        <main className="main-stage">
          {error ? <div className="banner error">{error}</div> : null}
          {message ? <div className="banner info">{message}</div> : null}
          {!localBinding.node || !boundNode ? (
            <StateCard title="尚未完成本机绑定" body={localBinding.reason + " " + localBinding.nextAction + " node-console 不允许出现机器选择器。"} />
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
              <section className="panel preflight-panel">
                <div className="panel-head small">
                  <div>
                    <h3>本机控制预检区</h3>
                    <p className="copy">这里是未来危险操作前的预检/确认层。当前还没有执行任何真实控制命令，只是在冻结本机危险动作的人机交互边界。</p>
                  </div>
                </div>
                <div className="check-list">
                  {localControlChecks.map((item) => (
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
                  <span>本机动作备注</span>
                  <textarea value={controlNote} onChange={(event) => setControlNote(event.target.value)} placeholder="可选：记录为什么要做本机动作预检/占位执行" />
                </label>
                <div className="button-row">
                  <button className="secondary" type="button" disabled={!boundNode || busy === "control-action"} onClick={() => void runControlAction("restart_agent", "node", boundNode.nodeId, true)}>{busy === "control-action" ? "处理中..." : "预检 restart_agent"}</button>
                  <button className="secondary" type="button" disabled={!boundNode || busy === "control-action"} onClick={() => void runControlAction("restart_agent", "node", boundNode.nodeId, false)}>{busy === "control-action" ? "处理中..." : "占位执行 restart_agent"}</button>
                </div>
                <div className="banner info">当前为什么还不能执行未来本机控制动作：只要本机绑定、受管实例、serviceUnit、online、未隔离这几项中任一不满足，就应继续阻断。</div>
                <div className="banner info">下一步建议：先补齐本机绑定与受管实例信息，再进入真正控制命令实现阶段；当前这轮只做确认层，不执行动作。</div>
                {controlResult ? <ControlResultBlock result={controlResult} /> : null}
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
