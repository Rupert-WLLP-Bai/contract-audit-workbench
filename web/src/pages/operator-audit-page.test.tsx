// File metadata (manifest mirror):
// Purpose: Checks the operator-audit page UI2 overview: heading/facts from real data, truthful unaccepted state, tabbed access to retained configuration, evidence and acceptance panels, and read-only check rows from persisted evaluation objects/checks.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-29
// Updated: 2026-11-08
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithAntd as render } from "../test/antd";
import { OperatorAuditPage } from "./operator-audit-page";

const mocks = vi.hoisted(() => ({
  listCapabilityPacks: vi.fn(),
  listExternalSnapshots: vi.fn(),
  createExternalCollectionJob: vi.fn(),
  createFulfillmentEvaluation: vi.fn(),
  getFulfillmentEvaluation: vi.fn(),
  getImpactPlan: vi.fn(),
  useSettings: vi.fn(),
  useCaseDetail: vi.fn(),
  getReview: vi.fn(),
  listServiceObjects: vi.fn(),
  getServiceObject: vi.fn(),
  listServiceObjectVersions: vi.fn(),
  listServiceObjectAssociations: vi.fn(),
}));

vi.mock("../features/operator-audit/api", () => ({
  defaultOperatorAuditClient: {
    listCapabilityPacks: mocks.listCapabilityPacks,
    listExternalSnapshots: mocks.listExternalSnapshots,
    createExternalCollectionJob: mocks.createExternalCollectionJob,
    createFulfillmentEvaluation: mocks.createFulfillmentEvaluation,
    getFulfillmentEvaluation: mocks.getFulfillmentEvaluation,
    getImpactPlan: mocks.getImpactPlan,
  },
}));
vi.mock("../api/queries", () => ({
  useSettings: mocks.useSettings,
  useCaseDetail: mocks.useCaseDetail,
}));
vi.mock("../api/endpoints", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/endpoints")>();
  return { ...actual, getReview: mocks.getReview };
});
vi.mock("../features/operator-audit/service-objects-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../features/operator-audit/service-objects-api")>();
  return {
    ...actual,
    listServiceObjects: mocks.listServiceObjects,
    getServiceObject: mocks.getServiceObject,
    listServiceObjectVersions: mocks.listServiceObjectVersions,
    listServiceObjectAssociations: mocks.listServiceObjectAssociations,
  };
});

