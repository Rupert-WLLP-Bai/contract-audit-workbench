// Package rulepack strictly parses immutable rule manifests and evaluates typed,
// evidence-bound facts with deterministic comparators.

package rulepack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion                  = "rulepack/v1"
	RegistrySchema                 = "rulepack-registry/v1"
	VerifierVersion                = "rulepack-validator/v1"
	RelationCompatible    Relation = "compatible"
	RelationConflict      Relation = "conflict"
	RelationInsufficient  Relation = "insufficient"
	RelationNotApplicable Relation = "not_applicable"
)

type Relation string

type State string

const (
	StateDraft     State = "draft"
	StateValidated State = "validated"
	StateApproved  State = "approved"
	StatePublished State = "published"
)

const (
	ComparatorMinimumBandwidth      = "minimum_bandwidth"
	ComparatorMonthlySLA            = "monthly_sla"
	ComparatorAdvancePaymentCeiling = "advance_payment_ceiling"
)

var (
	ErrInvalidManifest = errors.New("invalid rulepack manifest")
	ErrUnauthorized    = errors.New("rulepack execution is not authorized")
	ErrTenantMismatch  = errors.New("rulepack tenant mismatch")
	ErrNotPublished    = errors.New("rulepack is not published")
	ErrNotEffective    = errors.New("rulepack is not effective at the requested time")
	ErrInvalidState    = errors.New("invalid rulepack lifecycle transition")
	ErrIdentity        = errors.New("invalid or tampered rulepack evidence identity")
	ErrVersionRollback = errors.New("rulepack version rollback")
	ErrVersionReuse    = errors.New("rulepack version already exists with different content")
)

