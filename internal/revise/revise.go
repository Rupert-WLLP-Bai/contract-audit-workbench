// Package revise compares one run's constraints with the previous run.
// A match is the constraint identity plus each verified quote and value.
// Model prose is not an input.
package revise

import (
	"sort"
	"strings"
)

const (
	// Persisting means the same constraint and verified quote/value are on both runs.
	Persisting = "持续"
	// Added means the current run has a constraint the previous run does not.
	Added = "新增"
	// Resolved means the previous run had a constraint the current run does not.
	Resolved = "消除"
)

// Verified is one value a rule located in a quote.
// Role is the material role, not a document id, so a new version of the same role can match.
type Verified struct {
	Role  string
	Quote string
	Value string
	Unit  string
}

// Constraint is one rule result that produced a finding.
// Domain, rule version, and relation are the constraint identity.
type Constraint struct {
	Domain      string
	RuleVersion string
	Relation    string
	Verified    []Verified
}

// Classified is one comparison row.
// Current is nil for 消除. Previous is nil for 新增.
type Classified struct {
	Mark     string
	Current  *Constraint
	Previous *Constraint
}

// Classify pairs constraints. Quote text and the verified value both have to match.
// The order of verified facts does not. Unmatched current rows are 新增.
// Unmatched previous rows are 消除.
func Classify(previous, current []Constraint) []Classified {
	used := make([]bool, len(previous))
	out := make([]Classified, 0, len(previous)+len(current))
	for i := range current {
		cur := current[i]
		found := -1
		key := identity(cur)
		for j := range previous {
			if used[j] || identity(previous[j]) != key {
				continue
			}
			found = j
			break
		}
		if found < 0 {
			item := cur
			out = append(out, Classified{Mark: Added, Current: &item})
			continue
		}
		used[found] = true
		prev := previous[found]
		out = append(out, Classified{Mark: Persisting, Current: &cur, Previous: &prev})
	}
	for i := range previous {
		if used[i] {
			continue
		}
		prev := previous[i]
		out = append(out, Classified{Mark: Resolved, Previous: &prev})
	}
	return out
}

// Matches reports whether both constraints have the same identity and verified quote/value.
func (c Constraint) Matches(other Constraint) bool {
	return identity(c) == identity(other)
}

func identity(item Constraint) string {
	facts := append([]Verified(nil), item.Verified...)
	sort.Slice(facts, func(i, j int) bool {
		if facts[i].Role != facts[j].Role {
			return facts[i].Role < facts[j].Role
		}
		if facts[i].Quote != facts[j].Quote {
			return facts[i].Quote < facts[j].Quote
		}
		if facts[i].Value != facts[j].Value {
			return facts[i].Value < facts[j].Value
		}
		return facts[i].Unit < facts[j].Unit
	})
	var b strings.Builder
	b.WriteString(item.Domain)
	b.WriteByte('\n')
	b.WriteString(item.RuleVersion)
	b.WriteByte('\n')
	b.WriteString(item.Relation)
	for _, fact := range facts {
		b.WriteByte('\n')
		b.WriteString(fact.Role)
		b.WriteByte('\t')
		b.WriteString(fact.Quote)
		b.WriteByte('\t')
		b.WriteString(fact.Value)
		b.WriteByte('\t')
		b.WriteString(fact.Unit)
	}
	return b.String()
}
