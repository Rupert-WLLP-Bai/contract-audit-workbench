// File metadata (manifest mirror):
// Purpose: Defines raw immutable revision snapshot identities for domain-owned lifecycle projections without re-decoding unrelated frozen fields.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

// LifecycleRevisionSnapshot preserves the exact stored historical content.
type LifecycleRevisionSnapshot struct {
	RevisionID string
	Snapshot   []byte
}
