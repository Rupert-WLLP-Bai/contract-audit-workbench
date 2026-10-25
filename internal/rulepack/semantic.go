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
	if fact.SourceRole != "procurement" && fact.SourceRole != "award" {
		return false
	}
	switch rule.Comparator {
	case ComparatorMinimumBandwidth:
		if fact.Unit != "Mbit/s" && fact.Unit != "Gbit/s" {
			return false
		}
	case ComparatorMonthlySLA:
		if fact.Unit != "percent" {
			return false
		}
	case ComparatorAdvancePaymentCeiling:
		if fact.Unit != "ratio" || fact.Basis != "contract_total" {
			return false
		}
	default:
		return false
	}
	return evidenceSupportsValue(rule, fact, true)
}

func evidenceSupportsValue(rule Rule, fact Fact, authority bool) bool {
	target, err := parseRational(fact.Value)
	if err != nil {
		return false
	}
	candidates := 0
	matched := false
	for _, citation := range fact.Citations {
		for _, assertion := range evidenceAssertions(citation.Quote) {
			if fact.ObjectRef == "" || !strings.Contains(assertion, fact.ObjectRef) {
				continue
			}
			var count int
			var found bool
			switch rule.Comparator {
			case ComparatorMinimumBandwidth:
				count, found = evidenceBandwidthValue(assertion, fact, target, authority)
			case ComparatorMonthlySLA:
				count, found = evidenceMonthlyValue(assertion, citation.Quote, fact, target, authority)
			case ComparatorAdvancePaymentCeiling:
				count, found = evidenceAdvanceValue(assertion, fact, target, authority)
			}
			candidates += count
			matched = matched || found
		}
	}
	return candidates == 1 && matched
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
	candidates := 0
	matched := false
	for _, number := range evidenceNumbers(assertion) {
		unit := unitAfter(assertion, number.end)
		percent := unit == "percent" || hasPercentPrefix(assertion, number.start)
		if !percent || !hasNearbyTerm(assertion, number.start, number.end, 48, 20, "可用率", "availability", "sla") {
			continue
		}
		if evidenceNumberIsNegatedOrRanged(assertion, number.start, number.end) &&
			(!authority || !evidenceAuthorityBoundMatches(assertion, number.start, number.end, ComparatorMonthlySLA)) {
			return candidates, false
		}
		candidates++
		if number.value.Cmp(target) == 0 {
			matched = true
		}
	}
	if candidates != 1 || !matched {
		return candidates, false
	}
	citation = FoldSoftLineBreaks(citation)
	lower := strings.ToLower(citation)
	if !evidenceUsesCalendarMonth(citation) || !containsIdentifierToken(citation, fact.TimeZone) || !containsAny(lower, "gregorian", "公历") {
		return candidates, false
	}
	if fact.OutageDefinition == "" || !strings.Contains(lower, strings.ToLower(fact.OutageDefinition)) {
		return candidates, false
	}
	return candidates, evidenceExclusionsMatch(citation, fact.Exclusions)
}

func evidenceAdvanceValue(assertion string, fact Fact, target *big.Rat, authority bool) (int, bool) {
	if fact.Basis != "contract_total" || !containsAny(strings.ToLower(assertion), "合同总额为预付款计算基础", "预付款以合同总额为基础", "预付款按合同总额", "advance payment basis is contract total", "contract total basis") {
		return 0, false
	}
	candidates := 0
	matched := false
	for _, number := range evidenceNumbers(assertion) {
		if !hasNearbyTerm(assertion, number.start, number.end, 48, 20, "预付款", "预付", "advance payment") {
			continue
		}
		unit := unitAfter(assertion, number.end)
		value := number.value
		if unit == "percent" || hasPercentPrefix(assertion, number.start) {
			value = new(big.Rat).Quo(value, big.NewRat(100, 1))
		} else if !hasNearbyTerm(assertion, number.start, number.end, 16, 8, "比例", "ratio") {
			continue
		}
		if evidenceNumberIsNegatedOrRanged(assertion, number.start, number.end) &&
			(!authority || !evidenceAuthorityBoundMatches(assertion, number.start, number.end, ComparatorAdvancePaymentCeiling)) {
			return candidates, false
		}
		candidates++
		if value.Cmp(target) == 0 {
			matched = true
		}
	}
	return candidates, matched
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
		r, size := utf8.DecodeRuneInString(text[index:])
		if r != '\n' && r != '\r' {
			folded.WriteString(text[index : index+size])
			index += size
			continue
		}
		breakStart := index
		breaks := 0
		for index < len(text) {
			r, size = utf8.DecodeRuneInString(text[index:])
			if r != '\n' && r != '\r' {
				break
			}
			index += size
			if r == '\r' && index < len(text) && text[index] == '\n' {
				index++
			}
			breaks++
		}
		if breaks > 1 {
			folded.WriteByte('\n')
			continue
		}
		left, _ := utf8.DecodeLastRuneInString(text[:breakStart])
		right, _ := utf8.DecodeRuneInString(text[index:])
		if !joinsWrappedToken(left, right) {
			folded.WriteByte(' ')
		}
	}
	return folded.String()
}