var (
	identifierRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	rationalRE   = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]{1,18})?$|^(?:0|[1-9][0-9]*)/(?:[1-9][0-9]*)$`)
)

const maxRationalBytes = 96

// A normalized threshold can expand; longer strings are accepted only in canonical form.
const maxCanonicalRationalBytes = 128

// Manifest is a detached copy of pack content and its hash-bound lifecycle.
// Mutating its slices or lifecycle pointers cannot change a Pack.
type Manifest struct {
	SchemaVersion  string    `json:"schema_version"`
	ID             string    `json:"id"`
	Version        string    `json:"version"`
	TenantID       string    `json:"tenant_id"`
	EffectiveFrom  string    `json:"effective_from"`
	EffectiveUntil string    `json:"effective_until"`
	Rules          []Rule    `json:"rules"`
	Lifecycle      Lifecycle `json:"lifecycle"`
}

type Rule struct {
	RuleID           string            `json:"rule_id"`
	Version          string            `json:"version"`
	Domain           string            `json:"domain"`
	CapabilityID     string            `json:"capability_id"`
	Mandatory        bool              `json:"mandatory"`
	BusinessScope    BusinessScope     `json:"business_scope"`
	TenantID         string            `json:"tenant_id"`
	Comparator       string            `json:"comparator"`
	Threshold        Threshold         `json:"threshold"`
	RequiredFact     RequiredFact      `json:"required_fact"`
	AuthoritySources []AuthoritySource `json:"authority_sources"`
}

type BusinessScope struct {
	BusinessLine string `json:"business_line"`
	Scenario     string `json:"scenario"`
	ContractType string `json:"contract_type"`
}

type Threshold struct {
	Value string `json:"value"`
	Unit  string `json:"unit"`
	Basis string `json:"basis,omitempty"`
}

type RequiredFact struct {
	Dimension         string   `json:"dimension"`
	SourceRoles       []string `json:"source_roles"`
	ObjectRequired    bool     `json:"object_required"`
	UnitFamily        string   `json:"unit_family"`
	MeasurementWindow string   `json:"measurement_window,omitempty"`
	TimeZone          string   `json:"time_zone,omitempty"`
	Calendar          string   `json:"calendar,omitempty"`
	OutageDefinition  string   `json:"outage_definition,omitempty"`
	Exclusions        []string `json:"exclusions"`
	Basis             string   `json:"basis,omitempty"`
}

type AuthoritySource struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

type Lifecycle struct {
	State        State         `json:"state"`
	Verification *Verification `json:"verification,omitempty"`
	Approval     *Approval     `json:"approval,omitempty"`
	Release      *Release      `json:"release,omitempty"`
}

type Verification struct {
	ManifestHash    string `json:"manifest_hash"`
	VerifierVersion string `json:"verifier_version"`
	Passed          bool   `json:"passed"`
}

type Approval struct {
	ManifestHash     string `json:"manifest_hash"`
	VerificationHash string `json:"verification_hash"`
	ActorID          string `json:"actor_id"`
}

type Release struct {
	ManifestHash string `json:"manifest_hash"`
	ApprovalHash string `json:"approval_hash"`
	ReleaseHash  string `json:"release_hash"`
}

type manifestContent struct {
	SchemaVersion  string `json:"schema_version"`
	ID             string `json:"id"`
	Version        string `json:"version"`
	TenantID       string `json:"tenant_id"`
	EffectiveFrom  string `json:"effective_from"`
	EffectiveUntil string `json:"effective_until"`
	Rules          []Rule `json:"rules"`
}

type manifestInput struct {
	SchemaVersion  string     `json:"schema_version"`
	ID             string     `json:"id"`
	Version        string     `json:"version"`
	TenantID       string     `json:"tenant_id"`
	EffectiveFrom  string     `json:"effective_from"`
	EffectiveUntil string     `json:"effective_until"`
	Rules          []Rule     `json:"rules"`
	Lifecycle      *Lifecycle `json:"lifecycle,omitempty"`
}

type Pack struct {
	content   manifestContent
	lifecycle Lifecycle
	hash      string
}

// Parse strictly decodes a draft manifest or a hash-validated lifecycle snapshot.
func Parse(raw []byte) (*Pack, error) {
	var input manifestInput
	if err := decodeStrict(raw, &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := requireRuleMandatoryFields(raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	content := manifestContent{
		SchemaVersion:  input.SchemaVersion,
		ID:             input.ID,
		Version:        input.Version,
		TenantID:       input.TenantID,
		EffectiveFrom:  input.EffectiveFrom,
		EffectiveUntil: input.EffectiveUntil,
		Rules:          input.Rules,
	}
	if err := normalizeAndValidate(&content); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	hash, err := contentHash(content)
	if err != nil {
		return nil, fmt.Errorf("%w: canonicalize: %v", ErrInvalidManifest, err)
	}
	life := Lifecycle{State: StateDraft}
	if input.Lifecycle != nil {
		life = cloneLifecycle(*input.Lifecycle)
	}
	if err := validateLifecycle(life, hash); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	return &Pack{content: content, lifecycle: life, hash: hash}, nil
}

// Hash is the SHA-256 of the normalized rule content, excluding lifecycle records.
func (p *Pack) Hash() string {
	if p == nil {
		return ""
	}
	return p.hash
}

// Manifest returns a deep copy of content and lifecycle data.
func (p *Pack) Manifest() Manifest {
	if p == nil {
		return Manifest{}
	}
	c := cloneContent(p.content)
	return Manifest{
		SchemaVersion:  c.SchemaVersion,
		ID:             c.ID,
		Version:        c.Version,
		TenantID:       c.TenantID,
		EffectiveFrom:  c.EffectiveFrom,
		EffectiveUntil: c.EffectiveUntil,
		Rules:          c.Rules,
		Lifecycle:      cloneLifecycle(p.lifecycle),
	}
}

// Validate returns a validated snapshot bound to this manifest hash.
func (p *Pack) Validate() (*Pack, error) {
	if p == nil || p.lifecycle.State != StateDraft {
		return nil, ErrInvalidState
	}
	life := Lifecycle{
		State:        StateValidated,
		Verification: &Verification{ManifestHash: p.hash, VerifierVersion: VerifierVersion, Passed: true},
	}
	return p.withLifecycle(life), nil
}

// Approve returns an approved snapshot only when the caller names the exact validated hash.
func (p *Pack) Approve(actorID, expectedManifestHash string) (*Pack, error) {
	if p == nil || p.lifecycle.State != StateValidated || p.lifecycle.Verification == nil {
		return nil, ErrInvalidState
	}
	if expectedManifestHash != p.hash {
		return nil, fmt.Errorf("%w: stale manifest hash", ErrInvalidState)
	}
	if !validIdentifier(actorID) {
		return nil, fmt.Errorf("%w: invalid approver identity", ErrInvalidState)
	}
	approval := &Approval{
		ManifestHash:     p.hash,
		VerificationHash: verificationHash(*p.lifecycle.Verification),
		ActorID:          actorID,
	}
	life := Lifecycle{State: StateApproved, Verification: cloneVerification(p.lifecycle.Verification), Approval: approval}
	return p.withLifecycle(life), nil
}

// Publish returns an immutable released snapshot bound to its validated approval.
func (p *Pack) Publish() (*Pack, error) {
	if p == nil || p.lifecycle.State != StateApproved || p.lifecycle.Verification == nil || p.lifecycle.Approval == nil {
		return nil, ErrInvalidState
	}
	approvalHash := approvalHash(*p.lifecycle.Approval)
	life := Lifecycle{
		State:        StatePublished,
		Verification: cloneVerification(p.lifecycle.Verification),
		Approval:     cloneApproval(p.lifecycle.Approval),
		Release:      &Release{ManifestHash: p.hash, ApprovalHash: approvalHash, ReleaseHash: releaseHash(p.hash, approvalHash)},
	}
	return p.withLifecycle(life), nil
}

// Snapshot serializes a detached manifest plus lifecycle attestations for Parse-based reload.
func (p *Pack) Snapshot() ([]byte, error) {
	if p == nil {
		return nil, ErrInvalidManifest
	}
	return json.Marshal(p.Manifest())
}

func (p *Pack) withLifecycle(life Lifecycle) *Pack {
	return &Pack{content: cloneContent(p.content), lifecycle: cloneLifecycle(life), hash: p.hash}
}

func normalizeAndValidate(c *manifestContent) error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", c.SchemaVersion)
	}
	if !validIdentifier(c.ID) || !validIdentifier(c.TenantID) {
		return errors.New("id and tenant_id must be nonempty canonical identifiers")
	}
	version, err := strconv.ParseUint(c.Version, 10, 64)
	if err != nil || version == 0 || strconv.FormatUint(version, 10) != c.Version {
		return errors.New("version must be a positive canonical integer string")
	}
	from, fromTime, err := normalizeTime(c.EffectiveFrom)
	if err != nil {
		return fmt.Errorf("effective_from: %w", err)
	}
	until, untilTime, err := normalizeTime(c.EffectiveUntil)
	if err != nil {
		return fmt.Errorf("effective_until: %w", err)
	}
	if !fromTime.Before(untilTime) {
		return errors.New("effective_until must be later than effective_from")
	}
	c.EffectiveFrom, c.EffectiveUntil = from, until
	if len(c.Rules) == 0 {
		return errors.New("rules must not be empty")
	}
	seenRules := make(map[string]struct{}, len(c.Rules))
	for i := range c.Rules {
		r := &c.Rules[i]
		if err := normalizeRule(r, c.TenantID); err != nil {
			return fmt.Errorf("rule %q: %w", r.RuleID, err)
		}
		if _, exists := seenRules[r.RuleID]; exists {
			return fmt.Errorf("duplicate rule_id %q", r.RuleID)
		}
		seenRules[r.RuleID] = struct{}{}
	}
	sort.Slice(c.Rules, func(i, j int) bool { return c.Rules[i].RuleID < c.Rules[j].RuleID })
	return nil
}

func normalizeRule(r *Rule, packTenant string) error {
	if !validIdentifier(r.RuleID) || !validIdentifier(r.Version) || !validIdentifier(r.CapabilityID) {
		return errors.New("rule_id, version and capability_id must be nonempty canonical identifiers")
	}
	if _, err := strconv.ParseUint(r.Version, 10, 64); err != nil {
		return errors.New("rule version must be a positive integer string")
	} else if strings.HasPrefix(r.Version, "0") || r.Version == "0" {
		return errors.New("rule version must be canonical and positive")
	}
	if !validDomain(r.Domain) {
		return fmt.Errorf("unsupported domain %q", r.Domain)
	}
	if !validIdentifier(r.BusinessScope.BusinessLine) || !validIdentifier(r.BusinessScope.Scenario) || !validIdentifier(r.BusinessScope.ContractType) {
		return errors.New("business_scope fields must be nonempty canonical identifiers")
	}
	if r.TenantID != packTenant {
		return ErrTenantMismatch
	}
	if len(r.AuthoritySources) == 0 {
		return errors.New("authority_sources must not be empty")
	}
	seenAuthority := make(map[string]struct{}, len(r.AuthoritySources))
	for _, authority := range r.AuthoritySources {
		if !validIdentifier(authority.ID) || !validIdentifier(authority.Version) {
			return errors.New("authority source id and version must be nonempty canonical identifiers")
		}
		switch authority.Kind {
		case "synthetic_demo", "normative", "contractual", "internal_policy":
		default:
			return fmt.Errorf("unsupported authority source kind %q", authority.Kind)
		}
		key := authority.ID + "\x00" + authority.Version + "\x00" + authority.Kind
		if _, exists := seenAuthority[key]; exists {
			return fmt.Errorf("duplicate authority source %q", authority.ID)
		}
		seenAuthority[key] = struct{}{}
	}
	sort.Slice(r.AuthoritySources, func(i, j int) bool {
		a, b := r.AuthoritySources[i], r.AuthoritySources[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.Kind < b.Kind
	})
	rational, err := parseThresholdRational(r.Threshold.Value)
	if err != nil {
		return fmt.Errorf("threshold.value: %w", err)
	}
	canonical := rational.RatString()
	if len(canonical) > maxCanonicalRationalBytes {
		return errors.New("threshold.value canonical form exceeds size limit")
	}
	r.Threshold.Value = canonical
	if len(r.RequiredFact.SourceRoles) == 0 {
		return errors.New("required_fact.source_roles must not be empty")
	}
	if err := canonicalStrings(&r.RequiredFact.SourceRoles, "source_roles", false); err != nil {
		return err
	}
	if r.RequiredFact.ObjectRequired != true {
		return errors.New("required_fact.object_required must be true for supported comparators")
	}
	switch r.Comparator {
	case ComparatorMinimumBandwidth:
		if r.Threshold.Unit != "Mbit/s" && r.Threshold.Unit != "Gbit/s" {
			return errors.New("minimum_bandwidth threshold unit must be Mbit/s or Gbit/s")
		}
		if r.Threshold.Basis != "" || r.RequiredFact.Dimension != "minimum_bandwidth" || r.RequiredFact.UnitFamily != "bandwidth_decimal" || hasSLAFields(r.RequiredFact) || r.RequiredFact.Basis != "" {
			return errors.New("minimum_bandwidth required_fact metadata mismatch")
		}
	case ComparatorMonthlySLA:
		if r.Threshold.Unit != "percent" || r.Threshold.Basis != "" || rational.Cmp(big.NewRat(100, 1)) > 0 {
			return errors.New("monthly_sla threshold must be a percent from 0 through 100")
		}
		if r.RequiredFact.Dimension != "monthly_availability" || r.RequiredFact.UnitFamily != "percentage" || r.RequiredFact.MeasurementWindow != "calendar_month" || !validIdentifier(r.RequiredFact.TimeZone) || r.RequiredFact.Calendar != "gregorian" || !validIdentifier(r.RequiredFact.OutageDefinition) || r.RequiredFact.Exclusions == nil || r.RequiredFact.Basis != "" {
			return errors.New("monthly_sla requires calendar-month, time-zone, calendar, outage and explicit exclusions metadata")
		}
		if err := canonicalStrings(&r.RequiredFact.Exclusions, "exclusions", true); err != nil {
			return err
		}
	case ComparatorAdvancePaymentCeiling:
		if r.Threshold.Unit != "ratio" || r.Threshold.Basis != "contract_total" || rational.Cmp(big.NewRat(1, 1)) > 0 || rational.Sign() < 0 {
			return errors.New("advance_payment_ceiling requires a ratio in [0,1] with contract_total basis")
		}
		if r.RequiredFact.Dimension != "advance_payment_ratio" || r.RequiredFact.UnitFamily != "ratio" || r.RequiredFact.Basis != r.Threshold.Basis || hasSLAFields(r.RequiredFact) {
			return errors.New("advance_payment_ceiling required_fact metadata mismatch")
		}
	default:
		return fmt.Errorf("unsupported comparator %q", r.Comparator)
	}
	return nil
}

func hasSLAFields(f RequiredFact) bool {
	return f.MeasurementWindow != "" || f.TimeZone != "" || f.Calendar != "" || f.OutageDefinition != "" || f.Exclusions != nil
}

func validDomain(domain string) bool {
	switch domain {
	case "compliance", "commercial", "technical", "fulfillment", "counterparty":
		return true
	default:
		return false
	}
}

func validIdentifier(value string) bool { return identifierRE.MatchString(value) }

func normalizeTime(value string) (string, time.Time, error) {
	if value == "" {
		return "", time.Time{}, errors.New("timestamp is required")
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", time.Time{}, err
	}
	t = t.UTC()
	return t.Format(time.RFC3339Nano), t, nil
}

func canonicalStrings(values *[]string, field string, allowEmpty bool) error {
	if *values == nil {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("%s must be present", field)
	}
	seen := make(map[string]struct{}, len(*values))
	for _, value := range *values {
		if !validIdentifier(value) {
			return fmt.Errorf("%s contains an invalid identifier", field)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%s contains duplicate %q", field, value)
		}
		seen[value] = struct{}{}
	}
	sort.Strings(*values)
	return nil
}

func contentHash(c manifestContent) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func parseRational(value string) (*big.Rat, error) {
	return parseRationalLimit(value, maxRationalBytes)
}

func parseThresholdRational(value string) (*big.Rat, error) {
	rational, err := parseRationalLimit(value, maxCanonicalRationalBytes)
	if err != nil {
		return nil, err
	}
	if len(value) > maxRationalBytes && rational.RatString() != value {
		return nil, errors.New("threshold.value must use bounded canonical form")
	}
	return rational, nil
}

func parseRationalLimit(value string, limit int) (*big.Rat, error) {
	if len(value) > limit || !rationalRE.MatchString(value) {
		return nil, errors.New("value must be a bounded decimal or integer fraction")
	}
	rat, ok := new(big.Rat).SetString(value)
	if !ok || rat.Sign() < 0 || rat.Num().BitLen() > 256 || rat.Denom().BitLen() > 256 {
		return nil, errors.New("value must be a nonnegative bounded rational")
	}
	return rat, nil
}

func decodeStrict(raw []byte, target any) error {
	if !utf8.Valid(raw) {
		return errors.New("JSON is not valid UTF-8")
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return err
	}
	if err := rejectCaseInsensitiveFieldAliases(raw, reflect.TypeOf(target)); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func rejectCaseInsensitiveFieldAliases(raw []byte, target reflect.Type) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	return validateJSONFieldAliases(target, value)
}

func validateJSONFieldAliases(target reflect.Type, value any) error {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	switch typed := value.(type) {
	case map[string]any:
		if target.Kind() != reflect.Struct {
			return nil
		}
		fields := make(map[string]reflect.Type, target.NumField())
		for index := range target.NumField() {
			field := target.Field(index)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
		for name, child := range typed {
			for canonical := range fields {
				if name != canonical && strings.EqualFold(name, canonical) {
					return fmt.Errorf("field %q must use exact name %q", name, canonical)
				}
			}
			childType, exists := fields[name]
			if !exists {
				continue
			}
			if err := validateJSONFieldAliases(childType, child); err != nil {
				return err
			}
		}
	case []any:
		var element reflect.Type
		if target.Kind() == reflect.Slice || target.Kind() == reflect.Array {
			element = target.Elem()
		}
		if element == nil {
			return nil
		}
		for _, child := range typed {
			if err := validateJSONFieldAliases(element, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireRuleMandatoryFields(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	var rules []json.RawMessage
	if err := json.Unmarshal(root["rules"], &rules); err != nil {
		return err
	}
	for index, rawRule := range rules {
		var rule map[string]json.RawMessage
		if err := json.Unmarshal(rawRule, &rule); err != nil {
			return err
		}
		value, exists := rule["mandatory"]
		value = bytes.TrimSpace(value)
		if !exists || !(bytes.Equal(value, []byte("true")) || bytes.Equal(value, []byte("false"))) {
			return fmt.Errorf("rules[%d].mandatory must be present as a boolean", index)
		}
	}
	return nil
}

func rejectDuplicateKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return errors.New("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return errors.New("unterminated JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func validateLifecycle(life Lifecycle, manifestHash string) error {
	validVerification := func(v *Verification) bool {
		return v != nil && v.ManifestHash == manifestHash && v.VerifierVersion == VerifierVersion && v.Passed
	}
	switch life.State {
	case StateDraft:
		if life.Verification != nil || life.Approval != nil || life.Release != nil {
			return errors.New("draft lifecycle cannot carry attestations")
		}
	case StateValidated:
		if !validVerification(life.Verification) || life.Approval != nil || life.Release != nil {
			return errors.New("invalid validated lifecycle")
		}
	case StateApproved:
		if !validVerification(life.Verification) || life.Approval == nil || life.Release != nil {
			return errors.New("invalid approved lifecycle")
		}
		if life.Approval.ManifestHash != manifestHash || life.Approval.VerificationHash != verificationHash(*life.Verification) || !validIdentifier(life.Approval.ActorID) {
			return errors.New("approval is not bound to this manifest and verification")
		}
	case StatePublished:
		if !validVerification(life.Verification) || life.Approval == nil || life.Release == nil {
			return errors.New("invalid published lifecycle")
		}
		if life.Approval.ManifestHash != manifestHash || life.Approval.VerificationHash != verificationHash(*life.Verification) || !validIdentifier(life.Approval.ActorID) {
			return errors.New("approval is not bound to this manifest and verification")
		}
		approvalDigest := approvalHash(*life.Approval)
		if life.Release.ManifestHash != manifestHash || life.Release.ApprovalHash != approvalDigest || life.Release.ReleaseHash != releaseHash(manifestHash, approvalDigest) {
			return errors.New("release is not bound to this manifest and approval")
		}
	default:
		return fmt.Errorf("unsupported lifecycle state %q", life.State)
	}
	return nil
}

func verificationHash(v Verification) string {
	payload, _ := json.Marshal(v)
	return digestHex(payload)
}

func approvalHash(a Approval) string {
	payload, _ := json.Marshal(a)
	return digestHex(payload)
}

func releaseHash(manifestHash, approvalDigest string) string {
	return digestHex([]byte(manifestHash + "\n" + approvalDigest))
}

func digestHex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func cloneContent(c manifestContent) manifestContent {
	copy := c
	copy.Rules = make([]Rule, len(c.Rules))
	for i, rule := range c.Rules {
		copy.Rules[i] = cloneRule(rule)
	}
	return copy
}

func cloneRule(rule Rule) Rule {
	copy := rule
	copy.RequiredFact.SourceRoles = append([]string(nil), rule.RequiredFact.SourceRoles...)
	if rule.RequiredFact.Exclusions != nil {
		copy.RequiredFact.Exclusions = append([]string{}, rule.RequiredFact.Exclusions...)
	}
	copy.AuthoritySources = append([]AuthoritySource(nil), rule.AuthoritySources...)
	return copy
}

func cloneLifecycle(life Lifecycle) Lifecycle {
	copy := Lifecycle{State: life.State}
	copy.Verification = cloneVerification(life.Verification)
	copy.Approval = cloneApproval(life.Approval)
	if life.Release != nil {
		release := *life.Release
		copy.Release = &release
	}
	return copy
}

func cloneVerification(v *Verification) *Verification {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}

func cloneApproval(a *Approval) *Approval {
	if a == nil {
		return nil
	}
	copy := *a
	return &copy
}
