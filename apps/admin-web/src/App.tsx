import { FormEvent, useEffect, useState } from "react";

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

type DashboardPayload = {
  nodes: NodeSummary[];
  tunnels: TunnelSpec[];
  metrics: ServerMetrics;
  relayRuntime: RelayRuntimeSummary;
};

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || "http://localhost:8080";
const adminTokenStorageKey = "cloud-relay-admin-token";

const initialTunnelForm: TunnelForm = {
  nodeId: "",
  name: "",
  targetHost: "127.0.0.1",
  targetPort: "",
  publicPort: "",
};

function readStoredAdminToken() {
  if (typeof window === "undefined") {
    return "";
  }
  return window.localStorage.getItem(adminTokenStorageKey) || "";
}

export default function App() {
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [tunnels, setTunnels] = useState<TunnelSpec[]>([]);
  const [metrics, setMetrics] = useState<ServerMetrics | null>(null);
  const [relayRuntime, setRelayRuntime] = useState<RelayRuntimeSummary | null>(null);
  const [tunnelForm, setTunnelForm] = useState<TunnelForm>(initialTunnelForm);
  const [tokenInput, setTokenInput] = useState<string>(readStoredAdminToken);
  const [adminToken, setAdminToken] = useState<string>(readStoredAdminToken);
  const [hasInitializedNodeId, setHasInitializedNodeId] = useState(false);
  const [error, setError] = useState<string>("");
  const [message, setMessage] = useState<string>("");
  const [busyAction, setBusyAction] = useState<string>("");

  useEffect(() => {
    if (!adminToken) {
      setNodes([]);
      setTunnels([]);
      setMetrics(null);
      setRelayRuntime(null);
      setError("");
      return;
    }

    let cancelled = false;

    async function loadSilently() {
      try {
        const payload = await loadDashboard(adminToken);
        if (cancelled) {
          return;
        }
        applyDashboardPayload(payload);
        setError("");
      } catch (loadError) {
        if (!cancelled) {
          setError(loadError instanceof Error ? loadError.message : "加载管理数据失败");
        }
      }
    }

    void loadSilently();
    const timer = window.setInterval(() => {
      void loadSilently();
    }, 10000);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [adminToken, hasInitializedNodeId]);

  function applyDashboardPayload(payload: DashboardPayload) {
    setNodes(payload.nodes);
    setTunnels(payload.tunnels);
    setMetrics(payload.metrics);
    setRelayRuntime(payload.relayRuntime);

    if (!hasInitializedNodeId) {
      const defaultNodeId = payload.nodes[0]?.nodeId || "";
      if (defaultNodeId !== "") {
        setTunnelForm((current) => {
          if (current.nodeId !== "") {
            return current;
          }
          return { ...current, nodeId: defaultNodeId };
        });
        setHasInitializedNodeId(true);
      }
    }
  }

  async function requestJSON<T>(path: string, init?: { method?: string; body?: string; token?: string }): Promise<T> {
    const headers: Record<string, string> = {
      Accept: "application/json",
    };
    if (init?.body) {
      headers["Content-Type"] = "application/json";
    }
    if (init?.token) {
      headers.Authorization = "Bearer " + init.token;
    }

    const response = await fetch(apiBaseUrl + path, {
      method: init?.method || "GET",
      headers,
      body: init?.body,
    });

    const payload = await response.json().catch(() => null);
    if (response.status === 401) {
      throw new Error("管理令牌无效或缺失");
    }
    if (!response.ok) {
      const responseError = payload && typeof payload.error === "string" ? payload.error : "请求失败";
      throw new Error(responseError);
    }
    return payload as T;
  }

  async function loadDashboard(token: string): Promise<DashboardPayload> {
    const [nodesPayload, tunnelsPayload, metricsPayload, relayPayload] = await Promise.all([
      requestJSON<{ items: NodeSummary[] }>("/api/nodes", { token }),
      requestJSON<{ items: TunnelSpec[] }>("/api/tunnels", { token }),
      requestJSON<ServerMetrics>("/api/server/metrics", { token }),
      requestJSON<RelayRuntimeSummary>("/api/relay/tcp/runtime", { token }),
    ]);

    return {
      nodes: nodesPayload.items || [],
      tunnels: tunnelsPayload.items || [],
      metrics: metricsPayload,
      relayRuntime: relayPayload,
    };
  }

  async function refreshDashboard(showNotice: boolean) {
    if (!adminToken) {
      setError("请先输入管理令牌");
      return;
    }
    setBusyAction("refresh");
    setError("");
    try {
      const payload = await loadDashboard(adminToken);
      applyDashboardPayload(payload);
      if (showNotice) {
        setMessage("管理数据已刷新。");
      }
    } catch (refreshError) {
      setError(refreshError instanceof Error ? refreshError.message : "刷新管理数据失败");
    } finally {
      setBusyAction("");
    }
  }

  async function reloadAfterMutation() {
    if (!adminToken) {
      return;
    }
    const payload = await loadDashboard(adminToken);
    applyDashboardPayload(payload);
  }

  function submitAdminToken(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextToken = tokenInput.trim();
    if (nextToken === "") {
      setError("请输入管理令牌");
      return;
    }
    window.localStorage.setItem(adminTokenStorageKey, nextToken);
    setAdminToken(nextToken);
    setError("");
    setMessage("管理令牌已保存。");
  }

  function clearAdminToken() {
    window.localStorage.removeItem(adminTokenStorageKey);
    setAdminToken("");
    setTokenInput("");
    setNodes([]);
    setTunnels([]);
    setMetrics(null);
    setRelayRuntime(null);
    setTunnelForm(initialTunnelForm);
    setHasInitializedNodeId(false);
    setError("");
    setMessage("已清除管理令牌。");
  }

  async function submitTunnel(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!adminToken) {
      setError("请先输入管理令牌");
      return;
    }
    setBusyAction("create-tunnel");
    setError("");
    setMessage("");
    try {
      await requestJSON<TunnelSpec>("/api/tunnels", {
        method: "POST",
        token: adminToken,
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
      await reloadAfterMutation();
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : "创建隧道失败");
    } finally {
      setBusyAction("");
    }
  }

  async function updateTunnelStatus(tunnel: TunnelSpec, status: "active" | "paused") {
    if (!adminToken) {
      setError("请先输入管理令牌");
      return;
    }
    const actionKey = tunnel.id + ":" + status;
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      await requestJSON<TunnelSpec>("/api/tunnels/" + tunnel.id, {
        method: "PUT",
        token: adminToken,
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
      await reloadAfterMutation();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : "更新隧道失败");
    } finally {
      setBusyAction("");
    }
  }

  async function deleteTunnel(tunnel: TunnelSpec) {
    if (!adminToken) {
      setError("请先输入管理令牌");
      return;
    }
    const actionKey = tunnel.id + ":delete";
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      await requestJSON<{ status: string; id: string }>("/api/tunnels/" + tunnel.id, {
        method: "DELETE",
        token: adminToken,
      });
      setMessage("隧道已删除。");
      await reloadAfterMutation();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : "删除隧道失败");
    } finally {
      setBusyAction("");
    }
  }

  return (
    <div className="shell">
      <header className="hero">
        <div>
          <p className="eyebrow">云中继平台</p>
          <h1>管理面最小可用闭环</h1>
          <p className="summary">统一查看节点、隧道、服务指标和反向 TCP 待命池状态，并通过管理令牌保护云端接口。</p>
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

      <section className="panel">
        <div className="panel-header">
          <div>
            <p className="eyebrow">管理认证</p>
            <h2>管理员令牌</h2>
          </div>
        </div>
        <form className="tunnel-form" onSubmit={submitAdminToken}>
          <label>
            <span>Bearer Token</span>
            <input
              type="password"
              value={tokenInput}
              onChange={(event) => setTokenInput(event.target.value)}
              placeholder="输入 SERVER_API_ADMIN_TOKEN"
              required
            />
          </label>
          <button type="submit" disabled={tokenInput.trim() === ""}>
            保存令牌
          </button>
          <button type="button" className="secondary" disabled={adminToken === "" || busyAction === "refresh"} onClick={() => void refreshDashboard(true)}>
            {busyAction === "refresh" ? "刷新中..." : "立即刷新"}
          </button>
          <button type="button" className="secondary" disabled={adminToken === ""} onClick={clearAdminToken}>
            清除令牌
          </button>
        </form>
        <p className="inline-note">当前状态：{adminToken === "" ? "未认证" : "已认证"}</p>
      </section>

      <section className="panel">
        <div className="panel-header">
          <div>
            <p className="eyebrow">创建隧道</p>
            <h2>暴露节点本地服务</h2>
          </div>
        </div>
        <form className="tunnel-form" onSubmit={submitTunnel}>
          <label>
            <span>节点</span>
            <select
              value={tunnelForm.nodeId}
              onChange={(event) => {
                setTunnelForm((current) => ({ ...current, nodeId: event.target.value }));
                setHasInitializedNodeId(true);
              }}
              required
            >
              <option value="">选择节点</option>
              {nodes.map((node) => (
                <option key={node.nodeId} value={node.nodeId}>
                  {node.nodeName} ({node.nodeId})
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>名称</span>
            <input
              value={tunnelForm.name}
              onChange={(event) => setTunnelForm((current) => ({ ...current, name: event.target.value }))}
              placeholder="windows-16354"
              required
            />
          </label>
          <label>
            <span>目标主机</span>
            <input
              value={tunnelForm.targetHost}
              onChange={(event) => setTunnelForm((current) => ({ ...current, targetHost: event.target.value }))}
              required
            />
          </label>
          <label>
            <span>目标端口</span>
            <input
              value={tunnelForm.targetPort}
              onChange={(event) => setTunnelForm((current) => ({ ...current, targetPort: event.target.value }))}
              inputMode="numeric"
              required
            />
          </label>
          <label>
            <span>公网端口</span>
            <input
              value={tunnelForm.publicPort}
              onChange={(event) => setTunnelForm((current) => ({ ...current, publicPort: event.target.value }))}
              inputMode="numeric"
              required
            />
          </label>
          <button type="submit" disabled={busyAction === "create-tunnel" || adminToken === ""}>
            {busyAction === "create-tunnel" ? "创建中..." : "创建隧道"}
          </button>
        </form>
      </section>

      <section className="panel">
        <div className="panel-header">
          <div>
            <p className="eyebrow">中继状态</p>
            <h2>待命池摘要</h2>
          </div>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>池键</th>
                <th>节点</th>
                <th>公网端口</th>
                <th>待命数</th>
                <th>目标值</th>
                <th>上限</th>
              </tr>
            </thead>
            <tbody>
              {relayRuntime?.pools?.length ? (
                relayRuntime.pools.map((pool) => (
                  <tr key={pool.poolKey}>
                    <td>{pool.poolKey}</td>
                    <td>{pool.nodeId}</td>
                    <td>{pool.publicPort}</td>
                    <td>{pool.standbyCount}</td>
                    <td>{pool.targetSize}</td>
                    <td>{pool.maxSize}</td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={6}>{adminToken === "" ? "认证后显示实时待命池摘要。" : "暂无中继池状态。"}</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <div className="panel-header">
          <div>
            <p className="eyebrow">隧道</p>
            <h2>当前隧道列表</h2>
          </div>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>名称</th>
                <th>节点</th>
                <th>状态</th>
                <th>公网</th>
                <th>目标</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {tunnels.length === 0 ? (
                <tr>
                  <td colSpan={6}>{adminToken === "" ? "认证后显示隧道列表。" : "暂无隧道。"}</td>
                </tr>
              ) : (
                tunnels.map((tunnel) => (
                  <tr key={tunnel.id}>
                    <td>
                      <strong>{tunnel.name}</strong>
                      <div className="muted">{tunnel.id}</div>
                    </td>
                    <td>{tunnel.nodeId}</td>
                    <td>{tunnel.status}</td>
                    <td>{tunnel.publicPort}</td>
                    <td>
                      {tunnel.targetHost}:{tunnel.targetPort}
                    </td>
                    <td>
                      <div className="actions-row">
                        <button
                          type="button"
                          disabled={busyAction === tunnel.id + ":active" || tunnel.status === "active" || adminToken === ""}
                          onClick={() => void updateTunnelStatus(tunnel, "active")}
                        >
                          启用
                        </button>
                        <button
                          type="button"
                          disabled={busyAction === tunnel.id + ":paused" || tunnel.status === "paused" || adminToken === ""}
                          onClick={() => void updateTunnelStatus(tunnel, "paused")}
                        >
                          暂停
                        </button>
                        <button
                          type="button"
                          className="danger"
                          disabled={busyAction === tunnel.id + ":delete" || adminToken === ""}
                          onClick={() => void deleteTunnel(tunnel)}
                        >
                          删除
                        </button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <div className="panel-header">
          <div>
            <p className="eyebrow">节点</p>
            <h2>节点在线状态</h2>
          </div>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>节点</th>
                <th>状态</th>
                <th>Agent</th>
                <th>隧道数</th>
                <th>能力</th>
                <th>最后心跳</th>
              </tr>
            </thead>
            <tbody>
              {nodes.length === 0 ? (
                <tr>
                  <td colSpan={6}>{adminToken === "" ? "认证后显示节点状态。" : "暂无节点。"}</td>
                </tr>
              ) : (
                nodes.map((node) => (
                  <tr key={node.nodeId}>
                    <td>
                      <strong>{node.nodeName}</strong>
                      <div className="muted">{node.nodeId}</div>
                    </td>
                    <td>{node.status}</td>
                    <td>{node.agentVersion}</td>
                    <td>{node.activeTunnels}</td>
                    <td>{capabilitySummary(node.capabilities)}</td>
                    <td>{new Date(node.lastSeenAt).toLocaleString()}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

function MetricCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="metric-card">
      <span>{label}</span>
      <strong>{value}</strong>
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
  return active.length > 0 ? active.join(", ") : "无";
}
