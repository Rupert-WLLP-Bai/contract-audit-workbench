import { MenuFoldOutlined, MenuUnfoldOutlined } from "@ant-design/icons";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, NavLink, Outlet, useLocation, useNavigate } from "react-router";
import { useCaseDetail, useLogout, useSession } from "../api/queries";
import { actorRoleLabel } from "../domain/review";
import { Avatar, Breadcrumb, Button, Input, Layout, Menu } from "antd";
import {
  ApiOutlined,
  AuditOutlined,
  BookOutlined,
  BulbOutlined,
  CheckSquareOutlined,
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
import { CircuitMark } from "./circuit-mark";
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

const NAV_ICONS: Record<string, ReactNode> = {
  home: <HomeOutlined />,
  review: <FileSearchOutlined />,
  compare: <FileTextOutlined />,
  rules: <SafetyCertificateOutlined />,
  case: <FileTextOutlined />,
  tasks: <CheckSquareOutlined />,
  remediation: <SafetyCertificateOutlined />,
  reports: <FileTextOutlined />,
  evidence: <FileSearchOutlined />,
  executions: <AuditOutlined />,
  knowledge: <BookOutlined />,
  organization: <TeamOutlined />,
  templates: <FileTextOutlined />,
  models: <BulbOutlined />,
  tools: <ToolOutlined />,
  health: <CheckSquareOutlined />,
  audit: <AuditOutlined />,
  settings: <SettingOutlined />,
  api: <ApiOutlined />,
};

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
  const caseMatch = location.pathname.match(/^\/cases\/([^/]+)/);
  const caseId = caseMatch?.[1] ? decodeURIComponent(caseMatch[1]) : "";
  const caseDetail = useCaseDetail(caseId);
  useEffect(() => {
    setSearch(new URLSearchParams(location.search).get("q") ?? "");
  }, [location.pathname, location.search]);
  const reviewMatch = location.pathname.match(
    /^\/cases\/([^/]+)\/reviews\/([^/]+)/,
  );

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
  }

  const caseTitle = caseDetail.data?.title ?? caseId;
  const moduleTitles: Record<string, string> = {
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
  const moduleKey = location.pathname.split("/")[2] ?? "";
  const breadcrumbs =
    location.pathname === "/"
      ? [{ label: "工作台", to: null }]
      : location.pathname === "/review"
        ? [
            { label: "工作台", to: "/" },
            { label: "合同审查", to: null },
          ]
        : location.pathname === "/compare"
          ? [
              { label: "工作台", to: "/" },
              { label: "版本对照", to: null },
            ]
          : location.pathname === "/cases"
            ? [
                { label: "工作台", to: "/" },
                { label: "案件管理", to: null },
              ]
            : caseId
              ? [
                  { label: "工作台", to: "/" },
                  { label: "案件", to: "/cases" },
                  {
                    label: caseTitle,
                    to: reviewMatch
                      ? `/cases/${encodeURIComponent(caseId)}`
                      : null,
                  },
                  ...(reviewMatch
                    ? [
                        {
                          label: `审查，${decodeURIComponent(reviewMatch[2] ?? "")}`,
                          to: null,
                        },
                      ]
                    : []),
                ]
              : location.pathname.startsWith("/workbench/")
                ? [
                    { label: "工作台", to: "/" },
                    { label: "业务管理", to: null },
                    { label: moduleTitles[moduleKey] ?? "工作区", to: null },
                  ]
                : [
                    { label: "开发者", to: null },
                    { label: "API 接入", to: null },
                  ];

  return (
    <Layout
      className={`app${collapsed ? " app--nav-collapsed" : ""}${reviewMatch ? " app--review" : ""}${location.pathname === "/compare" ? " app--compare" : ""}${location.pathname === "/api-docs" ? " app--api-docs" : ""}`}
    >
      <a className="skip" href="#main">
        跳到主要内容
      </a>
      <Layout.Header className="topbar">
        <Link className="brand" to="/" aria-label="合同审查工作台首页">
          <span className="brand-mark" aria-hidden="true">
            <CircuitMark />
          </span>
          <span className="brand-copy">
            <strong>合同智能审计工作台</strong>
          </span>
        </Link>
        <span className="organization-badge" title={actor?.org_id}>
          组织，{actor?.org_id ?? "正在确认"}
        </span>
        <form className="global-search" role="search" onSubmit={searchCases}>
          <label className="sr-only" htmlFor="global-case-search">
            全局搜索案件
          </label>
          <Input
            className="header-search-input"
            id="global-case-search"
            type="search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="搜索案件标题或编号"
          />
          <Button className="header-action" aria-label={"搜索"} htmlType="submit" ghost>
            搜索
          </Button>
        </form>
        <div className="who" aria-label="当前身份">
          {actor ? (
            <>
              <Avatar className="header-avatar">{Array.from(actor.display_name)[0]}</Avatar>
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
            disabled={logout.isPending}
            onClick={() => {
              void logout.mutateAsync().finally(() => {
                void navigate("/login", { replace: true });
              });
            }}
          >
            退出
          </Button>
        </div>
      </Layout.Header>
      <Layout className="frame" hasSider>
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
          onCollapse={setCollapsed}
        >
          <Menu
            mode="inline"
            theme="dark"
            selectedKeys={[
              location.pathname === "/"
                ? "/"
                : reviewMatch
                  ? "/review"
                  : (AUXILIARY_LINKS.find(
                      (item) =>
                        location.pathname === item.to ||
                        location.pathname.startsWith(`${item.to}/`),
                    )?.to ?? location.pathname),
            ]}
            items={[
              {
                key: "main",
                type: "group",
                label: "主要工作区",
                children: [
                  ...MAIN_LINKS.map((item) => ({
                    key: item.to,
                    icon: NAV_ICONS[item.icon],
                    label: (
                      <NavLink to={item.to} end={item.end}>
                        {item.label}
                      </NavLink>
                    ),
                  })),
                  {
                    key: "rules",
                    icon: NAV_ICONS.rules,
                    label: <a href={adminRulePacksUrl}>规则包治理</a>,
                  },
                ],
              },
              {
                key: "business",
                type: "group",
                label: "业务管理",
                children: AUXILIARY_LINKS.map((item) => ({
                  key: item.to,
                  icon: NAV_ICONS[item.icon],
                  label: (
                    <NavLink to={item.to} end={item.end}>
                      {item.label}
                    </NavLink>
                  ),
                })),
              },
              {
                key: "developer",
                type: "group",
                label: "开发者资源",
                children: [
                  {
                    key: "/api-docs",
                    icon: NAV_ICONS.api,
                    label: <NavLink to="/api-docs">API 文档</NavLink>,
                  },
                ],
              },
            ]}
          />
        </Layout.Sider>
        <Layout.Content className="content-column">
          <Breadcrumb
            className="breadcrumbs"
            items={breadcrumbs.map((crumb) => ({
              title: crumb.to ? (
                <Link to={crumb.to}>{crumb.label}</Link>
              ) : (
                crumb.label
              ),
            }))}
          />
          <main id="main" tabIndex={-1}>
            <Outlet />
          </main>
        </Layout.Content>
      </Layout>
    </Layout>
  );
}
