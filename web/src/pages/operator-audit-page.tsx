// File metadata (manifest mirror):
// Purpose: Shows the operator-audit extension only when enabled by the server/deployment flags, composes the UI2 audit overview from real evaluation/object data, and keeps existing service-object, evidence and acceptance write panels in their tabs; no extension write path is offered while disabled.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-29
// Updated: 2026-11-08
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import { useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Input,
  Select,
  Space,
  Table,
  Tabs,
  type TableProps,
} from "antd";
import { useParams, useSearchParams } from "react-router";
import { getReview } from "../api/endpoints";
import { errorMessage } from "../api/http";
import { useCaseDetail, useSettings } from "../api/queries";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
import { suppliedBusinessName } from "../domain/business-labels";
import { defaultOperatorAuditClient } from "../features/operator-audit/api";
import { CapabilityPackPanel } from "../features/operator-audit/capability-pack-panel";
import { ExternalEvidencePanel } from "../features/operator-audit/external-evidence-panel";
import {
  DEFAULT_OPERATOR_AUDIT_FLAGS,
  type OperatorAuditFlags,
} from "../features/operator-audit/feature-flags";
import { FulfillmentDiffPanel } from "../features/operator-audit/fulfillment-diff-panel";
import {
  FulfillmentObjectsPanel,
  beijingTime,
} from "../features/operator-audit/fulfillment-objects-panel";
import { useStableIdempotencyKey } from "../features/operator-audit/idempotency";
import {
  machineStatusLabel,
  machineStatusTone,
  relationLabelFor,
  relationTone,
  timeLabel,
} from "../features/operator-audit/labels";
import { ObjectBindingPanel, type ContractObjectOption } from "../features/operator-audit/object-binding-panel";
import { ServiceObjectPanel } from "../features/operator-audit/service-object-panel";
import type { ServiceObjectSummary } from "../features/operator-audit/service-objects-api";
import type {
  CheckResult,
  ExternalCollectionJob,
  FulfillmentComparison,
  FulfillmentEvaluation,
  ObjectBinding,
} from "../features/operator-audit/types";
import "./operator-audit-page.css";

const client = defaultOperatorAuditClient;

type OperatorTabKey = "overview" | "objects" | "basis" | "records";

interface OverviewRow {
  key: string;
  check: CheckResult | null;
  objectLabel: string | null;
  objectStatus: string | null;
  objectReasonCode: string | null;
}

const objectStatusLabel: Record<string, string> = {
  evaluated: "已核验",
  insufficient: "证据不足",
  unresolved: "尚未确定",
  not_applicable: "不适用",
};

const objectStatusTone: Record<string, "good" | "attention" | "neutral"> = {
  evaluated: "good",
  insufficient: "attention",
  unresolved: "attention",
  not_applicable: "neutral",
};

function beijingInputNow(): string {
  return new Date(Date.now() + 8 * 60 * 60 * 1000).toISOString().slice(0, 19);
}

function beijingInstant(value: string): string | null {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?$/.test(value)) return null;
  const instant = new Date(`${value.length === 16 ? `${value}:00` : value}+08:00`);
  return Number.isFinite(instant.getTime()) ? instant.toISOString() : null;
}

/** Month in the same Beijing business timezone used by the rest of the page. */
function businessMonthLabel(value: string | null | undefined): string {
  if (!value) return "未报告";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "未报告";
  const parts = new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "numeric",
  }).formatToParts(date);
  const year = parts.find((part) => part.type === "year")?.value;
  const month = parts.find((part) => part.type === "month")?.value;
  return year && month ? `${year} 年 ${month} 月` : "未报告";
}

function uniqueLabels(values: string[]): string[] {
  return [...new Set(values.map((value) => value.trim()).filter((value) => value !== ""))];
}

/**
 * Read-only overview rows come from the persisted evaluation only. Object-scoped
 * checks win when present because they are the rows the evaluator actually
 * persisted; otherwise the aggregate evaluation checks are shown.
 */
