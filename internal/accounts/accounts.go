// Package accounts resolves trusted directory identities and local demonstration logins.
// Enterprise bindings are operator-managed; browser claims never select tenant or grants.
package accounts

import (
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"contractaudit/internal/access"
)

// User is a directory entry the API can name.
type User struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name"`
	Role        string         `json:"role"`
	TenantID    string         `json:"tenant_id"`
	OrgID       string         `json:"org_id"`
	Enabled     bool           `json:"enabled"`
	Grants      []access.Grant `json:"grants"`
	Issuer      string         `json:"issuer,omitempty"`
	Subject     string         `json:"subject,omitempty"`
}

// hash is a fixed, pre-computed bcrypt digest of the account's demonstration
// password (cost bcrypt.DefaultCost). The literals are legal salted test
// material: bcrypt carries a per-digest random salt, and these records exist
// only for development/test logins — ValidateEnvironment refuses every other
// mode, so they never guard a production login.
type record struct {
	User
	hash []byte
}

// demoHashes holds one fixed pre-computed bcrypt digest per demonstration
// account, generated once from the credentials documented in init with
// golang.org/x/crypto/bcrypt (bcrypt.DefaultCost). Recomputing slow digests on
// every process start would spend real time on material that never changes.
var demoHashes = map[string]string{
	"demo-admin":            "$2a$10$IwfyN3w2ppmRgHykt4O4r.6lD0NxhTUdjrE6zYSjMkZMmLA2u1CNS",
	"demo-reviewer":         "$2a$10$SpTb61VaPWzXOBuhd9Rpvuc616B82X.KgehksD9PVCyx4l916cS4y",
	"demo-clerk":            "$2a$10$7p/4PnutQp4F6vwuDA90meEnQXRB2ezCLlECRw6EgrKkgwI/X3Svm",
	"demo-viewer":           "$2a$10$nmmX1OAnZfkI9t/PNT.3yuuPHdWVPiginLyzTLM.DNbcgBLNbZJ.S",
	"demo-otherorg-clerk":   "$2a$10$cvSUfHIAmSd3QY/ndirM5u2ubQZRnlLVQKUvP3CvH2RI0OAMv3rta",
	"demo-tenant2-admin":    "$2a$10$qNpJIK3pHGyqNg/LA2lXKupU/aWSZlU/JK0UEBUpGStTZp5xqF3ki",
	"demo-tenant2-clerk":    "$2a$10$lR1NE78ZTxduU3hNXadQiO24Ozb.AeuY3VsY8zNryieApnOklxH9C",
	"demo-tenant2-viewer":   "$2a$10$YgTosFTKHP8NY0cctIH59eOuzXia1.X.faZ3KdXpHZx6VIqbUiIoy",
	"demo-tenant2-reviewer": "$2a$10$3QFiqYivSTLRTL6zavYQKeIKZUfRAk/ibs7kAO0m5M6l.vIRY0fNa",
}

// unknownUserHash is a bcrypt digest of an unrelated fixed string. It lets an
// unknown username spend the same verification cost as a known one.
var unknownUserHash = []byte("$2a$10$2eOVmp7XaEuKJ3g0fqcRF.D1jRhnmD8.aLpW.M1xiuaUKsiqHnpvW")

var directory []record

// ValidateEnvironment reports whether this process may start in the given mode
// with the identity sources loaded so far. Development and test keep the fixed
// demonstration directory. Production requires the enterprise directory to be
// loaded already, so callers must configure it first: an absent, unreadable, or
// invalid directory file refuses startup instead of leaving the fixed
// demonstration passwords as the only identity source. No environment flag
// bypasses this guard.
func ValidateEnvironment(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "development", "dev", "test":
		return nil
	case "production":
		if directoryConfigured() {
			return nil
		}
		return errors.New("production requires a loaded enterprise identity directory; the fixed demonstration passwords are never available")
	default:
		return errors.New("unknown application environment")
	}
}

func init() {
	directory = []record{
		seed("demo-admin", "演示管理员", "admin", "admin-demo"),
		seed("demo-reviewer", "演示审查员", "reviewer", "reviewer-demo"),
		seed("demo-clerk", "演示经办", "clerk", "clerk-demo"),
		seed("demo-viewer", "演示只读", "viewer", "viewer-demo"),
		seedIn("demo-otherorg-clerk", "演示其他组织经办", "clerk", "otherorg-clerk-demo", LocalDemoTenant, "demo-other-org"),
		seedIn("demo-tenant2-admin", "演示第二租户管理员", "admin", "tenant2-admin-demo", LocalDemoTenant2, "demo-org-2"),
		seedIn("demo-tenant2-clerk", "演示第二租户经办", "clerk", "tenant2-clerk-demo", LocalDemoTenant2, "demo-org-2"),
		seedIn("demo-tenant2-reviewer", "演示第二租户审查员", "reviewer", "tenant2-reviewer-demo", LocalDemoTenant2, "demo-org-2"),
		seedIn("demo-tenant2-viewer", "演示第二租户只读", "viewer", "tenant2-viewer-demo", LocalDemoTenant2, "demo-org-2"),
	}
}

// seed builds one demonstration record. The credential argument documents the
// login in the source and must stay identical to the input of the pre-computed
// digest in demoHashes; it is intentionally never re-hashed at process start.
func seed(id, name, role, _ string) record {
	return record{User: demoIdentity(id, name, role), hash: []byte(demoHashes[id])}
}

func seedIn(id, name, role, password, tenantID, orgID string) record {
	item := seed(id, name, role, password)
	item.TenantID, item.OrgID = tenantID, orgID
	return item
}

// Authenticate checks a username and password. Unknown users and bad passwords
// take the same failure path and the same bcrypt verification cost.
func Authenticate(username, password string) (User, bool) {
	if directoryConfigured() {
		return User{}, false
	}
	for _, item := range directory {
		if item.ID != username {
			continue
		}
		if bcrypt.CompareHashAndPassword(item.hash, []byte(password)) != nil {
			return User{}, false
		}
		return copyUser(item.User), item.Enabled
	}
	// Unknown username: burn one bcrypt verification so the response cost
	// matches a known account with a wrong password.
	_ = bcrypt.CompareHashAndPassword(unknownUserHash, []byte(password))
	return User{}, false
}

// ByID returns a directory user.
func ByID(id string) (User, bool) {
	if directoryConfigured() {
		return directoryUser(id)
	}
	for _, item := range directory {
		if item.ID == id {
			return copyUser(item.User), item.Enabled
		}
	}
	return User{}, false
}

// List returns the directory without secrets.
func List() []User {
	if directoryConfigured() {
		return directoryUsers()
	}
	out := make([]User, len(directory))
	for i, item := range directory {
		out[i] = copyUser(item.User)
	}
	return out
}

func CanCreate(role string) bool { return role == "admin" || role == "clerk" }
func CanUpload(role string) bool { return role == "admin" || role == "clerk" }
func CanStart(role string) bool {
	return role == "admin" || role == "clerk" || role == "reviewer"
}
func CanDecide(role string) bool { return role == "admin" || role == "reviewer" }
