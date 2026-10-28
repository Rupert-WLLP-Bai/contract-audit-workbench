package store

import (
	"strings"
	"testing"

	"contractaudit/internal/advisory"
	"contractaudit/internal/jev"
)

func judgmentDigest(seed string) string {
	return strings.Repeat(seed, 64/len(seed))
}

func TestAdvisoryJudgmentIDIsStableAndAttemptIndependent(t *testing.T) {
	const runID, feature = "run-a", "knowledge_rerank"
	input, snapshot, request := judgmentDigest("a"), judgmentDigest("b"), judgmentDigest("c")
	first, err := advisoryJudgmentID(runID, feature, input, snapshot, request)
	if err != nil || len(first) != 64 {
		t.Fatalf("judgment id = %q, %v", first, err)
	}
	second, err := advisoryJudgmentID(runID, feature, input, snapshot, request)
	if err != nil || second != first {
		t.Fatalf("judgment id is not stable: %s vs %s (%v)", first, second, err)
	}
	for _, other := range [][3]string{
		{"run-b", feature, input},
		{runID, "other_feature", input},
		{runID, feature, judgmentDigest("d")},
	} {
		changed, err := advisoryJudgmentID(other[0], other[1], input, snapshot, other[2])
		if err != nil || changed == first {
			t.Fatalf("judgment key did not change for %+v", other)
		}
	}
}

func TestAdvisoryJudgmentIDRequiresCompleteDigests(t *testing.T) {
	for _, test := range [][3]string{
		{"", judgmentDigest("b"), judgmentDigest("c")},
		{"short", judgmentDigest("b"), judgmentDigest("c")},
		{judgmentDigest("a"), "not-a-digest", judgmentDigest("c")},
	} {
		if _, err := advisoryJudgmentID("run-a", "knowledge_rerank", test[0], test[1], test[2]); err == nil {
			t.Fatalf("incomplete judgment key %+v was accepted", test)
		}
	}
}

func TestAdvisoryResponseRoundTripRecomputesCodeOwnedBands(t *testing.T) {
	probabilityHigh, probabilityLow, probabilityMiddle := 0.82, 0.12, 0.50
	result := advisory.KnowledgeResult{
		Model: "jev-1.14.0",
		Probabilities: map[string]float64{
			"candidate_0": probabilityHigh, "candidate_1": probabilityLow, "candidate_2": probabilityMiddle,
		},
		Usage: jev.Usage{InputTokens: nil, OutputTokens: nil},
	}
	responseJSON, usageJSON, err := encodeAdvisoryResponse(result)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := decodeAdvisoryResponse(responseJSON)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Model != result.Model || len(decoded.Probabilities) != 3 ||
		decoded.Bands["candidate_0"] != advisory.BandRelevant ||
		decoded.Bands["candidate_1"] != advisory.BandNotRelevant ||
		decoded.Bands["candidate_2"] != advisory.BandUncertain {
		t.Fatalf("decoded = %+v", decoded)
	}
	if len(usageJSON) == 0 {
		t.Fatal("usage JSON is empty")
	}

	invalid := []byte(`{"model":"jev-1.14.0","probabilities":{"candidate_0":1.2}}`)
	if _, err := decodeAdvisoryResponse(invalid); err == nil {
		t.Fatal("out-of-range persisted probability was accepted")
	}
	empty := []byte(`{"model":"jev-1.14.0","probabilities":{}}`)
	if _, err := decodeAdvisoryResponse(empty); err == nil {
		t.Fatal("empty persisted judgment was accepted")
	}
}
