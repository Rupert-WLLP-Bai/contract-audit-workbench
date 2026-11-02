import { MenuFoldOutlined, MenuUnfoldOutlined } from "@ant-design/icons";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import {
  Link,
  NavLink,
  Outlet,
  useLocation,
  useNavigate,
  type NavigateFunction,
} from "react-router";
import { useCaseDetail, useLogout, useSession } from "../api/queries";
import { actorRoleLabel } from "../domain/review";
import { Avatar, Breadcrumb, Button, Input, Layout, Menu } from "antd";
import {
  ApiOutlined,
  AuditOutlined,
  BookOutlined,
  CheckSquareOutlined,
  ClockCircleOutlined,
  ContainerOutlined,
  FileSearchOutlined,
  FileTextOutlined,
  HomeOutlined,
  LogoutOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  TeamOutlined,
  ToolOutlined,
} from "@ant-design/icons";
import type { ReactNode } from "react";
import type { Actor } from "../api/types";
import { CompetitionIdentity } from "./competition-identity";
import { ContextSwitcher } from "./context-switcher";
import { PageStatus } from "./page-status";
type NavLinkItem = {
  to: string;
  label: string;
  icon: string;
  end?: boolean;
  external?: boolean;
};

const NAV_GROUPS: {
  key: string;
  label: string;
  links: NavLinkItem[];
}[] = [
  {
    key: "main",
    label: "主要工作区",
    links: [
      { to: "/", label: "工作台", icon: "home", end: true },
      { to: "/review", label: "合同审查", icon: "review" },
      { to: "/compare", label: "版本对照", icon: "compare" },
    ],
  },
  {
    key: "business",
    label: "业务管理",
    links: [
      { to: "/cases", label: "案件管理", icon: "case", end: true },
      { to: "/support", label: "案件支援访问", icon: "support" },
      { to: "/contracts", label: "合同登记", icon: "contracts", end: true },
      { to: "/workbench/approvals", label: "合同会签", icon: "contracts" },
      { to: "/subjects", label: "主体台账", icon: "subjects" },
      { to: "/workbench/tasks", label: "审查任务", icon: "tasks" },
      { to: "/workbench/remediation", label: "整改中心", icon: "remediation" },
      { to: "/workbench/reports", label: "报告中心", icon: "reports" },
      { to: "/workbench/evidence", label: "证据中心", icon: "evidence" },
      { to: "/workbench/evidence-sla", label: "补证时效", icon: "evidence-sla" },
      { to: "/workbench/knowledge", label: "知识库", icon: "knowledge" },
    ],
  },
  {
    key: "platform",
    label: "平台配置",
    links: [
      { to: "rules", label: "规则包治理", icon: "rules", external: true },
      {
        to: "/workbench/organization",
        label: "组织与权限",
        icon: "organization",
      },
      { to: "/workbench/templates", label: "模板管理", icon: "templates" },
      { to: "/workbench/tools", label: "工具网关", icon: "tools" },
      { to: "/workbench/settings", label: "系统设置", icon: "settings" },
    ],
  },
  {
    key: "observability",
    label: "可观测",
    links: [
      { to: "/workbench/executions", label: "执行轨迹", icon: "executions" },
      { to: "/workbench/health", label: "系统健康", icon: "health" },
      { to: "/workbench/audit", label: "审计日志", icon: "audit" },
    ],
  },
  {
    key: "developer",
    label: "开发者资源",
    links: [{ to: "/api-docs", label: "API 文档", icon: "api" }],
  },
];

const NAV_LINKS = NAV_GROUPS.flatMap((group) => group.links);

const NAV_ICONS: Record<string, ReactNode> = {
  home: <HomeOutlined />,
  review: <FileSearchOutlined />,
  compare: <FileTextOutlined />,
  rules: <SafetyCertificateOutlined />,
  case: <FileTextOutlined />,
  support: <SafetyCertificateOutlined />,
  contracts: <ContainerOutlined />,
  subjects: <TeamOutlined />,
  tasks: <CheckSquareOutlined />,
  remediation: <SafetyCertificateOutlined />,
  reports: <FileTextOutlined />,
  evidence: <FileSearchOutlined />,
  "evidence-sla": <ClockCircleOutlined />,
  executions: <AuditOutlined />,
  knowledge: <BookOutlined />,
  organization: <TeamOutlined />,
  templates: <FileTextOutlined />,
  tools: <ToolOutlined />,
  health: <CheckSquareOutlined />,
  audit: <AuditOutlined />,
  settings: <SettingOutlined />,
  api: <ApiOutlined />,
};

const MODULE_TITLES: Record<string, string> = {
  tasks: "审查任务",
  remediation: "整改中心",
  reports: "报告中心",
  evidence: "证据中心",
  "evidence-sla": "补充材料时效",
  executions: "执行轨迹",
  knowledge: "知识库",
  organization: "组织与权限",
  templates: "模板管理",
  models: "模型服务",
  tools: "工具网关",
  health: "系统健康",
  audit: "审计日志",
  settings: "系统设置",
};

