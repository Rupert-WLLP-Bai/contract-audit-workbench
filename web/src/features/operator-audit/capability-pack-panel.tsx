import { useQuery } from "@tanstack/react-query";
import { Button, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { errorMessage, isForbidden } from "../../api/http";
import { PageStatus } from "../../components/page-status";
import { StatusText } from "../../components/status-text";
import { defaultOperatorAuditClient, type OperatorAuditClient } from "./api";
import {
  DEFAULT_OPERATOR_AUDIT_FLAGS,
  type OperatorAuditFlags,
} from "./feature-flags";
import {
  dataClassLabel,
  dataClassTone,
  packStatusLabel,
  packStatusTone,
  scenarioLabel,
} from "./labels";
import type { CapabilityPack } from "./types";

export interface CapabilityPackPanelProps {
  client?: OperatorAuditClient;
  flags?: OperatorAuditFlags;
}

const packQueryKey = ["operator-audit", "capability-packs"] as const;

function PackDetails({ pack }: { pack: CapabilityPack }) {
  return (
    <dl className="execution-dl">
      <div>
        <dt>规则包</dt>
        <dd>
          {pack.rulePack
            ? `${pack.rulePack.id} @ ${pack.rulePack.version ?? "版本未报告"}`
            : "规则包未报告"}
        </dd>
      </div>
      <div>
        <dt>宿主契约</dt>
        <dd>{pack.hostContractVersion ?? "未报告"}</dd>
      </div>
      <div>
        <dt>材料角色</dt>
        <dd>
          {pack.requiredMaterialRoles.length > 0
            ? pack.requiredMaterialRoles.join("、")
            : "未报告"}
        </dd>
      </div>
      <div>
        <dt>只读能力</dt>
        <dd>
          {pack.capabilities.length > 0
            ? pack.capabilities
                .map(
                  (capability) =>
                    `${capability.id}${
                      capability.connectorRef
                        ? `（${capability.connectorRef}）`
                        : "（连接器未报告）"
                    }`,
                )
                .join("；")
            : "未报告"}
        </dd>
      </div>
      <div>
        <dt>检查项</dt>
        <dd>
          {pack.checks.length > 0 ? (
            <ul>
              {pack.checks.map((check) => (
                <li key={check.id}>
                  {check.id}：{check.contractField ?? "合同字段未报告"} ↔{" "}
                  {check.externalField ?? "外部字段未报告"}；比较器{" "}
                  {check.comparatorRef ?? "未报告"}
                </li>
              ))}
            </ul>
          ) : (
            "未报告"
          )}
        </dd>
      </div>
    </dl>
  );
}

function CapabilityPackPanelContent({
  client = defaultOperatorAuditClient,
}: CapabilityPackPanelProps) {
  const packs = useQuery({
    queryKey: packQueryKey,
    queryFn: ({ signal }) => client.listCapabilityPacks(signal),
  });

  const columns: ColumnsType<CapabilityPack> = [
    {
      title: "能力包",
      key: "pack",
      render: (_, pack) => (
        <>
          <strong>{pack.displayName ?? pack.packId}</strong>
          <div className="meta">
            <span className="digest">{pack.packId}</span>
          </div>
        </>
      ),
    },
    {
      title: "版本",
      key: "version",
      render: (_, pack) => (
        <span className="digest">{pack.version ?? "版本未报告"}</span>
      ),
    },
    {
      title: "场景",
      key: "scenario",
      render: (_, pack) => scenarioLabel(pack.scenario),
    },
    {
      title: "状态",
      key: "status",
      render: (_, pack) => (
        <StatusText tone={packStatusTone(pack.status)}>
          {packStatusLabel(pack.status)}
        </StatusText>
      ),
    },
    {
      title: "数据分类",
      key: "dataClass",
      render: (_, pack) => (
        <StatusText tone={dataClassTone(pack.dataClass)}>
          {dataClassLabel(pack.dataClass)}
        </StatusText>
      ),
    },
  ];

  return (
    <section className="workflow-panel" aria-label="能力包目录">
      <header className="workflow-panel-head">
        <div>
          <h2>能力包目录</h2>
          <p className="meta">
            包版本随每次执行固定；后续升级不会改写历史审查。空目录不代表审查通过。
          </p>
        </div>
        <Button
          htmlType="button"
          loading={packs.isFetching}
          onClick={() => void packs.refetch()}
        >
          刷新能力包
        </Button>
      </header>
      {packs.isLoading ? (
        <PageStatus kind="loading" title="正在读取能力包目录" />
      ) : null}
      {packs.isError && isForbidden(packs.error) ? (
        <PageStatus
          kind="denied"
          title="没有查看能力包目录的权限"
          detail="能力包只会返回给已授权的会话与案件范围。"
        />
      ) : null}
      {packs.isError && !isForbidden(packs.error) ? (
        <PageStatus
          kind="error"
          title="能力包目录读取失败"
          detail={errorMessage(packs.error)}
          action={
            <Button htmlType="button" onClick={() => void packs.refetch()}>
              重试读取能力包
            </Button>
          }
        />
      ) : null}
      {packs.isSuccess && packs.data.length === 0 ? (
        <PageStatus
          kind="empty"
          title="当前范围没有已发布的能力包"
          detail="空目录不代表审查通过，也不改变已有案件。"
        />
      ) : null}
      {packs.isSuccess && packs.data.length > 0 ? (
        <Table<CapabilityPack>
          rowKey={(pack) => pack.packId}
          dataSource={packs.data}
          columns={columns}
          pagination={false}
          size="small"
          expandable={{
            expandedRowRender: (pack) => <PackDetails pack={pack} />,
          }}
        />
      ) : null}
    </section>
  );
}

/**
 * Mounted only when the Lead enables the flag. The default is off, so importing
 * this module does not add a visible or clickable surface.
 */
export function CapabilityPackPanel(props: CapabilityPackPanelProps) {
  const flags = props.flags ?? DEFAULT_OPERATOR_AUDIT_FLAGS;
  if (!flags.capabilityPacksEnabled) return null;
  return <CapabilityPackPanelContent {...props} />;
}
