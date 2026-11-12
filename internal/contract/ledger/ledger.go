package ledger

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

const (
	DefaultLimit         = 25
	MaxLimit             = 100
	CursorVersion        = 1
	SortCreated          = "created_at"
	CursorMaxBytes       = 2048
	ContentAmountPattern = `^(0|[1-9][0-9]{0,15})\.[0-9]{2}$`
	TimeLayout           = time.RFC3339
	DateLayout           = "2006-01-02"
)

var amountPattern = regexp.MustCompile(ContentAmountPattern)

type Filter struct {
	Text              string     `json:"text,omitempty"`
	SupplierName      string     `json:"supplier_name,omitempty"`
	DepartmentID      string     `json:"department_id,omitempty"`
	ContractState     string     `json:"contract_state,omitempty"`
	WorkflowState     string     `json:"workflow_state,omitempty"`
	AuthorityMode     string     `json:"authority_mode,omitempty"`
	ValidityState     string     `json:"validity_state,omitempty"`
	ProjectType       string     `json:"project_type,omitempty"`
	ProcurementMethod string     `json:"procurement_method,omitempty"`
	AmountMin         *string    `json:"amount_min,omitempty"`
	AmountMax         *string    `json:"amount_max,omitempty"`
	SigningDateFrom   *string    `json:"signing_date_from,omitempty"`
	SigningDateTo     *string    `json:"signing_date_to,omitempty"`
	CreatedFrom       *time.Time `json:"created_from,omitempty"`
	CreatedTo         *time.Time `json:"created_to,omitempty"`
}

