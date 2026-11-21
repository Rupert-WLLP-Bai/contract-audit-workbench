// File metadata (manifest mirror):
// Purpose: Core and document append-only projections preserve a single authoritative writer per table. Readers merge them with the unchanged historical execution rows. Related `store` operations include applyReadProjections.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-25
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

// applyReadProjections derives human versions and material freshness without a
// second service updating the authoritative Finding or Case row.
func applyReadProjections(st *state) {
	versions := make(map[string]int, len(st.Decisions))
	updated := make(map[string]string, len(st.Cases))
	for _, decision := range st.Decisions {
		versions[decision.FindingID]++
		if decision.CreatedAt > updated[decision.CaseID] {
			updated[decision.CaseID] = decision.CreatedAt
		}
	}
	for i := range st.Findings {
		version := versions[st.Findings[i].ID] + 1
		if version > st.Findings[i].Version {
			st.Findings[i].Version = version
		}
	}
	for _, doc := range st.Documents {
		if doc.CreatedAt > updated[doc.CaseID] {
			updated[doc.CaseID] = doc.CreatedAt
		}
	}
	// Parsed artifacts and generated drafts carry no case id of their own, so
	// they are attributed through the document they belong to. Without these two
	// loops a reparse or draft generation would leave Case.UpdatedAt stale, which
	// is why the document role used to write the core case row directly.
	docCases := make(map[string]string, len(st.Documents))
	for _, doc := range st.Documents {
		docCases[doc.ID] = doc.CaseID
	}
	for _, parsed := range st.ParsedDocuments {
		if caseID := docCases[parsed.DocumentID]; caseID != "" && parsed.CreatedAt > updated[caseID] {
			updated[caseID] = parsed.CreatedAt
		}
	}
	for _, generation := range st.Generations {
		if generation.CaseID != "" && generation.Record.CreatedAt > updated[generation.CaseID] {
			updated[generation.CaseID] = generation.Record.CreatedAt
		}
	}
	for _, activity := range st.Activity {
		if activity.At > updated[activity.CaseID] {
			updated[activity.CaseID] = activity.At
		}
	}
	for _, run := range st.Runs {
		if run.UpdatedAt > updated[run.CaseID] {
			updated[run.CaseID] = run.UpdatedAt
		}
	}
	for i := range st.Cases {
		if updated[st.Cases[i].ID] > st.Cases[i].UpdatedAt {
			st.Cases[i].UpdatedAt = updated[st.Cases[i].ID]
		}
	}
}
