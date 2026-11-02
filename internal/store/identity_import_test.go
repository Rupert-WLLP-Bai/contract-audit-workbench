package store

import (
	"strings"
	"testing"
)

func TestDecodeIdentityCrosswalkStrictnessAndDuplicateRejection(t *testing.T) {
	valid := `{"version":1,"jurisdictions":[{"id":"region-a","display_name":"Region A","status":"active"}],"tenants":[{"id":"tenant-a","display_name":"Tenant A","jurisdiction_id":"region-a","status":"active"}],"org_units":[{"tenant_id":"tenant-a","id":"org-a","display_name":"Org A","status":"active"}],"principals":[{"id":"11111111-1111-4111-8111-111111111111","actor_id":"actor-a","issuer":"https://issuer.example","subject":"sub-a","status":"active"}],"memberships":[{"id":"22222222-2222-4222-8222-222222222222","actor_id":"actor-a","tenant_id":"tenant-a","org_unit_id":"org-a","role_code":"reviewer","grants":[{"role":"reviewer","scope":"case","case_id":"case-a"}],"status":"active"}]}`
	if _, err := DecodeIdentityCrosswalk([]byte(valid)); err != nil {
		t.Fatalf("DecodeIdentityCrosswalk(valid) = %v", err)
	}
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "unknown field", body: strings.Replace(valid, `"version":1`, `"version":1,"legacy_roles":true`, 1), want: "unknown field"},
		{name: "duplicate actor", body: strings.Replace(valid,
			`{"id":"11111111-1111-4111-8111-111111111111","actor_id":"actor-a","issuer":"https://issuer.example","subject":"sub-a","status":"active"}`,
			`{"id":"11111111-1111-4111-8111-111111111111","actor_id":"actor-a","issuer":"https://issuer.example","subject":"sub-a","status":"active"},{"id":"33333333-3333-4333-8333-333333333333","actor_id":"actor-a","issuer":"https://issuer.example","subject":"sub-b","status":"active"}`, 1), want: "repeats an actor ID"},
		{name: "unknown grant", body: strings.Replace(valid, `"scope":"case"`, `"scope":"tenant_admin"`, 1), want: "unknown role or grant scope"},
		{name: "trailing value", body: valid + `{}`, want: "exactly one JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeIdentityCrosswalk([]byte(tc.body)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("DecodeIdentityCrosswalk() error=%v, want substring %q", err, tc.want)
			}
		})
	}
}
