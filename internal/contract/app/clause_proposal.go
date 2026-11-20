// File metadata (manifest mirror):
// Purpose: Coordinates authenticated idempotent clause decisions with complete frozen text verification and atomic contract revision creation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

// ClauseProposalRepository is bound to the same caller-owned transaction as
// the contract revision commands. Implementations do not authorize actors.
type ClauseProposalRepository interface {
	LockReceipt(context.Context, string, string, string, string) error
	LoadReceipt(context.Context, string, string, string, string) (string, []byte, bool, error)
	SaveReceipt(context.Context, string, string, string, string, string, []byte) error
	LoadProposal(context.Context, string, string, string, string, bool) (contract.ClauseProposal, error)
	AppendDecision(context.Context, string, string, string, contract.ClauseProposalHistory, int64) error
}

// ClauseProposalAcceptance is implemented by the boundary that can prove the
// frozen evidence and uploaded document, and run both Contract commands in the
// caller's transaction. It must re-check current case and contract authority.
type ClauseProposalAcceptance interface {
	AuthorizeDecision(context.Context, string, contract.ClauseProposalDecision) error
	FrozenText(context.Context, contract.ClauseProposal) (string, error)
	MaterialText(context.Context, contract.ClauseProposal, contract.ClauseProposalDecision) (string, error)
	CreateRevision(context.Context, contract.ClauseProposal, contract.ClauseProposalDecision, string) (string, error)
}

type ClauseProposalPrincipal struct{ TenantID, OrgID, CaseID, ActorID string }

// DecideClauseProposal records the decision only after the new revision is
// durably staged in the same transaction. A caller must check current case
// authority before entering this function, including on receipt replay.
func DecideClauseProposal(ctx context.Context, principal ClauseProposalPrincipal, proposalID string, input contract.ClauseProposalDecision, repository ClauseProposalRepository, acceptance ClauseProposalAcceptance, now string) (contract.ClauseProposal, error) {
	if err := contract.ValidateClauseProposalDecision(input); err != nil {
		return contract.ClauseProposal{}, fault.Invalid(err.Error())
	}
	if err := contract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return contract.ClauseProposal{}, err
	}
	payload, err := json.Marshal(struct {
		CaseID, ProposalID string
		Input              contract.ClauseProposalDecision
	}{principal.CaseID, proposalID, input})
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	digestBytes := sha256.Sum256(payload)
	digest := hex.EncodeToString(digestBytes[:])
	if err := acceptance.AuthorizeDecision(ctx, proposalID, input); err != nil {
		return contract.ClauseProposal{}, err
	}
	const route = "clm.clause_proposal.decision"
	if err := repository.LockReceipt(ctx, principal.TenantID, principal.ActorID, route, input.IdempotencyKey); err != nil {
		return contract.ClauseProposal{}, err
	}
	existingDigest, response, found, err := repository.LoadReceipt(ctx, principal.TenantID, principal.ActorID, route, input.IdempotencyKey)
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	if found {
		if existingDigest != digest {
			return contract.ClauseProposal{}, fault.Conflict("Idempotency-Key已用于不同请求载荷")
		}
		var result contract.ClauseProposal
		if err := json.Unmarshal(response, &result); err != nil {
			return contract.ClauseProposal{}, err
		}
		return result, nil
	}
	proposal, err := repository.LoadProposal(ctx, principal.TenantID, principal.OrgID, principal.CaseID, proposalID, true)
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	if proposal.Status != "pending" || proposal.Version != input.ExpectedVersion {
		return contract.ClauseProposal{}, fault.Conflict("条款建议已被处理或版本已变化")
	}
	history := contract.ClauseProposalHistory{Version: proposal.Version + 1, Action: input.Action, ActorID: principal.ActorID, Reason: input.Reason, CreatedAt: now}
	if input.Action != "reject" {
		if !proposal.EvidenceVerified || proposal.Evidence == nil {
			return contract.ClauseProposal{}, fault.Conflict("条款建议缺少可核验的原文引用")
		}
		frozen, err := acceptance.FrozenText(ctx, proposal)
		if err != nil {
			return contract.ClauseProposal{}, err
		}
		replacement := proposal.ProposedText
		if input.Action == "accept_edited" {
			replacement = input.AcceptedText
		}
		derived, err := acceptance.MaterialText(ctx, proposal, input)
		if err != nil {
			return contract.ClauseProposal{}, err
		}
		if err := contract.VerifyDerivedClauseText(frozen, *proposal.Evidence, replacement, derived); err != nil {
			return contract.ClauseProposal{}, fault.Conflict(err.Error())
		}
		revision, err := acceptance.CreateRevision(ctx, proposal, input, digest)
		if err != nil {
			return contract.ClauseProposal{}, err
		}
		history.AcceptedText, history.MaterialID, history.DocumentID, history.ContractID, history.SHA256, history.RevisionID = replacement, input.MaterialID, input.DocumentID, input.ContractID, input.SHA256, revision
	}
	if err := repository.AppendDecision(ctx, principal.TenantID, principal.OrgID, proposal.ID, history, input.ExpectedVersion); err != nil {
		return contract.ClauseProposal{}, err
	}
	proposal.Version++
	proposal.Status = input.Action
	proposal.History = append(proposal.History, history)
	response, err = json.Marshal(proposal)
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	if err := repository.SaveReceipt(ctx, principal.TenantID, principal.ActorID, route, input.IdempotencyKey, digest, response); err != nil {
		return contract.ClauseProposal{}, err
	}
	return proposal, nil
}
