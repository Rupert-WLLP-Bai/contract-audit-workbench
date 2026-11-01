package accounts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

const (
	directoryFileLimit            = 1 << 20
	directoryVerificationInterval = time.Second
)

var enterpriseDirectory struct {
	sync.Mutex
	path             string
	info             os.FileInfo
	users            map[string]User
	revision         string
	lastGoodRevision string
	checkedAt        time.Time
	loadedAt         time.Time
	verifiedAt       time.Time
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

// ConfigureDirectory enables enterprise-only authentication. Each process checks
// the trusted file on lookup and periodically verifies unchanged file metadata.
// The local verification interval does not bound mount propagation. Invalid or
// missing active files deny access rather than preserving stale grants.
func ConfigureDirectory(path string) error {
	enterpriseDirectory.Lock()
	defer enterpriseDirectory.Unlock()
	if path == "" {
		resetDirectoryLocked()
		return nil
	}
	verificationStarted := time.Now()
	data, info, revision, err := readDirectory(path)
	checkedAt := time.Now()
	if err == nil {
		if path == enterpriseDirectory.path && enterpriseDirectory.users != nil && revision == enterpriseDirectory.revision {
			enterpriseDirectory.info = info
			enterpriseDirectory.checkedAt, enterpriseDirectory.verifiedAt = checkedAt, verificationStarted
			return nil
		}
		var users map[string]User
		users, err = decodeDirectory(bytes.NewReader(data))
		if err == nil {
			loadedAt := time.Now()
			resetDirectoryLocked()
			enterpriseDirectory.path, enterpriseDirectory.info, enterpriseDirectory.users = path, info, users
			enterpriseDirectory.revision, enterpriseDirectory.lastGoodRevision = revision, revision
			enterpriseDirectory.checkedAt, enterpriseDirectory.loadedAt, enterpriseDirectory.verifiedAt = checkedAt, loadedAt, verificationStarted
			return nil
		}
	}
	// A rejected new configuration leaves the active directory in place. If
	// the active file itself failed, revoke its effective users immediately.
	if path == enterpriseDirectory.path {
		directoryUnavailableLocked()
	}
	return err
}

func resetDirectoryLocked() {
	enterpriseDirectory.path = ""
	enterpriseDirectory.info, enterpriseDirectory.users = nil, nil
	enterpriseDirectory.revision, enterpriseDirectory.lastGoodRevision = "", ""
	enterpriseDirectory.checkedAt, enterpriseDirectory.loadedAt, enterpriseDirectory.verifiedAt = time.Time{}, time.Time{}, time.Time{}
}

func directoryUnavailableLocked() {
	enterpriseDirectory.info, enterpriseDirectory.users = nil, nil
	enterpriseDirectory.revision = ""
	enterpriseDirectory.checkedAt, enterpriseDirectory.verifiedAt = time.Now(), time.Time{}
}

func directoryConfigured() bool {
	enterpriseDirectory.Lock()
	defer enterpriseDirectory.Unlock()
	return enterpriseDirectory.path != ""
}

// readDirectory hashes exactly the bytes decoded by the caller. File identity
// and metadata must remain stable until the configured path is checked again.
func readDirectory(path string) ([]byte, os.FileInfo, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > directoryFileLimit {
		return nil, nil, "", errors.New("invalid trusted directory file")
	}
	data, err := io.ReadAll(io.LimitReader(file, directoryFileLimit+1))
	if err != nil {
		return nil, nil, "", err
	}
	if len(data) > directoryFileLimit {
		return nil, nil, "", errors.New("invalid trusted directory file")
	}
	after, err := file.Stat()
	if err != nil {
		return nil, nil, "", err
	}
	current, err := os.Stat(path)
	if err != nil {
		return nil, nil, "", err
	}
	if int64(len(data)) != info.Size() || !sameDirectoryFile(info, after) || !sameDirectoryFile(after, current) {
		return nil, nil, "", errors.New("trusted directory changed during read")
	}
	digest := sha256.Sum256(data)
	return data, current, hex.EncodeToString(digest[:]), nil
}

func sameDirectoryFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Mode() == b.Mode() &&
		a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func decodeDirectory(file io.Reader) (map[string]User, error) {
	var body struct {
		Users []User `json:"users"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("directory must contain one JSON object")
	}
	users := make(map[string]User, len(body.Users))
	subjects := make(map[string]bool, len(body.Users))
	for _, user := range body.Users {
		if err := addDirectoryUser(users, subjects, user); err != nil {
			return nil, err
		}
	}
	return users, nil
}

func addDirectoryUser(users map[string]User, subjects map[string]bool, user User) error {
	key := user.Issuer + "\x00" + user.Subject
	if user.ID == "" || user.TenantID == "" || user.OrgID == "" || user.Issuer == "" || user.Subject == "" || subjects[key] {
		return errors.New("directory needs unique identity, issuer/subject, tenant and organization")
	}
	if _, exists := users[user.ID]; exists {
		return errors.New("duplicate directory user")
	}
	users[user.ID], subjects[key] = user, true
	return nil
}

func refreshDirectoryLocked() error {
	info, err := os.Stat(enterpriseDirectory.path)
	now := time.Now()
	enterpriseDirectory.checkedAt = now
	if err != nil {
		directoryUnavailableLocked()
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > directoryFileLimit {
		directoryUnavailableLocked()
		return errors.New("invalid trusted directory file")
	}
	if enterpriseDirectory.users != nil && sameDirectoryFile(info, enterpriseDirectory.info) &&
		now.Sub(enterpriseDirectory.verifiedAt) < directoryVerificationInterval {
		return nil
	}
	// Start the interval before reading so a long read or decode cannot extend
	// the bound for a same-metadata write that occurs during verification.
	data, info, revision, err := readDirectory(enterpriseDirectory.path)
	checkedAt := time.Now()
	if err != nil {
		directoryUnavailableLocked()
		return err
	}
	if enterpriseDirectory.users == nil || revision != enterpriseDirectory.revision {
		users, err := decodeDirectory(bytes.NewReader(data))
		if err != nil {
			directoryUnavailableLocked()
			return err
		}
		enterpriseDirectory.users = users
		enterpriseDirectory.revision, enterpriseDirectory.lastGoodRevision = revision, revision
		enterpriseDirectory.loadedAt = time.Now()
	}
	enterpriseDirectory.info = info
	enterpriseDirectory.checkedAt, enterpriseDirectory.verifiedAt = checkedAt, now
	return nil
}

func directoryUser(id string) (User, bool) {
	enterpriseDirectory.Lock()
	defer enterpriseDirectory.Unlock()
	if enterpriseDirectory.path == "" || refreshDirectoryLocked() != nil {
		return User{}, false
	}
	user, ok := enterpriseDirectory.users[id]
	return copyUser(user), ok && user.Enabled
}

func directoryUsers() []User {
	enterpriseDirectory.Lock()
	defer enterpriseDirectory.Unlock()
	if enterpriseDirectory.path == "" || refreshDirectoryLocked() != nil {
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
