package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"contractaudit/internal/fault"
)

// Aggregate is the Contract-owned identity and lifecycle row. Its persistence
// adapter may populate it only from the existing authorized transaction.
type Aggregate struct {
	TenantID           string
	ContractID         string
	OwnerOrgID         string
	AuthorityMode      string
	ContractNumber     *string
	SourceSystem       *string
	ExternalContractID *string
	SourceVersion      *string
	LifecycleState     string
	ValidityState      string
	CurrentRevisionID  *string
	RowVersion         int64
	CreatedBy          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Summary is the unrestricted contract summary shared by detail and ledger
// projections.
type Summary struct {
	ContractID    string    `json:"contract_id"`
	AuthorityMode string    `json:"authority_mode"`
	Name          string    `json:"name"`
	Number        *string   `json:"number"`
	Amount        *string   `json:"amount"`
	Currency      *string   `json:"currency"`
	ContractState string    `json:"contract_state"`
	WorkflowState string    `json:"workflow_state"`
	PaymentState  *string   `json:"payment_state,omitempty"`
	RowVersion    int64     `json:"row_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// CreateContractDraftInput is a strict user command. Tenant, actor and
// organization are always supplied by the authenticated Store boundary.
type CreateContractDraftInput struct {
	ExpectedRowVersion int64
	AuthorityMode      string
	Draft              DraftContent
	IdempotencyKey     string
}

// ReplaceContractDraftInput is the Store-compatible draft replacement
// command. Keep field order and default JSON names stable: they participate in
// the existing idempotency digest stored by core.
type ReplaceContractDraftInput struct {
	ContractID         string
	ExpectedRowVersion int64
	Draft              DraftContent
	IdempotencyKey     string
}

// DraftDetail is the Contract-owned projection used by draft commands and
// detail reads. SigningRegistration stays raw here because its response type
// and source-case visibility are owned by the signing Store bridge.
type DraftDetail struct {
	Summary                      Summary         `json:"summary"`
	CurrentRevisionID            *string         `json:"current_revision_id"`
	Draft                        *DraftContent   `json:"draft,omitempty"`
	ActiveRoundID                *string         `json:"active_round_id"`
	AllowedCommands              []string        `json:"allowed_commands"`
	SourceRef                    *SourceRef      `json:"source_ref,omitempty"`
	SourceID                     string          `json:"source_id,omitempty"`
	SourceObservedAt             *time.Time      `json:"source_observed_at,omitempty"`
	SourceObservedState          string          `json:"source_observed_state,omitempty"`
	SourceObservationDisposition string          `json:"source_observation_disposition,omitempty"`
	SigningRegistration          json.RawMessage `json:"signing_registration,omitempty"`
	Warnings                     []string        `json:"warnings"`
}

// CreateDraftResult preserves the #287 application name while sharing one
// authoritative response shape with detail and replacement.
type CreateDraftResult = DraftDetail

// ContractDraftSummary is one caller-owned resumable draft row.
type ContractDraftSummary struct {
	ContractID    string     `json:"contract_id"`
	AuthorityMode string     `json:"authority_mode"`
	Name          string     `json:"name"`
	Number        *string    `json:"number"`
	Amount        *string    `json:"amount"`
	Currency      *string    `json:"currency"`
	ContractState string     `json:"contract_state"`
	WorkflowState string     `json:"workflow_state"`
	RowVersion    int64      `json:"row_version"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

// DraftRecoveryRow carries the Contract identity needed for a current Read
// authorization check before its title and amount can be projected.
type DraftRecoveryRow struct {
	Record    Aggregate
	Name      string
	Amount    *string
	Currency  *string
	UpdatedAt time.Time
}

// DraftDetailFacts contains raw Contract-owned rows. The application layer
// selects the authoritative draft/revision and applies the response rules.
type DraftDetailFacts struct {
	DraftContent      []byte
	HasDraft          bool
	RevisionSnapshot  []byte
	HasRevision       bool
	SourceObservation *DraftSourceObservation
	LegacyImport      *DraftLegacyImport
}

type DraftSourceObservation struct {
	State       string
	Disposition string
	ObservedAt  time.Time
}

type DraftLegacyImport struct {
	ApprovalHistoryStatus string
	ImportedAt            time.Time
}

// WriteReceipt is an idempotency result stored with the business transaction.
type WriteReceipt struct {
	ContractID     string
	RequestDigest  string
	ResponseStatus int
	Response       []byte
}

// ValidateCLMIdempotencyKey enforces the visible-ASCII key contract shared by
// CLM commands.
func ValidateCLMIdempotencyKey(key string) error {
	if len(key) < 8 || len(key) > 128 {
		return fault.Invalid("Idempotency-Key必须是8到128个字符")
	}
	for _, char := range key {
		if char < 0x21 || char > 0x7e {
			return fault.Invalid("Idempotency-Key只能包含可见ASCII字符")
		}
	}
	return nil
}

// RequestDigest fingerprints the typed command; transport headers and receive
// time do not participate. JSON marshaling preserves the existing canonical
// Store representation used by idempotency receipts.
func RequestDigest(routeScope, method, path string, expectedRowVersion int64, payload any) (string, error) {
	raw, err := json.Marshal(struct {
		Schema             string `json:"schema"`
		RouteScope         string `json:"route_scope"`
		Method             string `json:"method"`
		Path               string `json:"path"`
		ExpectedRowVersion int64  `json:"expected_row_version"`
		Payload            any    `json:"payload"`
	}{"clm-write-request/v1", routeScope, method, path, expectedRowVersion, payload})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// SummaryFromDraft creates the unrestricted list projection from one aggregate.
func SummaryFromDraft(record Aggregate, name string, amount, currency *string) Summary {
	item := Summary{
		ContractID: record.ContractID, AuthorityMode: record.AuthorityMode, Name: name,
		Number: record.ContractNumber, Amount: amount, Currency: currency,
		ContractState: record.LifecycleState, WorkflowState: "not_started",
		RowVersion: record.RowVersion, CreatedAt: record.CreatedAt,
	}
	if record.AuthorityMode == "external" {
		item.ContractState, item.WorkflowState = "external_observed", "external_observed"
	}
	return item
}

// DraftCommands reports only commands supported by this aggregate state.
func DraftCommands(record Aggregate, canManage bool) []string {
	if !canManage {
		return []string{}
	}
	if record.AuthorityMode == "native" && record.LifecycleState == "draft" && record.ValidityState == "active" {
		commands := []string{"replace_draft", "freeze_version"}
		if record.CurrentRevisionID != nil {
			commands = append(commands, "submit_for_approval")
		}
		return commands
	}
	return []string{}
}
