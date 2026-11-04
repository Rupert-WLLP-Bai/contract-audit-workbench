// File metadata (manifest mirror):
// Purpose: Shares readable case selector presentation with wrapping secondary identity and a compact selected label while option values and search identity remain unchanged.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { ReactNode } from "react";

// Full labels remain searchable; wrapped options expose every character of the
// secondary machine identity, even when the closed control is narrow.
export const caseSelectPresentation = {
  virtual: false,
  styles: { popup: { listItem: { whiteSpace: "normal" as const } } },
  optionRender: (option: { label?: ReactNode }) =>
    <div style={{ whiteSpace: "normal", overflowWrap: "anywhere" }}>{option.label}</div>,
  labelRender: ({ label }: { label: ReactNode }) =>
    typeof label === "string" ? label.split(" · ").slice(0, 2).join(" · ") : label,
};
