import assert from "node:assert/strict";
import http from "node:http";
import React, { useState } from "react";
import TestRenderer, { act } from "react-test-renderer";
import { ControlResultBlock } from "./controlResultBlock.ts";

const duplicateInflight = {
  result: "rejected",
  actionKind: "restart_agent",
  targetKind: "node",
  targetId: "node-1",
  sourceSurface: "node_console",
  executeOutcome: "policy_rejected",
  rejectionKind: "duplicate_inflight",
  nextStep: "等待当前执行结果返回，不要在同一上下文下重复点击。",
  preflight: { allowed: true, items: [] },
  humanMessage: "相同控制上下文的动作仍在处理中，请勿重复提交。",
  dryRunOnly: false,
  executionMode: "real",
  placeholderOnly: false,
  executionNotes: [{ code: "", message: "该上下文动作仍在处理中，请等待当前结果或刷新后再重试。" }],
};

const retryableFailure = {
  result: "rejected",
  actionKind: "restart_agent",
  targetKind: "node",
  targetId: "node-2",
  sourceSurface: "node_console",
  executeOutcome: "retryable_failure",
  rejectionKind: "",
  nextStep: "可稍后重试；若连续失败，请结合审计与执行说明排查。",
  preflight: { allowed: true, items: [] },
  humanMessage: "执行器处理当前动作失败，但可稍后重试。",
  dryRunOnly: false,
  executionMode: "real",
  placeholderOnly: false,
  executionNotes: [{ code: "", message: "执行器处理失败，建议稍后重试。" }],
};

function RequestFlowHost({ url }) {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [result, setResult] = useState(null);

  async function runAction() {
    setBusy(true);
    setMessage("");
    const response = await fetch(url, { method: "POST" });
    const payload = await response.json();
    setResult(payload);
    setMessage(payload.humanMessage || "");
    setBusy(false);
  }

  return React.createElement(
    "section",
    null,
    React.createElement("button", { type: "button", onClick: () => runAction() }, busy ? "处理中..." : "执行"),
    message ? React.createElement("p", null, message) : null,
    result ? React.createElement(ControlResultBlock, { result }) : React.createElement("p", null, "暂无控制结果"),
  );
}

let firstResponse;
let resolveFirstResponse;
const firstResponsePromise = new Promise((resolve) => {
  resolveFirstResponse = resolve;
});
let resolveFirstSeen;
const firstSeenPromise = new Promise((resolve) => {
  resolveFirstSeen = resolve;
});
let requestCount = 0;
const server = http.createServer(async (_req, res) => {
  requestCount += 1;
  res.setHeader("Content-Type", "application/json");
  if (requestCount === 1) {
    firstResponse = res;
    resolveFirstSeen();
    await firstResponsePromise;
    return;
  }
  res.end(JSON.stringify(retryableFailure));
});

await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
const { port } = server.address();

try {
  const renderer = TestRenderer.create(React.createElement(RequestFlowHost, { url: `http://127.0.0.1:${port}/control` }));
  let tree = renderer.toJSON();
  assert.ok(JSON.stringify(tree).includes("暂无控制结果"));

  const button = renderer.root.findByType("button");
  const firstRun = button.props.onClick();
  await act(async () => {
    await Promise.resolve();
  });
  await firstSeenPromise;
  tree = renderer.toJSON();
  let serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("处理中..."));
  assert.ok(serialized.includes("暂无控制结果"));

  firstResponse.end(JSON.stringify(duplicateInflight));
  resolveFirstResponse();
  await act(async () => {
    await firstRun;
  });
  tree = renderer.toJSON();
  serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("处理中"));
  assert.ok(serialized.includes("duplicate_inflight"));
  assert.ok(serialized.includes("等待当前执行结果返回"));

  await act(async () => {
    await button.props.onClick();
  });
  tree = renderer.toJSON();
  serialized = JSON.stringify(tree);
  assert.ok(serialized.includes("可重试失败"));
  assert.ok(serialized.includes("可稍后重试"));
  assert.ok(!serialized.includes("duplicate_inflight"));
  assert.ok(!serialized.includes("暂无控制结果"));
} finally {
  server.close();
}

console.log("desktop-console control result request flow assertions passed");
