import type { NodeCapabilities, NodeSummary, TunnelSpec } from "./types";

export const tunnelTabs = ["tcp", "udp", "http", "https", "socks5"] as const;

export function capabilitySummary(capabilities: NodeCapabilities) {
  const active = [] as string[];
  if (capabilities.tcpRelay) active.push("TCP");
  if (capabilities.udpRelay) active.push("UDP");
  if (capabilities.httpRelay) active.push("HTTP");
  if (capabilities.httpsRelay || capabilities.httpRelay) active.push("HTTPS");
  if (capabilities.socks5Connect) active.push("SOCKS5");
  if (capabilities.p2pAssist) active.push("P2P assist");
  return active.length > 0 ? active.join(" / ") : "无能力上报";
}

export function nodeAgentDeploymentLabel(node: NodeSummary) {
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

export function statusClass(status: string) {
  return status === "online" ? "status-chip good" : "status-chip danger";
}

export function formatDate(value: string) {
  if (!value) return "-";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString();
}

export function publicEntry(tunnel: TunnelSpec) {
  if (tunnel.type === "http" || tunnel.type === "https") {
    return tunnel.domain || "尚未配置域名";
  }
  if (tunnel.publicPort) {
    return String(tunnel.publicPort);
  }
  return "-";
}

export function runtimeLabel(tunnel: TunnelSpec) {
  if (tunnel.runtimePath || tunnel.runtimeState) {
    return [tunnel.runtimePath || "尚无", tunnel.runtimeState || "尚无"].join(" / ");
  }
  return "尚无运行态上报";
}

export function resolveLocalNodeBinding(nodes: NodeSummary[], desktopNodeId: string) {
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
