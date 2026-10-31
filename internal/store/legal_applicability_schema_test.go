package store

import (
	"strings"
	"testing"
)

func TestLegalApplicabilitySchemaUsesAppendOnlyVersionedFacts(t *testing.T) {
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS case_legal_applicability_facts",
		"CREATE TABLE IF NOT EXISTS case_legal_applicability_fact_versions",
		"basis_obtained_at timestamptz,",
		"DEFERRABLE INITIALLY DEFERRED",
		"FOREIGN KEY (tenant_id,org_id,case_id,party_binding_id,party_binding_version,party_subject_id,party_subject_version)",
		"case_legal_applicability_fact_versions_append_only",
		"current_version<>OLD.current_version+1",
		"status<>'established' OR dimension NOT IN",
	} {
		if !strings.Contains(legalApplicabilitySchemaSQL, required) {
			t.Errorf("legal applicability schema is missing %q", required)
		}
	}
	if strings.Contains(legalApplicabilitySchemaSQL, "basis_obtained_at timestamptz NOT NULL") {
		t.Fatal("unknown facts without evidence must not invent basis_obtained_at")
	}
}
