package rulepack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
)

const fixtureSchemaVersion = "rulepack-fixture-suite/v1"

// FixtureSuite is synthetic authoring data hashed into one exact pack version.
type FixtureSuite struct {
	SchemaVersion string        `json:"schema_version"`
	ID            string        `json:"id"`
	PackID        string        `json:"pack_id"`
	PackVersion   string        `json:"pack_version"`
	DataClass     string        `json:"data_class"`
	Cases         []FixtureCase `json:"cases"`
}

type FixtureCase struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Kind     string            `json:"kind"`
	Input    FixtureInput      `json:"input"`
	Expected []FixtureExpected `json:"expected"`
}

type FixtureInput struct {
	BusinessLine   string          `json:"business_line"`
	Scenario       string          `json:"scenario"`
	ContractType   string          `json:"contract_type"`
	At             string          `json:"at"`
	Sources        []FixtureSource `json:"sources"`
	Facts          []FixtureFact   `json:"facts"`
	AuthorityGates []AuthorityGate `json:"authority_gates,omitempty"`
}

type FixtureSource struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Role    string `json:"role"`
	Text    string `json:"text"`
}

type FixtureCitation struct {
	SourceID      string `json:"source_id"`
	SourceVersion string `json:"source_version"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
	Quote         string `json:"quote"`
}

type FixtureFact struct {
	ID                string            `json:"id"`
	RuleID            string            `json:"rule_id"`
	Dimension         string            `json:"dimension"`
	ObjectRef         string            `json:"object_ref"`
	Value             string            `json:"value"`
	Unit              string            `json:"unit"`
	SourceRole        string            `json:"source_role"`
	MeasurementWindow string            `json:"measurement_window,omitempty"`
	TimeZone          string            `json:"time_zone,omitempty"`
	Calendar          string            `json:"calendar,omitempty"`
	OutageDefinition  string            `json:"outage_definition,omitempty"`
	Exclusions        []string          `json:"exclusions"`
	Basis             string            `json:"basis,omitempty"`
	Applicable        *bool             `json:"applicable,omitempty"`
	Citations         []FixtureCitation `json:"citations"`
}

type FixtureExpected struct {
	RuleID      string            `json:"rule_id"`
	RuleVersion string            `json:"rule_version"`
	Relation    Relation          `json:"relation"`
	ReasonCode  string            `json:"reason_code"`
	Citations   []FixtureCitation `json:"citations"`
}

type FixtureCaseResult struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Kind     string   `json:"kind"`
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures"`
	Report   *Report  `json:"report,omitempty"`
}

type FixtureRun struct {
	SuiteID     string              `json:"suite_id"`
	PackID      string              `json:"pack_id"`
	PackVersion string              `json:"pack_version"`
	PackHash    string              `json:"pack_hash"`
	Passed      bool                `json:"passed"`
	Cases       []FixtureCaseResult `json:"cases"`
}

type FixtureChange struct {
	CaseID      string  `json:"case_id"`
	RuleID      string  `json:"rule_id,omitempty"`
	Before      *Result `json:"before,omitempty"`
	After       *Result `json:"after,omitempty"`
	BeforeError string  `json:"before_error,omitempty"`
	AfterError  string  `json:"after_error,omitempty"`
}

type FixtureComparison struct {
	Baseline FixtureRun      `json:"baseline"`
	Changes  []FixtureChange `json:"changes"`
}

