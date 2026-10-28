// Package telecom extracts a bounded, explicit telecom contract vocabulary and
// compiles contractual thresholds into versioned RulePack comparisons.
package telecom

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"contractaudit/internal/access"
	"contractaudit/internal/rulepack"
)

// compilerVersion identifies extraction behavior. v5 preserves full sentences,
// including qualifiers before wrapped clauses. Historical snapshots remain
// readable but cannot be replayed using a different compiler.
const compilerVersion = "5"
const CompilerRef = "telecom-contract-compiler/v" + compilerVersion
const HistoricalCompilerRef = "telecom-contract-compiler/v3"

// IsSyntheticCompiler reports compiler identities produced by this package.
// The current identity and the previous frozen identity are both readable.
// Unknown values are not treated as this compiler.
func IsSyntheticCompiler(ref string) bool {
	return ref == CompilerRef || ref == HistoricalCompilerRef || ref == "telecom-contract-compiler/v4"
}

const authorityBoundOperatorPattern = `不得低于|不得超过|不得高于|不低于|至少|不少于|不高于|不超过|不多于|至多`

var (
	objectRE         = regexp.MustCompile(`(?:电路|线路|云服务)\s*([A-Za-z0-9][A-Za-z0-9._/-]{0,63})`)
	bandwidthRE      = regexp.MustCompile(`(?:最低保障带宽|保障带宽|带宽|保障速率)\s*(?:` + authorityBoundOperatorPattern + `\s*)?[:：]?\s*([0-9]+(?:\.[0-9]+)?)\s*(Mbit/s|Gbit/s)`)
	bandwidthBoundRE = regexp.MustCompile(`(?:最低保障带宽|保障带宽|带宽|保障速率)\s*(` + authorityBoundOperatorPattern + `)\s*[:：]?\s*[0-9]+(?:\.[0-9]+)?\s*(?:Mbit/s|Gbit/s)`)
	slaRE            = regexp.MustCompile(`(?:SLA\s*)?可用率\s*(?:` + authorityBoundOperatorPattern + `\s*)?[:：]?\s*([0-9]+(?:\.[0-9]+)?)\s*[%％]`)
	slaBoundRE       = regexp.MustCompile(`(?:SLA\s*)?可用率\s*(` + authorityBoundOperatorPattern + `)\s*[:：]?\s*[0-9]+(?:\.[0-9]+)?\s*[%％]`)
	advanceRE        = regexp.MustCompile(`预付款(?:比例|上限)?\s*(?:` + authorityBoundOperatorPattern + `\s*)?[:：]?\s*([0-9]+(?:\.[0-9]+)?)\s*[%％]`)
	advanceBoundRE   = regexp.MustCompile(`预付款(?:比例|上限)?\s*(` + authorityBoundOperatorPattern + `)\s*[:：]?\s*[0-9]+(?:\.[0-9]+)?\s*[%％]`)
	amountRE         = regexp.MustCompile(`合同总额\s*[:：]?\s*([0-9]+(?:\.[0-9]+)?)\s*(万元|元)`)
)

type ruleDefinition struct {
	id, comparator, dimension, domain, capability, family, unit string
}

var ruleDefinitions = []ruleDefinition{
	{"ENT-TECH-BW-001", rulepack.ComparatorMinimumBandwidth, "minimum_bandwidth", "technical", "dedicated_line.minimum_bandwidth", "bandwidth_decimal", "Mbit/s"},
	{"ENT-TECH-SLA-001", rulepack.ComparatorMonthlySLA, "monthly_availability", "technical", "dedicated_line.monthly_availability", "percentage", "percent"},
	{"ENT-COM-ADV-001", rulepack.ComparatorAdvancePaymentCeiling, "advance_payment_ratio", "commercial", "payment.advance_ratio.ceiling", "ratio", "ratio"},
}

var cloudRuleDefinitions = []ruleDefinition{
	{"CLOUD-TECH-BW-001", rulepack.ComparatorMinimumBandwidth, "minimum_bandwidth", "technical", "cloud_services.minimum_bandwidth", "bandwidth_decimal", "Mbit/s"},
	{"CLOUD-TECH-SLA-001", rulepack.ComparatorMonthlySLA, "monthly_availability", "technical", "cloud_services.monthly_availability", "percentage", "percent"},
	{"CLOUD-COM-ADV-001", rulepack.ComparatorAdvancePaymentCeiling, "advance_payment_ratio", "commercial", "cloud_services.advance_payment_ceiling", "ratio", "ratio"},
}

