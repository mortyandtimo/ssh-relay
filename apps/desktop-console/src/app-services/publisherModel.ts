import type { NodeSummary, TunnelSpec, TunnelProbeResult } from "../../../../packages/desktop-core/src/types";
import { formatDate } from "../../../../packages/desktop-core/src/utils";

export type PublishProtocol = "http" | "https" | "tcp" | "udp" | "socks5";

export type LocalServiceDraft = {
  id: string;
  name: string;
  targetHost: string;
  targetPort: string;
  note: string;
  detectedStatus: "reachable" | "unverified" | "failed";
  lastCheckedAt: string;
};

export type PublishRuleForm = {
  localServiceId: string;
  protocol: PublishProtocol;
  publicPort: string;
  domain: string;
  probePath: string;
  transportPolicy: string;
};

export type RuleStateEvaluation = {
  active: boolean;
  entryUsable: boolean;
  domainReady: boolean;
  runtimeUnavailable: boolean;
  hasFailure: boolean;
  attention: boolean;
  partial: boolean;
  badges: Array<{ label: string; tone: "good" | "danger" | "neutral" }>;
  messages: Array<{ tone: "danger" | "info"; message: string }>;
  nextStep: string;
};

export type CloudEntry = {
  entryType: "port" | "domain" | "domain_pending";
  publicLabel: string;
  publicUrl: string;
  domainReady: boolean;
};

export type VerificationItem = {
  ruleId: string;
  label: string;
  entry: string;
  publicUrl: string;
  summary: string;
  freshness: string;
  probeSupported: boolean;
  openSupported: boolean;
  copySupported: boolean;
  quickCommand: string;
  verificationHint: string;
  tunnel: TunnelSpec;
};

export type DiagnosticItem = {
  id: string;
  label: string;
  detail: string;
  tone: "danger" | "neutral";
  nextStep: string;
};

export type ProtocolCard = {
  key: string;
  label: string;
  state: string;
  tone: "good" | "danger" | "neutral";
  summary: string;
  detail: string;
};

export function protocolCapabilitySummary(node: NodeSummary) {
  const parts: string[] = [];
  if (node.capabilities.httpRelay) parts.push("HTTP");
  if (node.capabilities.httpsRelay || node.capabilities.httpRelay) parts.push("HTTPS");
  if (node.capabilities.tcpRelay) parts.push("TCP");
  if (node.capabilities.udpRelay) parts.push("UDP");
  if (node.capabilities.socks5Connect) parts.push("SOCKS5");
  if (node.capabilities.p2pAssist) parts.push("P2P assist");
  return parts.length > 0 ? parts.join(" / ") : "当前节点未上报协议能力";
}

export function buildProtocolCards(node: NodeSummary | null, tunnels: TunnelSpec[]): ProtocolCard[] {
  const cards = [
    protocolCard("http", "HTTP", Boolean(node?.capabilities.httpRelay), tunnels.filter((item) => item.type === "http")),
    protocolCard("https", "HTTPS", Boolean(node?.capabilities.httpsRelay || node?.capabilities.httpRelay), tunnels.filter((item) => item.type === "https")),
    protocolCard("tcp", "TCP", Boolean(node?.capabilities.tcpRelay), tunnels.filter((item) => item.type === "tcp")),
    protocolCard("udp", "UDP", Boolean(node?.capabilities.udpRelay), tunnels.filter((item) => item.type === "udp")),
    protocolCard("socks5", "SOCKS5", Boolean(node?.capabilities.socks5Connect), tunnels.filter((item) => item.type === "socks5")),
  ];
  cards.push({
    key: "p2p",
    label: "P2P",
    state: "Partial",
    tone: "neutral",
    summary: "P2P 继续按 partial / non-blocking 处理，不作为首版桌面主路径前提。",
    detail: "当前桌面产品仍围绕 HTTP / HTTPS / TCP / UDP / SOCKS5 组织。",
  });
  return cards;
}

