// File metadata (manifest mirror):
// Purpose: Shows the exact playbook reference frozen with a review or approval and explains why this view does not expose source content.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { Alert } from "antd";
import type { FrozenPlaybookAdoption } from "../api/playbooks";
import { TechnicalDetail } from "./technical-detail";

const unavailableReason = (reason: string | undefined): string => {
  if (reason === "source_authorization_required")
    return "此处保留本次采用的精确版本；正文需通过有权的来源入口核对。";
  return "此处保留本次采用的精确版本；请通过有权的来源入口核对正文。";
};

export function FrozenPlaybookPanel({
  adoption,
  heading = "本次冻结的审查手册",
}: {
  adoption: FrozenPlaybookAdoption | null | undefined;
  heading?: string;
}) {
  if (!adoption) return null;

  return (
    <section className="workflow-panel" aria-label={heading}>
      <h3>{heading}</h3>
      <p>本轮已冻结采用一个精确版本的审查手册。该引用不可由后续发布版本替换。</p>
      <Alert
        type="info"
        showIcon
        title="手册正文需要来源授权后读取"
        description={unavailableReason(adoption.content_unavailable_reason)}
      />
      <TechnicalDetail label="冻结手册精确身份" value={JSON.stringify(adoption.ref)} />
    </section>
  );
}
