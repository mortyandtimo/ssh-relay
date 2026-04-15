import { invoke } from "@tauri-apps/api/core";
import type { Certificate, DNSCheckResult, HttpOutput, User } from "./types";

let baseUrl = "";
const cookieJar = new Map<string, string>();

type RequestOptions = {
  body?: unknown;
  headers?: Record<string, string>;
};

function normalizeBaseUrl(url: string) {
  return url.replace(/\/+$/, "");
}

function mergeResponseCookies(setCookies: string[] | undefined) {
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

function cookieHeaderFromJar() {
  return Array.from(cookieJar.values()).join("; ");
}

export function setBaseUrl(url: string) {
  const normalized = normalizeBaseUrl(url);
  if (normalized !== baseUrl) {
    cookieJar.clear();
  }
  baseUrl = normalized;
}

export function getBaseUrl(): string {
  return baseUrl;
}

export async function apiRequest(
  method: string,
  path: string,
  options: RequestOptions = {},
): Promise<HttpOutput> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options.headers || {}),
  };
  const cookieHeader = cookieHeaderFromJar();
  if (cookieHeader) {
    headers.Cookie = cookieHeader;
  }
  const result: HttpOutput = await invoke("http_request", {
    input: {
      url: baseUrl + path,
      method,
      headers,
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
    },
  });
  mergeResponseCookies(result.setCookies);
  if (path === "/api/auth/logout") {
    cookieJar.clear();
  }
  return result;
}

function parseErrorMessage(status: number, body: string) {
  let msg = `HTTP ${status}`;
  try {
    const payload = JSON.parse(body);
    msg = payload.error || msg;
  } catch {
    // ignore invalid JSON payloads
  }
  return msg;
}

export async function apiJson<T>(method: string, path: string, options: RequestOptions = {}): Promise<T> {
  let result = await apiRequest(method, path, options);
  if (result.status === 401 && path !== "/api/auth/login" && path !== "/api/auth/bootstrap" && path !== "/api/auth/refresh") {
    const refreshResult = await apiRequest("POST", "/api/auth/refresh", { body: {} });
    if (refreshResult.ok) {
      result = await apiRequest(method, path, options);
    }
  }
  if (!result.ok) {
    throw new Error(parseErrorMessage(result.status, result.body));
  }
  return JSON.parse(result.body) as T;
}

export async function bootstrapStatus(): Promise<{ required: boolean }> {
  return apiJson("GET", "/api/auth/bootstrap-status");
}

export async function bootstrap(email: string, displayName: string, password: string, secret: string): Promise<User> {
  return apiJson("POST", "/api/auth/bootstrap", {
    body: { email, displayName, password },
    headers: secret.trim() ? { "X-Bootstrap-Secret": secret.trim() } : undefined,
  });
}

export async function login(email: string, password: string): Promise<User> {
  return apiJson("POST", "/api/auth/login", { body: { email, password } });
}

export async function refresh(): Promise<User> {
  return apiJson("POST", "/api/auth/refresh", { body: {} });
}

export async function logout(): Promise<{ status: string }> {
  return apiJson("POST", "/api/auth/logout", { body: {} });
}

export async function me(): Promise<User> {
  return apiJson("GET", "/api/auth/me");
}

export async function listCertificates(): Promise<Certificate[]> {
  const result = await apiJson<{ items: Certificate[] }>("GET", "/api/certificates");
  return result.items;
}

export async function getCertificate(id: string): Promise<Certificate> {
  return apiJson("GET", "/api/certificates/" + id);
}

export async function createCertificate(domain: string, certPem: string, keyPem: string): Promise<Certificate> {
  return apiJson("POST", "/api/certificates", { body: { domain, certPem, keyPem } });
}

export async function updateCertificate(id: string, data: Partial<Certificate>): Promise<Certificate> {
  return apiJson("PUT", "/api/certificates/" + id, { body: data });
}

export async function deleteCertificate(id: string): Promise<{ status: string; id: string }> {
  return apiJson("DELETE", "/api/certificates/" + id);
}

export async function autoIssue(domain: string): Promise<Certificate> {
  return apiJson("POST", "/api/certificates/auto-issue", { body: { domain } });
}

export async function dnsCheck(domain: string): Promise<DNSCheckResult> {
  return apiJson("GET", `/api/certificates/dns-check?domain=${encodeURIComponent(domain)}`);
}
