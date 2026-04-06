import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import net from "node:net";
import React, { useState } from "react";
import TestRenderer, { act } from "react-test-renderer";
import { ControlResultBlock } from "./controlResultBlock.ts";

function getFreePort() {
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

async function waitForServer(baseUrl) {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    try {
      const res = await fetch(baseUrl + "/healthz");
      if (res.ok) return;
    } catch {
      // keep polling
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("server-api did not become ready");
}

function cookieHeaderFrom(response) {
  const cookies = typeof response.headers.getSetCookie === "function" ? response.headers.getSetCookie() : [];
  return cookies.map((item) => item.split(";", 1)[0]).join("; ");
}

async function requestJSON(url, init = {}, cookieHeader = "") {
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

function RealServerResultHost({ baseUrl, cookieHeader, requests }) {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [result, setResult] = useState(null);
  const [index, setIndex] = useState(0);

  async function trigger() {
    setBusy(true);
    setMessage("");
    const { payload } = await requestJSON(baseUrl + "/api/control-actions", {
      method: "POST",
      body: JSON.stringify(requests[index]),
    }, cookieHeader);
    setResult(payload);
    setMessage(payload.humanMessage || "");
    setBusy(false);
    setIndex((current) => Math.min(current + 1, requests.length - 1));
  }

  return React.createElement(
    "section",
    null,
    React.createElement("button", { type: "button", onClick: () => trigger() }, busy ? "处理中..." : "执行"),
    message ? React.createElement("p", null, message) : null,
    result ? React.createElement(ControlResultBlock, { result }) : React.createElement("p", null, "暂无控制结果"),
  );
}

const port = await getFreePort();
const baseUrl = `http://127.0.0.1:${port}`;
const child = spawn("go", ["run", "./apps/server-api/cmd/server-api"], {
  cwd: "/root/cloud-relay-platform",
  env: {
    ...process.env,
    SERVER_API_ADDR: `127.0.0.1:${port}`,
    SERVER_API_ADMIN_BOOTSTRAP_SECRET: "desktop-flow-secret",
    SERVER_API_ADMIN_WEB_DIR: "/tmp",
    GOFLAGS: "",
  },
  stdio: ["ignore", "pipe", "pipe"],
});

try {
  await waitForServer(baseUrl);

  await requestJSON(baseUrl + "/agent/register", {
    method: "POST",
    body: JSON.stringify({
      nodeId: "node-desktop-flow",
      nodeName: "desktop-flow",
      agentVersion: "0.1.0",
      capabilities: { tcpRelay: true, udpRelay: true, httpRelay: true, httpsRelay: true, p2pAssist: false, socks5Connect: true },
      metadata: {
        deploymentMode: "managed",
        serviceUnit: "cloud-relay-client-agent@node-desktop-flow.service",
        instanceProfile: "node-desktop-flow",
        instanceManaged: "true",
        nodeRole: "cloud",
      },
    }),
  });

  await requestJSON(baseUrl + "/agent/heartbeat", {
    method: "POST",
    body: JSON.stringify({
      nodeId: "node-desktop-flow",
      observedAt: "2026-04-06T14:00:00Z",
      activeTunnels: 0,
    }),
  });

  const bootstrap = await requestJSON(baseUrl + "/api/auth/bootstrap", {
    method: "POST",
    body: JSON.stringify({ email: "desktopflow@example.com", displayName: "Desktop Flow", password: "desktop-pass" }),
    headers: { "X-Bootstrap-Secret": "desktop-flow-secret" },
  });
  const cookieHeader = cookieHeaderFrom(bootstrap.response);
  assert.ok(cookieHeader.includes("crp_access="));

  const { payload: options } = await requestJSON(baseUrl + "/api/control-actions/node/node-desktop-flow/operator_console/options", undefined, cookieHeader);
  const oldContextVersion = options.contextVersion;
  assert.ok(oldContextVersion);

  const renderer = TestRenderer.create(
    React.createElement(RealServerResultHost, {
      baseUrl,
      cookieHeader,
      requests: [
        {
          actionKind: "isolate_node",
          targetKind: "node",
          targetId: "node-desktop-flow",
          sourceSurface: "operator_console",
          dryRun: false,
          requestedAt: oldContextVersion,
        },
        {
          actionKind: "restart_agent",
          targetKind: "node",
          targetId: "node-desktop-flow",
          sourceSurface: "operator_console",
          dryRun: false,
          requestedAt: oldContextVersion,
        },
      ],
    }),
  );

  let tree = renderer.toJSON();
  assert.ok(JSON.stringify(tree).includes("暂无控制结果"));

  const button = renderer.root.findByType("button");
  await act(async () => {
    await button.props.onClick();
  });
  tree = renderer.toJSON();
  let serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("已真实隔离目标节点。"));
  assert.ok(serialized.includes("真实执行已受理"));
  assert.ok(serialized.includes("accepted_real"));

  await act(async () => {
    await button.props.onClick();
  });
  tree = renderer.toJSON();
  serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("上下文已过期"));
  assert.ok(serialized.includes("state_drift"));
  assert.ok(serialized.includes("请先刷新控制面板或动作列表，再基于新的上下文重新发起动作。"));
  assert.ok(serialized.includes("node_isolated") || serialized.includes("当前节点已隔离"));
  assert.ok(!serialized.includes("accepted_real"));
} finally {
  child.kill("SIGTERM");
  await new Promise((resolve) => child.once("exit", resolve));
}

console.log("desktop-console control server-api flow assertions passed");
