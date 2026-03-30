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

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || "http://localhost:8080";

const initialTunnelForm: TunnelForm = {
  nodeId: "",
  name: "",
  targetHost: "127.0.0.1",
  targetPort: "",
  publicPort: "",
};

export default function App() {
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [tunnels, setTunnels] = useState<TunnelSpec[]>([]);
  const [metrics, setMetrics] = useState<ServerMetrics | null>(null);
  const [relayRuntime, setRelayRuntime] = useState<RelayRuntimeSummary | null>(null);
  const [tunnelForm, setTunnelForm] = useState<TunnelForm>(initialTunnelForm);
  const [error, setError] = useState<string>("");
  const [message, setMessage] = useState<string>("");
  const [busyAction, setBusyAction] = useState<string>("");

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const [nodesResponse, tunnelsResponse, metricsResponse, relayResponse] = await Promise.all([
          fetch(`${apiBaseUrl}/api/nodes`),
          fetch(`${apiBaseUrl}/api/tunnels`),
          fetch(`${apiBaseUrl}/api/server/metrics`),
          fetch(`${apiBaseUrl}/api/relay/tcp/runtime`),
        ]);

        if (!nodesResponse.ok || !tunnelsResponse.ok || !metricsResponse.ok || !relayResponse.ok) {
          throw new Error("加载管理数据失败");
        }

        const nodesPayload = await nodesResponse.json();
        const tunnelsPayload = await tunnelsResponse.json();
        const metricsPayload = await metricsResponse.json();
        const relayPayload = await relayResponse.json();

        if (!cancelled) {
          const nodeItems = nodesPayload.items || [];
          setNodes(nodeItems);
          setTunnels(tunnelsPayload.items || []);
          setMetrics(metricsPayload);
          setRelayRuntime(relayPayload);
          setTunnelForm((current) => ({
            ...current,
            nodeId: current.nodeId || nodeItems[0]?.nodeId || "",
          }));
          setError("");
        }
      } catch (loadError) {
        if (!cancelled) {
          setError(loadError instanceof Error ? loadError.message : "未知错误");
        }
      }
    }

    void load();
    const timer = window.setInterval(load, 10000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  async function refresh() {
    const [nodesResponse, tunnelsResponse, metricsResponse, relayResponse] = await Promise.all([
      fetch(`${apiBaseUrl}/api/nodes`),
      fetch(`${apiBaseUrl}/api/tunnels`),
      fetch(`${apiBaseUrl}/api/server/metrics`),
      fetch(`${apiBaseUrl}/api/relay/tcp/runtime`),
    ]);
    if (!nodesResponse.ok || !tunnelsResponse.ok || !metricsResponse.ok || !relayResponse.ok) {
      throw new Error("刷新管理数据失败");
    }
    const nodesPayload = await nodesResponse.json();
    const tunnelsPayload = await tunnelsResponse.json();
    const metricsPayload = await metricsResponse.json();
    const relayPayload = await relayResponse.json();
    setNodes(nodesPayload.items || []);
    setTunnels(tunnelsPayload.items || []);
    setMetrics(metricsPayload);
    setRelayRuntime(relayPayload);
  }

  async function submitTunnel(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusyAction("create-tunnel");
    setError("");
    setMessage("");
    try {
      const response = await fetch(`${apiBaseUrl}/api/tunnels`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
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
      if (!response.ok) {
        const payload = await response.json().catch(() => ({ error: "创建隧道失败" }));
        throw new Error(payload.error || "创建隧道失败");
      }
      setTunnelForm((current) => ({ ...initialTunnelForm, nodeId: current.nodeId }));
	      setMessage("隧道已创建。");
      await refresh();
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : "创建隧道失败");
    } finally {
      setBusyAction("");
    }
  }

  async function updateTunnelStatus(tunnel: TunnelSpec, status: "active" | "paused") {
    const actionKey = `${tunnel.id}:${status}`;
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      const response = await fetch(`${apiBaseUrl}/api/tunnels/${tunnel.id}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
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
      if (!response.ok) {
        const payload = await response.json().catch(() => ({ error: "更新隧道失败" }));
        throw new Error(payload.error || "更新隧道失败");
      }
      setMessage(status === "active" ? "隧道已启用。" : "隧道已暂停。");
      await refresh();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : "更新隧道失败");
    } finally {
      setBusyAction("");
    }
  }

  async function deleteTunnel(tunnel: TunnelSpec) {
    const actionKey = `${tunnel.id}:delete`;
    setBusyAction(actionKey);
    setError("");
    setMessage("");
    try {
      const response = await fetch(`${apiBaseUrl}/api/tunnels/${tunnel.id}`, { method: "DELETE" });
      if (!response.ok) {
        const payload = await response.json().catch(() => ({ error: "删除隧道失败" }));
        throw new Error(payload.error || "删除隧道失败");
      }
      setMessage("隧道已删除。");
      await refresh();
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
          <p className="eyebrow">Cloud Relay Platform</p>
          <h1>最小可用管理闭环</h1>
          <p className="summary">
            直接管理节点和 TCP 隧道，不再依赖手写 SQL，并在一个页面里查看中继运行状态。
          </p>
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
            <p className="eyebrow">创建隧道</p>
            <h2>暴露节点本地服务</h2>
          </div>
        </div>
        <form className="tunnel-form" onSubmit={submitTunnel}>
          <label>
            <span>节点</span>
            <select
              value={tunnelForm.nodeId}
              onChange={(event) => setTunnelForm((current) => ({ ...current, nodeId: event.target.value }))}
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
          <button type="submit" disabled={busyAction === "create-tunnel"}>
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
                  <td colSpan={6}>暂无中继池状态。</td>
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
                  <td colSpan={6}>暂无隧道。</td>
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
                          disabled={busyAction === `${tunnel.id}:active` || tunnel.status === "active"}
                          onClick={() => updateTunnelStatus(tunnel, "active")}
                        >
                          启用
                        </button>
                        <button
                          type="button"
                          disabled={busyAction === `${tunnel.id}:paused` || tunnel.status === "paused"}
                          onClick={() => updateTunnelStatus(tunnel, "paused")}
                        >
                          暂停
                        </button>
                        <button
                          type="button"
                          className="danger"
                          disabled={busyAction === `${tunnel.id}:delete`}
                          onClick={() => deleteTunnel(tunnel)}
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
                  <td colSpan={6}>暂无节点。</td>
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
  return active.length > 0 ? active.join(", ") : "none";
}