func rulesFor(request rulepack.Request) []ruleDefinition {
	if request.BusinessLine != "ENT" || request.ContractType != "service" {
		return nil
	}
	switch request.Scenario {
	case "telecom_dedicated_line":
		return ruleDefinitions
	case "cloud_services":
		return cloudRuleDefinitions
	default:
		return nil
	}
}

type clause struct {
	fact      rulepack.Fact
	source    rulepack.Source
	tax       string
	direction string
}

func taxBasis(text string) string {
	exclusive := strings.Contains(text, "不含税") || strings.Contains(text, "未税")
	inclusive := strings.Contains(strings.ReplaceAll(text, "不含税", ""), "含税")
	if exclusive == inclusive {
		return ""
	}
	if exclusive {
		return "exclusive"
	}
	return "inclusive"
}

func numeric(text string) *big.Rat {
	if len(text) > 96 {
		return nil
	}
	value, ok := new(big.Rat).SetString(text)
	if !ok || value.Sign() < 0 {
		return nil
	}
	return value
}

func parseClause(source rulepack.Source, text string, offset, index int, definitions []ruleDefinition, allowBounds bool) (clause, bool) {
	if index < 0 || index >= len(definitions) {
		return clause{}, false
	}
	definition := definitions[index]
	matchText := rulepack.FoldSoftLineBreaks(text)
	objects := objectRE.FindAllStringSubmatch(matchText, -1)
	if len(objects) != 1 {
		return clause{}, false
	}
	var matches [][]string
	switch index {
	case 0:
		matches = bandwidthRE.FindAllStringSubmatch(matchText, -1)
	case 1:
		matches = slaRE.FindAllStringSubmatch(matchText, -1)
	case 2:
		matches = advanceRE.FindAllStringSubmatch(matchText, -1)
	}
	if len(matches) != 1 {
		return clause{}, false
	}
	for _, qualifier := range []string{"并非", "不是", "未承诺", "未载明", "不提供", "不保证", "另行协商", "区间", "范围", "或者", "浮动"} {
		if strings.Contains(matchText, qualifier) {
			return clause{}, false
		}
	}
	if !allowBounds {
		for _, qualifier := range []string{"不得低于", "不得超过", "不得高于", "不低于", "不超过", "不少于", "不多于", "至少", "至多", "不高于", "以上", "以下"} {
			if strings.Contains(matchText, qualifier) {
				return clause{}, false
			}
		}
	}
	value := numeric(matches[0][1])
	if value == nil {
		return clause{}, false
	}
	fact := rulepack.Fact{RuleID: definition.id, Dimension: definition.dimension, ObjectRef: objects[0][1], Value: value.RatString(), Unit: definition.unit, SourceRole: source.Role}
	if index == 0 {
		fact.Unit = matches[0][2]
	}
	if index == 1 {
		if strings.Contains(matchText, "自然月") && !strings.Contains(matchText, "非自然月") && !strings.Contains(matchText, "滚动") {
			fact.MeasurementWindow = "calendar_month"
		}
		if strings.Contains(matchText, "Asia/Shanghai") {
			fact.TimeZone = "Asia/Shanghai"
		}
		if strings.Contains(matchText, "gregorian") || strings.Contains(matchText, "公历") {
			fact.Calendar = "gregorian"
		}
		if strings.Contains(matchText, "all_service_unavailability") {
			fact.OutageDefinition = "all_service_unavailability"
		}
		switch {
		case strings.Contains(matchText, "无排除项") || strings.Contains(matchText, "不设排除") || strings.Contains(matchText, "排除项：无") || strings.Contains(matchText, "排除项:无"):
			fact.Exclusions = []string{}
		case strings.Contains(matchText, "planned_maintenance"):
			fact.Exclusions = []string{"planned_maintenance"}
		}
	}
	if index == 2 {
		fact.Value = new(big.Rat).Quo(value, big.NewRat(100, 1)).RatString()
		if strings.Contains(matchText, "预付款以合同总额为基础") || strings.Contains(matchText, "预付款按合同总额") || strings.Contains(matchText, "合同总额为预付款计算基础") {
			fact.Basis = "contract_total"
		}
	}
	fact.ID = fmt.Sprintf("%s/%s/%d", definition.id, source.ID, offset)
	fact.Citations = []rulepack.Citation{{SourceID: source.ID, SourceVersion: source.Version, TenantID: source.TenantID, CaseID: source.CaseID, SourceDigest: source.Digest, Start: offset, End: offset + utf8.RuneCountInString(text), Quote: text}}
	return clause{fact: fact, source: source, tax: taxBasis(matchText), direction: authorityDirection(index, matchText, allowBounds)}, true
}

