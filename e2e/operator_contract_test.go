package e2e

// Black-box contract tests for the operator audit extension modules.
//
// These tests only use the frozen auditext DTOs and the public constructors of
// capabilitypack, fulfillment and reviewimpact. They deliberately do not import
// any implementation-internal helper: fixtures under testdata/operatoraudit are
// the independent oracle, and every fixture is synthetic.
//
// No database or network is required.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/auditext"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/fulfillment"
	"contractaudit/internal/reviewimpact"
)

func operatorFixture(t *testing.T, parts ...string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("operator fixture path: runtime.Caller unavailable")
	}
	base := filepath.Join(filepath.Dir(file), "..", "testdata", "operatoraudit")
	return filepath.Join(append([]string{base}, parts...)...)
}

func loadOperatorFixture(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("fixture %s has trailing data: %v", path, err)
	}
}

// ---- golden fixture shapes -------------------------------------------------

type operatorScopeFixture struct {
	TenantID     string `json:"tenant_id"`
	CaseID       string `json:"case_id"`
	ActorID      string `json:"actor_id"`
	GrantVersion string `json:"grant_version"`
}

func (s operatorScopeFixture) scope() auditext.AuthorizedScope {
	return auditext.AuthorizedScope{
		TenantID:     s.TenantID,
		CaseID:       s.CaseID,
		ActorID:      s.ActorID,
		GrantVersion: s.GrantVersion,
	}
}

type operatorEvidenceFixture struct {
	Kind              string    `json:"kind"`
	DocumentVersionID string    `json:"document_version_id"`
	ParsedVersionID   string    `json:"parsed_version_id"`
	Start             int       `json:"start"`
	End               int       `json:"end"`
	Quote             string    `json:"quote"`
	SnapshotID        string    `json:"snapshot_id"`
	RecordID          string    `json:"record_id"`
	FieldPointer      string    `json:"field_pointer"`
	DisplayValue      string    `json:"display_value"`
	ObservedAt        time.Time `json:"observed_at"`
}

func (e operatorEvidenceFixture) evidence(t *testing.T) auditext.EvidenceRef {
	t.Helper()
	switch e.Kind {
	case string(auditext.EvidenceDocument):
		return auditext.EvidenceRef{
			Kind:              auditext.EvidenceDocument,
			DocumentVersionID: e.DocumentVersionID,
			ParsedVersionID:   e.ParsedVersionID,
			Start:             e.Start,
			End:               e.End,
			Quote:             e.Quote,
		}
	case string(auditext.EvidenceExternalSnapshot):
		return auditext.EvidenceRef{
			Kind:         auditext.EvidenceExternalSnapshot,
			SnapshotID:   e.SnapshotID,
			RecordID:     e.RecordID,
			FieldPointer: e.FieldPointer,
			DisplayValue: e.DisplayValue,
			ObservedAt:   e.ObservedAt,
		}
	default:
		t.Fatalf("fixture evidence kind %q is not part of the frozen union", e.Kind)
		return auditext.EvidenceRef{}
	}
}

type operatorFactFixture struct {
	Name         string                    `json:"name"`
	Value        auditext.ExactQuantity    `json:"value"`
	Evidence     []operatorEvidenceFixture `json:"evidence"`
	Uncertain    bool                      `json:"uncertain"`
	OutOfScope   bool                      `json:"out_of_scope"`
	SemanticKind string                    `json:"semantic_kind"`
}

func (f operatorFactFixture) fact(t *testing.T) auditext.Fact {
	t.Helper()
	evidence := make([]auditext.EvidenceRef, 0, len(f.Evidence))
	for _, item := range f.Evidence {
		ref := item.evidence(t)
		if err := ref.Validate(); err != nil {
			t.Fatalf("fixture fact %q evidence: %v", f.Name, err)
		}
		evidence = append(evidence, ref)
	}
	return auditext.Fact{
		Name:         f.Name,
		Value:        f.Value,
		Evidence:     evidence,
		Uncertain:    f.Uncertain,
		OutOfScope:   f.OutOfScope,
		SemanticKind: f.SemanticKind,
	}
}

