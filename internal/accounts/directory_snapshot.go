package accounts

import "time"

// UserDirectorySnapshot binds an effective user copy to the exact directory
// revision that resolved it. Its fields are copied while the directory lock
// is held and after one refresh; callers must not use the identity as proof of
// any application-level role beyond the fields represented by the directory.
type UserDirectorySnapshot struct {
	User       User
	Found      bool
	Status     DirectoryStatusSnapshot
	ResolvedAt time.Time
}

// LookupUserSnapshot resolves one enabled account and its directory metadata
// from the same locked state. It does not retain the directory lock after
// returning, and the returned user is a defensive copy.
func LookupUserSnapshot(id string) UserDirectorySnapshot {
	enterpriseDirectory.Lock()
	defer enterpriseDirectory.Unlock()

	if enterpriseDirectory.path != "" {
		_ = refreshDirectoryLocked()
		status := directoryStatusLocked()
		resolvedAt := time.Now().UTC()
		if status.State != "ready" {
			return UserDirectorySnapshot{Status: status, ResolvedAt: resolvedAt}
		}
		user, ok := enterpriseDirectory.users[id]
		if !ok || !user.Enabled {
			return UserDirectorySnapshot{Status: status, ResolvedAt: resolvedAt}
		}
		return UserDirectorySnapshot{User: copyUser(user), Found: true, Status: status, ResolvedAt: resolvedAt}
	}
	status := directoryStatusLocked()
	resolvedAt := time.Now().UTC()
	for _, item := range directory {
		if item.ID == id && item.Enabled {
			return UserDirectorySnapshot{User: copyUser(item.User), Found: true, Status: status, ResolvedAt: resolvedAt}
		}
	}
	return UserDirectorySnapshot{Status: status, ResolvedAt: resolvedAt}
}

// directoryStatusLocked returns metadata from the current state. The caller
// must hold enterpriseDirectory.Lock; this helper deliberately does no refresh.
func directoryStatusLocked() DirectoryStatusSnapshot {
	status := DirectoryStatusSnapshot{
		Mode: "local_demo", State: "local_demo",
		LocalVerificationIntervalMS: directoryVerificationInterval.Milliseconds(),
	}
	if enterpriseDirectory.path == "" {
		return status
	}
	status.Mode, status.State = "enterprise", "ready"
	status.Revision = enterpriseDirectory.revision
	status.LastGoodRevision = enterpriseDirectory.lastGoodRevision
	status.CheckedAt = directoryStatusTime(enterpriseDirectory.checkedAt)
	status.LoadedAt = directoryStatusTime(enterpriseDirectory.loadedAt)
	if enterpriseDirectory.users == nil {
		status.State = "unavailable"
		status.RefreshErrorCode = "directory_unavailable"
	}
	return status
}
