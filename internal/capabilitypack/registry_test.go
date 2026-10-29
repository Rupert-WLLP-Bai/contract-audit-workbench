package capabilitypack

import (
	"reflect"
	"testing"
)

func TestRegistryListOrdersAllAcceptedVersions(t *testing.T) {
	for _, test := range []struct {
		name     string
		versions []string // Expected numeric order, then lexical order for ties.
	}{
		{
			name: "major beyond machine integers",
			versions: []string{
				"999999999999999999999999.9.9",
				"1000000000000000000000000.0.0",
			},
		},
		{
			name: "minor beyond machine integers",
			versions: []string{
				"1.999999999999999999999999.9",
				"1.1000000000000000000000000.0",
			},
		},
		{
			name: "patch beyond machine integers",
			versions: []string{
				"1.0.999999999999999999999998",
				"1.0.999999999999999999999999",
				"1.0.1000000000000000000000000",
			},
		},
		{
			name: "leading zero ties remain stable",
			versions: []string{
				"0.0.0", "00.00.00", "0.0.1", "00.00.01",
				"01.0.0", "1.0.0", "1.00.0", "1.0.2", "1.0.10",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := newTestRegistry(t)
			for _, version := range test.versions {
				document := packDocument(t)
				document["version"] = version
				if err := registry.Register(mustParse(t, encodeDocument(t, document))); err != nil {
					t.Fatal(err)
				}
			}
			for range 20 {
				listed := registry.List()
				got := make([]string, len(listed))
				for i, manifest := range listed {
					got[i] = manifest.Version
				}
				if !reflect.DeepEqual(got, test.versions) {
					t.Fatalf("List() versions = %v, want %v", got, test.versions)
				}
			}
		})
	}
}

func TestRegistryOwnsNestedManifestValues(t *testing.T) {
	registry := newTestRegistry(t)
	manifest := mustParse(t, readPack(t, dedicatedLineFile))
	expected := mustParse(t, readPack(t, dedicatedLineFile))
	if err := registry.Register(manifest); err != nil {
		t.Fatal(err)
	}
	mutate := func(m Manifest) {
		m.RequiredMaterialRoles[0] = "tampered"
		*m.Capabilities[0].ConnectorRef = "tampered"
		m.Checks[0].ID = "tampered"
		*m.Checks[0].ExternalField = "tampered"
		m.UI.Fields[0] = "tampered"
	}
	mutate(manifest)
	stored, ok := registry.Get(expected.PackID, expected.Version)
	if !ok || !reflect.DeepEqual(stored, expected) {
		t.Fatalf("Register retained caller-owned values: %+v", stored)
	}
	mutate(stored)
	mutate(registry.List()[0])
	again, ok := registry.Get(expected.PackID, expected.Version)
	if !ok || !reflect.DeepEqual(again, expected) {
		t.Fatalf("Get/List exposed stored values: %+v", again)
	}
}

func TestRegistryOptionalConnectorAndNilLookup(t *testing.T) {
	var unavailable *Registry
	if manifest, found := unavailable.Get("operator.dedicated_line", "1.0.0"); found || manifest.PackID != "" {
		t.Fatal("nil registry returned a manifest")
	}
	for _, connector := range []*string{nil, new(string)} {
		registry := newTestRegistry(t)
		manifest := mustParse(t, readPack(t, dedicatedLineFile))
		manifest.Capabilities[0].ConnectorRef = connector
		if err := registry.Register(manifest); err != nil {
			t.Fatalf("optional connector rejected: %v", err)
		}
		got, found := registry.Get(manifest.PackID, manifest.Version)
		if !found || !reflect.DeepEqual(got, manifest) {
			t.Fatal("optional connector changed after registration")
		}
	}
}