type operatorFrozenFixture struct {
	Scope                  operatorScopeFixture   `json:"scope"`
	Pack                   auditext.PackRef       `json:"pack"`
	ContractVersion        string                 `json:"contract_version"`
	RulePackVersion        string                 `json:"rule_pack_version"`
	BindingStatus          string                 `json:"binding_status"`
	BindingVersion         string                 `json:"binding_version"`
	SnapshotSetID          string                 `json:"snapshot_set_id"`
	ExternalObjectID       string                 `json:"external_object_id"`
	SourceSystem           string                 `json:"source_system"`
	ModelConfigurationID   string                 `json:"model_configuration_id"`
	PlannerConfigurationID string                 `json:"planner_configuration_id"`
	ParserConfigurationID  string                 `json:"parser_configuration_id"`
	BusinessAt             time.Time              `json:"business_at"`
	Facts                  []operatorFactFixture  `json:"facts"`
	Observations           []auditext.Observation `json:"observations"`
}

func (f operatorFrozenFixture) frozenInput(t *testing.T) auditext.FrozenInput {
	t.Helper()
	facts := make([]auditext.Fact, 0, len(f.Facts))
	for _, item := range f.Facts {
		facts = append(facts, item.fact(t))
	}
	input := auditext.FrozenInput{
		Scope:                  f.Scope.scope(),
		Pack:                   f.Pack,
		ContractVersion:        f.ContractVersion,
		RulePackVersion:        f.RulePackVersion,
		BindingStatus:          auditext.BindingStatus(f.BindingStatus),
		BindingVersion:         f.BindingVersion,
		SnapshotSetID:          f.SnapshotSetID,
		ExternalObjectID:       f.ExternalObjectID,
		SourceSystem:           f.SourceSystem,
		ModelConfigurationID:   f.ModelConfigurationID,
		PlannerConfigurationID: f.PlannerConfigurationID,
		ParserConfigurationID:  f.ParserConfigurationID,
		BusinessAt:             f.BusinessAt,
		Facts:                  facts,
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("fixture frozen input is not valid: %v", err)
	}
	return input
}

type operatorGoldenExpected struct {
	Access                 string   `json:"access"`
	Relation               string   `json:"relation"`
	ReasonCode             string   `json:"reason_code"`
	MachineStatus          string   `json:"machine_status"`
	RequiresHumanReview    bool     `json:"requires_human_review"`
	EvidenceKinds          []string `json:"evidence_kinds"`
	ForbiddenEvidenceKinds []string `json:"forbidden_evidence_kinds"`
	AutoConfirm            bool     `json:"auto_confirm"`
	Error                  bool     `json:"error"`
}

type operatorGoldenCase struct {
	ID        string                 `json:"id"`
	Title     string                 `json:"title"`
	DataClass string                 `json:"data_class"`
	Input     operatorFrozenFixture  `json:"input"`
	Expected  operatorGoldenExpected `json:"expected"`
}

type operatorGoldenFile struct {
	Schema    string               `json:"schema"`
	DataClass string               `json:"data_class"`
	Notice    string               `json:"notice"`
	Cases     []operatorGoldenCase `json:"cases"`
}

