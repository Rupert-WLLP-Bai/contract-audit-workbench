// File metadata (manifest mirror):
// Purpose: Renders the contract-first client shell and authorized route groups, using a compact drawer and breadcrumb return path on the focused review reading route.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-19
// Revision: r6
// Copyright: See LICENSE
// End file metadata.

import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link, Outlet, useLocation, useNavigate, type NavigateFunction } from "react-router";
import { useCaseDetail, useLogout, useSession } from "../api/queries";
import { presentedPersonName } from "../domain/organization";
import { actorReadContextKey } from "../domain/actor-read-context";
import { readCaseListReturn } from "./section-navigation";
import { actorRoleLabel } from "../domain/review";
import { Avatar, Breadcrumb, Button, Input, Layout, Popover, Switch } from "antd";
import {
  ApiOutlined,
  AuditOutlined,
  BookOutlined,
  CheckSquareOutlined,
  ClockCircleOutlined,
  ContainerOutlined,
  DownOutlined,
  FileDoneOutlined,
  FileSearchOutlined,
  ProfileOutlined,
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
import { ProfessionalModeProvider, useProfessionalMode } from "./app-shell-preferences";
import "./app-shell.css";
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
  professionalOnly?: boolean;
  compact?: boolean;
};

const NAV_GROUPS: {
  key: string;
  title: string;
  icon?: ReactNode;
  links: NavLinkItem[];
  bottomStart?: boolean;
}[] = [
  {
    key: "primary",
    title: "工作台",
    links: [{ to: "/", label: "我的待办", icon: "home" }],
  },
  {
    key: "review",
    title: "审查",
    links: [
      { to: "/review", label: "发起审查", icon: "review" },
      { to: "/cases", label: "案件与材料", icon: "case", compact: true },
      { to: "/compare", label: "版本对照", icon: "compare" },
      { to: "/workbench/remediation", label: "整改中心", icon: "remediation" },
      { to: "/workbench/reports", label: "报告中心", icon: "reports" },
    ],
  },
  {
    key: "contracts",
    title: "合同履约",
    links: [
      { to: "/contract-desk", label: "合同清单", icon: "contracts", compact: true },
      { to: "/workbench/approvals", label: "合同会签", icon: "approvals" },
      { to: "/contracts", label: "合同登记与履约", icon: "fulfillment" },
      { to: "/subjects", label: "主体台账", icon: "subjects" },
    ],
  },
  {
    key: "knowledge",
    title: "知识与规则",
    bottomStart: true,
    icon: <BookOutlined aria-hidden="true" />,
    links: [
      { to: "/workbench/knowledge", label: "知识库", icon: "knowledge" },
      { to: "/workbench/templates", label: "模板管理", icon: "templates" },
      { to: "rules", label: "规则包治理", icon: "rules", external: true, adminRoleOnly: true },
      { to: "/docs/", label: "使用手册", icon: "knowledge", external: true, href: "/docs/" },
    ],
  },
  {
    key: "management-tools",
    title: "更多工具",
    icon: <ToolOutlined aria-hidden="true" />,
    links: [
      { to: "/workbench/tasks", label: "任务管理", icon: "tasks" },
      { to: "/workbench/evidence", label: "证据中心", icon: "evidence" },
      { to: "/support", label: "案件支援访问", icon: "support" },
      { to: "/workbench/evidence-sla", label: "补证时效", icon: "evidence-sla" },
      { to: "/workbench/organization", label: "组织与权限", icon: "organization" },
      {
        to: "/workbench/executions",
        label: "Agent Studio",
        icon: "executions",
        professionalOnly: true,
      },
      { to: "/workbench/tools", label: "工具网关", icon: "tools", professionalOnly: true },
      { to: "/api-docs", label: "API 文档", icon: "api", professionalOnly: true },
      { to: "/workbench/settings", label: "系统设置", icon: "settings", adminRoleOnly: true },
      {
        to: "/workbench/health",
        label: "系统健康",
        icon: "health",
        adminRoleOnly: true,
        professionalOnly: true,
      },
      { to: "/workbench/audit", label: "审计日志", icon: "audit", adminRoleOnly: true },
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
  approvals: <FileDoneOutlined />,
  fulfillment: <ProfileOutlined />,
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
  state?: { restoreScrollY: number; actorScope: string };
}

function staticBreadcrumb(pathname: string): Crumb[] | null {
  if (pathname === "/") return [];
  if (pathname === "/contract-desk")
    return [
      { label: "我的待办", to: "/" },
      { label: "合同清单", to: null },
    ];
  if (pathname === "/review")
    return [
      { label: "我的待办", to: "/" },
      { label: "合同审查", to: null },
    ];
  if (pathname === "/compare")
    return [
      { label: "我的待办", to: "/" },
      { label: "版本对照", to: null },
    ];
  if (pathname === "/cases")
    return [
      { label: "我的待办", to: "/" },
      { label: "案件管理", to: null },
    ];
  if (pathname === "/contracts")
    return [
      { label: "我的待办", to: "/" },
      { label: "合同登记", to: null },
    ];
  if (pathname === "/workbench/approvals")
    return [
      { label: "我的待办", to: "/" },
      { label: "合同会签", to: null },
    ];
  if (pathname === "/subjects")
    return [
      { label: "我的待办", to: "/" },
      { label: "主体台账", to: null },
    ];
  if (pathname.startsWith("/subjects/"))
    return [
      { label: "我的待办", to: "/" },
      { label: "主体台账", to: "/subjects" },
      { label: "主体详情", to: null },
    ];
  return null;
}

function caseBreadcrumbs(
  caseId: string,
  caseTitle: string,
  inReview: boolean,
  listCrumb: Crumb,
): Crumb[] {
  return [
    { label: "我的待办", to: "/" },
    listCrumb,
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
    { label: "我的待办", to: "/" },
    { label: "业务管理", to: null },
    { label: moduleTitle(moduleKey), to: null },
  ];
}

function breadcrumbsFor(
  pathname: string,
  caseId: string,
  caseTitle: string,
  inReview: boolean,
  listCrumb: Crumb,
): Crumb[] {
  const fixed = staticBreadcrumb(pathname);
  if (fixed) return fixed;
  if (caseId) {
    const crumbs = caseBreadcrumbs(caseId, caseTitle, inReview, listCrumb);
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
      aria-label="面包屑导航"
      items={crumbs.map((crumb) => ({
        title: crumb.to ? (
          <Link to={crumb.to} state={crumb.state}>
            {crumb.label}
          </Link>
        ) : (
          crumb.label
        ),
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
      <Link className="brand client-brand" to="/" aria-label="合同审计工作台首页">
        合同审计工作台
      </Link>
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
      <AccountMenu
        actor={actor}
        logoutPending={logoutPending}
        onLogout={onLogout}
        onContextTransition={onContextTransition}
      />
    </Layout.Header>
  );
}

function AccountMenu({
  actor,
  logoutPending,
  onLogout,
  onContextTransition,
}: {
  actor: Actor | undefined;
  logoutPending: boolean;
  onLogout: () => void;
  onContextTransition: (pending: boolean) => void;
}) {
  const { enabled, setEnabled } = useProfessionalMode();
  const name = actor ? presentedPersonName(actor.id, actor.display_name) : "身份未知";
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement | HTMLAnchorElement>(null);
  return (
    <div className="who" aria-label="当前身份">
      <Popover
        trigger="click"
        placement="bottomRight"
        getPopupContainer={(element) => element.parentElement ?? document.body}
        open={open}
        onOpenChange={setOpen}
        content={
          <div
            className="client-account-menu"
            aria-label="个人设置"
            role="group"
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                event.stopPropagation();
                setOpen(false);
                trigger.current?.focus();
              }
            }}
          >
            <strong>{name}</strong>
            {actor ? (
              <>
                <span className="meta">
                  {actorRoleLabel(actor.role)}
                  {enabled ? ` · ${actor.role}` : null}
                </span>
                <span className="meta">
                  {actor.active_context.tenant_name} · {actor.active_context.org_name}
                </span>
                <ContextSwitcher
                  actor={actor}
                  audience="client"
                  hideSingle
                  onTransition={onContextTransition}
                />
                <label className="client-professional-switch">
                  专业模式
                  <Switch aria-label="专业模式" checked={enabled} onChange={setEnabled} />
                </label>
              </>
            ) : null}
            <Button
              type="text"
              aria-label="退出"
              icon={<LogoutOutlined />}
              disabled={logoutPending}
              onClick={onLogout}
            >
              退出
            </Button>
          </div>
        }
      >
        <Button
          className="client-account-trigger"
          ref={trigger}
          type="text"
          aria-label="头像菜单"
          aria-expanded={open}
        >
          <Avatar className="header-avatar" aria-hidden="true">
            {Array.from(name)[0]}
          </Avatar>
          <span className="identity-copy">{name}</span>
          <DownOutlined aria-hidden="true" />
        </Button>
      </Popover>
    </div>
  );
}

function AppSidebar({
  actor,
  pathname,
  adminRulePacksUrl,
  focusedReading,
  context,
}: {
  actor: Actor | undefined;
  pathname: string;
  adminRulePacksUrl: string;
  focusedReading: boolean;
  context?: ReactNode;
}) {
  const { enabled: professional } = useProfessionalMode();
  const canSeeManagementLinks = actor?.role === "admin";
  const selectedPath = navigationPath(pathname);
  const groups: ShellNavGroup[] = NAV_GROUPS.map(({ key, title, icon, links, bottomStart }) => ({
    key,
    title,
    disclosureIcon: icon,
    bottomStart,
    links: links
      .filter(
        (item) =>
          (!item.adminRoleOnly || canSeeManagementLinks) &&
          (!item.professionalOnly || professional),
      )
      .map((item): ShellNavLink => ({
        href: item.external ? (item.href ?? adminRulePacksUrl) : item.to,
        label: item.label,
        icon: NAV_ICONS[item.icon],
        external: item.external,
        compact: item.compact,
        current:
          !item.external && (selectedPath === item.to || selectedPath.startsWith(`${item.to}/`)),
      })),
  }));

  return (
    <ShellNavigation
      title="主导航"
      className="client-sidebar"
      groups={groups}
      compact={focusedReading}
      drawerFooter={context}
    />
  );
}

function ClientOutlet({ pathname }: { pathname: string }) {
  const { enabled } = useProfessionalMode();
  const selected = navigationPath(pathname);
  const requiresProfessionalMode = NAV_GROUPS.some(({ links }) =>
    links.some(
      ({ professionalOnly, to }) =>
        professionalOnly && (selected === to || selected.startsWith(`${to}/`)),
    ),
  );
  if (requiresProfessionalMode && !enabled) {
    return <PageStatus kind="empty" title="请在头像菜单中打开专业模式" />;
  }
  return <Outlet />;
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
  const focusedReading = /^\/cases\/[^/]+\/reviews\/[^/]+\/?$/.test(location.pathname);
  const caseTitle = caseTitleOf(caseDetail.data?.title);
  // CasesPage holds the return until its session and case list are confirmed.
  const returnScope =
    !contextTransition && !session.isError ? actorReadContextKey(actor ?? null) : "";
  const listReturn = readCaseListReturn(location.state, caseId, returnScope);
  const listCrumb: Crumb = {
    label: "案件",
    to: listReturn?.returnTo ?? "/cases",
    state: listReturn ? { restoreScrollY: listReturn.scrollY, actorScope: returnScope } : undefined,
  };
  const breadcrumbs = breadcrumbsFor(
    location.pathname,
    caseId,
    caseTitle,
    reviewMatch !== null,
    listCrumb,
  );
  const breadcrumbNav = breadcrumbs.length > 0 ? <BreadcrumbItems crumbs={breadcrumbs} /> : null;

  const topbar = (
    <AppTopbar
      actor={actor}
      search={search}
      showCaseSearch={!focusedReading && location.pathname !== "/contract-desk"}
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
  );
  return (
    <ProfessionalModeProvider actor={contextTransition ? undefined : actor}>
      <Layout
        className={`${layoutClassName(reviewMatch !== null, location.pathname)}${focusedReading ? " app--focused-review" : ""}`}
      >
        <a className="skip" href="#main">
          跳到主要内容
        </a>
        {!focusedReading && topbar}
        <Layout className="frame" hasSider>
          <AppSidebar
            actor={actor}
            pathname={location.pathname}
            adminRulePacksUrl={adminRulePacksUrl}
            focusedReading={focusedReading}
            context={focusedReading ? topbar : undefined}
          />
          <Layout.Content className="content-column">
            {focusedReading ? null : breadcrumbNav}
            <main id="main" tabIndex={-1}>
              {contextTransition ? (
                <PageStatus kind="loading" title="正在确认新的工作上下文" />
              ) : (
                <ClientOutlet pathname={location.pathname} />
              )}
            </main>
          </Layout.Content>
        </Layout>
      </Layout>
    </ProfessionalModeProvider>
  );
}
