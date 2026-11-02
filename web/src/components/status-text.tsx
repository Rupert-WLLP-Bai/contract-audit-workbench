// File metadata (manifest mirror):
// Purpose: Renders StatusText, the status text workbench UI, from the selected case/workspace projection and the module’s query/state inputs; it displays server-derived state and does not establish business authorization or evidence validity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-25
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { ReactNode } from "react";
import type { StatusTone } from "../domain/review";
import { Tag } from "antd";

export function StatusText({
  tone,
  children,
}: {
  tone: StatusTone;
  children: ReactNode;
}) {
  const color =
    tone === "good"
      ? "success"
      : tone === "bad"
        ? "error"
        : tone === "attention"
          ? "warning"
          : tone === "progress"
            ? "processing"
            : "default";
  return <Tag color={color}>{children}</Tag>;
}