// TestOperatorGoldenCases exercises G01-G16 through the public Evaluator.
func TestOperatorGoldenCases(t *testing.T) {
	var file operatorGoldenFile
	loadOperatorFixture(t, operatorFixture(t, "golden-cases.json"), &file)
	if file.DataClass != auditext.DataClassSynthetic {
		t.Fatalf("golden fixture data_class = %q, want %q", file.DataClass, auditext.DataClassSynthetic)
	}
	if len(file.Cases) == 0 {
		t.Fatal("golden fixture contains no cases")
	}

	seen := make(map[string]bool, len(file.Cases))
	evaluator := fulfillment.New()
	for _, tc := range file.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			seen[tc.ID] = true
			if tc.DataClass != auditext.DataClassSynthetic {
				t.Fatalf("case %s data_class = %q, want synthetic", tc.ID, tc.DataClass)
			}
			input := tc.Input.frozenInput(t)
			observations := tc.Input.Observations
			for _, observation := range observations {
				if err := observation.Validate(input.Scope); err != nil {
					t.Fatalf("case %s fixture observation is not valid: %v", tc.ID, err)
				}
			}

			ctx := context.Background()
			if tc.Expected.Access == "deny" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel() // simulates a grant revoked between collection and acceptance
			}
			results, err := evaluator.Evaluate(ctx, input, observations)

			if tc.Expected.Access == "deny" {
				if err == nil {
					t.Fatalf("denied case must return an error, got results %+v", results)
				}
				if !errors.Is(err, context.Canceled) && !errors.Is(err, auditext.ErrUnauthorized) {
					t.Fatalf("denied case error = %v, want context.Canceled or ErrUnauthorized", err)
				}
				if len(results) != 0 {
					t.Fatalf("denied case returned %d business results; denial must not produce a relation", len(results))
				}
				return
			}
			if err != nil {
				t.Fatalf("Evaluate returned error: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("Evaluate returned %d results, want exactly 1", len(results))
			}
			result := results[0]
			if err := result.Validate(); err != nil {
				t.Fatalf("CheckResult.Validate: %v", err)
			}
			if result.CheckID != fulfillment.CheckConfiguredBandwidth {
				t.Fatalf("check id = %q, want %q", result.CheckID, fulfillment.CheckConfiguredBandwidth)
			}
			if got, want := result.Relation, auditext.Relation(tc.Expected.Relation); got != want {
				t.Fatalf("relation = %q, want %q (reason %q)", got, want, result.ReasonCode)
			}
			if result.ReasonCode != tc.Expected.ReasonCode {
				t.Fatalf("reason_code = %q, want %q", result.ReasonCode, tc.Expected.ReasonCode)
			}
			if got, want := result.MachineStatus, auditext.MachineStatus(tc.Expected.MachineStatus); got != want {
				t.Fatalf("machine_status = %q, want %q", got, want)
			}
			if !result.RequiresHumanReview {
				t.Fatalf("requires_human_review = false; machine output must never auto-confirm")
			}
			if tc.Expected.RequiresHumanReview && !result.RequiresHumanReview {
				t.Fatalf("fixture expected requires_human_review")
			}
			assertNoAutoConfirmSurface(t, result)

			kinds := make(map[string]int)
			for _, evidence := range result.Evidence {
				if err := evidence.Validate(); err != nil {
					t.Fatalf("evidence %+v does not validate: %v", evidence, err)
				}
				kinds[string(evidence.Kind)]++
				switch evidence.Kind {
				case auditext.EvidenceDocument:
					if evidence.Quote == "" || evidence.Start < 0 || evidence.End <= evidence.Start {
						t.Fatalf("document evidence is not a real quote range: %+v", evidence)
					}
				case auditext.EvidenceExternalSnapshot:
					if evidence.DisplayValue == "" || !strings.HasPrefix(evidence.FieldPointer, "/") || evidence.ObservedAt.IsZero() {
						t.Fatalf("external snapshot evidence lacks coordinates: %+v", evidence)
					}
				default:
					t.Fatalf("unexpected evidence kind %q", evidence.Kind)
				}
			}
			for _, required := range tc.Expected.EvidenceKinds {
				if kinds[required] == 0 {
					t.Fatalf("missing required %q evidence; got kinds %v", required, kinds)
				}
			}
			for _, forbidden := range tc.Expected.ForbiddenEvidenceKinds {
				if kinds[forbidden] != 0 {
					t.Fatalf("forbidden %q evidence present; got kinds %v", forbidden, kinds)
				}
			}
			if result.Relation == auditext.RelationNotApplicable && len(result.Evidence) == 0 {
				t.Fatalf("not_applicable must carry proof evidence")
			}
			if result.Relation == auditext.RelationConflict || result.Relation == auditext.RelationCompatible {
				if kinds[string(auditext.EvidenceExternalSnapshot)] == 0 {
					t.Fatalf("%s result must cite the external snapshot it compared", result.Relation)
				}
			}
		})
	}
	for number := 1; number <= 16; number++ {
		id := fmt.Sprintf("G%02d", number)
		if !seen[id] {
			t.Fatalf("golden fixture is missing case %s", id)
		}
	}
}

