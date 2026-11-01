package accounts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const directoryStatusReviewer = `{"users":[{"id":"alice-sensitive","display_name":"directory-name-sensitive","role":"reviewer","tenant_id":"tenant-a","org_id":"sales","enabled":true,"issuer":"https://issuer.example","subject":"subject-sensitive","grants":[{"role":"reviewer","scope":"org","org_id":"sales"}]}]}`

func directoryStatusObserver() string {
	return strings.ReplaceAll(directoryStatusReviewer, `"reviewer"`, `"observer"`)
}

func directoryStatusRevision(body string) string {
	digest := sha256.Sum256([]byte(body))
	return hex.EncodeToString(digest[:])
}

func writeDirectoryStatusFile(t *testing.T, path, body string, modified time.Time) os.FileInfo {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func replaceDirectoryStatusFile(t *testing.T, path, body string, modified time.Time) os.FileInfo {
	t.Helper()
	replacement := filepath.Join(filepath.Dir(path), "replacement.json")
	info := writeDirectoryStatusFile(t, replacement, body, modified)
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	return info
}

func setDirectoryVerificationTime(verified time.Time) {
	enterpriseDirectory.Lock()
	enterpriseDirectory.verifiedAt = verified
	enterpriseDirectory.Unlock()
}

func requireDirectoryStatusTime(t *testing.T, value string) {
	t.Helper()
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		t.Fatalf("invalid status timestamp %q: %v", value, err)
	}
}

func TestDirectoryStatusConfigurationReset(t *testing.T) {
	if err := ConfigureDirectory(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureDirectory("") })
	local := DirectoryStatus()
	if local.Mode != "local_demo" || local.State != "local_demo" || local.Revision != "" ||
		local.LastGoodRevision != "" || local.CheckedAt != "" || local.LoadedAt != "" ||
		local.RefreshErrorCode != "" || local.LocalVerificationIntervalMS != 1000 {
		t.Fatalf("unexpected local status: %+v", local)
	}

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.json")
	secondPath := filepath.Join(dir, "second.json")
	badPath := filepath.Join(dir, "bad.json")
	modified := time.Unix(1700000000, 0)
	writeDirectoryStatusFile(t, firstPath, directoryStatusReviewer, modified)
	writeDirectoryStatusFile(t, secondPath, directoryStatusObserver(), modified)
	writeDirectoryStatusFile(t, badPath, `{"users":`, modified)
	if err := ConfigureDirectory(firstPath); err != nil {
		t.Fatal(err)
	}
	first := DirectoryStatus()
	if first.Mode != "enterprise" || first.State != "ready" || first.Revision != directoryStatusRevision(directoryStatusReviewer) ||
		first.LastGoodRevision != first.Revision || first.RefreshErrorCode != "" || first.LocalVerificationIntervalMS != 1000 {
		t.Fatalf("unexpected ready status: %+v", first)
	}
	requireDirectoryStatusTime(t, first.CheckedAt)
	requireDirectoryStatusTime(t, first.LoadedAt)
	if err := ConfigureDirectory(firstPath); err != nil {
		t.Fatal(err)
	}
	if unchanged := DirectoryStatus(); unchanged.Revision != first.Revision || unchanged.LoadedAt != first.LoadedAt {
		t.Fatalf("unchanged configuration was reloaded: %+v", unchanged)
	}

	if err := ConfigureDirectory(badPath); err == nil {
		t.Fatal("invalid new configuration was accepted")
	}
	if status := DirectoryStatus(); status.State != "ready" || status.Revision != first.Revision {
		t.Fatalf("rejected new configuration changed active directory: %+v", status)
	}
	if _, ok := ByID("alice-sensitive"); !ok {
		t.Fatal("rejected new configuration revoked the active directory")
	}

	if err := ConfigureDirectory(secondPath); err != nil {
		t.Fatal(err)
	}
	second := DirectoryStatus()
	if second.State != "ready" || second.Revision != directoryStatusRevision(directoryStatusObserver()) ||
		second.LastGoodRevision != second.Revision || second.Revision == first.Revision {
		t.Fatalf("configuration replacement did not reset revision: %+v", second)
	}
	if err := ConfigureDirectory(""); err != nil {
		t.Fatal(err)
	}
	if disabled := DirectoryStatus(); disabled != local {
		t.Fatalf("disabled directory retained status: %+v", disabled)
	}
}

func TestDirectoryRevisionDetectsSameStatChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "directory.json")
	modified := time.Unix(1700000000, 0)
	originalInfo := writeDirectoryStatusFile(t, path, directoryStatusReviewer, modified)
	if err := ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureDirectory("") })
	initial := DirectoryStatus()

	// A verification with identical bytes advances the check without reloading.
	setDirectoryVerificationTime(time.Now().Add(-directoryVerificationInterval))
	unchanged := DirectoryStatus()
	if unchanged.Revision != initial.Revision || unchanged.LoadedAt != initial.LoadedAt {
		t.Fatalf("unchanged bytes changed the loaded revision: before=%+v after=%+v", initial, unchanged)
	}

	// Even inside the verification interval, a different inode forces a read.
	setDirectoryVerificationTime(time.Now().Add(time.Hour))
	replacementInfo := replaceDirectoryStatusFile(t, path, directoryStatusObserver(), modified)
	if os.SameFile(originalInfo, replacementInfo) || originalInfo.Size() != replacementInfo.Size() ||
		!originalInfo.ModTime().Equal(replacementInfo.ModTime()) {
		t.Fatal("replacement did not preserve size and mtime with a new file identity")
	}
	user, ok := ByID("alice-sensitive")
	if !ok || user.Role != "observer" || len(user.Grants) != 1 || user.Grants[0].Role != "observer" {
		t.Fatalf("replacement grants not observed: %+v, %v", user, ok)
	}
	if status := DirectoryStatus(); status.Revision != directoryStatusRevision(directoryStatusObserver()) {
		t.Fatalf("replacement revision not observed: %+v", status)
	}

	// Restore identical metadata on the same inode. The fast path stays in
	// effect until the simulated one-second verification window expires.
	setDirectoryVerificationTime(time.Now().Add(time.Hour))
	inPlaceInfo := writeDirectoryStatusFile(t, path, directoryStatusReviewer, modified)
	if !os.SameFile(replacementInfo, inPlaceInfo) || replacementInfo.Size() != inPlaceInfo.Size() ||
		!replacementInfo.ModTime().Equal(inPlaceInfo.ModTime()) {
		t.Fatal("in-place change did not preserve file identity, size and mtime")
	}
	if user, ok := ByID("alice-sensitive"); !ok || user.Role != "observer" {
		t.Fatalf("same-stat change bypassed the fast path: %+v, %v", user, ok)
	}
	setDirectoryVerificationTime(time.Now().Add(-directoryVerificationInterval))
	if user, ok := ByID("alice-sensitive"); !ok || user.Role != "reviewer" || len(user.Grants) != 1 || user.Grants[0].Role != "reviewer" {
		t.Fatalf("same-inode change not reverified: %+v, %v", user, ok)
	}
	status := DirectoryStatus()
	if status.Revision != directoryStatusRevision(directoryStatusReviewer) || status.LastGoodRevision != status.Revision {
		t.Fatalf("same-inode revision not observed: %+v", status)
	}
}

