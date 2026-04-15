import { invoke } from "@tauri-apps/api/core";
import type { DesktopApiTransport, DesktopApiTransportResponse } from "../../../packages/desktop-core/src/api";

export type RuntimeStatus = {
  available: boolean;
  running: boolean;
  healthy: boolean;
  pid: number | null;
  startedAt: number | null;
  nodeId: string;
  nodeName: string;
  apiBaseUrl: string;
  relayTcpUrl: string;
  relayUdpUrl: string;
  executablePath: string;
  workDir: string;
  stdoutLogPath: string;
  stderrLogPath: string;
  lastError: string;
};

type RuntimeStartInput = {
  apiBaseUrl: string;
  nodeId: string;
  nodeName?: string;
  relayTcpUrl?: string;
  relayUdpUrl?: string;
};

type HostHttpRequestInput = {
  url: string;
  method?: string;
  headers?: Record<string, string>;
  body?: string;
};

type HostHttpResponse = {
  status: number;
  ok: boolean;
  body: string;
  setCookies?: string[];
};

export type DesktopAppUsage = {
  available: boolean;
  cpuPercent: number | null;
  memoryMb: number | null;
  readBytes: number | null;
  writeBytes: number | null;
  sampledAt: number;
};

export type TunnelTrafficEntry = {
  tunnelId: string;
  downBytes: number;
  upBytes: number;
};

export type AgentTrafficSnapshot = {
  tunnels: TunnelTrafficEntry[];
  downTotal: number;
  upTotal: number;
  sampledAt: number;
};

export async function loadRuntimeStatus(): Promise<RuntimeStatus> {
  try {
    return await invoke<RuntimeStatus>("runtime_status");
  } catch {
    return {
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
      lastError: "未接入 Tauri 宿主",
    };
  }
}

export async function ensureRuntimeStarted(input: RuntimeStartInput): Promise<RuntimeStatus> {
  try {
    return await invoke<RuntimeStatus>("runtime_start", { input });
  } catch (error) {
    const detail = typeof error === "string" ? error : error instanceof Error ? error.message : "启动失败";
    throw new Error(detail);
  }
}

export async function stopRuntime(): Promise<RuntimeStatus> {
  try {
    return await invoke<RuntimeStatus>("runtime_stop");
  } catch (error) {
    const detail = typeof error === "string" ? error : error instanceof Error ? error.message : "停止失败";
    throw new Error(detail);
  }
}

export async function openRuntimeLog(kind: "stdout" | "stderr"): Promise<void> {
  try {
    await invoke("open_runtime_log", { kind });
  } catch (error) {
    const detail = typeof error === "string" ? error : error instanceof Error ? error.message : "打开日志失败";
    throw new Error(detail);
  }
}

export async function loadDesktopHostPaths() {
  try {
    const [configDir, logDir] = await Promise.all([invoke<string>("config_dir"), invoke<string>("log_dir")]);
    return { configDir, logDir, available: true };
  } catch {
    return { configDir: "未接入 Tauri 宿主", logDir: "未接入 Tauri 宿主", available: false };
  }
}

export async function loadDesktopAppUsage(): Promise<DesktopAppUsage> {
  try {
    return await invoke<DesktopAppUsage>("app_resource_usage");
  } catch {
    return {
      available: false,
      cpuPercent: null,
      memoryMb: null,
      readBytes: null,
      writeBytes: null,
      sampledAt: Date.now(),
    };
  }
}

export async function openDesktopExternal(url: string): Promise<void> {
  try {
    await invoke("open_external", { url });
  } catch (error) {
    const detail = typeof error === "string" ? error : error instanceof Error ? error.message : "打开失败";
    throw new Error(detail);
  }
}

export async function loadAgentTraffic(): Promise<AgentTrafficSnapshot | null> {
  try {
    return await invoke<AgentTrafficSnapshot>("agent_traffic");
  } catch {
    return null;
  }
}

function mergeResponseCookies(cookieJar: Map<string, string>, setCookies: string[] | undefined) {
  if (!setCookies?.length) return;
  for (const item of setCookies) {
    const segments = item.split(";").map((part) => part.trim()).filter(Boolean);
    const pair = segments[0];
    if (!pair) continue;
    const equalsIndex = pair.indexOf("=");
    if (equalsIndex <= 0) continue;
    const name = pair.slice(0, equalsIndex).trim();
    const value = pair.slice(equalsIndex + 1);
    const shouldDelete = value === "" || segments.some((segment) => {
      const lower = segment.toLowerCase();
      if (lower.startsWith("max-age=")) {
        const age = Number(segment.slice(8));
        return Number.isFinite(age) && age <= 0;
      }
      if (lower.startsWith("expires=")) {
        const expiresAt = Date.parse(segment.slice(8));
        return Number.isFinite(expiresAt) && expiresAt <= Date.now();
      }
      return false;
    });
    if (shouldDelete) {
      cookieJar.delete(name);
      continue;
    }
    cookieJar.set(name, `${name}=${value}`);
  }
}

