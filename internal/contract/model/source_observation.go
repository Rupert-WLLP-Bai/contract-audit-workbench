package contract

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/clmsource"
	"contractaudit/internal/fault"
)

// ContractSourceCaseScope is one exact case admitted by server configuration.
type ContractSourceCaseScope struct {
	OrgID  string `json:"org_id"`
	CaseID string `json:"case_id"`
}

// ContractSourceAuthority is resolved from deployment credentials and is
// never populated from the signed request body.
type ContractSourceAuthority struct {
	TenantID     string
	SourceID     string
	KeyID        string
	Kind         string
	Ordering     clmsource.VersionOrdering
	AllowedCases []ContractSourceCaseScope
}

type ContractSourceObservationInput struct {
	EventID            string
	ExternalContractID string
	SourceVersion      string
	SourceSequence     *uint64
	SourceState        string
	OccurredAt         time.Time
	CaseOrgID          string
	CaseID             string
	Snapshot           DraftContent
	RawBody            []byte
}

type ContractSourceObservationResult struct {
	ObservationID        string    `json:"observation_id"`
	ContractID           string    `json:"contract_id"`
	SourceEventID        string    `json:"source_event_id"`
	SourceKind           string    `json:"source_kind"`
	AuthorityMode        string    `json:"authority_mode"`
	Disposition          string    `json:"disposition"`
	Replayed             bool      `json:"replayed"`
	SourceVersion        string    `json:"source_version"`
	CurrentSourceVersion *string   `json:"current_source_version"`
	ContractRevisionID   *string   `json:"contract_revision_id,omitempty"`
	RecordedAt           time.Time `json:"recorded_at"`
}

// PreparedContractSourceObservation contains validated semantic input. The
// digest snapshot retains originally observed amount/currency when the stored
// snapshot must clear an unknown pair.
type PreparedContractSourceObservation struct {
	Authority           ContractSourceAuthority
	Input               ContractSourceObservationInput
	DigestSnapshot      DraftContent
	AmountCurrencyValid bool
}

// StoredContractSourceObservation is the immutable response projection read
// from a prior source event.
type StoredContractSourceObservation struct {
	ObservationID        string
	ContractID           string
	SourceKind           string
	PayloadDigest        string
	Disposition          string
	RevisionID           *string
	SourceVersion        string
	CurrentSourceVersion *string
	RecordedAt           time.Time
}

type SourceVersionIdentity struct {
	SnapshotDigest string
	Sequence       *uint64
}

type SourceObservationWrite struct {
	TenantID             string
	ObservationID        string
	ContractID           string
	SourceID             string
	SourceKind           string
	SourceKeyID          string
	ExternalContractID   string
	SourceEventID        string
	SourceVersion        string
	CurrentSourceVersion *string
	SourceSequence       *uint64
	PayloadDigest        string
	SnapshotDigest       string
	RawPayloadDigest     string
	RawPayloadCiphertext string
	SourceObservedState  string
	Disposition          string
	ContractRevisionID   *string
	CaseOrgID            string
	CaseID               string
	SourceOccurredAt     time.Time
}