func authorityDirection(index int, text string, allowBounds bool) string {
	direction := "lower"
	if index == 2 {
		direction = "upper"
	}
	if !allowBounds {
		return direction
	}
	var matches [][]string
	switch index {
	case 0:
		matches = bandwidthBoundRE.FindAllStringSubmatch(text, -1)
	case 1:
		matches = slaBoundRE.FindAllStringSubmatch(text, -1)
	case 2:
		matches = advanceBoundRE.FindAllStringSubmatch(text, -1)
	}
	if len(matches) != 1 {
		return direction
	}
	switch matches[0][1] {
	case "不得低于", "不低于", "至少", "不少于":
		return "lower"
	case "不得超过", "不得高于", "不高于", "不超过", "不多于", "至多":
		return "upper"
	default:
		return direction
	}
}

type sourceSentence struct {
	text   string
	offset int
}

func sourceSentences(text string) []sourceSentence {
	var out []sourceSentence
	start, startRune, runeOffset := 0, 0, 0
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		if isSourceSentenceBoundary(text, index, r, size) {
			end := index + size
			appendSourceSentence(text, start, startRune, end, &out)
			start = end
			startRune = runeOffset + 1
			index = end
			runeOffset++
			continue
		}
		index += size
		runeOffset++
	}
	appendSourceSentence(text, start, startRune, len(text), &out)
	return out
}

func isSourceSentenceBoundary(text string, index int, r rune, size int) bool {
	switch r {
	case '。', '！', '？', '!', '?':
		return true
	case '.', '．':
		end := index + size
		if end == len(text) {
			return true
		}
		next, _ := utf8.DecodeRuneInString(text[end:])
		return unicode.IsSpace(next)
	default:
		return false
	}
}

