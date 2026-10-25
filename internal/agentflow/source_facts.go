package agentflow

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type sourceFactTool struct {
	domain       Domain
	id           string
	capabilities []string
}

type semanticCandidate struct {
	semanticType string
	value        string
	unit         string
	currency     string
	role         string
	start        int
	end          int
	sourceID     string
}

var (
	amountPattern            = regexp.MustCompile(`((合同金额|合同总价|合同价款|采购金额|采购预算|项目预算|审批金额|审批额度|授权额度|预算上限|最高限额|最高金额|责任上限|赔偿上限)\s*[：:为是]?\s*)([0-9][0-9,]{0,39}(\.[0-9]{1,8})?)\s*(亿元|万元|万|元|美元|欧元)`)
	amountApprovalNegation   = regexp.MustCompile(`(?:未(?:经|获|得到|通过)?(?:批准|审批|授权|核准)|尚未(?:批准|审批|授权|核准))\s*$`)
	subjectIDPattern         = regexp.MustCompile(`((统一社会信用代码|社会信用代码)\s*[：:]?\s*)([0-9A-HJ-NPQRTUWXY]{18})`)
	partyPattern             = regexp.MustCompile(`((甲方|乙方|买方|卖方|采购人|供应商|服务方|客户)\s*[：:为是]?\s*)([^，。；;，,\n]{2,40})`)
	signingPartyPattern      = regexp.MustCompile(`((签署方|签约方)\s*[：:为是]?\s*)([^，。；;，,\n]{2,40})`)
	payeePattern             = regexp.MustCompile(`((收款人|收款方|收款单位|付款对象)\s*[：:为是]?\s*)([^，。；;，,\n]{2,40})`)
	scopePattern             = regexp.MustCompile(`((服务范围|适用范围|项目范围|实施范围|线路范围|接入范围|覆盖范围|授权范围)\s*[：:为是]?\s*)([^。；;\n]{2,100})`)
	approvalPattern          = regexp.MustCompile(`((审批部门|审批机关|审批人|授权主体|批准主体)\s*[：:为是]?\s*)([^，。；;，,\n]{2,60})`)
	milestonePattern         = regexp.MustCompile(`((里程碑|交付节点|交付时间|交付日期|验收期限|验收时间|验收日期|验收节点|进场时间|开工日期|开工时间|开通日期|上线日期)\s*[：:为是]?\s*)([^。；;\n]{1,60})`)
	startEventPattern        = regexp.MustCompile(`自[^。；;\n]{1,60}(之日起|开始|启动)`)
	responsibilityPattern    = regexp.MustCompile(`[^。；;\n]{0,40}(负责|责任|义务)[^。；;\n]{0,40}`)
	sourceDateToken          = `(?:[0-9]{4}-[0-9]{1,2}-[0-9]{1,2}|[0-9]{4}/[0-9]{1,2}/[0-9]{1,2}|[0-9]{4}年[0-9]{1,2}月[0-9]{1,2}日)(?:[Tt ](?:[0-9]{2}:[0-9]{2}(?::[0-9]{2}(?:\.[0-9]{1,9})?)?)(?:Z|[+-][0-9]{2}:[0-9]{2})?)?`
	approvalValidityPattern  = regexp.MustCompile(`((?:审批|授权|批准)?有效(?:期|期限)\s*(?:自|从)?\s*[：:]?\s*(` + sourceDateToken + `)\s*(?:起)?\s*(?:至|到|[-—–~～])\s*(` + sourceDateToken + `)(?:止)?)`)
	contractReferencePattern = regexp.MustCompile(`((?:合同)?(?:签订|签署)(?:日期|时间)|(?:合同)?生效(?:日期|时间)|(?:本次)?审查基准(?:日|日期|时间))\s*[：:]?\s*(` + sourceDateToken + `)`)
)

// SourceFactTools compares a finite set of explicit fields across the frozen
// procurement/award, draft-contract, and (when supplied) acceptance sources.
// It never queries an external registry or makes a general legal conclusion.
func SourceFactTools() []Tool {
	return []Tool{
		sourceFactTool{domain: Compliance, id: "source.compliance", capabilities: []string{"amount", "party", "approval_authority", "effective_scope", "approval_validity"}},
		sourceFactTool{domain: Commercial, id: "source.commercial", capabilities: []string{"amount"}},
		sourceFactTool{domain: Technical, id: "source.technical", capabilities: []string{"scope"}},
		sourceFactTool{domain: Fulfillment, id: "source.fulfillment", capabilities: []string{"milestone", "explicit_start_event"}},
		sourceFactTool{domain: Counterparty, id: "source.counterparty", capabilities: []string{"subject_id", "signing_party", "payee", "responsibility"}},
	}
}

