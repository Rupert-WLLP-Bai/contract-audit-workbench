import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ClauseProposal } from "../api/clause-proposals";
import type { Finding, ReviewDocument } from "../api/types";
import { renderWithAntd } from "../test/antd";
import { ClauseProposalPanel } from "./clause-proposal-panel";

const { list, mutation } = vi.hoisted(() => ({
  list: vi.fn(),
  mutation: { mutateAsync: vi.fn(), reset: vi.fn(), isPending: false },
}));
vi.mock("../api/queries", () => ({
  queryKeys: {},
  useSession: () => ({ isSuccess: true, isFetching: false, data: { id: "reviewer" } }),
  useCaseClauseProposals: list,
  useContractLedger: () => ({ data: { pages: [] } }),
  useCreateClauseProposal: () => mutation,
  useDeriveClauseProposalText: () => mutation,
  useUploadDocument: () => mutation,
  useDecideClauseProposal: () => mutation,
}));

const date = "2026-11-19T10:00:00Z";
const proposal: ClauseProposal = {
  id: "proposal",
  version: 1,
  status: "pending",
  case_id: "case",
  run_id: "run",
  attempt_id: "attempt",
  finding_id: "finding",
  input_hash: "saved-input",
  created_at: date,
  created_by: "reviewer",
  evidence_verified: true,
  evidence: {
    document_id: "document",
    parsed_document_id: "parse",
    normalized_digest: "saved-digest",
    quote: "付款期限",
    start: 1,
    end: 5,
  },
  original_text: "付款期限",
  proposed_text: "验收后付款",
  reason: "落实验收约定",
  history: [],
};
const finding: Finding = {
  id: "finding",
  origin: "model",
  domain: "payment",
  claim: "付款期限待核对",
  relation: "insufficient",
  confidence: null,
  verification_status: "unknown",
  evidence: [],
  unverified_reason: null,
  version: 1,
  severity: null,
  severity_source: null,
  decisions: [],
};
const document: ReviewDocument = {
  id: "document",
  parsed_document_id: "parse",
  parsed_document: null,
  text: "😀付款期限",
  filename: "合同.txt",
  role: "draft_contract",
  media_type: "text/plain",
  digest: "original-digest",
  size: 20,
  parse_status: "ready",
  parse_error: null,
  created_at: date,
  clauses: [],
};

function renderProposal(source = document) {
  list.mockReturnValue({
    isSuccess: true,
    isFetching: false,
    data: {
      items: [
        proposal,
        { ...proposal, id: "prior", attempt_id: "prior", proposed_text: "过往建议" },
      ],
    },
  });
  const client = new QueryClient();
  return renderWithAntd(
    <QueryClientProvider client={client}>
      <ClauseProposalPanel
        immersive
        caseId="case"
        runId="run"
        attemptId="attempt"
        finding={finding}
        frozenParsedDocumentIds={["parse"]}
        documents={[source]}
        remediations={[]}
        canWrite
      />
    </QueryClientProvider>,
  );
}

describe("immersive clause proposals", () => {
  it("shows the exact saved diff and opens the existing edited-acceptance flow", async () => {
    const user = userEvent.setup();
    renderProposal();
    expect(screen.getByText("付款期限", { selector: "del", exact: false })).toBeInTheDocument();
    expect(screen.getByText("验收后付款", { selector: "ins", exact: false })).toBeInTheDocument();
    expect(screen.queryByText("过往建议")).not.toBeInTheDocument();
    expect(screen.queryByText("来自 AI")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "改一下再采纳" }));
    expect(screen.getByRole("textbox", { name: "确认的替换条款" })).toHaveValue("验收后付款");
    expect(mutation.mutateAsync).not.toHaveBeenCalled();
  });

  it("blocks both acceptance modes if the displayed source differs from the saved proposal", () => {
    renderProposal({ ...document, text: "😀交付期限" });
    expect(screen.getByRole("button", { name: "采纳这条修改" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "改一下再采纳" })).toBeDisabled();
    expect(
      screen.queryByText("付款期限", { selector: "del", exact: false }),
    ).not.toBeInTheDocument();
  });
});
