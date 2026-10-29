package rulepack

import (
	"math/big"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var evidenceNumberRE = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?(?:/[0-9]+)?`)
var evidenceAuthorityBoundRE = regexp.MustCompile(`(不得低于|不得超过|不得高于|不低于|至少|不少于|不高于|不超过|不多于|至多)\s*[:：]?\s*([0-9]+(?:\.[0-9]+)?(?:/[0-9]+)?)`)

var evidenceNegatedOrRangeTerms = []string{
	"并非", "并不", "并不是", "未承诺", "未载明", "未列明", "未达到", "没有承诺", "不提供", "不保证", "不满足", "不符合",
	"不得低于", "不得超过", "不低于", "不超过", "不少于", "不多于", "至少", "至多", "以上", "以下", "不足", "低于", "少于", "超过", "高于", "区间", "范围", "介于", "至", "到",
	"not provided", "not committed", "not guaranteed", "not meet", "less than", "greater than", "no less than", "no more than", "at least", "at most", "up to", "between", "from", " to ", "below", "above", "exceed",
}

type evidenceNumber struct {
	text       string
	value      *big.Rat
	start, end int
}

// EvidenceSupportsValue accepts only a bounded explicit syntax for measured
// facts. The quoted object, dimension and numeric unit must occur together.
func EvidenceSupportsValue(rule Rule, fact Fact) bool {
	return evidenceSupportsValue(rule, fact, false)
}

// EvidenceSupportsAuthority accepts an authoritative procurement or award
// statement. Comparator-aligned bounds can establish a normative threshold;
// measured facts continue to use EvidenceSupportsValue's stricter semantics.
func EvidenceSupportsAuthority(rule Rule, fact Fact) bool {
	if !authorityRole(fact.SourceRole) || !authorityUnit(rule.Comparator, fact) {
		return false
	}
	return evidenceSupportsValue(rule, fact, true)
}

func authorityRole(role string) bool {
	return role == "procurement" || role == "award"
}

func authorityUnit(comparator string, fact Fact) bool {
	switch comparator {
	case ComparatorMinimumBandwidth:
		return fact.Unit == "Mbit/s" || fact.Unit == "Gbit/s"
	case ComparatorMonthlySLA:
		return fact.Unit == "percent"
	case ComparatorAdvancePaymentCeiling:
		return fact.Unit == "ratio" && fact.Basis == "contract_total"
	default:
		return false
	}
}

func evidenceSupportsValue(rule Rule, fact Fact, authority bool) bool {
	target, err := parseRational(fact.Value)
	if err != nil {
		return false
	}
	candidates, matched := evidenceValueCandidates(rule, fact, target, authority)
	return candidates == 1 && matched
}

func evidenceValueCandidates(rule Rule, fact Fact, target *big.Rat, authority bool) (int, bool) {
	candidates := 0
	matched := false
	for _, citation := range fact.Citations {
		for _, assertion := range evidenceAssertions(citation.Quote) {
			if fact.ObjectRef == "" || !strings.Contains(assertion, fact.ObjectRef) {
				continue
			}
			count, found := evidenceAssertionValue(rule, fact, assertion, citation.Quote, target, authority)
			candidates += count
			matched = matched || found
		}
	}
	return candidates, matched
}

func evidenceAssertionValue(rule Rule, fact Fact, assertion, citation string, target *big.Rat, authority bool) (int, bool) {
	switch rule.Comparator {
	case ComparatorMinimumBandwidth:
		return evidenceBandwidthValue(assertion, fact, target, authority)
	case ComparatorMonthlySLA:
		return evidenceMonthlyValue(assertion, citation, fact, target, authority)
	case ComparatorAdvancePaymentCeiling:
		return evidenceAdvanceValue(assertion, fact, target, authority)
	default:
		return 0, false
	}
}

func evidenceBandwidthValue(text string, fact Fact, target *big.Rat, authority bool) (int, bool) {
	candidates := 0
	matched := false
	for _, number := range evidenceNumbers(text) {
		unit := unitAfter(text, number.end)
		if unit != "Mbit/s" && unit != "Gbit/s" {
			continue
		}
		if !hasNearbyTerm(text, number.start, number.end, 48, 20, "带宽", "接入速率", "保障速率", "bandwidth") {
			continue
		}
		if evidenceNumberIsNegatedOrRanged(text, number.start, number.end) &&
			(!authority || !evidenceAuthorityBoundMatches(text, number.start, number.end, ComparatorMinimumBandwidth)) {
			return candidates, false
		}
		candidates++
		if bandwidthMbit(number.value, unit).Cmp(bandwidthMbit(target, fact.Unit)) == 0 {
			matched = true
		}
	}
	return candidates, matched
}

func evidenceMonthlyValue(assertion, citation string, fact Fact, target *big.Rat, authority bool) (int, bool) {
	candidates, matched, invalidBound := monthlyValueCandidates(assertion, fact, target, authority)
	if invalidBound {
		return candidates, false
	}
	if candidates != 1 || !matched {
		return candidates, false
	}
	return candidates, monthlyCitationMatches(citation, fact)
}

func monthlyValueCandidates(assertion string, fact Fact, target *big.Rat, authority bool) (int, bool, bool) {
	candidates, matched := 0, false
	for _, number := range evidenceNumbers(assertion) {
		if !monthlyNumberCandidate(assertion, number) {
			continue
		}
		if invalidAuthorityBound(assertion, number, ComparatorMonthlySLA, authority) {
			return candidates, false, true
		}
		candidates++
		matched = matched || number.value.Cmp(target) == 0
	}
	return candidates, matched, false
}

func monthlyNumberCandidate(assertion string, number evidenceNumber) bool {
	percent := unitAfter(assertion, number.end) == "percent" || hasPercentPrefix(assertion, number.start)
	return percent && hasNearbyTerm(assertion, number.start, number.end, 48, 20, "可用率", "availability", "sla")
}

func invalidAuthorityBound(text string, number evidenceNumber, comparator string, authority bool) bool {
	return evidenceNumberIsNegatedOrRanged(text, number.start, number.end) &&
		(!authority || !evidenceAuthorityBoundMatches(text, number.start, number.end, comparator))
}

func monthlyCitationMatches(citation string, fact Fact) bool {
	citation = FoldSoftLineBreaks(citation)
	lower := strings.ToLower(citation)
	if !evidenceUsesCalendarMonth(citation) || !containsIdentifierToken(citation, fact.TimeZone) || !containsAny(lower, "gregorian", "公历") {
		return false
	}
	if fact.OutageDefinition == "" || !strings.Contains(lower, strings.ToLower(fact.OutageDefinition)) {
		return false
	}
	return evidenceExclusionsMatch(citation, fact.Exclusions)
}

func evidenceAdvanceValue(assertion string, fact Fact, target *big.Rat, authority bool) (int, bool) {
	if !hasAdvanceBasis(assertion, fact) {
		return 0, false
	}
	candidates, matched, invalid := advanceValueCandidates(assertion, target, authority)
	if invalid {
		return candidates, false
	}
	return candidates, matched
}

func hasAdvanceBasis(assertion string, fact Fact) bool {
	phrases := []string{"合同总额为预付款计算基础", "预付款以合同总额为基础", "预付款按合同总额", "advance payment basis is contract total", "contract total basis"}
	return fact.Basis == "contract_total" && containsAny(strings.ToLower(assertion), phrases...)
}

func advanceValueCandidates(assertion string, target *big.Rat, authority bool) (int, bool, bool) {
	candidates, matched := 0, false
	for _, number := range evidenceNumbers(assertion) {
		if !advanceNumberCandidate(assertion, number) {
			continue
		}
		value := advanceNumberValue(assertion, number)
		if value == nil {
			continue
		}
		if invalidAuthorityBound(assertion, number, ComparatorAdvancePaymentCeiling, authority) {
			return candidates, false, true
		}
		candidates++
		matched = matched || value.Cmp(target) == 0
	}
	return candidates, matched, false
}

func advanceNumberCandidate(assertion string, number evidenceNumber) bool {
	return hasNearbyTerm(assertion, number.start, number.end, 48, 20, "预付款", "预付", "advance payment")
}

func advanceNumberValue(assertion string, number evidenceNumber) *big.Rat {
	unit := unitAfter(assertion, number.end)
	if unit == "percent" || hasPercentPrefix(assertion, number.start) {
		return new(big.Rat).Quo(number.value, big.NewRat(100, 1))
	}
	if !hasNearbyTerm(assertion, number.start, number.end, 16, 8, "比例", "ratio") {
		return nil
	}
	return number.value
}

func evidenceAssertions(text string) []string {
	return strings.FieldsFunc(FoldSoftLineBreaks(text), func(r rune) bool {
		switch r {
		case '。', '；', ';', '！', '!', '？', '?', '\n', '\r':
			return true
		default:
			return false
		}
	})
}

// FoldSoftLineBreaks joins line wraps that split Chinese text or an explicit
// token while keeping paragraph breaks separate from soft wraps.
func FoldSoftLineBreaks(text string) string {
	var folded strings.Builder
	folded.Grow(len(text))
	for index := 0; index < len(text); {
		index = foldSoftLineStep(text, index, &folded)
	}
	return folded.String()
}

func foldSoftLineStep(text string, index int, folded *strings.Builder) int {
	r, size := utf8.DecodeRuneInString(text[index:])
	if !isLineBreak(r) {
		folded.WriteString(text[index : index+size])
		return index + size
	}
	end, breaks := consumeLineBreaks(text, index)
	if breaks > 1 {
		folded.WriteByte('\n')
	} else {
		foldSoftWrapSeparator(text, index, end, folded)
	}
	return end
}

func consumeLineBreaks(text string, start int) (int, int) {
	index, breaks := start, 0
	for index < len(text) {
		r, size := utf8.DecodeRuneInString(text[index:])
		if !isLineBreak(r) {
			break
		}
		index += size
		if r == '\r' && index < len(text) && text[index] == '\n' {
			index++
		}
		breaks++
	}
	return index, breaks
}

func isLineBreak(value rune) bool {
	return value == '\n' || value == '\r'
}

func foldSoftWrapSeparator(text string, start, end int, folded *strings.Builder) {
	left, _ := utf8.DecodeLastRuneInString(text[:start])
	right, _ := utf8.DecodeRuneInString(text[end:])
	if !joinsWrappedToken(left, right) {
		folded.WriteByte(' ')
	}
}

func joinsWrappedToken(left, right rune) bool {
	if left == 0 || right == 0 {
		return false
	}
	if unicode.Is(unicode.Han, left) && unicode.Is(unicode.Han, right) {
		return true
	}
	leftToken, rightToken := wrappedTokenRune(left), wrappedTokenRune(right)
	leftConnector, rightConnector := wrappedConnectorRune(left), wrappedConnectorRune(right)
	return leftToken && rightConnector || leftConnector && rightToken
}

func wrappedTokenRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsNumber(value)
}

func wrappedConnectorRune(value rune) bool {
	return strings.ContainsRune("_+-./:", value)
}

func evidenceProvesInapplicable(rule Rule, fact Fact) bool {
	if !inapplicabilityFactMatches(rule, fact) {
		return false
	}
	for _, citation := range fact.Citations {
		for _, assertion := range evidenceAssertions(citation.Quote) {
			if inapplicabilityAssertionMatches(rule, fact, assertion) {
				return true
			}
		}
	}
	return false
}

func inapplicabilityFactMatches(rule Rule, fact Fact) bool {
	return fact.ObjectRef != "" && fact.Dimension == rule.RequiredFact.Dimension &&
		containsString(rule.RequiredFact.SourceRoles, fact.SourceRole)
}

func inapplicabilityAssertionMatches(rule Rule, fact Fact, assertion string) bool {
	if !strings.Contains(assertion, fact.ObjectRef) || !containsAny(strings.ToLower(assertion), dimensionTerms(rule.Comparator)...) {
		return false
	}
	return hasUnnegatedInapplicabilityMarker(strings.ToLower(assertion))
}

func hasUnnegatedInapplicabilityMarker(assertion string) bool {
	markers := []string{"不属于适用范围", "不适用于", "不适用", "not applicable", "does not apply"}
	for _, marker := range markers {
		if hasUnnegatedMarker(assertion, marker) {
			return true
		}
	}
	return false
}

func hasUnnegatedMarker(assertion, marker string) bool {
	start := 0
	for start < len(assertion) {
		index := strings.Index(assertion[start:], marker)
		if index < 0 {
			return false
		}
		index += start
		if !inapplicabilityMarkerNegated(assertion, index) {
			return true
		}
		start = index + len(marker)
	}
	return false
}

func inapplicabilityMarkerNegated(assertion string, markerStart int) bool {
	return containsAny(assertion[:markerStart], "并非", "并不", "并不是", "不是", "not ")
}

var evidenceDimensionTerms = map[string][]string{
	ComparatorMinimumBandwidth:      {"带宽", "接入速率", "保障速率", "bandwidth"},
	ComparatorMonthlySLA:            {"可用率", "availability", "sla"},
	ComparatorAdvancePaymentCeiling: {"预付款", "预付比例", "advance payment"},
}

func dimensionTerms(comparator string) []string {
	return evidenceDimensionTerms[comparator]
}

func evidenceNumbers(text string) []evidenceNumber {
	matches := evidenceNumberRE.FindAllStringIndex(text, -1)
	out := make([]evidenceNumber, 0, len(matches))
	for _, match := range matches {
		if !numericBoundary(text, match[0], match[1]) {
			continue
		}
		value, err := parseRational(text[match[0]:match[1]])
		if err != nil {
			continue
		}
		out = append(out, evidenceNumber{text: text[match[0]:match[1]], value: value, start: match[0], end: match[1]})
	}
	return out
}

func numericBoundary(text string, start, end int) bool {
	return numericStartBoundary(text, start) && numericEndBoundary(text, end)
}

func numericStartBoundary(text string, start int) bool {
	if start == 0 {
		return true
	}
	if isRationalByte(text[start-1]) || isASCIIAlpha(text[start-1]) {
		return false
	}
	return !startsInGroupedNumber(text, start)
}

func startsInGroupedNumber(text string, start int) bool {
	previous, previousStart := utf8.DecodeLastRuneInString(text[:start])
	if previous != ',' && previous != '，' {
		return false
	}
	beforePrevious, _ := utf8.DecodeLastRuneInString(text[:previousStart])
	return asciiDigit(beforePrevious)
}

func numericEndBoundary(text string, end int) bool {
	if end == len(text) {
		return true
	}
	if isRationalByte(text[end]) || isASCIIAlpha(text[end]) && unitAfter(text, end) == "" {
		return false
	}
	return !endsInGroupedNumber(text, end)
}

func endsInGroupedNumber(text string, end int) bool {
	next, nextSize := utf8.DecodeRuneInString(text[end:])
	if next != ',' && next != '，' {
		return false
	}
	afterNext, _ := utf8.DecodeRuneInString(text[end+nextSize:])
	return asciiDigit(afterNext)
}

func asciiDigit(value rune) bool {
	return value >= '0' && value <= '9'
}

func isRationalByte(value byte) bool {
	return value >= '0' && value <= '9' || value == '.' || value == '/' || value == '_' || value == '+' || value == '-'
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func unitAfter(text string, end int) string {
	index := end
	for index < len(text) {
		r, size := utf8.DecodeRuneInString(text[index:])
		if !unicode.IsSpace(r) {
			break
		}
		index += size
	}
	tail := strings.ToLower(text[index:])
	for _, unit := range []struct{ token, name string }{
		{"percentage", "percent"}, {"percent", "percent"}, {"mbit/s", "Mbit/s"}, {"gbit/s", "Gbit/s"}, {"％", "percent"}, {"%", "percent"},
	} {
		if !strings.HasPrefix(tail, unit.token) {
			continue
		}
		endUnit := index + len(unit.token)
		if endUnit < len(text) {
			r, _ := utf8.DecodeRuneInString(text[endUnit:])
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				continue
			}
		}
		return unit.name
	}
	return ""
}

func evidenceUsesCalendarMonth(text string) bool {
	lower := strings.ToLower(text)
	if containsAny(lower, "非自然月", "并非自然月", "不是自然月", "不按自然月", "not calendar month", "not a calendar month", "non calendar month", "non-calendar month", "rolling", "滚动", "30-day", "30 day", "30日") {
		return false
	}
	return strings.Contains(lower, "自然月") || containsIdentifierToken(lower, "calendar_month") || containsIdentifierToken(lower, "calendar month")
}

func containsIdentifierToken(text, token string) bool {
	if token == "" {
		return false
	}
	search := 0
	for search < len(text) {
		relative := strings.Index(text[search:], token)
		if relative < 0 {
			return false
		}
		index := search + relative
		end := index + len(token)
		leftBounded := index == 0 || !isIdentifierByte(text[index-1]) || text[index-1] == ':'
		rightBounded := end == len(text) || !isIdentifierByte(text[end])
		if leftBounded && rightBounded {
			return true
		}
		search = index + 1
	}
	return false
}

func isIdentifierByte(value byte) bool {
	return isASCIIAlpha(value) || value >= '0' && value <= '9' || strings.ContainsRune("_+-./:", rune(value))
}

func hasPercentPrefix(text string, start int) bool {
	prefix := strings.TrimRightFunc(text[:start], unicode.IsSpace)
	return strings.HasSuffix(prefix, "百分之")
}

func hasNearbyTerm(text string, start, end, before, after int, terms ...string) bool {
	return containsAny(strings.ToLower(runeContext(text, start, end, before, after)), terms...)
}

func evidenceNumberIsNegatedOrRanged(text string, start, end int) bool {
	wide := strings.ToLower(text)
	if containsAny(wide, evidenceNegatedOrRangeTerms...) {
		return true
	}
	narrow := strings.ToLower(runeContext(text, start, end, 2, 2))
	return containsAny(narrow, "-", "—", "–", "~", "～", "至", "到", "between", "from")
}

func evidenceAuthorityBoundMatches(text string, start, end int, comparator string) bool {
	matches := evidenceAuthorityBoundRE.FindAllStringSubmatchIndex(text, -1)
	if len(matches) != 1 || matches[0][4] != start || matches[0][5] != end {
		return false
	}
	match := matches[0]
	operator := text[match[2]:match[3]]
	if evidenceBoundDirection(operator) != evidenceComparatorAuthorityDirection(comparator) {
		return false
	}
	lower := strings.ToLower(text)
	operatorStart := len(strings.ToLower(text[:match[2]]))
	operatorEnd := len(strings.ToLower(text[:match[3]]))
	for _, marker := range evidenceNegatedOrRangeTerms {
		if evidenceContainsOutside(lower, strings.ToLower(marker), operatorStart, operatorEnd) {
			return false
		}
	}
	narrow := strings.ToLower(runeContext(text, start, end, 2, 2))
	return !containsAny(narrow, "-", "—", "–", "~", "～")
}

func evidenceBoundDirection(operator string) string {
	switch operator {
	case "不得低于", "不低于", "至少", "不少于":
		return "lower"
	case "不得超过", "不得高于", "不高于", "不超过", "不多于", "至多":
		return "upper"
	default:
		return ""
	}
}

func evidenceComparatorAuthorityDirection(comparator string) string {
	switch comparator {
	case ComparatorMinimumBandwidth, ComparatorMonthlySLA:
		return "lower"
	case ComparatorAdvancePaymentCeiling:
		return "upper"
	default:
		return ""
	}
}

func evidenceContainsOutside(text, marker string, allowedStart, allowedEnd int) bool {
	search := 0
	for search < len(text) {
		relative := strings.Index(text[search:], marker)
		if relative < 0 {
			return false
		}
		start := search + relative
		end := start + len(marker)
		if start < allowedStart || end > allowedEnd {
			return true
		}
		search = end
	}
	return false
}

func runeContext(text string, start, end, before, after int) string {
	runes := []rune(text)
	startRune := utf8.RuneCountInString(text[:start])
	endRune := utf8.RuneCountInString(text[:end])
	left := startRune - before
	if left < 0 {
		left = 0
	}
	right := endRune + after
	if right > len(runes) {
		right = len(runes)
	}
	return string(runes[left:right])
}

func evidenceExclusionsMatch(text string, expected []string) bool {
	lower := strings.ToLower(text)
	markers := []string{"排除项：", "排除项:", "排除项为", "exclusions:"}
	hasList := containsAny(lower, markers...)
	noExclusions := containsAny(lower, "无排除项", "不设排除", "no exclusions")
	if len(expected) == 0 {
		return evidenceDeclaresNoExclusions(lower, hasList, noExclusions)
	}
	if noExclusions {
		return false
	}
	listed, found := collectExclusionList(text, lower, markers)
	if found != 1 || hasAdditionalExclusionMarkers(lower) {
		return false
	}
	return exclusionItemsMatch(listed, expected)
}

func evidenceDeclaresNoExclusions(lower string, hasList, noExclusions bool) bool {
	return noExclusions && !hasList && !hasAdditionalExclusionMarkers(lower)
}

func hasAdditionalExclusionMarkers(lower string) bool {
	return containsAny(lower, "除外", "例外", "except", "excluding")
}

func collectExclusionList(text, lower string, markers []string) (string, int) {
	listed, found := "", 0
	for _, marker := range markers {
		value, count := collectExclusionMarker(text, lower, marker)
		if count > 0 {
			listed = value
			found += count
		}
	}
	return listed, found
}

func collectExclusionMarker(text, lower, marker string) (string, int) {
	listed, found, search := "", 0, 0
	for search < len(lower) {
		relative := strings.Index(lower[search:], strings.ToLower(marker))
		if relative < 0 {
			break
		}
		index := search + relative
		begin, end := index+len(marker), len(text)
		if stop := strings.IndexAny(text[begin:], "。.;；\n\r"); stop >= 0 {
			end = begin + stop
		}
		listed = strings.TrimSpace(text[begin:end])
		found++
		search = begin
	}
	return listed, found
}

func exclusionItemsMatch(listed string, expected []string) bool {
	items := strings.FieldsFunc(listed, isExclusionSeparator)
	for i := range items {
		items[i] = strings.TrimSpace(items[i])
	}
	want := append([]string(nil), expected...)
	sort.Strings(items)
	sort.Strings(want)
	if len(items) != len(want) {
		return false
	}
	for i := range want {
		if items[i] == "" || items[i] != want[i] {
			return false
		}
	}
	return true
}

func isExclusionSeparator(value rune) bool {
	return value == ',' || value == '，' || value == '、' || value == ';' || value == '；'
}

func sameStrings(left, right []string) bool {
	if left == nil || right == nil || len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for i := range leftCopy {
		if leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
}