func (tool sourceFactTool) Spec() ToolSpec {
	return ToolSpec{ID: tool.id, Domain: tool.domain, Version: "v1", Effect: "read", Capabilities: append([]string(nil), tool.capabilities...)}
}
func (tool sourceFactTool) Execute(ctx context.Context, input ToolInput) ([]Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.TenantID == "" || input.CaseID == "" || input.Gap.Domain != tool.domain || !containsCapability(tool.capabilities, input.Gap.Capability) {
		return nil, ErrInput
	}
	for _, source := range input.Evidence {
		if source.TenantID != input.TenantID || source.CaseID != input.CaseID ||
			source.DocumentID == "" || source.ParsedDocumentID == "" ||
			source.SHA256 != TextHash(source.Text) || !normalizedDigest.MatchString(source.NormalizedDigest) {
			return nil, ErrInput
		}
	}
	leftDocs, rightDocs := comparisonSources(input.Gap.Domain, input.Gap.Capability, input.Evidence)
	finding := Finding{Domain: tool.domain, Capability: input.Gap.Capability, RuleID: tool.id,
		Relation: "insufficient", Summary: "冻结来源缺少可直接比较的双方字段；此项不构成法律或主体资质结论。",
		ReasonCode: "required_source_or_field_missing", Facts: []SemanticFact{}, Citations: []Citation{}}
	if tool.domain == Counterparty {
		finding.Summary = "只比较本案上传材料中的稳定标识和主体表述，不查询外部登记或推断关联关系。"
	}
	if len(leftDocs) == 0 || len(rightDocs) == 0 {
		return []Finding{finding}, nil
	}
	if input.Gap.Capability == "approval_validity" {
		return compareApprovalValidity(finding, leftDocs, rightDocs)
	}
	if input.Gap.Capability == "signing_party" || input.Gap.Capability == "payee" {
		return compareNamedCounterparty(input.Gap.Capability, finding, leftDocs, rightDocs)
	}
	left, leftAmbiguous := sideCandidate(input.Gap.Capability, leftDocs)
	right, rightAmbiguous := sideCandidate(input.Gap.Capability, rightDocs)
	if leftAmbiguous || rightAmbiguous {
		finding.ReasonCode = "multiple_source_values_require_review"
		finding.Summary = "冻结来源含多个不同的受支持字段值，已保留缺口供人工核对。"
		return []Finding{finding}, nil
	}
	if left == nil || right == nil {
		if unsupportedOnMissingSide(input.Gap.Capability, left, right, leftDocs, rightDocs) {
			finding.Relation = "unsupported"
			finding.ReasonCode = "source_expression_not_supported"
			finding.Summary = "材料提到该检查项，但表达不在本片支持的有限语法内；未推断结果。"
		}
		return []Finding{finding}, nil
	}
	leftFact, leftCitation, err := candidateFact(*left, leftDocs)
	if err != nil {
		return nil, err
	}
	rightFact, rightCitation, err := candidateFact(*right, rightDocs)
	if err != nil {
		return nil, err
	}
	finding.Facts = append(finding.Facts, leftFact, rightFact)
	finding.Citations = append(finding.Citations, leftCitation, rightCitation)
	if input.Gap.Capability == "amount" {
		finding.Relation, finding.ReasonCode, finding.Summary = compareAmountCandidates(*left, *right)
	} else if input.Gap.Capability == "milestone" && left.semanticType != right.semanticType {
		finding.Relation = "insufficient"
		finding.ReasonCode = "milestone_event_type_mismatch"
		finding.Summary = "两侧字段属于不同履约事件，不能因日期相同认定已核对。"
	} else if input.Gap.Capability == "party" && left.semanticType != right.semanticType {
		finding.Relation = "insufficient"
		finding.ReasonCode = "party_role_mismatch"
		finding.Summary = "两侧材料指向不同或未映射的主体角色，不能按名称相同判定一致。"
	} else if input.Gap.Capability == "subject_id" &&
		(left.semanticType == "subject_id" || right.semanticType == "subject_id" || left.semanticType != right.semanticType) {
		finding.Relation = "insufficient"
		finding.ReasonCode = "subject_role_not_comparable"
		finding.Summary = "主体标识缺少相同且明确的角色关联，不能据此关闭主体核验缺口。"
	} else if left.value == right.value && left.unit == right.unit && left.currency == right.currency {
		finding.Relation = "compatible"
		finding.ReasonCode = "frozen_source_values_match"
		finding.Summary = "两侧冻结来源中的受支持字段值一致；此结论仅限该字段的来源一致性。"
	} else {
		finding.Relation = "conflict"
		finding.ReasonCode = "frozen_source_values_differ"
		finding.Summary = "两侧冻结来源中的受支持字段值不一致，需人工复核具体条款和适用背景。"
	}
	return []Finding{finding}, nil
}

func containsCapability(capabilities []string, want string) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func comparisonSources(domain Domain, capability string, evidence []Evidence) (left, right []Evidence) {
	var contract, authority, acceptance []Evidence
	for _, item := range evidence {
		switch item.Role {
		case "draft_contract":
			contract = append(contract, item)
		case "procurement", "award":
			authority = append(authority, item)
		case "attachment":
			acceptance = append(acceptance, item)
		}
	}
	if domain == Fulfillment {
		return contract, acceptance
	}
	return authority, contract
}

func sideCandidate(capability string, sources []Evidence) (*semanticCandidate, bool) {
	values := make(map[string]semanticCandidate)
	for _, source := range sources {
		for _, candidate := range candidatesFor(capability, source.Text) {
			candidate.sourceID = source.ID
			key := candidate.semanticType + "\x00" + candidate.value + "\x00" + candidate.unit + "\x00" + candidate.currency + "\x00" + candidate.role
			if _, exists := values[key]; !exists {
				values[key] = candidate
			}
		}
	}
	if len(values) != 1 {
		return nil, len(values) > 1
	}
	for _, candidate := range values {
		copy := candidate
		return &copy, false
	}
	return nil, false
}

