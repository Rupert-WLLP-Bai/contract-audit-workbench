import { useMemo, useState } from "react";
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
import { useStableIdempotencyKey } from "../features/operator-audit/idempotency";
import { ImpactPlanPanel } from "../features/operator-audit/impact-plan-panel";
import { ObjectBindingPanel } from "../features/operator-audit/object-binding-panel";
import type {
  ExternalCollectionJob,
  FulfillmentComparison,
  FulfillmentEvaluation,
  ImpactPlan,
  ObjectBinding,
} from "../features/operator-audit/types";

const client = defaultOperatorAuditClient;

export function OperatorAuditPage() {
  const { caseId = "" } = useParams();
  const [params] = useSearchParams();
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
  const [selectedBinding, setSelectedBinding] = useState<string>("");
  const [jobs, setJobs] = useState<ExternalCollectionJob[]>([]);
  const [businessAt, setBusinessAt] = useState(() =>
    new Date().toISOString(),
  );
  const [selectedPack, setSelectedPack] = useState<string>("");
  const [selectedContract, setSelectedContract] = useState<string>("");
  const [selectedSnapshotSet, setSelectedSnapshotSet] =
    useState<string>("");
  const [evaluation, setEvaluation] = useState<FulfillmentEvaluation | null>(
    null,
  );
  const [plan, setPlan] = useState<ImpactPlan | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const idempotencyKey = useStableIdempotencyKey();

  const contractObjects = useMemo(
    () =>
      (caseDetail.data?.documents ?? [])
        .filter((document) => document.role === "draft_contract")
        .map((document) => ({
          objectType: "service",
          objectId: document.id,
          label: `${document.filename}（v${document.id.slice(-6)}）`,
        })),
    [caseDetail.data?.documents],
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
  const snapshotSetOptions = useMemo(() => {
    const seen = new Set<string>();
    const options: { value: string; label: string }[] = [];
    for (const snapshot of snapshots.data ?? []) {
      const id = snapshot.snapshotSetId;
      if (id === null || seen.has(id)) continue;
      seen.add(id);
      options.push({ value: id, label: id });
    }
    return options;
  }, [snapshots.data]);

  const selectedBindingRecord = bindings.find(
    (binding) => (binding.version ?? binding.id) === selectedBinding,
  );

  async function collect() {
    if (!selectedBindingRecord) {
      setActionError("请先确认一个合同对象与外部对象的映射");
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
        business_at: businessAt,
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
    if (
      selectedPack === "" ||
      selectedBindingRecord === undefined ||
      selectedContract === "" ||
      selectedSnapshotSet === "" ||
      runId === "" ||
      attemptId === ""
    ) {
      setActionError("请选择能力包、合同版本、对象映射、快照集，并确保来源审查已完成");
      return;
    }
    const [packId = "", packVersion = ""] = selectedPack.split("@");
    setBusy(true);
    setActionError(null);
    try {
      const input = {
        pack_id: packId,
        pack_version: packVersion,
        source_review_id: runId,
        source_attempt_id: attemptId,
        contract_version_id: selectedContract,
        object_binding_version:
          selectedBindingRecord.version ?? selectedBindingRecord.id,
        snapshot_set_id: selectedSnapshotSet,
        business_at: businessAt,
      };
      const reference = await client.createFulfillmentEvaluation(
        caseId,
        input,
        idempotencyKey("fulfillment-evaluation", input),
      );
      const current = await client.getFulfillmentEvaluation(
        caseId,
        reference.evaluationId,
      );
      setEvaluation(current);
      setPlan(await client.getImpactPlan(caseId, reference.evaluationId));
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
          <Input
            aria-label="业务时间"
            placeholder="业务时间（RFC3339）"
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
        businessAt={businessAt}
      />

      <Card size="small" title="受理核验">
        <p className="meta">
          核验绑定冻结合同、规则包、对象映射与快照集；一次受理不会追加到旧审查。
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
            aria-label="选择合同版本"
            placeholder="选择冻结合同版本"
            value={selectedContract || undefined}
            options={contractObjects.map((item) => ({
              value: item.objectId,
              label: item.label,
            }))}
            onChange={setSelectedContract}
            style={{ maxWidth: 560 }}
          />
          <Select
            aria-label="选择快照集"
            placeholder="选择不可变快照集"
            value={selectedSnapshotSet || undefined}
            options={snapshotSetOptions}
            onChange={setSelectedSnapshotSet}
            style={{ maxWidth: 560 }}
          />
          <Button
            htmlType="button"
            loading={busy}
            disabled={
              selectedPack === "" ||
              selectedBindingRecord === undefined ||
              selectedContract === "" ||
              selectedSnapshotSet === "" ||
              runId === "" ||
              attemptId === ""
            }
            onClick={() => void evaluate()}
          >
            受理确定性核验
          </Button>
        </Space>
      </Card>

      {actionError !== null ? (
        <Alert type="error" showIcon message={actionError} />
      ) : null}
      {evaluation !== null ? (
        <FulfillmentDiffPanel
          items={comparisons}
          flags={flags}
          humanReviewHref={() =>
            `/cases/${caseId}/reviews/${runId}?attempt_id=${encodeURIComponent(attemptId)}`
          }
        />
      ) : null}
      {plan !== null ? <ImpactPlanPanel plan={plan} flags={flags} /> : null}
    </section>
  );
}
