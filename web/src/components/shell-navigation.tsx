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
  links: ShellNavLink[];
};

function NavigationMenu({ title, groups }: { title: string; groups: ShellNavGroup[] }) {
  const navigate = useNavigate();
  const links = groups.flatMap((group) => group.links);
  const items: MenuProps["items"] = groups.map((group) => ({
    type: "group",
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
  drawerGroups = groups,
  footer,
  className = "",
}: {
  title: string;
  groups: ShellNavGroup[];
  drawerGroups?: ShellNavGroup[];
  footer?: ReactNode;
  className?: string;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [openedAt, setOpenedAt] = useState<string | null>(null);
  const { key } = useLocation();
  const screens = Grid.useBreakpoint();
  const drawerId = useId();
  const opener = useRef<HTMLButtonElement | HTMLAnchorElement>(null);
  const restoreFocus = useRef(false);
  const drawerOpen = openedAt === key && !screens.lg;
  if (openedAt !== null && !drawerOpen) setOpenedAt(null);

  useEffect(() => {
    if (drawerOpen || !restoreFocus.current) return;
    restoreFocus.current = false;
    opener.current?.focus({ preventScroll: true });
  }, [drawerOpen]);

  return (
    <>
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
      <div className="shell-mobile-navigation">
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
        <NavigationMenu title={title} groups={drawerGroups} />
      </Drawer>
    </>
  );
}