export function buildVerificationItems(tunnels: TunnelSpec[], probeResults: Record<string, TunnelProbeResult>, publicHost: string): VerificationItem[] {
  return tunnels.map((tunnel) => {
    const entry = deriveCloudEntry(tunnel, publicHost);
    const probe = probeResults[tunnel.id];
    const state = evaluateRuleState(tunnel);
    const probeSupported = (tunnel.type === "http" || tunnel.type === "https") && state.entryUsable;
    const openSupported = (tunnel.type === "http" || tunnel.type === "https") && state.entryUsable;
    let verificationHint = "";
    if (tunnel.type === "http" || tunnel.type === "https") {
      verificationHint = "支持服务端 HTTP 探测与浏览器直接打开入口。";
    } else if (tunnel.type === "tcp") {
      verificationHint = `TCP 协议不支持浏览器打开，请用命令行验证端口可达性：Windows 执行 Test-NetConnection，Linux/macOS 执行 nc -vz。`;
    } else if (tunnel.type === "udp") {
      verificationHint = "UDP 协议暂不支持服务端标准探测，请用 nc -u 命令行验证。";
    } else if (tunnel.type === "socks5") {
      verificationHint = "SOCKS5 协议暂不支持服务端标准探测，请用 curl --proxy 命令行验证。";
    }
    return {
      ruleId: tunnel.id,
      label: tunnel.name,
      entry: entry.publicLabel,
      publicUrl: entry.publicUrl,
      summary: probe?.success ? "最近验证成功" : probe ? `最近验证失败${probe.error ? ` / ${probe.error}` : ""}` : (tunnel.type === "http" || tunnel.type === "https" ? "尚未执行验证" : "该协议需命令行验证"),
      freshness: probe ? formatDate(probe.probedAt) : "尚未执行验证",
      probeSupported,
      openSupported,
      copySupported: entry.publicLabel !== "",
      quickCommand: buildQuickCommand(tunnel, entry.publicUrl, publicHost),
      verificationHint,
      tunnel,
    };
  });
}

export function buildDiagnosticsItems(tunnels: TunnelSpec[]): DiagnosticItem[] {
  const items: DiagnosticItem[] = [];
  for (const tunnel of tunnels) {
    const state = evaluateRuleState(tunnel);
    if (state.hasFailure) {
      items.push({
        id: tunnel.id + ":failure",
        label: `${tunnel.name} 最近失败`,
        detail: tunnel.lastFailureReason || "最近存在失败，但未给出详细原因。",
        tone: "danger",
        nextStep: state.nextStep,
      });
    } else if (state.runtimeUnavailable) {
      items.push({
        id: tunnel.id + ":runtime",
        label: `${tunnel.name} 运行不可用`,
        detail: `runtimeState=${tunnel.runtimeState || "-"}`,
        tone: "danger",
        nextStep: state.nextStep,
      });
    } else if (!state.entryUsable) {
      items.push({
        id: tunnel.id + ":entry",
        label: `${tunnel.name} 入口尚不可用`,
        detail: `status=${tunnel.status} / domain=${tunnel.domain || "-"}`,
        tone: "neutral",
        nextStep: state.nextStep,
      });
    }
  }
  return items;
}