func TestDirectoryFailureRevokesAndRecovers(t *testing.T) {
	invalidIdentity := strings.Replace(directoryStatusReviewer, `"tenant_id":"tenant-a"`, `"tenant_id":""`, 1)
	invalidIdentity += strings.Repeat(" ", len(directoryStatusReviewer)-len(invalidIdentity))
	invalidJSON := "!" + directoryStatusReviewer[1:]
	for _, tc := range []struct {
		name string
		body string
	}{
		{"invalid JSON", invalidJSON},
		{"invalid identity", invalidIdentity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sensitive-directory-path.json")
			modified := time.Unix(1700000000, 0)
			beforeInfo := writeDirectoryStatusFile(t, path, directoryStatusReviewer, modified)
			if err := ConfigureDirectory(path); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ConfigureDirectory("") })
			initial := DirectoryStatus()
			setDirectoryVerificationTime(time.Now().Add(time.Hour))
			afterInfo := replaceDirectoryStatusFile(t, path, tc.body, modified)
			if os.SameFile(beforeInfo, afterInfo) || beforeInfo.Size() != afterInfo.Size() ||
				!beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
				t.Fatal("invalid replacement did not preserve size and mtime")
			}
			if users := List(); len(users) != 0 {
				t.Fatalf("invalid directory retained users: %+v", users)
			}
			if _, ok := ByID("alice-sensitive"); ok {
				t.Fatal("invalid directory retained an enabled user")
			}
			if _, ok := BySubject("https://issuer.example", "subject-sensitive"); ok {
				t.Fatal("invalid directory retained an OIDC binding")
			}
			if _, ok := Authenticate("demo-admin", "admin-demo"); ok {
				t.Fatal("invalid enterprise directory enabled local login")
			}
			failed := DirectoryStatus()
			if failed.Mode != "enterprise" || failed.State != "unavailable" || failed.Revision != "" ||
				failed.LastGoodRevision != initial.Revision || failed.LoadedAt != initial.LoadedAt ||
				failed.RefreshErrorCode != "directory_unavailable" {
				t.Fatalf("unexpected unavailable status: %+v", failed)
			}
			requireDirectoryStatusTime(t, failed.CheckedAt)
			encoded, err := json.Marshal(failed)
			if err != nil {
				t.Fatal(err)
			}
			for _, sensitive := range []string{path, directoryStatusReviewer, "alice-sensitive", "subject-sensitive", "directory-name-sensitive", "https://issuer.example"} {
				if strings.Contains(string(encoded), sensitive) {
					t.Fatalf("status leaked sensitive directory data: %s", sensitive)
				}
			}

			// Rewriting the invalid inode with the last-good bytes recovers even
			// when metadata and the historical revision are unchanged.
			recoveredInfo := writeDirectoryStatusFile(t, path, directoryStatusReviewer, modified)
			if !os.SameFile(afterInfo, recoveredInfo) || afterInfo.Size() != recoveredInfo.Size() ||
				!afterInfo.ModTime().Equal(recoveredInfo.ModTime()) {
				t.Fatal("recovery did not preserve metadata")
			}
			if user, ok := ByID("alice-sensitive"); !ok || len(user.Grants) != 1 || user.Grants[0].Role != "reviewer" {
				t.Fatalf("valid directory did not restore grants: %+v, %v", user, ok)
			}
			recovered := DirectoryStatus()
			if recovered.State != "ready" || recovered.Revision != initial.Revision ||
				recovered.LastGoodRevision != initial.Revision || recovered.RefreshErrorCode != "" {
				t.Fatalf("unexpected recovered status: %+v", recovered)
			}

			setDirectoryVerificationTime(time.Now().Add(-directoryVerificationInterval))
			invalidInPlace := writeDirectoryStatusFile(t, path, tc.body, modified)
			if !os.SameFile(recoveredInfo, invalidInPlace) || recoveredInfo.Size() != invalidInPlace.Size() ||
				!recoveredInfo.ModTime().Equal(invalidInPlace.ModTime()) {
				t.Fatal("invalid in-place rewrite did not preserve metadata")
			}
			if _, ok := ByID("alice-sensitive"); ok {
				t.Fatal("verified invalid in-place rewrite retained a user")
			}
			if status := DirectoryStatus(); status.State != "unavailable" || status.Revision != "" ||
				status.LastGoodRevision != initial.Revision || status.RefreshErrorCode != "directory_unavailable" {
				t.Fatalf("verified invalid in-place rewrite retained a revision: %+v", status)
			}
		})
	}
}

