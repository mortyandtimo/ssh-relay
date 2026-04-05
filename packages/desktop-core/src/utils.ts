import type { ControlActionResponse, NodeCapabilities, NodeSummary, TunnelSpec } from "./types";

export type CheckState = "pass" | "missing" | "blocked";

export type SafetyCheckItem = {
  label: string;
  state: CheckState;
  detail: string;
};

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
        status: "success",
        sourceLabel: "VITE_DESKTOP_NODE_ID",
        reason: "已通过 VITE_DESKTOP_NODE_ID 明确绑定到本机节点 " + desktopNodeId + "。",
        nextAction: "当前已经命中明确 nodeId，可继续使用 node-console 查看本机状态与本机隧道。",
      };
    }
    return {
      node: null,
      status: "config_error",
        sourceLabel: "VITE_DESKTOP_NODE_ID",
        reason: "当前配置了 VITE_DESKTOP_NODE_ID=" + desktopNodeId + "，但当前节点列表中没有命中该 nodeId。",
        nextAction: "请检查当前环境变量里的 nodeId 是否写对，或确认该节点已经向当前后端成功上报。",
    };
  }

  const managedLocalNodes = nodes.filter((node) => node.nodeRole === "local" && node.instanceManaged);
  if (managedLocalNodes.length === 1) {
    return {
      node: managedLocalNodes[0],
      status: "success",
      sourceLabel: "唯一受管 local 节点",
      reason: "当前账号下只检测到一个受管 local 节点，已可确定性绑定。",
      nextAction: "当前已经通过唯一受管 local 节点完成绑定，可继续使用 node-console。",
    };
  }
  if (managedLocalNodes.length === 0) {
    return {
      node: null,
      status: "unbound",
      sourceLabel: "无",
      reason: "没有检测到明确可绑定的受管 local 节点。",
      nextAction: "请优先配置 VITE_DESKTOP_NODE_ID，或让当前账号下只有一个 instanceManaged=true 且 nodeRole=local 的节点。",
    };
  }
  return {
    node: null,
    status: "ambiguous",
    sourceLabel: "唯一受管 local 节点",
    reason: "当前检测到 " + managedLocalNodes.length + " 个受管 local 节点，无法确定本机归属。",
    nextAction: "请显式配置 VITE_DESKTOP_NODE_ID，避免在多本地节点账号下继续歧义绑定。",
  };
}

export function checkStateLabel(state: CheckState) {
  if (state === "pass") return "通过";
  if (state === "missing") return "缺失";
  return "阻断";
}

export function checkStateTone(state: CheckState) {
  if (state === "pass") return "good";
  if (state === "missing") return "warn";
  return "danger";
}

export function controlResultTone(result: ControlActionResponse["result"]) {
	if (result === "accepted") return "good";
	if (result === "blocked") return "danger";
	return "warn";
}

export type ControlResultDisplay = {
	tone: string;
	message: string;
	executionMode: string;
	dryRunOnly: string;
	placeholderOnly: boolean;
	checks: Array<{ code: string; label: string; state: CheckState; stateLabel: string; stateTone: string; message: string }>;
	blockedReasons: Array<{ code: string; message: string }>;
	executionNotes: Array<{ code: string; message: string }>;
};

export function formatControlResultDisplay(result: ControlActionResponse): ControlResultDisplay {
	return {
		tone: controlResultTone(result.result),
		message: result.humanMessage,
		executionMode: result.executionMode,
		dryRunOnly: String(result.dryRunOnly),
		placeholderOnly: Boolean(result.placeholderOnly),
		checks: result.preflight.items.map((item) => ({
			code: item.code,
			label: item.label,
			state: item.state,
			stateLabel: checkStateLabel(item.state),
			stateTone: checkStateTone(item.state),
			message: item.message,
		})),
		blockedReasons: (result.preflight.blockedReasons || []).map((reason) => ({
			code: reason.code,
			message: reason.message,
		})),
		executionNotes: (result.executionNotes || []).map((note) => ({
			code: note.code,
			message: note.message,
		})),
	};
}
