import { screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DEMO_OPERATOR_AUDIT_FLAGS } from "./feature-flags";
import {
  conflictCheck,
  fulfillmentComparisons,
} from "./fixture";
import { FulfillmentDiffPanel } from "./fulfillment-diff-panel";
import { renderOperatorAudit } from "./test-utils";
import type { FulfillmentComparison } from "./types";

describe("fulfillment diff panel", () => {
  it("is off by default", () => {
    renderOperatorAudit(
      <FulfillmentDiffPanel items={fulfillmentComparisons} />,
    );
    expect(
      screen.queryByText("合同与配置履约对照"),
    ).not.toBeInTheDocument();
  });

  it("shows contract value, configured value and units side by side", () => {
    renderOperatorAudit(
      <FulfillmentDiffPanel
        items={fulfillmentComparisons}
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
      />,
    );
    expect(screen.getAllByText("合同值")).toHaveLength(2);
    expect(screen.getAllByText("配置值")).toHaveLength(2);
    expect(screen.getByText("100 Mbit/s")).toBeInTheDocument();
    expect(screen.getAllByText("80 Mbit/s")).toHaveLength(2);
    expect(screen.getByText("99.9 %")).toBeInTheDocument();
    expect(screen.getByText("99.95 %")).toBeInTheDocument();
    expect(screen.getAllByText("观测时间")).toHaveLength(2);
    expect(
      screen.getByText(/配置差异不等于实际吞吐违约/),
    ).toBeInTheDocument();
    expect(screen.getByText("冲突")).toBeInTheDocument();
    expect(screen.getByText("一致")).toBeInTheDocument();
  });

  it("keeps document and external-snapshot evidence in separate columns", () => {
    renderOperatorAudit(
      <FulfillmentDiffPanel
        items={[fulfillmentComparisons[0]!]}
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
      />,
    );
    const documents = screen.getByRole("region", { name: "合同原文证据" });
    expect(
      within(documents).getByText("合成示例：本线路最低保障带宽100Mbit/s。"),
    ).toBeInTheDocument();
    expect(within(documents).queryByText("80 Mbit/s")).not.toBeInTheDocument();

    const snapshots = screen.getByRole("region", { name: "外部快照证据" });
    expect(within(snapshots).getByText("80 Mbit/s")).toBeInTheDocument();
    expect(
      within(snapshots).queryByText(
        "合成示例：本线路最低保障带宽100Mbit/s。",
      ),
    ).not.toBeInTheDocument();
    expect(
      within(snapshots).getByText("/serviceCharacteristic/0/value"),
    ).toBeInTheDocument();
  });

  it("does not classify unknown evidence as contract or snapshot evidence", () => {
    const item: FulfillmentComparison = {
      check: {
        ...conflictCheck,
        evidence: [{ kind: "unknown", rawKind: "scanned_image" }],
      },
      contractValue: null,
      configuredValue: null,
    };
    renderOperatorAudit(
      <FulfillmentDiffPanel
        items={[item]}
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
      />,
    );
    const other = screen.getByRole("region", { name: "其他证据" });
    expect(within(other).getByText(/scanned_image/)).toBeInTheDocument();
    expect(
      within(screen.getByRole("region", { name: "合同原文证据" })).queryByText(
        /scanned_image/,
      ),
    ).not.toBeInTheDocument();
    expect(
      within(screen.getByRole("region", { name: "外部快照证据" })).queryByText(
        /scanned_image/,
      ),
    ).not.toBeInTheDocument();
  });

  it("keeps insufficient evidence inconclusive instead of calling it compatible", () => {
    const item: FulfillmentComparison = {
      check: {
        ...conflictCheck,
        relation: "insufficient",
        requiresHumanReview: true,
      },
      contractValue: null,
      configuredValue: null,
    };
    renderOperatorAudit(
      <FulfillmentDiffPanel
        items={[item]}
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
      />,
    );
    expect(screen.getByText("信息不足")).toBeInTheDocument();
    expect(
      screen.getByText(/不产生确定性结论/),
    ).toBeInTheDocument();
    expect(screen.getAllByText("数值未报告")).toHaveLength(2);
    expect(screen.queryByText("一致")).not.toBeInTheDocument();
  });

  it("links to the existing human-review flow when the host wires one", () => {
    const humanReviewHref = vi.fn(
      (checkId: string) => `/cases/case-1/reviews/run-1?check=${checkId}`,
    );
    const first = renderOperatorAudit(
      <FulfillmentDiffPanel
        items={[fulfillmentComparisons[0]!]}
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        humanReviewHref={humanReviewHref}
      />,
    );
    expect(screen.getByRole("link", { name: "进入人工复核" })).toHaveAttribute(
      "href",
      "/cases/case-1/reviews/run-1?check=line.configured_bandwidth",
    );
    first.unmount();

    renderOperatorAudit(
      <FulfillmentDiffPanel
        items={[fulfillmentComparisons[0]!]}
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
      />,
    );
    expect(
      screen.getByText(/人工复核入口尚未在当前页面接线/),
    ).toBeInTheDocument();
  });

  it("states that an empty result is not a pass", () => {
    renderOperatorAudit(
      <FulfillmentDiffPanel
        items={[]}
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
      />,
    );
    expect(
      screen.getByText("当前评估没有可展示的检查结果"),
    ).toBeInTheDocument();
    expect(screen.getByText("空结果不代表审查通过。")).toBeInTheDocument();
  });
});
