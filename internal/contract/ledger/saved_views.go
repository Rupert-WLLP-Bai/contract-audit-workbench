package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

const (
	savedViewNameMaxRunes = 60
)

// MaxSavedViewColumns is the request and stored-view limit for selected columns.
const MaxSavedViewColumns = 20

// SavedViewIdentity is the server-resolved scope for a personal view.
type SavedViewIdentity struct {
	TenantID string
	ActorID  string
}

// SavedView is one owner's normalized Contract ledger preference.
type SavedView struct {
	ViewID     string    `json:"view_id"`
	Name       string    `json:"name"`
	Filter     Filter    `json:"filter"`
	Columns    []string  `json:"columns"`
	RowVersion int64     `json:"row_version"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// SavedViewInput is the public create request shape retained by the Store facade.
type SavedViewInput struct {
	ViewID  string   `json:"-"`
	Name    string   `json:"name"`
	Filter  Filter   `json:"filter"`
	Columns []string `json:"columns"`
}

// SavedViewUpdateInput is the public replacement request shape retained by the Store facade.
type SavedViewUpdateInput struct {
	ViewID             string   `json:"-"`
	ExpectedRowVersion int64    `json:"expected_row_version"`
	Name               string   `json:"name"`
	Filter             Filter   `json:"filter"`
	Columns            []string `json:"columns"`
}

var savedViewColumnLabels = map[string]string{
	"contract_number":    "合同编号",
	"contract_name":      "合同名称",
	"authority_mode":     "权威模式",
	"supplier_name":      "供应商",
	"amount":             "金额",
	"currency":           "币种",
	"department_id":      "申请部门",
	"project_type":       "项目类型",
	"procurement_method": "采购方式",
	"contract_state":     "合同状态",
	"workflow_state":     "会签状态",
	"signing_date":       "签订日期",
	"source_system":      "来源系统",
	"created_at":         "创建时间",
	"updated_at":         "更新时间",
}

var defaultSavedViewColumns = []string{
	"contract_number", "contract_name", "supplier_name", "amount", "currency",
	"department_id", "contract_state", "workflow_state", "updated_at",
}

// IsSavedViewColumn reports whether a column can be stored in a personal view.
func IsSavedViewColumn(column string) bool {
	_, ok := savedViewColumnLabels[column]
	return ok
}

// SavedViewColumnLabel returns the stable label for a saved-view column.
func SavedViewColumnLabel(column string) (string, bool) {
	label, ok := savedViewColumnLabels[column]
	return label, ok
}

// DefaultSavedViewColumns returns a copy of the default column selection.
func DefaultSavedViewColumns() []string {
	return append([]string(nil), defaultSavedViewColumns...)
}

// NormalizeSavedViewColumns validates a personal-view column selection.
func NormalizeSavedViewColumns(columns []string) ([]string, error) {
	if len(columns) == 0 {
		return DefaultSavedViewColumns(), nil
	}
	if len(columns) > MaxSavedViewColumns {
		return nil, fault.Invalid("导出或视图列最多 20 项")
	}
	seen := make(map[string]struct{}, len(columns))
	out := make([]string, 0, len(columns))
	for _, column := range columns {
		if !IsSavedViewColumn(column) {
			return nil, fault.Invalid("不支持的台账列")
		}
		if _, ok := seen[column]; ok {
			return nil, fault.Invalid("台账列不能重复")
		}
		seen[column] = struct{}{}
		out = append(out, column)
	}
	return out, nil
}

// NormalizeSavedViewInput trims and validates a saved view's mutable fields.
func NormalizeSavedViewInput(name string, filter *Filter, columns []string) (string, []string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > savedViewNameMaxRunes {
		return "", nil, fault.Invalid("视图名称必须为 1 到 60 个字符")
	}
	for _, char := range name {
		if char < 0x20 || char == 0x7f {
			return "", nil, fault.Invalid("视图名称不能包含控制字符")
		}
	}
	if filter == nil {
		return "", nil, fault.Invalid("保存视图筛选条件无效")
	}
	if err := ValidateFilter(filter); err != nil {
		return "", nil, err
	}
	normalizedColumns, err := NormalizeSavedViewColumns(columns)
	if err != nil {
		return "", nil, err
	}
	return name, normalizedColumns, nil
}

// DecodeStoredSavedView checks the exact stored JSON shape before decoding,
// rejects duplicate keys and trailing values, and revalidates both fields.
func DecodeStoredSavedView(filterJSON, columnsJSON []byte) (Filter, []string, error) {
	var filter Filter
	if err := decodeStoredJSON(filterJSON, '{', &filter); err != nil {
		return Filter{}, nil, fault.Conflict("保存视图的筛选条件无法读取")
	}
	if err := ValidateFilter(&filter); err != nil {
		return Filter{}, nil, fault.Conflict("保存视图的筛选条件不再有效")
	}
	var columns []string
	if err := decodeStoredJSON(columnsJSON, '[', &columns); err != nil {
		return Filter{}, nil, fault.Conflict("保存视图的列设置无法读取")
	}
	normalized, err := NormalizeSavedViewColumns(columns)
	if err != nil {
		return Filter{}, nil, fault.Conflict("保存视图的列设置不再有效")
	}
	return filter, normalized, nil
}

func decodeStoredJSON(raw []byte, root json.Delim, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("stored saved view contains trailing JSON")
	}
	return validateStoredJSON(raw, root)
}

func validateStoredJSON(raw []byte, root json.Delim) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != root {
		return errors.New("stored saved view has an invalid JSON root")
	}
	if err := consumeStoredJSONContainer(decoder, delim); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("stored saved view contains trailing JSON")
		}
		return err
	}
	return nil
}

func consumeStoredJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return errors.New("stored saved view contains an unexpected delimiter")
	}
	return consumeStoredJSONContainer(decoder, delim)
}

func consumeStoredJSONContainer(decoder *json.Decoder, open json.Delim) error {
	if open == '{' {
		seen := make([]string, 0)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("stored saved view object key is not a string")
			}
			for _, previousKey := range seen {
				if strings.EqualFold(previousKey, key) {
					return errors.New("stored saved view contains a duplicate JSON key")
				}
			}
			seen = append(seen, key)
			if err := consumeStoredJSONValue(decoder); err != nil {
				return err
			}
		}
	} else if open == '[' {
		for decoder.More() {
			if err := consumeStoredJSONValue(decoder); err != nil {
				return err
			}
		}
	} else {
		return errors.New("stored saved view has an invalid JSON container")
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	wantClosing := json.Delim(']')
	if open == '{' {
		wantClosing = json.Delim('}')
	}
	if closing != wantClosing {
		return errors.New("stored saved view has an unclosed JSON container")
	}
	return nil
}

// Keep the aggregate UUID policy here so the application does not need a
// separate Store-only normalizer for saved-view identifiers.
// NormalizeSavedViewID applies the existing Contract UUID policy to a view ID.
func NormalizeSavedViewID(value string) (string, error) {
	return clmcontract.NormalizeContractUUID(value)
}
