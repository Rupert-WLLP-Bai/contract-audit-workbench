package agentflow

import (
	"errors"
	"testing"
)

func advisoryDigest(seed string) string {
	return TextHash(seed)
}

type advisorySelectionFixture struct {
	record    *KnowledgeSelectionRecord
	request   Request
	planner   PlannerConfiguration
	probB     float64
	probA     float64
	configAdv AdvisoryConfiguration
}

func newAdvisorySelectionFixture() advisorySelectionFixture {
	digestA, digestB := advisoryDigest("knowledge-a"), advisoryDigest("knowledge-b")
	probabilityB, probabilityA := 0.9, 0.1
	adviceB := &KnowledgeAdvisoryCandidate{
		KnowledgeID: "knowledge-b", VersionID: "v1", ContentSHA256: digestB,
		Alias: "candidate_0", Probability: &probabilityB, Band: "relevant", RankBefore: 2, RankAfter: 1,
	}
	adviceA := &KnowledgeAdvisoryCandidate{
		KnowledgeID: "knowledge-a", VersionID: "v1", ContentSHA256: digestA,
		Alias: "candidate_1", Probability: &probabilityA, Band: "not_relevant", RankBefore: 1, RankAfter: 2,
	}
	configuration := AdvisoryConfiguration{
		Mode: AdvisoryModeAdvisory, Provider: AdvisoryProviderGateway, Model: "jev-1.14.0",
		QuestionSetVersion: AdvisoryQuestionSetVersion, Egress: AdvisoryEgressMinimized,
		Features: []string{AdvisoryFeatureKnowledgeRerank}, ThresholdHigh: 0.70, ThresholdLow: 0.30,
		MaxCandidates: 16, TimeoutMS: 3000,
	}
	plannerID := advisoryDigest("planner")
	record := &KnowledgeSelectionRecord{
		Candidates: []KnowledgeCandidate{
			{KnowledgeID: "knowledge-b", VersionID: "v1", ContentSHA256: digestB, Kind: "guidance", Score: 40, Decision: "selected", Reason: "relevance", Advisory: adviceB},
			{KnowledgeID: "knowledge-a", VersionID: "v1", ContentSHA256: digestA, Kind: "guidance", Score: 50, Decision: "selected", Reason: "relevance", Advisory: adviceA},
		},
		Advisory: &KnowledgeAdvisoryRecord{
			Mode: AdvisoryModeAdvisory, Feature: AdvisoryFeatureKnowledgeRerank, Provider: AdvisoryProviderGateway,
			ConfigurationID: plannerID, ModelRequested: "jev-1.14.0", ModelObserved: "jev-1.14.0",
			QuestionSetVersion: AdvisoryQuestionSetVersion, Egress: AdvisoryEgressMinimized,
			RequestSHA256: advisoryDigest("request"), JudgmentID: advisoryDigest("judgment"), Status: "completed",
			Candidates: []KnowledgeAdvisoryCandidate{*adviceB, *adviceA},
		},
	}
	request := Request{Knowledge: []KnowledgeReference{
		{KnowledgeID: "knowledge-b", VersionID: "v1", ContentSHA256: digestB, Kind: "guidance", Summary: "B"},
		{KnowledgeID: "knowledge-a", VersionID: "v1", ContentSHA256: digestA, Kind: "guidance", Summary: "A"},
	}}
	planner := PlannerConfiguration{ConfigurationID: plannerID, Advisory: &configuration}
	return advisorySelectionFixture{
		record: record, request: request, planner: planner, probB: probabilityB, probA: probabilityA, configAdv: configuration,
	}
}

func TestValidateKnowledgeAdvisoryAcceptsFinalOrderWithDeterministicRanks(t *testing.T) {
	fixture := newAdvisorySelectionFixture()
	if err := validateKnowledgeAdvisory(fixture.record, fixture.request); err != nil {
		t.Fatalf("valid advisory record rejected: %v", err)
	}
	if err := validateAdvisoryAgainstConfiguration(fixture.record, fixture.planner); err != nil {
		t.Fatalf("valid advisory configuration rejected: %v", err)
	}
	fixture.record.Advisory.Provider = AdvisoryProviderDirect
	if err := validateKnowledgeAdvisory(fixture.record, fixture.request); err != nil {
		t.Fatalf("direct provider transport was rejected: %v", err)
	}
}

