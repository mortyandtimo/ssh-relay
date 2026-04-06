import assert from "node:assert/strict";
import { classifyControlResult, formatControlResultDisplay } from "./utils.ts";

const base = {
  result: "rejected",
  actionKind: "restart_agent",
  targetKind: "node",
  targetId: "node-1",
  sourceSurface: "node_console",
  preflight: { allowed: true, items: [] },
  dryRunOnly: false,
  executionMode: "real",
  placeholderOnly: false,
  executionNotes: [],
};

const staleA = classifyControlResult({
  ...base,
  humanMessage: "文案版本 A",
  executeOutcome: "policy_rejected",
  rejectionKind: "state_drift",
});

const staleB = classifyControlResult({
  ...base,
  humanMessage: "完全不同的展示文案 B",
  executeOutcome: "policy_rejected",
  rejectionKind: "state_drift",
});

const retryA = classifyControlResult({
  ...base,
  humanMessage: "文案版本 C",
  executeOutcome: "retryable_failure",
  rejectionKind: "",
});

const retryB = classifyControlResult({
  ...base,
  humanMessage: "另一套展示文案 D",
  executeOutcome: "retryable_failure",
  rejectionKind: "",
});

const duplicateInflightA = classifyControlResult({
  ...base,
  humanMessage: "处理中，请勿重复提交",
  executeOutcome: "policy_rejected",
  rejectionKind: "duplicate_inflight",
});

const duplicateHandledA = classifyControlResult({
  ...base,
  humanMessage: "该上下文已处理完成",
  executeOutcome: "policy_rejected",
  rejectionKind: "duplicate_handled",
});

const nonRetryableA = classifyControlResult({
  ...base,
  humanMessage: "不建议重试",
  executeOutcome: "non_retryable_failure",
  rejectionKind: "",
});

const staleDisplay = formatControlResultDisplay({
  ...base,
  humanMessage: "展示文案完全可变",
  nextStep: "请先刷新控制面板或动作列表，再基于新的上下文重新发起动作。",
  executeOutcome: "policy_rejected",
  rejectionKind: "state_drift",
  executionNotes: [{ code: "", message: "执行前发现控制上下文已经过期，目标状态已变化，请刷新控制摘要后重试。" }],
});

const duplicateInflightDisplay = formatControlResultDisplay({
  ...base,
  humanMessage: "另一套处理中提示",
  nextStep: "等待当前执行结果返回，不要在同一上下文下重复点击。",
  executeOutcome: "policy_rejected",
  rejectionKind: "duplicate_inflight",
  executionNotes: [{ code: "", message: "该上下文动作仍在处理中，请等待当前结果或刷新后再重试。" }],
});

const retryDisplay = formatControlResultDisplay({
  ...base,
  humanMessage: "展示文案可变化，但 machine fields 不变",
  nextStep: "可稍后重试；若连续失败，请结合审计与执行说明排查。",
  executeOutcome: "retryable_failure",
  rejectionKind: "",
  executionNotes: [{ code: "", message: "执行器处理失败，建议稍后重试。" }],
});

const nonRetryDisplay = formatControlResultDisplay({
  ...base,
  humanMessage: "当前不建议重试",
  nextStep: "当前不建议直接重试；请先修正环境或策略条件。",
  executeOutcome: "non_retryable_failure",
  rejectionKind: "",
  executionNotes: [{ code: "", message: "执行器处理失败，当前不建议重试。" }],
});

assert.equal(staleA, "stale_context");
assert.equal(staleB, "stale_context");
assert.equal(retryA, "retryable_failure");
assert.equal(retryB, "retryable_failure");
assert.equal(duplicateInflightA, "duplicate_inflight");
assert.equal(duplicateHandledA, "duplicate_handled");
assert.equal(nonRetryableA, "non_retryable_failure");

assert.equal(staleDisplay.category, "stale_context");
assert.equal(staleDisplay.categoryLabel, "上下文已过期");
assert.equal(staleDisplay.nextStep, "请先刷新控制面板或动作列表，再基于新的上下文重新发起动作。");
assert.equal(staleDisplay.executionNotes[0]?.message, "执行前发现控制上下文已经过期，目标状态已变化，请刷新控制摘要后重试。");

assert.equal(duplicateInflightDisplay.category, "duplicate_inflight");
assert.equal(duplicateInflightDisplay.categoryLabel, "处理中");
assert.equal(duplicateInflightDisplay.nextStep, "等待当前执行结果返回，不要在同一上下文下重复点击。");

assert.equal(retryDisplay.category, "retryable_failure");
assert.equal(retryDisplay.categoryLabel, "可重试失败");
assert.equal(retryDisplay.nextStep, "可稍后重试；若连续失败，请结合审计与执行说明排查。");

assert.equal(nonRetryDisplay.category, "non_retryable_failure");
assert.equal(nonRetryDisplay.categoryLabel, "不可重试失败");
assert.equal(nonRetryDisplay.nextStep, "当前不建议直接重试；请先修正环境或策略条件。");

console.log("desktop-core classifyControlResult machine-field assertions passed");
