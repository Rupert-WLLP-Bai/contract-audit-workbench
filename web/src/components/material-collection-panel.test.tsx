import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CaseDetail, DocumentVersion, MaterialProjection, MaterialVersionProjection } from "../api/types";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { MaterialCollectionPanel } from "./material-collection-panel";

const mocks = vi.hoisted(() => ({
  useUploadDocument: vi.fn(),
  useReparse: vi.fn(),
  useConfirmLegacyMaterialSelection: vi.fn(),
  idempotencyCounter: 0,
}));

vi.mock("../api/queries", () => ({
  useUploadDocument: mocks.useUploadDocument,
  useReparse: mocks.useReparse,
  useConfirmLegacyMaterialSelection: mocks.useConfirmLegacyMaterialSelection,
}));

vi.mock("../api/idempotency", () => ({
  fileSha256: (file: File) => new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(`content:${String(reader.result)}`);
    reader.onerror = () => reject(reader.error);
    reader.readAsText(file);
  }),
  idempotencyFingerprint: async (value: string) => {
    let hash = 2166136261;
    for (const char of value) hash = Math.imul(hash ^ char.charCodeAt(0), 16777619);
    return `fingerprint-${(hash >>> 0).toString(16)}`;
  },
  newIdempotencyKey: () => `test-key-${++mocks.idempotencyCounter}`,
}));

const at = "2026-10-31T00:00:00Z";

function doc(id: string, filename: string, role: string): DocumentVersion {
  return {
    id,
    filename,
    role,
    media_type: "text/plain",
    digest: `digest-${id}`,
    size: 10,
    parse_status: "ready",
    parse_error: null,
    created_at: at,
    parsed_document: null,
  };
}

const emptyDetail: CaseDetail = {
  id: "case-1",
  title: "专线采购",
  contract_type: "service",
  created_at: at,
  updated_at: at,
  documents: [],
  members: [],
  latest_run: null,
  finding_counts: null,
  activity: [],
  business_line: "ENT",
  scenario: "telecom_dedicated_line",
  business_status: "active",
  generations: [],
  remediations: [],
  reports: [],
  archives: [],
  archived_at: null,
  materials: [],
  material_versions: [],
  material_selection: {
    version: 3,
    status: "not_applicable",
    document_ids: [],
      candidate_document_ids: [],
    primary_material_id: null,
  },
};

function material(overrides: Partial<MaterialProjection> & Pick<MaterialProjection, "material_id" | "role" | "purpose" | "current_document_id">): MaterialProjection {
  return {
    legacy_status: "native",
    legacy_selected: false,
    selected: true,
    current_version: 1,
    ...overrides,
  };
}

function state(overrides: Record<string, unknown> = {}) {
  return { mutate: vi.fn(), isPending: false, isError: false, error: null, data: undefined, ...overrides };
}

function renderPanel(caseDetail = emptyDetail) {
  return render(<MaterialCollectionPanel caseDetail={caseDetail} canWrite actorId="actor-1" />);
}

beforeEach(() => {
  sessionStorage.clear();
  mocks.idempotencyCounter = 0;
  mocks.useUploadDocument.mockReturnValue(state());
  mocks.useReparse.mockReturnValue(state());
  mocks.useConfirmLegacyMaterialSelection.mockReturnValue(state());
});