type Item struct {
	ContractID        string    `json:"contract_id"`
	AuthorityMode     string    `json:"authority_mode"`
	Name              string    `json:"name"`
	Number            *string   `json:"number"`
	Amount            *string   `json:"amount"`
	Currency          *string   `json:"currency"`
	ContractState     string    `json:"contract_state"`
	WorkflowState     string    `json:"workflow_state"`
	SupplierName      *string   `json:"supplier_name,omitempty"`
	DepartmentID      *string   `json:"department_id,omitempty"`
	ProjectType       *string   `json:"project_type,omitempty"`
	ProcurementMethod *string   `json:"procurement_method,omitempty"`
	SigningDate       *string   `json:"signing_date,omitempty"`
	SourceSystem      *string   `json:"source_system,omitempty"`
	OwnerOrgID        string    `json:"owner_org_id"`
	RowVersion        int64     `json:"row_version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Page struct {
	Items      []Item  `json:"items"`
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

type Row struct {
	Aggregate clmcontract.Aggregate
	Item      Item
	SortKey   time.Time
}

type Query struct {
	Filter Filter
	Sort   string
	Cursor string
	Limit  int
}

type CursorIdentity struct{ TenantID, ActorID string }

type Cursor struct {
	Version      int       `json:"v"`
	TenantID     string    `json:"t"`
	ActorID      string    `json:"a"`
	Sort         string    `json:"s"`
	FilterDigest string    `json:"f"`
	Key          time.Time `json:"k"`
	ContractID   string    `json:"i"`
}

func ValidateFilter(filter *Filter) error {
	for _, item := range []struct {
		field string
		value *string
		max   int
	}{
		{"text", &filter.Text, 200},
		{"supplier_name", &filter.SupplierName, 200},
		{"department_id", &filter.DepartmentID, 100},
		{"project_type", &filter.ProjectType, 100},
		{"procurement_method", &filter.ProcurementMethod, 100},
	} {
		if err := normalizeText(item.field, item.value, item.max); err != nil {
			return err
		}
	}
	if filter.ContractState != "" && !lifecycleStates[filter.ContractState] {
		return fault.Invalid("contract_state 不是有效的合同状态")
	}
	if filter.WorkflowState != "" && !workflowStates[filter.WorkflowState] {
		return fault.Invalid("workflow_state 不是有效的会签状态")
	}
	if filter.AuthorityMode != "" && !authorityModes[filter.AuthorityMode] {
		return fault.Invalid("authority_mode 必须是 native 或 external")
	}
	if filter.ValidityState != "" && !validityStates[filter.ValidityState] {
		return fault.Invalid("validity_state 必须是 active 或 voided")
	}
	if err := normalizeAmount("amount_min", &filter.AmountMin); err != nil {
		return err
	}
	if err := normalizeAmount("amount_max", &filter.AmountMax); err != nil {
		return err
	}
	if filter.AmountMin != nil && filter.AmountMax != nil {
		minimum, _ := AmountScaled(*filter.AmountMin)
		maximum, _ := AmountScaled(*filter.AmountMax)
		if minimum > maximum {
			return fault.Invalid("amount_min 不能大于 amount_max")
		}
	}
	if err := normalizeDate("signing_date_from", &filter.SigningDateFrom); err != nil {
		return err
	}
	if err := normalizeDate("signing_date_to", &filter.SigningDateTo); err != nil {
		return err
	}
	if filter.SigningDateFrom != nil && filter.SigningDateTo != nil && *filter.SigningDateFrom > *filter.SigningDateTo {
		return fault.Invalid("signing_date_from 不能晚于 signing_date_to")
	}
	if err := normalizeTime("created_from", filter.CreatedFrom); err != nil {
		return err
	}
	if err := normalizeTime("created_to", filter.CreatedTo); err != nil {
		return err
	}
	if filter.CreatedFrom != nil && filter.CreatedTo != nil && filter.CreatedFrom.After(*filter.CreatedTo) {
		return fault.Invalid("created_from 不能晚于 created_to")
	}
	return nil
}

func ValidateSort(sort string) (string, error) {
	if sort == "" {
		return SortCreated, nil
	}
	if sort != SortCreated {
		return "", fault.Invalid("当前只支持按创建时间排序")
	}
	return sort, nil
}

func FilterDigest(filter Filter, sort string) (string, error) {
	raw, err := json.Marshal(struct {
		Schema string `json:"schema"`
		Filter Filter `json:"filter"`
		Sort   string `json:"sort"`
	}{"clm-contract-ledger-filter/v1", filter, sort})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func EncodeCursor(identity CursorIdentity, sort, digest string, key time.Time, contractID string) string {
	raw, _ := json.Marshal(Cursor{Version: CursorVersion, TenantID: identity.TenantID, ActorID: identity.ActorID,
		Sort: sort, FilterDigest: digest, Key: key.UTC(), ContractID: contractID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeCursor(value string, identity CursorIdentity, sort, digest string) (*Cursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > CursorMaxBytes {
		return nil, fault.Invalid("台账分页游标无效")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var cursor Cursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Version != CursorVersion ||
		cursor.TenantID != identity.TenantID || cursor.ActorID != identity.ActorID || cursor.Sort != sort ||
		cursor.FilterDigest != digest || cursor.Key.IsZero() || !clmcontract.ValidContractUUID(cursor.ContractID) {
		return nil, fault.Invalid("台账分页游标无效")
	}
	return &cursor, nil
}

func ItemFromAggregate(record clmcontract.Aggregate, content []byte, inReview bool) (Item, error) {
	var draft clmcontract.DraftContent
	if len(content) != 0 {
		if err := clmcontract.UnmarshalStoredDraft(content, &draft); err != nil {
			return Item{}, err
		}
	}
	item := Item{ContractID: record.ContractID, AuthorityMode: record.AuthorityMode,
		Number: record.ContractNumber, ContractState: record.LifecycleState, WorkflowState: "not_started",
		OwnerOrgID: record.OwnerOrgID, SourceSystem: record.SourceSystem, RowVersion: record.RowVersion,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
	if record.AuthorityMode == "external" {
		item.ContractState, item.WorkflowState = "external_observed", "external_observed"
	} else if inReview {
		item.WorkflowState = "in_review"
	}
	if basics := draft.Contract; basics != nil {
		item.Name = basics.Name
		if basics.Number != nil {
			item.Number = basics.Number
		}
		item.Amount = clmcontract.OptionalText(basics.Amount)
		item.Currency = clmcontract.OptionalText(basics.Currency)
		item.DepartmentID = clmcontract.OptionalText(basics.DepartmentID)
		item.ProjectType = clmcontract.OptionalText(basics.ProjectType)
		item.ProcurementMethod = clmcontract.OptionalText(basics.ProcurementMethod)
		item.SigningDate = clmcontract.OptionalText(basics.SigningDate)
	}
	if draft.Supplier != nil {
		item.SupplierName = clmcontract.OptionalText(draft.Supplier.Name)
	}
	return item, nil
}

func AmountScaled(value string) (int64, bool) {
	if !amountPattern.MatchString(value) {
		return 0, false
	}
	parts := strings.SplitN(value, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, false
	}
	cents, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return whole*100 + cents, true
}

func normalizeText(field string, value *string, max int) error {
	trimmed := strings.TrimSpace(*value)
	if utf8RuneCount(trimmed) > max {
		return fault.Invalid(field + "超过最大长度")
	}
	*value = trimmed
	return nil
}
func normalizeAmount(field string, value **string) error {
	if value == nil || *value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(**value)
	if trimmed == "" {
		*value = nil
		return nil
	}
	if _, ok := AmountScaled(trimmed); !ok {
		return fault.Invalid(field + "必须是两位小数的非负金额字符串")
	}
	*value = &trimmed
	return nil
}
func normalizeDate(field string, value **string) error {
	if value == nil || *value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(**value)
	if trimmed == "" {
		*value = nil
		return nil
	}
	parsed, err := time.Parse(DateLayout, trimmed)
	if err != nil {
		return fault.Invalid(field + "必须是有效日期")
	}
	formatted := parsed.Format(DateLayout)
	*value = &formatted
	return nil
}
func normalizeTime(field string, value *time.Time) error {
	if value == nil {
		return nil
	}
	if value.IsZero() {
		return fault.Invalid(field + "必须是有效时间")
	}
	*value = value.UTC()
	return nil
}
func utf8RuneCount(value string) int { return len([]rune(value)) }

var (
	lifecycleStates = map[string]bool{"draft": true, "ready_to_sign": true, "registered": true, "performing": true, "closed": true, "terminated": true, "external_observed": true}
	workflowStates  = map[string]bool{"not_started": true, "in_review": true, "external_observed": true}
	authorityModes  = map[string]bool{"native": true, "external": true}
	validityStates  = map[string]bool{"active": true, "voided": true}
)
