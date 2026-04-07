import assert from "node:assert/strict";
import React from "react";
import TestRenderer from "react-test-renderer";
import {
  bootstrapAdmin,
  ControlRequestHost,
  flushReact,
  getFreePort,
  requestJSON,
  startGoServer,
  stopProcessTree,
  waitForServer,
} from "./controlServerFlowHarness.mjs";

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

const port = await getFreePort();
const baseUrl = `http://127.0.0.1:${port}`;
const fixture = startGoServer({
  command: "go",
  args: ["run", "./apps/server-api/cmd/control-fixture"],
  env: {
    ...process.env,
    SERVER_API_ADDR: `127.0.0.1:${port}`,
    SERVER_API_ADMIN_BOOTSTRAP_SECRET: "duplicate-flow-secret",
    SERVER_API_ADMIN_WEB_DIR: "/tmp",
    CONTROL_FIXTURE_NODE_ID: "node-desktop-duplicate-live",
    CONTROL_FIXTURE_RELEASE_DELAY_MS: "600",
    GOFLAGS: "",
  },
});

let renderer;

try {
  await waitForServer(baseUrl, fixture.getLogs, "control fixture");

  const cookieHeader = await bootstrapAdmin(baseUrl, "duplicate-flow-secret", "duplicatelive@example.com", "Duplicate Live", "desktop-pass");
  assert.ok(cookieHeader.includes("crp_access="));

  const { payload: options } = await requestJSON(baseUrl + "/api/control-actions/node/node-desktop-duplicate-live/operator_console/options", undefined, cookieHeader);
  const contextVersion = options.contextVersion;
  assert.ok(contextVersion);

  renderer = TestRenderer.create(
    React.createElement(ControlRequestHost, {
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
  await flushReact();
  await waitForFixtureStarted(baseUrl, fixture.getLogs);

  tree = renderer.toJSON();
  let serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("处理中..."));
  assert.ok(serialized.includes("暂无控制结果"));

  await button.props.onClick();
  tree = renderer.toJSON();
  serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("处理中"));
  assert.ok(serialized.includes("policy_rejected"));
  assert.ok(serialized.includes("duplicate_inflight"));
  assert.ok(serialized.includes("等待当前执行结果返回，不要在同一上下文下重复点击。"));
  assert.ok(serialized.includes("该上下文动作仍在处理中，请等待当前结果或刷新后再重试。"));

  await firstRun;
  tree = renderer.toJSON();
  serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("已真实隔离目标节点。"));
  assert.ok(serialized.includes("真实执行已受理"));
  assert.ok(serialized.includes("accepted_real"));
  assert.ok(serialized.includes("节点已更新为 isolated=true。"));
  assert.ok(!serialized.includes("duplicate_inflight"));
} finally {
  renderer?.unmount();
  await stopProcessTree(fixture.child);
}

console.log("desktop-console control duplicate inflight server flow assertions passed");
