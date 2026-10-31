import { useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Card, Input, Select, Space } from "antd";
import { useParams, useSearchParams } from "react-router";
import { getReview } from "../api/endpoints";
import { errorMessage } from "../api/http";
import { useCaseDetail, useSettings } from "../api/queries";
import { PageStatus } from "../components/page-status";
import { defaultOperatorAuditClient } from "../features/operator-audit/api";
import { CapabilityPackPanel } from "../features/operator-audit/capability-pack-panel";
import { ExternalEvidencePanel } from "../features/operator-audit/external-evidence-panel";
import {
  DEFAULT_OPERATOR_AUDIT_FLAGS,
  type OperatorAuditFlags,
} from "../features/operator-audit/feature-flags";
import { FulfillmentDiffPanel } from "../features/operator-audit/fulfillment-diff-panel";
import { FulfillmentObjectsPanel } from "../features/operator-audit/fulfillment-objects-panel";
import { useStableIdempotencyKey } from "../features/operator-audit/idempotency";
import { ObjectBindingPanel, type ContractObjectOption } from "../features/operator-audit/object-binding-panel";
import { ServiceObjectPanel } from "../features/operator-audit/service-object-panel";
import type { ServiceObjectSummary } from "../features/operator-audit/service-objects-api";
import type {
  ExternalCollectionJob,
  FulfillmentComparison,
  ObjectBinding,
} from "../features/operator-audit/types";

const client = defaultOperatorAuditClient;

function beijingInputNow(): string {
  return new Date(Date.now() + 8 * 60 * 60 * 1000).toISOString().slice(0, 19);
}

function beijingInstant(value: string): string | null {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?$/.test(value)) return null;
  const instant = new Date(`${value.length === 16 ? `${value}:00` : value}+08:00`);
  return Number.isFinite(instant.getTime()) ? instant.toISOString() : null;
}

