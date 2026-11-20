// File metadata (manifest mirror):
// Purpose: Renders the client shell, shared case and contract search, the intake launcher, object navigation, identity menu and actor-scoped return links.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-20
// Revision: r9
// Copyright: See LICENSE
// End file metadata.

import { useEffect, useMemo, useRef, useState } from "react";
import { Link, Outlet, useLocation, useNavigate, type NavigateFunction } from "react-router";
import { useCaseDetail, useLogout, useSession } from "../api/queries";
import { presentedPersonName } from "../domain/organization";
import { actorReadContextKey } from "../domain/actor-read-context";
import { readCaseListReturn } from "./section-navigation";
import { actorRoleLabel } from "../domain/review";
import { Avatar, Breadcrumb, Button, Input, Layout, Popover, Switch, type InputRef } from "antd";
import {
  ApiOutlined,
  AuditOutlined,
  BookOutlined,
  CheckSquareOutlined,
  CloseCircleOutlined,
  DownOutlined,
  FileDoneOutlined,
  FolderOutlined,
  PlusOutlined,
  SearchOutlined,
  ProfileOutlined,
  FileTextOutlined,
  LogoutOutlined,
  SafetyCertificateOutlined,
  TeamOutlined,
  ToolOutlined,
} from "@ant-design/icons";
import type { ReactNode } from "react";
import type { Actor } from "../api/types";
import { DirtyLeaveGuardProvider } from "./dirty-leave-guard";
import { ReviewIntakeDialog } from "../features/intake/review-intake-dialog";
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
  adminRoleOnly?: boolean;
  professionalOnly?: boolean;
  compact?: boolean;
};

const CORE_NAV: NavLinkItem[] = [
  { to: "/", label: "我的待办", icon: "home", compact: true },
  { to: "/cases", label: "案件", icon: "case", compact: true },
  { to: "/contracts", label: "合同台账", icon: "ledger", compact: true },
  { to: "/workbench/approvals", label: "会签", icon: "approvals" },
  { to: "/workbench/remediation", label: "整改", icon: "remediation" },
  { to: "/workbench/reports", label: "报告", icon: "reports" },
  { to: "/subjects", label: "主体", icon: "subjects" },
  { to: "/workbench/executions", label: "Agent Studio", icon: "audit", professionalOnly: true },
];

const KNOWLEDGE_NAV: NavLinkItem[] = [
  { to: "/workbench/knowledge", label: "知识库", icon: "knowledge" },
  { to: "/workbench/templates", label: "合同模板", icon: "templates" },
  { to: "/docs/", label: "使用手册", icon: "knowledge", external: true },
  { to: "/workbench/organization", label: "组织与权限", icon: "subjects", adminRoleOnly: true },
  { to: "/workbench/audit", label: "审计日志", icon: "audit", adminRoleOnly: true },
  {
    to: "/admin/rulepacks",
    label: "规则包治理",
    icon: "rules",
    external: true,
    adminRoleOnly: true,
  },
  { to: "/workbench/tools", label: "工具网关", icon: "tools", professionalOnly: true },
  { to: "/api-docs", label: "API 文档", icon: "api", professionalOnly: true },
  {
    to: "/workbench/health",
    label: "系统健康",
    icon: "health",
    adminRoleOnly: true,
    professionalOnly: true,
  },
];

const NAV_ICONS: Record<string, ReactNode> = {
  home: <CheckSquareOutlined />,
  case: <FolderOutlined />,
  ledger: <ProfileOutlined />,
  approvals: <FileDoneOutlined />,
  remediation: <SafetyCertificateOutlined />,
  reports: <FileTextOutlined />,
  subjects: <TeamOutlined />,
  knowledge: <BookOutlined />,
  templates: <ProfileOutlined />,
  audit: <AuditOutlined />,
  rules: <SafetyCertificateOutlined />,
  tools: <ToolOutlined />,
  health: <CheckSquareOutlined />,
  api: <ApiOutlined />,
};

const LEGACY_TITLES: Record<string, string> = {
  "/contract-desk": "合同清单",
  "/review": "审一份合同",
  "/compare": "版本对照",
  "/support": "案件支援访问",
  "/workbench/tasks": "审查任务",
  "/workbench/evidence": "证据中心",
  "/workbench/evidence-sla": "补充材料时效",
  "/workbench/models": "模型服务",
  "/workbench/settings": "系统设置",
};

function currentLink(pathname: string, to: string) {
  return pathname === to || (to !== "/" && pathname.startsWith(`${to}/`));
}

interface Crumb {
  label: string;
  to: string | null;
  state?: { restoreScrollY: number; actorScope: string };
}

