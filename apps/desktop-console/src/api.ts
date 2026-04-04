import type { AuthUserResponse, BootstrapStatusResponse, NodeListResponse, TunnelListResponse, UserSummary } from "./types";

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || "";

let refreshInFlight: Promise<boolean> | null = null;

export async function requestJSON<T>(path: string, init?: RequestInit, allowRefresh = true): Promise<T> {
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

export async function loadBootstrapStatus() {
  return requestJSON<BootstrapStatusResponse>("/api/auth/bootstrap-status", undefined, false);
}

export async function loadCurrentUser() {
  return requestJSON<AuthUserResponse>("/api/auth/me");
}

export async function login(email: string, password: string) {
  return requestJSON<AuthUserResponse>("/api/auth/login", {
    method: "POST",
    body: JSON.stringify({ email, password }),
  }, false);
}

export async function logout() {
  return requestJSON<{ status: string }>("/api/auth/logout", { method: "POST" });
}

export async function loadNodes() {
  return requestJSON<NodeListResponse>("/api/nodes?limit=100&offset=0");
}

export async function loadTunnels() {
  return requestJSON<TunnelListResponse>("/api/tunnels");
}

export async function loadDesktopData() {
  const [nodes, tunnels] = await Promise.all([loadNodes(), loadTunnels()]);
  return { nodes: nodes.items, tunnels: tunnels.items };
}

export function localNodeHint(user: UserSummary | null) {
  if (!user) {
    return ["local", "third_party", "cloud"] as const;
  }
  return user.role === "admin" ? (["local", "third_party", "cloud"] as const) : (["local", "third_party", "cloud"] as const);
}