function evaluationChecks(
  evaluation: FulfillmentEvaluation,
  serviceObjects: ServiceObjectSummary[],
): OverviewRow[] {
  const objectRows: OverviewRow[] = (evaluation.objects ?? []).flatMap((object, objectIndex): OverviewRow[] => {
    const objectLabel =
      serviceObjects.find(
        (item) => item.service_object_id === object.serviceObjectId,
      )?.display_name ?? object.objectRef;
    if (object.checks.length === 0) {
      return [{
        key: `${object.objectRef}:${objectIndex}:object`,
        check: null,
        objectLabel,
        objectStatus: object.status,
        objectReasonCode: object.reasonCode,
      }];
    }
    return object.checks.map((check, checkIndex) => ({
      key: `${object.objectRef}:${objectIndex}:${check.checkId}:${checkIndex}`,
      check,
      objectLabel,
      objectStatus: object.status,
      objectReasonCode: object.reasonCode,
    }));
  });
  if (objectRows.length > 0) return objectRows;
  return evaluation.checks.map((check, index): OverviewRow => ({
    key: `evaluation:${check.checkId}:${index}`,
    check,
    objectLabel: null,
    objectStatus: null,
    objectReasonCode: null,
  }));
}

/** Aggregate basis state; never turns absent evidence into "complete". */
function evaluationBasisStatus(
  evaluation: FulfillmentEvaluation | null,
  checks: CheckResult[],
): string {
  if (evaluation === null) return "未报告";
  if (evaluation.coverage?.status === "partial") return "部分缺失";
  if (checks.length > 0) {
    const missing = checks.filter((check) => check.evidence.length === 0).length;
    if (missing > 0) return missing === checks.length ? "缺失" : "部分缺失";
    if (checks.some((check) => check.requiresHumanReview === true)) return "待判断";
    if (evaluation.coverage?.status === "complete") return "完整";
    return "已报告";
  }
  const objects = evaluation.objects ?? [];
  if (objects.length > 0) {
    if (objects.some((object) => object.status === "insufficient" || object.status === "unresolved")) {
      return "部分缺失";
    }
    if (objects.every((object) => object.status === "evaluated" || object.status === "not_applicable")) {
      return evaluation.coverage?.status === "complete" ? "完整" : "已报告";
    }
  }
  return "未报告";
}

/** A check-level label that keeps machine relation and human-review state apart. */
function checkBasisLabel(check: CheckResult): string {
  if (check.relation === "conflict") return "冲突";
  if (check.relation === "insufficient") return "信息不足";
  if (check.requiresHumanReview === true) return "待判断";
  return relationLabelFor(check.relation);
}

