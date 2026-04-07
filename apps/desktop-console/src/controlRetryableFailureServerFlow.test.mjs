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
    SERVER_API_ADMIN_BOOTSTRAP_SECRET: "retryable-flow-secret",
    SERVER_API_ADMIN_WEB_DIR: "/tmp",
    CONTROL_FIXTURE_MODE: "retryable_restart",
    CONTROL_FIXTURE_NODE_ID: "node-desktop-retryable-live",
    CONTROL_FIXTURE_RETRYABLE_DETAIL: "fixture transient restart failure",
    GOFLAGS: "",
  },
});

let renderer;

try {
  await waitForServer(baseUrl, fixture.getLogs, "control fixture");

  const cookieHeader = await bootstrapAdmin(baseUrl, "retryable-flow-secret", "retryablelive@example.com", "Retryable Live", "desktop-pass");
  assert.ok(cookieHeader.includes("crp_access="));

  const { payload: options } = await requestJSON(baseUrl + "/api/control-actions/node/node-desktop-retryable-live/node_console/options", undefined, cookieHeader);
  const contextVersion = options.contextVersion;
  assert.ok(contextVersion);

  renderer = TestRenderer.create(
    React.createElement(ControlRequestHost, {
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
  await clickButton(button);

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
  await stopProcessTree(fixture.child);
}

console.log("desktop-console control retryable failure server flow assertions passed");
