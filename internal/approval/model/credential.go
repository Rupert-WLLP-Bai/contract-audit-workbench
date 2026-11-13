package model

import "time"

// ApprovalCredentialClaims is the non-secret projection returned with a
// credential. The token itself is the only bearer value; these claims tell the
// page which task, revision and row versions the token is bound to.
type ApprovalCredentialClaims struct {
	TaskID             string    `json:"task_id"`
	ContractID         string    `json:"contract_id"`
	ApprovalRoundID    string    `json:"approval_round_id"`
	ContractRevisionID string    `json:"contract_revision_id"`
	RevisionNumber     int       `json:"revision_number"`
	SnapshotDigest     string    `json:"snapshot_digest"`
	TaskRowVersion     int64     `json:"task_row_version"`
	ContractRowVersion int64     `json:"contract_row_version"`
	AmountLimit        string    `json:"amount_limit"`
	Currency           string    `json:"currency"`
	Role               string    `json:"role"`
	OrgID              string    `json:"org_id"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// ApprovalCredentialResult is the issuance response. It is an internal
// confirmation credential, not a CA certificate or electronic signature.
type ApprovalCredentialResult struct {
	Credential string                   `json:"credential"`
	Claims     ApprovalCredentialClaims `json:"claims"`
}
