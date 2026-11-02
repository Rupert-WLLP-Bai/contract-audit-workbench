// File metadata (manifest mirror):
// Purpose: Renders CompetitionIdentity, the competition identity workbench UI, from the selected case/workspace projection and the module’s query/state inputs; it displays server-derived state and does not establish business authorization or evidence validity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-10-28
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

export function CompetitionIdentity() {
  return (
    <>
      <img
        className="competition-logo"
        src={`${import.meta.env.BASE_URL}huangjueshu-competition-logo.png?v=20261028`}
        alt=""
        width={38}
        height={38}
      />
      <span className="brand-copy">
        <strong>第一届黄桷树AI智能体开发大赛</strong>
        <span>合同智能审计工作台</span>
      </span>
    </>
  );
}