func joinsWrappedToken(left, right rune) bool {
	if left == 0 || right == 0 {
		return false
	}
	if unicode.Is(unicode.Han, left) && unicode.Is(unicode.Han, right) {
		return true
	}
	leftToken := unicode.IsLetter(left) || unicode.IsNumber(left)
	rightToken := unicode.IsLetter(right) || unicode.IsNumber(right)
	leftConnector := strings.ContainsRune("_+-./:", left)
	rightConnector := strings.ContainsRune("_+-./:", right)
	return leftToken && rightConnector || leftConnector && rightToken
}

func evidenceProvesInapplicable(rule Rule, fact Fact) bool {
	if fact.ObjectRef == "" || fact.Dimension != rule.RequiredFact.Dimension || !containsString(rule.RequiredFact.SourceRoles, fact.SourceRole) {
		return false
	}
	terms := dimensionTerms(rule.Comparator)
	for _, citation := range fact.Citations {
		for _, assertion := range evidenceAssertions(citation.Quote) {
			if !strings.Contains(assertion, fact.ObjectRef) || !containsAny(strings.ToLower(assertion), terms...) {
				continue
			}
			lower := strings.ToLower(assertion)
			for _, marker := range []string{"不属于适用范围", "不适用于", "不适用", "not applicable", "does not apply"} {
				start := 0
				for {
					index := strings.Index(lower[start:], strings.ToLower(marker))
					if index < 0 {
						break
					}
					index += start
					if !inapplicabilityMarkerNegated(lower, index) {
						return true
					}
					start = index + len(marker)
					if start >= len(lower) {
						break
					}
				}
			}
		}
	}
	return false
}

func inapplicabilityMarkerNegated(assertion string, markerStart int) bool {
	return containsAny(assertion[:markerStart], "并非", "并不", "并不是", "不是", "not ")
}

func dimensionTerms(comparator string) []string {
	switch comparator {
	case ComparatorMinimumBandwidth:
		return []string{"带宽", "接入速率", "保障速率", "bandwidth"}
	case ComparatorMonthlySLA:
		return []string{"可用率", "availability", "sla"}
	case ComparatorAdvancePaymentCeiling:
		return []string{"预付款", "预付比例", "advance payment"}
	default:
		return nil
	}
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
	if start > 0 && (isRationalByte(text[start-1]) || isASCIIAlpha(text[start-1])) {
		return false
	}
	if end < len(text) {
		if isRationalByte(text[end]) || isASCIIAlpha(text[end]) && unitAfter(text, end) == "" {
			return false
		}
	}
	if start > 0 {
		previous, previousStart := utf8.DecodeLastRuneInString(text[:start])
		if previous == ',' || previous == '，' {
			beforePrevious, _ := utf8.DecodeLastRuneInString(text[:previousStart])
			if beforePrevious >= '0' && beforePrevious <= '9' {
				return false
			}
		}
	}
	if end < len(text) {
		next, nextSize := utf8.DecodeRuneInString(text[end:])
		if next == ',' || next == '，' {
			afterNext, _ := utf8.DecodeRuneInString(text[end+nextSize:])
			if afterNext >= '0' && afterNext <= '9' {
				return false
			}
		}
	}
	return true
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
	hasList := false
	for _, marker := range markers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			hasList = true
			break
		}
	}
	noExclusions := containsAny(lower, "无排除项", "不设排除", "no exclusions")
	if len(expected) == 0 {
		if !noExclusions || hasList {
			return false
		}
		return !containsAny(lower, "除外", "例外", "except", "excluding")
	}
	if noExclusions {
		return false
	}
	var listed string
	found := 0
	for _, marker := range markers {
		search := 0
		for {
			index := strings.Index(lower[search:], strings.ToLower(marker))
			if index < 0 {
				break
			}
			index += search
			found++
			begin := index + len(marker)
			end := len(text)
			if stop := strings.IndexAny(text[begin:], "。.;；\n\r"); stop >= 0 {
				end = begin + stop
			}
			listed = strings.TrimSpace(text[begin:end])
			search = begin
			if search >= len(lower) {
				break
			}
		}
	}
	if found != 1 || containsAny(lower, "除外", "例外", "except", "excluding") {
		return false
	}
	items := strings.FieldsFunc(listed, func(r rune) bool { return r == ',' || r == '，' || r == '、' || r == ';' || r == '；' })
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