// Validation is separate from execution so failures cannot be mistaken for a
// passed sample run. The ID is compatible with capabilitypack's string ref.
func validateFixtureSuite(content manifestContent) error {
	suite := content.FixtureSuite
	if suite == nil {
		return nil
	}
	if suite.SchemaVersion != fixtureSchemaVersion || !validIdentifier(suite.ID) ||
		suite.PackID != content.ID || suite.PackVersion != content.Version || suite.DataClass != "synthetic" {
		return errors.New("fixture_suite identity, version or synthetic classification is invalid")
	}
	raw, err := json.Marshal(suite)
	if err != nil || len(raw) > 512*1024 || len(suite.Cases) < 3 || len(suite.Cases) > 32 {
		return errors.New("fixture_suite must contain 3 to 32 cases and at most 512 KiB")
	}
	seen := make(map[string]bool, len(suite.Cases))
	kinds := map[string]bool{}
	for _, item := range suite.Cases {
		if !validIdentifier(item.ID) || seen[item.ID] || item.Title == "" || len(item.Title) > 256 || !utf8.ValidString(item.Title) {
			return fmt.Errorf("fixture case %q has invalid or duplicate identity", item.ID)
		}
		seen[item.ID] = true
		if item.Kind != "positive" && item.Kind != "negative" && item.Kind != "insufficient" {
			return fmt.Errorf("fixture case %q has invalid kind", item.ID)
		}
		kinds[item.Kind] = true
		if err := validateFixtureCase(content, item); err != nil {
			return fmt.Errorf("fixture case %q: %w", item.ID, err)
		}
	}
	if !kinds["positive"] || !kinds["negative"] || !kinds["insufficient"] {
		return errors.New("fixture_suite requires positive, negative and insufficient cases")
	}
	return nil
}

func validateFixtureCase(content manifestContent, item FixtureCase) error {
	input := item.Input
	at, err := time.Parse(time.RFC3339, input.At)
	from, _ := time.Parse(time.RFC3339Nano, content.EffectiveFrom)
	until, _ := time.Parse(time.RFC3339Nano, content.EffectiveUntil)
	if err != nil || at.Before(from) || !at.Before(until) || !validIdentifier(input.BusinessLine) ||
		!validIdentifier(input.Scenario) || !validIdentifier(input.ContractType) {
		return errors.New("invalid scope or time outside the pack effective period")
	}
	if len(input.Sources) == 0 || len(input.Sources) > 16 || len(input.Facts) > 128 || len(input.AuthorityGates) > 128 {
		return errors.New("invalid source, fact or authority gate count")
	}
	sources := make(map[string]bool, len(input.Sources))
	total := 0
	for _, source := range input.Sources {
		key := source.ID + "\x00" + source.Version
		if !validIdentifier(source.ID) || !validIdentifier(source.Version) || !validIdentifier(source.Role) ||
			!utf8.ValidString(source.Text) || len(source.Text) > 32*1024 || sources[key] {
			return fmt.Errorf("invalid or duplicate source %q", source.ID)
		}
		sources[key] = true
		total += len(source.Text)
	}
	if total > 256*1024 {
		return errors.New("synthetic source text exceeds 256 KiB")
	}
	facts, citationCount := map[string]bool{}, 0
	for _, fact := range input.Facts {
		if !validIdentifier(fact.ID) || !validIdentifier(fact.RuleID) || facts[fact.ID] || len(fact.Citations) > 8 {
			return fmt.Errorf("invalid or duplicate fact %q", fact.ID)
		}
		facts[fact.ID] = true
		citationCount += len(fact.Citations)
	}
	if citationCount > 256 {
		return errors.New("too many fact citations")
	}
	matched := map[string]Rule{}
	for _, rule := range content.Rules {
		if rule.BusinessScope.BusinessLine == input.BusinessLine && rule.BusinessScope.Scenario == input.Scenario && rule.BusinessScope.ContractType == input.ContractType {
			matched[rule.RuleID] = rule
		}
	}
	if len(matched) == 0 || len(item.Expected) != len(matched) {
		return errors.New("expected results must cover every rule in the sample scope")
	}
	wantedKind := map[string]Relation{"positive": RelationCompatible, "negative": RelationConflict, "insufficient": RelationInsufficient}[item.Kind]
	seenExpected, hasKind := map[string]bool{}, false
	for _, expected := range item.Expected {
		rule, ok := matched[expected.RuleID]
		if !ok || seenExpected[expected.RuleID] || expected.RuleVersion != rule.Version || !validIdentifier(expected.ReasonCode) || expected.Citations == nil {
			return fmt.Errorf("invalid or duplicate expected result for rule %q", expected.RuleID)
		}
		seenExpected[expected.RuleID] = true
		switch expected.Relation {
		case RelationCompatible, RelationConflict:
			if len(expected.Citations) == 0 {
				return fmt.Errorf("expected result for rule %q requires citations", expected.RuleID)
			}
		case RelationInsufficient, RelationNotApplicable:
		default:
			return fmt.Errorf("invalid relation for rule %q", expected.RuleID)
		}
		if expected.Relation == wantedKind { hasKind = true }
		for _, citation := range expected.Citations {
			if !sources[citation.SourceID+"\x00"+citation.SourceVersion] || citation.Start < 0 || citation.End <= citation.Start || citation.Quote == "" {
				return fmt.Errorf("invalid expected citation for rule %q", expected.RuleID)
			}
		}
	}
	if !hasKind { return fmt.Errorf("case kind %q does not match any expected relation", item.Kind) }
	return nil
}

