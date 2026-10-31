import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../api/http";
import {
  createHttpOperatorAuditClient,
  parseCheckResult,
  parseExternalObservation,
  parseFulfillmentEvaluation,
  parseImpactPlan,
} from "./api";
import type {
  CreateObjectBindingInput,
  FulfillmentEvaluationInput,
} from "./types";

type Call = { path: string; init: RequestInit };

function install(
  respond: (call: Call) => Response | Promise<Response>,
): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const call = {
        path: new URL(String(input), "http://workbench.test").pathname,
        init,
      };
      calls.push(call);
      return respond(call);
    }),
  );
  return calls;
}

const bindingInput: CreateObjectBindingInput = {
  contract_object: {
    object_type: "contract_line",
    object_id: "line-1",
    label: "政企专线合同",
  },
  external_object: {
    connector_id: "fixture.oss",
    external_object_id: "fixture-line-001",
  },
  service_object_id: "service-object-1",
  expected_object_version: 2,
  expected_association_version: 1,
  effective_from: null,
  effective_until: null,
  confirmed: true,
};

const evaluationInput: FulfillmentEvaluationInput = {
  pack_id: "operator.dedicated_line",
  pack_version: "1.0.0",
  source_review_id: "review-1",
  source_attempt_id: "attempt-1",
  scope: { kind: "explicit_subset", service_object_ids: ["service-object-1"] },
  business_at: "2026-10-28T08:00:00Z",
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("operator audit API client", () => {
  it("loads the capability pack catalog from the draft path", async () => {
    const calls = install(() =>
      Response.json({
        items: [
          {
            pack_id: "operator.dedicated_line",
            version: "1.0.0",
            display_name: "专线配置核验（合成示例）",
            scenario: "telecom_dedicated_line",
            status: "published",
            rule_pack_ref: {
              id: "fixture.rulepack.dedicated_line",
              version: "1.0.0",
            },
            required_material_roles: ["draft_contract"],
            capabilities: [],
            checks: [{ id: "line.configured_bandwidth" }],
            data_class: "synthetic",
          },
        ],
      }),
    );
    const client = createHttpOperatorAuditClient();
    const packs = await client.listCapabilityPacks();
    expect(calls[0]?.path).toBe("/api/capability-packs");
    expect(calls[0]?.init.method).toBe("GET");
    expect(packs).toHaveLength(1);
    expect(packs[0]).toMatchObject({
      packId: "operator.dedicated_line",
      version: "1.0.0",
      scenario: "telecom_dedicated_line",
      status: "published",
      dataClass: "synthetic",
    });
    expect(packs[0]?.checks[0]?.externalField).toBeNull();
  });

  it("posts an explicitly confirmed binding with an idempotency key", async () => {
    const calls = install(() =>
      Response.json(
        {
          binding_id: "binding-1",
          version: "3",
          status: "confirmed",
          contract_object: {
            object_type: "contract_line",
            object_id: "line-1",
            label: "政企专线合同",
          },
          external_object: {
            connector_id: "fixture.oss",
            external_object_id: "fixture-line-001",
          },
          confirmed_by: "reviewer-1",
          confirmed_at: "2026-10-28T08:01:00Z",
          effective_from: "2026-10-01T00:00:00Z",
          effective_until: null,
        },
        { status: 201 },
      ),
    );
    const client = createHttpOperatorAuditClient();
    const binding = await client.createObjectBinding(
      "case/1",
      bindingInput,
      "binding-key-1",
    );
    expect(calls[0]?.path).toBe("/api/cases/case%2F1/object-bindings");
    expect(calls[0]?.init.method).toBe("POST");
    expect(new Headers(calls[0]?.init.headers).get("Idempotency-Key")).toBe(
      "binding-key-1",
    );
    expect(JSON.parse(String(calls[0]?.init.body))).toMatchObject({
      confirmed: true,
      contract_object: { object_type: "contract_line" },
      external_object: { connector_id: "fixture.oss" },
    });
    expect(binding).toMatchObject({
      id: "binding-1",
      version: "3",
      status: "confirmed",
      confirmedBy: "reviewer-1",
    });
  });

  it("refuses to send a write without an idempotency key", async () => {
    const calls = install(() => Response.json({}));
    const client = createHttpOperatorAuditClient();
    let failure: unknown = null;
    try {
      await client.createObjectBinding("case-1", bindingInput, "   ");
    } catch (error) {
      failure = error;
    }
    expect(failure).toBeInstanceOf(ApiError);
    expect((failure as ApiError).code).toBe("invalid_request");
    expect(calls).toHaveLength(0);
  });

  it("uses the draft paths for collection jobs and evaluations", async () => {
    const calls = install((call) => {
      if (call.path.endsWith("/external-collection-jobs"))
        return Response.json(
          {
            job_id: "job-1",
            state: "queued",
            connector_id: "fixture.oss",
            external_object_id: "fixture-line-001",
            accepted_at: "2026-10-28T08:00:00Z",
          },
          { status: 202 },
        );
      if (call.path.endsWith("/fulfillment-evaluations"))
        return Response.json({ evaluation_id: "eval-1" }, { status: 202 });
      if (call.path.endsWith("/impact"))
        return Response.json({
          base_evaluation_id: "eval-0",
          target_evaluation_id: "eval-1",
          mode: "explain",
          dependency_complete: true,
          fallback: "none",
          checks: [
            { check_id: "check-1", action: "recompute", reasons: [] },
          ],
          business_result_source: "full",
        });
      return Response.json({
        evaluation_id: "eval-1",
        checks: [],
        data_class: "synthetic",
      });
    });
    const client = createHttpOperatorAuditClient();
    const job = await client.createExternalCollectionJob(
      "case-1",
      {
        connector_id: "fixture.oss",
        external_object_id: "fixture-line-001",
        object_binding_version: "binding-1",
        business_at: "2026-10-28T08:00:00Z",
      },
      "job-key",
    );
    const accepted = await client.createFulfillmentEvaluation(
      "case-1",
      evaluationInput,
      "evaluation-key",
    );
    const evaluation = await client.getFulfillmentEvaluation(
      "case-1",
      "eval-1",
    );
    const impact = await client.getImpactPlan("case-1", "eval-1");

    expect(job.jobId).toBe("job-1");
    expect(accepted.evaluationId).toBe("eval-1");
    expect(evaluation.evaluationId).toBe("eval-1");
    expect(impact.mode).toBe("explain");
    expect(calls.map((call) => call.path)).toEqual([
      "/api/cases/case-1/external-collection-jobs",
      "/api/cases/case-1/fulfillment-evaluations",
      "/api/cases/case-1/fulfillment-evaluations/eval-1",
      "/api/cases/case-1/fulfillment-evaluations/eval-1/impact",
    ]);
    expect(
      calls
        .filter((call) => call.init.method === "POST")
        .map((call) => new Headers(call.init.headers).get("Idempotency-Key")),
    ).toEqual(["job-key", "evaluation-key"]);
  });

  it("keeps unknown or omitted values unknown instead of filling defaults", () => {
    const observation = parseExternalObservation({
      snapshot_id: "snapshot-1",
      availability: "mystery",
      fields: [{ name: "configured_bandwidth", value: null, unit: 42 }],
    });
    expect(observation.availability).toBe("unknown");
    expect(observation.observedAt).toBeNull();
    expect(observation.sourceUpdatedAt).toBeNull();
    expect(observation.effectiveFrom).toBeNull();
    expect(observation.dataClass).toBeNull();
    expect(observation.fields[0]?.value).toBeNull();
    expect(observation.fields[0]?.unit).toBeNull();

    const check = parseCheckResult({
      check_id: "line.configured_bandwidth",
      relation: "unexpected",
      evidence: [{ kind: "other_source" }],
    });
    expect(check.relation).toBe("unknown");
    expect(check.machineStatus).toBe("unknown");
    expect(check.requiresHumanReview).toBeNull();
    expect(check.evidence[0]).toEqual({
      kind: "unknown",
      rawKind: "other_source",
    });

    const plan = parseImpactPlan({
      base_evaluation_id: "eval-0",
      target_evaluation_id: "eval-1",
      mode: "replay",
      checks: [{ check_id: "check-1", action: "skip" }],
      business_result_source: "partial",
    });
    expect(plan.mode).toBe("unknown");
    expect(plan.dependencyComplete).toBeNull();
    expect(plan.fallback).toBe("unknown");
    expect(plan.checks[0]?.action).toBe("unknown");
    expect(plan.checks[0]?.reasons).toEqual([]);
    expect(plan.businessResultSource).toBe("partial");
  });

  it("keeps snapshot-set identity and contract facts explicit", () => {
    const observation = parseExternalObservation({
      snapshot_id: "snapshot-1",
      snapshot_set_id: "set-1",
      fields: [],
    });
    expect(observation.snapshotSetId).toBe("set-1");

    const evaluation = parseFulfillmentEvaluation({
      evaluation_id: "eval-1",
      checks: [],
      contract_facts: [
        {
          name: "minimum_bandwidth",
          value: { value: "100", unit: "Mbit/s" },
          uncertain: false,
          out_of_scope: false,
          semantic_kind: "configured_value",
        },
      ],
    });
    expect(evaluation.contractFacts[0]?.value).toEqual({
      value: "100",
      unit: "Mbit/s",
    });
    expect(parseExternalObservation({ snapshot_id: "s", fields: [] }).snapshotSetId).toBeNull();
  });

  it("surfaces a forbidden response as an ApiError", async () => {
    install(() =>
      Response.json(
        { error: { code: "forbidden", message: "没有权限" } },
        { status: 403 },
      ),
    );
    const client = createHttpOperatorAuditClient();
    const failure = await client
      .listExternalSnapshots("case-1")
      .catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(ApiError);
    expect((failure as ApiError).status).toBe(403);
    expect((failure as ApiError).code).toBe("forbidden");
  });
});