export function evaluateRuleState(tunnel: TunnelSpec): RuleStateEvaluation {
  const active = tunnel.status === "active";
  const domainReady = tunnel.type !== "https" || Boolean((tunnel.domain || "").trim());
  const entryConfigured = tunnel.type === "https" ? domainReady : tunnel.publicPort > 0;
  const runtimeUnavailable = tunnel.runtimeState === "unavailable";
  const hasFailure = Boolean(tunnel.lastFailureReason);
  const partial = tunnel.transportPolicy === "p2p_preferred";
  const entryUsable = active && entryConfigured && !runtimeUnavailable;
  const attention = !active || !entryConfigured || runtimeUnavailable || hasFailure;
  const badges = [
    { label: active ? "发布中" : tunnel.status, tone: active ? "good" as const : "danger" as const },
    { label: tunnel.runtimeState ? `runtime ${tunnel.runtimeState}` : "runtime 未上报", tone: tunnel.runtimeState === "active" ? "good" as const : tunnel.runtimeState === "unavailable" ? "danger" as const : "neutral" as const },
    { label: tunnel.healthStatus ? `health ${tunnel.healthStatus}` : "health 未上报", tone: tunnel.healthStatus === "healthy" ? "good" as const : tunnel.healthStatus ? "danger" as const : "neutral" as const },
  ];
  const messages: Array<{ tone: "danger" | "info"; message: string }> = [];
  if (!active) {
    messages.push({ tone: "danger", message: "当前发布规则处于非 active 状态，外部入口与验证动作会被降级。" });
  }
  if (hasFailure) {
    messages.push({ tone: "danger", message: `最近失败原因：${tunnel.lastFailureReason}` });
  }
  if (tunnel.type === "https" && !domainReady) {
    messages.push({ tone: "info", message: "HTTPS 当前缺少 domain，标准入口还不能直接对外使用。" });
  }
  if (partial) {
    messages.push({ tone: "info", message: "transportPolicy 为 p2p_preferred，但 P2P 仍按 partial / non-blocking 处理。" });
  }
  let nextStep = "去访问验证页执行 copy / open / probe，确认外部入口已经可用。";
  if (!active) {
    nextStep = "先执行恢复发布动作，再继续对外验证。";
  } else if (tunnel.type === "https" && !domainReady) {
    nextStep = "先补齐 domain，再继续打开入口或执行 HTTPS probe。";
  } else if (runtimeUnavailable) {
    nextStep = "先处理运行不可用问题，再决定是否继续访问公网入口。";
  } else if (hasFailure) {
    nextStep = "先处理最近失败原因，再继续访问验证。";
  }
  return { active, entryUsable, domainReady, runtimeUnavailable, hasFailure, attention, partial, badges, messages, nextStep };
}

export function deriveCloudEntry(tunnel: TunnelSpec, publicHost: string): CloudEntry {
  if (tunnel.type === "https") {
    return {
      entryType: tunnel.domain ? "domain" : "domain_pending",
      publicLabel: tunnel.domain ? `https://${tunnel.domain}` : "https://<待绑定域名>",
      publicUrl: tunnel.domain ? `https://${tunnel.domain}` : "",
      domainReady: Boolean(tunnel.domain),
    };
  }
  if (tunnel.type === "http") {
    const url = `http://${publicHost}:${tunnel.publicPort}`;
    return { entryType: "port", publicLabel: url, publicUrl: url, domainReady: true };
  }
  if (tunnel.type === "udp") {
    const url = `udp://${publicHost}:${tunnel.publicPort}`;
    return { entryType: "port", publicLabel: url, publicUrl: url, domainReady: true };
  }
  if (tunnel.type === "socks5") {
    const url = `socks5://${publicHost}:${tunnel.publicPort}`;
    return { entryType: "port", publicLabel: url, publicUrl: url, domainReady: true };
  }
  const url = `${publicHost}:${tunnel.publicPort}`;
  return { entryType: "port", publicLabel: url, publicUrl: url, domainReady: true };
}

export function buildQuickCommand(tunnel: TunnelSpec, entry: string, publicHost: string) {
  if (tunnel.type === "http" || tunnel.type === "https") {
    return `curl ${entry}${tunnel.probePath ? normalizeProbePath(tunnel.probePath) : ""}`;
  }
  if (tunnel.type === "udp") {
    return `echo -n "ping" | nc -u ${publicHost} ${tunnel.publicPort}`;
  }
  if (tunnel.type === "socks5") {
    return `curl --proxy ${entry} https://example.com -I`;
  }
  return `nc -vz ${publicHost} ${tunnel.publicPort}`;
}