func TestValidateKnowledgeAdvisoryKeepsRequiredPoliciesInDeliveredOrder(t *testing.T) {
	fixture := newAdvisorySelectionFixture()
	policyDigest := advisoryDigest("policy")
	fixture.record.Candidates = append([]KnowledgeCandidate{{
		KnowledgeID: "policy", VersionID: "v1", ContentSHA256: policyDigest,
		Kind: "policy", Required: true, Decision: "selected", Reason: "required_policy",
	}}, fixture.record.Candidates...)
	fixture.request.Knowledge = append([]KnowledgeReference{{
		KnowledgeID: "policy", VersionID: "v1", ContentSHA256: policyDigest,
		Kind: "policy", Required: true, Summary: "policy",
	}}, fixture.request.Knowledge...)
	fixture.record.Algorithm = "chinese-bigram-label/v1"
	fixture.record.SnapshotSHA256 = advisoryDigest("snapshot")
	fixture.record.PolicyStatus, fixture.record.PolicyReason = "available", ""
	fixture.record.Input = KnowledgeSelectionInput{
		BusinessLine: "ENT", Scenario: "telecom", ContractType: "service",
		Gaps: []KnowledgeInputGap{}, Evidence: []KnowledgeInputEvidence{},
	}
	fixture.record.Budget = KnowledgeSelectionBudget{
		ReferenceLimit: PlannerKnowledgeReferenceBudget, FieldBytes: PlannerKnowledgeFieldBytes,
		EvidenceRunesPerDocument: 4096, RequiredCount: 1, OptionalCandidates: 2, OptionalSelected: 2,
		ContentBytesLimit: 1000, ContentBytesUsed: 8,
		ReadCallsLimit: KnowledgeReadMaxCalls, ReadMaxRunes: KnowledgeReadMaxRunes, ReadMaxBytes: KnowledgeReadMaxBytes,
	}
	fixture.record.PlannerRounds = []KnowledgePlanningRound{}
	fixture.record.Reads = []KnowledgeReadRecord{}
	fixture.request.KnowledgeSelection = fixture.record
	if err := validateKnowledgeAdvisory(fixture.record, fixture.request); err != nil {
		t.Fatalf("required policy with advisory candidates was rejected: %v", err)
	}
	if err := validateKnowledgeSelection(fixture.request); err != nil {
		t.Fatalf("advisory trace with a required policy failed full selection validation: %v", err)
	}
	fixture.request.Knowledge[0], fixture.request.Knowledge[1] = fixture.request.Knowledge[1], fixture.request.Knowledge[0]
	if err := validateKnowledgeAdvisory(fixture.record, fixture.request); !errors.Is(err, ErrInput) {
		t.Fatalf("out-of-order required policy err = %v, want ErrInput", err)
	}
	if err := validateKnowledgeSelection(fixture.request); !errors.Is(err, ErrInput) {
		t.Fatalf("full selection accepted an out-of-order required policy: %v", err)
	}
}

