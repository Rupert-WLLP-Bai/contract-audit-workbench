package capabilitypack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"contractaudit/internal/auditext"
)

const (
	dedicatedLineFile = "dedicated-line-1.0.0.json"
	cloudServicesFile = "cloud-services-1.0.0.json"
)

func packsDir() string {
	return filepath.Join("..", "..", "capability-packs")
}

func readPack(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(packsDir(), name))
	if err != nil {
		t.Fatalf("read pack %s: %v", name, err)
	}
	return raw
}

func packDocument(t *testing.T) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(readPack(t, dedicatedLineFile), &document); err != nil {
		t.Fatalf("decode pack document: %v", err)
	}
	return document
}

func encodeDocument(t *testing.T, document map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode pack document: %v", err)
	}
	return raw
}

func mutatePack(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	document := packDocument(t)
	mutate(document)
	return encodeDocument(t, document)
}

func testRegistryConfig() RegistryConfig {
	return RegistryConfig{
		AllowedCapabilities: []string{"operator.oss.read_config"},
		AllowedConnectors:   []string{"fixture.oss"},
		AllowedComparators: []string{
			"builtin.configured_bandwidth_at_least/v1",
			"existing.minimum_bandwidth",
		},
	}
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := NewRegistry(testRegistryConfig())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return registry
}

func mustParse(t *testing.T, raw []byte) Manifest {
	t.Helper()
	manifest, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return manifest
}

func requireEqual[T comparable](t *testing.T, label string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

func TestParseValidPacks(t *testing.T) {
	dedicated := mustParse(t, readPack(t, dedicatedLineFile))
	requireEqual(t, "schema version", dedicated.SchemaVersion, auditext.CapabilityPackSchemaVersion)
	requireEqual(t, "pack id", dedicated.PackID, "operator.dedicated_line")
	requireEqual(t, "pack version", dedicated.Version, "1.0.0")
	requireEqual(t, "host contract", dedicated.HostContractVersion, auditext.HostContractVersion)
	requireEqual(t, "display name", dedicated.DisplayName, "专线配置核验（合成示例）")
	requireEqual(t, "scenario", dedicated.Scenario, ScenarioTelecomDedicatedLine)
	requireEqual(t, "rule pack id", dedicated.RulePackRef.ID, "telecom-contractual")
	requireEqual(t, "rule pack version", dedicated.RulePackRef.Version, "5")
	requireEqual(t, "fixture suite", dedicated.FixtureSuite, "operatoraudit.dedicated_line.v1")
	requireEqual(t, "data class", dedicated.DataClass, auditext.DataClassSynthetic)
	requireEqual(t, "material roles", len(dedicated.RequiredMaterialRoles), 2)

	if len(dedicated.Capabilities) != 1 {
		t.Fatalf("capabilities = %d, want 1", len(dedicated.Capabilities))
	}
	capability := dedicated.Capabilities[0]
	requireEqual(t, "capability id", capability.ID, "operator.oss.read_config")
	requireEqual(t, "capability effect", capability.Effect, EffectRead)
	if capability.ConnectorRef == nil || *capability.ConnectorRef != "fixture.oss" {
		t.Fatalf("capability connector = %v, want fixture.oss", capability.ConnectorRef)
	}

	if len(dedicated.Checks) != 1 {
		t.Fatalf("checks = %d, want 1", len(dedicated.Checks))
	}
	check := dedicated.Checks[0]
	requireEqual(t, "check id", check.ID, "line.configured_bandwidth")
	requireEqual(t, "comparator", check.ComparatorRef, "builtin.configured_bandwidth_at_least/v1")
	requireEqual(t, "contract field", check.ContractField, "minimum_bandwidth")
	if check.ExternalField == nil || *check.ExternalField != "configured_bandwidth" {
		t.Fatalf("external field = %v, want configured_bandwidth", check.ExternalField)
	}
	requireEqual(t, "ui view", dedicated.UI.View, ViewFieldComparison)
	requireEqual(t, "ui field count", len(dedicated.UI.Fields), 3)

	cloud := mustParse(t, readPack(t, cloudServicesFile))
	requireEqual(t, "cloud pack id", cloud.PackID, "operator.cloud_services")
	requireEqual(t, "cloud scenario", cloud.Scenario, ScenarioCloudServices)
	requireEqual(t, "cloud rule pack id", cloud.RulePackRef.ID, "cloud-services-contractual")
	requireEqual(t, "cloud rule pack version", cloud.RulePackRef.Version, "5")
	requireEqual(t, "cloud capabilities", len(cloud.Capabilities), 0)
	requireEqual(t, "cloud ui view", cloud.UI.View, ViewExistingReview)
	requireEqual(t, "cloud fixture suite", cloud.FixtureSuite, "operatoraudit.cloud.v1")
	if len(cloud.Checks) != 1 {
		t.Fatalf("cloud checks = %d, want 1", len(cloud.Checks))
	}
	requireEqual(t, "cloud check id", cloud.Checks[0].ID, "cloud.existing_bandwidth")
	requireEqual(t, "cloud comparator", cloud.Checks[0].ComparatorRef, "existing.minimum_bandwidth")
	if cloud.Checks[0].ExternalField != nil {
		t.Fatalf("cloud external field = %v, want nil", *cloud.Checks[0].ExternalField)
	}
}

func TestLoadDirLoadsBothPacks(t *testing.T) {
	manifests, err := LoadDir(packsDir())
	if err != nil {
		t.Fatalf("load dir: %v", err)
	}
	if len(manifests) != 2 {
		t.Fatalf("manifests = %d, want 2", len(manifests))
	}
	found := make(map[string]bool, len(manifests))
	for _, manifest := range manifests {
		found[manifest.PackID+"@"+manifest.Version] = true
	}
	for _, want := range []string{"operator.dedicated_line@1.0.0", "operator.cloud_services@1.0.0"} {
		if !found[want] {
			t.Errorf("missing manifest %s", want)
		}
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "top level",
			mutate: func(document map[string]any) {
				document["unexpected"] = true
			},
		},
		{
			name: "capability",
			mutate: func(document map[string]any) {
				document["capabilities"].([]any)[0].(map[string]any)["unexpected"] = true
			},
		},
		{
			name: "check",
			mutate: func(document map[string]any) {
				document["checks"].([]any)[0].(map[string]any)["unexpected"] = true
			},
		},
		{
			name: "rule pack reference",
			mutate: func(document map[string]any) {
				document["rule_pack_ref"].(map[string]any)["unexpected"] = true
			},
		},
		{
			name: "ui",
			mutate: func(document map[string]any) {
				document["ui"].(map[string]any)["unexpected"] = true
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := Parse(mutatePack(t, testCase.mutate)); err == nil {
				t.Fatal("expected unknown field to be rejected")
			}
		})
	}
}

