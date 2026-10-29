package agentflow

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestAdvisoryRecordRejectsInvalidEnvelopeFieldsIndependently(t *testing.T) {
	tests := map[string]func(*advisorySelectionFixture){
		"feature":       func(f *advisorySelectionFixture) { f.record.Advisory.Feature = "other" },
		"mode":          func(f *advisorySelectionFixture) { f.record.Advisory.Mode = "other" },
		"provider":      func(f *advisorySelectionFixture) { f.record.Advisory.Provider = "other" },
		"question set":  func(f *advisorySelectionFixture) { f.record.Advisory.QuestionSetVersion = "other" },
		"egress":        func(f *advisorySelectionFixture) { f.record.Advisory.Egress = "other" },
		"configuration": func(f *advisorySelectionFixture) { f.record.Advisory.ConfigurationID = "invalid" },
		"status": func(f *advisorySelectionFixture) {
			f.record.Advisory.Status = "other"
			f.record.Advisory.ModelObserved = ""
			f.record.Advisory.Candidates = []KnowledgeAdvisoryCandidate{}
			f.record.Candidates = nil
		},
		"missing candidates": func(f *advisorySelectionFixture) {
			f.record.Advisory.Status = "unavailable"
			f.record.Advisory.ModelObserved = ""
			f.record.Advisory.Candidates = nil
			f.record.Candidates = nil
		},
		"error code": func(f *advisorySelectionFixture) { f.record.Advisory.ErrorCode = "invalid code" },
		"model identity": func(f *advisorySelectionFixture) {
			f.record.Advisory.ModelRequested = "invalid model"
			f.record.Advisory.ModelObserved = "invalid model"
		},
		"request digest":  func(f *advisorySelectionFixture) { f.record.Advisory.RequestSHA256 = "invalid" },
		"judgment digest": func(f *advisorySelectionFixture) { f.record.Advisory.JudgmentID = "invalid" },
		"completed without judgments": func(f *advisorySelectionFixture) {
			f.record.Advisory.Candidates = []KnowledgeAdvisoryCandidate{}
			f.record.Candidates = nil
			f.request.Knowledge = nil
		},
		"unmatched judgment": func(f *advisorySelectionFixture) {
			f.record.Advisory.Status = "unavailable"
			f.record.Advisory.ModelObserved = ""
			f.record.Candidates = nil
			for i := range f.record.Advisory.Candidates {
				f.record.Advisory.Candidates[i].Probability = nil
				f.record.Advisory.Candidates[i].Band = "unknown"
			}
		},
	}
	for name, alter := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newAdvisorySelectionFixture()
			alter(&fixture)
			if err := validateKnowledgeAdvisory(fixture.record, fixture.request); !errors.Is(err, ErrInput) {
				t.Fatalf("invalid advisory record accepted: %v", err)
			}
		})
	}
}

