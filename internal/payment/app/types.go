package app

import "time"

type ContractPaymentEligibilityInput struct {
	ContractID         string `json:"-"`
	RevisionID         string `json:"contract_revision_id"`
	Sequence           int    `json:"payment_sequence"`
	ExpectedRowVersion int64  `json:"expected_row_version"`
	IdempotencyKey     string `json:"-"`
}

type ContractPaymentEligibility struct {
	CheckID            string    `json:"check_id"`
	ContractID         string    `json:"contract_id"`
	ContractRevisionID string    `json:"contract_revision_id"`
	PaymentSequence    int       `json:"payment_sequence"`
	Status             string    `json:"status"`
	Reasons            []string  `json:"reasons"`
	Evidence           []string  `json:"evidence"`
	CheckedAt          time.Time `json:"checked_at"`
	ContractRowVersion int64     `json:"contract_row_version"`
	InitiatesPayment   bool      `json:"initiates_payment"`
}

type ContractPaymentReceiptInput struct {
	ContractID      string    `json:"contract_id"`
	RevisionID      string    `json:"contract_revision_id"`
	Sequence        int       `json:"payment_sequence"`
	EventID         string    `json:"event_id"`
	EventType       string    `json:"event_type"`
	OriginalEventID string    `json:"original_event_id,omitempty"`
	Amount          string    `json:"amount"`
	Currency        string    `json:"currency"`
	OccurredAt      time.Time `json:"occurred_at"`
}

type TrustedPaymentSource struct{ TenantID, SourceID, KeyID string }

type ContractPaymentReceipt struct {
	ReceiptID             string    `json:"receipt_id"`
	ContractID            string    `json:"contract_id"`
	ContractRevisionID    string    `json:"contract_revision_id"`
	PaymentSequence       int       `json:"payment_sequence"`
	SourceID              string    `json:"source_id"`
	SourceKeyID           string    `json:"source_key_id"`
	SourceEventID         string    `json:"source_event_id"`
	EventType             string    `json:"event_type"`
	OriginalSourceEventID string    `json:"original_source_event_id,omitempty"`
	Amount                string    `json:"amount"`
	Currency              string    `json:"currency"`
	OccurredAt            time.Time `json:"occurred_at"`
	RecordedAt            time.Time `json:"recorded_at"`
	PayloadDigest         string    `json:"payload_digest"`
}

type ContractPaymentSummary struct {
	ContractID         string                   `json:"contract_id"`
	ContractRevisionID string                   `json:"contract_revision_id"`
	PaymentSequence    int                      `json:"payment_sequence"`
	PlannedAmount      string                   `json:"planned_amount,omitempty"`
	Currency           string                   `json:"currency,omitempty"`
	NetPaid            string                   `json:"net_paid"`
	State              string                   `json:"state"`
	Receipts           []ContractPaymentReceipt `json:"receipts"`
}
