package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"
)

// LegalApplicabilityFactInput is the authored request value accepted by the
// existing Store API. Keep its field order and JSON tags stable.
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

var legalApplicabilityValues = map[string]map[string]bool{
	"buyer_kind":             applicabilityValues("state_organ", "institution", "social_organization", "enterprise", "natural_person"),
	"seller_kind":            applicabilityValues("state_organ", "institution", "social_organization", "enterprise", "natural_person"),
	"buyer_enterprise_size":  applicabilityValues("large", "sme", "not_enterprise"),
	"seller_enterprise_size": applicabilityValues("large", "sme", "not_enterprise"),
	"funding_kind":           applicabilityValues("fiscal", "mixed_with_fiscal", "state_owned_non_fiscal", "private"),
	"procurement_scope":      applicabilityValues("centralized_catalog", "above_limit", "catalog_and_above_limit", "outside_catalog_and_below_limit"),
	"project_kind":           applicabilityValues("construction", "construction_related_goods", "construction_related_services", "standalone_goods", "standalone_services"),
	"contract_clause_kind":   applicabilityValues("standard_form", "negotiated"),
	"transaction_kind":       applicabilityValues("civil_contract", "non_civil"),
}

var legalApplicabilityUnknownReasons = applicabilityValues("conflicting_evidence", "uncertain_evidence", "unsupported_basis", "insufficient_evidence", "expired_basis", "stale_party_context", "party_scope_ambiguous", "historical_transition_basis_required")

