// Package capabilitypack parses and registers declarative audit capability
// manifests. A manifest may only reference capabilities, connectors and
// comparators that the host has already registered; it never contains or loads
// executable code, scripts or SQL.
package capabilitypack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"unicode/utf8"

	"contractaudit/internal/auditext"
)

// MaxManifestBytes bounds a single manifest. The limit keeps a malformed or
// hostile file from being expanded into memory before validation.
const MaxManifestBytes = 256 * 1024

const (
	// ScenarioTelecomDedicatedLine is the dedicated-line operator scenario.
	ScenarioTelecomDedicatedLine = "telecom_dedicated_line"
	// ScenarioCloudServices is the cloud-services scenario.
	ScenarioCloudServices = "cloud_services"

	// EffectRead is the only capability effect allowed in this release.
	EffectRead = "read"

	// ViewFieldComparison renders a contract field against an external field.
	ViewFieldComparison = "field_comparison"
	// ViewExistingReview reuses the existing review surface.
	ViewExistingReview = "existing_review"
)

var rulePackVersionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,2}$`)

// RulePackRef pins the exact rule pack a capability pack depends on. The
// version accepts the existing telecom compiler version form ("5") as well as
// dotted forms.
type RulePackRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// Capability declares one host-registered capability and, optionally, the
// connector it reads from. ConnectorRef is a deployment-registered identifier,
// never a URL.
type Capability struct {
	ID           string  `json:"id"`
	Effect       string  `json:"effect"`
	ConnectorRef *string `json:"connector_ref"`
}

// Check declares one deterministic check. ExternalField is nil when the check
// does not consume an external observation field.
type Check struct {
	ID            string  `json:"id"`
	ComparatorRef string  `json:"comparator_ref"`
	ContractField string  `json:"contract_field"`
	ExternalField *string `json:"external_field"`
}

// UI declares the review surface and the fields it shows.
type UI struct {
	View   string   `json:"view"`
	Fields []string `json:"fields"`
}

// Manifest is the declarative audit capability pack document.
type Manifest struct {
	SchemaVersion         string       `json:"schema_version"`
	PackID                string       `json:"pack_id"`
	Version               string       `json:"version"`
	HostContractVersion   string       `json:"host_contract_version"`
	DisplayName           string       `json:"display_name"`
	Scenario              string       `json:"scenario"`
	RequiredMaterialRoles []string     `json:"required_material_roles"`
	RulePackRef           RulePackRef  `json:"rule_pack_ref"`
	Capabilities          []Capability `json:"capabilities"`
	Checks                []Check      `json:"checks"`
	UI                    UI           `json:"ui"`
	FixtureSuite          string       `json:"fixture_suite"`
	DataClass             string       `json:"data_class"`
}

// Parse decodes and validates one capability pack manifest.
//
// Parsing is strict: unknown fields, trailing JSON values, empty input and
// documents larger than MaxManifestBytes are rejected. Validation checks shape
// and host compatibility only; registration against the host allow-lists is a
// Registry decision.
func Parse(raw []byte) (Manifest, error) {
	if len(raw) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: capability pack manifest exceeds %d bytes", auditext.ErrInvalidRequest, MaxManifestBytes)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return Manifest{}, fmt.Errorf("%w: empty capability pack manifest", auditext.ErrInvalidRequest)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode capability pack manifest: %v", auditext.ErrInvalidRequest, err)
	}

	var trailing any
	switch err := decoder.Decode(&trailing); {
	case err == nil:
		return Manifest{}, fmt.Errorf("%w: trailing JSON after capability pack manifest", auditext.ErrInvalidRequest)
	case !errors.Is(err, io.EOF):
		return Manifest{}, fmt.Errorf("%w: trailing data after capability pack manifest: %v", auditext.ErrInvalidRequest, err)
	}

	// Struct decoding cannot distinguish a missing nullable field from an
	// explicit null, nor a missing capabilities key from an empty array. Check
	// the required keys and container types against the schema directly.
	if err := checkRequiredJSONFields(raw); err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

var (
	manifestRequiredFields = []string{
		"schema_version",
		"pack_id",
		"version",
		"host_contract_version",
		"display_name",
		"scenario",
		"required_material_roles",
		"rule_pack_ref",
		"capabilities",
		"checks",
		"ui",
		"fixture_suite",
		"data_class",
	}
	capabilityRequiredFields = []string{"id", "effect", "connector_ref"}
	checkRequiredFields      = []string{"id", "comparator_ref", "contract_field", "external_field"}
)

func checkRequiredJSONFields(raw []byte) error {
	document, err := requiredManifestObject(raw)
	if err != nil {
		return err
	}
	if err := requireFields(document, manifestRequiredFields, ""); err != nil {
		return err
	}
	return validateRequiredManifestContainers(document)
}

func requiredManifestObject(raw []byte) (map[string]json.RawMessage, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil || document == nil {
		return nil, fmt.Errorf("%w: capability pack manifest must be a JSON object", auditext.ErrInvalidRequest)
	}
	return document, nil
}

func requireFields(document map[string]json.RawMessage, fields []string, label string) error {
	for _, field := range fields {
		if _, ok := document[field]; !ok {
			if label == "" {
				return fmt.Errorf("%w: missing required field %q", auditext.ErrInvalidRequest, field)
			}
			return fmt.Errorf("%w: missing required field %s.%s", auditext.ErrInvalidRequest, label, field)
		}
	}
	return nil
}

func validateRequiredManifestContainers(document map[string]json.RawMessage) error {
	if _, err := objectFields(document["rule_pack_ref"], "rule_pack_ref", "id", "version"); err != nil {
		return err
	}
	uiFields, err := objectFields(document["ui"], "ui", "view", "fields")
	if err != nil {
		return err
	}
	if err := checkArray(uiFields["fields"], "ui.fields"); err != nil {
		return err
	}
	if err := checkArray(document["required_material_roles"], "required_material_roles"); err != nil {
		return err
	}
	if err := checkArrayObjects(document["capabilities"], "capabilities", capabilityRequiredFields); err != nil {
		return err
	}
	return checkArrayObjects(document["checks"], "checks", checkRequiredFields)
}

func objectFields(raw json.RawMessage, label string, required ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%w: %s must be an object", auditext.ErrInvalidRequest, label)
	}
	if err := requireFields(object, required, label); err != nil {
		return nil, err
	}
	return object, nil
}

func checkArray(raw json.RawMessage, label string) error {
	if isJSONNull(raw) {
		return fmt.Errorf("%w: %s must be an array", auditext.ErrInvalidRequest, label)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("%w: %s must be an array", auditext.ErrInvalidRequest, label)
	}
	return nil
}

func checkArrayObjects(raw json.RawMessage, label string, required []string) error {
	if isJSONNull(raw) {
		return fmt.Errorf("%w: %s must be an array", auditext.ErrInvalidRequest, label)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("%w: %s must be an array", auditext.ErrInvalidRequest, label)
	}
	for index, item := range items {
		if _, err := objectFields(item, fmt.Sprintf("%s[%d]", label, index), required...); err != nil {
			return err
		}
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// Validate checks the manifest against the frozen capability pack schema. It
// does not check whether referenced capabilities, connectors or comparators are
// registered; use Registry.Register for that.
func (m Manifest) Validate() error {
	if err := m.validateIdentity(); err != nil {
		return err
	}
	if err := m.validateDisplay(); err != nil {
		return err
	}
	if err := m.validateBindings(); err != nil {
		return err
	}
	return m.validatePresentation()
}

func (m Manifest) validateIdentity() error {
	if m.SchemaVersion != auditext.CapabilityPackSchemaVersion {
		return fmt.Errorf("%w: schema_version must be %q", auditext.ErrInvalidRequest, auditext.CapabilityPackSchemaVersion)
	}
	if m.HostContractVersion != auditext.HostContractVersion {
		return fmt.Errorf("%w: host_contract_version %q", auditext.ErrVersionMismatch, m.HostContractVersion)
	}
	if err := auditext.ValidateID(m.PackID); err != nil {
		return fmt.Errorf("%w: pack_id %q", auditext.ErrInvalidRequest, m.PackID)
	}
	if err := auditext.ValidateVersion(m.Version); err != nil {
		return fmt.Errorf("%w: pack version %q", auditext.ErrInvalidRequest, m.Version)
	}
	return nil
}

func (m Manifest) validateDisplay() error {
	if m.DisplayName == "" || utf8.RuneCountInString(m.DisplayName) > 256 {
		return fmt.Errorf("%w: display_name", auditext.ErrInvalidRequest)
	}
	switch m.Scenario {
	case ScenarioTelecomDedicatedLine, ScenarioCloudServices:
	default:
		return fmt.Errorf("%w: scenario %q", auditext.ErrInvalidRequest, m.Scenario)
	}
	return nil
}

func (m Manifest) validateBindings() error {
	if err := validateMaterialRoles(m.RequiredMaterialRoles); err != nil {
		return err
	}
	if err := m.RulePackRef.validate(); err != nil {
		return err
	}
	if err := validateCapabilities(m.Capabilities); err != nil {
		return err
	}
	return validateChecks(m.Checks)
}

func (m Manifest) validatePresentation() error {
	if err := m.UI.validate(); err != nil {
		return err
	}
	if err := auditext.ValidateID(m.FixtureSuite); err != nil {
		return fmt.Errorf("%w: fixture_suite %q", auditext.ErrInvalidRequest, m.FixtureSuite)
	}
	if m.DataClass != auditext.DataClassSynthetic {
		return fmt.Errorf("%w: data_class must be %q", auditext.ErrInvalidRequest, auditext.DataClassSynthetic)
	}
	return nil
}

func (r RulePackRef) validate() error {
	if err := auditext.ValidateID(r.ID); err != nil {
		return fmt.Errorf("%w: rule_pack_ref.id %q", auditext.ErrInvalidRequest, r.ID)
	}
	if !rulePackVersionPattern.MatchString(r.Version) {
		return fmt.Errorf("%w: rule_pack_ref.version %q", auditext.ErrInvalidRequest, r.Version)
	}
	return nil
}

func validateMaterialRoles(roles []string) error {
	if len(roles) < 1 || len(roles) > 4 {
		return fmt.Errorf("%w: required_material_roles", auditext.ErrInvalidRequest)
	}
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		switch role {
		case "draft_contract", "procurement", "award", "attachment":
		default:
			return fmt.Errorf("%w: required material role %q", auditext.ErrInvalidRequest, role)
		}
		if _, duplicate := seen[role]; duplicate {
			return fmt.Errorf("%w: duplicate required material role %q", auditext.ErrInvalidRequest, role)
		}
		seen[role] = struct{}{}
	}
	return nil
}

func validateCapabilities(capabilities []Capability) error {
	if len(capabilities) > 16 {
		return fmt.Errorf("%w: too many capabilities", auditext.ErrInvalidRequest)
	}
	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if err := auditext.ValidateID(capability.ID); err != nil {
			return fmt.Errorf("%w: capability id %q", auditext.ErrInvalidRequest, capability.ID)
		}
		if _, duplicate := seen[capability.ID]; duplicate {
			return fmt.Errorf("%w: duplicate capability id %q", auditext.ErrInvalidRequest, capability.ID)
		}
		seen[capability.ID] = struct{}{}
		if capability.Effect != EffectRead {
			return fmt.Errorf("%w: capability %q effect must be %q", auditext.ErrInvalidRequest, capability.ID, EffectRead)
		}
		if capability.ConnectorRef != nil && utf8.RuneCountInString(*capability.ConnectorRef) > 128 {
			return fmt.Errorf("%w: capability %q connector_ref", auditext.ErrInvalidRequest, capability.ID)
		}
	}
	return nil
}

func validateChecks(checks []Check) error {
	if len(checks) < 1 || len(checks) > 16 {
		return fmt.Errorf("%w: checks", auditext.ErrInvalidRequest)
	}
	seen := make(map[string]struct{}, len(checks))
	for _, check := range checks {
		if err := auditext.ValidateID(check.ID); err != nil {
			return fmt.Errorf("%w: check id %q", auditext.ErrInvalidRequest, check.ID)
		}
		if _, duplicate := seen[check.ID]; duplicate {
			return fmt.Errorf("%w: duplicate check id %q", auditext.ErrInvalidRequest, check.ID)
		}
		seen[check.ID] = struct{}{}
		if err := validateCheckFields(check); err != nil {
			return err
		}
	}
	return nil
}

func validateCheckFields(check Check) error {
	if err := auditext.ValidateID(check.ComparatorRef); err != nil {
		return fmt.Errorf("%w: check %q comparator_ref %q", auditext.ErrInvalidRequest, check.ID, check.ComparatorRef)
	}
	if err := auditext.ValidateID(check.ContractField); err != nil {
		return fmt.Errorf("%w: check %q contract_field %q", auditext.ErrInvalidRequest, check.ID, check.ContractField)
	}
	if check.ExternalField != nil && utf8.RuneCountInString(*check.ExternalField) > 128 {
		return fmt.Errorf("%w: check %q external_field", auditext.ErrInvalidRequest, check.ID)
	}
	return nil
}

func (u UI) validate() error {
	switch u.View {
	case ViewFieldComparison, ViewExistingReview:
	default:
		return fmt.Errorf("%w: ui.view %q", auditext.ErrInvalidRequest, u.View)
	}
	if len(u.Fields) < 1 || len(u.Fields) > 16 {
		return fmt.Errorf("%w: ui.fields", auditext.ErrInvalidRequest)
	}
	for _, field := range u.Fields {
		if field == "" || utf8.RuneCountInString(field) > 256 {
			return fmt.Errorf("%w: ui field %q", auditext.ErrInvalidRequest, field)
		}
	}
	return nil
}
