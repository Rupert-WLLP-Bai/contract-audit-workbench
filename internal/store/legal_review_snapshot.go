package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
	"github.com/jackc/pgx/v5"
)

// FrozenLegalAuthority records one normative rule source and the exact legal
// article and applicability evidence selected when a review was accepted.
// A nil ExactReference is permitted only for an explicit legacy rule gap.
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
	Ref               KnowledgeRef                     `json:"ref"`
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

type legalReuseFingerprintPayload struct {
	EvidenceCutoffMode string                 `json:"evidence_cutoff_mode"`
	ExplicitCutoffAt   string                 `json:"explicit_evidence_cutoff_at,omitempty"`
	Authorities        []FrozenLegalAuthority `json:"legal_authorities"`
}

func prepareLegalKnowledgeReuse(snapshot *ReviewKnowledgeSnapshot) error {
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
	cutoff, err := parseCanonicalLegalCutoff(snapshot.EvidenceCutoffAt)
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
		explicit, err := parseCanonicalLegalCutoff(snapshot.ExplicitEvidenceCutoffAt)
		if err != nil || explicit != cutoff {
			return errors.New("explicit cutoff must equal the canonical frozen cutoff")
		}
		snapshot.ExplicitEvidenceCutoffAt = explicit
	default:
		return errors.New("unknown legal evidence cutoff mode")
	}
	for index := range snapshot.LegalAuthorities {
		want, err := frozenLegalCitationID(snapshot.LegalAuthorities[index])
		if err != nil {
			return err
		}
		if snapshot.LegalAuthorities[index].CitationID == "" {
			snapshot.LegalAuthorities[index].CitationID = want
		} else if snapshot.LegalAuthorities[index].CitationID != want {
			return errors.New("frozen legal citation id mismatch")
		}
	}
	if err := validateFrozenLegalAuthorities(snapshot.LegalAuthorities); err != nil {
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
	fingerprint, err := computeLegalReuseFingerprint(snapshot.EvidenceCutoffMode, snapshot.ExplicitEvidenceCutoffAt, snapshot.LegalAuthorities)
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

func frozenLegalCitationID(authority FrozenLegalAuthority) (string, error) {
	identity := frozenLegalCitationIdentity{RuleID: authority.RuleID, AuthoritySourceID: authority.AuthoritySourceID,
		AuthoritySourceVersion: authority.AuthoritySourceVersion, ExactReference: authority.ExactReference, LegacyGap: authority.LegacyRuleGap}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func validLegalReuseFingerprint(snapshot *ReviewKnowledgeSnapshot) bool {
	if snapshot == nil || snapshot.LegalReuseFingerprint == "" {
		return false
	}
	copy := *snapshot
	if err := prepareLegalKnowledgeReuse(&copy); err != nil {
		return false
	}
	return copy.LegalReuseFingerprint == snapshot.LegalReuseFingerprint
}

func hasLegalReviewSnapshot(snapshot *ReviewKnowledgeSnapshot) bool {
	return snapshot != nil && (len(snapshot.LegalAuthorities) > 0 || snapshot.EvidenceCutoffAt != "" || snapshot.EvidenceCutoffMode != "" ||
		snapshot.ExplicitEvidenceCutoffAt != "" || snapshot.LegalReuseFingerprint != "" || snapshot.LegalAuthorities != nil)
}

func hasNormativeRulePackAuthority(pack *rulepack.Pack) bool {
	if pack == nil {
		return false
	}
	for _, rule := range pack.Manifest().Rules {
		for _, source := range rule.AuthoritySources {
			if source.Kind == "normative" {
				return true
			}
		}
	}
	return false
}

// legacyFrozenAuthoritySemantics is only true for a governed pack and exact
// accepted run snapshot that predates the AUTH03 extension. New reviews with
// unbound normative rules carry explicit legal authority gap entries.
func legacyFrozenAuthoritySemantics(snapshot RunSnapshot, pack *rulepack.Pack) bool {
	return snapshot.RunID != "" && snapshot.GovernedKnowledge != nil && !hasLegalReviewSnapshot(snapshot.GovernedKnowledge) &&
		hasNormativeRulePackAuthority(pack)
}

func computeLegalReuseFingerprint(mode, explicitCutoff string, authorities []FrozenLegalAuthority) (string, error) {
	if mode != "acceptance_time" && mode != "explicit" {
		return "", errors.New("unknown legal evidence cutoff mode")
	}
	if mode == "acceptance_time" {
		explicitCutoff = ""
	} else {
		canonical, err := parseCanonicalLegalCutoff(explicitCutoff)
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

func validateFrozenLegalAuthorities(authorities []FrozenLegalAuthority) error {
	seen := make(map[string]struct{}, len(authorities))
	for _, authority := range authorities {
		citationID, err := frozenLegalCitationID(authority)
		if err != nil || authority.CitationID != citationID {
			return errors.New("frozen legal citation id is invalid")
		}
		if authority.RuleID == "" || authority.AuthoritySourceID == "" || authority.AuthoritySourceVersion == "" ||
			authority.RulePackID == "" || authority.RulePackVersion == "" || !validKnowledgeHash(authority.RulePackContentSHA256) ||
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
			if err := validateFrozenLegalArticle(authority); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFrozenLegalArticle(authority FrozenLegalAuthority) error {
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
		article.Status.CaptureSnapshotID == "" || !validKnowledgeHash(article.Status.CaptureRawResponseSHA256) || article.Status.ObservationID == "" || article.Status.ObservedStatus == "" ||
		article.Status.RegistryURL == "" || article.Status.ObservedAt == "" || !validKnowledgeHash(article.Status.ResponseSHA256) ||
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
func evaluateLegalAuthorityApplicability(references []rulepack.LegalAuthorityReference, resolved LegalApplicabilityFactSet) (LegalApplicabilityEvaluation, error) {
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

// rulepackAuthorityGates converts the exact per-reference result in an
// accepted snapshot into the conservative all_of gate consumed by RulePack.
func rulepackAuthorityGates(snapshot *ReviewKnowledgeSnapshot) ([]rulepack.AuthorityGate, error) {
	if snapshot == nil || len(snapshot.LegalAuthorities) == 0 {
		return nil, nil
	}
	if !validLegalReuseFingerprint(snapshot) {
		return nil, errors.New("frozen legal authority snapshot seal is invalid")
	}
	type key struct{ ruleID, sourceID, sourceVersion string }
	type aggregate struct {
		gate             rulepack.AuthorityGate
		unknown          bool
		unknownWhy       string
		notApplicable    bool
		referenceMissing bool
	}
	byKey := make(map[key]*aggregate)
	for _, authority := range snapshot.LegalAuthorities {
		k := key{authority.RuleID, authority.AuthoritySourceID, authority.AuthoritySourceVersion}
		item := byKey[k]
		if item == nil {
			item = &aggregate{gate: rulepack.AuthorityGate{RuleID: k.ruleID, SourceID: k.sourceID, SourceVersion: k.sourceVersion, Outcome: "applicable"}}
			byKey[k] = item
		}
		if authority.LegacyRuleGap {
			item.referenceMissing = true
			item.unknownWhy = "legal_reference_missing"
			continue
		}
		switch authority.Outcome {
		case "applicable":
		case "not_applicable":
			item.notApplicable = true
		case "unknown":
			item.unknown = true
			reason := authority.OutcomeReason
			if reason == "" {
				reason = "legal_authority_unknown"
			}
			if item.unknownWhy == "" || reason < item.unknownWhy {
				item.unknownWhy = reason
			}
		default:
			return nil, errors.New("frozen legal authority has an invalid outcome")
		}
	}
	keys := make([]key, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ruleID != keys[j].ruleID {
			return keys[i].ruleID < keys[j].ruleID
		}
		if keys[i].sourceID != keys[j].sourceID {
			return keys[i].sourceID < keys[j].sourceID
		}
		return keys[i].sourceVersion < keys[j].sourceVersion
	})
	out := make([]rulepack.AuthorityGate, 0, len(keys))
	for _, k := range keys {
		item := byKey[k]
		if item.referenceMissing {
			item.gate.Outcome = "unknown"
			item.gate.Reason = "legal_reference_missing"
			item.gate.ReferenceMissing = true
		} else if item.notApplicable {
			item.gate.Outcome = "not_applicable"
			item.gate.Reason = "legal_authority_not_applicable"
		} else if item.unknown {
			item.gate.Outcome = "unknown"
			item.gate.Reason = item.unknownWhy
			if item.gate.Reason == "" {
				item.gate.Reason = "legal_authority_unknown"
			}
		}
		out = append(out, item.gate)
	}
	return out, nil
}

func freezeLegalAuthoritiesTx(ctx context.Context, tx pgx.Tx, user accounts.User, item Case, pack *rulepack.Pack, businessAt, evidenceCutoff time.Time) ([]FrozenLegalAuthority, error) {
	if pack == nil {
		return nil, errors.New("nil rule pack while freezing legal authorities")
	}
	if err := validateLegalAuthorityReferences(ctx, tx, item.TenantID, pack); err != nil {
		return nil, err
	}
	resolved, err := resolveLegalApplicabilityFactsTx(ctx, tx, user, item.ID, businessAt, evidenceCutoff)
	if err != nil {
		return nil, err
	}
	factSet := LegalApplicabilityFactSet{BusinessAt: businessAt.UTC(), EvidenceCutoffAt: evidenceCutoff.UTC(), Facts: resolved}
	businessDate, err := legalBusinessDate(businessAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	manifest := pack.Manifest()
	var frozen []FrozenLegalAuthority
	for _, rule := range manifest.Rules {
		if rule.BusinessScope.BusinessLine != item.BusinessLine || rule.BusinessScope.Scenario != item.Scenario || rule.BusinessScope.ContractType != item.ContractType {
			continue
		}
		for _, source := range rule.AuthoritySources {
			if source.Kind != "normative" {
				continue
			}
			refs := make([]rulepack.LegalAuthorityReference, 0, len(rule.LegalAuthorities))
			for _, reference := range rule.LegalAuthorities {
				if reference.AuthoritySourceID == source.ID && reference.NormVersion == source.Version {
					refs = append(refs, reference)
				}
			}
			if len(refs) == 0 {
				frozen = append(frozen, FrozenLegalAuthority{RuleID: rule.RuleID, AuthoritySourceID: source.ID, AuthoritySourceVersion: source.Version,
					RulePackID: manifest.ID, RulePackVersion: manifest.Version, RulePackContentSHA256: pack.Hash(), Outcome: "unknown",
					OutcomeReason: "legal_reference_missing", Conditions: []FrozenLegalApplicabilityCondition{}, SelectedFactVersions: []LegalApplicabilityFactVersion{},
					MissingDependencies: []FrozenLegalDependencyGap{{Kind: "authority", Reason: "legal_reference_missing"}},
					LegacyRuleGap:       true, LegalReviewStatus: "NOT_RUN"})
				continue
			}
			for _, reference := range refs {
				version, _, _, _, err := loadGovernedKnowledge(ctx, tx, item.TenantID, reference.KnowledgeID, reference.VersionID)
				if err != nil || !isPublishableLegalAuthority(version) || version.Ref.ContentSHA256 != reference.ContentSHA256 ||
					version.ApprovedDigest != reference.ContentSHA256 || strings.TrimSpace(version.ApprovalActorID) == "" || strings.TrimSpace(version.ApprovalReason) == "" {
					return nil, fault.Conflict("规范性规则引用的法律条文没有可验证的正式批准")
				}
				metadata := version.Content.LegalMetadata
				if metadata == nil {
					return nil, fault.Conflict("规范性规则引用缺少法律来源元数据")
				}
				capture, err := loadLegalSourceCaptureTx(ctx, tx, item.TenantID, metadata.SourceSnapshotID)
				if err != nil {
					return nil, err
				}
				evaluation, err := evaluateLegalAuthorityApplicability([]rulepack.LegalAuthorityReference{reference}, factSet)
				if err != nil {
					return nil, err
				}
				authority := FrozenLegalAuthority{
					RuleID: rule.RuleID, AuthoritySourceID: source.ID, AuthoritySourceVersion: source.Version,
					RulePackID: manifest.ID, RulePackVersion: manifest.Version, RulePackContentSHA256: pack.Hash(),
					ExactReference: &reference, Outcome: evaluation.Outcome, OutcomeReason: evaluation.OutcomeReason,
					Conditions: evaluation.Conditions, SelectedFactVersions: evaluation.SelectedFactVersions,
					MissingDependencies: evaluation.MissingDependencies, LegalReviewStatus: "NOT_RUN",
				}
				article := FrozenLegalArticle{Ref: version.Ref, ContentSHA256: version.Ref.ContentSHA256, SourceID: metadata.SourceID,
					SourceSnapshotID: metadata.SourceSnapshotID, NormTitle: metadata.NormTitle, NormVersion: metadata.NormVersion,
					ArticleNumber: metadata.ArticleNumber, ArticleLabel: metadata.ArticleLabel, DocumentType: metadata.DocumentType,
					CanonicalBody: version.Content.Body, ArticleTextSHA256: reference.ArticleTextSHA256,
					QuoteSpan: reference.QuoteSpan, EffectiveFrom: metadata.EffectiveFrom, EffectiveUntil: metadata.EffectiveUntil,
					Source: FrozenLegalSourceProvenance{CaptureSnapshotID: capture.CaptureSnapshotID, Provenance: capture.Provenance,
						Publisher: capture.Publisher, Issuer: capture.Issuer, OfficialURL: capture.OfficialURL,
						RawResponseSHA256: capture.RawSHA256, ExtractedFullTextSHA256: capture.ExtractedSHA256,
						ExtractorID: capture.ExtractorID, ExtractorVersion: capture.ExtractorVersion, CanonicalizationVersion: capture.CanonicalVersion,
						CapturedAt: capture.SourceCapturedAt, ResponseStatus: capture.ResponseStatus},
					Status: FrozenLegalStatusProvenance{CaptureSnapshotID: capture.CaptureSnapshotID, CaptureRawResponseSHA256: capture.RawSHA256, ObservationID: capture.StatusID,
						ObservedStatus: capture.Status, RegistryURL: capture.StatusRegistryURL, ObservedAt: capture.StatusObservedAt,
						ResponseSHA256: capture.StatusSHA256, ResponseStatus: capture.StatusHTTPStatus},
				}
				authority.Article = &article
				if reason := legalSourceEvidenceGap(capture, evidenceCutoff); reason != "" {
					authority.Outcome, authority.OutcomeReason = "unknown", reason
					authority.MissingDependencies = append(authority.MissingDependencies, FrozenLegalDependencyGap{Kind: "source_status", Reason: reason})
				} else if capture.Status == "repealed" {
					authority.Outcome, authority.OutcomeReason = "unknown", "historical_transition_basis_required"
					authority.MissingDependencies = append(authority.MissingDependencies, FrozenLegalDependencyGap{Kind: "transition", Reason: "historical_transition_basis_required"})
				} else if businessDate < metadata.EffectiveFrom {
					authority.Outcome, authority.OutcomeReason = "not_applicable", "not_yet_effective"
				} else if metadata.EffectiveUntil != "" && businessDate >= metadata.EffectiveUntil {
					authority.Outcome, authority.OutcomeReason = "unknown", "historical_transition_basis_required"
					authority.MissingDependencies = append(authority.MissingDependencies, FrozenLegalDependencyGap{Kind: "transition", Reason: "historical_transition_basis_required"})
				} else if capture.Status != "effective" {
					authority.Outcome, authority.OutcomeReason = "unknown", "source_status_unverified"
					authority.MissingDependencies = append(authority.MissingDependencies, FrozenLegalDependencyGap{Kind: "source_status", Reason: "source_status_unverified"})
				}
				if err := validateFrozenLegalArticle(authority); err != nil {
					return nil, fault.Conflict("冻结的精确法律条文身份无效")
				}
				frozen = append(frozen, authority)
			}
		}
	}
	return frozen, nil
}

func validateFrozenLegalAuthorityCapturesTx(ctx context.Context, tx pgx.Tx, tenant string, authorities []FrozenLegalAuthority) error {
	for _, authority := range authorities {
		if authority.LegacyRuleGap {
			continue
		}
		if authority.Article == nil || authority.ExactReference == nil {
			return fault.Conflict("冻结的规范性法律引用缺少精确条文")
		}
		frozen := authority.Article
		version, _, _, _, err := loadGovernedKnowledge(ctx, tx, tenant, frozen.Ref.KnowledgeID, frozen.Ref.VersionID)
		if err != nil || version.Ref != frozen.Ref || version.Content.Kind != "legal" || version.Content.Body != frozen.CanonicalBody || version.Content.LegalMetadata == nil {
			return fault.Conflict("冻结的法律条文版本或正文不再匹配")
		}
		metadata := version.Content.LegalMetadata
		if metadata.SourceSnapshotID != frozen.SourceSnapshotID || metadata.SourceID != frozen.SourceID || metadata.NormVersion != frozen.NormVersion ||
			metadata.NormTitle != frozen.NormTitle || metadata.ArticleNumber != frozen.ArticleNumber || metadata.ArticleLabel != frozen.ArticleLabel ||
			metadata.DocumentType != frozen.DocumentType || metadata.EffectiveFrom != frozen.EffectiveFrom || metadata.EffectiveUntil != frozen.EffectiveUntil {
			return fault.Conflict("冻结的法律来源元数据不再匹配")
		}
		sourceCapture, err := loadLegalSourceCaptureTx(ctx, tx, tenant, frozen.Source.CaptureSnapshotID)
		if err != nil || sourceCapture.SourceID != frozen.SourceID || sourceCapture.NormVersion != frozen.NormVersion ||
			sourceCapture.RawSHA256 != frozen.Source.RawResponseSHA256 || sourceCapture.ExtractedSHA256 != frozen.Source.ExtractedFullTextSHA256 ||
			sourceCapture.Publisher != frozen.Source.Publisher || sourceCapture.Issuer != frozen.Source.Issuer || sourceCapture.OfficialURL != frozen.Source.OfficialURL ||
			sourceCapture.Provenance != frozen.Source.Provenance || sourceCapture.ExtractorID != frozen.Source.ExtractorID ||
			sourceCapture.ExtractorVersion != frozen.Source.ExtractorVersion || sourceCapture.CanonicalVersion != frozen.Source.CanonicalizationVersion ||
			!sameTimestamp(frozen.Source.CapturedAt, parseCaptureTime(sourceCapture.SourceCapturedAt)) || sourceCapture.ResponseStatus != frozen.Source.ResponseStatus {
			return fault.Conflict("冻结的法律原始来源快照摘要或元数据不再匹配")
		}
		statusCapture := sourceCapture
		if frozen.Status.CaptureSnapshotID != sourceCapture.CaptureSnapshotID {
			statusCapture, err = loadLegalSourceCaptureTx(ctx, tx, tenant, frozen.Status.CaptureSnapshotID)
			if err != nil || statusCapture.SourceID != frozen.SourceID || statusCapture.NormVersion != frozen.NormVersion ||
				statusCapture.ExtractedSHA256 != sourceCapture.ExtractedSHA256 || statusCapture.SourceCapturedAt == "" {
				return fault.Conflict("冻结的状态观察快照没有相同的规范文本")
			}
			matching := false
			for _, article := range statusCapture.Articles {
				if article.Ref.ArticleNumber == frozen.ArticleNumber && article.FullText == frozen.CanonicalBody && article.TextSHA256 == frozen.ArticleTextSHA256 {
					matching = true
					break
				}
			}
			if !matching {
				return fault.Conflict("冻结的状态观察快照没有相同的引用条文")
			}
		}
		if statusCapture.RawSHA256 != frozen.Status.CaptureRawResponseSHA256 || statusCapture.StatusSHA256 != frozen.Status.ResponseSHA256 ||
			statusCapture.StatusID != frozen.Status.ObservationID || statusCapture.Status != frozen.Status.ObservedStatus ||
			statusCapture.StatusRegistryURL != frozen.Status.RegistryURL || !sameTimestamp(frozen.Status.ObservedAt, parseCaptureTime(statusCapture.StatusObservedAt)) ||
			statusCapture.StatusHTTPStatus != frozen.Status.ResponseStatus {
			return fault.Conflict("冻结的法律状态观察原始摘要或元数据不再匹配")
		}
	}
	return nil
}

// ReadFrozenLegalAuthority returns only a reference present in the specified
// accepted run and attempt. It does not consult current rule pointers, current
// facts, or latest captures, so later changes cannot rewrite old citations.
func (s *Store) ReadFrozenLegalAuthority(ctx context.Context, user accounts.User, caseID, runID, attemptID, citationID string) (FrozenLegalAuthority, error) {
	if ctx == nil || s == nil || s.pool == nil {
		return FrozenLegalAuthority{}, fault.New(503, "governance_unavailable", "制度数据库不可用")
	}
	caseID, runID, attemptID = strings.TrimSpace(caseID), strings.TrimSpace(runID), strings.TrimSpace(attemptID)
	if caseID == "" || runID == "" || attemptID == "" || !isLegalCitationID(citationID) {
		return FrozenLegalAuthority{}, fault.Invalid("读取冻结法律依据需要完整的审查、执行和citation_id")
	}
	current, err := trustedUser(user)
	if err != nil {
		return FrozenLegalAuthority{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FrozenLegalAuthority{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
		return FrozenLegalAuthority{}, err
	}
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return FrozenLegalAuthority{}, err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return FrozenLegalAuthority{}, err
	}
	item, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		return FrozenLegalAuthority{}, err
	}
	if !authorizeCase(item, current, access.Read) {
		return FrozenLegalAuthority{}, fault.NotFound("没有找到对应记录")
	}
	var storedCase string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT r.case_id,s.governed_knowledge
		FROM review_runs r JOIN review_attempts a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id
		JOIN review_run_snapshots s ON s.run_id=r.id
		WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 AND r.id=$4 AND a.id=$5`,
		item.TenantID, item.OwnerOrgID, caseID, runID, attemptID).Scan(&storedCase, &raw)
	if errors.Is(err, pgx.ErrNoRows) || storedCase != caseID {
		return FrozenLegalAuthority{}, fault.NotFound("没有找到对应的冻结审查依据")
	}
	if err != nil {
		return FrozenLegalAuthority{}, err
	}
	frozen, err := decodeGovernedKnowledge(raw)
	if err != nil || frozen == nil {
		return FrozenLegalAuthority{}, fault.Conflict("所选审查没有有效的冻结法律依据")
	}
	sealed, err := sealKnowledgeSnapshot(*frozen)
	if err != nil || sealed.SnapshotSHA256 != frozen.SnapshotSHA256 || !validLegalReuseFingerprint(frozen) {
		return FrozenLegalAuthority{}, fault.Conflict("所选审查的冻结法律依据摘要不一致")
	}
	var selected *FrozenLegalAuthority
	for index := range frozen.LegalAuthorities {
		authority := &frozen.LegalAuthorities[index]
		if authority.CitationID == citationID {
			if selected != nil {
				return FrozenLegalAuthority{}, fault.Conflict("所选审查包含重复的法律引用身份")
			}
			selected = authority
		}
	}
	if selected == nil {
		return FrozenLegalAuthority{}, fault.NotFound("法律引用不属于本次审查")
	}
	if err := validateFrozenLegalAuthorities([]FrozenLegalAuthority{*selected}); err != nil {
		return FrozenLegalAuthority{}, fault.Conflict("所选审查的法律引用身份无效")
	}
	if err := validateFrozenLegalAuthorityCapturesTx(ctx, tx, item.TenantID, []FrozenLegalAuthority{*selected}); err != nil {
		return FrozenLegalAuthority{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return FrozenLegalAuthority{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FrozenLegalAuthority{}, err
	}
	return *selected, nil
}

func isLegalCitationID(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func parseCaptureTime(raw string) time.Time {
	at, _ := time.Parse(time.RFC3339Nano, raw)
	return at
}

func legalSourceEvidenceGap(capture LegalSourceCapture, cutoff time.Time) string {
	sourceAt, sourceErr := parseLegalFactTime(capture.SourceCapturedAt)
	statusAt, statusErr := parseLegalFactTime(capture.StatusObservedAt)
	if sourceErr != nil || sourceAt.After(cutoff) {
		return "source_capture_after_cutoff"
	}
	if statusErr != nil || statusAt.After(cutoff) {
		return "status_capture_after_cutoff"
	}
	if capture.ResponseStatus != 200 || capture.StatusHTTPStatus != 200 || capture.StatusSHA256 == "" || capture.RawSHA256 == "" {
		return "source_capture_unverified"
	}
	return ""
}

func parseCanonicalLegalCutoff(raw string) (string, error) {
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

func canonicalizeLegalCutoffInput(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	at, err := parseLegalFactTime(raw)
	if err != nil {
		return "", errors.New("legal evidence cutoff must be RFC3339")
	}
	return at.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano), nil
}
