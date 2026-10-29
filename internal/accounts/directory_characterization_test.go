package accounts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureDirectoryPinsAcceptedAndRejectedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "directory.json")
	replacement := filepath.Join(dir, "replacement.json")
	valid := `{"users":[{"id":"alice","tenant_id":"tenant-a","org_id":"sales","issuer":"https://issuer.example","subject":"subject-a","enabled":true}]}`
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureDirectory(path); err != nil {
		t.Fatalf("configure valid directory: %v", err)
	}
	t.Cleanup(func() { _ = ConfigureDirectory("") })
	if user, ok := BySubject("https://issuer.example", "subject-a"); !ok || user.ID != "alice" {
		t.Fatalf("configured identity = (%+v, %v), want alice", user, ok)
	}

	duplicateID := `{"users":[{"id":"alice","tenant_id":"tenant-a","org_id":"sales","issuer":"i","subject":"one"},{"id":"alice","tenant_id":"tenant-a","org_id":"sales","issuer":"i","subject":"two"}]}`
	duplicateSubject := `{"users":[{"id":"alice","tenant_id":"tenant-a","org_id":"sales","issuer":"i","subject":"same"},{"id":"bob","tenant_id":"tenant-a","org_id":"sales","issuer":"i","subject":"same"}]}`
	missingBinding := `{"users":[{"id":"alice","tenant_id":"tenant-a","issuer":"i","subject":"one"}]}`
	trailingObject := valid + ` {}`
	unknownField := `{"users":[],"unexpected":true}`
	tests := []struct {
		name string
		body string
		want string
	}{
		{"duplicate ID", duplicateID, "duplicate directory user"},
		{"duplicate issuer subject", duplicateSubject, "directory needs unique identity, issuer/subject, tenant and organization"},
		{"missing organization", missingBinding, "directory needs unique identity, issuer/subject, tenant and organization"},
		{"trailing object", trailingObject, "directory must contain one JSON object"},
		{"unknown field", unknownField, "json: unknown field \"unexpected\""},
		{"malformed JSON", `{"users":`, "unexpected EOF"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(replacement, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ConfigureDirectory(replacement); err == nil || err.Error() != tc.want {
				t.Fatalf("ConfigureDirectory() error = %v, want %q", err, tc.want)
			}
			if _, ok := BySubject("https://issuer.example", "subject-a"); !ok {
				t.Fatal("rejected replacement changed the active directory")
			}
		})
	}

	t.Run("oversized file", func(t *testing.T) {
		if err := os.WriteFile(replacement, []byte(strings.Repeat(" ", (1<<20)+1)), 0600); err != nil {
			t.Fatal(err)
		}
		if err := ConfigureDirectory(replacement); err == nil || err.Error() != "invalid trusted directory file" {
			t.Fatalf("ConfigureDirectory() error = %v, want invalid trusted directory file", err)
		}
		if _, ok := BySubject("https://issuer.example", "subject-a"); !ok {
			t.Fatal("oversized replacement changed the active directory")
		}
	})

	t.Run("non regular file", func(t *testing.T) {
		if err := ConfigureDirectory(t.TempDir()); err == nil || err.Error() != "invalid trusted directory file" {
			t.Fatalf("ConfigureDirectory() error = %v, want invalid trusted directory file", err)
		}
	})
}
