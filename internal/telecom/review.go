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
	object, ok := clauseObject(matchText)
	if !ok {
		return clause{}, false
	}
	matches := clauseValueMatches(index, matchText)
	if len(matches) != 1 || hasUnsupportedClauseQualifier(matchText, allowBounds) {
		return clause{}, false
	}
	value := numeric(matches[0][1])
	if value == nil {
		return clause{}, false
	}
	fact := buildClauseFact(source, definition, object, matches[0], value, matchText, index, offset, text)
	return clause{fact: fact, source: source, tax: taxBasis(matchText), direction: authorityDirection(index, matchText, allowBounds)}, true
}

func clauseObject(text string) (string, bool) {
	objects := objectRE.FindAllStringSubmatch(text, -1)
	if len(objects) != 1 {
		return "", false
	}
	return objects[0][1], true
}

func clauseValueMatches(index int, text string) [][]string {
	switch index {
	case 0:
		return bandwidthRE.FindAllStringSubmatch(text, -1)
	case 1:
		return slaRE.FindAllStringSubmatch(text, -1)
	case 2:
		return advanceRE.FindAllStringSubmatch(text, -1)
	default:
		return nil
	}
}

func hasUnsupportedClauseQualifier(text string, allowBounds bool) bool {
	if containsClauseQualifier(text, []string{"并非", "不是", "未承诺", "未载明", "不提供", "不保证", "另行协商", "区间", "范围", "或者", "浮动"}) {
		return true
	}
	return !allowBounds && containsClauseQualifier(text, []string{"不得低于", "不得超过", "不得高于", "不低于", "不超过", "不少于", "不多于", "至少", "至多", "不高于", "以上", "以下"})
}

func containsClauseQualifier(text string, qualifiers []string) bool {
	for _, qualifier := range qualifiers {
		if strings.Contains(text, qualifier) {
			return true
		}
	}
	return false
}

func buildClauseFact(source rulepack.Source, definition ruleDefinition, object string, match []string, value *big.Rat, text string, index, offset int, quote string) rulepack.Fact {
	fact := rulepack.Fact{RuleID: definition.id, Dimension: definition.dimension, ObjectRef: object, Value: value.RatString(), Unit: definition.unit, SourceRole: source.Role}
	if index == 0 {
		fact.Unit = match[2]
	}
	if index == 1 {
		applySLAMetadata(&fact, text)
	}
	if index == 2 {
		applyAdvanceMetadata(&fact, value, text)
	}
	fact.ID = fmt.Sprintf("%s/%s/%d", definition.id, source.ID, offset)
	fact.Citations = []rulepack.Citation{{SourceID: source.ID, SourceVersion: source.Version, TenantID: source.TenantID, CaseID: source.CaseID, SourceDigest: source.Digest, Start: offset, End: offset + utf8.RuneCountInString(quote), Quote: quote}}
	return fact
}

func applySLAMetadata(fact *rulepack.Fact, text string) {
	applySLAWindow(fact, text)
	applySLACalendar(fact, text)
	applySLAExclusions(fact, text)
}

func applySLAWindow(fact *rulepack.Fact, text string) {
	if strings.Contains(text, "自然月") && !strings.Contains(text, "非自然月") && !strings.Contains(text, "滚动") {
		fact.MeasurementWindow = "calendar_month"
	}
}

func applySLACalendar(fact *rulepack.Fact, text string) {
	if strings.Contains(text, "Asia/Shanghai") {
		fact.TimeZone = "Asia/Shanghai"
	}
	if strings.Contains(text, "gregorian") || strings.Contains(text, "公历") {
		fact.Calendar = "gregorian"
	}
	if strings.Contains(text, "all_service_unavailability") {
		fact.OutageDefinition = "all_service_unavailability"
	}
}

func applySLAExclusions(fact *rulepack.Fact, text string) {
	switch {
	case strings.Contains(text, "无排除项") || strings.Contains(text, "不设排除") || strings.Contains(text, "排除项：无") || strings.Contains(text, "排除项:无"):
		fact.Exclusions = []string{}
	case strings.Contains(text, "planned_maintenance"):
		fact.Exclusions = []string{"planned_maintenance"}
	}
}

