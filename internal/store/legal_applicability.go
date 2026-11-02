package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var legalApplicabilityDimensions = map[string]map[string]bool{
	"buyer_kind":             enumLegalValues("state_organ", "institution", "social_organization", "enterprise", "natural_person"),
	"seller_kind":            enumLegalValues("state_organ", "institution", "social_organization", "enterprise", "natural_person"),
	"buyer_enterprise_size":  enumLegalValues("large", "sme", "not_enterprise"),
	"seller_enterprise_size": enumLegalValues("large", "sme", "not_enterprise"),
	"funding_kind":           enumLegalValues("fiscal", "mixed_with_fiscal", "state_owned_non_fiscal", "private"),
	"procurement_scope":      enumLegalValues("centralized_catalog", "above_limit", "catalog_and_above_limit", "outside_catalog_and_below_limit"),
	"project_kind":           enumLegalValues("construction", "construction_related_goods", "construction_related_services", "standalone_goods", "standalone_services"),
	"contract_clause_kind":   enumLegalValues("standard_form", "negotiated"),
	"transaction_kind":       enumLegalValues("civil_contract", "non_civil"),
}

var legalApplicabilityUnknownReasons = enumLegalValues("conflicting_evidence", "uncertain_evidence", "unsupported_basis", "insufficient_evidence", "expired_basis", "stale_party_context", "party_scope_ambiguous", "historical_transition_basis_required")

