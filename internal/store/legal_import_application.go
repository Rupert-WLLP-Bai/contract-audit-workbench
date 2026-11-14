// File metadata (manifest mirror):
// Purpose: Binds the trusted import actor and caller transaction to Knowledge batch operations, exact replay checks, and capture identity generation.
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

	"contractaudit/internal/accounts"
	knowledgeApp "contractaudit/internal/knowledge/app"
	knowledgeModel "contractaudit/internal/knowledge/model"
	knowledgePostgres "contractaudit/internal/knowledge/postgres"
	legalApp "contractaudit/internal/legal/app"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type legalImportKnowledge struct {
	tx    pgx.Tx
	actor accounts.User
}

func (a legalImportKnowledge) PrepareLegalDrafts(ctx context.Context, contents []knowledgeModel.GovernedKnowledgeContent) ([]knowledgeModel.PreparedVerifiedLegalDraft, error) {
	repo := knowledgePostgres.NewGovernance(platformpostgres.Bind(a.tx))
	facts := governedKnowledgeFacts{tx: a.tx, admin: a.actor}
	return knowledgeApp.PrepareVerifiedLegalDrafts(ctx, repo, facts, accounts.Tenant(a.actor), contents)
}
func (a legalImportKnowledge) PersistLegalDrafts(ctx context.Context, captureID string, drafts []knowledgeModel.PreparedVerifiedLegalDraft) error {
	repo := knowledgePostgres.NewGovernance(platformpostgres.Bind(a.tx))
	// LoadLegalCapture scans a fresh row per call, preserving the original
	// per-article query and validation ordering inside the same transaction.
	facts := governedKnowledgeFacts{tx: a.tx, admin: a.actor}
	return knowledgeApp.PersistVerifiedLegalDrafts(ctx, repo, facts, accounts.Tenant(a.actor), a.actor.ID, captureID, drafts)
}
func (a legalImportKnowledge) ValidateImportedRefs(ctx context.Context, captureID string, refs []knowledgeModel.KnowledgeRef) error {
	return knowledgeApp.ValidateImportedLegalReferences(ctx, knowledgePostgres.NewGovernance(platformpostgres.Bind(a.tx)), accounts.Tenant(a.actor), captureID, refs)
}

type legalCaptureIDs struct{}

func (legalCaptureIDs) NewCaptureID() (string, error) { return newID("lsc_") }

var _ legalApp.ImportKnowledge = legalImportKnowledge{}