func TestParseRejectsTrailingJSON(t *testing.T) {
	raw := readPack(t, dedicatedLineFile)
	for _, suffix := range []string{"\n{}\n", "\n[]", "\ntrue"} {
		trailing := append(append([]byte(nil), raw...), []byte(suffix)...)
		if _, err := Parse(trailing); err == nil {
			t.Fatalf("expected trailing JSON %q to be rejected", suffix)
		}
	}
}

func TestParseRejectsEmptyInput(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, []byte("   \n\t ")} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("expected empty input %q to be rejected", raw)
		}
	}
}

func TestParseRejectsOversizedManifest(t *testing.T) {
	raw := bytes.Repeat([]byte(" "), MaxManifestBytes+1)
	if _, err := Parse(raw); err == nil {
		t.Fatal("expected oversized manifest to be rejected")
	}
}

func TestParseRejectsWrongHostContractVersion(t *testing.T) {
	raw := mutatePack(t, func(document map[string]any) {
		document["host_contract_version"] = "auditext/v2"
	})
	_, err := Parse(raw)
	if !errors.Is(err, auditext.ErrVersionMismatch) {
		t.Fatalf("error = %v, want ErrVersionMismatch", err)
	}
}

func TestParseRejectsInvalidManifests(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "missing capabilities",
			mutate: func(document map[string]any) {
				delete(document, "capabilities")
			},
		},
		{
			name: "null capabilities",
			mutate: func(document map[string]any) {
				document["capabilities"] = nil
			},
		},
		{
			name: "missing connector reference",
			mutate: func(document map[string]any) {
				delete(document["capabilities"].([]any)[0].(map[string]any), "connector_ref")
			},
		},
		{
			name: "missing external field",
			mutate: func(document map[string]any) {
				delete(document["checks"].([]any)[0].(map[string]any), "external_field")
			},
		},
		{
			name: "missing rule pack version",
			mutate: func(document map[string]any) {
				delete(document["rule_pack_ref"].(map[string]any), "version")
			},
		},
		{
			name: "missing ui fields",
			mutate: func(document map[string]any) {
				delete(document["ui"].(map[string]any), "fields")
			},
		},
		{
			name: "null ui fields",
			mutate: func(document map[string]any) {
				document["ui"].(map[string]any)["fields"] = nil
			},
		},
		{
			name: "null material roles",
			mutate: func(document map[string]any) {
				document["required_material_roles"] = nil
			},
		},
		{
			name: "wrong schema version",
			mutate: func(document map[string]any) {
				document["schema_version"] = "audit-capability-pack/v2"
			},
		},
		{
			name: "invalid pack id",
			mutate: func(document map[string]any) {
				document["pack_id"] = " operator.dedicated_line"
			},
		},
		{
			name: "two-part pack version",
			mutate: func(document map[string]any) {
				document["version"] = "1.0"
			},
		},
		{
			name: "four-part pack version",
			mutate: func(document map[string]any) {
				document["version"] = "1.0.0.0"
			},
		},
		{
			name: "empty display name",
			mutate: func(document map[string]any) {
				document["display_name"] = ""
			},
		},
		{
			name: "long display name",
			mutate: func(document map[string]any) {
				document["display_name"] = strings.Repeat("a", 257)
			},
		},
		{
			name: "unknown scenario",
			mutate: func(document map[string]any) {
				document["scenario"] = "telecom"
			},
		},
		{
			name: "empty material roles",
			mutate: func(document map[string]any) {
				document["required_material_roles"] = []any{}
			},
		},
		{
			name: "duplicate material roles",
			mutate: func(document map[string]any) {
				document["required_material_roles"] = []any{"draft_contract", "draft_contract"}
			},
		},
		{
			name: "unknown material role",
			mutate: func(document map[string]any) {
				document["required_material_roles"] = []any{"invoice"}
			},
		},
		{
			name: "invalid rule pack id",
			mutate: func(document map[string]any) {
				document["rule_pack_ref"].(map[string]any)["id"] = ""
			},
		},
		{
			name: "invalid rule pack version",
			mutate: func(document map[string]any) {
				document["rule_pack_ref"].(map[string]any)["version"] = "5.0.0.0"
			},
		},
		{
			name: "non read effect",
			mutate: func(document map[string]any) {
				document["capabilities"].([]any)[0].(map[string]any)["effect"] = "write"
			},
		},
		{
			name: "duplicate capability id",
			mutate: func(document map[string]any) {
				capabilities := document["capabilities"].([]any)
				document["capabilities"] = append(capabilities, capabilities[0])
			},
		},
		{
			name: "too many capabilities",
			mutate: func(document map[string]any) {
				capabilities := make([]any, 0, 17)
				for i := 0; i < 17; i++ {
					capabilities = append(capabilities, map[string]any{
						"id":            fmt.Sprintf("operator.cap%d", i),
						"effect":        "read",
						"connector_ref": nil,
					})
				}
				document["capabilities"] = capabilities
			},
		},
		{
			name: "long connector ref",
			mutate: func(document map[string]any) {
				document["capabilities"].([]any)[0].(map[string]any)["connector_ref"] = strings.Repeat("a", 129)
			},
		},
		{
			name: "empty checks",
			mutate: func(document map[string]any) {
				document["checks"] = []any{}
			},
		},
		{
			name: "duplicate check id",
			mutate: func(document map[string]any) {
				checks := document["checks"].([]any)
				document["checks"] = append(checks, checks[0])
			},
		},
		{
			name: "too many checks",
			mutate: func(document map[string]any) {
				checks := make([]any, 0, 17)
				for i := 0; i < 17; i++ {
					checks = append(checks, map[string]any{
						"id":             fmt.Sprintf("check.c%d", i),
						"comparator_ref": "builtin.configured_bandwidth_at_least/v1",
						"contract_field": "minimum_bandwidth",
						"external_field": nil,
					})
				}
				document["checks"] = checks
			},
		},
		{
			name: "empty comparator reference",
			mutate: func(document map[string]any) {
				document["checks"].([]any)[0].(map[string]any)["comparator_ref"] = ""
			},
		},
		{
			name: "empty contract field",
			mutate: func(document map[string]any) {
				document["checks"].([]any)[0].(map[string]any)["contract_field"] = ""
			},
		},
		{
			name: "long external field",
			mutate: func(document map[string]any) {
				document["checks"].([]any)[0].(map[string]any)["external_field"] = strings.Repeat("a", 129)
			},
		},
		{
			name: "unknown ui view",
			mutate: func(document map[string]any) {
				document["ui"].(map[string]any)["view"] = "dashboard"
			},
		},
		{
			name: "empty ui fields",
			mutate: func(document map[string]any) {
				document["ui"].(map[string]any)["fields"] = []any{}
			},
		},
		{
			name: "empty ui field",
			mutate: func(document map[string]any) {
				document["ui"].(map[string]any)["fields"] = []any{""}
			},
		},
		{
			name: "long ui field",
			mutate: func(document map[string]any) {
				document["ui"].(map[string]any)["fields"] = []any{strings.Repeat("a", 257)}
			},
		},
		{
			name: "invalid fixture suite",
			mutate: func(document map[string]any) {
				document["fixture_suite"] = "bad suite"
			},
		},
		{
			name: "non synthetic data class",
			mutate: func(document map[string]any) {
				document["data_class"] = "production"
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := Parse(mutatePack(t, testCase.mutate)); err == nil {
				t.Fatal("expected invalid manifest to be rejected")
			}
		})
	}
}