function staticBreadcrumb(pathname: string): Crumb[] | null {
  if (pathname === "/") return [];
  const home: Crumb = { label: "我的待办", to: "/" };
  if (pathname.startsWith("/subjects/")) {
    return [home, { label: "主体", to: "/subjects" }, { label: "主体详情", to: null }];
  }
  const label =
    CORE_NAV.find((item) => item.to === pathname)?.label ??
    KNOWLEDGE_NAV.find((item) => !item.external && currentLink(pathname, item.to))?.label ??
    Object.entries(LEGACY_TITLES).find(([path]) => currentLink(pathname, path))?.[1];
  return label ? [home, { label, to: null }] : null;
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
  return [
    { label: "我的待办", to: "/" },
    { label: pathname.startsWith("/workbench/") ? "工作区" : "未找到页面", to: null },
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

type SearchDestination = "/cases" | "/contracts";

function navigateSearch(
  navigate: NavigateFunction,
  location: ReturnType<typeof useLocation>,
  query: string,
  destination: SearchDestination,
) {
  const trimmed = query.trim();
  const params = new URLSearchParams(
    location.pathname === destination && destination === "/cases" ? location.search : "",
  );
  if (trimmed === "") params.delete("q");
  else params.set("q", trimmed);
  params.delete("page");
  params.delete("selected");
  const suffix = params.toString();
  void navigate(`${destination}${suffix === "" ? "" : `?${suffix}`}`);
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
  searchDestination,
  logoutPending,
  onSearchChange,
  onSearchSubmit,
  onSearchClear,
  onLogout,
  onContextTransition,
}: {
  actor: Actor | undefined;
  search: string;
  showCaseSearch: boolean;
  searchDestination: SearchDestination;
  logoutPending: boolean;
  onSearchChange: (value: string) => void;
  onSearchSubmit: (destination: SearchDestination) => void;
  onSearchClear: () => void;
  onLogout: () => void;
  onContextTransition: (pending: boolean) => void;
}) {
  const searchInput = useRef<InputRef>(null);
  const alternateButton = useRef<HTMLButtonElement>(null);
  const [showAlternate, setShowAlternate] = useState(false);
  const alternate = searchDestination === "/contracts" ? "/cases" : "/contracts";
  const alternateVisible = showAlternate && search.trim() !== "";
  const usesMeta = /Mac|iPhone|iPad/.test(navigator.platform);
  const shortcut = usesMeta ? "Meta+K" : "Control+K";
  useEffect(() => {
    if (!showCaseSearch) return;
    const focusSearch = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.isComposing ||
        event.shiftKey ||
        event.altKey ||
        (event.target instanceof Element && Boolean(event.target.closest('[role="dialog"]'))) ||
        !(event.ctrlKey || event.metaKey) ||
        event.key.toLowerCase() !== "k"
      )
        return;
      event.preventDefault();
      searchInput.current?.focus({ cursor: "all" });
    };
    window.addEventListener("keydown", focusSearch);
    return () => window.removeEventListener("keydown", focusSearch);
  }, [showCaseSearch]);

  return (
    <Layout.Header
      className={
        showCaseSearch ? "topbar client-topbar" : "topbar client-topbar topbar--without-search"
      }
    >
      <Link className="brand client-brand" to="/" aria-label="合同审计工作台首页">
        <span className="client-brand-mark" aria-hidden="true">
          <FileDoneOutlined />
        </span>
        合同审计工作台
      </Link>
      {showCaseSearch ? (
        <form
          className="global-search"
          role="search"
          onSubmit={(event) => {
            event.preventDefault();
            setShowAlternate(false);
            onSearchSubmit(searchDestination);
          }}
          onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget)) setShowAlternate(false);
          }}
          onKeyDown={(event) => {
            if (event.key === "Escape" && alternateVisible) {
              event.preventDefault();
              searchInput.current?.focus();
              setShowAlternate(false);
            }
            if (
              event.key === "ArrowDown" &&
              alternateVisible &&
              event.target === searchInput.current?.input
            ) {
              event.preventDefault();
              alternateButton.current?.focus();
            }
          }}
        >
          <label className="sr-only" htmlFor="global-case-search">
            全局搜索
          </label>
          <Input
            className="header-search-input"
            id="global-case-search"
            ref={searchInput}
            type="search"
            aria-keyshortcuts={shortcut}
            allowClear={{ clearIcon: <CloseCircleOutlined aria-label="清空搜索" /> }}
            onClear={onSearchClear}
            prefix={<SearchOutlined aria-hidden="true" />}
            suffix={
              <kbd className="client-search-shortcut" aria-hidden="true">
                {usesMeta ? "⌘K" : "Ctrl+K"}
              </kbd>
            }
            value={search}
            onFocus={() => setShowAlternate(true)}
            onChange={(event) => {
              onSearchChange(event.target.value);
              setShowAlternate(true);
            }}
            placeholder="搜索案件、合同或编号"
          />
          {alternateVisible ? (
            <div className="client-search-results">
              <button
                type="button"
                className="client-search-alternate"
                ref={alternateButton}
                onClick={() => {
                  setShowAlternate(false);
                  onSearchSubmit(alternate);
                }}
              >
                在{alternate === "/contracts" ? "合同台账" : "案件"}搜索
              </button>
            </div>
          ) : null}
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
  onStartReview,
  intakeAvailable,
}: {
  actor: Actor | undefined;
  pathname: string;
  adminRulePacksUrl: string;
  focusedReading: boolean;
  context?: ReactNode;
  onStartReview: (opener: HTMLElement, afterDrawerClosed?: (callback: () => void) => void) => void;
  intakeAvailable: boolean;
}) {
  const { enabled: professional } = useProfessionalMode();
  const canSeeManagementLinks = actor?.role === "admin";
  const selectedPath = navigationPath(pathname);
  const visibleLinks = (items: NavLinkItem[]): ShellNavLink[] =>
    items
      .filter(
        (item) =>
          (!item.adminRoleOnly || canSeeManagementLinks) &&
          (!item.professionalOnly || professional),
      )
      .map((item) => ({
        href: item.to === "/admin/rulepacks" ? adminRulePacksUrl : item.to,
        label: item.label,
        icon: NAV_ICONS[item.icon],
        external: item.external,
        compact: item.compact,
        current: !item.external && currentLink(selectedPath, item.to),
      }));
  const groups: ShellNavGroup[] = [
    { key: "primary", title: "工作台", links: visibleLinks(CORE_NAV) },
    {
      key: "knowledge",
      title: "知识与设置",
      disclosureIcon: <BookOutlined />,
      bottomStart: true,
      links: visibleLinks(KNOWLEDGE_NAV),
    },
  ];

  return (
    <ShellNavigation
      title="主导航"
      className="client-sidebar"
      groups={groups}
      compact={focusedReading}
      drawerFooter={context}
      action={(afterDrawerClosed) => (
        <Button
          className="client-intake-action"
          icon={<PlusOutlined className="client-intake-icon" aria-hidden="true" />}
          disabled={!intakeAvailable}
          aria-label="审一份合同"
          aria-haspopup="dialog"
          onClick={(event) => onStartReview(event.currentTarget, afterDrawerClosed)}
        >
          <span className="client-intake-label">审一份合同</span>
        </Button>
      )}
    />
  );
}

