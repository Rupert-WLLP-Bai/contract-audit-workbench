import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CaseRoleBindingProjection, SubjectSourceSnapshot, SubjectSourceVerificationResult } from "../api/types";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { SubjectSourceVerificationPanel } from "./subject-source-verification-panel";

const mocks = vi.hoisted(() => ({
  useSession: vi.fn(),
  useCreateSubjectSourceSnapshot: vi.fn(),
  useVerifySubjectAgainstSourceSnapshot: vi.fn(),
  useSubjectSourceSnapshot: vi.fn(),
  useSubjectSourceSnapshots: vi.fn(),
  useSubjectSourceDirectory: vi.fn(),
  pendingMutationKey: vi.fn(),
  clearPendingMutationKey: vi.fn(),
}));

vi.mock("../api/queries", () => ({
  useSession: mocks.useSession,
  useCreateSubjectSourceSnapshot: mocks.useCreateSubjectSourceSnapshot,
  useVerifySubjectAgainstSourceSnapshot: mocks.useVerifySubjectAgainstSourceSnapshot,
  useSubjectSourceSnapshot: mocks.useSubjectSourceSnapshot,
  useSubjectSourceSnapshots: mocks.useSubjectSourceSnapshots,
  useSubjectSourceDirectory: mocks.useSubjectSourceDirectory,
}));

vi.mock("../api/idempotency", () => ({
  idempotencyActorScope: () => ({ actorId: "actor-1", tenantId: "tenant-1", orgId: "org-1", audience: "client" }),
  pendingMutationKey: mocks.pendingMutationKey,
  clearPendingMutationKey: mocks.clearPendingMutationKey,
}));

const bindings = [{
  binding_id: "binding-1", binding_version: 2, case_id: "case-1", role: "service_provider",
  binding_state: "active", source_kind: "user_asserted", legacy_label: null,
  created_by: { user_id: "actor-1", issuer: "issuer", subject: "principal" }, created_at: "2026-10-31T00:00:00Z",
  subject: {
    subject_id: "subject-1", version: 3, display_name: "示例网络有限公司", legal_name: null,
    verification_state: "pending_verification", source_kind: "user_asserted", basis_summary: "待核验",
    payload_hash: "a".repeat(64), identity_claims: [], aliases: [],
    created_by: { user_id: "actor-1", issuer: "issuer", subject: "principal" }, created_at: "2026-10-31T00:00:00Z",
  },
}] as unknown as CaseRoleBindingProjection[];
const snapshot: SubjectSourceSnapshot = {
  snapshot_id: "snapshot-1", source_catalog_id: "synthetic.uscc.demo", source_version: 1,
  source_digest: "b".repeat(64), captured_at: "2026-10-31T00:00:00Z", source_scope: "synthetic_controlled",
  record_count: 3, label: "演示受控主体目录",
};

function Location() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}

function renderPanel(initialEntry = "/cases/case-1", canManage = true) {
  return render(<MemoryRouter initialEntries={[initialEntry]}>
    <Routes>
      <Route path="/cases/:caseId" element={<><SubjectSourceVerificationPanel caseId="case-1" bindings={bindings} canManage={canManage} /><Location /></>} />
      <Route path="/cases/:caseId/subject-changes/:requestId" element={<Location />} />
    </Routes>
  </MemoryRouter>);
}

async function begin(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "固定新的合成来源版本" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "对照合成来源并发起审批" })).toBeInTheDocument());
  await chooseOption(user, screen.getByRole("combobox", { name: "选择待对照的案件主体" }), /示例网络有限公司/);
}

beforeEach(() => {
  mocks.useSession.mockReturnValue({ data: { id: "actor-1", tenant_id: "tenant-1", org_id: "org-1", audience: "client" } });
  mocks.useCreateSubjectSourceSnapshot.mockReturnValue({ isPending: false, mutateAsync: vi.fn().mockResolvedValue(snapshot) });
  mocks.useSubjectSourceSnapshots.mockReturnValue({ data: { items: [], next_cursor: null }, isLoading: false });
  mocks.useSubjectSourceDirectory.mockReturnValue({ data: { items: [{
    source_id: "synthetic.uscc.demo", source_version: 4, display_name: "演示主体名录",
    verification_scope: "synthetic_controlled",
  }] }, isLoading: false, isError: false });
  mocks.useSubjectSourceSnapshot.mockImplementation((_caseId: string, snapshotId: string) => ({
    data: snapshotId ? {
      snapshot_id: snapshotId, source_catalog_id: "synthetic.uscc.demo", source_version: 1,
      source_digest: "b".repeat(64), source_scope: "synthetic_controlled",
      record_count: 3, label: "已对照核验（受控来源，合成演示）", case_id: "case-1", captured_at: "2026-10-31T00:00:00Z",
    } : undefined,
    isLoading: false,
  }));
  mocks.useVerifySubjectAgainstSourceSnapshot.mockReturnValue({ isPending: false, mutateAsync: vi.fn() });
  mocks.pendingMutationKey.mockResolvedValue({ storageKey: "safe-key-slot", key: "request-key" });
  mocks.clearPendingMutationKey.mockReset();
});

