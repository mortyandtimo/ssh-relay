import { invoke } from "@tauri-apps/api/core";
import type { DesktopApiTransport, DesktopApiTransportResponse } from "../../../packages/desktop-core/src/api";

export type LoginProfile = {
  email: string;
  encryptedPassword: string;
  autoLogin: boolean;
};

export type LoginProfilesFile = {
  profiles: LoginProfile[];
  lastUsedEmail: string | null;
};

export type AppConfig = {
  apiBaseUrl?: string;
  closeAction?: "ask" | "tray" | "exit";
  p2pAutoStart?: boolean;
  driveFallbackPolicy?: "admin_only" | "never";
  imageBulkUploadMode?: "p2p_bulk_https_light" | "https_only";
  p2pNetworkName?: string;
  p2pNetworkSecret?: string;
  p2pPeerUrl?: string;
  p2pVirtualIpv4?: string;
  p2pUseDhcp?: boolean;
  p2pInstanceName?: string;
  p2pHostname?: string;
};

export type P2PRuntimeStatus = {
  available: boolean;
  configured: boolean;
  running: boolean;
  pid: number | null;
  startedAt: number | null;
  executablePath: string;
  workDir: string;
  stdoutLogPath: string;
  stderrLogPath: string;
  argsSummary: string;
  lastError: string;
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
    } else if (rawHeaders && typeof rawHeaders === "object") {
      Object.assign(headers, rawHeaders);
    }
    const cookieHeader = cookieHeaderFromJar(cookieJar);
    if (cookieHeader) {
      headers.Cookie = cookieHeader;
    }
    const response = await invoke<HostHttpResponse>("http_request", {
      input: {
        url,
        method: init?.method,
        headers,
        body: typeof init?.body === "string" ? init.body : undefined,
      } satisfies HostHttpRequestInput,
    });
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

export async function readLoginProfiles(): Promise<LoginProfilesFile | null> {
  try {
    return await invoke<LoginProfilesFile>("read_login_profiles");
  } catch {
    return null;
  }
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

export async function saveAppConfig(config: AppConfig): Promise<void> {
  await invoke("save_app_config", { config });
}

export async function loadAppConfig(): Promise<AppConfig | null> {
  try {
    return await invoke<AppConfig>("load_app_config");
  } catch {
    return null;
  }
}

export async function loadDesktopHostPaths() {
  try {
    const [configDir, logDir] = await Promise.all([
      invoke<string>("config_dir"),
      invoke<string>("log_dir"),
    ]);
    return { configDir, logDir, available: true };
  } catch {
    return { configDir: "未接入 Tauri 宿主", logDir: "未接入 Tauri 宿主", available: false };
  }
}

export async function loadP2PRuntimeStatus(): Promise<P2PRuntimeStatus> {
  try {
    return await invoke<P2PRuntimeStatus>("p2p_runtime_status");
  } catch {
    return {
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
      lastError: "未接入 Tauri 宿主",
    };
  }
}

export async function startP2PRuntime(): Promise<P2PRuntimeStatus> {
  return await invoke<P2PRuntimeStatus>("p2p_runtime_start");
}

export async function stopP2PRuntime(): Promise<P2PRuntimeStatus> {
  return await invoke<P2PRuntimeStatus>("p2p_runtime_stop");
}

export async function openP2PRuntimeLog(kind: "stdout" | "stderr"): Promise<void> {
  await invoke("open_p2p_runtime_log", { kind });
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
