import {
  CheckCircleOutlined,
  ClearOutlined,
  ClockCircleOutlined,
  FileDoneOutlined,
  FileSearchOutlined,
  FolderOpenOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
  SyncOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import {
  Button,
  Card,
  Input,
  Modal,
  Pagination,
  Select,
  Tabs,
} from "antd";
import {
  useId,
  useLayoutEffect,
  useMemo,
  useState,
  type FormEvent,
} from "react";
import {
  Link,
  useLocation,
  useNavigate,
  useSearchParams,
} from "react-router";
import { errorMessage, isForbidden } from "../api/http";
import {
  useCases,
  useCreateCase,
  useSession,
  useWorkbench,
} from "../api/queries";
import type { WorkbenchCase } from "../api/types";
import { ChartLegend, DonutChart, type DonutItem } from "../components/charts/donut-chart";
import { CasesTable, type CaseTableRow } from "../components/cases-table";
import {
  EmptyState,
  ErrorState,
  KpiStat,
  LoadingBlock,
  PageHeader,
  QuickActions,
  RetryAction,
  SectionCard,
} from "../components/ui";
import {
  CASE_TABS,
  caseLink,
  caseTab,
  isOverdue,
  myOpenTasks,
  parseCaseTab,
  RISK_ROWS,
  tabCounts,
  taskKindLabel,
  taskPriorityLabel,
} from "../domain/dashboard";
import {
  contractTypeLabel,
  filterCases,
  formatDateTime,
  parseRunFilter,
  validateCaseTitle,
  type CaseSortField,
  type RunFilter,
  type SortDirection,
} from "../domain/review";
import "./cases-page.css";

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
const collator = new Intl.Collator("zh-CN", {
  numeric: true,
  sensitivity: "base",
});
const scrollPositions = new Map<string, number>();

type EnrichedCase = CaseTableRow & { ownerId: string | null };

export function CasesPage() {
  const cases = useCases();
  const workbench = useWorkbench("week");
  const session = useSession();
  const createCase = useCreateCase();
  const navigate = useNavigate();
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const query = params.get("q") ?? "";
  const run = parseRunFilter(params.get("run"));
  const businessStatus = params.get("business_status") ?? "all";
  const tab = parseCaseTab(params.get("tab"));
  const requestedRisk = params.get("risk") ?? "all";
  const risk = RISK_ROWS.some((row) => row.key === requestedRisk)
    ? requestedRisk
    : "all";
  const contractTypeFilter = params.get("type") ?? "all";
  const requestedOwner = params.get("owner") ?? "all";
  const requestedSort = params.get("sort");
  const sort: CaseSortField =
    requestedSort === "title" ||
    requestedSort === "pending_decisions" ||
    requestedSort === "document_count" ||
    requestedSort === "updated_at"
      ? requestedSort
      : "updated_at";
  const direction: SortDirection =
    params.get("direction") === "asc" ? "asc" : "desc";
  const requestedPage = Number(params.get("page"));
  const validatedPage =
    Number.isSafeInteger(requestedPage) && requestedPage > 0
      ? requestedPage
      : 1;
  const requestedSize = Number(params.get("page_size"));
  const pageSize = PAGE_SIZES.includes(
    requestedSize as (typeof PAGE_SIZES)[number],
  )
    ? requestedSize
    : 10;
  const selectedId = params.get("selected");
  const modalOpen = params.get("new") === "1";
  const titleId = useId();
  const [title, setTitle] = useState("");
  const [contractType, setContractType] = useState("service");
  const [scenario, setScenario] = useState("telecom_dedicated_line");
  const [titleError, setTitleError] = useState<string | null>(null);

  useLayoutEffect(() => {
    const routeState = location.state as { restoreScrollY?: unknown } | null;
    const requested =
      typeof routeState?.restoreScrollY === "number"
        ? routeState.restoreScrollY
        : scrollPositions.get(location.key);
    if (requested !== undefined) window.scrollTo(0, requested);
    return () => {
      scrollPositions.set(location.key, window.scrollY);
    };
  }, [location.key, location.state]);

  const workbenchCases = useMemo(
    () =>
      new Map<string, WorkbenchCase>(
        (workbench.data?.cases ?? []).map((item) => [item.id, item]),
      ),
    [workbench.data?.cases],
  );
  const members = useMemo(
    () =>
      new Map(
        (workbench.data?.members ?? []).map((member) => [
          member.id,
          member.display_name,
        ]),
      ),
    [workbench.data?.members],
  );
  const ownerOptions = useMemo(() => {
    const owners = new Set<string>();
    let hasUnassigned = false;
    for (const item of workbench.data?.cases ?? []) {
      if (item.profile?.owner_id) owners.add(item.profile.owner_id);
      else hasUnassigned = true;
    }
    return {
      hasUnassigned,
      options: [...owners]
        .sort(collator.compare)
        .map((ownerId) => ({
          value: ownerId,
          label: members.get(ownerId) ?? ownerId,
        })),
    };
  }, [workbench.data?.cases, members]);
  const owner =
    requestedOwner === "all" ||
    ownerOptions.options.some((option) => option.value === requestedOwner) ||
    (requestedOwner === "unassigned" && ownerOptions.hasUnassigned)
      ? requestedOwner
      : "all";

  function updateParam(
    key: string,
    value: string,
    { resetPage = true, clearSelection = false } = {},
  ) {
    const next = new URLSearchParams(params);
    if (value === "") next.delete(key);
    else next.set(key, value);
    if (resetPage) next.delete("page");
    if (clearSelection) next.delete("selected");
    setParams(next, { replace: true });
  }

  function sortBy(field: CaseSortField) {
    const nextDirection =
      sort === field
        ? direction === "asc"
          ? "desc"
          : "asc"
        : field === "title"
          ? "asc"
          : "desc";
    const next = new URLSearchParams(params);
    next.set("sort", field);
    next.set("direction", nextDirection);
    next.delete("page");
    setParams(next, { replace: true });
  }

  function resetFilters() {
    const next = new URLSearchParams();
    if (pageSize !== 10) next.set("page_size", String(pageSize));
    setParams(next, { replace: true });
  }

  function setModalOpen(open: boolean) {
    const next = new URLSearchParams(params);
    if (open) next.set("new", "1");
    else next.delete("new");
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
        business_line: "ENT",
        scenario,
      },
      {
        onSuccess: (id) => {
          setModalOpen(false);
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
  const contractTypes = [
    ...new Set((cases.data ?? []).map((item) => item.contract_type)),
  ].sort(collator.compare);
  const enriched: EnrichedCase[] = (cases.data ?? []).map((item) => {
    const snapshotCase = workbenchCases.get(item.id);
    return {
      ...item,
      riskKnown: snapshotCase !== undefined,
      severity: snapshotCase?.severity ?? null,
      ownerName: snapshotCase?.profile
        ? (members.get(snapshotCase.profile.owner_id) ??
          snapshotCase.profile.owner_id)
        : undefined,
      ownerId: snapshotCase?.profile?.owner_id ?? null,
    };
  });
  const counts = tabCounts(enriched);
  const filtered = filterCases(enriched, query, run, businessStatus).filter(
    (item) => {
      if (tab !== "all" && caseTab(item) !== tab) return false;
      if (contractTypeFilter !== "all" && item.contract_type !== contractTypeFilter)
        return false;
      if (risk !== "all") {
        if (!item.riskKnown) return false;
        if ((item.severity ?? "unrated") !== risk) return false;
      }
      if (owner !== "all") {
        if (!item.riskKnown) return false;
        if ((item.ownerId ?? "unassigned") !== owner) return false;
      }
      return true;
    },
  );
  const sorted = [...filtered].sort((left, right) => {
    let comparison = 0;
    if (sort === "title")
      comparison = collator.compare(left.title, right.title);
    if (sort === "document_count")
      comparison = left.document_count - right.document_count;
    if (sort === "pending_decisions")
      comparison = left.pending_decisions - right.pending_decisions;
    if (sort === "updated_at")
      comparison = left.updated_at.localeCompare(right.updated_at);
    return (
      (direction === "asc" ? comparison : -comparison) ||
      collator.compare(left.id, right.id)
    );
  });
  const pageCount = Math.max(1, Math.ceil(sorted.length / pageSize));
  const page = Math.min(validatedPage, pageCount);
  const pageItems = sorted.slice((page - 1) * pageSize, page * pageSize);
  const canCreate =
    session.data?.role === "admin" || session.data?.role === "clerk";
  const hasFilters =
    query !== "" ||
    run !== "all" ||
    businessStatus !== "all" ||
    tab !== "all" ||
    risk !== "all" ||
    contractTypeFilter !== "all" ||
    owner !== "all";

  const caseRiskItems: DonutItem[] = RISK_ROWS.map((row) => ({
    ...row,
    value: (workbench.data?.cases ?? []).filter(
      (item) => (item.severity ?? "unrated") === row.key,
    ).length,
  }));
  const knownRisks = new Set<string>(RISK_ROWS.map((row) => row.key));
  const unknownRisks = [
    ...new Set(
      (workbench.data?.cases ?? [])
        .map((item) => item.severity ?? "unrated")
        .filter((severity) => !knownRisks.has(severity)),
    ),
  ];
  for (const severity of unknownRisks) {
    caseRiskItems.push({
      key: severity,
      label: `未识别（${severity}）`,
      value: (workbench.data?.cases ?? []).filter(
        (item) => (item.severity ?? "unrated") === severity,
      ).length,
      color: "#64748b",
    });
  }
  const caseRiskTotal = workbench.data?.cases.length ?? 0;
  const myTasks = myOpenTasks(workbench.data?.tasks ?? [], session.data?.id);
  const now = workbench.data?.generated_at
    ? new Date(workbench.data.generated_at)
    : new Date();

  if (cases.isLoading) {
    return (
      <section className="page cases-page">
        <LoadingBlock label="正在加载案件" />
      </section>
    );
  }

  if (cases.isError) {
    const forbidden = isForbidden(cases.error);
    return (
      <section className="page cases-page">
        <PageHeader
          title="案件中心"
          description="统一管理合同审查任务，跟踪风险、负责人和审查进度。"
        />
        <ErrorState
          title={
            forbidden ? "没有权限查看案件列表" : "案件列表没有加载"
          }
          detail={
            forbidden
              ? "当前身份没有可见的案件范围。请联系管理员确认组织、角色与案件授权。"
              : errorMessage(cases.error)
          }
          action={
            forbidden ? undefined : (
              <RetryAction onRetry={() => void cases.refetch()} />
            )
          }
        />
      </section>
    );
  }

  return (
    <section className="page cases-page">
      <PageHeader
        eyebrow="案件 / 合同审查"
        title="案件中心"
        description="统一管理合同审查任务，实时掌握审查进度、风险等级与负责人；状态以服务端记录为准。"
        extra={
          <>
            <Button
              icon={<ReloadOutlined />}
              aria-label="刷新案件"
              loading={cases.isFetching || workbench.isFetching}
              onClick={() => {
                void cases.refetch();
                void workbench.refetch();
              }}
            >
              刷新
            </Button>
            <Button
              type="primary"
              icon={<PlusOutlined />}
              aria-label="新建案件"
              disabled={!canCreate}
              title={canCreate ? undefined : "当前角色没有新建案件权限"}
              onClick={() => setModalOpen(true)}
            >
              新建案件
            </Button>
          </>
        }
      />

      <section className="cases-kpis" aria-label="案件概况">
        <KpiStat
          label="全部案件"
          value={counts.all}
          hint="按当前身份与案件授权"
          icon={<FolderOpenOutlined />}
        />
        <KpiStat
          label="待审查"
          value={counts.pending}
          hint="等待审查或人工处理"
          icon={<FileSearchOutlined />}
          tone="medium"
        />
        <KpiStat
          label="审查中"
          value={counts.reviewing}
          hint="最新运行仍在进行"
          icon={<SyncOutlined />}
        />
        <KpiStat
          label="已完成"
          value={counts.completed}
          hint="最新运行已完成"
          icon={<CheckCircleOutlined />}
          tone="low"
        />
      </section>

      {workbench.isError ? (
        <div className="cases-inline-note" role="status">
          <WarningOutlined
            className="cases-inline-note__icon"
            aria-hidden="true"
          />
          <div>
            <strong>风险、负责人和右侧统计暂未加载</strong>
            <span>案件列表仍来自案件接口；这些字段需要工作台快照。</span>
          </div>
          <Button size="small" onClick={() => void workbench.refetch()}>
            重试
          </Button>
        </div>
      ) : null}

      <div className="cases-workspace">
        <div className="cases-main">
          <Card
            className="cases-panel"
            variant="outlined"
            styles={{ body: { padding: 0 } }}
          >
            <Tabs
              className="cases-tabs"
              activeKey={tab}
              onChange={(key) =>
                updateParam("tab", key === "all" ? "" : key, {
                  clearSelection: true,
                })
              }
              items={CASE_TABS.map((item) => ({
                key: item.key,
                label: `${item.label} (${counts[item.key]})`,
              }))}
            />
            <div className="cases-filter-bar" aria-label="案件筛选">
              <Input
                className="cases-filter-search"
                aria-label="搜索案件"
                type="search"
                allowClear
                prefix={<SearchOutlined />}
                value={query}
                placeholder="搜索案件名称或编号"
                onChange={(event) =>
                  updateParam("q", event.target.value, {
                    clearSelection: true,
                  })
                }
              />
              <Select<string>
                aria-label="筛选合同类型"
                value={contractTypeFilter}
                className="cases-filter-select"
                onChange={(value) =>
                  updateParam("type", value === "all" ? "" : value, {
                    clearSelection: true,
                  })
                }
                options={[
                  { value: "all", label: "全部合同类型" },
                  ...contractTypes.map((value) => ({
                    value,
                    label: contractTypeLabel(value),
                  })),
                ]}
              />
              <Select<string>
                aria-label="风险等级"
                value={risk}
                className="cases-filter-select"
                disabled={!workbench.data}
                onChange={(value) =>
                  updateParam("risk", value === "all" ? "" : value, {
                    clearSelection: true,
                  })
                }
                options={[
                  { value: "all", label: "全部风险等级" },
                  ...RISK_ROWS.map((row) => ({
                    value: row.key,
                    label: row.label,
                  })),
                ]}
              />
              <Select<string>
                aria-label="负责人"
                value={owner}
                className="cases-filter-select"
                disabled={!workbench.data}
                onChange={(value) =>
                  updateParam("owner", value === "all" ? "" : value, {
                    clearSelection: true,
                  })
                }
                options={[
                  { value: "all", label: "全部负责人" },
                  ...ownerOptions.options,
                  ...(ownerOptions.hasUnassigned
                    ? [{ value: "unassigned", label: "未设置负责人" }]
                    : []),
                ]}
              />
              <Select<string>
                aria-label="业务状态"
                value={String(businessStatus)}
                className="cases-filter-select"
                onChange={(value) =>
                  updateParam(
                    "business_status",
                    value === "all" ? "" : value,
                    { clearSelection: true },
                  )
                }
                options={[
                  { value: "all", label: "全部业务状态" },
                  ...businessStatuses.map((status) => ({
                    value: status,
                    label: status,
                  })),
                ]}
              />
              <Select<string>
                aria-label="运行状态"
                value={String(run)}
                className="cases-filter-select"
                onChange={(value) =>
                  updateParam("run", value === "all" ? "" : value, {
                    clearSelection: true,
                  })
                }
                options={FILTERS.map((item) => ({
                  value: String(item.id),
                  label: String(item.label ?? ""),
                }))}
              />
              <Button
                type="text"
                icon={<ClearOutlined />}
                aria-label="重置筛选"
                disabled={!hasFilters}
                onClick={resetFilters}
              >
                重置
              </Button>
            </div>

            <div className="cases-table-wrap">
              {cases.data?.length === 0 ? (
                <EmptyState
                  title="还没有案件"
                  detail="新建案件，登记材料并完成解析后开始审查。"
                />
              ) : sorted.length === 0 ? (
                <EmptyState
                  title="没有匹配的案件"
                  detail="当前搜索或筛选条件下没有可见结果。"
                  action={
                    <Button type="text" onClick={resetFilters}>
                      清除筛选
                    </Button>
                  }
                />
              ) : (
                <>
                  <CasesTable
                    items={pageItems}
                    sort={sort}
                    direction={direction}
                    selectedId={selectedId}
                    onSort={sortBy}
                    workbenchLoading={workbench.isLoading}
                  />
                  <div className="cases-pagination">
                    <Pagination
                      current={page}
                      total={sorted.length}
                      pageSize={pageSize}
                      pageSizeOptions={[...PAGE_SIZES]}
                      showSizeChanger
                      showTotal={(total, range) =>
                        `显示 ${range[0]}–${range[1]} 条，共 ${total} 条`
                      }
                      onChange={(nextPage, nextSize) => {
                        const next = new URLSearchParams(params);
                        next.set("page_size", String(nextSize));
                        if (nextSize !== pageSize) next.delete("page");
                        else next.set("page", String(nextPage));
                        setParams(next, { replace: true });
                      }}
                    />
                  </div>
                </>
              )}
            </div>
          </Card>
        </div>

        <aside className="cases-rail">
          <SectionCard
            ariaLabel="风险分布"
            title="风险分布"
            extra={<span className="cases-scope">按案件</span>}
          >
            {workbench.isLoading ? (
              <LoadingBlock label="正在加载风险分布" />
            ) : workbench.isError ? (
              <EmptyState
                title="风险分布没有加载"
                detail="需要工作台快照中的案件风险等级。"
                action={
                  <Button size="small" onClick={() => void workbench.refetch()}>
                    重试
                  </Button>
                }
              />
            ) : caseRiskTotal === 0 ? (
              <EmptyState
                title="还没有可见案件"
                detail="风险分布按案件最新审查结论的风险等级统计。"
              />
            ) : (
              <>
                <div className="cases-risk">
                  <DonutChart
                    items={caseRiskItems}
                    centerValue={caseRiskTotal}
                    centerLabel="案件"
                    size={120}
                    ariaLabel={`案件风险分布：${caseRiskItems
                      .map((item) => `${item.label} ${item.value} 件`)
                      .join("，")}`}
                  />
                  <ChartLegend
                    items={caseRiskItems}
                    total={caseRiskTotal}
                  />
                </div>
                <p className="cases-risk__note">
                  按每个案件最新审查结论的风险等级统计，与左侧案件数量同口径。
                </p>
              </>
            )}
          </SectionCard>

          <SectionCard
            ariaLabel="我的任务"
            title="我的任务"
            extra={
              <Link className="cases-rail__link" to="/workbench/tasks">
                查看全部 →
              </Link>
            }
          >
            {workbench.isLoading ? (
              <LoadingBlock label="正在加载我的任务" />
            ) : myTasks.length === 0 ? (
              <EmptyState
                title="没有未完成任务"
                detail="当前身份没有尚未完成的分派任务。"
              />
            ) : (
              <ul className="cases-task-list" aria-label="我的未完成任务">
                {myTasks.slice(0, 4).map((task) => (
                  <li key={task.id}>
                    <span
                      className={`cases-task-list__dot cases-task-list__dot--${task.priority}`}
                      aria-hidden="true"
                    />
                    <div>
                      <Link
                        to={caseLink({
                          id: task.case_id,
                          latest_run: null,
                        })}
                      >
                        {task.title}
                      </Link>
                      <span className="cases-task-list__meta">
                        {taskKindLabel(task.kind)} ·{" "}
                        {taskPriorityLabel(task.priority)} ·{" "}
                        {task.due_at ? (
                          <time
                            dateTime={task.due_at}
                            className={
                              isOverdue(task, now)
                                ? "cases-task-list__overdue"
                                : undefined
                            }
                          >
                            {isOverdue(task, now) ? "已逾期 · " : "截止 "}
                            {formatDateTime(task.due_at)}
                          </time>
                        ) : (
                          "未设期限"
                        )}
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </SectionCard>

          <SectionCard
            ariaLabel="快捷操作"
            title="快捷操作"
          >
            <QuickActions
              ariaLabel="案件快捷操作"
              items={[
                {
                  to: "/cases?new=1",
                  icon: <PlusOutlined />,
                  title: "新建案件",
                  description: canCreate
                    ? "登记合同并上传材料"
                    : "当前角色没有新建权限",
                  disabled: !canCreate,
                  disabledReason: "当前角色没有新建案件权限",
                },
                {
                  to: "/review",
                  icon: <FileSearchOutlined />,
                  title: "发起审查",
                  description: "选择案件启动审查",
                },
                {
                  to: "/workbench/tasks",
                  icon: <ClockCircleOutlined />,
                  title: "任务中心",
                  description: "查看分派与截止时间",
                },
                {
                  to: "/workbench/reports",
                  icon: <FileDoneOutlined />,
                  title: "报告中心",
                  description: "查看冻结报告与导出",
                },
              ]}
            />
          </SectionCard>
        </aside>
      </div>

      {modalOpen ? (
        <Modal
          open
          title="新建案件"
          footer={null}
          onCancel={() => setModalOpen(false)}
          destroyOnHidden
        >
          <form onSubmit={onCreate}>
            <h2 id={titleId}>新建案件</h2>
            <label className="field">
              标题
              <Input
                aria-label="案件标题"
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                required
              />
            </label>
            <label className="field">
              业务场景
              <Select<string>
                aria-label="业务场景"
                value={String(scenario)}
                onChange={(selectedValue) => {
                  setScenario(selectedValue);
                  if (selectedValue === "cloud_services")
                    setContractType("service");
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
            {titleError ? <p role="alert">{titleError}</p> : null}
            {createCase.isError ? (
              <p role="alert">{errorMessage(createCase.error)}</p>
            ) : null}
            <div className="dialog-actions">
              <Button type="text" htmlType="button" onClick={() => setModalOpen(false)}>
                取消
              </Button>
              <Button
                type="primary"
                htmlType="submit"
                loading={createCase.isPending}
              >
                {createCase.isPending ? "正在创建" : "创建"}
              </Button>
            </div>
          </form>
        </Modal>
      ) : null}
    </section>
  );
}
