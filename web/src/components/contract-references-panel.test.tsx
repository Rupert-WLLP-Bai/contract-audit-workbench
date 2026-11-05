// File metadata (manifest mirror):
// Purpose: Verifies losing source-case list access hides the prior selection and result instead of retaining an unauthorized case reference.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-05
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { act, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { onlineManager, QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import type { ReactElement, ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { emptyDraft, parseContractReferenceCheck, type ContractReferenceCheck } from "../api/contracts";
import { ApiError } from "../api/http";
import { ContractReferencesPanel } from "./contract-references-panel";

const mock = vi.hoisted(() => ({
  cases: { isError: false, isSuccess: true, isLoading: false, data: [] } as { isError: boolean; isSuccess: boolean; isLoading: boolean; isFetching?: boolean; dataUpdatedAt?: number; data: unknown[] },
  session: { isError: false, isSuccess: true, isFetching: false, data: { id: "actor-a", display_name: "只读成员甲", role: "viewer", audience: "client", tenant_id: "tenant-a", org_id: "org-a", principal_id: "principal-a", csrf_token: "a".repeat(32), context_switch_enabled: false, active_context: { membership_id: "membership-a", context_version: "1", tenant_id: "tenant-a", tenant_name: "租户甲", org_id: "org-a", org_name: "组织甲" } } },
  check: vi.fn(),
}));
vi.mock("../api/queries", async (importOriginal) => ({ ...await importOriginal<typeof import("../api/queries")>(), useCases: () => mock.cases, useSession: () => mock.session }));
vi.mock("../api/contracts", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/contracts")>(),
  checkContractReferences: mock.check,
}));

const record = { id: "case-full-1", title: "同名来源", contract_type: "service", document_count: 0,
  pending_decisions: 0, latest_run: null, updated_at: "2026-11-05T01:02:03Z", case_number: "BUS-1" };


// Complete synthetic resolver response: file names identify request ownership,
// while checked_at remains an actual timestamp rather than a test marker.
function checkWithFile(marker: string, source: "draft" | "revision" = "draft"): ContractReferenceCheck {
  const frozen = source === "revision";
  const sha = "a".repeat(64);
  return parseContractReferenceCheck({
    contract_id: "0f7d5c1e-4a2b-4c3d-8e9f-0123456789ab",
    source, contract_revision_id: frozen ? "1f7d5c1e-4a2b-4c3d-8e9f-0123456789ab" : null,
    source_case_id: record.id, checked_at: "2026-11-05T01:02:03Z",
    subjects: [{
      party_id: "party-full-1", role: "buyer", name_snapshot: "ACME-Corp",
      binding_id: "binding-full-1", binding_version: 2, case_role: "customer", binding_state: "active",
      subject_version: 3, subject_payload_hash: "b".repeat(64), verification_state: "pending_verification", frozen,
    }],
    materials: [{
      material_id: "material-full-1", material_version_id: "document-full-1", sha256: sha,
      purpose: "supporting", material_version: 3, material_role: "award", filename: `${marker}.txt`,
      media_type: "text/plain", frozen, original_path: "/api/cases/case-full-1/documents/document-full-1/original",
    }],
    procurement_result: {
      system: "system-id-1", result_id: "result-id-1", package_id: "package-id-1", snapshot_ref: "document-full-1",
      material_id: "material-full-1", material_version_id: "document-full-1", material_version: 3,
      material_role: "award", sha256: sha, captured_at: "2026-11-05T01:02:03Z",
    },
    warnings: ["subject_pending_verification"],
  });
}

function render(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["cases"], [record]);
  return rtlRender(ui, { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> });
}

