package app

import (
	"encoding/json"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})(?:\.[0-9]{1,6})?$`)

func ParseTime(field, raw string) (time.Time, error) {
	value, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, fault.Invalid(field + "必须是含时区的 RFC3339 时间")
	}
	return value.UTC(), nil
}

func ParseDecimal(field, raw string, allowZero bool) (*big.Rat, error) {
	if !decimalPattern.MatchString(raw) {
		return nil, fault.Invalid(field + "必须是18位以内、6位小数的非负十进制字符串")
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok || (!allowZero && value.Sign() <= 0) {
		return nil, fault.Invalid(field + "必须大于0")
	}
	return value, nil
}

func RationalString(value *big.Rat) string {
	if value == nil {
		return "0.000000"
	}
	return value.FloatString(6)
}

func ResultForQuantity(quantity, accepted string) (string, *big.Rat, *big.Rat, error) {
	q, err := ParseDecimal("quantity", quantity, false)
	if err != nil {
		return "", nil, nil, err
	}
	a, err := ParseDecimal("accepted_quantity", accepted, true)
	if err != nil {
		return "", nil, nil, err
	}
	if a.Cmp(q) > 0 {
		return "", nil, nil, fault.Invalid("accepted_quantity不能超过本次验收数量")
	}
	return acceptanceResult(q, a), q, a, nil
}

func ValidateText(field, value string, max int, required bool) error {
	value = strings.TrimSpace(value)
	if (required && value == "") || len([]rune(value)) > max {
		return fault.Invalid(field + "不能为空且不能超过限制长度")
	}
	return nil
}

func normalizeUUIDField(field string, value *string) error {
	normalized, err := clmcontract.NormalizeContractUUID(*value)
	if err != nil {
		return fault.Invalid(field + " 必须是有效 UUID")
	}
	*value = normalized
	return nil
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func acceptanceResult(span, accepted *big.Rat) string {
	if accepted.Sign() == 0 {
		return "failed"
	}
	if new(big.Rat).Sub(span, accepted).Sign() == 0 {
		return "passed"
	}
	return "partial"
}

func sortedEvidence(in []clmcontract.ContractVersionMaterial) []clmcontract.ContractVersionMaterial {
	out := append([]clmcontract.ContractVersionMaterial(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].MaterialID == out[j].MaterialID {
			return out[i].MaterialVersionID < out[j].MaterialVersionID
		}
		return out[i].MaterialID < out[j].MaterialID
	})
	return out
}

type revisionItemMetadata struct {
	ItemID   string
	Name     string
	Quantity string
	Unit     string
}

func frozenItemMetadata(snapshot []byte, itemID string) (revisionItemMetadata, error) {
	metadata := parseRevisionItemMetadata(snapshot)
	item, ok := metadata[itemID]
	if !ok || item.ItemID == "" {
		return revisionItemMetadata{}, fault.Invalid("冻结修订商品行快照缺失")
	}
	return item, nil
}

func parseRevisionItemMetadata(snapshot []byte) map[string]revisionItemMetadata {
	result := map[string]revisionItemMetadata{}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(snapshot, &root); err != nil {
		return result
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(root["items"], &rawItems); err != nil {
		return result
	}
	counts := map[string]int{}
	entries := make([]revisionItemMetadata, 0, len(rawItems))
	for _, raw := range rawItems {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			continue
		}
		var rawID string
		if err := json.Unmarshal(fields["item_id"], &rawID); err != nil {
			continue
		}
		id, err := clmcontract.NormalizeContractUUID(rawID)
		if err != nil {
			continue
		}
		decodeString := func(key string) string {
			var value string
			_ = json.Unmarshal(fields[key], &value)
			return value
		}
		entry := revisionItemMetadata{ItemID: id, Name: decodeString("name"), Quantity: decodeString("quantity"), Unit: decodeString("unit")}
		counts[id]++
		entries = append(entries, entry)
	}
	for _, entry := range entries {
		if counts[entry.ItemID] == 1 {
			result[entry.ItemID] = entry
		}
	}
	return result
}

func deliveryPlanForItem(draft clmcontract.DraftContent, sequence *int, itemID string) (*clmcontract.DraftDeliveryPlan, error) {
	if len(draft.DeliveryPlans) == 0 && sequence == nil {
		return nil, nil
	}
	if sequence == nil {
		return nil, fault.Invalid("必须选择冻结交付计划期次")
	}
	for i := range draft.DeliveryPlans {
		plan := &draft.DeliveryPlans[i]
		if plan.Sequence != *sequence {
			continue
		}
		if plan.ItemID != itemID {
			return nil, fault.Invalid("交付计划商品行与冻结 item_id 不匹配")
		}
		return plan, nil
	}
	return nil, fault.Invalid("交付计划期次不属于冻结修订")
}

func acceptancePlanForItem(draft clmcontract.DraftContent, sequence *int, itemID string) (*clmcontract.DraftAcceptancePlan, error) {
	if len(draft.AcceptancePlans) == 0 && sequence == nil {
		return nil, nil
	}
	if sequence == nil {
		return nil, fault.Invalid("必须选择冻结验收计划期次")
	}
	for i := range draft.AcceptancePlans {
		plan := &draft.AcceptancePlans[i]
		if plan.Sequence != *sequence {
			continue
		}
		if len(plan.ItemIDs) > 0 {
			for _, candidate := range plan.ItemIDs {
				if candidate == itemID {
					return plan, nil
				}
			}
			return nil, fault.Invalid("验收计划不覆盖该冻结商品行")
		}
		return plan, nil
	}
	return nil, fault.Invalid("验收计划期次不属于冻结修订")
}
