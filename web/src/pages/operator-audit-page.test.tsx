import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen } from "@testing-library/react";
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
  return { ...actual, listServiceObjects: mocks.listServiceObjects };
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

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter
        initialEntries={[
          "/cases/case-1/operator-audit?run_id=run-1&attempt_id=attempt-1",
        ]}
      >
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
    mocks.listCapabilityPacks.mockResolvedValue([pack]);
    mocks.listExternalSnapshots.mockResolvedValue([]);
    mocks.getReview.mockResolvedValue(workspace);
    mocks.listServiceObjects.mockResolvedValue({ items: [], next_cursor: null, can_manage: false });
    mocks.useCaseDetail.mockReturnValue({
      data: caseDetail,
      isLoading: false,
      isError: false,
      error: null,
    });
  });

  it("renders the extension only when the server enables it", async () => {
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
    expect(await screen.findByText(/专线配置核验/)).toBeInTheDocument();
    expect(screen.getByText("采集外部证据")).toBeInTheDocument();
    expect(screen.getByText("受理核验")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "采集一次外部证据" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "受理确定性核验" }),
    ).toBeDisabled();
    expect(mocks.listCapabilityPacks).toHaveBeenCalled();
    expect(mocks.listServiceObjects).toHaveBeenCalledWith("case-1", null, expect.any(AbortSignal));
    expect(mocks.listExternalSnapshots).toHaveBeenCalledWith(
      "case-1",
      expect.anything(),
    );
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
});
