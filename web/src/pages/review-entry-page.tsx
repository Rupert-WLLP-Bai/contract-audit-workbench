// File metadata (manifest mirror):
// Purpose: Lists server-visible cases with URL-backed all, actor-assigned pending-task, and recent-run views; searches case identities and opens the newest persisted run with its exact frozen attempt identity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-12
// Revision: r5
// Copyright: See LICENSE
// End file metadata.

import { ReviewIntakeSurface } from "../features/intake/review-intake-dialog";
import "./review-entry-page.css";

export function ReviewEntryPage() {
  return (
    <section className="page review-entry-page">
      <div className="review-intake-card">
        <h1>审一份合同</h1>
        <ReviewIntakeSurface />
      </div>
    </section>
  );
}
