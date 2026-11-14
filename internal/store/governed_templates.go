// File metadata (manifest mirror):
// Purpose: Bridges current session/case authorization and caller-owned transactions to Knowledge template resolution, and exposes authorized case template metadata to Document generation.
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
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/contracttemplate"
	knowledgeApp "contractaudit/internal/knowledge/app"
	knowledgeModel "contractaudit/internal/knowledge/model"
	knowledgePostgres "contractaudit/internal/knowledge/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

// ResolveDocumentTemplates verifies the current session and case before reading
// only active governed template versions. A nonempty exact identity restricts
// the result to one explicitly selected immutable version.
func (s *Store) ResolveDocumentTemplates(ctx context.Context, digest, version, caseID, id, templateVersion, templateDigest string) ([]contracttemplate.Resolved, error) {
	_, actor, err := s.sessionFromDigest(ctx, digest)
	if err != nil {
		return nil, errAttemptAuthorizationRevoked
	}
	proof, ok := actor.CommandAuthority()
	if !ok || proof.ContextVersion != version {
		return nil, errAttemptAuthorizationRevoked
	}
	current, item, err := s.documentWriteCase(ctx, actor, caseID, access.Review)
	if err != nil {
		return nil, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, userScope(current)); err != nil {
		return nil, err
	}
	repo := knowledgePostgres.NewGovernance(platformpostgres.Bind(tx))
	facts := newReviewKnowledgeFacts(tx, current, item, false)
	caseFacts := knowledgeModel.ReviewCaseFacts{CaseID: item.ID, OrgID: item.OwnerOrgID, BusinessLine: item.BusinessLine, Scenario: item.Scenario, ContractType: item.ContractType, BusinessStatus: item.BusinessStatus}
	out, err := knowledgeApp.ResolveTemplates(ctx, repo, facts, item.TenantID, caseFacts, id, templateVersion, templateDigest, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if _, finalActor, e := s.sessionFromDigest(ctx, digest); e != nil {
		return nil, errAttemptAuthorizationRevoked
	} else if finalProof, ok := finalActor.CommandAuthority(); !ok || finalProof.ContextVersion != version || finalActor.ID != current.ID {
		return nil, errAttemptAuthorizationRevoked
	}
	if _, finalCase, e := s.documentWriteCase(ctx, current, caseID, access.Review); e != nil || finalCase.ID != item.ID || finalCase.OwnerOrgID != item.OwnerOrgID || finalCase.BusinessLine != item.BusinessLine || finalCase.Scenario != item.Scenario || finalCase.ContractType != item.ContractType {
		return nil, errAttemptAuthorizationRevoked
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

type documentTemplateAuthority interface {
	ResolveDocumentTemplates(context.Context, contracttemplate.ResolveRequest) ([]contracttemplate.Resolved, error)
}

func (s *Store) documentTemplates(ctx context.Context, actor accounts.User, caseID, id, version, digest string) ([]contracttemplate.Resolved, error) {
	ctx = commandContext(ctx, actor)
	proof, ok := contextProof(ctx)
	if !ok || proof.digest == "" || proof.version == "" {
		return nil, errAttemptAuthorizationRevoked
	}
	input := contracttemplate.ResolveRequest{SessionDigest: proof.digest, ContextVersion: proof.version, CaseID: caseID, ID: id, Version: version, Digest: digest}
	if remote, ok := s.operationAuthority.(documentTemplateAuthority); ok {
		return remote.ResolveDocumentTemplates(ctx, input)
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return nil, errAttemptAuthorizationRevoked
	}
	return s.ResolveDocumentTemplates(ctx, input.SessionDigest, input.ContextVersion, input.CaseID, input.ID, input.Version, input.Digest)
}

// TemplatesForCase lists only templates authorized for this exact case.
func (s *Store) TemplatesForCase(ctx context.Context, actor accounts.User, caseID string) ([]Template, error) {
	if caseID == "" {
		return s.Templates(), nil
	}
	_, item, err := s.documentWriteCase(ctx, actor, caseID, access.Review)
	if err != nil {
		return nil, err
	}
	items, err := s.documentTemplates(ctx, actor, caseID, "", "", "")
	if err != nil {
		return nil, err
	}
	out := make([]Template, 0, len(items)+2)
	for _, v := range s.Templates() {
		if businessTemplateMatches(item, v.ID) {
			out = append(out, v)
		}
	}
	for _, v := range items {
		fields := make([]TemplateField, 0, len(v.Content.Fields))
		for _, f := range v.Content.Fields {
			fields = append(fields, TemplateField{Key: f.Key, Label: f.Label, Type: f.Type, Required: f.Required})
		}
		out = append(out, Template{ID: v.ID, Name: v.Name, Version: v.Version, Digest: v.Digest, Fields: fields, Source: "tenant"})
	}
	return out, nil
}
