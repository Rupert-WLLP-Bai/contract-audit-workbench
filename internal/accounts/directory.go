package accounts

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"contractaudit/internal/access"
)

const LocalDemoTenant = "local-demo"

// LocalDemoTenant2 identifies the second development/test demo tenant.
const LocalDemoTenant2 = "local-demo-2"

var enterpriseDirectory struct {
	sync.Mutex
	path     string
	modified time.Time
	size     int64
	users    map[string]User
}

func demoIdentity(id, name, role string) User {
	user := User{ID: id, DisplayName: name, Role: role, TenantID: LocalDemoTenant, OrgID: "demo-org", Enabled: true}
	// Case content is membership-scoped: the case owner is always seeded into
	// MemberIDs, and only an explicit functional role grants content. Platform and
	// tenant administration therefore never carry a tenant-wide content grant.
	grant := access.Grant{Scope: "case"}
	switch role {
	case "admin", "clerk":
		grant.Role = "case_owner"
	case "reviewer":
		grant.Role = "reviewer"
	case "viewer":
		grant.Role = "viewer"
	}
	user.Grants = []access.Grant{grant}
	return user
}

func copyUser(user User) User {
	user.Grants = append([]access.Grant(nil), user.Grants...)
	for i := range user.Grants {
		user.Grants[i].ResolvedOrgIDs = append([]string(nil), user.Grants[i].ResolvedOrgIDs...)
	}
	return user
}

// Tenant returns the server-bound tenant; only registered legacy identities can omit it.
func Tenant(user User) string {
	if user.TenantID != "" {
		return user.TenantID
	}
	if current, ok := ByID(user.ID); ok {
		return current.TenantID
	}
	return ""
}

func Principal(user User) access.Principal {
	if user.TenantID == "" {
		if current, ok := ByID(user.ID); ok {
			user = current
		}
	}
	return access.Principal{UserID: user.ID, TenantID: user.TenantID, Enabled: user.Enabled, Grants: user.Grants}
}

// ConfigureDirectory enables enterprise-only authentication. Changes to this trusted
// mounted file are reloaded on lookup, so all API/worker processes observe revocation.
// Invalid/missing replacement files deny access rather than preserving stale grants.
func ConfigureDirectory(path string) error {
	enterpriseDirectory.Lock()
	defer enterpriseDirectory.Unlock()
	if path == "" {
		enterpriseDirectory.path, enterpriseDirectory.users = "", nil
		return nil
	}
	users, info, err := readDirectory(path)
	if err != nil {
		return err
	}
	enterpriseDirectory.path, enterpriseDirectory.users = path, users
	enterpriseDirectory.modified, enterpriseDirectory.size = info.ModTime(), info.Size()
	return nil
}

func directoryConfigured() bool {
	enterpriseDirectory.Lock()
	defer enterpriseDirectory.Unlock()
	return enterpriseDirectory.path != ""
}

func readDirectory(path string) (map[string]User, os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, nil, errors.New("invalid trusted directory file")
	}
	var body struct {
		Users []User `json:"users"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return nil, nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, nil, errors.New("directory must contain one JSON object")
	}
	users := make(map[string]User, len(body.Users))
	subjects := make(map[string]bool, len(body.Users))
	for _, user := range body.Users {
		key := user.Issuer + "\x00" + user.Subject
		if user.ID == "" || user.TenantID == "" || user.OrgID == "" || user.Issuer == "" || user.Subject == "" || subjects[key] {
			return nil, nil, errors.New("directory needs unique identity, issuer/subject, tenant and organization")
		}
		if _, exists := users[user.ID]; exists {
			return nil, nil, errors.New("duplicate directory user")
		}
		users[user.ID], subjects[key] = user, true
	}
	return users, info, nil
}

func refreshDirectoryLocked() error {
	info, err := os.Stat(enterpriseDirectory.path)
	if err != nil {
		enterpriseDirectory.users = nil
		return err
	}
	if enterpriseDirectory.users != nil && info.ModTime().Equal(enterpriseDirectory.modified) && info.Size() == enterpriseDirectory.size {
		return nil
	}
	users, info, err := readDirectory(enterpriseDirectory.path)
	if err != nil {
		enterpriseDirectory.users = nil
		return err
	}
	enterpriseDirectory.users = users
	enterpriseDirectory.modified, enterpriseDirectory.size = info.ModTime(), info.Size()
	return nil
}

func directoryUser(id string) (User, bool) {
	enterpriseDirectory.Lock()
	defer enterpriseDirectory.Unlock()
	if refreshDirectoryLocked() != nil {
		return User{}, false
	}
	user, ok := enterpriseDirectory.users[id]
	return copyUser(user), ok && user.Enabled
}

func directoryUsers() []User {
	enterpriseDirectory.Lock()
	defer enterpriseDirectory.Unlock()
	if refreshDirectoryLocked() != nil {
		return []User{}
	}
	users := make([]User, 0, len(enterpriseDirectory.users))
	for _, user := range enterpriseDirectory.users {
		users = append(users, copyUser(user))
	}
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	return users
}

// BySubject maps verified OIDC identity through the trusted directory, never token roles.
func BySubject(issuer, subject string) (User, bool) {
	enterpriseDirectory.Lock()
	defer enterpriseDirectory.Unlock()
	if enterpriseDirectory.path == "" || refreshDirectoryLocked() != nil {
		return User{}, false
	}
	for _, user := range enterpriseDirectory.users {
		if user.Enabled && user.Issuer == issuer && user.Subject == subject {
			return copyUser(user), true
		}
	}
	return User{}, false
}
