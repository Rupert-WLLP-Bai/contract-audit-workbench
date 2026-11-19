// File metadata (manifest mirror):
// Purpose: Lists authorized proposed contract materials with search and status filters, opens exact frozen originals or case materials, and retains the filtered reading order.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-19
// Updated: 2026-11-19
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { Button, Checkbox, Grid, Input, Popover, Select, Table, type TableColumnsType } from "antd";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { Link, Navigate, useSearchParams } from "react-router";
import { getCase, getReview, listMaterialParsedDocuments } from "../api/endpoints";
import { errorMessage, isForbidden } from "../api/http";
import { queryKeys, useSession, useWorkbench } from "../api/queries";
import type {
  ReviewWorkspace,
  WorkbenchDocument,
  WorkbenchRun,
  WorkbenchSnapshot,
} from "../api/types";
import { ListPage } from "../components/page-patterns";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
import { actorReadContextKey } from "../domain/actor-read-context";
import { frozenSourceProof } from "../domain/frozen-source-proof";
import { caseHref, formatDateTime, parseLabel, parseTone } from "../domain/review";
import "./contract-desk-page.css";

const deskPath = "/contract-desk";
const pageSize = 10;
const statuses = ["pending", "parsing", "ready", "failed", "superseded"];
const statusOptions = [
  { value: "", label: "全部状态" },
  ...statuses.map((value) => ({
    value,
    label: value === "superseded" ? "历史版本" : parseLabel(value),
  })),
];

function deskHref(params: URLSearchParams) {
  const query = params.toString();
  return query ? `${deskPath}?${query}` : deskPath;
}

export function ContractDeskSources({
  active,
  materialsHref = deskPath,
  registrationState,
}: {
  active: "materials" | "registered";
  materialsHref?: string;
  registrationState?: { actorScope: string; materialsSearch: string };
}) {
  return (
    <nav className="contract-desk-sources" aria-label="合同来源">
      {active === "materials" ? (
        <span aria-current="page">审查材料</span>
      ) : (
        <Link to={materialsHref}>审查材料</Link>
      )}
      {active === "registered" ? (
        <span aria-current="page">已登记合同</span>
      ) : (
        <Link to="/contracts" state={registrationState}>
          已登记合同
        </Link>
      )}
    </nav>
  );
}

export function contractDeskReturnHref(state: unknown, actorScope: string) {
  if (
    !actorScope ||
    !state ||
    typeof state !== "object" ||
    !("actorScope" in state) ||
    state.actorScope !== actorScope ||
    !("materialsSearch" in state) ||
    typeof state.materialsSearch !== "string"
  ) {
    return deskPath;
  }
  const saved = new URLSearchParams(state.materialsSearch);
  const params = new URLSearchParams();
  for (const key of ["q", "status", "mine", "page"]) {
    const value = saved.get(key);
    if (value) params.set(key, value);
  }
  return deskHref(params);
}

function contractDocuments(snapshot: WorkbenchSnapshot, actorId: string) {
  const cases = new Map(snapshot.cases.map((item) => [item.id, item]));
  const people = new Map(snapshot.members.map((item) => [item.id, item.display_name]));
  const tasks = new Map<string, WorkbenchSnapshot["tasks"]>();
  const runs = new Map<string, WorkbenchRun>();
  for (const task of snapshot.tasks) {
    if (task.status !== "open" && task.status !== "in_progress") continue;
    const current = tasks.get(task.case_id) ?? [];
    current.push(task);
    tasks.set(task.case_id, current);
  }
  for (const run of snapshot.runs) {
    if (!run.attempt_id) continue;
    const current = runs.get(run.case_id);
    if (
      !current ||
      Date.parse(run.updated_at) > Date.parse(current.updated_at) ||
      (run.updated_at === current.updated_at && run.id > current.id)
    ) {
      runs.set(run.case_id, run);
    }
  }
  return snapshot.documents
    .filter((item) => item.role === "draft_contract" && cases.has(item.case_id))
    .sort(
      (left, right) =>
        Date.parse(right.created_at) - Date.parse(left.created_at) ||
        left.id.localeCompare(right.id),
    )
    .map((document) => {
      const caseRecord = cases.get(document.case_id);
      const caseTasks = tasks.get(document.case_id) ?? [];
      const myTask = caseTasks.find((task) => task.assignee_id === actorId);
      const task = myTask ?? caseTasks[0];
      return {
        document,
        caseTitle: caseRecord?.title || document.case_title,
        caseNumber: caseRecord?.profile?.number,
        hasMyTask: Boolean(myTask),
        status: document.superseded ? "superseded" : document.parse_status,
        task: task ? { title: task.title, assignee: people.get(task.assignee_id) } : null,
        otherTasks: Math.max(0, caseTasks.length - 1),
        run: runs.get(document.case_id),
      };
    });
}

