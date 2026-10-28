package decision

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"contractaudit/internal/access"
)

var (
	referencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	candidatePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,79}$`)
	ruleHashPattern  = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
)

// ValidInput applies the bounded shape checks shared by shadow backends. It
// does not check authorization; callers must still call access.Check with the
// resolved principal and resource before sending state anywhere.
func ValidInput(input Input) bool {
	if !validReference(input.RequestID) || !validReference(input.DecisionID) ||
		!validReference(input.RunID) || !validReference(input.AttemptID) ||
		!validReference(input.TraceID) || input.PolicyVersion != access.PolicyVersion ||
		!validReference(input.RulePackVersion) || !ruleHashPattern.MatchString(input.RulePackHash) ||
		len(input.State) == 0 || len(input.State) > MaxStateBytes || !utf8.ValidString(input.State) {
		return false
	}
	if input.Capability != CapabilityChoice {
		return false
	}
	if len(input.AllowedCandidates) < 2 || len(input.AllowedCandidates) > MaxCandidates {
		return false
	}
	seen := make(map[string]struct{}, len(input.AllowedCandidates))
	for _, candidate := range input.AllowedCandidates {
		if !candidatePattern.MatchString(candidate.ID) || len(candidate.ID) > MaxCandidateIDBytes ||
			len(candidate.Description) == 0 || len(candidate.Description) > MaxCandidateDescriptionBytes ||
			!utf8.ValidString(candidate.Description) || strings.TrimSpace(candidate.Description) == "" {
			return false
		}
		if _, duplicate := seen[candidate.ID]; duplicate {
			return false
		}
		seen[candidate.ID] = struct{}{}
	}
	_, manual := seen["manual_review"]
	return manual
}

func validReference(value string) bool {
	return referencePattern.MatchString(value)
}
