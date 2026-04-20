import type {
  AuthSettings,
  AuthUserResponse,
  BootstrapStatusResponse,
  ManagedUserListResponse,
  ControlActionOptionsResponse,
  ControlActionRequest,
  ControlActionResponse,
  ControlPanelSummary,
  ControlSurface,
  NodeListResponse,
  ServerMetrics,
  TunnelListResponse,
  TunnelProbeResult,
  UserServiceCatalogResponse,
  CertificateSpec,
  ManagedHTTPSDomainListResponse,
} from "./types";

export type DesktopApiTransportResponse = {
  ok: boolean;
  status: number;
  json(): Promise<unknown>;
};

export type DesktopApiTransport = (url: string, init?: RequestInit) => Promise<DesktopApiTransportResponse>;

export function createDesktopApi(apiBaseUrl = "", transport?: DesktopApiTransport) {
  let refreshInFlight: Promise<boolean> | null = null;

  async function send(url: string, init?: RequestInit): Promise<DesktopApiTransportResponse> {
    if (transport) {
      return transport(url, init);
    }
    return fetch(url, {
      credentials: "include",
      ...init,
    });
  }

  async function requestJSON<T>(path: string, init?: RequestInit, allowRefresh = true): Promise<T> {
    const response = await send(apiBaseUrl + path, {
      ...init,
      headers: {
        Accept: "application/json",
        ...(init?.body ? { "Content-Type": "application/json" } : {}),
        ...(init?.headers || {}),
      },
    });
    const payload = await response.json().catch(() => null);
    if (
      response.status === 401 &&
      allowRefresh &&
      path !== "/api/auth/login" &&
      path !== "/api/auth/register" &&
      path !== "/api/auth/bootstrap-status" &&
      path !== "/api/auth/refresh"
    ) {
      const refreshed = await refreshAuthSession();
      if (refreshed) {
        return requestJSON<T>(path, init, false);
      }
      throw new Error("登录已失效，请重新登录。");
    }
    if (!response.ok) {
      throw new Error(
        payload &&
        typeof payload === "object" &&
        payload !== null &&
        "error" in payload &&
        typeof (payload as { error?: unknown }).error === "string"
          ? (payload as { error: string }).error
          : "请求失败 (" + response.status + ")",
      );
    }
    return payload as T;
  }

  async function refreshAuthSession(): Promise<boolean> {
    if (refreshInFlight) {
      return refreshInFlight;
    }
    const task = (async () => {
      const response = await send(apiBaseUrl + "/api/auth/refresh", {
        method: "POST",
        headers: { Accept: "application/json" },
      });
      return response.ok;
    })();
    refreshInFlight = task;
    try {
      return await task;
    } finally {
      refreshInFlight = null;
    }
  }

  return {
    requestJSON,
    loadBootstrapStatus() {
      return requestJSON<BootstrapStatusResponse>("/api/auth/bootstrap-status", undefined, false);
    },
    loadCurrentUser() {
      return requestJSON<AuthUserResponse>("/api/auth/me");
    },
    loadUserServices() {
      return requestJSON<UserServiceCatalogResponse>("/api/user/services");
    },
    login(email: string, password: string) {
      return requestJSON<AuthUserResponse>(
        "/api/auth/login",
        {
          method: "POST",
          body: JSON.stringify({ email, password }),
        },
        false,
      );
    },
    register(email: string, displayName: string, password: string) {
      return requestJSON<AuthUserResponse>(
        "/api/auth/register",
        {
          method: "POST",
          body: JSON.stringify({ email, displayName, password }),
        },
        false,
      );
    },
    logout() {
      return requestJSON<{ status: string }>("/api/auth/logout", { method: "POST" });
    },
    sendPasswordChangeCode() {
      return requestJSON<{ status: string; expiresInSec: number; email: string }>("/api/auth/password-change/send-code", {
        method: "POST",
        body: JSON.stringify({}),
      });
    },
    confirmPasswordChange(code: string, password: string) {
      return requestJSON<AuthUserResponse>("/api/auth/password-change/confirm", {
        method: "POST",
        body: JSON.stringify({ code, password }),
      });
    },
    loadManagedUsers() {
      return requestJSON<ManagedUserListResponse>("/api/users");
    },
    createManagedUser(payload: { email: string; displayName: string; password: string; role: string }) {
      return requestJSON<AuthUserResponse["user"]>("/api/users", {
        method: "POST",
        body: JSON.stringify(payload),
      });
    },
    updateManagedUser(id: string, payload: Record<string, unknown>) {
      return requestJSON<AuthUserResponse["user"]>("/api/users/" + encodeURIComponent(id), {
        method: "PUT",
        body: JSON.stringify(payload),
      });
    },
    loadAuthSettings() {
      return requestJSON<AuthSettings>("/api/admin/auth-settings");
    },
    updateAuthSettings(publicRegistrationEnabled: boolean) {
      return requestJSON<AuthSettings>("/api/admin/auth-settings", {
        method: "PUT",
        body: JSON.stringify({ publicRegistrationEnabled }),
      });
    },
    loadNodes() {
      return requestJSON<NodeListResponse>("/api/nodes?limit=100&offset=0");
    },
    loadTunnels() {
      return requestJSON<TunnelListResponse>("/api/tunnels");
    },
    loadServerMetrics() {
      return requestJSON<ServerMetrics>("/api/server/metrics");
    },
    updateTunnel(id: string, payload: Record<string, unknown>) {
      return requestJSON("/api/tunnels/" + encodeURIComponent(id), {
        method: "PUT",
        body: JSON.stringify(payload),
      });
    },
    deleteTunnel(id: string) {
      return requestJSON<{ status: string; id: string }>("/api/tunnels/" + encodeURIComponent(id), {
        method: "DELETE",
      });
    },
    probeTunnel(id: string) {
      return requestJSON<TunnelProbeResult>("/api/tunnels/" + encodeURIComponent(id) + "/probe", {
        method: "POST",
      });
    },
    controlAction(payload: ControlActionRequest) {
      return requestJSON<ControlActionResponse>("/api/control-actions", {
        method: "POST",
        body: JSON.stringify(payload),
      });
    },
    loadNodeControlActionOptions(nodeId: string, surface: ControlSurface) {
      return requestJSON<ControlActionOptionsResponse>("/api/control-actions/node/" + encodeURIComponent(nodeId) + "/" + encodeURIComponent(surface) + "/options");
    },
    loadTunnelControlActionOptions(tunnelId: string, surface: ControlSurface) {
      return requestJSON<ControlActionOptionsResponse>("/api/control-actions/tunnel/" + encodeURIComponent(tunnelId) + "/" + encodeURIComponent(surface) + "/options");
    },
    loadNodeControlPanel(nodeId: string, surface: ControlSurface) {
      return requestJSON<ControlPanelSummary>("/api/control-panels/node/" + encodeURIComponent(nodeId) + "/" + encodeURIComponent(surface));
    },
    loadTunnelControlPanel(tunnelId: string, surface: ControlSurface) {
      return requestJSON<ControlPanelSummary>("/api/control-panels/tunnel/" + encodeURIComponent(tunnelId) + "/" + encodeURIComponent(surface));
    },
    async loadDesktopData() {
      const [nodes, tunnels] = await Promise.all([this.loadNodes(), this.loadTunnels()]);
      return { nodes: nodes.items, tunnels: tunnels.items };
    },
    // ─── Certificates ───
    listCertificates() {
      return requestJSON<{ items: CertificateSpec[] }>("/api/certificates");
    },
    listManagedHTTPSDomains() {
      return requestJSON<ManagedHTTPSDomainListResponse>("/api/managed-domains/https");
    },
    createCertificate(spec: { domain: string; certPem: string; keyPem: string }) {
      return requestJSON<CertificateSpec>("/api/certificates", { method: "POST", body: JSON.stringify(spec) });
    },
  };
}
