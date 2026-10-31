import { Alert, Button, Card, Select, Space } from "antd";
import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { errorMessage } from "../api/http";
import { clearPendingMutationKey, idempotencyActorScope, pendingMutationKey } from "../api/idempotency";
import {
  useCreateSubjectSourceSnapshot,
  useSession,
  useSubjectSourceDirectory,
  useSubjectSourceSnapshot,
  useSubjectSourceSnapshots,
  useVerifySubjectAgainstSourceSnapshot,
} from "../api/queries";
import type { CaseRoleBindingProjection, SubjectSourceVerificationResult } from "../api/types";
import { formatDateTime } from "../domain/review";

export function SubjectSourceVerificationPanel({
  caseId,
  bindings,
  canManage,
}: {
  caseId: string;
  bindings: CaseRoleBindingProjection[];
  canManage: boolean;
}) {
  const session = useSession();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const sourceCase = searchParams.get("source_case");
  const snapshotId = sourceCase === caseId ? searchParams.get("source_snapshot") ?? "" : "";
  const [snapshotCursor, setSnapshotCursor] = useState<string | null>(null);
  const [snapshotCursorStack, setSnapshotCursorStack] = useState<Array<string | null>>([]);
  const snapshotList = useSubjectSourceSnapshots(caseId, snapshotCursor);
  const sourceDirectory = useSubjectSourceDirectory(caseId);
  const selectedSnapshot = useSubjectSourceSnapshot(caseId, snapshotId);
  const selectedSnapshotMatchesCase = selectedSnapshot.data?.case_id === caseId;
  const createSnapshot = useCreateSubjectSourceSnapshot(caseId);
  const [bindingId, setBindingId] = useState<string | undefined>();
  const [result, setResult] = useState<SubjectSourceVerificationResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const eligibleBindings = bindings.filter((item) => item.binding_state !== "inactive");
  const verify = useVerifySubjectAgainstSourceSnapshot(caseId, snapshotId);

  async function captureSnapshot() {
    if (!session.data) return;
    const source = sourceDirectory.data?.items.find((item) => item.source_id === "synthetic.uscc.demo");
    if (!source) {
      setError("当前案件尚未配置可用的合成演示来源。请联系管理员配置来源目录。");
      return;
    }
    setError(null);
    setResult(null);
    try {
      const input = { source_id: source.source_id, expected_source_version: source.source_version };
      const pending = await pendingMutationKey(
        "subject.source-snapshot.create",
        idempotencyActorScope(session.data),
        caseId,
        input,
      );
      const value = await createSnapshot.mutateAsync({ value: input, idempotencyKey: pending.key });
      const next = new URLSearchParams(searchParams);
      next.set("source_case", caseId);
      next.set("source_snapshot", value.snapshot_id);
      setSearchParams(next, { replace: true });
      setSnapshotCursor(null);
      setSnapshotCursorStack([]);
      setBindingId(undefined);
      try {
        clearPendingMutationKey(pending.storageKey);
      } catch {
        setError("来源快照已生成，但浏览器无法清理安全重试标识。请勿重复点击；如需继续，请刷新后重试。");
      }
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  async function verifySelectedBinding() {
    const snapshot = selectedSnapshot.data;
    if (!session.data || !snapshot || !selectedSnapshotMatchesCase || !bindingId) return;
    const binding = eligibleBindings.find((item) => item.binding_id === bindingId);
    if (!binding) {
      setError("请从当前案件中重新选择主体绑定。");
      return;
    }
    setError(null);
    setResult(null);
    const input = {
      subject_id: binding.subject.subject_id,
      expected_subject_version: binding.subject.version,
      expected_binding_id: binding.binding_id,
    };
    try {
      const pending = await pendingMutationKey(
        "subject.source-snapshot.verify",
        idempotencyActorScope(session.data),
        `${caseId}:${snapshot.snapshot_id}`,
        input,
      );
      const value = await verify.mutateAsync({ value: input, idempotencyKey: pending.key });
      setResult(value);
      try {
        clearPendingMutationKey(pending.storageKey);
      } catch {
        setError("查询已完成，但浏览器无法清理安全重试标识。请勿重复提交；如需继续，请刷新后重试。");
      }
      if (value.request_id) {
        await navigate(`/cases/${encodeURIComponent(caseId)}/subject-changes/${encodeURIComponent(value.request_id)}`);
      }
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  return (
    <Card title="受控来源对照（合成演示）" className="subject-source-verification-panel">
      <Alert
        type="info"
        showIcon
        title="本功能仅对照系统配置的合成演示数据，不代表真实工商或主体身份核验。匹配只会发起待另一名有权审批人复核的更正申请，不会直接更改主体状态。"
      />
      <div className="subject-source-actions">
        <Select
          aria-label="选择已有合成来源快照"
          value={snapshotId || undefined}
          onChange={(value: string) => {
            const next = new URLSearchParams(searchParams);
            next.set("source_case", caseId);
            next.set("source_snapshot", value);
            setSearchParams(next, { replace: true });
            setBindingId(undefined);
            setResult(null);
            setError(null);
          }}
          placeholder="选择已有快照以继续办理"
          loading={snapshotList.isLoading}
          options={(snapshotList.data?.items ?? []).map((item) => ({
            value: item.snapshot_id,
            label: `${item.label} · 来源 v${item.source_version} · 固定于 ${formatDateTime(item.captured_at)}`,
          }))}
        />
          <Button onClick={() => void captureSnapshot()} loading={createSnapshot.isPending} disabled={!canManage || !sourceDirectory.data?.items.some((item) => item.source_id === "synthetic.uscc.demo")}>
          固定新的合成来源版本
        </Button>
      </div>
      {sourceDirectory.isError ? <Alert type="error" showIcon title="来源目录暂不可用" description={errorMessage(sourceDirectory.error)} action={<Button onClick={() => void sourceDirectory.refetch()}>重试</Button>} /> : null}
      {sourceDirectory.data?.items.length === 0 ? <Alert type="info" showIcon title="当前案件尚未配置可用的合成演示来源，请联系管理员配置。" /> : null}
      {Array.from(new Map((sourceDirectory.data?.items ?? []).map((source) => [`${source.source_id}:${source.source_version}`, source])).values()).map((source) => (
        <div className="subject-source-actions" key={`${source.source_id}:${source.source_version}`}>
          <span>{source.display_name} · 来源版本 v{source.source_version} · 受控来源（合成演示）</span>
        </div>
      ))}
      {snapshotList.isError ? <Alert type="error" showIcon title="合成来源快照列表暂不可用" description={errorMessage(snapshotList.error)} action={<Button onClick={() => void snapshotList.refetch()}>重试</Button>} /> : null}
      {snapshotList.data ? (
        <Space className="subject-source-actions">
          <span>当前页 {snapshotList.data.items.length} 个可见快照</span>
          <Button disabled={snapshotCursorStack.length === 0 || snapshotList.isFetching} onClick={() => {
            const stack = [...snapshotCursorStack];
            setSnapshotCursor(stack.pop() ?? null);
            setSnapshotCursorStack(stack);
          }}>上一页</Button>
          <Button disabled={snapshotList.data.next_cursor === null || snapshotList.isFetching} onClick={() => {
            if (snapshotList.data?.next_cursor == null) return;
            setSnapshotCursorStack((stack) => [...stack, snapshotCursor]);
            setSnapshotCursor(snapshotList.data.next_cursor);
          }}>下一页</Button>
        </Space>
      ) : null}
      {snapshotId && selectedSnapshot.isError ? <Alert type="error" showIcon title="所选合成来源快照暂不可用" description={errorMessage(selectedSnapshot.error)} action={<Button onClick={() => void selectedSnapshot.refetch()}>重试</Button>} /> : null}
      {selectedSnapshot.data && !selectedSnapshotMatchesCase ? <Alert type="error" showIcon title="所选快照与当前案件不匹配，请重新选择。" /> : null}
      {selectedSnapshot.data && selectedSnapshotMatchesCase ? (
        <div className="subject-source-actions">
          <span>{selectedSnapshot.data.label} · 来源 v{selectedSnapshot.data.source_version} · 快照时间 {formatDateTime(selectedSnapshot.data.captured_at)}</span>
          <span>固定记录 {selectedSnapshot.data.record_count} 条</span>
        </div>
      ) : null}
      {snapshotId && selectedSnapshot.isLoading ? <p>正在读取所选合成来源快照…</p> : null}
      {selectedSnapshot.data && selectedSnapshotMatchesCase ? (
        <Space wrap className="subject-source-verify-row">
          <Select
            aria-label="选择待对照的案件主体"
            value={bindingId}
            onChange={(value: string) => setBindingId(value)}
            placeholder="选择本案主体"
            options={eligibleBindings.map((item) => ({
              value: item.binding_id,
              label: `${item.subject.display_name} · ${item.role === "customer" ? "客户" : item.role === "supplier" ? "供应商" : item.role === "contractor" ? "承包方" : item.role === "service_provider" ? "服务提供方" : "其他角色"} · 身份 v${item.subject.version}`,
            }))}
          />
          <Button type="primary" disabled={!canManage || !bindingId || !selectedSnapshotMatchesCase} loading={verify.isPending} onClick={() => void verifySelectedBinding()}>
            对照合成来源并发起审批
          </Button>
        </Space>
      ) : null}
      {error ? <Alert type="error" showIcon title={error} /> : null}
      {result?.match_outcome === "exact_match" ? (
        <Alert
          type="warning"
          showIcon
          title="受控来源找到一致记录；更正仍待另一名有权审批人复核，尚未生效。"
          description={
            <div className="subject-source-match">
              <span>{result.matched_record?.display_name}</span>
              <span>{result.matched_record?.masked_identifier}</span>
              <span>来源记录时间 {result.source_as_of ? formatDateTime(result.source_as_of) : "未提供"}，适用期间 {result.valid_from ? formatDateTime(result.valid_from) : "未提供"} 至 {result.valid_until ? formatDateTime(result.valid_until) : "未设截止"}</span>
              {result.request_id ? <Link to={`/cases/${encodeURIComponent(caseId)}/subject-changes/${encodeURIComponent(result.request_id)}`}>查看更正与审批详情</Link> : null}
            </div>
          }
        />
      ) : null}
      {result?.match_outcome === "no_match" ? <Alert type="info" showIcon title="合成来源没有找到一致记录；未创建更正申请。" /> : null}
      {result?.match_outcome === "ambiguous" ? <Alert type="warning" showIcon title="合成来源匹配到多个可能记录；未创建更正申请。" /> : null}
    </Card>
  );
}