func compareApprovalValidity(finding Finding, authority, contract []Evidence) ([]Finding, error) {
	if hasMalformedApprovalValidity(authority) {
		finding.Relation = "unsupported"
		finding.ReasonCode = "approval_validity_expression_unsupported"
		finding.Summary = "审批材料含无法准确解析的有效期端点；未用其他日期或当前日期替代。"
		return []Finding{finding}, nil
	}
	if hasMalformedContractReferenceDate(contract) {
		finding.Relation = "unsupported"
		finding.ReasonCode = "contract_reference_date_unsupported"
		finding.Summary = "合同材料含无法准确解析的签订、生效或审查基准日期；未用其他日期或当前日期替代。"
		return []Finding{finding}, nil
	}
	from, fromAmbiguous := sideCandidate("approval_valid_from", authority)
	until, untilAmbiguous := sideCandidate("approval_valid_until", authority)
	if fromAmbiguous || untilAmbiguous {
		finding.Relation = "insufficient"
		finding.ReasonCode = "multiple_approval_validity_intervals_require_review"
		finding.Summary = "审批材料包含多个不同有效区间，需人工确认适用区间。"
		return []Finding{finding}, nil
	}
	if from == nil || until == nil {
		if hasUnsupportedIndicator("approval_validity", authority) {
			finding.Relation = "unsupported"
			finding.ReasonCode = "approval_validity_expression_unsupported"
			finding.Summary = "审批材料提及有效期，但日期不完整、含糊或不在支持的有限语法内；未推断结果。"
		} else {
			finding.ReasonCode = "approval_validity_interval_missing"
			finding.Summary = "冻结审批材料没有可核验的完整有效区间。"
		}
		return []Finding{finding}, nil
	}
	if from.sourceID != until.sourceID || from.unit != until.unit {
		finding.Relation = "insufficient"
		finding.ReasonCode = "approval_validity_interval_not_comparable"
		finding.Summary = "有效区间端点缺少同一冻结来源或一致的日期/时区精度，未推断适用性。"
		return []Finding{finding}, nil
	}
	references := sourceCandidates("contract_reference_date", contract)
	if len(references) == 0 {
		if hasUnsupportedIndicator("contract_reference_date", contract) {
			finding.Relation = "unsupported"
			finding.ReasonCode = "contract_reference_date_unsupported"
			finding.Summary = "合同材料提及签订、生效或审查基准日期，但日期格式或时区不受支持。"
		} else {
			finding.ReasonCode = "contract_reference_date_missing"
			finding.Summary = "合同材料没有明确的签订、生效或审查基准日期；不使用当前日期推断。"
		}
		return []Finding{finding}, nil
	}
	allCandidates := []*semanticCandidate{from, until}
	for index := range references {
		allCandidates = append(allCandidates, &references[index])
	}
	for _, candidate := range allCandidates {
		fact, citation, err := candidateFact(*candidate, append(append([]Evidence(nil), authority...), contract...))
		if err != nil {
			return nil, err
		}
		finding.Facts = append(finding.Facts, fact)
		finding.Citations = append(finding.Citations, citation)
	}
	intervalOrder, ok := compareSourceDates(from.value, until.value, from.unit)
	if !ok || intervalOrder > 0 {
		finding.Relation = "unsupported"
		finding.ReasonCode = "approval_validity_interval_invalid"
		finding.Summary = "审批有效区间无效或端点无法准确比较；未推断适用性。"
		return []Finding{finding}, nil
	}
	for _, reference := range references {
		if reference.unit != from.unit {
			finding.Relation = "unsupported"
			finding.ReasonCode = "approval_validity_timezone_or_precision_mismatch"
			finding.Summary = "审批有效期与合同基准日期的时区或日期精度不同，无法准确比较。"
			return []Finding{finding}, nil
		}
		startOrder, startOK := compareSourceDates(reference.value, from.value, from.unit)
		endOrder, endOK := compareSourceDates(reference.value, until.value, from.unit)
		if !startOK || !endOK {
			finding.Relation = "unsupported"
			finding.ReasonCode = "approval_validity_date_unparseable"
			finding.Summary = "审批有效期与合同基准日期无法准确比较；未推断适用性。"
			return []Finding{finding}, nil
		}
		if startOrder < 0 || endOrder > 0 {
			finding.Relation = "conflict"
			finding.ReasonCode = "contract_reference_outside_approval_validity"
			finding.Summary = "合同明确的签订、生效或审查基准日期超出审批材料注明的有效区间。"
			return []Finding{finding}, nil
		}
	}
	finding.Relation = "compatible"
	finding.ReasonCode = "contract_reference_within_approval_validity"
	finding.Summary = "合同明确的签订、生效或审查基准日期位于审批材料注明的有效区间内；仅核对材料中的日期关系。"
	return []Finding{finding}, nil
}

