// File metadata (manifest mirror):
// Purpose: CaseIntakeRequestIdentity is the original normalized command identity needed to reselect a file on another device. It contains no file bytes or credentials. The stored request… Related `store` operations include CaseIntakeRequestIdentity, PendingCaseIntake, PendingCaseIntakesPage, CaseIntakeContinuation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

// CaseIntakeRequestIdentity is the original normalized command identity needed
// to reselect a file on another device. It contains no file bytes or credentials.
// The stored request digest remains authoritative when this identity is restored.
type CaseIntakeRequestIdentity struct {
	Title        string `json:"title"`
	ContractType string `json:"contract_type"`
	BusinessLine string `json:"business_line"`
	Scenario     string `json:"scenario"`
	FileName     string `json:"file_name"`
	FileSize     int64  `json:"file_size"`
	MediaType    string `json:"media_type"`
	FileSHA256   string `json:"file_sha256"`
}

// PendingCaseIntake is visible only to the originating trusted tenant and actor
// while the case remains readable. Action flags are resolved on the server.
type PendingCaseIntake struct {
	OperationID string `json:"operation_id"`
	CaseID      string `json:"case_id"`
	Title       string `json:"title"`
	State       string `json:"state"`
	FileName    string `json:"file_name"`
	FileSize    *int64 `json:"file_size"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	ErrorCode   string `json:"error_code"`
	CanResume   bool   `json:"can_resume"`
	CanArchive  bool   `json:"can_archive"`
	ReasonCode  string `json:"reason_code"`
}

type PendingCaseIntakesPage struct {
	Items      []PendingCaseIntake `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

// CaseIntakeContinuation never exposes another actor's replay identity. A
// completed or unavailable command has no replay key; its existing case remains
// the continuation target instead of creating a replacement case.
type CaseIntakeContinuation struct {
	OperationID    string                     `json:"operation_id"`
	CaseID         string                     `json:"case_id"`
	State          string                     `json:"state"`
	Request        *CaseIntakeRequestIdentity `json:"request"`
	IdempotencyKey *string                    `json:"idempotency_key"`
	CanResume      bool                       `json:"can_resume"`
	CanArchive     bool                       `json:"can_archive"`
	ReasonCode     string                     `json:"reason_code"`
}

type CaseIntakeArchiveResult struct {
	OperationID string `json:"operation_id"`
	CaseID      string `json:"case_id"`
	ArchivedAt  string `json:"archived_at"`
}
