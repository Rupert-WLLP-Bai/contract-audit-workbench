// File metadata (manifest mirror):
// Purpose: Shares grouped client and admin navigation and keyboard focus restoration across a collapsible sidebar and the mobile or focused reading drawer.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-19
// Updated: 2026-11-20
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

import { MenuFoldOutlined, MenuOutlined, MenuUnfoldOutlined } from "@ant-design/icons";
import { Button, Drawer, Grid, Layout } from "antd";
import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { Link, useLocation } from "react-router";
import "./shell-navigation.css";

export type ShellNavLink = {
  href: string;
  label: string;
  icon: ReactNode;
  current?: boolean;
  external?: boolean;
  compact?: boolean;
};

export type ShellNavGroup = {
  key: string;
  title: string;
  disclosureIcon?: ReactNode;
  bottomStart?: boolean;
  links: ShellNavLink[];
};

function NavigationLink({ link, primary = false }: { link: ShellNavLink; primary?: boolean }) {
  const className = [
    "shell-nav-control",
    primary ? "shell-navigation-primary-item" : "",
    link.current ? "active" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const content = (
    <>
      <span className="shell-nav-icon" aria-hidden="true">
        {link.icon}
      </span>
      <span className="shell-nav-label">{link.label}</span>
    </>
  );
  const attributes = { className, "aria-current": link.current ? ("page" as const) : undefined };
  return link.external ? (
    <a href={link.href} {...attributes}>
      {content}
    </a>
  ) : (
    <Link to={link.href} {...attributes}>
      {content}
    </Link>
  );
}

function focusNavigationControl(event: KeyboardEvent<HTMLElement>) {
  const { key, altKey, ctrlKey, metaKey, shiftKey } = event;
  if (
    altKey ||
    ctrlKey ||
    metaKey ||
    shiftKey ||
    !["ArrowDown", "ArrowUp", "Home", "End"].includes(key)
  )
    return;
  const controls = Array.from(
    event.currentTarget.querySelectorAll<HTMLElement>(".shell-nav-control"),
  ).filter((control) => !control.closest("[hidden]"));
  const index = controls.findIndex((control) => control === event.target);
  if (index === -1) return;
  const last = controls.length - 1;
  const next =
    key === "Home"
      ? 0
      : key === "End"
        ? last
        : key === "ArrowDown"
          ? (index + 1) % controls.length
          : (index + last) % controls.length;
  event.preventDefault();
  controls[next]?.focus();
}

function NavigationMenu({
  title,
  groups,
  collapsed = false,
}: {
  title: string;
  groups: ShellNavGroup[];
  collapsed?: boolean;
}) {
  const id = useId();
  const [visibility, setVisibility] = useState<Record<string, boolean>>({});
  return (
    <nav
      className={`shell-navigation${collapsed ? " shell-nav--collapsed" : ""}`}
      aria-label={title}
      onKeyDown={focusNavigationControl}
    >
      <ul className="shell-nav-groups">
        {groups.map((group) => {
          const expanded = visibility[group.key] ?? group.links.some((link) => link.current);
          const groupId = `${id}-${group.key}`;
          return (
            <li
              key={group.key}
              className={group.bottomStart ? "shell-navigation-bottom-start" : undefined}
            >
              {group.disclosureIcon ? (
                <button
                  type="button"
                  className="shell-nav-control shell-nav-disclosure"
                  aria-expanded={expanded}
                  aria-controls={groupId}
                  onClick={() =>
                    setVisibility((current) => ({ ...current, [group.key]: !expanded }))
                  }
                >
                  <span className="shell-nav-icon" aria-hidden="true">
                    {group.disclosureIcon}
                  </span>
                  <span className="shell-nav-label">{group.title}</span>
                  <span className="shell-nav-chevron" aria-hidden="true">
                    ›
                  </span>
                </button>
              ) : group.key === "primary" ? null : (
                <div className="shell-nav-group-title">{group.title}</div>
              )}
              <ul
                className="shell-nav-links"
                id={groupId}
                hidden={Boolean(group.disclosureIcon) && !expanded}
              >
                {group.links.map((link) => (
                  <li key={link.href}>
                    <NavigationLink link={link} primary={group.key === "primary"} />
                  </li>
                ))}
              </ul>
            </li>
          );
        })}
      </ul>
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
  compact = false,
  drawerFooter,
}: {
  title: string;
  groups: ShellNavGroup[];
  footer?: ReactNode;
  drawerOnly?: boolean;
  drawerContext?: ReactNode;
  className?: string;
  compact?: boolean;
  drawerFooter?: ReactNode;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [openedAt, setOpenedAt] = useState<string | null>(null);
  const { key } = useLocation();
  const screens = Grid.useBreakpoint();
  const drawerId = useId();
  const opener = useRef<HTMLButtonElement | HTMLAnchorElement>(null);
  const restoreFocus = useRef(false);
  const drawerOpen = openedAt === key && (drawerOnly || compact || !screens.lg);
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
          inert={drawerOpen}
          aria-hidden={drawerOpen || undefined}
          theme="light"
          width="var(--sidebar-expanded-width)"
          collapsedWidth="var(--sidebar-collapsed-width)"
          collapsed={compact || collapsed}
          collapsible={!compact}
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
          {compact && (
            <Button
              ref={opener}
              type="primary"
              className="review-rail-opener"
              icon={<MenuOutlined />}
              aria-label={`打开${title}`}
              aria-expanded={drawerOpen}
              aria-haspopup="dialog"
              onClick={() => {
                restoreFocus.current = false;
                setOpenedAt(key);
              }}
            />
          )}
          {compact ? (
            <nav
              className="review-rail-navigation shell-nav--collapsed"
              aria-label={title}
              onKeyDown={focusNavigationControl}
            >
              {groups
                .flatMap((group) =>
                  group.links.filter((link) => group.key === "primary" || link.compact),
                )
                .map((link) => (
                  <NavigationLink key={link.href} link={link} />
                ))}
            </nav>
          ) : (
            <NavigationMenu title={title} groups={groups} collapsed={collapsed} />
          )}
          {compact || collapsed ? null : footer}
        </Layout.Sider>
      ) : null}
      {!compact && (
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
      )}
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
        footer={drawerFooter}
      >
        <NavigationMenu title={title} groups={groups} />
      </Drawer>
    </>
  );
}