func cloneFixtureSuite(suite *FixtureSuite) *FixtureSuite {
	if suite == nil { return nil }
	copy := *suite
	copy.Cases = cloneFixtureSlice(suite.Cases)
	for i, item := range suite.Cases {
		copy.Cases[i].Input.Sources = cloneFixtureSlice(item.Input.Sources)
		copy.Cases[i].Input.AuthorityGates = cloneFixtureSlice(item.Input.AuthorityGates)
		copy.Cases[i].Input.Facts = cloneFixtureSlice(item.Input.Facts)
		for j, fact := range item.Input.Facts {
			copy.Cases[i].Input.Facts[j].Exclusions = cloneFixtureSlice(fact.Exclusions)
			copy.Cases[i].Input.Facts[j].Citations = cloneFixtureSlice(fact.Citations)
			copy.Cases[i].Input.Facts[j].Applicable = clonePointer(fact.Applicable)
		}
		copy.Cases[i].Expected = cloneFixtureSlice(item.Expected)
		for j, expected := range item.Expected {
			copy.Cases[i].Expected[j].Citations = cloneFixtureSlice(expected.Citations)
		}
	}
	return &copy
}

func cloneFixtureSlice[T any](items []T) []T {
	if items == nil { return nil }
	return append(make([]T, 0, len(items)), items...)
}

func fixtureExecutable(pack *Pack) (*Pack, error) {
	if pack == nil { return nil, ErrInvalidManifest }
	var err error
	switch pack.lifecycle.State {
	case StateDraft:
		pack, err = pack.Validate()
		if err != nil { return nil, err }
		fallthrough
	case StateValidated:
		pack, err = pack.Approve("rulepack-sample-test", pack.Hash())
		if err != nil { return nil, err }
		fallthrough
	case StateApproved:
		return pack.Publish()
	case StatePublished:
		return pack, nil
	default:
		return nil, ErrInvalidState
	}
}

func fixtureRequest(tenant string, input FixtureInput) (Request, error) {
	at, err := time.Parse(time.RFC3339, input.At)
	if err != nil { return Request{}, err }
	request := Request{
		Principal: access.Principal{UserID: "rulepack-sample", TenantID: tenant, Enabled: true,
			Grants: []access.Grant{{Role: "reviewer", Scope: "case", CaseID: "rulepack-sample"}}},
		Resource: access.Resource{TenantID: tenant, CaseID: "rulepack-sample", OrgID: "rulepack-sample"},
		BusinessLine: input.BusinessLine, Scenario: input.Scenario, ContractType: input.ContractType,
		At: at, AuthorityGates: append([]AuthorityGate(nil), input.AuthorityGates...),
	}
	digests := make(map[string]string, len(input.Sources))
	for _, source := range input.Sources {
		digest := digestHex([]byte(source.Text))
		digests[source.ID+"\x00"+source.Version] = digest
		request.Sources = append(request.Sources, Source{ID: source.ID, Version: source.Version, Role: source.Role,
			TenantID: tenant, CaseID: "rulepack-sample", Digest: digest, Text: source.Text})
	}
	for _, fact := range input.Facts {
		converted := Fact{ID: fact.ID, RuleID: fact.RuleID, Dimension: fact.Dimension, ObjectRef: fact.ObjectRef,
			Value: fact.Value, Unit: fact.Unit, SourceRole: fact.SourceRole, MeasurementWindow: fact.MeasurementWindow,
			TimeZone: fact.TimeZone, Calendar: fact.Calendar, OutageDefinition: fact.OutageDefinition,
			Basis: fact.Basis, Applicable: fact.Applicable}
		if fact.Exclusions != nil { converted.Exclusions = append([]string{}, fact.Exclusions...) }
		for _, citation := range fact.Citations {
			converted.Citations = append(converted.Citations, Citation{SourceID: citation.SourceID, SourceVersion: citation.SourceVersion,
				TenantID: tenant, CaseID: "rulepack-sample", SourceDigest: digests[citation.SourceID+"\x00"+citation.SourceVersion],
				Start: citation.Start, End: citation.End, Quote: citation.Quote})
		}
		request.Facts = append(request.Facts, converted)
	}
	return request, nil
}

