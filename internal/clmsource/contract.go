// Package clmsource contains the small, dependency-free CLM external-source
// contract. It describes source capabilities and pure authority/version rules;
// it does not authenticate callers, authorize tenants or contracts, persist
// receipts, or prove that a configured external system is reachable.
package clmsource

import (
	"encoding/hex"
	"errors"
	"strings"
)

// AuthorityMode selects the single authority for a contract aggregate.
type AuthorityMode string

const (
	AuthorityNative   AuthorityMode = "native"
	AuthorityExternal AuthorityMode = "external"
)

// Action identifies a contract operation for authority-mode policy checks.
type Action string

const (
	ActionRead                Action = "read"
	ActionAuditLink           Action = "audit_link"
	ActionAppendSourceFact    Action = "append_source_fact"
	ActionEditContract        Action = "edit_contract"
	ActionApprove             Action = "approve"
	ActionRegister            Action = "register"
	ActionRecordDelivery      Action = "record_delivery"
	ActionRecordAcceptance    Action = "record_acceptance"
	ActionRecordPayment       Action = "record_payment"
	ActionRecordAssetHandover Action = "record_asset_handover"
)

// AllowsLocalAction answers only whether the contract's authority mode permits
// an operation. It is not user, tenant, organization, case, or source auth.
// External contracts may be read, linked to local audit cases, and receive
// separately authenticated append-only source facts; local contract decisions
// and fulfillment mutations remain forbidden.
func AllowsLocalAction(mode AuthorityMode, action Action) bool {
	if mode == AuthorityNative {
		return action == ActionRead || action == ActionAuditLink ||
			action == ActionAppendSourceFact || action == ActionEditContract ||
			action == ActionApprove || action == ActionRegister ||
			action == ActionRecordDelivery || action == ActionRecordAcceptance ||
			action == ActionRecordPayment || action == ActionRecordAssetHandover
	}
	if mode != AuthorityExternal {
		return false
	}
	return action == ActionRead || action == ActionAuditLink || action == ActionAppendSourceFact
}

// SourceKind is a declared deployment mode, not evidence of a live connection.
type SourceKind string

const (
	SourceMock SourceKind = "mock"
	SourceReal SourceKind = "real"
)

// AuthenticationProfile names the authentication mechanism configured for a
// source adapter. Naming a mechanism does not mean its credentials were
// checked; the server must authenticate the request before recording facts.
type AuthenticationProfile string

const (
	AuthFixture          AuthenticationProfile = "fixture"
	AuthHMACSHA256       AuthenticationProfile = "hmac-sha256"
	AuthMutualTLS        AuthenticationProfile = "mutual-tls"
	AuthWorkloadIdentity AuthenticationProfile = "workload-identity"
)

// VersionOrdering states what the source provider guarantees for source
// versions. Opaque versions support equality/deduplication only. Monotonic
// ordering may be used only when the provider guarantees a monotonic sequence
// and the server has authenticated its provenance.
type VersionOrdering string

const (
	VersionOpaque            VersionOrdering = "opaque"
	VersionMonotonicSequence VersionOrdering = "monotonic-sequence"
)

// Capability is a declaration of a CLM source adapter's supported behavior.
// It is configuration metadata only: it does not prove endpoint reachability,
// real-system integration, authentication, or business authorization.
type Capability struct {
	SourceID        string                `json:"source_id"`
	Kind            SourceKind            `json:"kind"`
	Authority       AuthorityMode         `json:"authority"`
	ReadOnly        bool                  `json:"read_only"`
	Authentication  AuthenticationProfile `json:"authentication"`
	CanReadContract bool                  `json:"can_read_contract"`
	VersionOrdering VersionOrdering       `json:"version_ordering"`
}

// Validate checks internal consistency of a declared external-source
// capability. The caller must still authenticate the source and authorize the
// tenant, source identity, contract, and any associated case on every request.
func (c Capability) Validate() error {
	if strings.TrimSpace(c.SourceID) == "" {
		return errors.New("source_id is required")
	}
	if c.Authority != AuthorityExternal {
		return errors.New("CLM source adapter must have external authority")
	}
	if !c.ReadOnly {
		return errors.New("external CLM source adapter must be read-only")
	}
	if !c.CanReadContract {
		return errors.New("external CLM source adapter must declare contract reads")
	}
	if c.VersionOrdering != VersionOpaque && c.VersionOrdering != VersionMonotonicSequence {
		return errors.New("version_ordering must be opaque or monotonic-sequence")
	}
	switch c.Kind {
	case SourceMock:
		if c.Authentication != AuthFixture {
			return errors.New("mock source must declare fixture authentication")
		}
	case SourceReal:
		switch c.Authentication {
		case AuthHMACSHA256, AuthMutualTLS, AuthWorkloadIdentity:
		default:
			return errors.New("real source must declare a supported authentication profile")
		}
	default:
		return errors.New("source kind must be mock or real")
	}
	return nil
}