func assertNoAutoConfirmSurface(t *testing.T, result auditext.CheckResult) {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal CheckResult: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal CheckResult surface: %v", err)
	}
	for name := range fields {
		if strings.Contains(strings.ToLower(name), "confirm") {
			t.Fatalf("check result exposes an auto-confirmation field %q; machine output is advice only", name)
		}
	}
	if _, ok := fields["requires_human_review"]; !ok {
		t.Fatalf("check result JSON lacks requires_human_review: %s", raw)
	}
}

// ---- capability pack fixture shapes ---------------------------------------

func TestOperatorCapabilityPackContract(t *testing.T) {
	validRaw, err := os.ReadFile(operatorFixture(t, "packs", "valid", "dedicated-line.json"))
	if err != nil {
		t.Fatalf("read valid manifest: %v", err)
	}
	manifest, err := capabilitypack.Parse(validRaw)
	if err != nil {
		t.Fatalf("Parse(valid) = %v", err)
	}
	if manifest.PackID != "operator.dedicated_line" || manifest.Version != "1.0.0" {
		t.Fatalf("parsed manifest identity = %s@%s", manifest.PackID, manifest.Version)
	}
	if manifest.HostContractVersion != auditext.HostContractVersion {
		t.Fatalf("host contract version = %q, want %q", manifest.HostContractVersion, auditext.HostContractVersion)
	}
	if manifest.DataClass != auditext.DataClassSynthetic {
		t.Fatalf("manifest data_class = %q, want synthetic", manifest.DataClass)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest.Validate = %v", err)
	}

	cloudRaw, err := os.ReadFile(operatorFixture(t, "packs", "valid", "cloud-services.json"))
	if err != nil {
		t.Fatalf("read cloud manifest: %v", err)
	}
	cloud, err := capabilitypack.Parse(cloudRaw)
	if err != nil {
		t.Fatalf("Parse(cloud) = %v", err)
	}
	if cloud.PackID != "operator.cloud_services" {
		t.Fatalf("cloud pack id = %q", cloud.PackID)
	}

	t.Run("LoadDir", func(t *testing.T) {
		loaded, err := capabilitypack.LoadDir(operatorFixture(t, "packs", "valid"))
		if err != nil {
			t.Fatalf("LoadDir(valid) = %v", err)
		}
		if len(loaded) != 2 {
			t.Fatalf("LoadDir(valid) returned %d manifests, want 2", len(loaded))
		}
	})

	t.Run("reject invalid", func(t *testing.T) {
		cases := []struct {
			name    string
			wantErr error
		}{
			{"unknown-field.json", auditext.ErrInvalidRequest},
			{"trailing-json.json", auditext.ErrInvalidRequest},
			{"wrong-version.json", auditext.ErrVersionMismatch},
			{"duplicate-check.json", auditext.ErrInvalidRequest},
		}
		for _, tc := range cases {
			raw, err := os.ReadFile(operatorFixture(t, "packs", "invalid", tc.name))
			if err != nil {
				t.Fatalf("read %s: %v", tc.name, err)
			}
			if _, err := capabilitypack.Parse(raw); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Parse(%s) = %v, want error %v", tc.name, err, tc.wantErr)
			}
		}
	})

	t.Run("registry allow-lists", func(t *testing.T) {
		registry, err := capabilitypack.NewRegistry(capabilitypack.RegistryConfig{
			AllowedCapabilities: []string{"operator.oss.read_config"},
			AllowedConnectors:   []string{"fixture.oss"},
			AllowedComparators:  []string{"builtin.configured_bandwidth_at_least/v1", "existing.minimum_bandwidth"},
		})
		if err != nil {
			t.Fatalf("NewRegistry: %v", err)
		}
		if err := registry.Register(manifest); err != nil {
			t.Fatalf("Register(valid) = %v", err)
		}
		if err := registry.Register(cloud); err != nil {
			t.Fatalf("Register(cloud) = %v", err)
		}
		if got, ok := registry.Get("operator.dedicated_line", "1.0.0"); !ok || got.PackID != manifest.PackID {
			t.Fatalf("Get(exact) = %+v, %v", got, ok)
		}
		if _, ok := registry.Get("operator.dedicated_line", "9.9.9"); ok {
			t.Fatal("Get(unknown version) returned a manifest")
		}
		if list := registry.List(); len(list) != 2 {
			t.Fatalf("List returned %d manifests, want 2", len(list))
		}
		if err := registry.Register(manifest); err == nil {
			t.Fatal("duplicate pack id@version was accepted")
		} else if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("duplicate registration error = %v, want ErrInvalidRequest", err)
		}

		raw, err := os.ReadFile(operatorFixture(t, "packs", "invalid", "unknown-capability.json"))
		if err != nil {
			t.Fatalf("read unknown-capability fixture: %v", err)
		}
		unknown, err := capabilitypack.Parse(raw)
		if err != nil {
			t.Fatalf("unknown-capability manifest is shape-valid and must reach the registry: %v", err)
		}
		if err := registry.Register(unknown); err == nil {
			t.Fatal("manifest with an unregistered capability was accepted")
		} else if !errors.Is(err, auditext.ErrInvalidRequest) {
			t.Fatalf("unknown capability error = %v, want ErrInvalidRequest", err)
		}
		if _, ok := registry.Get("operator.unknown_capability", "1.0.0"); ok {
			t.Fatal("rejected manifest became retrievable")
		}

		strict, err := capabilitypack.NewRegistry(capabilitypack.RegistryConfig{
			AllowedCapabilities: []string{"operator.oss.read_config"},
			AllowedComparators:  []string{"builtin.configured_bandwidth_at_least/v1"},
		})
		if err != nil {
			t.Fatalf("NewRegistry(strict): %v", err)
		}
		if err := strict.Register(manifest); err == nil {
			t.Fatal("manifest referencing an unregistered connector was accepted")
		}
	})

	t.Run("invalid directory rejected", func(t *testing.T) {
		if _, err := capabilitypack.LoadDir(operatorFixture(t, "packs", "invalid")); err == nil {
			t.Fatal("LoadDir accepted a directory containing an invalid manifest")
		}
	})
}

