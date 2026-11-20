// File metadata (manifest mirror):
// Purpose: Lists authorized cases with actionable search, status and view filters, permission-scoped creation and intact route state.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-20
// Revision: r9
// Copyright: See LICENSE
// End file metadata.

import { Button, Input, Modal, Pagination, Popover, Select, Tabs } from "antd";
import { useQueries } from "@tanstack/react-query";
import { useEffect, useLayoutEffect, useRef, useState, type FormEvent } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";
import { errorMessage, isForbidden } from "../api/http";
import { useCases, useCreateCase, useSession, useWorkbench } from "../api/queries";
import { evidenceRequestsKey, listEvidenceRequests } from "../api/evidence-workflow";
import { withEvidenceReadSlot } from "../features/review/evidence-read-slot";
import "./cases-page.css";
import { CasesTable } from "../components/cases-table";
import { isReadingInteraction } from "../components/section-navigation";
import { ListPage } from "../components/page-patterns";
import { actorReadContextKey } from "../domain/actor-read-context";
import { PageStatus } from "../components/page-status";
import {
  businessStatusLabel,
  contractTypeLabel,
  filterCases,
  parseRunFilter,
  validateCaseTitle,
  type CaseSortField,
  type RunFilter,
  type SortDirection,
} from "../domain/review";

const FILTERS: { id: RunFilter; label: string }[] = [
  { id: "all", label: "全部运行状态" },
  { id: "needs_human", label: "待人工" },
  { id: "active", label: "进行中" },
  { id: "partial", label: "部分完成" },
  { id: "completed", label: "执行完成" },
  { id: "failed", label: "失败" },
  { id: "cancelled", label: "已取消" },
  { id: "interrupted", label: "已中断" },
];

const PAGE_SIZES = [10, 25, 50] as const;
type CaseView = "all" | "mine" | "recent" | "archived";

function parseCaseView(value: string | null): CaseView {
  return value === "mine" || value === "recent" || value === "archived" ? value : "all";
}

const collator = new Intl.Collator("zh-CN", {
  numeric: true,
  sensitivity: "base",
});
const scrollPositions = new Map<string, { actorScope: string; scrollY: number }>();

