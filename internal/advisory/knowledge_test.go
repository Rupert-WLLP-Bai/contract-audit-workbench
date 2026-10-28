package advisory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"contractaudit/internal/agentflow"
	"contractaudit/internal/jev"
)

func testConfiguration() agentflow.AdvisoryConfiguration {
	return agentflow.AdvisoryConfiguration{
		Mode:               agentflow.AdvisoryModeAdvisory,
		Provider:           agentflow.AdvisoryProviderGateway,
		Model:              "jev-1.14.0",
		QuestionSetVersion: agentflow.AdvisoryQuestionSetVersion,
		Egress:             agentflow.AdvisoryEgressMinimized,
		Features:           []string{agentflow.AdvisoryFeatureKnowledgeRerank},
		ThresholdHigh:      0.70,
		ThresholdLow:       0.30,
		MaxCandidates:      16,
		TimeoutMS:          3000,
	}
}

func testScope() KnowledgeScope {
	return KnowledgeScope{BusinessLine: "telecom_enterprise", Scenario: "dedicated_line", ContractType: "procurement"}
}

func testGaps() []KnowledgeGap {
	return []KnowledgeGap{{Domain: "compliance", Capability: "warranty", Reason: "missing_evidence"}}
}

func testCandidates() []KnowledgeCandidate {
	return []KnowledgeCandidate{
		{Kind: "guidance", Summary: "质保期限应与采购文件一致", CheckAction: "compare_warranty", MinimumEvidence: "signed_contract"},
		{Kind: "experience", Summary: "专线项目常见交付延期", CheckAction: "check_delivery", MinimumEvidence: "acceptance_record"},
		{Kind: "exception", Summary: "客户书面同意可缩短质保", CheckAction: "check_waiver", MinimumEvidence: "waiver_letter"},
	}
}

func fakeProvider(probabilities map[string]float64, modelID string) Provider {
	return providerFunc(func(_ context.Context, request ProviderRequest) (ProviderResponse, error) {
		answers := make(map[string]jev.NoulAnswer, len(request.Questions))
		for questionID := range request.Questions {
			alias := strings.TrimSuffix(questionID, "_relevance")
			probability, ok := probabilities[alias]
			if !ok {
				continue
			}
			answers[questionID] = jev.NoulAnswer{Noul: probability, Model: modelID}
		}
		return ProviderResponse{Model: modelID, Answers: answers}, nil
	})
}

type providerFunc func(context.Context, ProviderRequest) (ProviderResponse, error)

func (f providerFunc) AskNouls(ctx context.Context, request ProviderRequest) (ProviderResponse, error) {
	return f(ctx, request)
}

func builtRequest(t *testing.T) KnowledgeRequest {
	t.Helper()
	request, err := BuildKnowledgeRequest(testConfiguration(), testScope(), testGaps(), testCandidates())
	if err != nil {
		t.Fatalf("build knowledge request: %v", err)
	}
	return request
}

func TestBuildKnowledgeRequestUsesOnlyAllowlistedMinimizedFields(t *testing.T) {
	scope := KnowledgeScope{
		BusinessLine: "telecom_enterprise",
		Scenario:     "see https://contract.example.com/case/42",
		ContractType: "procurement",
	}
	phone := "138" + "00138000"
	candidates := []KnowledgeCandidate{{
		Kind:            "guidance",
		Summary:         "联系 ops@example.com 或 " + phone + "，合同编号 ABCDEFGHIJKLMNOPQRSTUVWXYZ012345，签署日期 2026-01-31，金额 123456789元",
		CheckAction:     "核对金额 123456789",
		MinimumEvidence: "signed_contract",
	}}
	request, err := BuildKnowledgeRequest(testConfiguration(), scope, testGaps(), candidates)
	if err != nil {
		t.Fatalf("build minimized request: %v", err)
	}
	for _, forbidden := range []string{"contract.example.com", "ops@example.com", phone, "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345", "2026-01-31", "123456789"} {
		if strings.Contains(request.State, forbidden) {
			t.Fatalf("minimized state leaked %q: %s", forbidden, request.State)
		}
	}
	for _, marker := range []string{"[url]", "[email]", "[phone]", "[date]", "[amount]", "[identifier]", "[number]"} {
		if !strings.Contains(request.State, marker) {
			t.Fatalf("state did not apply %s: %s", marker, request.State)
		}
	}
	if request.Candidates[0].Alias != "candidate_0" || request.Candidates[0].Kind != "guidance" {
		t.Fatalf("candidate alias or kind changed: %+v", request.Candidates[0])
	}
	if len(request.RequestSHA256) != 64 || len(request.StateSHA256) != 64 {
		t.Fatalf("request digests are not fixed-width hex: %+v", request)
	}
	if _, ok := request.Questions["candidate_0_relevance"]; !ok {
		t.Fatalf("question id is not derived from the local alias: %+v", request.Questions)
	}
}

