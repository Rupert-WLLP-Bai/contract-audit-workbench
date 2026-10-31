import { Alert, Button, Tag } from "antd";
import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { errorMessage } from "../api/http";
import {
  archiveIntake,
  getIntakeContinuation,
  listPendingIntakes,
  type PendingIntake,
} from "../api/intakes";
import { queryKeys } from "../api/queries";
import type { Actor } from "../api/types";
import { caseHref, formatBytes, formatDateTime } from "../domain/review";
import type { IntakeContinuationSelection } from "../features/intake/intake-continuation";
import "./intake-pending-list.css";

const REASON_MESSAGES: Record<string, string> = {
  legacy_request_unavailable: "这是旧办理，服务器没有可验证的原始请求信息，无法安全续传。原案件仍可打开。",
  request_identity_invalid: "服务器无法验证原始文件身份，已停止续办以避免改写案件。",
  forbidden: "当前身份无权继续该办理。请使用原经办身份登录，或在有权查看时打开原案件。",
  read_only: "当前工作区处于只读状态，不能继续上传。",
  case_not_writable: "原案件当前不可写，不能继续上传。",
  completed: "服务器已确认该办理完成；请打开原案件查看。",
  archived: "该办理已归档。归档只从未完成办理列表中隐藏办理，不会删除原案件；已开始的上传仍可能完成。",
};

function actorScopeKey(actor: Actor | null): string {
  return actor === null
    ? ""
    : [actor.audience, actor.tenant_id, actor.org_id, actor.id].join("\u0000");
}

function reasonMessage(code: string): string {
  return (
    REASON_MESSAGES[code] ??
    "服务器当前没有开放继续办理。可以打开原案件，或刷新列表查看最新状态。"
  );
}

function pendingStatus(errorCode: string | null): string {
  if (errorCode === "document_upload_unavailable")
    return "主合同尚未上传完成";
  return errorCode === null ? "等待主合同上传" : "续办状态需要刷新";
}