export function CasesPage() {
  const cases = useCases();
  const session = useSession();
  const createCase = useCreateCase();
  const navigate = useNavigate();
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const query = params.get("q") ?? "";
  const run = parseRunFilter(params.get("run"));
  const businessStatus = params.get("business_status") ?? "all";
  const typeFilter = params.get("contract_type") ?? "";
  const view = parseCaseView(params.get("view"));
  const requestedSort = params.get("sort");
  const sort: CaseSortField =
    requestedSort === "title" ||
    requestedSort === "pending_decisions" ||
    requestedSort === "document_count" ||
    requestedSort === "updated_at"
      ? requestedSort
      : "updated_at";
  const direction: SortDirection = params.get("direction") === "asc" ? "asc" : "desc";
  const requestedPage = Number(params.get("page"));
  const validatedPage =
    Number.isSafeInteger(requestedPage) && requestedPage > 0 ? requestedPage : 1;
  const requestedSize = Number(params.get("page_size"));
  const pageSize = PAGE_SIZES.includes(requestedSize as (typeof PAGE_SIZES)[number])
    ? requestedSize
    : 10;
  const selectedId = params.get("selected");
  const actorScope = session.data ? actorReadContextKey(session.data) : "";
  const confirmedActorScope = session.isSuccess && session.fetchStatus === "idle" ? actorScope : "";
  const confirmedCases = cases.isSuccess && cases.fetchStatus === "idle";
  const routeState =
    location.state && typeof location.state === "object"
      ? (location.state as { restoreScrollY?: unknown; actorScope?: unknown })
      : null;
  const returnScope =
    typeof routeState?.actorScope === "string" && routeState.actorScope
      ? routeState.actorScope
      : "";
  const verifiedReturn = useRef("");
  const consumedVisit = useRef("");
  const readingContent = useRef<HTMLElement>(null);
  const returnMismatch = Boolean(
    returnScope && confirmedActorScope && returnScope !== confirmedActorScope,
  );
  const returnPending = Boolean(
    returnScope &&
    verifiedReturn.current !== location.key &&
    !cases.isError &&
    (!confirmedActorScope || !confirmedCases),
  );
  const previousActorScope = useRef(actorScope);
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState("");
  const [contractType, setContractType] = useState("service");
  const [scenario, setScenario] = useState("telecom_dedicated_line");
  const [businessLine, setBusinessLine] = useState("ENT");
  const [customScope, setCustomScope] = useState(false);
  const [titleError, setTitleError] = useState<string | null>(null);

  const visibleCases = cases.data ?? [];
  const workbench = useWorkbench("week", confirmedActorScope);
  const snapshot = workbench.isSuccess && !workbench.isFetching ? workbench.data : undefined;
  const actorId = confirmedActorScope ? session.data?.id : undefined;
  const personalCaseIds = new Set(
    snapshot?.tasks
      .filter(
        (task) =>
          actorId && task.assignee_id === actorId && ["open", "in_progress"].includes(task.status),
      )
      .map((task) => task.case_id),
  );
  for (const profile of snapshot?.profiles ?? []) {
    if (actorId && profile.owner_id === actorId) personalCaseIds.add(profile.case_id);
  }
  const unconfirmedAssignments =
    view === "mine" && actorId && confirmedCases && !returnMismatch
      ? visibleCases.filter((item) => !item.archived_at && !personalCaseIds.has(item.id))
      : [];
  const supplements = useQueries({
    queries: unconfirmedAssignments.map((item) => ({
      queryKey: [...evidenceRequestsKey(item.id), "cases", confirmedActorScope] as const,
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        withEvidenceReadSlot(signal, async () => {
          const entries = await listEvidenceRequests(item.id, signal);
          if (entries.some((entry) => entry.case_id !== item.id))
            throw new Error("补件记录与当前案件不一致");
          return entries;
        }),
      retry: false,
      staleTime: 30_000,
    })),
  });
  for (const query of supplements) {
    if (!query.isSuccess || query.isFetching) continue;
    for (const item of query.data) {
      if (item.status === "open" && item.assignee_id === actorId) personalCaseIds.add(item.case_id);
    }
  }
  const assignmentsPending =
    view === "mine" &&
    (!!workbench.isFetching || supplements.some((query) => query.isPending || query.isFetching));
  const assignmentsIncomplete =
    view === "mine" && (!!workbench.isError || supplements.some((query) => query.isError));

  useLayoutEffect(() => {
    if (returnMismatch) {
      void navigate("/cases", { replace: true, state: null });
      return;
    }
    if (returnScope && !returnPending && confirmedActorScope && !cases.isError)
      verifiedReturn.current = location.key;
  }, [
    returnMismatch,
    returnPending,
    returnScope,
    confirmedActorScope,
    cases.isError,
    location.key,
    navigate,
  ]);

  useLayoutEffect(() => {
    const visit = JSON.stringify([location.key, actorScope]);
    const cancelRestore = (event: Event) => {
      if (isReadingInteraction(event, readingContent.current)) consumedVisit.current = visit;
    };
    document.addEventListener("pointerdown", cancelRestore, true);
    document.addEventListener("keydown", cancelRestore, true);
    document.addEventListener("wheel", cancelRestore, true);
    document.addEventListener("touchstart", cancelRestore, true);
    return () => {
      document.removeEventListener("pointerdown", cancelRestore, true);
      document.removeEventListener("keydown", cancelRestore, true);
      document.removeEventListener("wheel", cancelRestore, true);
      document.removeEventListener("touchstart", cancelRestore, true);
      if (actorScope && consumedVisit.current === visit) {
        scrollPositions.set(location.key, { actorScope, scrollY: window.scrollY });
      }
    };
  }, [location.key, actorScope]);

  useLayoutEffect(() => {
    if (previousActorScope.current && previousActorScope.current !== actorScope)
      scrollPositions.clear();
    previousActorScope.current = actorScope;
    if (
      !confirmedActorScope ||
      !confirmedCases ||
      returnPending ||
      returnMismatch ||
      assignmentsPending
    )
      return;
    const saved = scrollPositions.get(location.key);
    const visit = JSON.stringify([location.key, actorScope]);
    const declared =
      routeState?.actorScope === actorScope &&
      typeof routeState.restoreScrollY === "number" &&
      Number.isFinite(routeState.restoreScrollY) &&
      routeState.restoreScrollY >= 0
        ? routeState.restoreScrollY
        : undefined;
    const requested = saved?.actorScope === actorScope ? saved.scrollY : declared;
    const needsRestore = consumedVisit.current !== visit && requested !== undefined;
    const frame = needsRestore
      ? requestAnimationFrame(() => {
          if (consumedVisit.current === visit) return;
          consumedVisit.current = visit;
          window.scrollTo(0, requested);
        })
      : 0;
    if (!needsRestore) consumedVisit.current = visit;
    return () => {
      cancelAnimationFrame(frame);
    };
  }, [
    location.key,
    location.state,
    actorScope,
    confirmedActorScope,
    confirmedCases,
    returnPending,
    returnMismatch,
    assignmentsPending,
  ]);

  function updateParam(
    key: string,
    value: string,
    { resetPage = true, clearSelection = false } = {},
  ) {
    const next = new URLSearchParams(params);
    if (value === "") next.delete(key);
    else next.set(key, value);
    if (resetPage) next.delete("page");
    if (key === "view" && value === "recent") {
      next.delete("sort");
      next.delete("direction");
    }
    if (clearSelection) next.delete("selected");
    setParams(next, { replace: true });
  }

  function sortBy(field: CaseSortField) {
    const nextDirection =
      sort === field ? (direction === "asc" ? "desc" : "asc") : field === "title" ? "asc" : "desc";
    const next = new URLSearchParams(params);
    next.set("sort", field);
    next.set("direction", nextDirection);
    next.delete("page");
    setParams(next, { replace: true });
  }

  function onCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const problem = validateCaseTitle(title);
    setTitleError(problem);
    if (problem) return;
    createCase.mutate(
      {
        title: title.trim(),
        contract_type: contractType,
        business_line: businessLine,
        scenario,
      },
      {
        onSuccess: (id) => {
          setOpen(false);
          setTitle("");
          void navigate(`/cases/${id}`);
        },
      },
    );
  }

  const businessStatuses = [
    ...new Set(
      (cases.data ?? [])
        .map((item) => item.business_status)
        .filter((value): value is string => Boolean(value)),
    ),
  ].sort(collator.compare);
  const caseViewItems = visibleCases.filter((item) => {
    if (view === "archived") return Boolean(item.archived_at);
    if (view === "mine") return !item.archived_at && personalCaseIds.has(item.id);
    if (view === "recent") return Boolean(item.latest_run);
    return true;
  });
  const contractTypes = [
    ...new Set(visibleCases.flatMap((item) => (item.contract_type ? [item.contract_type] : []))),
  ].sort(collator.compare);
  const filtered = filterCases(caseViewItems, query, run, businessStatus).filter(
    (item) => !typeFilter || item.contract_type === typeFilter,
  );
  const sorted = [...filtered].sort((left, right) => {
    if (view === "recent" && !requestedSort)
      return (
        (right.latest_run?.updated_at ?? "").localeCompare(left.latest_run?.updated_at ?? "") ||
        collator.compare(left.id, right.id)
      );
    let comparison = 0;
    if (sort === "title") comparison = collator.compare(left.title, right.title);
    if (sort === "document_count") comparison = left.document_count - right.document_count;
    if (sort === "pending_decisions") comparison = left.pending_decisions - right.pending_decisions;
    if (sort === "updated_at") comparison = left.updated_at.localeCompare(right.updated_at);
    return (direction === "asc" ? comparison : -comparison) || collator.compare(left.id, right.id);
  });
  const pageCount = Math.max(1, Math.ceil(sorted.length / pageSize));
  const page = Math.min(validatedPage, pageCount);
  const pageItems = sorted.slice((page - 1) * pageSize, page * pageSize);
  useEffect(() => {
    if (
      !confirmedActorScope ||
      !confirmedCases ||
      returnPending ||
      returnMismatch ||
      assignmentsPending ||
      assignmentsIncomplete ||
      !selectedId ||
      sorted.some((item) => item.id === selectedId)
    )
      return;
    const next = new URLSearchParams(params);
    next.delete("selected");
    setParams(next, { replace: true });
  }, [
    confirmedActorScope,
    confirmedCases,
    returnPending,
    returnMismatch,
    selectedId,
    assignmentsPending,
    assignmentsIncomplete,
    sorted,
    params,
    setParams,
  ]);
  const canCreate = session.data?.role === "admin" || session.data?.role === "clerk";

  if (returnPending || returnMismatch)
    return <PageStatus kind="loading" title="正在确认案件列表" />;
  if (cases.isLoading) return <PageStatus kind="loading" title="正在加载案件" />;
  if (cases.isError) {
    if (isForbidden(cases.error)) {
      return (
        <PageStatus kind="denied" title="没有权限查看案件列表" detail={errorMessage(cases.error)} />
      );
    }
    return (
      <PageStatus
        kind="error"
        title="案件列表没有加载"
        detail={errorMessage(cases.error)}
        action={
          <Button
            aria-label={"重试"}
            type="primary"
            htmlType="button"
            onClick={() => void cases.refetch()}
          >
            重试
          </Button>
        }
      />
    );
  }

  return (
    <ListPage
      contentRef={readingContent}
      className="cases-page"
      title="案件"
      standaloneSurface={false}
      actions={
        <Button
          aria-label="新建空白案件"
          htmlType="button"
          disabled={!canCreate}
          title={canCreate ? undefined : "当前角色仅可查看案件；新建案件需要管理员或经办人角色。"}
          onClick={() => setOpen(true)}
        >
          新建空白案件
        </Button>
      }
      filters={
        <div className="case-list-toolbar" aria-label="案件筛选">
          <Tabs
            activeKey={view}
            classNames={{ header: "case-list-tabs", item: "case-list-tab" }}
            onChange={(key) => {
              updateParam("view", key === "all" ? "" : key, { clearSelection: true });
            }}
            items={[
              { key: "all", label: "全部" },
              { key: "mine", label: "待我处理" },
              { key: "recent", label: "最近审查" },
              { key: "archived", label: "已归档" },
            ]}
          />
          <div className="case-list-search">
            <Input
              aria-label="搜索案件"
              value={query}
              onChange={(event) => updateParam("q", event.target.value, { clearSelection: true })}
              placeholder="搜索名称或编号"
              type="search"
              allowClear
            />
            <Popover
              trigger="click"
              placement="bottomRight"
              content={
                <div className="case-list-filter-fields">
                  <label className="field">
                    业务状态
                    <Select
                      aria-label="业务状态"
                      value={businessStatus}
                      onChange={(value) =>
                        updateParam("business_status", value === "all" ? "" : value, {
                          clearSelection: true,
                        })
                      }
                      options={[
                        { value: "all", label: "全部业务状态" },
                        ...businessStatuses.map((status) => ({
                          value: status,
                          label: businessStatusLabel(status),
                        })),
                      ]}
                    />
                  </label>
                  <label className="field">
                    运行状态
                    <Select
                      aria-label="运行状态"
                      value={run}
                      onChange={(value) =>
                        updateParam("run", value === "all" ? "" : value, { clearSelection: true })
                      }
                      options={FILTERS.map(({ id, label }) => ({ value: id, label }))}
                    />
                  </label>
                  <label className="field">
                    合同类型
                    <Select
                      aria-label="合同类型"
                      value={typeFilter}
                      onChange={(value) =>
                        updateParam("contract_type", value, { clearSelection: true })
                      }
                      options={[
                        { value: "", label: "全部合同类型" },
                        ...contractTypes.map((value) => ({
                          value,
                          label: contractTypeLabel(value),
                        })),
                      ]}
                    />
                  </label>
                </div>
              }
            >
              <Button aria-label="状态与类型">状态与类型</Button>
            </Popover>
          </div>
        </div>
      }
      pagination={
        pageCount > 1 ? (
          <Pagination
            current={page}
            total={sorted.length}
            pageSize={pageSize}
            pageSizeOptions={[...PAGE_SIZES]}
            showSizeChanger
            showTotal={(total, range) => `显示 ${range[0]}–${range[1]} 条，共 ${total} 条`}
            onChange={(nextPage, nextSize) => {
              const next = new URLSearchParams(params);
              next.set("page_size", String(nextSize));
              if (nextSize !== pageSize) next.delete("page");
              else next.set("page", String(nextPage));
              setParams(next, { replace: true });
            }}
          />
        ) : null
      }
    >
      {assignmentsPending || assignmentsIncomplete ? (
        <p role="status">
          {assignmentsPending ? "正在核对分派与补件请求" : "部分分派或补件请求未能核对"}
        </p>
      ) : null}
      {cases.data?.length === 0 ? (
        <PageStatus
          kind="empty"
          title="还没有案件"
          detail="新建案件，登记材料并完成解析后开始审查。"
        />
      ) : filtered.length === 0 &&
        (assignmentsPending || assignmentsIncomplete) ? null : filtered.length === 0 ? (
        <section className="page-status page-status-empty" role="status">
          <h2>
            {view === "mine" && !query && !typeFilter && run === "all" && businessStatus === "all"
              ? "暂无待我处理的案件"
              : "没有匹配的案件"}
          </h2>
          <Button
            aria-label={"清除筛选"}
            type="text"
            htmlType="button"
            onClick={() => setParams(new URLSearchParams(), { replace: true })}
          >
            清除筛选
          </Button>
        </section>
      ) : (
        <CasesTable
          items={pageItems}
          sort={sort}
          direction={direction}
          selectedId={selectedId}
          actorScope={actorScope}
          onSort={sortBy}
          snapshot={snapshot}
          role={confirmedActorScope ? session.data?.role : undefined}
        />
      )}

      {open ? (
        <Modal
          open
          title="新建空白案件"
          footer={null}
          onCancel={() => setOpen(false)}
          destroyOnHidden
        >
          <form onSubmit={onCreate}>
            <label className="field">
              案件名称
              <Input value={title} onChange={(event) => setTitle(event.target.value)} required />
            </label>
            {customScope ? (
              <>
                <label className="field">
                  业务线标识
                  <Input
                    value={businessLine}
                    onChange={(event) => setBusinessLine(event.target.value)}
                    required
                    maxLength={128}
                    pattern="[A-Za-z0-9](?:[A-Za-z0-9._:]|/|-)*"
                  />
                </label>
                <label className="field">
                  业务场景标识
                  <Input
                    value={scenario}
                    onChange={(event) => setScenario(event.target.value)}
                    required
                    maxLength={128}
                    pattern="[A-Za-z0-9](?:[A-Za-z0-9._:]|/|-)*"
                  />
                </label>
                <label className="field">
                  合同类型标识
                  <Input
                    value={contractType}
                    onChange={(event) => setContractType(event.target.value)}
                    required
                    maxLength={128}
                    pattern="[A-Za-z0-9](?:[A-Za-z0-9._:]|/|-)*"
                  />
                </label>
                <p>
                  填写已发布且当前生效的规则包中声明的业务范围。标识以字母或数字开头，可包含字母、数字及
                  . _ : / -。
                </p>
              </>
            ) : (
              <>
                <label className="field">
                  业务场景
                  <Select<string>
                    aria-label="业务场景"
                    value={String(scenario)}
                    onChange={(selectedValue) => {
                      setScenario(selectedValue);
                      if (selectedValue === "cloud_services") setContractType("service");
                    }}
                    options={[
                      { value: "telecom_dedicated_line", label: "政企专线" },
                      { value: "cloud_services", label: "云服务采购" },
                    ]}
                  />
                </label>
                <label className="field">
                  合同类型
                  <Select<string>
                    aria-label="合同类型"
                    value={String(contractType)}
                    disabled={scenario === "cloud_services"}
                    onChange={(selectedValue) => setContractType(selectedValue)}
                    options={[
                      { value: "procurement_ict", label: "采购 / ICT" },
                      { value: "network_engineering", label: "网络工程" },
                      { value: "service", label: "服务合同" },
                      { value: "unspecified", label: "未分类" },
                    ]}
                  />
                </label>
              </>
            )}
            <Button
              type="link"
              className="case-scope-toggle"
              aria-expanded={customScope}
              htmlType="button"
              onClick={() => {
                setCustomScope(!customScope);
                if (customScope) {
                  setBusinessLine("ENT");
                  setScenario("telecom_dedicated_line");
                  setContractType("service");
                }
              }}
            >
              {customScope ? "选择常用业务场景" : "自定义规则范围编码"}
            </Button>
            {titleError ? <p role="alert">{titleError}</p> : null}
            {createCase.isError ? <p role="alert">{errorMessage(createCase.error)}</p> : null}
            <div className="dialog-actions">
              <Button
                aria-label={"取消"}
                type="text"
                htmlType="button"
                onClick={() => setOpen(false)}
              >
                取消
              </Button>
              <Button
                aria-label={String(createCase.isPending ? "正在创建" : "创建")}
                type="primary"
                htmlType="submit"
                disabled={createCase.isPending}
              >
                {createCase.isPending ? "正在创建" : "创建"}
              </Button>
            </div>
          </form>
        </Modal>
      ) : null}
    </ListPage>
  );
}
