// Package accounts resolves trusted directory identities and local demonstration logins.
// Enterprise bindings are operator-managed; browser claims never select tenant or grants.
package accounts

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"

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

type record struct {
	User
	hash [32]byte
}

var directory []record

// ValidateEnvironment prevents the fixed demonstration directory from being
// mistaken for a production identity provider. Production requires a separately
// implemented identity provider; no environment flag bypasses this guard.
func ValidateEnvironment(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "development", "dev", "test":
		return nil
	default:
		return errors.New("demo accounts are unavailable in production; configure an audited identity provider before deployment")
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

func seed(id, name, role, password string) record {
	return record{User: demoIdentity(id, name, role), hash: digest(id, password)}
}

func seedIn(id, name, role, password, tenantID, orgID string) record {
	item := seed(id, name, role, password)
	item.TenantID, item.OrgID = tenantID, orgID
	return item
}

func digest(id, password string) [32]byte {
	return sha256.Sum256([]byte(id + "\x00" + password))
}

// Authenticate checks a username and password. Unknown users and bad passwords
// take the same failure path.
func Authenticate(username, password string) (User, bool) {
	if directoryConfigured() {
		return User{}, false
	}
	got := digest(username, password)
	for _, item := range directory {
		if subtle.ConstantTimeCompare(item.hash[:], got[:]) == 1 && item.ID == username {
			return copyUser(item.User), item.Enabled
		}
	}
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