it("invalidates checks across a same-reference authorization refetch, including a batched cycle", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["cases"], [record], { updatedAt: 42 });
  const sameData = client.getQueryData(["cases"]);
  const draft = emptyDraft();
  function ObservedPanel() {
    const result = useQuery({ queryKey: ["cases"], queryFn: async () => [record], enabled: false });
    mock.cases = { isError: result.isError, isSuccess: result.isSuccess, isLoading: result.isLoading,
      isFetching: result.isFetching, dataUpdatedAt: result.dataUpdatedAt, data: result.data ?? [] };
    return <ContractReferencesPanel contractID="contract-A" draft={draft} />;
  }
  mock.check.mockResolvedValueOnce(checkWithFile("重验前结果", "draft"));
  const view = render(<QueryClientProvider client={client}><ObservedPanel /></QueryClientProvider>);
  const user = userEvent.setup();
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(button);
  expect(await screen.findByText(/重验前结果/, { selector: "strong" })).toBeVisible();
  const deferred = Promise.withResolvers<typeof record[]>();
  const now = vi.spyOn(Date, "now").mockReturnValue(42);
  let fetching!: Promise<unknown>;
  act(() => { fetching = client.fetchQuery({ queryKey: ["cases"], queryFn: () => deferred.promise, staleTime: 0 }); });
  await waitFor(() => expect(button).toBeDisabled());
  expect(screen.queryByText(/重验前结果/)).not.toBeInTheDocument();
  await act(async () => { deferred.resolve([{ ...record }]); await fetching; });
  expect(client.getQueryData(["cases"])).toBe(sameData);
  expect(client.getQueryState(["cases"])?.dataUpdatedAt).toBe(42);
  expect(screen.queryByText(/重验前结果/)).not.toBeInTheDocument();
  now.mockRestore();

  const delayedCheck = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(delayedCheck.promise);
  await user.click(button);
  await act(async () => { await client.fetchQuery({ queryKey: ["cases"], queryFn: async () => [{ ...record }], staleTime: 0 }); });
  await act(async () => delayedCheck.resolve(checkWithFile("重验前的迟到结果", "draft")));
  expect(screen.queryByText(/重验前的迟到结果/)).not.toBeInTheDocument();
  await act(async () => { await client.invalidateQueries({ queryKey: ["cases"], refetchType: "none" }); });
  expect(button).toBeDisabled();
  view.unmount();
  client.clear();
});

beforeEach(() => {
  mock.cases = { isError: false, isSuccess: true, isLoading: false, data: [record] };
  mock.session = { isError: false, isSuccess: true, isFetching: false, data: { id: "actor-a", display_name: "只读成员甲", role: "viewer", audience: "client", tenant_id: "tenant-a", org_id: "org-a", principal_id: "principal-a", csrf_token: "a".repeat(32), context_switch_enabled: false, active_context: { membership_id: "membership-a", context_version: "1", tenant_id: "tenant-a", tenant_name: "租户甲", org_id: "org-a", org_name: "组织甲" } } };
  mock.check.mockReset();
});

it("hides a selected source and its previous result when list access is revoked", async () => {
  mock.check.mockResolvedValue(checkWithFile("撤权前结果", "draft"));
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-1" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("combobox"));
  await user.click(screen.getByRole("option", { name: /BUS-1.*case-full-1/ }));
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(button);
  expect(await screen.findByText(/撤权前结果/, { selector: "strong" })).toBeVisible();
  mock.cases = { isError: false, isSuccess: true, isLoading: false, data: [] };
  view.rerender(<ContractReferencesPanel contractID="contract-1" draft={draft} />);
  await waitFor(() => expect(screen.queryByText(/撤权前结果/)).not.toBeInTheDocument());
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
  await act(async () => delayed.resolve(checkWithFile("合同A的旧结果", "draft")));
  expect(screen.queryByText(/合同A的旧结果/)).not.toBeInTheDocument();
});

it("does not show a completed result when the same source is reused by another contract", async () => {
  mock.check.mockResolvedValue(checkWithFile("合同A已完成结果", "draft"));
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(/合同A已完成结果/, { selector: "strong" })).toBeVisible();
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
  await act(async () => newRequest.resolve(checkWithFile("合同B新结果", "revision")));
  expect(screen.getByText(/合同B新结果/, { selector: "strong" })).toBeVisible();
  expect(button).not.toHaveClass("ant-btn-loading");
});

