import assert from "node:assert/strict";
import React from "react";
import TestRenderer from "react-test-renderer";
import {
  bootstrapAdmin,
  clickButton,
  ControlRequestHost,
  getFreePort,
  requestJSON,
  startGoServer,
  stopProcessTree,
  waitForServer,
} from "./controlServerFlowHarness.mjs";

const port = await getFreePort();
const baseUrl = `http://127.0.0.1:${port}`;
const fixture = startGoServer({
  command: "go",
  args: ["run", "./apps/server-api/cmd/control-fixture"],
  env: {
    ...process.env,
    SERVER_API_ADDR: `127.0.0.1:${port}`,
    SERVER_API_ADMIN_BOOTSTRAP_SECRET: "handled-flow-secret",
    SERVER_API_ADMIN_WEB_DIR: "/tmp",
    CONTROL_FIXTURE_MODE: "duplicate_handled",
    CONTROL_FIXTURE_NODE_ID: "node-desktop-duplicate-handled-live",
    GOFLAGS: "",
  },
});

let renderer;

try {
  await waitForServer(baseUrl, fixture.getLogs, "control fixture");

  const cookieHeader = await bootstrapAdmin(baseUrl, "handled-flow-secret", "handledlive@example.com", "Handled Live", "desktop-pass");
  assert.ok(cookieHeader.includes("crp_access="));

  const { payload: options } = await requestJSON(baseUrl + "/api/control-actions/node/node-desktop-duplicate-handled-live/operator_console/options", undefined, cookieHeader);
  const contextVersion = options.contextVersion;
  assert.ok(contextVersion);

  renderer = TestRenderer.create(
    React.createElement(ControlRequestHost, {
      baseUrl,
      cookieHeader,
      request: {
        actionKind: "isolate_node",
        targetKind: "node",
        targetId: "node-desktop-duplicate-handled-live",
        sourceSurface: "operator_console",
        dryRun: false,
        requestedAt: contextVersion,
      },
    }),
  );

  let tree = renderer.toJSON();
  assert.ok(JSON.stringify(tree).includes("暂无控制结果"));

  const button = renderer.root.findByType("button");
  await clickButton(button);

  tree = renderer.toJSON();
  let serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("已真实隔离目标节点。"));
  assert.ok(serialized.includes("accepted_real"));
  assert.ok(serialized.includes("真实执行已受理"));

  await clickButton(button);

  tree = renderer.toJSON();
  serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("已处理完成"));
  assert.ok(serialized.includes("policy_rejected"));
  assert.ok(serialized.includes("duplicate_handled"));
  assert.ok(serialized.includes("该上下文动作已经处理完成；刷新后再决定是否需要新的动作。"));
  assert.ok(serialized.includes("该上下文动作已经处理完成，避免重复执行。请刷新控制摘要后再决定下一步。"));
  assert.ok(!serialized.includes("duplicate_inflight"));
  assert.ok(!serialized.includes("accepted_real"));
} finally {
  renderer?.unmount();
  await stopProcessTree(fixture.child);
}

console.log("desktop-console control duplicate handled server flow assertions passed");
