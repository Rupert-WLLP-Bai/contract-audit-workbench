// File metadata (manifest mirror):
// Purpose: Preserves malformed display timestamps verbatim in copyable technical details while the business view distinguishes invalid times from missing values.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { isInvalidTime } from "../domain/time";
import { TechnicalDetail } from "./technical-detail";

// Keep malformed server timestamps available verbatim without presenting them as dates.
export function InvalidTimeDetail({ value, label }: { value: string | null | undefined; label: string }) {
  return isInvalidTime(value) ? <TechnicalDetail label={`${label}原始值`} value={value} /> : null;
}
