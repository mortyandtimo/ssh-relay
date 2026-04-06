import React from "react";
import type { ControlActionResponse } from "../../../packages/desktop-core/src/types.ts";
import { buildDesktopControlResultView } from "./controlResultView.ts";

function Metric({ label, value }: { label: string; value: string }) {
  return React.createElement(
    "div",
    { className: "metric-box" },
    React.createElement("span", null, label),
    React.createElement("strong", null, value),
  );
}

export function ControlResultBlock({ result }: { result: ControlActionResponse }) {
  const view = buildDesktopControlResultView(result);
  return React.createElement(
    "section",
    { className: "panel result-panel" },
    React.createElement(
      "div",
      { className: "panel-head small" },
      React.createElement("h2", null, "最近一次控制结果"),
      React.createElement("span", { className: "status-chip " + view.tone }, view.categoryLabel),
    ),
    React.createElement("p", { className: "copy" }, view.message),
    React.createElement(
      "div",
      { className: "detail-stack result-summary-grid" },
      ...view.summaryItems.map((item) => React.createElement(Metric, { key: item.label, label: item.label, value: item.value })),
    ),
    React.createElement("p", { className: "copy" }, "下一步：", view.nextStep),
    view.rejectionKind
      ? React.createElement(
          "div",
          { className: "banner info" },
          "拒绝细分：",
          React.createElement("code", null, view.rejectionKind),
        )
      : null,
    view.blockedReasons.length > 0
      ? React.createElement(
          "div",
          { className: "check-list compact-check-list" },
          ...view.blockedReasons.map((reason) =>
            React.createElement(
              "div",
              { key: reason.code, className: "check-item" },
              React.createElement(
                "div",
                { className: "check-head" },
                React.createElement("strong", null, reason.code),
                React.createElement("span", { className: "status-chip danger" }, "阻断"),
              ),
              React.createElement("p", { className: "copy" }, reason.message),
            ),
          ),
        )
      : null,
    view.executionNotes.length > 0
      ? React.createElement(
          "div",
          { className: "check-list compact-check-list" },
          ...view.executionNotes.map((note) =>
            React.createElement(
              "div",
              { key: note.code || note.message, className: "check-item" },
              React.createElement(
                "div",
                { className: "check-head" },
                React.createElement("strong", null, note.code || "note"),
                React.createElement("span", { className: "status-chip neutral" }, "执行说明"),
              ),
              React.createElement("p", { className: "copy" }, note.message),
            ),
          ),
        )
      : null,
  );
}
