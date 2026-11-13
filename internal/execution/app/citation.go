package app

import "contractaudit/internal/engine"

// CitationChange is applied in order to the same locked state used to plan it.
// Insert and Update are mutually exclusive; Update targets a finding by ID.
type CitationChange struct {
	Insert *Finding
	Update *CitationUpdate
}
type CitationUpdate struct {
	FindingID                               string
	DispositionSuggestion, UnverifiedReason string
	AppendAnchors                           []Anchor
}
type CitationDelta struct {
	Changes []CitationChange
	Added   []Finding
}

// PlanCitationMerge preserves first-match and anchor deduplication semantics.
// Candidates must be the ordered, fresh findings from the locked attempt state.
func PlanCitationMerge(attempt FrozenAttempt, docs []Document, candidates []Finding, drafts []engine.Draft, ids IDs, clock Clock) (CitationDelta, error) {
	view := make([]Finding, len(candidates))
	copy(view, candidates)
	for i := range view {
		view[i].Anchors = append([]Anchor(nil), view[i].Anchors...)
	}
	var result CitationDelta
	for _, draft := range drafts {
		idx := -1
		for i, f := range view {
			if f.AttemptID == attempt.AttemptID && f.Origin == "rule" && f.Domain == draft.Domain && f.Relation == draft.Relation && f.Claim == draft.Claim {
				idx = i
				break
			}
		}
		if idx < 0 {
			made, err := materialize(attempt, clock.Stamp(), []engine.Draft{draft}, docs, ids)
			if err != nil {
				return CitationDelta{}, err
			}
			f := made[0]
			view = append(view, f)
			result.Added = append(result.Added, f)
			result.Changes = append(result.Changes, CitationChange{Insert: &f})
			continue
		}
		f := &view[idx]
		f.DispositionSuggestion = draft.Disposition
		f.UnverifiedReason = draft.UnverifiedReason
		update := CitationUpdate{FindingID: f.ID, DispositionSuggestion: draft.Disposition, UnverifiedReason: draft.UnverifiedReason}
		for _, anchor := range draft.Anchors {
			parsed, _ := parsedIdentity(docs, anchor.DocumentID)
			found := false
			for _, existing := range f.Anchors {
				if existing.DocumentID == anchor.DocumentID && existing.ParsedDocumentID == parsed && existing.Quote == anchor.Quote && existing.Status == anchor.Status && existing.Start == anchor.Start && existing.End == anchor.End {
					found = true
					break
				}
			}
			if found {
				continue
			}
			made, err := anchorFrom(anchor, docs, ids)
			if err != nil {
				return CitationDelta{}, err
			}
			f.Anchors = append(f.Anchors, made)
			update.AppendAnchors = append(update.AppendAnchors, made)
		}
		result.Changes = append(result.Changes, CitationChange{Update: &update})
	}
	return result, nil
}
