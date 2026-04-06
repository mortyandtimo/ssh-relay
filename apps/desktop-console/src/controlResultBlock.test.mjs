import assert from "node:assert/strict";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
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

const staleWithReason = {
  result: "rejected",
  actionKind: "pause_tunnel",
  targetKind: "tunnel",
  targetId: "tunnel-1",
  sourceSurface: "operator_console",
  executeOutcome: "policy_rejected",
  rejectionKind: "state_drift",
  nextStep: "请先刷新控制面板或动作列表，再基于新的上下文重新发起动作。",
  preflight: {
    allowed: false,
    items: [],
    blockedReasons: [{ code: "tunnel_state_conflict", message: "当前 tunnel 已变化，请刷新后重试。" }],
  },
  humanMessage: "目标状态已变化，请刷新后重试。",
  dryRunOnly: false,
  executionMode: "real",
  placeholderOnly: false,
  executionNotes: [{ code: "", message: "执行前发现控制上下文已经过期，目标状态已变化，请刷新控制摘要后重试。" }],
};

const duplicateMarkup = renderToStaticMarkup(React.createElement(ControlResultBlock, { result: duplicateInflight }));
assert.match(duplicateMarkup, /处理中/);
assert.match(duplicateMarkup, /duplicate_inflight/);
assert.match(duplicateMarkup, /等待当前执行结果返回/);
assert.match(duplicateMarkup, /该上下文动作仍在处理中/);
assert.match(duplicateMarkup, /executeOutcome/);

const retryMarkup = renderToStaticMarkup(React.createElement(ControlResultBlock, { result: retryableFailure }));
assert.match(retryMarkup, /可重试失败/);
assert.match(retryMarkup, /可稍后重试/);
assert.match(retryMarkup, /执行器处理失败，建议稍后重试/);
assert.doesNotMatch(retryMarkup, /拒绝细分/);

const staleMarkup = renderToStaticMarkup(React.createElement(ControlResultBlock, { result: staleWithReason }));
assert.match(staleMarkup, /上下文已过期/);
assert.match(staleMarkup, /tunnel_state_conflict/);
assert.match(staleMarkup, /当前 tunnel 已变化，请刷新后重试/);

console.log("desktop-console control result block markup assertions passed");
