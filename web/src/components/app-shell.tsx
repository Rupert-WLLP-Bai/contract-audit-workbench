import {
  ApiOutlined,
  AuditOutlined,
  BookOutlined,
  BulbOutlined,
  CheckSquareOutlined,
  CloseOutlined,
  DashboardOutlined,
  FileDoneOutlined,
  FileSearchOutlined,
  FileTextOutlined,
  FolderOpenOutlined,
  HeartOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  MenuOutlined,
  MenuUnfoldOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  SettingOutlined,
  SwapOutlined,
  TeamOutlined,
  ToolOutlined,
} from "@ant-design/icons";
import type { MenuProps } from "antd";
import {
  Avatar,
  Breadcrumb,
  Button,
  Drawer,
  Input,
  Layout,
  Menu,
} from "antd";
import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react";
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router";
import { useCaseDetail, useLogout, useSession } from "../api/queries";
import { actorRoleLabel } from "../domain/review";
import "./app-shell.css";

type NavItem = {
  key: string;
  label: string;
  icon: ReactNode;
  to?: string;
  href?: string;
  end?: boolean;
};

const BUSINESS_NAV: NavItem[] = [
  {
    key: "/dashboard",
    label: "工作台",
    icon: <DashboardOutlined />,
    to: "/dashboard",
    end: true,
  },
  {
    key: "/cases",
    label: "案件",
    icon: <FolderOpenOutlined />,
    to: "/cases",
    end: true,
  },
  {
    key: "/review",
    label: "智能审查",
    icon: <FileSearchOutlined />,
    to: "/review",
    end: true,
  },
  {
    key: "/workbench/reports",
    label: "报告",
    icon: <FileDoneOutlined />,
    to: "/workbench/reports",
    end: true,
  },
  {
    key: "/workbench/knowledge",
    label: "知识库",
    icon: <BookOutlined />,
    to: "/workbench/knowledge",
    end: true,
  },
  {
    key: "/workbench/settings",
    label: "设置",
    icon: <SettingOutlined />,
    to: "/workbench/settings",
    end: true,
  },
];

const MORE_NAV: NavItem[] = [
  {
    key: "/compare",
    label: "版本对照",
    icon: <SwapOutlined />,
    to: "/compare",
    end: true,
  },
  {
    key: "/workbench/tasks",
    label: "审查任务",
    icon: <CheckSquareOutlined />,
    to: "/workbench/tasks",
    end: true,
  },
  {
    key: "/workbench/remediation",
    label: "整改中心",
    icon: <SafetyCertificateOutlined />,
    to: "/workbench/remediation",
    end: true,
  },
  {
    key: "/workbench/evidence",
    label: "证据中心",
    icon: <FileTextOutlined />,
    to: "/workbench/evidence",
    end: true,
  },
  {
    key: "/workbench/organization",
    label: "组织与权限",
    icon: <TeamOutlined />,
    to: "/workbench/organization",
    end: true,
  },
  {
    key: "/workbench/templates",
    label: "模板管理",
    icon: <FileTextOutlined />,
    to: "/workbench/templates",
    end: true,
  },
];

const OPS_NAV: NavItem[] = [
  {
    key: "/workbench/executions",
    label: "执行轨迹",
    icon: <AuditOutlined />,
    to: "/workbench/executions",
    end: true,
  },
  {
    key: "/workbench/models",
    label: "模型服务",
    icon: <BulbOutlined />,
    to: "/workbench/models",
    end: true,
  },
  {
    key: "/workbench/tools",
    label: "工具网关",
    icon: <ToolOutlined />,
    to: "/workbench/tools",
    end: true,
  },
  {
    key: "/workbench/health",
    label: "系统健康",
    icon: <HeartOutlined />,
    to: "/workbench/health",
    end: true,
  },
  {
    key: "/workbench/audit",
    label: "审计日志",
    icon: <AuditOutlined />,
    to: "/workbench/audit",
    end: true,
  },
  {
    key: "/api-docs",
    label: "API 文档",
    icon: <ApiOutlined />,
    to: "/api-docs",
    end: true,
  },
];