func applyAdvanceMetadata(fact *rulepack.Fact, value *big.Rat, text string) {
	fact.Value = new(big.Rat).Quo(value, big.NewRat(100, 1)).RatString()
	if strings.Contains(text, "预付款以合同总额为基础") || strings.Contains(text, "预付款按合同总额") || strings.Contains(text, "合同总额为预付款计算基础") {
		fact.Basis = "contract_total"
	}
}

func authorityDirection(index int, text string, allowBounds bool) string {
	direction := "lower"
	if index == 2 {
		direction = "upper"
	}
	if !allowBounds {
		return direction
	}
	operator := authorityBoundOperator(index, text)
	if operator == "" {
		return direction
	}
	return boundOperatorDirection(operator, direction)
}

func authorityBoundOperator(index int, text string) string {
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
		return ""
	}
	return matches[0][1]
}

func boundOperatorDirection(operator, fallback string) string {
	switch operator {
	case "不得低于", "不低于", "至少", "不少于":
		return "lower"
	case "不得超过", "不得高于", "不高于", "不超过", "不多于", "至多":
		return "upper"
	default:
		return fallback
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
		if !amountSourceRole(source.Role, authority) {
			continue
		}
		for _, sentence := range sourceSentences(source.Text) {
			if !eligibleClauseSentence(sentence.text, index, authority) {
				continue
			}
			if parsed, ok := parseClause(source, sentence.text, sentence.offset, index, definitions, request.Scenario == "cloud_services"); ok {
				out = append(out, parsed)
			}
		}
	}
	return out
}

func eligibleClauseSentence(text string, index int, authority bool) bool {
	if !authority || index != 2 {
		return true
	}
	matchText := rulepack.FoldSoftLineBreaks(text)
	return strings.Contains(matchText, "审批") && strings.Contains(matchText, "上限")
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
	return sameClauseFact(x, y) && a.tax == b.tax && a.direction == b.direction && sameClauseExclusions(x.Exclusions, y.Exclusions)
}

func sameClauseFact(left, right rulepack.Fact) bool {
	return left.ObjectRef == right.ObjectRef && left.Value == right.Value && left.Unit == right.Unit &&
		left.MeasurementWindow == right.MeasurementWindow && left.TimeZone == right.TimeZone &&
		left.Calendar == right.Calendar && left.OutageDefinition == right.OutageDefinition && left.Basis == right.Basis
}

func sameClauseExclusions(left, right []string) bool {
	return (left == nil) == (right == nil) && slices.Equal(left, right)
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
	return validateAuthorityClause(definitions[index], index, items[0])
}

func validateAuthorityClause(definition ruleDefinition, index int, item clause) (clause, string) {
	value := numeric(item.fact.Value)
	maximum := big.NewRat(100, 1)
	if index == 2 {
		maximum = big.NewRat(1, 1)
	}
	if invalidAuthorityValue(value, maximum, index) {
		return clause{}, "authority_value_out_of_range"
	}
	if !authorityEvidenceSupportsValue(definition, item) {
		return clause{}, "authority_semantics_unsupported"
	}
	if index == 1 && missingSLAAuthorityBasis(item.fact) {
		return clause{}, "authority_measurement_basis_missing"
	}
	if index == 2 && missingAdvanceAuthorityBasis(item) {
		return clause{}, "authority_amount_basis_missing"
	}
	return item, ""
}

func invalidAuthorityValue(value, maximum *big.Rat, index int) bool {
	return value == nil || (index != 0 && value.Cmp(maximum) > 0)
}

func missingSLAAuthorityBasis(fact rulepack.Fact) bool {
	return fact.MeasurementWindow == "" || fact.TimeZone == "" || fact.Calendar == "" ||
		fact.OutageDefinition == "" || fact.Exclusions == nil
}

