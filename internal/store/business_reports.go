package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"sort"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
)

const (
	businessReportSchema = "case-report/v1"
	businessReportHTML   = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.CaseTitle}}</title><style>body{font-family:system-ui,sans-serif;max-width:72rem;margin:2rem auto;padding:0 1rem;color:#17212b}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#f4f6f8;padding:1rem;border-radius:.5rem}</style></head><body><h1>{{.CaseTitle}}</h1><p>本报告记录本次技术审查的内容，不构成法律意见或业务审批。</p><pre>{{.ReportJSON}}</pre></body></html>`
)

// CreateReport appends immutable JSON and escaped HTML for one exact terminal
// attempt. Later decisions produce a different report identity and never edit
// these bytes.
func (s *Store) CreateReport(user accounts.User, caseID, runID, attemptID string) (ReportRecord, error) {
	return s.CreateReportContext(context.Background(), user, caseID, runID, attemptID)
}

// CreateReportContext is the request-scoped form. It lets a caller cancel the
// object-integrity and agent-review reads when the HTTP request is gone while
// keeping the existing CreateReport signature for internal and test callers.
func (s *Store) CreateReportContext(ctx context.Context, user accounts.User, caseID, runID, attemptID string) (ReportRecord, error) {
	if err := ctx.Err(); err != nil {
		return ReportRecord{}, err
	}
	if s.ReadOnly() {
		return ReportRecord{}, fault.New(503, "read_only", "只读模式下不能创建报告")
	}
	currentUser, err := trustedUser(user)
	if err != nil {
		return ReportRecord{}, err
	}
	caseID, runID, attemptID = strings.TrimSpace(caseID), strings.TrimSpace(runID), strings.TrimSpace(attemptID)
	if caseID == "" || runID == "" || attemptID == "" {
		return ReportRecord{}, fault.Invalid("生成报告需要指定案件、审查和执行记录")
	}
	st, err := s.snapshotAs(currentUser)
	if err != nil {
		return ReportRecord{}, err
	}
	caseItem, err := s.visibleCase(&st, currentUser, caseID)
	if err != nil {
		return ReportRecord{}, err
	}
	if err := businessWritable(caseItem); err != nil {
		return ReportRecord{}, err
	}
	if err := requireCaseAction(caseItem, currentUser, access.Export); err != nil {
		return ReportRecord{}, err
	}
	run, attempt, input, _, err := s.validateFrozenTerminalAttempt(st, caseItem, runID, attemptID)
	if err != nil {
		return ReportRecord{}, err
	}
	if run.CaseID != caseID {
		return ReportRecord{}, fault.NotFound("没有找到对应审查记录")
	}
	executionView := selectedExecution(st, runID, attemptID)
	if executionView == nil {
		return ReportRecord{}, fault.Conflict("所选执行记录缺少已保存的执行信息")
	}
	execution := reportExecution{
		SchemaVersion: executionView.SchemaVersion, Events: append([]ExecutionEvent{}, executionView.Events...),
		Usage: executionView.Usage, DurationMS: executionView.DurationMS,
		FirstVerifiedFindingMS: executionView.FirstVerifiedFindingMS, QueueWaitMS: executionView.QueueWaitMS,
	}
	findings := st.findingsForAttempt(runID, attemptID)
	evaluations := st.evaluationsForAttempt(runID, attemptID)
	decisions := decisionsForFindings(st, findings)
	decisionHash, err := canonicalHash(decisions)
	if err != nil {
		return ReportRecord{}, err
	}
	id, err := newID("report_")
	if err != nil {
		return ReportRecord{}, err
	}
	now := stamp()
	nextVersion := nextReportVersion(st.Reports, caseID)
	parsedDocs := make([]reportDocument, 0, len(input.Documents))
	for _, frozen := range input.Documents {
		parsed, ok := st.parsedByID(frozen.ParsedDocumentID)
		if !ok || parsed.DocumentID != frozen.Document.ID || parsed.OriginalDigest != frozen.Document.Digest ||
			parsed.ArtifactDigest != frozen.ParsedArtifactDigest {
			return ReportRecord{}, fault.Conflict("报告材料与所选执行记录保存的解析结果不一致")
		}
		// Recorded identity is not proof of content: a read may carry an artifact
		// whose stored payload failed verification. Re-verify against the row before
		// freezing it into a report.
		if err := s.verifyParsedRecordContext(ctx, parsed, frozen.Document); err != nil {
			return ReportRecord{}, fault.Conflict("报告材料的解析结果未通过完整性校验")
		}
		parsedDocs = append(parsedDocs, reportDocument{Document: frozen.Document, Parsed: parsed})
	}
	var manifestBytes json.RawMessage
	availability := "frozen"
	unavailableReason := ""
	if len(attempt.RulePackSnapshot) == 0 {
		if attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" || attempt.RulePackCompilerRef == "" ||
			!completeNoAuthorityRuleEvidence(caseItem, runID, attemptID, evaluations, findings) {
			return ReportRecord{}, fault.Conflict("缺少已授权 RulePack，且三项强制能力没有完整的授权不足证据")
		}
		availability = "unavailable"
		unavailableReason = "此尝试未冻结可用 RulePack；带宽、月度可用率和预付款三项强制能力均以 insufficient / contractual_authority_missing 记录，采购或中标文件缺少合同依据。"
		manifestBytes = json.RawMessage("null")
	} else {
		manifestBytes, err = manifestSnapshot(attempt.RulePackSnapshot)
		if err != nil {
			return ReportRecord{}, fault.Conflict("所选执行记录保存的规则包已损坏")
		}
	}
	// The five-domain projection is read from this attempt's persisted record only;
	// it is never recomputed for the report, and an absent record stays null.
	agentReviewJSON, hasAgentReview, err := s.AgentReviewForAttempt(ctx, currentUser, caseID, runID, attemptID)
	if err != nil {
		return ReportRecord{}, err
	}
	var agentReviewBytes []byte
	agentReviewDigest := ""
	if hasAgentReview {
		agentReviewBytes = append([]byte(nil), agentReviewJSON...)
		agentReviewDigest = sha256Bytes(agentReviewBytes)
	} else {
		agentReviewJSON = nil
	}
	envelope := reportEnvelope{
		SchemaVersion: businessReportSchema, ReportID: id, ReportVersion: nextVersion, CreatedAt: now,
		Notice: "本报告记录技术审查结果，不构成法律意见或业务审批。",
		Case:   reportCase{ID: caseItem.ID, Title: caseItem.Title, ContractType: caseItem.ContractType, BusinessLine: caseItem.BusinessLine, Scenario: caseItem.Scenario},
		Run:    run, Attempt: attempt, Execution: execution, RunSnapshot: st.snapshotOf(runID), Input: input,
		Documents: parsedDocs, Findings: findings, Evaluations: evaluations, Decisions: decisions,
		RulePack: reportRulePack{
			Availability: availability, CompilerRef: attempt.RulePackCompilerRef,
			Hash: attempt.RulePackHash, SnapshotDigest: attempt.RulePackSnapshotDigest,
			Snapshot: manifestBytes, UnavailableReason: unavailableReason,
		},
		InputHash: *attempt.InputHash, OutputHash: *attempt.OutputHash, DecisionSnapshotHash: decisionHash,
		AgentReview: agentReviewJSON, AgentReviewBytes: agentReviewBytes, AgentReviewSHA256: agentReviewDigest,
	}
	jsonBytes, err := json.Marshal(envelope)
	if err != nil {
		return ReportRecord{}, err
	}
	htmlBytes, err := renderBusinessReportHTML(caseItem.Title, jsonBytes)
	if err != nil {
		return ReportRecord{}, err
	}
	record := ReportRecord{
		ID: id, Version: nextVersion, RunID: runID, AttemptID: attemptID,
		InputHash: *attempt.InputHash, OutputHash: *attempt.OutputHash,
		DecisionSnapshotHash: decisionHash, Digest: reportDigest(jsonBytes, htmlBytes), CreatedAt: now,
	}
	if err := s.updateCaseAs(currentUser, caseID, func(next *state) error {
		currentCase, err := s.requireBusinessWrite(next, currentUser, caseID)
		if err != nil {
			return err
		}
		if err := requireCaseAction(currentCase, currentUser, access.Export); err != nil {
			return err
		}
		currentAttempt, ok := next.attemptByID(attemptID)
		if !ok || currentAttempt.RunID != runID || currentAttempt.InputHash == nil || currentAttempt.OutputHash == nil ||
			*currentAttempt.InputHash != record.InputHash || *currentAttempt.OutputHash != record.OutputHash ||
			currentAttempt.RulePackHash != attempt.RulePackHash || currentAttempt.RulePackSnapshotDigest != attempt.RulePackSnapshotDigest ||
			currentAttempt.RulePackCompilerRef != attempt.RulePackCompilerRef {
			return fault.Conflict("所选执行记录在生成报告期间发生变化")
		}
		currentFindings := next.findingsForAttempt(runID, attemptID)
		currentDecisionHash, err := canonicalHash(decisionsForFindings(*next, currentFindings))
		if err != nil {
			return err
		}
		if currentDecisionHash != record.DecisionSnapshotHash || nextReportVersion(next.Reports, caseID) != record.Version {
			return fault.Conflict("决定或报告版本刚被更新，请刷新后重试")
		}
		next.Reports = append(next.Reports, frozenReport{
			Record: record, TenantID: currentCase.TenantID, OrgID: currentCase.OwnerOrgID, CaseID: caseID,
			JSONBytes: append([]byte(nil), jsonBytes...), HTMLBytes: append([]byte(nil), htmlBytes...),
		})
		return nil
	}); err != nil {
		return ReportRecord{}, err
	}
	return record, nil
}

