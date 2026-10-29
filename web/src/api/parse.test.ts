import {
  parseCaseDetail,
  parseCaseList,
  parseCaseSummary,
  parseFinding,
  parseRemediation,
  parseReviewWorkspace,
  parseSettings,
} from "./parse";
import { parseWorkbenchSnapshot } from "./workbench";

const caseDefaults = {
  business_line: "ENT",
  scenario: "telecom_dedicated_line",
  business_status: "active",
  members: [],
  generations: [],
  remediations: [],
  reports: [],
  archives: [],
  archived_at: null,
};

describe("response parsers", () => {
  it("keeps operator audit defaults off unless the server enables them", () => {
    expect(parseSettings({}).operator_audit).toBeNull();
    const parsed = parseSettings({
      operator_audit: {
        enabled: true,
        connectors: [
          {
            connector_id: "fixture.oss",
            source_system: "fixture-oss-east",
            allowed_objects: ["fixture-line-001"],
          },
        ],
      },
    });
    expect(parsed.operator_audit?.enabled).toBe(true);
    expect(parsed.operator_audit?.connectors[0]?.connector_id).toBe(
      "fixture.oss",
    );
    expect(
      parseSettings({ operator_audit: { enabled: "true" } }).operator_audit
        ?.enabled,
    ).toBe(false);
  });

  it("rejects a list that is not the documented shape", () => {
    expect(() => parseCaseList({})).toThrow(/items/);
  });

  it("does not treat a missing finding version or anchor status as safe to save or highlight", () => {
    const finding = parseFinding({
      id: "f1",
      claim: "质保期冲突",
      evidence: [{ id: "e1", quote: "三十六个月" }],
    });
    expect(finding.version).toBeNull();
    expect(finding.evidence[0]?.status).toBe("missing");
  });

  it("does not infer claim verification from a located citation", () => {
    const finding = parseFinding({
      id: "f1",
      claim: "期限不一致",
      unverified_reason: null,
      evidence: [{ id: "e1", document_version_id: "d1", status: "located", quote: "三十六个月" }],
    });
    expect(finding.evidence[0]?.status).toBe("located");
    expect(finding.verification_status).toBe("unknown");
  });

  it("keeps an explicit zero count distinct from a missing summary", () => {
    const withZero = parseCaseDetail({
      ...caseDefaults,
      id: "c1",
      title: "样本",
      finding_counts: { total: 0, undecided: 0, conflict: 0, insufficient: 0 },
    });
    const missing = parseCaseDetail({
      ...caseDefaults,
      id: "c2",
      title: "样本",
    });
    expect(withZero.finding_counts?.total).toBe(0);
    expect(missing.finding_counts).toBeNull();
  });
  it("preserves empty legacy business context without inferring telecom values", () => {
    const legacy = parseCaseDetail({
      ...caseDefaults,
      id: "legacy-case",
      business_line: "",
      scenario: "",
      business_status: "",
    });
    expect(legacy.business_line).toBe("");
    expect(legacy.scenario).toBe("");
    expect(legacy.business_status).toBe("");
  });

  it("keeps missing historical parse and evidence provenance null", () => {
    const detail = parseCaseDetail({
      ...caseDefaults,
      id: "legacy-case",
      documents: [
        { id: "d1", digest: "original-digest", parse_status: "ready" },
      ],
    });
    const finding = parseFinding({
      id: "f1",
      evidence: [
        {
          id: "e1",
          document_version_id: "d1",
          status: "located",
          quote: "三十六个月",
        },
      ],
    });
    expect(detail.documents[0]?.parsed_document).toBeNull();
    expect(finding.evidence[0]).toMatchObject({
      parsed_document_id: null,
      normalized_digest: null,
    });
  });

  it("preserves text and artifact digests separately from the original digest", () => {
    const detail = parseCaseDetail({
      ...caseDefaults,
      id: "c1",
      documents: [
        {
          id: "d1",
          digest: "original-digest",
          parse_status: "ready",
          parsed_document: {
            id: "pd1",
            document_id: "d1",
            original_digest: "original-digest",
            parser_id: "text",
            parser_version: "1",
            config_hash: "config-digest",
            normalization_version: "unicode-v1",
            normalized_digest: "text-digest",
            ir_digest: "ir-digest",
            artifact_digest: "artifact-digest",
            created_at: "2026-10-21T01:00:00Z",
            status: "ready",
          },
        },
      ],
    });
    expect(detail.documents[0]?.parsed_document).toMatchObject({
      original_digest: "original-digest",
      normalized_digest: "text-digest",
      ir_digest: "ir-digest",
      artifact_digest: "artifact-digest",
    });
  });

  it("reads a review workspace with two evidence anchors", () => {
    const workspace = parseReviewWorkspace({
      run: { id: "r1", state: "awaiting_human", case_id: "c1" },
      audit_events: [],
      evaluations: [
        {
          id: "eval1",
          domain: "technical",
          relation: "compatible",
          rule_version: "1.0.0",
          reason: "规则满足",
          rule_id: "ENT-TECH-BW-001",
          capability_id: "dedicated_line.minimum_bandwidth",
          object_refs: ["LINE-001"],
          finding_id: null,
          reason_code: "minimum_bandwidth_met",
          fact_ids: ["fact1"],
        },
      ],
      documents: [
        {
          id: "d1",
          filename: "采购.docx",
          role: "procurement",
          parse_status: "ready",
          text: "三十六个月",
        },
      ],
      findings: [
        {
          id: "f1",
          origin: "ai",
          relation: "conflict",
          claim: "期限不一致",
          version: 2,
          unverified_reason: "引用未复核",
          evidence: [
            {
              id: "e1",
              document_version_id: "d1",
              status: "located",
              quote: "三十六个月",
              text_offset: { start: 0, end: 5 },
            },
          ],
          decisions: [],
        },
      ],
    });
    expect(workspace.run.state).toBe("awaiting_human");
    expect(workspace.findings[0]?.version).toBe(2);
    expect(workspace.documents[0]?.text).toBe("三十六个月");
    expect(workspace.documents[0]?.parsed_document_id).toBeNull();
    expect(workspace.evaluations?.[0]).toMatchObject({
      rule_id: "ENT-TECH-BW-001",
      rule_version: "1.0.0",
      capability_id: "dedicated_line.minimum_bandwidth",
      object_refs: ["LINE-001"],
      finding_id: null,
      reason_code: "minimum_bandwidth_met",
      fact_ids: ["fact1"],
    });
  });

  it("reads public attempts and frozen document identity without privileged diagnostics", () => {
    const attempt = {
      id: "a1", run_id: "r1", ordinal: 1, retry_of_attempt_id: null,
      state: "awaiting_human", accepted_at: "2026-10-21T06:00:00Z",
      started_at: null, finished_at: null, history_kind: "frozen",
      parsed_document_ids: ["parsed-1"],
    };
    const workspace = parseReviewWorkspace({
      run: { id: "r1", state: "awaiting_human", case_id: "c1" },
      documents: [{ id: "d1", filename: "合同.txt", role: "draft_contract", parse_status: "ready", parsed_document: null, parsed_document_id: "parsed-1", text: "合同正文" }],
      findings: [], execution: null, audit_events: [],
      review_attempts: { attempt, attempts: [attempt] },
      business_events: [{ event_id: "event-1", run_id: "r1", actor: "u1", seq: 1, event_type: "review.started", correlation_ids: null }],
    });
    expect(workspace.execution).toBeNull();
    expect(workspace.review_attempts?.attempt).toMatchObject({ id: "a1", parsed_document_ids: ["parsed-1"], input_hash: null });
    expect(workspace.review_attempts?.attempts).toHaveLength(1);
    expect(workspace.documents[0]).toMatchObject({ parsed_document: null, parsed_document_id: "parsed-1", text: "合同正文" });
    expect(workspace.business_events?.[0]).toMatchObject({ event_id: "event-1", correlation_ids: [] });
    expect(workspace.business_events?.[0]).not.toHaveProperty("hash");
  });

  it("preserves explicit parsed document references on frozen attempts", () => {
    const attempt = {
      id: "a1",
      run_id: "r1",
      ordinal: 1,
      retry_of_attempt_id: null,
      parsed_document_ids: ["pd1", "pd2"],
      state: "failed",
      accepted_at: null,
      started_at: null,
      finished_at: null,
      input_hash: null,
      output_hash: null,
      error_code: "provider_unavailable",
      history_kind: "frozen",
    };
    const workspace = parseReviewWorkspace({
      run: { id: "r1", state: "failed", case_id: "c1" },
      audit_events: [],
      execution: {
        schema_version: "audit-execution/v1",
        attempt,
        attempts: [attempt],
        events: [],
        usage: null,
        all_attempts_usage: null,
        duration_ms: null,
        first_verified_finding_ms: null,
        queue_wait_ms: null,
      },
    });
    expect(workspace.execution?.attempt.parsed_document_ids).toEqual([
      "pd1",
      "pd2",
    ]);
  });
  it("preserves unknown token usage and null timestamps without converting them to zero", () => {
    const attempt = {
      id: "a1",
      run_id: "r1",
      ordinal: 1,
      retry_of_attempt_id: null,
      state: "failed",
      accepted_at: null,
      started_at: null,
      finished_at: null,
      input_hash: null,
      output_hash: null,
      error_code: "provider_unavailable",
      history_kind: "frozen",
    };
    const usage = {
      input_tokens: null,
      output_tokens: null,
      total_tokens: null,
      calls: 2,
      unknown_calls: 2,
      sources: ["model_http"],
      known_subtotals: {
        input_tokens: null,
        output_tokens: null,
        total_tokens: null,
      },
      cost: null,
    };
    const workspace = parseReviewWorkspace({
      run: { id: "r1", state: "failed", case_id: "c1" },
      audit_events: [],
      execution: {
        schema_version: "audit-execution/v1",
        attempt,
        attempts: [attempt],
        events: [
          {
            seq: 1,
            attempt_id: "a1",
            span_id: null,
            parent_span_id: null,
            node_kind: "model",
            phase: "model",
            event_type: "completed",
            at: null,
            duration_ms: null,
            status: "completed",
            error_code: null,
            model_ref: "group/auto-deepseek-v4-flash",
            usage: {
              source: "openai_response.usage",
              input_tokens: 183,
              output_tokens: 144,
              total_tokens: 327,
            },
          },
          {
            seq: 2,
            attempt_id: "a1",
            span_id: null,
            parent_span_id: null,
            node_kind: "model",
            phase: "model",
            event_type: "failed",
            at: null,
            duration_ms: null,
            status: "failed",
            error_code: "provider_unavailable",
            model_ref: null,
            usage: {
              source: "model_http",
              input_tokens: null,
              output_tokens: null,
              total_tokens: null,
            },
          },
        ],
        usage,
        all_attempts_usage: usage,
        duration_ms: null,
        first_verified_finding_ms: null,
        queue_wait_ms: null,
      },
    });
    expect(workspace.execution?.usage?.input_tokens).toBeNull();
    expect(workspace.execution?.usage?.total_tokens).toBeNull();
    expect(workspace.execution?.all_attempts_usage?.unknown_calls).toBe(2);
    expect(workspace.execution?.events[0]?.at).toBeNull();
    expect(workspace.execution?.events[0]?.duration_ms).toBeNull();
    expect(workspace.execution?.attempt.input_hash).toBeNull();
    expect(workspace.execution?.attempt.parsed_document_ids).toEqual([]);
    expect(workspace.execution?.events[0]?.usage).toEqual({
      source: "openai_response.usage",
      input_tokens: 183,
      output_tokens: 144,
      total_tokens: 327,
    });
    expect(workspace.execution?.events[1]?.usage?.total_tokens).toBeNull();
  });
  it("preserves omitted optional fields on a newly created remediation", () => {
    const remediation = parseRemediation({
      id: "rem-1",
      case_id: "c1",
      run_id: "r1",
      attempt_id: "a1",
      finding_ids: ["f1"],
      measure: "补充可执行的服务期限",
      status: "open",
      version: 1,
      document_ids: [],
      created_at: "2026-10-22T01:00:00Z",
      updated_at: "2026-10-22T01:00:00Z",
    });
    expect(remediation).toMatchObject({
      assignee_id: "",
      due_at: "",
      reason: "",
      verification_run_id: null,
      verification_attempt_id: null,
    });
  });

  it("keeps case summary defaults and rejects missing counts", () => {
    expect(
      parseCaseSummary({ id: "c1", document_count: 0, pending_decisions: 0 }),
    ).toEqual({
      id: "c1",
      title: "未命名案件",
      contract_type: "",
      document_count: 0,
      pending_decisions: 0,
      latest_run: null,
      updated_at: "",
      business_line: undefined,
      scenario: undefined,
      business_status: undefined,
      archived_at: null,
    });
    expect(
      parseCaseSummary({
        id: "c1",
        title: "合同",
        contract_type: "service",
        document_count: 2,
        pending_decisions: 1,
        updated_at: "2026-10-01T00:00:00Z",
        business_line: "ENT",
        scenario: "telecom_dedicated_line",
        business_status: "active",
        archived_at: "2026-10-02T00:00:00Z",
        latest_run: { id: "r1", state: "running" },
      }),
    ).toMatchObject({
      title: "合同",
      latest_run: { id: "r1", state: "running", updated_at: "" },
      archived_at: "2026-10-02T00:00:00Z",
    });
    expect(() => parseCaseSummary({ document_count: 0, pending_decisions: 0 })).toThrow(
      "响应缺少 id",
    );
    expect(() => parseCaseSummary({ id: "c1", pending_decisions: 0 })).toThrow(
      "案件列表缺少数量",
    );
    expect(() => parseCaseSummary({ id: "c1", document_count: 0 })).toThrow(
      "案件列表缺少数量",
    );
    expect(() => parseCaseSummary(null)).toThrow("case不是对象");
  });

  it("parses case members and drops incomplete activity entries", () => {
    const detail = parseCaseDetail({
      ...caseDefaults,
      id: "c1",
      business_line: "ENT",
      scenario: "telecom_dedicated_line",
      business_status: "active",
      archived_at: null,
      members: [
        {
          id: "u1",
          display_name: "审查员",
          role: "reviewer",
          tenant_id: "t1",
          org_id: "o1",
        },
      ],
      activity: [
        { id: "a1", summary: "创建案件", at: "2026-10-01T00:00:00Z" },
        { id: "a2", summary: "缺少时间" },
        { id: "a3" },
        { summary: "缺少标识" },
        null,
        "text",
      ],
    });
    expect(detail.members).toEqual([
      {
        id: "u1",
        display_name: "审查员",
        role: "reviewer",
        tenant_id: "t1",
        org_id: "o1",
      },
    ]);
    expect(detail.activity).toEqual([
      { id: "a1", at: "2026-10-01T00:00:00Z", summary: "创建案件" },
      { id: "a2", at: "", summary: "缺少时间" },
    ]);
    expect(() =>
      parseCaseDetail({
        ...caseDefaults,
        id: "c1",
        business_line: "ENT",
        scenario: "telecom_dedicated_line",
        business_status: "active",
        archived_at: null,
        members: [null],
      }),
    ).toThrow("案件成员不是对象");
    expect(() =>
      parseCaseDetail({
        ...caseDefaults,
        id: "c1",
        business_line: "ENT",
        scenario: "telecom_dedicated_line",
        business_status: "active",
        archived_at: null,
        members: [{ id: "u1" }],
      }),
    ).toThrow("响应缺少 display_name");
    expect(
      parseCaseDetail({
        ...caseDefaults,
        id: "c1",
        business_line: "ENT",
        scenario: "telecom_dedicated_line",
        business_status: "active",
        archived_at: null,
        members: [],
        documents: "none",
      }),
    ).toMatchObject({ documents: [] });
    expect(
      parseCaseDetail({
        ...caseDefaults,
        id: "c1",
        business_line: "ENT",
        scenario: "telecom_dedicated_line",
        business_status: "active",
        archived_at: null,
        members: [],
        activity: "none",
      }),
    ).toMatchObject({ activity: [] });
    const missingBusinessStatus = {
      ...caseDefaults,
      id: "c1",
      business_line: "ENT",
      scenario: "telecom_dedicated_line",
      members: [],
    } as Record<string, unknown>;
    delete missingBusinessStatus.business_status;
    expect(() => parseCaseDetail(missingBusinessStatus)).toThrow(
      "响应缺少 business_status",
    );
    const missingArchive = { ...caseDefaults, id: "c1", business_line: "ENT", scenario: "s", business_status: "active", members: [] } as Record<string, unknown>;
    delete missingArchive.archived_at;
    expect(() => parseCaseDetail(missingArchive)).toThrow("响应缺少 archived_at");
    expect(() =>
      parseCaseDetail({
        ...caseDefaults,
        id: "c1",
        business_line: "ENT",
        scenario: "s",
        business_status: "active",
        members: [],
        archived_at: 3,
      }),
    ).toThrow("archived_at不是字符串或null");
  });

  it("rejects a case without its business record arrays or run summary", () => {
    expect(() =>
      parseCaseDetail({
        id: "c1",
        business_line: "ENT",
        scenario: "s",
        business_status: "active",
        archived_at: null,
        members: [],
      }),
    ).toThrow("案件缺少业务记录数组");
    expect(() =>
      parseCaseDetail({
        ...caseDefaults,
        id: "c1",
        business_line: "ENT",
        scenario: "s",
        business_status: "active",
        members: [],
        archived_at: null,
        latest_run: 7,
      }),
    ).toThrow("latest_run不是对象");
  });

  it("reads evaluations through the review workspace and filters non-string references", () => {
    const workspace = parseReviewWorkspace({
      run: { id: "r1", state: "completed", case_id: "c1" },
      audit_events: [],
      evaluations: [
        {
          id: "e1",
          object_refs: ["LINE-1", 7, null],
          fact_ids: ["fact-1", false],
          finding_id: null,
        },
        {
          id: "e2",
          domain: "technical",
          relation: "conflict",
          rule_version: "1",
          reason: "原因",
          rule_id: "R1",
          capability_id: "C1",
          object_refs: "not-array",
          finding_id: "f1",
          reason_code: "code",
          fact_ids: "not-array",
        },
      ],
    });
    expect(workspace.evaluations?.[0]).toEqual({
      id: "e1",
      domain: "",
      relation: "unknown",
      rule_version: "",
      reason: "",
      rule_id: "",
      capability_id: "",
      object_refs: ["LINE-1"],
      finding_id: null,
      reason_code: "",
      fact_ids: ["fact-1"],
    });
    expect(workspace.evaluations?.[1]).toMatchObject({
      object_refs: [],
      fact_ids: [],
      finding_id: "f1",
    });
    expect(() =>
      parseReviewWorkspace({
        run: { id: "r1", state: "completed", case_id: "c1" },
        audit_events: [],
        evaluations: [null],
      }),
    ).toThrow("evaluation不是对象");
    expect(() =>
      parseReviewWorkspace({
        run: { id: "r1", state: "completed", case_id: "c1" },
        audit_events: [],
        evaluations: [{}],
      }),
    ).toThrow("响应缺少 id");
  });

  it("parses finding severity, decisions and severity source with defaults", () => {
    const finding = parseFinding({
      id: "f1",
      version: 2,
      severity: "high",
      severity_source: {
        policy_id: "p1",
        policy_version: "1",
        clause: "3.1",
        reason: "幅度",
      },
      evidence: [{ id: "e1", status: "unknown-status" }],
      decisions: [{ id: "d1", action: "confirm_risk", reason: "确认" }],
    });
    expect(finding).toMatchObject({
      origin: "unknown",
      domain: "",
      claim: "",
      relation: "unknown",
      confidence: null,
      verification_status: "unknown",
      version: 2,
      severity: "high",
      severity_source: { policy_id: "p1", clause: "3.1" },
      decisions: [{ id: "d1", action: "confirm_risk", reason: "确认", actor: "未知人员", decided_at: "" }],
    });
    expect(finding.evidence[0]?.status).toBe("missing");
    expect(
      parseFinding({
        id: "f2",
        version: 1.5,
        severity: null,
        evidence: "none",
        decisions: "none",
      }),
    ).toMatchObject({ version: null, severity: null, evidence: [], decisions: [] });
    expect(
      parseFinding({ id: "f1", version: "2" }),
    ).toMatchObject({ version: null });
    expect(() =>
      parseFinding({ id: "f1", evidence: [null] }),
    ).toThrow("evidence不是对象");
    expect(() =>
      parseFinding({ id: "f1", decisions: [null] }),
    ).toThrow("decision不是对象");
    expect(() =>
      parseFinding({ id: "f1", severity: 2 }),
    ).toThrow("severity不是字符串或null");
    expect(() =>
      parseFinding({
        id: "f1",
        severity_source: { policy_id: "p1" },
      }),
    ).toThrow("响应缺少 policy_version");
    expect(() => parseFinding({ id: 1 })).toThrow("响应缺少 id");
  });

  it("keeps platform audit events unlinked and parses nested document provenance", () => {
    const document = {
      id: "d1",
      case_id: "c1",
      case_title: "云服务协议",
      role: "draft_contract",
      filename: "云服务协议.txt",
      media_type: "text/plain",
      digest: "original-hash",
      size: 18,
      parse_status: "ready",
      parse_error: null,
      created_at: "2026-10-24T08:00:00Z",
      superseded: false,
      parsed_document: {
        id: "pd1",
        document_id: "d1",
        original_digest: "original-hash",
        parser_id: "text",
        parser_version: "1",
        config_hash: "config-hash",
        normalization_version: "unicode-v1",
        normalized_digest: "normalized-hash",
        ir_digest: "ir-hash",
        artifact_digest: "artifact-hash",
        created_at: "2026-10-24T08:00:01Z",
        status: "ready",
      },
    };
    const snapshot = parseWorkbenchSnapshot(
      {
        schema_version: "workbench/v1",
        generated_at: "2026-10-24T09:00:00Z",
        period: "day",
        dashboard: {
          visible_cases: 0,
          my_pending_tasks: 0,
          overdue_tasks: 0,
          pending_review_cases: 0,
          remediation_cases: 0,
          completed_reviews: 0,
          average_duration_ms: null,
          progress: [],
          risks: [],
        },
        cases: [],
        profiles: [],
        tasks: [],
        relations: [],
        knowledge: [],
        members: [],
        remediations: [],
        reports: [],
        documents: [document],
        runs: [],
        audit: [
          {
            id: "event-platform",
            case_id: null,
            case_title: null,
            actor_id: "actor-1",
            actor_name: "审查员",
            action: "rulepack.published",
            at: "2026-10-24T08:00:00Z",
            summary: "发布规则包",
          },
        ],
        templates: [],
        model: {
          name: "codex/gpt-6-luna",
          ocr_name: "ocr",
          status: "configured",
          label: "真实模型",
        },
        tools: [],
      },
      "day",
    );
    expect(snapshot.audit[0]).toMatchObject({
      case_id: null,
      case_title: null,
      action: "rulepack.published",
    });
    expect(snapshot.documents[0]?.parsed_document).toMatchObject({
      id: "pd1",
      normalized_digest: "normalized-hash",
    });
    expect(snapshot.documents[0]).not.toHaveProperty("parsed_document_id");
    expect(snapshot.documents[0]).not.toHaveProperty("normalized_text_digest");
  });
});