const pack = {
  packId: "operator.dedicated_line",
  version: "1.0.0",
  displayName: "专线配置核验（合成示例）",
  scenario: "telecom_dedicated_line",
  status: null,
  hostContractVersion: "auditext/v1",
  requiredMaterialRoles: ["draft_contract"],
  rulePack: { id: "telecom-contractual", version: "5" },
  capabilities: [],
  checks: [],
  dataClass: "synthetic",
};
const caseDetail = {
  id: "case-1",
  title: "专线跨系统核验",
  documents: [
    { id: "doc-1", role: "draft_contract", filename: "draft.txt" },
  ],
  latest_run: { id: "run-1", state: "completed", updated_at: "2026-10-28T00:00:00Z" },
};
const workspace = {
  review_attempts: { attempt: { id: "attempt-1" } },
  documents: [],
};
const serviceObject = {
  service_object_id: "so-1",
  object_type: "dedicated_line",
  state: "confirmed",
  current_version: 1,
  current_association_version: 1,
  display_name: "园区专线 A",
  business_code: "LINE-001",
  aliases: [],
  current_association: {
    association_version: 1,
    object_version: 1,
    material_id: "material-1",
    material_version: 1,
    document_id: "doc-1",
    parsed_document_id: "parsed-1",
    source_run_id: "run-1",
    source_attempt_id: "attempt-1",
    object_ref: "line-1",
    facts_sha256: "a".repeat(64),
    confirmed_by: "reviewer-1",
    confirmed_at: "2026-10-31T00:00:00Z",
  },
  can_manage: false,
  needs_reassociation: false,
};
const serviceObjectDetail = {
  service_object_id: "so-1",
  current_version: 1,
  identity: {
    service_object_id: "so-1",
    version: 1,
    object_type: "dedicated_line",
    display_name: "园区专线 A",
    business_code: "LINE-001",
    aliases: [],
    state: "confirmed",
    basis_refs: [],
    basis_summary: "",
    created_by: "reviewer-1",
    created_at: "2026-10-31T00:00:00Z",
    effective_from: "2026-10-01T00:00:00Z",
    payload_sha256: "b".repeat(64),
  },
  association: null,
  mapping: null,
  can_manage: false,
  needs_reassociation: false,
};
const check = {
  checkId: "line.configured_bandwidth",
  relation: "insufficient",
  reasonCode: "configured_bandwidth_unreported",
  machineStatus: "completed",
  evidence: [],
  requiresHumanReview: true,
  limitations: ["合成示例限制"],
};
const evaluation = {
  evaluationId: "evaluation-1",
  sourceReviewId: "run-1",
  sourceAttemptId: "attempt-1",
  pack: { id: "operator.dedicated_line", version: "1.0.0" },
  contractVersion: "contract-1",
  rulePackVersion: "5",
  objectBindingVersion: "binding-1",
  snapshotSetId: "snapshot-set-1",
  businessAt: "2026-11-05T04:30:00Z",
  evidenceCutoffAt: "2026-11-05T03:00:00Z",
  scope: { kind: "whole_case", serviceObjectIds: [] },
  coverage: {
    scopeKind: "whole_case",
    candidateCount: 1,
    evaluatedCount: 1,
    unresolvedCount: 0,
    insufficientCount: 1,
    status: "partial",
    reasonCode: null,
  },
  objects: [
    {
      objectRef: "line-1",
      serviceObjectId: "so-1",
      objectVersion: 1,
      associationVersion: 1,
      associationRunId: "run-1",
      associationAttemptId: "attempt-1",
      mappingId: "mapping-1",
      mappingVersion: 1,
      bindingVersion: "binding-1",
      snapshotSetId: "snapshot-set-1",
      snapshotCollectedAt: "2026-11-05T03:30:00Z",
      status: "insufficient",
      reasonCode: "external_snapshot_missing",
      factsSha256: null,
      factRefs: [],
      facts: [],
      checks: [check],
      source: null,
    },
  ],
  checks: [],
  contractFacts: [],
  dataClass: "synthetic",
};

