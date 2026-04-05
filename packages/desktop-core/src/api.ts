import type { AuthUserResponse, BootstrapStatusResponse, ControlActionRequest, ControlActionResponse, NodeListResponse, TunnelListResponse } from "./types";

export function createDesktopApi(apiBaseUrl = "") {
  let refreshInFlight: Promise<boolean> | null = null;

  async function requestJSON<T>(path: string, init?: RequestInit, allowRefresh = true): Promise<T> {
    const response = await fetch(apiBaseUrl + path, {
      credentials: "include",
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
      throw new Error(payload && typeof payload.error === "string" ? payload.error : "请求失败");
    }
    return payload as T;
  }

  async function refreshAuthSession(): Promise<boolean> {
    if (refreshInFlight) {
      return refreshInFlight;
    }
    const task = (async () => {
      const response = await fetch(apiBaseUrl + "/api/auth/refresh", {
        method: "POST",
        credentials: "include",
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
    login(email: string, password: string) {
      return requestJSON<AuthUserResponse>("/api/auth/login", {
        method: "POST",
        body: JSON.stringify({ email, password }),
      }, false);
    },
    logout() {
      return requestJSON<{ status: string }>("/api/auth/logout", { method: "POST" });
    },
    loadNodes() {
      return requestJSON<NodeListResponse>("/api/nodes?limit=100&offset=0");
    },
    loadTunnels() {
      return requestJSON<TunnelListResponse>("/api/tunnels");
    },
    updateTunnel(id: string, payload: Record<string, unknown>) {
      return requestJSON("/api/tunnels/" + encodeURIComponent(id), {
        method: "PUT",
        body: JSON.stringify(payload),
      });
    },
    controlAction(payload: ControlActionRequest) {
      return requestJSON<ControlActionResponse>("/api/control-actions", {
        method: "POST",
        body: JSON.stringify(payload),
      });
    },
    async loadDesktopData() {
      const [nodes, tunnels] = await Promise.all([this.loadNodes(), this.loadTunnels()]);
      return { nodes: nodes.items, tunnels: tunnels.items };
    },
  };
}