func compareNamedCounterparty(capability string, finding Finding, authority, contract []Evidence) ([]Finding, error) {
	left, leftAmbiguous := sideCandidate(capability, authority)
	right, rightAmbiguous := sideCandidate(capability, contract)
	if leftAmbiguous || rightAmbiguous {
		finding.ReasonCode = "multiple_counterparty_values_require_review"
		finding.Summary = "冻结来源包含多个不同的主体名称或角色，需人工核对。"
		return []Finding{finding}, nil
	}
	if left == nil || right == nil {
		if unsupportedOnMissingSide(capability, left, right, authority, contract) {
			finding.Relation = "unsupported"
			finding.ReasonCode = "counterparty_expression_unsupported"
			finding.Summary = "材料提及签约方或收款方，但主体表述不在支持的有限语法内。"
		}
		return []Finding{finding}, nil
	}
	for _, candidate := range []*semanticCandidate{left, right} {
		fact, citation, err := candidateFact(*candidate, append(append([]Evidence(nil), authority...), contract...))
		if err != nil {
			return nil, err
		}
		finding.Facts = append(finding.Facts, fact)
		finding.Citations = append(finding.Citations, citation)
	}
	if left.value != right.value {
		finding.Relation = "conflict"
		finding.ReasonCode = "counterparty_name_text_differs"
		finding.Summary = "冻结来源中的签约方或收款方名称文字不同；此比较不查询外部登记。"
		return []Finding{finding}, nil
	}
	if left.role == "" || right.role == "" || left.role != right.role {
		finding.ReasonCode = "counterparty_role_not_verified"
		finding.Summary = "名称文字相同，但材料未证明两侧名称对应同一明确主体角色；稳定主体缺口仍保留。"
		return []Finding{finding}, nil
	}
	leftID, leftFound, leftOtherRole, leftIDAmbiguous := subjectIDForParty(*left, authority)
	rightID, rightFound, rightOtherRole, rightIDAmbiguous := subjectIDForParty(*right, contract)
	if leftIDAmbiguous || rightIDAmbiguous {
		finding.ReasonCode = "multiple_subject_ids_for_counterparty_role"
		finding.Summary = "同一主体角色关联到多个不同稳定标识，需人工核对。"
		return []Finding{finding}, nil
	}
	if !leftFound || !rightFound {
		if leftOtherRole || rightOtherRole {
			finding.ReasonCode = "subject_id_role_mismatch"
			finding.Summary = "稳定标识未与签约方或收款方的相同角色明确关联，不能据此关闭主体缺口。"
		} else {
			finding.ReasonCode = "stable_subject_id_required"
			finding.Summary = "名称文字相同但缺少两侧经校验且与相同角色关联的稳定主体标识；主体缺口仍保留。"
		}
		return []Finding{finding}, nil
	}
	if leftID.semanticType != rightID.semanticType {
		finding.ReasonCode = "subject_id_role_mismatch"
		finding.Summary = "两侧稳定标识对应的主体角色不同；主体缺口仍保留。"
		return []Finding{finding}, nil
	}
	for _, candidate := range []*semanticCandidate{leftID, rightID} {
		fact, citation, err := candidateFact(*candidate, append(append([]Evidence(nil), authority...), contract...))
		if err != nil {
			return nil, err
		}
		finding.Facts = append(finding.Facts, fact)
		finding.Citations = append(finding.Citations, citation)
	}
	if leftID.value != rightID.value {
		finding.Relation = "conflict"
		finding.ReasonCode = "stable_subject_id_differs_for_role"
		finding.Summary = "相同主体角色的稳定标识不同；名称相同不能消除主体冲突。"
		return []Finding{finding}, nil
	}
	finding.Relation = "compatible"
	finding.ReasonCode = "stable_subject_id_matches_for_role"
	finding.Summary = "名称、经校验的稳定主体标识及其材料内角色关联一致；仅属本案材料的一致性核对，不构成资质结论。"
	return []Finding{finding}, nil
}

func subjectIDForParty(party semanticCandidate, sources []Evidence) (*semanticCandidate, bool, bool, bool) {
	var selected *semanticCandidate
	otherRole := false
	for _, source := range sources {
		if source.ID != party.sourceID {
			continue
		}
		for _, candidate := range candidatesFor("subject_id", source.Text) {
			if !sameSourceClause(source.Text, party.start, candidate.start) {
				continue
			}
			role := strings.TrimPrefix(candidate.semanticType, "subject_id_")
			if !strings.HasPrefix(candidate.semanticType, "subject_id_") || role != party.role {
				otherRole = true
				continue
			}
			candidate.sourceID = source.ID
			if selected != nil && (selected.value != candidate.value || selected.semanticType != candidate.semanticType) {
				return nil, false, otherRole, true
			}
			copy := candidate
			selected = &copy
		}
	}
	return selected, selected != nil, otherRole, false
}

func sameSourceClause(text string, left, right int) bool {
	if left < 0 || right < 0 || left > len(text) || right > len(text) {
		return false
	}
	leftStart := strings.LastIndexAny(text[:left], "。；;\n") + 1
	leftEnd := len(text)
	if relative := strings.IndexAny(text[left:], "。；;\n"); relative >= 0 {
		leftEnd = left + relative
	}
	rightStart := strings.LastIndexAny(text[:right], "。；;\n") + 1
	return leftStart == rightStart && right >= leftStart && right <= leftEnd
}

