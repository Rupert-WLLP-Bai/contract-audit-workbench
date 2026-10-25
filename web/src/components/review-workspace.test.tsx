import { useState, type ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import type { ReviewDocument, ReviewWorkspace } from "../api/types";
import { ReviewWorkspaceView } from "./review-workspace";
import { DocumentStage } from "./document-stage";

const procurementText = "质保期为三十六个月，自验收合格之日起算。";
const contractText = "质保期为二十四个月。";

function documentOf(id: string, role: string, filename: string, text: string): ReviewDocument {
  return {
    id,
    role,
    filename,
    media_type: "text/plain",
    digest: "abc12345",
    size: text.length,
    parse_status: "ready",
    parse_error: null,
    created_at: "2026-10-21T01:00:00Z",
    parsed_document: null,
    text,
    clauses: [{ id: `${id}-clause`, heading: "质保", text_offset: { start: 0, end: 4 }, page: null }],
  };
}

function workspace(): ReviewWorkspace {
  return {
    run: {
      id: "r1",
      case_id: "c1",
      state: "awaiting_human",
      updated_at: "2026-10-21T01:00:00Z",
      started_at: "2026-10-21T00:50:00Z",
      model_ref: "fake",
      partial_reason: null,
      failure_message: null,
    },
    documents: [
      documentOf("d1", "procurement", "采购文件.txt", procurementText),
      documentOf("d2", "draft_contract", "拟签合同.txt", contractText),
    ],
    findings: [
      {
        id: "f1",
        origin: "ai",
        domain: "质保",
        claim: "采购文件与拟签合同的质保期不一致",
        relation: "conflict",
        confidence: "medium",
        severity: null,
        severity_source: null,
        verification_status: "unverified",
        unverified_reason: "引用尚未复核",
        version: 3,
        decisions: [],
        evidence: [
          {
            id: "e1",
            document_version_id: "d1",
            parsed_document_id: null,
            normalized_digest: null,
            quote: "三十六个月",
            status: "located",
            page: 2,
            text_offset: { start: 4, end: 9 },
            bbox: null,
            mismatch_reason: null,
          },
          {
            id: "e2",
            document_version_id: "d2",
            parsed_document_id: null,
            normalized_digest: null,
            quote: "二十四个月",
            status: "located",
            page: 5,
            text_offset: { start: 4, end: 9 },
            bbox: null,
            mismatch_reason: null,
          },
        ],
      },
      {
        id: "f2",
        origin: "rule",
        domain: "付款",
        claim: "付款节点缺少可定位原文",
        relation: "insufficient",
        confidence: null,
        severity: null,
        severity_source: null,
        unverified_reason: null,
        verification_status: "unknown",
        version: null,
        decisions: [
          {
            id: "dec-1",
            action: "insufficient_evidence",
            reason: "页码对不上",
            actor: "周审查",
            decided_at: "2026-10-21T02:00:00Z",
          },
        ],
        evidence: [
          {
            id: "e3",
            document_version_id: "d1",
            parsed_document_id: null,
            normalized_digest: null,
            quote: "付款按进度",
            status: "anchor_mismatch",
            page: 8,
            text_offset: { start: 0, end: 4 },
            bbox: null,
            mismatch_reason: "定位坐标与当前版本不匹配",
          },
        ],
      },
    ],
    execution: null,
    audit_events: [],
  };
}

type Decide = ComponentProps<typeof ReviewWorkspaceView>["onDecide"];

function WorkspaceHarness({ layout, onDecide }: { layout: "split" | "switch"; onDecide: Decide }) {
  const [selectedId, setSelectedId] = useState("f1");
  return (
    <ReviewWorkspaceView
      workspace={workspace()}
      layout={layout}
      selectedId={selectedId}
      streamStatus="live"
      pending={false}
      retrying={false}
      conflictMessage={selectedId === "f1" ? "该事项已被他人更新，已载入最新内容，请重新确认。" : null}
      saveError={null}
      onSelect={setSelectedId}
      onDecide={onDecide}
      onRetry={vi.fn()}
      onSelectAttempt={vi.fn()}
      onCancel={vi.fn()}
    />
  );
}

function renderWorkspace(layout: "split" | "switch") {
  const onDecide = vi.fn().mockResolvedValue(true);
  const view = render(<WorkspaceHarness layout={layout} onDecide={onDecide} />);
  return { ...view, onDecide };
}

describe("review workspace", () => {
  it("shows both sources on request and does not treat the model claim as verified", async () => {
    const user = userEvent.setup();
    const { container } = renderWorkspace("split");
    await user.click(screen.getByRole("button", { name: "双来源对照" }));
    const marks = container.querySelectorAll("mark.hit-evidence");
    expect([...marks].map((mark) => mark.textContent)).toEqual(["三十六个月", "二十四个月"]);
    expect(screen.getByText("尚未核验：引用尚未复核")).toBeInTheDocument();
    expect(screen.getByText("该事项已被他人更新，已载入最新内容，请重新确认。")).toBeInTheDocument();
    const provenance = [...container.querySelectorAll(".parsed-document-summary")];
    expect(provenance).toHaveLength(2);
    expect(provenance.every((item) => {
      const text = item.textContent ?? "";
      return text.includes("解析来源 未记录（历史记录）") && text.includes("正文摘要 未记录");
    })).toBe(true);
    expect(screen.getAllByText("历史来源未记录解析身份或正文摘要")).toHaveLength(2);
  });

  it("keeps a same-document evidence quote unhighlighted for a different parse", () => {
    const finding = workspace().findings[0];
    if (!finding) throw new Error("test finding is missing");
    const anchor = finding.evidence[0];
    if (!anchor) throw new Error("test evidence is missing");
    anchor.parsed_document_id = "pd-old";
    anchor.normalized_digest = "text-old";
    const source = documentOf("d1", "procurement", "采购文件.txt", procurementText);
    source.parsed_document = {
      id: "pd-current",
      document_id: "d1",
      original_digest: "abc12345",
      parser_id: "text",
      parser_version: "1",
      config_hash: "config-current",
      normalization_version: "unicode-v1",
      normalized_digest: "text-current",
      ir_digest: "ir-current",
      artifact_digest: "artifact-current",
      created_at: "2026-10-21T03:00:00Z",
      status: "ready",
    };
    const { container } = render(<DocumentStage documents={[source]} finding={finding} findings={[finding]} selectedFindingId={finding.id} layout="switch" onSelectFinding={vi.fn()} />);

    expect(container.querySelector("mark.hit-evidence")).toBeNull();
    expect(screen.getByText(/解析产物与当前正文不一致/)).toBeInTheDocument();
  });

  it("keeps the attempt input primary when an older parse finishes later", () => {
    const selected = documentOf("d1", "procurement", "采购文件.txt", "当前审查采用的原文");
    selected.parsed_document = {
      id: "pd-selected", document_id: "d1", original_digest: selected.digest,
      parser_id: "text", parser_version: "1", config_hash: "config-1",
      normalization_version: "unicode-v1", normalized_digest: "selected-text",
      ir_digest: "selected-ir", artifact_digest: "selected-artifact",
      created_at: "2026-10-21T03:00:00Z", status: "ready",
    };
    const historical = documentOf("d1", "procurement", "采购文件.txt", "晚完成的旧任务原文");
    historical.parsed_document = {
      ...selected.parsed_document, id: "pd-historical", normalized_digest: "historical-text",
      created_at: "2026-10-21T04:00:00Z",
    };
    const { container } = render(<DocumentStage documents={[selected, historical]} finding={null} findings={[]} selectedFindingId={null} layout="switch" onSelectFinding={vi.fn()} />);

    expect(container.querySelector(".doc-pane")?.getAttribute("data-parsed-document-id")).toBe("pd-selected");
    expect(screen.getByText("当前审查采用的原文")).toBeInTheDocument();
    expect(screen.queryByText("晚完成的旧任务原文")).not.toBeInTheDocument();
  });

  it("navigates from a material reference back to the linked finding", async () => {
    const user = userEvent.setup();
    const { container } = renderWorkspace("switch");
    await user.click(screen.getByRole("button", { name: "查看线索：付款节点缺少可定位原文" }));

    expect(screen.getByRole("heading", { name: "付款节点缺少可定位原文" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "查看线索：付款节点缺少可定位原文" })).toHaveAttribute("aria-pressed", "true");
    expect(container.querySelector(".doc-pane")?.getAttribute("data-document-version-id")).toBe("d1");
  });

  it("switches evidence sources without dropping the finding, and refuses a bad anchor highlight", async () => {
    const user = userEvent.setup();
    const { container } = renderWorkspace("switch");
    expect(container.querySelector("mark.hit-evidence")?.textContent).toBe("三十六个月");
    await user.click(screen.getByRole("button", { name: "拟签合同" }));
    expect(container.querySelector("mark.hit-evidence")?.textContent).toBe("二十四个月");

    await user.click(screen.getByRole("button", { name: "选择线索：付款节点缺少可定位原文" }));
    expect(container.querySelector("mark.hit-evidence")).toBeNull();
    expect(screen.getAllByText("定位坐标与当前版本不匹配").length).toBeGreaterThan(0);
    expect(screen.getAllByText("付款按进度").length).toBeGreaterThan(0);
  });

  it("saves a reasoned decision and blocks a finding that has no version", async () => {
    const user = userEvent.setup();
    const { onDecide } = renderWorkspace("switch");
    const save = screen.getByRole("button", { name: "保存决定" });
    expect(save).toBeDisabled();
    await user.click(screen.getByRole("radio", { name: "误报" }));
    await user.type(screen.getByRole("textbox", { name: "理由" }), "两处期限实际指向同一附件");
    expect(save).toBeEnabled();
    await user.click(save);
    expect(onDecide).toHaveBeenCalledWith({
      findingId: "f1",
      action: "false_positive",
      reason: "两处期限实际指向同一附件",
      expectedVersion: 3,
    });

    await user.click(screen.getByRole("button", { name: "选择线索：付款节点缺少可定位原文" }));
    expect(screen.getByText("服务没有返回这条线索的版本号，暂不能保存，以免覆盖其他人的决定。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存决定" })).toBeDisabled();
    expect(screen.getByText(/周审查/)).toBeInTheDocument();
  });
  it("creates or removes saved case relations", async () => {
    const user = userEvent.setup();
    const onCreateRelation = vi.fn().mockResolvedValue(undefined);
    const onDeleteRelation = vi.fn();
    const caseContext = {
      caseId: "c1", title: "云服务协议", number: "CN-1", customer: "甲方", supplier: "乙方",
      contractType: "采购合同", businessLine: "ENT", scenario: "cloud_services", status: "审查中",
      ownerId: "m1", ownerName: "林晓", dueAt: null,
      collaborators: [{ id: "m1", name: "林晓", role: "reviewer", responsibilities: ["案件负责人", "任务责任人：核对SLA（open）"] }],
      relatedCases: [{ relationId: "relation-3", id: "c3", title: "云平台服务协议", reason: "同供应商年度续约", createdAt: "2026-10-21T01:00:00Z", runId: "r3" }],
      availableCases: [{ id: "c2", title: "云托管服务补充协议" }],
    };
    render(<MemoryRouter><DocumentStage documents={[]} finding={null} findings={[]} selectedFindingId={null} layout="switch" onSelectFinding={vi.fn()} caseContext={caseContext} canEditRelations onCreateRelation={onCreateRelation} onDeleteRelation={onDeleteRelation} /></MemoryRouter>);

    await user.click(screen.getByRole("tab", { name: /相关案件/ }));
    expect(screen.getByRole("link", { name: "云平台服务协议" })).toHaveAttribute("href", "/cases/c3/reviews/r3");
    await user.click(screen.getByRole("button", { name: "解除关联" }));
    expect(onDeleteRelation).toHaveBeenCalledWith("relation-3");
    await user.selectOptions(screen.getByLabelText("关联可见案件"), "c2");
    await user.type(screen.getByLabelText("关联原因"), "同一云资源范围");
    await user.click(screen.getByRole("button", { name: "建立关联" }));
    expect(onCreateRelation).toHaveBeenCalledWith({ related_case_id: "c2", reason: "同一云资源范围" });
  });
});
