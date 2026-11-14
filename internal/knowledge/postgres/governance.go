// File metadata (manifest mirror):
// Purpose: Persists Knowledge-owned governed versions, states, pointers, bindings, history, and receipts and reads exact active and imported Legal reference facts.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package postgres stores Knowledge governance rows through a caller-owned
// transaction-bound executor.
package postgres

import (
	"context"
	"errors"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/knowledge/app"
	"contractaudit/internal/knowledge/model"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

// Governance persists Knowledge-owned governance rows through a bound executor.
type Governance struct {
	db platformpostgres.Executor
}

// NewGovernance binds the adapter to the transaction executor supplied by its caller.
func NewGovernance(db platformpostgres.Executor) *Governance {
	return &Governance{db: db}
}

var _ app.CreateDraftRepository = (*Governance)(nil)
var _ app.CommandRepository = (*Governance)(nil)
var _ app.KnowledgeReadRepository = (*Governance)(nil)
var _ app.LegalDraftPrepareRepository = (*Governance)(nil)
var _ app.LegalDraftRepository = (*Governance)(nil)
var _ app.ImportedLegalReferenceReader = (*Governance)(nil)
var _ app.PublishedLegalCaptureReader = (*Governance)(nil)

func (g *Governance) IdempotencyReceipt(ctx context.Context, identity app.CreateDraftIdentity, key string) (string, []byte, bool, error) {
	var digest string
	var response []byte
	err := g.db.QueryRow(ctx, `SELECT request_digest,response FROM governed_knowledge_idempotency WHERE tenant_id=$1 AND actor_id=$2 AND idem_key=$3`,
		identity.TenantID, identity.ActorID, key).Scan(&digest, &response)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	return digest, response, true, nil
}

func (g *Governance) GovernedFamilyHasOtherKind(ctx context.Context, tenant, knowledgeID, kind string) (bool, error) {
	var found bool
	err := g.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM governed_knowledge_versions WHERE tenant_id=$1 AND knowledge_id=$2 AND kind<>$3)`,
		tenant, knowledgeID, kind).Scan(&found)
	return found, err
}

func (g *Governance) InsertVersion(ctx context.Context, record app.CreateVersionRecord) (time.Time, error) {
	content := record.Content
	var legalCaptureID any
	if record.LegalCaptureID != "" {
		legalCaptureID = record.LegalCaptureID
	}
	var created time.Time
	err := g.db.QueryRow(ctx, `INSERT INTO governed_knowledge_versions
		(tenant_id,knowledge_id,version_id,display_version,scope_key,kind,scope_org_id,scope_case_id,source_org_id,source_case_id,candidate_id,candidate_version_id,candidate_content_sha256,content_sha256,content,created_by,created_at,legal_capture_snapshot_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,clock_timestamp(),$17) RETURNING created_at`,
		record.Identity.TenantID, content.KnowledgeID, record.VersionID, content.Version, record.ScopeKey, content.Kind,
		nullableKnowledgeID(content.Scope.OrgID), nullableKnowledgeID(content.Scope.CaseID), nullableKnowledgeID(content.SourceOrgID),
		nullableKnowledgeID(content.SourceCaseID), nullableKnowledgeID(content.CandidateID), nullableKnowledgeID(content.CandidateVersionID),
		nullableKnowledgeID(content.CandidateSHA256), record.ContentSHA256, record.ContentBytes, record.CreatedBy, legalCaptureID).Scan(&created)
	return created, err
}

func (g *Governance) InsertInitialState(ctx context.Context, identity app.CreateDraftIdentity, knowledgeID, versionID string) error {
	_, err := g.db.Exec(ctx, `INSERT INTO governed_knowledge_state (tenant_id,knowledge_id,version_id,status,revision) VALUES ($1,$2,$3,'draft',1)`,
		identity.TenantID, knowledgeID, versionID)
	return err
}

func (g *Governance) InsertPolicyBinding(ctx context.Context, identity app.CreateDraftIdentity, content model.GovernedKnowledgeContent, versionID string, binding []byte) error {
	_, err := g.db.Exec(ctx, `INSERT INTO governed_policy_rule_bindings (tenant_id,knowledge_id,version_id,pack_id,pack_version,binding) VALUES ($1,$2,$3,$4,$5,$6)`,
		identity.TenantID, content.KnowledgeID, versionID, content.Binding.PackID, content.Binding.PackVersion, binding)
	return err
}

func (g *Governance) CurrentPointer(ctx context.Context, identity app.CreateDraftIdentity, knowledgeID, scopeKey string) (app.KnowledgePointer, error) {
	var pointer app.KnowledgePointer
	err := g.db.QueryRow(ctx, `SELECT version_id,active,pointer_revision FROM governed_knowledge_pointers WHERE tenant_id=$1 AND knowledge_id=$2 AND scope_key=$3`,
		identity.TenantID, knowledgeID, scopeKey).Scan(&pointer.VersionID, &pointer.Active, &pointer.PointerRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.KnowledgePointer{}, nil
	}
	return pointer, err
}

func (g *Governance) AppendHistory(ctx context.Context, identity app.CreateDraftIdentity, entry model.GovernedKnowledgeHistoryEntry) error {
	_, err := g.db.Exec(ctx, `INSERT INTO governed_knowledge_history
		(tenant_id,knowledge_id,version_id,action,revision,pointer_revision,actor_id,reason,content_sha256,previous_version_id,previous_active,resulting_version_id,resulting_active,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,clock_timestamp())`, identity.TenantID, entry.Ref.KnowledgeID, entry.Ref.VersionID,
		entry.Action, entry.Revision, entry.PointerRevision, entry.ActorID, entry.Reason, entry.Ref.ContentSHA256, entry.PreviousVersionID,
		entry.PreviousActive, entry.ResultingVersionID, entry.ResultingActive)
	return err
}

func (g *Governance) InsertIdempotencyReceipt(ctx context.Context, identity app.CreateDraftIdentity, key, digest string, response []byte) error {
	_, err := g.db.Exec(ctx, `INSERT INTO governed_knowledge_idempotency (tenant_id,actor_id,idem_key,request_digest,response,created_at) VALUES ($1,$2,$3,$4,$5,clock_timestamp())`,
		identity.TenantID, identity.ActorID, key, digest, response)
	return err
}

func nullableKnowledgeID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

const governedKnowledgeVersionColumns = `SELECT v.knowledge_id,v.version_id,v.content,v.content_sha256,v.display_version,v.scope_key,v.kind,v.scope_org_id,v.scope_case_id,v.source_org_id,v.source_case_id,v.candidate_id,v.candidate_version_id,v.candidate_content_sha256,v.created_by,v.created_at,COALESCE(v.legal_capture_snapshot_id,''),
 COALESCE(s.status,''),COALESCE(s.revision,0),COALESCE(s.approved_digest,''),COALESCE(s.approval_actor_id,''),COALESCE(s.approval_reason,''),COALESCE(s.cross_case_reviewed,false),
 COALESCE(p.version_id,''),COALESCE(p.active,false),COALESCE(p.pointer_revision,0),COALESCE(b.binding,''::bytea),COALESCE(b.pack_id,''),COALESCE(b.pack_version,'')
 FROM governed_knowledge_versions v LEFT JOIN governed_knowledge_state s USING (tenant_id,knowledge_id,version_id)
	LEFT JOIN governed_policy_rule_bindings b USING (tenant_id,knowledge_id,version_id)
	LEFT JOIN governed_knowledge_pointers p ON p.tenant_id=v.tenant_id AND p.knowledge_id=v.knowledge_id AND p.scope_key=v.scope_key`

func scanGovernedKnowledgeVersion(row pgx.Row) (app.RawVersion, error) {
	var r app.RawVersion
	v := &r.Version
	err := row.Scan(&v.Ref.KnowledgeID, &v.Ref.VersionID, &r.ContentBytes, &v.Ref.ContentSHA256, &r.DisplayVersion, &r.ScopeKey, &r.Kind, &r.ScopeOrg, &r.ScopeCase, &r.SourceOrg, &r.SourceCase, &r.CandidateID, &r.CandidateVersionID, &r.CandidateDigest, &v.CreatedBy, &r.CreatedAt, &r.LegalCaptureID, &v.Status, &v.Revision, &v.ApprovedDigest, &v.ApprovalActorID, &v.ApprovalReason, &v.CrossCaseReviewed, &r.PointerID, &r.PointerActive, &v.PointerRevision, &r.Binding, &r.BindingPackID, &r.BindingPackVersion)
	return r, err
}

func (g *Governance) LoadVersion(ctx context.Context, tenant, id, version string) (app.RawVersion, error) {
	r, err := scanGovernedKnowledgeVersion(g.db.QueryRow(ctx, governedKnowledgeVersionColumns+` WHERE v.tenant_id=$1 AND v.knowledge_id=$2 AND v.version_id=$3`, tenant, id, version))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.RawVersion{}, fault.NotFound("没有找到对应制度版本")
	}
	return r, err
}

func (g *Governance) ListVersionPage(ctx context.Context, tenant, lastKnowledge, lastVersion string, limit int) ([]app.RawVersion, error) {
	rows, err := g.db.Query(ctx, governedKnowledgeVersionColumns+` WHERE v.tenant_id=$1 AND ($2='' OR (v.knowledge_id,v.version_id)>($2,$3))
		ORDER BY v.knowledge_id,v.version_id LIMIT $4`, tenant, lastKnowledge, lastVersion, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []app.RawVersion{}
	for rows.Next() {
		item, err := scanGovernedKnowledgeVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *Governance) LoadVersions(ctx context.Context, tenant, knowledgeID string, versionIDs []string) ([]app.RawVersion, error) {
	out := []app.RawVersion{}
	if len(versionIDs) == 0 {
		return out, nil
	}
	rows, err := g.db.Query(ctx, governedKnowledgeVersionColumns+` WHERE v.tenant_id=$1 AND v.knowledge_id=$2 AND v.version_id=ANY($3::text[])`,
		tenant, knowledgeID, versionIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanGovernedKnowledgeVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *Governance) ListHistory(ctx context.Context, tenant, knowledgeID string) ([]model.GovernedKnowledgeHistoryEntry, error) {
	rows, err := g.db.Query(ctx, `SELECT seq,version_id,action,revision,pointer_revision,actor_id,reason,content_sha256,previous_version_id,previous_active,resulting_version_id,resulting_active,occurred_at
		FROM governed_knowledge_history WHERE tenant_id=$1 AND knowledge_id=$2 ORDER BY seq`, tenant, knowledgeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.GovernedKnowledgeHistoryEntry{}
	for rows.Next() {
		var entry model.GovernedKnowledgeHistoryEntry
		var occurredAt time.Time
		entry.Ref.KnowledgeID = knowledgeID
		if err := rows.Scan(&entry.Sequence, &entry.Ref.VersionID, &entry.Action, &entry.Revision, &entry.PointerRevision, &entry.ActorID, &entry.Reason, &entry.Ref.ContentSHA256,
			&entry.PreviousVersionID, &entry.PreviousActive, &entry.ResultingVersionID, &entry.ResultingActive, &occurredAt); err != nil {
			return nil, err
		}
		entry.OccurredAt = occurredAt.UTC().Format(time.RFC3339Nano)
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func (g *Governance) UpdateState(ctx context.Context, id app.CreateDraftIdentity, input model.GovernedKnowledgeCommandInput, plan model.CommandPlan) (bool, error) {
	tag, err := g.db.Exec(ctx, `UPDATE governed_knowledge_state SET status=$1,revision=revision+1,approved_digest=$2,approval_actor_id=$3,approval_reason=$4,cross_case_reviewed=$5 WHERE tenant_id=$6 AND knowledge_id=$7 AND version_id=$8 AND revision=$9`, plan.NextStatus, plan.ApprovedDigest, plan.ApprovalActorID, plan.ApprovalReason, plan.CrossCaseReviewed, id.TenantID, input.KnowledgeID, input.VersionID, input.ExpectedRevision)
	return tag.RowsAffected() == 1, err
}
func (g *Governance) UpdatePointer(ctx context.Context, id app.CreateDraftIdentity, knowledgeID, scopeKey, version string, active bool, revision int64) (bool, error) {
	if revision == 0 {
		tag, err := g.db.Exec(ctx, `INSERT INTO governed_knowledge_pointers (tenant_id,knowledge_id,scope_key,version_id,active,pointer_revision,updated_by,updated_at) VALUES ($1,$2,$3,$4,$5,1,$6,clock_timestamp()) ON CONFLICT DO NOTHING`, id.TenantID, knowledgeID, scopeKey, version, active, id.ActorID)
		return tag.RowsAffected() == 1, err
	}
	tag, err := g.db.Exec(ctx, `UPDATE governed_knowledge_pointers SET version_id=$1,active=$2,pointer_revision=pointer_revision+1,updated_by=$3,updated_at=clock_timestamp() WHERE tenant_id=$4 AND knowledge_id=$5 AND scope_key=$6 AND pointer_revision=$7`, version, active, id.ActorID, id.TenantID, knowledgeID, scopeKey, revision)
	return tag.RowsAffected() == 1, err
}

// ActiveKnowledge returns pointer identities in the resolver's original order.
func (g *Governance) ActiveKnowledge(ctx context.Context, tenant string) ([]app.ActiveKnowledgeIdentity, error) {
	rows, err := g.db.Query(ctx, `SELECT knowledge_id,version_id FROM governed_knowledge_pointers WHERE tenant_id=$1 AND active ORDER BY knowledge_id,scope_key`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []app.ActiveKnowledgeIdentity
	for rows.Next() {
		var id app.ActiveKnowledgeIdentity
		if err := rows.Scan(&id.KnowledgeID, &id.VersionID); err != nil {
			return nil, err
		}
		ids = append(ids, id)
		if len(ids) > 4096 {
			return nil, fault.Conflict("适用制度集合超过处理上限")
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

// ImportedLegalContentSHA is the legacy replay projection; it does not inspect
// current pointer, body, approval, or Legal-owned capture rows.
func (g *Governance) ImportedLegalContentSHA(ctx context.Context, tenant, captureID string, ref model.KnowledgeRef) (string, error) {
	var digest string
	err := g.db.QueryRow(ctx, `SELECT content_sha256 FROM governed_knowledge_versions WHERE tenant_id=$1 AND knowledge_id=$2 AND version_id=$3 AND kind='legal' AND legal_capture_snapshot_id=$4`, tenant, ref.KnowledgeID, ref.VersionID, captureID).Scan(&digest)
	return digest, err
}
func (g *Governance) HasPublishedLegalCapture(ctx context.Context, tenant, captureID string) (bool, error) {
	var referenced bool
	err := g.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM governed_knowledge_versions v JOIN governed_knowledge_state s USING (tenant_id,knowledge_id,version_id) WHERE v.tenant_id=$1 AND v.kind='legal' AND v.legal_capture_snapshot_id=$2 AND s.status IN ('published','withdrawn'))`, tenant, captureID).Scan(&referenced)
	return referenced, err
}