func TestValidateKnowledgeAdvisoryRejectsAuthorityAndOrderingDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*advisorySelectionFixture)
	}{
		{"required candidate cannot carry advice", func(f *advisorySelectionFixture) {
			f.record.Candidates[0].Required = true
		}},
		{"score zero cannot carry advice", func(f *advisorySelectionFixture) {
			f.record.Candidates[0].Score = 0
		}},
		{"candidate order does not match delivered refs", func(f *advisorySelectionFixture) {
			f.request.Knowledge[0], f.request.Knowledge[1] = f.request.Knowledge[1], f.request.Knowledge[0]
		}},
		{"rank before is not the deterministic rank", func(f *advisorySelectionFixture) {
			f.record.Advisory.Candidates[0].RankBefore = 1
			f.record.Candidates[0].Advisory.RankBefore = 1
		}},
		{"rank after is not the final rank", func(f *advisorySelectionFixture) {
			f.record.Advisory.Candidates[0].RankAfter = 2
			f.record.Candidates[0].Advisory.RankAfter = 2
		}},
		{"advice on an unknown candidate", func(f *advisorySelectionFixture) {
			f.record.Advisory.Candidates[1].KnowledgeID = "other"
		}},
		{"observed model copied from request", func(f *advisorySelectionFixture) {
			f.record.Advisory.ModelObserved = "jev-1.15.0"
		}},
		{"completed without judgment identity", func(f *advisorySelectionFixture) {
			f.record.Advisory.JudgmentID = ""
		}},
		{"unsupported band", func(f *advisorySelectionFixture) {
			f.record.Advisory.Candidates[0].Band = "maybe"
			f.record.Candidates[0].Advisory.Band = "maybe"
		}},
		{"unavailable status must not carry an observed model", func(f *advisorySelectionFixture) {
			f.record.Advisory.Status = "unavailable"
			f.record.Advisory.JudgmentID = ""
			f.record.Advisory.ModelObserved = "jev-1.14.0"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAdvisorySelectionFixture()
			test.mutate(&fixture)
			if err := validateKnowledgeAdvisory(fixture.record, fixture.request); !errors.Is(err, ErrInput) {
				t.Fatalf("err = %v, want ErrInput", err)
			}
		})
	}
}

func TestValidateAdvisoryAgainstConfigurationRejectsBandDriftAndMissingConfig(t *testing.T) {
	fixture := newAdvisorySelectionFixture()
	fixture.record.Advisory.Candidates[0].Band = "not_relevant"
	fixture.record.Candidates[0].Advisory.Band = "not_relevant"
	if err := validateAdvisoryAgainstConfiguration(fixture.record, fixture.planner); !errors.Is(err, ErrInput) {
		t.Fatalf("band drift err = %v", err)
	}
	fixture = newAdvisorySelectionFixture()
	fixture.planner.Advisory = nil
	if err := validateAdvisoryAgainstConfiguration(fixture.record, fixture.planner); !errors.Is(err, ErrInput) {
		t.Fatalf("missing frozen configuration err = %v", err)
	}
	fixture = newAdvisorySelectionFixture()
	fixture.record.Advisory.Provider = AdvisoryProviderDirect
	fixture.planner.Advisory = &fixture.configAdv
	fixture.planner.Advisory.Provider = AdvisoryProviderDirect
	if err := validateAdvisoryAgainstConfiguration(fixture.record, fixture.planner); err != nil {
		t.Fatalf("direct provider configuration rejected: %v", err)
	}
}

func TestValidateKnowledgeAdvisoryRejectsProbabilityOnFailedBatch(t *testing.T) {
	fixture := newAdvisorySelectionFixture()
	fixture.record.Advisory.Status = "unavailable"
	fixture.record.Advisory.ModelObserved = ""
	fixture.record.Advisory.JudgmentID = ""
	fixture.record.Advisory.Candidates[0].Band = "unknown"
	fixture.record.Candidates[0].Advisory.Band = "unknown"
	if err := validateKnowledgeAdvisory(fixture.record, fixture.request); !errors.Is(err, ErrInput) {
		t.Fatalf("failed batch with a probability err = %v, want ErrInput", err)
	}
}

func TestValidateAdvisoryAllowsUnknownProbabilityOnlyForUnknownBand(t *testing.T) {
	fixture := newAdvisorySelectionFixture()
	fixture.record.Advisory.Status = "unavailable"
	fixture.record.Advisory.JudgmentID = ""
	fixture.record.Advisory.ModelObserved = ""
	for index := range fixture.record.Advisory.Candidates {
		fixture.record.Advisory.Candidates[index].Band = "unknown"
		fixture.record.Advisory.Candidates[index].Probability = nil
		fixture.record.Candidates[index].Advisory.Band = "unknown"
		fixture.record.Candidates[index].Advisory.Probability = nil
	}
	if err := validateKnowledgeAdvisory(fixture.record, fixture.request); err != nil {
		t.Fatalf("unavailable batch was rejected: %v", err)
	}
	fixture.record.Advisory.Candidates[0].Band = "relevant"
	fixture.record.Candidates[0].Advisory.Band = "relevant"
	if err := validateKnowledgeAdvisory(fixture.record, fixture.request); !errors.Is(err, ErrInput) {
		t.Fatalf("known band without probability err = %v", err)
	}
}
