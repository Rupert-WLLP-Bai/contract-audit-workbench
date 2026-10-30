// Package compare provides an offline regression evaluator. Its bundled corpus
// is synthetic; the resulting scores are not estimates of production accuracy.
package compare

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"contractaudit/internal/docir"
	"contractaudit/internal/engine"
)

const SchemaVersion = "contract-audit-evaluation-v1"
const maxDatasetBytes = 8 << 20

//go:embed testdata/golden-v1.json
var bundled []byte

type Document struct {
	ID          string `json:"id"`
	Role        string `json:"role"`
	ParseStatus string `json:"parse_status"`
	Text        string `json:"text"`
	FromOCR     bool   `json:"from_ocr,omitempty"`
}

type Evidence struct {
	DocumentID string `json:"document_id"`
	Quote      string `json:"quote"`
}

type Scenario struct {
	ID        string     `json:"id"`
	Domain    string     `json:"domain"`
	Tags      []string   `json:"tags"`
	Documents []Document `json:"documents"`
	Expected  string     `json:"expected_relation"`
	Evidence  []Evidence `json:"expected_evidence"`
}

type Dataset struct {
	SchemaVersion string     `json:"schema_version"`
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	Description   string     `json:"description"`
	Scenarios     []Scenario `json:"scenarios"`
}

// DefaultDataset returns a fresh copy; callers cannot mutate the embedded set.
func DefaultDataset() (Dataset, error) { return Load(bytes.NewReader(bundled)) }

// Load rejects unknown fields, concatenated JSON, oversized input and invalid
// gold labels. Incorrect evaluation input must not silently improve a score.
func Load(r io.Reader) (Dataset, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxDatasetBytes+1))
	if err != nil {
		return Dataset{}, err
	}
	if len(raw) > maxDatasetBytes {
		return Dataset{}, fmt.Errorf("evaluation dataset exceeds %d bytes", maxDatasetBytes)
	}
	var data Dataset
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&data); err != nil {
		return Dataset{}, fmt.Errorf("evaluation JSON: %w", err)
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return Dataset{}, fmt.Errorf("evaluation JSON must contain exactly one object")
	}
	if err := data.Validate(); err != nil {
		return Dataset{}, err
	}
	return data, nil
}

var labels = []string{engine.RelCompatible, engine.RelConflict, engine.RelInsufficient, engine.RelNotApplicable}

func validRelation(v string) bool { return slices.Contains(labels, v) }

var domainRules = map[string]func([]engine.Source) engine.Evaluation{
	"warranty":          engine.Evaluate,
	"payment_sum":       engine.EvaluatePaymentSum,
	"payment_condition": engine.EvaluatePaymentCondition,
	"schedule":          engine.EvaluateSchedule,
	"amount":            engine.EvaluateAmount,
}

func supported(domain string) bool { _, ok := domainRules[domain]; return ok }

func (d Dataset) Validate() error {
	if d.SchemaVersion != SchemaVersion || strings.TrimSpace(d.ID) == "" || len(d.Scenarios) == 0 {
		return fmt.Errorf("dataset requires schema_version %q, id and nonempty scenarios", SchemaVersion)
	}
	if d.Kind != "synthetic" && d.Kind != "human_annotated" {
		return fmt.Errorf("dataset kind must be synthetic or human_annotated")
	}
	ids := map[string]bool{}
	for _, scenario := range d.Scenarios {
		if err := validateScenario(scenario, ids); err != nil {
			return err
		}
		ids[scenario.ID] = true
	}
	return nil
}

func validateScenario(s Scenario, ids map[string]bool) error {
	if strings.TrimSpace(s.ID) == "" || ids[s.ID] || !supported(s.Domain) || !validRelation(s.Expected) {
		return fmt.Errorf("scenario %q: duplicate/empty id, unknown domain or invalid relation", s.ID)
	}
	if (s.Expected == engine.RelCompatible || s.Expected == engine.RelConflict) && len(s.Evidence) == 0 {
		return fmt.Errorf("scenario %q: a decisive gold label requires expected evidence", s.ID)
	}
	docs, err := scenarioDocuments(s)
	if err != nil {
		return err
	}
	return validateGoldEvidence(s, docs)
}