it("hides a completed error on contract switch and allows the new contract to retry", async () => {
  mock.check.mockRejectedValueOnce(new ApiError("合同A无法核对", 403, "forbidden", false, null)).mockResolvedValueOnce(checkWithFile("合同B合法结果", "revision"));
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(/合同A无法核对/)).toBeVisible();
  view.rerender(<ContractReferencesPanel contractID="contract-B" draft={draft} />);
  expect(screen.queryByText(/合同A无法核对/)).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /核对引用/ }));
  expect(await screen.findByText(/合同B合法结果/, { selector: "strong" })).toBeVisible();
  expect(mock.check).toHaveBeenLastCalledWith("contract-B", undefined);
});

it("keeps the newest completed result when an earlier request settles later", async () => {
  const oldRequest = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(oldRequest.promise).mockResolvedValueOnce(checkWithFile("新合同结果", "draft"));
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  const user = userEvent.setup();
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(button);
  view.rerender(<ContractReferencesPanel contractID="contract-B" draft={draft} />);
  await user.click(button);
  expect(await screen.findByText(/新合同结果/, { selector: "strong" })).toBeVisible();
  await act(async () => oldRequest.resolve(checkWithFile("迟到旧合同结果", "draft")));
  expect(screen.getByText(/新合同结果/, { selector: "strong" })).toBeVisible();
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
  await act(async () => oldRequest.resolve(checkWithFile("旧请求结果", "revision")));
  expect(screen.queryByText(/旧请求结果/)).not.toBeInTheDocument();
  expect(button).toHaveClass("ant-btn-loading");
  await act(async () => newRequest.resolve(checkWithFile("新请求结果", "revision")));
  expect(screen.getByText(/新请求结果/, { selector: "strong" })).toBeVisible();
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
  await act(async () => oldRequest.resolve(checkWithFile("撤权前结果", "draft")));
  expect(screen.queryByText(/撤权前结果/)).not.toBeInTheDocument();
  expect(button).toHaveClass("ant-btn-loading");
  await act(async () => newRequest.resolve(checkWithFile("重新授权结果", "draft")));
  expect(screen.getByText(/重新授权结果/, { selector: "strong" })).toBeVisible();
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
  await act(async () => delayed.resolve(checkWithFile("切换前结果", "revision")));
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
  await act(async () => oldRequest.resolve(checkWithFile("身份A旧结果", "draft")));
  expect(screen.queryByText(/身份A旧结果/)).not.toBeInTheDocument();
  expect(button).toHaveClass("ant-btn-loading");
  await act(async () => newRequest.resolve(checkWithFile("身份A重新核对结果", "draft")));
  expect(screen.getByText(/身份A重新核对结果/, { selector: "strong" })).toBeVisible();
});

it.each(["unknown", "revalidating"])("hides and clears results while the session is %s", async (state) => {
  mock.check.mockResolvedValue(checkWithFile("重验前结果", "revision"));
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="imported-contract" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(/重验前结果/, { selector: "strong" })).toBeVisible();
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
  await act(async () => delayed.resolve(checkWithFile("卸载前结果", "draft")));
  expect(screen.queryByText(/卸载前结果/)).not.toBeInTheDocument();
});