export function OperatorAuditPage() {
  const { caseId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const settings = useSettings("client");
  const caseDetail = useCaseDetail(caseId);
  const enabled = settings.data?.operator_audit?.enabled === true;
  const connectors = settings.data?.operator_audit?.connectors ?? [];
  const flags: OperatorAuditFlags = enabled
    ? {
        capabilityPacksEnabled: true,
        externalEvidenceMode: "fixture",
        fulfillmentChecksEnabled: true,
        reviewImpactMode: "explain",
        incrementalReuseEnabled: false,
      }
    : DEFAULT_OPERATOR_AUDIT_FLAGS;

  const packs = useQuery({
    queryKey: ["operator-audit", "capability-packs"],
    queryFn: ({ signal }) => client.listCapabilityPacks(signal),
    enabled,
  });
  const snapshots = useQuery({
    queryKey: ["operator-audit", "external-snapshots", caseId],
    queryFn: ({ signal }) => client.listExternalSnapshots(caseId, signal),
    enabled: enabled && caseId !== "",
  });
  const runId = params.get("run_id") ?? caseDetail.data?.latest_run?.id ?? "";
  const review = useQuery({
    queryKey: ["operator-audit", "review", caseId, runId],
    queryFn: ({ signal }) => getReview(caseId, runId, null, signal),
    enabled: enabled && caseId !== "" && runId !== "",
    retry: false,
  });
  const attemptId =
    params.get("attempt_id") ??
    review.data?.review_attempts?.attempt.id ??
    "";

  const [bindings, setBindings] = useState<ObjectBinding[]>([]);
  const [serviceObjects, setServiceObjects] = useState<ServiceObjectSummary[]>([]);
  const [selectedBinding, setSelectedBinding] = useState<string>("");
  const [jobs, setJobs] = useState<ExternalCollectionJob[]>([]);
  const [businessAt, setBusinessAt] = useState(beijingInputNow);
  const [evidenceCutoffAt, setEvidenceCutoffAt] = useState("");
  const [scopeKind, setScopeKind] = useState<"whole_case" | "explicit_subset">("whole_case");
  const [selectedObjects, setSelectedObjects] = useState<string[]>([]);
  const implicitCutoffs = useRef(new Map<string, string>());
  const [selectedPack, setSelectedPack] = useState<string>("");
  const evaluationId = params.get("evaluation_id") ?? "";
  const evaluationQuery = useQuery({
    queryKey: ["operator-audit", "fulfillment-evaluation", caseId, evaluationId],
    queryFn: ({ signal }) => client.getFulfillmentEvaluation(caseId, evaluationId, signal),
    enabled: enabled && caseId !== "" && evaluationId !== "",
    retry: false,
  });
  const evaluation = evaluationQuery.data ?? null;
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const idempotencyKey = useStableIdempotencyKey();

  const contractObjects: ContractObjectOption[] = useMemo(
    () => serviceObjects.flatMap((item) => {
      const association = item.current_association;
      return item.state === "confirmed" && association !== null
        ? [{ objectType: item.object_type, objectId: association.object_ref, label: item.display_name,
            serviceObjectId: item.service_object_id, objectVersion: item.current_version,
            associationVersion: association.association_version }]
        : [];
    }),
    [serviceObjects],
  );
  const externalObjects = useMemo(
    () =>
      connectors.flatMap((connector) =>
        (connector.allowed_objects ?? []).map((objectId) => ({
          connectorId: connector.connector_id,
          externalObjectId: objectId,
          label: `${connector.connector_id} · ${objectId}`,
        })),
      ),
    [connectors],
  );
  const bindingOptions = useMemo(
    () =>
      bindings.map((binding) => ({
        value: binding.version ?? binding.id,
        label: `${binding.contractObject.objectId ?? "合同对象"} → ${
          binding.externalObject.externalObjectId ?? "外部对象"
        }`,
      })),
    [bindings],
  );
  const packOptions = useMemo(
    () =>
      (packs.data ?? [])
        .filter((pack) => pack.scenario === "telecom_dedicated_line")
        .map((pack) => ({
          value: `${pack.packId}@${pack.version ?? ""}`,
          label: `${pack.displayName ?? pack.packId} @ ${pack.version ?? "版本未报告"}`,
        })),
    [packs.data],
  );

  const selectedBindingRecord = bindings.find(
    (binding) => (binding.version ?? binding.id) === selectedBinding,
  );

  async function collect() {
    if (!selectedBindingRecord) {
      setActionError("请先确认一个合同对象与外部对象的映射");
      return;
    }
    const instant = beijingInstant(businessAt);
    if (instant === null) {
      setActionError("请填写有效的北京时间业务时间");
      return;
    }
    setBusy(true);
    setActionError(null);
    try {
      const input = {
        connector_id: selectedBindingRecord.externalObject.connectorId ?? "",
        external_object_id:
          selectedBindingRecord.externalObject.externalObjectId ?? "",
        object_binding_version:
          selectedBindingRecord.version ?? selectedBindingRecord.id,
        business_at: instant,
      };
      const job = await client.createExternalCollectionJob(
        caseId,
        input,
        idempotencyKey("external-collection", input),
      );
      setJobs((current) => [job, ...current]);
      await snapshots.refetch();
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusy(false);
    }
  }

  async function evaluate() {
    const instant = beijingInstant(businessAt);
    const requestedCutoff = evidenceCutoffAt === "" ? null : beijingInstant(evidenceCutoffAt);
    if (selectedPack === "" || runId === "" || attemptId === "" || instant === null ||
        (scopeKind === "explicit_subset" && selectedObjects.length === 0) ||
        (evidenceCutoffAt !== "" && requestedCutoff === null)) {
      setActionError("请选择能力包、核验范围和有效的北京时间，并确保来源审查已完成");
      return;
    }
    const [packId = "", packVersion = ""] = selectedPack.split("@");
    const identity = JSON.stringify([packId, packVersion, runId, attemptId, instant, scopeKind, [...selectedObjects].sort()]);
    let cutoff = requestedCutoff ?? implicitCutoffs.current.get(identity);
    if (!cutoff) {
      cutoff = new Date().toISOString();
      implicitCutoffs.current.set(identity, cutoff);
    }
    setBusy(true);
    setActionError(null);
    try {
      const input = {
        pack_id: packId,
        pack_version: packVersion,
        source_review_id: runId,
        source_attempt_id: attemptId,
        business_at: instant,
        evidence_cutoff_at: cutoff,
        scope: scopeKind === "whole_case"
          ? { kind: "whole_case" as const }
          : { kind: "explicit_subset" as const, service_object_ids: [...selectedObjects].sort() },
      };
      const reference = await client.createFulfillmentEvaluation(
        caseId, input, idempotencyKey("fulfillment-evaluation", input),
      );
      setParams((current) => {
        const next = new URLSearchParams(current);
        next.set("evaluation_id", reference.evaluationId);
        return next;
      });
      if (reference.evaluationId === evaluationId) await evaluationQuery.refetch();
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setBusy(false);
    }
  }

  const comparisons: FulfillmentComparison[] = useMemo(() => {
    if (evaluation === null) return [];
    const fact = evaluation.contractFacts.find(
      (item) => item.name === "minimum_bandwidth",
    );
    const observation = (snapshots.data ?? []).find(
      (snapshot) => snapshot.snapshotSetId === evaluation.snapshotSetId,
    );
    const configured = observation?.fields.find(
      (field) => field.name === "configured_bandwidth",
    );
    return evaluation.checks.map((check) => ({
      check,
      contractValue: fact?.value ?? null,
      configuredValue: configured
        ? { value: configured.value, unit: configured.unit }
        : null,
    }));
  }, [evaluation, snapshots.data]);

  if (settings.isLoading) {
    return <PageStatus kind="loading" title="正在读取跨系统核验配置" />;
  }
  if (settings.isError) {
    return (
      <PageStatus
        kind="error"
        title="无法读取跨系统核验配置"
        detail={errorMessage(settings.error)}
      />
    );
  }
  if (!enabled) {
    return (
      <PageStatus
        kind="denied"
        title="跨系统核验未启用"
        detail="该功能默认关闭。启用只读连接器与能力包后，才能创建对象映射、采集证据并受理核验。"
      />
    );
  }
  if (caseDetail.isLoading) {
    return <PageStatus kind="loading" title="正在读取案件" />;
  }
  if (caseDetail.isError) {
    return (
      <PageStatus
        kind="error"
        title="案件读取失败"
        detail={errorMessage(caseDetail.error)}
      />
    );
  }

  return (
    <section>
      <header className="page-header">
        <div>
          <h1>跨系统核验</h1>
          <p className="meta">
            {caseDetail.data?.title ?? "案件"} · 配置差异只作为候选证据，最终结论仍需人工复核。
          </p>
        </div>
      </header>

      {runId === "" || attemptId === "" ? (
        <Alert
          type="info"
          showIcon
          message="需要一次已完成的专线审查"
          description="请从审查工作区进入本页，或等待来源审查完成后再受理核验。"
        />
      ) : null}

      <CapabilityPackPanel client={client} flags={flags} />

      <ObjectBindingPanel
        caseId={caseId}
        client={client}
        flags={flags}
        contractObjects={contractObjects}
        externalObjects={externalObjects}
        existingBindings={bindings}
        onCreated={(binding) => {
          setBindings((current) => [...current, binding]);
          setSelectedBinding(binding.version ?? binding.id);
        }}
      />

      <ServiceObjectPanel
        key={caseId}
        caseId={caseId}
        initialRunId={runId}
        initialAttemptId={attemptId || null}
        caseDetail={caseDetail.data}
        onObjectsChange={setServiceObjects}
      />

      <Card size="small" title="采集外部证据">
        <p className="meta">
          采集只读取已登记连接器；任务受理不代表证据可用，失败会保留为不可用观测。
        </p>
        <Space orientation="vertical" style={{ width: "100%" }}>
          <Select
            aria-label="选择对象映射"
            placeholder="选择已确认的对象映射"
            value={selectedBinding || undefined}
            options={bindingOptions}
            onChange={setSelectedBinding}
            style={{ maxWidth: 560 }}
          />
          <label htmlFor="operator-business-at">业务时间（北京时间）</label>
          <Input
            id="operator-business-at"
            aria-label="业务时间（北京时间）"
            type="datetime-local"
            step={1}
            value={businessAt}
            onChange={(event) => setBusinessAt(event.target.value)}
          />
          <Button
            htmlType="button"
            loading={busy}
            disabled={selectedBindingRecord === undefined}
            onClick={() => void collect()}
          >
            采集一次外部证据
          </Button>
        </Space>
      </Card>

      <ExternalEvidencePanel
        caseId={caseId}
        client={client}
        flags={flags}
        jobs={jobs}
        businessAt={beijingInstant(businessAt) ?? ""}
      />

      <Card size="small" title="受理核验">
        <p className="meta">
          默认核验整案；也可明确选择对象范围。每个对象单独固定合同事实、映射和外部快照。
        </p>
        <Space orientation="vertical" style={{ width: "100%" }}>
          <Select
            aria-label="选择能力包"
            placeholder="选择能力包与版本"
            value={selectedPack || undefined}
            options={packOptions}
            onChange={setSelectedPack}
            style={{ maxWidth: 560 }}
          />
          <Select
            aria-label="核验范围"
            value={scopeKind}
            options={[{ value: "whole_case", label: "整案全部对象" }, { value: "explicit_subset", label: "明确选择对象" }]}
            onChange={setScopeKind}
            style={{ maxWidth: 560 }}
          />
          {scopeKind === "explicit_subset" ? (
            <Select
              aria-label="选择核验对象"
              mode="multiple"
              placeholder="选择一个或多个已登记对象"
              value={selectedObjects}
              options={serviceObjects.map((item) => ({ value: item.service_object_id, label: `${item.display_name} · ${item.service_object_id}` }))}
              onChange={setSelectedObjects}
              style={{ width: "100%", maxWidth: 560 }}
            />
          ) : null}
          <label htmlFor="operator-evidence-cutoff">证据截止时间（北京时间，可选）</label>
          <Input id="operator-evidence-cutoff" aria-label="证据截止时间（北京时间）" type="datetime-local" step={1} value={evidenceCutoffAt} onChange={(event) => setEvidenceCutoffAt(event.target.value)} />
          <p className="meta">不填写时使用首次受理时间作为截止时间；重试保持相同截止时间。</p>
          <Button
            htmlType="button"
            loading={busy}
            disabled={selectedPack === "" || runId === "" || attemptId === "" ||
              (scopeKind === "explicit_subset" && selectedObjects.length === 0)}
            onClick={() => void evaluate()}
          >
            受理确定性核验
          </Button>
        </Space>
      </Card>

      {actionError !== null ? (
        <Alert type="error" showIcon message={actionError} />
      ) : null}
      {evaluationQuery.isError ? <Alert type="error" showIcon message={`核验结果读取失败：${errorMessage(evaluationQuery.error)}`} /> : null}
      {evaluationQuery.isLoading && evaluationId !== "" ? <PageStatus kind="loading" title="正在读取核验结果" /> : null}
      {evaluation !== null && evaluation.scope ? (
        <FulfillmentObjectsPanel
          evaluation={evaluation}
          objects={serviceObjects}
          snapshots={snapshots.data ?? []}
          flags={flags}
          humanReviewHref={() => `/cases/${caseId}/reviews/${encodeURIComponent(evaluation.sourceReviewId ?? runId)}?attempt_id=${encodeURIComponent(evaluation.sourceAttemptId ?? attemptId)}`}
        />
      ) : evaluation !== null ? (
        <FulfillmentDiffPanel
          items={comparisons}
          flags={flags}
          humanReviewHref={() => `/cases/${caseId}/reviews/${runId}?attempt_id=${encodeURIComponent(attemptId)}`}
        />
      ) : null}
    </section>
  );
}