function renderPage(
  initialEntry = "/cases/case-1/operator-audit?run_id=run-1&attempt_id=attempt-1",
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route
            path="/cases/:caseId/operator-audit"
            element={<OperatorAuditPage />}
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("OperatorAuditPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.listCapabilityPacks.mockResolvedValue([pack]);
    mocks.listExternalSnapshots.mockResolvedValue([]);
    mocks.getReview.mockResolvedValue(workspace);
    mocks.listServiceObjects.mockResolvedValue({ items: [], next_cursor: null, can_manage: false });
    mocks.getServiceObject.mockResolvedValue(serviceObjectDetail);
    mocks.listServiceObjectVersions.mockResolvedValue({ items: [], next_cursor: null });
    mocks.listServiceObjectAssociations.mockResolvedValue({ items: [], next_cursor: null });
    mocks.useCaseDetail.mockReturnValue({
      data: caseDetail,
      isLoading: false,
      isError: false,
      error: null,
    });
  });

  it("renders the extension only when the server enables it", async () => {
    const user = userEvent.setup();
    mocks.useSettings.mockReturnValue({
      data: {
        operator_audit: {
          enabled: true,
          connectors: [
            {
              connector_id: "fixture.oss",
              source_system: "fixture-oss-east",
              allowed_objects: ["fixture-line-001"],
            },
          ],
        },
      },
      isLoading: false,
      isError: false,
      error: null,
    });
    renderPage();
    expect(await screen.findByText("跨系统核验")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 1, name: "运营商专项核验" }),
    ).toBeInTheDocument();
    expect(screen.getByText("政企专线·履约要素核验")).toBeInTheDocument();
    const facts = screen.getByLabelText("核验关键事实");
    for (const label of ["服务对象", "业务期间", "核验范围", "依据状态"]) {
      expect(within(facts).getByText(label)).toBeInTheDocument();
    }
    expect(screen.getByRole("tab", { name: "核验概览" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "服务对象" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "依据与差异" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "处理记录" })).toBeInTheDocument();
    expect(await screen.findByText("未受理核验")).toBeInTheDocument();
    expect(mocks.listCapabilityPacks).toHaveBeenCalled();
    expect(mocks.listServiceObjects).toHaveBeenCalledWith("case-1", null, expect.any(AbortSignal));
    expect(mocks.listExternalSnapshots).toHaveBeenCalledWith(
      "case-1",
      expect.anything(),
    );

    await user.click(screen.getByRole("tab", { name: "服务对象" }));
    expect(
      await screen.findByRole("heading", { name: "服务对象与冻结事实" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "合同对象与外部对象映射" }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "依据与差异" }));
    expect(await screen.findByText(/专线配置核验/)).toBeInTheDocument();
    expect(screen.getByText("采集外部证据")).toBeInTheDocument();
    expect(screen.getByText("受理核验")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "采集一次外部证据" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "受理确定性核验" }),
    ).toBeDisabled();
  });

  it("searches packs by their exact ID while preserving provided names and submission identity", async () => {
    const user = userEvent.setup();
    mocks.useSettings.mockReturnValue({ data: { operator_audit: { enabled: true, connectors: [] } }, isLoading: false, isError: false, error: null });
    mocks.listCapabilityPacks.mockResolvedValue([
      { ...pack, packId: "pack-unnamed", displayName: null },
      { ...pack, packId: "pack-named", displayName: "ENT（测试）股份有限公司" },
    ]);
    mocks.createFulfillmentEvaluation.mockResolvedValue({ evaluationId: "evaluation-1" });
    renderPage();
    await user.click(screen.getByRole("tab", { name: "依据与差异" }));
    expect(await screen.findByText("ENT（测试）股份有限公司")).toBeInTheDocument();
    const select = screen.getByRole("combobox", { name: "选择能力包" });
    await user.click(select);
    await user.type(select, "pack-unnamed");
    await user.click(await screen.findByRole("option", { name: /能力包名称未提供.*能力包标识 pack-unnamed/ }));
    await user.click(screen.getByRole("button", { name: "受理确定性核验" }));
    expect(mocks.createFulfillmentEvaluation).toHaveBeenCalledWith("case-1", expect.objectContaining({ pack_id: "pack-unnamed", pack_version: "1.0.0" }), expect.any(String));
  });

  it("does not expose a write surface when disabled", async () => {
    mocks.useSettings.mockReturnValue({
      data: { operator_audit: { enabled: false, connectors: [] } },
      isLoading: false,
      isError: false,
      error: null,
    });
    renderPage();
    expect(await screen.findByText("跨系统核验未启用")).toBeInTheDocument();
    expect(mocks.listCapabilityPacks).not.toHaveBeenCalled();
    expect(mocks.listExternalSnapshots).not.toHaveBeenCalled();
  });

  it("keeps missing facts as 未报告 and shows a truthful unaccepted overview", async () => {
    mocks.useSettings.mockReturnValue({ data: { operator_audit: { enabled: true, connectors: [] } }, isLoading: false, isError: false, error: null });
    renderPage();
    const facts = screen.getByLabelText("核验关键事实");
    expect(within(facts).getAllByText("未报告")).toHaveLength(4);
    expect(await screen.findByText("未受理核验")).toBeInTheDocument();
    expect(screen.getByText("检查项目")).toBeInTheDocument();
    expect(screen.queryByRole("table", { name: "检查项目" })).not.toBeInTheDocument();
  });

  it("renders read-only check rows and real facts from a persisted evaluation", async () => {
    mocks.useSettings.mockReturnValue({ data: { operator_audit: { enabled: true, connectors: [] } }, isLoading: false, isError: false, error: null });
    mocks.listServiceObjects.mockResolvedValue({ items: [serviceObject], next_cursor: null, can_manage: false });
    mocks.getFulfillmentEvaluation.mockResolvedValue(evaluation);
    renderPage("/cases/case-1/operator-audit?run_id=run-1&attempt_id=attempt-1&evaluation_id=evaluation-1");
    const overviewCard = (await screen.findByText("检查项目")).closest(".ant-card");
    expect(overviewCard).not.toBeNull();
    expect(await within(overviewCard as HTMLElement).findByText("line.configured_bandwidth")).toBeInTheDocument();
    const facts = screen.getByLabelText("核验关键事实");
    expect(within(facts).getByText("园区专线 A")).toBeInTheDocument();
    expect(within(facts).getByText("2026 年 11 月")).toBeInTheDocument();
    expect(within(facts).getByText("整案")).toBeInTheDocument();
    expect(within(facts).getByText("部分缺失")).toBeInTheDocument();
    expect(within(overviewCard as HTMLElement).getByText("执行完成")).toBeInTheDocument();
    expect(within(overviewCard as HTMLElement).getByText("信息不足")).toBeInTheDocument();
    expect(within(overviewCard as HTMLElement).getByRole("link", { name: "核对依据" })).toHaveAttribute(
      "href",
      "/cases/case-1/reviews/run-1?attempt_id=attempt-1",
    );
    expect(mocks.getFulfillmentEvaluation).toHaveBeenCalledWith(
      "case-1",
      "evaluation-1",
      expect.any(AbortSignal),
    );
  });

  it("falls back to aggregate evaluation checks when objects have no checks", async () => {
    mocks.useSettings.mockReturnValue({ data: { operator_audit: { enabled: true, connectors: [] } }, isLoading: false, isError: false, error: null });
    mocks.getFulfillmentEvaluation.mockResolvedValue({
      ...evaluation,
      objects: [],
      checks: [check],
    });
    renderPage("/cases/case-1/operator-audit?run_id=run-1&attempt_id=attempt-1&evaluation_id=evaluation-1");
    const overviewCard = (await screen.findByText("检查项目")).closest(".ant-card");
    expect(overviewCard).not.toBeNull();
    expect(await within(overviewCard as HTMLElement).findByText("line.configured_bandwidth")).toBeInTheDocument();
    expect(within(overviewCard as HTMLElement).getByText("执行完成")).toBeInTheDocument();
  });

  it("shows object-level outcomes when evaluation objects have no checks", async () => {
    mocks.useSettings.mockReturnValue({ data: { operator_audit: { enabled: true, connectors: [] } }, isLoading: false, isError: false, error: null });
    mocks.listServiceObjects.mockResolvedValue({ items: [serviceObject], next_cursor: null, can_manage: false });
    mocks.getFulfillmentEvaluation.mockResolvedValue({
      ...evaluation,
      objects: [{ ...evaluation.objects[0], checks: [] }],
      checks: [],
    });
    renderPage("/cases/case-1/operator-audit?run_id=run-1&attempt_id=attempt-1&evaluation_id=evaluation-1");
    const overviewCard = (await screen.findByText("检查项目")).closest(".ant-card");
    expect(overviewCard).not.toBeNull();
    expect(await within(overviewCard as HTMLElement).findByText("园区专线 A")).toBeInTheDocument();
    expect(within(overviewCard as HTMLElement).getByText("对象级结果")).toBeInTheDocument();
    expect(within(overviewCard as HTMLElement).getByText("证据不足")).toBeInTheDocument();
  });

  it("keeps the processing-record tab truthful when nothing has been accepted", async () => {
    const user = userEvent.setup();
    mocks.useSettings.mockReturnValue({ data: { operator_audit: { enabled: true, connectors: [] } }, isLoading: false, isError: false, error: null });
    renderPage();
    await user.click(screen.getByRole("tab", { name: "处理记录" }));
    expect(await screen.findByText("暂无处理记录")).toBeInTheDocument();
    expect(
      screen.getByText(/处理记录只展示当前页面已读取的数据/),
    ).toBeInTheDocument();
  });
});
