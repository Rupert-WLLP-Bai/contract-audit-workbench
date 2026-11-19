// File metadata (manifest mirror):
// Purpose: Shares grouped navigation across client and admin shells with active routes, keyboard-expandable sections, a collapsible desktop sidebar and mobile drawer focus restoration.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-19
// Updated: 2026-11-19
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { MenuFoldOutlined, MenuOutlined, MenuUnfoldOutlined } from "@ant-design/icons";
import { Button, Drawer, Grid, Layout, Menu, type MenuProps } from "antd";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router";

export type ShellNavLink = {
  href: string;
  label: string;
  icon: ReactNode;
  current?: boolean;
  external?: boolean;
};

export type ShellNavGroup = {
  key: string;
  title: string;
  disclosureIcon?: ReactNode;
  links: ShellNavLink[];
};

function NavigationMenu({ title, groups }: { title: string; groups: ShellNavGroup[] }) {
  const navigate = useNavigate();
  const links = groups.flatMap((group) => group.links);
  const items: MenuProps["items"] = groups.map((group) => ({
    ...(group.disclosureIcon ? { icon: group.disclosureIcon } : { type: "group" as const }),
    key: group.key,
    label: group.title,
    children: group.links.map((link) => {
      const attributes = {
        "aria-label": link.label,
        "aria-current": link.current ? ("page" as const) : undefined,
        className: link.current ? "active" : undefined,
      };
      return {
        key: link.href,
        icon: link.icon,
        className: group.key === "primary" ? "shell-navigation-primary-item" : undefined,
        title: link.label,
        label: link.external ? (
          <a href={link.href} {...attributes}>
            {link.label}
          </a>
        ) : (
          <Link to={link.href} {...attributes}>
            {link.label}
          </Link>
        ),
      };
    }),
  }));

  return (
    <nav className="shell-navigation" aria-label={title}>
      <Menu
        mode="inline"
        items={items}
        triggerSubMenuAction="click"
        defaultOpenKeys={groups
          .filter((group) => group.disclosureIcon && group.links.some((link) => link.current))
          .map((group) => group.key)}
        selectedKeys={links.filter((link) => link.current).map((link) => link.href)}
        onClick={({ key, domEvent }) => {
          // Anchors keep native browser gestures and React Router blockers. The
          // Menu itself can also activate a row by icon or keyboard.
          if (domEvent.target instanceof Element && domEvent.target.closest("a")) return;
          const link = links.find((item) => item.href === key);
          if (!link) return;
          if (link.external) window.location.assign(link.href);
          else void navigate(link.href);
        }}
      />
    </nav>
  );
}

export function ShellNavigation({
  title,
  groups,
  footer,
  drawerOnly = false,
  drawerContext,
  className = "",
}: {
  title: string;
  groups: ShellNavGroup[];
  footer?: ReactNode;
  drawerOnly?: boolean;
  drawerContext?: ReactNode;
  className?: string;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [openedAt, setOpenedAt] = useState<string | null>(null);
  const { key } = useLocation();
  const screens = Grid.useBreakpoint();
  const drawerId = useId();
  const opener = useRef<HTMLButtonElement | HTMLAnchorElement>(null);
  const restoreFocus = useRef(false);
  const drawerOpen = openedAt === key && (drawerOnly || !screens.lg);
  if (openedAt !== null && !drawerOpen) setOpenedAt(null);

  useEffect(() => {
    if (drawerOpen || !restoreFocus.current) return;
    restoreFocus.current = false;
    opener.current?.focus({ preventScroll: true });
  }, [drawerOpen]);

  return (
    <>
      {!drawerOnly ? (
        <Layout.Sider
          className={`sidenav shell-sidenav ${className}`}
          theme="light"
          width={248}
          collapsedWidth={72}
          collapsed={collapsed}
          collapsible
          onCollapse={setCollapsed}
          trigger={
            <Button
              type="text"
              className="nav-collapse-action"
              aria-label={collapsed ? "展开侧栏" : "收合侧栏"}
              aria-expanded={!collapsed}
              icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
            />
          }
        >
          <NavigationMenu title={title} groups={groups} />
          {collapsed ? null : footer}
        </Layout.Sider>
      ) : null}
      <div
        className={
          drawerOnly
            ? "shell-mobile-navigation shell-mobile-navigation--always"
            : "shell-mobile-navigation"
        }
      >
        <Button
          ref={opener}
          type="text"
          icon={<MenuOutlined />}
          aria-label={`打开${title}`}
          aria-haspopup="dialog"
          aria-expanded={drawerOpen}
          aria-controls={drawerOpen ? drawerId : undefined}
          onClick={() => {
            restoreFocus.current = false;
            setOpenedAt(key);
          }}
        >
          {title}
        </Button>
        {drawerContext}
      </div>
      <Drawer
        id={drawerId}
        title={title}
        open={drawerOpen}
        onClose={() => {
          restoreFocus.current = true;
          setOpenedAt(null);
        }}
        focusable={{ focusTriggerAfterClose: false }}
        placement="left"
        size="min(344px, 100vw)"
        destroyOnHidden
        classNames={{ body: "shell-navigation-body" }}
      >
        <NavigationMenu title={title} groups={groups} />
      </Drawer>
    </>
  );
}
