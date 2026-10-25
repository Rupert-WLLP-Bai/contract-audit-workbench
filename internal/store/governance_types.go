package store

import "contractaudit/internal/accounts"

// Governance lifecycle states mirror rulepack.State. A version is a separately
// persisted content revision; its manifest bytes and lifecycle attestations are
// immutable once published.
const (
	GovernanceDraft     = "draft"
	GovernanceValidated = "validated"
	GovernanceApproved  = "approved"
	GovernancePublished = "published"
)

// GovernanceAction names the append-only history entries recorded in the same
// transaction as the mutation they describe.
const (
	GovernanceActionCreate   = "create"
	GovernanceActionEdit     = "edit"
	GovernanceActionValidate = "validate"
	GovernanceActionApprove  = "approve"
	GovernanceActionPublish  = "publish"
	GovernanceActionDisable  = "disable"
	GovernanceActionRollback = "rollback"
	GovernanceActionTest     = "test"
)

// Governance boundaries keep every persisted list bounded. A request beyond a
// limit is refused instead of silently truncated.
const (
	maxGovernancePackVersions   = 64
	maxGovernanceRules          = 32
	maxGovernanceManifestBytes  = 1 << 20
	maxGovernanceHistoryEntries = 256
)

// RulePackVersion is one persisted manifest revision. Manifest holds the exact
// parsed manifest bytes as stored, so a published version is byte-immutable.
type RulePackVersion struct {
	ID               string   `json:"id"`
	Version          string   `json:"version"`
	TenantID         string   `json:"tenant_id"`
	OrgID            string   `json:"org_id"`
	Status           string   `json:"status"`
	Revision         int64    `json:"revision"`
	Manifest         []byte   `json:"manifest"`
	ContentHash      string   `json:"content_hash"`
	SnapshotDigest   string   `json:"snapshot_sha256"`
	EffectiveFrom    string   `json:"effective_from"`
	EffectiveUntil   string   `json:"effective_until"`
	AuthoritySources []string `json:"authority_sources"`
	ApprovalActorID  string   `json:"approval_actor_id,omitempty"`
	ApprovedAt       string   `json:"approved_at,omitempty"`
	PublishedAt      string   `json:"published_at,omitempty"`
	PublishedBy      string   `json:"published_by,omitempty"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

// RulePackPointer is the single active published version per (tenant, pack ID).
type RulePackPointer struct {
	TenantID        string `json:"tenant_id"`
	PackID          string `json:"pack_id"`
	Version         string `json:"version"`
	PointerRevision int64  `json:"pointer_revision"`
	UpdatedAt       string `json:"updated_at"`
	UpdatedBy       string `json:"updated_by"`
}

// RulePackHistoryEntry is one append-only governance record.
type RulePackHistoryEntry struct {
	Sequence        int64  `json:"sequence"`
	TenantID        string `json:"tenant_id"`
	PackID          string `json:"pack_id"`
	Version         string `json:"version"`
	Action          string `json:"action"`
	ActorID         string `json:"actor_id"`
	ContentHash     string `json:"content_hash"`
	PointerRevision int64  `json:"pointer_revision"`
	CreatedAt       string `json:"created_at"`
}

// GovernanceInput carries one trusted admin mutation. TenantID and ActorID are
// always server-bound from the verified admin session.
type GovernanceInput struct {
	User                    accounts.User
	PackID                  string
	Version                 string
	ExpectedRevision        int64
	ExpectedContentHash     string
	ExpectedPointerRevision int64
	Manifest                []byte
	IdempotencyKey          string
	RequestDigest           string
}