// ---- impact/change fixture shapes -----------------------------------------

type operatorScopeOverride struct {
	TenantID     *string `json:"tenant_id"`
	CaseID       *string `json:"case_id"`
	ActorID      *string `json:"actor_id"`
	GrantVersion *string `json:"grant_version"`
}

type operatorFrozenOverride struct {
	Scope                  *operatorScopeOverride `json:"scope"`
	Pack                   *auditext.PackRef      `json:"pack"`
	ContractVersion        *string                `json:"contract_version"`
	RulePackVersion        *string                `json:"rule_pack_version"`
	BindingStatus          *string                `json:"binding_status"`
	BindingVersion         *string                `json:"binding_version"`
	SnapshotSetID          *string                `json:"snapshot_set_id"`
	ExternalObjectID       *string                `json:"external_object_id"`
	SourceSystem           *string                `json:"source_system"`
	ModelConfigurationID   *string                `json:"model_configuration_id"`
	PlannerConfigurationID *string                `json:"planner_configuration_id"`
	ParserConfigurationID  *string                `json:"parser_configuration_id"`
	BusinessAt             *time.Time             `json:"business_at"`
	Facts                  *[]operatorFactFixture `json:"facts"`
}

func applyFrozenOverride(base operatorFrozenFixture, override operatorFrozenOverride) operatorFrozenFixture {
	if override.Scope != nil {
		if override.Scope.TenantID != nil {
			base.Scope.TenantID = *override.Scope.TenantID
		}
		if override.Scope.CaseID != nil {
			base.Scope.CaseID = *override.Scope.CaseID
		}
		if override.Scope.ActorID != nil {
			base.Scope.ActorID = *override.Scope.ActorID
		}
		if override.Scope.GrantVersion != nil {
			base.Scope.GrantVersion = *override.Scope.GrantVersion
		}
	}
	if override.Pack != nil {
		base.Pack = *override.Pack
	}
	applyString := func(target *string, value *string) {
		if value != nil {
			*target = *value
		}
	}
	applyString(&base.ContractVersion, override.ContractVersion)
	applyString(&base.RulePackVersion, override.RulePackVersion)
	applyString(&base.BindingStatus, override.BindingStatus)
	applyString(&base.BindingVersion, override.BindingVersion)
	applyString(&base.SnapshotSetID, override.SnapshotSetID)
	applyString(&base.ExternalObjectID, override.ExternalObjectID)
	applyString(&base.SourceSystem, override.SourceSystem)
	applyString(&base.ModelConfigurationID, override.ModelConfigurationID)
	applyString(&base.PlannerConfigurationID, override.PlannerConfigurationID)
	applyString(&base.ParserConfigurationID, override.ParserConfigurationID)
	if override.BusinessAt != nil {
		base.BusinessAt = *override.BusinessAt
	}
	if override.Facts != nil {
		base.Facts = *override.Facts
	}
	return base
}

