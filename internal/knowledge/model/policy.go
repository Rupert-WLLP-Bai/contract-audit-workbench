// File metadata (manifest mirror):
// Purpose: Extracts and compares exact policy thresholds and source spans with existing RulePack semantics, keeping ambiguous natural-language requirements explicit.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package model contains deterministic threshold extraction and comparison rules for governed policy content.
package model

import (
	"math/big"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"contractaudit/internal/rulepack"
)

// PolicyThresholdCandidate is extracted from policy text, before comparison
// with a RulePack. Offsets are Unicode code points in the immutable body.
type PolicyThresholdCandidate struct {
	Comparator string `json:"comparator"`
	Value      string `json:"value"`
	Unit       string `json:"unit"`
	Basis      string `json:"basis,omitempty"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Quote      string `json:"quote"`
}

type PolicyRuleComparison struct {
	RuleID        string                    `json:"rule_id"`
	Comparator    string                    `json:"comparator"`
	Candidate     *PolicyThresholdCandidate `json:"candidate,omitempty"`
	RuleThreshold rulepack.Threshold        `json:"rule_threshold"`
	Status        string                    `json:"status"`
	Reason        string                    `json:"reason,omitempty"`
}

type PolicyTextIssue struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Quote  string `json:"quote"`
	Reason string `json:"reason"`
}

// GovernedPolicyPreview reports extracted candidates and the exact comparison
// against the selected RulePack. SemanticOnly statements are not compiled into
// deterministic rules and require a separate semantic review.
type GovernedPolicyPreview struct {
	Ref          KnowledgeRef           `json:"ref"`
	ExtractorRef string                 `json:"extractor_ref"`
	Comparisons  []PolicyRuleComparison `json:"comparisons"`
	Issues       []PolicyTextIssue      `json:"issues"`
	SemanticOnly []PolicyTextIssue      `json:"semantic_only"`
	Approvable   bool                   `json:"approvable"`
}

var policyThresholdPatterns = []struct {
	comparator string
	pattern    *regexp.Regexp
}{
	{rulepack.ComparatorMinimumBandwidth, regexp.MustCompile(`(?:最低保障带宽|保障带宽|最低带宽|带宽|保障速率)\s*(?:不得低于|不低于|不少于|至少|下限为)\s*[:：]?\s*([0-9]+(?:\.[0-9]+)?(?:/[0-9]+)?)\s*(Mbit/s|Gbit/s)`)},
	{rulepack.ComparatorMonthlySLA, regexp.MustCompile(`(?:(?:月度|月)\s*|SLA\s*)?可用率\s*(?:不得低于|不低于|不少于|至少|下限为)\s*[:：]?\s*([0-9]+(?:\.[0-9]+)?(?:/[0-9]+)?)\s*([%％])`)},
	{rulepack.ComparatorAdvancePaymentCeiling, regexp.MustCompile(`(?:预付款比例|预付款上限|预付款)\s*(?:不得超过|不超过|不多于|至多|不得高于|不高于|上限为)\s*[:：]?\s*([0-9]+(?:\.[0-9]+)?(?:/[0-9]+)?)\s*([%％])?`)},
}

var policySLASuffix = regexp.MustCompile(`^(?:\s*[，,]\s*(?:统计周期(?:为|[:：])?\s*自然月|时区(?:为|[:：])?\s*[A-Za-z_]+/[A-Za-z_]+|公历(?:为|[:：])?\s*gregorian|故障口径(?:为|[:：])?\s*[a-z_]+|无排除项|不设排除))+$`)
var policyAnyNumber = regexp.MustCompile(`[0-9]+`)

func policyStatementSuffixAllowed(comparator, suffix string) bool {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return comparator != rulepack.ComparatorMonthlySLA
	}
	switch comparator {
	case rulepack.ComparatorMonthlySLA:
		return policySLASuffix.MatchString(suffix) && strings.Count(suffix, "统计周期") == 1 && strings.Count(suffix, "时区") == 1 &&
			strings.Count(suffix, "公历") == 1 && strings.Count(suffix, "故障口径") == 1 &&
			strings.Count(suffix, "无排除项")+strings.Count(suffix, "不设排除") == 1
	case rulepack.ComparatorAdvancePaymentCeiling:
		suffix = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(suffix, "，"), ","))
		return suffix == "预付款以合同总额为基础" || suffix == "预付款按合同总额" || suffix == "合同总额为预付款计算基础"
	default:
		return false
	}
}

func policyMetadataStatement(text string) bool {
	text = strings.TrimSpace(text)
	return text == "无排除项" || text == "不设排除" || strings.HasPrefix(text, "统计周期") ||
		strings.HasPrefix(text, "时区") || strings.HasPrefix(text, "公历") || strings.HasPrefix(text, "故障口径")
}

func policyDimensionMentioned(text string) bool {
	lower := strings.ToLower(text)
	for _, term := range []string{"带宽", "保障速率", "可用率", "预付款", "bandwidth", "availability", "advance payment"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func policyUnresolvedQualifier(text string) bool {
	lower := strings.ToLower(text)
	for _, term := range []string{"例外", "除外", "不适用", "除非", "并非", "不保证", "另行协商", "视情况", "可能调整", "特殊情况", "可以降低", "予以豁免", "if ", "unless ", "except "} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

// policyStatements preserves byte positions while using the same sentence
// boundaries as the RulePack evidence verifier.
func policyStatements(body string) []struct {
	text  string
	start int
} {
	var out []struct {
		text  string
		start int
	}
	start := 0
	for index, r := range body {
		if !strings.ContainsRune("。；;！!？?\n\r", r) {
			continue
		}
		if strings.TrimSpace(body[start:index]) != "" {
			out = append(out, struct {
				text  string
				start int
			}{body[start:index], start})
		}
		start = index + utf8.RuneLen(r)
	}
	if strings.TrimSpace(body[start:]) != "" {
		out = append(out, struct {
			text  string
			start int
		}{body[start:], start})
	}
	return out
}

func policyRational(value string) (*big.Rat, bool) {
	if len(value) == 0 || len(value) > 96 {
		return nil, false
	}
	parsed, ok := new(big.Rat).SetString(value)
	return parsed, ok && parsed.Sign() >= 0
}

func ExtractGovernedPolicyThresholds(body string) (map[string][]PolicyThresholdCandidate, []PolicyTextIssue, []PolicyTextIssue) {
	candidates := make(map[string][]PolicyThresholdCandidate)
	issues, semantic := []PolicyTextIssue{}, []PolicyTextIssue{}
	for _, statement := range policyStatements(body) {
		matches := 0
		for _, definition := range policyThresholdPatterns {
			for _, indices := range definition.pattern.FindAllStringSubmatchIndex(statement.text, -1) {
				matches++
				start, end := statement.start+indices[0], statement.start+indices[1]
				issue := PolicyTextIssue{Start: utf8.RuneCountInString(body[:start]), End: utf8.RuneCountInString(body[:end]), Quote: body[start:end]}
				if strings.TrimSpace(statement.text[:indices[0]]) != "" || !policyStatementSuffixAllowed(definition.comparator, statement.text[indices[1]:]) {
					issue.Reason = "unexpressible_qualifier_or_scope"
					issues = append(issues, issue)
					continue
				}
				value, ok := policyRational(statement.text[indices[2]:indices[3]])
				if !ok {
					issue.Reason = "invalid_numeric_value"
					issues = append(issues, issue)
					continue
				}
				unit := ""
				if indices[4] >= 0 {
					unit = statement.text[indices[4]:indices[5]]
				}
				candidate := PolicyThresholdCandidate{Comparator: definition.comparator, Start: issue.Start, End: issue.End, Quote: issue.Quote}
				switch definition.comparator {
				case rulepack.ComparatorMinimumBandwidth:
					candidate.Unit = unit
				case rulepack.ComparatorMonthlySLA:
					candidate.Unit = "percent"
					if value.Cmp(big.NewRat(100, 1)) > 0 {
						issue.Reason = "percentage_out_of_range"
					}
				case rulepack.ComparatorAdvancePaymentCeiling:
					candidate.Unit, candidate.Basis = "ratio", "contract_total"
					if unit != "" {
						value.Quo(value, big.NewRat(100, 1))
					} else if !strings.Contains(statement.text[indices[2]:indices[3]], "/") || !strings.Contains(issue.Quote, "比例") {
						issue.Reason = "ratio_unit_missing"
					}
					if value.Cmp(big.NewRat(1, 1)) > 0 {
						issue.Reason = "ratio_out_of_range"
					}
				}
				if issue.Reason != "" {
					issues = append(issues, issue)
					continue
				}
				candidate.Value = value.RatString()
				candidates[definition.comparator] = append(candidates[definition.comparator], candidate)
			}
		}
		if matches == 0 {
			issue := PolicyTextIssue{Start: utf8.RuneCountInString(body[:statement.start]), End: utf8.RuneCountInString(body[:statement.start+len(statement.text)]), Quote: statement.text}
			if policyDimensionMentioned(statement.text) || policyAnyNumber.MatchString(statement.text) || policyUnresolvedQualifier(statement.text) || policyMetadataStatement(statement.text) {
				issue.Reason = "unexpressible_threshold"
				issues = append(issues, issue)
			} else {
				issue.Reason = "semantic_check_only"
				semantic = append(semantic, issue)
			}
		}
	}
	return candidates, issues, semantic
}

func policyCandidateProved(rule rulepack.Rule, candidate PolicyThresholdCandidate, body string) bool {
	object := "带宽"
	if rule.Comparator == rulepack.ComparatorMinimumBandwidth && !strings.Contains(candidate.Quote, object) {
		object = "保障速率"
	}
	if rule.Comparator == rulepack.ComparatorMonthlySLA {
		object = "可用率"
	}
	if rule.Comparator == rulepack.ComparatorAdvancePaymentCeiling {
		object = "预付款"
	}
	fact := rulepack.Fact{ObjectRef: object, Value: candidate.Value, Unit: candidate.Unit, SourceRole: "procurement",
		MeasurementWindow: rule.RequiredFact.MeasurementWindow, TimeZone: rule.RequiredFact.TimeZone, Calendar: rule.RequiredFact.Calendar,
		OutageDefinition: rule.RequiredFact.OutageDefinition, Exclusions: rule.RequiredFact.Exclusions, Basis: candidate.Basis,
		Citations: []rulepack.Citation{{Quote: body}}}
	return rulepack.EvidenceSupportsAuthority(rule, fact)
}

func equalPolicyThreshold(candidate PolicyThresholdCandidate, threshold rulepack.Threshold) bool {
	left, leftOK := policyRational(candidate.Value)
	right, rightOK := policyRational(threshold.Value)
	if !leftOK || !rightOK {
		return false
	}
	if candidate.Comparator == rulepack.ComparatorMinimumBandwidth {
		if candidate.Unit == "Gbit/s" {
			left.Mul(left, big.NewRat(1000, 1))
		}
		if threshold.Unit == "Gbit/s" {
			right.Mul(right, big.NewRat(1000, 1))
		}
	} else if candidate.Unit != threshold.Unit || candidate.Basis != threshold.Basis {
		return false
	}
	return left.Cmp(right) == 0
}

func CompareGovernedPolicyThresholds(content GovernedKnowledgeContent, manifest rulepack.Manifest) GovernedPolicyPreview {
	candidates, issues, semantic := ExtractGovernedPolicyThresholds(content.Body)
	preview := GovernedPolicyPreview{ExtractorRef: PolicyThresholdCompilerRef, Comparisons: []PolicyRuleComparison{}, Issues: issues, SemanticOnly: semantic, Approvable: true}
	rules := make(map[string]rulepack.Rule, len(manifest.Rules))
	for _, rule := range manifest.Rules {
		rules[rule.RuleID] = rule
	}
	boundComparators := make(map[string]bool)
	for _, id := range content.Binding.RuleIDs {
		rule, exists := rules[id]
		comparison := PolicyRuleComparison{RuleID: id, Comparator: rule.Comparator, RuleThreshold: rule.Threshold, Status: "unexpressible"}
		if !exists {
			comparison.Reason = "unknown_rule"
			preview.Comparisons = append(preview.Comparisons, comparison)
			continue
		}
		boundComparators[rule.Comparator] = true
		items := candidates[rule.Comparator]
		if len(items) != 1 {
			comparison.Reason = "threshold_missing"
			if len(items) > 1 {
				comparison.Status, comparison.Reason = "ambiguous", "multiple_thresholds"
			}
			preview.Comparisons = append(preview.Comparisons, comparison)
			continue
		}
		candidate := items[0]
		comparison.Candidate = &candidate
		spans, covered := 0, 0
		for _, span := range content.Binding.SourceSpans {
			if span.RuleID == id {
				spans++
				if span.Start <= candidate.Start && span.End >= candidate.End {
					covered++
				}
			}
		}
		switch {
		case spans != 1 || covered != 1:
			comparison.Reason = "source_span_missing_or_ambiguous"
		case !policyCandidateProved(rule, candidate, content.Body):
			comparison.Reason = "unsupported_semantics_or_measurement_basis"
		case !equalPolicyThreshold(candidate, rule.Threshold):
			comparison.Status, comparison.Reason = "different", "threshold_or_unit_mismatch"
		default:
			comparison.Status = "match"
		}
		preview.Comparisons = append(preview.Comparisons, comparison)
	}
	for comparator, items := range candidates {
		if boundComparators[comparator] {
			continue
		}
		for _, candidate := range items {
			preview.Issues = append(preview.Issues, PolicyTextIssue{Start: candidate.Start, End: candidate.End, Quote: candidate.Quote, Reason: "unbound_supported_threshold"})
		}
	}
	sort.Slice(preview.Issues, func(i, j int) bool {
		if preview.Issues[i].Start != preview.Issues[j].Start {
			return preview.Issues[i].Start < preview.Issues[j].Start
		}
		if preview.Issues[i].End != preview.Issues[j].End {
			return preview.Issues[i].End < preview.Issues[j].End
		}
		return preview.Issues[i].Reason < preview.Issues[j].Reason
	})
	for _, comparison := range preview.Comparisons {
		if comparison.Status != "match" {
			preview.Approvable = false
		}
	}
	if len(preview.Issues) != 0 || len(preview.Comparisons) == 0 {
		preview.Approvable = false
	}
	return preview
}
