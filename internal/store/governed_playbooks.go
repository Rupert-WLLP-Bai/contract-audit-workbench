// File metadata (manifest mirror):
// Purpose: Bridges authenticated case and verified contract-revision facts to Knowledge playbook catalogs and exact adoption inside caller-owned transactions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/agentflow"
	"contractaudit/internal/fault"
	knowledgeApp "contractaudit/internal/knowledge/app"
	knowledgeModel "contractaudit/internal/knowledge/model"
	knowledgePostgres "contractaudit/internal/knowledge/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type playbookFacts struct {
	reviewKnowledgeFacts
	repo   *knowledgePostgres.Governance
	tenant string
	at     time.Time
	store  *Store
}

func (f playbookFacts) VerifyPlaybookTemplate(ctx context.Context, ref knowledgeModel.PlaybookTemplateRef) error {
	for _, item := range f.store.Templates() {
		if item.ID == ref.TemplateID {
			if !businessTemplateMatches(f.item, item.ID) || item.Version != ref.TemplateVersion || item.Digest != ref.TemplateDigest {
				return fault.Conflict("手册正文范本不匹配")
			}
			return nil
		}
	}
	scope := knowledgeModel.ReviewCaseFacts{CaseID: f.item.ID, OrgID: f.item.OwnerOrgID, BusinessLine: f.item.BusinessLine, Scenario: f.item.Scenario, ContractType: f.item.ContractType, BusinessStatus: f.item.BusinessStatus}
	_, err := knowledgeApp.ResolveTemplates(ctx, f.repo, f.reviewKnowledgeFacts, f.tenant, scope, ref.TemplateID, ref.TemplateVersion, ref.TemplateDigest, f.at)
	return err
}

func (s *Store) resolvePlaybookTx(ctx context.Context, tx pgx.Tx, user accounts.User, item Case, ref knowledgeModel.KnowledgeRef, at time.Time) (knowledgeModel.AdoptedPlaybook, error) {
	if !authorizeCase(item, user, access.Review) {
		return knowledgeModel.AdoptedPlaybook{}, fault.NotFound("没有找到对应记录")
	}
	pack, err := selectActiveRulePackTx(ctx, tx, item.TenantID, item.BusinessLine, item.Scenario, item.ContractType, at)
	if err != nil {
		return knowledgeModel.AdoptedPlaybook{}, err
	}
	if pack == nil {
		return knowledgeModel.AdoptedPlaybook{}, fault.Conflict("案件没有适用的已发布规则包")
	}
	repo := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
	facts := playbookFacts{reviewKnowledgeFacts: newReviewKnowledgeFacts(tx, user, item, true), repo: repo, tenant: item.TenantID, at: at, store: s}
	scope := knowledgeModel.ReviewCaseFacts{CaseID: item.ID, OrgID: item.OwnerOrgID, BusinessLine: item.BusinessLine, Scenario: item.Scenario, ContractType: item.ContractType, BusinessStatus: item.BusinessStatus}
	adopted, err := knowledgeApp.ResolvePlaybook(ctx, repo, facts, item.TenantID, scope, ref, rulePackRef(pack), at)
	if err != nil {
		return adopted, err
	}
	encoded, err := json.Marshal(adopted.Content.Playbook)
	if err != nil || len(encoded) > agentflow.AdoptedPlaybookFieldBytes {
		return knowledgeModel.AdoptedPlaybook{}, fault.Conflict("审查手册超出可用上下文预算，请缩短后重新发布")
	}
	return adopted, nil
}

// PlaybookOption is the authorized identity shown before explicit adoption.
type PlaybookOption struct {
	Ref   knowledgeModel.KnowledgeRef `json:"ref"`
	Title string                      `json:"title"`
}
type PlaybookOptions struct {
	Items  []PlaybookOption `json:"items"`
	Reason string           `json:"reason,omitempty"`
}

func loadPlaybookSourceCase(ctx context.Context, tx pgx.Tx, user accounts.User, orgID, caseID string) (Case, error) {
	item, err := loadKnowledgeCase(ctx, tx, user, caseID)
	if err != nil {
		return Case{}, err
	}
	if item.OwnerOrgID != orgID {
		return Case{}, fault.NotFound("没有找到对应记录")
	}
	return item, nil
}

