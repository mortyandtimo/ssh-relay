import { useEffect, useState } from "react";

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

type ServerMetrics = {
  service: string;
  startedAt: string;
  registeredNodes: number;
  onlineNodes: number;
  configuredTunnels: number;
  protocolRelayCount: number;
};

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || "http://localhost:8080";

export default function App() {
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [metrics, setMetrics] = useState<ServerMetrics | null>(null);
  const [error, setError] = useState<string>("");

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const [nodesResponse, metricsResponse] = await Promise.all([
          fetch(`${apiBaseUrl}/api/nodes`),
          fetch(`${apiBaseUrl}/api/server/metrics`),
        ]);

        if (!nodesResponse.ok || !metricsResponse.ok) {
          throw new Error("failed to load management data");
        }

        const nodesPayload = await nodesResponse.json();
        const metricsPayload = await metricsResponse.json();

        if (!cancelled) {
          setNodes(nodesPayload.items || []);
          setMetrics(metricsPayload);
          setError("");
        }
      } catch (loadError) {
        if (!cancelled) {
          setError(loadError instanceof Error ? loadError.message : "unknown error");
        }
      }
    }

    load();
    const timer = window.setInterval(load, 15000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  return (
    <div className="shell">
      <header className="hero">
        <div>
          <p className="eyebrow">Cloud Relay Platform</p>
          <h1>Control plane first, relays split by protocol.</h1>
          <p className="summary">
            This first web console tracks nodes and server health while the TCP, HTTP,
            and HTTPS relays evolve independently.
          </p>
        </div>
        {metrics ? (
          <div className="metrics-grid">
            <MetricCard label="Registered Nodes" value={String(metrics.registeredNodes)} />
            <MetricCard label="Online Nodes" value={String(metrics.onlineNodes)} />
            <MetricCard label="Configured Tunnels" value={String(metrics.configuredTunnels)} />
            <MetricCard label="Relay Services" value={String(metrics.protocolRelayCount)} />
          </div>
        ) : null}
      </header>

      {error ? <div className="error">{error}</div> : null}

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