export function buildQuickCommandWindows(tunnel: TunnelSpec, publicHost: string) {
  if (tunnel.type === "http" || tunnel.type === "https") {
    const proto = tunnel.type === "https" ? "https" : "http";
    const domain = tunnel.type === "https" && tunnel.domain ? tunnel.domain : `${publicHost}:${tunnel.publicPort}`;
    return `Invoke-WebRequest ${proto}://${domain}${tunnel.probePath ? normalizeProbePath(tunnel.probePath) : ""}`;
  }
  if (tunnel.type === "tcp") {
    return `Test-NetConnection -ComputerName ${publicHost} -Port ${tunnel.publicPort}`;
  }
  if (tunnel.type === "udp") {
    return `$udp = New-Object System.Net.Sockets.UdpClient; $udp.Connect("${publicHost}", ${tunnel.publicPort}); Write-Host "UDP packet sent"`;
  }
  if (tunnel.type === "socks5") {
    return `curl --proxy socks5://${publicHost}:${tunnel.publicPort} https://example.com -I`;
  }
  return `Test-NetConnection -ComputerName ${publicHost} -Port ${tunnel.publicPort}`;
}

export function protocolEntryHint(protocol: PublishProtocol): string {
  switch (protocol) {
    case "http":
      return "HTTP 协议可通过 http://公网入口服务:端口 直接访问，无需域名。";
    case "https":
      return "HTTPS 协议需要一个可签发 TLS 证书的域名（如 app.example.com），系统会在公网入口侧执行 edge_terminate TLS 终止，再以内网 HTTP 回源到本地服务。不支持仅用裸 IP 端口提供标准 HTTPS。";
    case "tcp":
      return "TCP 协议的公网入口为 公网入口服务:端口，外部访问者连接该地址即可转发到本地服务。无需域名。";
    case "udp":
      return "UDP 协议的公网入口为 公网入口服务:端口，外部访问者向该地址发送 UDP 包即可转发到本地服务。无需域名。";
    case "socks5":
      return "SOCKS5 协议的公网入口为 公网入口服务:端口，外部访问者通过该地址作为 SOCKS5 代理即可使用。无需域名。";
  }
}

export function defaultPublicPortForProtocol(type: PublishProtocol, index: number) {
  if (type === "http") return 18080 + index;
  if (type === "https") return 18443 + index;
  if (type === "udp") return 19053 + index;
  if (type === "socks5") return 11080 + index;
  return 10022 + index;
}

export function normalizeProbePath(value?: string) {
  const trimmed = (value || "/").trim();
  if (!trimmed) return "/";
  return trimmed.startsWith("/") ? trimmed : "/" + trimmed;
}

export function sanitizeSlug(value: string) {
  return value.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "service";
}

function protocolCard(key: string, label: string, supported: boolean, items: TunnelSpec[]): ProtocolCard {
  if (!supported) {
    return { key, label, state: "不可用", tone: "danger", summary: "当前设备没有上报该协议能力。", detail: "不要让这个能力阻塞当前桌面发布主路径。" };
  }
  if (items.length === 0) {
    return { key, label, state: "空", tone: "neutral", summary: "当前协议还没有发布规则。", detail: "可以继续录入本地服务并创建新的发布规则。" };
  }
  const attention = items.some((item) => evaluateRuleState(item).attention);
  if (attention) {
    return { key, label, state: "需关注", tone: "danger", summary: "当前协议下存在 failure / unavailable / 配置不完整规则。", detail: "先处理失败和绑定问题，再继续对外验证。" };
  }
  return { key, label, state: "可用", tone: "good", summary: `当前协议已有 ${items.length} 条发布规则。`, detail: "可以直接进入访问验证执行 copy / open / probe。" };
}