// SourceIdentity binds a receipt to its deployment-configured source and
// credential key. Tenant/contract authorization and credential verification
// remain server responsibilities; validating these strings is not proof of
// identity or permission.
type SourceIdentity struct {
	TenantID string `json:"tenant_id"`
	SourceID string `json:"source_id"`
	KeyID    string `json:"key_id"`
}

// Validate checks only that a source identity is structurally complete.
func (i SourceIdentity) Validate() error {
	if strings.TrimSpace(i.TenantID) == "" || strings.TrimSpace(i.SourceID) == "" || strings.TrimSpace(i.KeyID) == "" {
		return errors.New("tenant_id, source_id, and key_id are required")
	}
	return nil
}

// Receipt is the minimum stable identity needed to classify an incoming
// source event against its previously persisted receipt.
type Receipt struct {
	SourceID string
	EventID  string
	Digest   string
}

type ReceiptDecision string

const (
	ReceiptNew      ReceiptDecision = "new"
	ReceiptReplay   ReceiptDecision = "replay"
	ReceiptConflict ReceiptDecision = "conflict"
)

// CompareReceipt classifies event-ID deduplication. The persistence layer must
// enforce uniqueness and retain the prior digest durably; this helper alone
// does not prevent replay.
func CompareReceipt(previous, incoming Receipt) (ReceiptDecision, error) {
	if strings.TrimSpace(incoming.SourceID) == "" || strings.TrimSpace(incoming.EventID) == "" || !validDigest(incoming.Digest) {
		return "", errors.New("incoming receipt requires source_id, event_id, and a lowercase SHA-256 digest")
	}
	if previous.EventID == "" {
		return ReceiptNew, nil
	}
	if previous.SourceID != incoming.SourceID || previous.EventID != incoming.EventID {
		return "", errors.New("receipts do not have the same source and event identity")
	}
	if !validDigest(previous.Digest) {
		return "", errors.New("previous receipt digest is invalid")
	}
	if previous.Digest == incoming.Digest {
		return ReceiptReplay, nil
	}
	return ReceiptConflict, nil
}

func validDigest(digest string) bool {
	if len(digest) != 64 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

// SnapshotVersion is the source version and payload digest for one observation.
type SnapshotVersion struct {
	Version  string
	Digest   string
	Sequence uint64
}

type VersionDecision string

const (
	VersionReplay                 VersionDecision = "replay"
	VersionConflict               VersionDecision = "conflict"
	VersionAdvanceCurrent         VersionDecision = "advance_current"
	VersionArchiveObservationOnly VersionDecision = "archive_observation_only"
)

// CompareVersion compares source snapshots without imposing lexical or
// timestamp ordering on opaque provider version strings. Set ordering to
// VersionMonotonicSequence only when the provider contract guarantees a
// monotonic sequence and the calling server has authenticated its provenance.
// Older observations may still be archived, but may not replace current state.
func CompareVersion(current, incoming SnapshotVersion, ordering VersionOrdering) (VersionDecision, error) {
	if ordering != VersionOpaque && ordering != VersionMonotonicSequence {
		return "", errors.New("unsupported version ordering")
	}
	if strings.TrimSpace(incoming.Version) == "" || !validDigest(incoming.Digest) {
		return "", errors.New("incoming snapshot requires version and a lowercase SHA-256 digest")
	}
	if current.Version == "" {
		if ordering == VersionMonotonicSequence && incoming.Sequence == 0 {
			return VersionArchiveObservationOnly, nil
		}
		return VersionAdvanceCurrent, nil
	}
	if !validDigest(current.Digest) {
		return "", errors.New("current snapshot digest is invalid")
	}
	if ordering == VersionOpaque {
		if current.Version != incoming.Version {
			return VersionArchiveObservationOnly, nil
		}
		if current.Digest == incoming.Digest {
			return VersionReplay, nil
		}
		return VersionConflict, nil
	}

	// A provider version is immutable: its content and, in monotonic mode,
	// sequence cannot change. Check this before treating a low sequence as a
	// stale observation, or a mutated old version could be silently archived.
	if current.Version == incoming.Version {
		if current.Digest != incoming.Digest || current.Sequence != incoming.Sequence {
			return VersionConflict, nil
		}
		return VersionReplay, nil
	}
	if incoming.Sequence == 0 || current.Sequence == 0 || incoming.Sequence < current.Sequence {
		return VersionArchiveObservationOnly, nil
	}
	if incoming.Sequence == current.Sequence {
		return VersionConflict, nil
	}
	return VersionAdvanceCurrent, nil
}