// RunFixtureSuite executes every case against the exact pack's synthetic suite.
func RunFixtureSuite(ctx context.Context, pack *Pack) (FixtureRun, error) {
	if pack == nil || pack.content.FixtureSuite == nil { return FixtureRun{}, errors.New("rule pack has no fixture_suite") }
	if err := validateFixtureSuite(pack.content); err != nil { return FixtureRun{}, err }
	return runFixtureCases(ctx, pack, pack.content.FixtureSuite)
}

func runFixtureCases(ctx context.Context, pack *Pack, suite *FixtureSuite) (FixtureRun, error) {
	if ctx == nil { return FixtureRun{}, errors.New("nil context") }
	executable, err := fixtureExecutable(pack)
	if err != nil { return FixtureRun{}, err }
	out := FixtureRun{SuiteID: suite.ID, PackID: pack.content.ID, PackVersion: pack.content.Version,
		PackHash: pack.Hash(), Passed: true, Cases: make([]FixtureCaseResult, 0, len(suite.Cases))}
	for _, item := range suite.Cases {
		if err := ctx.Err(); err != nil { return FixtureRun{}, err }
		result := FixtureCaseResult{ID: item.ID, Title: item.Title, Kind: item.Kind, Failures: []string{}}
		request, err := fixtureRequest(pack.content.TenantID, item.Input)
		if err == nil {
			var report Report
			report, err = executable.Execute(ctx, request)
			if err == nil { result.Report = &report; result.Failures = fixtureFailures(item.Expected, report.Results) }
		}
		if err != nil {
			if ctx.Err() != nil { return FixtureRun{}, ctx.Err() }
			result.Failures = append(result.Failures, "execution: "+err.Error())
		}
		result.Passed = len(result.Failures) == 0
		out.Passed = out.Passed && result.Passed
		out.Cases = append(out.Cases, result)
	}
	return out, nil
}

func fixtureFailures(expected []FixtureExpected, actual []Result) []string {
	failures := []string{}
	byID := make(map[string]Result, len(actual))
	for _, result := range actual { byID[result.RuleID] = result }
	for _, want := range expected {
		got, ok := byID[want.RuleID]
		if !ok { failures = append(failures, fmt.Sprintf("%s: no result", want.RuleID)); continue }
		delete(byID, want.RuleID)
		if got.RuleVersion != want.RuleVersion { failures = append(failures, fmt.Sprintf("%s: rule version expected %s, got %s", want.RuleID, want.RuleVersion, got.RuleVersion)) }
		if got.Relation != want.Relation { failures = append(failures, fmt.Sprintf("%s: relation expected %s, got %s", want.RuleID, want.Relation, got.Relation)) }
		if got.ReasonCode != want.ReasonCode { failures = append(failures, fmt.Sprintf("%s: reason expected %s, got %s", want.RuleID, want.ReasonCode, got.ReasonCode)) }
		if !fixtureCitationsEqual(want.Citations, got.Citations) { failures = append(failures, fmt.Sprintf("%s: citations differ from expected source, version, span or quote", want.RuleID)) }
	}
	extra := make([]string, 0, len(byID))
	for id := range byID { extra = append(extra, id) }
	sort.Strings(extra)
	for _, id := range extra { failures = append(failures, fmt.Sprintf("%s: unexpected result", id)) }
	return failures
}