// ReadReport returns the stored bytes only after rechecking case export access
// and the report digest. Archived cases remain readable to authorized users.
func (s *Store) ReadReport(user accounts.User, caseID, reportID, format string) (ReportRecord, []byte, error) {
	currentUser, err := trustedUser(user)
	if err != nil {
		return ReportRecord{}, nil, err
	}
	if format != "json" && format != "html" {
		return ReportRecord{}, nil, fault.Invalid("报告格式只能是 json 或 html")
	}
	st, err := s.snapshotAs(currentUser)
	if err != nil {
		return ReportRecord{}, nil, err
	}
	caseItem, err := s.visibleCase(&st, currentUser, caseID)
	if err != nil {
		return ReportRecord{}, nil, err
	}
	if err := requireCaseAction(caseItem, currentUser, access.Export); err != nil {
		return ReportRecord{}, nil, err
	}
	for _, report := range st.Reports {
		if report.Record.ID != reportID || report.CaseID != caseID || report.TenantID != caseItem.TenantID || report.OrgID != caseItem.OwnerOrgID {
			continue
		}
		if reportDigest(report.JSONBytes, report.HTMLBytes) != report.Record.Digest {
			return ReportRecord{}, nil, fault.Conflict("保存的报告未通过摘要校验")
		}
		body := report.JSONBytes
		if format == "html" {
			body = report.HTMLBytes
		}
		return report.Record, append([]byte(nil), body...), nil
	}
	return ReportRecord{}, nil, fault.NotFound("没有找到对应报告")
}

