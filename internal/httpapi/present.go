package httpapi

import (
	"contractaudit/internal/accounts"
	"contractaudit/internal/docir"
	"contractaudit/internal/store"
)

func summaryJSON(item store.CaseSummary) map[string]any {
	var latest any
	if item.Run != nil {
		latest = map[string]any{
			"id":         item.Run.ID,
			"state":      item.State,
			"updated_at": item.Run.UpdatedAt,
		}
	}
	return map[string]any{
		"id":                item.Case.ID,
		"title":             item.Case.Title,
		"contract_type":     item.Case.ContractType,
		"business_line":     item.Case.BusinessLine,
		"scenario":          item.Case.Scenario,
		"business_status":   item.Case.BusinessStatus,
		"archived_at":       item.Case.ArchivedAt,
		"document_count":    item.DocumentCount,
		"pending_decisions": item.Pending,
		"latest_run":        latest,
		"updated_at":        item.Case.UpdatedAt,
	}
}

func detailJSON(detail store.CaseDetail) map[string]any {
	body := summaryJSON(detail.CaseSummary)
	body["created_at"] = detail.Case.CreatedAt
	body["business_line"] = detail.BusinessLine
	body["scenario"] = detail.Scenario
	body["business_status"] = detail.BusinessStatus
	body["archived_at"] = detail.ArchivedAt
	if detail.Generations == nil {
		body["generations"] = []store.GenerationRecord{}
	} else {
		body["generations"] = detail.Generations
	}
	if detail.Remediations == nil {
		body["remediations"] = []store.Remediation{}
	} else {
		body["remediations"] = detail.Remediations
	}
	if detail.Reports == nil {
		body["reports"] = []store.ReportRecord{}
	} else {
		body["reports"] = detail.Reports
	}
	if detail.Archives == nil {
		body["archives"] = []store.ArchiveRecord{}
	} else {
		body["archives"] = detail.Archives
	}
	replaced := supersededIDs(detail.Documents)
	docs := make([]any, len(detail.Documents))
	for i, doc := range detail.Documents {
		docs[i] = documentJSON(doc, false, replaced[doc.Document.ID])
	}
	body["documents"] = docs
	body["revision_pending"] = detail.RevisionPending
	body["configuration_pending"] = detail.ConfigurationPending
	if detail.Counts == nil {
		body["finding_counts"] = nil
	} else {
		body["finding_counts"] = map[string]int{
			"total": detail.Counts.Total, "undecided": detail.Counts.Undecided,
			"conflict": detail.Counts.Conflict, "insufficient": detail.Counts.Insufficient,
		}
	}
	activity := make([]any, len(detail.Activities))
	for i, item := range detail.Activities {
		activity[i] = map[string]string{"id": item.ID, "at": item.At, "summary": item.Summary}
	}
	body["activity"] = activity
	members := make([]any, 0, len(detail.Case.MemberIDs))
	for _, id := range detail.Case.MemberIDs {
		if user, ok := accounts.ByID(id); ok {
			members = append(members, actorJSON(user))
		}
	}
	body["members"] = members
	return body
}

func supersededIDs(docs []store.Interpreted) map[string]bool {
	latest := map[string]string{}
	for _, doc := range docs {
		latest[doc.Document.Role] = doc.Document.ID
	}
	out := map[string]bool{}
	for _, doc := range docs {
		if latest[doc.Document.Role] != doc.Document.ID {
			out[doc.Document.ID] = true
		}
	}
	return out
}

func documentJSON(doc store.Interpreted, withText, superseded bool) map[string]any {
	status := "failed"
	var parseError any
	if doc.Ready {
		status = "ready"
		parseError = nil
	} else if doc.ParsePending {
		status = "pending"
	} else {
		parseError = doc.ParseError
	}
	out := map[string]any{
		"id": doc.Document.ID, "role": doc.Document.Role, "filename": doc.Document.Filename,
		"media_type": doc.Document.MediaType, "digest": doc.Document.Digest, "size": doc.Document.Size,
		"parse_status": status, "parse_error": parseError, "created_at": doc.Document.CreatedAt,
		"superseded": superseded, "parsed_document": parsedDocumentJSON(doc.ParsedDocument),
	}
	if doc.Ready {
		out["coordinate_space"] = doc.IR.CoordinateSpace
		out["normalized_text_digest"] = doc.IR.NormalizedDigest
	}
	if !withText {
		return out
	}
	if !doc.Ready {
		out["text"] = nil
		out["clauses"] = []any{}
		return out
	}
	out["text"] = doc.Text
	clauses := make([]any, len(doc.IR.Clauses))
	for i, clause := range doc.IR.Clauses {
		item := map[string]any{
			"id": clause.ID, "heading": heading(clause.Heading, i+1),
			"text_offset": map[string]int{"start": clause.Start, "end": clause.End},
			"page":        nil,
		}
		if clause.Page != nil {
			item["page"] = *clause.Page
		}
		clauses[i] = item
	}
	out["clauses"] = clauses
	return out
}

