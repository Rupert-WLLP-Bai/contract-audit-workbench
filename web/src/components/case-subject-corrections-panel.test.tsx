import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithAntd as render, chooseOption } from "../test/antd";
import { CaseSubjectCorrectionsPanel } from "./case-subject-corrections-panel";

const mocks = vi.hoisted(() => ({
  useSession: vi.fn(), useCaseDetail: vi.fn(), useCaseSubjectChanges: vi.fn(), useCaseSubjects: vi.fn(),
  useMaterialParsedDocuments: vi.fn(), useMaterialParsedTextPage: vi.fn(), useSubjects: vi.fn(), useSubject: vi.fn(), useProposeSubjectChange: vi.fn(),
  pendingMutationKey: vi.fn(), clearPendingMutationKey: vi.fn(),
}));

vi.mock("../api/queries", () => ({
  useSession: mocks.useSession, useCaseDetail: mocks.useCaseDetail,
  useCaseSubjectChanges: mocks.useCaseSubjectChanges, useCaseSubjects: mocks.useCaseSubjects,
  useMaterialParsedDocuments: mocks.useMaterialParsedDocuments, useMaterialParsedTextPage: mocks.useMaterialParsedTextPage, useSubjects: mocks.useSubjects,
  useSubject: mocks.useSubject,
  useProposeSubjectChange: mocks.useProposeSubjectChange,
}));

vi.mock("../api/idempotency", () => ({
  idempotencyActorScope: () => ({ actorId: "actor-1", tenantId: "tenant-1", orgId: "org-1", audience: "client" }),
  pendingMutationKey: mocks.pendingMutationKey, clearPendingMutationKey: mocks.clearPendingMutationKey,
}));

const quote = "本合同所列服务提供方为示例网络有限公司。";
const subject = {
  subject_id: "subject-1", version: 2, display_name: "示例网络有限公司", legal_name: null,
  verification_state: "pending_verification" as const, source_kind: "user_asserted" as const,
  basis_summary: "录入待核", payload_hash: "a".repeat(64), identity_claims: [], aliases: [],
  created_by: { user_id: "actor-1", issuer: "issuer", subject: "principal" }, created_at: "2026-10-31T00:00:00Z",
};
const binding = {
  binding_id: "binding-1", binding_version: 4, case_id: "case-1", role: "service_provider",
  binding_state: "active", source_kind: "user_asserted", legacy_label: null,
  created_by: subject.created_by, created_at: "2026-10-31T00:00:00Z", subject,
};
const textPage = {
  material_id: "material-1", material_version: 3, document_id: "document-1", parsed_document_id: "parse-3",
  filename: "专线服务合同.pdf", original_digest: "b".repeat(64), parse_digest: "c".repeat(64), offset: 0,
  total_runes: [...quote].length, text: quote, next_offset: null,
};

function renderPanel() {
  return render(<MemoryRouter><CaseSubjectCorrectionsPanel caseId="case-1" /></MemoryRouter>);
}

async function selectWholeSource(user: ReturnType<typeof userEvent.setup>) {
  await chooseOption(user, screen.getByRole("combobox", { name: "选择固定材料版本" }), /附件 · 补充协议 · v3/);
  await chooseOption(user, screen.getByRole("combobox", { name: "选择材料固定解析版本" }), /parse-3/);
  const pre = screen.getByLabelText("固定材料原文，可选择作为更正依据");
  const textNode = pre.firstChild;
  if (!textNode) throw new Error("parsed page text node missing");
  const range = document.createRange();
  range.setStart(textNode, 0);
  range.setEnd(textNode, quote.length);
  const selection = window.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
  fireEvent.mouseUp(pre);
  expect(screen.getAllByText(quote).length).toBeGreaterThan(1);
}

beforeEach(() => {
  mocks.useSession.mockReturnValue({ data: { id: "actor-1", tenant_id: "tenant-1", org_id: "org-1", audience: "client" } });
  mocks.useCaseDetail.mockReturnValue({ data: {
    materials: [{ material_id: "material-1", role: "attachment", purpose: "supplement", current_document_id: "document-1", current_version: 3 }],
    material_versions: [{ material_id: "material-1", version: 3, document_id: "document-1", previous_document_id: null, created_at: "2026-10-31T00:00:00Z" }],
  }, isLoading: false, isError: false });
  mocks.useCaseSubjectChanges.mockReturnValue({ data: { items: [] }, refetch: vi.fn() });
  mocks.useCaseSubjects.mockReturnValue({ data: { items: [binding], legacy_profile: null, can_manage: true }, isLoading: false });
  mocks.useMaterialParsedDocuments.mockReturnValue({ data: { items: [{ parsed_document_id: "parse-3", status: "ready", parser_id: "pdf", parser_version: "2", created_at: "2026-10-31T00:00:00Z", original_digest: "b".repeat(64), normalized_digest: "c".repeat(64) }] }, isLoading: false, isError: false });
  mocks.useSubject.mockReturnValue({ data: { subject_id: "subject-1", current_version: 2, current: subject, identifiers: [] }, isLoading: false, isError: false });
  mocks.useMaterialParsedTextPage.mockReturnValue({ data: textPage, isLoading: false, isError: false });
  mocks.useSubjects.mockReturnValue({ data: { items: [{ subject_id: "subject-2", current_version: 6, display_name: "备选服务企业", verification_state: "pending_verification" }] }, isLoading: false, isError: false });
  mocks.pendingMutationKey.mockResolvedValue({ storageKey: "safe-key-slot", key: "random-idempotency-key" });
  mocks.clearPendingMutationKey.mockReset();
  mocks.useProposeSubjectChange.mockReturnValue({ isPending: false, mutateAsync: vi.fn().mockResolvedValue({ request_id: "request-1" }) });
});

