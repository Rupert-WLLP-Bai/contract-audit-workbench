import { useEffect, useId, useLayoutEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";
import { errorMessage, isForbidden } from "../api/http";
import { useCases, useCreateCase, useSession } from "../api/queries";
import { CasesTable } from "../components/cases-table";
import { PageStatus } from "../components/page-status";
import { filterCases, parseRunFilter, validateCaseTitle, type CaseSortField, type RunFilter, type SortDirection } from "../domain/review";

function CreateDialog({ titleId, onClose, children }: { titleId: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const node = ref.current;
    if (!node) return undefined;
    node.showModal();
    return () => {
      if (node.open) node.close();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className="dialog"
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      {children}
    </dialog>
  );
}

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
const collator = new Intl.Collator("zh-CN", { numeric: true, sensitivity: "base" });
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
  const sort: CaseSortField = requestedSort === "title" || requestedSort === "pending_decisions" || requestedSort === "document_count" || requestedSort === "updated_at" ? requestedSort : "updated_at";
  const direction: SortDirection = params.get("direction") === "asc" ? "asc" : "desc";
  const requestedPage = Number(params.get("page"));
  const validatedPage = Number.isSafeInteger(requestedPage) && requestedPage > 0 ? requestedPage : 1;
  const requestedSize = Number(params.get("page_size"));
  const pageSize = PAGE_SIZES.includes(requestedSize as (typeof PAGE_SIZES)[number]) ? requestedSize : 10;
  const selectedId = params.get("selected");
  const [open, setOpen] = useState(false);
  const titleId = useId();
  const [title, setTitle] = useState("");
  const [contractType, setContractType] = useState("service");
  const [scenario, setScenario] = useState("telecom_dedicated_line");
  const [titleError, setTitleError] = useState<string | null>(null);

  useLayoutEffect(() => {
    const routeState = location.state as { restoreScrollY?: unknown } | null;
    const requested = typeof routeState?.restoreScrollY === "number" ? routeState.restoreScrollY : scrollPositions.get(location.key);
    if (requested !== undefined) window.scrollTo(0, requested);
    return () => {
      scrollPositions.set(location.key, window.scrollY);
    };
  }, [location.key, location.state]);

  function updateParam(key: string, value: string, { resetPage = true, clearSelection = false } = {}) {
    const next = new URLSearchParams(params);
    if (value === "") next.delete(key);
    else next.set(key, value);
    if (resetPage) next.delete("page");
    if (clearSelection) next.delete("selected");
    setParams(next, { replace: true });
  }

  function sortBy(field: CaseSortField) {
    const nextDirection = sort === field ? direction === "asc" ? "desc" : "asc" : field === "title" ? "asc" : "desc";
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
      { title: title.trim(), contract_type: contractType, business_line: "ENT", scenario },
      {
        onSuccess: (id) => {
          setOpen(false);
          setTitle("");
          void navigate(`/cases/${id}`);
        },
      },
    );
  }

  const businessStatuses = [...new Set((cases.data ?? []).map((item) => item.business_status).filter((value): value is string => Boolean(value)))].sort(collator.compare);
  const filtered = filterCases(cases.data ?? [], query, run, businessStatus);
  const sorted = [...filtered].sort((left, right) => {
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
  const canCreate = session.data?.role === "admin" || session.data?.role === "clerk";

  if (cases.isLoading) return <PageStatus kind="loading" title="正在加载案件" />;
  if (cases.isError) {
    if (isForbidden(cases.error)) {
      return <PageStatus kind="denied" title="没有权限查看案件列表" detail={errorMessage(cases.error)} />;
    }
    return (
      <PageStatus
        kind="error"
        title="案件列表没有加载"
        detail={errorMessage(cases.error)}
        action={<button className="button button-primary" type="button" onClick={() => void cases.refetch()}>重试</button>}
      />
    );
  }

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>案件</h1>
          <p>搜索和筛选当前身份有权查看的案件；状态以服务端记录为准。</p>
        </div>
        <button className="button button-primary" type="button" disabled={!canCreate} onClick={() => setOpen(true)}>
          新建案件
        </button>
      </header>

      <div className="cases-filters" aria-label="案件筛选">
        <label className="field cases-search">
          搜索案件
          <input value={query} onChange={(event) => updateParam("q", event.target.value, { clearSelection: true })} placeholder="标题或案件编号" type="search" />
        </label>
        <label className="field">
          业务状态
          <select aria-label="业务状态" value={businessStatus} onChange={(event) => updateParam("business_status", event.target.value, { clearSelection: true })}>
            <option value="all">全部业务状态</option>
            {businessStatuses.map((status) => <option key={status} value={status}>{status}</option>)}
          </select>
        </label>
        <label className="field">
          运行状态
          <select aria-label="运行状态" value={run} onChange={(event) => updateParam("run", event.target.value === "all" ? "" : event.target.value, { clearSelection: true })}>
            {FILTERS.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
          </select>
        </label>
      </div>

      {cases.data?.length === 0 ? (
        <PageStatus kind="empty" title="还没有案件" detail="新建案件，登记材料并完成解析后开始审查。" />
      ) : filtered.length === 0 ? (
        <section className="page-status page-status-empty" role="status">
          <h2>没有匹配的案件</h2>
          <p>当前搜索或筛选条件下没有可见结果。</p>
          <button className="button button-quiet" type="button" onClick={() => setParams(new URLSearchParams(), { replace: true })}>清除筛选</button>
        </section>
      ) : (
        <>
          <CasesTable items={pageItems} sort={sort} direction={direction} selectedId={selectedId} onSort={sortBy} />
          <div className="pagination" aria-label="案件分页">
            <p aria-live="polite">显示 {(page - 1) * pageSize + 1}–{Math.min(page * pageSize, sorted.length)} 条，共 {sorted.length} 条</p>
            <label className="field pagination-size">
              每页
              <select aria-label="每页条数" value={pageSize} onChange={(event) => updateParam("page_size", event.target.value)}>
                {PAGE_SIZES.map((size) => <option key={size} value={size}>{size}</option>)}
              </select>
              条
            </label>
            <button className="button button-quiet" type="button" disabled={page <= 1} onClick={() => updateParam("page", String(page - 1), { resetPage: false })}>上一页</button>
            <span aria-current="page">第 {page} / {pageCount} 页</span>
            <button className="button button-quiet" type="button" disabled={page >= pageCount} onClick={() => updateParam("page", String(page + 1), { resetPage: false })}>下一页</button>
          </div>
        </>
      )}

      {open ? (
        <CreateDialog titleId={titleId} onClose={() => setOpen(false)}>
          <form onSubmit={onCreate}>
            <h2 id={titleId}>新建案件</h2>
            <label className="field">
              标题
              <input value={title} onChange={(event) => setTitle(event.target.value)} required />
            </label>
            <label className="field">业务场景<select aria-label="业务场景" value={scenario} onChange={(event) => { setScenario(event.target.value); if (event.target.value === "cloud_services") setContractType("service"); }}><option value="telecom_dedicated_line">政企专线</option><option value="cloud_services">云服务采购</option></select></label>
            <label className="field">
              合同类型
              <select aria-label="合同类型" value={contractType} disabled={scenario === "cloud_services"} onChange={(event) => setContractType(event.target.value)}>
                <option value="procurement_ict">采购 / ICT</option>
                <option value="network_engineering">网络工程</option>
                <option value="service">服务合同</option>
                <option value="unspecified">未分类</option>
              </select>
            </label>
            {titleError ? <p role="alert">{titleError}</p> : null}
            {createCase.isError ? <p role="alert">{errorMessage(createCase.error)}</p> : null}
            <div className="dialog-actions">
              <button className="button button-quiet" type="button" onClick={() => setOpen(false)}>取消</button>
              <button className="button button-primary" type="submit" disabled={createCase.isPending}>{createCase.isPending ? "正在创建" : "创建"}</button>
            </div>
          </form>
        </CreateDialog>
      ) : null}
    </section>
  );
}