interface Crumb {
  label: string;
  to: string | null;
}

function staticBreadcrumb(pathname: string): Crumb[] | null {
  if (pathname === "/") return [{ label: "工作台", to: null }];
  if (pathname === "/review")
    return [
      { label: "工作台", to: "/" },
      { label: "合同审查", to: null },
    ];
  if (pathname === "/compare")
    return [
      { label: "工作台", to: "/" },
      { label: "版本对照", to: null },
    ];
  if (pathname === "/cases")
    return [
      { label: "工作台", to: "/" },
      { label: "案件管理", to: null },
    ];
  if (pathname === "/contracts")
    return [
      { label: "工作台", to: "/" },
      { label: "合同登记", to: null },
    ];
  if (pathname === "/workbench/approvals")
    return [
      { label: "工作台", to: "/" },
      { label: "合同会签", to: null },
    ];
  if (pathname === "/subjects")
    return [
      { label: "工作台", to: "/" },
      { label: "主体台账", to: null },
    ];
  if (pathname.startsWith("/subjects/"))
    return [
      { label: "工作台", to: "/" },
      { label: "主体台账", to: "/subjects" },
      { label: "主体详情", to: null },
    ];
  return null;
}

function caseBreadcrumbs(
  caseId: string,
  caseTitle: string,
  inReview: boolean,
): Crumb[] {
  return [
    { label: "工作台", to: "/" },
    { label: "案件", to: "/cases" },
    {
      label: caseTitle,
      to: inReview ? `/cases/${encodeURIComponent(caseId)}` : null,
    },
    ...(inReview ? [{ label: "审查工作区", to: null }] : []),
  ];
}

function moduleTitle(moduleKey: string): string {
  return Object.hasOwn(MODULE_TITLES, moduleKey)
    ? MODULE_TITLES[moduleKey]!
    : "工作区";
}

function workbenchBreadcrumbs(pathname: string): Crumb[] {
  const moduleKey = pathname.slice("/workbench/".length).split("/")[0]!;
  return [
    { label: "工作台", to: "/" },
    { label: "业务管理", to: null },
    { label: moduleTitle(moduleKey), to: null },
  ];
}

function breadcrumbsFor(
  pathname: string,
  caseId: string,
  caseTitle: string,
  inReview: boolean,
): Crumb[] {
  const fixed = staticBreadcrumb(pathname);
  if (fixed) return fixed;
  if (caseId) {
    const crumbs = caseBreadcrumbs(caseId, caseTitle, inReview);
    return /^\/cases\/[^/]+\/subject-changes\/[^/]+/.test(pathname)
      ? [...crumbs, { label: "主体更正复核", to: null }]
      : crumbs;
  }
  if (pathname.startsWith("/workbench/")) return workbenchBreadcrumbs(pathname);
  return [
    { label: "开发者", to: null },
    { label: "API 接入", to: null },
  ];
}

function selectedMenuKey(
  pathname: string,
  inReview: boolean,
): string {
  if (pathname === "/") return "/";
  if (inReview) return "/review";
  const match = NAV_LINKS.find(
    (item) =>
      !item.external &&
      (pathname === item.to || pathname.startsWith(`${item.to}/`)),
  );
  return match ? match.to : pathname;
}

function searchFromLocation(search: string): string {
  const query = new URLSearchParams(search).get("q");
  return query === null ? "" : query;
}

function caseTitleOf(title: string | undefined): string {
  return title === undefined ? "案件" : title;
}

function navigateSearch(
  navigate: NavigateFunction,
  location: ReturnType<typeof useLocation>,
  query: string,
) {
  const trimmed = query.trim();
  const params = new URLSearchParams(location.search);
  if (trimmed === "") params.delete("q");
  else params.set("q", trimmed);
  params.delete("page");
  params.delete("selected");
  const suffix = params.toString();
  void navigate(`/cases${suffix === "" ? "" : `?${suffix}`}`);
}

function caseIdOf(pathname: string): string {
  const match = pathname.match(/^\/cases\/([^/]+)/);
  return match?.[1] ? decodeURIComponent(match[1]) : "";
}

function layoutClassName(
  collapsed: boolean,
  inReview: boolean,
  pathname: string,
): string {
  const classes = ["app"];
  if (collapsed) classes.push("app--nav-collapsed");
  if (inReview) classes.push("app--review");
  if (pathname === "/compare") classes.push("app--compare");
  if (pathname === "/api-docs") classes.push("app--api-docs");
  return classes.join(" ");
}

function BreadcrumbItems({ crumbs }: { crumbs: Crumb[] }) {
  return (
    <Breadcrumb
      className="breadcrumbs"
      items={crumbs.map((crumb) => ({
        title: crumb.to ? <Link to={crumb.to}>{crumb.label}</Link> : crumb.label,
      }))}
    />
  );
}

