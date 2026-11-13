package app

// LifecycleRevisionSnapshot preserves the exact stored historical content.
type LifecycleRevisionSnapshot struct {
	RevisionID string
	Snapshot   []byte
}