func missingAdvanceAuthorityBasis(item clause) bool {
	return item.fact.Basis == "" || item.tax == ""
}

func contractAmount(request rulepack.Request, object string, authority bool) (*big.Rat, string) {
	var amount *big.Rat
	tax := ""
	for _, source := range request.Sources {
		if !amountSourceRole(source.Role, authority) {
			continue
		}
		for _, line := range strings.Split(source.Text, "\n") {
			current, currentTax, ok := parseContractAmountLine(line, object)
			if !ok {
				continue
			}
			if amount != nil && (amount.Cmp(current) != 0 || currentTax != tax) {
				return nil, ""
			}
			amount, tax = current, currentTax
		}
	}
	return amount, tax
}

func amountSourceRole(role string, authority bool) bool {
	if authority {
		return role == "procurement" || role == "award"
	}
	return role == "draft_contract"
}

func parseContractAmountLine(line, object string) (*big.Rat, string, bool) {
	objects := objectRE.FindAllStringSubmatch(line, -1)
	matches := amountRE.FindAllStringSubmatch(line, -1)
	if len(objects) != 1 || objects[0][1] != object || len(matches) != 1 {
		return nil, "", false
	}
	value := numeric(matches[0][1])
	if value == nil {
		return nil, "", false
	}
	if matches[0][2] == "万元" {
		value.Mul(value, big.NewRat(10000, 1))
	}
	return value, taxBasis(line), true
}

func build(request rulepack.Request) (*rulepack.Pack, map[string]string, map[string]clause, error) {
	definitions := rulesFor(request)
	if len(definitions) != 3 {
		return nil, nil, nil, rulepack.ErrInvalidRequest
	}
	manifest := compilerManifest(request)
	missing, bases := make(map[string]string), make(map[string]clause)
	manifest.Rules = compilerRules(request, definitions, missing, bases)
	if len(manifest.Rules) == 0 {
		return nil, missing, bases, nil
	}
	pack, err := publishCompilerManifest(manifest)
	return pack, missing, bases, err
}

func compilerManifest(request rulepack.Request) rulepack.Manifest {
	manifestID := "telecom-contractual"
	if request.Scenario == "cloud_services" {
		manifestID = "cloud-services-contractual"
	}
	return rulepack.Manifest{SchemaVersion: rulepack.SchemaVersion, ID: manifestID, Version: compilerVersion, TenantID: request.Resource.TenantID, EffectiveFrom: "2000-01-01T00:00:00Z", EffectiveUntil: "2100-01-01T00:00:00Z", Rules: []rulepack.Rule{}, Lifecycle: rulepack.Lifecycle{State: rulepack.StateDraft}}
}

func compilerRules(request rulepack.Request, definitions []ruleDefinition, missing map[string]string, bases map[string]clause) []rulepack.Rule {
	rules := make([]rulepack.Rule, 0, len(definitions))
	for index, definition := range definitions {
		base, reason := baseline(request, index)
		if reason != "" {
			missing[definition.id] = reason
			continue
		}
		bases[definition.id] = base
		rules = append(rules, compilerRule(request, definition, index, base))
	}
	return rules
}

func compilerRule(request rulepack.Request, definition ruleDefinition, index int, base clause) rulepack.Rule {
	required := rulepack.RequiredFact{Dimension: definition.dimension, SourceRoles: []string{"draft_contract"}, ObjectRequired: true, UnitFamily: definition.family}
	threshold := rulepack.Threshold{Value: base.fact.Value, Unit: base.fact.Unit}
	if index == 1 {
		required.MeasurementWindow, required.TimeZone, required.Calendar, required.OutageDefinition, required.Exclusions = base.fact.MeasurementWindow, base.fact.TimeZone, base.fact.Calendar, base.fact.OutageDefinition, base.fact.Exclusions
	}
	if index == 2 {
		required.Basis, threshold.Basis = base.fact.Basis, base.fact.Basis
	}
	return rulepack.Rule{RuleID: definition.id, Version: "1", Domain: definition.domain, CapabilityID: definition.capability, Mandatory: true, BusinessScope: rulepack.BusinessScope{BusinessLine: request.BusinessLine, Scenario: request.Scenario, ContractType: request.ContractType}, TenantID: request.Resource.TenantID, Comparator: definition.comparator, Threshold: threshold, RequiredFact: required, AuthoritySources: []rulepack.AuthoritySource{{ID: base.source.ID, Version: base.source.Version, Kind: "contractual"}}}
}

