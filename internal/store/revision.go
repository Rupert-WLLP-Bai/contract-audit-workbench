package store

import (
	"strconv"

	"contractaudit/internal/engine"
	"contractaudit/internal/revise"
)

func revisionPending(st *state, caseID string) bool {
	run, ok := st.latestRun(caseID)
	if !ok {
		return false
	}
	documents := currentDocuments(st.documents(caseID))
	current := documentIDs(documents)
	snapshot := st.snapshotOf(run.ID)
	if !sameDocumentSet(snapshot.DocumentIDs, current) {
		return true
	}
	if attempt, ok := st.latestAttempt(run.ID); ok && attempt.HistoryKind == "preparing" {
		for _, selection := range st.AttemptParseSelections {
			if selection.AttemptID != attempt.ID {
				continue
			}
			if parsed, exists := st.latestParsed(selection.DocumentID, ""); exists && parsed.SelectionOrder > selection.SelectionOrder {
				return true
			}
		}
		return false
	}
	if len(snapshot.ParsedDocumentIDs) == 0 {
		for _, doc := range documents {
			if _, ok := st.latestParsed(doc.ID, ""); ok {
				return true
			}
		}
		return false
	}
	var parsedIDs []string
	for _, doc := range documents {
		if parsed, ok := st.latestParsed(doc.ID, ""); ok {
			parsedIDs = append(parsedIDs, parsed.ID)
		}
	}
	return !sameDocumentSet(snapshot.ParsedDocumentIDs, parsedIDs)
}

type boundConstraint struct {
	constraint revise.Constraint
	finding    Finding
}

func (s *Store) compareRun(st *state, run Run, current []Interpreted, selectedAttemptID ...string) (string, []Revision, []Interpreted, string) {
	prev, ok := st.previousRun(run)
	if !ok {
		return "", []Revision{}, nil, ""
	}
	previousSnapshot, currentSnapshot := st.snapshotOf(prev.ID), st.snapshotOf(run.ID)
	if !sameRunConfiguration(previousSnapshot, currentSnapshot) ||
		!sameRunConfiguration(currentSnapshot, withFrozenGovernance(s.comparisonSignature(st, run), currentSnapshot)) {
		return prev.ID, []Revision{}, nil, "两次审查的规则或模型配置与当前版本不一致，无法自动判断修改稿差异的原因；请查看两次记录并人工比较。"
	}
	currentAttemptID := ""
	if len(selectedAttemptID) > 0 {
		currentAttemptID = selectedAttemptID[0]
	}
	if currentAttemptID == "" {
		if attempt, ok := st.latestAttempt(run.ID); ok {
			currentAttemptID = attempt.ID
		}
	}
	previousAttemptID := ""
	if attempt, ok := st.latestAttempt(prev.ID); ok {
		previousAttemptID = attempt.ID
	}
	if !ruleResultsCommitted(st, previousAttemptID) || !ruleResultsCommitted(st, currentAttemptID) {
		return prev.ID, []Revision{}, nil, "两次执行尚未都保存完整的规则结果，无法判断修改稿的风险是否消除；请查看原始证据。"
	}
	prevDocs := st.documentsForAttempt(prev, previousAttemptID)
	previousFindings := st.findings(prev.ID)
	if previousAttemptID != "" {
		previousFindings = st.findingsForAttempt(prev.ID, previousAttemptID)
	}
	currentFindings := st.findings(run.ID)
	if currentAttemptID != "" {
		currentFindings = st.findingsForAttempt(run.ID, currentAttemptID)
	}
	previousAttempt, _ := st.attemptByID(previousAttemptID)
	currentAttempt, _ := st.attemptByID(currentAttemptID)
	if previousAttempt.RulePackCompilerRef != "" || currentAttempt.RulePackCompilerRef != "" {
		if previousAttempt.RulePackCompilerRef == "" || previousAttempt.RulePackCompilerRef != currentAttempt.RulePackCompilerRef ||
			previousAttempt.RulePackHash == "" || previousAttempt.RulePackHash != currentAttempt.RulePackHash ||
			previousAttempt.RulePackSnapshotDigest == "" || previousAttempt.RulePackSnapshotDigest != currentAttempt.RulePackSnapshotDigest {
			return prev.ID, []Revision{}, nil, "两次执行保存的规则包版本不一致或不完整，无法判断修改稿的风险变化；请查看原始证据。"
		}
		out := compareRulePackRevisions(st, previousAttempt, currentAttempt, previousFindings, currentFindings)
		return prev.ID, out, extraDocuments(current, prevDocs), ""
	}
	previous := bindConstraints(prevDocs, previousFindings)
	next := bindConstraints(current, currentFindings)
	classified := revise.Classify(constraintList(previous), constraintList(next))
	usedPrev := make([]bool, len(previous))
	usedNext := make([]bool, len(next))
	out := make([]Revision, 0, len(classified))
	for _, row := range classified {
		item := Revision{Mark: row.Mark, Anchors: []Anchor{}}
		if row.Current != nil {
			if found, ok := takeBound(next, usedNext, *row.Current); ok {
				item.FindingID = found.finding.ID
				item.Domain = found.finding.Domain
				item.Relation = found.finding.Relation
				item.Claim = found.finding.Claim
				item.Anchors = copyAnchors(found.finding.Anchors)
			}
		}
		if row.Previous != nil {
			if found, ok := takeBound(previous, usedPrev, *row.Previous); ok {
				item.PreviousFindingID = found.finding.ID
				if item.FindingID == "" {
					item.Domain = found.finding.Domain
					item.Relation = found.finding.Relation
					item.Claim = found.finding.Claim
					item.Anchors = copyAnchors(found.finding.Anchors)
				}
				if decisions := st.decisions(found.finding.ID); len(decisions) > 0 {
					last := decisions[len(decisions)-1]
					item.Prior = &last
				}
			}
		}
		out = append(out, item)
	}
	return prev.ID, out, extraDocuments(current, prevDocs), ""
}