func TestRegistryRegistersBothPacks(t *testing.T) {
	manifests, err := LoadDir(packsDir())
	if err != nil {
		t.Fatalf("load dir: %v", err)
	}
	registry := newTestRegistry(t)
	for _, manifest := range manifests {
		if err := registry.Register(manifest); err != nil {
			t.Fatalf("register %s@%s: %v", manifest.PackID, manifest.Version, err)
		}
	}
	if listed := registry.List(); len(listed) != 2 {
		t.Fatalf("registered manifests = %d, want 2", len(listed))
	}
}

func TestRegistryRejectsUnregisteredReferences(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "capability",
			mutate: func(document map[string]any) {
				document["capabilities"].([]any)[0].(map[string]any)["id"] = "operator.unknown"
			},
		},
		{
			name: "connector",
			mutate: func(document map[string]any) {
				document["capabilities"].([]any)[0].(map[string]any)["connector_ref"] = "fixture.unknown"
			},
		},
		{
			name: "comparator",
			mutate: func(document map[string]any) {
				document["checks"].([]any)[0].(map[string]any)["comparator_ref"] = "builtin.unknown"
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := mustParse(t, mutatePack(t, testCase.mutate))
			if err := newTestRegistry(t).Register(manifest); err == nil {
				t.Fatal("expected unregistered reference to be rejected")
			}
		})
	}
}

