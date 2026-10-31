package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/docir"
	"contractaudit/internal/fault"
)

// EvidenceManualFinding is the append-only manual finding as observed by the
// export transaction. It remains separate from the original report findings.
type EvidenceManualFinding struct {
	ID        string                `json:"id"`
	CaseID    string                `json:"case_id"`
	RunID     string                `json:"run_id"`
	AttemptID string                `json:"attempt_id"`
	ActorID   string                `json:"actor_id"`
	ActorName string                `json:"actor_name"`
	Claim     string                `json:"claim"`
	Severity  string                `json:"severity"`
	Evidence  ExperienceEvidenceRef `json:"evidence"`
	CreatedAt time.Time             `json:"created_at"`
}

// EvidenceOriginal maps a fixed safe archive path to the immutable document.
// The bytes are read separately through Original under current case access.
type EvidenceOriginal struct {
	Path             string `json:"path"`
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
	Filename         string `json:"filename"`
	MediaType        string `json:"media_type"`
	Size             int64  `json:"size"`
	SHA256           string `json:"sha256"`
}

type EvidenceBundleData struct {
	Record          ReportRecord
	ReportJSON      []byte
	CaseTitle       string
	Findings        []Finding
	Evaluations     []ConstraintEval
	FrozenDecisions []Decision
	Decisions       []Decision
	ManualFindings  []EvidenceManualFinding
	Originals       []EvidenceOriginal
	HistoryCutoff   time.Time
}

// EvidenceBundleDataForReport reads an immutable report only after current
// export authorization and revalidates the original frozen attempt, parse and
// rule identities. Current append-only human rows are a separate snapshot,
// never a replacement for the report's own frozen decisions.
func (s *Store) EvidenceBundleDataForReport(ctx context.Context, user accounts.User, caseID, reportID string) (EvidenceBundleData, error) {
	var out EvidenceBundleData
	if err := ctx.Err(); err != nil {
		return out, err
	}
	record, reportJSON, err := s.ReadReport(user, caseID, reportID, "json")
	if err != nil {
		return out, err
	}
	var report reportEnvelope
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		return out, fault.Conflict("保存的报告内容无法读取")
	}
	if report.SchemaVersion != businessReportSchema || report.ReportID != record.ID || report.ReportVersion != record.Version ||
		report.Case.ID != caseID || report.Run.ID != record.RunID || report.Attempt.ID != record.AttemptID ||
		report.Attempt.RunID != record.RunID || report.InputHash != record.InputHash || report.OutputHash != record.OutputHash ||
		report.DecisionSnapshotHash != record.DecisionSnapshotHash {
		return out, fault.Conflict("报告与执行记录身份不一致")
	}
	decisionHash, err := canonicalHash(report.Decisions)
	if err != nil || decisionHash != record.DecisionSnapshotHash {
		return out, fault.Conflict("报告的决定快照未通过完整性校验")
	}
	current, err := trustedUser(user)
	if err != nil {
		return out, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return out, err
	}
	item, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return out, err
	}
	if err := requireCaseAction(item, current, access.Export); err != nil {
		return out, err
	}
	_, attempt, input, _, err := s.validateFrozenTerminalAttempt(st, item, record.RunID, record.AttemptID)
	if err != nil {
		return out, err
	}
	if *attempt.InputHash != record.InputHash || *attempt.OutputHash != record.OutputHash || !equalEvidenceJSON(report.Input, input) {
		return out, fault.Conflict("报告的冻结输入与所选执行记录不一致")
	}
	if len(attempt.RulePackSnapshot) == 0 {
		if report.RulePack.Availability != "unavailable" || string(report.RulePack.Snapshot) != "null" {
			return out, fault.Conflict("报告规则快照与执行记录不一致")
		}
	} else {
		snapshot, err := manifestSnapshot(attempt.RulePackSnapshot)
		if err != nil || report.RulePack.Availability != "frozen" || !bytes.Equal(report.RulePack.Snapshot, snapshot) ||
			report.RulePack.Hash != attempt.RulePackHash || report.RulePack.SnapshotDigest != attempt.RulePackSnapshotDigest ||
			report.RulePack.CompilerRef != attempt.RulePackCompilerRef {
			return out, fault.Conflict("报告规则快照与执行记录不一致")
		}
	}
	if err := verifyEvidenceReportReferences(report, input); err != nil {
		return out, err
	}
	originals := make([]EvidenceOriginal, 0, len(input.Documents))
	for index, frozen := range input.Documents {
		doc := frozen.Document
		if doc.CaseID != caseID || doc.TenantID != item.TenantID || doc.OrgID != item.OwnerOrgID ||
			doc.ID == "" || doc.Digest != frozen.OriginalHash || doc.Size < 0 {
			return out, fault.Conflict("报告中的原件身份不一致")
		}
		originals = append(originals, EvidenceOriginal{Path: fmt.Sprintf("originals/%06d.bin", index+1), DocumentID: doc.ID,
			ParsedDocumentID: frozen.ParsedDocumentID, Filename: doc.Filename, MediaType: doc.MediaType, Size: doc.Size, SHA256: doc.Digest})
	}
	cutoff, decisions, manual, err := s.evidenceHumanHistory(ctx, current, item.ID, record.RunID, record.AttemptID, input.Documents)
	if err != nil {
		return out, err
	}
	// An append-only decision that was included in the immutable report must
	// still be present, byte-for-byte in meaning, in the later history snapshot.
	decisionByID := make(map[string]Decision, len(decisions))
	for _, decision := range decisions {
		decisionByID[decision.ID] = decision
	}
	for _, frozen := range report.Decisions {
		if currentDecision, ok := decisionByID[frozen.ID]; !ok || currentDecision != frozen {
			return out, fault.Conflict("报告的决定历史与当前追加记录不一致")
		}
	}
	return EvidenceBundleData{Record: record, ReportJSON: reportJSON, CaseTitle: report.Case.Title,
		Findings: report.Findings, Evaluations: report.Evaluations, FrozenDecisions: report.Decisions,
		Decisions: decisions, ManualFindings: manual, Originals: originals, HistoryCutoff: cutoff.UTC()}, nil
}