func appendSourceSentence(text string, start, startRune, end int, out *[]sourceSentence) {
	for start < end {
		r, size := utf8.DecodeRuneInString(text[start:end])
		if !unicode.IsSpace(r) {
			break
		}
		start += size
		startRune++
	}
	for end > start {
		r, size := utf8.DecodeLastRuneInString(text[start:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	if start < end {
		*out = append(*out, sourceSentence{text: text[start:end], offset: startRune})
	}
}

func clauses(request rulepack.Request, index int, authority bool) []clause {
	definitions := rulesFor(request)
	if index < 0 || index >= len(definitions) {
		return nil
	}
	var out []clause
	for _, source := range request.Sources {
		if authority && source.Role != "procurement" && source.Role != "award" {
			continue
		}
		if !authority && source.Role != "draft_contract" {
			continue
		}
		for _, sentence := range sourceSentences(source.Text) {
			if authority && index == 2 {
				matchText := rulepack.FoldSoftLineBreaks(sentence.text)
				if !strings.Contains(matchText, "审批") || !strings.Contains(matchText, "上限") {
					continue
				}
			}
			if parsed, ok := parseClause(source, sentence.text, sentence.offset, index, definitions, request.Scenario == "cloud_services"); ok {
				out = append(out, parsed)
			}
		}
	}
	return out
}

// Extract returns only statements actually present in the frozen draft. The
// RulePack guard independently checks source identity, Unicode quote and semantics.
func Extract(request rulepack.Request) ([]rulepack.Fact, error) {
	definitions := rulesFor(request)
	if len(definitions) != 3 {
		return nil, rulepack.ErrInvalidRequest
	}
	facts := make([]rulepack.Fact, 0, len(definitions))
	for index := range definitions {
		for _, item := range clauses(request, index, false) {
			facts = append(facts, item.fact)
		}
	}
	return facts, nil
}

func sameClause(a, b clause) bool {
	x, y := a.fact, b.fact
	return x.ObjectRef == y.ObjectRef && x.Value == y.Value && x.Unit == y.Unit && x.MeasurementWindow == y.MeasurementWindow && x.TimeZone == y.TimeZone && x.Calendar == y.Calendar && x.OutageDefinition == y.OutageDefinition && x.Basis == y.Basis && a.tax == b.tax && a.direction == b.direction && (x.Exclusions == nil) == (y.Exclusions == nil) && slices.Equal(x.Exclusions, y.Exclusions)
}

func authorityEvidenceSupportsValue(definition ruleDefinition, item clause) bool {
	return rulepack.EvidenceSupportsAuthority(rulepack.Rule{Comparator: definition.comparator}, item.fact)
}

func baseline(request rulepack.Request, index int) (clause, string) {
	definitions := rulesFor(request)
	if index < 0 || index >= len(definitions) {
		return clause{}, "contract_scope_unsupported"
	}
	items := clauses(request, index, true)
	if len(items) == 0 {
		return clause{}, "contractual_authority_missing"
	}
	for _, item := range items[1:] {
		if !sameClause(items[0], item) {
			return clause{}, "contractual_authority_ambiguous"
		}
	}
	item := items[0]
	value := numeric(item.fact.Value)
	maximum := big.NewRat(100, 1)
	if index == 2 {
		maximum = big.NewRat(1, 1)
	}
	if value == nil || (index != 0 && value.Cmp(maximum) > 0) {
		return clause{}, "authority_value_out_of_range"
	}
	if !authorityEvidenceSupportsValue(definitions[index], item) {
		return clause{}, "authority_semantics_unsupported"
	}
	if index == 1 && (item.fact.MeasurementWindow == "" || item.fact.TimeZone == "" || item.fact.Calendar == "" || item.fact.OutageDefinition == "" || item.fact.Exclusions == nil) {
		return clause{}, "authority_measurement_basis_missing"
	}
	if index == 2 && (item.fact.Basis == "" || item.tax == "") {
		return clause{}, "authority_amount_basis_missing"
	}
	return item, ""
}

func contractAmount(request rulepack.Request, object string, authority bool) (*big.Rat, string) {
	var amount *big.Rat
	tax := ""
	for _, source := range request.Sources {
		if authority && source.Role != "procurement" && source.Role != "award" {
			continue
		}
		if !authority && source.Role != "draft_contract" {
			continue
		}
		for _, line := range strings.Split(source.Text, "\n") {
			objects := objectRE.FindAllStringSubmatch(line, -1)
			matches := amountRE.FindAllStringSubmatch(line, -1)
			if len(objects) != 1 || objects[0][1] != object || len(matches) != 1 {
				continue
			}
			value := numeric(matches[0][1])
			if value == nil {
				continue
			}
			if matches[0][2] == "万元" {
				value.Mul(value, big.NewRat(10000, 1))
			}
			currentTax := taxBasis(line)
			if amount != nil && (amount.Cmp(value) != 0 || currentTax != tax) {
				return nil, ""
			}
			amount, tax = value, currentTax
		}
	}
	return amount, tax
}

func build(request rulepack.Request) (*rulepack.Pack, map[string]string, map[string]clause, error) {
	definitions := rulesFor(request)
	if len(definitions) != 3 {
		return nil, nil, nil, rulepack.ErrInvalidRequest
	}
	missing := make(map[string]string)
	bases := make(map[string]clause)
	manifestID := "telecom-contractual"
	if request.Scenario == "cloud_services" {
		manifestID = "cloud-services-contractual"
	}
	manifest := rulepack.Manifest{SchemaVersion: rulepack.SchemaVersion, ID: manifestID, Version: compilerVersion, TenantID: request.Resource.TenantID, EffectiveFrom: "2000-01-01T00:00:00Z", EffectiveUntil: "2100-01-01T00:00:00Z", Rules: []rulepack.Rule{}, Lifecycle: rulepack.Lifecycle{State: rulepack.StateDraft}}
	for index, definition := range definitions {
		base, reason := baseline(request, index)
		if reason != "" {
			missing[definition.id] = reason
			continue
		}
		bases[definition.id] = base
		required := rulepack.RequiredFact{Dimension: definition.dimension, SourceRoles: []string{"draft_contract"}, ObjectRequired: true, UnitFamily: definition.family}
		threshold := rulepack.Threshold{Value: base.fact.Value, Unit: base.fact.Unit}
		if index == 1 {
			required.MeasurementWindow, required.TimeZone, required.Calendar, required.OutageDefinition, required.Exclusions = base.fact.MeasurementWindow, base.fact.TimeZone, base.fact.Calendar, base.fact.OutageDefinition, base.fact.Exclusions
		}
		if index == 2 {
			required.Basis, threshold.Basis = base.fact.Basis, base.fact.Basis
		}
		manifest.Rules = append(manifest.Rules, rulepack.Rule{RuleID: definition.id, Version: "1", Domain: definition.domain, CapabilityID: definition.capability, Mandatory: true, BusinessScope: rulepack.BusinessScope{BusinessLine: request.BusinessLine, Scenario: request.Scenario, ContractType: request.ContractType}, TenantID: request.Resource.TenantID, Comparator: definition.comparator, Threshold: threshold, RequiredFact: required, AuthoritySources: []rulepack.AuthoritySource{{ID: base.source.ID, Version: base.source.Version, Kind: "contractual"}}})
	}
	if len(manifest.Rules) == 0 {
		return nil, missing, bases, nil
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, nil, nil, err
	}
	pack, err := rulepack.Parse(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	pack, err = pack.Validate()
	if err != nil {
		return nil, nil, nil, err
	}
	// This service attests a deterministic compiler release. It does not record
	// human policy approval, legal validity, or approval of the uploaded contract.
	pack, err = pack.Approve("contract-rule-compiler", pack.Hash())
	if err != nil {
		return nil, nil, nil, err
	}
	pack, err = pack.Publish()
	return pack, missing, bases, err
}

// Snapshot freezes the exact compiler-produced rule manifest for the attempt.
func Snapshot(request rulepack.Request) ([]byte, error) {
	if err := rulepack.ValidateSources(request); err != nil {
		return nil, err
	}
	pack, _, _, err := build(request)
	if err != nil || pack == nil {
		return nil, err
	}
	return pack.Snapshot()
}

// Execute never converts missing authority into a numeric default. Every required
// capability returns a result, even when no executable threshold can be proven.
func Execute(ctx context.Context, request rulepack.Request) (rulepack.Report, error) {
	request, err := prepareExecutionRequest(ctx, request)
	if err != nil {
		return rulepack.Report{}, err
	}
	pack, missing, bases, err := build(request)
	if err != nil {
		return rulepack.Report{}, err
	}
	return executePack(ctx, request, pack, missing, bases)
}

// ExecuteFrozen evaluates the published pack already pinned to an accepted
// attempt. It keeps the contractual-authority, circuit-object, amount/tax and
// SLA-annex checks, while never compiling a replacement policy.
func ExecuteFrozen(ctx context.Context, request rulepack.Request, pack *rulepack.Pack) (rulepack.Report, error) {
	request, err := prepareExecutionRequest(ctx, request)
	if err != nil {
		return rulepack.Report{}, err
	}
	if err := validateFrozenTelecomPack(request, pack); err != nil {
		return rulepack.Report{}, err
	}
	missing, bases := authorityBaselines(request)
	return executePack(ctx, request, pack, missing, bases)
}

func prepareExecutionRequest(ctx context.Context, request rulepack.Request) (rulepack.Request, error) {
	if ctx == nil {
		return rulepack.Request{}, rulepack.ErrInvalidRequest
	}
	if !access.Check(access.Review, request.Principal, request.Resource, time.Now()).Allowed {
		return rulepack.Request{}, rulepack.ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return rulepack.Request{}, err
	}
	if request.At.IsZero() || request.BusinessLine == "" || request.Scenario == "" || request.ContractType == "" {
		return rulepack.Request{}, rulepack.ErrInvalidRequest
	}
	if err := rulepack.ValidateSources(request); err != nil {
		return rulepack.Request{}, err
	}
	if request.Facts == nil {
		facts, err := Extract(request)
		if err != nil {
			return rulepack.Request{}, err
		}
		request.Facts = facts
	}
	return request, nil
}

func validateFrozenTelecomPack(request rulepack.Request, pack *rulepack.Pack) error {
	if pack == nil {
		return rulepack.ErrInvalidManifest
	}
	manifest := pack.Manifest()
	if manifest.Lifecycle.State != rulepack.StatePublished || manifest.TenantID != request.Resource.TenantID {
		return rulepack.ErrInvalidManifest
	}
	definitions := rulesFor(request)
	if len(definitions) != 3 {
		return rulepack.ErrInvalidManifest
	}
	required := make(map[string]ruleDefinition, len(definitions))
	for _, definition := range definitions {
		required[definition.id] = definition
	}
	seen := make(map[string]bool, len(required))
	for _, rule := range manifest.Rules {
		definition, mandatory := required[rule.RuleID]
		if !mandatory {
			if request.Scenario == "cloud_services" {
				return rulepack.ErrInvalidManifest
			}
			continue
		}
		if !rule.Mandatory || rule.Comparator != definition.comparator || rule.Domain != definition.domain || rule.CapabilityID != definition.capability ||
			rule.BusinessScope.BusinessLine != request.BusinessLine || rule.BusinessScope.Scenario != request.Scenario ||
			rule.BusinessScope.ContractType != request.ContractType || rule.TenantID != request.Resource.TenantID || len(rule.AuthoritySources) == 0 {
			return rulepack.ErrInvalidManifest
		}
		if request.Scenario == "cloud_services" && !validCloudRule(rule) {
			return rulepack.ErrInvalidManifest
		}
		seen[rule.RuleID] = true
	}
	for ruleID := range required {
		if !seen[ruleID] {
			return rulepack.ErrInvalidManifest
		}
	}
	return nil
}

func ValidateCloudServicesRulePack(snapshot []byte) error {
	pack, err := rulepack.Parse(snapshot)
	if err != nil {
		return rulepack.ErrInvalidManifest
	}
	manifest := pack.Manifest()
	return validateFrozenTelecomPack(rulepack.Request{
		Resource: access.Resource{TenantID: manifest.TenantID}, BusinessLine: "ENT", Scenario: "cloud_services", ContractType: "service",
	}, pack)
}

func validCloudRule(rule rulepack.Rule) bool {
	if len(rule.RequiredFact.SourceRoles) != 1 || rule.RequiredFact.SourceRoles[0] != "draft_contract" ||
		!rule.RequiredFact.ObjectRequired || rule.RequiredFact.Dimension == "" || rule.RequiredFact.UnitFamily == "" {
		return false
	}
	switch rule.RuleID {
	case "CLOUD-TECH-BW-001":
		if rule.RequiredFact.Dimension != "minimum_bandwidth" || rule.RequiredFact.UnitFamily != "bandwidth_decimal" || hasSLARequirementFields(rule.RequiredFact) || rule.RequiredFact.Basis != "" {
			return false
		}
	case "CLOUD-TECH-SLA-001":
		if rule.RequiredFact.Dimension != "monthly_availability" || rule.RequiredFact.UnitFamily != "percentage" ||
			rule.RequiredFact.MeasurementWindow != "calendar_month" || rule.RequiredFact.TimeZone != "Asia/Shanghai" ||
			rule.RequiredFact.Calendar != "gregorian" || rule.RequiredFact.OutageDefinition != "all_service_unavailability" ||
			rule.RequiredFact.Exclusions == nil || len(rule.RequiredFact.Exclusions) != 0 || rule.RequiredFact.Basis != "" {
			return false
		}
	case "CLOUD-COM-ADV-001":
		if rule.RequiredFact.Dimension != "advance_payment_ratio" || rule.RequiredFact.UnitFamily != "ratio" ||
			rule.RequiredFact.Basis != "contract_total" || hasSLARequirementFields(rule.RequiredFact) {
			return false
		}
	}
	for _, source := range rule.AuthoritySources {
		if source.Kind == "internal_policy" || source.Kind == "synthetic_demo" {
			return true
		}
	}
	return false
}

func hasSLARequirementFields(fact rulepack.RequiredFact) bool {
	return fact.MeasurementWindow != "" || fact.TimeZone != "" || fact.Calendar != "" || fact.OutageDefinition != "" || fact.Exclusions != nil
}
func authorityBaselines(request rulepack.Request) (map[string]string, map[string]clause) {
	missing := make(map[string]string)
	bases := make(map[string]clause)
	for index, definition := range rulesFor(request) {
		base, reason := baseline(request, index)
		if reason != "" {
			missing[definition.id] = reason
			continue
		}
		bases[definition.id] = base
	}
	return missing, bases
}

func executePack(ctx context.Context, request rulepack.Request, pack *rulepack.Pack, missing map[string]string, bases map[string]clause) (rulepack.Report, error) {
	definitions := rulesFor(request)
	if len(definitions) != 3 {
		return rulepack.Report{}, rulepack.ErrInvalidRequest
	}
	packID := "telecom-contractual"
	if request.Scenario == "cloud_services" {
		packID = "cloud-services-contractual"
	}
	report := rulepack.Report{PackID: packID, PackVersion: compilerVersion,
		Relations: []rulepack.Relation{rulepack.RelationCompatible, rulepack.RelationConflict, rulepack.RelationInsufficient, rulepack.RelationNotApplicable},
		Results:   []rulepack.Result{}}
	if pack != nil {
		// Missing authority has no authority-backed facts; retain the mandatory
		// result below even though this pack has an explicit frozen threshold.
		filtered := make([]rulepack.Fact, 0, len(request.Facts))
		for _, fact := range request.Facts {
			if _, absent := missing[fact.RuleID]; !absent {
				filtered = append(filtered, fact)
			}
		}
		request.Facts = filtered
		var err error
		report, err = pack.Execute(ctx, request)
		if err != nil {
			return rulepack.Report{}, err
		}
	}
	for _, definition := range definitions {
		if reason, absent := missing[definition.id]; absent {
			found := false
			for index := range report.Results {
				if report.Results[index].RuleID != definition.id {
					continue
				}
				report.Results[index].Relation = rulepack.RelationInsufficient
				report.Results[index].ReasonCode = reason
				report.Results[index].FactIDs = []string{}
				report.Results[index].Citations = []rulepack.Citation{}
				found = true
			}
			if !found {
				report.Results = append(report.Results, rulepack.Result{RuleID: definition.id, RuleVersion: "1", Domain: definition.domain,
					CapabilityID: definition.capability, Mandatory: true, Relation: rulepack.RelationInsufficient, ReasonCode: reason})
			}
		}
	}
	for index := range report.Results {
		result := &report.Results[index]
		base, exists := bases[result.RuleID]
		if !exists {
			continue
		}
		for _, fact := range request.Facts {
			if fact.RuleID != result.RuleID {
				continue
			}
			if fact.ObjectRef != base.fact.ObjectRef {
				reason := "circuit_object_mismatch"
				if request.Scenario == "cloud_services" {
					reason = "service_object_mismatch"
				}
				result.Relation, result.ReasonCode = rulepack.RelationInsufficient, reason
			}
			if fact.RuleID == definitions[2].id {
				actual, actualTax := contractAmount(request, fact.ObjectRef, false)
				expected, expectedTax := contractAmount(request, base.fact.ObjectRef, true)
				if actual == nil || expected == nil || actual.Cmp(expected) != 0 || actualTax == "" || actualTax != expectedTax || actualTax != base.tax {
					result.Relation, result.ReasonCode = rulepack.RelationInsufficient, "amount_or_tax_basis_incomparable"
				}
			}
		}
		if result.RuleID == definitions[1].id && result.Relation != rulepack.RelationInsufficient {
			annexFound := false
			for _, source := range request.Sources {
				if source.Role != "attachment" {
					continue
				}
				for _, sentence := range sourceSentences(source.Text) {
					annex, ok := parseClause(source, sentence.text, sentence.offset, 1, definitions, request.Scenario == "cloud_services")
					if !ok {
						continue
					}
					annexFound = true
					result.Citations = append(result.Citations, annex.fact.Citations...)
					if annex.fact.ObjectRef != base.fact.ObjectRef || annex.fact.MeasurementWindow != base.fact.MeasurementWindow ||
						annex.fact.TimeZone != base.fact.TimeZone || annex.fact.Calendar != base.fact.Calendar ||
						annex.fact.OutageDefinition != base.fact.OutageDefinition || annex.fact.Exclusions == nil ||
						!slices.Equal(annex.fact.Exclusions, base.fact.Exclusions) {
						result.Relation, result.ReasonCode = rulepack.RelationInsufficient, "sla_annex_basis_incomparable"
						continue
					}
					for _, fact := range request.Facts {
						if fact.RuleID == result.RuleID && fact.Value != annex.fact.Value && result.Relation != rulepack.RelationInsufficient {
							result.Relation, result.ReasonCode = rulepack.RelationConflict, "contract_sla_annex_conflict"
						}
					}
				}
			}
			if !annexFound {
				result.Relation, result.ReasonCode = rulepack.RelationInsufficient, "sla_annex_missing"
			}
		}
		result.Citations = append(result.Citations, base.fact.Citations...)
	}
	return report, nil
}
