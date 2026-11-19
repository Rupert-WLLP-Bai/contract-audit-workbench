// File metadata (manifest mirror):
// Purpose: Renders the client shell with case and module breadcrumbs, actor and tenant context, and business and management links supplied to shared responsive navigation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-19
// Revision: r5
// Copyright: See LICENSE
// End file metadata.

import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, Outlet, useLocation, useNavigate, type NavigateFunction } from "react-router";
import { useCaseDetail, useLogout, useSession } from "../api/queries";
import { presentedPersonName } from "../domain/organization";
import { actorRoleLabel } from "../domain/review";
import { Avatar, Breadcrumb, Button, Input, Layout } from "antd";
import {
  ApiOutlined,
  AppstoreOutlined,
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
import { ShellNavigation, type ShellNavGroup, type ShellNavLink } from "./shell-navigation";
type NavLinkItem = {
  to: string;
  label: string;
  icon: string;
  external?: boolean;
  href?: string;
  adminRoleOnly?: boolean;
};

const NAV_GROUPS: {
  key: string;
  title: string;
  icon?: ReactNode;
  links: NavLinkItem[];
}[] = [
  {
    key: "primary",
    title: "合同业务",
    links: [
      { to: "/contract-desk", label: "合同", icon: "contracts" },
      { to: "/workbench/tasks", label: "我的待办", icon: "tasks" },
      { to: "/cases", label: "案件与材料", icon: "case" },
    ],
  },
  {
    key: "business",
    title: "办理",
    links: [
      { to: "/workbench/approvals", label: "合同会签", icon: "contracts" },
      { to: "/contracts", label: "合同登记与履约", icon: "contracts" },
      { to: "/workbench/reports", label: "报告中心", icon: "reports" },
    ],
  },
  {
    key: "more-business",
    title: "更多业务",
    icon: <AppstoreOutlined aria-hidden="true" />,
    links: [
      { to: "/workbench/home", label: "工作台概览", icon: "home" },
      { to: "/review", label: "案件审查入口", icon: "review" },
      { to: "/workbench/remediation", label: "整改中心", icon: "remediation" },
      { to: "/workbench/evidence", label: "证据中心", icon: "evidence" },
      { to: "/workbench/evidence-sla", label: "补证时效", icon: "evidence-sla" },
      { to: "/support", label: "案件支援访问", icon: "support" },
      { to: "/compare", label: "版本对照", icon: "compare" },
      { to: "/subjects", label: "主体台账", icon: "subjects" },
      { to: "/workbench/knowledge", label: "知识库", icon: "knowledge" },
      { to: "/workbench/templates", label: "模板管理", icon: "templates" },
    ],
  },
  {
    key: "management-tools",
    title: "管理与工具",
    icon: <ToolOutlined aria-hidden="true" />,
    links: [
      { to: "/workbench/organization", label: "组织与权限", icon: "organization" },
      { to: "/workbench/executions", label: "Agent Studio", icon: "executions" },
      { to: "/workbench/tools", label: "工具网关", icon: "tools" },
      { to: "/api-docs", label: "API 文档", icon: "api" },
      { to: "/workbench/settings", label: "系统设置", icon: "settings", adminRoleOnly: true },
      { to: "/workbench/health", label: "系统健康", icon: "health", adminRoleOnly: true },
      { to: "/workbench/audit", label: "审计日志", icon: "audit", adminRoleOnly: true },
      { to: "rules", label: "规则包治理", icon: "rules", external: true, adminRoleOnly: true },
      { to: "/docs/", label: "使用手册", icon: "knowledge", external: true, href: "/docs/" },
    ],
  },
];

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
  executions: "Agent Studio",
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
  if (pathname === "/" || pathname === "/contract-desk") return [];
  if (pathname === "/workbench/home") return [{ label: "工作台概览", to: null }];
  if (pathname === "/review")
    return [
      { label: "工作台", to: "/workbench/home" },
      { label: "合同审查", to: null },
    ];
  if (pathname === "/compare")
    return [
      { label: "工作台", to: "/workbench/home" },
      { label: "版本对照", to: null },
    ];
  if (pathname === "/cases")
    return [
      { label: "工作台", to: "/workbench/home" },
      { label: "案件管理", to: null },
    ];
  if (pathname === "/contracts")
    return [
      { label: "工作台", to: "/workbench/home" },
      { label: "合同登记", to: null },
    ];
  if (pathname === "/workbench/approvals")
    return [
      { label: "工作台", to: "/workbench/home" },
      { label: "合同会签", to: null },
    ];
  if (pathname === "/subjects")
    return [
      { label: "工作台", to: "/workbench/home" },
      { label: "主体台账", to: null },
    ];
  if (pathname.startsWith("/subjects/"))
    return [
      { label: "工作台", to: "/workbench/home" },
      { label: "主体台账", to: "/subjects" },
      { label: "主体详情", to: null },
    ];
  return null;
}

function caseBreadcrumbs(caseId: string, caseTitle: string, inReview: boolean): Crumb[] {
  return [
    { label: "工作台", to: "/workbench/home" },
    { label: "案件", to: "/cases" },
    {
      label: caseTitle,
      to: inReview ? `/cases/${encodeURIComponent(caseId)}` : null,
    },
    ...(inReview ? [{ label: "审查工作区", to: null }] : []),
  ];
}

