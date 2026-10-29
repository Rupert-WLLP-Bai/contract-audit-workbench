import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Finding, ReviewDocument } from "../api/types";
import { formatDateTime } from "../domain/review";
import { renderWithAntd as render } from "../test/antd";
import { FindingPanel } from "./finding-panel";

const at = "2026-10-21T06:00:00Z";

const parsedDocument = {
  id: "parsed-1",
  document_id: "doc-1",
  original_digest: "original-1",
  parser_id: "parser",
  parser_version: "v1",
  config_hash: "config-1",
  normalization_version: "norm-1",
  normalized_digest: "norm-digest-1",
  ir_digest: "ir-digest-1",
  artifact_digest: "artifact-digest-1",
  created_at: at,
  status: "ready" as const,
};

const document: ReviewDocument = {
  id: "doc-1",
  role: "draft_contract",
  filename: "合同.txt",
  media_type: "text/plain",
  digest: "document-digest",
  size: 20,
  parse_status: "ready",
  parse_error: null,
  created_at: at,
  parsed_document: parsedDocument,
  parsed_document_id: "parsed-1",
  text: "第一条 付款期限为三十日。",
  clauses: [{ id: "clause-1", heading: "第一条", text_offset: { start: 0, end: 2 }, page: 1 }],
};

const locatedAnchor = {
  id: "anchor-1",
  document_version_id: "doc-1",
  parsed_document_id: "parsed-1",
  normalized_digest: "norm-digest-1",
  quote: "三十日",
  status: "located" as const,
  page: 2,
  text_offset: { start: 9, end: 12 },
  bbox: null,
  mismatch_reason: null,
};

const severitySource = {
  policy_id: "policy-1",
  policy_version: "1",
  clause: "4.2",
  reason: "超过制度上限",
};

function finding(overrides: Partial<Finding> = {}): Finding {
  return {
    id: "finding-1",
    origin: "rule",
    domain: "付款",
    claim: "付款期限过长",
    relation: "conflict",
    confidence: "high",
    verification_status: "verified",
    evidence: [locatedAnchor],
    unverified_reason: null,
    version: 3,
    severity: "high",
    severity_source: severitySource,
    decisions: [],
    ...overrides,
  };
}

const decision = {
  id: "decision-1",
  action: "confirm_risk",
  reason: "已核实",
  actor: "审查员甲",
  decided_at: at,
};

function renderPanel(props: Partial<Parameters<typeof FindingPanel>[0]> = {}) {
  const onDecide = vi.fn().mockResolvedValue(true);
  const onSelect = vi.fn();
  const view = render(
    <FindingPanel
      findings={[finding()]}
      documents={[document]}
      selectedId={null}
      onSelect={onSelect}
      pending={false}
      conflictMessage={null}
      saveError={null}
      onDecide={onDecide}
      {...props}
    />,
  );
  return { ...view, onDecide, onSelect };
}