const MODULE_TITLES: Record<string, string> = {
  tasks: "审查任务",
  remediation: "整改中心",
  reports: "报告中心",
  evidence: "证据中心",
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

function navLabel(item: NavItem, onNavigate?: () => void) {
  if (item.href) {
    return (
      <a href={item.href} onClick={onNavigate}>
        {item.label}
      </a>
    );
  }
  return (
    <NavLink to={item.to ?? item.key} end={item.end} onClick={onNavigate}>
      {item.label}
    </NavLink>
  );
}

function selectedKey(pathname: string): string {
  if (pathname === "/" || pathname === "/dashboard" || pathname === "/home")
    return "/dashboard";
  if (/^\/cases\/[^/]+\/reviews\//.test(pathname)) return "/review";
  if (pathname === "/review") return "/review";
  if (pathname === "/cases" || pathname.startsWith("/cases/")) return "/cases";
  if (pathname === "/compare") return "/compare";
  if (pathname.startsWith("/workbench/")) return pathname;
  if (pathname === "/api-docs") return "/api-docs";
  return pathname;
}

function Navigation({
  collapsed,
  mobile,
  onNavigate,
  onClose,
  onToggle,
  adminRulePacksUrl,
}: {
  collapsed: boolean;
  mobile?: boolean;
  onNavigate?: () => void;
  onClose?: () => void;
  onToggle?: () => void;
  adminRulePacksUrl: string;
}) {
  const location = useLocation();
  const isCollapsed = mobile ? false : collapsed;
  const items = useMemo<MenuProps["items"]>(
    () => [
      {
        key: "business",
        type: "group",
        label: "工作区",
        children: BUSINESS_NAV.map((item) => ({
          key: item.key,
          icon: item.icon,
          label: navLabel(item, onNavigate),
        })),
      },
      {
        key: "more",
        type: "group",
        label: "更多工作区",
        children: MORE_NAV.map((item) => ({
          key: item.key,
          icon: item.icon,
          label: navLabel(item, onNavigate),
        })),
      },
      {
        key: "ops",
        type: "group",
        label: "开发者与运维",
        children: [
          {
            key: "/admin/rulepacks",
            icon: <SafetyCertificateOutlined />,
            label: (
              <a href={adminRulePacksUrl} onClick={onNavigate}>
                规则包治理
              </a>
            ),
          },
          ...OPS_NAV.map((item) => ({
            key: item.key,
            icon: item.icon,
            label: navLabel(item, onNavigate),
          })),
        ],
      },
    ],
    [adminRulePacksUrl, onNavigate],
  );

  return (
    <div className="shell-sider__inner">
      <Link
        className="shell-brand"
        to="/dashboard"
        aria-label="合同智能审计工作台首页"
        onClick={onNavigate}
      >
        <span className="shell-brand__mark" aria-hidden="true">
          <SafetyCertificateOutlined />
        </span>
        {isCollapsed ? null : (
          <span className="shell-brand__copy">
            <strong>合同智能审计</strong>
            <small>让审查更可靠</small>
          </span>
        )}
      </Link>
      <div className="shell-sider__menu">
        <Menu
          mode="inline"
          inlineCollapsed={isCollapsed}
          selectedKeys={[selectedKey(location.pathname)]}
          items={items}
          aria-label="主导航"
        />
      </div>
      <div className="shell-sider__footer">
        {mobile ? (
          <Button
            className="shell-collapse"
            type="text"
            icon={<CloseOutlined />}
            aria-label="关闭导航"
            onClick={onClose}
          >
            关闭导航
          </Button>
        ) : (
          <Button
            className="shell-collapse"
            type="text"
            aria-label={
              isCollapsed
                ? "展开侧栏"
                : "收合侧栏"
            }
            aria-expanded={!isCollapsed}
            icon={
              isCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />
            }
            onClick={onToggle}
          >
            {isCollapsed ? "展开" : "收合"}
          </Button>
        )}
      </div>
    </div>
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
  const [search, setSearch] = useState("");
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const [mobile, setMobile] = useState(false);
  const caseMatch = location.pathname.match(/^\/cases\/([^/]+)(?:\/|$)/);
  const caseId = caseMatch?.[1] ? decodeURIComponent(caseMatch[1]) : "";
  const caseDetail = useCaseDetail(caseId);
  const reviewMatch = location.pathname.match(
    /^\/cases\/([^/]+)\/reviews\/([^/]+)/,
  );

  useEffect(() => {
    setSearch(new URLSearchParams(location.search).get("q") ?? "");
  }, [location.pathname, location.search]);

  useEffect(() => {
    const query = window.matchMedia?.("(max-width: 991px)");
    const update = () => setMobile(query ? query.matches : window.innerWidth < 992);
    update();
    if (query?.addEventListener) {
      query.addEventListener("change", update);
      return () => query.removeEventListener("change", update);
    }
    window.addEventListener("resize", update);
    return () => window.removeEventListener("resize", update);
  }, []);

  useEffect(() => {
    if (!mobile) setMobileNavOpen(false);
  }, [mobile]);

  function searchCases(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const query = search.trim();
    const params = new URLSearchParams();
    if (query !== "") params.set("q", query);
    const suffix = params.toString();
    setMobileNavOpen(false);
    void navigate(`/cases${suffix === "" ? "" : `?${suffix}`}`);
  }

  const caseTitle = caseDetail.data?.title ?? caseId;
  const moduleKey = location.pathname.split("/")[2] ?? "";
  const breadcrumbs: { label: string; to?: string }[] =
    location.pathname === "/dashboard"
      ? [{ label: "工作台" }]
      : location.pathname === "/review"
        ? [{ label: "工作台", to: "/dashboard" }, { label: "智能审查" }]
        : location.pathname === "/compare"
          ? [{ label: "工作台", to: "/dashboard" }, { label: "版本对照" }]
          : location.pathname === "/cases"
            ? [{ label: "工作台", to: "/dashboard" }, { label: "案件" }]
            : caseId
              ? [
                  { label: "工作台", to: "/dashboard" },
                  { label: "案件", to: "/cases" },
                  {
                    label: caseTitle,
                    to: reviewMatch
                      ? `/cases/${encodeURIComponent(caseId)}`
                      : undefined,
                  },
                  ...(reviewMatch
                    ? [{ label: "审查分析" }]
                    : []),
                ]
              : location.pathname.startsWith("/workbench/")
                ? [
                    { label: "工作台", to: "/dashboard" },
                    { label: MODULE_TITLES[moduleKey] ?? "工作区" },
                  ]
                : [{ label: "工作台", to: "/dashboard" }, { label: "API 文档" }];

  return (
    <Layout className="shell">
      <a className="skip" href="#main">
        跳到主要内容
      </a>
      {mobile ? (
        <Drawer
          className="shell-drawer"
          open={mobileNavOpen}
          onClose={() => setMobileNavOpen(false)}
          placement="left"
          size={264}
          closable={false}
          title={null}
          styles={{ body: { padding: 0 } }}
        >
          <Navigation
            collapsed={false}
            mobile
            onNavigate={() => setMobileNavOpen(false)}
            onClose={() => setMobileNavOpen(false)}
            adminRulePacksUrl={adminRulePacksUrl}
          />
        </Drawer>
      ) : (
        <Layout.Sider
          theme="light"
          className="shell-sider"
          width={224}
          collapsedWidth={72}
          collapsed={collapsed}
          trigger={null}
        >
          <Navigation
            collapsed={collapsed}
            onToggle={() => setCollapsed((value) => !value)}
            adminRulePacksUrl={adminRulePacksUrl}
          />
        </Layout.Sider>
      )}
      <Layout>
        <Layout.Header className="shell-header">
          <Button
            className="shell-header__menu"
            type="text"
            icon={<MenuOutlined />}
            aria-label="打开导航"
            onClick={() => setMobileNavOpen(true)}
          />
          <form className="shell-search" role="search" onSubmit={searchCases}>
            <label className="sr-only" htmlFor="global-case-search">
              全局搜索案件
            </label>
            <Input
              id="global-case-search"
              type="search"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="搜索案件名称、编号…"
              allowClear
              prefix={<SearchOutlined aria-hidden="true" />}
            />
          </form>
          <span className="shell-header__spacer" />
          <div className="shell-account" aria-label="当前身份">
            <Avatar size={34}>
              {actor ? Array.from(actor.display_name)[0] : "?"}
            </Avatar>
            <span className="shell-account__copy">
              <strong>{actor?.display_name ?? "身份未知"}</strong>
              <small>
                {actor ? actorRoleLabel(actor.role) : "正在确认"}
              </small>
            </span>
            <Button
              type="text"
              icon={<LogoutOutlined />}
              aria-label="退出"
              title="退出"
              disabled={logout.isPending}
              onClick={() => {
                void logout.mutateAsync().finally(() => {
                  void navigate("/login", { replace: true });
                });
              }}
            />
          </div>
        </Layout.Header>
        <Layout.Content className="shell-content-outer">
          <div className="shell-content">
            {location.pathname === "/dashboard" ? null : (
              <Breadcrumb
                className="shell-breadcrumbs"
                items={breadcrumbs.map((crumb) => ({
                  title: crumb.to ? (
                    <Link to={crumb.to}>{crumb.label}</Link>
                  ) : (
                    crumb.label
                  ),
                }))}
              />
            )}
            <main id="main" className="shell-main" tabIndex={-1}>
              <Outlet />
            </main>
          </div>
        </Layout.Content>
      </Layout>
    </Layout>
  );
}