// PrepareContractSourceObservation preserves the ingress validation order and
// normalizes only the typed semantic snapshot; RawBody remains byte-exact.
func PrepareContractSourceObservation(authority ContractSourceAuthority,
	input ContractSourceObservationInput) (PreparedContractSourceObservation, error) {
	if err := validateContractSourceAuthority(authority); err != nil {
		return PreparedContractSourceObservation{}, fault.Invalid("合同来源配置无效")
	}
	if err := validateContractSourceObservationInput(&input, authority.Ordering); err != nil {
		return PreparedContractSourceObservation{}, err
	}
	if !AllowsContractSourceCase(authority.AllowedCases, input.CaseOrgID, input.CaseID) {
		return PreparedContractSourceObservation{}, fault.NotFound("没有找到对应记录")
	}

	normalizedSnapshot, err := cloneSourceSnapshot(input.Snapshot)
	if err != nil {
		return PreparedContractSourceObservation{}, err
	}
	if normalizedSnapshot.Contract == nil {
		return PreparedContractSourceObservation{}, fault.Invalid("snapshot.contract 必须是对象")
	}
	SanitizeDraftReferences(&normalizedSnapshot)
	if normalizedSnapshot.Contract.ProcurementResultPackage != nil || len(normalizedSnapshot.Materials) != 0 {
		return PreparedContractSourceObservation{}, fault.Invalid("外部同步暂不接受本地采购或案件材料引用")
	}
	amountCurrencyValid := sourceAmountCurrencyValid(normalizedSnapshot.Contract)
	observedAmount, observedCurrency := normalizedSnapshot.Contract.Amount, normalizedSnapshot.Contract.Currency
	for i := range normalizedSnapshot.Contract.RelatedParties {
		normalizedSnapshot.Contract.RelatedParties[i].BindingID = ""
	}
	if input.SourceState == "" || strings.TrimSpace(input.SourceState) == "" {
		input.SourceState = "unknown"
	} else {
		input.SourceState = strings.TrimSpace(input.SourceState)
	}
	if !amountCurrencyValid {
		normalizedSnapshot.Contract.Amount = ""
		normalizedSnapshot.Contract.Currency = ""
	}
	if err := ValidateDraftContent(&normalizedSnapshot); err != nil {
		return PreparedContractSourceObservation{}, err
	}

	digestSnapshot := normalizedSnapshot
	if !amountCurrencyValid {
		contract := *normalizedSnapshot.Contract
		contract.Amount = observedAmount
		contract.Currency = observedCurrency
		digestSnapshot.Contract = &contract
	}
	input.Snapshot = normalizedSnapshot
	return PreparedContractSourceObservation{
		Authority: authority, Input: input, DigestSnapshot: digestSnapshot,
		AmountCurrencyValid: amountCurrencyValid,
	}, nil
}

func AllowsContractSourceCase(scopes []ContractSourceCaseScope, orgID, caseID string) bool {
	for _, scope := range scopes {
		if scope.OrgID == orgID && scope.CaseID == caseID {
			return true
		}
	}
	return false
}

func validateContractSourceAuthority(authority ContractSourceAuthority) error {
	if strings.TrimSpace(authority.TenantID) == "" || strings.TrimSpace(authority.SourceID) == "" || strings.TrimSpace(authority.KeyID) == "" {
		return errors.New("source authority identity is incomplete")
	}
	if authority.Kind != string(clmsource.SourceMock) && authority.Kind != string(clmsource.SourceReal) {
		return errors.New("source kind is invalid")
	}
	if authority.Ordering != clmsource.VersionOpaque && authority.Ordering != clmsource.VersionMonotonicSequence {
		return errors.New("source version ordering is invalid")
	}
	return nil
}

func validateContractSourceObservationInput(input *ContractSourceObservationInput, ordering clmsource.VersionOrdering) error {
	if strings.TrimSpace(input.EventID) == "" || len(input.EventID) > 300 || strings.TrimSpace(input.EventID) != input.EventID {
		return fault.Invalid("source event_id 无效")
	}
	if strings.TrimSpace(input.ExternalContractID) == "" || len(input.ExternalContractID) > 200 || strings.TrimSpace(input.ExternalContractID) != input.ExternalContractID {
		return fault.Invalid("external_contract_id 无效")
	}
	if strings.TrimSpace(input.SourceVersion) == "" || len(input.SourceVersion) > 200 || strings.TrimSpace(input.SourceVersion) != input.SourceVersion {
		return fault.Invalid("source_version 无效")
	}
	if input.SourceSequence != nil && (*input.SourceSequence == 0 || ordering != clmsource.VersionMonotonicSequence) {
		return fault.Invalid("source_sequence 与配置的来源版本策略不匹配")
	}
	if len(input.RawBody) == 0 || len(input.RawBody) > 1<<20 {
		return fault.Invalid("来源事件原文长度无效")
	}
	if input.OccurredAt.IsZero() {
		return fault.Invalid("occurred_at 无效")
	}
	if len(input.SourceState) > 200 || strings.ContainsAny(input.SourceState, "\x00\r\n") {
		return fault.Invalid("source_state 无效")
	}
	return nil
}

func sourceAmountCurrencyValid(contract *ContractBasics) bool {
	if contract == nil || contract.Amount == "" || contract.Currency == "" ||
		ValidateDraftMoney("contract.amount", contract.Amount) != nil || !currencyPattern.MatchString(contract.Currency) {
		return false
	}
	return true
}

func cloneSourceSnapshot(snapshot DraftContent) (DraftContent, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return DraftContent{}, err
	}
	var cloned DraftContent
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return DraftContent{}, err
	}
	return cloned, nil
}
