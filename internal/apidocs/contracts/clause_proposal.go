// File metadata (manifest mirror):
// Purpose: Defines public request and response contracts for frozen clause proposals and confirmed revision decisions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package contracts

import contract "contractaudit/internal/contract/model"

type ClauseProposal = contract.ClauseProposal
type ClauseProposalCreate struct {
	RunID         string                   `json:"run_id"`
	AttemptID     string                   `json:"attempt_id"`
	FindingID     string                   `json:"finding_id,omitempty"`
	Evidence      *contract.ClauseEvidence `json:"evidence,omitempty"`
	ProposedText  string                   `json:"proposed_text"`
	Reason        string                   `json:"reason"`
	RemediationID string                   `json:"remediation_id,omitempty"`
}
type ClauseProposalDecision = contract.ClauseProposalDecision
type ClauseProposalList struct {
	Items []contract.ClauseProposal `json:"items"`
}
type ClauseDerivedRequest struct {
	ExpectedVersion int64   `json:"expected_version"`
	EditedText      *string `json:"edited_text,omitempty"`
}
type ClauseDerivedResponse struct {
	Text   string `json:"text"`
	Format string `json:"format"`
}