func TestConfigureCurrentDirectoryFailureRevokes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "directory.json")
	modified := time.Unix(1700000000, 0)
	writeDirectoryStatusFile(t, path, directoryStatusReviewer, modified)
	if err := ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureDirectory("") })
	good := DirectoryStatus()
	writeDirectoryStatusFile(t, path, `{"users":`, modified)
	if err := ConfigureDirectory(path); err == nil {
		t.Fatal("invalid current configuration was accepted")
	}
	if user, ok := ByID("alice-sensitive"); ok {
		t.Fatalf("failed current configuration retained user: %+v", user)
	}
	status := DirectoryStatus()
	if status.State != "unavailable" || status.Revision != "" || status.LastGoodRevision != good.Revision {
		t.Fatalf("failed current configuration retained revision: %+v", status)
	}
}

func TestDirectoryRemovalRevokesAndRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "directory.json")
	modified := time.Unix(1700000000, 0)
	writeDirectoryStatusFile(t, path, directoryStatusReviewer, modified)
	if err := ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureDirectory("") })
	good := DirectoryStatus()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	failed := DirectoryStatus()
	if failed.State != "unavailable" || failed.Revision != "" || failed.LastGoodRevision != good.Revision ||
		failed.RefreshErrorCode != "directory_unavailable" {
		t.Fatalf("missing directory retained its effective revision: %+v", failed)
	}
	if _, ok := ByID("alice-sensitive"); ok {
		t.Fatal("missing directory retained its effective user")
	}
	writeDirectoryStatusFile(t, path, directoryStatusReviewer, modified)
	recovered := DirectoryStatus()
	if recovered.State != "ready" || recovered.Revision != good.Revision || recovered.RefreshErrorCode != "" {
		t.Fatalf("restored directory was not reloaded: %+v", recovered)
	}
	if _, ok := BySubject("https://issuer.example", "subject-sensitive"); !ok {
		t.Fatal("restored directory did not restore its subject binding")
	}
}

func TestDirectoryConcurrentLookupsAndAtomicReplacements(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "directory.json")
	modified := time.Unix(1700000000, 0)
	writeDirectoryStatusFile(t, path, directoryStatusReviewer, modified)
	if err := ConfigureDirectory(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureDirectory("") })
	const replacements = 24
	paths := make([]string, replacements)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("replacement-%d.json", i))
		body := directoryStatusReviewer
		if i%2 == 0 {
			body = directoryStatusObserver()
		}
		writeDirectoryStatusFile(t, paths[i], body, modified)
	}

	start := make(chan struct{})
	failures := make(chan string, 4)
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for range 80 {
				status := DirectoryStatus()
				if status.Mode != "enterprise" || (status.State != "ready" && status.State != "unavailable") ||
					(status.State == "ready" && (status.Revision == "" || status.Revision != status.LastGoodRevision || status.RefreshErrorCode != "")) ||
					(status.State == "unavailable" && (status.Revision != "" || status.RefreshErrorCode != "directory_unavailable")) {
					failures <- fmt.Sprintf("inconsistent status: %+v", status)
					return
				}
				if user, ok := ByID("alice-sensitive"); ok && (len(user.Grants) != 1 || user.Role != user.Grants[0].Role || (user.Role != "reviewer" && user.Role != "observer")) {
					failures <- fmt.Sprintf("inconsistent user: %+v", user)
					return
				}
				if users := List(); len(users) > 1 {
					failures <- fmt.Sprintf("inconsistent directory size: %d", len(users))
					return
				}
			}
		}()
	}
	close(start)
	var renameErr error
	for _, replacement := range paths {
		if renameErr = os.Rename(replacement, path); renameErr != nil {
			break
		}
	}
	readers.Wait()
	close(failures)
	if renameErr != nil {
		t.Fatal(renameErr)
	}
	for failure := range failures {
		t.Error(failure)
	}
	status := DirectoryStatus()
	if status.State != "ready" || status.Revision != directoryStatusRevision(directoryStatusReviewer) {
		t.Fatalf("final replacement did not recover: %+v", status)
	}
}