export function IntakePendingList({
  actor,
  selectedOperationId = null,
  onSelectContinuation,
  onArchived,
}: {
  actor: Actor | null;
  selectedOperationId?: string | null;
  onSelectContinuation: (selection: IntakeContinuationSelection) => void;
  onArchived: (operationId: string) => void;
}) {
  const queryClient = useQueryClient();
  const actorScope = actorScopeKey(actor);
  const actorScopeRef = useRef(actorScope);
  actorScopeRef.current = actorScope;
  const [pageState, setPageState] = useState<{
    actorScope: string;
    cursors: (string | null)[];
  }>({ actorScope: "", cursors: [null] });
  const cursors =
    pageState.actorScope === actorScope ? pageState.cursors : [null];
  const cursor = cursors[cursors.length - 1] ?? null;
  const pageNumber = cursors.length;
  const [detailOperationId, setDetailOperationId] = useState<string | null>(null);
  const [detailMessage, setDetailMessage] = useState<{
    operationId: string;
    message: string;
  } | null>(null);
  const [archiveOperationId, setArchiveOperationId] = useState<string | null>(
    null,
  );
  const [archivingOperationId, setArchivingOperationId] = useState<
    string | null
  >(null);
  const [archiveError, setArchiveError] = useState<{
    operationId: string;
    message: string;
  } | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const detailSequence = useRef(0);
  const detailController = useRef<AbortController | null>(null);

  const pending = useQuery({
    queryKey: queryKeys.pendingIntakes(actorScope, cursor),
    queryFn: ({ signal }) => listPendingIntakes(cursor, signal),
    enabled: actor !== null,
    retry: false,
  });

  useEffect(() => {
    detailSequence.current += 1;
    detailController.current?.abort();
    detailController.current = null;
    setPageState({ actorScope, cursors: [null] });
    setDetailOperationId(null);
    setDetailMessage(null);
    setArchiveOperationId(null);
    setArchivingOperationId(null);
    setArchiveError(null);
    setNotice(null);
    return () => {
      detailSequence.current += 1;
      detailController.current?.abort();
      detailController.current = null;
    };
  }, [actorScope]);

  useEffect(() => {
    if (
      actor === null ||
      pending.isFetching ||
      pending.isError ||
      pending.data?.items.length !== 0 ||
      pending.data.next_cursor !== null ||
      cursors.length <= 1
    )
      return;
    setPageState({ actorScope, cursors: cursors.slice(0, -1) });
  }, [actor, actorScope, cursors, pending.data, pending.isError, pending.isFetching]);

  async function selectContinuation(item: PendingIntake) {
    if (
      actor === null ||
      !item.can_resume ||
      detailOperationId !== null ||
      archivingOperationId !== null
    )
      return;
    const requestScope = actorScope;
    const sequence = ++detailSequence.current;
    const controller = new AbortController();
    detailController.current?.abort();
    detailController.current = controller;
    setDetailOperationId(item.operation_id);
    setDetailMessage(null);
    setNotice(null);
    try {
      const detail = await getIntakeContinuation(
        item.operation_id,
        controller.signal,
      );
      if (
        controller.signal.aborted ||
        actorScopeRef.current !== requestScope ||
        sequence !== detailSequence.current
      )
        return;
      if (!detail.can_resume || detail.request === null || detail.idempotency_key === null) {
        setDetailMessage({
          operationId: item.operation_id,
          message: reasonMessage(detail.reason_code),
        });
        void queryClient.invalidateQueries({
          queryKey: queryKeys.pendingIntakesPrefix,
        });
        return;
      }
      onSelectContinuation({
        detail,
        error_code: item.error_code,
        archived: false,
      });
      setNotice(`已读取“${item.title}”的服务器原始请求。请选择原文件；文件校验通过后才会提交。`);
    } catch (error) {
      if (
        controller.signal.aborted ||
        actorScopeRef.current !== requestScope ||
        sequence !== detailSequence.current
      )
        return;
      setDetailMessage({
        operationId: item.operation_id,
        message: errorMessage(error),
      });
    } finally {
      if (
        actorScopeRef.current === requestScope &&
        sequence === detailSequence.current
      ) {
        setDetailOperationId(null);
        detailController.current = null;
      }
    }
  }

  async function confirmArchive(item: PendingIntake) {
    if (
      actor === null ||
      !item.can_archive ||
      archivingOperationId !== null ||
      detailOperationId !== null
    )
      return;
    const requestScope = actorScope;
    setArchivingOperationId(item.operation_id);
    setArchiveError(null);
    setNotice(null);
    try {
      await archiveIntake(item.operation_id);
      if (actorScopeRef.current !== requestScope) return;
      setArchiveOperationId(null);
      setNotice(
        `已归档“${item.title}”。原案件仍保留；已经开始的上传仍可能完成。`,
      );
      onArchived(item.operation_id);
      await queryClient.invalidateQueries({
        queryKey: queryKeys.pendingIntakesPrefix,
      });
    } catch (error) {
      if (actorScopeRef.current !== requestScope) return;
      setArchiveError({
        operationId: item.operation_id,
        message: errorMessage(error),
      });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.pendingIntakesPrefix,
      });
    } finally {
      if (actorScopeRef.current === requestScope)
        setArchivingOperationId(null);
    }
  }

  function retryList() {
    void pending.refetch();
  }

  const items = actor === null ? [] : (pending.data?.items ?? []);

  return (
    <section
      className="home-panel home-pending-intakes"
      aria-labelledby="intake-pending-title"
      id="intake-pending-list"
    >
      <header className="home-panel-head">
        <div className="home-section-title">
          <span aria-hidden="true" />
          <h2 id="intake-pending-title">我的未完成办理</h2>
          {pending.data ? (
            <strong className="home-figure-badge">{items.length}</strong>
          ) : null}
        </div>
        {actor !== null ? (
          <Button
            htmlType="button"
            disabled={pending.isFetching}
            onClick={retryList}
          >
            刷新列表
          </Button>
        ) : null}
      </header>
      <p className="intake-pending-summary">
        列表由服务器按当前身份和案件权限返回。清除本机续办信息、换浏览器或重新登录后，未完成办理仍可在这里找回。
      </p>
      {notice === null ? null : (
        <Alert className="intake-pending-feedback" type="success" showIcon title={notice} />
      )}
      {actor === null ? (
        <p className="intake-pending-empty" role="status">
          登录后可查看当前身份的未完成办理。
        </p>
      ) : null}
      {actor !== null && pending.isLoading ? (
        <p className="intake-pending-empty" role="status" aria-live="polite">
          正在读取服务器上的未完成办理…
        </p>
      ) : null}
      {actor !== null && pending.isError ? (
        <Alert
          className="intake-pending-feedback"
          type="error"
          showIcon
          title="未完成办理列表暂不可用"
          description={errorMessage(pending.error)}
          action={
            <Button htmlType="button" onClick={retryList}>
              重试
            </Button>
          }
        />
      ) : null}
      {actor !== null && pending.data && items.length === 0 ? (
        <p className="intake-pending-empty" role="status">
          当前没有未完成办理。
        </p>
      ) : null}
      {items.length > 0 ? (
        <ol className="intake-pending-rows" aria-busy={pending.isFetching}>
          {items.map((item) => {
            const detailBusy = detailOperationId === item.operation_id;
            const archiving = archivingOperationId === item.operation_id;
            const confirmingArchive = archiveOperationId === item.operation_id;
            const message =
              detailMessage?.operationId === item.operation_id
                ? detailMessage.message
                : null;
            const archiveFailure =
              archiveError?.operationId === item.operation_id
                ? archiveError.message
                : null;
            return (
              <li className="intake-pending-row" key={item.operation_id}>
                <div className="intake-pending-copy">
                  <div className="intake-pending-title-line">
                    <h3>{item.title}</h3>
                    <Tag color={item.can_resume ? "blue" : "default"}>
                      {item.can_resume ? "可继续" : "需要查看原因"}
                    </Tag>
                    {selectedOperationId === item.operation_id ? (
                      <Tag color="green">已选择</Tag>
                    ) : null}
                  </div>
                  <p>
                    原案件：
                    <Link to={caseHref(item.case_id, null)}>{item.case_id}</Link>
                  </p>
                  <p>
                    原文件：{item.file_name ?? "服务器未提供文件名"}
                    {item.file_size === null
                      ? " · 大小未提供"
                      : ` · ${formatBytes(item.file_size)}`}
                  </p>
                  <p>
                    更新于 <time dateTime={item.updated_at}>{formatDateTime(item.updated_at)}</time>
                    {" · "}
                    {pendingStatus(item.error_code)}
                  </p>
                  {!item.can_resume ? (
                    <p className="intake-pending-reason">
                      {reasonMessage(item.reason_code)}
                    </p>
                  ) : null}
                  {message === null ? null : (
                    <Alert
                      className="intake-pending-inline-alert"
                      type="warning"
                      showIcon
                      title="无法读取续办权限"
                      description={message}
                    />
                  )}
                  {archiveFailure === null ? null : (
                    <Alert
                      className="intake-pending-inline-alert"
                      type="error"
                      showIcon
                      title="归档没有完成"
                      description={archiveFailure}
                    />
                  )}
                  {confirmingArchive ? (
                    <div className="intake-pending-archive-confirm" role="group" aria-label={`确认归档 ${item.title}`}>
                      <p>
                        归档只隐藏这项未完成办理，不删除原案件或回执；已开始的上传仍可能完成。
                      </p>
                      <Button
                        danger
                        htmlType="button"
                        loading={archiving}
                        disabled={archivingOperationId !== null}
                        onClick={() => void confirmArchive(item)}
                      >
                        确认归档
                      </Button>
                      <Button
                        htmlType="button"
                        disabled={archiving}
                        onClick={() => setArchiveOperationId(null)}
                      >
                        取消
                      </Button>
                    </div>
                  ) : null}
                </div>
                <div className="intake-pending-actions">
                  {item.can_resume ? (
                    <Button
                      type="primary"
                      htmlType="button"
                      loading={detailBusy}
                      disabled={
                        detailOperationId !== null || archivingOperationId !== null
                      }
                      onClick={() => void selectContinuation(item)}
                    >
                      继续办理
                    </Button>
                  ) : null}
                  <Button href={caseHref(item.case_id, null)}>
                    打开原案件
                  </Button>
                  {item.can_archive && !confirmingArchive ? (
                    <Button
                      htmlType="button"
                      disabled={detailOperationId !== null || archivingOperationId !== null}
                      onClick={() => {
                        setArchiveError(null);
                        setArchiveOperationId(item.operation_id);
                      }}
                    >
                      归档办理
                    </Button>
                  ) : null}
                </div>
              </li>
            );
          })}
        </ol>
      ) : null}
      {actor !== null && pending.data ? (
        <nav className="intake-pending-pagination" aria-label="未完成办理分页">
          <Button
            htmlType="button"
            disabled={pageNumber <= 1 || pending.isFetching}
            onClick={() => {
              setPageState({ actorScope, cursors: cursors.slice(0, -1) });
              setDetailMessage(null);
            }}
          >
            上一页
          </Button>
          <span>第 {pageNumber} 页</span>
          <Button
            htmlType="button"
            disabled={pending.data.next_cursor === null || pending.isFetching}
            onClick={() => {
              const nextCursor = pending.data?.next_cursor;
              if (nextCursor === null || nextCursor === undefined) return;
              setPageState({ actorScope, cursors: [...cursors, nextCursor] });
              setDetailMessage(null);
            }}
          >
            下一页
          </Button>
        </nav>
      ) : null}
    </section>
  );
}
