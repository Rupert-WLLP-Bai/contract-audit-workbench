import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter, Route, Routes } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithAntd as render } from "../test/antd";
import { SubjectChangeReviewPage } from "./subject-change-review-page";

const mocks = vi.hoisted(() => ({
  useCaseSubjectChange: vi.fn(),
  useCaseSubjectChangeImpact: vi.fn(),
  useDecideSubjectChange: vi.fn(),
  useSession: vi.fn(),
  pendingMutationKey: vi.fn(),
  clearPendingMutationKey: vi.fn(),
}));

vi.mock("../api/queries", () => ({
  useCaseSubjectChange: mocks.useCaseSubjectChange,
  useCaseSubjectChangeImpact: mocks.useCaseSubjectChangeImpact,
  useDecideSubjectChange: mocks.useDecideSubjectChange,
  useSession: mocks.useSession,
}));

vi.mock("../api/idempotency", () => ({
  idempotencyActorScope: () => ({ actorId: "reviewer-2", tenantId: "tenant-1", orgId: "org-1", audience: "client" }),
  pendingMutationKey: mocks.pendingMutationKey,
  clearPendingMutationKey: mocks.clearPendingMutationKey,
}));

const before = {
  subject_id: "subject-1", version: 1, display_name: "示例网络公司", legal_name: null,
  verification_state: "pending_verification", source_kind: "user_asserted",
  basis_summary: "录入待核验", payload_hash: "a".repeat(64), identity_claims: [], aliases: [],
  created_by: { user_id: "requester-1", issuer: "issuer", subject: "principal-1" }, created_at: "2026-10-30T00:00:00Z",
};
const after = { ...before, version: 2, display_name: "示例网络有限公司", payload_hash: "b".repeat(64) };
const request = {
  summary: {
    request_id: "change-1", target_kind: "subject", state: "pending_decision", source_case_id: "case-1",
    target_subject_id: "subject-1", target_binding_id: null, expected_subject_version: 1,
    expected_binding_version: null, payload_hash: "c".repeat(64),
    proposer: { user_id: "requester-1", issuer: "issuer", subject: "principal-1" },
    proposed_by_display_name: "提案经办人", can_decide: true, decision_blocked_reason: null,
    created_at: "2026-10-30T00:00:00Z",
  },
  proposed_by_display_name: "提案经办人", can_decide: true, decision_blocked_reason: null,
  before,
  after,
  basis_refs: [{
    kind: "material_anchor", material_id: "material-1", material_version: 3,
    document_id: "document-1", parsed_document_id: "parse-1", start: 0, end: 15,
    quote_digest: "d".repeat(64), filename: "专线合同.pdf", quote: "供应商名称为示例网络有限公司。",
  }],
  impact_etag: "impact-secret-etag",
  authorized_impact_cases: [{ case_id: "case-1", case_name: "政企专线采购", status: "active" }],
};

function renderPage() {
  return render(<MemoryRouter initialEntries={["/cases/case-1/subject-changes/change-1"]}>
    <Link to="/cases/case-2/subject-changes/change-2">切换案件</Link>
    <Routes><Route path="/cases/:caseId/subject-changes/:requestId" element={<SubjectChangeReviewPage />} /></Routes>
  </MemoryRouter>);
}

beforeEach(() => {
  mocks.useSession.mockReturnValue({ data: { id: "reviewer-2", tenant_id: "tenant-1", org_id: "org-1", audience: "client" } });
  mocks.useCaseSubjectChange.mockReturnValue({ data: request, isLoading: false, isError: false });
  mocks.useCaseSubjectChangeImpact.mockReturnValue({ data: {
    visible_affected_count: 1, affected_cases: [{ case_id: "case-1", case_name: "政企专线采购", status: "active" }],
    impact_etag: "impact-secret-etag",
    proposed_by_display_name: "提案经办人", can_decide: true, decision_blocked_reason: null,
  }, isLoading: false, isError: false, refetch: vi.fn() });
  mocks.useDecideSubjectChange.mockReturnValue({ isPending: false, mutateAsync: vi.fn().mockResolvedValue({ decision: "approved" }) });
  mocks.pendingMutationKey.mockResolvedValue({ storageKey: "review-key-slot", key: "decision-key" });
  mocks.clearPendingMutationKey.mockReset();
});

describe("subject change review", () => {
  it("shows the real before/after and evidence while keeping hashes and etags out of the page", async () => {
    const mutateAsync = vi.fn().mockResolvedValue({ decision: "approved" });
    mocks.useDecideSubjectChange.mockReturnValue({ isPending: false, mutateAsync });
    renderPage();
    expect(screen.getByText("示例网络公司")).toBeInTheDocument();
    expect(screen.getByText("示例网络有限公司")).toBeInTheDocument();
    expect(screen.getByText("供应商名称为示例网络有限公司。")).toBeInTheDocument();
    expect(screen.getByText("专线合同.pdf")).toBeInTheDocument();
    expect(screen.getByText("政企专线采购")).toBeInTheDocument();
    expect(screen.getByText("申请人：提案经办人")).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("impact-secret-etag");
    expect(document.body.textContent).not.toContain("c".repeat(64));

    const user = userEvent.setup();
    await user.type(screen.getByLabelText("复核理由"), "材料足以支持本次名称更正");
    await user.click(screen.getByRole("button", { name: "批准并生效" }));
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync.mock.calls[0]![0]).toMatchObject({
      value: {
        decision: "approve", expected_subject_version: 1, expected_binding_version: null,
        expected_payload_hash: "c".repeat(64), expected_impact_etag: "impact-secret-etag",
        reason: "材料足以支持本次名称更正",
      },
      idempotencyKey: "decision-key",
    });
    expect(screen.getByText("更正已批准并由服务端追加新版本。")).toBeInTheDocument();
  });

  it("prevents the proposing account from reviewing its own request", () => {
    mocks.useSession.mockReturnValue({ data: { id: "requester-1", tenant_id: "tenant-1", org_id: "org-1", audience: "client" } });
    mocks.useCaseSubjectChange.mockReturnValue({ data: {
      ...request, summary: { ...request.summary, can_decide: false, decision_blocked_reason: "申请人不能复核自己的更正。" },
      can_decide: false, decision_blocked_reason: "申请人不能复核自己的更正。",
    }, isLoading: false, isError: false });
    renderPage();
    expect(screen.getByText("申请人不能复核自己的更正。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "批准并生效" })).not.toBeInTheDocument();
  });

  it("clears a pending review reason when the case or request route changes", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.type(screen.getByLabelText("复核理由"), "上一案件的理由");
    await user.click(screen.getByRole("link", { name: "切换案件" }));
    await waitFor(() => expect(screen.getByLabelText("复核理由")).toHaveValue(""));
  });
});