func TestRegistryRejectsDuplicateRegistration(t *testing.T) {
	manifest := mustParse(t, readPack(t, dedicatedLineFile))
	registry := newTestRegistry(t)
	if err := registry.Register(manifest); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := registry.Register(manifest); err == nil {
		t.Fatal("expected duplicate registration to be rejected")
	}
}

func TestRegistryGet(t *testing.T) {
	manifest := mustParse(t, readPack(t, dedicatedLineFile))
	registry := newTestRegistry(t)
	if err := registry.Register(manifest); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, ok := registry.Get("operator.dedicated_line", "1.0.0")
	if !ok {
		t.Fatal("expected registered manifest")
	}
	requireEqual(t, "got pack id", got.PackID, "operator.dedicated_line")
	if _, ok := registry.Get("operator.dedicated_line", "2.0.0"); ok {
		t.Fatal("unexpected manifest for unknown version")
	}
	if _, ok := registry.Get("operator.missing", "1.0.0"); ok {
		t.Fatal("unexpected manifest for unknown pack")
	}
}

func TestRegistryReturnsDefensiveCopies(t *testing.T) {
	manifest := mustParse(t, readPack(t, dedicatedLineFile))
	registry := newTestRegistry(t)
	if err := registry.Register(manifest); err != nil {
		t.Fatalf("register: %v", err)
	}

	listed := registry.List()
	listed[0].Capabilities[0].ID = "operator.tampered"
	if *listed[0].Capabilities[0].ConnectorRef != "fixture.oss" {
		t.Fatal("test setup changed the connector reference")
	}
	got, ok := registry.Get("operator.dedicated_line", "1.0.0")
	if !ok {
		t.Fatal("expected registered manifest")
	}
	requireEqual(t, "stored capability id", got.Capabilities[0].ID, "operator.oss.read_config")
}