// Archive appends the archive identity and freezes all later business writes.
func (s *Store) Archive(user accounts.User, caseID, reportID, reason string) (ArchiveRecord, error) {
	if s.ReadOnly() {
		return ArchiveRecord{}, fault.New(503, "read_only", "只读模式下不能归档案件")
	}
	currentUser, err := trustedUser(user)
	if err != nil {
		return ArchiveRecord{}, err
	}
	caseID, reportID, reason = strings.TrimSpace(caseID), strings.TrimSpace(reportID), strings.TrimSpace(reason)
	if caseID == "" || reportID == "" || !validBusinessText(reason, 2000) {
		return ArchiveRecord{}, fault.Invalid("归档需要有效报告和说明理由")
	}
	st, err := s.snapshotAs(currentUser)
	if err != nil {
		return ArchiveRecord{}, err
	}
	caseItem, err := s.visibleCase(&st, currentUser, caseID)
	if err != nil {
		return ArchiveRecord{}, err
	}
	if err := businessWritable(caseItem); err != nil {
		return ArchiveRecord{}, err
	}
	if err := requireCaseAction(caseItem, currentUser, access.Manage); err != nil {
		return ArchiveRecord{}, err
	}
	if !reportExists(st, caseItem, reportID) {
		return ArchiveRecord{}, fault.NotFound("没有找到对应报告")
	}
	id, err := newID("archive_")
	if err != nil {
		return ArchiveRecord{}, err
	}
	now := stamp()
	record := ArchiveRecord{ID: id, CaseID: caseID, ReportID: reportID, Reason: reason, ArchivedAt: now}
	wrapper := archiveRecord{Record: record, TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, ActorID: currentUser.ID}
	if err := s.scopedCaseMutation(context.Background(), currentUser, caseID, func(next *state) error {
		currentCase, err := s.visibleCase(next, currentUser, caseID)
		if err != nil {
			return err
		}
		if err := businessWritable(currentCase); err != nil {
			return err
		}
		if err := requireCaseAction(currentCase, currentUser, access.Manage); err != nil {
			return err
		}
		if !reportExists(*next, currentCase, reportID) {
			return fault.NotFound("没有找到对应报告")
		}
		for _, archive := range next.Archives {
			if archive.Record.CaseID == caseID && archive.TenantID == currentCase.TenantID && archive.OrgID == currentCase.OwnerOrgID {
				return fault.Conflict("案件已归档")
			}
		}
		next.Archives = append(next.Archives, wrapper)
		archivedAt := now
		for i := range next.Cases {
			if next.Cases[i].ID == caseID {
				next.Cases[i].ArchivedAt = &archivedAt
				next.Cases[i].BusinessStatus = "archived"
				next.Cases[i].UpdatedAt = now
			}
		}
		return nil
	}); err != nil {
		return ArchiveRecord{}, err
	}
	return record, nil
}