it("shows authorized business names and keeps the full frozen source chain copyable", async () => {
  mock.cases = { ...mock.cases, data: [{ ...record, title: "ACME-Case" }] };
  const result = checkWithFile("AWARD.source", "revision");
  mock.check.mockResolvedValueOnce(result);
  render(<ContractReferencesPanel contractID="contract-1" draft={emptyDraft()} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));

  expect((await screen.findByText(/来源案件：ACME-Case/)).closest("p")).toHaveTextContent("案件业务编号：BUS-1 · 冻结版本");
  expect(screen.getByText(/核对时间：/)).toHaveTextContent("2026/11/05 09:02:03（北京时间 UTC+08:00）");
  expect(screen.getByText("ACME-Corp")).toBeVisible();
  expect(screen.getByText("合同角色：采购方")).toBeVisible();
  expect(screen.getByText(/案件角色：客户/)).toHaveTextContent("在办绑定 · 登记核验状态：待核验 · 核验范围未提供");
  expect(screen.getByText("AWARD.source.txt")).toBeVisible();
  expect(screen.getByText(/用途：佐证材料/)).toHaveTextContent("材料第 3 版");
  expect(screen.getByText(/来源材料：AWARD.source.txt/)).toHaveTextContent("中标文件 · 材料第 3 版");
  expect(screen.getByRole("link", { name: "预览/下载" })).toHaveAttribute("href", result.materials[0]!.original_path);
  expect(screen.getByText("提示：存在尚未核验的主体")).toBeVisible();

  const caseDetails = screen.getByText("来源与合同版本技术详情").closest("details")!;
  const subjectDetails = screen.getByText("主体引用技术详情").closest("details")!;
  const materialDetails = screen.getByText("材料引用技术详情").closest("details")!;
  const procurementDetails = screen.getByText("采购来源技术详情").closest("details")!;
  const warningDetails = screen.getByText("提示代码技术详情").closest("details")!;
  for (const details of [caseDetails, subjectDetails, materialDetails, procurementDetails, warningDetails]) {
    expect(details).not.toHaveAttribute("open");
    expect(within(details).getByRole("button", { name: "复制完整值", hidden: true })).not.toBeVisible();
    await user.click(within(details).getByText(/技术详情/));
    expect(within(details).getByRole("button", { name: "复制完整值" })).toBeVisible();
  }
  expect(JSON.parse(caseDetails.querySelector("code")!.textContent!)).toEqual({
    contract_id: result.contract_id, source_case_id: record.id, source: "revision",
    contract_revision_id: result.contract_revision_id, checked_at: result.checked_at,
  });
  expect(JSON.parse(subjectDetails.querySelector("code")!.textContent!)).toEqual(result.subjects[0]);
  expect(JSON.parse(materialDetails.querySelector("code")!.textContent!)).toEqual(result.materials[0]);
  expect(JSON.parse(procurementDetails.querySelector("code")!.textContent!)).toEqual(result.procurement_result);
  expect(warningDetails.querySelector("code")).toHaveTextContent("subject_pending_verification");
  await user.click(within(procurementDetails).getByRole("button", { name: "复制完整值" }));
  expect(JSON.parse(await navigator.clipboard.readText())).toEqual(result.procurement_result);
  expect(within(procurementDetails).getByRole("status")).toHaveTextContent("已复制完整值");
});

it("uses honest fallbacks when the source is absent or its name and number were not recorded", async () => {
  const missingSource = { ...checkWithFile("未列出来源", "revision"), source_case_id: "unlisted-case-full-id" };
  mock.check.mockResolvedValueOnce(missingSource).mockResolvedValueOnce(checkWithFile("缺少名称"));
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-1" draft={draft} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect((await screen.findByText(/名称未在当前授权列表中/)).closest("p")).toHaveTextContent("案件业务编号：未在当前授权列表中 · 冻结版本");
  const details = screen.getByText("来源与合同版本技术详情").closest("details")!;
  expect(details).not.toHaveAttribute("open");
  await user.click(within(details).getByText("来源与合同版本技术详情"));
  expect(JSON.parse(details.querySelector("code")!.textContent!).source_case_id).toBe("unlisted-case-full-id");

  mock.cases = { ...mock.cases, data: [{ ...record, title: "", case_number: "" }] };
  view.rerender(<ContractReferencesPanel contractID="contract-1" draft={draft} />);
  await user.click(screen.getByRole("button", { name: /核对引用/ }));
  expect((await screen.findByText(/来源案件：名称未记录/)).closest("p")).toHaveTextContent("案件业务编号：未登记 · 当前草稿");
});

