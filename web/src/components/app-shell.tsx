import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router";
import { useCaseDetail, useLogout, useSession } from "../api/queries";
import { actorRoleLabel } from "../domain/review";
const MAIN_LINKS = [
  { to: "/", label: "工作台", icon: "home", end: true },
  { to: "/review", label: "合同审查", icon: "review" },
  { to: "/compare", label: "版本对照", icon: "compare" },
];
const AUXILIARY_LINKS = [
  { to: "/cases", label: "案件管理", icon: "case", end: true },
  { to: "/workbench/tasks", label: "审查任务", icon: "tasks" },
  { to: "/workbench/remediation", label: "整改中心", icon: "remediation" },
  { to: "/workbench/reports", label: "报告中心", icon: "reports" },
  { to: "/workbench/evidence", label: "证据中心", icon: "evidence" },
  { to: "/workbench/executions", label: "执行轨迹", icon: "executions" },
  { to: "/workbench/knowledge", label: "知识库", icon: "knowledge" },
  { to: "/workbench/organization", label: "组织与权限", icon: "organization" },
  { to: "/workbench/templates", label: "模板管理", icon: "templates" },
  { to: "/workbench/models", label: "模型服务", icon: "models" },
  { to: "/workbench/tools", label: "工具网关", icon: "tools" },
  { to: "/workbench/health", label: "系统健康", icon: "health" },
  { to: "/workbench/audit", label: "审计日志", icon: "audit" },
  { to: "/workbench/settings", label: "系统设置", icon: "settings" },
];

function AppNavIcon({ name }: { name: string }) {
  const glyph: Record<string, string> = {
    home: "M3 10 12 3l9 7v10a1 1 0 0 1-1 1h-5v-7H9v7H4a1 1 0 0 1-1-1Z",
    review: "M5 3h9l5 5v13H5ZM14 3v6h5M8 13h8M8 17h6",
    compare: "M3 4h7v16H3Zm11 0h7v16h-7M7 9h3m4 6h3",
    rules: "m12 3 10 5-10 5L2 8Zm-9 10 9 5 9-5M3 17l9 5 9-5",
    case: "M8 6V4h8v2M3 7h18v13H3ZM3 12c6 3 12 3 18 0M10 12h4v4h-4Z",
    tasks: "m7 12 3 3 7-7M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0",
    remediation: "M12 3 3 7v5c0 5 4 8 9 10 5-2 9-5 9-10V7ZM8 12l3 3 5-6",
    reports: "M5 3h9l5 5v13H5ZM14 3v6h5M8 13h8M8 17h6",
    evidence: "M5 3h9l5 5v13H5ZM14 3v6h5M8 13h8M8 17h6",
    executions: "M4 20V10h3v10m4 0V4h3v16m4 0V8h3v12",
    knowledge: "M12 5c-3-2-6-2-10-1v15c4-1 7-1 10 1 3-2 6-2 10-1V4c-4-1-7-1-10 1Zm0 0v15",
    organization: "M16 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0M4 21v-2a8 8 0 0 1 16 0v2",
    templates: "M5 3h9l5 5v13H5ZM14 3v6h5M8 13h8M8 17h6",
    models: "M3 12h4l3-8 4 16 3-8h4",
    tools: "m8 6-6 6 6 6m8-12 6 6-6 6m-3-14-2 16",
    health: "M3 12h4l3-8 4 16 3-8h4",
    audit: "M5 3h9l5 5v13H5ZM14 3v6h5M8 13h8M8 17h6",
    settings: "m10 3-1 3-3 1-3-1-1 4 3 2v3l-2 3 3 3 3-2h3l3 2 3-3-2-3v-3l3-2-1-4-3 1-3-1-1-3ZM15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0",
    api: "M4 3h12l4 4v14H4ZM8 11l-2 3 2 3m6-6 2 3-2 3m-3-7-1 8",
  };
  return <svg viewBox="0 0 24 24"><path d={glyph[name] ?? glyph.case} /></svg>;
}


