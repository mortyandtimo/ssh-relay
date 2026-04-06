import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import net from "node:net";
import React, { useState } from "react";
import TestRenderer, { act } from "react-test-renderer";
import { ControlResultBlock } from "./controlResultBlock.ts";

function createExitPromise(child) {
  return new Promise((resolve) => child.once("exit", resolve));
}

async function stopProcessTree(child) {
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

async function waitForServer(baseUrl, getLogs) {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    try {
      const res = await fetch(baseUrl + "/healthz");
      if (res.ok) return;
    } catch {
      // keep polling
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("control fixture did not become ready\n" + getLogs());
}

async function waitForFixtureStarted(baseUrl, getLogs) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    try {
      const { payload } = await requestJSON(baseUrl + "/fixture/started");
      if (payload.started) return;
    } catch {
      // keep polling
    }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error("fixture never observed the first execute entering in-flight state\n" + getLogs());
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

function RealDuplicateInflightHost({ baseUrl, cookieHeader, request }) {
  const [busyCount, setBusyCount] = useState(0);
  const [message, setMessage] = useState("");
  const [result, setResult] = useState(null);

  async function trigger() {
    setBusyCount((current) => current + 1);
    setMessage("");
    const { payload } = await requestJSON(baseUrl + "/api/control-actions", {
      method: "POST",
      body: JSON.stringify(request),
    }, cookieHeader);
    setResult(payload);
    setMessage(payload.humanMessage || "");
    setBusyCount((current) => Math.max(0, current - 1));
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

const port = await getFreePort();
const baseUrl = `http://127.0.0.1:${port}`;
let childLogs = "";
const child = spawn("go", ["run", "./apps/server-api/cmd/control-fixture"], {
  cwd: "/root/cloud-relay-platform",
  env: {
    ...process.env,
    SERVER_API_ADDR: `127.0.0.1:${port}`,
    SERVER_API_ADMIN_BOOTSTRAP_SECRET: "duplicate-flow-secret",
    SERVER_API_ADMIN_WEB_DIR: "/tmp",
    CONTROL_FIXTURE_NODE_ID: "node-desktop-duplicate-live",
    CONTROL_FIXTURE_RELEASE_DELAY_MS: "600",
    GOFLAGS: "",
  },
  stdio: ["ignore", "pipe", "pipe"],
  detached: true,
});
child.stdout.on("data", (chunk) => {
  childLogs += chunk.toString();
});
child.stderr.on("data", (chunk) => {
  childLogs += chunk.toString();
});

try {
  await waitForServer(baseUrl, () => childLogs);

  const bootstrap = await requestJSON(baseUrl + "/api/auth/bootstrap", {
    method: "POST",
    body: JSON.stringify({ email: "duplicatelive@example.com", displayName: "Duplicate Live", password: "desktop-pass" }),
    headers: { "X-Bootstrap-Secret": "duplicate-flow-secret" },
  });
  const cookieHeader = cookieHeaderFrom(bootstrap.response);
  assert.ok(cookieHeader.includes("crp_access="));

  const { payload: options } = await requestJSON(baseUrl + "/api/control-actions/node/node-desktop-duplicate-live/operator_console/options", undefined, cookieHeader);
  const contextVersion = options.contextVersion;
  assert.ok(contextVersion);

  const renderer = TestRenderer.create(
    React.createElement(RealDuplicateInflightHost, {
      baseUrl,
      cookieHeader,
      request: {
        actionKind: "isolate_node",
        targetKind: "node",
        targetId: "node-desktop-duplicate-live",
        sourceSurface: "operator_console",
        dryRun: false,
        requestedAt: contextVersion,
      },
    }),
  );

  let tree = renderer.toJSON();
  assert.ok(JSON.stringify(tree).includes("暂无控制结果"));

  const button = renderer.root.findByType("button");
  const firstRun = button.props.onClick();
  await act(async () => {
    await Promise.resolve();
  });
  await waitForFixtureStarted(baseUrl, () => childLogs);

  tree = renderer.toJSON();
  let serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("处理中..."));
  assert.ok(serialized.includes("暂无控制结果"));

  await act(async () => {
    await button.props.onClick();
  });
  tree = renderer.toJSON();
  serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("处理中"));
  assert.ok(serialized.includes("policy_rejected"));
  assert.ok(serialized.includes("duplicate_inflight"));
  assert.ok(serialized.includes("等待当前执行结果返回，不要在同一上下文下重复点击。"));
  assert.ok(serialized.includes("该上下文动作仍在处理中，请等待当前结果或刷新后再重试。"));

  await act(async () => {
    await firstRun;
  });
  tree = renderer.toJSON();
  serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("已真实隔离目标节点。"));
  assert.ok(serialized.includes("真实执行已受理"));
  assert.ok(serialized.includes("accepted_real"));
  assert.ok(serialized.includes("节点已更新为 isolated=true。"));
  assert.ok(!serialized.includes("duplicate_inflight"));
  renderer.unmount();
} finally {
  await stopProcessTree(child);
}

console.log("desktop-console control duplicate inflight server flow assertions passed");
