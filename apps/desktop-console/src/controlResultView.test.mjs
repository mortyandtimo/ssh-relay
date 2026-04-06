import assert from "node:assert/strict";
import { buildDesktopControlResultView } from "./controlResultView.ts";

const base = {
  result: "rejected",
  actionKind: "pause_tunnel",
  targetKind: "tunnel",
  targetId: "tunnel-1",
  sourceSurface: "operator_console",
  preflight: { allowed: false, items: [], blockedReasons: [] },
  humanMessage: "展示文案可以变化",
  dryRunOnly: false,
  executionMode: "real",
  placeholderOnly: false,
  executionNotes: [],
};

const staleView = buildDesktopControlResultView({
  ...base,
  executeOutcome: "policy_rejected",
  rejectionKind: "state_drift",
  nextStep: "请先刷新控制面板或动作列表，再基于新的上下文重新发起动作。",
  preflight: {
    allowed: false,
    items: [],
    blockedReasons: [{ code: "tunnel_state_conflict", message: "当前 tunnel 已变化，请刷新后重试。" }],
  },
  executionNotes: [{ code: "", message: "执行前发现控制上下文已经过期，目标状态已变化，请刷新控制摘要后重试。" }],
});

const inflightView = buildDesktopControlResultView({
  ...base,
  actionKind: "restart_agent",
  targetKind: "node",
  targetId: "node-1",
  sourceSurface: "node_console",
  executeOutcome: "policy_rejected",
  rejectionKind: "duplicate_inflight",
  nextStep: "等待当前执行结果返回，不要在同一上下文下重复点击。",
  executionNotes: [{ code: "", message: "该上下文动作仍在处理中，请等待当前结果或刷新后再重试。" }],
});

const handledView = buildDesktopControlResultView({
  ...base,
  actionKind: "isolate_node",
  targetKind: "node",
  targetId: "node-2",
  executeOutcome: "policy_rejected",
  rejectionKind: "duplicate_handled",
  nextStep: "该上下文动作已经处理完成；刷新后再决定是否需要新的动作。",
  preflight: {
    allowed: false,
    items: [],
    blockedReasons: [{ code: "node_isolated", message: "当前节点已经隔离，不能重复 isolate。" }],
  },
  executionNotes: [{ code: "", message: "该上下文动作已经处理完成，避免重复执行。请刷新控制摘要后再决定下一步。" }],
});

assert.equal(staleView.categoryLabel, "上下文已过期");
assert.equal(staleView.rejectionKind, "state_drift");
assert.equal(staleView.nextStep, "请先刷新控制面板或动作列表，再基于新的上下文重新发起动作。");
assert.equal(staleView.blockedReasons[0]?.code, "tunnel_state_conflict");
assert.equal(staleView.executionNotes[0]?.message, "执行前发现控制上下文已经过期，目标状态已变化，请刷新控制摘要后重试。");

assert.equal(inflightView.categoryLabel, "处理中");
assert.equal(inflightView.rejectionKind, "duplicate_inflight");
assert.equal(inflightView.nextStep, "等待当前执行结果返回，不要在同一上下文下重复点击。");
assert.equal(inflightView.executionNotes[0]?.message, "该上下文动作仍在处理中，请等待当前结果或刷新后再重试。");

assert.equal(handledView.categoryLabel, "已处理完成");
assert.equal(handledView.rejectionKind, "duplicate_handled");
assert.equal(handledView.blockedReasons[0]?.code, "node_isolated");
assert.equal(handledView.executionNotes[0]?.message, "该上下文动作已经处理完成，避免重复执行。请刷新控制摘要后再决定下一步。");

for (const view of [staleView, inflightView, handledView]) {
  const labels = view.summaryItems.map((item) => item.label);
  assert.ok(labels.includes("category label"));
  assert.ok(labels.includes("executeOutcome"));
  assert.ok(labels.includes("rejectionKind"));
  assert.ok(labels.includes("executionMode"));
}

console.log("desktop-console control result view assertions passed");