it.each([
  ["not-a-time", "时间格式异常"],
  ["2026-02-30T01:02:03Z", "时间格式异常"],
  ["", "未记录"],
])("distinguishes a defensive checked_at value %j from a missing value", async (checkedAt, expected) => {
  mock.check.mockResolvedValueOnce({ ...checkWithFile("时间示例"), checked_at: checkedAt });
  render(<ContractReferencesPanel contractID="contract-1" draft={emptyDraft()} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(/核对时间：/)).toHaveTextContent(`核对时间：${expected}`);
  const invalidDetail = screen.queryByText("核对时间原始值");
  if (checkedAt) {
    expect(invalidDetail).toBeInTheDocument();
    await user.click(invalidDetail!);
    const details = invalidDetail!.closest("details")!;
    await user.click(within(details).getByRole("button", { name: "复制完整值" }));
    expect(await navigator.clipboard.readText()).toBe(checkedAt);
  } else {
    expect(invalidDetail).not.toBeInTheDocument();
  }
});

it.each([
  "2026-11-05",
  "2026-11-05T12:34:56",
  "2026-11-05T12:34Z",
  "2026-11-05T12:34:56+0800",
  "2026-11-05T24:00:00Z",
  "2026-11-05 12:34:56Z",
])("rejects incomplete or non-contract checked_at %s and preserves its raw value", async (checkedAt) => {
  mock.check.mockResolvedValueOnce({ ...checkWithFile("异常时间"), checked_at: checkedAt });
  render(<ContractReferencesPanel contractID="contract-1" draft={emptyDraft()} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(/核对时间：/)).toHaveTextContent("核对时间：时间格式异常");
  const raw = screen.getByText("核对时间原始值");
  await user.click(raw);
  const details = raw.closest("details")!;
  expect(details.querySelector("code")).toHaveTextContent(checkedAt);
  await user.click(within(details).getByRole("button", { name: "复制完整值" }));
  expect(await navigator.clipboard.readText()).toBe(checkedAt);
});

it.each([
  "2026-11-05T01:02:03Z",
  "2026-11-05t01:02:03z",
  "2026-11-05T09:02:03.123456789+08:00",
  "2026-11-04T19:32:03-05:30",
])("formats a complete zoned checked_at %s in Beijing time", async (checkedAt) => {
  mock.check.mockResolvedValueOnce({ ...checkWithFile("合法时间"), checked_at: checkedAt });
  render(<ContractReferencesPanel contractID="contract-1" draft={emptyDraft()} />);
  await userEvent.setup().click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(/核对时间：/)).toHaveTextContent("2026/11/05 09:02:03（北京时间 UTC+08:00）");
  expect(screen.queryByText("核对时间原始值")).not.toBeInTheDocument();
});

it("shows contract and case roles, binding states, verification states, and material purposes in their own domains", async () => {
  const result = checkWithFile("基础材料");
  const subject = result.subjects[0]!;
  const material = result.materials[0]!;
  result.subjects = [
    ["buyer", "customer", "active", "verified"],
    ["supplier", "supplier", "active", "pending_verification"],
    ["guarantor", "contractor", "active", "verified"],
    ["consortium_member", "service_provider", "active", "verified"],
    ["other", "other", "active", "verified"],
  ].map(([role, caseRole, bindingState, verificationState], index) => ({
    ...subject, party_id: `party-${index}`, binding_id: `binding-${index}`, name_snapshot: `主体${index}`,
    role: role!, case_role: caseRole!, binding_state: bindingState!, verification_state: verificationState!,
  }));
  result.materials = ["draft", "signed_contract", "supporting", "acceptance", "payment", "other"].map((purpose, index) => ({
    ...material, material_id: `material-${index}`, material_version_id: `document-${index}`,
    filename: `材料${index}.txt`, purpose, original_path: `/api/cases/case-full-1/documents/document-${index}/original`,
  }));
  result.procurement_result = undefined;
  mock.check.mockResolvedValueOnce(parseContractReferenceCheck(result));
  render(<ContractReferencesPanel contractID="contract-1" draft={emptyDraft()} />);
  await userEvent.setup().click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText("合同角色：采购方")).toBeVisible();
  for (const label of ["供应商", "担保方", "联合体成员", "其他角色"]) {
    expect(screen.getByText(`合同角色：${label}`)).toBeVisible();
  }
  for (const label of ["客户", "供应商", "承包方", "服务提供方", "其他角色"]) {
    expect(screen.getByText(new RegExp(`案件角色：${label}`))).toBeVisible();
  }
  expect(screen.getByText(/案件角色：供应商/)).toHaveTextContent("在办绑定 · 登记核验状态：待核验");
  expect(screen.getByText(/案件角色：客户/)).toHaveTextContent("登记核验状态：已核验 · 核验范围未提供");
  for (const label of ["合同草稿", "已签合同", "佐证材料", "验收材料", "付款材料", "其他用途"]) {
    expect(screen.getByText(new RegExp(`用途：${label}`))).toBeVisible();
  }
});

