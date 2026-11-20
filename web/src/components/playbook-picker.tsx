// File metadata (manifest mirror):
// Purpose: Lets reviewers explicitly select an authorized exact playbook version, isolates catalog caches by actor context, and rejects stale selections.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-20
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { Alert, Button, Select } from "antd";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useId } from "react";
import { errorMessage } from "../api/http";
import { listCasePlaybooks, type PlaybookRef } from "../api/playbooks";
import { useSession } from "../api/queries";
import { actorReadContextKey } from "../domain/actor-read-context";
import { TechnicalDetail } from "./technical-detail";

function selectionKey(ref: PlaybookRef): string {
  return JSON.stringify([ref.knowledge_id, ref.version_id, ref.content_sha256]);
}

export function PlaybookPicker({
  caseId,
  value,
  onChange,
  onAvailabilityChange,
  disabled = false,
  compact = false,
}: {
  caseId: string;
  value: PlaybookRef | undefined;
  onChange: (value: PlaybookRef | undefined) => void;
  onAvailabilityChange?: (available: boolean) => void;
  disabled?: boolean;
  compact?: boolean;
}) {
  const instanceId = useId();
  const headingId = `${instanceId}-playbook-heading`;
  const selectId = `${instanceId}-playbook-select`;
  const session = useSession();
  const actorScope = actorReadContextKey(session.data ?? null);
  const candidates = useQuery({
    queryKey: ["cases", caseId, "playbooks", actorScope],
    queryFn: ({ signal }) => listCasePlaybooks(caseId, signal),
    enabled: caseId !== "" && session.isSuccess && session.data !== null,
    retry: false,
  });
  const catalogReady = candidates.isSuccess && !candidates.isFetching;
  const items = catalogReady ? candidates.data.items : [];
  const selected = value && items.find((item) => selectionKey(item.ref) === selectionKey(value));
  const selectionAvailable = value === undefined || (catalogReady && selected !== undefined);

  useEffect(() => {
    onAvailabilityChange?.(selectionAvailable);
  }, [onAvailabilityChange, selectionAvailable]);

  return (
    <section
      className={compact ? "review-preflight-playbook" : "workflow-panel"}
      aria-label={compact ? "审查手册" : undefined}
      aria-labelledby={compact ? undefined : headingId}
    >
      {!compact ? (
        <>
          <h3 id={headingId}>审查手册（可选）</h3>
          <p className="meta">手册供审查参考。所选版本随本次审查留档，也可沿用默认流程。</p>
        </>
      ) : null}
      {candidates.isError ? (
        <Alert
          type="error"
          showIcon
          title="可用手册没有加载"
          description={errorMessage(candidates.error)}
          action={
            <Button size="small" onClick={() => void candidates.refetch()}>
              重试
            </Button>
          }
        />
      ) : null}
      {session.isError ? (
        <Alert
          type="error"
          showIcon
          title="当前登录身份没有确认"
          description={errorMessage(session.error)}
          action={
            <Button size="small" onClick={() => void session.refetch()}>
              重试
            </Button>
          }
        />
      ) : null}
      {value && (candidates.isError || session.isError) ? (
        <Alert
          type="warning"
          showIcon
          title="所选手册尚未重新核验"
          description="当前不会提交该手册。可重试读取，或清除本次选择后按原审查流程继续。"
        />
      ) : null}
      {!compact && catalogReady && candidates.data.reason ? (
        <Alert type="info" showIcon title={candidates.data.reason} />
      ) : null}
      {!compact && catalogReady && items.length === 0 && !candidates.data.reason ? (
        <Alert
          type="info"
          showIcon
          title="当前案件没有可采用的已发布手册；可以不选手册继续原审查流程。"
        />
      ) : null}
      {value && !catalogReady && candidates.isFetching ? (
        <p role="status">正在重新确认手册版本；确认完成前不能按旧目录提交所选手册。</p>
      ) : null}
      {value && catalogReady && !selected ? (
        <Alert
          type="warning"
          showIcon
          title="先前选择的手册版本已不在当前可用列表中"
          description="系统没有替换为新版本。请重新选择，或清除选择后按原流程继续。"
        />
      ) : null}
      <label className="field" htmlFor={selectId}>
        {compact ? "审查手册" : "采用的手册"}
        <Select
          id={selectId}
          aria-label="采用的审查手册"
          allowClear
          showSearch={{ optionFilterProp: "label" }}
          placeholder={compact ? "默认规则审查" : "不采用手册"}
          value={value && catalogReady ? selectionKey(value) : undefined}
          loading={session.isLoading || candidates.isLoading}
          disabled={disabled || candidates.isError || !session.isSuccess || !catalogReady}
          options={items.map((candidate) => ({
            value: selectionKey(candidate.ref),
            label: candidate.title,
          }))}
          onChange={(key?: string) => {
            if (!key) {
              onChange(undefined);
              return;
            }
            const item = items.find((candidate) => selectionKey(candidate.ref) === key);
            if (item) onChange(item.ref);
          }}
        />
      </label>
      {value && catalogReady ? (
        <TechnicalDetail label="所选手册精确版本" value={JSON.stringify(value)} />
      ) : null}
      {value ? (
        <Button htmlType="button" disabled={disabled} onClick={() => onChange(undefined)}>
          本次不采用手册
        </Button>
      ) : null}
      {candidates.isLoading ? <p role="status">正在读取当前可用手册…</p> : null}
    </section>
  );
}