func TestRegistryListStable(t *testing.T) {
	registry := newTestRegistry(t)
	variants := []struct {
		packID  string
		version string
	}{
		{packID: "zeta.pack", version: "1.0.0"},
		{packID: "alpha.pack", version: "2.0.0"},
		{packID: "alpha.pack", version: "1.0.10"},
		{packID: "alpha.pack", version: "1.0.2"},
		{packID: "alpha.pack", version: "1.0.0"},
		{packID: "operator.dedicated_line", version: "1.0.0"},
	}
	for _, variant := range variants {
		document := packDocument(t)
		document["pack_id"] = variant.packID
		document["version"] = variant.version
		manifest := mustParse(t, encodeDocument(t, document))
		if err := registry.Register(manifest); err != nil {
			t.Fatalf("register %s@%s: %v", variant.packID, variant.version, err)
		}
	}

	want := []string{
		"alpha.pack@1.0.0",
		"alpha.pack@1.0.2",
		"alpha.pack@1.0.10",
		"alpha.pack@2.0.0",
		"operator.dedicated_line@1.0.0",
		"zeta.pack@1.0.0",
	}
	for attempt := 0; attempt < 3; attempt++ {
		listed := registry.List()
		got := make([]string, len(listed))
		for i, manifest := range listed {
			got[i] = manifest.PackID + "@" + manifest.Version
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("List() = %v, want %v", got, want)
		}
	}
}

func TestNewRegistryRejectsEmptyReferences(t *testing.T) {
	if _, err := NewRegistry(RegistryConfig{AllowedCapabilities: []string{""}}); err == nil {
		t.Fatal("expected empty allowed capability to be rejected")
	}
	if _, err := NewRegistry(RegistryConfig{AllowedConnectors: []string{""}}); err == nil {
		t.Fatal("expected empty allowed connector to be rejected")
	}
	if _, err := NewRegistry(RegistryConfig{AllowedComparators: []string{""}}); err == nil {
		t.Fatal("expected empty allowed comparator to be rejected")
	}
}

func TestLoadDirRejectsEmptyDirectory(t *testing.T) {
	if _, err := LoadDir(t.TempDir()); err == nil {
		t.Fatal("expected empty directory to be rejected")
	}

	noJSON := t.TempDir()
	if err := os.WriteFile(filepath.Join(noJSON, "notes.txt"), []byte("notes"), 0o600); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	if _, err := LoadDir(noJSON); err == nil {
		t.Fatal("expected directory without manifests to be rejected")
	}
}

func TestLoadDirIgnoresNonJSONFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), readPack(t, dedicatedLineFile), 0o600); err != nil {
		t.Fatalf("write pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0o600); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	manifests, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("load dir: %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("manifests = %d, want 1", len(manifests))
	}
	requireEqual(t, "pack id", manifests[0].PackID, "operator.dedicated_line")
}

func TestLoadDirRejectsDirectoryEntry(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("expected directory entry to be rejected")
	}
}

func TestLoadDirRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, readPack(t, dedicatedLineFile), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("expected symlink entry to be rejected")
	}
}

func TestLoadDirRejectsMissingDirectory(t *testing.T) {
	if _, err := LoadDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected missing directory to be rejected")
	}
}