func reportExists(st state, caseItem Case, reportID string) bool {
	for _, report := range st.Reports {
		if report.Record.ID == reportID && report.CaseID == caseItem.ID && report.TenantID == caseItem.TenantID && report.OrgID == caseItem.OwnerOrgID {
			return reportDigest(report.JSONBytes, report.HTMLBytes) == report.Record.Digest
		}
	}
	return false
}

func decisionsForFindings(st state, findings []Finding) []Decision {
	selected := make(map[string]bool, len(findings))
	for _, finding := range findings {
		selected[finding.ID] = true
	}
	out := make([]Decision, 0)
	for _, decision := range st.Decisions {
		if selected[decision.FindingID] {
			out = append(out, decision)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out
}

func canonicalHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func nextReportVersion(reports []frozenReport, caseID string) int {
	version := 1
	for _, report := range reports {
		if report.CaseID == caseID && report.Record.Version >= version {
			version = report.Record.Version + 1
		}
	}
	return version
}

func manifestSnapshot(raw []byte) (json.RawMessage, error) {
	pack, err := rulepack.Parse(raw)
	if err != nil {
		return nil, err
	}
	encoded, err := pack.Snapshot()
	if err != nil || !bytes.Equal(raw, encoded) {
		return nil, fault.Conflict("保存的规则包内容不符合规范格式")
	}
	return append(json.RawMessage(nil), raw...), nil
}

func renderBusinessReportHTML(caseTitle string, reportJSON []byte) ([]byte, error) {
	tpl, err := template.New("business-report").Parse(businessReportHTML)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := tpl.Execute(&output, reportHTMLModel{CaseTitle: caseTitle, ReportJSON: string(reportJSON)}); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func reportDigest(jsonBytes, htmlBytes []byte) string {
	hasher := sha256.New()
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(jsonBytes)))
	_, _ = hasher.Write(size[:])
	_, _ = hasher.Write(jsonBytes)
	binary.BigEndian.PutUint64(size[:], uint64(len(htmlBytes)))
	_, _ = hasher.Write(size[:])
	_, _ = hasher.Write(htmlBytes)
	return hex.EncodeToString(hasher.Sum(nil))
}

func completeNoAuthorityRuleEvidence(caseItem Case, runID, attemptID string, evaluations []ConstraintEval, findings []Finding) bool {
	if !noAuthorityCaseScope(caseItem) {
		return false
	}
	var seen [3]bool
	var usedFindingIDs [3]string
	for _, evaluation := range evaluations {
		if !recordNoAuthorityEvaluation(caseItem, runID, attemptID, evaluation, findings, &seen, &usedFindingIDs) {
			return false
		}
	}
	return noAuthorityFindingsComplete(caseItem, runID, attemptID, findings, seen, usedFindingIDs)
}

func noAuthorityCaseScope(item Case) bool {
	return item.BusinessLine == "ENT" && item.Scenario == "telecom_dedicated_line" && item.ContractType == "service"
}

func recordNoAuthorityEvaluation(item Case, runID, attemptID string, evaluation ConstraintEval, findings []Finding, seen *[3]bool, used *[3]string) bool {
	index, ruleID, domain, required := noAuthorityRuleForCapability(evaluation.CapabilityID)
	if !required {
		return !evaluation.Mandatory && !knownNoAuthorityRule(evaluation.RuleID)
	}
	if !validNoAuthorityEvaluation(item, runID, attemptID, evaluation, index, ruleID, domain, *seen) {
		return false
	}
	if !linkedNoAuthorityFindingValid(item, runID, attemptID, evaluation, findings, ruleID, domain) {
		return false
	}
	seen[index] = true
	used[index] = evaluation.FindingID
	return true
}

func knownNoAuthorityRule(ruleID string) bool {
	_, known := noAuthorityCapabilityForRule(ruleID)
	return known
}

func validNoAuthorityEvaluation(item Case, runID, attemptID string, evaluation ConstraintEval, index int, ruleID, domain string, seen [3]bool) bool {
	return !seen[index] && noAuthorityEvaluationIdentityMatches(item, runID, attemptID, evaluation, ruleID, domain) &&
		noAuthorityEvaluationEvidenceMatches(evaluation)
}

func noAuthorityEvaluationIdentityMatches(item Case, runID, attemptID string, evaluation ConstraintEval, ruleID, domain string) bool {
	return evaluation.RuleID == ruleID && evaluation.Domain == domain && evaluation.RunID == runID &&
		evaluation.AttemptID == attemptID && evaluation.CaseID == item.ID && evaluation.RuleVersion == "1"
}

func noAuthorityEvaluationEvidenceMatches(evaluation ConstraintEval) bool {
	return evaluation.Mandatory && evaluation.Relation == "insufficient" &&
		evaluation.ReasonCode == "contractual_authority_missing" && strings.TrimSpace(evaluation.Reason) != "" &&
		len(evaluation.FactIDs) == 0 && len(evaluation.ObjectRefs) == 0 && evaluation.FindingID != ""
}

func linkedNoAuthorityFindingValid(item Case, runID, attemptID string, evaluation ConstraintEval, findings []Finding, ruleID, domain string) bool {
	linked, matches := linkedNoAuthorityFinding(item, runID, attemptID, evaluation.FindingID, findings)
	return matches == 1 && noAuthorityFindingMatchesEvaluation(linked, evaluation, ruleID, domain)
}

func linkedNoAuthorityFinding(item Case, runID, attemptID, findingID string, findings []Finding) (Finding, int) {
	var linked Finding
	matches := 0
	for _, finding := range findings {
		if finding.ID == findingID && finding.RunID == runID && finding.AttemptID == attemptID && finding.CaseID == item.ID {
			linked = finding
			matches++
		}
	}
	return linked, matches
}

func noAuthorityFindingMatchesEvaluation(linked Finding, evaluation ConstraintEval, ruleID, domain string) bool {
	return noAuthorityFindingIdentityMatches(linked, evaluation, ruleID, domain) && noAuthorityFindingClaimMatches(linked)
}

func noAuthorityFindingIdentityMatches(linked Finding, evaluation ConstraintEval, ruleID, domain string) bool {
	return linked.Origin == "rule" && linked.RuleID == ruleID && linked.CapabilityID == evaluation.CapabilityID &&
		linked.Domain == domain && linked.Relation == "insufficient" && linked.DispositionSuggestion == "insufficient_evidence"
}

func noAuthorityFindingClaimMatches(linked Finding) bool {
	return linked.Version >= 1 && strings.TrimSpace(linked.Claim) != "" && linked.Claim == linked.UnverifiedReason &&
		linked.ObjectRef == "" && len(linked.Anchors) == 0
}

func noAuthorityFindingsComplete(item Case, runID, attemptID string, findings []Finding, seen [3]bool, used [3]string) bool {
	for _, finding := range findings {
		if !noAuthorityFindingMatches(item, runID, attemptID, finding, seen, used) {
			return false
		}
	}
	return seen[0] && seen[1] && seen[2]
}

func noAuthorityFindingMatches(item Case, runID, attemptID string, finding Finding, seen [3]bool, used [3]string) bool {
	if finding.Origin != "rule" {
		return true
	}
	index, ruleID, _, requiredCapability := noAuthorityRuleForCapability(finding.CapabilityID)
	capability, requiredRule := noAuthorityCapabilityForRule(finding.RuleID)
	if !requiredCapability {
		return !requiredRule
	}
	return requiredNoAuthorityFindingMatches(item, runID, attemptID, finding, index, ruleID, capability, requiredRule, seen, used)
}

func requiredNoAuthorityFindingMatches(item Case, runID, attemptID string, finding Finding, index int, ruleID, capability string, knownRule bool, seen [3]bool, used [3]string) bool {
	return knownRule && capability == finding.CapabilityID && finding.RuleID == ruleID && seen[index] &&
		used[index] == finding.ID && finding.RunID == runID && finding.AttemptID == attemptID && finding.CaseID == item.ID
}

func noAuthorityRuleForCapability(capability string) (int, string, string, bool) {
	switch capability {
	case "dedicated_line.minimum_bandwidth":
		return 0, "ENT-TECH-BW-001", "technical", true
	case "dedicated_line.monthly_availability":
		return 1, "ENT-TECH-SLA-001", "technical", true
	case "payment.advance_ratio.ceiling":
		return 2, "ENT-COM-ADV-001", "commercial", true
	default:
		return 0, "", "", false
	}
}

func noAuthorityCapabilityForRule(ruleID string) (string, bool) {
	switch ruleID {
	case "ENT-TECH-BW-001":
		return "dedicated_line.minimum_bandwidth", true
	case "ENT-TECH-SLA-001":
		return "dedicated_line.monthly_availability", true
	case "ENT-COM-ADV-001":
		return "payment.advance_ratio.ceiling", true
	default:
		return "", false
	}
}