function moduleTitle(moduleKey: string): string {
  return Object.hasOwn(MODULE_TITLES, moduleKey) ? MODULE_TITLES[moduleKey]! : "工作区";
}

function workbenchBreadcrumbs(pathname: string): Crumb[] {
  const moduleKey = pathname.slice("/workbench/".length).split("/")[0]!;
  return [
    { label: "工作台", to: "/workbench/home" },
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
    if (/^\/cases\/[^/]+\/reviews\/[^/]+\/agent$/.test(pathname)) {
      return [...crumbs, { label: "Agent Studio", to: null }];
    }
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

function navigationPath(pathname: string): string {
  if (pathname === "/") return "/contract-desk";
  if (/^\/cases\/[^/]+\/reviews\/[^/]+\/agent$/.test(pathname)) return "/workbench/executions";
  if (pathname === "/support-grants" || pathname.startsWith("/support-grants/")) return "/support";
  return pathname;
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

function layoutClassName(inReview: boolean, pathname: string): string {
  const classes = ["app"];
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
  showCaseSearch,
  logoutPending,
  onSearchChange,
  onSearchSubmit,
  onLogout,
  onContextTransition,
}: {
  actor: Actor | undefined;
  search: string;
  showCaseSearch: boolean;
  logoutPending: boolean;
  onSearchChange: (value: string) => void;
  onSearchSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onLogout: () => void;
  onContextTransition: (pending: boolean) => void;
}) {
  return (
    <Layout.Header
      className={
        showCaseSearch ? "topbar client-topbar" : "topbar client-topbar topbar--without-search"
      }
    >
      <Link
        className="brand competition-brand"
        to="/contract-desk"
        aria-label="合同智能审计工作台首页，第一届黄桷树AI智能体开发大赛"
      >
        <CompetitionIdentity />
      </Link>
      <ContextSwitcher actor={actor} audience="client" onTransition={onContextTransition} />
      {showCaseSearch ? (
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
      ) : null}
      <div className="who" aria-label="当前身份">
        {actor ? (
          <>
            <Avatar className="header-avatar">
              {Array.from(presentedPersonName(actor.id, actor.display_name))[0]}
            </Avatar>
            <span className="identity-copy">
              <strong>{presentedPersonName(actor.id, actor.display_name)}</strong>
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
          <span className="header-logout-label">退出</span>
        </Button>
      </div>
    </Layout.Header>
  );
}

function AppSidebar({
  actor,
  pathname,
  adminRulePacksUrl,
}: {
  actor: Actor | undefined;
  pathname: string;
  adminRulePacksUrl: string;
}) {
  const canSeeManagementLinks = actor?.role === "admin";
  const selectedPath = navigationPath(pathname);
  const groups: ShellNavGroup[] = NAV_GROUPS.map(({ key, title, icon, links }) => ({
    key,
    title,
    disclosureIcon: icon,
    links: links
      .filter((item) => !item.adminRoleOnly || canSeeManagementLinks)
      .map((item): ShellNavLink => ({
        href: item.external ? (item.href ?? adminRulePacksUrl) : item.to,
        label: item.label,
        icon: NAV_ICONS[item.icon],
        external: item.external,
        current:
          !item.external && (selectedPath === item.to || selectedPath.startsWith(`${item.to}/`)),
      })),
  }));

  return <ShellNavigation title="主导航" groups={groups} />;
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
  const [search, setSearch] = useState("");
  const [contextTransition, setContextTransition] = useState(false);
  const caseId = caseIdOf(location.pathname);
  const caseDetail = useCaseDetail(caseId);
  useEffect(() => {
    setSearch(searchFromLocation(location.search));
  }, [location.pathname, location.search]);
  const reviewMatch = location.pathname.match(/^\/cases\/([^/]+)\/reviews\/([^/]+)/);
  const caseTitle = caseTitleOf(caseDetail.data?.title);
  const breadcrumbs = breadcrumbsFor(location.pathname, caseId, caseTitle, reviewMatch !== null);

  return (
    <Layout className={layoutClassName(reviewMatch !== null, location.pathname)}>
      <a className="skip" href="#main">
        跳到主要内容
      </a>
      <AppTopbar
        actor={actor}
        search={search}
        showCaseSearch={location.pathname !== "/" && location.pathname !== "/contract-desk"}
        logoutPending={logout.isPending}
        onSearchChange={setSearch}
        onSearchSubmit={(event) => {
          event.preventDefault();
          navigateSearch(navigate, location, search);
        }}
        onLogout={() => {
          void logout.mutateAsync().finally(() => {
            void navigate("/login", {
              replace: true,
              state: { from: location.pathname + location.search + location.hash },
            });
          });
        }}
        onContextTransition={setContextTransition}
      />
      <Layout className="frame" hasSider>
        <AppSidebar
          actor={actor}
          pathname={location.pathname}
          adminRulePacksUrl={adminRulePacksUrl}
        />
        <Layout.Content className="content-column">
          {breadcrumbs.length > 0 ? <BreadcrumbItems crumbs={breadcrumbs} /> : null}
          <main id="main" tabIndex={-1}>
            {contextTransition ? (
              <PageStatus kind="loading" title="正在确认新的工作上下文" />
            ) : (
              <Outlet />
            )}
          </main>
        </Layout.Content>
      </Layout>
    </Layout>
  );
}
