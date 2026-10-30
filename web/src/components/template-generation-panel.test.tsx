import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DocumentVersion, GenerationRecord, MaterialProjection, MaterialSelectionProjection, Template } from "../api/types";
import { chooseOption, renderWithAntd as render } from "../test/antd";
import { TemplateGenerationPanel } from "./template-generation-panel";

const mocks = vi.hoisted(() => ({
  useTemplates: vi.fn(),
  useCreateGeneration: vi.fn(),
}));

vi.mock("../api/queries", () => ({
  useTemplates: mocks.useTemplates,
  useCreateGeneration: mocks.useCreateGeneration,
}));

const at = "2026-10-21T06:00:00Z";

const template: Template = {
  id: "template-1",
  name: "采购合同模板",
  version: "3",
  digest: "template-digest",
  fields: [
    { key: "title", label: "标题", required: true, type: "text" },
    { key: "summary", label: "摘要", required: false, type: "textarea" },
    { key: "amount", label: "金额", required: false, type: "number" },
    { key: "count", label: "数量", required: false, type: "integer" },
    { key: "ratio", label: "比例", required: false, type: "decimal" },
    { key: "discount", label: "折扣", required: false, type: "percent" },
    { key: "fee", label: "费用", required: false, type: "currency" },
    { key: "signed", label: "签署日", required: false, type: "date" },
    { key: "note", label: "备注", required: false, type: "multiline" },
  ],
};

const record: GenerationRecord = {
  id: "generation-1",
  document_id: "document-1",
  template_id: "template-1",
  template_version: "3",
  digest: "generation-digest",
  created_at: at,
};

function templatesState(overrides: Record<string, unknown> = {}) {
  return {
    data: undefined,
    isLoading: false,
    isError: false,
    isSuccess: false,
    error: null,
    refetch: vi.fn(),
    ...overrides,
  };
}

function generationState(overrides: Record<string, unknown> = {}) {
  return {
    mutate: vi.fn(),
    isPending: false,
    error: null,
    data: undefined,
    ...overrides,
  };
}

function renderPanel(
  overrides: Partial<{
    records: GenerationRecord[];
    canWrite: boolean;
    archived: boolean;
    materials: MaterialProjection[];
    documents: DocumentVersion[];
    materialSelection: MaterialSelectionProjection;
  }> = {},
) {
  return render(
    <TemplateGenerationPanel
      caseId="case-1"
      actorId="actor-1"
      records={overrides.records ?? []}
      materials={overrides.materials ?? []}
      documents={overrides.documents ?? []}
      materialSelection={overrides.materialSelection ?? { version: 1, status: "not_applicable", document_ids: [], primary_material_id: null }}
      canWrite={overrides.canWrite ?? true}
      archived={overrides.archived ?? false}
    />,
  );
}

beforeEach(() => {
  mocks.useTemplates.mockReturnValue(
    templatesState({ isSuccess: true, data: [template] }),
  );
  mocks.useCreateGeneration.mockReturnValue(generationState());
});

