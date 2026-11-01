import { Alert, Button, Card, Descriptions, Tag } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef } from "react";
import {
  getContractSourceCapability,
  type ContractDetail,
} from "../api/contracts";
import { ApiError, errorMessage } from "../api/http";
import type { Actor } from "../api/types";
import { formatDateTime } from "../domain/review";

interface ContractSourceCapabilityPanelProps {
  detail: ContractDetail;
  actor: Actor | null;
  onAccessInvalidated: (contractId: string, actorScope: string) => void;
}

function actorKey(actor: Actor | null): string {
  return actor === null
    ? ""
    : [actor.audience, actor.tenant_id, actor.org_id, actor.id, actor.role].join("\u0000");
}

function sourceKindLabel(kind: "mock" | "real" | "unconfigured"): string {
  switch (kind) {
    case "mock":
      return "模拟来源";
    case "real":
      return "真实来源类型已配置";
    case "unconfigured":
      return "来源通道未配置";
  }
}

function versionOrderingLabel(ordering: "opaque" | "monotonic-sequence" | "unknown"): string {
  switch (ordering) {
    case "opaque":
      return "不透明版本标识";
    case "monotonic-sequence":
      return "单调递增序号";
    case "unknown":
      return "未知";
  }
}

function observationDispositionLabel(
  disposition: ContractDetail["source_observation_disposition"],
): string {
  switch (disposition) {
    case "applied":
      return "已应用为外部镜像修订";
    case "observation_only":
      return "仅留存观察记录，未生成修订";
    case "unknown":
      return "应用状态未知";
    default:
      return "未提供观察记录状态";
  }
}

export function ContractSourceCapabilityPanel({
  detail,
  actor,
  onAccessInvalidated,
}: ContractSourceCapabilityPanelProps) {
  const contractId = detail.summary.contract_id;
  const currentActorKey = actorKey(actor);
  const queryClient = useQueryClient();
  const queryKey = useMemo(
    () => ["contracts", "source-capability", contractId, currentActorKey] as const,
    [contractId, currentActorKey],
  );
  const generationRef = useRef(0);
  const currentIdentityRef = useRef({ contractId, actorScope: currentActorKey });
  currentIdentityRef.current = { contractId, actorScope: currentActorKey };
  const invalidationHandlerRef = useRef(onAccessInvalidated);
  const invalidationNotifiedRef = useRef(false);
  invalidationHandlerRef.current = onAccessInvalidated;
  const capability = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      const generation = ++generationRef.current;
      const requestContractId = contractId;
      const requestActorScope = currentActorKey;
      const response = await getContractSourceCapability(requestContractId, signal);
      if (
        signal.aborted ||
        generation !== generationRef.current ||
        response.contract_id !== requestContractId ||
        currentIdentityRef.current.contractId !== requestContractId ||
        currentIdentityRef.current.actorScope !== requestActorScope
      )
        throw new DOMException("合同来源能力响应已过期", "AbortError");
      return response;
    },
    enabled: actor !== null,
    staleTime: 0,
  });
  const accessInvalidated =
    actor === null ||
    (capability.error instanceof ApiError &&
      [401, 403, 404].includes(capability.error.status));

  useEffect(() => {
    if (!accessInvalidated) {
      invalidationNotifiedRef.current = false;
      return;
    }
    if (invalidationNotifiedRef.current) return;
    invalidationNotifiedRef.current = true;
    void queryClient.cancelQueries({ queryKey, exact: true });
    invalidationHandlerRef.current(contractId, currentActorKey);
  }, [accessInvalidated, contractId, currentActorKey, queryClient, queryKey]);

  useEffect(
    () => () => {
      generationRef.current += 1;
      void queryClient.cancelQueries({ queryKey, exact: true });
      queryClient.removeQueries({ queryKey, exact: true });
    },
    [queryClient, queryKey],
  );

  return (
    <Card
      title="外部来源合同"
      className="contract-source-capability-panel"
      extra={<Tag>只读镜像</Tag>}
    >
      <Alert
        type="info"
        showIcon
        title="合同内容由外部来源管理"
        description="本页展示本地留存的来源观察事实，不提供本地草稿、会签、签署或状态写回入口。外部来源的配置声明不代表连接已成功。"
      />

      {actor === null ? (
        <Alert
          type="warning"
          showIcon
          title="当前会话不可用"
          description="已清除当前合同详情。请在有效会话下重新选择合同。"
        />
      ) : accessInvalidated ? (
        <Alert
          type="warning"
          showIcon
          title="合同来源访问已失效"
          description="当前会话已失效，或该合同的查看授权已变化。为避免显示旧信息，当前合同详情已清除。"
        />
      ) : capability.isLoading ? (
        <p role="status">正在读取来源能力声明…</p>
      ) : capability.isError ? (
        <Alert
          type="error"
          showIcon
          title="来源能力信息暂不可用"
          description={errorMessage(capability.error)}
          action={
            <Button size="small" onClick={() => void capability.refetch()}>
              重试
            </Button>
          }
        />
      ) : capability.data ? (
        <Descriptions bordered size="small" column={1}>
          <Descriptions.Item label="来源标识">
            {capability.data.source_id}
          </Descriptions.Item>
          <Descriptions.Item label="来源通道">
            {sourceKindLabel(capability.data.source_kind)}
          </Descriptions.Item>
          <Descriptions.Item label="连接状态">未探测</Descriptions.Item>
          <Descriptions.Item label="来源版本顺序">
            {versionOrderingLabel(capability.data.version_ordering)}
          </Descriptions.Item>
          <Descriptions.Item label="来源读取声明">
            {capability.data.can_read_contract
              ? "当前来源通道声明可读取该来源合同"
              : "该来源通道未声明可读取合同"}
            <span>（此项描述来源配置，不表示当前用户权限）</span>
          </Descriptions.Item>
        </Descriptions>
      ) : null}

      {!accessInvalidated ? (
      <Descriptions bordered size="small" column={1} title="本地来源观察">
        <Descriptions.Item label="合同名称">
          {detail.summary.name || "未知"}
        </Descriptions.Item>
        <Descriptions.Item label="合同编号">
          {detail.summary.number || "未知"}
        </Descriptions.Item>
        <Descriptions.Item label="金额">
          {detail.summary.amount !== null && detail.summary.currency !== null
            ? `${detail.summary.currency} ${detail.summary.amount}`
            : "金额未知"}
        </Descriptions.Item>
        <Descriptions.Item label="合同状态">外部来源已观测</Descriptions.Item>
        <Descriptions.Item label="工作流状态">外部来源已观测</Descriptions.Item>
        <Descriptions.Item label="来源通道标识">
          {detail.source_id || "未知"}
        </Descriptions.Item>
        <Descriptions.Item label="来源系统与合同标识">
          {detail.source_ref
            ? `${detail.source_ref.system} · ${detail.source_ref.id}`
            : "未知"}
        </Descriptions.Item>
        <Descriptions.Item label="来源版本">
          {detail.source_ref?.source_version || "未知"}
        </Descriptions.Item>
        <Descriptions.Item label="外部观察状态">
          {detail.source_observed_state || "未知"}
        </Descriptions.Item>
        <Descriptions.Item label="观察处理结果">
          {observationDispositionLabel(detail.source_observation_disposition)}
        </Descriptions.Item>
        <Descriptions.Item label="观察时间">
          {detail.source_observed_at
            ? formatDateTime(detail.source_observed_at)
            : "未知"}
        </Descriptions.Item>
      </Descriptions>
      ) : null}
    </Card>
  );
}