func sourceCandidates(capability string, sources []Evidence) []semanticCandidate {
	seen := make(map[string]struct{})
	var candidates []semanticCandidate
	for _, source := range sources {
		for _, candidate := range candidatesFor(capability, source.Text) {
			candidate.sourceID = source.ID
			key := candidate.semanticType + "\x00" + candidate.value + "\x00" + candidate.unit + "\x00" + candidate.currency
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func compareSourceDates(left, right, precision string) (int, bool) {
	layout := "2006-01-02"
	if precision == "instant" {
		layout = time.RFC3339Nano
	} else if precision != "date" {
		return 0, false
	}
	leftTime, leftErr := time.Parse(layout, left)
	rightTime, rightErr := time.Parse(layout, right)
	if leftErr != nil || rightErr != nil {
		return 0, false
	}
	if leftTime.Before(rightTime) {
		return -1, true
	}
	if leftTime.After(rightTime) {
		return 1, true
	}
	return 0, true
}

func hasMalformedApprovalValidity(sources []Evidence) bool {
	for _, source := range sources {
		for _, indexes := range approvalValidityPattern.FindAllStringSubmatchIndex(source.Text, 32) {
			if _, _, _, ok := normalizeSourceDate(source.Text[indexes[4]:indexes[5]]); !ok {
				return true
			}
			if _, _, _, ok := normalizeSourceDate(source.Text[indexes[6]:indexes[7]]); !ok {
				return true
			}
		}
	}
	return false
}

func hasMalformedContractReferenceDate(sources []Evidence) bool {
	for _, source := range sources {
		for _, indexes := range contractReferencePattern.FindAllStringSubmatchIndex(source.Text, 32) {
			if _, _, _, ok := normalizeSourceDate(source.Text[indexes[4]:indexes[5]]); !ok {
				return true
			}
		}
	}
	return false
}
func unsupportedOnMissingSide(capability string, left, right *semanticCandidate, leftDocs, rightDocs []Evidence) bool {
	if left == nil && hasUnsupportedIndicator(capability, leftDocs) {
		return true
	}
	return right == nil && hasUnsupportedIndicator(capability, rightDocs)
}

func hasUnsupportedIndicator(capability string, sources []Evidence) bool {
	markers := map[string][]string{
		"amount":                  {"金额", "总价", "价款", "预算", "额度", "限额"},
		"party":                   {"甲方", "乙方", "采购人", "供应商", "买方", "卖方"},
		"approval_authority":      {"审批", "批准", "授权"},
		"effective_scope":         {"范围", "适用"},
		"scope":                   {"范围", "适用"},
		"milestone":               {"里程碑", "交付", "验收", "进场", "开工", "开通", "上线"},
		"explicit_start_event":    {"起算", "之日起", "开始", "启动"},
		"subject_id":              {"统一社会信用代码", "社会信用代码"},
		"signing_party":           {"签署方", "签约方"},
		"payee":                   {"收款人", "收款方", "收款单位", "付款对象"},
		"approval_validity":       {"有效期", "有效期限", "审批期间", "授权期限"},
		"contract_reference_date": {"签订日期", "签订时间", "签署日期", "签署时间", "生效日期", "生效时间", "审查基准"},
		"responsibility":          {"负责", "责任", "义务"},
	}
	for _, source := range sources {
		for _, marker := range markers[capability] {
			if strings.Contains(source.Text, marker) {
				return true
			}
		}
	}
	return false
}

func candidatesFor(capability, text string) []semanticCandidate {
	var candidates []semanticCandidate
	addCapture := func(pattern *regexp.Regexp, semanticType string, valueGroup int) {
		for _, indexes := range pattern.FindAllStringSubmatchIndex(text, 32) {
			valueStart, valueEnd := valueGroup*2, valueGroup*2+1
			if len(indexes) <= valueEnd || indexes[0] < 0 || indexes[1] < indexes[0] ||
				indexes[valueStart] < 0 || indexes[valueEnd] < indexes[valueStart] {
				continue
			}
			value := normalizeSourceValue(text[indexes[valueStart]:indexes[valueEnd]])
			if value != "" {
				candidates = append(candidates, semanticCandidate{semanticType: semanticType, value: value, start: indexes[0], end: indexes[1]})
			}
		}
	}
	addWhole := func(pattern *regexp.Regexp, semanticType string) {
		addWholeMatches(pattern, semanticType, text, &candidates)
	}
	switch capability {
	case "amount":
		for _, indexes := range amountPattern.FindAllStringSubmatchIndex(text, 32) {
			quote := text[indexes[0]:indexes[1]]
			if amountNegatedBefore(text, indexes[0]) {
				continue
			}
			value, unit, currency, ok := normalizeQuotedAmount(quote)
			if ok {
				candidates = append(candidates, semanticCandidate{semanticType: amountSemanticType(text[indexes[4]:indexes[5]]), value: value,
					unit: unit, currency: currency, start: indexes[0], end: indexes[1]})
			}
		}
	case "party", "party_a", "party_b", "party_buyer", "party_seller", "party_service_provider", "party_customer":
		for _, indexes := range partyPattern.FindAllStringSubmatchIndex(text, 32) {
			role := partySemanticType(text[indexes[4]:indexes[5]])
			if capability != "party" && role != capability {
				continue
			}
			candidates = append(candidates, semanticCandidate{semanticType: role,
				value: normalizeSourceValue(text[indexes[6]:indexes[7]]), start: indexes[0], end: indexes[1]})
		}
	case "approval_authority":
		addCapture(approvalPattern, "approval_authority", 3)
	case "effective_scope", "scope":
		addCapture(scopePattern, capability, 3)
	case "milestone", "milestone_delivery", "milestone_acceptance", "milestone_mobilization", "milestone_start", "milestone_activation", "milestone_launch":
		for _, indexes := range milestonePattern.FindAllStringSubmatchIndex(text, 32) {
			kind := milestoneSemanticType(text[indexes[4]:indexes[5]])
			if capability != "milestone" && kind != capability {
				continue
			}
			value, ok := normalizeMilestoneDate(text[indexes[6]:indexes[7]])
			if !ok {
				continue
			}
			candidates = append(candidates, semanticCandidate{semanticType: kind,
				value: value, start: indexes[0], end: indexes[1]})
		}
	case "explicit_start_event":
		addWhole(startEventPattern, "explicit_start_event")
	case "subject_id", "subject_id_party_a", "subject_id_party_b", "subject_id_party_buyer", "subject_id_party_seller", "subject_id_party_service_provider", "subject_id_party_customer":
		for _, indexes := range subjectIDPattern.FindAllStringSubmatchIndex(text, 32) {
			value := text[indexes[6]:indexes[7]]
			if !validUnifiedSocialCreditCode(value) {
				continue
			}
			kind, start := subjectIDSemanticRole(text, indexes[0])
			if capability != "subject_id" && kind != capability {
				continue
			}
			candidates = append(candidates, semanticCandidate{semanticType: kind,
				value: value, start: start, end: indexes[1]})
		}
	case "signing_party":
		for _, indexes := range signingPartyPattern.FindAllStringSubmatchIndex(text, 32) {
			candidates = append(candidates, semanticCandidate{semanticType: "signing_party",
				value: normalizeSourceValue(text[indexes[6]:indexes[7]]), role: partyRoleBefore(text, indexes[0]),
				start: indexes[0], end: indexes[1]})
		}
	case "payee":
		for _, indexes := range payeePattern.FindAllStringSubmatchIndex(text, 32) {
			candidates = append(candidates, semanticCandidate{semanticType: "payee",
				value: normalizeSourceValue(text[indexes[6]:indexes[7]]), role: partyRoleBefore(text, indexes[0]),
				start: indexes[0], end: indexes[1]})
		}
	case "responsibility":
		addWhole(responsibilityPattern, "responsibility")
	case "approval_validity", "approval_valid_from", "approval_valid_until":
		for _, indexes := range approvalValidityPattern.FindAllStringSubmatchIndex(text, 32) {
			fromRaw, untilRaw := text[indexes[4]:indexes[5]], text[indexes[6]:indexes[7]]
			from, fromUnit, _, fromOK := normalizeSourceDate(fromRaw)
			until, untilUnit, _, untilOK := normalizeSourceDate(untilRaw)
			if !fromOK || !untilOK {
				continue
			}
			if capability != "approval_valid_until" {
				candidates = append(candidates, semanticCandidate{semanticType: "approval_valid_from", value: from,
					unit: fromUnit, start: indexes[0], end: indexes[1]})
			}
			if capability != "approval_valid_from" {
				candidates = append(candidates, semanticCandidate{semanticType: "approval_valid_until", value: until,
					unit: untilUnit, start: indexes[0], end: indexes[1]})
			}
		}
	case "contract_reference_date", "contract_signing_date", "contract_effective_date", "contract_review_basis_date":
		for _, indexes := range contractReferencePattern.FindAllStringSubmatchIndex(text, 32) {
			label := text[indexes[2]:indexes[3]]
			semanticType := contractReferenceSemanticType(label)
			if capability != "contract_reference_date" && capability != semanticType {
				continue
			}
			value, unit, _, ok := normalizeSourceDate(text[indexes[4]:indexes[5]])
			if !ok {
				continue
			}
			candidates = append(candidates, semanticCandidate{semanticType: semanticType, value: value, unit: unit,
				start: indexes[0], end: indexes[1]})
		}
	}
	return candidates

}

func normalizeMilestoneDate(raw string) (string, bool) {
	raw = normalizeSourceValue(raw)
	for _, layout := range []struct {
		pattern string
		output  string
	}{
		{"2006-01-02", "2006-01-02"},
		{"2006/01/02", "2006-01-02"},
		{"2006年1月2日", "2006-01-02"},
		{"2006-01-02 15:04", "2006-01-02T15:04:00"},
		{"2006-01-02 15:04:05", "2006-01-02T15:04:05"},
		{"2006/01/02 15:04", "2006-01-02T15:04:00"},
		{"2006/01/02 15:04:05", "2006-01-02T15:04:05"},
		{"2006年1月2日 15:04", "2006-01-02T15:04:00"},
		{"2006年1月2日 15:04:05", "2006-01-02T15:04:05"},
	} {
		parsed, err := time.Parse(layout.pattern, raw)
		if err == nil {
			return parsed.Format(layout.output), true
		}
	}
	return "", false
}

func normalizeSourceDate(raw string) (value, precision, currency string, ok bool) {
	raw = normalizeSourceValue(raw)
	canonical := strings.NewReplacer("年", "-", "月", "-", "日", "", "/", "-").Replace(raw)
	if !strings.ContainsAny(canonical, "Tt ") {
		parsed, err := time.Parse("2006-1-2", canonical)
		if err != nil {
			return "", "", "", false
		}
		return parsed.Format("2006-01-02"), "date", "", true
	}
	separator := strings.IndexAny(canonical, "Tt ")
	if separator < 0 || separator == len(canonical)-1 {
		return "", "", "", false
	}
	datePart := canonical[:separator]
	timePart := canonical[separator+1:]
	if !(strings.HasSuffix(timePart, "Z") || hasExplicitUTCOffset(timePart)) {
		return "", "", "", false
	}
	canonical = datePart + "T" + timePart
	for _, layout := range []string{
		"2006-1-2T15:04:05.999999999Z07:00",
		"2006-1-2T15:04:05Z07:00",
		"2006-1-2T15:04Z07:00",
	} {
		parsed, err := time.Parse(layout, canonical)
		if err == nil {
			return parsed.UTC().Format(time.RFC3339Nano), "instant", "", true
		}
	}
	return "", "", "", false
}

func hasExplicitUTCOffset(value string) bool {
	if len(value) < 6 {
		return false
	}
	index := len(value) - 6
	return (value[index] == '+' || value[index] == '-') && value[index+3] == ':'
}

func contractReferenceSemanticType(label string) string {
	switch {
	case strings.Contains(label, "签订"), strings.Contains(label, "签署"):
		return "contract_signing_date"
	case strings.Contains(label, "生效"):
		return "contract_effective_date"
	default:
		return "contract_review_basis_date"
	}
}

func partyRoleBefore(text string, start int) string {
	if start < 0 || start > len(text) {
		return ""
	}
	role, _ := subjectIDSemanticRole(text, start)
	if !strings.HasPrefix(role, "subject_id_") {
		return ""
	}
	return strings.TrimPrefix(role, "subject_id_")
}

func validUnifiedSocialCreditCode(value string) bool {
	if len(value) != 18 {
		return false
	}
	const alphabet = "0123456789ABCDEFGHJKLMNPQRTUWXY"
	weights := [...]int{1, 3, 9, 27, 19, 26, 16, 17, 20, 29, 25, 13, 8, 24, 10, 30, 28}
	sum := 0
	for index, weight := range weights {
		valueIndex := strings.IndexByte(alphabet, value[index])
		if valueIndex < 0 {
			return false
		}
		sum += valueIndex * weight
	}
	expected := alphabet[(31-sum%31)%31]
	return value[17] == expected
}

func addWholeMatches(pattern *regexp.Regexp, semanticType, text string, candidates *[]semanticCandidate) {
	for _, span := range pattern.FindAllStringIndex(text, 32) {
		if span[0] == span[1] {
			continue
		}
		quote := text[span[0]:span[1]]
		leading := len(quote) - len(strings.TrimLeftFunc(quote, unicode.IsSpace))
		quote = strings.TrimSpace(quote)
		if quote != "" {
			start := span[0] + leading
			*candidates = append(*candidates, semanticCandidate{semanticType: semanticType, value: normalizeSourceValue(quote), start: start, end: start + len(quote)})
		}
	}
}

func candidateFact(candidate semanticCandidate, sources []Evidence) (SemanticFact, Citation, error) {
	for _, evidence := range sources {
		if evidence.ID != candidate.sourceID || candidate.start < 0 || candidate.end > len(evidence.Text) || candidate.end <= candidate.start {
			continue
		}
		quote := evidence.Text[candidate.start:candidate.end]
		value, unit, currency, ok := normalizeSemanticQuote(candidate.semanticType, quote)
		if !ok || value != candidate.value || unit != candidate.unit || currency != candidate.currency {
			return SemanticFact{}, Citation{}, ErrEvidence
		}
		start := utf8.RuneCountInString(evidence.Text[:candidate.start])
		end := start + utf8.RuneCountInString(quote)
		citation := Citation{EvidenceID: evidence.ID, Version: evidence.Version, Quote: quote,
			Start: start, End: end, DocumentID: evidence.DocumentID, ParsedDocumentID: evidence.ParsedDocumentID,
			NormalizedDigest: evidence.NormalizedDigest, Status: "located"}
		key := evidence.ID + "\x00" + candidate.semanticType + "\x00" + quote + "\x00" + candidate.value
		fact := SemanticFact{ID: "fact_" + TextHash(key)[:24], Type: candidate.semanticType,
			Value: candidate.value, Unit: candidate.unit, Currency: candidate.currency, Citation: citation}
		return fact, citation, nil
	}
	return SemanticFact{}, Citation{}, ErrEvidence
}

func sourceSemanticType(semanticType string) bool {
	switch semanticType {
	case "amount", "amount_total", "amount_ceiling", "liability_cap", "party", "party_a", "party_b", "party_buyer", "party_seller", "party_service_provider", "party_customer",
		"approval_authority", "effective_scope", "scope", "milestone", "milestone_delivery", "milestone_acceptance", "milestone_mobilization", "milestone_start", "milestone_activation", "milestone_launch",
		"explicit_start_event", "subject_id", "subject_id_party_a", "subject_id_party_b", "subject_id_party_buyer", "subject_id_party_seller", "subject_id_party_service_provider", "subject_id_party_customer",
		"signing_party", "payee", "responsibility", "approval_valid_from", "approval_valid_until", "contract_signing_date", "contract_effective_date", "contract_review_basis_date":
		return true
	default:
		return false
	}
}

func normalizeSemanticQuote(semanticType, quote string) (value, unit, currency string, ok bool) {
	if semanticType == "amount" || semanticType == "liability_cap" {
		return normalizeQuotedAmount(quote)
	}
	if semanticType == "amount_total" || semanticType == "amount_ceiling" {
		value, unit, currency, ok = normalizeQuotedAmount(quote)
		if !ok {
			return "", "", "", false
		}
		match := amountPattern.FindStringSubmatch(quote)
		if len(match) != 6 || amountSemanticType(match[2]) != semanticType {
			return "", "", "", false
		}
		return value, unit, currency, true
	}
	candidates := candidatesFor(semanticType, quote)
	for _, candidate := range candidates {
		if candidate.start == 0 && candidate.end == len(quote) &&
			(candidate.semanticType == semanticType || semanticType == "party" || semanticType == "milestone") {
			return candidate.value, candidate.unit, candidate.currency, true
		}
	}
	return "", "", "", false
}

func amountSemanticType(label string) string {
	switch label {
	case "合同金额", "合同总价", "合同价款", "采购金额":
		return "amount_total"
	case "采购预算", "项目预算", "审批金额", "审批额度", "授权额度", "预算上限", "最高限额", "最高金额", "责任上限", "赔偿上限":
		return "amount_ceiling"
	default:
		return "amount"
	}
}

func amountNegatedBefore(text string, start int) bool {
	clauseStart := strings.LastIndexAny(text[:start], "。；;\n") + 1
	return amountApprovalNegation.MatchString(strings.TrimSpace(text[clauseStart:start]))
}

func compareAmountCandidates(left, right semanticCandidate) (string, string, string) {
	if left.currency != right.currency {
		return "unsupported", "currency_conversion_not_supported", "两侧金额币种不同且本版没有汇率来源；未换算或推断比较结果。"
	}
	if left.semanticType == "amount_ceiling" && right.semanticType == "amount_total" {
		return compareAmountToCeiling(right.value, left.value)
	}
	if right.semanticType == "amount_ceiling" && left.semanticType == "amount_total" {
		return compareAmountToCeiling(left.value, right.value)
	}
	if left.semanticType != right.semanticType &&
		(left.semanticType == "amount_ceiling" || right.semanticType == "amount_ceiling" || left.semanticType == "amount_total" || right.semanticType == "amount_total") {
		return "insufficient", "amount_semantic_roles_not_comparable", "两侧金额表达含义不同且不构成可直接比较的预算上限与合同金额。"
	}
	if left.value == right.value && left.unit == right.unit {
		return "compatible", "frozen_source_values_match", "两侧冻结来源中的受支持字段值一致；此结论仅限该字段的来源一致性。"
	}
	return "conflict", "frozen_source_values_differ", "两侧冻结来源中的受支持字段值不一致，需人工复核具体条款和适用背景。"
}

func compareAmountToCeiling(amount, ceiling string) (string, string, string) {
	comparison, ok := compareAmountValues(amount, ceiling)
	if !ok {
		return "unsupported", "amount_value_not_comparable", "金额值无法进行精确比较；未推断结果。"
	}
	if comparison <= 0 {
		return "compatible", "contract_amount_within_approved_ceiling", "合同金额未超过冻结来源列明的审批或预算上限。"
	}
	return "conflict", "contract_amount_exceeds_approved_ceiling", "合同金额超过冻结来源列明的审批或预算上限。"
}

func compareAmountValues(left, right string) (int, bool) {
	leftWhole, leftFraction, leftHasFraction := strings.Cut(left, ".")
	rightWhole, rightFraction, rightHasFraction := strings.Cut(right, ".")
	if !validAmountDecimal(leftWhole, leftFraction, leftHasFraction) ||
		!validAmountDecimal(rightWhole, rightFraction, rightHasFraction) {
		return 0, false
	}
	leftWhole = strings.TrimLeft(leftWhole, "0")
	rightWhole = strings.TrimLeft(rightWhole, "0")
	if leftWhole == "" {
		leftWhole = "0"
	}
	if rightWhole == "" {
		rightWhole = "0"
	}
	if len(leftWhole) != len(rightWhole) {
		return len(leftWhole) - len(rightWhole), true
	}
	if comparison := strings.Compare(leftWhole, rightWhole); comparison != 0 {
		return comparison, true
	}
	for index := range max(len(leftFraction), len(rightFraction)) {
		leftDigit, rightDigit := byte('0'), byte('0')
		if index < len(leftFraction) {
			leftDigit = leftFraction[index]
		}
		if index < len(rightFraction) {
			rightDigit = rightFraction[index]
		}
		if leftDigit != rightDigit {
			return int(leftDigit) - int(rightDigit), true
		}
	}
	return 0, true
}

func validAmountDecimal(whole, fraction string, hasFraction bool) bool {
	if whole == "" || hasFraction && fraction == "" {
		return false
	}
	for index := range len(whole) {
		if whole[index] < '0' || whole[index] > '9' {
			return false
		}
	}
	for index := range len(fraction) {
		if fraction[index] < '0' || fraction[index] > '9' {
			return false
		}
	}
	return true
}
func partySemanticType(label string) string {
	switch label {
	case "甲方":
		return "party_a"
	case "乙方":
		return "party_b"
	case "买方", "采购人":
		return "party_buyer"
	case "卖方", "供应商":
		return "party_seller"
	case "服务方":
		return "party_service_provider"
	case "客户":
		return "party_customer"
	default:
		return "party"
	}
}

func milestoneSemanticType(label string) string {
	switch label {
	case "交付节点", "交付时间", "交付日期":
		return "milestone_delivery"
	case "验收期限", "验收时间", "验收日期", "验收节点":
		return "milestone_acceptance"
	case "进场时间":
		return "milestone_mobilization"
	case "开工日期", "开工时间":
		return "milestone_start"
	case "开通日期":
		return "milestone_activation"
	case "上线日期":
		return "milestone_launch"
	default:
		return "milestone"
	}
}

func subjectIDSemanticRole(text string, idStart int) (string, int) {
	clauseStart := strings.LastIndexAny(text[:idStart], "。；;\n") + 1
	prefix := text[clauseStart:idStart]
	roleStart, role := -1, ""
	for _, label := range []string{"甲方", "乙方", "买方", "卖方", "采购人", "供应商", "服务方", "客户"} {
		if index := strings.LastIndex(prefix, label); index > roleStart {
			roleStart, role = index, partySemanticType(label)
		}
	}
	if roleStart < 0 {
		return "subject_id", idStart
	}
	return "subject_id_" + role, clauseStart + roleStart
}

func normalizeSourceValue(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func normalizeQuotedAmount(quote string) (value, unit, currency string, ok bool) {
	match := amountPattern.FindStringSubmatch(quote)
	if len(match) != 6 || match[0] != quote {
		return "", "", "", false
	}
	number, sourceUnit := match[3], match[5]
	whole, fraction, _ := strings.Cut(number, ".")
	groups := strings.Split(whole, ",")
	if groups[0] == "" || len(groups) > 1 && len(groups[0]) > 3 {
		return "", "", "", false
	}
	for _, group := range groups[1:] {
		if len(group) != 3 {
			return "", "", "", false
		}
	}
	digits := strings.TrimLeft(strings.Join(groups, "")+fraction, "0")
	if digits == "" {
		digits = "0"
	}
	power := 0
	switch sourceUnit {
	case "亿元":
		power = 8
	case "万元", "万":
		power = 4
	}
	scale := len(fraction) - power
	if scale <= 0 {
		value = digits + strings.Repeat("0", -scale)
	} else if len(digits) <= scale {
		value = "0." + strings.Repeat("0", scale-len(digits)) + digits
	} else {
		point := len(digits) - scale
		value = digits[:point] + "." + digits[point:]
	}
	if strings.Contains(value, ".") {
		value = strings.TrimRight(strings.TrimRight(value, "0"), ".")
	}
	if value == "" {
		value = "0"
	}
	currency = "CNY"
	if sourceUnit == "美元" {
		currency = "USD"
	} else if sourceUnit == "欧元" {
		currency = "EUR"
	}
	return value, "major_currency", currency, true
}