describe("case subject correction proposal", () => {
  it("uses any pinned material version without a finding and sends only IDs, rune span and quote digest", async () => {
    const mutateAsync = vi.fn().mockResolvedValue({ request_id: "request-1" });
    mocks.useProposeSubjectChange.mockReturnValue({ isPending: false, mutateAsync });
    const user = userEvent.setup();
    renderPanel();

    await chooseOption(user, screen.getByRole("combobox", { name: "选择本案主体" }), /示例网络有限公司/);
    await selectWholeSource(user);
    await user.type(screen.getByLabelText("更正后主体名称"), "示例网络有限公司（更正）");
    await user.click(screen.getByLabelText("明确清空法定名称"));
    await chooseOption(user, screen.getByRole("combobox", { name: "统一社会信用代码更正方式" }), "更正统一社会信用代码");
    await user.type(screen.getByLabelText("更正后统一社会信用代码"), "9132000000000001X3");
    await user.type(screen.getByLabelText("申请说明"), "依据固定合同原文更正主体资料");
    await user.click(screen.getByRole("button", { name: "提交更正申请" }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    const request = mutateAsync.mock.calls[0]![0];
    expect(request).toMatchObject({
      value: {
        target: { kind: "subject", subject_id: "subject-1", binding_id: null },
        expected_subject_version: 2,
        patch: {
          display_name: "示例网络有限公司（更正）",
          legal_name: "",
          identity_claims: [{ namespace: "cn.gov.samr.uscc", identifier_type: "unified_social_credit_code", value: "9132000000000001X3" }],
        },
        basis_refs: [{
          kind: "material_anchor", material_id: "material-1", material_version: 3,
          document_id: "document-1", parsed_document_id: "parse-3", start: 0, end: [...quote].length,
        }],
      },
      idempotencyKey: "random-idempotency-key",
    });
    expect(request.value.basis_refs[0].quote_digest).toMatch(/^[a-f0-9]{64}$/);
    expect(JSON.stringify(request)).not.toContain(quote);
    expect(request.value).not.toHaveProperty("payload_hash");
  });

  it("supports a flat role, binding-state and explicit existing-subject rebind patch", async () => {
    const mutateAsync = vi.fn().mockResolvedValue({ request_id: "request-2" });
    mocks.useProposeSubjectChange.mockReturnValue({ isPending: false, mutateAsync });
    const user = userEvent.setup();
    renderPanel();
    await chooseOption(user, screen.getByRole("combobox", { name: "更正目标类型" }), "案件角色绑定");
    await chooseOption(user, screen.getByRole("combobox", { name: "选择本案角色绑定" }), /服务提供方/);
    await chooseOption(user, screen.getByRole("combobox", { name: "更正后案件角色" }), "供应商");
    await chooseOption(user, screen.getByRole("combobox", { name: "更正后绑定状态" }), "在办绑定");
    await user.type(screen.getByLabelText("搜索重新绑定主体"), "备选");
    await chooseOption(user, screen.getByRole("combobox", { name: "选择重新绑定主体" }), /备选服务企业/);
    await selectWholeSource(user);
    await user.type(screen.getByLabelText("申请说明"), "更正案件角色归属");
    await user.click(screen.getByRole("button", { name: "提交更正申请" }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync.mock.calls[0]![0].value).toMatchObject({
      target: { kind: "case_subject_role", subject_id: "subject-1", binding_id: "binding-1" },
      expected_subject_version: 2, expected_binding_version: 4,
      patch: { role: "supplier", binding_state: "active", subject_id: "subject-2", expected_new_subject_version: 6 },
    });
  });

  it("keeps the submit action unavailable when no parsed material version is available", () => {
    mocks.useCaseDetail.mockReturnValue({ data: { materials: [], material_versions: [] }, isLoading: false, isError: false });
    mocks.useMaterialParsedTextPage.mockReturnValue({ data: undefined, isLoading: false, isError: false });
    renderPanel();
    expect(screen.getByText("本案没有可引用的固定材料版本")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "提交更正申请" })).toBeDisabled();
  });

  it("does not render a correction write form when the server marks the case read-only", () => {
    mocks.useCaseSubjects.mockReturnValue({ data: { items: [binding], legacy_profile: null, can_manage: false }, isLoading: false });
    renderPanel();
    expect(screen.getByText("当前案件为只读状态，无法发起主体更正。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "提交更正申请" })).not.toBeInTheDocument();
  });
});
