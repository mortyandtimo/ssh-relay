import { FormEvent, ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { listen } from "@tauri-apps/api/event";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { createDesktopApi } from "../../../packages/desktop-core/src/api";
import type {
  CertificateSpec,
  ControlActionOption,
  ControlActionRequest,
  ControlActionResponse,
  ControlPanelSummary,
  ManagedHTTPSDomain,
  NodeSummary,
  ServerMetrics,
  TunnelSpec,
  TunnelProbeResult,
  UserSummary,
} from "../../../packages/desktop-core/src/types";
import { formatDate } from "../../../packages/desktop-core/src/utils";
import {
  buildDiagnosticsItems,
  buildQuickCommand,
  buildQuickCommandWindows,
  defaultPublicPortForProtocol,
  deriveCloudEntry,
  evaluateRuleState,
  normalizeProbePath,
  protocolEntryHint,
  type CloudEntry,
  type DiagnosticItem,
  type LocalServiceDraft,
  type PublishProtocol,
  type PublishRuleForm,
} from "./app-services/publisherModel";
import { ControlResultBlock } from "./controlResultBlock";
import { appExit, createTauriDesktopTransport, decryptLoginPassword, deleteLoginProfile, ensureRuntimeStarted, loadAgentTraffic, loadAppConfig, loadAutoStartEnabled, loadDesktopAppUsage, loadDesktopHostPaths, loadP2PRuntimeStatus, loadRuntimeStatus, loadTrafficHistory, openDesktopExternal, openP2PRuntimeLog, openRuntimeLog, readLoginProfiles, recordTrafficDelta, saveAppConfig, saveLoginProfile, setAutoStart, startP2PRuntime, stopP2PRuntime, stopRuntime, syncP2PServiceForwarders, windowMinimize, windowRequestClose, windowStartDrag, windowToggleMaximize, type AgentTrafficSnapshot, type AppConfig, type DesktopAppUsage, type LoginProfilesFile, type P2PRuntimeStatus, type P2PServiceForwarderRuleInput, type RuntimeStatus, type TrafficHistoryDayEntry, type TrafficHistorySnapshot } from "./desktopHost";

type DesktopWindowEnv = {
  apiBaseUrl?: string;
  publicEntryHost?: string;
};

declare global {
  interface Window {
    __DESKTOP_ENV__?: DesktopWindowEnv;
  }
}

type TunnelEditForm = {
  name: string;
  targetHost: string;
  targetPort: string;
  publicPort: string;
  domain: string;
  probePath: string;
  transportPolicy: string;
  serviceKey: string;
  serviceTitle: string;
  serviceKind: "app" | "drive" | "gallery";
  serviceSummary: string;
  servicePublicUrl: string;
  serviceP2PUrl: string;
  serviceP2PNodeId: string;
  serviceP2PTargetPort: string;
  serviceP2PPath: string;
  serviceCloudAccess: "all_users" | "admin_only" | "disabled";
  serviceP2PAccess: "all_users" | "admin_only" | "disabled";
  servicePreferredPath: "dual" | "cloud" | "p2p";
};

type ServiceMetadataDraft = {
  serviceKey: string;
  serviceTitle: string;
  serviceKind: "app" | "drive" | "gallery";
  serviceSummary: string;
  servicePublicUrl: string;
  serviceP2PUrl: string;
  serviceP2PNodeId: string;
  serviceP2PTargetPort: string;
  serviceP2PPath: string;
  serviceCloudAccess: "all_users" | "admin_only" | "disabled";
  serviceP2PAccess: "all_users" | "admin_only" | "disabled";
  servicePreferredPath: "dual" | "cloud" | "p2p";
};

type ServiceTemplateKey = "drive" | "gallery";

type RelayEndpointPreset = {
  id: string;
  label: string;
  apiBaseUrl: string;
  publicHost: string;
  note: string;
};

type RouteMeta = {
  path: string;
  key: "dashboard" | "services" | "publish" | "diagnostics" | "p2p" | "settings";
  label: string;
  icon: string;
};


type DrawerState =
  | { kind: "rule"; tunnelId: string }
  | { kind: "create-rule" }
  | { kind: "service"; serviceId: string | null }
  | null;

const desktopPublisherApiBaseUrl = "https://publisher.manage.020309.top";
const desktopPublisherHost = "publisher.manage.020309.top";
const desktopManagePortalHost = "manage.020309.top";
const defaultPublisherP2PPeerUrl = "tcp://easytier.manage.020309.top:11010\nudp://easytier.manage.020309.top:11010";
const emptyTrafficHistory: TrafficHistorySnapshot = {
  nodeId: "",
  currentMonth: "",
  days: [],
  months: [],
};

const emptyP2PRuntimeStatus: P2PRuntimeStatus = {
  available: false,
  configured: false,
  running: false,
  pid: null,
  startedAt: null,
  executablePath: "",
  workDir: "",
  stdoutLogPath: "",
  stderrLogPath: "",
  argsSummary: "",
  lastError: "",
  machineId: "",
  rpcPortal: "",
  nodeHostname: "",
  virtualIpv4: "",
  instanceId: "",
  peerCount: 0,
  connectedPeers: [],
};

function normalizePublisherAppConfig(config?: AppConfig | null): AppConfig {
  return {
    closeAction: (config?.closeAction || "ask") as "ask" | "tray" | "exit",
    silentStart: Boolean(config?.silentStart),
    autoStart: Boolean(config?.autoStart),
    p2pAutoStart: Boolean(config?.p2pAutoStart),
    p2pNetworkName: (config?.p2pNetworkName || "").trim() || "cloud-relay",
    p2pNetworkSecret: (config?.p2pNetworkSecret || "").trim(),
    p2pPeerUrl: typeof config?.p2pPeerUrl === "string" && config.p2pPeerUrl.trim()
      ? config.p2pPeerUrl.trim()
      : defaultPublisherP2PPeerUrl,
    p2pVirtualIpv4: (config?.p2pVirtualIpv4 || "").trim(),
    p2pUseDhcp: config?.p2pUseDhcp !== false,
    p2pInstanceName: (config?.p2pInstanceName || "").trim() || "cloud-relay-publisher",
    p2pHostname: (config?.p2pHostname || "").trim(),
  };
}

function formatStartedAt(timestamp?: number | null) {
  if (!timestamp) return "未启动";
  return new Date(timestamp).toLocaleString();
}

function p2pRuntimeLabel(status?: P2PRuntimeStatus | null) {
  if (!status) return "读取中";
  if (status.running && status.peerCount > 0) return `已联网 (${status.peerCount})`;
  if (status.running) return "运行中";
  if (!status.available) return "未打包 easytier-core";
  if (!status.configured) return "待配置";
  return "已停止";
}

function normalizeDesktopApiBaseUrl(value?: string) {
  const trimmed = (value || "").trim();
  if (!trimmed) return "";
  const normalized = trimmed.replace(/\/+$/, "");
  if (normalized === desktopManagePortalHost) {
    return desktopPublisherApiBaseUrl;
  }
  if (!/^https?:\/\//i.test(normalized)) {
    return normalized;
  }
  try {
    const parsed = new URL(normalized);
    if (parsed.hostname === desktopManagePortalHost) {
      return desktopPublisherApiBaseUrl;
    }
  } catch {
    // keep custom values as-is; connect validation handles invalid input later
  }
  return normalized;
}

function normalizeDesktopPublicHost(value?: string) {
  const trimmed = (value || "").trim();
  if (!trimmed) return "";
  return trimmed === desktopManagePortalHost ? desktopPublisherHost : trimmed;
}

const runtimeDesktopEnv = typeof window !== "undefined" ? window.__DESKTOP_ENV__ || {} : {};
const runtimeInjectedApiBaseUrl = normalizeDesktopApiBaseUrl(runtimeDesktopEnv.apiBaseUrl || "");
const runtimeInjectedPublicEntryHost = normalizeDesktopPublicHost(runtimeDesktopEnv.publicEntryHost || "");
const hasRuntimeInjectedApiBaseUrl = Boolean(runtimeInjectedApiBaseUrl);
const desktopNodeId = (import.meta.env.VITE_DESKTOP_NODE_ID || "").trim();
const defaultDesktopPublicHost = (() => {
  const configured = normalizeDesktopPublicHost(runtimeInjectedPublicEntryHost || import.meta.env.VITE_PUBLIC_ENTRY_HOST || "");
  if (configured) return configured;
  if (typeof window !== "undefined") {
    const hostname = normalizeDesktopPublicHost(window.location.hostname.trim());
    if (hostname) return hostname;
  }
  return desktopPublisherHost;
})();

const savedRelayEndpoint = (() => {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem("desktop-publisher-relay-endpoint");
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { apiBaseUrl?: string; publicHost?: string };
    return {
      apiBaseUrl: normalizeDesktopApiBaseUrl(parsed.apiBaseUrl),
      publicHost: normalizeDesktopPublicHost(parsed.publicHost),
    };
  } catch {
    return null;
  }
})();

const publisherRoutes: RouteMeta[] = [
  { path: "/dashboard", key: "dashboard", label: "仪表盘", icon: "fas fa-tachometer-alt" },
  { path: "/services", key: "services", label: "本地服务", icon: "fas fa-network-wired" },
  { path: "/publish", key: "publish", label: "发布管理", icon: "fas fa-sitemap" },
  { path: "/diagnostics", key: "diagnostics", label: "诊断中心", icon: "fas fa-bug" },
  { path: "/p2p", key: "p2p", label: "P2P 网络", icon: "fas fa-project-diagram" },
  { path: "/settings", key: "settings", label: "设置", icon: "fas fa-cog" },
];

const protocolList: PublishProtocol[] = ["http", "https", "tcp", "udp", "socks5"];
const serviceMetadataKeys = [
  "serviceKey",
  "serviceTitle",
  "serviceKind",
  "serviceSummary",
  "servicePublicUrl",
  "serviceP2PUrl",
  "serviceP2PNodeId",
  "serviceP2PTargetPort",
  "serviceP2PPath",
  "serviceCloudAccess",
  "serviceP2PAccess",
  "servicePreferredPath",
] as const;

const emptyServiceMetadataDraft: ServiceMetadataDraft = {
  serviceKey: "",
  serviceTitle: "",
  serviceKind: "app",
  serviceSummary: "",
  servicePublicUrl: "",
  serviceP2PUrl: "",
  serviceP2PNodeId: "",
  serviceP2PTargetPort: "",
  serviceP2PPath: "",
  serviceCloudAccess: "all_users",
  serviceP2PAccess: "all_users",
  servicePreferredPath: "dual",
};

function suggestedPublicPortValue(protocol: PublishProtocol, index: number) {
  const suggested = defaultPublicPortForProtocol(protocol, index);
  return suggested > 0 ? String(suggested) : "";
}

function publishPublicPortLabel(protocol: PublishProtocol | string) {
  return protocol === "https" ? "内部端口（可留空）" : "公网端口";
}

function serializePublishPublicPort(protocol: PublishProtocol | string, publicPort: string) {
  const trimmed = publicPort.trim();
  if (protocol === "https") {
    return trimmed ? Number(trimmed) : 0;
  }
  return Number(trimmed);
}

function extractTunnelServiceFields(metadata?: Record<string, string>): ServiceMetadataDraft {
  return {
    serviceKey: metadata?.serviceKey || "",
    serviceTitle: metadata?.serviceTitle || "",
    serviceKind: (metadata?.serviceKind || "app") as "app" | "drive" | "gallery",
    serviceSummary: metadata?.serviceSummary || "",
    servicePublicUrl: metadata?.servicePublicUrl || "",
    serviceP2PUrl: metadata?.serviceP2PUrl || "",
    serviceP2PNodeId: metadata?.serviceP2PNodeId || "",
    serviceP2PTargetPort: metadata?.serviceP2PTargetPort || "",
    serviceP2PPath: metadata?.serviceP2PPath || "",
    serviceCloudAccess: (metadata?.serviceCloudAccess || "all_users") as "all_users" | "admin_only" | "disabled",
    serviceP2PAccess: (metadata?.serviceP2PAccess || "all_users") as "all_users" | "admin_only" | "disabled",
    servicePreferredPath: (metadata?.servicePreferredPath || "dual") as "dual" | "cloud" | "p2p",
  };
}

function buildTunnelServiceMetadata(baseMetadata: Record<string, string> | undefined, form: ServiceMetadataDraft) {
  const next = { ...(baseMetadata || {}) };
  for (const key of serviceMetadataKeys) {
    delete next[key];
  }
  if (!form.serviceKey.trim()) {
    return next;
  }
  next.serviceKey = form.serviceKey.trim().toLowerCase();
  if (form.serviceTitle.trim()) next.serviceTitle = form.serviceTitle.trim();
  if (form.serviceKind.trim()) next.serviceKind = form.serviceKind.trim();
  if (form.serviceSummary.trim()) next.serviceSummary = form.serviceSummary.trim();
  if (form.servicePublicUrl.trim()) next.servicePublicUrl = form.servicePublicUrl.trim();
  if (form.serviceP2PUrl.trim()) next.serviceP2PUrl = form.serviceP2PUrl.trim();
  if (form.serviceP2PNodeId.trim()) next.serviceP2PNodeId = form.serviceP2PNodeId.trim();
  if (form.serviceP2PTargetPort.trim()) next.serviceP2PTargetPort = form.serviceP2PTargetPort.trim();
  if (form.serviceP2PPath.trim()) next.serviceP2PPath = form.serviceP2PPath.trim();
  next.serviceCloudAccess = form.serviceCloudAccess;
  next.serviceP2PAccess = form.serviceP2PAccess;
  next.servicePreferredPath = form.servicePreferredPath;
  return next;
}

function serviceKindLabel(kind?: string) {
  switch (kind) {
    case "drive":
      return "网盘";
    case "gallery":
      return "图床";
    case "app":
      return "通用应用";
    default:
      return kind || "未指定";
  }
}

function serviceAccessLabel(value?: string) {
  switch (value) {
    case "admin_only":
      return "仅管理员";
    case "disabled":
      return "已禁用";
    default:
      return "全部用户";
  }
}

function servicePreferredPathLabel(value?: string) {
  switch (value) {
    case "cloud":
      return "优先云端";
    case "p2p":
      return "优先 P2P";
    case "dual":
      return "双入口";
    default:
      return value || "未指定";
  }
}

function buildServiceTemplate(template: ServiceTemplateKey, targetPort: string): Partial<ServiceMetadataDraft> {
  const normalizedPort = targetPort.trim();
  if (template === "drive") {
    return {
      serviceKey: "drive",
      serviceTitle: "网盘服务",
      serviceKind: "drive",
      serviceSummary: "公网入口保留目录与分享，用户端工作台只通过 P2P 下载与访问。",
      serviceCloudAccess: "admin_only",
      serviceP2PAccess: "all_users",
      servicePreferredPath: "dual",
      serviceP2PTargetPort: normalizedPort,
      serviceP2PPath: "/",
    };
  }
  return {
    serviceKey: "gallery",
    serviceTitle: "图床服务",
    serviceKind: "gallery",
    serviceSummary: "公网 HTTPS 继续承担展示与轻量访问，批量上传与下载通过 P2P 工作台接入。",
    serviceCloudAccess: "all_users",
    serviceP2PAccess: "all_users",
    servicePreferredPath: "dual",
    serviceP2PTargetPort: normalizedPort,
    serviceP2PPath: "/",
  };
}

function buildDesiredP2PServiceForwarders(tunnels: TunnelSpec[]): P2PServiceForwarderRuleInput[] {
  return tunnels
    .filter((tunnel) => tunnel.status === "active" && (tunnel.type === "http" || tunnel.type === "https"))
    .map((tunnel) => {
      const service = extractTunnelServiceFields(tunnel.metadata);
      if (!service.serviceKey.trim() || service.serviceP2PAccess === "disabled") {
        return null;
      }
      const listenPort = Number(service.serviceP2PTargetPort || tunnel.targetPort || 0);
      const targetPort = Number(tunnel.targetPort || 0);
      if (!listenPort || listenPort <= 0 || !targetPort || targetPort <= 0) {
        return null;
      }
      return {
        tunnelId: tunnel.id,
        serviceKey: service.serviceKey.trim().toLowerCase(),
        serviceTitle: service.serviceTitle.trim() || tunnel.name,
        targetHost: tunnel.targetHost || "127.0.0.1",
        targetPort,
        listenPort,
      } satisfies P2PServiceForwarderRuleInput;
    })
    .filter((item): item is P2PServiceForwarderRuleInput => Boolean(item));
}

const initialLocalServiceForm = {
  name: "",
  targetHost: "127.0.0.1",
  targetPort: "",
  note: "",
};

const initialPublishRuleForm: PublishRuleForm = {
  localServiceId: "",
  protocol: "http",
  publicPort: "",
  domain: "",
  probePath: "/",
  transportPolicy: "relay_only",
  ...emptyServiceMetadataDraft,
};

const relayEndpointPresets: RelayEndpointPreset[] = (() => {
  const items: RelayEndpointPreset[] = [];
  const seenApiBaseUrls = new Set<string>();

  const pushPreset = (preset: RelayEndpointPreset | null) => {
    if (!preset) return;
    const normalizedApiBaseUrl = normalizeDesktopApiBaseUrl(preset.apiBaseUrl);
    const normalizedPublicHost = normalizeDesktopPublicHost(preset.publicHost);
    if (preset.id !== "custom") {
      if (!normalizedApiBaseUrl || seenApiBaseUrls.has(normalizedApiBaseUrl)) return;
      seenApiBaseUrls.add(normalizedApiBaseUrl);
    }
    items.push({
      ...preset,
      apiBaseUrl: normalizedApiBaseUrl,
      publicHost: normalizedPublicHost,
    });
  };

  pushPreset(hasRuntimeInjectedApiBaseUrl
    ? {
        id: "runtime",
        label: "当前环境",
        apiBaseUrl: runtimeInjectedApiBaseUrl,
        publicHost: runtimeInjectedPublicEntryHost,
        note: "",
      }
    : null);

  pushPreset(savedRelayEndpoint?.apiBaseUrl
    ? {
        id: "recent",
        label: "最近使用",
        apiBaseUrl: (savedRelayEndpoint.apiBaseUrl || "").trim(),
        publicHost: (savedRelayEndpoint.publicHost || "").trim(),
        note: "",
      }
    : null);

  pushPreset({
    id: "publisher-manage",
    label: "驻阡陌管理域名",
    apiBaseUrl: "https://publisher.manage.020309.top",
    publicHost: "publisher.manage.020309.top",
    note: "推荐默认入口",
  });

  pushPreset({
    id: "custom",
    label: "自定义",
    apiBaseUrl: "",
    publicHost: "",
    note: "",
  });

  return items;
})();

const initialRelayEndpointPreset = relayEndpointPresets[0];