func scenarioDocuments(s Scenario) (map[string]Document, error) {
	docs := map[string]Document{}
	for _, doc := range s.Documents {
		if strings.TrimSpace(doc.ID) == "" {
			return nil, fmt.Errorf("scenario %q: empty document id", s.ID)
		}
		if _, ok := docs[doc.ID]; ok {
			return nil, fmt.Errorf("scenario %q: duplicate document %q", s.ID, doc.ID)
		}
		if doc.ParseStatus != "ready" && doc.ParseStatus != "failed" && doc.ParseStatus != "pending" {
			return nil, fmt.Errorf("scenario %q: document %q has invalid parse_status", s.ID, doc.ID)
		}
		docs[doc.ID] = doc
	}
	return docs, nil
}

func validateGoldEvidence(s Scenario, docs map[string]Document) error {
	seen := map[string]bool{}
	for _, gold := range s.Evidence {
		doc, ok := docs[gold.DocumentID]
		key := gold.DocumentID + "\x00" + gold.Quote
		if !ok || doc.ParseStatus != "ready" || seen[key] || docir.Locate(docir.Build(doc.Text), gold.Quote).Status != docir.StatusLocated {
			return fmt.Errorf("scenario %q: expected evidence is duplicate or not grounded in a ready document", s.ID)
		}
		seen[key] = true
	}
	return nil
}

