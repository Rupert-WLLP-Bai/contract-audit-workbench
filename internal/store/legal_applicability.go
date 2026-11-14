// File metadata (manifest mirror):
// Purpose: Preserves Legal fact entry-point authorization and transaction boundaries while delegating Record and Resolve business to Legal applications.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-14
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	legalApp "contractaudit/internal/legal/app"
	legalModel "contractaudit/internal/legal/model"
	legalPostgres "contractaudit/internal/legal/postgres"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type LegalApplicabilityPartyPin = legalModel.LegalApplicabilityPartyPin
type LegalApplicabilityFactInput = legalModel.LegalApplicabilityFactInput
type LegalApplicabilityResolutionInput = legalModel.LegalApplicabilityResolutionInput

type LegalApplicabilityResolvedFact = legalModel.LegalApplicabilityResolvedFact
type LegalApplicabilityFactSet = legalModel.LegalApplicabilityFactSet
type LegalApplicabilityFactVersion = legalModel.LegalApplicabilityFactVersion

func normalizeLegalApplicabilityInput(input LegalApplicabilityFactInput, createdAt time.Time, verifiedBasisRefs json.RawMessage, basisObtainedAt *time.Time) (LegalApplicabilityFactVersion, error) {
	fact, err := legalModel.NormalizeLegalApplicabilityInput(input, createdAt, verifiedBasisRefs, basisObtainedAt)
	if err != nil {
		return LegalApplicabilityFactVersion{}, fault.Invalid(err.Error())
	}
	return fact, nil
}

func parseLegalFactTime(raw string) (time.Time, error) {
	return legalModel.ParseFactTime(raw)
}

func legalApplicabilityBasisProvided(raw json.RawMessage) (bool, error) {
	return legalModel.LegalApplicabilityBasisProvided(raw)
}

func legalApplicabilityRequestDigest(tenantID, orgID, caseID, actorID, key string, input LegalApplicabilityFactInput) (string, error) {
	return legalModel.LegalApplicabilityRequestDigest(tenantID, orgID, caseID, actorID, key, input)
}

func legalFactBasisObtainedAt(st state, caseItem Case, refs json.RawMessage) (*time.Time, error) {
	var anchors []materialAnchorProjection
	if err := json.Unmarshal(refs, &anchors); err != nil {
		return nil, err
	}
	if len(anchors) == 0 {
		return nil, nil
	}
	var latest time.Time
	for _, anchor := range anchors {
		for _, document := range st.Documents {
			if document.ID != anchor.DocumentID || document.CaseID != caseItem.ID || document.TenantID != caseItem.TenantID || document.OrgID != caseItem.OwnerOrgID {
				continue
			}
			created, err := parseLegalFactTime(document.CreatedAt)
			if err != nil {
				return nil, fault.Invalid("依据材料记录时间无效")
			}
			if created.After(latest) {
				latest = created
			}
			break
		}
	}
	if latest.IsZero() {
		return nil, fault.Invalid("材料依据没有对应的服务端记录")
	}
	return &latest, nil
}

// RecordLegalApplicabilityFact appends a human-confirmed fact version. The
// caller supplies a CAS version and idempotency key; evidence timestamps and
// current-state valid_from are always derived by the server.
func (s *Store) RecordLegalApplicabilityFact(ctx context.Context, user accounts.User, caseID string, input LegalApplicabilityFactInput) (LegalApplicabilityFactVersion, error) {
	ctx = commandContext(ctx, user)
	if err := s.legalReady(true); err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return LegalApplicabilityFactVersion{}, fault.Forbidden("只有 Core 服务可以记录法律适用事实")
	}
	if ctx == nil {
		return LegalApplicabilityFactVersion{}, fault.Invalid("法律适用事实写入需要请求上下文")
	}
	current, err := trustedUser(user)
	if err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	if strings.TrimSpace(caseID) != caseID || caseID == "" || input.ExpectedVersion < 0 {
		return LegalApplicabilityFactVersion{}, fault.Invalid("事实版本或幂等请求标识无效")
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	scope := userScope(current)
	scope.CaseID = caseID
	// ReadCommitted gives each statement a fresh snapshot after the shared
	// schema/case advisory locks, so a waiter sees the winning idempotent write
	// or CAS head instead of retaining a pre-lock Serializable snapshot.
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	caseItem, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	if !authorizeCase(caseItem, current, access.Manage) {
		return LegalApplicabilityFactVersion{}, fault.Forbidden("当前授权不能确认该案件的法律适用事实")
	}
	id := legalApp.RecordIdentity{TenantID: accounts.Tenant(current), OrgID: caseItem.OwnerOrgID, CaseID: caseID, ActorID: current.ID, Issuer: current.Issuer, Subject: current.Subject}
	repo := legalPostgres.NewApplicability(platformpostgres.Bind(tx))
	stored, digest, found, err := legalApp.FindRecordReplay(ctx, repo, id, input)
	if err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	if found {
		if !authorizeCase(caseItem, current, access.Manage) {
			return LegalApplicabilityFactVersion{}, fault.Forbidden("当前授权不能读取该案件的法律适用事实")
		}
		if err := tx.Commit(ctx); err != nil {
			return LegalApplicabilityFactVersion{}, err
		}
		return stored, nil
	}
	if err := businessWritable(caseItem); err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	facts := legalRecordFacts{tx: tx, user: current, item: caseItem}
	fact, err := legalApp.RecordNewFact(ctx, repo, facts, facts, legalRecordClock{}, id, input, digest)
	if err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	if !authorizeCase(caseItem, current, access.Manage) {
		return LegalApplicabilityFactVersion{}, fault.Forbidden("当前授权不能确认该案件的法律适用事实")
	}
	if err := tx.Commit(ctx); err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	return fact, nil
}