// The history rows and cutoff are selected by one SQL statement, so PostgreSQL
// gives both subqueries the same MVCC snapshot even at READ COMMITTED. The case
// lock and RLS scope come from beginExperienceCase. A record committed after the
// statement's snapshot is not asserted to be present, regardless of its timestamp.
func (s *Store) evidenceHumanHistory(ctx context.Context, user accounts.User, caseID, runID, attemptID string, documents []FrozenDocument) (time.Time, []Decision, []EvidenceManualFinding, error) {
	tx, current, item, err := s.beginExperienceCase(ctx, user, caseID, access.Export)
	if err != nil {
		return time.Time{}, nil, nil, err
	}
	defer tx.Rollback(context.Background())
	var cutoff time.Time
	var decisionsJSON, manualJSON []byte
	err = tx.QueryRow(ctx, `WITH boundary AS (SELECT statement_timestamp() AS cutoff)
SELECT boundary.cutoff,
 COALESCE((SELECT jsonb_agg(jsonb_build_object(
  'id',d.id,'finding_id',d.finding_id,'run_id',d.run_id,'case_id',d.case_id,
  'actor_id',d.actor_id,'actor_name',d.actor_name,'action',d.action,'reason',d.reason,
  'old_disposition',d.old_disposition,'new_disposition',d.new_disposition,'created_at',d.created_at)
  ORDER BY d.created_at::timestamptz,d.id)
  FROM decisions d JOIN findings f ON f.tenant_id=d.tenant_id AND f.org_id=d.org_id AND f.id=d.finding_id
  WHERE d.tenant_id=$1 AND d.org_id=$2 AND d.case_id=$3 AND d.run_id=$4 AND f.attempt_id=$5
   AND f.case_id=$3 AND f.run_id=$4 AND d.created_at::timestamptz<=boundary.cutoff), '[]'::jsonb),
 COALESCE((SELECT jsonb_agg(jsonb_build_object(
  'id',m.id,'case_id',m.case_id,'run_id',m.run_id,'attempt_id',m.attempt_id,
  'actor_id',m.actor_id,'actor_name',m.actor_name,'claim',m.claim,'severity',m.severity,
  'evidence',jsonb_build_object('document_id',m.document_id,'parsed_document_id',m.parsed_document_id,
   'normalized_digest',m.normalized_digest,'start',m.start_offset,'end',m.end_offset,'quote',m.quote),
  'created_at',m.created_at) ORDER BY m.created_at,m.id)
  FROM manual_finding_records m WHERE m.tenant_id=$1 AND m.org_id=$2 AND m.case_id=$3
   AND m.run_id=$4 AND m.attempt_id=$5 AND m.created_at<=boundary.cutoff), '[]'::jsonb)
FROM boundary`, item.TenantID, item.OwnerOrgID, caseID, runID, attemptID).Scan(&cutoff, &decisionsJSON, &manualJSON)
	if err != nil {
		return time.Time{}, nil, nil, err
	}
	var decisions []Decision
	var manual []EvidenceManualFinding
	if err := json.Unmarshal(decisionsJSON, &decisions); err != nil {
		return time.Time{}, nil, nil, err
	}
	if err := json.Unmarshal(manualJSON, &manual); err != nil {
		return time.Time{}, nil, nil, err
	}
	for _, record := range manual {
		if record.CaseID != caseID || record.RunID != runID || record.AttemptID != attemptID || record.CreatedAt.After(cutoff) ||
			record.Evidence.Start < 0 || record.Evidence.End <= record.Evidence.Start {
			return time.Time{}, nil, nil, fault.Conflict("人工发现记录与执行身份不一致")
		}
		if err := verifyFeedbackEvidence(ctx, tx, item, runID, attemptID, documents, record.Evidence); err != nil {
			return time.Time{}, nil, nil, fault.Conflict("人工发现引用未通过冻结原文校验")
		}
	}
	fresh, err := trustedUser(current)
	if err != nil || !authorizeCase(item, fresh, access.Read) || !authorizeCase(item, fresh, access.Export) {
		return time.Time{}, nil, nil, fault.NotFound("没有找到对应记录")
	}
	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, nil, nil, err
	}
	return cutoff, decisions, manual, nil
}

