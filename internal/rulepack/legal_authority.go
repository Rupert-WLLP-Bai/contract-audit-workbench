package rulepack

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxLegalAuthorities         = 32
	maxLegalApplicabilityFields = 9
	maxLegalValuesPerCondition  = 16
	maxLegalQuoteRunes          = 2000
	maxLegalNormTitleRunes      = 300
)

var legalApplicabilityValues = map[string]map[string]struct{}{
	"buyer_kind":             enumSet("state_organ", "institution", "social_organization", "enterprise", "natural_person"),
	"seller_kind":            enumSet("state_organ", "institution", "social_organization", "enterprise", "natural_person"),
	"buyer_enterprise_size":  enumSet("large", "sme", "not_enterprise"),
	"seller_enterprise_size": enumSet("large", "sme", "not_enterprise"),
	"funding_kind":           enumSet("fiscal", "mixed_with_fiscal", "state_owned_non_fiscal", "private"),
	"procurement_scope":      enumSet("centralized_catalog", "above_limit", "catalog_and_above_limit", "outside_catalog_and_below_limit"),
	"project_kind":           enumSet("construction", "construction_related_goods", "construction_related_services", "standalone_goods", "standalone_services"),
	"contract_clause_kind":   enumSet("standard_form", "negotiated"),
	"transaction_kind":       enumSet("civil_contract", "non_civil"),
}

var legalDocumentTypes = map[string]struct{}{
	"law": {}, "administrative_regulation": {}, "judicial_interpretation": {},
	"normative_notice": {}, "draft": {}, "legislative_plan": {},
}

// LegalAuthorityReference is an exact, authored citation attached to a rule.
// It carries shape and identity only; store validation proves the referenced
// governed article and capture, while review acceptance resolves applicability.
type LegalAuthorityReference struct {
	AuthoritySourceID string                  `json:"authority_source_id"`
	KnowledgeID       string                  `json:"knowledge_id"`
	VersionID         string                  `json:"version_id"`
	ContentSHA256     string                  `json:"content_sha256"`
	SourceID          string                  `json:"source_id"`
	SourceSnapshotID  string                  `json:"source_snapshot_id"`
	NormTitle         string                  `json:"norm_title"`
	NormVersion       string                  `json:"norm_version"`
	ArticleNumber     int                     `json:"article_number"`
	DocumentType      string                  `json:"document_type"`
	SourceSHA256      string                  `json:"source_sha256"`
	ArticleTextSHA256 string                  `json:"article_text_sha256"`
	EffectiveFrom     string                  `json:"effective_from"`
	EffectiveUntil    string                  `json:"effective_until"`
	QuoteSpan         LegalAuthorityQuoteSpan `json:"quote_span"`
	Applicability     LegalApplicability      `json:"applicability"`
}

type LegalAuthorityQuoteSpan struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Quote string `json:"quote"`
}

type LegalApplicability struct {
	AllOf []LegalApplicabilityCondition `json:"all_of"`
}

type LegalApplicabilityCondition struct {
	Field string   `json:"field"`
	OneOf []string `json:"one_of"`
}