func applicabilityValues(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

// ApplicabilityDimensions returns a fresh sorted slice, not the mutable rule
// table. It is the single dimension source for validation and resolution.
func ApplicabilityDimensions() []string {
	dimensions := make([]string, 0, len(legalApplicabilityValues))
	for dimension := range legalApplicabilityValues {
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	return dimensions
}

func ValidApplicabilityDimension(dimension string) bool {
	_, ok := legalApplicabilityValues[dimension]
	return ok
}

func NormalizeLegalApplicabilityInput(input LegalApplicabilityFactInput, createdAt time.Time, verifiedBasisRefs json.RawMessage, basisObtainedAt *time.Time) (LegalApplicabilityFactVersion, error) {
	values, ok := legalApplicabilityValues[input.Dimension]
	if !ok {
		return LegalApplicabilityFactVersion{}, errors.New("法律适用事实维度无效")
	}
	if input.Status != "established" && input.Status != "unknown" {
		return LegalApplicabilityFactVersion{}, errors.New("法律适用事实状态无效")
	}
	if strings.TrimSpace(input.BasisNote) == "" || len([]rune(input.BasisNote)) > 2000 {
		return LegalApplicabilityFactVersion{}, errors.New("法律适用事实必须说明依据")
	}
	if createdAt.Location() != time.UTC {
		createdAt = createdAt.UTC()
	}
	createdAt = createdAt.Truncate(time.Microsecond)
	validFrom, validUntil := createdAt, (*time.Time)(nil)
	switch input.TemporalBasis {
	case "current_state":
		if input.ValidFrom != "" || input.ValidUntil != "" {
			return LegalApplicabilityFactVersion{}, errors.New("当前状态的有效起点由服务端记录，不接受回填日期")
		}
	case "historical_interval":
		var err error
		validFrom, err = ParseFactTime(input.ValidFrom)
		if err != nil {
			return LegalApplicabilityFactVersion{}, errors.New("历史事实必须提供明确有效起点")
		}
		until, err := ParseFactTime(input.ValidUntil)
		if err != nil || !validFrom.Before(until) {
			return LegalApplicabilityFactVersion{}, errors.New("历史事实必须提供晚于起点的排他有效终点")
		}
		validFrom = validFrom.Truncate(time.Microsecond)
		until = until.Truncate(time.Microsecond)
		validUntil = &until
		if len(verifiedBasisRefs) == 0 || string(verifiedBasisRefs) == "[]" {
			return LegalApplicabilityFactVersion{}, errors.New("历史事实必须引用已核验的材料依据")
		}
	default:
		return LegalApplicabilityFactVersion{}, errors.New("法律适用事实时间依据无效")
	}
	if basisObtainedAt != nil {
		obtained := basisObtainedAt.UTC().Truncate(time.Microsecond)
		basisObtainedAt = &obtained
		if obtained.After(createdAt) {
			return LegalApplicabilityFactVersion{}, errors.New("依据取得时间不能晚于事实记录时间")
		}
	}
	value, unknownReason := strings.TrimSpace(input.Value), strings.TrimSpace(input.UnknownReason)
	switch input.Status {
	case "established":
		if !values[value] || unknownReason != "" || len(verifiedBasisRefs) == 0 || string(verifiedBasisRefs) == "[]" {
			return LegalApplicabilityFactVersion{}, errors.New("已确认事实必须有枚举值和已核验的材料依据")
		}
	case "unknown":
		if value != "" || !legalApplicabilityUnknownReasons[unknownReason] {
			return LegalApplicabilityFactVersion{}, errors.New("未知事实必须使用受控原因且不能携带结论值")
		}
		if unknownReason != "insufficient_evidence" && (len(verifiedBasisRefs) == 0 || string(verifiedBasisRefs) == "[]") {
			return LegalApplicabilityFactVersion{}, errors.New("该未知原因必须保留已核验的材料依据")
		}
	}
	party := input.Party
	party.Side, party.BindingID, party.SubjectID, party.ScopeNote = strings.TrimSpace(party.Side), strings.TrimSpace(party.BindingID), strings.TrimSpace(party.SubjectID), strings.TrimSpace(party.ScopeNote)
	partyDimension := input.Dimension == "buyer_kind" || input.Dimension == "buyer_enterprise_size" || input.Dimension == "seller_kind" || input.Dimension == "seller_enterprise_size"
	if !partyDimension {
		if party != (LegalApplicabilityPartyPin{}) {
			return LegalApplicabilityFactVersion{}, errors.New("非主体维度不能绑定买方或卖方主体")
		}
	} else {
		anyPin := party.Side != "" || party.BindingID != "" || party.BindingVersion != 0 || party.SubjectID != "" || party.SubjectVersion != 0 || party.ScopeNote != ""
		allPin := party.Side != "" && party.BindingID != "" && party.BindingVersion > 0 && party.SubjectID != "" && party.SubjectVersion > 0 && party.ScopeNote != ""
		if anyPin && !allPin {
			return LegalApplicabilityFactVersion{}, errors.New("主体事实的关联版本必须全部提供或全部省略")
		}
		expectedSide := "buyer"
		if strings.HasPrefix(input.Dimension, "seller_") {
			expectedSide = "seller"
		}
		if allPin && party.Side != expectedSide {
			return LegalApplicabilityFactVersion{}, errors.New("事实主体侧与维度不一致")
		}
		if input.Status == "established" && !allPin {
			return LegalApplicabilityFactVersion{}, errors.New("已确认买卖方事实必须固定案件主体及版本")
		}
	}
	refs, err := canonicalRawJSON(verifiedBasisRefs)
	if err != nil || len(refs) == 0 {
		return LegalApplicabilityFactVersion{}, errors.New("事实依据格式无效")
	}
	return LegalApplicabilityFactVersion{
		Dimension: input.Dimension, Status: input.Status, Value: value, UnknownReason: unknownReason,
		TemporalBasis: input.TemporalBasis, ValidFrom: validFrom, ValidUntil: validUntil,
		BasisObtainedAt: basisObtainedAt, BasisNote: strings.TrimSpace(input.BasisNote), BasisRefs: refs,
		BasisSHA256: digestBytes(refs), Party: party, CreatedAt: createdAt,
	}, nil
}

func ParseFactTime(raw string) (time.Time, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return time.Time{}, errors.New("time missing or padded")
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC().Truncate(time.Microsecond), nil
}

func LegalApplicabilityBasisProvided(raw json.RawMessage) (bool, error) {
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

func LegalApplicabilityRequestDigest(tenantID, orgID, caseID, actorID, key string, input LegalApplicabilityFactInput) (string, error) {
	canonicalInput := input
	hasBasis, err := LegalApplicabilityBasisProvided(input.BasisRefs)
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
	return digestBytes(canonical), nil
}

func SameTimestamp(raw string, stored time.Time) bool {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && parsed.Equal(stored)
}

func canonicalRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	encoded, err := json.Marshal(value)
	return encoded, err
}

func digestBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// VerifyApplicabilityBasisManifest checks the persisted canonical basis hash.
func VerifyApplicabilityBasisManifest(raw []byte, digest string) (json.RawMessage, error) {
	canonical, err := canonicalRawJSON(raw)
	if err != nil || digestBytes(canonical) != digest {
		return nil, errors.New("legal applicability basis digest mismatch")
	}
	return canonical, nil
}
