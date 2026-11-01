package accounts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLookupUserSnapshotBindsIdentityToUpdatedAndRevokedDirectoryRevision(t *testing.T) {
	if err := ConfigureDirectory(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureDirectory("") })
	path := filepath.Join(t.TempDir(), "directory.json")
	first := `{"users":[{"id":"snapshot-user","display_name":"Before","role":"clerk","tenant_id":"tenant-a","org_id":"org-a","enabled":true,"issuer":"https://issuer.example","subject":"snapshot-subject","grants":[{"role":"case_owner","scope":"case"}]}]}`
	second := `{"users":[{"id":"snapshot-user","display_name":"After","role":"viewer","tenant_id":"tenant-a","org_id":"org-b","enabled":true,"issuer":"https://issuer.example","subject":"snapshot-subject","grants":[{"role":"viewer","scope":"case"}]}]}`
	revoked := `{"users":[]}`
	writeDirectorySnapshotTestFile(t, path, first)
	if err := ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	initial := LookupUserSnapshot("snapshot-user")
	if !initial.Found || initial.User.DisplayName != "Before" || initial.Status.Revision != directoryStatusRevision(first) || initial.Status.State != "ready" || initial.ResolvedAt.IsZero() {
		t.Fatalf("initial identity/revision snapshot mismatch: %+v", initial)
	}
	initial.User.DisplayName = "mutated by caller"
	writeDirectorySnapshotTestFile(t, path, second)
	setDirectoryVerificationTime(time.Now().UTC().Add(-2 * directoryVerificationInterval))
	updated := LookupUserSnapshot("snapshot-user")
	if !updated.Found || updated.User.DisplayName != "After" || updated.User.OrgID != "org-b" || updated.Status.Revision != directoryStatusRevision(second) {
		t.Fatalf("updated identity was not returned with its matching revision: %+v", updated)
	}
	writeDirectorySnapshotTestFile(t, path, revoked)
	setDirectoryVerificationTime(time.Now().UTC().Add(-2 * directoryVerificationInterval))
	revokedSnapshot := LookupUserSnapshot("snapshot-user")
	if revokedSnapshot.Found || revokedSnapshot.User.ID != "" || revokedSnapshot.Status.Revision != directoryStatusRevision(revoked) || revokedSnapshot.Status.State != "ready" {
		t.Fatalf("revocation was not bound to the refreshed directory revision: %+v", revokedSnapshot)
	}
}

func writeDirectorySnapshotTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}