function ClientOutlet({ pathname }: { pathname: string }) {
  const { enabled } = useProfessionalMode();
  const selected = navigationPath(pathname);
  const requiresProfessionalMode = [...CORE_NAV, ...KNOWLEDGE_NAV].some(
    ({ professionalOnly, to }) => professionalOnly && currentLink(selected, to),
  );
  if (requiresProfessionalMode && !enabled) {
    return <PageStatus kind="empty" title="请在头像菜单中打开专业模式" />;
  }
  return <Outlet />;
}

export function AppShell() {
  return (
    <DirtyLeaveGuardProvider>
      <AppShellContent />
    </DirtyLeaveGuardProvider>
  );
}

function AppShellContent() {
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
  const [intakeScope, setIntakeScope] = useState("");
  const intakeOpener = useRef<{ scope: string; element: HTMLElement } | null>(null);
  const caseId = caseIdOf(location.pathname);
  const caseDetail = useCaseDetail(caseId);
  useEffect(() => {
    setSearch(
      location.pathname === "/cases" || location.pathname === "/contracts"
        ? searchFromLocation(location.search)
        : "",
    );
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
      showCaseSearch={!focusedReading}
      searchDestination={location.pathname === "/contracts" ? "/contracts" : "/cases"}
      logoutPending={logout.isPending}
      onSearchChange={setSearch}
      onSearchClear={() => {
        if (location.pathname === "/cases" || location.pathname === "/contracts") {
          navigateSearch(navigate, location, "", location.pathname);
        }
      }}
      onSearchSubmit={(destination) => navigateSearch(navigate, location, search, destination)}
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
            onStartReview={(opener, afterDrawerClosed) => {
              if (location.pathname === "/review") {
                const focusPrimary = () =>
                  document
                    .querySelector<HTMLButtonElement>("#main [data-intake-primary-picker]")
                    ?.focus();
                if (afterDrawerClosed) afterDrawerClosed(focusPrimary);
                else focusPrimary();
                return;
              }
              intakeOpener.current = { scope: returnScope, element: opener };
              setIntakeScope(returnScope);
            }}
            intakeAvailable={Boolean(returnScope)}
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
        {returnScope ? (
          <ReviewIntakeDialog
            key={returnScope}
            open={intakeScope === returnScope}
            onClose={() => {
              setIntakeScope((current) => (current === returnScope ? "" : current));
              const opener = intakeOpener.current;
              if (opener?.scope !== returnScope) return;
              intakeOpener.current = null;
              requestAnimationFrame(() => {
                if (opener.element.isConnected) opener.element.focus({ preventScroll: true });
              });
            }}
          />
        ) : null}
      </Layout>
    </ProfessionalModeProvider>
  );
}