function cookieHeaderFromJar(cookieJar: Map<string, string>) {
  return Array.from(cookieJar.values()).join("; ");
}

export async function windowStartDrag(): Promise<void> {
  await invoke("window_start_drag");
}

export async function windowMinimize(): Promise<void> {
  await invoke("window_minimize");
}

export async function windowToggleMaximize(): Promise<void> {
  await invoke("window_toggle_maximize");
}

export async function windowRequestClose(): Promise<void> {
  await invoke("window_request_close");
}

export async function appExit(): Promise<void> {
  await invoke("app_exit");
}

export function createTauriDesktopTransport(): DesktopApiTransport | null {
  if (!("__TAURI_INTERNALS__" in window)) {
    return null;
  }
  const cookieJar = new Map<string, string>();
  return async (url: string, init?: RequestInit): Promise<DesktopApiTransportResponse> => {
    const headers: Record<string, string> = {};
    const rawHeaders = init?.headers;
    if (rawHeaders instanceof Headers) {
      rawHeaders.forEach((value, key) => {
        headers[key] = value;
      });
    } else if (Array.isArray(rawHeaders)) {
      for (const [key, value] of rawHeaders) {
        headers[key] = value;
      }
    } else if (rawHeaders) {
      Object.assign(headers, rawHeaders as Record<string, string>);
    }
    const cookieHeader = cookieHeaderFromJar(cookieJar);
    if (cookieHeader) {
      headers.Cookie = cookieHeader;
    }
    const payload: HostHttpRequestInput = {
      url,
      method: init?.method,
      headers,
      body: typeof init?.body === "string" ? init.body : undefined,
    };
    const response = await invoke<HostHttpResponse>("http_request", { input: payload });
    mergeResponseCookies(cookieJar, response.setCookies);
    if (url.includes("/api/auth/logout")) {
      cookieJar.clear();
    }
    return {
      ok: response.ok,
      status: response.status,
      async json() {
        return response.body ? JSON.parse(response.body) : null;
      },
    };
  };
}

export type LoginProfile = {
  email: string;
  encryptedPassword: string;
  autoLogin: boolean;
};

export type LoginProfilesFile = {
  profiles: LoginProfile[];
  lastUsedEmail: string | null;
};

export type WindowBounds = {
  x: number;
  y: number;
  width: number;
  height: number;
  maximized: boolean;
};

export type AppConfig = {
  closeAction?: "ask" | "tray" | "exit";
  silentStart?: boolean;
  autoStart?: boolean;
};

export async function readLoginProfiles(): Promise<LoginProfilesFile | null> {
  try { return await invoke<LoginProfilesFile>("read_login_profiles"); }
  catch { return null; }
}

export async function saveLoginProfile(email: string, password: string, autoLogin: boolean): Promise<void> {
  await invoke("save_login_profile", { email, password, autoLogin });
}

export async function deleteLoginProfile(email: string): Promise<void> {
  await invoke("delete_login_profile", { email });
}

export async function decryptLoginPassword(email: string): Promise<string> {
  return await invoke<string>("decrypt_login_password", { email });
}

export async function saveWindowBounds(bounds: WindowBounds): Promise<void> {
  await invoke("save_window_bounds", { bounds });
}

export async function loadWindowBounds(): Promise<WindowBounds | null> {
  try { return await invoke<WindowBounds>("load_window_bounds"); }
  catch { return null; }
}

export async function saveAppConfig(config: AppConfig): Promise<void> {
  await invoke("save_app_config", { config });
}

export async function loadAppConfig(): Promise<AppConfig | null> {
  try { return await invoke<AppConfig>("load_app_config"); }
  catch { return null; }
}

export async function loadAutoStartEnabled(): Promise<boolean> {
  try {
    return await invoke<boolean>("auto_start_enabled");
  } catch {
    return false;
  }
}

export async function setAutoStart(enable: boolean): Promise<void> {
  await invoke("set_auto_start", { enable });
}