type operatorDependencyFixture struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Version string `json:"version"`
	Present bool   `json:"present"`
}

type operatorDependencySetFixture struct {
	Complete bool                        `json:"complete"`
	Items    []operatorDependencyFixture `json:"items"`
}

type operatorCheckFixture struct {
	CheckID             string                       `json:"check_id"`
	Relation            string                       `json:"relation"`
	ReasonCode          string                       `json:"reason_code"`
	MachineStatus       string                       `json:"machine_status"`
	Evidence            []operatorEvidenceFixture    `json:"evidence"`
	RequiresHumanReview bool                         `json:"requires_human_review"`
	Dependencies        operatorDependencySetFixture `json:"dependencies"`
}

func (c operatorCheckFixture) checkResult(t *testing.T) auditext.CheckResult {
	t.Helper()
	evidence := make([]auditext.EvidenceRef, 0, len(c.Evidence))
	for _, item := range c.Evidence {
		evidence = append(evidence, item.evidence(t))
	}
	items := make([]auditext.Dependency, 0, len(c.Dependencies.Items))
	for _, item := range c.Dependencies.Items {
		items = append(items, auditext.Dependency{
			Kind:    auditext.DependencyKind(item.Kind),
			Ref:     item.Ref,
			Version: item.Version,
			Present: item.Present,
		})
	}
	return auditext.CheckResult{
		CheckID:             c.CheckID,
		Relation:            auditext.Relation(c.Relation),
		ReasonCode:          c.ReasonCode,
		MachineStatus:       auditext.MachineStatus(c.MachineStatus),
		Evidence:            evidence,
		RequiresHumanReview: c.RequiresHumanReview,
		Dependencies:        auditext.DependencySet{Complete: c.Dependencies.Complete, Items: items},
	}
}

func resolveOperatorChecks(defaults, overrides []operatorCheckFixture) []operatorCheckFixture {
	resolved := append([]operatorCheckFixture(nil), defaults...)
	index := make(map[string]int, len(resolved))
	for position, item := range resolved {
		index[item.CheckID] = position
	}
	for _, item := range overrides {
		if position, ok := index[item.CheckID]; ok {
			resolved[position] = item
			continue
		}
		index[item.CheckID] = len(resolved)
		resolved = append(resolved, item)
	}
	return resolved
}

type operatorChangeExpected struct {
	KitAction            string            `json:"kit_action"`
	Fallback             string            `json:"fallback"`
	DependencyComplete   bool              `json:"dependency_complete"`
	Actions              map[string]string `json:"actions"`
	AllActions           string            `json:"all_actions"`
	ForbidReuse          bool              `json:"forbid_reuse"`
	ChangedInputsInclude []string          `json:"changed_inputs_include"`
	BusinessResultSource string            `json:"business_result_source"`
}

type operatorChangeCase struct {
	ID           string                 `json:"id"`
	Title        string                 `json:"title"`
	DataClass    string                 `json:"data_class"`
	Change       string                 `json:"change"`
	Base         operatorFrozenOverride `json:"base"`
	Target       operatorFrozenOverride `json:"target"`
	BaseChecks   []operatorCheckFixture `json:"base_checks"`
	TargetChecks []operatorCheckFixture `json:"target_checks"`
	Expected     operatorChangeExpected `json:"expected"`
}

type operatorChangeDefaults struct {
	Frozen operatorFrozenOverride `json:"frozen"`
	Checks []operatorCheckFixture `json:"checks"`
}

