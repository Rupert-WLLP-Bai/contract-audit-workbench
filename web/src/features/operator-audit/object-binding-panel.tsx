import { useState, type SyntheticEvent } from "react";
import { Button, Checkbox, Input, Select, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { PageStatus } from "../../components/page-status";
import { StatusText } from "../../components/status-text";
import { defaultOperatorAuditClient, type OperatorAuditClient } from "./api";
import { OperatorAuditError } from "./error-notice";
import { type OperatorAuditFlags } from "./feature-flags";
import { ExternalEvidenceGate } from "./external-evidence-gate";
import { useStableIdempotencyKey } from "./idempotency";
import { bindingStatusLabel, bindingStatusTone, timeLabel } from "./labels";
import type {
  CreateObjectBindingInput,
  ObjectBinding,
} from "./types";

export interface ContractObjectOption {
  objectType: string;
  objectId: string;
  label: string;
  serviceObjectId: string;
  objectVersion: number;
  associationVersion: number;
}

export interface ExternalObjectOption {
  connectorId: string;
  externalObjectId: string;
  label: string;
}

export interface ObjectBindingPanelProps {
  caseId: string;
  client?: OperatorAuditClient;
  flags?: OperatorAuditFlags;
  /** Explicit candidates supplied by the case workspace; never inferred by name. */
  contractObjects?: ContractObjectOption[];
  externalObjects?: ExternalObjectOption[];
  existingBindings?: ObjectBinding[];
  onCreated?: (binding: ObjectBinding) => void;
}

function contractKey(option: ContractObjectOption): string {
  return `${option.objectType}\u0000${option.objectId}\u0000${option.serviceObjectId}`;
}

function externalKey(option: ExternalObjectOption): string {
  return `${option.connectorId}\u0000${option.externalObjectId}`;
}

function localDateTimeToIso(value: string): string | null {
  if (value === "") return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date.toISOString();
}

function windowLabel(binding: ObjectBinding): string {
  if (binding.effectiveFrom === null && binding.effectiveUntil === null)
    return "有效区间未报告";
  return `${timeLabel(binding.effectiveFrom)} 至 ${
    binding.effectiveUntil === null ? "时间未报告" : timeLabel(binding.effectiveUntil)
  }`;
}

const bindingColumns: ColumnsType<ObjectBinding> = [
  {
    title: "合同对象",
    key: "contract",
    render: (_, binding) => (
      <>
        <strong>
          {binding.contractObject.label ??
            binding.contractObject.objectId ??
            "合同对象未报告"}
        </strong>
        <div className="meta">
          {binding.contractObject.objectType ?? "类型未报告"} ·{" "}
          {binding.contractObject.objectId ?? "ID 未报告"}
        </div>
      </>
    ),
  },
  {
    title: "外部对象",
    key: "external",
    render: (_, binding) => (
      <>
        <strong>
          {binding.externalObject.externalObjectId ?? "外部对象未报告"}
        </strong>
        <div className="meta">
          {binding.externalObject.connectorId ?? "连接器未报告"}
        </div>
      </>
    ),
  },
  {
    title: "状态",
    key: "status",
    render: (_, binding) => (
      <StatusText tone={bindingStatusTone(binding.status)}>
        {bindingStatusLabel(binding.status)}
      </StatusText>
    ),
  },
  {
    title: "版本",
    key: "version",
    render: (_, binding) => (
      <span className="digest">{binding.version ?? "版本未报告"}</span>
    ),
  },
  {
    title: "确认人",
    key: "confirmedBy",
    render: (_, binding) => binding.confirmedBy ?? "确认人未报告",
  },
  {
    title: "确认时间",
    key: "confirmedAt",
    render: (_, binding) => timeLabel(binding.confirmedAt),
  },
  {
    title: "有效区间",
    key: "window",
    render: (_, binding) => windowLabel(binding),
  },
];

function ObjectBindingPanelContent({
  caseId,
  client = defaultOperatorAuditClient,
  contractObjects = [],
  externalObjects = [],
  existingBindings = [],
  onCreated,
}: ObjectBindingPanelProps) {
  const keyFor = useStableIdempotencyKey();
  const [selectedContract, setSelectedContract] = useState("");
  const [selectedExternal, setSelectedExternal] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [effectiveFrom, setEffectiveFrom] = useState("");
  const [effectiveUntil, setEffectiveUntil] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [created, setCreated] = useState<ObjectBinding | null>(null);

  const contract =
    contractObjects.find((option) => contractKey(option) === selectedContract) ??
    null;
  const external =
    externalObjects.find((option) => externalKey(option) === selectedExternal) ??
    null;
  const effectiveFromIso = localDateTimeToIso(effectiveFrom);
  const effectiveUntilIso = localDateTimeToIso(effectiveUntil);
  const timeInvalid =
    (effectiveFrom !== "" && effectiveFromIso === null) ||
    (effectiveUntil !== "" && effectiveUntilIso === null);
  const ready =
    contract !== null && external !== null && confirmed && !timeInvalid && !pending;

  const bindings = [
    ...existingBindings.filter((binding) => binding.id !== created?.id),
    ...(created ? [created] : []),
  ];
  const unresolved = bindings.filter(
    (binding) => binding.status === "missing" || binding.status === "ambiguous",
  );

  async function submit(event: SyntheticEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!ready || contract === null || external === null) return;
    setError(null);
    setPending(true);
    try {
      const input: CreateObjectBindingInput = {
        contract_object: {
          object_type: contract.objectType,
          object_id: contract.objectId,
          label: contract.label,
        },
        external_object: {
          connector_id: external.connectorId,
          external_object_id: external.externalObjectId,
        },
        service_object_id: contract.serviceObjectId,
        expected_object_version: contract.objectVersion,
        expected_association_version: contract.associationVersion,
        effective_from: effectiveFromIso,
        effective_until: effectiveUntilIso,
        confirmed: true,
      };
      const binding = await client.createObjectBinding(
        caseId,
        input,
        keyFor(`binding:${caseId}:${selectedContract}:${selectedExternal}`, input),
      );
      setCreated(binding);
      setConfirmed(false);
      onCreated?.(binding);
    } catch (caught) {
      setError(caught);
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="workflow-panel" aria-label="对象映射">
      <header className="workflow-panel-head">
        <div>
          <h2>合同对象与外部对象映射</h2>
          <p className="meta">
            映射由人工逐项确认并按版本保存；缺失或多义映射只按信息不足处理。
          </p>
        </div>
      </header>

      {bindings.length === 0 ? (
        <PageStatus
          kind="empty"
          title="尚未报告对象映射"
          detail="没有映射时不会产生确定性结论，也不会按名称相似度自动匹配。"
        />
      ) : (
        <Table<ObjectBinding>
          rowKey={(binding) => binding.id}
          dataSource={bindings}
          columns={bindingColumns}
          pagination={false}
          size="small"
        />
      )}

      {unresolved.length > 0 ? (
        <p className="callout">
          存在缺失或多义映射：这些映射不产生确定性结论，请人工补充确认后再核验。
        </p>
      ) : null}

      <form className="workflow-form" onSubmit={(event) => void submit(event)}>
        <h3>人工确认对象映射</h3>
        <p className="meta">
          合同对象与外部对象必须逐项人工选择并勾选确认；系统不会按名称相似度自动匹配。
        </p>
        <label className="field">
          合同对象
          <Select<string>
            aria-label="合同对象"
            placeholder="选择合同业务对象"
            value={selectedContract || undefined}
            onChange={setSelectedContract}
            disabled={pending}
            options={contractObjects.map((option) => ({
              value: contractKey(option),
              label: `${option.label}（${option.objectType} · ${option.objectId}）`,
            }))}
          />
        </label>
        <label className="field">
          外部对象
          <Select<string>
            aria-label="外部对象"
            placeholder="选择已登记连接器中的外部对象"
            value={selectedExternal || undefined}
            onChange={setSelectedExternal}
            disabled={pending || externalObjects.length === 0}
            options={externalObjects.map((option) => ({
              value: externalKey(option),
              label: `${option.label}（${option.connectorId} · ${option.externalObjectId}）`,
            }))}
          />
        </label>
        {contractObjects.length === 0 || externalObjects.length === 0 ? (
          <p className="callout">
            没有可确认的候选对象。请先由管理员登记合同对象与只读连接器；当前不能产生映射结论。
          </p>
        ) : null}
        <label className="field">
          生效时间（本地时间，可选）
          <Input
            type="datetime-local"
            value={effectiveFrom}
            onChange={(event) => setEffectiveFrom(event.target.value)}
            disabled={pending}
          />
        </label>
        <label className="field">
          失效时间（本地时间，可选）
          <Input
            type="datetime-local"
            value={effectiveUntil}
            onChange={(event) => setEffectiveUntil(event.target.value)}
            disabled={pending}
          />
        </label>
        {timeInvalid ? (
          <p className="callout">时间无法识别，请重新选择。</p>
        ) : null}
        <Checkbox
          checked={confirmed}
          onChange={(event) => setConfirmed(event.target.checked)}
          disabled={pending}
        >
          我已人工核对上述合同对象与外部对象的对应关系
        </Checkbox>
        <p className="meta">
          确认人和映射版本由服务端根据当前会话返回，页面不会代替服务端填写。
        </p>
        <OperatorAuditError error={error} />
        {created ? (
          <p role="status">
            映射已确认，版本 {created.version ?? "版本未报告"}，确认人{" "}
            {created.confirmedBy ?? "未报告"}。
          </p>
        ) : null}
        <Button
          type="primary"
          htmlType="submit"
          loading={pending}
          disabled={!ready}
        >
          确认对象映射
        </Button>
      </form>
    </section>
  );
}

/** Default off; the Lead mounts this panel only after wiring candidates. */
export function ObjectBindingPanel(props: ObjectBindingPanelProps) {
  return (
    <ExternalEvidenceGate flags={props.flags}>
      <ObjectBindingPanelContent {...props} />
    </ExternalEvidenceGate>
  );
}
