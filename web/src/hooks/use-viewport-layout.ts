// File metadata (manifest mirror):
// Purpose: Chooses split or single-column review layout from the viewport breakpoint and updates when the browser media query changes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { useEffect, useState } from "react";
import { reviewLayout, type ReviewLayout } from "../domain/review";

export function useReviewLayout(): ReviewLayout {
  const [layout, setLayout] = useState<ReviewLayout>(() =>
    typeof window === "undefined" ? "switch" : reviewLayout(window.innerWidth),
  );

  useEffect(() => {
    const media = window.matchMedia("(min-width: 1100px)");
    const apply = () => setLayout(media.matches ? "split" : "switch");
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, []);

  return layout;
}
