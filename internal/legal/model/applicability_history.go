// File metadata (manifest mirror):
// Purpose: Defines Legal applicability history request and response wire values.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package model

type LegalApplicabilityHistoryInput struct {
	Cursor string
	Limit  int
}

// LegalApplicabilityFactHistory is the authored immutable history and current CAS head.
type LegalApplicabilityFactHistory struct {
	CaseID         string                          `json:"case_id"`
	Dimension      string                          `json:"dimension"`
	CurrentVersion int64                           `json:"current_version"`
	Items          []LegalApplicabilityFactVersion `json:"items"`
	NextCursor     *string                         `json:"next_cursor"`
	CanManage      bool                            `json:"can_manage"`
}