describe("synthetic source verification flow", () => {
  it("keeps source history readable but disables capture and verification for a server read-only case", () => {
    renderPanel("/cases/case-1?source_case=case-1&source_snapshot=snapshot-1", false);
    expect(screen.getByText(/已对照核验（受控来源，合成演示）/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "固定新的合成来源版本" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "对照合成来源并发起审批" })).toBeDisabled();
  });

  it("labels the source as synthetic and routes only an exact match to a pending review", async () => {
    const result: SubjectSourceVerificationResult = {
      request_id: "change-1", source_catalog_id: "synthetic.uscc.demo",
      source_version: 1, source_digest: "b".repeat(64), source_scope: "synthetic_controlled",
      source_as_of: "2026-10-30T00:00:00Z", valid_from: "2026-01-01T00:00:00Z", valid_until: null,
      match_outcome: "exact_match", state: "pending_decision",
      snapshot_id: "snapshot-1",
      matched_record: { display_name: "示例网络有限公司", masked_identifier: "9132••••••••••01X3" },
    };
    const verify = vi.fn().mockResolvedValue(result);
    mocks.useVerifySubjectAgainstSourceSnapshot.mockReturnValue({ isPending: false, mutateAsync: verify });
    const user = userEvent.setup();
    renderPanel();
    expect(screen.getByText(/不代表真实工商或主体身份核验/)).toBeInTheDocument();
    await begin(user);
    await user.click(screen.getByRole("button", { name: "对照合成来源并发起审批" }));
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/cases/case-1/subject-changes/change-1"));
    expect(verify).toHaveBeenCalledWith({
      value: { subject_id: "subject-1", expected_subject_version: 3, expected_binding_id: "binding-1" },
      idempotencyKey: "request-key",
    });
    const createCall = mocks.useCreateSubjectSourceSnapshot.mock.results[0]?.value.mutateAsync.mock.calls[0]?.[0];
    expect(createCall?.value.expected_source_version).toBe(4);
  });

  it("does not navigate or claim verification when no record matches", async () => {
    const noMatch: SubjectSourceVerificationResult = {
      request_id: null, source_catalog_id: "synthetic.uscc.demo",
      source_version: 1, source_digest: "b".repeat(64), source_scope: "synthetic_controlled",
      source_as_of: "2026-10-30T00:00:00Z", valid_from: "2026-01-01T00:00:00Z", valid_until: null,
      match_outcome: "no_match", state: "not_proposed", matched_record: null, snapshot_id: "snapshot-1",
    };
    mocks.useVerifySubjectAgainstSourceSnapshot.mockReturnValue({ isPending: false, mutateAsync: vi.fn().mockResolvedValue(noMatch) });
    const user = userEvent.setup();
    renderPanel();
    await begin(user);
    await user.click(screen.getByRole("button", { name: "对照合成来源并发起审批" }));
    expect(await screen.findByText("合成来源没有找到一致记录；未创建更正申请。")).toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent("/cases/case-1");
    expect(screen.queryByText("已核验")).not.toBeInTheDocument();
  });

  it("restores a prior snapshot from the URL and authorized snapshot list without creating another", async () => {
    const create = vi.fn().mockResolvedValue(snapshot);
    mocks.useCreateSubjectSourceSnapshot.mockReturnValue({ isPending: false, mutateAsync: create });
    mocks.useSubjectSourceSnapshots.mockReturnValue({ data: { items: [{
      snapshot_id: "snapshot-1", source_catalog_id: "synthetic.uscc.demo", source_version: 1,
      source_digest: "b".repeat(64), source_scope: "synthetic_controlled",
      record_count: 3, label: "演示受控主体目录", captured_at: "2026-10-31T00:00:00Z",
    }], next_cursor: null }, isLoading: false });
    const user = userEvent.setup();
    renderPanel();
    await chooseOption(user, screen.getByRole("combobox", { name: "选择已有合成来源快照" }), /来源 v1/);
    await waitFor(() => expect(screen.getByText(/固定记录 3 条/)).toBeInTheDocument());
    await chooseOption(user, screen.getByRole("combobox", { name: "选择待对照的案件主体" }), /示例网络有限公司/);
    await user.click(screen.getByRole("button", { name: "对照合成来源并发起审批" }));
    await waitFor(() => expect(mocks.useVerifySubjectAgainstSourceSnapshot).toHaveBeenCalledWith("case-1", "snapshot-1"));
    expect(create).not.toHaveBeenCalled();
  });

  it("keeps source creation disabled when the server has no configured source", () => {
    mocks.useSubjectSourceDirectory.mockReturnValue({ data: { items: [] }, isLoading: false, isError: false });
    renderPanel();
    expect(screen.getByText(/尚未配置可用的合成演示来源/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "固定新的合成来源版本" })).toBeDisabled();
  });
});