type ContractDeskRow = ReturnType<typeof contractDocuments>[number];

function frozenDocumentCandidate(
  workspace: ReviewWorkspace,
  document: WorkbenchDocument,
  run: WorkbenchRun,
) {
  const attempt = workspace.review_attempts?.attempt;
  if (
    workspace.run.id !== run.id ||
    workspace.run.case_id !== document.case_id ||
    attempt?.id !== run.attempt_id ||
    attempt?.run_id !== run.id ||
    attempt.history_kind !== "frozen"
  ) {
    return null;
  }
  const matches = workspace.documents.filter((item) => item.id === document.id);
  const [frozen] = matches;
  const parsedId = frozen?.parsed_document_id;
  if (!frozen?.text?.trim() || !parsedId || matches.length !== 1) return null;
  if (frozen.parse_status !== "ready" || !attempt.parsed_document_ids.includes(parsedId))
    return null;
  return { frozen, parsedId, parsedIds: attempt.parsed_document_ids };
}

function OpenContractMaterial({
  row,
  actorScope,
  returnTo,
  previousHref,
  nextHref,
}: {
  row: ContractDeskRow;
  actorScope: string;
  returnTo: string;
  previousHref: string | null;
  nextHref: string | null;
}) {
  const { document, run } = row;
  const attemptId = run?.attempt_id ?? null;
  const review = useQuery({
    queryKey: queryKeys.review(document.case_id, run?.id ?? "", attemptId),
    queryFn: ({ signal }) => getReview(document.case_id, run?.id ?? "", attemptId, signal),
    enabled: Boolean(run && attemptId),
    retry: false,
  });
  const detail = useQuery({
    queryKey: queryKeys.case(document.case_id),
    queryFn: ({ signal }) => getCase(document.case_id, signal),
    enabled: Boolean(run && attemptId),
    retry: false,
  });
  const candidate =
    run && review.isSuccess && !review.isFetching
      ? frozenDocumentCandidate(review.data, document, run)
      : null;
  const verifiedCase =
    detail.isSuccess && !detail.isFetching && detail.data.id === document.case_id
      ? detail.data
      : undefined;
  const originals = verifiedCase?.documents.filter((item) => item.id === document.id) ?? [];
  const original =
    originals.length === 1 && originals[0]?.digest === document.digest ? originals[0] : null;
  const sources =
    candidate && original && candidate.parsedId !== original.parsed_document?.id
      ? (verifiedCase?.material_versions ?? []).filter((item) => item.document_id === document.id)
      : [];
  const source = sources.length === 1 ? sources[0] : undefined;
  const history = useQuery({
    queryKey: [
      "frozen-source-proof",
      actorScope,
      document.case_id,
      run?.id,
      attemptId,
      source?.material_id,
      source?.version,
    ],
    queryFn: ({ signal }) =>
      listMaterialParsedDocuments(
        document.case_id,
        source?.material_id ?? "",
        source?.version ?? 0,
        signal,
      ),
    enabled: Boolean(source),
    retry: false,
  });
  const materialQuery = new URLSearchParams({ source_document: document.id });
  const materialsHref = `${caseHref(document.case_id)}?${materialQuery}#case-material-source`;
  const materialsState = { actorScope, materialsSearch: returnTo.slice(deskPath.length) };
  if (!run || !attemptId) return <Navigate to={materialsHref} replace state={materialsState} />;
  if (
    review.isPending ||
    review.isFetching ||
    detail.isPending ||
    detail.isFetching ||
    (source && (history.isPending || history.isFetching))
  ) {
    return (
      <div className="contract-desk-open" role="status">
        <span>正在核对原文…</span>
        <Link to={returnTo} replace>
          取消
        </Link>
      </div>
    );
  }
  const proof =
    candidate &&
    original &&
    frozenSourceProof(
      verifiedCase,
      candidate.frozen,
      candidate.parsedIds,
      history.isSuccess ? history.data : undefined,
    );
  if (!proof) {
    return <Navigate to={materialsHref} replace state={materialsState} />;
  }
  const query = new URLSearchParams({
    attempt_id: attemptId,
    review_section: "document",
    source_document: document.id,
  });
  return (
    <Navigate
      to={`${caseHref(document.case_id, run.id)}?${query}`}
      replace
      state={{
        contractDesk: {
          actorScope,
          returnTo,
          documentId: document.id,
          previousHref,
          nextHref,
        },
      }}
    />
  );
}

