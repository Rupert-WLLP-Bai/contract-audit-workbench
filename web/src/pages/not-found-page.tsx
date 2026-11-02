// File metadata (manifest mirror):
// Purpose: Renders the application’s unmatched-route state and navigation recovery link without querying or mutating case data.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-25
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { Link } from "react-router";

export function NotFoundPage() {
  return (
    <section className="page">
      <h1>没有这个页面</h1>
      <p>可从工作台进入案件、审查工作区、设置或 API 文档。</p>
      <Link to="/cases">返回案件</Link>
    </section>
  );
}
