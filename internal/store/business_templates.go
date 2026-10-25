package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"unicode"

	"contractaudit/internal/fault"
)

const (
	telecomTemplateID            = "ent_telecom_dedicated_line_v1"
	telecomTemplateVersion       = "1.0.0"
	cloudServicesTemplateID      = "ent_cloud_services_v1"
	cloudServicesTemplateVersion = "1.0.0"
)

const cloudServicesTemplateSource = "云平台服务采购合同（待审草稿）\n甲方（客户）：{{customer}}\n乙方（供应商）：{{supplier}}\n\n云服务 {{service_id}}：最低保障带宽 {{bandwidth_mbps}} Mbit/s。\n云服务 {{service_id}}：SLA 可用率 {{sla_percent}}%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；{{sla_exclusions}}。\n云服务 {{service_id}}：预付款比例 {{prepayment_percent}}%，预付款以合同总额为基础，{{tax_basis}}。\n云服务 {{service_id}}：合同总额 {{amount_cny}} 元，{{tax_basis}}。\n"

const telecomTemplateSource = "政企专线服务合同（待审草稿）\n甲方（客户）：{{customer}}\n乙方（供应商）：{{supplier}}\n\n电路 {{circuit_id}}：最低保障带宽 {{bandwidth_mbps}} Mbit/s。\n电路 {{circuit_id}}：SLA 可用率 {{sla_percent}}%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；{{sla_exclusions}}。\n电路 {{circuit_id}}：预付款比例 {{prepayment_percent}}%，预付款以合同总额为基础，{{tax_basis}}。\n电路 {{circuit_id}}：合同总额 {{amount_cny}} 元，{{tax_basis}}。\n"

var telecomTemplateFields = []TemplateField{
	{Key: "circuit_id", Label: "电路编号", Required: true, Type: "string"},
	{Key: "bandwidth_mbps", Label: "最低保障带宽（Mbit/s）", Required: true, Type: "decimal"},
	{Key: "sla_percent", Label: "SLA 可用率（%）", Required: true, Type: "decimal"},
	{Key: "sla_period", Label: "SLA 统计周期", Required: true, Type: "enum"},
	{Key: "sla_exclusions", Label: "SLA 排除项", Required: true, Type: "enum"},
	{Key: "amount_cny", Label: "合同总额（元）", Required: true, Type: "decimal"},
	{Key: "tax_basis", Label: "税口径", Required: true, Type: "enum"},
	{Key: "prepayment_percent", Label: "预付款比例（%）", Required: true, Type: "decimal"},
	{Key: "customer", Label: "客户名称", Required: true, Type: "string"},
	{Key: "supplier", Label: "供应商名称", Required: true, Type: "string"},
}

var cloudServicesTemplateFields = []TemplateField{
	{Key: "service_id", Label: "云服务编号", Required: true, Type: "string"},
	{Key: "bandwidth_mbps", Label: "最低保障带宽（Mbit/s）", Required: true, Type: "decimal"},
	{Key: "sla_percent", Label: "SLA 可用率（%）", Required: true, Type: "decimal"},
	{Key: "sla_period", Label: "SLA 统计周期", Required: true, Type: "enum"},
	{Key: "sla_exclusions", Label: "SLA 排除项", Required: true, Type: "enum"},
	{Key: "amount_cny", Label: "合同总额（元）", Required: true, Type: "decimal"},
	{Key: "tax_basis", Label: "税口径", Required: true, Type: "enum"},
	{Key: "prepayment_percent", Label: "预付款比例（%）", Required: true, Type: "decimal"},
	{Key: "customer", Label: "客户名称", Required: true, Type: "string"},
	{Key: "supplier", Label: "供应商名称", Required: true, Type: "string"},
}

// Templates returns detached metadata for each immutable published template.
func (s *Store) Templates() []Template {
	return []Template{
		versionedTemplate(telecomTemplateID, "政企专线合同草稿", telecomTemplateVersion, telecomTemplateFields, telecomTemplateSource),
		versionedTemplate(cloudServicesTemplateID, "云平台服务合同草稿", cloudServicesTemplateVersion, cloudServicesTemplateFields, cloudServicesTemplateSource),
	}
}

func versionedTemplate(id, name, version string, sourceFields []TemplateField, source string) Template {
	fields := append([]TemplateField(nil), sourceFields...)
	identity, _ := json.Marshal(struct {
		ID      string          `json:"id"`
		Version string          `json:"version"`
		Fields  []TemplateField `json:"fields"`
		Source  string          `json:"source"`
	}{ID: id, Version: version, Fields: fields, Source: source})
	digest := sha256.Sum256(identity)
	return Template{ID: id, Name: name, Version: version, Digest: hex.EncodeToString(digest[:]), Fields: fields}
}

func renderBusinessTemplate(templateID string, input map[string]string) ([]byte, error) {
	switch templateID {
	case telecomTemplateID:
		return renderTelecomTemplate(templateID, input)
	case cloudServicesTemplateID:
		return renderCloudServicesTemplate(templateID, input)
	default:
		return nil, faultInvalidTemplate()
	}
}

func renderTelecomTemplate(templateID string, input map[string]string) ([]byte, error) {
	return renderConfiguredTemplate(templateID, telecomTemplateID, telecomTemplateFields, telecomTemplateSource, "circuit_id", "电路", input)
}

func renderCloudServicesTemplate(templateID string, input map[string]string) ([]byte, error) {
	return renderConfiguredTemplate(templateID, cloudServicesTemplateID, cloudServicesTemplateFields, cloudServicesTemplateSource, "service_id", "云服务", input)
}

