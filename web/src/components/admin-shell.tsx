import { useLocation, useNavigate, NavLink, Outlet } from "react-router";
import { useLogout, useSession } from "../api/queries";
import { actorRoleLabel } from "../domain/review";
import { PageStatus } from "./page-status";
import { CircuitMark } from "./circuit-mark";
import { Avatar, Breadcrumb, Button, Layout, Menu } from "antd";
import {
  ApiOutlined,
  LogoutOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
} from "@ant-design/icons";

export function AdminShell() {
  const session = useSession("admin");
  const logout = useLogout("admin");
  const navigate = useNavigate();
  const location = useLocation();
  const actor = session.data;
  const rulepackRoute = location.pathname.startsWith("/admin/rulepacks");
  const breadcrumb = location.pathname.startsWith("/admin/infrastructure")
    ? "基础设施与中间件"
    : !rulepackRoute
      ? "设置与运行状态"
      : location.pathname.endsWith("/new")
        ? "创建规则包草稿"
        : location.pathname.includes("/versions/")
          ? "规则包详情"
          : "规则包治理";

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
      <Layout.Header className="topbar admin-topbar">
        <NavLink className="brand" to="/admin/settings" aria-label="管理工作台">
          <span className="brand-mark" aria-hidden="true">
            <CircuitMark />
          </span>
          <span className="brand-copy">
            <strong>合同智能审计工作台</strong>
          </span>
        </NavLink>
        <span className="organization-badge" title={actor?.org_id}>
          组织，{actor?.org_id ?? "正在确认"}
        </span>
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
            disabled={logout.isPending}
            onClick={() => {
              void logout.mutateAsync().finally(() => {
                void navigate("/admin/login", { replace: true });
              });
            }}
          >
            退出
          </Button>
        </div>
      </Layout.Header>
      <Layout className="frame" hasSider>
        <Layout.Sider
          theme="dark"
          className="sidenav admin-sidenav"
          aria-label="管理导航"
          width={248}
          collapsedWidth={72}
          collapsible
        >
          <Menu
            mode="inline"
            theme="dark"
            selectedKeys={[
              location.pathname.startsWith("/admin/infrastructure")
                ? "/admin/infrastructure"
                : rulepackRoute
                  ? "/admin/rulepacks"
                  : "/admin/settings",
            ]}
            items={[
              {
                key: "organization",
                type: "group",
                label: "组织管理",
                children: [
                  {
                    key: "/admin/settings",
                    icon: <SettingOutlined />,
                    label: (
                      <NavLink to="/admin/settings">设置与运行状态</NavLink>
                    ),
                  },
                  {
                    key: "/admin/rulepacks",
                    icon: <SafetyCertificateOutlined />,
                    label: <NavLink to="/admin/rulepacks">规则库</NavLink>,
                  },
                  {
                    key: "/admin/infrastructure",
                    icon: <ApiOutlined />,
                    label: (
                      <NavLink to="/admin/infrastructure">
                        基础设施与中间件
                      </NavLink>
                    ),
                  },
                ],
              },
            ]}
          />
          <div className="nav-footer">
            规则版本与发布历史
            <br />
            按当前管理身份授权
          </div>
        </Layout.Sider>
        <Layout.Content className="content-column">
          <Breadcrumb
            className="breadcrumbs"
            items={[{ title: "管理" }, { title: breadcrumb }]}
          />
          <main id="main" tabIndex={-1}>
            <Outlet />
          </main>
        </Layout.Content>
      </Layout>
    </Layout>
  );
}
