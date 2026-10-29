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
	if !validInputReferences(input) || !validInputState(input.State) {
		return false
	}
	if input.Capability != CapabilityChoice {
		return false
	}
	return validCandidates(input.AllowedCandidates)
}

func validInputReferences(input Input) bool {
	return validReference(input.RequestID) && validReference(input.DecisionID) &&
		validReference(input.RunID) && validReference(input.AttemptID) &&
		validReference(input.TraceID) && input.PolicyVersion == access.PolicyVersion &&
		validReference(input.RulePackVersion) && ruleHashPattern.MatchString(input.RulePackHash)
}

func validInputState(state string) bool {
	return len(state) != 0 && len(state) <= MaxStateBytes && utf8.ValidString(state)
}

func validCandidates(candidates []Candidate) bool {
	if len(candidates) < 2 || len(candidates) > MaxCandidates {
		return false
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if !validCandidate(candidate) {
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

func validCandidate(candidate Candidate) bool {
	return candidatePattern.MatchString(candidate.ID) && len(candidate.ID) <= MaxCandidateIDBytes &&
		len(candidate.Description) != 0 && len(candidate.Description) <= MaxCandidateDescriptionBytes &&
		utf8.ValidString(candidate.Description) && strings.TrimSpace(candidate.Description) != ""
}

func validReference(value string) bool {
	return referencePattern.MatchString(value)
}
