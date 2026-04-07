import assert from "node:assert/strict";
import React from "react";
import TestRenderer from "react-test-renderer";
import {
  bootstrapAdmin,
  clickButton,
  ControlPageHost,
  getFreePort,
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
    SERVER_API_ADMIN_BOOTSTRAP_SECRET: "page-flow-secret",
    SERVER_API_ADMIN_WEB_DIR: "/tmp",
    CONTROL_FIXTURE_MODE: "duplicate_handled",
    CONTROL_FIXTURE_NODE_ID: "node-desktop-page-flow",
    GOFLAGS: "",
  },
});

let renderer;

try {
  await waitForServer(baseUrl, fixture.getLogs, "control fixture");

  const cookieHeader = await bootstrapAdmin(baseUrl, "page-flow-secret", "pageflow@example.com", "Page Flow", "desktop-pass");
  assert.ok(cookieHeader.includes("crp_access="));

  renderer = TestRenderer.create(
    React.createElement(ControlPageHost, {
      baseUrl,
      cookieHeader,
      targetKind: "node",
      targetId: "node-desktop-page-flow",
      sourceSurface: "operator_console",
      actionKind: "isolate_node",
    }),
  );

  let tree = renderer.toJSON();
  let serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("正在读取控制选项"));

  for (let attempt = 0; attempt < 20; attempt += 1) {
    await new Promise((resolve) => setTimeout(resolve, 50));
    tree = renderer.toJSON();
    serialized = JSON.stringify(tree);
    if (serialized.includes("contextVersion=")) {
      break;
    }
  }
  assert.ok(serialized.includes("contextVersion="));
  assert.ok(serialized.includes("暂无控制结果"));

  const button = renderer.root.findByType("button");
  await clickButton(button);
  tree = renderer.toJSON();
  serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("已真实隔离目标节点。"));
  assert.ok(serialized.includes("accepted_real"));

  await clickButton(button);
  tree = renderer.toJSON();
  serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("已处理完成"));
  assert.ok(serialized.includes("duplicate_handled"));
  assert.ok(serialized.includes("该上下文动作已经处理完成；刷新后再决定是否需要新的动作。"));
  assert.ok(!serialized.includes("accepted_real"));
} finally {
  renderer?.unmount();
  await stopProcessTree(fixture.child);
}

console.log("desktop-console control page interaction flow assertions passed");