func verifyEvidenceReportReferences(report reportEnvelope, input AttemptInput) error {
	if len(report.Documents) != len(input.Documents) {
		return fault.Conflict("报告材料与冻结输入不一致")
	}
	byID := make(map[string]FrozenDocument, len(input.Documents))
	for i, document := range input.Documents {
		if _, exists := byID[document.Document.ID]; exists || !equalEvidenceJSON(report.Documents[i].Document, document.Document) ||
			report.Documents[i].Parsed.ID != document.ParsedDocumentID || report.Documents[i].Parsed.ArtifactDigest != document.ParsedArtifactDigest ||
			report.Documents[i].Parsed.Text != document.Text {
			return fault.Conflict("报告解析结果与冻结输入不一致")
		}
		byID[document.Document.ID] = document
	}
	for _, finding := range report.Findings {
		if finding.CaseID != report.Case.ID || finding.RunID != report.Run.ID || finding.AttemptID != report.Attempt.ID {
			return fault.Conflict("报告发现与执行身份不一致")
		}
		for _, anchor := range finding.Anchors {
			if anchor.Status != "located" {
				continue
			}
			doc, ok := byID[anchor.DocumentID]
			if !ok || !evidenceAnchorMatches(anchor, doc) {
				return fault.Conflict("报告发现引用未通过冻结原文校验")
			}
		}
	}
	for _, evaluation := range report.Evaluations {
		if evaluation.CaseID != report.Case.ID || evaluation.RunID != report.Run.ID || evaluation.AttemptID != report.Attempt.ID {
			return fault.Conflict("报告评估与执行身份不一致")
		}
		for _, evidence := range evaluation.Evidence {
			doc, ok := byID[evidence.Ref.DocumentID]
			if !ok || !evidenceReferenceMatches(evidence.Ref, doc) {
				return fault.Conflict("报告评估引用未通过冻结原文校验")
			}
		}
	}
	return nil
}

func evidenceReferenceMatches(ref ExperienceEvidenceRef, doc FrozenDocument) bool {
	if !doc.Ready || ref.DocumentID != doc.Document.ID || ref.ParsedDocumentID != doc.ParsedDocumentID ||
		ref.NormalizedDigest == "" || doc.NormalizedHash == nil || ref.NormalizedDigest != *doc.NormalizedHash || ref.Quote == "" {
		return false
	}
	quote, ok := docir.Slice(doc.Text, ref.Start, ref.End)
	return ok && ref.End > ref.Start && quote == ref.Quote
}

func evidenceAnchorMatches(anchor Anchor, doc FrozenDocument) bool {
	if !doc.Ready || anchor.DocumentID != doc.Document.ID || anchor.Quote == "" || anchor.End <= anchor.Start {
		return false
	}
	if anchor.ParsedDocumentID != "" && (anchor.ParsedDocumentID != doc.ParsedDocumentID || anchor.NormalizedDigest == "") {
		return false
	}
	if anchor.NormalizedDigest != "" && (doc.NormalizedHash == nil || *doc.NormalizedHash != anchor.NormalizedDigest) {
		return false
	}
	quote, ok := docir.Slice(doc.Text, anchor.Start, anchor.End)
	if !ok || quote != anchor.Quote {
		return false
	}
	if anchor.ElementID != "" {
		element := doc.IR.ElementAt(anchor.Start, anchor.End)
		if element != "" && element != anchor.ElementID {
			return false
		}
	}
	if anchor.Page != nil {
		page, exists := doc.IR.PageAt(anchor.Start, anchor.End)
		if !exists || *anchor.Page != page {
			return false
		}
	}
	return true
}

func equalEvidenceJSON(left, right any) bool {
	leftHash, leftErr := canonicalHash(left)
	rightHash, rightErr := canonicalHash(right)
	return leftErr == nil && rightErr == nil && leftHash == rightHash
}
