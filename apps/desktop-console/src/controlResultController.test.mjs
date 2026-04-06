import assert from "node:assert/strict";
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

function ResultHost({ result }) {
  return React.createElement(
    "section",
    null,
    result ? React.createElement(ControlResultBlock, { result }) : React.createElement("p", null, "暂无控制结果"),
  );
}

function ResultController({ sequence }) {
  const [result, setResult] = useState(null);
  return React.createElement(
    "section",
    null,
    React.createElement(
      "button",
      {
        type: "button",
        onClick: async () => {
          for (const item of sequence) {
            await Promise.resolve();
            setResult(item);
          }
        },
      },
      "run",
    ),
    React.createElement(ResultHost, { result }),
  );
}

const renderer = TestRenderer.create(React.createElement(ResultController, { sequence: [duplicateInflight, retryableFailure] }));
let tree = renderer.toJSON();
assert.ok(JSON.stringify(tree).includes("暂无控制结果"));

const button = renderer.root.findByType("button");
await act(async () => {
  await button.props.onClick();
});

tree = renderer.toJSON();
const serialized = JSON.stringify(tree);
assert.ok(serialized.includes("可重试失败"));
assert.ok(serialized.includes("可稍后重试"));
assert.ok(!serialized.includes("duplicate_inflight"));
assert.ok(!serialized.includes("暂无控制结果"));

console.log("desktop-console control result controller assertions passed");
