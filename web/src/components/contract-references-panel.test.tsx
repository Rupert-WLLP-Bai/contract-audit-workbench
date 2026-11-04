import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { emptyDraft } from "../api/contracts";
import { ApiError } from "../api/http";
import { ContractReferencesPanel } from "./contract-references-panel";

const mock = vi.hoisted(() => ({
  cases: { isError: false, isSuccess: true, isLoading: false, data: [] as unknown[] },
  session: { isError: false, isSuccess: true, isFetching: false, data: { id: "actor-a", display_name: "只读成员甲", role: "viewer", audience: "client", tenant_id: "tenant-a", org_id: "org-a", principal_id: "principal-a", csrf_token: "a".repeat(32), context_switch_enabled: false, active_context: { membership_id: "membership-a", context_version: "1", tenant_id: "tenant-a", tenant_name: "租户甲", org_id: "org-a", org_name: "组织甲" } } },
  check: vi.fn(),
}));
vi.mock("../api/queries", () => ({ useCases: () => mock.cases, useSession: () => mock.session }));
vi.mock("../api/contracts", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/contracts")>(),
  checkContractReferences: mock.check,
}));

const record = { id: "case-full-1", title: "同名来源", contract_type: "service", document_count: 0,
  pending_decisions: 0, latest_run: null, updated_at: "2026-11-05T01:02:03Z", case_number: "BUS-1" };

beforeEach(() => {
  mock.cases = { isError: false, isSuccess: true, isLoading: false, data: [record] };
  mock.session = { isError: false, isSuccess: true, isFetching: false, data: { id: "actor-a", display_name: "只读成员甲", role: "viewer", audience: "client", tenant_id: "tenant-a", org_id: "org-a", principal_id: "principal-a", csrf_token: "a".repeat(32), context_switch_enabled: false, active_context: { membership_id: "membership-a", context_version: "1", tenant_id: "tenant-a", tenant_name: "租户甲", org_id: "org-a", org_name: "组织甲" } } };
  mock.check.mockReset();
});

it("hides a selected source and its previous result when list access is revoked", async () => {
  mock.check.mockResolvedValue({ source_case_id: record.id, source: "draft", checked_at: "2026-11-05", subjects: [], materials: [], warnings: [] });
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-1" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("combobox"));
  await user.click(screen.getByRole("option", { name: /BUS-1.*case-full-1/ }));
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(button);
  expect(await screen.findByText(/来源案件 case-full-1/)).toBeVisible();
  mock.cases = { isError: false, isSuccess: true, isLoading: false, data: [] };
  view.rerender(<ContractReferencesPanel contractID="contract-1" draft={draft} />);
  await waitFor(() => expect(screen.queryByText(/来源案件 case-full-1/)).not.toBeInTheDocument());
  await user.click(screen.getByRole("combobox"));
  expect(screen.queryByRole("option", { name: /BUS-1/ })).not.toBeInTheDocument();
});

it.each([false, true])("discards a delayed result after switching contracts with selected source %s", async (withSource) => {
  const delayed = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(delayed.promise);
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  const user = userEvent.setup();
  if (withSource) {
    await user.click(screen.getByRole("combobox"));
    await user.click(screen.getByRole("option", { name: /BUS-1.*case-full-1/ }));
  }
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(mock.check).toHaveBeenCalledWith("contract-A", withSource ? record.id : undefined);
  view.rerender(<ContractReferencesPanel contractID="contract-B" draft={draft} />);
  await act(async () => delayed.resolve({ source_case_id: record.id, source: "draft", checked_at: "合同A的旧结果", subjects: [], materials: [], warnings: [] }));
  expect(screen.queryByText(/合同A的旧结果/)).not.toBeInTheDocument();
});

it("does not show a completed result when the same source is reused by another contract", async () => {
  mock.check.mockResolvedValue({ source_case_id: record.id, source: "draft", checked_at: "合同A已完成结果", subjects: [], materials: [], warnings: [] });
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(/合同A已完成结果/)).toBeVisible();
  view.rerender(<ContractReferencesPanel contractID="contract-B" draft={draft} />);
  expect(screen.queryByText(/合同A已完成结果/)).not.toBeInTheDocument();
  view.rerender(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  expect(screen.queryByText(/合同A已完成结果/)).not.toBeInTheDocument();
});

