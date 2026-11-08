// File metadata (manifest mirror):
// Purpose: Renders dedicated client and admin unmatched-route states with explicit recovery links, keeping missing routes distinct from authorization or loading failures.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-08
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { Link } from "react-router";
import "./not-found-page.css";

function NotFoundView({
  title,
  primary,
  primaryLabel,
  secondary,
  secondaryLabel,
}: {
  title: string;
  primary: string;
  primaryLabel: string;
  secondary: string;
  secondaryLabel: string;
}) {
  return (
    <section className="page page-not-found">
      <header className="page-head">
        <div>
          <h1>{title}</h1>
          <p>这个入口暂时不可用</p>
        </div>
        <Link className="not-found-primary" to={primary}>{primaryLabel}</Link>
      </header>
      <div className="not-found-card">
        <span className="not-found-mark" aria-hidden="true">i</span>
        <h2>这个入口暂时不可用</h2>
        <p>保留返回入口；不要把无权限、未找到和加载失败混为同一种状态。</p>
        <div className="not-found-actions">
          <Link className="not-found-primary" to={primary}>{primaryLabel}</Link>
          <Link className="not-found-secondary" to={secondary}>{secondaryLabel}</Link>
        </div>
      </div>
    </section>
  );
}

export function NotFoundPage() {
  return <NotFoundView title="页面不存在" primary="/" primaryLabel="返回工作台" secondary="/cases" secondaryLabel="打开案件中心" />;
}

export function AdminNotFoundPage() {
  return <NotFoundView title="管理页面不存在" primary="/admin/settings" primaryLabel="返回管理工作台" secondary="/admin/rulepacks" secondaryLabel="规则包治理" />;
}