function coverageStatusLabel(status: string | null | undefined): string {
  if (status === "complete") return "覆盖完整";
  if (status === "partial") return "覆盖不完整";
  return "未报告";
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
  const [activeTab, setActiveTab] = useState<OperatorTabKey>("overview");
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
          sourceSystem: connector.source_system,
          label: "外部对象名称未提供",
        })),
      ),
    [connectors],
  );
  const bindingOptions = useMemo(
    () =>
      bindings.map((binding) => {
        const external = externalObjects.find((object) => object.connectorId === binding.externalObject.connectorId && object.externalObjectId === binding.externalObject.externalObjectId);
        return {
          value: binding.version ?? binding.id,
          label: `${suppliedBusinessName(binding.contractObject.label, "合同对象名称未提供")} → ${suppliedBusinessName(external?.label, "外部对象名称未提供")} · 合同对象标识 ${binding.contractObject.objectId ?? "未报告"} · 连接器标识 ${binding.externalObject.connectorId ?? "未报告"} · 外部对象标识 ${binding.externalObject.externalObjectId ?? "未报告"} · 来源系统标识 ${external?.sourceSystem || "未提供"} · 映射版本 ${binding.version ?? binding.id}`,
        };
      }),
    [bindings, externalObjects],
  );
  const packOptions = useMemo(
    () =>
      (packs.data ?? [])
        .filter((pack) => pack.scenario === "telecom_dedicated_line")
        .map((pack) => ({
          value: `${pack.packId}@${pack.version ?? ""}`,
          label: `${suppliedBusinessName(pack.displayName, "能力包名称未提供")} · 版本 ${pack.version ?? "未报告"} · 能力包标识 ${pack.packId}`,
        })),
    [packs.data],
  );

  const selectedBindingRecord = bindings.find(
    (binding) => (binding.version ?? binding.id) === selectedBinding,
  );

  const overviewRows = useMemo(
    () => (evaluation === null ? [] : evaluationChecks(evaluation, serviceObjects)),
    [evaluation, serviceObjects],
  );
  const overviewChecks = useMemo(
    () => overviewRows.flatMap((row) => (row.check === null ? [] : [row.check])),
    [overviewRows],
  );
  const serviceObjectFact = useMemo(() => {
    const fromEvaluation = overviewRows.flatMap((row) =>
      row.objectLabel === null ? [] : [row.objectLabel],
    );
    const confirmed = serviceObjects
      .filter((item) => item.state === "confirmed")
      .map((item) => item.display_name);
    return uniqueLabels(fromEvaluation.length > 0 ? fromEvaluation : confirmed).join("、") || "未报告";
  }, [overviewRows, serviceObjects]);
  const scopeFact = useMemo(() => {
    const kind = evaluation?.scope?.kind ?? evaluation?.coverage?.scopeKind ?? null;
    if (kind === "whole_case") return "整案";
    if (kind === "explicit_subset") {
      const ids = evaluation?.scope?.serviceObjectIds ?? [];
      if (ids.length === 0) return "明确选择的对象（对象标识未报告）";
      return `明确选择：${ids
        .map(
          (id) =>
            serviceObjects.find((item) => item.service_object_id === id)?.display_name ?? id,
        )
        .join("、")}`;
    }
    return "未报告";
  }, [evaluation, serviceObjects]);
  const basisFact = evaluationBasisStatus(evaluation, overviewChecks);
  const reviewHref =
    runId !== "" && attemptId !== ""
      ? `/cases/${caseId}/reviews/${encodeURIComponent(runId)}?attempt_id=${encodeURIComponent(attemptId)}`
      : null;
  /** Object results keep the evaluation's frozen source pair when the server reports it. */
  const objectReviewHref = () => {
    const sourceRunId = evaluation?.sourceReviewId ?? runId;
    const sourceAttemptId = evaluation?.sourceAttemptId ?? attemptId;
    return sourceRunId !== "" && sourceAttemptId !== ""
      ? `/cases/${caseId}/reviews/${encodeURIComponent(sourceRunId)}?attempt_id=${encodeURIComponent(sourceAttemptId)}`
      : null;
  };

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
      <section className="page operator-audit-page">
        <header className="page-head">
          <div>
            <h1>运营商专项核验</h1>
            <p>政企专线·履约要素核验</p>
          </div>
        </header>
        <PageStatus
          kind="denied"
          title="跨系统核验未启用"
          detail="该功能默认关闭。启用只读连接器与能力包后，才能创建对象映射、采集证据并受理核验。"
        />
      </section>
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

  const overviewColumns: TableProps<OverviewRow>["columns"] = [
    {
      title: "核验维度",
      key: "dimension",
      render: (_, row) => (
        <>
          {row.check !== null ? (
            <strong className="digest">{row.check.checkId}</strong>
          ) : (
            <strong>{row.objectLabel ?? "对象未报告"}</strong>
          )}
          {row.check !== null && row.objectLabel !== null ? (
            <div className="meta">对象：{row.objectLabel}</div>
          ) : row.check === null ? (
            <div className="meta">对象级结果</div>
          ) : null}
        </>
      ),
    },
    {
      title: "实际覆盖",
      key: "coverage",
      render: (_, row) =>
        row.check !== null ? (
          <StatusText tone={machineStatusTone(row.check.machineStatus)}>
            {machineStatusLabel(row.check.machineStatus)}
          </StatusText>
        ) : row.objectStatus !== null ? (
          <StatusText tone={objectStatusTone[row.objectStatus] ?? "neutral"}>
            {objectStatusLabel[row.objectStatus] ?? row.objectStatus}
          </StatusText>
        ) : (
          <span className="meta">未报告</span>
        ),
    },
    {
      title: "依据状态",
      key: "basis",
      render: (_, row) =>
        row.check !== null ? (
          <>
            <StatusText tone={relationTone(row.check.relation)}>
              {checkBasisLabel(row.check)}
            </StatusText>
            {row.check.reasonCode ? (
              <div className="meta">原因代码 {row.check.reasonCode}</div>
            ) : null}
          </>
        ) : (
          <>
            <StatusText
              tone={
                row.objectStatus === "insufficient" || row.objectStatus === "unresolved"
                  ? "attention"
                  : "neutral"
              }
            >
              {row.objectStatus === "insufficient"
                ? "信息不足"
                : row.objectStatus === "unresolved"
                  ? "尚未确定"
                  : row.objectStatus === "not_applicable"
                    ? "不适用"
                    : "未报告"}
            </StatusText>
            {row.objectReasonCode ? (
              <div className="meta">原因代码 {row.objectReasonCode}</div>
            ) : null}
          </>
        ),
    },
    {
      title: "下一步",
      key: "next",
      render: () =>
        reviewHref !== null ? (
          <a href={reviewHref}>核对依据</a>
        ) : (
          <span className="meta">来源审查未就绪</span>
        ),
    },
  ];

  const overviewTab = (
    <>
      <Alert
        type="warning"
        showIcon
        title="本视图不虚构网络测量结果；未接入获授权来源时保持未核验。"
        description="机器结果只说明合同约定与已采集配置记录的关系，最终结论仍需人工复核。"
      />
      <Card size="small" title="检查项目" className="operator-check-card">
        {evaluationQuery.isLoading ? (
          <PageStatus kind="loading" title="正在读取核验结果" />
        ) : evaluationQuery.isError ? (
          <Alert
            type="error"
            showIcon
            title={`核验结果读取失败：${errorMessage(evaluationQuery.error)}`}
          />
        ) : evaluation === null ? (
          <PageStatus
            kind="empty"
            title="未受理核验"
            detail="还没有履约核验结果。可在“依据与差异”里配置和受理。"
          />
        ) : overviewRows.length === 0 ? (
          <PageStatus
            kind="empty"
            title="本次核验没有可展示的检查项目"
            detail="当前没有结果，请结合核验范围核对。"
          />
        ) : (
          <Table<OverviewRow>
            aria-label="检查项目"
            rowKey="key"
            dataSource={overviewRows}
            columns={overviewColumns}
            pagination={false}
            size="middle"
          />
        )}
      </Card>
    </>
  );

  const objectsTab = (
    <>
      <ServiceObjectPanel
        key={caseId}
        caseId={caseId}
        initialRunId={runId}
        initialAttemptId={attemptId || null}
        caseDetail={caseDetail.data}
        onObjectsChange={setServiceObjects}
      />
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
    </>
  );

  const basisTab = (
    <>
      <CapabilityPackPanel client={client} flags={flags} />

      <Card size="small" title="采集外部证据">
        <p className="meta">
          采集读取已登记连接器；失败会保留为不可用记录。
        </p>
        <Space orientation="vertical" style={{ width: "100%" }}>
          <Select
            aria-label="选择对象映射"
            placeholder="选择已确认的对象映射"
            value={selectedBinding || undefined}
            options={bindingOptions}
            showSearch={{ optionFilterProp: "label" }}
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
            showSearch={{ optionFilterProp: "label" }}
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
        <Alert type="error" showIcon title={actionError} />
      ) : null}
      {evaluationQuery.isError ? <Alert type="error" showIcon title={`核验结果读取失败：${errorMessage(evaluationQuery.error)}`} /> : null}
      {evaluationQuery.isLoading && evaluationId !== "" ? <PageStatus kind="loading" title="正在读取核验结果" /> : null}
      {evaluation !== null && evaluation.scope ? (
        <FulfillmentObjectsPanel
          evaluation={evaluation}
          objects={serviceObjects}
          snapshots={snapshots.data ?? []}
          flags={flags}
          humanReviewHref={objectReviewHref}
        />
      ) : evaluation !== null ? (
        <FulfillmentDiffPanel
          items={comparisons}
          flags={flags}
          humanReviewHref={() => reviewHref}
        />
      ) : null}
    </>
  );

  const recordsTab = (
    <section className="operator-records" aria-label="处理记录">
      {evaluation !== null ? (
        <Card size="small" title="核验受理记录">
          <dl className="execution-dl">
            <div><dt>核验编号</dt><dd className="digest">{evaluation.evaluationId}</dd></div>
            <div><dt>能力包</dt><dd>{evaluation.pack ? `${evaluation.pack.id} @ ${evaluation.pack.version ?? "版本未报告"}` : "未报告"}</dd></div>
            <div><dt>来源审查</dt><dd>{evaluation.sourceReviewId ?? "未报告"} / {evaluation.sourceAttemptId ?? "未报告"}</dd></div>
            <div><dt>业务时间</dt><dd>{beijingTime(evaluation.businessAt)}</dd></div>
            <div><dt>证据截止时间</dt><dd>{beijingTime(evaluation.evidenceCutoffAt)}</dd></div>
            <div><dt>核验范围</dt><dd>{scopeFact}</dd></div>
            <div><dt>覆盖状态</dt><dd>{coverageStatusLabel(evaluation.coverage?.status)}</dd></div>
          </dl>
        </Card>
      ) : null}
      {jobs.length > 0 ? (
        <Card size="small" title="本次页面采集任务">
          <Table<ExternalCollectionJob>
            rowKey="jobId"
            dataSource={jobs}
            pagination={false}
            size="small"
            columns={[
              { title: "处理", key: "action", render: () => "采集外部证据" },
              { title: "受理时间", dataIndex: "acceptedAt", key: "acceptedAt", render: (value: string | null) => timeLabel(value) },
              { title: "任务标识", dataIndex: "jobId", key: "jobId", render: (value: string) => <span className="digest">{value}</span> },
              { title: "状态", dataIndex: "state", key: "state", render: (value: string | null) => value ?? "未报告" },
            ]}
          />
        </Card>
      ) : null}
      {evaluation === null && jobs.length === 0 ? (
        <PageStatus
          kind="empty"
          title="暂无处理记录"
          detail="这里还没有核验受理或证据采集记录。"
        />
      ) : null}
      <p className="meta">处理记录只展示当前页面已读取的数据；完整审计以审计日志为准。</p>
    </section>
  );

  return (
    <section className="page operator-audit-page">
      <header className="page-head operator-page-head">
        <div>
          <p className="operator-page-kicker">跨系统核验</p>
          <h1>运营商专项核验</h1>
          <p>政企专线·履约要素核验</p>
        </div>
      </header>

      <dl className="operator-facts" aria-label="核验关键事实">
        <div><dt>服务对象</dt><dd>{serviceObjectFact}</dd></div>
        <div><dt>业务期间</dt><dd>{businessMonthLabel(evaluation?.businessAt)}</dd></div>
        <div><dt>核验范围</dt><dd>{scopeFact}</dd></div>
        <div><dt>依据状态</dt><dd>{basisFact}</dd></div>
      </dl>

      {runId === "" || attemptId === "" ? (
        <Alert
          type="info"
          showIcon
          title="需要一次已完成的专线审查"
          description="请从审查工作区进入本页，或等待来源审查完成后再受理核验。"
        />
      ) : null}

      <Tabs
        className="operator-audit-tabs"
        activeKey={activeTab}
        onChange={(key) => setActiveTab(key as OperatorTabKey)}
        destroyOnHidden={false}
        items={[
          { key: "overview", label: "核验概览", forceRender: true, children: overviewTab },
          { key: "objects", label: "服务对象", forceRender: true, children: objectsTab },
          { key: "basis", label: "依据与差异", forceRender: true, children: basisTab },
          { key: "records", label: "处理记录", forceRender: true, children: recordsTab },
        ]}
      />
    </section>
  );
}