func TestBuildKnowledgeRequestOmitsUnsafeOrOversizedCandidates(t *testing.T) {
	candidates := []KnowledgeCandidate{
		{Kind: "unknown", Summary: "unsupported kind"},
		{Kind: "guidance", Summary: strings.Repeat("字", agentflow.PlannerKnowledgeFieldBytes)},
		{Kind: "guidance"},
		{Kind: "guidance", Summary: "safe summary"},
	}
	request, err := BuildKnowledgeRequest(testConfiguration(), testScope(), testGaps(), candidates)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(request.Candidates) != 1 || request.Candidates[0].Summary != "safe summary" {
		t.Fatalf("unsafe candidates were not omitted: %+v", request.Candidates)
	}
	reasons := map[string]string{}
	for _, omission := range request.Omitted {
		reasons[omission.Alias] = omission.Reason
	}
	if reasons["candidate_0"] != "invalid_kind" || reasons["candidate_1"] != "field_too_long" || reasons["candidate_2"] != "empty" {
		t.Fatalf("omission reasons = %+v", reasons)
	}
	if request.Truncated {
		t.Fatalf("omission was reported as truncation: %+v", request)
	}
}

func TestBuildKnowledgeRequestStaysWithinTheStateBudget(t *testing.T) {
	configuration := testConfiguration()
	configuration.MaxCandidates = 16
	gaps := make([]KnowledgeGap, 0, maxStateGaps)
	for index := 0; index < maxStateGaps; index++ {
		gaps = append(gaps, KnowledgeGap{
			Domain:     strings.Repeat("a", 60),
			Capability: strings.Repeat("c", 55) + fmt.Sprintf("%06d", index),
		})
	}
	candidates := make([]KnowledgeCandidate, 0, 20)
	for index := 0; index < 20; index++ {
		candidates = append(candidates, KnowledgeCandidate{
			Kind:            "guidance",
			Summary:         strings.Repeat("规", 170),
			CheckAction:     strings.Repeat("检", 170),
			MinimumEvidence: strings.Repeat("证", 170),
		})
	}
	request, err := BuildKnowledgeRequest(configuration, testScope(), gaps, candidates)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(request.State) > jev.MaxStateBytes {
		t.Fatalf("state is %d bytes, over the provider bound", len(request.State))
	}
	if len(request.Candidates) == 0 || len(request.Candidates) >= configuration.MaxCandidates {
		t.Fatalf("state-budget pressure did not reduce the candidate count: %d", len(request.Candidates))
	}
	if !request.Truncated || len(request.Omitted) == 0 {
		t.Fatalf("budget truncation was not recorded: candidates=%d omitted=%d", len(request.Candidates), len(request.Omitted))
	}
	for _, omission := range request.Omitted {
		if omission.Reason != "state_budget" {
			t.Fatalf("unexpected omission reason after budget pressure: %+v", request.Omitted)
		}
	}
}

