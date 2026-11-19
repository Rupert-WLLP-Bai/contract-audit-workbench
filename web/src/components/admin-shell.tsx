// File metadata (manifest mirror):
// Purpose: Renders the administration shell with its own session audience, scope and status, and passes its routes to the shared responsive navigation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-19
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import { useLocation, useNavigate, NavLink, Outlet } from "react-router";
import { useState } from "react";
import { useLogout, useSession } from "../api/queries";
import { actorRoleLabel } from "../domain/review";
import { PageStatus } from "./page-status";
import { CircuitMark } from "./circuit-mark";
import { Avatar, Breadcrumb, Button, Layout } from "antd";
import {
  ApiOutlined,
  BookOutlined,
  LogoutOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  TeamOutlined,
} from "@ant-design/icons";
import type { Actor } from "../api/types";
import { ContextSwitcher } from "./context-switcher";
import { ShellNavigation, type ShellNavGroup } from "./shell-navigation";

type VersionedRouteTitles = {
  create: string;
  version: string;
  list: string;
};

function versionedRouteTitle(pathname: string, titles: VersionedRouteTitles): string {
  if (pathname.endsWith("/new")) return titles.create;
  if (pathname.includes("/versions/")) return titles.version;
  return titles.list;
}

function adminBreadcrumb(pathname: string): string {
  if (pathname.startsWith("/admin/tenant-lifecycle")) return "租户生命周期";
  if (pathname.startsWith("/admin/jurisdiction-operations")) return "辖区运营统计";
  if (pathname.startsWith("/admin/infrastructure")) return "基础设施与中间件";
  if (pathname.startsWith("/admin/governed-knowledge"))
    return versionedRouteTitle(pathname, {
      create: "创建知识版本",
      version: "知识版本详情",
      list: "知识治理",
    });
  if (!pathname.startsWith("/admin/rulepacks")) return "设置与运行状态";
  return versionedRouteTitle(pathname, {
    create: "创建规则包草稿",
    version: "规则包详情",
    list: "规则包治理",
  });
}

function adminMenuKey(pathname: string): string {
  if (pathname.startsWith("/admin/tenant-lifecycle")) return "/admin/tenant-lifecycle";
  if (pathname.startsWith("/admin/jurisdiction-operations"))
    return "/admin/jurisdiction-operations";
  if (pathname.startsWith("/admin/infrastructure")) return "/admin/infrastructure";
  if (pathname.startsWith("/admin/governed-knowledge")) return "/admin/governed-knowledge";
  if (pathname.startsWith("/admin/rulepacks")) return "/admin/rulepacks";
  return "/admin/settings";
}

const ADMIN_MENU_GROUPS: ShellNavGroup[] = [
  {
    key: "organization",
    title: "组织管理",
    links: [
      {
        href: "/admin/settings",
        icon: <SettingOutlined />,
        label: "设置与运行状态",
      },
      {
        href: "/admin/rulepacks",
        icon: <SafetyCertificateOutlined />,
        label: "规则库",
      },
      {
        href: "/admin/governed-knowledge",
        icon: <BookOutlined />,
        label: "知识治理",
      },
      {
        href: "/admin/infrastructure",
        icon: <ApiOutlined />,
        label: "基础设施与中间件",
      },
    ],
  },
  {
    key: "tenant-operations",
    title: "租户运营",
    links: [
      {
        href: "/admin/tenant-lifecycle",
        icon: <TeamOutlined />,
        label: "租户生命周期",
      },
      {
        href: "/admin/jurisdiction-operations",
        icon: <ApiOutlined />,
        label: "辖区运营统计",
      },
    ],
  },
];

function AdminTopbar({
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
  return (
    <Layout.Header className="topbar admin-topbar">
      <NavLink className="brand" to="/admin/settings" aria-label="管理工作台">
        <span className="brand-mark" aria-hidden="true">
          <CircuitMark />
        </span>
        <span className="brand-copy">
          <strong>合同智能审计工作台</strong>
        </span>
      </NavLink>
      <ContextSwitcher actor={actor} audience="admin" onTransition={onContextTransition} />
      <p className="disclaimer">此工作区不授予案件正文访问权限。</p>
      <div className="who" aria-label="当前管理身份">
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
          disabled={logoutPending}
          onClick={onLogout}
        >
          退出
        </Button>
      </div>
    </Layout.Header>
  );
}

function AdminSidebar({ pathname }: { pathname: string }) {
  const selected = adminMenuKey(pathname);
  return (
    <ShellNavigation
      title="管理导航"
      className="admin-sidenav"
      groups={ADMIN_MENU_GROUPS.map((group) => ({
        ...group,
        links: group.links.map((link) => ({ ...link, current: link.href === selected })),
      }))}
      footer={<div className="nav-footer">规则版本与发布历史</div>}
    />
  );
}

export function AdminShell() {
  const session = useSession("admin");
  const logout = useLogout("admin");
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const actor = session.data;
  const [contextTransition, setContextTransition] = useState(false);

  if (actor && actor.role !== "admin") {
    return (
      <PageStatus
        kind="denied"
        title="需要管理权限"
        detail="当前管理会话没有管理角色。案件访问权限由业务工作台单独授权。"
      />
    );
  }

  return (
    <Layout className="app">
      <a className="skip" href="#main">
        跳到主要内容
      </a>
      <AdminTopbar
        actor={actor}
        logoutPending={logout.isPending}
        onLogout={() => {
          void logout.mutateAsync().finally(() => {
            void navigate("/admin/login", { replace: true });
          });
        }}
        onContextTransition={setContextTransition}
      />
      <Layout className="frame" hasSider>
        <AdminSidebar pathname={pathname} />
        <Layout.Content className="content-column">
          <Breadcrumb
            className="breadcrumbs"
            items={[{ title: "管理" }, { title: adminBreadcrumb(pathname) }]}
          />
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
