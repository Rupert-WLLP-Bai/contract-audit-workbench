import { Select } from "antd";
import { Button, Input, Modal, Pagination } from "antd";
import { useId, useLayoutEffect, useState, type FormEvent } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";
import { errorMessage, isForbidden } from "../api/http";
import { useCases, useCreateCase, useSession } from "../api/queries";
import { CasesTable } from "../components/cases-table";
import { PageStatus } from "../components/page-status";
import {
  businessStatusLabel,
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
const collator = new Intl.Collator("zh-CN", {
  numeric: true,
  sensitivity: "base",
});
const scrollPositions = new Map<string, number>();

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
  const [open, setOpen] = useState(false);
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
  const filtered = filterCases(cases.data ?? [], query, run, businessStatus);
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

  if (cases.isLoading)
    return <PageStatus kind="loading" title="正在加载案件" />;
  if (cases.isError) {
    if (isForbidden(cases.error)) {
      return (
        <PageStatus
          kind="denied"
          title="没有权限查看案件列表"
          detail={errorMessage(cases.error)}
        />
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
    <section className="page cases-page">
      <header className="page-head">
        <div>
          <h1>案件</h1>
          <p>搜索和筛选当前身份有权查看的案件；状态以服务端记录为准。</p>
        </div>
        <Button
          aria-label={"新建案件"}
          type="primary"
          htmlType="button"
          disabled={!canCreate}
          onClick={() => setOpen(true)}
        >
          新建案件
        </Button>
      </header>

      <div className="cases-filters" aria-label="案件筛选">
        <label className="field cases-search">
          搜索案件
          <Input
            value={query}
            onChange={(event) =>
              updateParam("q", event.target.value, { clearSelection: true })
            }
            placeholder="标题或案件编号"
            type="search"
          />
        </label>
        <label className="field">
          业务状态
          <Select<string>
            aria-label="业务状态"
            value={String(businessStatus)}
            onChange={(selectedValue) =>
              updateParam("business_status", selectedValue, {
                clearSelection: true,
              })
            }
            options={[
              { value: "all", label: "全部业务状态" },
              ...businessStatuses.map((status) => ({
                value: String(status),
                label: businessStatusLabel(status),
              })),
            ]}
          />
        </label>
        <label className="field">
          运行状态
          <Select<string>
            aria-label="运行状态"
            value={String(run)}
            onChange={(selectedValue) =>
              updateParam("run", selectedValue === "all" ? "" : selectedValue, {
                clearSelection: true,
              })
            }
            options={[
              ...FILTERS.map((item) => ({
                value: String(item.id),
                label: String(item.label ?? ""),
              })),
            ]}
          />
        </label>
      </div>

      {cases.data?.length === 0 ? (
        <PageStatus
          kind="empty"
          title="还没有案件"
          detail="新建案件，登记材料并完成解析后开始审查。"
        />
      ) : filtered.length === 0 ? (
        <section className="page-status page-status-empty" role="status">
          <h2>没有匹配的案件</h2>
          <p>当前搜索或筛选条件下没有可见结果。</p>
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
        <>
          <CasesTable
            items={pageItems}
            sort={sort}
            direction={direction}
            selectedId={selectedId}
            onSort={sortBy}
          />
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
        </>
      )}

      {open ? (
        <Modal
          open
          title="新建案件"
          footer={null}
          onCancel={() => setOpen(false)}
          destroyOnHidden
        >
          <form onSubmit={onCreate}>
            <h2 id={titleId}>新建案件</h2>
            <label className="field">
              标题
              <Input
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
    </section>
  );
}