func TestBuildKnowledgeRequestCapsTheCandidateLimit(t *testing.T) {
	configuration := testConfiguration()
	configuration.MaxCandidates = 3
	candidates := append(testCandidates(), KnowledgeCandidate{Kind: "experience", Summary: "extra candidate"})
	request, err := BuildKnowledgeRequest(configuration, testScope(), testGaps(), candidates)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(request.Candidates) != 3 || !request.Truncated {
		t.Fatalf("candidate limit was not applied: candidates=%d truncated=%v", len(request.Candidates), request.Truncated)
	}
}

func TestBuildKnowledgeRequestRequiresAllowlistedGaps(t *testing.T) {
	for _, gaps := range [][]KnowledgeGap{
		nil,
		{{Domain: "see the attached contract", Capability: "warranty"}},
		{{Domain: "compliance", Capability: "含合同原文"}},
	} {
		if _, err := BuildKnowledgeRequest(testConfiguration(), testScope(), gaps, testCandidates()); !errors.Is(err, ErrNotApplicable) {
			t.Fatalf("gaps %+v produced err=%v", gaps, err)
		}
	}
}

func TestEvaluateKnowledgeAppliesTheTruthTable(t *testing.T) {
	request := builtRequest(t)
	provider := fakeProvider(map[string]float64{
		"candidate_0": 0.70,
		"candidate_1": 0.30,
		"candidate_2": 0.50,
	}, request.Model)
	result, err := EvaluateKnowledge(context.Background(), provider, testConfiguration(), request)
	if err != nil {
		t.Fatalf("evaluate knowledge: %v", err)
	}
	if result.Bands["candidate_0"] != BandRelevant || result.Bands["candidate_1"] != BandNotRelevant || result.Bands["candidate_2"] != BandUncertain {
		t.Fatalf("bands = %+v", result.Bands)
	}
	if result.RequestSHA256 != request.RequestSHA256 || result.Model != request.Model {
		t.Fatalf("result identity changed: %+v", result)
	}
	if BandRank(BandRelevant) >= BandRank(BandUncertain) || BandRank(BandUncertain) >= BandRank(BandNotRelevant) {
		t.Fatalf("band ranks do not order relevant before uncertain before not_relevant")
	}
}

func TestEvaluateKnowledgeRejectsIncompleteOrDriftingResponses(t *testing.T) {
	request := builtRequest(t)
	tests := []struct {
		name     string
		provider Provider
		want     error
	}{
		{
			name: "missing answer",
			provider: providerFunc(func(context.Context, ProviderRequest) (ProviderResponse, error) {
				return ProviderResponse{Model: request.Model, Answers: map[string]jev.NoulAnswer{
					"candidate_0_relevance": {Noul: 0.9, Model: request.Model},
				}}, nil
			}),
			want: ErrUnavailable,
		},
		{
			name: "model drift",
			provider: fakeProvider(map[string]float64{
				"candidate_0": 0.9, "candidate_1": 0.1, "candidate_2": 0.5,
			}, "jev-1.15.0"),
			want: ErrModelMismatch,
		},
		{
			name: "provider failure",
			provider: providerFunc(func(context.Context, ProviderRequest) (ProviderResponse, error) {
				return ProviderResponse{}, errors.New("transport")
			}),
			want: ErrUnavailable,
		},
		{
			name: "answer model drift",
			provider: providerFunc(func(_ context.Context, request ProviderRequest) (ProviderResponse, error) {
				answers := map[string]jev.NoulAnswer{}
				for questionID := range request.Questions {
					answers[questionID] = jev.NoulAnswer{Noul: 0.5, Model: "jev-9.9.9"}
				}
				return ProviderResponse{Model: request.Model, Answers: answers}, nil
			}),
			want: ErrUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := EvaluateKnowledge(context.Background(), test.provider, testConfiguration(), request); !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
		})
	}
}

