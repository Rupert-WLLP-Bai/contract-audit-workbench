import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../../api/http";
import { ExternalEvidencePanel } from "./external-evidence-panel";
import { DEMO_OPERATOR_AUDIT_FLAGS } from "./feature-flags";
import {
  availableObservation,
  collectionJobs,
  rejectedObservation,
  staleObservation,
  unavailableObservation,
} from "./fixture";
import {
  deferred,
  operatorAuditClient,
  renderOperatorAudit,
} from "./test-utils";
import type { ExternalObservation } from "./types";

describe("external evidence panel", () => {
  it("is off by default and does not query snapshots", () => {
    const listExternalSnapshots = vi.fn(
      async (): Promise<ExternalObservation[]> => [],
    );
    renderOperatorAudit(
      <ExternalEvidencePanel
        caseId="case-1"
        client={operatorAuditClient({ listExternalSnapshots })}
      />,
    );
    expect(screen.queryByText("外部证据快照")).not.toBeInTheDocument();
    expect(listExternalSnapshots).not.toHaveBeenCalled();
  });

  it("renders observation times, the effective window and snapshot fields, and labels fixtures", async () => {
    renderOperatorAudit(
      <ExternalEvidencePanel
        caseId="case-1"
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient({
          listExternalSnapshots: async () => [availableObservation],
        })}
        jobs={collectionJobs}
      />,
    );
    expect(await screen.findByText("fixture-oss-east")).toBeInTheDocument();
    expect(screen.getByText("观测时间（observed_at）")).toBeInTheDocument();
    expect(
      screen.getByText("来源更新时间（source_updated_at）"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("有效区间（effective window）"),
    ).toBeInTheDocument();
    expect(screen.getByText("合成数据（fixture）")).toBeInTheDocument();
    expect(
      screen.getByText(
        "合成 fixture 数据：仅用于组件与流程演示，不是生产数据。",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/实时/)).not.toBeInTheDocument();
    expect(screen.getByText("configured_bandwidth")).toBeInTheDocument();
    expect(screen.getByText("80")).toBeInTheDocument();
    expect(screen.getByText("Mbit/s")).toBeInTheDocument();
    expect(
      screen.getByText("/serviceCharacteristic/0/value"),
    ).toBeInTheDocument();
    expect(screen.getByText("采集中")).toBeInTheDocument();
    expect(screen.getByText("已拒绝")).toBeInTheDocument();
    expect(
      screen.getByText(/有采集任务被拒绝：未取得外部数据/),
    ).toBeInTheDocument();
  });

  it("keeps stale, unavailable and rejected snapshots explicitly inconclusive", async () => {
    renderOperatorAudit(
      <ExternalEvidencePanel
        caseId="case-1"
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient({
          listExternalSnapshots: async () => [
            staleObservation,
            unavailableObservation,
            rejectedObservation,
          ],
        })}
      />,
    );
    expect(await screen.findByText("已过期")).toBeInTheDocument();
    expect(
      screen.getByText(/该快照已过期：不能作为当前证据/),
    ).toBeInTheDocument();
    expect(screen.getByText("不可用")).toBeInTheDocument();
    expect(
      screen.getByText(/该快照不可用：不能作为证据/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/该快照已被拒绝：不能进入核验/),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("当前快照与合同一致"),
    ).not.toBeInTheDocument();
  });

  it("flags an observation whose effective window does not cover the business instant", async () => {
    renderOperatorAudit(
      <ExternalEvidencePanel
        caseId="case-1"
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        businessAt="2026-12-01T00:00:00Z"
        client={operatorAuditClient({
          listExternalSnapshots: async () => [availableObservation],
        })}
      />,
    );
    expect(
      await screen.findByText(/业务时点不在该快照的有效区间内/),
    ).toBeInTheDocument();
  });

  it("treats a forbidden response as denied without rendering snapshot data", async () => {
    renderOperatorAudit(
      <ExternalEvidencePanel
        caseId="case-1"
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient({
          listExternalSnapshots: async () => {
            throw new ApiError("没有访问该内容的权限", 403, "forbidden", false, null);
          },
        })}
      />,
    );
    expect(
      await screen.findByText("没有查看外部证据的权限"),
    ).toBeInTheDocument();
    expect(screen.queryByText("合成数据（fixture）")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "重试读取外部快照" }),
    ).not.toBeInTheDocument();
  });

  it("shows loading, then an empty state, then a retryable failure", async () => {
    const loading = deferred<ExternalObservation[]>();
    const first = renderOperatorAudit(
      <ExternalEvidencePanel
        caseId="case-1"
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient({
          listExternalSnapshots: () => loading.promise,
        })}
      />,
    );
    expect(screen.getByText("正在读取外部证据快照")).toBeInTheDocument();
    await act(async () => {
      loading.resolve([]);
    });
    expect(
      await screen.findByText("当前案件没有外部快照"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("没有快照不代表合规；请检查采集任务状态与案件授权。"),
    ).toBeInTheDocument();
    first.unmount();

    const user = userEvent.setup();
    let attempt = 0;
    renderOperatorAudit(
      <ExternalEvidencePanel
        caseId="case-1"
        flags={DEMO_OPERATOR_AUDIT_FLAGS}
        client={operatorAuditClient({
          listExternalSnapshots: async () => {
            attempt += 1;
            if (attempt === 1) {
              throw new ApiError(
                "外部快照服务暂不可用",
                503,
                "unavailable",
                true,
                null,
              );
            }
            return [availableObservation];
          },
        })}
      />,
    );
    expect(
      await screen.findByText("外部快照读取失败"),
    ).toBeInTheDocument();
    expect(screen.getByText("外部快照服务暂不可用")).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "重试读取外部快照" }),
    );
    expect(await screen.findByText("fixture-oss-east")).toBeInTheDocument();
    expect(attempt).toBe(2);
  });
});
