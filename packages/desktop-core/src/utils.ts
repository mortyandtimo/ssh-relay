import type { ControlActionOption, ControlActionResponse, NodeCapabilities, NodeSummary, TunnelSpec } from "./types";

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

export type ControlResultCategory =
	| "accepted_real"
	| "accepted_placeholder"
	| "blocked"
	| "stale_context"
	| "duplicate_inflight"
	| "duplicate_handled"
	| "retryable_failure"
	| "non_retryable_failure"
	| "policy_rejected"
	| "rejected_generic";

export function classifyControlResult(result: ControlActionResponse): ControlResultCategory {
	if (result.result === "accepted") {
		return result.placeholderOnly ? "accepted_placeholder" : "accepted_real";
	}
	if (result.result === "blocked") {
		return "blocked";
	}
	const message = (result.humanMessage || "").toLowerCase();
	const notes = (result.executionNotes || []).map((item) => (item.message || "").toLowerCase()).join(" ");
	const combined = message + " " + notes;
	if (combined.includes("刷新") || combined.includes("状态已变化")) return "stale_context";
	if (combined.includes("处理中") || combined.includes("请勿重复提交")) return "duplicate_inflight";
	if (combined.includes("已处理完成") || combined.includes("避免重复执行")) return "duplicate_handled";
	if (combined.includes("可稍后重试") || combined.includes("建议稍后重试") || combined.includes("retry")) return "retryable_failure";
	if (combined.includes("不建议重试")) return "non_retryable_failure";
	if (result.executionMode === "placeholder" && !result.placeholderOnly) return "policy_rejected";
	return "rejected_generic";
}

export function controlResultCategoryLabel(category: ControlResultCategory) {
	switch (category) {
		case "accepted_real":
			return "真实执行已受理";
		case "accepted_placeholder":
			return "占位执行已受理";
		case "blocked":
			return "预检阻断";
		case "stale_context":
			return "上下文已过期";
		case "duplicate_inflight":
			return "处理中";
		case "duplicate_handled":
			return "已处理完成";
		case "retryable_failure":
			return "可重试失败";
		case "non_retryable_failure":
			return "不可重试失败";
		case "policy_rejected":
			return "策略拒绝";
		default:
			return "执行被拒绝";
	}
}

export function controlResultCategoryTone(category: ControlResultCategory) {
	switch (category) {
		case "accepted_real":
			return "good";
		case "accepted_placeholder":
			return "neutral";
		case "blocked":
			return "danger";
		case "retryable_failure":
			return "warn";
		default:
			return "danger";
	}
}

export function controlResultNextStep(category: ControlResultCategory) {
	switch (category) {
		case "accepted_real":
			return "观察最新目标状态和审计记录，确认真实执行结果已经反映到页面。";
		case "accepted_placeholder":
			return "当前只经过占位执行边界；如需真实动作，请确认该动作是否已接入真实执行路径。";
		case "blocked":
			return "先处理阻断原因，再重新读取控制摘要。";
		case "stale_context":
			return "请先刷新控制面板或动作列表，再基于新的上下文重新发起动作。";
		case "duplicate_inflight":
			return "等待当前执行结果返回，不要在同一上下文下重复点击。";
		case "duplicate_handled":
			return "该上下文动作已经处理完成；刷新后再决定是否需要新的动作。";
		case "retryable_failure":
			return "可稍后重试；若连续失败，请结合审计与执行说明排查。";
		case "non_retryable_failure":
			return "当前不建议直接重试；请先修正环境或策略条件。";
		case "policy_rejected":
			return "请先处理策略拒绝原因，再决定是否重新发起动作。";
		default:
			return "请结合执行说明与审计记录继续排查。";
	}
}

export function controlOptionTone(option: ControlActionOption) {
	if (option.availabilityState === "blocked") return "danger";
	if (option.availabilityState === "placeholder_only") return "neutral";
	return "good";
}

export function controlOptionStateLabel(option: ControlActionOption) {
	if (option.availabilityState === "blocked") return "阻断";
	if (option.availabilityState === "placeholder_only") return "占位执行";
	return "可用";
}

export type ControlResultDisplay = {
	tone: string;
	category: ControlResultCategory;
	categoryLabel: string;
	message: string;
	nextStep: string;
	executionMode: string;
	dryRunOnly: string;
	placeholderOnly: boolean;
	checks: Array<{ code: string; label: string; state: CheckState; stateLabel: string; stateTone: string; message: string }>;
	blockedReasons: Array<{ code: string; message: string }>;
	executionNotes: Array<{ code: string; message: string }>;
};

export function formatControlResultDisplay(result: ControlActionResponse): ControlResultDisplay {
	const category = classifyControlResult(result);
	return {
		tone: controlResultCategoryTone(category),
		category,
		categoryLabel: controlResultCategoryLabel(category),
		message: result.humanMessage,
		nextStep: controlResultNextStep(category),
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