func TestFixtureProviderIsDeterministicAndPinned(t *testing.T) {
	configuration := testConfiguration()
	configuration.Mode = agentflow.AdvisoryModeFixture
	configuration.Provider = agentflow.AdvisoryProviderFixture
	configuration.Model = agentflow.AdvisoryFixtureModel
	request, err := BuildKnowledgeRequest(configuration, testScope(), testGaps(), testCandidates())
	if err != nil {
		t.Fatalf("build fixture request: %v", err)
	}
	first, err := EvaluateKnowledge(context.Background(), FixtureProvider{}, configuration, request)
	if err != nil {
		t.Fatalf("fixture evaluation: %v", err)
	}
	second, err := EvaluateKnowledge(context.Background(), FixtureProvider{}, configuration, request)
	if err != nil {
		t.Fatalf("second fixture evaluation: %v", err)
	}
	if first.Bands["candidate_0"] != BandRelevant || first.Bands["candidate_1"] != BandNotRelevant || first.Bands["candidate_2"] != BandUncertain {
		t.Fatalf("fixture bands = %+v", first.Bands)
	}
	for alias, band := range first.Bands {
		if second.Bands[alias] != band || first.Probabilities[alias] != second.Probabilities[alias] {
			t.Fatalf("fixture is not deterministic for %s", alias)
		}
	}
	nonFixture := builtRequest(t)
	if _, err := (FixtureProvider{}).AskNouls(context.Background(), ProviderRequest{
		Feature: FeatureKnowledgeRerank, Egress: EgressMinimized, Model: nonFixture.Model,
		State: nonFixture.State, Questions: nonFixture.Questions,
	}); !errors.Is(err, ErrModelMismatch) {
		t.Fatalf("fixture answered a non-fixture model: %v", err)
	}
}

func TestValidateRequestShapeRejectsTextEgressAndInvalidQuestions(t *testing.T) {
	request := ProviderRequest{
		Feature: FeatureKnowledgeRerank, Egress: EgressMinimized, Model: "jev-1.14.0",
		State: `{"feature":"knowledge_rerank"}`, Questions: map[string]jev.NoulQuestion{
			"candidate_0_relevance": {Instructions: "Is it relevant?", Criteria: jev.NoulCriteria{True: "It applies.", False: "It does not."}},
		},
	}
	if err := ValidateRequestShape(request); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	request.Egress = agentflow.AdvisoryEgressApprovedText
	if err := ValidateRequestShape(request); err == nil {
		t.Fatal("approved_text egress was accepted")
	}
	request.Egress = EgressMinimized
	request.Questions["candidate_0_relevance"] = jev.NoulQuestion{}
	if err := ValidateRequestShape(request); err == nil {
		t.Fatal("empty question was accepted")
	}
}

func TestBandForKeepsTheUncertainBand(t *testing.T) {
	for _, test := range []struct {
		probability float64
		want        Band
	}{
		{0.70, BandRelevant}, {1, BandRelevant}, {0.30, BandNotRelevant}, {0, BandNotRelevant},
		{0.699999, BandUncertain}, {0.300001, BandUncertain}, {0.5, BandUncertain},
	} {
		if got, err := BandFor(test.probability, 0.70, 0.30); err != nil || got != test.want {
			t.Fatalf("BandFor(%v) = %v, %v", test.probability, got, err)
		}
	}
	for _, probability := range []float64{-0.01, 1.01} {
		if _, err := BandFor(probability, 0.70, 0.30); err == nil {
			t.Fatalf("probability %v was accepted", probability)
		}
	}
}

func TestMinimizedFieldRejectsInvalidUTF8AndControlNUL(t *testing.T) {
	if got := minimizedField("ok\x00value", 512); got != "" {
		t.Fatalf("NUL was accepted: %q", got)
	}
	if got := minimizedField(string([]byte{0xff, 0xfe}), 512); got != "" {
		t.Fatalf("invalid UTF-8 was accepted: %q", got)
	}
	if got := minimizedField("  a   b  ", 512); got != "a b" {
		t.Fatalf("whitespace was not collapsed: %q", got)
	}
	if !utf8.ValidString(minimizedField("正常中文", 512)) {
		t.Fatal("valid Chinese text was rejected")
	}
}
