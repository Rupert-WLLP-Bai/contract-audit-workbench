import {
  act,
  cleanup,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { chooseOption, renderWithAntd as render } from "./test/antd";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { createMemoryRouter, RouterProvider } from "react-router";
import { clearAllDecisionDrafts } from "./hooks/decision-drafts";
import { AdminApp } from "./admin-app";
import { App } from "./app";
import type {
  CaseDetail,
  ConstraintEvaluation,
  ExecutionAttempt,
  Finding,
  MaterialProjection,
  Remediation,
  ReviewDocument,
  ReviewWorkspace,
  RevisionComparison,
} from "./api/types";
// These tests exercise actual routing, fetch adapters, response parsing, query
// invalidation and page interactions. The HTTP service is a stateful fixture;
// database/rule correctness and real browser behavior are separate test layers.
const at = "2026-10-21T06:00:00Z";
const text = "质保期为36个月。";
function doc(
  id: string,
  role: string,
  extra: Partial<ReviewDocument> = {},
): ReviewDocument {
  return {
    id,
    role,
    filename: `${id}.txt`,
    media_type: "text/plain",
    digest: "abc123456789",
    size: 24,
    parse_status: "ready",
    parse_error: null,
    created_at: at,
    parsed_document: null,
    parsed_document_id: null,
    text,
    clauses: [
      {
        id: `${id}-c`,
        heading: "质保条款",
        text_offset: { start: 0, end: 9 },
        page: null,
      },
    ],
    superseded: false,
    ...extra,
  };
}
function finding(): Finding {
  return {
    id: "f1",
    origin: "rule",
    domain: "质保",
    claim: "质保期限不一致",
    relation: "conflict",
    verification_status: "verified",
    confidence: null,
    unverified_reason: null,
    version: 1,
    decisions: [],
    severity: null,
    severity_source: null,
    evidence: [
      {
        id: "a1",
        document_version_id: "d1",
        parsed_document_id: null,
        normalized_digest: null,
        quote: "36个月",
        status: "located",
        page: null,
        text_offset: { start: 4, end: 8 },
        bbox: null,
        mismatch_reason: null,
      },
    ],
  };
}

function evaluation(
  overrides: Partial<ConstraintEvaluation> = {},
): ConstraintEvaluation {
  return {
    id: "evaluation-source",
    domain: "technical",
    relation: "conflict",
    rule_version: "1.0.0",
    reason: "带宽低于采购基线",
    rule_id: "ENT-TECH-BW-001",
    capability_id: "dedicated_line.minimum_bandwidth",
    object_refs: ["LINE-001"],
    finding_id: "f1",
    reason_code: "minimum_bandwidth_below_threshold",
    fact_ids: ["fact-source"],
    ...overrides,
  };
}
function caseOf(id = "c1"): CaseDetail {
  return {
    id,
    title: "接入设备采购",
    contract_type: "procurement_ict",
    created_at: at,
    updated_at: at,
    documents: [],
    materials: [],
    material_versions: [],
    material_selection: {
      version: 1,
      status: "not_applicable",
      document_ids: [],
      candidate_document_ids: [],
      primary_material_id: null,
    },
    latest_run: null,
    finding_counts: null,
    activity: [],
    revision_pending: false,
    subject_context_changed_after_latest_run: false,
    members: [
      {
        id: "u1",
        display_name: "周审查",
        role: "reviewer",
        tenant_id: "tenant-test",
        org_id: "org-test",
      },
    ],
    configuration_pending: false,
    business_line: "ENT",
    scenario: "telecom_dedicated_line",
    business_status: "active",
    generations: [],
    remediations: [],
    reports: [],
    archives: [],
    archived_at: null,
  };
}

class Stream {
  static latest: Stream | null = null;
  static instances: Stream[] = [];
  url: string;
  withCredentials: boolean;
  closed = false;
  readyState = 0;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  close = vi.fn(() => {
    this.closed = true;
  });
  constructor(url: string, init?: { withCredentials?: boolean }) {
    this.url = url;
    this.withCredentials = Boolean(init?.withCredentials);
    Stream.latest = this;
    Stream.instances.push(this);
  }
}

class Service {
  signedIn = true;
  adminSignedIn = true;
  identity = "u1";
  holdNextWorkspaceRead = false;
  releaseHeldWorkspace: (() => void) | null = null;
  role = "admin";
  tenant = "tenant-test";
  org = "org-test";
  cases = new Map<string, CaseDetail>([["c1", caseOf()]]);
  reviews = new Map<string, ReviewWorkspace>();
  requests: {
    key: string;
    input: unknown;
    headers: Headers;
    signal: AbortSignal | null;
  }[] = [];
  failures = new Map<string, number[]>();
  settings = {
    model: { status: "disabled", label: "规则模式" },
    queue: { depth: null, oldest_wait_ms: null },
    redaction_policy: "",
    users: [{ id: "u1", display_name: "审查员甲", role: "reviewer" }],
  };

  fail(key: string, ...codes: number[]) {
    this.failures.set(key, codes);
  }
  actor(audience: "client" | "admin" = "client") {
    return {
      id: this.identity,
      display_name: this.identity === "u1" ? "测试管理员" : "测试用户乙",
      role: this.role,
      tenant_id: this.tenant,
      org_id: this.org,
      audience,
    };
  }
  summary(item: CaseDetail) {
    return {
      ...item,
      document_count: item.documents.length,
      pending_decisions: item.finding_counts?.undecided ?? 0,
    };
  }
  ensureMaterialProjection(item: CaseDetail) {
    if (!item.materials || !item.material_versions || !item.material_selection) return;
    if (item.materials.length > 0 || item.documents.length === 0) return;
    const purposeFor = (role: string) => role === "draft_contract" ? "primary_contract" : role === "procurement" ? "procurement" : role === "award" ? "award" : "supplement";
    item.materials = item.documents.map((document) => ({
      material_id: `mat-${document.id}`,
      role: document.role as "procurement" | "award" | "draft_contract" | "attachment",
      purpose: purposeFor(document.role) as "primary_contract" | "supplement" | "procurement" | "award",
      legacy_status: "native",
      legacy_selected: false,
      selected: true,
      current_document_id: document.id,
      current_version: 1,
    }));
    item.material_versions = item.documents.map((document) => ({
      material_id: `mat-${document.id}`,
      version: 1,
      document_id: document.id,
      previous_document_id: null,
      created_at: document.created_at,
    }));
    const documentIds = item.documents.map((document) => document.id);
    item.material_selection = {
      version: item.material_selection.version,
      status: "confirmed",
      document_ids: documentIds,
      candidate_document_ids: [],
      primary_material_id: item.materials.find((material) => material.purpose === "primary_contract")?.material_id ?? null,
    };
  }
  addReview(state = "awaiting_human", id = "r1", caseId = "c1") {
    const item = this.cases.get(caseId)!;
    const docs = item.documents.length
      ? (item.documents as ReviewDocument[])
      : [doc("d1", "procurement"), doc("d2", "draft_contract")];
    item.documents = docs;
    item.latest_run = { id, state, updated_at: at };
    item.finding_counts = {
      total: 1,
      conflict: 1,
      insufficient: 0,
      undecided: 1,
    };
    const finished = [
      "awaiting_human",
      "partial",
      "completed",
      "failed",
      "cancelled",
      "interrupted",
    ].includes(state);
    const attempt: ExecutionAttempt = {
      id: `attempt-${id}-1`,
      run_id: id,
      ordinal: 1,
      retry_of_attempt_id: null,
      parsed_document_ids: [],
      state,
      accepted_at: at,
      started_at: at,
      finished_at: finished ? at : null,
      input_hash: null,
      output_hash: null,
      error_code: null,
      history_kind: "frozen",
    };
    const review: ReviewWorkspace = {
      run: {
        ...item.latest_run,
        case_id: caseId,
        started_at: at,
        model_ref: null,
        partial_reason: null,
        failure_message: null,
      },
      documents: docs.filter((entry) => !entry.superseded),
      findings: [finding()],
      review_attempts: { attempt, attempts: [attempt] },
      execution: {
        schema_version: "audit-execution/v1",
        attempt,
        attempts: [attempt],
        events: [],
        agent_review: null,
        usage: null,
        all_attempts_usage: null,
        duration_ms: null,
        first_verified_finding_ms: null,
        queue_wait_ms: null,
      },
      snapshot: {
        document_ids: docs
          .filter((entry) => !entry.superseded)
          .map((entry) => entry.id),
        rule_set_hash: "rules-sha256",
        playbook_hash: "playbook-sha256",
        model_ref: null,
        comparator_ref: "typed-constraint-v1",
      },
      audit_events: [],
      revisions: [],
      previous_run_id: null,
    };
    this.reviews.set(id, review);
    return review;
  }

  handle = async (
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> => {
    const url = new URL(String(input), "http://service.test");
    const path = url.pathname;
    const method = init?.method ?? "GET";
    const key = `${method} ${path}${url.search}`;
    const body: unknown =
      typeof init?.body === "string" ? JSON.parse(init.body) : init?.body;
    this.requests.push({
      key,
      input: body,
      headers: new Headers(init?.headers),
      signal: init?.signal ?? null,
    });
    const queued = this.failures.get(key);
    const fail = queued?.shift();
    if (fail)
      return this.json(
        {
          error: {
            message: `受控失败 ${fail}`,
            code: fail === 409 ? "conflict" : "test_failure",
          },
        },
        fail,
      );
    if (path === "/api/session") {
      const credentials = body as {
        username?: string;
        password?: string;
        audience?: string;
      } | null;
      const audience =
        url.searchParams.get("audience") === "admin" ||
        credentials?.audience === "admin"
          ? "admin"
          : "client";
      if (method === "DELETE") {
        if (audience === "admin") this.adminSignedIn = false;
        else this.signedIn = false;
        return new Response(null, { status: 204 });
      }
      if (method === "POST") {
        if (
          credentials?.username !== "demo-admin" ||
          credentials.password !== "admin-demo"
        )
          return this.json({ error: "账号密码不正确" }, 401);
        if (audience === "admin") this.adminSignedIn = true;
        else this.signedIn = true;
      }
      return (audience === "admin" ? this.adminSignedIn : this.signedIn)
        ? this.json(this.actor(audience))
        : this.json({ error: "请登录" }, 401);
    }
    if (path.startsWith("/api/admin/") ? !this.adminSignedIn : !this.signedIn)
      return this.json({ error: "请登录" }, 401);
    if (path === "/api/settings" || path === "/api/admin/settings") {
      const attempts = [...this.reviews.values()].flatMap(
        (review) => review.execution?.attempts ?? [],
      );
      const usage = [...this.reviews.values()]
        .map((review) => review.execution?.all_attempts_usage)
        .filter((value) => value !== null && value !== undefined);
      const execution = {
        total_attempts: attempts.length,
        running_attempts: attempts.filter((attempt) =>
          ["accepted", "queued", "started", "running"].includes(attempt.state),
        ).length,
        completed_attempts: attempts.filter(
          (attempt) =>
            attempt.state === "completed" || attempt.state === "awaiting_human",
        ).length,
        partial_attempts: attempts.filter(
          (attempt) => attempt.state === "partial",
        ).length,
        failed_attempts: attempts.filter(
          (attempt) => attempt.state === "failed",
        ).length,
        cancelled_attempts: attempts.filter(
          (attempt) => attempt.state === "cancelled",
        ).length,
        model_calls: usage.reduce((total, item) => total + item.calls, 0),
        unknown_usage_calls: usage.reduce(
          (total, item) => total + item.unknown_calls,
          0,
        ),
        queue_depth: null,
      };
      return this.json(
        this.role === "admin" ? { ...this.settings, execution } : this.settings,
      );
    }
    if (path === "/api/templates" && method === "GET")
      return this.json({ items: [] });
    if (path === "/api/cases") {
      if (method === "GET")
        return this.json({
          items: [...this.cases.values()].map((item) => this.summary(item)),
        });
      const submitted = body as { title: string; contract_type: string };
      const id = `c${this.cases.size + 1}`;
      this.cases.set(id, { ...caseOf(id), ...submitted });
      return this.json({ id }, 201);
    }
    const parts = path.split("/");
    const caseId = parts[3]!;
    const item = this.cases.get(caseId);
    if (!item) return this.json({ error: "案件不存在" }, 404);
    if (parts.length === 4) {
      this.ensureMaterialProjection(item);
      return this.json(item);
    }
    if (parts[4] === "documents" && parts.length === 5 && method === "POST") {
      this.ensureMaterialProjection(item);
      const form = init?.body as FormData;
      const role = String(form.get("role"));
      const intent = String(form.get("intent"));
      const purpose = String(form.get("purpose") ?? (role === "draft_contract" ? "primary_contract" : role === "procurement" ? "procurement" : role === "award" ? "award" : "supplement"));
      const file = form.get("file") as File;
      if (role === "draft_contract" && intent === "add" && item.materials!.some((material) => material.purpose === "primary_contract")) {
        return this.json({ error: { message: "此案已有主合同，请明确替换或另建案件", code: "primary_contract_exists" } }, 409);
      }
      const id = `doc-${item.documents.length + 1}`;
      const materialId = String(form.get("material_id") ?? `mat-${id}`);
      const target = intent === "replace" ? item.materials!.find((material) => material.material_id === materialId) : undefined;
      if (intent === "replace" && (!target || target.current_document_id !== form.get("replaces_document_id"))) {
        return this.json({ error: { message: "材料版本已变化", code: "conflict" } }, 409);
      }
      const sourceDoc = doc(id, role, {
        filename: file.name,
        media_type: file.type,
        size: file.size,
        parse_status: "pending",
        text: null,
        clauses: [],
      });
      item.documents.push(sourceDoc);
      let version = 1;
      if (target) {
        const previousId = target.current_document_id;
        version = target.current_version + 1;
        target.current_document_id = id;
        target.current_version = version;
        item.documents.find((document) => document.id === previousId)!.superseded = true;
        item.material_versions!.push({ material_id: target.material_id, version, document_id: id, previous_document_id: previousId, created_at: at });
        if (item.material_selection!.document_ids.includes(previousId)) {
          item.material_selection!.document_ids = item.material_selection!.document_ids.map((documentId) => documentId === previousId ? id : documentId);
        }
      } else {
        item.materials!.push({ material_id: materialId, role: role as "procurement" | "award" | "draft_contract" | "attachment", purpose: purpose as "primary_contract" | "supplement" | "procurement" | "award" | "acceptance" | "invoice" | "correspondence" | "other", legacy_status: "native", legacy_selected: false, selected: true, current_document_id: id, current_version: 1 });
        item.material_versions!.push({ material_id: materialId, version: 1, document_id: id, previous_document_id: null, created_at: at });
        item.material_selection!.document_ids.push(id);
        item.material_selection!.status = "confirmed";
        if (purpose === "primary_contract") item.material_selection!.primary_material_id = materialId;
      }
      item.revision_pending = true;
      return this.json({ id, material_id: materialId, version, replayed: false }, 201);
    }
    if (parts[4] === "materials" && parts[5] === "selection" && method === "POST") {
      this.ensureMaterialProjection(item);
      const submitted = body as { version: number; document_ids: string[] };
      if (submitted.version !== item.material_selection!.version) return this.json({ error: { message: "材料选择已变化", code: "conflict" } }, 409);
      const allowed = new Set(item.materials!.map((material) => material.current_document_id));
      if (!submitted.document_ids.every((id) => allowed.has(id))) return this.json({ error: { message: "材料不属于此案件", code: "invalid_material_selection" } }, 400);
      item.material_selection!.version += 1;
      item.material_selection!.status = "confirmed";
      item.material_selection!.document_ids = [...submitted.document_ids];
      item.material_selection!.candidate_document_ids = [];
      item.materials!.forEach((material) => { material.selected = submitted.document_ids.includes(material.current_document_id); });
      return this.json(item.material_selection);
    }
    if (parts[4] === "evidence-requests" && method === "GET")
      return this.json({ items: [] });
    if (parts[4] === "remediations" && parts.length === 5 && method === "POST") {
      const submitted = body as Pick<Remediation, "run_id" | "attempt_id" | "finding_ids" | "assignee_id" | "due_at" | "measure">;
      const remediation: Remediation = {
        ...submitted,
        id: "rem-created",
        case_id: caseId,
        status: "open",
        version: 1,
        document_ids: [],
        reason: "",
        verification_run_id: null,
        verification_attempt_id: null,
        created_at: at,
        updated_at: at,
      };
      item.remediations.push(remediation);
      return this.json(remediation, 201);
    }
    if (parts[4] === "documents") {
      if (parts[6] === "parse") {
        const target = item.documents.find((entry) => entry.id === parts[5])!;
        target.parse_status = "ready";
        target.parse_error = null;
        return new Response(null, { status: 204 });
      }
      const form = body as FormData;
      const role = String(form.get("role"));
      const file = form.get("file") as File;
      for (const previous of item.documents)
        if (previous.role === role) previous.superseded = true;
      item.documents.push(
        doc(`d${item.documents.length + 1}`, role, { filename: file.name }),
      );
      item.revision_pending = item.latest_run !== null;
      return this.json({ id: `d${item.documents.length}` }, 201);
    }
    if (parts[4] === "reviews") {
      if (parts.length === 5 && method === "POST") {
        const start = body as { business_at?: string } | null;
        const governed = item.business_line === "ENT" &&
          (item.scenario === "telecom_dedicated_line" ||
            (item.scenario === "cloud_services" && item.contract_type === "service"));
        if (governed && !start?.business_at)
          return this.json({ error: "必须选择业务时间" }, 422);
        const id = `r${this.reviews.size + 1}`;
        const review = this.addReview("awaiting_human", id, caseId);
        item.revision_pending = false;
        item.configuration_pending = false;
        item.subject_context_changed_after_latest_run = false;
        return this.json({ id, attempt_id: review.execution!.attempt.id }, 202);
      }
      const review = this.reviews.get(parts[5]!);
      if (!review) return this.json({ error: "审查不存在" }, 404);
      if (parts[6] === "retry") {
        const execution = review.execution!;
        const previous = execution.attempt;
        const ordinal =
          execution.attempts.reduce(
            (maximum, attempt) => Math.max(maximum, attempt.ordinal),
            0,
          ) + 1;
        const attempt: ExecutionAttempt = {
          ...previous,
          id: `attempt-${review.run.id}-${ordinal}`,
          ordinal,
          retry_of_attempt_id: previous.id,
          state: "awaiting_human",
          accepted_at: at,
          started_at: at,
          finished_at: at,
        };
        review.execution = {
          ...execution,
          attempt,
          attempts: [...execution.attempts, attempt],
        };
        review.run.state = attempt.state;
        item.latest_run = { ...item.latest_run!, state: attempt.state };
        return this.json({ id: review.run.id, attempt_id: attempt.id }, 202);
      }
      if (parts[6] === "cancel") {
        const execution = review.execution!;
        const current = execution.attempt;
        if (
          ["accepted", "queued", "started", "running"].includes(current.state)
        ) {
          const cancelled = { ...current, state: "cancelled", finished_at: at };
          review.execution = {
            ...execution,
            attempt: cancelled,
            attempts: execution.attempts.map((attempt) =>
              attempt.id === current.id ? cancelled : attempt,
            ),
          };
          review.run.state = "cancelled";
          if (item.latest_run?.id === review.run.id)
            item.latest_run = { ...item.latest_run, state: "cancelled" };
        }
        return new Response(null, { status: 204 });
      }
      if (parts[6] === "findings") {
        const submitted = body as {
          action: string;
          reason: string;
          expected_version: number;
        };
        const target = review.findings.find((entry) => entry.id === parts[7])!;
        if (submitted.expected_version !== target.version)
          return this.json({ error: "版本冲突" }, 409);
        target.decisions.push({
          id: `decision-${target.decisions.length + 1}`,
          action: submitted.action,
          reason: submitted.reason,
          actor: "测试管理员",
          decided_at: at,
        });
        target.version = (target.version ?? 0) + 1;
        const execution = review.execution!;
        const completed = {
          ...execution.attempt,
          state: "completed",
          finished_at: at,
        };
        review.execution = {
          ...execution,
          attempt: completed,
          attempts: execution.attempts.map((attempt) =>
            attempt.id === completed.id ? completed : attempt,
          ),
        };
        review.run.state = "completed";
        item.latest_run = { ...item.latest_run!, state: "completed" };
        item.finding_counts!.undecided = 0;
        return new Response(null, { status: 204 });
      }
      const requestedAttempt = url.searchParams.get("attempt_id");
      const execution = review.execution;
      const attempts = execution?.attempts ?? [];
      const selectedAttempt = !execution ? null : requestedAttempt === null
        ? attempts.reduce(
            (latest, attempt) => attempt.ordinal > latest.ordinal ? attempt : latest,
            execution.attempt,
          )
        : attempts.find((attempt) => attempt.id === requestedAttempt);
      if (execution && !selectedAttempt) return this.json({ error: "审查尝试不存在" }, 404);
      if (parts[6] === "diagnostics") {
        if (this.role !== "admin") return this.json({ error: "需要平台管理权限" }, 403);
        return this.json({
          case_id: caseId,
          run_id: review.run.id,
          execution: execution && selectedAttempt ? { ...execution, attempt: selectedAttempt } : null,
          snapshot: review.snapshot ?? null,
          audit_events: review.audit_events,
          documents: [],
          evidence: [],
        });
      }
      const payload = structuredClone(review);
      if (this.identity !== "u1" && payload.findings[0])
        payload.findings[0].claim = "乙账号的专属线索";
      const businessAttempt = (attempt: ExecutionAttempt) => ({
        id: attempt.id,
        run_id: attempt.run_id,
        ordinal: attempt.ordinal,
        retry_of_attempt_id: attempt.retry_of_attempt_id,
        state: attempt.state,
        accepted_at: attempt.accepted_at,
        started_at: attempt.started_at,
        finished_at: attempt.finished_at,
        parsed_document_ids: attempt.parsed_document_ids,
        history_kind: attempt.history_kind,
      });
      const publicPayload = {
        ...payload,
        execution: null,
        review_attempts: selectedAttempt ? {
          attempt: businessAttempt(selectedAttempt),
          attempts: attempts.map(businessAttempt),
        } : null,
        snapshot: payload.snapshot ? { document_ids: payload.snapshot.document_ids } : null,
        audit_events: [],
        business_events: payload.business_events ?? [],
      };
      if (this.holdNextWorkspaceRead) {
        this.holdNextWorkspaceRead = false;
        const gate = Promise.withResolvers<Response>();
        this.releaseHeldWorkspace = () => gate.resolve(this.json(publicPayload));
        return gate.promise;
      }
      return this.json(publicPayload);
    }
    throw new Error(`Unhandled UI request: ${key}`);
  };
  json(value: unknown, status = 200) {
    return new Response(JSON.stringify(value), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  }
}

function addSubmittedRemediation(
  api: Service,
  candidateEvaluations: ConstraintEvaluation[],
  comparisonReason: string | null = null,
) {
  const item = api.cases.get("c1")!;
  item.documents = [
    doc("source-doc", "procurement"),
    doc("draft-doc", "draft_contract"),
  ];
  const source = api.addReview("completed", "r1");
  source.evaluations = [evaluation()];
  const candidate = api.addReview("completed", "r2");
  candidate.previous_run_id = "r1";
  candidate.evaluations = candidateEvaluations;
  candidate.revision_comparison_reason = comparisonReason;
  const resolvedPresentation: RevisionComparison = {
    mark: "消除",
    finding_id: null,
    previous_finding_id: "f1",
    domain: "technical",
    relation: "conflict",
    claim: "原始风险未在新稿继续出现。",
    prior_decision: null,
  };
  candidate.revisions = [resolvedPresentation];
  const remediation: Remediation = {
    id: "rem1",
    case_id: "c1",
    run_id: "r1",
    attempt_id: source.execution!.attempt.id,
    finding_ids: ["f1"],
    assignee_id: "u1",
    due_at: "2027-01-01",
    measure: "修订合同带宽",
    status: "submitted",
    version: 2,
    document_ids: ["draft-doc"],
    reason: "已提交修改稿",
    verification_run_id: null,
    verification_attempt_id: null,
    created_at: at,
    updated_at: at,
  };
  item.remediations = [remediation];
}

const clients: QueryClient[] = [];
function open(
  service: Service,
  path = "/cases",
  application: ReactNode = <App />,
) {
  vi.stubGlobal("fetch", service.handle);
  vi.stubGlobal("EventSource", Stream);
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  const router = createMemoryRouter(
    [{ path: "*", element: application }],
    { initialEntries: [path] },
  );
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { ...view, user: userEvent.setup(), client };
}
afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  Stream.latest = null;
  Stream.instances = [];
  clearAllDecisionDrafts();
});

function selectBusinessTime() {
  fireEvent.change(screen.getByLabelText("审查业务时间（本地时区）"), {
    target: { value: "2026-10-21T08:00" },
  });
}

describe("application routes and API contract", () => {
  it("confirms the server-provided legacy material candidates before opening a new review", async () => {
    const api = new Service();
    const item = api.cases.get("c1")!;
    item.documents = [doc("legacy-a", "procurement", { filename: "旧采购材料.txt" }), doc("native-b", "attachment", { filename: "新增补充材料.txt" })];
    item.materials = [
      { material_id: "mat-legacy", role: "procurement", purpose: "procurement", legacy_status: "pending_confirmation", legacy_selected: true, selected: false, current_document_id: "legacy-a", current_version: 1 },
      { material_id: "mat-native", role: "attachment", purpose: "supplement", legacy_status: "native", legacy_selected: false, selected: false, current_document_id: "native-b", current_version: 1 },
    ] satisfies MaterialProjection[];
    item.material_versions = [
      { material_id: "mat-legacy", version: 1, document_id: "legacy-a", previous_document_id: null, created_at: at },
      { material_id: "mat-native", version: 1, document_id: "native-b", previous_document_id: null, created_at: at },
    ];
    item.material_selection = {
      version: 8,
      status: "pending_confirmation",
      document_ids: [],
      candidate_document_ids: ["legacy-a", "native-b"],
      primary_material_id: null,
    };
    const { user } = open(api, "/cases/c1");
    await screen.findByText("新增补充材料.txt");
    selectBusinessTime();
    expect(screen.getByRole("button", { name: "开始审查" })).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: /旧采购材料/ })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: /新增补充材料/ })).toBeChecked();
    await user.click(screen.getByRole("button", { name: "确认本次材料集合" }));
    await waitFor(() => expect(item.material_selection?.status).toBe("confirmed"));
    const confirmation = api.requests.find((entry) => entry.key === "POST /api/cases/c1/materials/selection");
    expect(confirmation?.input).toEqual({ version: 8, document_ids: ["legacy-a", "native-b"] });
    expect(confirmation?.headers.has("If-Match")).toBe(false);
    expect(confirmation?.headers.has("Idempotency-Key")).toBe(false);
    await waitFor(() => expect(screen.getByRole("button", { name: "开始审查" })).toBeEnabled());
  });

  it("logs in, creates a case, uploads source versions, reviews, decides, and logs out", async () => {
    const api = new Service();
    api.signedIn = false;
    api.cases.clear();
    const { user } = open(api, "/");
    expect(
      await screen.findByRole("heading", { name: "登录合同审查" }),
    ).toBeInTheDocument();
    await user.type(screen.getByLabelText("账号"), "demo-admin");
    await user.type(screen.getByLabelText("密码"), "wrong");
    await user.click(screen.getByRole("button", { name: "登录" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "账号密码不正确",
    );
    await user.clear(screen.getByLabelText("密码"));
    await user.type(screen.getByLabelText("密码"), "admin-demo");
    await user.click(screen.getByRole("button", { name: "登录" }));
    expect(await screen.findByRole("heading", { name: "案件", level: 1 })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "新建案件" }));
    await user.type(screen.getByLabelText("标题"), " 新设备采购 ");
    await chooseOption(user, screen.getByLabelText("合同类型"), "服务合同");
    await user.click(screen.getByRole("button", { name: "创建" }));
    expect(
      await screen.findByRole("heading", { name: "新设备采购" }),
    ).toBeInTheDocument();
    expect(
      api.requests.find((entry) => entry.key === "POST /api/cases")?.input,
    ).toEqual({
      title: "新设备采购",
      contract_type: "service",
      business_line: "ENT",
      scenario: "telecom_dedicated_line",
    });
    expect(screen.getByRole("button", { name: "开始审查" })).toBeDisabled();
    selectBusinessTime();
    await user.upload(
      screen.getByLabelText("文件"),
      new File([text], "采购.txt", { type: "text/plain" }),
    );
    await user.click(screen.getByRole("button", { name: "新增材料" }));
    expect(await screen.findAllByText("采购.txt")).not.toHaveLength(0);
    await chooseOption(user, screen.getByLabelText("材料角色"), "拟签合同");
    await user.upload(
      screen.getByLabelText("文件"),
      new File(["质保24个月"], "合同.txt", { type: "text/plain" }),
    );
    await user.click(screen.getByRole("button", { name: "新增材料" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "开始审查" })).toBeEnabled(),
    );
    await user.click(screen.getByRole("button", { name: "开始审查" }));
    await waitFor(() => expect(api.requests.find((entry) => entry.key === "POST /api/cases/c1/reviews")?.input)
      .toEqual({ business_at: new Date(2026, 9, 21, 8, 0).toISOString() }));
    expect(
      await screen.findByRole("heading", { name: "审查工作区" }),
    ).toBeInTheDocument();
    expect(
      api.requests.some(
        (entry) =>
          entry.key === "GET /api/cases/c1/reviews/r1?attempt_id=attempt-r1-1",
      ),
    ).toBe(true);
    expect(
      screen.getByText("质保期限不一致", { selector: "h3" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "加载运行轨迹与诊断" }));
    await user.click(
      screen.getByRole("button", { name: "运行依据与规则评估" }),
    );
    expect(await screen.findByText("rules-sha256")).toBeVisible();
    await user.click(screen.getByRole("radio", { name: "确认风险" }));
    await user.type(
      screen.getByLabelText("理由"),
      "逐项核对两份原文，期限确有差异",
    );
    await user.click(screen.getByRole("button", { name: "保存决定" }));
    expect(
      await screen.findByText("逐项核对两份原文，期限确有差异"),
    ).toBeInTheDocument();
    const submitted = api.requests.find((entry) =>
      entry.key.endsWith("/findings/f1/decisions"),
    )!;
    expect(submitted.input).toEqual({
      action: "confirm_risk",
      reason: "逐项核对两份原文，期限确有差异",
      expected_version: 1,
    });
    expect(submitted.headers.get("Idempotency-Key")).toMatch(/^[\w-]{20,}$/);
    await user.click(screen.getByRole("button", { name: "退出" }));
    expect(
      await screen.findByRole("heading", { name: "登录合同审查" }),
    ).toBeInTheDocument();
    expect(api.signedIn).toBe(false);
  }, 30_000);

  it.each([
    ["/cases", "GET /api/session", "无法连接审查服务", "案件"],
    ["/cases", "GET /api/cases", "案件列表没有加载", "案件"],
    ["/cases/c1", "GET /api/cases/c1", "案件没有加载", "接入设备采购"],
    [
      "/cases/c1/reviews/r1",
      "GET /api/cases/c1/reviews/r1",
      "审查工作区没有加载",
      "审查工作区",
    ],
  ])(
    "recovers %s after a failed request without hiding the failure",
    async (path, request, title, recovered) => {
      const api = new Service();
      api.addReview();
      api.fail(request, 503);
      const { user } = open(api, path);
      expect(
        await screen.findByRole("heading", { name: title }),
      ).toBeInTheDocument();
      expect(screen.getByText("受控失败 503")).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "重试" }));
      expect(
        await screen.findByRole("heading", { name: recovered, level: 1 }),
      ).toBeInTheDocument();
    },
  );
  it.each([
    ["/cases", "GET /api/cases", "没有权限查看案件列表"],
    ["/cases/c1", "GET /api/cases/c1", "没有权限查看这个案件"],
    [
      "/cases/c1/reviews/r1",
      "GET /api/cases/c1/reviews/r1",
      "没有权限查看这次审查",
    ],
  ])(
    "shows %s as an authorisation outcome instead of a retryable failure",
    async (path, request, title) => {
      const api = new Service();
      api.addReview();
      api.fail(request, 403);
      open(api, path);
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent(title);
      expect(
        within(alert).queryByRole("button", { name: "重试" }),
      ).not.toBeInTheDocument();
    },
  );
  it("recovers admin settings through its isolated audience and API", async () => {
    const api = new Service();
    api.fail("GET /api/admin/settings", 503);
    const { user } = open(api, "/admin/settings", <AdminApp />);
    expect(
      await screen.findByRole("heading", { name: "设置没有加载" }),
    ).toBeInTheDocument();
    expect(screen.getByText("受控失败 503")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(
      await screen.findByRole("heading", { name: "设置", level: 1 }),
    ).toBeInTheDocument();
    expect(
      api.requests.some(
        (entry) => entry.key === "GET /api/session?audience=admin",
      ),
    ).toBe(true);
    expect(
      api.requests.some((entry) => entry.key === "GET /api/admin/settings"),
    ).toBe(true);
  });

  it("permits another version after four historical files and starts an explicit re-review", async () => {
    const api = new Service();
    const item = api.cases.get("c1")!;
    item.documents = [
      doc("old1", "procurement", { superseded: true, parse_status: "failed" }),
      doc("d1", "procurement"),
      doc("d2", "draft_contract"),
      doc("d3", "award"),
      doc("d4", "attachment"),
    ];
    api.addReview();
    const { user } = open(api, "/cases/c1");
    expect(await screen.findByRole("heading", { name: "材料版本历史" })).toBeInTheDocument();
    expect(
      await screen.findByText(/有材料解析失败。文件仍保留/),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("文件")).toBeEnabled();
    await chooseOption(user, screen.getByLabelText("材料角色"), "拟签合同");
    await chooseOption(user, screen.getByLabelText("材料操作"), "替换选中的材料");
    await user.upload(
      screen.getByLabelText("文件"),
      new File([text], "修改稿.txt", { type: "text/plain" }),
    );
    await user.click(screen.getByRole("button", { name: "替换材料" }));
    expect(await screen.findAllByText("修改稿.txt")).not.toHaveLength(0);
    expect(api.cases.get("c1")!.materials?.find((material) => material.purpose === "primary_contract")).toMatchObject({
      material_id: "mat-d2",
      current_document_id: "doc-6",
      current_version: 2,
    });
    expect(api.cases.get("c1")!.documents.find((document) => document.id === "d2")?.superseded).toBe(true);
    expect(api.cases.get("c1")!.revision_pending).toBe(true);
    await waitFor(() => expect(screen.getByLabelText("审查业务时间（本地时区）")).toBeInTheDocument());
    selectBusinessTime();
    const reReview = await screen.findByRole("button", { name: "开始复审" });
    expect(reReview).toBeEnabled();
    await user.click(reReview);
    expect(
      await screen.findByRole("heading", { name: "审查工作区" }),
    ).toBeInTheDocument();
    expect(api.reviews.size).toBe(2);
    expect(api.cases.get("c1")!.revision_pending).toBe(false);
  });

  it("starts review with pending image input without requiring a prior OCR read", async () => {
    const api = new Service();
    const item = api.cases.get("c1")!;
    item.documents = [
      doc("d1", "procurement"),
      doc("image", "draft_contract", {
        filename: "待转写.png",
        media_type: "image/png",
        parse_status: "pending",
        text: null,
        clauses: [],
      }),
    ];
    const { user } = open(api, "/cases/c1");
    expect(await screen.findByText("待转写.png")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "开始审查" })).toBeDisabled();
    selectBusinessTime();
    const startReview = screen.getByRole("button", { name: "开始审查" });
    expect(startReview).toBeEnabled();
    await user.click(startReview);
    expect(
      await screen.findByRole("heading", { name: "审查工作区" }),
    ).toBeInTheDocument();
    expect(api.reviews.size).toBe(1);
  });

  it("keeps failed parsing visible, handles upload/reparse/start errors and only then enables review", async () => {
    const api = new Service();
    const item = api.cases.get("c1")!;
    item.documents = [
      doc("d1", "procurement"),
      doc("d2", "draft_contract", {
        parse_status: "failed",
        parse_error: "无法解码",
        text: null,
      }),
    ];
    api.fail("POST /api/cases/c1/documents/d2/parse", 503);
    api.fail("POST /api/cases/c1/reviews", 503);
    api.fail("POST /api/cases/c1/documents", 413);
    const { user } = open(api, "/cases/c1");
    expect(await screen.findByText("无法解码")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "开始审查" })).toBeDisabled();
    selectBusinessTime();
    await user.click(screen.getByRole("button", { name: "重新解析 d2.txt" }));
    expect(await screen.findByText("受控失败 503")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重新解析 d2.txt" }));
    await waitFor(() => expect(item.documents.find((document) => document.id === "d2")?.parse_status).toBe("ready"));
    expect(item.material_selection).toMatchObject({ status: "confirmed", document_ids: ["d1", "d2"] });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "开始审查" })).toBeEnabled(),
    );
    await user.click(screen.getByRole("button", { name: "开始审查" }));
    expect(await screen.findByText("受控失败 503")).toBeInTheDocument();
    await user.upload(
      screen.getByLabelText("文件"),
      new File([text], "超限.txt", { type: "text/plain" }),
    );
    await user.click(screen.getByRole("button", { name: "新增材料" }));
    expect(await screen.findByText("受控失败 413")).toBeInTheDocument();
    expect(item.documents).toHaveLength(2);
  });

  it("starts a new review after a configuration change without requiring another upload", async () => {
    const api = new Service();
    api.addReview();
    const item = api.cases.get("c1")!;
    item.configuration_pending = true;
    const originalIds = item.documents.map((entry) => entry.id);
    const { user } = open(api, "/cases/c1");
    expect(await screen.findByText(/规则或模型配置已更新/)).toBeInTheDocument();
    selectBusinessTime();
    await user.click(screen.getByRole("button", { name: "开始复审" }));
    expect(
      await screen.findByRole("heading", { name: "审查工作区" }),
    ).toBeInTheDocument();
    expect(api.reviews.size).toBe(2);
    expect(item.documents.map((entry) => entry.id)).toEqual(originalIds);
    expect(item.configuration_pending).toBe(false);
    expect(
      api.requests.filter(
        (entry) => entry.key === "POST /api/cases/c1/documents",
      ),
    ).toHaveLength(0);
  });

  it("starts a new review when the server reports changed subject context and clears the notice after freezing it", async () => {
    const api = new Service();
    api.addReview();
    const item = api.cases.get("c1")!;
    item.subject_context_changed_after_latest_run = true;
    const originalIds = item.documents.map((entry) => entry.id);
    const { user } = open(api, "/cases/c1");

    expect(await screen.findByText(/主体信息已变更，需发起新审查/)).toBeInTheDocument();
    selectBusinessTime();
    await user.click(screen.getByRole("button", { name: "开始复审" }));
    expect(await screen.findByRole("heading", { name: "审查工作区" })).toBeInTheDocument();
    expect(api.reviews.size).toBe(2);
    expect(item.subject_context_changed_after_latest_run).toBe(false);
    expect(item.documents.map((entry) => entry.id)).toEqual(originalIds);
  });

  it("does not interpret a missing subject-context status as no change", async () => {
    const api = new Service();
    api.addReview();
    const item = api.cases.get("c1")!;
    delete item.subject_context_changed_after_latest_run;
    open(api, "/cases/c1");
    expect(await screen.findByText(/无法确认当前主体信息是否与最近审查一致/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "主体状态暂不可用" })).toBeDisabled();
  });

  it("refreshes a stale decision, retains the entered reason, and submits the refreshed version", async () => {
    const api = new Service();
    const review = api.addReview();
    const { user } = open(api, "/cases/c1/reviews/r1");
    await screen.findByRole("heading", { name: "审查工作区" });
    await user.click(screen.getByRole("radio", { name: "误报" }));
    await user.type(screen.getByLabelText("理由"), "重新核查引用的对象范围");
    review.findings[0]!.version = 2;
    await user.click(screen.getByRole("button", { name: "保存决定" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "该事项已被他人更新",
    );
    expect(screen.getByLabelText("理由")).toHaveValue("重新核查引用的对象范围");
    await user.click(screen.getByRole("button", { name: "保存决定" }));
    expect(
      await screen.findByText("重新核查引用的对象范围"),
    ).toBeInTheDocument();
    const posts = api.requests.filter((entry) =>
      entry.key.endsWith("/decisions"),
    );
    expect(
      posts.map(
        (entry) =>
          (entry.input as { expected_version: number }).expected_version,
      ),
    ).toEqual([1, 2]);
    expect(review.findings[0]!.decisions).toHaveLength(1);
  });

  it("shows model failure, retries, and displays ordinary decision errors separately from conflict", async () => {
    const api = new Service();
    const review = api.addReview("failed");
    review.run.failure_message = "模型调用超时";
    const firstAttempt = review.execution!.attempt;
    review.execution!.events = [
      {
        seq: 1,
        attempt_id: firstAttempt.id,
        span_id: "rules",
        parent_span_id: null,
        node_kind: "rules",
        phase: "rules",
        event_type: "failed",
        at: null,
        duration_ms: null,
        status: "failed",
        error_code: "model_timeout",
        model_ref: null,
        usage: null,
      },
    ];
    api.fail("POST /api/cases/c1/reviews/r1/retry", 503);
    api.fail("POST /api/cases/c1/reviews/r1/findings/f1/decisions", 503);
    const { user } = open(api, "/cases/c1/reviews/r1");
    expect(await screen.findByText("模型调用超时")).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "重新执行未完成的审查" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("受控失败 503");
    await user.click(
      screen.getByRole("button", { name: "重新执行未完成的审查" }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "重新执行未完成的审查" }),
      ).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(review.execution!.attempts).toHaveLength(2));
    expect(review.execution!.attempts.map((attempt) => attempt.id)).toEqual([
      firstAttempt.id,
      "attempt-r1-2",
    ]);
    expect(review.execution!.attempts[0]).toEqual(firstAttempt);
    expect(review.execution!.attempts[1]?.retry_of_attempt_id).toBe(
      firstAttempt.id,
    );
    expect(review.execution!.events[0]?.attempt_id).toBe(firstAttempt.id);
    await waitFor(() => expect(api.requests.some((entry) => entry.key === "GET /api/cases/c1/reviews/r1?attempt_id=attempt-r1-2")).toBe(true));
    await chooseOption(user, screen.getByLabelText("查看执行记录"), /第 2 次，\s*待人工，\s*最新/);
    await user.click(await screen.findByRole("radio", { name: "证据不足" }));
    expect(screen.getByRole("radio", { name: "证据不足" })).toBeEnabled();
    await user.type(screen.getByLabelText("理由"), "仍需补充附件");
    const saveDecision = screen.getByRole("button", { name: "保存决定" });
    expect(saveDecision).toBeEnabled();
    await user.click(saveDecision);
    expect(await screen.findByRole("alert")).toHaveTextContent("受控失败 503");
    expect(screen.queryByText(/该事项已被他人更新/)).not.toBeInTheDocument();
    expect(review.findings[0]!.decisions).toHaveLength(0);
  });

  it("shows unknown aggregate and per-call usage in both event views", async () => {
    const api = new Service();
    const review = api.addReview("partial");
    const usage = {
      input_tokens: null,
      output_tokens: null,
      total_tokens: null,
      calls: 3,
      unknown_calls: 3,
      sources: ["model_http"],
      known_subtotals: {
        input_tokens: null,
        output_tokens: null,
        total_tokens: null,
      },
      cost: null,
    };
    review.execution!.usage = usage;
    review.execution!.all_attempts_usage = usage;
    review.execution!.events = [
      {
        seq: 1,
        attempt_id: review.execution!.attempt.id,
        span_id: "model-span",
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
    ];
    const { user } = open(api, "/cases/c1/reviews/r1");
    expect(
      await screen.findByRole("heading", { name: "执行检查器" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "加载运行轨迹与诊断" }));
    expect(
      await screen.findByRole("heading", { name: "引用已定位的线索" }),
    ).toBeInTheDocument();
    const attemptUsage = screen.getByRole("region", { name: "本次执行用量" });
    expect(within(attemptUsage).getAllByText("未知").length).toBeGreaterThan(0);
    expect(
      within(attemptUsage).getByText("物理调用").parentElement,
    ).toHaveTextContent("3");
    const eventDetails = screen.getByRole("region", { name: "事件详情" });
    expect(within(eventDetails).getByText("provider_unavailable")).toBeVisible();
    expect(within(eventDetails).getByText("model_http")).toBeVisible();
    expect(within(eventDetails).getAllByText("未知")).toHaveLength(3);
    await user.click(screen.getByRole("button", { name: "时间线" }));
    expect(screen.getByRole("button", { name: /#1/ })).toBeInTheDocument();
    expect(
      within(eventDetails).getByText("时间").parentElement,
    ).toHaveTextContent("未记录");
    expect(screen.getAllByText("未报告").length).toBeGreaterThan(0);
    await user.click(screen.getByRole("button", { name: /序号 1/ }));
    expect(within(eventDetails).getByText("model_http")).toBeVisible();
    expect(within(eventDetails).getAllByText("未知")).toHaveLength(3);
  });

  it("renders the captured B2 per-call usage without rejecting the workspace", async () => {
    const api = new Service();
    const review = api.addReview("awaiting_human");
    const usage = {
      input_tokens: 183,
      output_tokens: 144,
      total_tokens: 327,
      calls: 1,
      unknown_calls: 0,
      sources: ["openai_response.usage"],
      known_subtotals: {
        input_tokens: 183,
        output_tokens: 144,
        total_tokens: 327,
      },
      cost: null,
    };
    review.execution!.usage = usage;
    review.execution!.all_attempts_usage = usage;
    review.execution!.events = [
      {
        seq: 12,
        attempt_id: review.execution!.attempt.id,
        span_id: "model-call",
        parent_span_id: "model-review",
        node_kind: "model_call",
        phase: "model_request",
        event_type: "model_completed",
        at: "2026-10-21T18:26:11.3417828Z",
        duration_ms: 3358,
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
    ];
    const { user } = open(api, "/cases/c1/reviews/r1");
    expect(
      await screen.findByRole("heading", { name: "执行检查器" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "加载运行轨迹与诊断" }));
    const eventDetails = await screen.findByRole("region", { name: "事件详情" });
    expect(
      within(eventDetails).getByText("openai_response.usage"),
    ).toBeVisible();
    expect(within(eventDetails).getByText("183")).toBeVisible();
    expect(within(eventDetails).getByText("144")).toBeVisible();
    expect(within(eventDetails).getByText("327")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "时间线" }));
    expect(screen.getByRole("button", { name: /#12/ })).toBeInTheDocument();
  });

  it("blocks decisions during execution and preserves cancellation as a terminal state", async () => {
    const api = new Service();
    const review = api.addReview("running");
    const { user } = open(api, "/cases/c1/reviews/r1");
    expect(
      await screen.findByText("本次审查仍在执行，完成后才能提交审查判断。"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存决定" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "取消本次执行" }));
    await waitFor(() =>
      expect(review.execution!.attempt.state).toBe("cancelled"),
    );
    expect(review.run.state).toBe("cancelled");
    expect(
      api.requests.some(
        (entry) => entry.key === "POST /api/cases/c1/reviews/r1/cancel",
      ),
    ).toBe(true);
    expect(
      screen.queryByRole("button", { name: "取消本次执行" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "重新执行未完成的审查" }),
    ).toBeEnabled();
  });
  it.each([
    { availability: "archived", state: "awaiting_human", action: "decision" },
    { availability: "archived", state: "partial", action: "retry" },
    { availability: "archived", state: "running", action: "cancel" },
    {
      availability: "unavailable",
      state: "awaiting_human",
      action: "decision",
    },
    { availability: "unavailable", state: "partial", action: "retry" },
    { availability: "unavailable", state: "running", action: "cancel" },
  ])(
    "keeps direct review history readable and disables $action when case state is $availability",
    async ({ availability, state, action }) => {
      const api = new Service();
      api.addReview(state);
      if (availability === "archived") api.cases.get("c1")!.archived_at = at;
      else api.fail("GET /api/cases/c1", 503);

      const { user } = open(
        api,
        "/cases/c1/reviews/r1?attempt_id=attempt-r1-1",
      );
      expect(
        await screen.findByRole("heading", { name: "审查工作区" }),
      ).toBeInTheDocument();
      expect(
        await screen.findByText("质保期限不一致", { selector: "h3" }),
      ).toBeInTheDocument();
      expect(
        await screen.findByText(
          availability === "archived"
            ? "案件已归档，历史审查仅供查看。"
            : "无法确认案件状态，当前仅可查看。",
        ),
      ).toBeInTheDocument();
      expect(screen.getByRole("radio", { name: "确认风险" })).toBeDisabled();
      expect(screen.getByRole("button", { name: "保存决定" })).toBeDisabled();
      if (action === "retry")
        expect(
          screen.getByRole("button", { name: "重新执行未完成的审查" }),
        ).toBeDisabled();
      for (const button of screen.queryAllByRole("button", {
        name: /^(保存决定|重新执行未完成的审查|取消本次执行)$/,
      })) {
        await user.click(button);
      }
      expect(
        api.requests.some((entry) => entry.key === "GET /api/cases/c1"),
      ).toBe(true);
      expect(
        api.requests.filter(
          (entry) =>
            entry.key.includes("/decisions") ||
            entry.key.endsWith("/retry") ||
            entry.key.endsWith("/cancel"),
        ),
      ).toHaveLength(0);
    },
  );

  it("keeps earlier attempts and decisions visible while making a selected history read-only", async () => {
    const api = new Service();
    const review = api.addReview("awaiting_human");
    const execution = review.execution!;
    const prior = {
      ...execution.attempt,
      state: "failed",
      history_kind: "legacy_unfrozen",
    };
    const latest = {
      ...execution.attempt,
      id: "attempt-r1-2",
      ordinal: 2,
      retry_of_attempt_id: prior.id,
    };
    execution.attempt = latest;
    execution.attempts = [prior, latest];
    review.findings[0]!.decisions = [
      {
        id: "old-decision",
        action: "false_positive",
        reason: "此前留存的理由",
        actor: "原审查员",
        decided_at: at,
      },
    ];
    const { user } = open(api, "/cases/c1/reviews/r1");
    await screen.findByRole("heading", { name: "审查工作区" });
    await chooseOption(
      user,
      screen.getByLabelText("查看执行记录"),
      /第 1 次，\s*失败/,
    );
    expect(
      await screen.findByText(
        "当前选择的是之前的执行记录，只能查看，不能提交审查判断。",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText("较早的审查没有保存当时使用的材料版本记录，无法补回当时的执行过程。"),
    ).toBeInTheDocument();
    expect(screen.getByText("此前留存的理由")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "确认风险" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "保存决定" })).toBeDisabled();
    expect(
      api.requests.filter((entry) => entry.key.startsWith("POST ")),
    ).toHaveLength(0);
  });

  it("presents prior-run decisions and recorded input identities without implying they were applied", async () => {
    const api = new Service();
    const review = api.addReview("partial");
    review.run.partial_reason = "图片转写未完成";
    review.evaluations = [evaluation()];
    review.previous_run_id = "r0";
    review.revisions = [
      {
        mark: "持续",
        finding_id: "f1",
        previous_finding_id: "old-f1",
        domain: "质保",
        relation: "conflict",
        claim: "期限仍不一致",
        prior_decision: {
          id: "p1",
          actor: "原审查员",
          action: "accept_residual",
          reason: "待复核",
          decided_at: at,
        },
      },
    ];
    const { user } = open(api, "/cases/c1/reviews/r1");
    await user.click(await screen.findByRole("button", { name: "修改稿对照" }));
    expect(
      screen.getByText(/先前决定：接受残余风险，\s*原审查员，\s*待复核/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "查看上一轮审查" }),
    ).toHaveAttribute("href", "/cases/c1/reviews/r0");
    expect(screen.getByText(/图片转写未完成/)).toBeInTheDocument();
    expect(
      screen.getByText("还没有审查人员的判断。模型建议不会自动结案。"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "加载运行轨迹与诊断" }));
    await user.click(
      screen.getByRole("button", { name: "运行依据与规则评估" }),
    );
    expect(await screen.findByText("typed-constraint-v1")).toBeVisible();
    expect(
      screen.getByText(
        /ENT-TECH-BW-001，\s*1\.0\.0，\s*dedicated_line\.minimum_bandwidth/,
      ),
    ).toBeVisible();
    expect(screen.getByText(/业务对象：LINE-001/)).toBeVisible();
    const before = api.requests.filter(
      (entry) => entry.key === "GET /api/cases/c1/reviews/r1",
    ).length;
    act(() =>
      Stream.latest?.onmessage?.({
        data: JSON.stringify({ seq: 2, type: "decision", state: "partial" }),
      }),
    );
    await waitFor(() =>
      expect(
        api.requests.filter(
          (entry) => entry.key === "GET /api/cases/c1/reviews/r1",
        ).length,
      ).toBeGreaterThan(before),
    );
  });

  it("shows why a cross-version comparison is unavailable instead of inventing resolved risks", async () => {
    const api = new Service();
    const review = api.addReview();
    review.previous_run_id = "r0";
    review.revision_comparison_reason =
      "两次运行使用不同规则版本，不能直接对照。";
    review.revisions = [];
    review.snapshot = null;
    review.evaluations = [];
    const { user } = open(api, "/cases/c1/reviews/r1");
    await user.click(await screen.findByRole("button", { name: "修改稿对照" }));
    expect(
      await screen.findByText("两次运行使用不同规则版本，不能直接对照。"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/^消除/)).not.toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "运行依据与规则评估" }),
    );
    expect(screen.getByText("未获取到本次审查的材料和规则记录。")).toBeVisible();
    expect(screen.getByText("没有返回规则评估记录。")).toBeVisible();
  });

  it("keeps navigation identities out of the business review while preserving links", async () => {
    const api = new Service();
    const review = api.addReview();
    review.documents[0]!.parsed_document_id = "parsed_private_source";
    review.findings[0]!.evidence[0]!.parsed_document_id = "parsed_private_source";
    const { container } = open(api, "/cases/c1/reviews/r1");
    await screen.findByRole("heading", { name: "审查工作区" });
    const breadcrumbs = container.querySelector<HTMLElement>(".breadcrumbs")!;
    expect(breadcrumbs).toHaveTextContent("审查工作区");
    expect(breadcrumbs).not.toHaveTextContent(/c1|r1/);
    expect(await within(breadcrumbs).findByRole("link", { name: "接入设备采购" })).toHaveAttribute("href", "/cases/c1");
    expect(container.querySelector(".finding-detail")!).not.toHaveTextContent(/parsed_private_source|SHA-256|已核验/);
    expect(screen.getByText("内容含义待核实")).toBeInTheDocument();
  });

  it("creates remediation from a terminal public attempt without diagnostics", async () => {
    const api = new Service();
    api.role = "reviewer";
    api.addReview("completed");
    const { user } = open(api, "/cases/c1");
    const finding = await screen.findByRole("checkbox", { name: /质保期限不一致/ });
    expect(screen.getByRole("button", { name: "创建整改" })).toBeDisabled();
    await user.click(finding);
    await waitFor(() => expect(screen.getByRole("button", { name: "创建整改" })).toBeEnabled());
    fireEvent.change(screen.getByLabelText("负责人 ID"), { target: { value: "u1" } });
    fireEvent.change(screen.getByLabelText("截止时间"), { target: { value: "2026-11-01T09:00" } });
    fireEvent.change(screen.getByLabelText("整改措施"), { target: { value: "补充合同条款" } });
    await user.click(screen.getByRole("button", { name: "创建整改" }));
    await waitFor(() => expect(api.requests.find((entry) => entry.key === "POST /api/cases/c1/remediations")?.input).toEqual({
      run_id: "r1",
      attempt_id: "attempt-r1-1",
      finding_ids: ["f1"],
      assignee_id: "u1",
      due_at: new Date(2026, 10, 1, 9, 0).toISOString(),
      measure: "补充合同条款",
    }));
    expect(api.requests.some((entry) => entry.key.includes("/diagnostics"))).toBe(false);
  });

  it("keeps business decisions available when explicitly requested diagnostics are forbidden", async () => {
    const api = new Service();
    const review = api.addReview("awaiting_human");
    review.business_events = [{ event_id: "event-1", run_id: "r1", actor: "u1", seq: 1, event_type: "review_started", correlation_ids: [] }];
    api.fail("GET /api/cases/c1/reviews/r1/diagnostics", 403);
    const { user } = open(api, "/cases/c1/reviews/r1");
    await screen.findByRole("heading", { name: "审查工作区" });
    expect(api.requests.some((entry) => entry.key.includes("/diagnostics"))).toBe(false);
    await user.click(screen.getByRole("button", { name: "加载运行轨迹与诊断" }));
    expect(await screen.findByText(/审查诊断不可用/)).toHaveTextContent("受控失败 403");
    await user.click(screen.getByRole("button", { name: "查看审计记录（1）" }));
    expect(screen.getByText(/审查已发起/)).toBeInTheDocument();
    const decision = screen.getByRole("radio", { name: "确认风险" });
    await waitFor(() => expect(decision).toBeEnabled());
    await user.click(decision);
    fireEvent.change(screen.getByLabelText("理由"), { target: { value: "依据业务证据确认" } });
    await user.click(screen.getByRole("button", { name: "保存决定" }));
    expect(await screen.findByText("依据业务证据确认")).toBeInTheDocument();
  });

  it("starts an ungoverned case without inventing a business time", async () => {
    const api = new Service();
    const item = api.cases.get("c1")!;
    item.scenario = "general";
    item.documents = [doc("d1", "procurement"), doc("d2", "draft_contract")];
    const { user } = open(api, "/cases/c1");
    const start = await screen.findByRole("button", { name: "开始审查" });
    expect(screen.queryByLabelText("审查业务时间（本地时区）")).not.toBeInTheDocument();
    expect(start).toBeEnabled();
    await user.click(start);
    await waitFor(() => expect(api.requests.find((entry) => entry.key === "POST /api/cases/c1/reviews")?.input).toEqual({}));
    expect(await screen.findByRole("heading", { name: "审查工作区" })).toBeInTheDocument();
  });

  it("uses exact rule evaluations instead of a resolved presentation mark for remediation eligibility", async () => {
    const api = new Service();
    addSubmittedRemediation(api, [
      evaluation({
        id: "evaluation-candidate",
        relation: "compatible",
        finding_id: null,
      }),
    ]);
    open(api, "/cases/c1");
    await screen.findByText(/复核状态/);
    const reason = screen.getByLabelText("复核理由");
    await waitFor(() => expect(reason).toBeEnabled());
    expect(screen.getByRole("button", { name: "提交复核" })).toBeEnabled();
  });

  it("keeps verification blocked when the selected runs are not comparable", async () => {
    const api = new Service();
    addSubmittedRemediation(
      api,
      [
        evaluation({
          id: "evaluation-candidate",
          relation: "compatible",
          finding_id: null,
        }),
      ],
      "两次运行的规则或模型配置与当前版本不一致，无法自动比较。",
    );
    open(api, "/cases/c1");
    await screen.findByText(/复核状态/);
    expect(
      screen.getAllByText(/规则或模型配置与当前版本不一致/).length,
    ).toBeGreaterThan(0);
    expect(screen.getByLabelText("复核理由")).toBeDisabled();
  });

  it.each<[string, ConstraintEvaluation[]]>([
    ["missing rule evidence", []],
    [
      "insufficient evidence",
      [
        evaluation({
          id: "evaluation-candidate",
          relation: "insufficient",
          finding_id: null,
        }),
      ],
    ],
    [
      "wrong business object",
      [
        evaluation({
          id: "evaluation-candidate",
          relation: "compatible",
          object_refs: ["LINE-002"],
          finding_id: null,
        }),
      ],
    ],
  ])(
    "keeps remediation verification disabled for %s even when the revision mark says resolved",
    async (_scenario, candidateEvaluations) => {
      const api = new Service();
      addSubmittedRemediation(api, candidateEvaluations);
      open(api, "/cases/c1");
      await screen.findByText(/复核状态/);
      expect(screen.getByLabelText("复核理由")).toBeDisabled();
      expect(screen.getByRole("button", { name: "提交复核" })).toBeDisabled();
    },
  );

  it("searches and filters cases, validates create input and permits cancel without writes", async () => {
    const api = new Service();
    api.addReview("failed");
    const { user } = open(api);
    await screen.findByRole("heading", { name: "案件", level: 1 });
    await user.type(
      screen.getByRole("searchbox", { name: "搜索案件" }),
      "不存在",
    );
    expect(
      screen.queryByRole("link", { name: "接入设备采购" }),
    ).not.toBeInTheDocument();
    await user.clear(screen.getByRole("searchbox", { name: "搜索案件" }));
    await chooseOption(
      user,
      screen.getByRole("combobox", { name: "运行状态" }),
      "失败",
    );
    expect(
      screen.getByRole("link", { name: "接入设备采购" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "新建案件" }));
    fireEvent.change(screen.getByLabelText("标题"), {
      target: { value: "x".repeat(121) },
    });
    fireEvent.submit(
      screen.getByRole("button", { name: "创建" }).closest("form")!,
    );
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(
      api.requests.filter((entry) => entry.key === "POST /api/cases"),
    ).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "新建案件" }));
    await user.keyboard("{Escape}");
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("shows create failures without discarding input", async () => {
    const api = new Service();
    api.fail("POST /api/cases", 503);
    const { user } = open(api);
    await screen.findByRole("heading", { name: "案件", level: 1 });
    await user.click(screen.getByRole("button", { name: "新建案件" }));
    await user.type(screen.getByLabelText("标题"), "不能丢失的名称");
    await user.click(screen.getByRole("button", { name: "创建" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("受控失败 503");
    expect(screen.getByLabelText("标题")).toHaveValue("不能丢失的名称");
  });

  it("disables mutation affordances for a read-only user and keeps navigation working", async () => {
    const api = new Service();
    api.role = "viewer";
    const { user } = open(api);
    await screen.findByRole("heading", { name: "案件", level: 1 });
    expect(screen.getByRole("button", { name: "新建案件" })).toBeDisabled();
    await user.click(screen.getByRole("link", { name: "接入设备采购" }));
    await screen.findByRole("heading", { name: "接入设备采购" });
    expect(screen.getByRole("button", { name: "选择文件" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "开始审查" })).toBeDisabled();
    expect(screen.getByText(/当前角色只能查看材料/)).toBeInTheDocument();
  });

  it("disables retry and decisions for a read-only reviewer without hiding evidence", async () => {
    const api = new Service();
    api.role = "viewer";
    api.addReview("partial");
    open(api, "/cases/c1/reviews/r1");
    expect(
      await screen.findByText("当前角色不能提交审查判断。"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "重新执行未完成的审查" }),
    ).toBeDisabled();
    expect(screen.getByRole("radio", { name: "确认风险" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "保存决定" })).toBeDisabled();
    expect(screen.getAllByText("36个月").length).toBeGreaterThan(0);
    expect(
      api.requests.filter((entry) => entry.key.startsWith("POST ")),
    ).toHaveLength(0);
  });

  it("allows a clerk to recover execution while keeping human decisions unavailable", async () => {
    const api = new Service();
    api.role = "clerk";
    api.addReview("partial");
    open(api, "/cases/c1/reviews/r1");
    await screen.findByRole("heading", { name: "审查工作区" });
    expect(
      screen.getByRole("button", { name: "重新执行未完成的审查" }),
    ).toBeEnabled();
    expect(screen.getByRole("radio", { name: "确认风险" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "保存决定" })).toBeDisabled();
    expect(
      api.requests.filter((entry) => entry.key.includes("/decisions")),
    ).toHaveLength(0);
  });

  it("offers a manual new attempt after interruption without replacing the original attempt", async () => {
    const api = new Service();
    api.addReview("interrupted");
    const { user } = open(api, "/cases/c1/reviews/r1");
    await user.click(
      await screen.findByRole("button", { name: "重新执行未完成的审查" }),
    );
    await waitFor(() =>
      expect(api.reviews.get("r1")!.execution!.attempts).toHaveLength(2),
    );
    expect(api.reviews.get("r1")!.execution!.attempts[0]!.state).toBe(
      "interrupted",
    );
    expect(api.reviews.get("r1")!.execution!.attempt.id).toBe("attempt-r1-2");
  });

  it("keeps absent execution history and measurements unreported", async () => {
    const api = new Service();
    api.addReview().execution = null;
    open(api, "/cases/c1/reviews/r1");
    expect(await screen.findByText(/没有获取到本次审查的执行记录/)).toBeInTheDocument();
    expect(screen.queryByText(/legacy_unfrozen/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText("查看执行记录")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "取消本次执行" }),
    ).not.toBeInTheDocument();
  });

  it("links governance to the separately served Admin app and keeps the same-origin fallback", async () => {
    const fallback = new Service();
    vi.stubEnv("VITE_ADMIN_URL", "");
    open(fallback, "/cases");
    await screen.findByRole("heading", { name: "案件", level: 1 });
    expect(screen.getByRole("link", { name: "规则包治理" })).toHaveAttribute(
      "href",
      "/admin/rulepacks",
    );
    expect(
      fallback.requests.some(
        (entry) => entry.key === "GET /api/session?audience=admin",
      ),
    ).toBe(false);

    cleanup();
    vi.stubEnv("VITE_ADMIN_URL", "http://127.0.0.1:5175/");
    const api = new Service();
    open(api, "/cases");
    await screen.findByRole("heading", { name: "案件", level: 1 });
    expect(screen.getByRole("link", { name: "规则包治理" })).toHaveAttribute(
      "href",
      "http://127.0.0.1:5175/admin/rulepacks",
    );
    expect(
      api.requests.some(
        (entry) => entry.key === "GET /api/session?audience=admin",
      ),
    ).toBe(false);
  });

  it("keeps the Admin login reachable without treating a client session as an admin session", async () => {
    const api = new Service();
    api.adminSignedIn = false;
    const { user } = open(api, "/admin/rulepacks");
    expect(
      await screen.findByRole("heading", { name: "登录管理工作台" }),
    ).toBeInTheDocument();
    expect(
      api.requests.filter(
        (entry) => entry.key === "GET /api/session?audience=admin",
      ).length,
    ).toBeLessThanOrEqual(2);
    expect(api.signedIn).toBe(true);
    expect(api.adminSignedIn).toBe(false);
    await user.type(screen.getByLabelText("账号"), "demo-admin");
    await user.type(screen.getByLabelText("密码"), "admin-demo");
    await user.click(screen.getByRole("button", { name: "登录" }));
    expect(
      await screen.findByRole("heading", { name: "设置", level: 1 }),
    ).toBeInTheDocument();
    expect(
      api.requests.some(
        (entry) =>
          entry.key === "POST /api/session" &&
          entry.input !== null &&
          typeof entry.input === "object" &&
          "audience" in entry.input &&
          entry.input.audience === "admin",
      ),
    ).toBe(true);
    expect(api.signedIn).toBe(true);
    expect(api.adminSignedIn).toBe(true);
    await user.click(screen.getByRole("button", { name: "退出" }));
    expect(
      await screen.findByRole("heading", { name: "登录管理工作台" }),
    ).toBeInTheDocument();
    expect(
      api.requests.some(
        (entry) => entry.key === "DELETE /api/session?audience=admin",
      ),
    ).toBe(true);
    expect(api.signedIn).toBe(true);
    expect(api.adminSignedIn).toBe(false);

    cleanup();
    open(api, "/cases");
    expect(
      await screen.findByRole("heading", { name: "案件", level: 1 }),
    ).toBeInTheDocument();
    expect(api.requests.some((entry) => entry.key === "GET /api/session")).toBe(
      true,
    );
  });

  it("preserves manual sidebar collapse and expansion across Compare and other routes", async () => {
    const { user } = open(new Service(), "/cases");
    await screen.findByRole("heading", { name: "案件", level: 1 });
    await user.click(screen.getByRole("button", { name: "收合侧栏" }));
    expect(screen.getByRole("button", { name: "展开侧栏" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );

    await user.click(screen.getAllByRole("link", { name: "版本对照" })[0]!);
    await waitFor(() =>
      expect(
        screen
          .getAllByRole("link", { name: "版本对照" })
          .some((link) => link.getAttribute("aria-current") === "page"),
      ).toBe(true),
    );
    expect(screen.getByRole("button", { name: "展开侧栏" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );

    await user.click(screen.getByRole("button", { name: "展开侧栏" }));
    await user.click(screen.getAllByRole("link", { name: "案件管理" })[0]!);
    await waitFor(() =>
      expect(
        screen
          .getAllByRole("link", { name: "案件管理" })
          .some((link) => link.getAttribute("aria-current") === "page"),
      ).toBe(true),
    );
    expect(screen.getByRole("button", { name: "收合侧栏" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );

    await user.click(screen.getAllByRole("link", { name: "版本对照" })[0]!);
    await waitFor(() =>
      expect(
        screen
          .getAllByRole("link", { name: "版本对照" })
          .some((link) => link.getAttribute("aria-current") === "page"),
      ).toBe(true),
    );
    expect(screen.getByRole("button", { name: "收合侧栏" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });

  it("preserves the active session observer through the login transition", async () => {
    const api = new Service();
    api.signedIn = false;
    api.cases.clear();
    const { user } = open(api, "/");
    await screen.findByRole("heading", { name: "登录合同审查" });
    await user.type(screen.getByLabelText("账号"), "demo-admin");
    await user.type(screen.getByLabelText("密码"), "admin-demo");
    await user.click(screen.getByRole("button", { name: "登录" }));
    expect(await screen.findByText("还没有案件")).toBeInTheDocument();
    expect(
      api.requests.some((entry) => entry.key === "POST /api/session"),
    ).toBe(true);
    expect(api.requests.some((entry) => entry.key === "GET /api/cases")).toBe(
      true,
    );
  });

  it("clears prior identity data and discards a delayed response from the former identity", async () => {
    const api = new Service();
    api.addReview();
    const { client } = open(api, "/cases/c1/reviews/r1");
    expect(
      (await screen.findAllByText("质保期限不一致")).length,
    ).toBeGreaterThan(0);
    api.holdNextWorkspaceRead = true;
    const formerIdentityRead = client.invalidateQueries({
      queryKey: ["cases", "c1", "reviews", "r1"],
    });
    await waitFor(() => expect(api.releaseHeldWorkspace).not.toBeNull());
    const heldRequest = api.requests
      .filter((entry) => entry.key === "GET /api/cases/c1/reviews/r1")
      .at(-1)!;
    expect(heldRequest.signal?.aborted).toBe(false);
    api.identity = "u2";
    await client.invalidateQueries({ queryKey: ["session"] });
    // Cancel before clear: the outstanding read of the former identity is aborted,
    // not merely ignored after it lands.
    await waitFor(() => expect(heldRequest.signal?.aborted).toBe(true));
    expect(
      (await screen.findAllByText("乙账号的专属线索")).length,
    ).toBeGreaterThan(0);
    const release = api.releaseHeldWorkspace!;
    api.releaseHeldWorkspace = null;
    release();
    await formerIdentityRead;
    expect(
      (await screen.findAllByText("乙账号的专属线索")).length,
    ).toBeGreaterThan(0);
    expect(screen.queryByText("质保期限不一致")).not.toBeInTheDocument();
  });

  it("resubscribes the run stream when the session identity changes and ignores the superseded stream", async () => {
    const api = new Service();
    api.addReview();
    const { client } = open(api, "/cases/c1/reviews/r1");
    expect(
      (await screen.findAllByText("质保期限不一致")).length,
    ).toBeGreaterThan(0);
    const first = Stream.latest!;
    expect(first.url).toBe("/api/cases/c1/reviews/r1/events");
    expect(first.withCredentials).toBe(true);
    const reviewReads = () =>
      api.requests.filter(
        (entry) => entry.key === "GET /api/cases/c1/reviews/r1",
      ).length;
    // Same account id and role, different tenant: this is a session identity
    // change, so the stream must not survive as "the same session".
    api.tenant = "tenant-other";
    await client.invalidateQueries({ queryKey: ["session"] });
    await waitFor(() => expect(first.closed).toBe(true));
    const second = await waitFor(() => {
      const latest = Stream.latest!;
      expect(latest).not.toBe(first);
      return latest;
    });
    expect(second.url).toBe("/api/cases/c1/reviews/r1/events");
    // A delivery queued on the superseded stream cannot trigger the current session's refresh:
    // the hook detaches the handlers, so no refetch is recorded for it.
    const beforeSuperseded = reviewReads();
    act(() => {
      first.onmessage?.({
        data: JSON.stringify({
          seq: 21,
          type: "run_state",
          state: "awaiting_human",
        }),
      });
    });
    expect(first.onmessage).toBeNull();
    expect(reviewReads()).toBe(beforeSuperseded);
    // The active stream drives the refresh exactly once per event.
    act(() => {
      second.onmessage?.({
        data: JSON.stringify({
          seq: 22,
          type: "run_state",
          state: "awaiting_human",
        }),
      });
    });
    await waitFor(() => expect(reviewReads()).toBe(beforeSuperseded + 1));
  });

  it("refreshes a terminal business attempt promptly on a live event", async () => {
    const api = new Service();
    const review = api.addReview("running");
    open(api, "/cases/c1/reviews/r1");
    const decision = await screen.findByRole("radio", { name: "确认风险" });
    expect(decision).toBeDisabled();
    const source = Stream.latest!;
    act(() => source.onopen?.());
    expect(await screen.findByText("运行状态已连接")).toBeInTheDocument();
    const execution = review.execution!;
    const terminal = { ...execution.attempt, state: "awaiting_human", finished_at: at };
    review.execution = { ...execution, attempt: terminal, attempts: [terminal] };
    review.run.state = "awaiting_human";
    api.cases.get("c1")!.latest_run!.state = "awaiting_human";
    act(() => source.onmessage?.({ data: JSON.stringify({ seq: 1, type: "run_state", state: "awaiting_human" }) }));
    await waitFor(() => expect(decision).toBeEnabled());
  });

  it("keeps the run stream connected while an event refreshes case permissions", async () => {
    const api = new Service();
    api.addReview();
    open(api, "/cases/c1/reviews/r1");
    const decision = await screen.findByRole("radio", { name: "确认风险" });
    await waitFor(() => expect(decision).toBeEnabled());
    const source = Stream.latest!;
    const held = Promise.withResolvers<Response>();
    let pending = false;
    vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/cases/c1" && !pending) {
        pending = true;
        return held.promise;
      }
      return api.handle(input, init);
    });
    try {
      act(() =>
        source.onmessage?.({
          data: JSON.stringify({
            seq: 23,
            type: "run_state",
            state: "awaiting_human",
          }),
        }),
      );
      await waitFor(() => expect(decision).toBeDisabled());
      expect(pending).toBe(true);
      expect(source.closed).toBe(false);
      held.resolve(api.json(api.cases.get("c1")));
      await waitFor(() => expect(decision).toBeEnabled());
      expect(Stream.latest).toBe(source);
    } finally {
      held.resolve(api.json(api.cases.get("c1")));
    }
  });

  it("labels a refused run stream as closed instead of reconnecting", async () => {
    const api = new Service();
    api.addReview();
    open(api, "/cases/c1/reviews/r1");
    expect(
      (await screen.findAllByText("质保期限不一致")).length,
    ).toBeGreaterThan(0);
    const source = Stream.latest!;
    // Non-200 connect: the browser closes the stream and does not retry.
    source.readyState = 2;
    act(() => source.onerror?.());
    expect(
      await screen.findByText(/运行状态流已关闭，不会自动重连/),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("连接中断，正在按游标重连"),
    ).not.toBeInTheDocument();
  });

  it("clears protected workspace data when the session is revoked on an open stream", async () => {
    const api = new Service();
    api.addReview();
    open(api, "/cases/c1/reviews/r1");
    expect(
      (await screen.findAllByText("质保期限不一致")).length,
    ).toBeGreaterThan(0);
    api.signedIn = false;
    const source = Stream.latest!;
    source.readyState = 2;
    act(() => source.onerror?.());
    expect(
      await screen.findByRole("heading", { name: "登录合同审查" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("质保期限不一致")).not.toBeInTheDocument();
  });

  it("refuses a revoked review read without presenting the cached workspace", async () => {
    const api = new Service();
    api.addReview();
    open(api, "/cases/c1/reviews/r1");
    expect(
      (await screen.findAllByText("质保期限不一致")).length,
    ).toBeGreaterThan(0);
    // The refusal must not be masked by a duplicate refetch of the same workspace.
    api.fail("GET /api/cases/c1/reviews/r1", 403);
    act(() => {
      Stream.latest?.onmessage?.({
        data: JSON.stringify({
          seq: 31,
          type: "run_state",
          state: "awaiting_human",
        }),
      });
    });
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("没有权限查看这次审查");
    expect(screen.queryByText("质保期限不一致")).not.toBeInTheDocument();
    // A refused read must also leave no live stream behind: the browser fails a
    // 403 connect terminally, so the page must not keep a stream it cannot trust.
    expect(Stream.instances.filter((entry) => !entry.closed)).toHaveLength(0);
  });

  it("shows operational totals and unknown queue depth only to administrators", async () => {
    const api = new Service();
    const review = api.addReview("partial");
    review.execution!.all_attempts_usage = {
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
    open(api, "/admin/settings", <AdminApp />);
    const operations = await screen.findByRole("region", {
      name: "执行运维统计",
    });
    expect(
      within(operations).getByText("全部执行记录").parentElement,
    ).toHaveTextContent("1");
    expect(
      within(operations).getByText("用量未知的调用").parentElement,
    ).toHaveTextContent("2");
    expect(within(operations).getByText("未报告")).toBeInTheDocument();
  });

  it("does not show admin operational execution metadata to a viewer", async () => {
    const api = new Service();
    api.role = "viewer";
    open(api, "/admin/settings", <AdminApp />);
    expect(await screen.findByRole("alert")).toBeVisible();
    expect(
      api.requests.some((entry) => entry.key === "GET /api/admin/settings"),
    ).toBe(false);
    expect(
      screen.queryByRole("heading", { name: "执行运维统计" }),
    ).not.toBeInTheDocument();
  });

  it("renders an unknown route and returns to cases", async () => {
    const api = new Service();
    const { user } = open(api, "/unknown");
    expect(
      await screen.findByRole("heading", { name: "没有这个页面" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "返回案件" }));
    expect(
      await screen.findByRole("heading", { name: "案件", level: 1 }),
    ).toBeInTheDocument();
  });
});