export function AppShell() {
  const session = useSession();
  const logout = useLogout();
  const navigate = useNavigate();
  const location = useLocation();
  const actor = session.data;
  const adminRulePacksUrl = useMemo(() => {
    const base = import.meta.env.VITE_ADMIN_URL;
    return base ? new URL("/admin/rulepacks", base).href : "/admin/rulepacks";
  }, []);
  const [collapsed, setCollapsed] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [search, setSearch] = useState("");
  const caseMatch = location.pathname.match(/^\/cases\/([^/]+)/);
  const caseId = caseMatch?.[1] ? decodeURIComponent(caseMatch[1]) : "";
  const caseDetail = useCaseDetail(caseId);
  useEffect(() => {
    setSearch(new URLSearchParams(location.search).get("q") ?? "");
  }, [location.pathname, location.search]);
  const reviewMatch = location.pathname.match(/^\/cases\/([^/]+)\/reviews\/([^/]+)/);

  function searchCases(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const query = search.trim();
    const params = new URLSearchParams(location.search);
    if (query === "") params.delete("q");
    else params.set("q", query);
    params.delete("page");
    params.delete("selected");
    const suffix = params.toString();
    void navigate(`/cases${suffix === "" ? "" : `?${suffix}`}`);
    setMobileNavOpen(false);
  }

  const caseTitle = caseDetail.data?.title ?? caseId;
  const moduleTitles: Record<string, string> = { tasks: "审查任务", remediation: "整改中心", reports: "报告中心", evidence: "证据中心", executions: "执行轨迹", knowledge: "知识库", organization: "组织与权限", templates: "模板管理", models: "模型服务", tools: "工具网关", health: "系统健康", audit: "审计日志", settings: "系统设置" };
  const moduleKey = location.pathname.split("/")[2] ?? "";
  const breadcrumbs = location.pathname === "/"
    ? [{ label: "工作台", to: null }]
    : location.pathname === "/review"
      ? [{ label: "工作台", to: "/" }, { label: "合同审查", to: null }]
      : location.pathname === "/compare"
        ? [{ label: "工作台", to: "/" }, { label: "版本对照", to: null }]
        : location.pathname === "/cases"
          ? [{ label: "工作台", to: "/" }, { label: "案件管理", to: null }]
          : caseId
            ? [
                { label: "工作台", to: "/" },
                { label: "案件", to: "/cases" },
                { label: caseTitle, to: reviewMatch ? `/cases/${encodeURIComponent(caseId)}` : null },
                ...(reviewMatch ? [{ label: `审查 · ${decodeURIComponent(reviewMatch[2] ?? "")}`, to: null }] : []),
              ]
            : location.pathname.startsWith("/workbench/")
              ? [{ label: "工作台", to: "/" }, { label: "业务管理", to: null }, { label: moduleTitles[moduleKey] ?? "工作区", to: null }]
              : [{ label: "开发者", to: null }, { label: "API 接入", to: null }];

  return (
    <div className={`app${collapsed ? " app--nav-collapsed" : ""}${reviewMatch ? " app--review" : ""}${location.pathname === "/compare" ? " app--compare" : ""}${location.pathname === "/api-docs" ? " app--api-docs" : ""}`}>
      <a className="skip" href="#main">跳到主要内容</a>
      <header className="topbar">
        <Link className="brand" to="/" aria-label="合同审查工作台首页">
          <span className="brand-mark" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="m12 2 9 5v10l-9 5-9-5V7z" /><path d="m12 6 5 3v5c0 3-5 5-5 5s-5-2-5-5V9zM9 12l2 2 4-4" /></svg></span>
          <span className="brand-copy"><strong>合同智能审计工作台</strong></span>
        </Link>
        <span className="organization-badge" title={actor?.org_id}>组织 · {actor?.org_id ?? "正在确认"}</span>
        <button className="nav-mobile-toggle" type="button" aria-expanded={mobileNavOpen} aria-controls="primary-navigation" onClick={() => setMobileNavOpen((value) => !value)}>
          {mobileNavOpen ? "关闭导航" : "打开导航"}
        </button>
        <form className="global-search" role="search" onSubmit={searchCases}>
          <label className="sr-only" htmlFor="global-case-search">全局搜索案件</label>
          <input id="global-case-search" type="search" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索案件标题或编号" />
          <button type="submit">搜索</button>
        </form>
        <div className="who" aria-label="当前身份">
          {actor ? <><span className="identity-avatar" aria-hidden="true">{Array.from(actor.display_name)[0]}</span><span className="identity-copy"><strong>{actor.display_name}</strong><span className="meta">{actorRoleLabel(actor.role)}</span></span></> : <span>身份未知</span>}
          <button className="button button-quiet" type="button" disabled={logout.isPending} onClick={() => {
            void logout.mutateAsync().finally(() => { void navigate("/login", { replace: true }); });
          }}>退出</button>
        </div>
      </header>
      <div className="frame">
        <nav id="primary-navigation" className="sidenav" aria-label="主导航" data-mobile-open={mobileNavOpen} onClick={() => setMobileNavOpen(false)}>
          <p className="nav-section-label">主要工作区</p>
          {MAIN_LINKS.map((item) => <NavLink key={item.to} to={item.to} end={item.to === "/"} className={({ isActive }) => `nav-item${isActive || (item.to === "/review" && Boolean(reviewMatch)) ? " nav-item--active" : ""}`} aria-label={item.label}><span className="nav-glyph" aria-hidden="true"><AppNavIcon name={item.icon} /></span><span className="nav-label">{item.label}</span></NavLink>)}
          <a className="nav-item" href={adminRulePacksUrl} aria-label="规则包治理"><span className="nav-glyph" aria-hidden="true"><AppNavIcon name="rules" /></span><span className="nav-label">规则包治理</span></a>
          <p className="nav-section-label">业务管理</p>
          {AUXILIARY_LINKS.map((item) => <NavLink key={item.to} to={item.to} end={item.to === "/cases"} className="nav-item" aria-label={item.label}><span className="nav-glyph" aria-hidden="true"><AppNavIcon name={item.icon} /></span><span className="nav-label">{item.label}</span></NavLink>)}
          <p className="nav-section-label">开发者资源</p>
          <NavLink to="/api-docs" className="nav-item" aria-label="API 文档"><span className="nav-glyph" aria-hidden="true"><AppNavIcon name="api" /></span><span className="nav-label">API 文档</span></NavLink>
          <div className="nav-footer">审查建议需人工核验<br />决定与证据持续留存</div>
          <button className="nav-collapse-toggle" type="button" aria-label={collapsed ? "展开侧栏" : "收合侧栏"} aria-expanded={!collapsed} aria-controls="primary-navigation" onClick={() => setCollapsed((value) => !value)}>
            <span aria-hidden="true">{collapsed ? "›" : "‹"}</span><span className="nav-label">收合导航</span>
          </button>
        </nav>
        <div className="content-column">
          <nav className="breadcrumbs" aria-label="面包屑">
            {breadcrumbs.map((crumb, index) => (
              <span key={`${crumb.label}-${index}`}>
                {index > 0 ? <span className="breadcrumb-separator" aria-hidden="true">/</span> : null}
                {crumb.to ? <Link to={crumb.to}>{crumb.label}</Link> : <span aria-current="page">{crumb.label}</span>}
              </span>
            ))}
          </nav>
          <main id="main" tabIndex={-1}><Outlet /></main>
        </div>
      </div>
    </div>
  );
}
