package accounts

import "time"

// DirectoryStatusSnapshot describes this process's effective trusted directory.
// The verification interval applies to local lookups, not mount propagation.
type DirectoryStatusSnapshot struct {
	Mode                        string `json:"mode"`
	State                       string `json:"state"`
	Revision                    string `json:"revision"`
	LastGoodRevision            string `json:"last_good_revision"`
	CheckedAt                   string `json:"checked_at"`
	LoadedAt                    string `json:"loaded_at"`
	RefreshErrorCode            string `json:"refresh_error_code"`
	LocalVerificationIntervalMS int64  `json:"local_verification_interval_ms"`
}

// DirectoryStatus refreshes the configured directory and returns a sanitized
// snapshot. An unavailable directory never exposes an effective revision.
func DirectoryStatus() DirectoryStatusSnapshot {
	enterpriseDirectory.Lock()
	defer enterpriseDirectory.Unlock()

	status := DirectoryStatusSnapshot{
		Mode:                        "local_demo",
		State:                       "local_demo",
		LocalVerificationIntervalMS: directoryVerificationInterval.Milliseconds(),
	}
	if enterpriseDirectory.path == "" {
		return status
	}

	_ = refreshDirectoryLocked()
	status.Mode = "enterprise"
	status.State = "ready"
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

func directoryStatusTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
