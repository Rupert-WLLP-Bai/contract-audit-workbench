import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithAntd as render } from "../test/antd";
import { SubjectDetailPage } from "./subjects-page";

const mocks = vi.hoisted(() => ({
  useSession: vi.fn(), useSubject: vi.fn(), useSubjectCaseLinks: vi.fn(), useSubjectHistory: vi.fn(),
}));

vi.mock("../api/queries", () => ({
  useSession: mocks.useSession, useSubject: mocks.useSubject,
  useSubjectCaseLinks: mocks.useSubjectCaseLinks, useSubjectHistory: mocks.useSubjectHistory,
  useCreateSubject: vi.fn(), useSubjects: vi.fn(),
}));
vi.mock("../api/idempotency", () => ({ pendingMutationKey: vi.fn(), clearPendingMutationKey: vi.fn() }));

const principal = { user_id: "user-1", issuer: "issuer", subject: "principal-1" };
const current = {
  subject_id: "subject-1", version: 2, display_name: "演示主体", legal_name: null,
  verification_state: "pending_verification", source_kind: "user_asserted", basis_summary: "经办人录入",
  payload_hash: "a".repeat(64), identity_claims: [], aliases: [], created_by: principal, created_at: "2026-10-31T00:00:00Z",
};
const binding = (id: string, title: string) => ({
  binding_id: id, binding_version: 1, case_id: `case-${id}`, case_title: title, business_status: "active",
  role: "supplier", binding_state: "active", source_kind: "user_asserted", legacy_label: null,
  created_by: principal, subject: current, created_at: "2026-10-31T00:00:00Z",
});

function renderPage() {
  return render(<MemoryRouter initialEntries={["/subjects/subject-1"]}>
    <Routes><Route path="/subjects/:subjectId" element={<SubjectDetailPage />} /></Routes>
  </MemoryRouter>);
}

beforeEach(() => {
  mocks.useSession.mockReturnValue({ data: { id: "user-1", tenant_id: "tenant-1", org_id: "org-1", audience: "client" } });
  mocks.useSubject.mockReturnValue({ data: { subject_id: "subject-1", current_version: 2, current, identifiers: [] }, isLoading: false, isError: false });
  mocks.useSubjectHistory.mockReturnValue({ data: { versions: [current], events: [] }, isLoading: false, isError: false });
  mocks.useSubjectCaseLinks.mockImplementation((_subjectId: string, cursor: string | null) => ({
    data: cursor ? { items: [binding("b2", "第二页案件")], visible_count: 1, next_cursor: null }
      : { items: [binding("b1", "第一页案件")], visible_count: 1, next_cursor: "cursor-2" },
    isLoading: false, isFetching: false, isError: false,
  }));
});

describe("subject detail pagination scope", () => {
  it("resets visible case-link pagination when the actor changes", async () => {
    const user = userEvent.setup();
    const view = renderPage();
    expect(screen.getByRole("link", { name: "第一页案件" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "下一页" }));
    expect(await screen.findByRole("link", { name: "第二页案件" })).toBeInTheDocument();
    mocks.useSession.mockReturnValue({ data: { id: "user-2", tenant_id: "tenant-1", org_id: "org-1", audience: "client" } });
    view.rerender(<MemoryRouter initialEntries={["/subjects/subject-1"]}>
      <Routes><Route path="/subjects/:subjectId" element={<SubjectDetailPage />} /></Routes>
    </MemoryRouter>);
    await waitFor(() => expect(screen.getByRole("link", { name: "第一页案件" })).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
  });
});