func fixtureCitationsEqual(want []FixtureCitation, got []Citation) bool {
	if len(want) != len(got) { return false }
	wanted := append([]FixtureCitation(nil), want...)
	actual := make([]FixtureCitation, 0, len(got))
	for _, c := range got { actual = append(actual, FixtureCitation{SourceID: c.SourceID, SourceVersion: c.SourceVersion, Start: c.Start, End: c.End, Quote: c.Quote}) }
	less := func(a, b FixtureCitation) bool {
		if a.SourceID != b.SourceID { return a.SourceID < b.SourceID }
		if a.SourceVersion != b.SourceVersion { return a.SourceVersion < b.SourceVersion }
		if a.Start != b.Start { return a.Start < b.Start }
		if a.End != b.End { return a.End < b.End }
		return a.Quote < b.Quote
	}
	sort.Slice(wanted, func(i, j int) bool { return less(wanted[i], wanted[j]) })
	sort.Slice(actual, func(i, j int) bool { return less(actual[i], actual[j]) })
	for i := range wanted { if wanted[i] != actual[i] { return false } }
	return true
}

// CompareFixtureSuite runs the candidate inputs on an independently pinned
// version. Baseline failures are observations, not candidate passes.
func CompareFixtureSuite(ctx context.Context, candidate, baseline *Pack, candidateRun FixtureRun) (FixtureComparison, error) {
	if candidate == nil || baseline == nil || candidate.content.FixtureSuite == nil ||
		candidate.content.TenantID != baseline.content.TenantID || candidate.content.ID != baseline.content.ID ||
		candidateRun.PackID != candidate.content.ID || candidateRun.PackVersion != candidate.content.Version || candidateRun.PackHash != candidate.Hash() ||
		candidateRun.SuiteID != candidate.content.FixtureSuite.ID || len(candidateRun.Cases) != len(candidate.content.FixtureSuite.Cases) {
		return FixtureComparison{}, errors.New("fixture comparison pack or candidate identity mismatch")
	}
	before, err := runFixtureCases(ctx, baseline, candidate.content.FixtureSuite)
	if err != nil { return FixtureComparison{}, err }
	out := FixtureComparison{Baseline: before, Changes: []FixtureChange{}}
	for i, oldCase := range before.Cases {
		newCase := candidateRun.Cases[i]
		if oldCase.ID != newCase.ID { return FixtureComparison{}, errors.New("fixture comparison case mismatch") }
		if oldCase.Report == nil || newCase.Report == nil {
			if !reflect.DeepEqual(oldCase.Failures, newCase.Failures) {
				out.Changes = append(out.Changes, FixtureChange{CaseID: oldCase.ID, BeforeError: strings.Join(oldCase.Failures, "; "), AfterError: strings.Join(newCase.Failures, "; ")})
			}
			continue
		}
		oldResults, newResults, ids := map[string]Result{}, map[string]Result{}, map[string]bool{}
		for _, result := range oldCase.Report.Results { oldResults[result.RuleID] = result; ids[result.RuleID] = true }
		for _, result := range newCase.Report.Results { newResults[result.RuleID] = result; ids[result.RuleID] = true }
		ordered := make([]string, 0, len(ids))
		for id := range ids { ordered = append(ordered, id) }
		sort.Strings(ordered)
		for _, id := range ordered {
			oldResult, oldOK := oldResults[id]; newResult, newOK := newResults[id]
			if oldOK && newOK && reflect.DeepEqual(oldResult, newResult) { continue }
			change := FixtureChange{CaseID: oldCase.ID, RuleID: id}
			if oldOK { change.Before = &oldResult }
			if newOK { change.After = &newResult }
			out.Changes = append(out.Changes, change)
		}
	}
	return out, nil
}
