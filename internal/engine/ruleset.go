package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	PaymentSumRuleVersion       = "payment-sum-v2"
	PaymentConditionRuleVersion = "payment-condition-v2"
	ScheduleRuleVersion         = "schedule-acceptance-v2"
	AmountRuleVersion           = "amount-v2"

	// PlaybookID is the fixed rule list shipped with this process.
	// It is not a general playbook editor.
	PlaybookID = "fixed-playbook-v2"

	// ComparatorRef names the deterministic comparator stored on a run snapshot.
	ComparatorRef = "typed-constraint-v2"
)

// RuleVersions is the order locked into RuleSetHash.
func RuleVersions() []string {
	return []string{
		WarrantyRuleVersion,
		PaymentSumRuleVersion,
		PaymentConditionRuleVersion,
		ScheduleRuleVersion,
		AmountRuleVersion,
	}
}

// RuleSetHash is sha256 of RuleVersions joined by newlines.
func RuleSetHash() string {
	return sha256hex(strings.Join(RuleVersions(), "\n"))
}

// PlaybookHash is sha256 of the playbook id plus RuleVersions.
func PlaybookHash() string {
	lines := append([]string{PlaybookID}, RuleVersions()...)
	return sha256hex(strings.Join(lines, "\n"))
}

// CrossFile returns payment, schedule, and amount evaluations that apply.
// Not-applicable rows are omitted so a warranty-only case stays one evaluation.
func CrossFile(docs []Source) []Evaluation {
	all := []Evaluation{
		EvaluatePaymentSum(docs),
		EvaluatePaymentCondition(docs),
		EvaluateSchedule(docs),
		EvaluateAmount(docs),
	}
	var out []Evaluation
	for _, ev := range all {
		if ev.Relation == RelNotApplicable {
			continue
		}
		out = append(out, ev)
	}
	return out
}

func sha256hex(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