func verifyLegalApplicabilityBasisManifest(raw []byte, digest string) (json.RawMessage, error) {
	return legalModel.VerifyApplicabilityBasisManifest(raw, digest)
}
func loadLegalApplicabilityVersion(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, dimension string, version int64) (LegalApplicabilityFactVersion, error) {
	return legalApp.ReadRecordVersion(ctx, legalPostgres.NewApplicability(platformpostgres.Bind(tx)), legalApp.RecordIdentity{TenantID: tenantID, OrgID: orgID, CaseID: caseID}, dimension, version)
}

// ResolveLegalApplicabilityFacts returns selected immutable versions alongside
// a separate resolution projection. Omitted times share one server UTC instant.
func (s *Store) ResolveLegalApplicabilityFacts(ctx context.Context, user accounts.User, caseID string, input LegalApplicabilityResolutionInput) (LegalApplicabilityFactSet, error) {
	ctx = commandContext(ctx, user)
	if err := s.legalReady(false); err != nil {
		return LegalApplicabilityFactSet{}, err
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return LegalApplicabilityFactSet{}, fault.Forbidden("只有 Core 服务可以读取法律适用事实")
	}
	if ctx == nil {
		return LegalApplicabilityFactSet{}, fault.Invalid("法律适用事实读取需要请求上下文")
	}
	current, err := trustedUser(user)
	if err != nil {
		return LegalApplicabilityFactSet{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	at := now
	if input.BusinessAt != "" {
		at, err = parseLegalFactTime(input.BusinessAt)
		if err != nil {
			return LegalApplicabilityFactSet{}, fault.Invalid("业务时间必须是有效的带时区 RFC3339 时间")
		}
	}
	factCutoffAt := now
	if input.EvidenceCutoffAt != "" {
		factCutoffAt, err = parseLegalFactTime(input.EvidenceCutoffAt)
		if err != nil {
			return LegalApplicabilityFactSet{}, fault.Invalid("证据截止时间必须有效且不晚于当前时间")
		}
	}
	if factCutoffAt.After(now) {
		return LegalApplicabilityFactSet{}, fault.Invalid("证据截止时间必须不晚于当前时间")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return LegalApplicabilityFactSet{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return LegalApplicabilityFactSet{}, err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return LegalApplicabilityFactSet{}, err
	}
	selected, err := resolveLegalApplicabilityFactsTx(ctx, tx, current, caseID, at, factCutoffAt)
	if err != nil {
		return LegalApplicabilityFactSet{}, err
	}
	caseItem, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		return LegalApplicabilityFactSet{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return LegalApplicabilityFactSet{}, err
	}
	if !authorizeCase(caseItem, current, access.Read) {
		return LegalApplicabilityFactSet{}, fault.Forbidden("当前授权不能读取该案件的法律适用事实")
	}
	if err := tx.Commit(ctx); err != nil {
		return LegalApplicabilityFactSet{}, err
	}
	return LegalApplicabilityFactSet{BusinessAt: at.UTC(), EvidenceCutoffAt: factCutoffAt.UTC(), Facts: selected}, nil
}

func resolveLegalApplicabilityFactsTx(ctx context.Context, tx pgx.Tx, user accounts.User, caseID string, businessAt, factCutoffAt time.Time) ([]LegalApplicabilityResolvedFact, error) {
	return legalApp.ResolveApplicability(ctx, newLegalReviewFacts(tx, user, Case{ID: caseID}, false), businessAt, factCutoffAt)
}

type legalApplicabilitySelection = legalModel.LegalApplicabilitySelection

func selectLegalApplicabilityFactVersion(currentVersion int64, versions []LegalApplicabilityFactVersion, businessAt, factCutoffAt time.Time) legalApplicabilitySelection {
	return legalModel.SelectLegalApplicabilityFactVersion(currentVersion, versions, businessAt, factCutoffAt)
}