type ClassMetric struct {
	Label     string  `json:"label"`
	Support   int     `json:"support"`
	Predicted int     `json:"predicted"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
}

// Rates use the explicitly reported counts. A nil evidence rate is JSON null,
// meaning no denominator; zero citations must never become 100% precision.
type Metrics struct {
	Scenarios            int                       `json:"scenarios"`
	Correct              int                       `json:"correct"`
	Accuracy             float64                   `json:"accuracy"`
	Confusion            map[string]map[string]int `json:"confusion_expected_to_predicted"`
	Classes              []ClassMetric             `json:"classes"`
	MacroF1              float64                   `json:"macro_f1_observed_classes"`
	MacroClassCount      int                       `json:"macro_class_count"`
	Decisive             int                       `json:"decisive_compatible_or_conflict"`
	DecisionCoverage     float64                   `json:"decision_coverage"`
	Insufficient         int                       `json:"insufficient"`
	InsufficientRate     float64                   `json:"insufficient_rate"`
	NotApplicable        int                       `json:"not_applicable"`
	LocatedAnchors       int                       `json:"located_anchors"`
	VerifiedAnchors      int                       `json:"verified_anchors"`
	FalseAnchors         int                       `json:"false_anchors"`
	CitationVerification *float64                  `json:"citation_verification_rate"`
	GoldEvidence         int                       `json:"gold_evidence"`
	MatchedGold          int                       `json:"matched_gold_evidence"`
	EvidenceRecall       *float64                  `json:"gold_evidence_recall"`
}

type CaseResult struct {
	ID              string     `json:"id"`
	Domain          string     `json:"domain"`
	Expected        string     `json:"expected_relation"`
	Actual          string     `json:"actual_relation"`
	Correct         bool       `json:"correct"`
	LocatedAnchors  int        `json:"located_anchors"`
	VerifiedAnchors int        `json:"verified_anchors"`
	GoldEvidence    int        `json:"gold_evidence"`
	MatchedGold     int        `json:"matched_gold_evidence"`
	FalseAnchors    []string   `json:"false_anchors"`
	MissingEvidence []Evidence `json:"missing_expected_evidence"`
}

type DomainMetric struct {
	Domain string `json:"domain"`
	Metrics
}

type Report struct {
	SchemaVersion string         `json:"schema_version"`
	DatasetID     string         `json:"dataset_id"`
	DatasetKind   string         `json:"dataset_kind"`
	RuleSetHash   string         `json:"rule_set_hash"`
	Comparator    string         `json:"comparator"`
	Limitations   string         `json:"limitations"`
	Passed        bool           `json:"passed"`
	Overall       Metrics        `json:"overall"`
	Domains       []DomainMetric `json:"domains"`
	Cases         []CaseResult   `json:"cases"`
}

type Evaluator func(domain string, documents []engine.Source) (engine.Evaluation, error)

func Rules(domain string, documents []engine.Source) (engine.Evaluation, error) {
	rule, ok := domainRules[domain]
	if !ok {
		return engine.Evaluation{}, fmt.Errorf("unknown evaluation domain %q", domain)
	}
	return rule(documents), nil
}

// Run measures predictions against gold without changing either. A caller can
// supply an alternative evaluator for ablations; it must return grounded spans.
func Run(data Dataset, evaluator Evaluator) (Report, error) {
	if err := data.Validate(); err != nil {
		return Report{}, err
	}
	ruleSetHash, comparator := "", "custom-evaluator"
	if evaluator == nil {
		evaluator = Rules
		ruleSetHash, comparator = engine.RuleSetHash(), engine.ComparatorRef
	}
	report := Report{SchemaVersion: SchemaVersion, DatasetID: data.ID, DatasetKind: data.Kind,
		RuleSetHash: ruleSetHash, Comparator: comparator, Passed: true,
		Limitations: "Regression corpus only; not production legal accuracy. Macro-F1 averages labels with gold or predicted support. Exact citation verification checks source consistency, not legal sufficiency. No train/test split or paper reproduction is claimed."}
	byDomain := map[string][]CaseResult{}
	for _, scenario := range data.Scenarios {
		result, err := evaluateScenario(scenario, evaluator)
		if err != nil {
			return Report{}, err
		}
		report.Cases = append(report.Cases, result)
		byDomain[scenario.Domain] = append(byDomain[scenario.Domain], result)
		if !result.Correct || len(result.FalseAnchors) > 0 || len(result.MissingEvidence) > 0 {
			report.Passed = false
		}
	}
	report.Overall = summarize(report.Cases)
	report.Domains = summarizeDomains(byDomain)
	return report, nil
}

func evaluateScenario(scenario Scenario, evaluator Evaluator) (CaseResult, error) {
	sources := make([]engine.Source, 0, len(scenario.Documents))
	for _, d := range scenario.Documents {
		sources = append(sources, engine.Source{ID: d.ID, Role: d.Role, ParseStatus: d.ParseStatus, Text: d.Text, FromOCR: d.FromOCR})
	}
	evaluation, err := evaluator(scenario.Domain, sources)
	if err != nil {
		return CaseResult{}, fmt.Errorf("scenario %q: %w", scenario.ID, err)
	}
	if evaluation.Domain != scenario.Domain || !validRelation(evaluation.Relation) {
		return CaseResult{}, fmt.Errorf("scenario %q: evaluator returned invalid domain or relation", scenario.ID)
	}
	return score(scenario, evaluation), nil
}

func summarizeDomains(byDomain map[string][]CaseResult) []DomainMetric {
	var result []DomainMetric
	var domains []string
	for domain := range byDomain {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	for _, domain := range domains {
		result = append(result, DomainMetric{Domain: domain, Metrics: summarize(byDomain[domain])})
	}
	return result
}

func score(s Scenario, ev engine.Evaluation) CaseResult {
	r := CaseResult{ID: s.ID, Domain: s.Domain, Expected: s.Expected, Actual: ev.Relation,
		Correct: s.Expected == ev.Relation, GoldEvidence: len(s.Evidence), FalseAnchors: []string{}, MissingEvidence: []Evidence{}}
	verified := r.verifyEvidence(s.Documents, ev)
	for _, gold := range s.Evidence {
		if verified[gold.DocumentID+"\x00"+gold.Quote] {
			r.MatchedGold++
		} else {
			r.MissingEvidence = append(r.MissingEvidence, gold)
		}
	}
	return r
}

func (r *CaseResult) verifyEvidence(documents []Document, ev engine.Evaluation) map[string]bool {
	docs := map[string]Document{}
	for _, d := range documents {
		docs[d.ID] = d
	}
	anchors := append([]engine.Anchor(nil), ev.Missing...)
	anchors = append(anchors, ev.Uncertain...)
	for _, fact := range ev.Facts {
		anchors = append(anchors, fact.Anchor)
		if fact.DocumentID != fact.Anchor.DocumentID {
			r.FalseAnchors = append(r.FalseAnchors, "fact document does not match anchor document")
		}
	}
	verified := map[string]bool{}
	for _, anchor := range anchors {
		if r.verifyAnchor(anchor, docs) {
			verified[anchor.DocumentID+"\x00"+anchor.Quote] = true
		}
	}
	return verified
}

func (r *CaseResult) verifyAnchor(a engine.Anchor, docs map[string]Document) bool {
	if a.Status != docir.StatusLocated {
		if a.Start != 0 || a.End != 0 || a.Page != nil || a.ElementID != "" {
			r.FalseAnchors = append(r.FalseAnchors, a.DocumentID+": unlocated evidence has coordinates")
		}
		return false
	}
	r.LocatedAnchors++
	doc, exists := docs[a.DocumentID]
	if !exists || !matchesSourceAnchor(doc, a) {
		r.FalseAnchors = append(r.FalseAnchors, a.DocumentID+": citation does not match source coordinates")
		return false
	}
	r.VerifiedAnchors++
	return true
}

func matchesSourceAnchor(doc Document, a engine.Anchor) bool {
	ir := docir.Build(doc.Text)
	quote, ok := docir.Slice(ir.NormalizedText, a.Start, a.End)
	pageOK := true
	if a.Page != nil {
		page, found := ir.PageAt(a.Start, a.End)
		pageOK = found && page == *a.Page
	}
	elementOK := a.ElementID == "" || ir.ElementAt(a.Start, a.End) == a.ElementID
	return doc.ParseStatus == "ready" && ok && quote == a.Quote && pageOK && elementOK
}

func summarize(cases []CaseResult) Metrics {
	m := Metrics{Scenarios: len(cases), Confusion: map[string]map[string]int{}}
	for _, label := range labels {
		m.Confusion[label] = map[string]int{}
		for _, predicted := range labels {
			m.Confusion[label][predicted] = 0
		}
	}
	for _, c := range cases {
		m.accumulate(c)
	}
	for _, label := range labels {
		class := ClassMetric{Label: label}
		for _, other := range labels {
			class.Support += m.Confusion[label][other]
			class.Predicted += m.Confusion[other][label]
		}
		tp := m.Confusion[label][label]
		class.Precision = ratio(tp, class.Predicted)
		class.Recall = ratio(tp, class.Support)
		class.F1 = ratio(2*tp, class.Predicted+class.Support)
		m.Classes = append(m.Classes, class)
		if class.Support+class.Predicted > 0 {
			m.MacroF1 += class.F1
			m.MacroClassCount++
		}
	}
	if m.MacroClassCount > 0 {
		m.MacroF1 /= float64(m.MacroClassCount)
	}
	m.Accuracy = ratio(m.Correct, m.Scenarios)
	m.DecisionCoverage = ratio(m.Decisive, m.Scenarios)
	m.InsufficientRate = ratio(m.Insufficient, m.Scenarios)
	m.CitationVerification = optionalRatio(m.VerifiedAnchors, m.LocatedAnchors)
	m.EvidenceRecall = optionalRatio(m.MatchedGold, m.GoldEvidence)
	return m
}

func (m *Metrics) accumulate(c CaseResult) {
	m.Confusion[c.Expected][c.Actual]++
	if c.Correct {
		m.Correct++
	}
	switch c.Actual {
	case engine.RelCompatible, engine.RelConflict:
		m.Decisive++
	case engine.RelInsufficient:
		m.Insufficient++
	case engine.RelNotApplicable:
		m.NotApplicable++
	}
	m.LocatedAnchors += c.LocatedAnchors
	m.VerifiedAnchors += c.VerifiedAnchors
	m.FalseAnchors += len(c.FalseAnchors)
	m.GoldEvidence += c.GoldEvidence
	m.MatchedGold += c.MatchedGold
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func optionalRatio(n, d int) *float64 {
	if d == 0 {
		return nil
	}
	f := ratio(n, d)
	return &f
}