export function ContractDeskPage() {
  const [params, setParams] = useSearchParams();
  const compactFilters = Grid.useBreakpoint().md === false;
  const session = useSession();
  const actor = session.isSuccess && !session.isFetching ? session.data : null;
  const actorScope = actorReadContextKey(actor);
  const workbench = useWorkbench("week", actorScope);
  const search = params.get("q") ?? "";
  const requestedStatus = params.get("status") ?? "";
  const status = statuses.includes(requestedStatus) ? requestedStatus : "";
  const mine = params.get("mine") === "1";
  const requestedPage = Number(params.get("page"));
  const requestedOpen = params.get("open_material");
  const actorId = actor?.id ?? "";
  const rows = useMemo(
    () => (workbench.data ? contractDocuments(workbench.data, actorId) : []),
    [actorId, workbench.data],
  );
  const query = search.trim().toLocaleLowerCase("zh-CN");
  const filtered = rows.filter(
    (row) =>
      Boolean(row.document.superseded) === (status === "superseded") &&
      (!status || row.status === status) &&
      (!mine || row.hasMyTask) &&
      (!query ||
        row.document.filename.toLocaleLowerCase("zh-CN").includes(query) ||
        row.caseTitle.toLocaleLowerCase("zh-CN").includes(query) ||
        row.caseNumber?.toLocaleLowerCase("zh-CN").includes(query)),
  );
  const page = Math.min(
    Number.isSafeInteger(requestedPage) && requestedPage > 0 ? requestedPage : 1,
    Math.max(1, Math.ceil(filtered.length / pageSize)),
  );
  const listParams = new URLSearchParams();
  if (search) listParams.set("q", search);
  if (status) listParams.set("status", status);
  if (mine) listParams.set("mine", "1");
  if (page > 1) listParams.set("page", String(page));

  function openHref(row: ContractDeskRow, index: number) {
    const targetParams = new URLSearchParams(listParams);
    const targetPage = Math.floor(index / pageSize) + 1;
    if (targetPage > 1) targetParams.set("page", String(targetPage));
    else targetParams.delete("page");
    targetParams.set("open_material", row.document.id);
    return deskHref(targetParams);
  }

  function updateFilter(key: "q" | "status" | "mine", value: string) {
    const next = new URLSearchParams(listParams);
    if (value) next.set(key, value);
    else next.delete(key);
    next.delete("page");
    setParams(next, { replace: true });
  }

  const openIndex = requestedOpen
    ? filtered.findIndex((row) => row.document.id === requestedOpen)
    : -1;
  const target = filtered[openIndex];
  const previous = filtered[openIndex - 1];
  const next = filtered[openIndex + 1];
  const ready = actorScope && workbench.isSuccess && !workbench.isFetching;
  const returnTo = deskHref(listParams);

  const offset = (page - 1) * pageSize;
  const pageRows = filtered.slice(offset, offset + pageSize).map((row, index) => ({
    ...row,
    href: openHref(row, offset + index),
  }));
  const columns: TableColumnsType<(typeof pageRows)[number]> = [
    {
      key: "material",
      title: "材料名",
      width: 220,
      rowScope: "row",
      render: (_, row) => (
        <div className="contract-desk-cell">
          <strong>{row.document.filename}</strong>
          <Link to={row.href} aria-label={`查看材料：${row.document.filename}`}>
            查看材料
          </Link>
        </div>
      ),
    },
    {
      key: "case",
      title: "所属案件",
      width: 190,
      render: (_, row) => (
        <div className="contract-desk-cell">
          <Link to={caseHref(row.document.case_id)}>{row.caseTitle}</Link>
          {row.caseNumber ? (
            <span className="contract-desk-secondary">{row.caseNumber}</span>
          ) : null}
        </div>
      ),
    },
    {
      key: "status",
      title: "材料状态",
      width: 104,
      render: (_, row) => (
        <StatusText tone={row.status === "superseded" ? "neutral" : parseTone(row.status)}>
          {row.status === "superseded" ? "已替换" : parseLabel(row.status)}
        </StatusText>
      ),
    },
    {
      key: "task",
      title: "案内待办",
      width: 142,
      render: (_, row) => (
        <div className="contract-desk-cell">
          <span>{row.task?.title || "—"}</span>
          {row.task?.assignee ? (
            <span className="contract-desk-secondary">{row.task.assignee}</span>
          ) : null}
          {row.otherTasks > 0 ? (
            <span className="contract-desk-secondary">另有 {row.otherTasks} 项</span>
          ) : null}
        </div>
      ),
    },
    {
      key: "created",
      title: "上传时间",
      width: 152,
      render: (_, row) => (
        <time dateTime={row.document.created_at}>{formatDateTime(row.document.created_at)}</time>
      ),
    },
  ];
  const extraFilters = (
    <>
      <label className="field">
        材料状态
        <Select
          aria-label="材料状态"
          value={status}
          options={statusOptions}
          onChange={(value) => updateFilter("status", value)}
        />
      </label>
      <Checkbox
        className="contract-desk-mine"
        checked={mine}
        onChange={(event) => updateFilter("mine", event.target.checked ? "1" : "")}
      >
        我有待办的案件
      </Checkbox>
      <Button onClick={() => setParams(new URLSearchParams(), { replace: true })}>重置</Button>
    </>
  );
  const activeFilters = Number(Boolean(status)) + Number(mine);

  return (
    <ListPage
      className="contract-desk"
      title="合同"
      beforeSummary={
        <ContractDeskSources
          active="materials"
          registrationState={{ actorScope, materialsSearch: listParams.toString() }}
        />
      }
      summary={
        actorScope && workbench.isSuccess ? (
          <span role="status">
            共 {filtered.length} 份{status === "superseded" ? "历史拟签合同材料" : "拟签合同材料"}
          </span>
        ) : null
      }
      filters={
        <div className="contract-desk-filters" role="group" aria-label="审查材料筛选">
          <label className="field">
            材料名或案件
            <Input
              type="search"
              value={search}
              allowClear
              maxLength={240}
              placeholder="输入材料名、案件名或案号"
              onChange={(event) => updateFilter("q", event.target.value)}
            />
          </label>
          {compactFilters ? (
            <Popover
              trigger="click"
              placement="bottomRight"
              content={<div className="contract-desk-filter-popover">{extraFilters}</div>}
            >
              <Button>{activeFilters > 0 ? `筛选（${activeFilters}）` : "筛选"}</Button>
            </Popover>
          ) : (
            extraFilters
          )}
        </div>
      }
    >
      {workbench.isError ? (
        <PageStatus
          kind={isForbidden(workbench.error) ? "denied" : "error"}
          title={isForbidden(workbench.error) ? "无法查看审查材料" : "审查材料读取失败"}
          detail={errorMessage(workbench.error)}
          action={<Button onClick={() => void workbench.refetch()}>重试</Button>}
        />
      ) : !actorScope || workbench.isPending ? (
        <PageStatus kind="loading" title="正在读取审查材料" />
      ) : filtered.length === 0 ? (
        <PageStatus
          kind="empty"
          title={search || status || mine ? "没有符合条件的材料" : "暂无拟签合同材料"}
          action={<Link to="/cases">前往案件</Link>}
        />
      ) : (
        <Table
          columns={columns}
          dataSource={pageRows}
          rowKey={(row) => row.document.id}
          scroll={{ x: 808 }}
          tableLayout="fixed"
          pagination={{
            current: page,
            pageSize,
            total: filtered.length,
            hideOnSinglePage: true,
            responsive: true,
            showSizeChanger: false,
            onChange: (value) => {
              const next = new URLSearchParams(listParams);
              if (value > 1) next.set("page", String(value));
              else next.delete("page");
              setParams(next);
            },
          }}
        />
      )}
      {ready && requestedOpen && target ? (
        <OpenContractMaterial
          key={target.document.id}
          row={target}
          actorScope={actorScope}
          returnTo={returnTo}
          previousHref={previous ? openHref(previous, openIndex - 1) : null}
          nextHref={next ? openHref(next, openIndex + 1) : null}
        />
      ) : ready && requestedOpen ? (
        <div className="contract-desk-open" role="status">
          <span>当前清单中没有这份材料。</span>
          <Link to={returnTo} replace>
            返回清单
          </Link>
        </div>
      ) : null}
    </ListPage>
  );
}