type operatorChangeFile struct {
	Schema    string                 `json:"schema"`
	DataClass string                 `json:"data_class"`
	Notice    string                 `json:"notice"`
	Defaults  operatorChangeDefaults `json:"defaults"`
	Cases     []operatorChangeCase   `json:"cases"`
}

// TestOperatorImpactPlan exercises D01-D12 through the public Planner.
func TestOperatorImpactPlan(t *testing.T) {
	var file operatorChangeFile
	loadOperatorFixture(t, operatorFixture(t, "change-cases.json"), &file)
	if file.DataClass != auditext.DataClassSynthetic {
		t.Fatalf("change fixture data_class = %q, want synthetic", file.DataClass)
	}
	planner := reviewimpact.New()
	seen := make(map[string]bool, len(file.Cases))

	for _, tc := range file.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			seen[tc.ID] = true
			if tc.DataClass != auditext.DataClassSynthetic {
				t.Fatalf("case %s data_class = %q, want synthetic", tc.ID, tc.DataClass)
			}
			baseFixture := applyFrozenOverride(operatorFrozenFixture{}, file.Defaults.Frozen)
			baseFixture = applyFrozenOverride(baseFixture, tc.Base)
			targetFixture := applyFrozenOverride(operatorFrozenFixture{}, file.Defaults.Frozen)
			targetFixture = applyFrozenOverride(targetFixture, tc.Target)

			baseChecks := resolveOperatorChecks(file.Defaults.Checks, tc.BaseChecks)
			targetChecks := resolveOperatorChecks(file.Defaults.Checks, tc.TargetChecks)
			baseResults := make([]auditext.CheckResult, 0, len(baseChecks))
			for _, item := range baseChecks {
				baseResults = append(baseResults, item.checkResult(t))
			}
			targetResults := make([]auditext.CheckResult, 0, len(targetChecks))
			for _, item := range targetChecks {
				targetResults = append(targetResults, item.checkResult(t))
			}

			request := auditext.ImpactRequest{
				BaseEvaluationID:   "fixture-eval-base-" + strings.ToLower(tc.ID),
				TargetEvaluationID: "fixture-eval-target-" + strings.ToLower(tc.ID),
				Base:               baseFixture.frozenInput(t),
				Target:             targetFixture.frozenInput(t),
				BaseChecks:         baseResults,
				TargetChecks:       targetResults,
				Mode:               auditext.ImpactShadow,
			}
			plan, err := planner.Plan(context.Background(), request)
			if err != nil {
				t.Fatalf("Plan(%s) = %v", tc.ID, err)
			}
			if err := plan.Validate(); err != nil {
				t.Fatalf("plan does not validate: %v", err)
			}
			if plan.SchemaVersion != auditext.ImpactPlanSchema {
				t.Fatalf("schema = %q", plan.SchemaVersion)
			}
			if plan.Mode != auditext.ImpactShadow {
				t.Fatalf("mode = %q, want shadow", plan.Mode)
			}
			if plan.DataClass != auditext.DataClassSynthetic {
				t.Fatalf("plan data_class = %q, want synthetic", plan.DataClass)
			}
			if plan.BusinessResultSource != "full" {
				t.Fatalf("business_result_source = %q, want full; a plan never supersedes the full result", plan.BusinessResultSource)
			}
			if string(plan.Fallback) != tc.Expected.Fallback {
				t.Fatalf("fallback = %q, want %q (reason %q, changed %v)", plan.Fallback, tc.Expected.Fallback, plan.FallbackReason, plan.ChangedInputs)
			}
			if plan.DependencyComplete != tc.Expected.DependencyComplete {
				t.Fatalf("dependency_complete = %v, want %v", plan.DependencyComplete, tc.Expected.DependencyComplete)
			}
			if plan.DependencyComplete != (plan.Fallback == auditext.FallbackNone) {
				t.Fatalf("dependency_complete/fallback are inconsistent: %v/%q", plan.DependencyComplete, plan.Fallback)
			}
			actions := make(map[string]auditext.ImpactAction, len(plan.Checks))
			for _, check := range plan.Checks {
				actions[check.CheckID] = check.Action
				if len(check.Reasons) == 0 {
					t.Fatalf("check %s has no reason", check.CheckID)
				}
				for _, reason := range check.Reasons {
					if err := auditext.ValidateID(reason); err != nil {
						t.Fatalf("check %s reason %q is not a stable identifier", check.CheckID, reason)
					}
				}
			}
			if len(actions) != len(targetResults) {
				t.Fatalf("plan covers %d checks, target has %d", len(actions), len(targetResults))
			}
			if tc.Expected.AllActions != "" {
				for checkID, action := range actions {
					if string(action) != tc.Expected.AllActions {
						t.Fatalf("check %s action = %q, want all %q", checkID, action, tc.Expected.AllActions)
					}
				}
			}
			for checkID, want := range tc.Expected.Actions {
				got, ok := actions[checkID]
				if !ok {
					t.Fatalf("plan is missing check %s", checkID)
				}
				if string(got) != want {
					t.Fatalf("check %s action = %q, want %q", checkID, got, want)
				}
			}
			if tc.Expected.ForbidReuse {
				for checkID, action := range actions {
					if action == auditext.ImpactReuseCandidate {
						t.Fatalf("check %s still has a reuse candidate while the full result differs", checkID)
					}
				}
			}
			changed := make(map[string]bool, len(plan.ChangedInputs))
			for _, input := range plan.ChangedInputs {
				changed[input] = true
			}
			for _, required := range tc.Expected.ChangedInputsInclude {
				if !changed[required] {
					t.Fatalf("changed_inputs %v is missing %q", plan.ChangedInputs, required)
				}
			}
			if tc.Expected.BusinessResultSource != "" && tc.Expected.BusinessResultSource != plan.BusinessResultSource {
				t.Fatalf("business_result_source = %q, want %q", plan.BusinessResultSource, tc.Expected.BusinessResultSource)
			}
		})
	}
	for number := 1; number <= 12; number++ {
		id := fmt.Sprintf("D%02d", number)
		if !seen[id] {
			t.Fatalf("change fixture is missing case %s", id)
		}
	}
}