func renderConfiguredTemplate(templateID, expectedID string, fields []TemplateField, source, objectField, objectName string, input map[string]string) ([]byte, error) {
	if templateID != expectedID {
		return nil, faultInvalidTemplate()
	}
	if len(input) != len(fields) {
		return nil, fault.Invalid("模板字段不完整或包含未知字段")
	}
	allowed := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		allowed[field.Key] = struct{}{}
	}
	values := make(map[string]string, len(input))
	for key, raw := range input {
		if _, exists := allowed[key]; !exists {
			return nil, fault.Invalid("模板字段不完整或包含未知字段")
		}
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, fault.Invalid("缺少必填模板字段：" + key)
		}
		values[key] = value
	}
	for _, field := range fields {
		if _, exists := values[field.Key]; !exists {
			return nil, fault.Invalid("缺少必填模板字段：" + field.Key)
		}
	}
	objectID := values[objectField]
	if len(objectID) > 64 || !validCircuitID(objectID) {
		return nil, fault.Invalid(objectName + "编号格式无效")
	}
	var err error
	values["bandwidth_mbps"], err = normalizeBoundedDecimal(values["bandwidth_mbps"], 6, "0.001", "1000000")
	if err != nil {
		return nil, fault.Invalid("最低保障带宽格式或范围无效")
	}
	values["sla_percent"], err = normalizeBoundedDecimal(values["sla_percent"], 4, "0.01", "100")
	if err != nil {
		return nil, fault.Invalid("SLA 可用率格式或范围无效")
	}
	if values["sla_period"] != "calendar_month" {
		return nil, fault.Invalid("当前模板只支持自然月统计周期")
	}
	if values["sla_exclusions"] != "none" && values["sla_exclusions"] != "planned_maintenance" {
		return nil, fault.Invalid("SLA 排除项不在模板范围内")
	}
	values["amount_cny"], err = normalizeBoundedDecimal(values["amount_cny"], 2, "0.01", "1000000000000")
	if err != nil {
		return nil, fault.Invalid("合同总额格式或范围无效")
	}
	switch values["tax_basis"] {
	case "inclusive":
		values["tax_basis"] = "含税"
	case "exclusive":
		values["tax_basis"] = "不含税"
	default:
		return nil, fault.Invalid("税口径无效")
	}
	values["prepayment_percent"], err = normalizeBoundedDecimal(values["prepayment_percent"], 4, "0", "100")
	if err != nil {
		return nil, fault.Invalid("预付款比例格式或范围无效")
	}
	for _, key := range []string{"customer", "supplier"} {
		if !validSingleLineName(values[key], 120) {
			return nil, fault.Invalid("客户或供应商名称格式无效")
		}
	}
	values["sla_period"] = "自然月"
	if values["sla_exclusions"] == "none" {
		values["sla_exclusions"] = "无排除项"
	}
	var builder strings.Builder
	builder.Grow(len(source) + 128)
	for i := 0; i < len(source); {
		if source[i] != '{' || i+1 >= len(source) || source[i+1] != '{' {
			builder.WriteByte(source[i])
			i++
			continue
		}
		end := strings.Index(source[i+2:], "}}")
		if end < 0 {
			return nil, fault.Invalid("模板版本无效")
		}
		key := source[i+2 : i+2+end]
		value, exists := values[key]
		if !exists {
			return nil, fault.Invalid("模板版本包含未知字段")
		}
		builder.WriteString(value)
		i += 2 + end + 2
	}
	return []byte(builder.String()), nil
}

func templateFieldByKey(key string) (TemplateField, bool) {
	for _, field := range telecomTemplateFields {
		if field.Key == key {
			return field, true
		}
	}
	return TemplateField{}, false
}

func validCircuitID(value string) bool {
	if value == "" || !isASCIILetterOrDigit(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if !isASCIILetterOrDigit(c) && c != '.' && c != '_' && c != '/' && c != '-' {
			return false
		}
	}
	return true
}

func isASCIILetterOrDigit(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func normalizeBoundedDecimal(value string, scale int, minimum, maximum string) (string, error) {
	if value == "" || len(value) > 32 {
		return "", errBusinessDecimal
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts) == 2 && (parts[1] == "" || len(parts[1]) > scale) {
		return "", errBusinessDecimal
	}
	for _, part := range parts {
		for i := range part {
			if part[i] < '0' || part[i] > '9' {
				return "", errBusinessDecimal
			}
		}
	}
	integer := strings.TrimLeft(parts[0], "0")
	if integer == "" {
		integer = "0"
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = strings.TrimRight(parts[1], "0")
	}
	canonical := integer
	if fraction != "" {
		canonical += "." + fraction
	}
	number, ok := new(big.Rat).SetString(canonical)
	if !ok {
		return "", errBusinessDecimal
	}
	min, _ := new(big.Rat).SetString(minimum)
	max, _ := new(big.Rat).SetString(maximum)
	if number.Cmp(min) < 0 || number.Cmp(max) > 0 {
		return "", errBusinessDecimal
	}
	return canonical, nil
}

func validSingleLineName(value string, maxRunes int) bool {
	if value == "" || runeCount(value) > maxRunes {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}

var errBusinessDecimal = errors.New("invalid bounded decimal")

func faultInvalidTemplate() error {
	return fault.Invalid("合同模板不存在或版本不可用")
}