func normalizeRuleLegalAuthorities(rule *Rule) error {
	if len(rule.LegalAuthorities) == 0 {
		return nil
	}
	if len(rule.LegalAuthorities) > maxLegalAuthorities {
		return fmt.Errorf("legal_authorities exceeds %d entries", maxLegalAuthorities)
	}
	unique := make(map[string]LegalAuthorityReference, len(rule.LegalAuthorities))
	for i := range rule.LegalAuthorities {
		ref := &rule.LegalAuthorities[i]
		if err := normalizeLegalAuthorityReference(rule, ref); err != nil {
			return fmt.Errorf("legal_authorities[%d]: %w", i, err)
		}
		canonical, err := json.Marshal(ref)
		if err != nil {
			return fmt.Errorf("canonicalize legal authority: %w", err)
		}
		unique[string(canonical)] = *ref
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	refs := make([]LegalAuthorityReference, 0, len(keys))
	for _, key := range keys {
		refs = append(refs, unique[key])
	}
	rule.LegalAuthorities = refs
	return nil
}

func normalizeLegalAuthorityReference(rule *Rule, ref *LegalAuthorityReference) error {
	if !validIdentifier(ref.AuthoritySourceID) || !validIdentifier(ref.KnowledgeID) || !validDigest(ref.VersionID) ||
		!validDigest(ref.ContentSHA256) || !validIdentifier(ref.SourceID) || !validIdentifier(ref.SourceSnapshotID) ||
		!validLegalText(ref.NormTitle, maxLegalNormTitleRunes) || !validIdentifier(ref.NormVersion) || ref.ArticleNumber <= 0 ||
		!validDigest(ref.SourceSHA256) || !validDigest(ref.ArticleTextSHA256) {
		return errors.New("legal reference identity, article, title, or digest is invalid")
	}
	if _, ok := legalDocumentTypes[ref.DocumentType]; !ok {
		return fmt.Errorf("unsupported document_type %q", ref.DocumentType)
	}
	matchedSource := false
	for _, source := range rule.AuthoritySources {
		if source.ID == ref.AuthoritySourceID && source.Kind == "normative" && source.Version == ref.NormVersion && ref.SourceID == source.ID {
			matchedSource = true
			break
		}
	}
	if !matchedSource {
		return errors.New("legal authority must match a normative authority source ID and version")
	}
	if err := normalizeLegalInterval(ref); err != nil {
		return err
	}
	if err := normalizeLegalQuoteSpan(&ref.QuoteSpan); err != nil {
		return err
	}
	return normalizeLegalApplicability(&ref.Applicability)
}

func normalizeLegalInterval(ref *LegalAuthorityReference) error {
	from, err := time.Parse("2006-01-02", ref.EffectiveFrom)
	if err != nil || from.Format("2006-01-02") != ref.EffectiveFrom {
		return errors.New("effective_from must be a canonical YYYY-MM-DD date")
	}
	if ref.EffectiveUntil == "" {
		return nil
	}
	until, err := time.Parse("2006-01-02", ref.EffectiveUntil)
	if err != nil || until.Format("2006-01-02") != ref.EffectiveUntil || !from.Before(until) {
		return errors.New("effective_until must be a later exclusive YYYY-MM-DD date")
	}
	return nil
}

func normalizeLegalQuoteSpan(span *LegalAuthorityQuoteSpan) error {
	if !utf8.ValidString(span.Quote) || strings.TrimSpace(span.Quote) == "" || span.Start < 0 || span.End <= span.Start ||
		span.End-span.Start != utf8.RuneCountInString(span.Quote) || utf8.RuneCountInString(span.Quote) > maxLegalQuoteRunes {
		return errors.New("quote_span must use a nonempty bounded Unicode code-point span")
	}
	return nil
}

func normalizeLegalApplicability(applicability *LegalApplicability) error {
	if len(applicability.AllOf) == 0 || len(applicability.AllOf) > maxLegalApplicabilityFields {
		return fmt.Errorf("applicability.all_of must contain 1 to %d conditions", maxLegalApplicabilityFields)
	}
	conditions := make(map[string]LegalApplicabilityCondition, len(applicability.AllOf))
	fields := make(map[string]string, len(applicability.AllOf))
	for i := range applicability.AllOf {
		condition := &applicability.AllOf[i]
		allowedValues, fieldAllowed := legalApplicabilityValues[condition.Field]
		if !fieldAllowed || len(condition.OneOf) == 0 || len(condition.OneOf) > maxLegalValuesPerCondition {
			return fmt.Errorf("applicability condition %d has unsupported field or empty/oversized one_of", i)
		}
		values := make(map[string]struct{}, len(condition.OneOf))
		for _, value := range condition.OneOf {
			if _, ok := allowedValues[value]; !ok {
				return fmt.Errorf("unsupported %s applicability value %q", condition.Field, value)
			}
			values[value] = struct{}{}
		}
		condition.OneOf = make([]string, 0, len(values))
		for value := range values {
			condition.OneOf = append(condition.OneOf, value)
		}
		sort.Strings(condition.OneOf)
		key, err := json.Marshal(condition)
		if err != nil {
			return err
		}
		keyString := string(key)
		if previous, ok := fields[condition.Field]; ok && previous != keyString {
			return fmt.Errorf("applicability field %q has conflicting conditions", condition.Field)
		}
		fields[condition.Field] = keyString
		conditions[keyString] = *condition
	}
	keys := make([]string, 0, len(conditions))
	for key := range conditions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	applicability.AllOf = make([]LegalApplicabilityCondition, 0, len(keys))
	for _, key := range keys {
		applicability.AllOf = append(applicability.AllOf, conditions[key])
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validLegalText(value string, maxRunes int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes
}

func enumSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