// TestOperatorImpactNoChange pins the conservative baseline: an identical
// target input may only produce reuse candidates, never a changed action, and
// the business result still comes from the full path.
func TestOperatorImpactNoChange(t *testing.T) {
	var file operatorChangeFile
	loadOperatorFixture(t, operatorFixture(t, "change-cases.json"), &file)

	frozen := applyFrozenOverride(operatorFrozenFixture{}, file.Defaults.Frozen)
	checks := resolveOperatorChecks(file.Defaults.Checks, nil)
	results := make([]auditext.CheckResult, 0, len(checks))
	for _, item := range checks {
		results = append(results, item.checkResult(t))
	}
	request := auditext.ImpactRequest{
		BaseEvaluationID:   "fixture-eval-base-identical",
		TargetEvaluationID: "fixture-eval-target-identical",
		Base:               frozen.frozenInput(t),
		Target:             frozen.frozenInput(t),
		BaseChecks:         results,
		TargetChecks:       results,
		Mode:               auditext.ImpactExplain,
	}
	plan, err := reviewimpact.New().Plan(context.Background(), request)
	if err != nil {
		t.Fatalf("Plan(no change) = %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("no-change plan does not validate: %v", err)
	}
	if plan.Fallback != auditext.FallbackNone || !plan.DependencyComplete {
		t.Fatalf("no-change fallback = %q/%v, want none/true", plan.Fallback, plan.DependencyComplete)
	}
	if plan.BusinessResultSource != "full" {
		t.Fatalf("business_result_source = %q, want full", plan.BusinessResultSource)
	}
	if len(plan.ChangedInputs) != 0 {
		t.Fatalf("no-change plan reports changed inputs %v", plan.ChangedInputs)
	}
	if len(plan.Checks) != len(results) {
		t.Fatalf("plan covers %d checks, want %d", len(plan.Checks), len(results))
	}
	for _, check := range plan.Checks {
		if check.Action != auditext.ImpactReuseCandidate {
			t.Fatalf("check %s action = %q, want reuse_candidate", check.CheckID, check.Action)
		}
	}
}
