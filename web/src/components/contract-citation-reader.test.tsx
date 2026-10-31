import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parseBusinessAgentReview, type BusinessAgentReview } from "../api/business-agent-review";
import { renderWithAntd as render } from "../test/antd";
import { BusinessAgentReviewView } from "./business-agent-review";

const EVIDENCE_ID = "fact_0123456789abcdef01234567";
const TEXT = "😀甲方应支付100万元。";

const projection = {
  attempt_id: "attempt-1",
  status: "completed",
  rulepack: null,
  scope: [{ domain: "commercial", capability_ids: ["amount"], source_ids: ["doc-1"] }],
  domains: [{
    domain: "commercial",
    status: "completed",
    capabilities: [{
      capability_id: "amount",
      mandatory: true,
      status: "compatible",
      relation: "compatible",
      reason_code: "matched_supported_field",
      evidence_ids: [EVIDENCE_ID],
      canonical_evaluation_ids: [],
    }],
  }],
  gaps: [],
  evidence: [{
    evidence_id: EVIDENCE_ID,
    semantic_type: "amount",
    value: "1000000",
    unit: "元",
    currency: null,
    verification: "typed_semantic_and_frozen_quote_verified",
    anchor: {
      id: `anchor-${EVIDENCE_ID}`,
      document_id: "doc-1",
      parsed_document_id: "parsed-1",
      quote: "100万元",
      status: "located",
      start: 6,
      end: 11,
      reason: null,
      page: null,
      element_id: null,
    },
  }],
  unresolved_gap_ids: [],
  observations: null,
};

function review(): BusinessAgentReview {
  const parsed = parseBusinessAgentReview(projection);
  if (parsed === null) throw new Error("fixture review did not parse");
  return parsed;
}

function citationBody(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    case_id: "case-1",
    run_id: "run-1",
    attempt_id: "attempt-1",
    evidence_id: EVIDENCE_ID,
    document_id: "doc-1",
    parsed_document_id: "parsed-1",
    filename: "合同.txt",
    original_digest: "a".repeat(64),
    parse_digest: "b".repeat(64),
    start: 6,
    end: 11,
    quote: "100万元",
    text: TEXT,
    material_id: null,
    material_version: null,
    ...overrides,
  };
}

function mount(options: {
  selectedEvidenceId?: string | null;
  onSelectEvidence?: (evidenceId: string | null) => void;
} = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return render(
    <QueryClientProvider client={client}>
      <BusinessAgentReviewView
        review={review()}
        caseId="case-1"
        runId="run-1"
        attemptId="attempt-1"
        selectedEvidenceId={options.selectedEvidenceId ?? null}
        onSelectEvidence={options.onSelectEvidence ?? (() => undefined)}
      />
    </QueryClientProvider>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("frozen contract citation reader", () => {
  it("reads the attempt's frozen text and highlights the Unicode code-point range", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(citationBody()), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const onSelect = vi.fn();
    const { container } = mount({ selectedEvidenceId: EVIDENCE_ID, onSelectEvidence: onSelect });

    await screen.findByText(/原文摘要/);
    const body = container.ownerDocument.querySelector(".frozen-citation-text");
    expect(body).not.toBeNull();
    expect(body?.textContent).toBe(TEXT);
    const mark = container.ownerDocument.querySelector("mark.hit-evidence");
    expect(mark?.textContent).toBe("100万元");
    expect(container.ownerDocument.querySelector(".frozen-citation-reader")?.textContent)
      .toContain("字符 7–11（Unicode 码点）");
    expect(container.ownerDocument.querySelector(".frozen-citation-reader")?.textContent)
      .toContain("材料版本未记录");
    await waitFor(() => expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      `/api/cases/case-1/runs/run-1/attempts/attempt-1/contract-citations/${EVIDENCE_ID}`,
    ));

    fireEvent.click(screen.getByRole("button", { name: "收起本次冻结原文" }));
    expect(onSelect).toHaveBeenCalledWith(null);
  });

  it("offers the reader only with an exact attempt scope", () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    render(
      <QueryClientProvider client={client}>
        <BusinessAgentReviewView review={review()} />
      </QueryClientProvider>,
    );
    expect(screen.queryByRole("button", { name: "读取本次冻结原文" })).not.toBeInTheDocument();
  });

  it("refuses a payload that answers a different frozen document", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(
      new Response(JSON.stringify(citationBody({ document_id: "doc-2" })), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ));
    mount({ selectedEvidenceId: EVIDENCE_ID });
    expect(await screen.findByText("返回的原文身份与当前引用不一致，未显示正文。")).toBeInTheDocument();
    expect(document.querySelector("mark.hit-evidence")).toBeNull();
  });

  it("warns instead of showing another attempt's citation from the URL", () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    mount({ selectedEvidenceId: "fact_ffffffffffffffffffffffff" });
    expect(screen.getByText("URL 中的引用不属于当前执行记录")).toBeInTheDocument();
    expect(document.querySelector(".frozen-citation-reader")).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