describe("material collection behavior", () => {
  it("keeps upload success copy usable without exposing random identities", () => {
    mocks.useUploadDocument.mockReturnValue(state({ data: { id: "doc-random", material_id: "mat-random", version: 2, replayed: true } }));
    renderPanel();
    const status = screen.getByText("此前提交已成功，当前第 2 版。");
    expect(status).toHaveAttribute("data-document-id", "doc-random");
    expect(status).toHaveAttribute("data-material-id", "mat-random");
    expect(status).not.toHaveTextContent("doc-random");
    expect(status).not.toHaveTextContent("mat-random");
    expect(status).not.toHaveTextContent("重放");
  });

  it("adds an attachment by default with its explicit purpose and an idempotency key", async () => {
    const mutate = vi.fn();
    mocks.useUploadDocument.mockReturnValue(state({ mutate }));
    const user = userEvent.setup();
    renderPanel();
    await chooseOption(user, screen.getByRole("combobox", { name: "材料角色" }), "附件");
    await user.upload(screen.getByLabelText("文件"), new File(["正文"], "技术附件.txt", { type: "text/plain" }));
    await user.click(screen.getByRole("button", { name: "新增材料" }));
    expect(mutate).toHaveBeenCalledTimes(1);
    const input = mutate.mock.calls[0]![0];
    expect(input).toMatchObject({ role: "attachment", purpose: "supplement", intent: "add" });
    expect(input.idempotency_key).toBe("test-key-1");
    expect(input).not.toHaveProperty("material_id");
    expect(input).not.toHaveProperty("replaces_document_id");
    const options = mutate.mock.calls[0]![1] as { onSuccess: () => void };
    await act(async () => options.onSuccess());
    await waitFor(() => expect(screen.getByLabelText("文件")).not.toBeDisabled());
    await user.upload(screen.getByLabelText("文件"), new File(["正文"], "技术附件.txt", { type: "text/plain" }));
    await user.click(screen.getByRole("button", { name: "新增材料" }));
    expect(mutate.mock.calls[1]![0].idempotency_key).not.toBe(input.idempotency_key);
  });

  it("shows a primary-contract route and sends the selected current document for explicit replacement", async () => {
    const mutate = vi.fn();
    mocks.useUploadDocument.mockReturnValue(state({ mutate }));
    const primary = material({ material_id: "mat-primary", role: "draft_contract", purpose: "primary_contract", current_document_id: "doc-primary", current_version: 4 });
    const detail: CaseDetail = {
      ...emptyDetail,
      documents: [doc("doc-primary", "当前合同.txt", "draft_contract"), doc("doc-v3", "初始合同.txt", "draft_contract")],
      materials: [primary],
      material_versions: [
        { material_id: "mat-primary", version: 3, document_id: "doc-v3", previous_document_id: null, created_at: at },
        { material_id: "mat-primary", version: 4, document_id: "doc-primary", previous_document_id: "doc-v3", created_at: at },
      ],
      material_selection: { version: 4, status: "confirmed", document_ids: ["doc-primary"], primary_material_id: "mat-primary" },
    };
    const user = userEvent.setup();
    renderPanel(detail);
    await chooseOption(user, screen.getByRole("combobox", { name: "材料角色" }), "拟签合同");
    expect(screen.getByText(/已有主合同/)).toBeInTheDocument();
    await chooseOption(user, screen.getByRole("combobox", { name: "材料操作" }), "替换选中的材料");
    expect(screen.getByText(/将替换“当前合同.txt”（主合同）当前第 4 版；若版本已更新，请刷新重试/)).toBeInTheDocument();
    expect(screen.getByText(/替换自“初始合同.txt”第 3 版/)).toBeInTheDocument();
    expect(screen.queryByText("doc-v3")).not.toBeInTheDocument();
    await user.upload(screen.getByLabelText("文件"), new File(["新正文"], "修订合同.txt", { type: "text/plain" }));
    await user.click(screen.getByRole("button", { name: "替换材料" }));
    expect(mutate.mock.calls[0]![0]).toMatchObject({
      intent: "replace",
      material_id: "mat-primary",
      replaces_document_id: "doc-primary",
    });
  });

  it("only offers the server-designated primary chain for replacement", async () => {
    const chain = material({ material_id: "mat-chain", role: "draft_contract", purpose: "primary_contract", current_document_id: "doc-chain", current_version: 2 });
    const unrelated = material({ material_id: "mat-unrelated", role: "draft_contract", purpose: "primary_contract", current_document_id: "doc-unrelated", current_version: 7, legacy_status: "confirmed", legacy_selected: true, selected: false });
    const detail: CaseDetail = {
      ...emptyDetail,
      documents: [doc("doc-chain", "主链合同.txt", "draft_contract"), doc("doc-unrelated", "其他旧合同.txt", "draft_contract")],
      materials: [chain, unrelated],
      material_versions: [
        { material_id: "mat-chain", version: 2, document_id: "doc-chain", previous_document_id: null, created_at: at },
        { material_id: "mat-unrelated", version: 7, document_id: "doc-unrelated", previous_document_id: null, created_at: at },
      ],
      material_selection: { version: 7, status: "confirmed", document_ids: ["doc-chain"], primary_material_id: "mat-chain" },
    };
    const user = userEvent.setup();
    renderPanel(detail);
    await chooseOption(user, screen.getByRole("combobox", { name: "材料角色" }), "拟签合同");
    await chooseOption(user, screen.getByRole("combobox", { name: "材料操作" }), "替换选中的材料");
    const target = screen.getByRole("combobox", { name: "要替换的材料" });
    const selectedLabel = target.closest(".ant-select")?.querySelector(".ant-select-content")?.textContent?.trim();
    expect(selectedLabel).toContain("主链合同.txt，v2，本系统材料");
    await user.click(target);
    const options = screen.getAllByRole("option").map((item) => item.textContent?.trim());
    expect(options).toContain("主链合同.txt，v2，本系统材料");
    expect(options).not.toContain("其他旧合同.txt，v7，本系统材料");
    expect(screen.getByText("已确认旧材料；未选入本次审查")).toBeInTheDocument();
    expect(screen.getByText("其他旧合同.txt · 主合同版本历史")).toBeInTheDocument();
  });

  it("requires an explicit legacy selection and preserves same-purpose materials as separate entities", async () => {
    const mutate = vi.fn();
    mocks.useConfirmLegacyMaterialSelection.mockReturnValue(state({ mutate }));
    const materials = [
      material({ material_id: "mat-a", role: "attachment", purpose: "supplement", current_document_id: "doc-a", legacy_status: "pending_confirmation", legacy_selected: true, selected: false }),
      material({ material_id: "mat-b", role: "attachment", purpose: "supplement", current_document_id: "doc-b", legacy_status: "native", legacy_selected: false, selected: false }),
    ];
    const detail: CaseDetail = {
      ...emptyDetail,
      documents: [doc("doc-a", "补充A.txt", "attachment"), doc("doc-b", "补充B.txt", "attachment")],
      materials,
      material_versions: materials.map((item): MaterialVersionProjection => ({ material_id: item.material_id, version: 1, document_id: item.current_document_id, previous_document_id: null, created_at: at })),
      material_selection: { version: 9, status: "pending_confirmation", document_ids: [], candidate_document_ids: ["doc-a", "doc-b"], primary_material_id: null },
    };
    const user = userEvent.setup();
    renderPanel(detail);
    expect(screen.getAllByText(/补充协议/).length).toBeGreaterThan(1);
    expect(screen.getAllByText(/本系统材料/).length).toBeGreaterThan(0);
    await user.click(screen.getByRole("button", { name: "确认本次材料集合" }));
    expect(mutate).toHaveBeenCalledWith(
      { version: 9, document_ids: ["doc-a", "doc-b"] },
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
    expect(screen.getAllByText(/待确认/).length).toBeGreaterThan(0);
  });

  it("does not mark unrelated legacy materials as superseded by a role-level latest document", () => {
    const detail: CaseDetail = {
      ...emptyDetail,
      documents: [doc("doc-a", "同用途A.txt", "attachment"), doc("doc-b", "同用途B.txt", "attachment")],
      materials: [
        material({ material_id: "mat-a", role: "attachment", purpose: "supplement", current_document_id: "doc-a", legacy_status: "pending_confirmation", selected: false }),
        material({ material_id: "mat-b", role: "attachment", purpose: "supplement", current_document_id: "doc-b", legacy_status: "pending_confirmation", selected: false }),
      ],
      material_versions: [],
      material_selection: { version: 1, status: "pending_confirmation", document_ids: [], candidate_document_ids: [], primary_material_id: null },
    };
    renderPanel(detail);
    expect(screen.getByText("同用途A.txt")).toBeInTheDocument();
    expect(screen.getByText("同用途B.txt")).toBeInTheDocument();
    expect(screen.queryByText(/历史版本/)).not.toBeInTheDocument();
  });

  it("keeps a retry key after a failed request and separates same-metadata files by content", async () => {
    const mutate = vi.fn();
    mocks.useUploadDocument.mockReturnValue(state({ mutate }));
    const user = userEvent.setup();
    const first = new File(["aaaa"], "同名.txt", { type: "text/plain", lastModified: 10 });
    const second = new File(["bbbb"], "同名.txt", { type: "text/plain", lastModified: 10 });
    const firstView = renderPanel();
    await user.upload(screen.getByLabelText("文件"), first);
    await user.click(screen.getByRole("button", { name: "新增材料" }));
    const firstKey = mutate.mock.calls[0]![0].idempotency_key;
    firstView.unmount();

    const retryView = renderPanel();
    await user.upload(screen.getByLabelText("文件"), first);
    await user.click(screen.getByRole("button", { name: "新增材料" }));
    expect(mutate.mock.calls[1]![0].idempotency_key).toBe(firstKey);
    retryView.unmount();

    renderPanel();
    await user.upload(screen.getByLabelText("文件"), second);
    await user.click(screen.getByRole("button", { name: "新增材料" }));
    expect(mutate.mock.calls[2]![0].idempotency_key).not.toBe(firstKey);
  });

  it("uses server pending candidates and resets checklist state when the case changes at the same version", async () => {
    const firstDetail: CaseDetail = {
      ...emptyDetail,
      documents: [doc("doc-a", "案件A材料.txt", "attachment")],
      materials: [material({ material_id: "mat-a", role: "attachment", purpose: "supplement", current_document_id: "doc-a", legacy_status: "pending_confirmation", selected: false })],
      material_versions: [],
      material_selection: { version: 5, status: "pending_confirmation", document_ids: [], candidate_document_ids: ["doc-a"], primary_material_id: null },
    };
    const secondDetail: CaseDetail = {
      ...emptyDetail,
      id: "case-2",
      documents: [doc("doc-b", "案件B材料.txt", "attachment")],
      materials: [material({ material_id: "mat-b", role: "attachment", purpose: "supplement", current_document_id: "doc-b", legacy_status: "native", legacy_selected: false, selected: false })],
      material_versions: [],
      material_selection: { version: 5, status: "pending_confirmation", document_ids: [], candidate_document_ids: ["doc-b"], primary_material_id: null },
    };
    const view = render(<MaterialCollectionPanel key="case-1:actor-1" caseDetail={firstDetail} canWrite actorId="actor-1" />);
    expect(screen.getByRole("checkbox", { name: /案件A材料/ })).toBeChecked();
    await userEvent.setup().click(screen.getByRole("checkbox", { name: /案件A材料/ }));
    view.rerender(<MaterialCollectionPanel key="case-2:actor-1" caseDetail={secondDetail} canWrite actorId="actor-1" />);
    expect(screen.getByRole("checkbox", { name: /案件B材料/ })).toBeChecked();
    expect(screen.queryByText("案件A材料.txt")).not.toBeInTheDocument();
  });

  it("does not allow legacy confirmation when the service omits its candidate projection", () => {
    const detail: CaseDetail = {
      ...emptyDetail,
      documents: [doc("doc-legacy", "旧材料.txt", "attachment")],
      materials: [material({ material_id: "mat-legacy", role: "attachment", purpose: "supplement", current_document_id: "doc-legacy", legacy_status: "pending_confirmation", legacy_selected: true, selected: false })],
      material_versions: [],
      material_selection: { version: 2, status: "pending_confirmation", document_ids: [], primary_material_id: null },
    };
    renderPanel(detail);
    expect(screen.getByText("材料信息暂不可用，请刷新重试或联系管理员。")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: /旧材料/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: "确认本次材料集合" })).toBeDisabled();
  });
});