func enumLegalValues(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

type LegalApplicabilityPartyPin struct {
	Side           string `json:"side,omitempty"`
	BindingID      string `json:"binding_id,omitempty"`
	BindingVersion int64  `json:"binding_version,omitempty"`
	SubjectID      string `json:"subject_id,omitempty"`
	SubjectVersion int64  `json:"subject_version,omitempty"`
	ScopeNote      string `json:"scope_note,omitempty"`
}

type LegalApplicabilityFactInput struct {
	ExpectedVersion int64                      `json:"expected_version"`
	IdempotencyKey  string                     `json:"idempotency_key"`
	Dimension       string                     `json:"dimension"`
	Status          string                     `json:"status"`
	Value           string                     `json:"value,omitempty"`
	UnknownReason   string                     `json:"unknown_reason,omitempty"`
	TemporalBasis   string                     `json:"temporal_basis"`
	ValidFrom       string                     `json:"valid_from,omitempty"`
	ValidUntil      string                     `json:"valid_until,omitempty"`
	BasisNote       string                     `json:"basis_note"`
	BasisRefs       json.RawMessage            `json:"basis_refs"`
	Party           LegalApplicabilityPartyPin `json:"party,omitempty"`
}

type LegalApplicabilityResolutionInput struct {
	BusinessAt       string `json:"business_at"`
	EvidenceCutoffAt string `json:"evidence_cutoff_at"`
}

// LegalApplicabilityResolvedFact keeps the immutable authored version separate
// from the answer derived for one business time and evidence cutoff.
type LegalApplicabilityResolvedFact struct {
	Dimension     string                         `json:"dimension"`
	Status        string                         `json:"status"`
	Value         string                         `json:"value,omitempty"`
	UnknownReason string                         `json:"unknown_reason,omitempty"`
	Fact          *LegalApplicabilityFactVersion `json:"fact,omitempty"`
}

type LegalApplicabilityFactSet struct {
	BusinessAt       time.Time                        `json:"business_at"`
	EvidenceCutoffAt time.Time                        `json:"evidence_cutoff_at"`
	Facts            []LegalApplicabilityResolvedFact `json:"facts"`
}

// LegalApplicabilityFactVersion is an immutable, server-recorded fact version.
// Value is populated only for established facts; unknown versions carry a
// controlled UnknownReason and still participate in temporal selection.
type LegalApplicabilityFactVersion struct {
	TenantID        string                     `json:"tenant_id,omitempty"`
	OrgID           string                     `json:"org_id,omitempty"`
	CaseID          string                     `json:"case_id,omitempty"`
	FactID          string                     `json:"fact_id"`
	Dimension       string                     `json:"dimension"`
	Version         int64                      `json:"version"`
	Status          string                     `json:"status"`
	Value           string                     `json:"value,omitempty"`
	UnknownReason   string                     `json:"unknown_reason,omitempty"`
	TemporalBasis   string                     `json:"temporal_basis"`
	ValidFrom       time.Time                  `json:"valid_from"`
	ValidUntil      *time.Time                 `json:"valid_until,omitempty"`
	BasisObtainedAt *time.Time                 `json:"basis_obtained_at,omitempty"`
	BasisNote       string                     `json:"basis_note"`
	BasisRefs       json.RawMessage            `json:"basis_refs"`
	BasisSHA256     string                     `json:"basis_sha256"`
	Party           LegalApplicabilityPartyPin `json:"party,omitempty"`
	RequestDigest   string                     `json:"request_digest,omitempty"`
	IdempotencyKey  string                     `json:"idempotency_key,omitempty"`
	ActorUserID     string                     `json:"actor_user_id,omitempty"`
	ActorIssuer     string                     `json:"actor_issuer,omitempty"`
	ActorSubject    string                     `json:"actor_subject,omitempty"`
	CreatedAt       time.Time                  `json:"created_at"`
}

func normalizeLegalApplicabilityInput(input LegalApplicabilityFactInput, createdAt time.Time, verifiedBasisRefs json.RawMessage, basisObtainedAt *time.Time) (LegalApplicabilityFactVersion, error) {
	values, ok := legalApplicabilityDimensions[input.Dimension]
	if !ok {
		return LegalApplicabilityFactVersion{}, fault.Invalid("法律适用事实维度无效")
	}
	if input.Status != "established" && input.Status != "unknown" {
		return LegalApplicabilityFactVersion{}, fault.Invalid("法律适用事实状态无效")
	}
	if strings.TrimSpace(input.BasisNote) == "" || len([]rune(input.BasisNote)) > 2000 {
		return LegalApplicabilityFactVersion{}, fault.Invalid("法律适用事实必须说明依据")
	}
	if createdAt.Location() != time.UTC {
		createdAt = createdAt.UTC()
	}
	createdAt = createdAt.Truncate(time.Microsecond)
	validFrom, validUntil := createdAt, (*time.Time)(nil)
	switch input.TemporalBasis {
	case "current_state":
		if input.ValidFrom != "" || input.ValidUntil != "" {
			return LegalApplicabilityFactVersion{}, fault.Invalid("当前状态的有效起点由服务端记录，不接受回填日期")
		}
	case "historical_interval":
		var err error
		validFrom, err = parseLegalFactTime(input.ValidFrom)
		if err != nil {
			return LegalApplicabilityFactVersion{}, fault.Invalid("历史事实必须提供明确有效起点")
		}
		until, err := parseLegalFactTime(input.ValidUntil)
		if err != nil || !validFrom.Before(until) {
			return LegalApplicabilityFactVersion{}, fault.Invalid("历史事实必须提供晚于起点的排他有效终点")
		}
		validFrom = validFrom.Truncate(time.Microsecond)
		until = until.Truncate(time.Microsecond)
		validUntil = &until
		if len(verifiedBasisRefs) == 0 || string(verifiedBasisRefs) == "[]" {
			return LegalApplicabilityFactVersion{}, fault.Invalid("历史事实必须引用已核验的材料依据")
		}
	default:
		return LegalApplicabilityFactVersion{}, fault.Invalid("法律适用事实时间依据无效")
	}
	if basisObtainedAt != nil {
		obtained := basisObtainedAt.UTC().Truncate(time.Microsecond)
		basisObtainedAt = &obtained
		if obtained.After(createdAt) {
			return LegalApplicabilityFactVersion{}, fault.Invalid("依据取得时间不能晚于事实记录时间")
		}
	}
	value, unknownReason := strings.TrimSpace(input.Value), strings.TrimSpace(input.UnknownReason)
	switch input.Status {
	case "established":
		if !values[value] || unknownReason != "" || len(verifiedBasisRefs) == 0 || string(verifiedBasisRefs) == "[]" {
			return LegalApplicabilityFactVersion{}, fault.Invalid("已确认事实必须有枚举值和已核验的材料依据")
		}
	case "unknown":
		if value != "" || !legalApplicabilityUnknownReasons[unknownReason] {
			return LegalApplicabilityFactVersion{}, fault.Invalid("未知事实必须使用受控原因且不能携带结论值")
		}
		if unknownReason != "insufficient_evidence" && (len(verifiedBasisRefs) == 0 || string(verifiedBasisRefs) == "[]") {
			return LegalApplicabilityFactVersion{}, fault.Invalid("该未知原因必须保留已核验的材料依据")
		}
	}
	party := input.Party
	party.Side, party.BindingID, party.SubjectID, party.ScopeNote = strings.TrimSpace(party.Side), strings.TrimSpace(party.BindingID), strings.TrimSpace(party.SubjectID), strings.TrimSpace(party.ScopeNote)
	partyDimension := input.Dimension == "buyer_kind" || input.Dimension == "buyer_enterprise_size" || input.Dimension == "seller_kind" || input.Dimension == "seller_enterprise_size"
	if !partyDimension {
		if party != (LegalApplicabilityPartyPin{}) {
			return LegalApplicabilityFactVersion{}, fault.Invalid("非主体维度不能绑定买方或卖方主体")
		}
	} else {
		anyPin := party.Side != "" || party.BindingID != "" || party.BindingVersion != 0 || party.SubjectID != "" || party.SubjectVersion != 0 || party.ScopeNote != ""
		allPin := party.Side != "" && party.BindingID != "" && party.BindingVersion > 0 && party.SubjectID != "" && party.SubjectVersion > 0 && party.ScopeNote != ""
		if anyPin && !allPin {
			return LegalApplicabilityFactVersion{}, fault.Invalid("主体事实的关联版本必须全部提供或全部省略")
		}
		expectedSide := "buyer"
		if strings.HasPrefix(input.Dimension, "seller_") {
			expectedSide = "seller"
		}
		if allPin && party.Side != expectedSide {
			return LegalApplicabilityFactVersion{}, fault.Invalid("事实主体侧与维度不一致")
		}
		if input.Status == "established" && !allPin {
			return LegalApplicabilityFactVersion{}, fault.Invalid("已确认买卖方事实必须固定案件主体及版本")
		}
	}
	refs, err := canonicalRawJSON(verifiedBasisRefs)
	if err != nil || len(refs) == 0 {
		return LegalApplicabilityFactVersion{}, fault.Invalid("事实依据格式无效")
	}
	return LegalApplicabilityFactVersion{
		Dimension: input.Dimension, Status: input.Status, Value: value, UnknownReason: unknownReason,
		TemporalBasis: input.TemporalBasis, ValidFrom: validFrom, ValidUntil: validUntil,
		BasisObtainedAt: basisObtainedAt, BasisNote: strings.TrimSpace(input.BasisNote), BasisRefs: refs,
		BasisSHA256: sha256Bytes(refs), Party: party, CreatedAt: createdAt,
	}, nil
}

func parseLegalFactTime(raw string) (time.Time, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return time.Time{}, errors.New("time missing or padded")
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC().Truncate(time.Microsecond), nil
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

func legalApplicabilityRequestDigest(tenantID, orgID, caseID, actorID, key string, input LegalApplicabilityFactInput) (string, error) {
	canonicalInput := input
	hasBasis, err := legalApplicabilityBasisProvided(input.BasisRefs)
	if err != nil {
		return "", err
	}
	if !hasBasis {
		canonicalInput.BasisRefs = json.RawMessage("[]")
	} else {
		refs, err := canonicalRawJSON(input.BasisRefs)
		if err != nil {
			return "", err
		}
		canonicalInput.BasisRefs = refs
	}
	canonical, err := json.Marshal(struct {
		TenantID string                      `json:"tenant_id"`
		OrgID    string                      `json:"org_id"`
		CaseID   string                      `json:"case_id"`
		ActorID  string                      `json:"actor_id"`
		Key      string                      `json:"idempotency_key"`
		Input    LegalApplicabilityFactInput `json:"input"`
	}{tenantID, orgID, caseID, actorID, key, canonicalInput})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func legalApplicabilityBasisProvided(raw json.RawMessage) (bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false, nil
	}
	canonical, err := canonicalRawJSON(raw)
	if err != nil {
		return false, err
	}
	return string(canonical) != "[]", nil
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
	digest, err := legalApplicabilityRequestDigest(accounts.Tenant(current), caseItem.OwnerOrgID, caseID, current.ID, input.IdempotencyKey, input)
	if err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	var replayVersion int64
	err = tx.QueryRow(ctx, `SELECT version FROM case_legal_applicability_fact_versions
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4 AND actor_user_id=$5 AND idempotency_key=$6`,
		accounts.Tenant(current), caseItem.OwnerOrgID, caseID, input.Dimension, current.ID, input.IdempotencyKey).Scan(&replayVersion)
	if err == nil {
		stored, loadErr := loadLegalApplicabilityVersion(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, input.Dimension, replayVersion)
		if loadErr != nil {
			return LegalApplicabilityFactVersion{}, loadErr
		}
		if stored.RequestDigest != digest {
			return LegalApplicabilityFactVersion{}, fault.Conflict("同一幂等键不能用于不同法律适用事实")
		}
		if !authorizeCase(caseItem, current, access.Manage) {
			return LegalApplicabilityFactVersion{}, fault.Forbidden("当前授权不能读取该案件的法律适用事实")
		}
		if err := tx.Commit(ctx); err != nil {
			return LegalApplicabilityFactVersion{}, err
		}
		return stored, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return LegalApplicabilityFactVersion{}, err
	}
	if err := businessWritable(caseItem); err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	stateSnapshot, err := loadState(ctx, tx, true)
	if err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	caseFromState, ok := stateSnapshot.caseByID(caseID)
	if !ok || caseFromState.TenantID != accounts.Tenant(current) || caseFromState.OwnerOrgID != caseItem.OwnerOrgID {
		return LegalApplicabilityFactVersion{}, fault.NotFound("没有找到对应案件")
	}
	refs := json.RawMessage("[]")
	hasBasis, err := legalApplicabilityBasisProvided(input.BasisRefs)
	if err != nil {
		return LegalApplicabilityFactVersion{}, fault.Invalid("法律适用事实依据格式无效")
	}
	if hasBasis {
		verifiedRefs, verifyErr := validateSubjectBasisRefs(stateSnapshot, &caseFromState, input.BasisRefs)
		if verifyErr != nil {
			return LegalApplicabilityFactVersion{}, fault.Invalid("法律适用事实依据不是本案已冻结的材料原文")
		}
		refs = verifiedRefs
	}
	obtainedAt, err := legalFactBasisObtainedAt(stateSnapshot, caseFromState, refs)
	if err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	fact, err := normalizeLegalApplicabilityInput(input, now, refs, obtainedAt)
	if err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	if fact.Party.BindingID != "" {
		if err := validateLegalApplicabilityPartyPin(ctx, tx, current, caseItem, fact); err != nil {
			return LegalApplicabilityFactVersion{}, err
		}
	}
	factID, currentVersion, err := lockLegalApplicabilityFact(ctx, tx, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, input.Dimension)
	if err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	if currentVersion != input.ExpectedVersion {
		return LegalApplicabilityFactVersion{}, fault.Conflict("法律适用事实已变化，请刷新后重试")
	}
	if factID == "" {
		factID, err = newID("laf_")
		if err != nil {
			return LegalApplicabilityFactVersion{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO case_legal_applicability_facts
			(tenant_id,org_id,case_id,fact_id,dimension,current_version,created_by_user_id,created_by_issuer,created_by_subject,created_at)
			VALUES ($1,$2,$3,$4,$5,1,$6,$7,$8,$9)`, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, factID, input.Dimension, current.ID, current.Issuer, current.Subject, now); err != nil {
			return LegalApplicabilityFactVersion{}, err
		}
		currentVersion = 0
	}
	nextVersion := currentVersion + 1
	fact.TenantID, fact.OrgID, fact.CaseID, fact.FactID = accounts.Tenant(current), caseItem.OwnerOrgID, caseID, factID
	fact.Version, fact.RequestDigest, fact.IdempotencyKey, fact.ActorUserID = nextVersion, digest, input.IdempotencyKey, current.ID
	fact.ActorIssuer, fact.ActorSubject = current.Issuer, current.Subject
	if err := insertLegalApplicabilityFactVersion(ctx, tx, fact); err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	if currentVersion > 0 {
		tag, err := tx.Exec(ctx, `UPDATE case_legal_applicability_facts SET current_version=$1 WHERE tenant_id=$2 AND org_id=$3 AND case_id=$4 AND fact_id=$5 AND current_version=$6`, nextVersion, accounts.Tenant(current), caseItem.OwnerOrgID, caseID, factID, currentVersion)
		if err != nil {
			return LegalApplicabilityFactVersion{}, err
		}
		if tag.RowsAffected() != 1 {
			return LegalApplicabilityFactVersion{}, fault.Conflict("法律适用事实已变化，请刷新后重试")
		}
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

func lockLegalApplicabilityFact(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, dimension string) (string, int64, error) {
	var factID string
	var version int64
	err := tx.QueryRow(ctx, `SELECT fact_id,current_version FROM case_legal_applicability_facts
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4 FOR UPDATE`, tenantID, orgID, caseID, dimension).Scan(&factID, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, nil
	}
	return factID, version, err
}

func validateLegalApplicabilityPartyPin(ctx context.Context, tx pgx.Tx, user accounts.User, item Case, fact LegalApplicabilityFactVersion) error {
	pin := fact.Party
	var headVersion, bindingSubjectVersion int64
	var subjectID string
	var role, bindingState string
	var subjectHead int64
	err := tx.QueryRow(ctx, `SELECT h.current_version,v.subject_id,v.subject_version,v.role,v.binding_state,sv.current_version
		FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=h.current_version
		JOIN subjects sv ON sv.tenant_id=v.tenant_id AND sv.subject_id=v.subject_id
		WHERE h.tenant_id=$1 AND h.org_id=$2 AND h.case_id=$3 AND h.binding_id=$4`, accounts.Tenant(user), item.OwnerOrgID, item.ID, pin.BindingID).
		Scan(&headVersion, &subjectID, &bindingSubjectVersion, &role, &bindingState, &subjectHead)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.Conflict("买卖方主体关联已变化，请重新确认事实")
	}
	if err != nil {
		return err
	}
	if headVersion != pin.BindingVersion || subjectID != pin.SubjectID || bindingSubjectVersion != pin.SubjectVersion || subjectHead != pin.SubjectVersion || bindingState != "active" {
		return fault.Conflict("买卖方主体关联已变化，请重新确认事实")
	}
	if !legalPartyRoleMatches(pin.Side, role) {
		return fault.Invalid("事实主体角色与当前案件关联不一致")
	}
	var sameRoleCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=h.current_version
		WHERE h.tenant_id=$1 AND h.org_id=$2 AND h.case_id=$3 AND h.current_version=v.version AND v.role=$4 AND v.binding_state='active'`,
		accounts.Tenant(user), item.OwnerOrgID, item.ID, role).Scan(&sameRoleCount); err != nil {
		return err
	}
	if sameRoleCount > 1 && strings.TrimSpace(pin.ScopeNote) == "" {
		return fault.Invalid("案件存在多个同角色主体，必须说明本事实的主体范围")
	}
	return nil
}

func legalPartyRoleMatches(side, role string) bool {
	if side == "buyer" {
		return role == "customer"
	}
	return side == "seller" && (role == "supplier" || role == "contractor" || role == "service_provider")
}

func verifyLegalApplicabilityBasisManifest(raw []byte, digest string) (json.RawMessage, error) {
	canonical, err := canonicalRawJSON(raw)
	if err != nil || sha256Bytes(canonical) != digest {
		return nil, errors.New("legal applicability basis digest mismatch")
	}
	return canonical, nil
}

func insertLegalApplicabilityFactVersion(ctx context.Context, tx pgx.Tx, fact LegalApplicabilityFactVersion) error {
	var until any
	if fact.ValidUntil != nil {
		until = *fact.ValidUntil
	}
	var obtained any
	if fact.BasisObtainedAt != nil {
		obtained = *fact.BasisObtainedAt
	}
	p := fact.Party
	var side, bindingID, subjectID, scopeNote any
	var bindingVersion, subjectVersion any
	if p.BindingID != "" {
		side, bindingID, bindingVersion, subjectID, subjectVersion, scopeNote = p.Side, p.BindingID, p.BindingVersion, p.SubjectID, p.SubjectVersion, p.ScopeNote
	}
	_, err := tx.Exec(ctx, `INSERT INTO case_legal_applicability_fact_versions
		(tenant_id,org_id,case_id,fact_id,version,dimension,status,value,unknown_reason,temporal_basis,valid_from,valid_until,basis_note,basis_refs,basis_sha256,basis_obtained_at,
		 party_side,party_binding_id,party_binding_version,party_subject_id,party_subject_version,party_scope_note,request_digest,idempotency_key,actor_user_id,actor_issuer,actor_subject,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11,$12,$13,$14::jsonb,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28)`,
		fact.TenantID, fact.OrgID, fact.CaseID, fact.FactID, fact.Version, fact.Dimension, fact.Status, fact.Value, fact.UnknownReason,
		fact.TemporalBasis, fact.ValidFrom, until, fact.BasisNote, string(fact.BasisRefs), fact.BasisSHA256, obtained,
		side, bindingID, bindingVersion, subjectID, subjectVersion, scopeNote, fact.RequestDigest, fact.IdempotencyKey,
		fact.ActorUserID, fact.ActorIssuer, fact.ActorSubject, fact.CreatedAt)
	return err
}

func loadLegalApplicabilityVersion(ctx context.Context, tx pgx.Tx, tenantID, orgID, caseID, dimension string, version int64) (LegalApplicabilityFactVersion, error) {
	var fact LegalApplicabilityFactVersion
	var until, obtained pgtype.Timestamptz
	var side, bindingID, subjectID, scopeNote pgtype.Text
	var bindingVersion, subjectVersion pgtype.Int8
	err := tx.QueryRow(ctx, `SELECT fact_id,version,dimension,status,COALESCE(value,''),COALESCE(unknown_reason,''),temporal_basis,valid_from,valid_until,basis_note,basis_refs,basis_sha256,basis_obtained_at,
		COALESCE(party_side,''),party_binding_id,party_binding_version,party_subject_id,party_subject_version,COALESCE(party_scope_note,''),request_digest,idempotency_key,actor_user_id,actor_issuer,actor_subject,created_at
		FROM case_legal_applicability_fact_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4 AND version=$5`,
		tenantID, orgID, caseID, dimension, version).Scan(&fact.FactID, &fact.Version, &fact.Dimension, &fact.Status, &fact.Value, &fact.UnknownReason, &fact.TemporalBasis,
		&fact.ValidFrom, &until, &fact.BasisNote, &fact.BasisRefs, &fact.BasisSHA256, &obtained,
		&side, &bindingID, &bindingVersion, &subjectID, &subjectVersion, &scopeNote, &fact.RequestDigest, &fact.IdempotencyKey, &fact.ActorUserID, &fact.ActorIssuer, &fact.ActorSubject, &fact.CreatedAt)
	if err != nil {
		return LegalApplicabilityFactVersion{}, err
	}
	fact.ValidFrom = fact.ValidFrom.UTC().Truncate(time.Microsecond)
	fact.CreatedAt = fact.CreatedAt.UTC().Truncate(time.Microsecond)
	fact.TenantID, fact.OrgID, fact.CaseID = tenantID, orgID, caseID
	if until.Valid {
		value := until.Time.UTC().Truncate(time.Microsecond)
		fact.ValidUntil = &value
	}
	if obtained.Valid {
		value := obtained.Time.UTC().Truncate(time.Microsecond)
		fact.BasisObtainedAt = &value
	}
	canonicalBasis, err := verifyLegalApplicabilityBasisManifest(fact.BasisRefs, fact.BasisSHA256)
	if err != nil {
		return LegalApplicabilityFactVersion{}, fault.Conflict("法律适用事实材料依据摘要不匹配")
	}
	fact.BasisRefs = canonicalBasis
	fact.Party = LegalApplicabilityPartyPin{Side: side.String, BindingID: bindingID.String, SubjectID: subjectID.String, ScopeNote: scopeNote.String}
	if bindingVersion.Valid {
		fact.Party.BindingVersion = bindingVersion.Int64
	}
	if subjectVersion.Valid {
		fact.Party.SubjectVersion = subjectVersion.Int64
	}
	return fact, nil
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
	caseItem, err := loadKnowledgeCase(ctx, tx, user, caseID)
	if err != nil {
		return nil, err
	}
	if err := setLocalScope(ctx, tx, func() dbScope { scope := userScope(user); scope.CaseID = caseID; return scope }()); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT f.fact_id,f.dimension,f.current_version,v.version,v.status,COALESCE(v.value,''),COALESCE(v.unknown_reason,''),v.temporal_basis,v.valid_from,v.valid_until,v.basis_obtained_at,v.created_at,v.basis_note,v.basis_refs,v.basis_sha256,
		COALESCE(v.party_side,''),v.party_binding_id,v.party_binding_version,v.party_subject_id,v.party_subject_version,COALESCE(v.party_scope_note,'')
		FROM case_legal_applicability_facts f JOIN case_legal_applicability_fact_versions v USING (tenant_id,org_id,case_id,fact_id,dimension)
		WHERE f.tenant_id=$1 AND f.org_id=$2 AND f.case_id=$3 ORDER BY f.dimension,v.version`, accounts.Tenant(user), caseItem.OwnerOrgID, caseID)
	if err != nil {
		return nil, err
	}
	type factFamily struct {
		head     int64
		versions []LegalApplicabilityFactVersion
	}
	families := make(map[string]*factFamily)
	for rows.Next() {
		var fact LegalApplicabilityFactVersion
		var head int64
		var until, obtained pgtype.Timestamptz
		var side, bindingID, subjectID, scopeNote pgtype.Text
		var bindingVersion, subjectVersion pgtype.Int8
		if err := rows.Scan(&fact.FactID, &fact.Dimension, &head, &fact.Version, &fact.Status, &fact.Value, &fact.UnknownReason,
			&fact.TemporalBasis, &fact.ValidFrom, &until, &obtained, &fact.CreatedAt, &fact.BasisNote, &fact.BasisRefs, &fact.BasisSHA256,
			&side, &bindingID, &bindingVersion, &subjectID, &subjectVersion, &scopeNote); err != nil {
			rows.Close()
			return nil, err
		}
		if until.Valid {
			value := until.Time.UTC().Truncate(time.Microsecond)
			fact.ValidUntil = &value
		}
		if obtained.Valid {
			value := obtained.Time.UTC().Truncate(time.Microsecond)
			fact.BasisObtainedAt = &value
		}
		fact.Party = LegalApplicabilityPartyPin{Side: side.String, BindingID: bindingID.String, SubjectID: subjectID.String, ScopeNote: scopeNote.String}
		if bindingVersion.Valid {
			fact.Party.BindingVersion = bindingVersion.Int64
		}
		if subjectVersion.Valid {
			fact.Party.SubjectVersion = subjectVersion.Int64
		}
		canonicalBasis, err := verifyLegalApplicabilityBasisManifest(fact.BasisRefs, fact.BasisSHA256)
		if err != nil {
			rows.Close()
			return nil, fault.Conflict("法律适用事实材料依据摘要不匹配")
		}
		fact.BasisRefs = canonicalBasis
		fact.ValidFrom = fact.ValidFrom.UTC().Truncate(time.Microsecond)
		fact.CreatedAt = fact.CreatedAt.UTC().Truncate(time.Microsecond)
		fact.TenantID, fact.OrgID, fact.CaseID = accounts.Tenant(user), caseItem.OwnerOrgID, caseID
		family := families[fact.Dimension]
		if family == nil {
			family = &factFamily{head: head}
			families[fact.Dimension] = family
		}
		if family.head != head {
			rows.Close()
			return nil, errors.New("legal applicability head changed within snapshot")
		}
		family.versions = append(family.versions, fact)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	dimensions := make([]string, 0, len(legalApplicabilityDimensions))
	for dimension := range legalApplicabilityDimensions {
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	result := make([]LegalApplicabilityResolvedFact, 0, len(dimensions))
	for _, dimension := range dimensions {
		family := families[dimension]
		if family == nil {
			result = append(result, LegalApplicabilityResolvedFact{Dimension: dimension, Status: "unknown", UnknownReason: "missing_fact"})
			continue
		}
		selection := selectLegalApplicabilityFactVersion(family.head, family.versions, businessAt, factCutoffAt)
		selected := selection.Version
		if selected.Version <= 0 {
			result = append(result, LegalApplicabilityResolvedFact{Dimension: dimension, Status: "unknown", UnknownReason: "missing_fact"})
			continue
		}
		selected.Dimension = dimension
		resolved := LegalApplicabilityResolvedFact{Dimension: dimension, Status: selected.Status, Value: selected.Value, UnknownReason: selection.UnknownReason, Fact: &selected}
		if selection.UnknownReason != "" {
			resolved.Status, resolved.Value = "unknown", ""
		}
		if resolved.Status == "established" && selected.Party.BindingID != "" {
			stale, ambiguous, err := legalApplicabilityPartyStatus(ctx, tx, user, caseItem, selected.Party)
			if err != nil {
				return nil, err
			}
			if stale {
				resolved.Status, resolved.Value, resolved.UnknownReason = "unknown", "", "stale_party_context"
			} else if ambiguous {
				resolved.Status, resolved.Value, resolved.UnknownReason = "unknown", "", "party_scope_ambiguous"
			}
		}
		result = append(result, resolved)
	}
	return result, nil
}

func legalApplicabilityPartyStatus(ctx context.Context, tx pgx.Tx, user accounts.User, item Case, pin LegalApplicabilityPartyPin) (bool, bool, error) {
	var headVersion, pinnedSubjectVersion int64
	var subjectID, role, bindingState string
	err := tx.QueryRow(ctx, `SELECT h.current_version,v.subject_id,v.subject_version,v.role,v.binding_state FROM case_subject_roles h
		JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=h.current_version
		WHERE h.tenant_id=$1 AND h.org_id=$2 AND h.case_id=$3 AND h.binding_id=$4`, accounts.Tenant(user), item.OwnerOrgID, item.ID, pin.BindingID).
		Scan(&headVersion, &subjectID, &pinnedSubjectVersion, &role, &bindingState)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, false, nil
	}
	if err != nil {
		return false, false, err
	}
	var currentSubjectVersion int64
	if err := tx.QueryRow(ctx, `SELECT current_version FROM subjects WHERE tenant_id=$1 AND subject_id=$2`, accounts.Tenant(user), subjectID).Scan(&currentSubjectVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, false, nil
		}
		return false, false, err
	}
	if headVersion != pin.BindingVersion || subjectID != pin.SubjectID || pinnedSubjectVersion != pin.SubjectVersion || currentSubjectVersion != pin.SubjectVersion || bindingState != "active" || !legalPartyRoleMatches(pin.Side, role) {
		return true, false, nil
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM case_subject_roles h JOIN case_subject_role_versions v ON v.tenant_id=h.tenant_id AND v.org_id=h.org_id AND v.case_id=h.case_id AND v.binding_id=h.binding_id AND v.version=h.current_version
		WHERE h.tenant_id=$1 AND h.org_id=$2 AND h.case_id=$3 AND h.current_version=v.version AND v.role=$4 AND v.binding_state='active'`, accounts.Tenant(user), item.OwnerOrgID, item.ID, role).Scan(&count); err != nil {
		return false, false, err
	}
	return false, count > 1 && strings.TrimSpace(pin.ScopeNote) == "", nil
}

type legalApplicabilitySelection struct {
	Version       LegalApplicabilityFactVersion
	UnknownReason string
}

// selectLegalApplicabilityFactVersion selects by business validity first, not
// by append order. Evidence cutoff uses acquisition time; a current-state fact
// also requires its server observation point to be within the cutoff.
func selectLegalApplicabilityFactVersion(currentVersion int64, versions []LegalApplicabilityFactVersion, businessAt, factCutoffAt time.Time) legalApplicabilitySelection {
	var selected *LegalApplicabilityFactVersion
	for i := range versions {
		candidate := &versions[i]
		if candidate.Version <= 0 || candidate.Version > currentVersion ||
			(candidate.BasisObtainedAt != nil && candidate.BasisObtainedAt.After(factCutoffAt)) || candidate.ValidFrom.After(businessAt) ||
			(candidate.TemporalBasis == "current_state" && candidate.ValidFrom.After(factCutoffAt)) {
			continue
		}
		if selected == nil || candidate.ValidFrom.After(selected.ValidFrom) ||
			(candidate.ValidFrom.Equal(selected.ValidFrom) && candidate.Version > selected.Version) {
			selected = candidate
		}
	}
	if selected == nil {
		return legalApplicabilitySelection{UnknownReason: "missing_fact"}
	}
	result := legalApplicabilitySelection{Version: *selected}
	if selected.ValidUntil != nil && !businessAt.Before(*selected.ValidUntil) {
		result.UnknownReason = "expired_basis"
		return result
	}
	if selected.Status != "established" {
		result.UnknownReason = selected.UnknownReason
		if result.UnknownReason == "" {
			result.UnknownReason = "insufficient_evidence"
		}
		return result
	}
	if selected.TemporalBasis == "current_state" && selected.BasisObtainedAt != nil && businessAt.Before(*selected.BasisObtainedAt) {
		result.UnknownReason = "missing_fact"
	}
	return result
}
