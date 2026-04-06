import assert from "node:assert/strict";
import { classifyControlResult } from "./utils.ts";

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

assert.equal(staleA, "stale_context");
assert.equal(staleB, "stale_context");
assert.equal(retryA, "retryable_failure");
assert.equal(retryB, "retryable_failure");

console.log("desktop-core classifyControlResult machine-field assertions passed");
