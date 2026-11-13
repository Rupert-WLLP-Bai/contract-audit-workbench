// Package model provides Legal-owned frozen values and pure evidence rules.
package model

import (
	knowledgeModel "contractaudit/internal/knowledge/model"
	"contractaudit/internal/rulepack"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type FrozenLegalAuthority struct {
	CitationID             string                              `json:"citation_id"`
	RuleID                 string                              `json:"rule_id"`
	AuthoritySourceID      string                              `json:"authority_source_id"`
	AuthoritySourceVersion string                              `json:"authority_source_version"`
	RulePackID             string                              `json:"rulepack_id"`
	RulePackVersion        string                              `json:"rulepack_version"`
	RulePackContentSHA256  string                              `json:"rulepack_content_sha256"`
	ExactReference         *rulepack.LegalAuthorityReference   `json:"exact_reference,omitempty"`
	Article                *FrozenLegalArticle                 `json:"article,omitempty"`
	Outcome                string                              `json:"outcome"`
	OutcomeReason          string                              `json:"outcome_reason,omitempty"`
	Conditions             []FrozenLegalApplicabilityCondition `json:"conditions"`
	SelectedFactVersions   []LegalApplicabilityFactVersion     `json:"selected_fact_versions"`
	MissingDependencies    []FrozenLegalDependencyGap          `json:"missing_dependencies"`
	LegacyRuleGap          bool                                `json:"legacy_rule_gap,omitempty"`
	LegalReviewStatus      string                              `json:"legal_review_status"`
	Reviewer               *string                             `json:"reviewer"`
}

// FrozenLegalArticle holds canonical article text and the metadata needed to
// identify its immutable official source and status captures. Raw response
// bodies remain in append-only capture rows and are checked by the reader.
type FrozenLegalArticle struct {
	Ref               knowledgeModel.KnowledgeRef      `json:"ref"`
	ContentSHA256     string                           `json:"content_sha256"`
	SourceID          string                           `json:"source_id"`
	SourceSnapshotID  string                           `json:"source_snapshot_id"`
	NormTitle         string                           `json:"norm_title"`
	NormVersion       string                           `json:"norm_version"`
	ArticleNumber     int                              `json:"article_number"`
	ArticleLabel      string                           `json:"article_label"`
	DocumentType      string                           `json:"document_type"`
	CanonicalBody     string                           `json:"canonical_full_article_body"`
	ArticleTextSHA256 string                           `json:"article_text_sha256"`
	QuoteSpan         rulepack.LegalAuthorityQuoteSpan `json:"quote_span"`
	EffectiveFrom     string                           `json:"effective_from"`
	EffectiveUntil    string                           `json:"effective_until,omitempty"`
	Source            FrozenLegalSourceProvenance      `json:"source_provenance"`
	Status            FrozenLegalStatusProvenance      `json:"status_provenance"`
}

type FrozenLegalSourceProvenance struct {
	CaptureSnapshotID       string `json:"source_capture_snapshot_id"`
	Provenance              string `json:"provenance"`
	Publisher               string `json:"publisher"`
	Issuer                  string `json:"issuer"`
	OfficialURL             string `json:"official_url"`
	RawResponseSHA256       string `json:"raw_response_sha256"`
	ExtractedFullTextSHA256 string `json:"extracted_full_text_sha256"`
	ExtractorID             string `json:"extractor_id"`
	ExtractorVersion        string `json:"extractor_version"`
	CanonicalizationVersion string `json:"canonicalization_version"`
	CapturedAt              string `json:"captured_at"`
	ResponseStatus          int    `json:"response_status"`
}

type FrozenLegalStatusProvenance struct {
	CaptureSnapshotID        string `json:"status_observation_capture_snapshot_id"`
	CaptureRawResponseSHA256 string `json:"capture_raw_response_sha256"`
	ObservationID            string `json:"status_observation_id"`
	ObservedStatus           string `json:"observed_status"`
	RegistryURL              string `json:"status_registry_url"`
	ObservedAt               string `json:"observed_at"`
	ResponseSHA256           string `json:"response_sha256"`
	ResponseStatus           int    `json:"response_status"`
}

type FrozenLegalApplicabilityCondition struct {
	Field         string                         `json:"field"`
	OneOf         []string                       `json:"one_of"`
	Outcome       string                         `json:"outcome"`
	Fact          *LegalApplicabilityFactVersion `json:"fact,omitempty"`
	MissingReason string                         `json:"missing_reason,omitempty"`
}

type FrozenLegalDependencyGap struct {
	Dimension string `json:"dimension,omitempty"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
}

type LegalApplicabilityEvaluation struct {
	Outcome              string                              `json:"outcome"`
	OutcomeReason        string                              `json:"outcome_reason,omitempty"`
	Conditions           []FrozenLegalApplicabilityCondition `json:"conditions"`
	SelectedFactVersions []LegalApplicabilityFactVersion     `json:"selected_fact_versions"`
	MissingDependencies  []FrozenLegalDependencyGap          `json:"missing_dependencies"`
}

type LegalReuseFields struct {
	EvidenceCutoffAt, EvidenceCutoffMode, ExplicitEvidenceCutoffAt, LegalReuseFingerprint string
	LegalAuthorities                                                                      []FrozenLegalAuthority
}
type legalReuseFingerprintPayload struct {
	EvidenceCutoffMode string                 `json:"evidence_cutoff_mode"`
	ExplicitCutoffAt   string                 `json:"explicit_evidence_cutoff_at,omitempty"`
	Authorities        []FrozenLegalAuthority `json:"legal_authorities"`
}

func PrepareLegalReuse(snapshot *LegalReuseFields) error {
	if snapshot == nil {
		return errors.New("nil governed knowledge snapshot")
	}
	hasLegal := len(snapshot.LegalAuthorities) > 0
	hasAny := snapshot.EvidenceCutoffAt != "" || snapshot.EvidenceCutoffMode != "" || snapshot.ExplicitEvidenceCutoffAt != "" ||
		snapshot.LegalReuseFingerprint != "" || snapshot.LegalAuthorities != nil
	if !hasAny {
		return nil
	}
	if !hasLegal {
		return errors.New("AUTH03 snapshot fields require at least one frozen authority or gap")
	}
	cutoff, err := ParseCanonicalLegalCutoff(snapshot.EvidenceCutoffAt)
	if err != nil {
		return err
	}
	snapshot.EvidenceCutoffAt = cutoff
	switch snapshot.EvidenceCutoffMode {
	case "acceptance_time":
		if snapshot.ExplicitEvidenceCutoffAt != "" {
			return errors.New("acceptance-time cutoff must not include an explicit timestamp")
		}
	case "explicit":
		explicit, err := ParseCanonicalLegalCutoff(snapshot.ExplicitEvidenceCutoffAt)
		if err != nil || explicit != cutoff {
			return errors.New("explicit cutoff must equal the canonical frozen cutoff")
		}
		snapshot.ExplicitEvidenceCutoffAt = explicit
	default:
		return errors.New("unknown legal evidence cutoff mode")
	}
	for index := range snapshot.LegalAuthorities {
		want, err := FrozenLegalCitationID(snapshot.LegalAuthorities[index])
		if err != nil {
			return err
		}
		if snapshot.LegalAuthorities[index].CitationID == "" {
			snapshot.LegalAuthorities[index].CitationID = want
		} else if snapshot.LegalAuthorities[index].CitationID != want {
			return errors.New("frozen legal citation id mismatch")
		}
	}
	if err := ValidateFrozenLegalAuthorities(snapshot.LegalAuthorities); err != nil {
		return err
	}
	sort.Slice(snapshot.LegalAuthorities, func(i, j int) bool {
		a, b := snapshot.LegalAuthorities[i], snapshot.LegalAuthorities[j]
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		if a.AuthoritySourceID != b.AuthoritySourceID {
			return a.AuthoritySourceID < b.AuthoritySourceID
		}
		return a.AuthoritySourceVersion < b.AuthoritySourceVersion
	})
	fingerprint, err := ComputeLegalReuseFingerprint(snapshot.EvidenceCutoffMode, snapshot.ExplicitEvidenceCutoffAt, snapshot.LegalAuthorities)
	if err != nil {
		return err
	}
	if snapshot.LegalReuseFingerprint != "" && snapshot.LegalReuseFingerprint != fingerprint {
		return errors.New("legal reuse fingerprint mismatch")
	}
	snapshot.LegalReuseFingerprint = fingerprint
	return nil
}

type frozenLegalCitationIdentity struct {
	RuleID                 string                            `json:"rule_id"`
	AuthoritySourceID      string                            `json:"authority_source_id"`
	AuthoritySourceVersion string                            `json:"authority_source_version"`
	ExactReference         *rulepack.LegalAuthorityReference `json:"exact_reference,omitempty"`
	LegacyGap              bool                              `json:"legacy_gap,omitempty"`
}

func FrozenLegalCitationID(authority FrozenLegalAuthority) (string, error) {
	identity := frozenLegalCitationIdentity{RuleID: authority.RuleID, AuthoritySourceID: authority.AuthoritySourceID,
		AuthoritySourceVersion: authority.AuthoritySourceVersion, ExactReference: authority.ExactReference, LegacyGap: authority.LegacyRuleGap}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func ComputeLegalReuseFingerprint(mode, explicitCutoff string, authorities []FrozenLegalAuthority) (string, error) {
	if mode != "acceptance_time" && mode != "explicit" {
		return "", errors.New("unknown legal evidence cutoff mode")
	}
	if mode == "acceptance_time" {
		explicitCutoff = ""
	} else {
		canonical, err := ParseCanonicalLegalCutoff(explicitCutoff)
		if err != nil {
			return "", err
		}
		explicitCutoff = canonical
	}
	canonicalAuthorities := append([]FrozenLegalAuthority(nil), authorities...)
	sort.Slice(canonicalAuthorities, func(i, j int) bool {
		a, b := canonicalAuthorities[i], canonicalAuthorities[j]
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		if a.AuthoritySourceID != b.AuthoritySourceID {
			return a.AuthoritySourceID < b.AuthoritySourceID
		}
		return a.AuthoritySourceVersion < b.AuthoritySourceVersion
	})
	raw, err := json.Marshal(legalReuseFingerprintPayload{EvidenceCutoffMode: mode, ExplicitCutoffAt: explicitCutoff, Authorities: canonicalAuthorities})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func ValidateFrozenLegalAuthorities(authorities []FrozenLegalAuthority) error {
	seen := make(map[string]struct{}, len(authorities))
	for _, authority := range authorities {
		citationID, err := FrozenLegalCitationID(authority)
		if err != nil || authority.CitationID != citationID {
			return errors.New("frozen legal citation id is invalid")
		}
		if authority.RuleID == "" || authority.AuthoritySourceID == "" || authority.AuthoritySourceVersion == "" ||
			authority.RulePackID == "" || authority.RulePackVersion == "" || !knowledgeModel.ValidHash(authority.RulePackContentSHA256) ||
			authority.LegalReviewStatus != "NOT_RUN" || authority.Reviewer != nil {
			return errors.New("frozen legal authority identity or review status is invalid")
		}
		refKey := "legacy-gap"
		if authority.ExactReference != nil {
			raw, err := json.Marshal(authority.ExactReference)
			if err != nil {
				return err
			}
			refKey = string(raw)
		}
		key := authority.RuleID + "\x00" + authority.AuthoritySourceID + "\x00" + authority.AuthoritySourceVersion + "\x00" + refKey
		if _, exists := seen[key]; exists {
			return errors.New("duplicate frozen normative authority")
		}
		seen[key] = struct{}{}
		switch authority.Outcome {
		case "applicable", "not_applicable", "unknown":
		default:
			return fmt.Errorf("invalid frozen legal applicability outcome %q", authority.Outcome)
		}
		if authority.LegacyRuleGap {
			if authority.ExactReference != nil || authority.Article != nil || authority.Outcome != "unknown" || authority.OutcomeReason != "legal_reference_missing" {
				return errors.New("legacy authority gap must remain unknown and have no fabricated citation")
			}
		} else {
			if authority.ExactReference == nil || authority.Article == nil {
				return errors.New("bound authority requires an exact citation and article")
			}
			if err := ValidateFrozenLegalArticle(authority); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateFrozenLegalArticle(authority FrozenLegalAuthority) error {
	ref, article := authority.ExactReference, authority.Article
	if ref.AuthoritySourceID != authority.AuthoritySourceID || ref.SourceID != authority.AuthoritySourceID ||
		ref.NormVersion != authority.AuthoritySourceVersion || ref.KnowledgeID != article.Ref.KnowledgeID ||
		ref.VersionID != article.Ref.VersionID || ref.ContentSHA256 != article.Ref.ContentSHA256 ||
		ref.ContentSHA256 != article.ContentSHA256 || ref.SourceID != article.SourceID ||
		ref.SourceSnapshotID != article.SourceSnapshotID || ref.NormTitle != article.NormTitle ||
		ref.NormVersion != article.NormVersion || ref.ArticleNumber != article.ArticleNumber ||
		ref.DocumentType != article.DocumentType || ref.SourceSHA256 != article.Source.RawResponseSHA256 ||
		ref.ArticleTextSHA256 != article.ArticleTextSHA256 ||
		ref.EffectiveFrom != article.EffectiveFrom || ref.EffectiveUntil != article.EffectiveUntil ||
		ref.QuoteSpan != article.QuoteSpan || article.Source.CaptureSnapshotID != article.SourceSnapshotID ||
		article.Status.CaptureSnapshotID == "" || !knowledgeModel.ValidHash(article.Status.CaptureRawResponseSHA256) || article.Status.ObservationID == "" || article.Status.ObservedStatus == "" ||
		article.Status.RegistryURL == "" || article.Status.ObservedAt == "" || !knowledgeModel.ValidHash(article.Status.ResponseSHA256) ||
		article.Status.ResponseStatus != 200 || article.Source.ExtractedFullTextSHA256 == "" || article.Source.ExtractorID == "" ||
		article.Source.ExtractorVersion == "" || article.Source.CanonicalizationVersion == "" || article.ArticleLabel == "" ||
		article.Ref.ContentSHA256 == "" || article.CanonicalBody == "" {
		return errors.New("frozen legal article does not match its exact authority reference")
	}
	bodyDigest := sha256.Sum256([]byte(article.CanonicalBody))
	if hex.EncodeToString(bodyDigest[:]) != article.ArticleTextSHA256 {
		return errors.New("frozen legal article body or legal review status is invalid")
	}
	runes := []rune(article.CanonicalBody)
	span := ref.QuoteSpan
	if !utf8.ValidString(article.CanonicalBody) || span.Start < 0 || span.End <= span.Start || span.End > len(runes) ||
		string(runes[span.Start:span.End]) != span.Quote {
		return errors.New("frozen legal quote span does not match canonical article text")
	}
	return nil
}

// evaluateLegalAuthorityApplicability combines every authored reference with
// all_of semantics. It only reads the supplied immutable resolution result;
// it never infers a missing value or creates a legal finding.
func EvaluateLegalAuthorityApplicability(references []rulepack.LegalAuthorityReference, resolved LegalApplicabilityFactSet) (LegalApplicabilityEvaluation, error) {
	if len(references) == 0 {
		return LegalApplicabilityEvaluation{}, errors.New("legal applicability requires an exact authority reference")
	}
	byDimension := make(map[string]LegalApplicabilityResolvedFact, len(resolved.Facts))
	for _, fact := range resolved.Facts {
		if _, duplicate := byDimension[fact.Dimension]; duplicate {
			return LegalApplicabilityEvaluation{}, fmt.Errorf("duplicate resolved legal fact dimension %q", fact.Dimension)
		}
		byDimension[fact.Dimension] = fact
	}
	out := LegalApplicabilityEvaluation{
		Outcome: "applicable", Conditions: []FrozenLegalApplicabilityCondition{},
		SelectedFactVersions: []LegalApplicabilityFactVersion{}, MissingDependencies: []FrozenLegalDependencyGap{},
	}
	selected := make(map[string]struct{})
	unknown := false
	for _, reference := range references {
		if len(reference.Applicability.AllOf) == 0 {
			return LegalApplicabilityEvaluation{}, errors.New("legal authority reference has no all_of conditions")
		}
		for _, authored := range reference.Applicability.AllOf {
			condition := FrozenLegalApplicabilityCondition{Field: authored.Field, OneOf: append([]string(nil), authored.OneOf...)}
			resolvedFact, exists := byDimension[authored.Field]
			if !exists || resolvedFact.Fact == nil {
				condition.Outcome, condition.MissingReason = "unknown", "missing_fact"
				unknown = true
				out.MissingDependencies = append(out.MissingDependencies, FrozenLegalDependencyGap{Dimension: authored.Field, Kind: "fact", Reason: "missing_fact"})
				out.Conditions = append(out.Conditions, condition)
				continue
			}
			fact := *resolvedFact.Fact
			condition.Fact = &fact
			factKey := fact.Dimension + "\x00" + fact.FactID + "\x00" + fmt.Sprint(fact.Version)
			if _, ok := selected[factKey]; !ok {
				selected[factKey] = struct{}{}
				out.SelectedFactVersions = append(out.SelectedFactVersions, fact)
			}
			if resolvedFact.UnknownReason != "" || resolvedFact.Status != "established" {
				reason := resolvedFact.UnknownReason
				if reason == "" {
					reason = "insufficient_evidence"
				}
				condition.Outcome, condition.MissingReason = "unknown", reason
				unknown = true
				out.MissingDependencies = append(out.MissingDependencies, FrozenLegalDependencyGap{Dimension: authored.Field, Kind: "fact", Reason: reason})
			} else if containsLegalValue(authored.OneOf, resolvedFact.Value) {
				condition.Outcome = "satisfied"
			} else {
				condition.Outcome = "not_satisfied"
				out.Outcome, out.OutcomeReason = "not_applicable", "condition_not_satisfied"
			}
			out.Conditions = append(out.Conditions, condition)
		}
	}
	if out.Outcome != "not_applicable" && unknown {
		out.Outcome, out.OutcomeReason = "unknown", firstLegalMissingReason(out.MissingDependencies)
	}
	return out, nil
}

func containsLegalValue(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func firstLegalMissingReason(gaps []FrozenLegalDependencyGap) string {
	if len(gaps) == 0 {
		return "missing_fact"
	}
	return gaps[0].Reason
}

func ParseCanonicalLegalCutoff(raw string) (string, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return "", errors.New("legal evidence cutoff is missing or padded")
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || at.IsZero() {
		return "", errors.New("legal evidence cutoff is invalid")
	}
	at = at.UTC().Truncate(time.Microsecond)
	canonical := at.Format(time.RFC3339Nano)
	if raw != canonical {
		return "", errors.New("legal evidence cutoff is not canonical UTC microsecond precision")
	}
	return canonical, nil
}