const emptyRuntimeStatus: RuntimeStatus = {
  available: false,
  running: false,
  healthy: false,
  pid: null,
  startedAt: null,
  nodeId: "",
  nodeName: "",
  apiBaseUrl: "",
  relayTcpUrl: "",
  relayUdpUrl: "",
  executablePath: "",
  workDir: "",
  stdoutLogPath: "",
  stderrLogPath: "",
  lastError: "",
};

const emptyUsage: DesktopAppUsage = {
  available: false,
  cpuPercent: null,
  memoryMb: null,
  readBytes: null,
  writeBytes: null,
  sampledAt: Date.now(),
};

export default function App() {
  const location = useLocation();
  const navigate = useNavigate();
  const [bootstrapRequired, setBootstrapRequired] = useState<boolean | null>(hasRuntimeInjectedApiBaseUrl ? null : false);
  const [currentUser, setCurrentUser] = useState<UserSummary | null>(null);
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [tunnels, setTunnels] = useState<TunnelSpec[]>([]);
  const [serverMetrics, setServerMetrics] = useState<ServerMetrics | null>(null);
  const [localServices, setLocalServices] = useState<LocalServiceDraft[]>([]);
  const [localServiceForm, setLocalServiceForm] = useState(initialLocalServiceForm);
  const [publishForm, setPublishForm] = useState<PublishRuleForm>(initialPublishRuleForm);
  const [selectedRelayPresetId, setSelectedRelayPresetId] = useState(initialRelayEndpointPreset.id);
  const [apiDraft, setApiDraft] = useState(initialRelayEndpointPreset.apiBaseUrl);
  const [cloudPublicHost, setCloudPublicHost] = useState(initialRelayEndpointPreset.publicHost || defaultDesktopPublicHost);
  const [connectionReady, setConnectionReady] = useState(hasRuntimeInjectedApiBaseUrl);
  const [connectionStatus, setConnectionStatus] = useState<"idle" | "checking" | "connected" | "failed">(hasRuntimeInjectedApiBaseUrl ? "connected" : "idle");
  const [drawerState, setDrawerState] = useState<DrawerState>(null);
  const [editForm, setEditForm] = useState<TunnelEditForm | null>(null);
  const [probeResults, setProbeResults] = useState<Record<string, TunnelProbeResult>>({});
  const [tunnelActionOptions, setTunnelActionOptions] = useState<ControlActionOption[]>([]);
  const [tunnelControlPanel, setTunnelControlPanel] = useState<ControlPanelSummary | null>(null);
  const [tunnelControlContextAt, setTunnelControlContextAt] = useState("");
  const [controlNote, setControlNote] = useState("");
  const [controlResult, setControlResult] = useState<ControlActionResponse | null>(null);
  const [loginForm, setLoginForm] = useState({ email: "", password: "" });
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [toastState, setToastState] = useState<{ text: string; tone: "info" | "danger"; phase: "show" | "fade" | "gone" } | null>(null);
  const toastTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [syncIssue, setSyncIssue] = useState("");
  const [busy, setBusy] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [lastRefreshAt, setLastRefreshAt] = useState("");
  const [desktopHostPaths, setDesktopHostPaths] = useState({ configDir: "读取中...", logDir: "读取中...", available: false });
  const [runtimeStatus, setRuntimeStatus] = useState<RuntimeStatus>(emptyRuntimeStatus);
  const [p2pStatus, setP2PStatus] = useState<P2PRuntimeStatus>(emptyP2PRuntimeStatus);
  const [appUsage, setAppUsage] = useState<DesktopAppUsage>(emptyUsage);
  const [trafficRate, setTrafficRate] = useState({ downRate: 0, upRate: 0 });
  const [agentTraffic, setAgentTraffic] = useState<AgentTrafficSnapshot | null>(null);
  const [tunnelRates, setTunnelRates] = useState<Map<string, { downRate: number; upRate: number }>>(new Map());
  const [trafficHistory, setTrafficHistory] = useState<TrafficHistorySnapshot>(emptyTrafficHistory);
  const [trafficCalendarOpen, setTrafficCalendarOpen] = useState(false);
  const [trafficCalendarMonth, setTrafficCalendarMonth] = useState(currentMonthKey(new Date()));
  const agentTrafficPrevRef = useRef<{ sampledAt: number; downTotal: number; upTotal: number; tunnels: Map<string, { downBytes: number; upBytes: number }> } | null>(null);
  const refreshInFlightRef = useRef(false);
  const runtimePollInFlightRef = useRef(false);
  const p2pPollInFlightRef = useRef(false);
  const usagePollInFlightRef = useRef(false);
  const trafficPollInFlightRef = useRef(false);
  const appUsageSampleRef = useRef<DesktopAppUsage | null>(null);
  const [loginProfiles, setLoginProfiles] = useState<LoginProfilesFile | null>(null);
  const [savePassword, setSavePassword] = useState(false);
  const [autoLogin, setAutoLogin] = useState(false);
  const [appConfig, setAppConfig] = useState<AppConfig>(normalizePublisherAppConfig(null));
  const [p2pBusy, setP2PBusy] = useState(false);
  const [closeDialogOpen, setCloseDialogOpen] = useState(false);
  const [profileListOpen, setProfileListOpen] = useState(false);
  const [deleteConfirmTarget, setDeleteConfirmTarget] = useState<string | null>(null);
  const [certificates, setCertificates] = useState<CertificateSpec[]>([]);
  const [certFormDomain, setCertFormDomain] = useState("");
  const [certFormCert, setCertFormCert] = useState("");
  const [certFormKey, setCertFormKey] = useState("");
  const [certBusy, setCertBusy] = useState(false);
  const [managedHTTPSDomains, setManagedHTTPSDomains] = useState<ManagedHTTPSDomain[]>([]);
  const managedDomainsRefreshingRef = useRef(false);
  const managedHTTPSDomainValues = useMemo(() => managedHTTPSDomains.map((item) => item.domain), [managedHTTPSDomains]);

  function clearToastTimers() {
    if (toastTimerRef.current) { clearTimeout(toastTimerRef.current); toastTimerRef.current = null; }
    if (fadeTimerRef.current) { clearTimeout(fadeTimerRef.current); fadeTimerRef.current = null; }
  }

  function toastAutoDismissMs(text: string) {
    // 1.5s–3s based on character count: ~80ms per char, clamped
    return Math.max(1500, Math.min(3000, 800 + text.length * 80));
  }

  function showToast(text: string, tone: "info" | "danger") {
    clearToastTimers();
    setToastState({ text, tone, phase: "show" });
    const ms = toastAutoDismissMs(text);
    toastTimerRef.current = setTimeout(() => {
      setToastState((prev) => prev ? { ...prev, phase: "fade" } : null);
      fadeTimerRef.current = setTimeout(() => {
        setToastState(null);
        fadeTimerRef.current = null;
      }, 500);
      toastTimerRef.current = null;
    }, ms);
  }

  function handleToastMouseEnter() {
    clearToastTimers();
    setToastState((prev) => prev ? { ...prev, phase: "show" } : null);
  }

  function handleToastMouseLeave() {
    if (!toastState || toastState.phase === "gone") return;
    clearToastTimers();
    const ms = toastAutoDismissMs(toastState.text);
    toastTimerRef.current = setTimeout(() => {
      setToastState((prev) => prev ? { ...prev, phase: "fade" } : null);
      fadeTimerRef.current = setTimeout(() => {
        setToastState(null);
        fadeTimerRef.current = null;
      }, 500);
      toastTimerRef.current = null;
    }, ms);
  }

  // Bridge: whenever error or message changes, show a toast
  useEffect(() => {
    if (error) showToast(error, "danger");
  }, [error]);
  useEffect(() => {
    if (message) showToast(message, "info");
  }, [message]);

  // Cleanup toast timers on unmount
  useEffect(() => () => clearToastTimers(), []);

  // Load login profiles and app config on mount; attempt auto-login
  const autoLoginAttemptedRef = useRef(false);
  useEffect(() => {
    if (autoLoginAttemptedRef.current) return;
    autoLoginAttemptedRef.current = true;
    void (async () => {
      const [profiles, config, autoStartEnabled] = await Promise.all([
        readLoginProfiles(),
        loadAppConfig(),
        desktopTransport ? loadAutoStartEnabled() : Promise.resolve(false),
      ]);
      if (profiles) setLoginProfiles(profiles);
      if (config) {
        setAppConfig({
          ...normalizePublisherAppConfig(config),
          autoStart: desktopTransport ? autoStartEnabled : Boolean(config.autoStart),
        });
      } else if (desktopTransport) {
        setAppConfig((current) => ({ ...normalizePublisherAppConfig(current), autoStart: autoStartEnabled }));
      }
      // Window bounds are restored by Rust setup before first paint
      // Pre-fill last used email and check if it has a saved password
      if (profiles?.profiles.length) {
        const lastEmail = profiles.lastUsedEmail || profiles.profiles[0].email;
        const lastProfile = profiles.profiles.find((p) => p.email === lastEmail);
        setLoginForm((prev) => ({ ...prev, email: lastEmail }));
        if (desktopTransport && lastProfile) {
          try {
            const pw = await decryptLoginPassword(lastEmail);
            if (pw) {
              setLoginForm((prev) => ({ ...prev, password: pw }));
              setSavePassword(true);
              setAutoLogin(lastProfile.autoLogin);
            } else {
              setSavePassword(false);
              setAutoLogin(false);
            }
          } catch {
            setSavePassword(false);
            setAutoLogin(false);
          }
        }
      }
      // Auto-login at mount: only if we already have an API base URL (e.g. from runtime env)
      // If not, it will be retried when connection becomes ready (see effect below)
    })();
  }, []);

  const normalizedApiDraft = normalizeDesktopApiBaseUrl(apiDraft);
  const normalizedCloudPublicHost = normalizeDesktopPublicHost(cloudPublicHost) || defaultDesktopPublicHost;
  const effectiveApiBaseUrl = hasRuntimeInjectedApiBaseUrl ? runtimeInjectedApiBaseUrl : connectionReady ? normalizedApiDraft : "";
  const desktopTransport = useMemo(() => createTauriDesktopTransport(), []);
  const desktopApi = useMemo(() => createDesktopApi(effectiveApiBaseUrl, desktopTransport || undefined), [effectiveApiBaseUrl, desktopTransport]);

  // Auto-login when connection becomes ready (user selects relay)
  const autoLoginOnConnectRef = useRef(false);
  useEffect(() => {
    if (autoLoginOnConnectRef.current) return;
    if (!connectionReady || !effectiveApiBaseUrl || currentUser) return;
    const autoProfile = loginProfiles?.profiles.find((p) => p.autoLogin);
    if (!autoProfile) return;
    autoLoginOnConnectRef.current = true;
    void (async () => {
      try {
        const password = await decryptLoginPassword(autoProfile.email);
        const auth = await desktopApi.login(autoProfile.email, password);
        setCurrentUser(auth.user);
        if (!runtimeReady && effectiveApiBaseUrl) {
          await startEmbeddedRuntime(effectiveApiBaseUrl, false);
        }
        await loadSnapshot();
        setLoginForm({ email: autoProfile.email, password });
        setSavePassword(true);
        setAutoLogin(true);
      } catch { /* auto-login failed */ }
    })();
  }, [connectionReady, effectiveApiBaseUrl]);

  const reloadCertificateState = useCallback(async () => {
    const [certificatesResult, managedDomainsResult] = await Promise.allSettled([
      desktopApi.listCertificates(),
      desktopApi.listManagedHTTPSDomains(),
    ]);
    if (certificatesResult.status === "fulfilled") {
      setCertificates(certificatesResult.value.items || []);
    }
    if (managedDomainsResult.status === "fulfilled") {
      setManagedHTTPSDomains(managedDomainsResult.value.items || []);
    } else {
      setManagedHTTPSDomains([]);
    }
  }, [desktopApi]);

  const refreshManagedHTTPSDomains = useCallback(async () => {
    if (!currentUser || managedDomainsRefreshingRef.current) return;
    managedDomainsRefreshingRef.current = true;
    try {
      const result = await desktopApi.listManagedHTTPSDomains();
      setManagedHTTPSDomains(result.items || []);
    } finally {
      managedDomainsRefreshingRef.current = false;
    }
  }, [currentUser, desktopApi]);

  useEffect(() => {
    if (!currentUser) return;
    void reloadCertificateState();
  }, [currentUser, reloadCertificateState]);

  useEffect(() => {
    if (!desktopTransport) return;
    let unlisten: undefined | (() => void);
    void listen<string>("app-already-open", (event) => {
      showToast(event.payload || "驻阡陌已经开启。", "info");
    }).then((dispose) => {
      unlisten = dispose;
    }).catch(() => {
      unlisten = undefined;
    });
    return () => {
      unlisten?.();
    };
  }, [desktopTransport]);

  useEffect(() => {
    if (!desktopTransport) return;
    let unlisten: undefined | (() => void);
    void listen("app-close-requested", () => {
      if (appConfig.closeAction === "ask") {
        setCloseDialogOpen(true);
      } else if (appConfig.closeAction === "tray") {
        void windowRequestClose();
      } else {
        void appExit();
      }
    }).then((dispose) => {
      unlisten = dispose;
    }).catch(() => {
      unlisten = undefined;
    });
    return () => {
      unlisten?.();
    };
  }, [desktopTransport, appConfig.closeAction]);

  const runtimeNodeId = runtimeStatus.nodeId.trim();
  const runtimeRunning = runtimeStatus.available && runtimeStatus.running;
  const runtimeReady = runtimeRunning && runtimeStatus.healthy;
  const runtimeRelayReady = runtimeReady && Boolean(runtimeStatus.relayTcpUrl);

  useEffect(() => {
    if (location.pathname === "/") {
      navigate("/dashboard", { replace: true });
      return;
    }
    if (!publisherRoutes.some((route) => route.path === location.pathname)) {
      navigate("/dashboard", { replace: true });
    }
  }, [location.pathname, navigate]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const stored = window.localStorage.getItem("desktop-publisher-local-services");
    if (!stored) return;
    try {
      const parsed = JSON.parse(stored) as LocalServiceDraft[];
      if (Array.isArray(parsed)) {
        setLocalServices(parsed);
      }
    } catch {
      setLocalServices([]);
    }
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem("desktop-publisher-local-services", JSON.stringify(localServices));
  }, [localServices]);


  useEffect(() => {
    let cancelled = false;
    async function loadRuntime() {
      if (runtimePollInFlightRef.current) return;
      runtimePollInFlightRef.current = true;
      try {
        const payload = await loadRuntimeStatus();
        if (!cancelled) {
          setRuntimeStatus((prev) => {
            if (prev.available === payload.available && prev.running === payload.running &&
                prev.healthy === payload.healthy && prev.nodeId === payload.nodeId &&
                prev.relayTcpUrl === payload.relayTcpUrl && prev.lastError === payload.lastError) {
              return prev;
            }
            return payload;
          });
        }
      } finally {
        runtimePollInFlightRef.current = false;
      }
    }
    void loadRuntime();
    const timer = window.setInterval(() => {
      void loadRuntime();
    }, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    async function loadP2P() {
      if (p2pPollInFlightRef.current) return;
      p2pPollInFlightRef.current = true;
      try {
        const payload = await loadP2PRuntimeStatus();
        if (!cancelled) {
          setP2PStatus((prev) => {
            if (
              prev.available === payload.available &&
              prev.configured === payload.configured &&
              prev.running === payload.running &&
              prev.pid === payload.pid &&
              prev.peerCount === payload.peerCount &&
              prev.virtualIpv4 === payload.virtualIpv4 &&
              prev.lastError === payload.lastError
            ) {
              return prev;
            }
            return payload;
          });
        }
      } finally {
        p2pPollInFlightRef.current = false;
      }
    }
    void loadP2P();
    const timer = window.setInterval(() => {
      void loadP2P();
    }, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    async function loadPaths() {
      const payload = await loadDesktopHostPaths();
      if (!cancelled) {
        setDesktopHostPaths(payload);
      }
    }
    void loadPaths();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    const runtimeNode = runtimeStatus.nodeId.trim();

    async function syncTrafficHistory() {
      if (!currentUser || !runtimeNode) {
        if (!cancelled) {
          setTrafficHistory(emptyTrafficHistory);
          setTrafficCalendarMonth(currentMonthKey(new Date()));
        }
        return;
      }
      const payload = await loadTrafficHistory(runtimeNode);
      if (!cancelled) {
        setTrafficHistory(payload);
        setTrafficCalendarMonth((current) => current || payload.currentMonth || currentMonthKey(new Date()));
      }
    }

    void syncTrafficHistory();
    return () => {
      cancelled = true;
    };
  }, [currentUser, runtimeStatus.nodeId]);

  useEffect(() => {
    let cancelled = false;

    async function pollUsage() {
      if (usagePollInFlightRef.current) return;
      usagePollInFlightRef.current = true;
      try {
        const payload = await loadDesktopAppUsage();
        if (cancelled) return;
        const prev = appUsageSampleRef.current;
        const cpuChanged = !prev || Math.abs((payload.cpuPercent || 0) - (prev.cpuPercent || 0)) > 1;
        const memChanged = !prev || Math.abs((payload.memoryMb || 0) - (prev.memoryMb || 0)) > 1;
        if (cpuChanged || memChanged) {
          appUsageSampleRef.current = payload;
          setAppUsage(payload);
        }
      } finally {
        usagePollInFlightRef.current = false;
      }
    }

    void pollUsage();
    const timer = window.setInterval(() => {
      void pollUsage();
    }, 2000);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    const runtimeNode = runtimeStatus.nodeId.trim();

    async function pollTraffic() {
      if (!currentUser || !runtimeRunning || trafficPollInFlightRef.current) return;
      trafficPollInFlightRef.current = true;
      try {
        const snapshot = await loadAgentTraffic();
        if (cancelled || !snapshot) return;

        const now = Date.now();
        const prev = agentTrafficPrevRef.current;
        const totalsChanged = !prev || prev.downTotal !== snapshot.downTotal || prev.upTotal !== snapshot.upTotal;

        if (totalsChanged) {
          setAgentTraffic(snapshot);
        }

        if (prev && now > prev.sampledAt && totalsChanged) {
          const seconds = (now - prev.sampledAt) / 1000;
          if (seconds > 0) {
            const newRates = new Map<string, { downRate: number; upRate: number }>();
            for (const t of snapshot.tunnels) {
              const prevT = prev.tunnels.get(t.tunnelId);
              newRates.set(t.tunnelId, {
                downRate: prevT ? Math.max(0, t.downBytes - prevT.downBytes) / seconds : 0,
                upRate: prevT ? Math.max(0, t.upBytes - prevT.upBytes) / seconds : 0,
              });
            }
            setTunnelRates(newRates);

            const downRate = Math.max(0, snapshot.downTotal - prev.downTotal) / seconds;
            const upRate = Math.max(0, snapshot.upTotal - prev.upTotal) / seconds;
            setTrafficRate({ downRate, upRate });

            const downDelta = Math.max(0, snapshot.downTotal - prev.downTotal);
            const upDelta = Math.max(0, snapshot.upTotal - prev.upTotal);
            if ((downDelta > 0 || upDelta > 0) && runtimeNode) {
              try {
                const history = await recordTrafficDelta(runtimeNode, downDelta, upDelta, snapshot.sampledAt || now);
                if (!cancelled) {
                  setTrafficHistory(history);
                  setTrafficCalendarMonth((current) => current || history.currentMonth || currentMonthKey(new Date()));
                }
              } catch {
                // ignore persistence failures; live traffic should remain visible
              }
            }
          }
        }

        const tunnelMap = new Map<string, { downBytes: number; upBytes: number }>();
        for (const t of snapshot.tunnels) {
          tunnelMap.set(t.tunnelId, { downBytes: t.downBytes, upBytes: t.upBytes });
        }
        agentTrafficPrevRef.current = { sampledAt: now, downTotal: snapshot.downTotal, upTotal: snapshot.upTotal, tunnels: tunnelMap };
      } finally {
        trafficPollInFlightRef.current = false;
      }
    }

    if (!currentUser || !runtimeRunning) {
      setAgentTraffic(null);
      setTunnelRates(new Map());
      setTrafficRate({ downRate: 0, upRate: 0 });
      agentTrafficPrevRef.current = null;
      return () => {
        cancelled = true;
      };
    }

    void pollTraffic();
    const timer = window.setInterval(() => {
      void pollTraffic();
    }, 1500);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [currentUser, runtimeRunning, runtimeStatus.nodeId]);

  async function loadSnapshot() {
    const [payload, metrics] = await Promise.all([
      desktopApi.loadDesktopData(),
      currentUser?.role === "admin" ? desktopApi.loadServerMetrics().catch(() => null) : Promise.resolve(null),
    ]);
    // Only update if data actually changed (shallow compare by JSON)
    setNodes((prev) => {
      const next = payload.nodes;
      if (prev.length === next.length && prev.every((n, i) => n.nodeId === next[i].nodeId && n.status === next[i].status)) return prev;
      return next;
    });
    setTunnels((prev) => {
      const next = payload.tunnels;
      if (prev.length === next.length && prev.every((t, i) => t.id === next[i].id && t.status === next[i].status && t.runtimeState === next[i].runtimeState)) return prev;
      return next;
    });
    setServerMetrics(metrics);
    setLastRefreshAt(new Date().toISOString());
  }

  useEffect(() => {
    let cancelled = false;

    async function init() {
      if (!connectionReady) return;
      try {
        const status = await desktopApi.loadBootstrapStatus();
        if (cancelled) return;
        setBootstrapRequired(status.required);
        if (status.required) {
          setError("仍需先完成初始化配置，请通过管理面完成。");
          return;
        }
        try {
          const me = await desktopApi.loadCurrentUser();
          if (cancelled) return;
          setCurrentUser(me.user);
          await loadSnapshot();
          if (cancelled) return;
          setError("");
        } catch (authError) {
          const detail = authError instanceof Error ? authError.message : "登录已失效，请重新登录。";
          if (!cancelled && /登录已失效|401/.test(detail)) {
            setCurrentUser(null);
            setNodes([]);
            setTunnels([]);
            setServerMetrics(null);
            setError("");
            return;
          }
          throw authError;
        }
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
  }, [connectionReady, desktopApi]);

  useEffect(() => {
    if (!currentUser) return;
    const timer = window.setInterval(() => {
      void refreshPublisherData(false, "auto");
    }, 10000);
    return () => window.clearInterval(timer);
  }, [currentUser]);

  const currentDevice = useMemo(() => {
    if (runtimeNodeId) {
      return nodes.find((node) => node.nodeId === runtimeNodeId) ?? null;
    }
    if (desktopNodeId) {
      return nodes.find((node) => node.nodeId === desktopNodeId) ?? null;
    }
    const localManaged = nodes.filter((node) => node.instanceManaged && node.nodeRole === "local");
    if (localManaged.length === 1) return localManaged[0];
    return localManaged[0] ?? nodes[0] ?? null;
  }, [nodes, runtimeNodeId]);

  const runtimeBoundDevice = useMemo(() => {
    if (runtimeNodeId) {
      return nodes.find((node) => node.nodeId === runtimeNodeId) ?? null;
    }
    if (desktopNodeId) {
      return nodes.find((node) => node.nodeId === desktopNodeId) ?? null;
    }
    return null;
  }, [nodes, runtimeNodeId]);
  const p2pCloudMetrics = useMemo<Record<string, string>>(
    () => runtimeBoundDevice?.latestMetrics || currentDevice?.latestMetrics || {},
    [currentDevice?.latestMetrics, runtimeBoundDevice?.latestMetrics],
  );

  const publishableTunnels = useMemo(() => {
    if (!runtimeBoundDevice) return [];
    return tunnels.filter((tunnel) => tunnel.nodeId === runtimeBoundDevice.nodeId);
  }, [runtimeBoundDevice, tunnels]);
  const p2pCandidateRules = useMemo(
    () => publishableTunnels.filter((item) => item.transportPolicy === "p2p_preferred"),
    [publishableTunnels],
  );

  useEffect(() => {
    if (!currentUser) return;
    const items = p2pStatus?.running ? buildDesiredP2PServiceForwarders(publishableTunnels) : [];
    void syncP2PServiceForwarders(items).catch(() => {
      // host-side P2P forwarder sync is best-effort; surface failures later in dedicated runtime UI
    });
  }, [currentUser, p2pStatus?.running, p2pStatus?.virtualIpv4, publishableTunnels]);

  const diagnosticsItems = useMemo(() => buildDiagnosticsItems(publishableTunnels), [publishableTunnels]);
  const activeRulesCount = useMemo(() => publishableTunnels.filter((item) => item.status === "active").length, [publishableTunnels]);
  const attentionCount = useMemo(() => diagnosticsItems.filter((item) => item.tone === "danger").length, [diagnosticsItems]);
  const currentRoute = useMemo(() => findRouteMeta(location.pathname), [location.pathname]);
  const cloudEndpointLabel = effectiveApiBaseUrl || normalizedApiDraft;
  const cloudEndpointDisplayName = connectionReady ? relayEndpointPresets.find((p) => p.apiBaseUrl.replace(/\/$/, "") === cloudEndpointLabel)?.label || "云站点" : "未连接";
  const runtimeBindingIssue = useMemo(() => {
    if (!runtimeRelayReady) {
      return runtimeStatus.lastError || "本地发布运行时未就绪，请先连接云站点并确认 runtime 已启动。";
    }
    if (!runtimeStatus.nodeId.trim()) {
      return "当前 runtime 还没有稳定 nodeId，暂时不能创建或修改发布规则。";
    }
    if (!runtimeBoundDevice) {
      return `等待当前发布器节点注册并上线（runtime nodeId: ${runtimeStatus.nodeId}）。`;
    }
    if (runtimeBoundDevice.status !== "online") {
      return `当前发布器节点 ${runtimeBoundDevice.nodeName} (${runtimeBoundDevice.nodeId}) 还未在线，暂时不能创建或修改发布规则。`;
    }
    return "";
  }, [runtimeBoundDevice, runtimeRelayReady, runtimeStatus.lastError, runtimeStatus.nodeId]);


  const globalNextStep = useMemo(() => {
    if (!currentUser) return "先登录并绑定当前设备。";
    if (runtimeBindingIssue) return runtimeBindingIssue;
    if (localServices.length === 0) return "先录入至少一个本地服务。";
    if (publishableTunnels.length === 0) return "下一步为一个本地服务新建发布规则。";
    const firstDanger = diagnosticsItems.find((item) => item.tone === "danger");
    if (firstDanger) return firstDanger.nextStep;
    return "所有规则运行正常。";
  }, [currentUser, diagnosticsItems, localServices.length, publishableTunnels.length, runtimeBindingIssue]);

  const dashboardActionLabel = useMemo(() => {
    if (localServices.length === 0) return "添加服务";
    if (publishableTunnels.length === 0) return "新建规则";
    if (diagnosticsItems.length > 0) return "查看诊断";
    return "查看设置";
  }, [diagnosticsItems.length, localServices.length, publishableTunnels.length]);

  const userInitial = useMemo(() => {
    const source = (currentUser?.displayName || currentUser?.email || "C").trim();
    return Array.from(source)[0]?.toUpperCase() || "C";
  }, [currentUser]);

  const localTrafficTotals = useMemo(() => {
    if (!agentTraffic) return { downTotal: 0, upTotal: 0 };
    return { downTotal: agentTraffic.downTotal, upTotal: agentTraffic.upTotal };
  }, [agentTraffic]);

  const localTrafficByTunnel = useMemo(() => {
    const map = new Map<string, { downBytes: number; upBytes: number }>();
    if (agentTraffic) {
      for (const entry of agentTraffic.tunnels) {
        map.set(entry.tunnelId, { downBytes: entry.downBytes, upBytes: entry.upBytes });
      }
    }
    return map;
  }, [agentTraffic]);



  const effectiveTrafficRate = useMemo(() => {
    if (publishableTunnels.length === 0) {
      return { downRate: 0, upRate: 0 };
    }
    return trafficRate;
  }, [publishableTunnels.length, trafficRate]);
  const currentTrafficMonth = useMemo(
    () => trafficHistory.currentMonth || currentMonthKey(new Date()),
    [trafficHistory.currentMonth],
  );
  const currentMonthTrafficSummary = useMemo(
    () => trafficHistory.months.find((item) => item.month === currentTrafficMonth) || null,
    [currentTrafficMonth, trafficHistory.months],
  );
  const effectiveTrafficTotal = useMemo(
    () => currentMonthTrafficSummary?.totalBytes || 0,
    [currentMonthTrafficSummary],
  );
  const downRateLabel = useMemo(() => formatRate(effectiveTrafficRate.downRate), [effectiveTrafficRate.downRate]);
  const upRateLabel = useMemo(() => formatRate(effectiveTrafficRate.upRate), [effectiveTrafficRate.upRate]);
  const totalTrafficLabel = useMemo(() => formatBytesTotal(effectiveTrafficTotal), [effectiveTrafficTotal]);
  const currentRouteLabel = useMemo(() => `${currentRoute.label} · ${currentDevice?.nodeName || "当前设备未绑定"}`, [currentDevice, currentRoute.label]);
  const selectedTrafficMonthSummary = useMemo(
    () => trafficHistory.months.find((item) => item.month === trafficCalendarMonth) || null,
    [trafficCalendarMonth, trafficHistory.months],
  );
  const trafficMonthDays = useMemo(
    () => buildTrafficCalendar(trafficCalendarMonth, trafficHistory.days),
    [trafficCalendarMonth, trafficHistory.days],
  );

  const drawerTunnel = useMemo(() => {
    if (!drawerState || drawerState.kind !== "rule") return null;
    return publishableTunnels.find((item) => item.id === drawerState.tunnelId) ?? null;
  }, [drawerState, publishableTunnels]);
  const drawerTunnelTrafficTotals = useMemo(() => {
    if (!drawerTunnel) return { downTotal: 0, upTotal: 0 };
    const entry = localTrafficByTunnel.get(drawerTunnel.id);
    if (entry) return { downTotal: entry.downBytes, upTotal: entry.upBytes };
    return { downTotal: 0, upTotal: 0 };
  }, [drawerTunnel, localTrafficByTunnel]);

  const drawerRuleState = drawerTunnel ? evaluateRuleState(drawerTunnel) : null;
  const drawerCloudEntry = drawerTunnel ? deriveCloudEntry(drawerTunnel, normalizedCloudPublicHost) : null;

  useEffect(() => {
    if (!currentUser) return;
    if (drawerState?.kind === "create-rule" && publishForm.protocol === "https") {
      void refreshManagedHTTPSDomains();
      return;
    }
    if (drawerState?.kind === "rule" && drawerTunnel?.type === "https") {
      void refreshManagedHTTPSDomains();
    }
  }, [currentUser, drawerState, publishForm.protocol, drawerTunnel?.id, drawerTunnel?.type, refreshManagedHTTPSDomains]);

  useEffect(() => {
    if (!publishForm.localServiceId && localServices[0]) {
      setPublishForm((current) => ({ ...current, localServiceId: localServices[0].id }));
    }
  }, [localServices, publishForm.localServiceId]);

  useEffect(() => {
    if (!publishForm.publicPort && publishForm.protocol !== "https") {
      setPublishForm((current) => ({
        ...current,
        publicPort: suggestedPublicPortValue(current.protocol, publishableTunnels.length),
      }));
    }
  }, [publishForm.protocol, publishForm.publicPort, publishableTunnels.length]);

  useEffect(() => {
    if (!drawerTunnel) {
      setEditForm(null);
      setTunnelActionOptions([]);
      setTunnelControlPanel(null);
      return;
    }
    setEditForm({
      name: drawerTunnel.name,
      targetHost: drawerTunnel.targetHost || "",
      targetPort: String(drawerTunnel.targetPort || ""),
      publicPort: String(drawerTunnel.publicPort || ""),
      domain: drawerTunnel.domain || "",
      probePath: drawerTunnel.probePath || "/",
      transportPolicy: drawerTunnel.transportPolicy || "relay_only",
      ...extractTunnelServiceFields(drawerTunnel.metadata),
    });
  }, [drawerTunnel]);

  useEffect(() => {
    let cancelled = false;

    async function loadRuleExecution() {
      if (!drawerTunnel || !currentUser || drawerState?.kind !== "rule") return;
      try {
        const [options, panel] = await Promise.all([
          desktopApi.loadTunnelControlActionOptions(drawerTunnel.id, "node_console"),
          desktopApi.loadTunnelControlPanel(drawerTunnel.id, "node_console"),
        ]);
        if (cancelled) return;
        setTunnelActionOptions(options.items.filter((item) => item.actionKind === "pause_tunnel" || item.actionKind === "resume_tunnel"));
        setTunnelControlPanel(panel);
        setTunnelControlContextAt(panel.contextVersion || options.contextVersion || options.items[0]?.contextVersion || "");
      } catch (controlError) {
        if (cancelled) return;
        const detail = controlError instanceof Error ? controlError.message : "读取发布规则执行摘要失败";
        setTunnelActionOptions([]);
        if (/404|page not found/i.test(detail)) {
          setTunnelControlPanel({
            targetKind: "tunnel",
            targetId: drawerTunnel.id,
            sourceSurface: "node_console",
            headline: "当前环境未提供规则动作摘要",
            summary: "已保留发布规则展示，不再把缺失的控制摘要接口当成全局错误反复弹出。",
            readinessState: "partial",
            checks: [],
            nextStep: "继续使用发布、访问验证和诊断主路径；如需暂停/恢复，再补服务端动作摘要接口。",
            executionMode: "real",
          });
          setError("");
        } else {
          setTunnelControlPanel(null);
          setError(detail);
        }
      }
    }

    void loadRuleExecution();
    return () => {
      cancelled = true;
    };
  }, [currentUser, desktopApi, drawerState, drawerTunnel]);

  async function startEmbeddedRuntime(apiBaseUrlValue: string, showSuccessMessage: boolean) {
    const normalizedApi = normalizeDesktopApiBaseUrl(apiBaseUrlValue);
    if (!normalizedApi) {
      throw new Error("请先填写云站点 API 地址");
    }
    const existingNodeName = runtimeStatus.nodeName.trim();
    const fallbackNodeName = typeof window !== "undefined" ? window.navigator.userAgent.includes("Windows") ? "windows-publisher" : "desktop-publisher" : "desktop-publisher";
    const requestedNodeName = existingNodeName || fallbackNodeName;
    const nextStatus = await ensureRuntimeStarted({
      apiBaseUrl: normalizedApi,
      nodeId: runtimeStatus.nodeId,
      nodeName: requestedNodeName,
    });
    setRuntimeStatus(nextStatus);
    if (!nextStatus.healthy) {
      throw new Error(nextStatus.lastError || "本地发布运行时未就绪");
    }
    if (showSuccessMessage) {
      setMessage(`云站点连接成功，本地发布运行时已启动（node: ${nextStatus.nodeId}）。`);
    }
  }

  async function validateCloudConnection() {
    setBusy("connect-cloud");
    setError("");
    setMessage("");
    setConnectionStatus("checking");
    try {
      const normalizedApi = normalizeDesktopApiBaseUrl(apiDraft);
      if (!normalizedApi) {
        throw new Error("请先填写云站点 API 地址");
      }

      let response: Awaited<ReturnType<NonNullable<typeof desktopTransport>>> | Response;
      try {
        response = await (desktopTransport
          ? desktopTransport(normalizedApi + "/api/auth/bootstrap-status", { headers: { Accept: "application/json" } })
          : fetch(normalizedApi + "/api/auth/bootstrap-status", { headers: { Accept: "application/json" } }));
      } catch (requestError) {
        const detail = requestError instanceof Error ? requestError.message : String(requestError || "");
        throw new Error(detail ? `无法连接到云站点 API：${detail}` : "无法连接到云站点 API，请检查地址、端口和网络连通性。");
      }

      const payload = await response.json().catch(() => null) as { error?: unknown; required?: unknown } | null;
      if (!response.ok) {
        const serviceMessage = payload && typeof payload.error === "string" && payload.error.trim()
          ? payload.error.trim()
          : "";
        throw new Error(serviceMessage || `云站点 API 返回异常状态（HTTP ${response.status}）`);
      }
      if (!payload || typeof payload.required !== "boolean") {
        throw new Error("云站点 API 可达，但返回的 bootstrap-status 数据无效");
      }

      try {
        await startEmbeddedRuntime(normalizedApi, false);
      } catch (runtimeError) {
        const detail = runtimeError instanceof Error ? runtimeError.message : "本地发布运行时未就绪";
        throw new Error(`云站点 API 可达，但本地发布运行时启动失败：${detail}`);
      }

      const normalizedPublicHost = normalizedCloudPublicHost;
      window.localStorage.setItem(
        "desktop-publisher-relay-endpoint",
        JSON.stringify({ apiBaseUrl: normalizedApi, publicHost: normalizedPublicHost }),
      );
      setApiDraft(normalizedApi);
      setCloudPublicHost(normalizedPublicHost);
      setBootstrapRequired(payload.required);
      setConnectionReady(true);
      setConnectionStatus("connected");
      setMessage(payload.required
        ? "云站点 API 连接成功，但该环境尚未完成初始化，请先完成初始化配置。"
        : "云站点 API 连接成功，本地发布运行时已启动。下一步登录并绑定当前设备。");
    } catch (connectionError) {
      setConnectionReady(false);
      setConnectionStatus("failed");
      setError(connectionError instanceof Error ? connectionError.message : "连接云站点 API 失败");
    } finally {
      setBusy("");
    }
  }
  async function handleLogin(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy("login");
    setError("");
    setMessage("");
    try {
      const auth = await desktopApi.login(loginForm.email, loginForm.password);
      setCurrentUser(auth.user);
      // Always save login profile so email appears in history
      if (desktopTransport) {
        try {
          const pw = savePassword ? loginForm.password : "";
          await saveLoginProfile(loginForm.email, pw, savePassword && autoLogin);
          const updated = await readLoginProfiles();
          if (updated) setLoginProfiles(updated);
        } catch { /* ignore save errors */ }
      }
      if (!runtimeReady && effectiveApiBaseUrl) {
        await startEmbeddedRuntime(effectiveApiBaseUrl, false);
      }
      await loadSnapshot();
      setMessage("已接入云站点，隧道同步就绪。可开始管理本地服务与发布规则。");
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
      await desktopApi.logout();
      setCurrentUser(null);
      setNodes([]);
      setTunnels([]);
      setServerMetrics(null);
      setDrawerState(null);
      setTunnelActionOptions([]);
      setTunnelControlPanel(null);
      setControlResult(null);
      setControlNote("");
      setLastRefreshAt("");
      setSyncIssue("");
      setCertificates([]);
      setManagedHTTPSDomains([]);
      setMessage("已退出登录。");
    } catch (logoutError) {
      setError(logoutError instanceof Error ? logoutError.message : "退出失败");
    } finally {
      setBusy("");
    }
  }

  async function refreshPublisherData(showNotice: boolean, source: "manual" | "auto") {
    if (!currentUser || refreshInFlightRef.current) return;
    refreshInFlightRef.current = true;
    setRefreshing(true);
    if (showNotice) {
      setError("");
      setMessage("");
    }
    try {
      await Promise.all([
        loadSnapshot(),
        source === "manual" ? reloadCertificateState() : Promise.resolve(),
      ]);
      setSyncIssue("");
      if (showNotice) {
        setMessage("当前机器的发布规则、验证结果、运行状态和托管域名已刷新。");
      }
    } catch (refreshError) {
      const detail = refreshError instanceof Error ? refreshError.message : "刷新失败";
      if (source === "manual") {
        setError("手动刷新失败，已保留当前发布数据。 " + detail);
      } else {
        setSyncIssue(detail);
      }
    } finally {
      refreshInFlightRef.current = false;
      setRefreshing(false);
    }
  }

  function openAddServiceDrawer(serviceId: string | null = null) {
    const service = serviceId ? localServices.find((item) => item.id === serviceId) : null;
    setLocalServiceForm(
      service
        ? {
            name: service.name,
            targetHost: service.targetHost,
            targetPort: service.targetPort,
            note: service.note,
          }
        : initialLocalServiceForm,
    );
    setDrawerState({ kind: "service", serviceId });
  }

  function closeDrawer() {
    setDrawerState(null);
    setEditForm(null);
    setControlNote("");
    setControlResult(null);
  }

  function saveLocalService(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const host = localServiceForm.targetHost.trim();
    const port = Number(localServiceForm.targetPort);
    if (!host || port <= 0) {
      setError("本地服务需要有效的 targetHost 和 targetPort。");
      return;
    }
    const editingId = drawerState?.kind === "service" ? drawerState.serviceId : null;
    const nextItem: LocalServiceDraft = {
      id: editingId || "local-service-" + Date.now(),
      name: localServiceForm.name.trim() || `${host}:${port}`,
      targetHost: host,
      targetPort: String(port),
      note: localServiceForm.note.trim(),
      detectedStatus: "unverified",
      lastCheckedAt: new Date().toISOString(),
    };
    setLocalServices((current) => {
      if (!editingId) return [nextItem, ...current];
      return current.map((item) => (item.id === editingId ? nextItem : item));
    });
    setPublishForm((current) => ({ ...current, localServiceId: nextItem.id || current.localServiceId }));
    setMessage(editingId ? "本地服务已更新。" : "本地服务已加入清单。下一步去发布管理创建发布规则。");
    setError("");
    closeDrawer();
  }

  function deleteLocalService(id: string) {
    setLocalServices((current) => current.filter((item) => item.id !== id));
    setMessage("本地服务已移除。");
    setError("");
  }

  async function openCreateRuleDrawer() {
    if (runtimeBindingIssue) {
      setError(runtimeBindingIssue);
      return;
    }
    try {
      await reloadCertificateState();
    } catch {
      // keep existing cached managed domains if refresh fails
    }
    setPublishForm((current) => ({
      ...current,
      localServiceId: current.localServiceId || localServices[0]?.id || "",
      publicPort: current.publicPort || suggestedPublicPortValue(current.protocol, publishableTunnels.length),
    }));
    setDrawerState({ kind: "create-rule" });
  }

  async function createPublishRule(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (runtimeBindingIssue) {
      setError(runtimeBindingIssue);
      return;
    }
    if (!runtimeBoundDevice) {
      setError("当前还没有绑定到本机 runtime 对应的设备节点。");
      return;
    }
    const service = localServices.find((item) => item.id === publishForm.localServiceId) || localServices[0];
    if (!service) {
      setError("请先在本地服务页录入至少一个本地服务。");
      return;
    }
    if (publishForm.protocol !== "https" && (!publishForm.publicPort || Number(publishForm.publicPort) <= 0)) {
      setError("请填写有效的公网端口。");
      return;
    }
    if (publishForm.protocol === "https" && !publishForm.domain.trim()) {
      setError("HTTPS 发布规则必须填写 domain。");
      return;
    }
    setBusy("create-rule");
    setError("");
    setMessage("");
    try {
      await desktopApi.requestJSON<TunnelSpec>("/api/tunnels", {
        method: "POST",
        body: JSON.stringify({
          nodeId: runtimeBoundDevice.nodeId,
          name: `${service.name} ${publishForm.protocol.toUpperCase()}`,
          type: publishForm.protocol,
          transportPolicy: publishForm.transportPolicy,
          targetHost: publishForm.protocol === "socks5" ? "socks5" : service.targetHost,
          targetPort: publishForm.protocol === "socks5" ? 1080 : Number(service.targetPort),
          publicPort: serializePublishPublicPort(publishForm.protocol, publishForm.publicPort),
          domain: publishForm.protocol === "https" ? publishForm.domain.trim() : undefined,
          tlsMode: publishForm.protocol === "https" ? "edge_terminate" : undefined,
          probePath: publishForm.protocol === "http" || publishForm.protocol === "https" ? normalizeProbePath(publishForm.probePath) : undefined,
          metadata: buildTunnelServiceMetadata(undefined, publishForm),
          status: "active",
        }),
      });
      await refreshPublisherData(false, "manual");
      setMessage(`发布规则已创建，并强制绑定到当前 runtime 节点 ${runtimeBoundDevice.nodeName} (${runtimeBoundDevice.nodeId})。`);
      closeDrawer();
      navigate("/publish");
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "创建发布规则失败");
    } finally {
      setBusy("");
    }
  }

  async function handleTunnelSave(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!drawerTunnel || !runtimeBoundDevice || !editForm) return;
    if (runtimeBindingIssue) {
      setError(runtimeBindingIssue);
      return;
    }
    if (drawerTunnel.type === "https" && !editForm.domain.trim()) {
      setError("HTTPS 发布规则必须保留 domain。");
      return;
    }
    if (drawerTunnel.type !== "https" && (!editForm.publicPort || Number(editForm.publicPort) <= 0)) {
      setError("请填写有效的公网端口。");
      return;
    }
    setBusy("save-rule");
    setError("");
    setMessage("");
    try {
      await desktopApi.updateTunnel(drawerTunnel.id, {
        id: drawerTunnel.id,
        nodeId: runtimeBoundDevice.nodeId,
        name: editForm.name.trim(),
        type: drawerTunnel.type,
        status: drawerTunnel.status,
        targetHost: editForm.targetHost.trim(),
        targetPort: Number(editForm.targetPort),
        publicPort: serializePublishPublicPort(drawerTunnel.type, editForm.publicPort),
        domain: drawerTunnel.type === "http" || drawerTunnel.type === "https" ? editForm.domain.trim() : "",
        probePath: drawerTunnel.type === "http" || drawerTunnel.type === "https" ? normalizeProbePath(editForm.probePath) : "",
        transportPolicy: editForm.transportPolicy,
        tlsMode: drawerTunnel.tlsMode || "",
        metadata: buildTunnelServiceMetadata(drawerTunnel.metadata, editForm),
      });
      await refreshPublisherData(false, "manual");
      setMessage(`发布规则已更新，并保持绑定到当前 runtime 节点 ${runtimeBoundDevice.nodeName} (${runtimeBoundDevice.nodeId})。`);
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "保存发布规则失败");
    } finally {
      setBusy("");
    }
  }

  async function deletePublishRule(tunnel: TunnelSpec) {
    if (!window.confirm(`确认删除发布规则“${tunnel.name}”？`)) return;
    setBusy("delete-rule");
    setError("");
    setMessage("");
    try {
      await desktopApi.deleteTunnel(tunnel.id);
      await refreshPublisherData(false, "manual");
      setMessage("发布规则已删除。");
      if (drawerState?.kind === "rule" && drawerState.tunnelId === tunnel.id) {
        closeDrawer();
      }
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : "删除发布规则失败");
    } finally {
      setBusy("");
    }
  }

  async function runTunnelProbe(tunnel: TunnelSpec) {
    const busyKey = tunnel.id + ":probe";
    setBusy(busyKey);
    setError("");
    setMessage("");
    try {
      const result = await desktopApi.probeTunnel(tunnel.id);
      setProbeResults((current) => ({ ...current, [tunnel.id]: result }));
      setMessage(result.success ? "访问验证完成：当前公网入口可访问。" : "访问验证完成：当前公网入口不可访问。");
      await refreshPublisherData(false, "manual");
    } catch (probeError) {
      setError(probeError instanceof Error ? probeError.message : "访问验证失败");
    } finally {
      setBusy("");
    }
  }


  async function executeQuickToggle(tunnel: TunnelSpec) {
    const desiredAction: ControlActionRequest["actionKind"] = tunnel.status === "active" ? "pause_tunnel" : "resume_tunnel";
    setBusy("toggle-rule");
    setError("");
    setMessage("");
    try {
      const options = await desktopApi.loadTunnelControlActionOptions(tunnel.id, "node_console");
      const option = options.items.find((item) => item.actionKind === desiredAction);
      if (!option?.available) {
        throw new Error(option?.message || "当前动作不可用");
      }
      const result = await desktopApi.controlAction({
        actionKind: desiredAction,
        targetKind: "tunnel",
        targetId: tunnel.id,
        sourceSurface: "node_console",
        dryRun: false,
        note: controlNote.trim(),
        requestedAt: option.contextVersion || options.contextVersion,
      });
      setControlResult(result);
      setMessage(result.humanMessage);
      await refreshPublisherData(false, "manual");
    } catch (toggleError) {
      setError(toggleError instanceof Error ? toggleError.message : "规则状态切换失败");
    } finally {
      setBusy("");
    }
  }

  async function runRuleAction(actionKind: ControlActionRequest["actionKind"], tunnel: TunnelSpec) {
    setBusy("control-action");
    setError("");
    setMessage("");
    try {
      const result = await desktopApi.controlAction({
        actionKind,
        targetKind: "tunnel",
        targetId: tunnel.id,
        sourceSurface: "node_console",
        dryRun: false,
        note: controlNote.trim(),
        requestedAt: tunnelControlPanel?.contextVersion || tunnelActionOptions[0]?.contextVersion || tunnelControlContextAt || undefined,
      });
      setControlResult(result);
      setMessage(result.humanMessage);
      await refreshPublisherData(false, "manual");
    } catch (controlError) {
      setError(controlError instanceof Error ? controlError.message : "发布规则动作失败");
    } finally {
      setBusy("");
    }
  }

  async function copyToClipboard(value: string, label: string) {
    try {
      if (!navigator?.clipboard?.writeText) {
        throw new Error("clipboard unavailable");
      }
      await navigator.clipboard.writeText(value);
      setMessage(label + " 已复制到剪贴板。");
      setError("");
    } catch (copyError) {
      setError(copyError instanceof Error ? copyError.message : "复制失败");
    }
  }

  async function openExternal(url: string, label: string) {
    try {
      if (desktopTransport) {
        await openDesktopExternal(url);
      } else {
        const opened = window.open(url, "_blank", "noopener,noreferrer");
        if (!opened) {
          throw new Error("窗口被拦截，请允许当前桌面页打开新窗口。");
        }
      }
      setMessage(label + " 已在系统默认浏览器打开。");
      setError("");
    } catch (openError) {
      setError(openError instanceof Error ? openError.message : "打开失败");
    }
  }

  function handleDashboardAction() {
    if (localServices.length === 0) {
      openAddServiceDrawer(null);
      return;
    }
    if (publishableTunnels.length === 0) {
      openCreateRuleDrawer();
      return;
    }
    if (diagnosticsItems.length > 0) {
      navigate("/diagnostics");
      return;
    }
    navigate("/settings");
  }

  function handleWindowDrag(event: React.MouseEvent<HTMLDivElement>) {
    const target = event.target as HTMLElement;
    if (target.closest(".window-controls")) return;
    if (desktopTransport) {
      void windowStartDrag();
    }
  }

  function handleWindowToggleMaximize() {
    if (desktopTransport) {
      void windowToggleMaximize();
    }
  }

  function handleWindowMinimize() {
    if (desktopTransport) {
      void windowMinimize();
    }
  }

  function handleWindowClose() {
    if (appConfig.closeAction === "ask") {
      setCloseDialogOpen(true);
    } else if (appConfig.closeAction === "tray") {
      void windowRequestClose();
    } else {
      void appExit(); // Rust app_exit saves bounds with DPI correction
    }
  }

  async function handleCloseDialogChoice(action: "tray" | "exit", dontAskAgain: boolean) {
    setCloseDialogOpen(false);
    if (dontAskAgain) {
      const newConfig = { ...appConfig, closeAction: action === "tray" ? "tray" as const : "exit" as const };
      setAppConfig(newConfig);
      try { await saveAppConfig(newConfig); } catch { /* ignore */ }
    }
    if (action === "exit") {
      void appExit(); // Rust app_exit saves bounds with DPI correction
    } else {
      void windowRequestClose(); // hide_to_tray saves bounds with DPI correction
    }
  }

  async function saveP2PSettings() {
    setP2PBusy(true);
    setError("");
    setMessage("");
    try {
      const merged = normalizePublisherAppConfig(appConfig);
      setAppConfig(merged);
      await saveAppConfig(merged);
      const status = await loadP2PRuntimeStatus();
      setP2PStatus(status);
      setMessage(runtimeRunning
        ? "P2P 设置已保存。当前发布 Runtime 已经带上 EasyTier telemetry；若需要立即应用新的 EasyTier 参数，请停止后重新启动 EasyTier。"
        : "P2P 设置已保存。");
    } catch (configError) {
      setError(configError instanceof Error ? configError.message : "保存 P2P 设置失败");
    } finally {
      setP2PBusy(false);
    }
  }

  async function handleP2PRuntimeStart() {
    if (p2pStatus.running) {
      setMessage("EasyTier 已在运行，本次不会重复拉起。");
      return;
    }
    setP2PBusy(true);
    setError("");
    setMessage("");
    try {
      const merged = normalizePublisherAppConfig(appConfig);
      setAppConfig(merged);
      await saveAppConfig(merged);
      const status = await startP2PRuntime();
      setP2PStatus(status);
      setMessage(status.peerCount > 0 ? `EasyTier 已启动并接入 ${status.peerCount} 个对等节点。` : "EasyTier 已启动。");
    } catch (startError) {
      setError(startError instanceof Error ? startError.message : "启动 EasyTier 失败");
    } finally {
      setP2PBusy(false);
    }
  }

  async function handleP2PRuntimeStop() {
    setP2PBusy(true);
    setError("");
    setMessage("");
    try {
      const status = await stopP2PRuntime();
      setP2PStatus(status);
      setMessage("EasyTier 已停止。");
    } catch (stopError) {
      setError(stopError instanceof Error ? stopError.message : "停止 EasyTier 失败");
    } finally {
      setP2PBusy(false);
    }
  }

  async function handleOpenP2PLog(kind: "stdout" | "stderr") {
    try {
      await openP2PRuntimeLog(kind);
    } catch (logError) {
      setError(logError instanceof Error ? logError.message : "打开 EasyTier 日志失败");
    }
  }

  function renderDrawer(): ReactNode {
    if (!drawerState) return null;
    if (drawerState.kind === "service") {
      return (
        <div className="drawer-overlay" onClick={(event) => event.target === event.currentTarget && closeDrawer()}>
          <div className="drawer drawer-wide">
            <div className="drawer-header">
              <span className="drawer-title">{drawerState.serviceId ? "编辑本地服务" : "添加本地服务"}</span>
              <button className="drawer-close" type="button" onClick={closeDrawer}><i className="fas fa-times" /></button>
            </div>
            <div className="drawer-body">
            <form className="drawer-form" onSubmit={saveLocalService}>
              <label>
                <span>服务名称</span>
                <input value={localServiceForm.name} onChange={(event) => setLocalServiceForm((current) => ({ ...current, name: event.target.value }))} placeholder="例如 Web-Local" />
              </label>
              <label>
                <span>targetHost</span>
                <input value={localServiceForm.targetHost} onChange={(event) => setLocalServiceForm((current) => ({ ...current, targetHost: event.target.value }))} required />
              </label>
              <label>
                <span>targetPort</span>
                <input value={localServiceForm.targetPort} onChange={(event) => setLocalServiceForm((current) => ({ ...current, targetPort: event.target.value }))} inputMode="numeric" required />
              </label>
              <label>
                <span>备注</span>
                <textarea value={localServiceForm.note} onChange={(event) => setLocalServiceForm((current) => ({ ...current, note: event.target.value }))} placeholder="例如 当前机器上的本地前端或数据库服务" />
              </label>
              <button className="btn btn-primary" type="submit"><i className="fas fa-save" /> {drawerState.serviceId ? "保存服务" : "添加服务"}</button>
            </form>
            </div>
          </div>
        </div>
      );
    }

    if (drawerState.kind === "create-rule") {
      const selectedLocalService = localServices.find((item) => item.id === publishForm.localServiceId) || localServices[0] || null;
      return (
        <div className="drawer-overlay" onClick={(event) => event.target === event.currentTarget && closeDrawer()}>
          <div className="drawer drawer-wide">
            <div className="drawer-header">
              <span className="drawer-title">新建发布规则</span>
              <button className="drawer-close" type="button" onClick={closeDrawer}><i className="fas fa-times" /></button>
            </div>
            <div className="drawer-body">
            <form className="drawer-form" onSubmit={createPublishRule}>
              <label>
                <span>本地服务</span>
                <select value={publishForm.localServiceId} onChange={(event) => setPublishForm((current) => ({ ...current, localServiceId: event.target.value }))}>
                  <option value="">选择本地服务</option>
                  {localServices.map((item) => (
                    <option key={item.id} value={item.id}>{item.name} ({item.targetHost}:{item.targetPort})</option>
                  ))}
                </select>
              </label>
              <label>
                <span>协议</span>
                <select value={publishForm.protocol} onChange={(event) => {
                  const nextProtocol = event.target.value as PublishProtocol;
                  setPublishForm((current) => ({ ...current, protocol: nextProtocol, publicPort: suggestedPublicPortValue(nextProtocol, publishableTunnels.length) }));
                  if (nextProtocol === "https") {
                    void refreshManagedHTTPSDomains();
                  }
                }}>
                  {protocolList.map((item) => <option key={item} value={item}>{item.toUpperCase()}</option>)}
                </select>
              </label>
              <div className="surface-banner info protocol-entry-hint">{protocolEntryHint(publishForm.protocol)}</div>
              <label>
                <span>{publishPublicPortLabel(publishForm.protocol)}</span>
                <input value={publishForm.publicPort} onChange={(event) => setPublishForm((current) => ({ ...current, publicPort: event.target.value }))} inputMode="numeric" required={publishForm.protocol !== "https"} placeholder={publishForm.protocol === "https" ? "留空即可，标准入口固定走 443" : ""} />
              </label>
              <label>
                <span>传输策略</span>
                <select value={publishForm.transportPolicy} onChange={(event) => setPublishForm((current) => ({ ...current, transportPolicy: event.target.value }))}>
                  <option value="relay_only">relay_only</option>
                  <option value="p2p_preferred">p2p_preferred / 可登记 P2P 服务</option>
                </select>
              </label>
              {publishForm.protocol === "https" ? (
                <>
                  <label>
                    <span>已托管域名</span>
                    <select value={managedHTTPSDomainValues.includes(publishForm.domain.trim().toLowerCase()) ? publishForm.domain.trim().toLowerCase() : ""} onChange={(event) => setPublishForm((current) => ({ ...current, domain: event.target.value }))}>
                      <option value="">手动输入新域名</option>
                      {managedHTTPSDomains.map((item) => <option key={`${item.source}:${item.domain}`} value={item.domain}>{item.domain}{item.source === "cert_keeper" ? "（证书管家）" : ""}</option>)}
                    </select>
                  </label>
                  <label>
                    <span>Domain</span>
                    <input value={publishForm.domain} onChange={(event) => setPublishForm((current) => ({ ...current, domain: event.target.value }))} onBlur={() => { void refreshManagedHTTPSDomains(); }} onPaste={() => { void refreshManagedHTTPSDomains(); }} placeholder="例如 app.example.com" required />
                  </label>
                </>
              ) : null}
              {publishForm.protocol === "https" ? (() => {
                const domainTrim = publishForm.domain.trim().toLowerCase();
                const matchedCert = domainTrim ? certificates.find((c) => c.domain === domainTrim) : null;
                return (
                  <div className="drawer-cert-section">
                    <div className="section-title" style={{ fontSize: 12 }}><i className="fas fa-lock" /> SSL 证书</div>
                    {matchedCert ? (
                      <div className="surface-banner info">
                        域名 {matchedCert.domain} 已有证书（证书管家托管）{matchedCert.expiresAt ? `，到期: ${formatDate(matchedCert.expiresAt)}` : ""}
                      </div>
                    ) : (
                      <>
                        {!domainTrim ? <div className="surface-banner info">请先填写 Domain，再上传证书。</div> : (
                          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                            <div className="surface-banner info">域名 {domainTrim} 尚无证书。可在证书管家中自动签发，或在此手动上传。</div>
                            <textarea placeholder="证书 PEM (含 -----BEGIN CERTIFICATE-----)" value={certFormDomain === domainTrim ? certFormCert : ""} onChange={(e) => { setCertFormDomain(domainTrim); setCertFormCert(e.target.value); }} rows={3} style={{ fontFamily: "monospace", fontSize: 11, resize: "vertical" }} />
                            <textarea placeholder="私钥 PEM (含 -----BEGIN PRIVATE KEY-----)" value={certFormDomain === domainTrim ? certFormKey : ""} onChange={(e) => { setCertFormDomain(domainTrim); setCertFormKey(e.target.value); }} rows={3} style={{ fontFamily: "monospace", fontSize: 11, resize: "vertical" }} />
                            <button className="btn btn-sm" type="button" disabled={certBusy || !certFormCert || !certFormKey} onClick={async () => {
                              setCertBusy(true);
                              try {
                                await desktopApi.createCertificate({ domain: domainTrim, certPem: certFormCert, keyPem: certFormKey });
                                await reloadCertificateState();
                                setCertFormCert(""); setCertFormKey("");
                                setMessage("证书已上传至证书管家，HTTPS 将自动启用 SSL 终止。");
                              } catch (e) { setError(e instanceof Error ? e.message : "上传失败"); }
                              setCertBusy(false);
                            }}>手动上传</button>
                          </div>
                        )}
                      </>
                    )}
                  </div>
                );
              })() : null}
              {publishForm.protocol === "http" || publishForm.protocol === "https" ? (
                <label>
                  <span>验证路径</span>
                  <input value={publishForm.probePath} onChange={(event) => setPublishForm((current) => ({ ...current, probePath: event.target.value }))} placeholder="/" />
                  <div className="helper-text">仅用于连通性探测和示例命令，不影响实际转发路由，默认 /。</div>
                </label>
              ) : null}
              <ServiceMetadataEditor
                form={publishForm}
                targetPortHint={selectedLocalService?.targetPort || ""}
                nodeIdHint={runtimeBoundDevice?.nodeId || runtimeStatus.nodeId || ""}
                onChange={(patch) => setPublishForm((current) => ({ ...current, ...patch }))}
              />
              <div className="drawer-note">云端入口和用户端 P2P 工作台现在按服务元数据拆开登记，不再把所有业务流量混成统一回退路径。</div>
              <div className="surface-banner info">当前 runtime 节点：{runtimeBoundDevice ? `${runtimeBoundDevice.nodeName} (${runtimeBoundDevice.nodeId})` : runtimeStatus.nodeId ? `等待注册 ${runtimeStatus.nodeId}` : "未生成 nodeId"}</div>
              {runtimeBindingIssue ? <div className="surface-banner danger">{runtimeBindingIssue}{runtimeStatus.stderrLogPath ? <button className="btn btn-link-inline" type="button" onClick={() => void openRuntimeLog("stderr")}>打开 stderr 日志</button> : null}</div> : null}
              <button className="btn btn-primary" type="submit" disabled={busy === "create-rule" || localServices.length === 0 || Boolean(runtimeBindingIssue)}><i className="fas fa-plus" /> {busy === "create-rule" ? "创建中..." : "创建规则"}</button>
            </form>
            </div>
          </div>
        </div>
      );
    }

    if (!drawerTunnel || !drawerRuleState || !drawerCloudEntry || !editForm) return null;
    const linkedService = resolveLinkedService(drawerTunnel, localServices);
    const registeredService = extractTunnelServiceFields(drawerTunnel.metadata);
    const quickCommand = buildQuickCommand(drawerTunnel, drawerCloudEntry.publicUrl, normalizedCloudPublicHost);
    const probe = probeResults[drawerTunnel.id];

    return (
      <div className="drawer-overlay" onClick={(event) => event.target === event.currentTarget && closeDrawer()}>
        <div className="drawer">
          <div className="drawer-header">
            <span className="drawer-title">{drawerTunnel.name}</span>
            <button className="drawer-close" type="button" onClick={closeDrawer}><i className="fas fa-times" /></button>
          </div>
          <div className="drawer-body">
          <div className="detail-stat">
            <div className="detail-stat-row"><span><i className="fas fa-download" /> 规则总下载</span><strong>{formatBytesTotal(drawerTunnelTrafficTotals.downTotal)}</strong></div>
            <div className="detail-stat-row"><span><i className="fas fa-upload" /> 规则总上传</span><strong>{formatBytesTotal(drawerTunnelTrafficTotals.upTotal)}</strong></div>
            <div className="detail-stat-note">这里显示的是当前规则的真实累计流量，来自 agent 心跳上报；无规则流量时保持 0 KB。</div>
          </div>

          <div className="drawer-section">
            <div className="section-title"><i className="fas fa-circle-info" /> 规则详情</div>
            <div className="drawer-grid">
              <MetricBox label="本地服务" value={linkedService?.name || `${drawerTunnel.targetHost}:${drawerTunnel.targetPort}`} />
              <MetricBox label="协议" value={drawerTunnel.type.toUpperCase()} />
              <MetricBox label="绑定节点" value={`${drawerTunnel.nodeId}${runtimeStatus.nodeId && drawerTunnel.nodeId === runtimeStatus.nodeId ? " / 当前 runtime" : runtimeStatus.nodeId ? ` / runtime=${runtimeStatus.nodeId}` : ""}`} />
              <MetricBox label="运行状态" value={drawerTunnel.runtimeState || "尚无状态"} />
              <MetricBox label="运行路径" value={drawerTunnel.runtimePath || "尚无路径"} />
              <MetricBox label="失败原因" value={drawerTunnel.lastFailureReason || "尚无"} />
            </div>
            <div className="badge-row">
              {drawerRuleState.badges.map((badge) => <span key={badge.label} className={`status-chip ${badge.tone}`}>{badge.label}</span>)}
            </div>
            {drawerRuleState.messages.map((item) => <div key={item.message} className={item.tone === "danger" ? "surface-banner danger" : "surface-banner info"}>{item.message}</div>)}
            <div className="surface-banner info">下一步：{drawerRuleState.nextStep}</div>
          </div>

          <div className="drawer-section">
            <div className="section-title"><i className="fas fa-wand-magic-sparkles" /> 快速操作</div>
            <div className="drawer-action-grid">
              <button className="btn" type="button" onClick={() => void copyToClipboard(drawerCloudEntry.publicLabel, "用户入口")}><i className="fas fa-copy" /> 复制入口</button>
              <button className="btn" type="button" disabled={!supportsOpenEntry(drawerTunnel, drawerRuleState, drawerCloudEntry)} onClick={() => void openExternal(drawerCloudEntry.publicUrl, "用户入口")}><i className="fas fa-arrow-up-right-from-square" /> 打开入口</button>
              <button className="btn" type="button" onClick={() => void copyToClipboard(quickCommand, "协议示例命令(Linux/Mac)")}><i className="fas fa-terminal" /> 复制命令</button>
              <button className="btn" type="button" onClick={() => void copyToClipboard(buildQuickCommandWindows(drawerTunnel, normalizedCloudPublicHost), "验证命令(Windows)")}><i className="fas fa-terminal" /> 复制 Win 命令</button>
              <button className="btn" type="button" disabled={!supportsProbe(drawerTunnel, drawerRuleState, drawerCloudEntry) || busy === drawerTunnel.id + ":probe"} onClick={() => void runTunnelProbe(drawerTunnel)}><i className="fas fa-satellite-dish" /> {busy === drawerTunnel.id + ":probe" ? "探测中..." : "执行探测"}</button>
            </div>
            <div className="command-box"><code>{quickCommand}</code></div>
            <div className="command-box"><code>{buildQuickCommandWindows(drawerTunnel, normalizedCloudPublicHost)}</code></div>
            {probe ? <div className={`status-badge ${probe.success ? "" : "warning"}`}>{probe.success ? `最近 probe 成功 · ${formatDate(probe.probedAt)}` : `最近 probe 失败 · ${formatDate(probe.probedAt)} · ${probe.error || "未知错误"}`}</div> : null}
          </div>

          <div className="drawer-section">
            <div className="section-title"><i className="fas fa-pen-to-square" /> 编辑绑定</div>
            <form className="drawer-form" onSubmit={handleTunnelSave}>
              <label>
                <span>规则名称</span>
                <input value={editForm.name} onChange={(event) => setEditForm((current) => current ? { ...current, name: event.target.value } : current)} />
              </label>
              <label>
                <span>targetHost</span>
                <input value={editForm.targetHost} onChange={(event) => setEditForm((current) => current ? { ...current, targetHost: event.target.value } : current)} />
              </label>
              <label>
                <span>targetPort</span>
                <input value={editForm.targetPort} onChange={(event) => setEditForm((current) => current ? { ...current, targetPort: event.target.value } : current)} inputMode="numeric" />
              </label>
              <label>
                <span>{publishPublicPortLabel(drawerTunnel.type)}</span>
                <input value={editForm.publicPort} onChange={(event) => setEditForm((current) => current ? { ...current, publicPort: event.target.value } : current)} inputMode="numeric" placeholder={drawerTunnel.type === "https" ? "留空即可，标准入口固定走 443" : ""} />
              </label>
              {drawerTunnel.type === "http" || drawerTunnel.type === "https" ? (
                <>
                  {drawerTunnel.type === "https" ? (
                    <label>
                      <span>已托管域名</span>
                      <select value={managedHTTPSDomainValues.includes(editForm.domain.trim().toLowerCase()) ? editForm.domain.trim().toLowerCase() : ""} onChange={(event) => setEditForm((current) => current ? { ...current, domain: event.target.value } : current)}>
                        <option value="">手动输入新域名</option>
                        {managedHTTPSDomains.map((item) => <option key={`${item.source}:${item.domain}`} value={item.domain}>{item.domain}{item.source === "cert_keeper" ? "（证书管家）" : ""}</option>)}
                      </select>
                    </label>
                  ) : null}
                  <label>
                    <span>Domain</span>
                    <input value={editForm.domain} onChange={(event) => setEditForm((current) => current ? { ...current, domain: event.target.value } : current)} onBlur={() => { void refreshManagedHTTPSDomains(); }} onPaste={() => { void refreshManagedHTTPSDomains(); }} />
                  </label>
                </>
              ) : null}
              {drawerTunnel.type === "https" ? (() => {
                const domainTrim = editForm.domain.trim().toLowerCase();
                const matchedCert = domainTrim ? certificates.find((c) => c.domain === domainTrim) : null;
                return (
                  <div className="drawer-cert-section">
                    <div className="section-title" style={{ fontSize: 12 }}><i className="fas fa-lock" /> SSL 证书</div>
                    {matchedCert ? (
                      <div className="surface-banner info">
                        域名 {matchedCert.domain} 已有证书（证书管家托管）{matchedCert.expiresAt ? `，到期: ${formatDate(matchedCert.expiresAt)}` : ""}
                      </div>
                    ) : (
                      <>
                        {!domainTrim ? <div className="surface-banner info">请先填写 Domain，再上传证书。</div> : (
                          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                            <div className="surface-banner info">域名 {domainTrim} 尚无证书。可在证书管家中自动签发，或在此手动上传。</div>
                            <textarea placeholder="证书 PEM (含 -----BEGIN CERTIFICATE-----)" value={certFormDomain === domainTrim ? certFormCert : ""} onChange={(e) => { setCertFormDomain(domainTrim); setCertFormCert(e.target.value); }} rows={3} style={{ fontFamily: "monospace", fontSize: 11, resize: "vertical" }} />
                            <textarea placeholder="私钥 PEM (含 -----BEGIN PRIVATE KEY-----)" value={certFormDomain === domainTrim ? certFormKey : ""} onChange={(e) => { setCertFormDomain(domainTrim); setCertFormKey(e.target.value); }} rows={3} style={{ fontFamily: "monospace", fontSize: 11, resize: "vertical" }} />
                            <button className="btn btn-sm" type="button" disabled={certBusy || !certFormCert || !certFormKey} onClick={async () => {
                              setCertBusy(true);
                              try {
                                await desktopApi.createCertificate({ domain: domainTrim, certPem: certFormCert, keyPem: certFormKey });
                                await reloadCertificateState();
                                setCertFormCert(""); setCertFormKey("");
                                setMessage("证书已上传至证书管家，HTTPS 将自动启用 SSL 终止。");
                              } catch (e) { setError(e instanceof Error ? e.message : "上传失败"); }
                              setCertBusy(false);
                            }}>手动上传</button>
                          </div>
                        )}
                      </>
                    )}
                  </div>
                );
              })() : null}
              {drawerTunnel.type === "http" || drawerTunnel.type === "https" ? (
                <label>
                  <span>验证路径</span>
                  <input value={editForm.probePath} onChange={(event) => setEditForm((current) => current ? { ...current, probePath: event.target.value } : current)} placeholder="/" />
                  <div className="helper-text">仅用于连通性探测和示例命令，不影响实际转发路由，默认 /。</div>
                </label>
              ) : null}
              <label>
                <span>transportPolicy</span>
                <select value={editForm.transportPolicy} onChange={(event) => setEditForm((current) => current ? { ...current, transportPolicy: event.target.value } : current)}>
                  <option value="relay_only">relay_only</option>
                  <option value="p2p_preferred">p2p_preferred / 可登记 P2P 服务</option>
                </select>
              </label>
              <ServiceMetadataEditor
                form={editForm}
                targetPortHint={editForm.targetPort || String(drawerTunnel.targetPort || "")}
                nodeIdHint={drawerTunnel.nodeId || runtimeBoundDevice?.nodeId || ""}
                onChange={(patch) => setEditForm((current) => current ? { ...current, ...patch } : current)}
              />
              <button className="btn btn-primary" type="submit" disabled={busy === "save-rule"}><i className="fas fa-save" /> {busy === "save-rule" ? "保存中..." : "保存绑定"}</button>
            </form>
          </div>

          <div className="drawer-section">
            <div className="section-title"><i className="fas fa-layer-group" /> 服务登记</div>
            {registeredService.serviceKey ? (
              <div className="drawer-grid">
                <MetricBox label="服务标识" value={registeredService.serviceKey} />
                <MetricBox label="服务类型" value={serviceKindLabel(registeredService.serviceKind)} />
                <MetricBox label="云端入口权限" value={serviceAccessLabel(registeredService.serviceCloudAccess)} />
                <MetricBox label="P2P 入口权限" value={serviceAccessLabel(registeredService.serviceP2PAccess)} />
                <MetricBox label="用户端首选" value={servicePreferredPathLabel(registeredService.servicePreferredPath)} />
                <MetricBox label="P2P 节点" value={registeredService.serviceP2PNodeId || drawerTunnel.nodeId || "沿用当前规则节点"} />
              </div>
            ) : (
              <div className="surface-banner info">
                当前规则还没有登记成服务工作台入口。这样它仍然可以走云端反代，但用户端不会把它识别成“网盘”或“图床”。
              </div>
            )}
            {registeredService.serviceSummary ? <div className="drawer-note" style={{ marginTop: 12 }}>{registeredService.serviceSummary}</div> : null}
          </div>

          <div className="drawer-section">
            <div className="section-title"><i className="fas fa-sliders" /> 规则动作</div>
            <label className="drawer-form single-field">
              <span>动作备注</span>
              <textarea value={controlNote} onChange={(event) => setControlNote(event.target.value)} placeholder="记录为什么暂停或恢复当前发布规则" />
            </label>
            {tunnelControlPanel ? <div className="surface-banner info">{tunnelControlPanel.headline} {tunnelControlPanel.summary}</div> : null}
            <div className="drawer-action-grid action-grid-wide">
              {tunnelActionOptions.map((item) => (
                <button key={item.actionKind} className="btn" type="button" disabled={!item.available || busy === "control-action"} onClick={() => void runRuleAction(item.actionKind, drawerTunnel)}>
                  <i className={item.actionKind === "pause_tunnel" ? "fas fa-pause" : "fas fa-play"} />
                  {busy === "control-action" ? "处理中..." : item.label}
                </button>
              ))}
              <button className="btn danger-button" type="button" disabled={busy === "delete-rule"} onClick={() => void deletePublishRule(drawerTunnel)}><i className="fas fa-trash" /> 删除规则</button>
            </div>
            {controlResult ? <ControlResultBlock result={controlResult} /> : null}
          </div>
          </div>
        </div>
      </div>
    );
  }

  function renderDashboardPage() {
    const statusTone = syncIssue ? "warning" : "";
    return (
      <div className="page-panel active">
        <div className="page-header">
          <h1>仪表盘</h1>
          <div><button className="btn" type="button" onClick={() => void refreshPublisherData(true, "manual")} disabled={refreshing}><i className="fas fa-sync-alt" /> {refreshing ? "刷新中..." : "刷新"}</button></div>
        </div>
        <div className="status-bar-card">
          <div><span className={`status-dot ${statusTone}`} /> 云站点: {cloudEndpointDisplayName}</div>
          <div><i className="fas fa-server" /> {currentDevice?.nodeName || "当前设备未绑定"}</div>
          <div style={{ marginLeft: "auto" }}><i className={`fas ${syncIssue ? "fa-exclamation-triangle" : "fa-check-circle"}`} style={{ color: syncIssue ? "#dc2626" : "#f59e0b" }} /> {syncIssue ? "最近自动刷新失败" : "代理运行时正常"}</div>
        </div>
        <div className="cards-grid">
          <div className="stat-card"><div className="stat-title"><i className="fas fa-database" /> 本地服务</div><div className="stat-number">{localServices.length}</div><div>{localServices.length > 0 ? "已录入本机服务" : "尚未录入"}</div></div>
          <div className="stat-card"><div className="stat-title"><i className="fas fa-share-alt" /> 已发布规则</div><div className="stat-number">{publishableTunnels.length}</div><div>{activeRulesCount} 活跃 · {Math.max(0, publishableTunnels.length - activeRulesCount)} 非活跃</div></div>
          <div className="stat-card"><div className="stat-title"><i className="fas fa-exclamation-triangle" /> 需关注</div><div className="stat-number">{attentionCount}</div><div>{diagnosticsItems[0]?.label || "当前没有阻塞项"}</div></div>
          <button className="stat-card stat-card-button" type="button" onClick={() => setTrafficCalendarOpen(true)}>
            <div className="stat-title"><i className="fas fa-chart-line" /> 总流量</div>
            <div className="stat-number">{totalTrafficLabel}</div>
            <div>{monthLabel(currentTrafficMonth)}累计 · 点击查看日历</div>
          </button>
        </div>

        {publishableTunnels.length > 0 ? (
          <div className="section-title" style={{ marginBottom: 8 }}><i className="fas fa-sitemap" /> 隧道预览</div>
        ) : null}
        {publishableTunnels.length > 0 ? (
          <div className="table-wrapper">
            <table>
              <thead><tr><th>规则名称</th><th>协议</th><th>公网入口</th><th>状态</th><th>速率</th><th>累计流量</th></tr></thead>
              <tbody>
                {publishableTunnels.map((tunnel) => {
                  const state = evaluateRuleState(tunnel);
                  const entry = deriveCloudEntry(tunnel, normalizedCloudPublicHost);
                  const traffic = localTrafficByTunnel.get(tunnel.id);
                  const rate = tunnelRates.get(tunnel.id);
                  return (
                    <tr key={tunnel.id}>
                      <td>{tunnel.name}</td>
                      <td>{tunnel.type.toUpperCase()}</td>
                      <td>{entry.publicLabel}</td>
                      <td><span className={`status-badge ${state.attention ? "warning" : ""}`}>{tunnel.status === "active" && !state.attention ? "Active" : tunnel.status === "paused" ? "Paused" : state.attention ? "Pending" : tunnel.status}</span></td>
                      <td>{rate ? `↓${formatRate(rate.downRate)} ↑${formatRate(rate.upRate)}` : "0 KB/s"}</td>
                      <td>{traffic ? formatBytesTotal(traffic.downBytes + traffic.upBytes) : "0 KB"}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}
      </div>
    );
  }

  function renderServicesPage() {
    const linkedTargets = new Set(publishableTunnels.map((item) => `${item.targetHost}:${item.targetPort}`));
    return (
      <div className="page-panel active">
        <div className="page-header"><h1>本地服务</h1><div><button className="btn btn-primary" type="button" onClick={() => openAddServiceDrawer(null)}><i className="fas fa-plus" /> 添加服务</button></div></div>
        <div className="table-wrapper">
          <table>
            <thead><tr><th>服务名称</th><th>目标地址</th><th>状态</th><th>备注</th><th>操作</th></tr></thead>
            <tbody>
              {localServices.length === 0 ? <tr><td colSpan={5}>当前还没有本地服务</td></tr> : localServices.map((item) => (
                <tr key={item.id}>
                  <td>{item.name}</td>
                  <td>{item.targetHost}:{item.targetPort}</td>
                  <td><span className={`status-badge ${linkedTargets.has(`${item.targetHost}:${item.targetPort}`) ? "" : "warning"}`}>{linkedTargets.has(`${item.targetHost}:${item.targetPort}`) ? "已发布" : "未发布"}</span></td>
                  <td>{item.note || "暂无备注"}</td>
                  <td className="action-icons"><i className="fas fa-edit" onClick={() => openAddServiceDrawer(item.id)} /><i className="fas fa-trash" onClick={() => deleteLocalService(item.id)} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    );
  }

  function renderPublishPage() {
    return (
      <div className="page-panel active">
        <div className="page-header"><h1>发布规则</h1><div><button className="btn btn-primary" type="button" onClick={() => void openCreateRuleDrawer()}><i className="fas fa-plus" /> 新建规则</button></div></div>
        <div className="surface-banner info">当前 runtime 节点：{runtimeBoundDevice ? `${runtimeBoundDevice.nodeName} (${runtimeBoundDevice.nodeId})` : runtimeStatus.nodeId ? `等待注册 ${runtimeStatus.nodeId}` : "未生成 nodeId"}</div>
        {runtimeBindingIssue ? <div className="surface-banner danger">{runtimeBindingIssue}{runtimeStatus.stderrLogPath ? <button className="btn btn-link-inline" type="button" onClick={() => void openRuntimeLog("stderr")}>打开 stderr 日志</button> : null}</div> : null}
        <div className="table-wrapper">
          <table>
            <thead><tr><th>本地服务</th><th>协议/入口</th><th>状态</th><th>操作</th></tr></thead>
            <tbody>
              {publishableTunnels.length === 0 ? <tr><td colSpan={4}>当前还没有发布规则</td></tr> : publishableTunnels.map((item) => {
                const state = evaluateRuleState(item);
                const entry = deriveCloudEntry(item, normalizedCloudPublicHost);
                const service = resolveLinkedService(item, localServices);
                const registeredService = extractTunnelServiceFields(item.metadata);
                return (
                  <tr key={item.id}>
                    <td>
                      <div>{registeredService.serviceTitle || service?.name || `${item.targetHost}:${item.targetPort}`}</div>
                      <div className="weak-note">
                        {registeredService.serviceKey
                          ? `${registeredService.serviceKey} · ${serviceKindLabel(registeredService.serviceKind)}`
                          : `未登记服务工作台 · ${service?.name || `${item.targetHost}:${item.targetPort}`}`}
                      </div>
                    </td>
                    <td>
                      <div>{item.type.toUpperCase()} · {entry.publicLabel}</div>
                      <div className="weak-note">
                        {registeredService.serviceKey
                          ? `云端 ${serviceAccessLabel(registeredService.serviceCloudAccess)} / P2P ${serviceAccessLabel(registeredService.serviceP2PAccess)} / 首选 ${servicePreferredPathLabel(registeredService.servicePreferredPath)}`
                          : "当前仅作为普通发布规则存在"}
                      </div>
                    </td>
                    <td><span className={`status-badge ${state.attention ? "warning" : ""}`}>{item.status === "active" && !state.attention ? "Active" : item.status === "paused" ? "Paused" : state.attention ? "Pending" : item.status}</span></td>
                    <td className="action-icons">
                      <i className="fas fa-chart-bar detail-trigger" onClick={() => setDrawerState({ kind: "rule", tunnelId: item.id })} />
                      <i className={item.status === "active" ? "fas fa-pause" : "fas fa-play"} onClick={() => void executeQuickToggle(item)} />
                      <i className="fas fa-trash" onClick={() => void deletePublishRule(item)} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>
    );
  }

  function renderDiagnosticsPage() {
    return (
      <div className="page-panel active">
        <div className="page-header"><h1>诊断中心</h1><div><button className="btn" type="button"><i className="fas fa-download" /> 导出诊断包</button></div></div>
        <div className="diagnostics-panel">
          {diagnosticsItems.length === 0 ? (
            <div>当前没有需要关注的问题。</div>
          ) : diagnosticsItems.map((item) => (
            <div key={item.id} className="diagnostic-row">
              <i className="fas fa-exclamation-triangle" style={{ color: "#f59e0b" }} />
              <div>
                <strong>{item.label}</strong>
                <div>{item.detail}</div>
                <div className="weak-note">下一步：{item.nextStep}</div>
              </div>
            </div>
          ))}
          <div className="weak-note">全局 next-step：{globalNextStep}</div>
          <div className="weak-note">日志目录：{desktopHostPaths.logDir}</div>
        </div>
      </div>
    );
  }

  function renderP2PPage() {
    const cloudP2PRunning = p2pCloudMetrics["p2p:running"] === "true";
    const cloudPeerCount = p2pCloudMetrics["p2p:peer_count"] || String(p2pStatus.peerCount || 0);
    const cloudIpv4 = p2pCloudMetrics["p2p:ipv4"] || p2pStatus.virtualIpv4 || "未分配";
    const cloudHostname = p2pCloudMetrics["p2p:hostname"] || p2pStatus.nodeHostname || currentDevice?.nodeName || "待上报";
    const cloudInstanceId = p2pCloudMetrics["p2p:instance_id"] || p2pStatus.instanceId || "未上报";
    const cloudRpcPortal = p2pCloudMetrics["p2p:rpc_portal"] || p2pStatus.rpcPortal || "127.0.0.1:15888";

    return (
      <div className="page-panel active">
        <div className="page-header">
          <h1>P2P 网络</h1>
          <div className="settings-actions" style={{ marginTop: 0 }}>
            <button className="btn" type="button" onClick={() => void loadP2PRuntimeStatus().then(setP2PStatus)} disabled={p2pBusy}>
              <i className="fas fa-rotate" /> 刷新状态
            </button>
            <button className="btn" type="button" onClick={() => void startEmbeddedRuntime(cloudEndpointLabel, true)} disabled={!connectionReady}>
              <i className="fas fa-plug" /> 重启发布 Runtime
            </button>
          </div>
        </div>
        <div className="surface-banner info">
          EasyTier 是节点级覆盖网络，不是单条隧道。这里管理的是服务端节点本身，以及当前设备上哪些服务发布规则准备接入 P2P 数据面。
        </div>

        <div className="drawer-grid" style={{ marginTop: 20 }}>
          <div className="settings-panel">
            <div className="settings-section-label">本地策略</div>
            <label style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer", padding: "10px 0" }}>
              <input
                type="checkbox"
                checked={Boolean(appConfig.p2pAutoStart)}
                onChange={(event) => setAppConfig((current) => ({ ...current, p2pAutoStart: event.target.checked }))}
              />
              <span>随驻阡陌启动 EasyTier</span>
            </label>
            <div className="settings-row"><span>当前设备</span><span>{currentDevice?.nodeName || "未绑定"}</span></div>
            <div className="settings-row"><span>当前 P2P 候选规则</span><span>{String(p2pCandidateRules.length)}</span></div>
            <div className="settings-row"><span>本地监听</span><span>`21110` / tcp+udp</span></div>
            <div className="settings-row"><span>固定 RPC</span><span>127.0.0.1:15888</span></div>
            <div className="surface-banner info">
              服务端这里会固定使用 `21110` 的 `tcp/udp` 监听，主动避开云端引导节点使用的 `11010`，减少 Windows 端口冲突。
            </div>
            <div className="settings-actions">
              <button className="btn btn-primary" type="button" onClick={() => void saveP2PSettings()} disabled={p2pBusy}>
                <i className="fas fa-save" /> {p2pBusy ? "保存中..." : "保存 P2P 设置"}
              </button>
            </div>
          </div>

          <div className="settings-panel">
            <div className="settings-section-label">EasyTier 运行态</div>
            <div className="settings-row"><span>当前状态</span><span>{p2pRuntimeLabel(p2pStatus)}</span></div>
            <div className="settings-row"><span>进程 PID</span><span>{p2pStatus.pid ?? "未运行"}</span></div>
            <div className="settings-row"><span>最近启动</span><span>{formatStartedAt(p2pStatus.startedAt)}</span></div>
            <div className="settings-row"><span>参数状态</span><span>{p2pStatus.configured ? "已配置" : "未配置"}</span></div>
            <div className="settings-row"><span>本机主机名</span><span>{p2pStatus.nodeHostname || "待上报"}</span></div>
            <div className="settings-row"><span>虚拟 IPv4</span><span>{p2pStatus.virtualIpv4 || "未分配"}</span></div>
            <div className="settings-row"><span>对等节点数</span><span>{p2pStatus.peerCount}</span></div>
            <div className="settings-row"><span>machine-id</span><span>{p2pStatus.machineId || "未生成"}</span></div>
            <div className="settings-row"><span>RPC 端口</span><span>{p2pStatus.rpcPortal || "未配置"}</span></div>
            <div className="surface-banner info">当前发行包会把 `easytier-core.exe` 和 `easytier-cli.exe` 一并放入 `runtime/`，这里会直接读取实际打包结果。</div>
            <div className="command-box"><code>{p2pStatus.executablePath || "当前安装包尚未带入 easytier-core.exe"}</code></div>
            <div className="command-box"><code>{p2pStatus.argsSummary || "当前尚未生成启动参数。先填写下方 EasyTier 节点参数。"}</code></div>
            <div className="command-box"><code>{p2pStatus.connectedPeers.length ? p2pStatus.connectedPeers.join("\n") : "当前尚未接入任何对等节点。"}</code></div>
            {p2pStatus.lastError ? <div className="surface-banner danger">{p2pStatus.lastError}</div> : null}
            <div className="settings-actions">
              <button className="btn btn-primary" type="button" onClick={() => void handleP2PRuntimeStart()} disabled={p2pBusy || !p2pStatus.available || p2pStatus.running}>
                <i className="fas fa-play" /> {p2pBusy ? "处理中..." : p2pStatus.running ? "EasyTier 运行中" : "启动 EasyTier"}
              </button>
              <button className="btn" type="button" onClick={() => void handleP2PRuntimeStop()} disabled={p2pBusy || !p2pStatus.running}>
                <i className="fas fa-stop" /> 停止 EasyTier
              </button>
              <button className="btn" type="button" onClick={() => void handleOpenP2PLog("stdout")}>
                <i className="fas fa-file-lines" /> stdout 日志
              </button>
              <button className="btn" type="button" onClick={() => void handleOpenP2PLog("stderr")}>
                <i className="fas fa-file-waveform" /> stderr 日志
              </button>
            </div>
          </div>
        </div>

        <div className="drawer-grid" style={{ marginTop: 20 }}>
          <div className="settings-panel">
            <div className="settings-section-label">节点上云状态</div>
            <div className="settings-row"><span>云端节点</span><span>{runtimeBoundDevice ? `${runtimeBoundDevice.nodeName} (${runtimeBoundDevice.nodeId})` : "待注册"}</span></div>
            <div className="settings-row"><span>P2P Assist 能力</span><span>{runtimeBoundDevice?.capabilities.p2pAssist ? "已上报" : "未上报"}</span></div>
            <div className="settings-row"><span>云端看到的运行态</span><span>{cloudP2PRunning ? "running" : "stopped"}</span></div>
            <div className="settings-row"><span>云端看到的主机名</span><span>{cloudHostname}</span></div>
            <div className="settings-row"><span>云端看到的虚拟 IPv4</span><span>{cloudIpv4}</span></div>
            <div className="settings-row"><span>云端看到的对等节点数</span><span>{cloudPeerCount}</span></div>
            <div className="settings-row"><span>云端看到的实例 ID</span><span>{cloudInstanceId}</span></div>
            <div className="settings-row"><span>云端看到的 RPC</span><span>{cloudRpcPortal}</span></div>
            <div className="surface-banner info">
              `client-agent.exe` 已经会带上 EasyTier telemetry 环境变量。只要当前发布 Runtime 在线，云端节点页就会逐步看到 `p2p:ipv4`、`p2p:peer_count` 等指标。
            </div>
          </div>

          <div className="settings-panel">
            <div className="settings-section-label">EasyTier 节点参数</div>
            <div className="p2p-config-panel">
              <div className="p2p-config-header">
                <div>
                  <strong>服务端节点配置</strong>
                  <p>这里配置的是服务端 EasyTier 节点本身，不是云端隧道。保存只会落盘，不会自动重启当前运行中的 EasyTier。</p>
                </div>
                <div className="p2p-config-pills">
                  <span className="p2p-pill">监听 `21110` / TCP</span>
                  <span className="p2p-pill">监听 `21110` / UDP</span>
                  <span className="p2p-pill">RPC `127.0.0.1:15888`</span>
                </div>
              </div>

              <div className="p2p-config-grid">
                <label className="p2p-field">
                  <span>网络名</span>
                  <input
                    value={appConfig.p2pNetworkName || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pNetworkName: event.target.value }))}
                    placeholder="例如 cloud-relay"
                  />
                  <small>同一 EasyTier 网络内必须一致。</small>
                </label>

                <label className="p2p-field">
                  <span>实例名</span>
                  <input
                    value={appConfig.p2pInstanceName || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pInstanceName: event.target.value }))}
                    placeholder="默认 cloud-relay-publisher"
                  />
                  <small>用于区分这台服务端上的 EasyTier 实例。</small>
                </label>

                <label className="p2p-field p2p-field-wide">
                  <span>网络密钥</span>
                  <input
                    type="password"
                    value={appConfig.p2pNetworkSecret || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pNetworkSecret: event.target.value }))}
                    placeholder="用于同一 EasyTier 网络鉴权"
                  />
                  <small>这里只做本地保存；打包结果不会把密钥硬编码进软件。</small>
                </label>

                <label className="p2p-field p2p-field-wide">
                  <span>初始对等节点</span>
                  <textarea
                    rows={4}
                    value={appConfig.p2pPeerUrl || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pPeerUrl: event.target.value }))}
                    placeholder={defaultPublisherP2PPeerUrl}
                  />
                  <small>支持换行、空格、逗号或分号分隔。默认建议同时填入云端引导节点的 `tcp` 和 `udp`。</small>
                </label>
              </div>

              <div className="p2p-config-grid p2p-config-grid-bottom">
                <label className="p2p-toggle-card">
                  <div className="p2p-toggle-head">
                    <span>地址分配方式</span>
                    <input
                      type="checkbox"
                      checked={appConfig.p2pUseDhcp !== false}
                      onChange={(event) => setAppConfig((current) => ({ ...current, p2pUseDhcp: event.target.checked }))}
                    />
                  </div>
                  <strong>{appConfig.p2pUseDhcp !== false ? "DHCP 自动分配" : "使用固定虚拟 IPv4"}</strong>
                  <small>默认建议开启 DHCP，只有你需要稳定地址映射时再改成固定 IP。</small>
                </label>

                <label className="p2p-field">
                  <span>固定虚拟 IPv4</span>
                  <input
                    value={appConfig.p2pVirtualIpv4 || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pVirtualIpv4: event.target.value }))}
                    placeholder="关闭 DHCP 时必填，例如 10.144.144.23"
                    disabled={appConfig.p2pUseDhcp !== false}
                  />
                  <small>{appConfig.p2pUseDhcp !== false ? "当前已启用 DHCP，此项会保持禁用。" : "关闭 DHCP 后这里必须填写。"} </small>
                </label>

                <label className="p2p-field">
                  <span>主机名</span>
                  <input
                    value={appConfig.p2pHostname || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pHostname: event.target.value }))}
                    placeholder="可选，便于识别服务端节点"
                  />
                  <small>建议填“服务端”或具体机器名，便于用户端和云端识别。</small>
                </label>
              </div>
            </div>
          </div>
        </div>

        <div style={{ marginTop: 20 }}>
          <div className="section-title" style={{ marginBottom: 8 }}><i className="fas fa-project-diagram" /> 当前设备的 P2P 服务候选</div>
          <div className="surface-banner info" style={{ marginTop: 0 }}>
            这里单独列出声明了 `p2p_preferred` 的规则。现在可以直接在发布规则里登记网盘/图床等服务元数据，用户端工作台会按这里的服务登记进行识别。
          </div>
          <div className="table-wrapper" style={{ marginTop: 12 }}>
            <table>
              <thead><tr><th>规则名称</th><th>服务登记</th><th>传输策略</th><th>运行路径</th><th>运行状态</th></tr></thead>
              <tbody>
                {p2pCandidateRules.length === 0 ? (
                  <tr><td colSpan={5}>当前还没有 `p2p_preferred` 的发布规则</td></tr>
                ) : p2pCandidateRules.map((item) => {
                  const registeredService = extractTunnelServiceFields(item.metadata);
                  return (
                    <tr key={item.id}>
                      <td>{item.name}</td>
                      <td>
                        {registeredService.serviceKey ? (
                          <>
                            <div>{registeredService.serviceTitle || registeredService.serviceKey}</div>
                            <div className="weak-note">
                              {serviceKindLabel(registeredService.serviceKind)} · P2P {serviceAccessLabel(registeredService.serviceP2PAccess)}
                            </div>
                          </>
                        ) : (
                          <span className="weak-note">未登记服务元数据</span>
                        )}
                      </td>
                      <td>{item.transportPolicy}</td>
                      <td>{item.runtimePath || "未上报"}</td>
                      <td>{item.runtimeState || "未上报"}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    );
  }

  function renderSettingsPage() {
    return (
      <div className="page-panel active">
        <div className="page-header"><h1>设置</h1></div>
        <div className="settings-panel">
          <div className="settings-section-label">基本</div>
          <div className="settings-row"><span>云站点</span><span>{cloudEndpointDisplayName}</span></div>
          <div className="settings-row"><span>账号</span><span>{currentUser?.email || "未登录"}</span></div>
          <div className="settings-row"><span>当前设备</span><span>{currentDevice?.nodeName || "未绑定"}</span></div>

          <div className="settings-section-label">行为</div>
          <div className="settings-row">
            <span>关闭行为</span>
            <select value={appConfig.closeAction || "ask"} onChange={async (event) => {
              const val = event.target.value as "ask" | "tray" | "exit";
              const newConfig = { ...appConfig, closeAction: val };
              setAppConfig(newConfig);
              try { await saveAppConfig(newConfig); } catch { /* ignore */ }
            }}>
              <option value="ask">每次询问</option>
              <option value="tray">最小化到托盘</option>
              <option value="exit">直接退出</option>
            </select>
          </div>
          <div className="settings-row">
            <span>静默启动</span>
            <label style={{ display: "flex", alignItems: "center", gap: 6, cursor: "pointer" }}>
              <input type="checkbox" checked={appConfig.silentStart || false} onChange={async (event) => {
                const newConfig = { ...appConfig, silentStart: event.target.checked };
                setAppConfig(newConfig);
                try { await saveAppConfig(newConfig); } catch { /* ignore */ }
              }} />
              <span style={{ fontSize: 12, color: "#5a6e80" }}>启动时最小化到托盘</span>
            </label>
          </div>
          <div className="settings-row">
            <span>开机启动</span>
            <label style={{ display: "flex", alignItems: "center", gap: 6, cursor: "pointer" }}>
              <input type="checkbox" checked={appConfig.autoStart || false} onChange={async (event) => {
                const val = event.target.checked;
                try {
                  await setAutoStart(val);
                  const newConfig = { ...appConfig, autoStart: val };
                  setAppConfig(newConfig);
                  await saveAppConfig(newConfig);
                } catch (e) { setError(e instanceof Error ? e.message : "设置失败"); }
              }} />
              <span style={{ fontSize: 12, color: "#5a6e80" }}>开机时自动运行</span>
            </label>
          </div>

          {loginProfiles && loginProfiles.profiles.length > 0 ? (
            <>
              <div className="settings-section-label">登录历史</div>
              {loginProfiles.profiles.map((p) => (
                <div key={p.email} className="settings-row">
                  <span>{p.email}{p.autoLogin ? " (自动登录)" : ""}</span>
                  <button className="btn btn-sm" type="button" onClick={async () => {
                    try { await deleteLoginProfile(p.email); const updated = await readLoginProfiles(); setLoginProfiles(updated); } catch (e) { setError(e instanceof Error ? e.message : "删除失败"); }
                  }}>移除</button>
                </div>
              ))}
            </>
          ) : null}

          <div className="settings-section-label">运行时</div>
          <div className="settings-row"><span>Runtime 状态</span><span>{runtimeReady ? "运行中" : runtimeStatus.running ? "已启动但未就绪" : "未运行"}</span></div>
          <div className="settings-row"><span>Runtime Node ID</span><span>{runtimeStatus.nodeId || "未生成"}</span></div>
          <div className="settings-row"><span>Relay TCP</span><span>{runtimeStatus.relayTcpUrl || "未配置"}</span></div>
          <div className="settings-row"><span>EasyTier 打包</span><span>当前发行包会把 EasyTier runtime 一并放入 runtime/</span></div>
          <div className="settings-row"><span>配置目录</span><span>{desktopHostPaths.configDir}</span></div>
          <div className="settings-row"><span>日志目录</span><span>{desktopHostPaths.logDir}</span></div>

          {currentUser?.role === "admin" ? (
            <>
              <div className="settings-section-label">管理</div>
              <div className="settings-row"><span>服务端指标</span><span>{serverMetrics ? `${serverMetrics.onlineNodes}/${serverMetrics.registeredNodes} 节点在线` : "未读取"}</span></div>
              {serverMetrics ? <div className="settings-row"><span>已配置隧道</span><span>{serverMetrics.configuredTunnels}</span></div> : null}
            </>
          ) : null}
          {runtimeStatus.lastError ? <div className="surface-banner danger">{runtimeStatus.lastError}</div> : null}
          <div className="settings-actions">
            <button className="btn" type="button" onClick={() => void startEmbeddedRuntime(cloudEndpointLabel, true)} disabled={!connectionReady || busy === "runtime-start"}>重新启动 Runtime</button>
            <button className="btn" type="button" onClick={() => void openRuntimeLog("stdout")} disabled={!runtimeStatus.stdoutLogPath}>打开 stdout 日志</button>
            <button className="btn" type="button" onClick={() => void openRuntimeLog("stderr")} disabled={!runtimeStatus.stderrLogPath}>打开 stderr 日志</button>
            <button className="btn" type="button" onClick={() => void stopRuntime().then(setRuntimeStatus).catch((runtimeError) => setError(runtimeError instanceof Error ? runtimeError.message : "停止运行时失败"))} disabled={!runtimeStatus.running}>停止 Runtime</button>
          </div>
          <div className="settings-actions" style={{ marginTop: 12, borderTop: "1px solid rgba(0,0,0,0.06)", paddingTop: 12 }}>
            <button className="btn btn-danger" type="button" onClick={() => void handleLogout()}>退出当前账号</button>
          </div>
        </div>
      </div>
    );
  }

  function renderBootstrapShell(title: string, body: string, content?: ReactNode, danger = false) {
    return (
      <Shell bootstrap>
        <div className="window app-surface">
          {toastState && toastState.phase !== "gone" ? (
            <div
              className={`toast-bubble ${toastState.tone} ${toastState.phase === "fade" ? "toast-fading" : ""}`}
              onMouseEnter={handleToastMouseEnter}
              onMouseLeave={handleToastMouseLeave}
            >
              <i className={toastState.tone === "danger" ? "fas fa-exclamation-circle" : "fas fa-info-circle"} />
              {toastState.text}
            </div>
          ) : null}
          <div className="title-bar" onMouseDown={handleWindowDrag} onDoubleClick={handleWindowToggleMaximize}>
            <i className="fas fa-tower-broadcast app-icon" />
            <span className="app-title">驻阡陌</span>            <div className="window-controls">
              <button type="button" onClick={handleWindowMinimize}>—</button>
              <button type="button" onClick={handleWindowToggleMaximize}>☐</button>
              <button type="button" className="close" onClick={handleWindowClose}>✕</button>
            </div>
          </div>
          <div className="bootstrap-content">
            <div className="bootstrap-panel-shell">
              <SurfaceCard title={title} body={body} danger={danger}>
                {content}
              </SurfaceCard>
            </div>
          </div>
          {closeDialogOpen ? (
            <div className="modal-overlay" onClick={(e) => e.target === e.currentTarget && setCloseDialogOpen(false)}>
              <div className="modal-dialog">
                <h3>关闭确认</h3>
                <p>您希望关闭时如何处理？</p>
                <CloseDialogChoice onClick={handleCloseDialogChoice} onCancel={() => setCloseDialogOpen(false)} />
              </div>
            </div>
          ) : null}
          {deleteConfirmTarget ? (
            <div className="modal-overlay" onClick={(e) => e.target === e.currentTarget && setDeleteConfirmTarget(null)}>
              <div className="modal-dialog">
                <h3>确认删除</h3>
                <p>确定删除账号 {deleteConfirmTarget} 的登录记录？</p>
                <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
                  <button className="btn" type="button" onClick={() => setDeleteConfirmTarget(null)}>取消</button>
                  <button className="btn btn-danger" type="button" onClick={async () => {
                    const email = deleteConfirmTarget;
                    setDeleteConfirmTarget(null);
                    try {
                      await deleteLoginProfile(email);
                      const updated = await readLoginProfiles();
                      setLoginProfiles(updated);
                      if (loginForm.email === email) {
                        setLoginForm((current) => ({ ...current, email: updated?.profiles[0]?.email || "", password: "" }));
                        setSavePassword(false);
                        setAutoLogin(false);
                      }
                      if (!updated?.profiles.length) setProfileListOpen(false);
                    } catch (err) { setError(err instanceof Error ? err.message : "删除失败"); }
                  }}>删除</button>
                </div>
              </div>
            </div>
          ) : null}
        </div>
      </Shell>
    );
  }

  function renderCurrentPage() {
    switch (currentRoute.key) {
      case "services":
        return renderServicesPage();
      case "publish":
        return renderPublishPage();
      case "diagnostics":
        return renderDiagnosticsPage();
      case "p2p":
        return renderP2PPage();
      case "settings":
        return renderSettingsPage();
      default:
        return renderDashboardPage();
    }
  }

  if (bootstrapRequired === null) {
    return renderBootstrapShell("初始化中", error || "正在读取账号与设备状态…", undefined, Boolean(error));
  }

  if (bootstrapRequired) {
    return renderBootstrapShell("需要初始化", "请先通过管理面完成初始化配置。", undefined, true);
  }

  if (!connectionReady) {
    const isCustomPreset = selectedRelayPresetId === "custom";
    return renderBootstrapShell(
      "选择云站点",
      "选择或填写云站点后连接。",
      <>
        <div className="surface-form">
          <label>
            <span>云站点</span>
            <select value={selectedRelayPresetId} onChange={(event) => {
              const preset = relayEndpointPresets.find((item) => item.id === event.target.value) || initialRelayEndpointPreset;
              setSelectedRelayPresetId(preset.id);
              setApiDraft(preset.apiBaseUrl);
              setCloudPublicHost(preset.publicHost || defaultDesktopPublicHost);
            }}>
              {relayEndpointPresets.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
            </select>
          </label>
          {isCustomPreset ? (
            <>
              <label>
                <span>API 地址</span>
                <input placeholder="https://publisher.manage.020309.top" value={apiDraft} onChange={(event) => setApiDraft(event.target.value)} />
              </label>
              <label>
                <span>公网入口服务</span>
                <input placeholder="publisher.manage.020309.top" value={cloudPublicHost} onChange={(event) => setCloudPublicHost(event.target.value)} />
              </label>
            </>
          ) : null}
          <button className="btn btn-primary" type="button" onClick={() => void validateCloudConnection()} disabled={busy === "connect-cloud"}><i className="fas fa-link" /> {busy === "connect-cloud" ? "连接中..." : "连接并继续"}</button>
        </div>
      </>,
    );
  }

  if (!currentUser) {
    return renderBootstrapShell(
      "登录",
      "登录您的账号以开始使用。",
      <>
        <form className="surface-form" onSubmit={handleLogin}>
          <label>
            <span>邮箱</span>
            {loginProfiles && loginProfiles.profiles.length > 0 ? (
              <div className="profile-selector">
                <div className="profile-input-wrap">
                  <input value={loginForm.email} onChange={(event) => setLoginForm((current) => ({ ...current, email: event.target.value }))} required onFocus={() => setProfileListOpen(false)} />
                  <button type="button" className="profile-arrow-btn" onClick={() => setProfileListOpen(!profileListOpen)}>
                    <i className={`fas fa-chevron-${profileListOpen ? "up" : "down"}`} />
                  </button>
                </div>
                {profileListOpen ? (
                  <div className="profile-dropdown">
                    {loginProfiles.profiles.map((p) => (
                      <div key={p.email} className={`profile-row${loginForm.email === p.email ? " selected" : ""}`} onClick={async () => {
                        setLoginForm((current) => ({ ...current, email: p.email }));
                        if (desktopTransport) {
                          try {
                            const pw = await decryptLoginPassword(p.email);
                            if (pw) {
                              setLoginForm((current) => ({ ...current, password: pw }));
                              setSavePassword(true);
                            } else {
                              setLoginForm((current) => ({ ...current, password: "" }));
                              setSavePassword(false);
                            }
                            setAutoLogin(p.autoLogin);
                          } catch {
                            setLoginForm((current) => ({ ...current, password: "" }));
                            setSavePassword(false);
                            setAutoLogin(false);
                          }
                        }
                        setProfileListOpen(false);
                      }}>
                        <span className="profile-email">{p.email}</span>
                        <button type="button" className="profile-delete-btn" onClick={(e) => {
                          e.stopPropagation();
                          setDeleteConfirmTarget(p.email);
                        }}><i className="fas fa-trash-alt" /></button>
                      </div>
                    ))}
                  </div>
                ) : null}
              </div>
            ) : (
              <input value={loginForm.email} onChange={(event) => setLoginForm((current) => ({ ...current, email: event.target.value }))} required />
            )}
          </label>
          <label>
            <span>密码</span>
            <input type="password" value={loginForm.password} onChange={(event) => setLoginForm((current) => ({ ...current, password: event.target.value }))} required />
          </label>
          <label style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer" }}>
            <input type="checkbox" checked={savePassword} onChange={(event) => {
              if (autoLogin && !event.target.checked) return; // can't uncheck when autoLogin is on
              setSavePassword(event.target.checked);
            }} disabled={autoLogin} />
            <span style={{ fontSize: 13 }}>保存密码{autoLogin ? "（自动登录需要）" : ""}</span>
          </label>
          <label style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer" }}>
            <input type="checkbox" checked={autoLogin} onChange={(event) => {
              const val = event.target.checked;
              setAutoLogin(val);
              if (val) setSavePassword(true); // auto-login requires saved password
            }} />
            <span style={{ fontSize: 13 }}>自动登录</span>
          </label>
          <button className="btn btn-primary" type="submit" disabled={busy === "login"}><i className="fas fa-right-to-bracket" /> {busy === "login" ? "登录中..." : "进入发布器"}</button>
        </form>
      </>,
    );
  }

  return (
    <Shell>
      <div className="window app-surface">
        {toastState && toastState.phase !== "gone" ? (
          <div
            className={`toast-bubble ${toastState.tone} ${toastState.phase === "fade" ? "toast-fading" : ""}`}
            onMouseEnter={handleToastMouseEnter}
            onMouseLeave={handleToastMouseLeave}
          >
            <i className={toastState.tone === "danger" ? "fas fa-exclamation-circle" : "fas fa-info-circle"} />
            {toastState.text}
          </div>
        ) : null}
        {renderDrawer()}
        <div className="title-bar" onMouseDown={handleWindowDrag} onDoubleClick={handleWindowToggleMaximize}>
          <i className="fas fa-shield-alt app-icon" />
          <span className="app-title">驻阡陌</span>
          <div className="window-controls">
            <button type="button" onClick={handleWindowMinimize}>—</button>
            <button type="button" onClick={handleWindowToggleMaximize}>☐</button>
            <button type="button" className="close" onClick={handleWindowClose}>✕</button>
          </div>
        </div>

        <div className="app-main">
          <div className="nav-sidebar">
            <div className="nav-header">
              <div className="device-badge">
                <i className="fas fa-server device-icon" />
                <div className="device-info">{currentDevice?.nodeName || "当前设备未绑定"}<small>{currentDevice ? `已绑定 · ${currentDevice.status}` : "未绑定 · 待配置"}</small></div>
              </div>
            </div>

            <div className="nav-menu">
              {publisherRoutes.map((route) => (
                <NavLink key={route.key} to={route.path} className={({ isActive }) => isActive ? "nav-item active" : "nav-item"}>
                  <i className={route.icon} /> {route.label}
                </NavLink>
              ))}
            </div>

            <div className="sys-res-card">
              <div className="sys-title"><i className="fas fa-chart-bar" /> 当前软件资源</div>
              <div className="resource-item">
                <div className="res-header"><span>CPU</span><span>{appUsage.cpuPercent != null ? `${Math.round(appUsage.cpuPercent)}%` : "--"}</span></div>
                <div className="progress-bar"><div className="progress-fill" style={{ width: `${Math.min(100, Math.max(0, Math.round(appUsage.cpuPercent || 0)))}%` }} /></div>
              </div>
              <div className="resource-item">
                <div className="res-header"><span>内存</span><span>{appUsage.memoryMb != null ? `${Math.round(appUsage.memoryMb)} MB` : "--"}</span></div>
                <div className="progress-bar"><div className="progress-fill" style={{ width: `${Math.min(100, Math.max(6, Math.round((appUsage.memoryMb || 0) / 4)))}%` }} /></div>
              </div>
            </div>

            <div className="nav-footer">
              <div className="user-profile">
                <div className="user-avatar">{userInitial}</div>
                <div className="user-details">
                  <div className="user-name">{currentUser.email}</div>
                  <div className="user-role">设备管理员</div>
                </div>
              </div>
              <div className="version-tag">
                <span>v0.1.0</span>
                <span><i className="fas fa-circle" style={{ color: syncIssue ? "#dc2626" : "#f59e0b", fontSize: 8 }} /> {syncIssue ? "异常" : "在线"}</span>
              </div>
            </div>
          </div>

          <div className="content-area">
            <div className="content-scroll-area">{renderCurrentPage()}</div>
            <div className="footer-status">
              <i className="fas fa-circle" style={{ color: syncIssue ? "#dc2626" : "#f59e0b" }} />
              {syncIssue ? `请求异常 · ${syncIssue}` : `在线 · ${currentRouteLabel}`}
              <span style={{ marginLeft: 20 }}><i className="fas fa-arrow-down" /> {downRateLabel} <i className="fas fa-arrow-up" style={{ marginLeft: 16 }} /> {upRateLabel}</span>
              <span style={{ marginLeft: "auto" }}>上次同步: {lastRefreshAt ? formatDate(lastRefreshAt) : "尚无"}</span>
            </div>
          </div>
        </div>
        {trafficCalendarOpen ? (
          <div className="modal-overlay" onClick={(e) => e.target === e.currentTarget && setTrafficCalendarOpen(false)}>
            <div className="modal-dialog traffic-calendar-dialog">
              <div className="traffic-calendar-head">
                <div>
                  <h3>流量月历</h3>
                  <p>统计口径为当前发布器节点的日累计与月累计流量，账本保存在配置目录，不会因为重新下载便携版而清零。</p>
                </div>
                <button className="btn" type="button" onClick={() => setTrafficCalendarOpen(false)}>关闭</button>
              </div>
              <div className="traffic-calendar-toolbar">
                <button className="btn" type="button" onClick={() => setTrafficCalendarMonth(previousMonthKey(trafficCalendarMonth))}>上个月</button>
                <strong>{monthLabel(trafficCalendarMonth)}</strong>
                <button className="btn" type="button" onClick={() => setTrafficCalendarMonth(nextMonthKey(trafficCalendarMonth))}>下个月</button>
              </div>
              <div className="traffic-calendar-summary">
                <div className="metric-box"><span>月累计</span><strong>{formatBytesTotal(selectedTrafficMonthSummary?.totalBytes || 0)}</strong></div>
                <div className="metric-box"><span>下载</span><strong>{formatBytesTotal(selectedTrafficMonthSummary?.downBytes || 0)}</strong></div>
                <div className="metric-box"><span>上传</span><strong>{formatBytesTotal(selectedTrafficMonthSummary?.upBytes || 0)}</strong></div>
                <div className="metric-box"><span>活跃天数</span><strong>{String(selectedTrafficMonthSummary?.dayCount || 0)}</strong></div>
              </div>
              <div className="traffic-calendar-grid traffic-calendar-weekdays">
                {["日", "一", "二", "三", "四", "五", "六"].map((item) => <div key={item} className="traffic-weekday">{item}</div>)}
              </div>
              <div className="traffic-calendar-grid">
                {trafficMonthDays.map((item, index) => item ? (
                  <div key={item.date} className={`traffic-day-cell ${item.entry ? "has-traffic" : ""}`}>
                    <div className="traffic-day-top">
                      <strong>{item.day}</strong>
                      <span>{formatBytesCompact(item.entry?.totalBytes || 0)}</span>
                    </div>
                    <div className="traffic-day-lines">
                      <span>↓ {formatBytesCompact(item.entry?.downBytes || 0)}</span>
                      <span>↑ {formatBytesCompact(item.entry?.upBytes || 0)}</span>
                    </div>
                  </div>
                ) : <div key={`empty-${index}`} className="traffic-day-cell traffic-day-empty" />)}
              </div>
              <div className="traffic-month-list">
                {trafficHistory.months.length === 0 ? (
                  <div className="weak-note">当前还没有持久化流量数据。</div>
                ) : trafficHistory.months.map((item) => (
                  <button key={item.month} type="button" className={`traffic-month-row ${item.month === trafficCalendarMonth ? "active" : ""}`} onClick={() => setTrafficCalendarMonth(item.month)}>
                    <span>{monthLabel(item.month)}</span>
                    <strong>{formatBytesTotal(item.totalBytes)}</strong>
                  </button>
                ))}
              </div>
            </div>
          </div>
        ) : null}
        {closeDialogOpen ? (
          <div className="modal-overlay" onClick={(e) => e.target === e.currentTarget && setCloseDialogOpen(false)}>
            <div className="modal-dialog">
              <h3>关闭确认</h3>
              <p>您希望关闭时如何处理？</p>
              <CloseDialogChoice onClick={handleCloseDialogChoice} onCancel={() => setCloseDialogOpen(false)} />
            </div>
          </div>
        ) : null}
      </div>
    </Shell>
  );
}

function Shell({ children, bootstrap = false }: { children: ReactNode; bootstrap?: boolean }) {
  return <div className={bootstrap ? "window-shell bootstrap-shell" : "window-shell"}>{children}</div>;
}

function SurfaceCard({ title, body, danger = false, children }: { title: string; body: string; danger?: boolean; children?: ReactNode }) {
  return (
    <div className="surface-card">
      <p className="surface-eyebrow">驻阡陌</p>
      <h1>{title}</h1>
      <p className={`surface-copy ${danger ? "surface-copy-danger" : ""}`}>{body}</p>
      {children}
    </div>
  );
}

function MetricBox({ label, value }: { label: string; value: string }) {
  return (
    <div className="metric-box">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function CloseDialogChoice({ onClick, onCancel }: { onClick: (action: "tray" | "exit", dontAskAgain: boolean) => void; onCancel: () => void }) {
  const [dontAsk, setDontAsk] = useState(false);
  return (
    <div className="close-dialog-actions">
      <label style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer", marginBottom: 12 }}>
        <input type="checkbox" checked={dontAsk} onChange={(e) => setDontAsk(e.target.checked)} />
        <span style={{ fontSize: 13 }}>以后不再询问</span>
      </label>
      <div style={{ display: "flex", gap: 8 }}>
        <button className="btn btn-primary" type="button" onClick={() => onClick("tray", dontAsk)}>最小化到托盘</button>
        <button className="btn" type="button" onClick={() => onClick("exit", dontAsk)}>退出程序</button>
        <button className="btn" type="button" onClick={onCancel} style={{ marginLeft: "auto" }}>取消</button>
      </div>
    </div>
  );
}

function ServiceMetadataEditor({
  form,
  onChange,
  targetPortHint,
  nodeIdHint,
}: {
  form: ServiceMetadataDraft;
  onChange: (patch: Partial<ServiceMetadataDraft>) => void;
  targetPortHint: string;
  nodeIdHint: string;
}) {
  const applyTemplate = (template: ServiceTemplateKey) => {
    onChange(buildServiceTemplate(template, targetPortHint));
  };

  return (
    <div className="service-meta-panel">
      <div className="service-meta-header">
        <div>
          <strong>服务工作台登记</strong>
          <p>把这条发布规则登记成网盘、图床或其他服务后，云端目录和用户端工作台才能按业务身份识别它，而不是只把它当普通隧道。</p>
        </div>
        <div className="service-meta-actions">
          <button className="btn btn-sm" type="button" onClick={() => applyTemplate("drive")}><i className="fas fa-hard-drive" /> 网盘模板</button>
          <button className="btn btn-sm" type="button" onClick={() => applyTemplate("gallery")}><i className="fas fa-images" /> 图床模板</button>
          <button className="btn btn-sm" type="button" onClick={() => onChange({ ...emptyServiceMetadataDraft })}><i className="fas fa-eraser" /> 清空登记</button>
        </div>
      </div>

      <div className="surface-banner info" style={{ marginTop: 0 }}>
        {form.serviceKey.trim()
          ? `当前登记为 ${serviceKindLabel(form.serviceKind)} · ${form.serviceKey.trim()}。留空的 P2P 节点和端口会自动沿用当前发布规则。`
          : "不填写服务标识时，这条规则仍会正常发布，但用户端不会把它识别成网盘或图床工作台入口。"}
      </div>

      <div className="service-meta-grid">
        <label>
          <span>服务标识</span>
          <input value={form.serviceKey} onChange={(event) => onChange({ serviceKey: event.target.value })} placeholder="例如 drive、gallery" />
          <small>这是服务目录里的稳定 key。建议网盘用 `drive`，图床用 `gallery`。</small>
        </label>

        <label>
          <span>服务标题</span>
          <input value={form.serviceTitle} onChange={(event) => onChange({ serviceTitle: event.target.value })} placeholder="例如 网盘服务" />
          <small>用户端与云端目录里展示给用户看的名称。</small>
        </label>

        <label>
          <span>服务类型</span>
          <select value={form.serviceKind} onChange={(event) => onChange({ serviceKind: event.target.value as "app" | "drive" | "gallery" })}>
            <option value="app">通用应用</option>
            <option value="drive">网盘</option>
            <option value="gallery">图床</option>
          </select>
          <small>用于用户端工作台决定采用哪类界面与交互。</small>
        </label>

        <label>
          <span>用户端首选路径</span>
          <select value={form.servicePreferredPath} onChange={(event) => onChange({ servicePreferredPath: event.target.value as "dual" | "cloud" | "p2p" })}>
            <option value="dual">dual / 双入口</option>
            <option value="cloud">cloud / 云端优先</option>
            <option value="p2p">p2p / P2P 优先</option>
          </select>
          <small>用户端工作台会据此决定默认展示哪条入口策略。</small>
        </label>

        <label className="drawer-wide-field">
          <span>服务说明</span>
          <textarea value={form.serviceSummary} onChange={(event) => onChange({ serviceSummary: event.target.value })} placeholder="说明公网入口和 P2P 工作台各自承担什么职责" />
          <small>建议直接写清楚云端入口与 P2P 入口的分工，便于后续联调和运营。</small>
        </label>

        <label>
          <span>云端入口权限</span>
          <select value={form.serviceCloudAccess} onChange={(event) => onChange({ serviceCloudAccess: event.target.value as "all_users" | "admin_only" | "disabled" })}>
            <option value="all_users">all_users / 全部用户</option>
            <option value="admin_only">admin_only / 仅管理员</option>
            <option value="disabled">disabled / 禁用</option>
          </select>
          <small>控制云端目录和公开入口对哪些人可见。</small>
        </label>

        <label>
          <span>P2P 入口权限</span>
          <select value={form.serviceP2PAccess} onChange={(event) => onChange({ serviceP2PAccess: event.target.value as "all_users" | "admin_only" | "disabled" })}>
            <option value="all_users">all_users / 全部用户</option>
            <option value="admin_only">admin_only / 仅管理员</option>
            <option value="disabled">disabled / 禁用</option>
          </select>
          <small>控制用户端工作台能否使用这条 P2P 服务入口。</small>
        </label>

        <label>
          <span>P2P 节点 ID</span>
          <input value={form.serviceP2PNodeId} onChange={(event) => onChange({ serviceP2PNodeId: event.target.value })} placeholder={nodeIdHint ? `留空则沿用当前规则节点 ${nodeIdHint}` : "留空则沿用当前规则节点"} />
          <small>当 P2P 实际业务服务跑在另一台服务端时，在这里显式指定节点。</small>
        </label>

        <label>
          <span>P2P 目标端口</span>
          <input value={form.serviceP2PTargetPort} onChange={(event) => onChange({ serviceP2PTargetPort: event.target.value })} inputMode="numeric" placeholder={targetPortHint ? `留空则复用 targetPort ${targetPortHint}` : "留空则复用当前规则 targetPort"} />
          <small>例如网盘 `5212`、图床 `8180`；不填就沿用这条规则的目标端口。</small>
        </label>

        <label>
          <span>P2P 路径</span>
          <input value={form.serviceP2PPath} onChange={(event) => onChange({ serviceP2PPath: event.target.value })} placeholder="例如 / 或 /workspace/" />
          <small>用户端工作台最终会打开到这条路径。</small>
        </label>
      </div>

      <div className="service-meta-advanced">
        <div className="service-meta-advanced-title">高级覆盖</div>
        <div className="service-meta-grid">
          <label>
            <span>云端入口 URL</span>
            <input value={form.servicePublicUrl} onChange={(event) => onChange({ servicePublicUrl: event.target.value })} placeholder="留空则按当前发布规则自动推导" />
            <small>只有在你需要覆盖默认公网入口时才填写。</small>
          </label>

          <label>
            <span>P2P 入口 URL</span>
            <input value={form.serviceP2PUrl} onChange={(event) => onChange({ serviceP2PUrl: event.target.value })} placeholder="留空则按 EasyTier IPv4 + 目标端口自动推导" />
            <small>只有在你需要完全手动指定 P2P 工作台地址时才填写。</small>
          </label>
        </div>
      </div>
    </div>
  );
}

function findRouteMeta(pathname: string) {
  return publisherRoutes.find((item) => item.path === pathname) || publisherRoutes[0];
}

function resolveLinkedService(tunnel: TunnelSpec, services: LocalServiceDraft[]) {
  return services.find((item) => item.targetHost === tunnel.targetHost && Number(item.targetPort) === tunnel.targetPort) || null;
}

function supportsOpenEntry(tunnel: TunnelSpec, state: ReturnType<typeof evaluateRuleState>, entry: CloudEntry) {
  return (tunnel.type === "http" || tunnel.type === "https") && state.entryUsable && Boolean(entry.publicUrl);
}

function supportsProbe(tunnel: TunnelSpec, state: ReturnType<typeof evaluateRuleState>, entry: CloudEntry) {
  return (tunnel.type === "http" || tunnel.type === "https") && state.entryUsable && Boolean(entry.publicUrl);
}

function formatRate(bytesPerSecond: number) {
  if (!Number.isFinite(bytesPerSecond) || bytesPerSecond <= 0) return "0 KB/s";
  if (bytesPerSecond >= 1024 * 1024) return `${(bytesPerSecond / 1024 / 1024).toFixed(2)} MB/s`;
  return `${Math.max(bytesPerSecond / 1024, 0).toFixed(2)} KB/s`;
}

function formatBytesTotal(bytes?: number | null) {
  if (bytes == null || bytes <= 0) return "0 KB";
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(2)} MB`;
  return `${Math.max(bytes / 1024, 0).toFixed(2)} KB`;
}

function formatBytesCompact(bytes?: number | null) {
  if (bytes == null || bytes <= 0) return "0";
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)}G`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)}M`;
  return `${Math.max(bytes / 1024, 0).toFixed(1)}K`;
}

function currentMonthKey(date: Date) {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  return `${year}-${month}`;
}

function monthLabel(month: string) {
  if (!/^\d{4}-\d{2}$/.test(month)) return month || "当月";
  return `${month.slice(0, 4)}年${month.slice(5, 7)}月`;
}

function previousMonthKey(month: string) {
  const [year, value] = month.split("-").map(Number);
  const date = Number.isFinite(year) && Number.isFinite(value) ? new Date(year, value - 2, 1) : new Date();
  return currentMonthKey(date);
}

function nextMonthKey(month: string) {
  const [year, value] = month.split("-").map(Number);
  const date = Number.isFinite(year) && Number.isFinite(value) ? new Date(year, value, 1) : new Date();
  return currentMonthKey(date);
}

function buildTrafficCalendar(month: string, days: TrafficHistoryDayEntry[]) {
  const [year, value] = month.split("-").map(Number);
  const monthDate = Number.isFinite(year) && Number.isFinite(value) ? new Date(year, value - 1, 1) : new Date();
  const firstWeekday = monthDate.getDay();
  const daysInMonth = new Date(monthDate.getFullYear(), monthDate.getMonth() + 1, 0).getDate();
  const dayMap = new Map(days.filter((item) => item.month === currentMonthKey(monthDate)).map((item) => [item.date, item]));
  const cells = [] as Array<{ date: string; day: number; entry: TrafficHistoryDayEntry | null } | null>;
  for (let index = 0; index < firstWeekday; index += 1) {
    cells.push(null);
  }
  for (let day = 1; day <= daysInMonth; day += 1) {
    const date = `${currentMonthKey(monthDate)}-${String(day).padStart(2, "0")}`;
    cells.push({ date, day, entry: dayMap.get(date) || null });
  }
  return cells;
}

function metricNumber(value: string | undefined) {
  const parsed = Number(value || 0);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function sumTunnelTrafficMetrics(metrics: Record<string, string> | undefined, tunnels: TunnelSpec[]) {
  if (!metrics || tunnels.length === 0) {
    return { downTotal: 0, upTotal: 0 };
  }
  let matchedPerTunnel = false;
  let downTotal = 0;
  let upTotal = 0;
  for (const tunnel of tunnels) {
    const downKey = `traffic:down:${tunnel.id}`;
    const upKey = `traffic:up:${tunnel.id}`;
    const hasDown = Object.prototype.hasOwnProperty.call(metrics, downKey);
    const hasUp = Object.prototype.hasOwnProperty.call(metrics, upKey);
    if (hasDown || hasUp) {
      matchedPerTunnel = true;
    }
    downTotal += metricNumber(metrics[downKey]);
    upTotal += metricNumber(metrics[upKey]);
  }
  if (matchedPerTunnel) {
    return { downTotal, upTotal };
  }
  const fallbackDown = metricNumber(metrics["traffic:down_total"]);
  const fallbackUp = metricNumber(metrics["traffic:up_total"]);
  if (fallbackDown > 0 || fallbackUp > 0) {
    return { downTotal: fallbackDown, upTotal: fallbackUp };
  }
  const trafficKeys = Object.keys(metrics).filter((k) => k.startsWith("traffic:"));
  for (const key of trafficKeys) {
    const val = metricNumber(metrics[key]);
    if (val > 0) {
      if (key.startsWith("traffic:down:")) {
        downTotal += val;
      } else if (key.startsWith("traffic:up:")) {
        upTotal += val;
      }
    }
  }
  if (downTotal > 0 || upTotal > 0) {
    return { downTotal, upTotal };
  }
  return { downTotal: 0, upTotal: 0 };
}