describe("finding panel", () => {
  it("counts severities and includes an unrated bucket only when needed", () => {
    const findings = [
      finding({ id: "f1", severity: "high" }),
      finding({ id: "f2", severity: "medium" }),
      finding({ id: "f3", severity: "low" }),
      finding({ id: "f4", severity: "info" }),
    ];
    const view = renderPanel({ findings });
    const counts = screen.getByLabelText("本次审查风险分级");
    expect(within(counts).getByText("高风险").parentElement).toHaveTextContent("高风险1");
    expect(within(counts).getByText("中风险").parentElement).toHaveTextContent("中风险1");
    expect(within(counts).getByText("低风险").parentElement).toHaveTextContent("低风险1");
    expect(within(counts).getByText("提示").parentElement).toHaveTextContent("提示1");
    expect(within(counts).queryByText("未分级")).not.toBeInTheDocument();
    view.rerender(
      <FindingPanel
        findings={[...findings, finding({ id: "f5", severity: null })]}
        documents={[document]}
        selectedId={null}
        onSelect={vi.fn()}
        pending={false}
        conflictMessage={null}
        saveError={null}
        onDecide={vi.fn()}
      />,
    );
    expect(
      within(screen.getByLabelText("本次审查风险分级")).getByText("未分级")
        .parentElement,
    ).toHaveTextContent("未分级1");
  });

  it("filters findings and keeps the selected one visible in its detail", async () => {
    const user = userEvent.setup();
    const { onSelect } = renderPanel({
      findings: [
        finding({ id: "conflict", claim: "冲突线索", relation: "conflict" }),
        finding({ id: "insufficient", claim: "缺证线索", relation: "insufficient" }),
        finding({ id: "decided", claim: "已决线索", decisions: [decision] }),
      ],
    });
    expect(screen.getByRole("button", { name: "全部" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await user.click(screen.getByRole("button", { name: "冲突" }));
    expect(screen.getByText("冲突线索")).toBeInTheDocument();
    expect(screen.getByText("已决线索")).toBeInTheDocument();
    expect(screen.queryByText("缺证线索")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "信息不足" }));
    expect(screen.getByText("缺证线索")).toBeInTheDocument();
    expect(screen.queryByText("冲突线索")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "待裁决" }));
    expect(screen.getByText("冲突线索")).toBeInTheDocument();
    expect(screen.getByText("缺证线索")).toBeInTheDocument();
    expect(screen.queryByText("已决线索")).not.toBeInTheDocument();
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("explains an empty finding list without claiming the contract is clean", () => {
    renderPanel({ findings: [] });
    expect(
      screen.getByText("本次审查还没有生成风险线索。这不代表材料没有风险。"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
  });

  it("labels list rows and keeps the selected finding visible under a filter", async () => {
    const user = userEvent.setup();
    renderPanel({
      findings: [
        finding({ id: "a", claim: "冲突线索", relation: "conflict" }),
        finding({ id: "b", claim: "缺证线索", relation: "insufficient" }),
      ],
      selectedId: "b",
    });
    expect(screen.getByText(/规则，\s*冲突，\s*待裁决/)).toBeInTheDocument();
    const detail = screen.getByRole("article");
    expect(within(detail).getByRole("heading", { name: "缺证线索" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "冲突" }));
    expect(
      screen.getByText("当前线索不在这个筛选结果里，右侧仍保留它。"),
    ).toBeInTheDocument();
  });

  it("selects a finding from the list, clause tab and conclusion tab", async () => {
    const user = userEvent.setup();
    const { onSelect } = renderPanel({
      findings: [finding({ decisions: [decision] })],
    });
    await user.click(screen.getByRole("button", { name: "选择线索：付款期限过长" }));
    expect(onSelect).toHaveBeenCalledWith("finding-1");
    await user.click(screen.getByRole("tab", { name: "条款比对" }));
    expect(
      screen.getByText("各发现引用的实际材料片段；变更本身不代表风险。"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "付款期限过长" }));
    expect(onSelect).toHaveBeenLastCalledWith("finding-1");
    expect(screen.getByRole("tab", { name: "风险审查" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await user.click(screen.getByRole("tab", { name: "审查结论" }));
    expect(
      screen.getByText("规则结果、模型建议和审查判断分别保存。审查执行结束不代表合同已获批。"),
    ).toBeInTheDocument();
    expect(screen.getByText("确认风险")).toBeInTheDocument();
  });

  it("names an unmatched clause source and the contract information tab", async () => {
    const user = userEvent.setup();
    renderPanel({
      findings: [finding({ evidence: [{ ...locatedAnchor, id: "orphan", document_version_id: "missing" }] })],
    });
    await user.click(screen.getByRole("tab", { name: "条款比对" }));
    expect(screen.getByText("来源未对应到材料")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "合同信息" }));
    expect(screen.getByRole("heading", { name: "合同.txt" })).toBeInTheDocument();
    expect(screen.getByText("拟签合同")).toBeInTheDocument();
    expect(screen.getByText("已解析")).toBeInTheDocument();
    expect(screen.queryByText("原件身份")).not.toBeInTheDocument();
    expect(screen.queryByText("document-digest")).not.toBeInTheDocument();
  });

  it("shows contract diagnostic identities only on request", async () => {
    const user = userEvent.setup();
    renderPanel({ showDiagnostics: true });
    await user.click(screen.getByRole("tab", { name: "合同信息" }));
    expect(screen.getByText("原件身份")).toBeInTheDocument();
    expect(screen.getByText("doc-1")).toBeInTheDocument();
    expect(screen.getByText("parsed-1")).toBeInTheDocument();
    expect(screen.getByText("document-digest")).toBeInTheDocument();
  });

  it("shows an undecided conclusion without inventing a decision", async () => {
    const user = userEvent.setup();
    renderPanel({ findings: [finding({ decisions: [] })] });
    await user.click(screen.getByRole("tab", { name: "审查结论" }));
    expect(screen.getByText("尚未记录审查判断。")).toBeInTheDocument();
  });

  it("renders the finding circuit, severity source and evidence in the detail", () => {
    renderPanel({ selectedId: "finding-1" });
    const detail = screen.getByRole("article");
    expect(within(detail).getByText(/规则，\s*冲突，\s*置信高，\s*付款/)).toBeInTheDocument();
    const circuit = within(detail).getByRole("list", { name: "线索链路" });
    expect(within(circuit).getByText("本次审查原文")).toBeInTheDocument();
    expect(within(circuit).getByText("1/1")).toBeInTheDocument();
    expect(within(circuit).getByText("待裁决")).toBeInTheDocument();
    expect(within(detail).getByText(/高风险，/)).toBeInTheDocument();
    expect(within(detail).getByText(/超过制度上限/)).toBeInTheDocument();
    expect(within(detail).queryByText(/policy-1/)).not.toBeInTheDocument();
    expect(within(detail).getByText("已定位")).toBeInTheDocument();
    expect(within(detail).getByText("合同.txt")).toBeInTheDocument();
    expect(within(detail).getByText("第 2 页")).toBeInTheDocument();
    expect(within(detail).getByText("三十日")).toBeInTheDocument();
  });

  it("shows policy diagnostics, unverified reason and decision history", () => {
    renderPanel({
      selectedId: "finding-1",
      showDiagnostics: true,
      findings: [
        finding({
          verification_status: "unverified",
          unverified_reason: "模型未引用原文",
          decisions: [decision],
        }),
      ],
    });
    const detail = screen.getByRole("article");
    expect(within(detail).getByText(/policy-1 v1 第4.2条/)).toBeInTheDocument();
    expect(within(detail).getByText("尚未核验：模型未引用原文")).toBeInTheDocument();
    expect(within(detail).getAllByText("确认风险").length).toBeGreaterThan(0);
    expect(within(detail).getByText(/审查员甲/)).toBeInTheDocument();
    expect(
      within(detail).getAllByText(formatDateTime(at)).length,
    ).toBeGreaterThan(0);
    expect(
      within(detail).getByText("修改判断时，之前的记录也会保留。"),
    ).toBeInTheDocument();
    expect(
      within(detail).getByRole("group", { name: "更正判断" }),
    ).toBeInTheDocument();
  });

  it.each([
    ["unlocatable", "无法定位"],
    ["anchor_mismatch", "定位失效"],
    ["missing", "来源缺失"],
  ])("labels a %s anchor as %s", (status, label) => {
    renderPanel({
      selectedId: "finding-1",
      findings: [
        finding({
          evidence: [
            {
              ...locatedAnchor,
              status: status as typeof locatedAnchor.status,
              mismatch_reason: "坐标不一致",
            },
          ],
        }),
      ],
    });
    expect(screen.getByText(label)).toBeInTheDocument();
    expect(screen.getByText("坐标不一致")).toBeInTheDocument();
    expect(screen.getByText("三十日")).toBeInTheDocument();
  });

  it("keeps a located anchor without body text in the saved snapshot", () => {
    renderPanel({
      selectedId: "finding-1",
      documents: [{ ...document, text: null }],
    });
    expect(
      screen.getByText("正文尚未返回。下面只显示保存下来的引用快照，没有在文档中定位。"),
    ).toBeInTheDocument();
    expect(screen.getByText("未能在正文定位")).toBeInTheDocument();
    expect(screen.getByText("三十日")).toBeInTheDocument();
  });

  it("explains a missing reference and a parsed-source mismatch", () => {
    renderPanel({
      selectedId: "finding-1",
      findings: [
        finding({
          evidence: [
            {
              ...locatedAnchor,
              id: "no-quote",
              status: "missing",
              quote: "",
            },
          ],
        }),
      ],
    });
    expect(
      screen.getByText("找不到对应来源，不能把这条引用当成已验证证据。"),
    ).toBeInTheDocument();
    expect(screen.getByText("没有保存引用内容。")).toBeInTheDocument();
  });

  it("marks a finding without evidence as not verified", () => {
    renderPanel({ selectedId: "finding-1", findings: [finding({ evidence: [] })] });
    expect(
      screen.getByText("这条线索没有附带证据，不能视为已验证。"),
    ).toBeInTheDocument();
  });

  it("disables the decision form with the supplied reason", () => {
    renderPanel({
      selectedId: "finding-1",
      canDecide: false,
      decisionDisabledReason: "只有审查员可以决定",
    });
    expect(screen.getByText("只有审查员可以决定")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "确认风险" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "保存决定" })).toBeDisabled();
  });

  it("uses the default disabled message when no reason is supplied", () => {
    renderPanel({ selectedId: "finding-1", canDecide: false });
    expect(
      screen.getByText("当前角色不能提交审查判断。"),
    ).toBeInTheDocument();
  });

  it("requires a decision and a valid reason before saving", async () => {
    const user = userEvent.setup();
    const onDecide = vi.fn().mockResolvedValue(false);
    renderPanel({ selectedId: "finding-1", onDecide });
    const save = screen.getByRole("button", { name: "保存决定" });
    expect(save).toBeDisabled();
    await user.type(screen.getByRole("textbox"), "核对完成");
    expect(save).toBeDisabled();
    expect(onDecide).not.toHaveBeenCalled();
    await user.click(screen.getByRole("radio", { name: "确认风险" }));
    expect(save).toBeEnabled();
    await user.click(save);
    await waitFor(() => expect(onDecide).toHaveBeenCalledTimes(1));
    await user.clear(screen.getByRole("textbox"));
    expect(screen.getByRole("alert")).toHaveTextContent("请填写理由");
    expect(save).toBeDisabled();
  });

  it("blocks saving without a server version", () => {
    renderPanel({ selectedId: "finding-1", findings: [finding({ version: null })] });
    expect(
      screen.getByText(
        "服务没有返回这条线索的版本号，暂不能保存，以免覆盖其他人的决定。",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存决定" })).toBeDisabled();
  });

  it("submits a trimmed reason with the expected version and clears it on success", async () => {
    const user = userEvent.setup();
    const { onDecide } = renderPanel({ selectedId: "finding-1" });
    await user.click(screen.getByRole("radio", { name: "误报" }));
    const reason = screen.getByRole("textbox");
    await user.type(reason, "  条款核对无误  ");
    expect(screen.getByRole("button", { name: "保存决定" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "保存决定" }));
    expect(onDecide).toHaveBeenCalledWith({
      findingId: "finding-1",
      action: "false_positive",
      reason: "条款核对无误",
      expectedVersion: 3,
    });
    await waitFor(() => expect(reason).toHaveValue(""));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("keeps the reason and errors after a failed save, showing conflict and save errors", async () => {
    const user = userEvent.setup();
    const onDecide = vi.fn().mockResolvedValue(false);
    const view = renderPanel({
      selectedId: "finding-1",
      onDecide,
      conflictMessage: "决定已被他人更新",
      saveError: "保存失败",
    });
    expect(screen.getByText("决定已被他人更新")).toBeInTheDocument();
    expect(screen.getByText("保存失败")).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "确认风险" }));
    await user.type(screen.getByRole("textbox"), "核对完成");
    await user.click(screen.getByRole("button", { name: "保存决定" }));
    expect(onDecide).toHaveBeenCalled();
    expect(screen.getByRole("textbox")).toHaveValue("核对完成");
    view.unmount();
  });

  it("marks a pending save and prevents duplicate submissions", () => {
    renderPanel({ selectedId: "finding-1", pending: true });
    expect(screen.getByRole("button", { name: "正在保存" })).toBeDisabled();
  });

  it("uses neutral fallbacks for a minimal finding", () => {
    renderPanel({
      selectedId: "finding-1",
      findings: [
        finding({
          claim: "",
          domain: "",
          confidence: null,
          severity: null,
          severity_source: null,
          unverified_reason: null,
          decisions: [],
        }),
      ],
    });
    const detail = screen.getByRole("article");
    expect(
      within(detail).getByRole("heading", { name: "未命名线索" }),
    ).toBeInTheDocument();
    expect(within(detail).getByText(/规则，\s*冲突/)).not.toHaveTextContent("置信");
    expect(within(detail).queryByText("高风险")).not.toBeInTheDocument();
    expect(within(detail).queryByText(/尚未核验：/)).not.toBeInTheDocument();
  });

  it("reports partial anchor provenance without inventing identities", () => {
    renderPanel({
      selectedId: "finding-1",
      showDiagnostics: true,
      documents: [],
      findings: [
        finding({
          evidence: [
            { ...locatedAnchor, id: "parsed-only", normalized_digest: null },
            {
              ...locatedAnchor,
              id: "digest-only",
              parsed_document_id: null,
              normalized_digest: "normalized-digest-9",
            },
            {
              ...locatedAnchor,
              id: "legacy",
              parsed_document_id: null,
              normalized_digest: null,
            },
          ],
        }),
      ],
    });
    expect(screen.getByText(/正文 SHA-256\s*未记录/)).toBeInTheDocument();
    expect(
      screen.getByText(/本次使用的解析结果\s*未记录/),
    ).toBeInTheDocument();
    expect(
      screen.getAllByText("历史来源未记录解析身份或正文摘要"),
    ).toHaveLength(1);
  });
});
