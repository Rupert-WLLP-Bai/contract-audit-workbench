// File metadata (manifest mirror):
// Purpose: Defines the Knowledge-owned prepared Legal draft value exchanged by the same-transaction import workflow.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

// PreparedVerifiedLegalDraft is a Knowledge-owned immutable draft plan. Its
// scope key is deliberately derived only after the Legal capture is inserted.
type PreparedVerifiedLegalDraft struct {
	Ref          KnowledgeRef
	Content      GovernedKnowledgeContent
	ContentBytes []byte
}
