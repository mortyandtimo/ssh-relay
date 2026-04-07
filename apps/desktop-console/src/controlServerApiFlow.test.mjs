import assert from "node:assert/strict";
import React from "react";
import TestRenderer from "react-test-renderer";
import {
  bootstrapAdmin,
  clickButton,
  ControlRequestHost,
  getFreePort,
  registerManagedNode,
  requestJSON,
  startGoServer,
  stopProcessTree,
  waitForServer,
} from "./controlServerFlowHarness.mjs";

const port = await getFreePort();
const baseUrl = `http://127.0.0.1:${port}`;
const server = startGoServer({
  command: "go",
  args: ["run", "./apps/server-api/cmd/server-api"],
  env: {
    ...process.env,
    SERVER_API_ADDR: `127.0.0.1:${port}`,
    SERVER_API_ADMIN_BOOTSTRAP_SECRET: "desktop-flow-secret",
    SERVER_API_ADMIN_WEB_DIR: "/tmp",
    GOFLAGS: "",
  },
});

let renderer;

try {
  await waitForServer(baseUrl, server.getLogs, "server-api");
  await registerManagedNode(baseUrl, "node-desktop-flow");

  const cookieHeader = await bootstrapAdmin(baseUrl, "desktop-flow-secret", "desktopflow@example.com", "Desktop Flow", "desktop-pass");
  assert.ok(cookieHeader.includes("crp_access="));

  const { payload: options } = await requestJSON(baseUrl + "/api/control-actions/node/node-desktop-flow/operator_console/options", undefined, cookieHeader);
  const oldContextVersion = options.contextVersion;
  assert.ok(oldContextVersion);

  renderer = TestRenderer.create(
    React.createElement(ControlRequestHost, {
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
  await clickButton(button);
  tree = renderer.toJSON();
  let serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("已真实隔离目标节点。"));
  assert.ok(serialized.includes("真实执行已受理"));
  assert.ok(serialized.includes("accepted_real"));

  await clickButton(button);
  tree = renderer.toJSON();
  serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("上下文已过期"));
  assert.ok(serialized.includes("state_drift"));
  assert.ok(serialized.includes("请先刷新控制面板或动作列表，再基于新的上下文重新发起动作。"));
  assert.ok(serialized.includes("node_isolated") || serialized.includes("当前节点已隔离"));
  assert.ok(!serialized.includes("accepted_real"));
} finally {
  renderer?.unmount();
  await stopProcessTree(server.child);
}

console.log("desktop-console control server-api flow assertions passed");