it("keeps the new request busy when an older contract request fails", async () => {
  const oldRequest = Promise.withResolvers<unknown>();
  const newRequest = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(oldRequest.promise).mockReturnValueOnce(newRequest.promise);
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  const user = userEvent.setup();
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(button);
  expect(button).toHaveClass("ant-btn-loading");
  view.rerender(<ContractReferencesPanel contractID="contract-B" draft={draft} />);
  expect(button).not.toHaveClass("ant-btn-loading");
  await user.click(button);
  expect(mock.check).toHaveBeenLastCalledWith("contract-B", undefined);
  await act(async () => oldRequest.reject(new ApiError("合同A旧错误", 403, "forbidden", false, null)));
  expect(screen.queryByText(/合同A旧错误/)).not.toBeInTheDocument();
  expect(button).toHaveClass("ant-btn-loading");
  await act(async () => newRequest.resolve({ source_case_id: record.id, source: "revision", checked_at: "合同B新结果", subjects: [], materials: [], warnings: [] }));
  expect(screen.getByText(/合同B新结果/)).toBeVisible();
  expect(button).not.toHaveClass("ant-btn-loading");
});

it("hides a completed error on contract switch and allows the new contract to retry", async () => {
  mock.check.mockRejectedValueOnce(new ApiError("合同A无法核对", 403, "forbidden", false, null)).mockResolvedValueOnce({
    source_case_id: record.id, source: "revision", checked_at: "合同B合法结果", subjects: [], materials: [], warnings: [],
  });
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(/合同A无法核对/)).toBeVisible();
  view.rerender(<ContractReferencesPanel contractID="contract-B" draft={draft} />);
  expect(screen.queryByText(/合同A无法核对/)).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /核对引用/ }));
  expect(await screen.findByText(/合同B合法结果/)).toBeVisible();
  expect(mock.check).toHaveBeenLastCalledWith("contract-B", undefined);
});

it("keeps the newest completed result when an earlier request settles later", async () => {
  const oldRequest = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(oldRequest.promise).mockResolvedValueOnce({
    source_case_id: record.id, source: "draft", checked_at: "新合同结果", subjects: [], materials: [], warnings: [],
  });
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  const user = userEvent.setup();
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(button);
  view.rerender(<ContractReferencesPanel contractID="contract-B" draft={draft} />);
  await user.click(button);
  expect(await screen.findByText(/新合同结果/)).toBeVisible();
  await act(async () => oldRequest.resolve({ source_case_id: record.id, source: "draft", checked_at: "迟到旧合同结果", subjects: [], materials: [], warnings: [] }));
  expect(screen.getByText(/新合同结果/)).toBeVisible();
  expect(screen.queryByText(/迟到旧合同结果/)).not.toBeInTheDocument();
});

