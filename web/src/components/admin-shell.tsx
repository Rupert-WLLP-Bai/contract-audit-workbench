import { useLocation, useNavigate, NavLink, Outlet } from "react-router";
import { useLogout, useSession } from "../api/queries";
import { actorRoleLabel } from "../domain/review";
import { PageStatus } from "./page-status";

export function AdminShell() {
  const session = useSession("admin");
  const logout = useLogout("admin");
  const navigate = useNavigate();
  const location = useLocation();
  const actor = session.data;
  const rulepackRoute = location.pathname.startsWith("/admin/rulepacks");
  const breadcrumb = !rulepackRoute ? "设置与运行状态"
    : location.pathname.endsWith("/new") ? "创建规则包草稿"
      : location.pathname.includes("/versions/") ? "规则包详情"
        : "规则包治理";

  if (actor && actor.role !== "admin") {
    return <PageStatus kind="denied" title="需要管理权限" detail="当前管理会话没有管理角色。案件访问权限由业务工作台单独授权。" />;
  }

  return (
    <div className="app">
      <a className="skip" href="#main">跳到主要内容</a>
      <header className="topbar admin-topbar">
        <NavLink className="brand" to="/admin/settings" aria-label="管理工作台">
          <span className="brand-mark" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="m12 2 9 5v10l-9 5-9-5V7z" /><path d="m12 6 5 3v5c0 3-5 5-5 5s-5-2-5-5V9zM9 12l2 2 4-4" /></svg></span>
          <span className="brand-copy"><strong>合同智能审计工作台</strong></span>
        </NavLink>
        <span className="organization-badge" title={actor?.org_id}>组织 · {actor?.org_id ?? "正在确认"}</span>
        <p className="disclaimer">此工作区不授予案件正文访问权限。</p>
        <div className="who" aria-label="当前管理身份">
          {actor ? <><span className="identity-avatar" aria-hidden="true">{Array.from(actor.display_name)[0]}</span><span className="identity-copy"><strong>{actor.display_name}</strong><span className="meta">{actorRoleLabel(actor.role)}</span></span></> : <span>身份未知</span>}
          <button className="button button-quiet" type="button" disabled={logout.isPending} onClick={() => {
            void logout.mutateAsync().finally(() => { void navigate("/admin/login", { replace: true }); });
          }}>退出</button>
        </div>
      </header>
      <div className="frame">
        <nav className="sidenav admin-sidenav" aria-label="管理导航">
          <p className="nav-section-label">组织管理</p>
          <NavLink to="/admin/settings" className="nav-item" aria-label="设置"><span className="nav-glyph" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="m10 3-1 3-3 1-2 3 2 2v3l2 3h3l2 3 3-2 1-3 3-1v-4l-3-1-1-3z" /><circle cx="12" cy="12" r="3" /></svg></span><span className="nav-label">设置与运行状态</span></NavLink>
          <NavLink to="/admin/rulepacks" className="nav-item" aria-label="规则包治理"><span className="nav-glyph" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="m12 3 9 5-9 5-9-5zM3 12l9 5 9-5M3 16l9 5 9-5" /></svg></span><span className="nav-label">规则库</span></NavLink>
          <div className="nav-footer">规则版本与发布历史<br />按当前管理身份授权</div>
        </nav>
        <div className="content-column">
          <nav className="breadcrumbs" aria-label="面包屑"><span>管理</span><span className="breadcrumb-separator" aria-hidden="true">/</span><span aria-current="page">{breadcrumb}</span></nav>
          <main id="main" tabIndex={-1}><Outlet /></main>
        </div>
      </div>
    </div>
  );
}
