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
          throw new Error("failed to load management data");
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
          setError(loadError instanceof Error ? loadError.message : "unknown error");
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
      throw new Error("failed to refresh management data");
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
        const payload = await response.json().catch(() => ({ error: "failed to create tunnel" }));
        throw new Error(payload.error || "failed to create tunnel");
      }
      setTunnelForm((current) => ({ ...initialTunnelForm, nodeId: current.nodeId }));
      setMessage("Tunnel created.");
      await refresh();
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : "failed to create tunnel");
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
        const payload = await response.json().catch(() => ({ error: "failed to update tunnel" }));
        throw new Error(payload.error || "failed to update tunnel");
      }
      setMessage(`Tunnel ${status === "active" ? "enabled" : "paused"}.`);
      await refresh();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : "failed to update tunnel");
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
        const payload = await response.json().catch(() => ({ error: "failed to delete tunnel" }));
        throw new Error(payload.error || "failed to delete tunnel");
      }
      setMessage("Tunnel deleted.");
      await refresh();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : "failed to delete tunnel");
    } finally {
      setBusyAction("");
    }
  }

  return (
    <div className="shell">
      <header className="hero">
        <div>
          <p className="eyebrow">Cloud Relay Platform</p>
          <h1>Minimal relay management loop.</h1>
          <p className="summary">
            Manage nodes and TCP tunnels without hand-written SQL and keep an eye on
            relay runtime state from one page.
          </p>
        </div>
        {metrics ? (
          <div className="metrics-grid">
            <MetricCard label="Registered Nodes" value={String(metrics.registeredNodes)} />
            <MetricCard label="Online Nodes" value={String(metrics.onlineNodes)} />
            <MetricCard label="Configured Tunnels" value={String(metrics.configuredTunnels)} />
            <MetricCard label="Relay Services" value={String(metrics.protocolRelayCount)} />
            <MetricCard label="Total Standby" value={String(relayRuntime?.totalStandby ?? 0)} />
          </div>
        ) : null}
      </header>

      {error ? <div className="error">{error}</div> : null}
      {message ? <div className="notice">{message}</div> : null}

      <section className="panel">
        <div className="panel-header">
          <div>
            <p className="eyebrow">Create Tunnel</p>
            <h2>Expose a node service</h2>
          </div>
        </div>
        <form className="tunnel-form" onSubmit={submitTunnel}>
          <label>
            <span>Node</span>
            <select
              value={tunnelForm.nodeId}
              onChange={(event) => setTunnelForm((current) => ({ ...current, nodeId: event.target.value }))}
              required
            >
              <option value="">Select node</option>
              {nodes.map((node) => (
                <option key={node.nodeId} value={node.nodeId}>
                  {node.nodeName} ({node.nodeId})
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>Name</span>
            <input
              value={tunnelForm.name}
              onChange={(event) => setTunnelForm((current) => ({ ...current, name: event.target.value }))}
              placeholder="windows-16354"
              required
            />
          </label>
          <label>
            <span>Target Host</span>
            <input
              value={tunnelForm.targetHost}
              onChange={(event) => setTunnelForm((current) => ({ ...current, targetHost: event.target.value }))}
              required
            />
          </label>
          <label>
            <span>Target Port</span>
            <input
              value={tunnelForm.targetPort}
              onChange={(event) => setTunnelForm((current) => ({ ...current, targetPort: event.target.value }))}
              inputMode="numeric"
              required
            />
          </label>
          <label>
            <span>Public Port</span>
            <input
              value={tunnelForm.publicPort}
              onChange={(event) => setTunnelForm((current) => ({ ...current, publicPort: event.target.value }))}
              inputMode="numeric"
              required
            />
          </label>
          <button type="submit" disabled={busyAction === "create-tunnel"}>
            {busyAction === "create-tunnel" ? "Creating..." : "Create Tunnel"}
          </button>
        </form>
      </section>

      <section className="panel">
        <div className="panel-header">
          <div>
            <p className="eyebrow">Relay Runtime</p>
            <h2>Standby pool summary</h2>
          </div>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Pool</th>
                <th>Node</th>
                <th>Public Port</th>
                <th>Standby</th>
                <th>Target</th>
                <th>Max</th>
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
                  <td colSpan={6}>No relay pool state available.</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <div className="panel-header">
          <div>
            <p className="eyebrow">Tunnels</p>
            <h2>Configured tunnel inventory</h2>
          </div>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Node</th>
                <th>Status</th>
                <th>Public</th>
                <th>Target</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {tunnels.length === 0 ? (
                <tr>
                  <td colSpan={6}>No tunnels configured.</td>
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
                          Enable
                        </button>
                        <button
                          type="button"
                          disabled={busyAction === `${tunnel.id}:paused` || tunnel.status === "paused"}
                          onClick={() => updateTunnelStatus(tunnel, "paused")}
                        >
                          Pause
                        </button>
                        <button
                          type="button"
                          className="danger"
                          disabled={busyAction === `${tunnel.id}:delete`}
                          onClick={() => deleteTunnel(tunnel)}
                        >
                          Delete
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
            <p className="eyebrow">Nodes</p>
            <h2>Online state and capability surface</h2>
          </div>
        </div>

        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Node</th>
                <th>Status</th>
                <th>Agent</th>
                <th>Tunnels</th>
                <th>Capabilities</th>
                <th>Last Seen</th>
              </tr>
            </thead>
            <tbody>
              {nodes.length === 0 ? (
                <tr>
                  <td colSpan={6}>No nodes registered yet.</td>
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