it("does not revive a pending result after contract A to B to A", async () => {
  const oldRequest = Promise.withResolvers<unknown>();
  const newRequest = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(oldRequest.promise).mockReturnValueOnce(newRequest.promise);
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  const user = userEvent.setup();
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(button);
  view.rerender(<ContractReferencesPanel contractID="contract-B" draft={draft} />);
  view.rerender(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  await user.click(button);
  await act(async () => oldRequest.resolve({ source_case_id: record.id, source: "revision", checked_at: "旧请求结果", subjects: [], materials: [], warnings: [] }));
  expect(screen.queryByText(/旧请求结果/)).not.toBeInTheDocument();
  expect(button).toHaveClass("ant-btn-loading");
  await act(async () => newRequest.resolve({ source_case_id: record.id, source: "revision", checked_at: "新请求结果", subjects: [], materials: [], warnings: [] }));
  expect(screen.getByText(/新请求结果/)).toBeVisible();
});

it("does not revive a request after a source is revoked and the same ID is selected again", async () => {
  const oldRequest = Promise.withResolvers<unknown>();
  const newRequest = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(oldRequest.promise).mockReturnValueOnce(newRequest.promise);
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("combobox"));
  await user.click(screen.getByRole("option", { name: /BUS-1.*case-full-1/ }));
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(button);
  mock.cases = { isError: false, isSuccess: true, isLoading: false, data: [] };
  view.rerender(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  mock.cases = { isError: false, isSuccess: true, isLoading: false, data: [record] };
  view.rerender(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  await user.click(screen.getByRole("combobox"));
  await user.click(screen.getByRole("option", { name: /BUS-1.*case-full-1/ }));
  await user.click(button);
  await act(async () => oldRequest.resolve({ source_case_id: record.id, source: "draft", checked_at: "撤权前结果", subjects: [], materials: [], warnings: [] }));
  expect(screen.queryByText(/撤权前结果/)).not.toBeInTheDocument();
  expect(button).toHaveClass("ant-btn-loading");
  await act(async () => newRequest.resolve({ source_case_id: record.id, source: "draft", checked_at: "重新授权结果", subjects: [], materials: [], warnings: [] }));
  expect(screen.getByText(/重新授权结果/)).toBeVisible();
});

it("invalidates a pending blank-source check when the authorized list changes", async () => {
  const delayed = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(delayed.promise);
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="imported-contract" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(mock.check).toHaveBeenCalledWith("imported-contract", undefined);
  mock.cases = { isError: false, isSuccess: true, isLoading: false, data: [record] };
  view.rerender(<ContractReferencesPanel contractID="imported-contract" draft={draft} />);
  await act(async () => delayed.resolve({ source_case_id: record.id, source: "revision", checked_at: "切换前结果", subjects: [], materials: [], warnings: [] }));
  expect(screen.queryByText(/切换前结果/)).not.toBeInTheDocument();
});

it("invalidates the old request when the session switches A to B to A with the same case list", async () => {
  const oldRequest = Promise.withResolvers<unknown>();
  const newRequest = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(oldRequest.promise).mockReturnValueOnce(newRequest.promise);
  const draft = emptyDraft();
  const originalSession = mock.session;
  const view = render(<ContractReferencesPanel contractID="same-contract" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("combobox"));
  await user.click(screen.getByRole("option", { name: /BUS-1.*case-full-1/ }));
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(button);
  mock.session = { ...originalSession, data: { ...originalSession.data, id: "actor-b", display_name: "只读成员乙", tenant_id: "tenant-b", principal_id: "principal-b", csrf_token: "b".repeat(32),
    active_context: { ...originalSession.data.active_context, membership_id: "membership-b", tenant_id: "tenant-b", tenant_name: "租户乙", context_version: "2" } } };
  view.rerender(<ContractReferencesPanel contractID="same-contract" draft={draft} />);
  mock.session = originalSession;
  view.rerender(<ContractReferencesPanel contractID="same-contract" draft={draft} />);
  await user.click(button);
  await act(async () => oldRequest.resolve({ source_case_id: record.id, source: "draft", checked_at: "身份A旧结果", subjects: [], materials: [], warnings: [] }));
  expect(screen.queryByText(/身份A旧结果/)).not.toBeInTheDocument();
  expect(button).toHaveClass("ant-btn-loading");
  await act(async () => newRequest.resolve({ source_case_id: record.id, source: "draft", checked_at: "身份A重新核对结果", subjects: [], materials: [], warnings: [] }));
  expect(screen.getByText(/身份A重新核对结果/)).toBeVisible();
});

it.each(["unknown", "revalidating"])("hides and clears results while the session is %s", async (state) => {
  mock.check.mockResolvedValue({ source_case_id: record.id, source: "revision", checked_at: "重验前结果", subjects: [], materials: [], warnings: [] });
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="imported-contract" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(/重验前结果/)).toBeVisible();
  mock.session = { ...mock.session, isSuccess: state !== "unknown", isFetching: state === "revalidating" };
  view.rerender(<ContractReferencesPanel contractID="imported-contract" draft={draft} />);
  expect(screen.queryByText(/重验前结果/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: /核对引用/ })).toBeDisabled();
  expect(screen.getByRole("combobox")).toBeDisabled();
  mock.session = { ...mock.session, isSuccess: true, isFetching: false };
  view.rerender(<ContractReferencesPanel contractID="imported-contract" draft={draft} />);
  expect(screen.queryByText(/重验前结果/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: /核对引用/ })).toBeEnabled();
});

it("ignores a pending result after unmount", async () => {
  const delayed = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(delayed.promise);
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={emptyDraft()} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  view.unmount();
  await act(async () => delayed.resolve({ source_case_id: record.id, source: "draft", checked_at: "卸载前结果", subjects: [], materials: [], warnings: [] }));
  expect(screen.queryByText(/卸载前结果/)).not.toBeInTheDocument();
});