// comparisonSignature rebuilds the live engine and model identity for one
// frozen run. Governed acceptance stores the pack marker in
// RulePackCompilerRef, while ComparatorRef still carries the case compiler.
// Comparing against a compiler-free signature would mark every governed
// revision incomparable even when the live compiler has not changed.
func (s *Store) comparisonSignature(st *state, run Run) RunSnapshot {
	if caseItem, ok := st.caseByID(run.CaseID); ok {
		return currentSignatureFor(nil, nil, s.currentModels(), telecomCompilerRefForCase(caseItem))
	}
	return s.currentSignature(nil)
}

func ruleResultsCommitted(st *state, attemptID string) bool {
	attempt, ok := st.attemptByID(attemptID)
	if !ok {
		return false
	}
	if attempt.RulePackCompilerRef != "" {
		if (attempt.State != "completed" && attempt.State != "partial") || attempt.OutputHash == nil || *attempt.OutputHash == "" {
			return false
		}
		// This receipt and the output hash commit atomically in both worker paths.
		// Historical attempts need not contain a newer Eino stage callback.
		for _, event := range st.ExecutionEvents {
			if event.AttemptID == attemptID && event.Phase == "terminal_persistence" &&
				event.EventType == "attempt_finished" && event.Status == attempt.State {
				return true
			}
		}
		return false
	}
	for _, event := range st.ExecutionEvents {
		if event.AttemptID == attemptID && event.Phase == "deterministic_rules" && event.EventType == "node_completed" && event.Status == "completed" {
			return true
		}
	}
	return false
}

func constraintList(items []boundConstraint) []revise.Constraint {
	out := make([]revise.Constraint, len(items))
	for i, item := range items {
		out[i] = item.constraint
	}
	return out
}

func bindConstraints(docs []Interpreted, findings []Finding) []boundConstraint {
	if len(findings) == 0 {
		return nil
	}
	roleOf := map[string]string{}
	for _, doc := range docs {
		roleOf[doc.Document.ID] = doc.Document.Role
	}
	sources := sourcesOf(docs)
	evals := []engine.Evaluation{engine.Evaluate(sources)}
	evals = append(evals, engine.CrossFile(sources)...)
	used := map[string]bool{}
	var out []boundConstraint
	for _, ev := range evals {
		if ev.Relation != engine.RelConflict && ev.Relation != engine.RelInsufficient {
			continue
		}
		finding, ok := unusedFinding(findings, ev.Domain, used)
		if !ok {
			continue
		}
		item := revise.Constraint{Domain: ev.Domain, RuleVersion: ev.RuleVersion, Relation: ev.Relation}
		for _, fact := range ev.Facts {
			if fact.Anchor.Status != "located" || fact.Anchor.Quote == "" {
				continue
			}
			item.Verified = append(item.Verified, revise.Verified{
				Role: roleOf[fact.DocumentID], Quote: fact.Anchor.Quote,
				Value: strconv.Itoa(fact.Value), Unit: fact.Unit,
			})
		}
		out = append(out, boundConstraint{constraint: item, finding: finding})
	}
	return out
}

func unusedFinding(findings []Finding, domain string, used map[string]bool) (Finding, bool) {
	for _, finding := range findings {
		if used[finding.ID] || finding.Domain != domain {
			continue
		}
		used[finding.ID] = true
		return finding, true
	}
	return Finding{}, false
}

func takeBound(items []boundConstraint, used []bool, want revise.Constraint) (boundConstraint, bool) {
	for i := range items {
		if used[i] || !items[i].constraint.Matches(want) {
			continue
		}
		used[i] = true
		return items[i], true
	}
	return boundConstraint{}, false
}

func copyAnchors(in []Anchor) []Anchor {
	if len(in) == 0 {
		return []Anchor{}
	}
	out := make([]Anchor, len(in))
	copy(out, in)
	return out
}

func extraDocuments(current, previous []Interpreted) []Interpreted {
	key := func(doc Interpreted) string {
		parsedID := ""
		if doc.ParsedDocument != nil {
			parsedID = doc.ParsedDocument.ID
		}
		return doc.Document.ID + "\x00" + parsedID
	}
	seen := map[string]bool{}
	for _, doc := range current {
		seen[key(doc)] = true
	}
	var out []Interpreted
	for _, doc := range previous {
		if seen[key(doc)] {
			continue
		}
		out = append(out, doc)
	}
	return out
}