func TestAdvisoryRecordCandidateCountAndRankBoundaries(t *testing.T) {
	for _, count := range []int{1, MaxAdvisoryMaxCandidates, MaxAdvisoryMaxCandidates + 1} {
		fixture := newAdvisorySelectionFixture()
		fixture.record.Candidates = []KnowledgeCandidate{}
		fixture.record.Advisory.Candidates = []KnowledgeAdvisoryCandidate{}
		fixture.request.Knowledge = nil
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("candidate-%02d", i)
			advice := KnowledgeAdvisoryCandidate{KnowledgeID: id, VersionID: "v1", ContentSHA256: TextHash(id),
				Alias: id, Probability: &fixture.probA, Band: "not_relevant", RankBefore: i + 1, RankAfter: i + 1}
			fixture.record.Advisory.Candidates = append(fixture.record.Advisory.Candidates, advice)
			fixture.record.Candidates = append(fixture.record.Candidates, KnowledgeCandidate{KnowledgeID: id,
				VersionID: "v1", ContentSHA256: advice.ContentSHA256, Kind: "guidance", Score: 1,
				Decision: "selected", Reason: "relevance", Advisory: &advice})
			fixture.request.Knowledge = append(fixture.request.Knowledge, KnowledgeReference{
				KnowledgeID: id, VersionID: "v1", ContentSHA256: advice.ContentSHA256})
		}
		err := validateKnowledgeAdvisory(fixture.record, fixture.request)
		if (err == nil) != (count <= MaxAdvisoryMaxCandidates) {
			t.Fatalf("candidate count %d: %v", count, err)
		}
	}
	for _, rank := range []int{-1, 0, 3} {
		for _, before := range []bool{true, false} {
			fixture := newAdvisorySelectionFixture()
			if before {
				fixture.record.Advisory.Candidates[0].RankBefore = rank
				fixture.record.Candidates[0].Advisory.RankBefore = rank
			} else {
				fixture.record.Advisory.Candidates[0].RankAfter = rank
				fixture.record.Candidates[0].Advisory.RankAfter = rank
			}
			if err := validateKnowledgeAdvisory(fixture.record, fixture.request); !errors.Is(err, ErrInput) {
				t.Fatalf("out-of-range rank %d (before=%v) accepted: %v", rank, before, err)
			}
		}
	}
	fixture := newAdvisorySelectionFixture()
	fixture.record.Advisory.Status, fixture.record.Advisory.ModelObserved = "unavailable", ""
	fixture.record.Candidates = []KnowledgeCandidate{}
	fixture.record.Advisory.Candidates = []KnowledgeAdvisoryCandidate{}
	fixture.request.Knowledge = nil
	if err := validateKnowledgeAdvisory(fixture.record, fixture.request); err != nil {
		t.Fatalf("unavailable empty batch rejected: %v", err)
	}
}

func TestAdvisoryRecordProbabilityBoundsAndAttachedValues(t *testing.T) {
	for _, value := range []float64{0, 1, -0.1, 1.1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		fixture := newAdvisorySelectionFixture()
		fixture.record.Advisory.Candidates[0].Probability = &value
		fixture.record.Candidates[0].Advisory.Probability = &value
		err := validateKnowledgeAdvisory(fixture.record, fixture.request)
		if value == 0 || value == 1 {
			if err != nil {
				t.Fatalf("probability endpoint %v rejected: %v", value, err)
			}
		} else if !errors.Is(err, ErrInput) {
			t.Fatalf("invalid probability %v accepted: %v", value, err)
		}
	}
	for _, missing := range []string{"attached", "record"} {
		fixture := newAdvisorySelectionFixture()
		if missing == "attached" {
			fixture.record.Candidates[0].Advisory.Probability = nil
		} else {
			fixture.record.Advisory.Candidates[0].Probability = nil
		}
		if err := validateKnowledgeAdvisory(fixture.record, fixture.request); !errors.Is(err, ErrInput) {
			t.Fatalf("mismatched %s probability accepted: %v", missing, err)
		}
	}
	fixture := newAdvisorySelectionFixture()
	fixture.record.Advisory.Status, fixture.record.Advisory.ModelObserved = "unavailable", ""
	for i := range fixture.record.Advisory.Candidates {
		fixture.record.Advisory.Candidates[i].Band = "unknown"
		fixture.record.Candidates[i].Advisory.Band = "unknown"
	}
	if err := validateKnowledgeAdvisory(fixture.record, fixture.request); !errors.Is(err, ErrInput) {
		t.Fatalf("failed batch carried judgments: %v", err)
	}
}

func TestAdvisoryRecordAcceptsMinimumPositiveScoreAndReasonBoundary(t *testing.T) {
	fixture := newAdvisorySelectionFixture()
	fixture.record.Candidates[0].Score = 1
	fixture.record.Advisory.Candidates[0].Reason = strings.Repeat("a", 64)
	if err := validateKnowledgeAdvisory(fixture.record, fixture.request); err != nil {
		t.Fatalf("valid score/reason boundary rejected: %v", err)
	}
	fixture.record.Advisory.Candidates[0].Reason += "a"
	if err := validateKnowledgeAdvisory(fixture.record, fixture.request); !errors.Is(err, ErrInput) {
		t.Fatalf("overlong reason accepted: %v", err)
	}
}
