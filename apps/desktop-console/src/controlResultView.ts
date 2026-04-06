import type { ControlActionResponse } from "../../../packages/desktop-core/src/types.ts";
import { formatControlResultDisplay } from "../../../packages/desktop-core/src/utils.ts";

export type DesktopControlResultView = {
  tone: string;
  categoryLabel: string;
  message: string;
  nextStep: string;
  rejectionKind: string;
  summaryItems: Array<{ label: string; value: string }>;
  blockedReasons: Array<{ code: string; message: string }>;
  executionNotes: Array<{ code: string; message: string }>;
};

export function buildDesktopControlResultView(result: ControlActionResponse): DesktopControlResultView {
  const display = formatControlResultDisplay(result);
  return {
    tone: display.tone,
    categoryLabel: display.categoryLabel,
    message: display.message,
    nextStep: display.nextStep,
    rejectionKind: result.rejectionKind || "",
    summaryItems: [
      { label: "category label", value: display.categoryLabel },
      { label: "result", value: result.result },
      { label: "executeOutcome", value: result.executeOutcome || "-" },
      { label: "rejectionKind", value: result.rejectionKind || "-" },
      { label: "executionMode", value: display.executionMode || "-" },
      { label: "placeholderOnly", value: String(display.placeholderOnly) },
    ],
    blockedReasons: display.blockedReasons,
    executionNotes: display.executionNotes,
  };
}
