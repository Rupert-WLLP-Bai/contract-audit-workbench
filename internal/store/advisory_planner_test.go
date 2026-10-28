package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"contractaudit/internal/advisory"
	"contractaudit/internal/agentflow"
)

type fakeAdvisoryEvaluator struct {
	calls         int
	probabilities map[string]float64
	err           error
}

func (f *fakeAdvisoryEvaluator) Evaluate(_ context.Context, request advisory.KnowledgeRequest) (advisoryOutcome, error) {
	f.calls++
	if f.err != nil {
		return advisoryOutcome{}, f.err
	}
	result := advisory.KnowledgeResult{
		Model: request.Model, Probabilities: map[string]float64{}, Bands: map[string]advisory.Band{},
	}
	for _, candidate := range request.Candidates {
		probability := f.probabilities[candidate.Alias]
		band, err := advisory.BandFor(probability, 0.70, 0.30)
		if err != nil {
			return advisoryOutcome{}, err
		}
		result.Probabilities[candidate.Alias] = probability
		result.Bands[candidate.Alias] = band
	}
	return advisoryOutcome{Result: result, JudgmentID: strings.Repeat("d", 64)}, nil
}

func plannerAdvisoryFixture() (*ReviewKnowledgeSnapshot, []agentflow.Gap, agentflow.AdvisoryConfiguration, string) {
	snapshot := ReviewKnowledgeSnapshot{
		SnapshotSHA256: sha256Bytes([]byte("snapshot")),
		Scope:          KnowledgeScope{BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service"},
	}
	snapshot.RequiredPolicies = []GovernedKnowledgeSelection{plannerSelectionFixture("policy", "policy", "制度摘要")}
	snapshot.OptionalCandidates = []GovernedKnowledgeSelection{
		plannerSelectionFixture("guide-a", "guidance", "最低带宽核验"),
		plannerSelectionFixture("guide-b", "guidance", "带宽测试"),
	}
	gaps := []agentflow.Gap{{ID: "bandwidth", Domain: agentflow.Technical, Capability: "scope", Question: "核查最低带宽"}}
	configuration := agentflow.AdvisoryConfiguration{
		Mode: agentflow.AdvisoryModeAdvisory, Provider: agentflow.AdvisoryProviderGateway, Model: "jev-1.14.0",
		QuestionSetVersion: agentflow.AdvisoryQuestionSetVersion, Egress: agentflow.AdvisoryEgressMinimized,
		Features: []string{agentflow.AdvisoryFeatureKnowledgeRerank}, ThresholdHigh: 0.70, ThresholdLow: 0.30,
		MaxCandidates: 16, TimeoutMS: 3000,
	}
	return &snapshot, gaps, configuration, strings.Repeat("c", 64)
}

func TestAdvisoryPlannerReordersOnlyOptionalCandidates(t *testing.T) {
	snapshot, gaps, configuration, plannerID := plannerAdvisoryFixture()
	baselineRefs, baselineTrace, err := governedPlannerKnowledge(snapshot, gaps, nil, 2<<20)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if len(baselineRefs) != 3 || baselineRefs[1].KnowledgeID != "guide-a" || baselineRefs[2].KnowledgeID != "guide-b" {
		t.Fatalf("baseline order = %+v", baselineRefs)
	}
	baselineScores := map[string]int{}
	for _, candidate := range baselineTrace.Candidates {
		baselineScores[candidate.KnowledgeID] = candidate.Score
	}

	// candidate_0 is the deterministic first optional (guide-a). Jev says it is
	// not relevant and the second candidate is relevant.
	evaluator := &fakeAdvisoryEvaluator{probabilities: map[string]float64{"candidate_0": 0.10, "candidate_1": 0.90}}
	refs, trace, err := governedPlannerKnowledgeWithAdvisory(context.Background(), snapshot, gaps, nil, 2<<20, &configuration, plannerID, evaluator)
	if err != nil {
		t.Fatalf("advisory planner: %v", err)
	}
	if evaluator.calls != 1 {
		t.Fatalf("evaluator calls = %d", evaluator.calls)
	}
	if len(refs) != 3 || !refs[0].Required || refs[0].KnowledgeID != "policy" ||
		refs[1].KnowledgeID != "guide-b" || refs[2].KnowledgeID != "guide-a" {
		t.Fatalf("advisory order changed more than optional identity: %+v", refs)
	}
	for _, candidate := range trace.Candidates {
		if candidate.Score != baselineScores[candidate.KnowledgeID] {
			t.Fatalf("deterministic score changed for %s: %d vs %d", candidate.KnowledgeID, candidate.Score, baselineScores[candidate.KnowledgeID])
		}
	}
	if trace.Advisory == nil || trace.Advisory.Status != "completed" || trace.Advisory.Provider != agentflow.AdvisoryProviderGateway ||
		trace.Advisory.ModelObserved != configuration.Model || trace.Advisory.ConfigurationID != plannerID {
		t.Fatalf("advisory trace = %+v", trace.Advisory)
	}
	ranks := map[string]struct{ before, after int }{
		"guide-a": {1, 2}, "guide-b": {2, 1},
	}
	for _, candidate := range trace.Advisory.Candidates {
		want, ok := ranks[candidate.KnowledgeID]
		if !ok || candidate.RankBefore != want.before || candidate.RankAfter != want.after {
			t.Fatalf("advisory candidate = %+v", candidate)
		}
	}
	selected := []string{}
	for _, candidate := range trace.Candidates {
		if candidate.Decision == "selected" {
			selected = append(selected, candidate.KnowledgeID)
		}
	}
	for index, ref := range refs {
		if selected[index] != ref.KnowledgeID {
			t.Fatalf("trace order does not match delivered references: %+v vs %+v", selected, refs)
		}
	}
}

func TestAdvisoryFailureKeepsTheDeterministicBytes(t *testing.T) {
	snapshot, gaps, configuration, plannerID := plannerAdvisoryFixture()
	baselineRefs, baselineTrace, err := governedPlannerKnowledge(snapshot, gaps, nil, 2<<20)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	for _, evaluator := range []advisoryEvaluator{
		nil,
		&fakeAdvisoryEvaluator{err: errors.New("provider timeout")},
	} {
		refs, trace, err := governedPlannerKnowledgeWithAdvisory(context.Background(), snapshot, gaps, nil, 2<<20, &configuration, plannerID, evaluator)
		if err != nil {
			t.Fatalf("fallback planner: %v", err)
		}
		if !reflect.DeepEqual(refs, baselineRefs) {
			t.Fatalf("fallback changed references: %+v vs %+v", refs, baselineRefs)
		}
		if trace.Advisory == nil || trace.Advisory.Status != "unavailable" || trace.Advisory.ErrorCode == "" {
			t.Fatalf("fallback advisory trace = %+v", trace.Advisory)
		}
		for index, candidate := range trace.Candidates {
			if candidate.KnowledgeID != baselineTrace.Candidates[index].KnowledgeID ||
				candidate.Score != baselineTrace.Candidates[index].Score ||
				candidate.Decision != baselineTrace.Candidates[index].Decision {
				t.Fatalf("fallback candidate %d = %+v, baseline %+v", index, candidate, baselineTrace.Candidates[index])
			}
		}
	}
}

func TestAdvisoryPlannerRequiredPolicyNeverDependsOnJev(t *testing.T) {
	snapshot, gaps, configuration, plannerID := plannerAdvisoryFixture()
	snapshot.OptionalCandidates = nil
	evaluator := &fakeAdvisoryEvaluator{probabilities: map[string]float64{}}
	refs, trace, err := governedPlannerKnowledgeWithAdvisory(context.Background(), snapshot, gaps, nil, 2<<20, &configuration, plannerID, evaluator)
	if err != nil {
		t.Fatalf("required-only planner: %v", err)
	}
	if len(refs) != 1 || !refs[0].Required || evaluator.calls != 0 {
		t.Fatalf("required policy was sent to Jev or removed: refs=%+v calls=%d", refs, evaluator.calls)
	}
	if trace.Advisory == nil || trace.Advisory.Status != "not_applicable" || len(trace.Advisory.Candidates) != 0 {
		t.Fatalf("required-only advisory trace = %+v", trace.Advisory)
	}
}