it.each([
  ["pending_confirmation", "待确认绑定"],
  ["inactive", "已停用"],
])("defensively labels a %s binding, which the current resolver normally excludes", async (bindingState, label) => {
  const result = checkWithFile("非活跃绑定");
  result.subjects[0]!.binding_state = bindingState;
  mock.check.mockResolvedValueOnce(parseContractReferenceCheck(result));
  render(<ContractReferencesPanel contractID="contract-1" draft={emptyDraft()} />);
  await userEvent.setup().click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(new RegExp(`案件角色：客户 · ${label}`))).toBeVisible();
});

it("uses neutral fallbacks for defensive unknown enums and preserves their exact codes", async () => {
  // These future or malformed values are display defenses, not permitted server enum fixtures.
  const result = checkWithFile("未知枚举");
  Object.assign(result.subjects[0]!, { role: "future_party", case_role: "future_case", binding_state: "future_binding", verification_state: "future_verification" });
  Object.assign(result.materials[0]!, { purpose: "future_purpose", material_role: "future_material" });
  result.procurement_result = undefined;
  result.warnings = ["future_warning"];
  mock.check.mockResolvedValueOnce(parseContractReferenceCheck(result));
  render(<ContractReferencesPanel contractID="contract-1" draft={emptyDraft()} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText("合同角色：待核对")).toBeVisible();
  expect(screen.getByText(/案件角色：待核对/)).toHaveTextContent("绑定状态待核对 · 登记核验状态：待核对 · 核验范围未提供");
  expect(screen.getByText("材料角色待核对")).toBeVisible();
  expect(screen.getByText(/用途：待核对/)).toBeVisible();
  expect(screen.getByText("提示：待核对")).toBeVisible();
  for (const [label, codes] of [
    ["主体引用技术详情", ["future_party", "future_case", "future_binding", "future_verification"]],
    ["材料引用技术详情", ["future_purpose", "future_material"]],
    ["提示代码技术详情", ["future_warning"]],
  ] as const) {
    const details = screen.getByText(label).closest("details")!;
    await user.click(within(details).getByText(label));
    for (const code of codes) expect(details.querySelector("code")).toHaveTextContent(code);
  }
});

it("distinguishes defensively missing enum values and names from unknown values", async () => {
  const result = checkWithFile("字段缺失");
  Object.assign(result.subjects[0]!, { role: "", case_role: "", binding_state: "", verification_state: "", name_snapshot: "" });
  Object.assign(result.materials[0]!, { purpose: "", material_role: "", filename: "" });
  result.procurement_result = undefined;
  result.warnings = [""];
  mock.check.mockResolvedValueOnce(parseContractReferenceCheck(result));
  render(<ContractReferencesPanel contractID="contract-1" draft={emptyDraft()} />);
  await userEvent.setup().click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText("主体名称未记录")).toBeVisible();
  expect(screen.getByText("合同角色：未记录")).toBeVisible();
  expect(screen.getByText(/案件角色：未记录/)).toHaveTextContent("绑定状态未记录 · 登记核验状态：未记录");
  expect(screen.getByText("材料文件名未记录")).toBeVisible();
  expect(screen.getByText("材料角色未记录")).toBeVisible();
  expect(screen.getByText(/用途：未记录/)).toBeVisible();
  expect(screen.getByText("提示：未记录")).toBeVisible();
});

