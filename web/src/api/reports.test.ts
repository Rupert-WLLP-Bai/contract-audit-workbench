import { parseReportDetail } from "./reports";

const at = "2026-10-25T11:54:44.284Z";

function envelope(overrides: Record<string, unknown> = {}) {
  return {
    schema_version: "case-report/v1",
    report_id: "report_1",
    report_version: 1,
    created_at: at,
    notice: "本报告记录技术审查结果，不构成法律意见或业务审批。",
    case: {
      id: "case_1",
      title: "政企专线服务合同",
      contract_type: "service",
      business_line: "ENT",
      scenario: "telecom_dedicated_line",
    },
    run: {
      id: "run_1",
      case_id: "case_1",
      state: "partial",
      model_ref: "model/demo",
      partial_reason: "模型输出无法解析，已保留规则结果。",
      created_at: at,
      updated_at: at,
    },
    attempt: {
      id: "attempt_1",
      run_id: "run_1",
      ordinal: 1,
      state: "partial",
      retry_of_attempt_id: null,
      accepted_at: at,
      started_at: at,
      finished_at: at,
      input_hash: "input-hash",
      output_hash: "output-hash",
      error_code: "model_output_unusable",
      history_kind: "frozen",
    },
    execution: {
      schema_version: "audit-execution/v1",
      events: [],
      usage: {
        input_tokens: 10,
        output_tokens: 20,
        total_tokens: 30,
        calls: 1,
        unknown_calls: 0,
        sources: ["planner"],
        known_subtotals: {
          input_tokens: 10,
          output_tokens: 20,
          total_tokens: 30,
        },
        cost: null,
      },
      duration_ms: 3000,
      first_verified_finding_ms: 2000,
      queue_wait_ms: 0,
    },
    documents: [
      {
        document: {
          id: "doc_1",
          case_id: "case_1",
          role: "draft_contract",
          filename: "draft.txt",
          media_type: "text/plain",
          digest: "doc-digest",
          size: 42,
          origin: "upload",
          created_at: at,
        },
        parsed: {
          id: "parsed_1",
          document_id: "doc_1",
          original_digest: "doc-digest",
          parser_id: "document-inspection",
          parser_version: "text-structure-v2",
          config_hash: "config-hash",
          normalization_version: "text-normalization-v1",
          normalized_digest: "normalized-digest",
          ir_digest: "ir-digest",
          artifact_digest: "artifact-digest",
          created_at: at,
          status: "ready",
          text: "预付款比例 40%。",
          ir: {
            Clauses: [
              {
                ID: "c-1",
                Heading: "第二条 付款",
                Start: 0,
                End: 9,
                Page: null,
              },
            ],
          },
        },
      },
    ],
    findings: [
      {
        id: "finding_1",
        origin: "rule",
        domain: "commercial",
        rule_id: "ENT-COM-ADV-001",
        capability_id: "payment.advance_ratio.ceiling",
        object_ref: "LINE-001",
        claim: "预付款比例超过审批上限。",
        relation: "conflict",
        disposition_suggestion: "confirm_risk",
        severity: null,
        version: 1,
        anchors: [
          {
            id: "anchor_1",
            document_id: "doc_1",
            parsed_document_id: "parsed_1",
            normalized_digest: "normalized-digest",
            quote: "预付款比例 40%",
            status: "located",
            end: 9,
          },
        ],
        created_at: at,
      },
    ],
    evaluations: [],
    decisions: [],
    rulepack: {
      availability: "frozen",
      compiler_ref: "compiler/v1",
      manifest_hash: "manifest-hash",
      snapshot_sha256: "snapshot-sha256",
      snapshot: {
        schema_version: "rulepack/v1",
        id: "telecom-contractual",
        version: "5",
        rules: [{ rule_id: "ENT-COM-ADV-001" }],
      },
    },
    input_hash: "input-hash",
    output_hash: "output-hash",
    decision_snapshot_hash: "decision-hash",
    agent_review: {
      status: "partial",
      mode: "deterministic_planner",
      domains: [{ domain: "commercial" }],
      gaps: [{ gap_id: "gap_1" }],
    },
    agent_review_sha256: "agent-hash",
    ...overrides,
  };
}

describe("case-report/v1 parser", () => {
  it("keeps the frozen report identity, rulepack metadata and a zero-start anchor", () => {
    const report = parseReportDetail(envelope());
    expect(report.schema_version).toBe("case-report/v1");
    expect(report.report_version).toBe(1);
    expect(report.case.title).toBe("政企专线服务合同");
    expect(report.rulepack).toMatchObject({
      availability: "frozen",
      snapshot_id: "telecom-contractual",
      snapshot_version: "5",
      rule_count: 1,
    });
    expect(report.findings[0]?.anchors[0]?.text_offset).toEqual({
      start: 0,
      end: 9,
    });
    expect(report.documents[0]?.clauses[0]).toMatchObject({
      heading: "第二条 付款",
      text_offset: { start: 0, end: 9 },
    });
    expect(report.agent_review).toMatchObject({
      status: "partial",
      domain_count: 1,
      gap_count: 1,
    });
  });

  it("does not treat a report with another schema as a frozen case report", () => {
    expect(() =>
      parseReportDetail(envelope({ schema_version: "report/v2" })),
    ).toThrow(/case-report\/v1/);
  });

  it("rejects a report without the frozen document bundle", () => {
    const payload = envelope();
    delete (payload as Record<string, unknown>).documents;
    expect(() => parseReportDetail(payload)).toThrow(/documents/);
  });

  it("keeps missing optional decisions and model review explicit rather than guessed", () => {
    const report = parseReportDetail(
      envelope({ decisions: null, agent_review: null, report_version: 0 }),
    );
    expect(report.decisions).toEqual([]);
    expect(report.agent_review).toBeNull();
    expect(report.report_version).toBe(0);
  });
});
