package accounts

import (
	"reflect"
	"testing"
)

func TestDemoIdentityAndCapabilities(t *testing.T) {
	for _, tc := range []struct {
		id, password, role    string
		create, start, decide bool
	}{
		{"demo-admin", "admin-demo", "admin", true, true, true},
		{"demo-clerk", "clerk-demo", "clerk", true, true, false},
		{"demo-reviewer", "reviewer-demo", "reviewer", false, true, true},
		{"demo-viewer", "viewer-demo", "viewer", false, false, false},
	} {
		user, ok := Authenticate(tc.id, tc.password)
		if !ok || user.Role != tc.role || user.ID != tc.id {
			t.Fatalf("identity %+v %v", user, ok)
		}
		if user2, ok := ByID(tc.id); !ok || !reflect.DeepEqual(user2, user) {
			t.Fatalf("directory %+v %v", user2, ok)
		}
		if CanCreate(user.Role) != tc.create || CanUpload(user.Role) != tc.create || CanStart(user.Role) != tc.start || CanDecide(user.Role) != tc.decide {
			t.Fatalf("capabilities for %s", user.Role)
		}
		if _, ok := Authenticate(tc.id, "bad-password"); ok {
			t.Fatal("bad password accepted")
		}
	}
	if _, ok := Authenticate("unknown", "admin-demo"); ok {
		t.Fatal("unknown account accepted")
	}
	if _, ok := ByID("unknown"); ok {
		t.Fatal("unknown account exists")
	}
	if CanCreate("unknown") || CanUpload("unknown") || CanStart("unknown") || CanDecide("unknown") {
		t.Fatal("unknown role has privileges")
	}
	items := List()
	if len(items) != 4 {
		t.Fatalf("directory count %d", len(items))
	}
	items[0].Role = "changed"
	if List()[0].Role != "admin" {
		t.Fatal("caller mutated the directory")
	}
}

func TestDemoIdentityCannotRunInProduction(t *testing.T) {
	for _, mode := range []string{"", "dev", "development", "test"} {
		if err := ValidateEnvironment(mode); err != nil {
			t.Fatalf("%q: %v", mode, err)
		}
	}
	for _, mode := range []string{"production", "prod", "PRODUCTION", "unknown"} {
		if err := ValidateEnvironment(mode); err == nil {
			t.Fatalf("%q accepted fixed demo passwords", mode)
		}
	}
}