it.each(["material_id", "material_version_id", "material_version", "sha256", "material_role"] as const)("does not assign a filename when procurement %s does not match the returned material", async (field) => {
  // These inconsistent pins exercise a defensive display boundary; the resolver normally rejects them.
  const result = checkWithFile("不匹配的材料");
  const pin = result.procurement_result!;
  if (field === "material_id") pin.material_id = "different-material-id";
  if (field === "material_version_id") pin.material_version_id = "different-document-id";
  if (field === "material_version") pin.material_version = 4;
  if (field === "sha256") pin.sha256 = "c".repeat(64);
  if (field === "material_role") pin.material_role = "procurement";
  mock.check.mockResolvedValueOnce(parseContractReferenceCheck(result));
  render(<ContractReferencesPanel contractID="contract-1" draft={emptyDraft()} />);
  await userEvent.setup().click(screen.getByRole("button", { name: "核对引用" }));
  expect(await screen.findByText(/来源材料：文件名暂不可用/)).toBeVisible();
  expect(screen.getByText("不匹配的材料.txt")).toBeVisible();
});

it("resolves the source by its exact ID when authorized cases have the same title", async () => {
  const second = { ...record, id: "case-full-2", case_number: "BUS-2" };
  mock.cases = { ...mock.cases, data: [record, second] };
  const result = checkWithFile("同名案件");
  result.source_case_id = second.id;
  result.materials[0]!.original_path = "/api/cases/case-full-2/documents/document-full-1/original";
  mock.check.mockResolvedValueOnce(parseContractReferenceCheck(result));
  render(<ContractReferencesPanel contractID="contract-1" draft={emptyDraft()} />);
  await userEvent.setup().click(screen.getByRole("button", { name: "核对引用" }));
  expect((await screen.findByText("来源案件：同名来源")).closest("p")).toHaveTextContent("案件业务编号：BUS-2");
  expect(mock.check).toHaveBeenCalledTimes(1);
});

it("does not turn a frozen result with no warnings into an approval or authenticity conclusion", async () => {
  const result = checkWithFile("无提示", "revision");
  result.subjects[0]!.verification_state = "verified";
  result.warnings = [];
  mock.check.mockResolvedValueOnce(parseContractReferenceCheck(result));
  render(<ContractReferencesPanel contractID="contract-1" draft={emptyDraft()} />);
  await userEvent.setup().click(screen.getByRole("button", { name: "核对引用" }));
  const subject = await screen.findByText(/登记核验状态：已核验/);
  expect(subject).toHaveTextContent("核验范围未提供 · 冻结引用");
  expect(subject.closest(".contracts-page-reference-result")).not.toHaveTextContent(/批准|审批通过|内容真实|合法有效/);
});

it("discards a source A request after selecting A to B to A and publishes the new request", async () => {
  const second = { ...record, id: "case-full-2", title: "另一来源", case_number: "BUS-2" };
  mock.cases = { ...mock.cases, data: [record, second] };
  const oldRequest = Promise.withResolvers<unknown>();
  const newRequest = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(oldRequest.promise).mockReturnValueOnce(newRequest.promise);
  render(<ContractReferencesPanel contractID="contract-A" draft={emptyDraft()} />);
  const user = userEvent.setup();
  const select = screen.getByRole("combobox");
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(select);
  await user.click(screen.getByRole("option", { name: /BUS-1.*case-full-1/ }));
  await user.click(button);
  await user.click(select);
  await user.click(screen.getByRole("option", { name: /BUS-2.*case-full-2/ }));
  await user.click(select);
  await user.click(screen.getByRole("option", { name: /BUS-1.*case-full-1/ }));
  await user.click(button);
  await act(async () => oldRequest.resolve(checkWithFile("来源A旧结果")));
  expect(screen.queryByText(/来源A旧结果/)).not.toBeInTheDocument();
  expect(button).toHaveClass("ant-btn-loading");
  await act(async () => newRequest.resolve(checkWithFile("来源A新结果")));
  expect(screen.getByText(/来源A新结果/, { selector: "strong" })).toBeVisible();
});

