package compare

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"contractaudit/internal/engine"
)

func TestBundledGoldRegression(t *testing.T) {
	data, err := DefaultDataset()
	if err != nil {
		t.Fatal(err)
	}
	report, err := Run(data, nil)
	if err != nil || !report.Passed {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if report.Overall.Scenarios != 62 || len(report.Domains) != 5 || report.Overall.GoldEvidence != 84 || report.Overall.VerifiedAnchors != 84 {
		t.Fatalf("gold support changed; review corpus version and acceptance contract: %+v", report.Overall)
	}
	if report.Overall.Accuracy != 1 || report.Overall.MacroF1 != 1 || report.Overall.DecisionCoverage != 21.0/62.0 || report.Overall.FalseAnchors != 0 {
		t.Fatalf("metrics %+v", report.Overall)
	}
	for _, domain := range report.Domains {
		if domain.MacroClassCount != 4 || len(domain.Classes) != 4 || domain.Scenarios < 10 {
			t.Fatalf("domain lacks expected heterogeneous label support: %+v", domain)
		}
	}
	again, err := Run(data, nil)
	if err != nil || !reflect.DeepEqual(report, again) {
		t.Fatal("evaluation must be deterministic")
	}
	data.Scenarios[0].Expected = engine.RelCompatible
	fresh, err := DefaultDataset()
	if err != nil || fresh.Scenarios[0].Expected != engine.RelConflict {
		t.Fatal("caller mutated embedded gold data")
	}
}

func tinyDataset() Dataset {
	return Dataset{SchemaVersion: SchemaVersion, ID: "test-fixture", Kind: "synthetic", Scenarios: []Scenario{
		{ID: "conflict", Domain: "warranty", Expected: engine.RelConflict,
			Documents: []Document{{ID: "a", ParseStatus: "ready", Text: "质保36个月。"}, {ID: "b", ParseStatus: "ready", Text: "质保24个月。"}},
			Evidence:  []Evidence{{DocumentID: "a", Quote: "质保36个月"}}},
		{ID: "missing", Domain: "warranty", Expected: engine.RelInsufficient},
	}}
}

func TestWrongPredictionsCannotHideMissingEvidence(t *testing.T) {
	r, err := Run(tinyDataset(), func(domain string, docs []engine.Source) (engine.Evaluation, error) {
		return engine.Evaluation{Domain: domain, Relation: engine.RelCompatible}, nil
	})
	if err != nil || r.Passed || r.Overall.Accuracy != 0 || r.Overall.DecisionCoverage != 1 || r.Overall.MacroF1 != 0 || r.Overall.MacroClassCount != 3 {
		t.Fatalf("false agreement metrics: %+v %v", r, err)
	}
	if r.Overall.CitationVerification != nil || r.Overall.EvidenceRecall == nil || *r.Overall.EvidenceRecall != 0 || len(r.Cases[0].MissingEvidence) != 1 {
		t.Fatalf("empty citations cannot be perfect: %+v", r.Overall)
	}
	if r.Comparator != "custom-evaluator" || r.RuleSetHash != "" {
		t.Fatal("custom evaluator was attributed to the default comparator")
	}
}

func TestMetricsUseExplicitDenominators(t *testing.T) {
	m := summarize([]CaseResult{
		{Expected: engine.RelConflict, Actual: engine.RelConflict, Correct: true, LocatedAnchors: 2, VerifiedAnchors: 1, GoldEvidence: 2, MatchedGold: 1},
		{Expected: engine.RelConflict, Actual: engine.RelCompatible},
		{Expected: engine.RelCompatible, Actual: engine.RelCompatible, Correct: true},
	})
	if m.Accuracy != 2.0/3.0 || m.MacroF1 != 2.0/3.0 || m.MacroClassCount != 2 || m.CitationVerification == nil || *m.CitationVerification != 0.5 || m.EvidenceRecall == nil || *m.EvidenceRecall != 0.5 {
		t.Fatalf("incorrect denominator or macro aggregation: %+v", m)
	}
	empty := summarize(nil)
	if empty.Accuracy != 0 || empty.MacroF1 != 0 || empty.CitationVerification != nil || empty.EvidenceRecall != nil {
		t.Fatalf("empty metrics fabricated rate: %+v", empty)
	}
}

func TestFalseCoordinatesAndFactOwnershipFailGate(t *testing.T) {
	data := tinyDataset()
	data.Scenarios = data.Scenarios[:1]
	page := 999
	for _, bad := range []engine.Anchor{
		{DocumentID: "a", Quote: "质保99个月", Status: "located", Start: 0, End: 6},
		{DocumentID: "a", Quote: "质保36个月", Status: "located", Start: 0, End: 6, Page: &page},
		{DocumentID: "a", Quote: "质保36个月", Status: "located", Start: 0, End: 6, ElementID: "invented"},
		{DocumentID: "missing", Quote: "质保36个月", Status: "located", Start: 0, End: 6},
		{DocumentID: "a", Status: "unlocatable", Start: 1, End: 2},
	} {
		r, err := Run(data, func(domain string, docs []engine.Source) (engine.Evaluation, error) {
			return engine.Evaluation{Domain: domain, Relation: engine.RelConflict, Missing: []engine.Anchor{bad}, Facts: []engine.Fact{{DocumentID: "different-document", Anchor: bad}}}, nil
		})
		if err != nil || r.Passed || r.Overall.FalseAnchors == 0 {
			t.Fatalf("bad citation passed: %+v %v", r, err)
		}
	}
}

func TestEvaluatorErrorsAreNotScoredAsSuccess(t *testing.T) {
	for _, evaluator := range []Evaluator{
		func(string, []engine.Source) (engine.Evaluation, error) {
			return engine.Evaluation{}, errors.New("offline failure")
		},
		func(string, []engine.Source) (engine.Evaluation, error) {
			return engine.Evaluation{Domain: "unknown", Relation: engine.RelConflict}, nil
		},
		func(string, []engine.Source) (engine.Evaluation, error) {
			return engine.Evaluation{Domain: "warranty", Relation: "COMPLIANT"}, nil
		},
	} {
		if _, err := Run(tinyDataset(), evaluator); err == nil {
			t.Fatal("invalid evaluation accepted")
		}
	}
	if _, err := Rules("unknown", nil); err == nil {
		t.Fatal("unknown rule accepted")
	}
	if _, err := Run(Dataset{}, nil); err == nil {
		t.Fatal("invalid dataset accepted")
	}
}

func TestDatasetValidation(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Dataset)
	}{
		{"schema", func(d *Dataset) { d.SchemaVersion = "v0" }},
		{"id", func(d *Dataset) { d.ID = " " }},
		{"kind", func(d *Dataset) { d.Kind = "unknown" }},
		{"empty", func(d *Dataset) { d.Scenarios = nil }},
		{"scenario-duplicate", func(d *Dataset) { d.Scenarios = append(d.Scenarios, d.Scenarios[0]) }},
		{"scenario-id", func(d *Dataset) { d.Scenarios[0].ID = "" }},
		{"domain", func(d *Dataset) { d.Scenarios[0].Domain = "unknown" }},
		{"label", func(d *Dataset) { d.Scenarios[0].Expected = "safe" }},
		{"decisive-without-gold", func(d *Dataset) { d.Scenarios[0].Evidence = nil }},
		{"document-id", func(d *Dataset) { d.Scenarios[0].Documents[0].ID = "" }},
		{"document-duplicate", func(d *Dataset) {
			d.Scenarios[0].Documents = append(d.Scenarios[0].Documents, d.Scenarios[0].Documents[0])
		}},
		{"status", func(d *Dataset) { d.Scenarios[0].Documents[0].ParseStatus = "made-up" }},
		{"gold-document", func(d *Dataset) { d.Scenarios[0].Evidence[0].DocumentID = "missing" }},
		{"gold-text", func(d *Dataset) { d.Scenarios[0].Evidence[0].Quote = "质保99个月" }},
		{"gold-failed", func(d *Dataset) { d.Scenarios[0].Documents[0].ParseStatus = "failed" }},
		{"gold-duplicate", func(d *Dataset) {
			d.Scenarios[0].Evidence = append(d.Scenarios[0].Evidence, d.Scenarios[0].Evidence[0])
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := tinyDataset()
			tc.edit(&data)
			if err := data.Validate(); err == nil {
				t.Fatal("invalid gold accepted")
			}
		})
	}
	good := tinyDataset()
	good.Kind = "human_annotated"
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestStrictJSONLoading(t *testing.T) {
	valid, err := json.Marshal(tinyDataset())
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"{", string(valid) + " {}", string(valid) + " invalid", strings.Replace(string(valid), `"id":`, `"unknown":`, 1),
		`{"schema_version":"wrong"}`, strings.Repeat(" ", maxDatasetBytes+1),
	} {
		if _, err := Load(strings.NewReader(raw)); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, err := Load(brokenReader{}); err == nil {
		t.Fatal("reader error ignored")
	}
	if _, err := Load(bytes.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
}
