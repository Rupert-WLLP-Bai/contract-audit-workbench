// File metadata (manifest mirror):
// Purpose: Renders dependency-light route failure recovery with reload and audience-home actions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import "./application-error.css";

// Keep this eagerly loaded with the router and free of Ant Design and hooks:
// the failing route may be using a stale dependency graph.
export function ApplicationError() {
  return (
    <main className="application-error" role="alert">
      <h1>页面暂时无法显示</h1>
      <p>加载页面时遇到了问题。你可以重新加载当前页面，或返回首页继续查看。</p>
      <div className="application-error-actions">
        <button type="button" onClick={() => window.location.reload()}>
          重新加载当前页面
        </button>
        <a href={import.meta.env.BASE_URL}>返回首页</a>
      </div>
    </main>
  );
}
