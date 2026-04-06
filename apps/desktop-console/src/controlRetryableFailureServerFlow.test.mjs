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

function RealRetryableFailureHost({ baseUrl, cookieHeader, request }) {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [result, setResult] = useState(null);

  async function trigger() {
    setBusy(true);
    setMessage("");
    const { payload } = await requestJSON(baseUrl + "/api/control-actions", {
      method: "POST",
      body: JSON.stringify(request),
    }, cookieHeader);
    setResult(payload);
    setMessage(payload.humanMessage || "");
    setBusy(false);
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
let childLogs = "";
const child = spawn("go", ["run", "./apps/server-api/cmd/control-fixture"], {
  cwd: "/root/cloud-relay-platform",
  env: {
    ...process.env,
    SERVER_API_ADDR: `127.0.0.1:${port}`,
    SERVER_API_ADMIN_BOOTSTRAP_SECRET: "retryable-flow-secret",
    SERVER_API_ADMIN_WEB_DIR: "/tmp",
    CONTROL_FIXTURE_MODE: "retryable_restart",
    CONTROL_FIXTURE_NODE_ID: "node-desktop-retryable-live",
    CONTROL_FIXTURE_RETRYABLE_DETAIL: "fixture transient restart failure",
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

let renderer;

try {
  await waitForServer(baseUrl, () => childLogs);

  const bootstrap = await requestJSON(baseUrl + "/api/auth/bootstrap", {
    method: "POST",
    body: JSON.stringify({ email: "retryablelive@example.com", displayName: "Retryable Live", password: "desktop-pass" }),
    headers: { "X-Bootstrap-Secret": "retryable-flow-secret" },
  });
  const cookieHeader = cookieHeaderFrom(bootstrap.response);
  assert.ok(cookieHeader.includes("crp_access="));

  const { payload: options } = await requestJSON(baseUrl + "/api/control-actions/node/node-desktop-retryable-live/node_console/options", undefined, cookieHeader);
  const contextVersion = options.contextVersion;
  assert.ok(contextVersion);

  renderer = TestRenderer.create(
    React.createElement(RealRetryableFailureHost, {
      baseUrl,
      cookieHeader,
      request: {
        actionKind: "restart_agent",
        targetKind: "node",
        targetId: "node-desktop-retryable-live",
        sourceSurface: "node_console",
        dryRun: false,
        requestedAt: contextVersion,
      },
    }),
  );

  let tree = renderer.toJSON();
  assert.ok(JSON.stringify(tree).includes("暂无控制结果"));

  const button = renderer.root.findByType("button");
  await act(async () => {
    await button.props.onClick();
  });

  tree = renderer.toJSON();
  const serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("可重试失败"));
  assert.ok(serialized.includes("retryable_failure"));
  assert.ok(serialized.includes("real"));
  assert.ok(serialized.includes("可稍后重试；若连续失败，请结合审计与执行说明排查。"));
  assert.ok(serialized.includes("fixture transient restart failure"));
  assert.ok(!serialized.includes("duplicate_inflight"));
} finally {
  renderer?.unmount();
  await stopProcessTree(child);
}

console.log("desktop-console control retryable failure server flow assertions passed");
