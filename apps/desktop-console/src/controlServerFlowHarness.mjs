import { spawn } from "node:child_process";
import net from "node:net";
import React, { useEffect, useState } from "react";
import { act } from "react-test-renderer";
import { ControlResultBlock } from "./controlResultBlock.ts";

function createExitPromise(child) {
  return new Promise((resolve) => child.once("exit", resolve));
}

export async function stopProcessTree(child) {
  if (!child?.pid) return;
  const exitPromise = createExitPromise(child);
  try {
    process.kill(-child.pid, "SIGTERM");
  } catch {
    child.stdout?.destroy();
    child.stderr?.destroy();
    return;
  }
  const exited = await Promise.race([
    exitPromise.then(() => true),
    new Promise((resolve) => setTimeout(() => resolve(false), 5000)),
  ]);
  if (!exited) {
    try {
      process.kill(-child.pid, "SIGKILL");
    } catch {
      // process tree already exited
    }
    await exitPromise;
  }
  child.stdout?.destroy();
  child.stderr?.destroy();
}

export function getFreePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      server.close((err) => {
        if (err) {
          reject(err);
          return;
        }
        resolve(address.port);
      });
    });
    server.on("error", reject);
  });
}

export async function waitForServer(baseUrl, getLogs = () => "", label = "server") {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    try {
      const res = await fetch(baseUrl + "/healthz");
      if (res.ok) return;
    } catch {
      // keep polling
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(label + " did not become ready\n" + getLogs());
}

export function cookieHeaderFrom(response) {
  const cookies = typeof response.headers.getSetCookie === "function" ? response.headers.getSetCookie() : [];
  return cookies.map((item) => item.split(";", 1)[0]).join("; ");
}

export async function requestJSON(url, init = {}, cookieHeader = "") {
  const response = await fetch(url, {
    ...init,
    headers: {
      Accept: "application/json",
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...(cookieHeader ? { Cookie: cookieHeader } : {}),
      ...(init?.headers || {}),
    },
  });
  const payload = await response.json();
  if (!response.ok) {
    throw new Error(typeof payload?.error === "string" ? payload.error : "请求失败");
  }
  return { response, payload };
}

export function ControlRequestHost({ baseUrl, cookieHeader, request, requests }) {
  const sequence = requests || [request];
  const [busyCount, setBusyCount] = useState(0);
  const [message, setMessage] = useState("");
  const [result, setResult] = useState(null);
  const [index, setIndex] = useState(0);

  async function trigger() {
    const currentRequest = sequence[Math.min(index, sequence.length - 1)];
    setBusyCount((current) => current + 1);
    setMessage("");
    const { payload } = await requestJSON(baseUrl + "/api/control-actions", {
      method: "POST",
      body: JSON.stringify(currentRequest),
    }, cookieHeader);
    setResult(payload);
    setMessage(payload.humanMessage || "");
    setBusyCount((current) => Math.max(0, current - 1));
    setIndex((current) => Math.min(current + 1, sequence.length - 1));
    return payload;
  }

  return React.createElement(
    "section",
    null,
    React.createElement("button", { type: "button", onClick: () => trigger() }, busyCount > 0 ? "处理中..." : "执行"),
    message ? React.createElement("p", null, message) : null,
    result ? React.createElement(ControlResultBlock, { result }) : React.createElement("p", null, "暂无控制结果"),
  );
}

export function ControlPageHost({
  baseUrl,
  cookieHeader,
  targetKind,
  targetId,
  sourceSurface,
  actionKind,
  requests,
}) {
  const [loading, setLoading] = useState(true);
  const [message, setMessage] = useState("");
  const [busyCount, setBusyCount] = useState(0);
  const [result, setResult] = useState(null);
  const [contextVersion, setContextVersion] = useState("");
  const [index, setIndex] = useState(0);

  useEffect(() => {
    let cancelled = false;
    async function loadOptions() {
      setLoading(true);
      setMessage("");
      const { payload } = await requestJSON(baseUrl + `/api/control-actions/${targetKind}/${encodeURIComponent(targetId)}/${encodeURIComponent(sourceSurface)}/options`, undefined, cookieHeader);
      if (cancelled) return;
      setContextVersion(payload.contextVersion || "");
      setLoading(false);
    }
    void loadOptions();
    return () => {
      cancelled = true;
    };
  }, [baseUrl, cookieHeader, sourceSurface, targetId, targetKind]);

  async function trigger() {
    const requestList = requests || [{ actionKind, targetKind, targetId, sourceSurface, dryRun: false }];
    const currentRequest = requestList[Math.min(index, requestList.length - 1)];
    setBusyCount((current) => current + 1);
    setMessage("");
    const { payload } = await requestJSON(baseUrl + "/api/control-actions", {
      method: "POST",
      body: JSON.stringify({
        ...currentRequest,
        requestedAt: currentRequest.requestedAt || contextVersion,
      }),
    }, cookieHeader);
    setResult(payload);
    setMessage(payload.humanMessage || "");
    setBusyCount((current) => Math.max(0, current - 1));
    setIndex((current) => Math.min(current + 1, requestList.length - 1));
    return payload;
  }

  if (loading) {
    return React.createElement("section", null, React.createElement("p", null, "正在读取控制选项"));
  }

  return React.createElement(
    "section",
    null,
    React.createElement("p", null, `contextVersion=${contextVersion}`),
    React.createElement("button", { type: "button", onClick: () => trigger() }, busyCount > 0 ? "处理中..." : "执行"),
    message ? React.createElement("p", null, message) : null,
    result ? React.createElement(ControlResultBlock, { result }) : React.createElement("p", null, "暂无控制结果"),
  );
}

export async function bootstrapAdmin(baseUrl, bootstrapSecret, email, displayName, password) {
  const bootstrap = await requestJSON(baseUrl + "/api/auth/bootstrap", {
    method: "POST",
    body: JSON.stringify({ email, displayName, password }),
    headers: { "X-Bootstrap-Secret": bootstrapSecret },
  });
  return cookieHeaderFrom(bootstrap.response);
}

export async function registerManagedNode(baseUrl, nodeID) {
  await requestJSON(baseUrl + "/agent/register", {
    method: "POST",
    body: JSON.stringify({
      nodeId: nodeID,
      nodeName: nodeID,
      agentVersion: "0.1.0",
      capabilities: { tcpRelay: true, udpRelay: true, httpRelay: true, httpsRelay: true, p2pAssist: false, socks5Connect: true },
      metadata: {
        deploymentMode: "managed",
        serviceUnit: `cloud-relay-client-agent@${nodeID}.service`,
        instanceProfile: nodeID,
        instanceManaged: "true",
        nodeRole: "cloud",
      },
    }),
  });
  await requestJSON(baseUrl + "/agent/heartbeat", {
    method: "POST",
    body: JSON.stringify({
      nodeId: nodeID,
      observedAt: "2026-04-06T14:00:00Z",
      activeTunnels: 0,
    }),
  });
}

export function startGoServer({ command, args, env, cwd = "/root/cloud-relay-platform" }) {
  let logs = "";
  const child = spawn(command, args, {
    cwd,
    env,
    stdio: ["ignore", "pipe", "pipe"],
    detached: true,
  });
  child.stdout.on("data", (chunk) => {
    logs += chunk.toString();
  });
  child.stderr.on("data", (chunk) => {
    logs += chunk.toString();
  });
  return {
    child,
    getLogs() {
      return logs;
    },
  };
}

export async function clickButton(button) {
  await act(async () => {
    await button.props.onClick();
  });
}

export async function flushReact() {
  await act(async () => {
    await Promise.resolve();
  });
}