func publishCompilerManifest(manifest rulepack.Manifest) (*rulepack.Pack, error) {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	pack, err := rulepack.Parse(raw)
	if err != nil {
		return nil, err
	}
	pack, err = pack.Validate()
	if err != nil {
		return nil, err
	}
	// This service attests a deterministic compiler release. It does not record
	// human policy approval, legal validity, or approval of the uploaded contract.
	pack, err = pack.Approve("contract-rule-compiler", pack.Hash())
	if err != nil {
		return nil, err
	}
	pack, err = pack.Publish()
	return pack, err
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
	if err := validateExecutionContext(ctx, request); err != nil {
		return rulepack.Request{}, err
	}
	if !validExecutionScope(request) {
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

func validateExecutionContext(ctx context.Context, request rulepack.Request) error {
	if ctx == nil {
		return rulepack.ErrInvalidRequest
	}
	if !access.Check(access.Review, request.Principal, request.Resource, time.Now()).Allowed {
		return rulepack.ErrUnauthorized
	}
	return ctx.Err()
}

func validExecutionScope(request rulepack.Request) bool {
	return !request.At.IsZero() && request.BusinessLine != "" && request.Scenario != "" && request.ContractType != ""
}

func validateFrozenTelecomPack(request rulepack.Request, pack *rulepack.Pack) error {
	if !frozenPackIdentityMatches(request, pack) {
		return rulepack.ErrInvalidManifest
	}
	definitions := rulesFor(request)
	if len(definitions) != 3 {
		return rulepack.ErrInvalidManifest
	}
	required := ruleDefinitionMap(definitions)
	seen := make(map[string]bool, len(required))
	for _, rule := range pack.Manifest().Rules {
		if err := validateFrozenRule(request, rule, required); err != nil {
			return rulepack.ErrInvalidManifest
		}
		if _, known := required[rule.RuleID]; known {
			seen[rule.RuleID] = true
		}
	}
	return allRequiredRulesSeen(required, seen)
}

func frozenPackIdentityMatches(request rulepack.Request, pack *rulepack.Pack) bool {
	if pack == nil {
		return false
	}
	manifest := pack.Manifest()
	return manifest.Lifecycle.State == rulepack.StatePublished && manifest.TenantID == request.Resource.TenantID
}

func ruleDefinitionMap(definitions []ruleDefinition) map[string]ruleDefinition {
	required := make(map[string]ruleDefinition, len(definitions))
	for _, definition := range definitions {
		required[definition.id] = definition
	}
	return required
}

func validateFrozenRule(request rulepack.Request, rule rulepack.Rule, required map[string]ruleDefinition) error {
	definition, known := required[rule.RuleID]
	if !known {
		if request.Scenario == "cloud_services" {
			return rulepack.ErrInvalidManifest
		}
		return nil
	}
	if !validFrozenRuleShape(request, rule, definition) {
		return rulepack.ErrInvalidManifest
	}
	if request.Scenario == "cloud_services" && !validCloudRule(rule) {
		return rulepack.ErrInvalidManifest
	}
	return nil
}

func validFrozenRuleShape(request rulepack.Request, rule rulepack.Rule, definition ruleDefinition) bool {
	return validFrozenRuleIdentity(request, rule, definition) && validFrozenRuleScope(request, rule) && len(rule.AuthoritySources) > 0
}

func validFrozenRuleIdentity(request rulepack.Request, rule rulepack.Rule, definition ruleDefinition) bool {
	return rule.Mandatory && rule.Comparator == definition.comparator && rule.Domain == definition.domain &&
		rule.CapabilityID == definition.capability && rule.TenantID == request.Resource.TenantID
}

func validFrozenRuleScope(request rulepack.Request, rule rulepack.Rule) bool {
	return rule.BusinessScope.BusinessLine == request.BusinessLine && rule.BusinessScope.Scenario == request.Scenario &&
		rule.BusinessScope.ContractType == request.ContractType
}

func allRequiredRulesSeen(required map[string]ruleDefinition, seen map[string]bool) error {
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
	if !validCloudFactBase(rule.RequiredFact) || !validCloudFactMetadata(rule) {
		return false
	}
	return hasAllowedCloudAuthority(rule.AuthoritySources)
}

func validCloudFactBase(fact rulepack.RequiredFact) bool {
	return len(fact.SourceRoles) == 1 && fact.SourceRoles[0] == "draft_contract" &&
		fact.ObjectRequired && fact.Dimension != "" && fact.UnitFamily != ""
}

func validCloudFactMetadata(rule rulepack.Rule) bool {
	switch rule.RuleID {
	case "CLOUD-TECH-BW-001":
		return validCloudBandwidthFact(rule.RequiredFact)
	case "CLOUD-TECH-SLA-001":
		return validCloudSLAFact(rule.RequiredFact)
	case "CLOUD-COM-ADV-001":
		return validCloudAdvanceFact(rule.RequiredFact)
	default:
		return false
	}
}

func validCloudBandwidthFact(fact rulepack.RequiredFact) bool {
	return fact.Dimension == "minimum_bandwidth" && fact.UnitFamily == "bandwidth_decimal"
}

func validCloudSLAFact(fact rulepack.RequiredFact) bool {
	return fact.Dimension == "monthly_availability" && fact.UnitFamily == "percentage" &&
		fact.MeasurementWindow == "calendar_month" && fact.TimeZone == "Asia/Shanghai" &&
		fact.Calendar == "gregorian" && fact.OutageDefinition == "all_service_unavailability" &&
		fact.Exclusions != nil && len(fact.Exclusions) == 0 && fact.Basis == ""
}

func validCloudAdvanceFact(fact rulepack.RequiredFact) bool {
	return fact.Dimension == "advance_payment_ratio" && fact.UnitFamily == "ratio" && fact.Basis == "contract_total"
}

func hasAllowedCloudAuthority(sources []rulepack.AuthoritySource) bool {
	for _, source := range sources {
		if source.Kind == "internal_policy" || source.Kind == "synthetic_demo" {
			return true
		}
	}
	return false
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
	var err error
	report, err = executeRulePack(ctx, request, pack, missing, report)
	if err != nil {
		return rulepack.Report{}, err
	}
	appendMissingResults(&report, definitions, missing)
	applyBaselineChecks(&report, request, definitions, bases)
	return report, nil
}

func executeRulePack(ctx context.Context, request rulepack.Request, pack *rulepack.Pack, missing map[string]string, empty rulepack.Report) (rulepack.Report, error) {
	if pack == nil {
		return empty, nil
	}
	filtered := make([]rulepack.Fact, 0, len(request.Facts))
	for _, fact := range request.Facts {
		if _, absent := missing[fact.RuleID]; !absent {
			filtered = append(filtered, fact)
		}
	}
	request.Facts = filtered
	return pack.Execute(ctx, request)
}

func appendMissingResults(report *rulepack.Report, definitions []ruleDefinition, missing map[string]string) {
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
}

func applyBaselineChecks(report *rulepack.Report, request rulepack.Request, definitions []ruleDefinition, bases map[string]clause) {
	for index := range report.Results {
		result := &report.Results[index]
		base, exists := bases[result.RuleID]
		if !exists {
			continue
		}
		checkResultFacts(result, request, definitions, base)
		checkSLAResult(result, request, definitions, base)
		result.Citations = append(result.Citations, base.fact.Citations...)
	}
}

func checkResultFacts(result *rulepack.Result, request rulepack.Request, definitions []ruleDefinition, base clause) {
	for _, fact := range request.Facts {
		if fact.RuleID == result.RuleID {
			checkBaselineFact(result, fact, base, request, fact.RuleID == definitions[2].id)
		}
	}
}

func checkSLAResult(result *rulepack.Result, request rulepack.Request, definitions []ruleDefinition, base clause) {
	if result.RuleID != definitions[1].id || result.Relation == rulepack.RelationInsufficient {
		return
	}
	if scanSLAAnnexes(result, request, definitions, base) {
		return
	}
	result.Relation, result.ReasonCode = rulepack.RelationInsufficient, "sla_annex_missing"
}

func scanSLAAnnexes(result *rulepack.Result, request rulepack.Request, definitions []ruleDefinition, base clause) bool {
	found := false
	for _, source := range request.Sources {
		if source.Role == "attachment" && scanSLAAnnexSource(result, source, request, definitions, base) {
			found = true
		}
	}
	return found
}

func scanSLAAnnexSource(result *rulepack.Result, source rulepack.Source, request rulepack.Request, definitions []ruleDefinition, base clause) bool {
	found := false
	for _, sentence := range sourceSentences(source.Text) {
		annex, ok := parseClause(source, sentence.text, sentence.offset, 1, definitions, request.Scenario == "cloud_services")
		if ok {
			found = true
			checkSLAAnnex(result, annex, base, request)
		}
	}
	return found
}

func checkSLAAnnex(result *rulepack.Result, annex, base clause, request rulepack.Request) {
	result.Citations = append(result.Citations, annex.fact.Citations...)
	if !sameSLABasis(annex.fact, base.fact) {
		result.Relation, result.ReasonCode = rulepack.RelationInsufficient, "sla_annex_basis_incomparable"
		return
	}
	checkSLAAnnexValue(result, annex.fact, request.Facts)
}

func sameSLABasis(annex, base rulepack.Fact) bool {
	return annex.ObjectRef == base.ObjectRef && annex.MeasurementWindow == base.MeasurementWindow &&
		annex.TimeZone == base.TimeZone && annex.Calendar == base.Calendar &&
		annex.OutageDefinition == base.OutageDefinition && annex.Exclusions != nil &&
		slices.Equal(annex.Exclusions, base.Exclusions)
}

func checkSLAAnnexValue(result *rulepack.Result, annex rulepack.Fact, facts []rulepack.Fact) {
	for _, fact := range facts {
		if fact.RuleID == result.RuleID && fact.Value != annex.Value && result.Relation != rulepack.RelationInsufficient {
			result.Relation, result.ReasonCode = rulepack.RelationConflict, "contract_sla_annex_conflict"
		}
	}
}

func checkBaselineFact(result *rulepack.Result, fact rulepack.Fact, base clause, request rulepack.Request, compareAmount bool) {
	checkBaselineObject(result, fact, base, request)
	if compareAmount {
		checkBaselineAmount(result, fact, base, request)
	}
}

func checkBaselineObject(result *rulepack.Result, fact rulepack.Fact, base clause, request rulepack.Request) {
	if fact.ObjectRef == base.fact.ObjectRef {
		return
	}
	reason := "circuit_object_mismatch"
	if request.Scenario == "cloud_services" {
		reason = "service_object_mismatch"
	}
	result.Relation, result.ReasonCode = rulepack.RelationInsufficient, reason
}

func checkBaselineAmount(result *rulepack.Result, fact rulepack.Fact, base clause, request rulepack.Request) {
	actual, actualTax := contractAmount(request, fact.ObjectRef, false)
	expected, expectedTax := contractAmount(request, base.fact.ObjectRef, true)
	if actual == nil || expected == nil || actual.Cmp(expected) != 0 || actualTax == "" || actualTax != expectedTax || actualTax != base.tax {
		result.Relation, result.ReasonCode = rulepack.RelationInsufficient, "amount_or_tax_basis_incomparable"
	}
}