func parsedDocumentJSON(parsed *store.ParsedDocument) any {
	if parsed == nil {
		return nil
	}
	return map[string]any{
		"id": parsed.ID, "document_id": parsed.DocumentID, "original_digest": parsed.OriginalDigest,
		"parser_id": parsed.ParserID, "parser_version": parsed.ParserVersion, "config_hash": parsed.ConfigHash,
		"normalization_version": parsed.NormalizationVersion, "normalized_digest": nilString(parsed.NormalizedDigest),
		"ir_digest": nilString(parsed.IRDigest), "artifact_digest": parsed.ArtifactDigest,
		"created_at": parsed.CreatedAt, "status": parsed.Status,
	}
}

func anchorDocument(anchor store.Anchor, docs map[string]store.Interpreted) store.Interpreted {
	if anchor.ParsedDocumentID != "" {
		return docs["parsed:"+anchor.ParsedDocumentID]
	}
	return docs[anchor.DocumentID]
}

func workspaceJSON(st *store.Store, ws store.Workspace) map[string]any {
	inSnapshot := map[string]bool{}
	for _, id := range ws.Snapshot.DocumentIDs {
		inSnapshot[id] = true
	}
	docs := make([]any, len(ws.Documents))
	byID := map[string]store.Interpreted{}
	for i, doc := range ws.Documents {
		replaced := len(inSnapshot) > 0 && !inSnapshot[doc.Document.ID]
		docs[i] = documentJSON(doc, true, replaced)
		byID[doc.Document.ID] = doc
		if doc.ParsedDocument != nil {
			byID["parsed:"+doc.ParsedDocument.ID] = doc
		}
	}
	markOf := map[string]string{}
	for _, item := range ws.Revisions {
		if item.FindingID != "" {
			markOf[item.FindingID] = item.Mark
		}
	}
	findings := make([]any, len(ws.Findings))
	for i, finding := range ws.Findings {
		findings[i] = findingJSON(st, finding, byID, markOf[finding.ID])
	}
	run := map[string]any{
		"id": ws.View.Run.ID, "state": ws.View.State, "updated_at": ws.View.Run.UpdatedAt,
		"case_id": ws.Case.ID, "started_at": ws.View.Run.CreatedAt, "model_ref": nilString(ws.View.Run.ModelRef),
		"partial_reason": nilString(ws.View.PartialReason), "failure_message": nilString(ws.View.FailureMessage),
	}
	evals := make([]any, len(ws.Evaluations))
	for i, ev := range ws.Evaluations {
		var findingID any
		if ev.FindingID != "" {
			findingID = ev.FindingID
		}
		factIDs := ev.FactIDs
		if factIDs == nil {
			factIDs = []string{}
		}
		objectRefs := ev.ObjectRefs
		if objectRefs == nil {
			objectRefs = []string{}
		}
		evals[i] = map[string]any{
			"id": ev.ID, "domain": ev.Domain, "relation": ev.Relation,
			"rule_version": ev.RuleVersion, "reason": ev.Reason, "finding_id": findingID,
			"rule_id": ev.RuleID, "capability_id": ev.CapabilityID, "reason_code": ev.ReasonCode, "fact_ids": factIDs, "object_refs": objectRefs,
		}
	}
	var execution any
	if ws.Execution != nil {
		execution = ws.Execution
	}
	return map[string]any{
		"run": run, "documents": docs, "findings": findings, "evaluations": evals,
		"execution":                  execution,
		"snapshot":                   snapshotJSON(ws.Snapshot),
		"audit_events":               auditJSON(ws.Audit),
		"previous_run_id":            nilString(ws.PreviousRunID),
		"revision_comparison_reason": nilString(ws.RevisionComparisonReason),
		"revisions":                  revisionsJSON(ws.Revisions, byID),
	}
}

func reviewIdentityJSON(ws store.Workspace) map[string]any {
	var attemptID any
	if ws.Execution != nil {
		attemptID = ws.Execution.Attempt.ID
	}
	return map[string]any{"id": ws.View.Run.ID, "attempt_id": attemptID}
}

func revisionsJSON(items []store.Revision, docs map[string]store.Interpreted) []any {
	out := make([]any, len(items))
	for i, item := range items {
		evidence := make([]any, len(item.Anchors))
		for j, anchor := range item.Anchors {
			evidence[j] = anchorJSON(anchor, anchorDocument(anchor, docs))
		}
		var prior any
		if item.Prior != nil {
			prior = map[string]any{
				"id": item.Prior.ID, "action": item.Prior.Action, "reason": item.Prior.Reason,
				"actor": item.Prior.ActorName, "decided_at": item.Prior.CreatedAt,
			}
		}
		out[i] = map[string]any{
			"mark": item.Mark, "finding_id": nilString(item.FindingID),
			"previous_finding_id": nilString(item.PreviousFindingID),
			"domain":              item.Domain, "relation": item.Relation, "claim": item.Claim,
			"evidence": evidence, "prior_decision": prior,
		}
	}
	return out
}