describe("template generation panel", () => {
  it("explains a missing write permission without loading templates", () => {
    renderPanel({ canWrite: false });
    expect(
      screen.getByText("当前角色没有已定义的模板写入权限。"),
    ).toBeInTheDocument();
    expect(mocks.useTemplates).toHaveBeenCalledWith(false);
    expect(screen.queryByRole("button", { name: "生成待审草稿" })).not.toBeInTheDocument();
    expect(screen.getByText("没有已返回的模板生成记录。")).toBeInTheDocument();
  });

  it("explains archive write refusals and does not load templates", () => {
    renderPanel({ archived: true });
    expect(
      screen.getByText("案件已归档，模板生成写入会被服务端拒绝（HTTP 409）。"),
    ).toBeInTheDocument();
    expect(mocks.useTemplates).toHaveBeenCalledWith(false);
    expect(screen.queryByRole("form")).not.toBeInTheDocument();
  });

  it("shows template loading and error states with an explicit retry", async () => {
    const refetch = vi.fn();
    mocks.useTemplates.mockReturnValue(
      templatesState({ isLoading: true, refetch }),
    );
    const view = renderPanel();
    expect(screen.getByRole("status")).toHaveTextContent("正在读取可用模板。");
    mocks.useTemplates.mockReturnValue(
      templatesState({ isError: true, error: new Error("boom"), refetch }),
    );
    view.rerender(
      <TemplateGenerationPanel caseId="case-1" actorId="actor-1" records={[]} materials={[]} documents={[]} canWrite archived={false} />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("请求没有完成");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "重试读取模板" }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it("says when a successful read returned no templates", () => {
    mocks.useTemplates.mockReturnValue(
      templatesState({ isSuccess: true, data: [] }),
    );
    renderPanel();
    expect(screen.getByText("服务没有返回可用模板。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "生成待审草稿" })).not.toBeInTheDocument();
  });

  it("renders the selected template identity and a control per field type", () => {
    renderPanel();
    expect(screen.getByText(/模板 ID：template-1，版本：3/)).toBeInTheDocument();
    expect(screen.getByText("template-digest")).toBeInTheDocument();
    const title = screen.getByRole("textbox", { name: /标题（必填）/ }) as HTMLInputElement;
    expect(title).toHaveAttribute("type", "text");
    expect(screen.getByRole("textbox", { name: "摘要" }).tagName).toBe("TEXTAREA");
    expect(screen.getByRole("spinbutton", { name: "金额" })).toHaveAttribute("step", "any");
    expect(screen.getByRole("spinbutton", { name: "数量" })).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: "比例" })).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: "折扣" })).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: "费用" })).toBeInTheDocument();
    expect(screen.getByLabelText("签署日")).toHaveAttribute("type", "date");
    expect(screen.getByRole("textbox", { name: "备注" }).tagName).toBe("TEXTAREA");
    expect(screen.getByText("字段：title，类型：text")).toBeInTheDocument();
    expect(screen.getByText("字段：note，类型：multiline")).toBeInTheDocument();
  });

  it("lists every available template as a selectable option", async () => {
    const second: Template = {
      id: "template-2",
      name: "服务合同模板",
      version: "1",
      digest: "second-digest",
      fields: [],
    };
    mocks.useTemplates.mockReturnValue(
      templatesState({ isSuccess: true, data: [template, second] }),
    );
    const user = userEvent.setup();
    renderPanel();
    await user.click(screen.getByRole("combobox", { name: "模板" }));
    const options = await screen.findAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual([
      "采购合同模板，3",
      "服务合同模板，1",
    ]);
  });

  it("submits typed values for every template field and clears them on success", async () => {
    const mutate = vi.fn();
    mocks.useCreateGeneration.mockReturnValue(generationState({ mutate }));
    const user = userEvent.setup();
    renderPanel();
    await user.type(screen.getByRole("textbox", { name: /标题（必填）/ }), "合同");
    await user.type(screen.getByRole("textbox", { name: "摘要" }), "说明");
    await user.click(screen.getByRole("button", { name: "生成待审草稿" }));
    expect(mutate).toHaveBeenCalledTimes(1);
    const [input, options] = mutate.mock.calls[0]!;
    expect(input).toMatchObject({
      input: {
        template_id: "template-1",
        intent: "add",
        fields: {
        title: "合同",
        summary: "说明",
        amount: "",
        count: "",
        ratio: "",
        discount: "",
        fee: "",
        signed: "",
        note: "",
        },
      },
    });
    act(() => (options as { onSuccess: () => void }).onSuccess());
    await waitFor(() =>
      expect(screen.getByRole("textbox", { name: /标题（必填）/ })).toHaveValue(""),
    );
  });

  it("switches templates and drops values belonging to the previous one", async () => {
    const second: Template = {
      id: "template-2",
      name: "服务合同模板",
      version: "1",
      digest: "second-digest",
      fields: [{ key: "title", label: "标题", required: true, type: "text" }],
    };
    mocks.useTemplates.mockReturnValue(
      templatesState({ isSuccess: true, data: [template, second] }),
    );
    const user = userEvent.setup();
    renderPanel();
    await user.type(screen.getByRole("textbox", { name: /标题（必填）/ }), "草稿");
    await chooseOption(user, screen.getByRole("combobox", { name: "模板" }), /服务合同模板，1/);
    expect(screen.getByText(/模板 ID：template-2，版本：1/)).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: /标题（必填）/ })).toHaveValue("");
  });

  it("shows pending, error and generated-document states", async () => {
    mocks.useCreateGeneration.mockReturnValue(
      generationState({ isPending: true }),
    );
    const view = renderPanel();
    expect(screen.getByRole("button", { name: "正在生成草稿" })).toBeDisabled();
    mocks.useCreateGeneration.mockReturnValue(
      generationState({ error: new Error("boom") }),
    );
    view.rerender(
      <TemplateGenerationPanel caseId="case-1" actorId="actor-1" records={[]} materials={[]} documents={[]} canWrite archived={false} />,
    );
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("请求没有完成"),
    );
    expect(screen.getByRole("alert")).toHaveAttribute("tabindex", "-1");
    expect(screen.getByRole("alert")).toHaveFocus();
    mocks.useCreateGeneration.mockReturnValue(
      generationState({
        data: {
          id: "generation-2",
          document_id: "document-9",
          template_id: "template-1",
          template_version: "3",
          material_id: "mat-primary",
          version: 2,
          digest: "result-digest",
          created_at: at,
        },
      }),
    );
    view.rerender(
      <TemplateGenerationPanel caseId="case-1" actorId="actor-1" records={[]} materials={[]} documents={[]} canWrite archived={false} />,
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      /草稿已生成：采购合同模板（3），当前第 2 版。\s*摘要 result-digest/,
    );
    expect(screen.getByRole("status")).toHaveAttribute("data-document-id", "document-9");
    expect(screen.getByRole("status")).toHaveAttribute("data-material-id", "mat-primary");
    expect(screen.getByRole("status")).not.toHaveTextContent("document-9");
    expect(screen.getByRole("status")).not.toHaveTextContent("mat-primary");
    expect(screen.getByRole("status")).not.toHaveTextContent("重放");
  });

  it("requires an explicit target and sends the current document CAS for generated replacement", async () => {
    const mutate = vi.fn();
    mocks.useCreateGeneration.mockReturnValue(generationState({ mutate }));
    const materials: MaterialProjection[] = [{
      material_id: "mat-primary",
      role: "draft_contract",
      purpose: "primary_contract",
      legacy_status: "confirmed",
      legacy_selected: true,
      selected: true,
      current_document_id: "doc-current",
      current_version: 3,
    }];
    const documents: DocumentVersion[] = [{
      id: "doc-current", role: "draft_contract", filename: "主合同.txt", media_type: "text/plain", digest: "digest", size: 8,
      parse_status: "ready", parse_error: null, created_at: at, parsed_document: null,
    }];
    const user = userEvent.setup();
    renderPanel({ materials, documents, materialSelection: { version: 3, status: "confirmed", document_ids: ["doc-current"], primary_material_id: "mat-primary" } });
    await user.type(screen.getByRole("textbox", { name: /标题（必填）/ }), "新文本");
    await chooseOption(user, screen.getByRole("combobox", { name: "草稿操作" }), "明确替换已有草稿材料");
    await user.click(screen.getByRole("button", { name: "生成待审草稿" }));
    expect(mutate).toHaveBeenCalledTimes(1);
    const command = mutate.mock.calls[0]![0];
    expect(command.input).toMatchObject({ intent: "replace", material_id: "mat-primary", replaces_document_id: "doc-current" });
    expect(command.idempotencyKey).toMatch(/^[0-9a-f-]{32,36}$/i);
  });

  it("only offers the server-designated primary chain for generated replacement", async () => {
    const materials: MaterialProjection[] = [
      { material_id: "mat-chain", role: "draft_contract", purpose: "primary_contract", legacy_status: "native", legacy_selected: false, selected: true, current_document_id: "doc-chain", current_version: 2 },
      { material_id: "mat-unrelated", role: "draft_contract", purpose: "primary_contract", legacy_status: "confirmed", legacy_selected: true, selected: false, current_document_id: "doc-unrelated", current_version: 7 },
    ];
    const documents: DocumentVersion[] = [
      { id: "doc-chain", role: "draft_contract", filename: "主链合同.txt", media_type: "text/plain", digest: "a", size: 8, parse_status: "ready", parse_error: null, created_at: at, parsed_document: null },
      { id: "doc-unrelated", role: "draft_contract", filename: "其他旧合同.txt", media_type: "text/plain", digest: "b", size: 8, parse_status: "ready", parse_error: null, created_at: at, parsed_document: null },
    ];
    const user = userEvent.setup();
    renderPanel({
      materials,
      documents,
      materialSelection: { version: 8, status: "confirmed", document_ids: ["doc-chain"], primary_material_id: "mat-chain" },
    });
    await user.type(screen.getByRole("textbox", { name: /标题（必填）/ }), "新文本");
    await chooseOption(user, screen.getByRole("combobox", { name: "草稿操作" }), "明确替换已有草稿材料");
    const target = screen.getByRole("combobox", { name: "要替换的主合同材料" });
    const selectedLabel = target.closest(".ant-select")?.querySelector(".ant-select-content")?.textContent?.trim();
    expect(selectedLabel).toContain("主链合同.txt，v2");
    await user.click(target);
    const options = screen.getAllByRole("option").map((item) => item.textContent?.trim());
    expect(options).toContain("主链合同.txt，v2");
    expect(options).not.toContain("其他旧合同.txt，v7");
  });

  it("lists generation history with digests and times", () => {
    renderPanel({ records: [record] });
    const history = screen.getByRole("region", { name: "生成历史" });
    expect(within(history).getByText("template-1，3")).toBeInTheDocument();
    expect(within(history).getByText("generation-1")).toBeInTheDocument();
    expect(within(history).getByText("document-1")).toBeInTheDocument();
    expect(within(history).getByText("generation-digest")).toBeInTheDocument();
    expect(within(history).getByText("生成时间")).toBeInTheDocument();
  });
});