it("discards a pending result while the real query is paused, then accepts a fresh check", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["cases"], [record]);
  const oldRequest = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(oldRequest.promise).mockResolvedValueOnce(checkWithFile("恢复后新结果"));
  const view = render(<QueryClientProvider client={client}><ContractReferencesPanel contractID="contract-A" draft={emptyDraft()} /></QueryClientProvider>);
  const user = userEvent.setup();
  const button = screen.getByRole("button", { name: "核对引用" });
  try {
    await user.click(button);
    act(() => onlineManager.setOnline(false));
    let fetching!: Promise<unknown>;
    act(() => { fetching = client.fetchQuery({ queryKey: ["cases"], queryFn: async () => [record], staleTime: 0 }); });
    await waitFor(() => expect(client.getQueryState(["cases"])?.fetchStatus).toBe("paused"));
    await waitFor(() => expect(button).toBeDisabled());
    await act(async () => oldRequest.resolve(checkWithFile("暂停前旧结果")));
    expect(screen.queryByText(/暂停前旧结果/)).not.toBeInTheDocument();
    await act(async () => { onlineManager.setOnline(true); await fetching; });
    await waitFor(() => expect(button).toBeEnabled());
    await user.click(button);
    expect(await screen.findByText(/恢复后新结果/, { selector: "strong" })).toBeVisible();
    expect(screen.queryByText(/暂停前旧结果/)).not.toBeInTheDocument();
  } finally {
    onlineManager.setOnline(true);
    view.unmount();
    client.clear();
  }
});

it("invalidates a pending result when the real case query is removed and recreated", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["cases"], [record]);
  const oldRequest = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(oldRequest.promise).mockResolvedValueOnce(checkWithFile("重建查询后结果"));
  const view = render(<QueryClientProvider client={client}><ContractReferencesPanel contractID="contract-A" draft={emptyDraft()} /></QueryClientProvider>);
  const user = userEvent.setup();
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(button);
  act(() => client.removeQueries({ queryKey: ["cases"], exact: true }));
  await waitFor(() => expect(button).toBeDisabled());
  await act(async () => oldRequest.resolve(checkWithFile("删除查询前结果")));
  expect(screen.queryByText(/删除查询前结果/)).not.toBeInTheDocument();
  act(() => { client.setQueryData(["cases"], [record]); });
  await waitFor(() => expect(button).toBeEnabled());
  await user.click(button);
  expect(await screen.findByText(/重建查询后结果/, { selector: "strong" })).toBeVisible();
  expect(screen.queryByText(/删除查询前结果/)).not.toBeInTheDocument();
  view.unmount();
  client.clear();
});

it("keeps a new refusal visible when an older request succeeds later and permits a fresh retry", async () => {
  const oldRequest = Promise.withResolvers<unknown>();
  mock.check.mockReturnValueOnce(oldRequest.promise)
    .mockRejectedValueOnce(new ApiError("当前合同无权核对", 403, "forbidden", false, null))
    .mockResolvedValueOnce(checkWithFile("重新核对新结果"));
  const draft = emptyDraft();
  const view = render(<ContractReferencesPanel contractID="contract-A" draft={draft} />);
  const user = userEvent.setup();
  const button = screen.getByRole("button", { name: "核对引用" });
  await user.click(button);
  view.rerender(<ContractReferencesPanel contractID="contract-B" draft={draft} />);
  await user.click(button);
  expect(await screen.findByText("当前合同无权核对")).toBeVisible();
  await act(async () => oldRequest.resolve(checkWithFile("拒绝后的旧结果")));
  expect(screen.queryByText(/拒绝后的旧结果/)).not.toBeInTheDocument();
  expect(screen.getByText("当前合同无权核对")).toBeVisible();
  await user.click(button);
  expect(await screen.findByText(/重新核对新结果/, { selector: "strong" })).toBeVisible();
  expect(screen.queryByText(/拒绝后的旧结果/)).not.toBeInTheDocument();
});