func snapshotJSON(snap store.RunSnapshot) map[string]any {
	ids := make([]any, len(snap.DocumentIDs))
	for i, id := range snap.DocumentIDs {
		ids[i] = id
	}
	if ids == nil {
		ids = []any{}
	}
	return map[string]any{
		"document_ids": ids, "rule_set_hash": snap.RuleSetHash, "playbook_hash": snap.PlaybookHash,
		"model_ref": nilString(snap.ModelRef), "comparator_ref": nilString(snap.ComparatorRef),
	}
}

func auditJSON(events []store.AuditRecord) []any {
	out := make([]any, len(events))
	for i, event := range events {
		corr := make([]any, len(event.CorrelationIDs))
		for j, id := range event.CorrelationIDs {
			corr[j] = id
		}
		if corr == nil {
			corr = []any{}
		}
		out[i] = map[string]any{
			"event_id": event.EventID, "run_id": event.RunID, "actor": event.Actor, "seq": event.Seq,
			"event_type": event.EventType, "prev_hash": event.PrevHash, "hash": event.Hash,
			"correlation_ids": corr,
		}
	}
	return out
}

func findingJSON(st *store.Store, finding store.Finding, docs map[string]store.Interpreted, mark string) map[string]any {
	evidence := make([]any, len(finding.Anchors))
	for i, anchor := range finding.Anchors {
		evidence[i] = anchorJSON(anchor, anchorDocument(anchor, docs))
	}
	decisions := st.DecisionsFor(finding.ID)
	history := make([]any, len(decisions))
	for i, decision := range decisions {
		history[i] = map[string]any{
			"id": decision.ID, "action": decision.Action, "reason": decision.Reason,
			"actor": decision.ActorName, "decided_at": decision.CreatedAt,
			"old_disposition": decision.OldDisposition, "new_disposition": decision.NewDisposition,
		}
	}
	return map[string]any{
		"id": finding.ID, "origin": finding.Origin, "domain": finding.Domain, "claim": finding.Claim,
		"relation": finding.Relation, "confidence": nil, "evidence": evidence,
		"severity": finding.Severity, "severity_source": finding.SeveritySource,
		"disposition_suggestion": nilString(finding.DispositionSuggestion),
		"unverified_reason":      nilString(finding.UnverifiedReason), "version": finding.Version,
		"decisions": history, "revision_mark": nilString(mark),
	}
}

func anchorJSON(anchor store.Anchor, doc store.Interpreted) map[string]any {
	out := map[string]any{
		"id": anchor.ID, "document_version_id": anchor.DocumentID, "quote": anchor.Quote,
		"status": anchor.Status, "page": nil, "text_offset": nil, "bbox": nil, "element_id": nil,
		"mismatch_reason":    nilString(anchor.Reason),
		"parsed_document_id": nilString(anchor.ParsedDocumentID), "normalized_digest": nilString(anchor.NormalizedDigest),
	}
	if anchor.Status != "located" {
		if anchor.Reason == "" {
			out["mismatch_reason"] = "找不到对应来源，不能把这条引用当成已验证证据。"
		}
		if anchor.Status != "missing" && anchor.Status != "unlocatable" {
			out["status"] = "unlocatable"
		}
		return out
	}
	if anchor.DocumentID != doc.Document.ID ||
		(anchor.ParsedDocumentID != "" && (doc.ParsedDocument == nil || doc.ParsedDocument.ID != anchor.ParsedDocumentID ||
			doc.ParsedDocument.DocumentID != anchor.DocumentID || doc.ParsedDocument.Status != "ready" ||
			doc.ParsedDocument.OriginalDigest != doc.Document.Digest || anchor.NormalizedDigest == "" || doc.ParsedDocument.NormalizedDigest != anchor.NormalizedDigest)) ||
		(anchor.NormalizedDigest != "" && anchor.NormalizedDigest != doc.IR.NormalizedDigest) {
		out["status"] = "unlocatable"
		out["mismatch_reason"] = "引用解析身份与冻结正文不一致，未在正文中高亮。"
		return out
	}
	quote, ok := docir.Slice(doc.Text, anchor.Start, anchor.End)
	if !doc.Ready || !ok || quote != anchor.Quote {
		out["status"] = "unlocatable"
		out["quote"] = anchor.Quote
		out["mismatch_reason"] = "定位坐标与当前正文不一致，未在正文中高亮。"
		return out
	}
	element := doc.IR.ElementAt(anchor.Start, anchor.End)
	if anchor.ElementID != "" && element != "" && anchor.ElementID != element {
		out["status"] = "unlocatable"
		out["quote"] = anchor.Quote
		out["mismatch_reason"] = "定位坐标与当前正文不一致，未在正文中高亮。"
		return out
	}
	page, hasPage := doc.IR.PageAt(anchor.Start, anchor.End)
	if anchor.Page != nil && (!hasPage || *anchor.Page != page) {
		out["status"] = "unlocatable"
		out["quote"] = anchor.Quote
		out["mismatch_reason"] = "定位坐标与当前正文不一致，未在正文中高亮。"
		return out
	}
	out["status"] = "located"
	out["text_offset"] = map[string]int{"start": anchor.Start, "end": anchor.End}
	out["mismatch_reason"] = nil
	if element != "" {
		out["element_id"] = element
	}
	if hasPage {
		out["page"] = page
	}
	return out
}

func nilString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