function AppTopbar({
  actor,
  search,
  logoutPending,
  onSearchChange,
  onSearchSubmit,
  onLogout,
  onContextTransition,
}: {
  actor: Actor | undefined;
  search: string;
  logoutPending: boolean;
  onSearchChange: (value: string) => void;
  onSearchSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onLogout: () => void;
  onContextTransition: (pending: boolean) => void;
}) {
  return (
    <Layout.Header className="topbar">
      <Link
        className="brand competition-brand"
        to="/"
        aria-label="第一届黄桷树AI智能体开发大赛，合同智能审计工作台首页"
      >
        <CompetitionIdentity />
      </Link>
      <ContextSwitcher actor={actor} audience="client" onTransition={onContextTransition} />
      <form className="global-search" role="search" onSubmit={onSearchSubmit}>
        <label className="sr-only" htmlFor="global-case-search">
          全局搜索案件
        </label>
        <Input
          className="header-search-input"
          id="global-case-search"
          type="search"
          value={search}
          onChange={(event) => onSearchChange(event.target.value)}
          placeholder="搜索案件标题或编号"
        />
        <Button className="header-action" aria-label={"搜索"} htmlType="submit" ghost>
          搜索
        </Button>
      </form>
      <div className="who" aria-label="当前身份">
        {actor ? (
          <>
            <Avatar className="header-avatar">
              {Array.from(actor.display_name)[0]}
            </Avatar>
            <span className="identity-copy">
              <strong>{actor.display_name}</strong>
              <span className="meta">{actorRoleLabel(actor.role)}</span>
            </span>
          </>
        ) : (
          <span>身份未知</span>
        )}
        <Button
          className="header-action"
          ghost
          aria-label={"退出"}
          icon={<LogoutOutlined />}
          disabled={logoutPending}
          onClick={onLogout}
        >
          退出
        </Button>
      </div>
    </Layout.Header>
  );
}

function AppSidebar({
  pathname,
  inReview,
  adminRulePacksUrl,
  collapsed,
  onCollapse,
}: {
  pathname: string;
  inReview: boolean;
  adminRulePacksUrl: string;
  collapsed: boolean;
  onCollapse: (collapsed: boolean) => void;
}) {
  return (
    <Layout.Sider
      trigger={
        <Button
          type="text"
          className="nav-collapse-action"
          aria-label={collapsed ? "展开侧栏" : "收合侧栏"}
          aria-expanded={!collapsed}
          icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
        />
      }
      theme="dark"
      id="primary-navigation"
      className="sidenav"
      aria-label="主导航"
      width={248}
      collapsedWidth={72}
      collapsed={collapsed}
      collapsible
      onCollapse={onCollapse}
    >
      <Menu
        mode="inline"
        theme="dark"
        selectedKeys={[selectedMenuKey(pathname, inReview)]}
        items={NAV_GROUPS.map((group) => ({
          key: group.key,
          type: "group" as const,
          label: group.label,
          children: group.links.map((item) => ({
            key: item.to,
            icon: NAV_ICONS[item.icon],
            label: item.external ? (
              <a href={adminRulePacksUrl}>{item.label}</a>
            ) : (
              <NavLink to={item.to} end={item.end}>
                {item.label}
              </NavLink>
            ),
          })),
        }))}
      />
    </Layout.Sider>
  );
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
  const [search, setSearch] = useState("");
  const [contextTransition, setContextTransition] = useState(false);
  const caseId = caseIdOf(location.pathname);
  const caseDetail = useCaseDetail(caseId);
  useEffect(() => {
    setSearch(searchFromLocation(location.search));
  }, [location.pathname, location.search]);
  const reviewMatch = location.pathname.match(
    /^\/cases\/([^/]+)\/reviews\/([^/]+)/,
  );
  const caseTitle = caseTitleOf(caseDetail.data?.title);
  const breadcrumbs = breadcrumbsFor(
    location.pathname,
    caseId,
    caseTitle,
    reviewMatch !== null,
  );

  return (
    <Layout
      className={layoutClassName(
        collapsed,
        reviewMatch !== null,
        location.pathname,
      )}
    >
      <a className="skip" href="#main">
        跳到主要内容
      </a>
      <AppTopbar
        actor={actor}
        search={search}
        logoutPending={logout.isPending}
        onSearchChange={setSearch}
        onSearchSubmit={(event) => {
          event.preventDefault();
          navigateSearch(navigate, location, search);
        }}
        onLogout={() => {
          void logout.mutateAsync().finally(() => {
            void navigate("/login", { replace: true, state: { from: location.pathname + location.search + location.hash } });
          });
        }}
        onContextTransition={setContextTransition}
      />
      <Layout className="frame" hasSider>
        <AppSidebar
          pathname={location.pathname}
          inReview={reviewMatch !== null}
          adminRulePacksUrl={adminRulePacksUrl}
          collapsed={collapsed}
          onCollapse={setCollapsed}
        />
        <Layout.Content className="content-column">
          <BreadcrumbItems crumbs={breadcrumbs} />
          <main id="main" tabIndex={-1}>
            {contextTransition ? (
              <PageStatus kind="loading" title="正在确认新的工作上下文" />
            ) : <Outlet />}
          </main>
        </Layout.Content>
      </Layout>
    </Layout>
  );
}
