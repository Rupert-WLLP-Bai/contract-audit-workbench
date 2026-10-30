import { useQuery } from "@tanstack/react-query";
import { Button, Card, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { errorMessage, isForbidden } from "../../api/http";
import { PageStatus } from "../../components/page-status";
import { StatusText } from "../../components/status-text";
import { defaultOperatorAuditClient, type OperatorAuditClient } from "./api";
import { type OperatorAuditFlags } from "./feature-flags";
import { ExternalEvidenceGate } from "./external-evidence-gate";
import {
  availabilityLabel,
  availabilityTone,
  dataClassLabel,
  dataClassTone,
  jobStateLabel,
  jobStateTone,
  timeLabel,
} from "./labels";
import type {
  ExternalCollectionJob,
  ExternalField,
  ExternalObservation,
} from "./types";

export interface ExternalEvidencePanelProps {
  caseId: string;
  client?: OperatorAuditClient;
  flags?: OperatorAuditFlags;
  /** Job references already accepted by the server; acceptance is not evidence. */
  jobs?: ExternalCollectionJob[];
  /** Business instant used only to check the declared effective window. */
  businessAt?: string | null;
}

const fieldColumns: ColumnsType<ExternalField> = [
  {
    title: "字段",
    key: "name",
    render: (_, field) => <span className="digest">{field.name}</span>,
  },
  {
    title: "值",
    key: "value",
    render: (_, field) => field.value ?? "未报告",
  },
  {
    title: "单位",
    key: "unit",
    render: (_, field) => field.unit ?? "单位未报告",
  },
  {
    title: "JSON Pointer",
    key: "pointer",
    render: (_, field) => (
      <span className="digest">{field.sourcePointer ?? "未报告"}</span>
    ),
  },
  {
    title: "语义类型",
    key: "semanticKind",
    render: (_, field) => field.semanticKind ?? "未报告",
  },
];

const jobColumns: ColumnsType<ExternalCollectionJob> = [
  {
    title: "采集任务",
    key: "job",
    render: (_, job) => <span className="digest">{job.jobId}</span>,
  },
  {
    title: "状态",
    key: "state",
    render: (_, job) => (
      <StatusText tone={jobStateTone(job.state)}>
        {jobStateLabel(job.state)}
      </StatusText>
    ),
  },
  {
    title: "连接器",
    key: "connector",
    render: (_, job) => job.connectorId ?? "未报告",
  },
  {
    title: "外部对象",
    key: "object",
    render: (_, job) =>
      job.externalObjectId ? (
        <span className="digest">{job.externalObjectId}</span>
      ) : (
        "未报告"
      ),
  },
  {
    title: "受理时间",
    key: "acceptedAt",
    render: (_, job) => timeLabel(job.acceptedAt),
  },
];

function effectiveWindowLabel(snapshot: ExternalObservation): string {
  const from =
    snapshot.effectiveFrom === null
      ? "开始时间未报告"
      : timeLabel(snapshot.effectiveFrom);
  const until =
    snapshot.effectiveUntil === null
      ? "结束时间未报告"
      : timeLabel(snapshot.effectiveUntil);
  return `${from} 至 ${until}`;
}

/** Null means the declared window or business instant is unknown. */
function coversBusinessAt(
  snapshot: ExternalObservation,
  businessAt: string | null | undefined,
): boolean | null {
  if (!businessAt || !snapshot.effectiveFrom || !snapshot.effectiveUntil)
    return null;
  const at = Date.parse(businessAt);
  const from = Date.parse(snapshot.effectiveFrom);
  const until = Date.parse(snapshot.effectiveUntil);
  if (Number.isNaN(at) || Number.isNaN(from) || Number.isNaN(until))
    return null;
  return from <= at && at < until;
}

function availabilityNote(
  availability: ExternalObservation["availability"],
): string | null {
  switch (availability) {
    case "unavailable":
      return "该快照不可用：不能作为证据，也不能据此形成确定性结论。";
    case "stale":
      return "该快照已过期：不能作为当前证据；缺失或过期不等于合规。";
    case "rejected":
      return "该快照已被拒绝：不能进入核验。";
    case "unknown":
      return "可用性未报告：不视为可用证据。";
    default:
      return null;
  }
}

function SnapshotCard({
  snapshot,
  businessAt,
}: {
  snapshot: ExternalObservation;
  businessAt: string | null | undefined;
}) {
  const note = availabilityNote(snapshot.availability);
  const covers = coversBusinessAt(snapshot, businessAt);
  return (
    <Card
      size="small"
      title={
        <div className="workflow-panel-head">
          <div>
            <strong>{snapshot.sourceSystem ?? "来源系统未报告"}</strong>
            <div className="meta">
              {snapshot.connectorId ?? "连接器未报告"} ·{" "}
              <span className="digest">{snapshot.snapshotId}</span>
            </div>
          </div>
          <div className="rulepack-badges">
            <StatusText tone={availabilityTone(snapshot.availability)}>
              {availabilityLabel(snapshot.availability)}
            </StatusText>
            <StatusText tone={dataClassTone(snapshot.dataClass)}>
              {dataClassLabel(snapshot.dataClass)}
            </StatusText>
          </div>
        </div>
      }
    >
      <dl className="execution-dl">
        <div>
          <dt>记录</dt>
          <dd>
            {snapshot.recordId ?? "记录未报告"}
            {snapshot.recordVersion ? ` · ${snapshot.recordVersion}` : ""}
          </dd>
        </div>
        <div>
          <dt>外部对象</dt>
          <dd>
            {snapshot.externalObjectId ?? "对象未报告"} · 映射版本{" "}
            {snapshot.objectBindingVersion ?? "未报告"}
          </dd>
        </div>
        <div>
          <dt>观测时间（observed_at）</dt>
          <dd>{timeLabel(snapshot.observedAt)}</dd>
        </div>
        <div>
          <dt>来源更新时间（source_updated_at）</dt>
          <dd>{timeLabel(snapshot.sourceUpdatedAt)}</dd>
        </div>
        <div>
          <dt>有效区间（effective window）</dt>
          <dd>{effectiveWindowLabel(snapshot)}</dd>
        </div>
        <div>
          <dt>原因代码</dt>
          <dd className="digest">{snapshot.reasonCode ?? "未报告"}</dd>
        </div>
        <div>
          <dt>原始响应引用</dt>
          <dd className="digest">{snapshot.payloadRef ?? "未报告"}</dd>
        </div>
      </dl>
      {note ? <p className="callout">{note}</p> : null}
      {snapshot.availability === "available" ? (
        <p className="meta">
          可用只表示该快照可用于核验，不表示业务关系已经成立。
        </p>
      ) : null}
      {covers === false ? (
        <p className="callout">
          业务时点不在该快照的有效区间内，不能作为该时点的证据。
        </p>
      ) : null}
      {snapshot.dataClass === "synthetic" ? (
        <p className="meta">
          合成 fixture 数据：仅用于组件与流程演示，不是生产数据。
        </p>
      ) : null}
      <Table<ExternalField>
        rowKey={(field) => `${field.name}:${field.sourcePointer ?? ""}`}
        dataSource={snapshot.fields}
        columns={fieldColumns}
        pagination={false}
        size="small"
        locale={{ emptyText: "该快照没有报告规范化字段。" }}
      />
    </Card>
  );
}

function ExternalEvidencePanelContent({
  caseId,
  client = defaultOperatorAuditClient,
  jobs = [],
  businessAt,
}: ExternalEvidencePanelProps) {
  const snapshots = useQuery({
    queryKey: ["operator-audit", "external-snapshots", caseId],
    queryFn: ({ signal }) => client.listExternalSnapshots(caseId, signal),
    enabled: caseId !== "",
  });
  const rejectedJobs = jobs.filter(
    (job) => job.state === "denied" || job.state === "rejected",
  );

  return (
    <section className="workflow-panel" aria-label="外部证据">
      <header className="workflow-panel-head">
        <div>
          <h2>外部证据快照</h2>
          <p className="meta">
            采集任务受理不代表已取得证据；只有可用且在有效期内的快照才能参与核验。
          </p>
        </div>
        <Button
          htmlType="button"
          loading={snapshots.isFetching}
          onClick={() => void snapshots.refetch()}
        >
          刷新外部快照
        </Button>
      </header>

      {jobs.length > 0 ? (
        <section aria-label="采集任务状态">
          <h3>采集任务</h3>
          <Table<ExternalCollectionJob>
            rowKey={(job) => job.jobId}
            dataSource={jobs}
            columns={jobColumns}
            pagination={false}
            size="small"
          />
          {rejectedJobs.length > 0 ? (
            <p className="callout">
              有采集任务被拒绝：未取得外部数据，不能据此形成结论。
            </p>
          ) : null}
        </section>
      ) : null}

      {snapshots.isLoading ? (
        <PageStatus kind="loading" title="正在读取外部证据快照" />
      ) : null}
      {snapshots.isError && isForbidden(snapshots.error) ? (
        <PageStatus
          kind="denied"
          title="没有查看外部证据的权限"
          detail="外部快照只返回给有案件授权的成员，拒绝时不会返回快照内容。"
        />
      ) : null}
      {snapshots.isError && !isForbidden(snapshots.error) ? (
        <PageStatus
          kind="error"
          title="外部快照读取失败"
          detail={errorMessage(snapshots.error)}
          action={
            <Button htmlType="button" onClick={() => void snapshots.refetch()}>
              重试读取外部快照
            </Button>
          }
        />
      ) : null}
      {snapshots.isSuccess && snapshots.data.length === 0 ? (
        <PageStatus
          kind="empty"
          title="当前案件没有外部快照"
          detail="没有快照不代表合规；请检查采集任务状态与案件授权。"
        />
      ) : null}
      {snapshots.isSuccess && snapshots.data.length > 0 ? (
        <div className="evidence-stack">
          {snapshots.data.map((snapshot) => (
            <SnapshotCard
              key={snapshot.snapshotId}
              snapshot={snapshot}
              businessAt={businessAt}
            />
          ))}
        </div>
      ) : null}
    </section>
  );
}

/** Default off; fixture or live mode must be wired explicitly. */
export function ExternalEvidencePanel(props: ExternalEvidencePanelProps) {
  return (
    <ExternalEvidenceGate flags={props.flags}>
      <ExternalEvidencePanelContent {...props} />
    </ExternalEvidenceGate>
  );
}