func (s *Store) casePlaybooksTx(ctx context.Context, tx pgx.Tx, user accounts.User, item Case, at time.Time) (PlaybookOptions, error) {
	out := PlaybookOptions{Items: []PlaybookOption{}}
	if !authorizeCase(item, user, access.Review) {
		return out, fault.NotFound("没有找到对应记录")
	}
	repo := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
	scope := knowledgeModel.ReviewCaseFacts{CaseID: item.ID, OrgID: item.OwnerOrgID, BusinessLine: item.BusinessLine, Scenario: item.Scenario, ContractType: item.ContractType, BusinessStatus: item.BusinessStatus}
	refs, err := repo.ActivePlaybooks(ctx, item.TenantID, scope, "", "", "")
	if err != nil {
		return out, err
	}
	facts := newReviewKnowledgeFacts(tx, user, item, false)
	for _, id := range refs {
		loaded, e := knowledgeApp.LoadExactVersion(ctx, repo, facts, item.TenantID, id.KnowledgeID, id.VersionID)
		if e != nil {
			return out, e
		}
		adopted, e := s.resolvePlaybookTx(ctx, tx, user, item, loaded.Version.Ref, at)
		if e != nil {
			var problem *fault.Error
			if errors.As(e, &problem) && (problem.Status == 403 || problem.Status == 404 || problem.Status == 409) {
				continue
			}
			return out, e
		}
		out.Items = append(out.Items, PlaybookOption{Ref: adopted.Ref, Title: adopted.Content.Title})
	}
	return out, nil
}

func (s *Store) ListCasePlaybooks(ctx context.Context, user accounts.User, caseID string) (PlaybookOptions, error) {
	ctx = commandContext(ctx, user)
	current, err := trustedUser(user)
	if err != nil {
		return PlaybookOptions{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return PlaybookOptions{}, err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, userScope(current)); err != nil {
		return PlaybookOptions{}, err
	}
	item, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		return PlaybookOptions{}, err
	}
	out, err := s.casePlaybooksTx(ctx, tx, current, item, time.Now().UTC())
	if err != nil {
		return PlaybookOptions{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PlaybookOptions{}, err
	}
	return out, nil
}

// ListContractPlaybooks derives case scope solely from the sealed current
// revision. Missing or ambiguous provenance leaves non-adoption available.
func (s *Store) ListContractPlaybooks(ctx context.Context, user accounts.User, contractID string) (PlaybookOptions, error) {
	ctx = commandContext(ctx, user)
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return PlaybookOptions{}, err
	}
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return PlaybookOptions{}, err
	}
	defer rollbackCLM(tx)
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return PlaybookOptions{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return PlaybookOptions{}, fault.NotFound("没有找到对应记录")
	}
	unavailable := PlaybookOptions{Items: []PlaybookOption{}, Reason: "contract_scope_unavailable"}
	if record.CurrentRevisionID == nil {
		if err = tx.Commit(ctx); err != nil {
			return PlaybookOptions{}, err
		}
		return unavailable, nil
	}
	freeze := approvalSubmissionFreeze{store: s, tx: tx, user: current, executor: platformpostgres.Bind(tx)}
	materials, err := freeze.VerifyFreeze(ctx, record.TenantID, id, *record.CurrentRevisionID)
	if err != nil {
		return PlaybookOptions{}, err
	}
	if len(materials) == 0 {
		if err = tx.Commit(ctx); err != nil {
			return PlaybookOptions{}, err
		}
		return unavailable, nil
	}
	caseID, orgID := materials[0].CaseID, materials[0].CaseOrgID
	for _, material := range materials {
		if caseID == "" || orgID == "" || material.CaseID != caseID || material.CaseOrgID != orgID {
			if err = tx.Commit(ctx); err != nil {
				return PlaybookOptions{}, err
			}
			return unavailable, nil
		}
	}
	item, err := loadPlaybookSourceCase(ctx, tx, current, orgID, caseID)
	if err != nil {
		return PlaybookOptions{}, err
	}
	out, err := s.casePlaybooksTx(ctx, tx, current, item, time.Now().UTC())
	if err != nil {
		return PlaybookOptions{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PlaybookOptions{}, err
	}
	return out, nil
}
