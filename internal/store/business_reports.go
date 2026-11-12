// File metadata (manifest mirror):
// Purpose: CreateReport appends immutable JSON and escaped HTML for one exact terminal attempt. Later decisions produce a different report identity and never edit these bytes. Related `store` operations include CreateReport, CreateReportContext, CreateReportWithIdempotency, frozenLegalReportProjection.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-10
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"sort"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/engine"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"

	"github.com/jackc/pgx/v5"
)

const (
	businessReportSchema = "case-report/v1"
	businessReportHTML   = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.CaseTitle}}</title><style>body{font-family:system-ui,sans-serif;max-width:72rem;margin:2rem auto;padding:0 1rem;color:#17212b}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#f4f6f8;padding:1rem;border-radius:.5rem}</style></head><body><h1>{{.CaseTitle}}</h1><p>本报告记录本次技术审查的内容，不构成法律意见或业务审批。模型建议仍需人工核对。</p><h2>案件与审查范围</h2><p>{{.BusinessLine}} / {{.Scenario}} / {{.ContractType}}</p><p>{{.Scope}}</p>{{if .CoverageNotice}}<p>{{.CoverageNotice}}</p>{{end}}<h2>已返回的规则检查</h2>{{if .Evaluations}}<ul>{{range .Evaluations}}<li><strong>{{.Name}}</strong>：{{.Relation}}{{if .Reason}}；{{.Reason}}{{end}}</li>{{end}}</ul>{{else}}<p>未返回规则检查，不能推断已经检查。</p>{{end}}<h2>审查发现</h2>{{if .Findings}}<ul>{{range .Findings}}<li><strong>{{.Origin}}</strong>：{{.Claim}}（{{.Relation}}）{{if .UnverifiedReason}}；尚未核验：{{.UnverifiedReason}}{{end}}</li>{{end}}</ul>{{else}}<p>未返回审查发现，不能据此判断风险已消除。</p>{{end}}<h2>人工决定历史</h2>{{if .Decisions}}<ul>{{range .Decisions}}<li>{{.Action}} · {{.Actor}}{{if .Reason}}：{{.Reason}}{{end}}</li>{{end}}</ul>{{else}}<p>未返回人工决定。</p>{{end}}<details><summary>完整技术详情</summary><pre>{{.ReportJSON}}</pre></details></body></html>`
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
	ctx = commandContext(ctx, user)
	return s.CreateReportWithIdempotency(ctx, user, caseID, runID, attemptID, "")
}

// CreateReportWithIdempotency commits the optional receipt with the frozen report.
func (s *Store) CreateReportWithIdempotency(ctx context.Context, user accounts.User, caseID, runID, attemptID, key string) (ReportRecord, error) {
	ctx = commandContext(ctx, user)
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
	key, err = optionalIdempotencyKey(key)
	if err != nil {
		return ReportRecord{}, err
	}
	route := "POST /api/cases/" + caseID + "/reports"
	digest := ""
	if key != "" {
		digest, err = canonicalHash([]string{caseID, runID, attemptID})
		if err != nil {
			return ReportRecord{}, err
		}
	}
	if receipt, found, err := s.readWriteReceipt(ctx, currentUser, coreWriteReceipts, route, key, digest, access.Export, caseID); err != nil {
		return ReportRecord{}, err
	} else if found {
		var record ReportRecord
		if err := json.Unmarshal(receipt.Response, &record); err != nil || record.ID == "" || record.RunID != runID || record.AttemptID != attemptID {
			return ReportRecord{}, errors.New("invalid report idempotency receipt")
		}
		return record, nil
	}
	st, err := s.snapshotReportState(ctx, currentUser, caseID, runID, attemptID)
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
	runSnapshot := st.snapshotOf(runID)
	precheck := isSingleContractSnapshot(runSnapshot)
	var run Run
	var attempt Attempt
	var input AttemptInput
	if precheck && !isGovernedReviewCase(caseItem) {
		run, attempt, input, err = s.validateSingleContractReport(st, caseItem, runID, attemptID)
	} else {
		run, attempt, input, _, err = s.validateFrozenTerminalAttempt(st, caseItem, runID, attemptID)
	}
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
		if precheck && !isGovernedReviewCase(caseItem) {
			availability = "not_applicable"
			unavailableReason = "本次仅执行单合同预审映射，未执行正式治理规则包。"
			manifestBytes = json.RawMessage("null")
		} else {
			if attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" || attempt.RulePackCompilerRef == "" ||
				!completeNoAuthorityRuleEvidence(caseItem, runID, attemptID, evaluations, findings) {
				return ReportRecord{}, fault.Conflict("缺少已授权 RulePack，且三项强制能力没有完整的授权不足证据")
			}
			availability = "unavailable"
			unavailableReason = "此尝试未冻结可用 RulePack；带宽、月度可用率和预付款三项强制能力均以 insufficient / contractual_authority_missing 记录，采购或中标文件缺少合同依据。"
			manifestBytes = json.RawMessage("null")
		}
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
	if runSnapshot.ReviewScope != nil {
		attempt.ReviewScope = runSnapshot.ReviewScope
		input.ReviewScope = runSnapshot.ReviewScope
	}
	legalReview, err := frozenLegalReportProjection(runSnapshot, runID, attemptID)
	if err != nil {
		return ReportRecord{}, fault.Conflict("所选执行记录保存的法律适用快照已损坏")
	}
	envelope := reportEnvelope{
		SchemaVersion: businessReportSchema, ReportID: id, ReportVersion: nextVersion, CreatedAt: now,
		Notice: "本报告记录技术审查结果，不构成法律意见或业务审批。",
		Case:   reportCase{ID: caseItem.ID, Title: caseItem.Title, ContractType: caseItem.ContractType, BusinessLine: caseItem.BusinessLine, Scenario: caseItem.Scenario},
		Run:    run, Attempt: attempt, Execution: execution, RunSnapshot: runSnapshot, LegalReview: legalReview, Input: input,
		Documents: parsedDocs, Findings: findings, Evaluations: evaluations, Decisions: decisions,
		RulePack: reportRulePack{
			Availability: availability, CompilerRef: attempt.RulePackCompilerRef,
			Hash: attempt.RulePackHash, SnapshotDigest: attempt.RulePackSnapshotDigest,
			Snapshot: manifestBytes, UnavailableReason: unavailableReason,
		},
		InputHash: *attempt.InputHash, OutputHash: *attempt.OutputHash, DecisionSnapshotHash: decisionHash,
		AgentReview: agentReviewJSON, AgentReviewBytes: agentReviewBytes, AgentReviewSHA256: agentReviewDigest,
	}
	if precheck {
		envelope.PrecheckMapping = json.RawMessage(engine.SingleContractPrecheckManifest)
		envelope.CoverageNotice = "仅核对主合同中可识别的付款比例；跨材料比对、正式制度审查及其他风险未执行。应补齐材料后发起完整审查，识别结果仍需人工核对。"
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
	var receipt writeReceipt
	var found bool
	var receiptTx pgx.Tx
	if err := s.scopedCaseWrite(ctx, currentUser, caseID, "", "", true, false, func(tx pgx.Tx) error {
		receiptTx = tx
		if key == "" {
			return nil
		}
		if err := lockWriteReceipt(ctx, tx, currentUser, route, key); err != nil {
			return err
		}
		receipt, found, err = loadWriteReceipt(ctx, tx, coreWriteReceipts, currentUser, route, key)
		return err
	}, func(next *state) error {
		currentUser, err = trustedUser(currentUser)
		if err != nil {
			return err
		}
		currentCase, err := s.requireBusinessWrite(next, currentUser, caseID)
		if err != nil {
			return err
		}
		if err := requireCaseAction(currentCase, currentUser, access.Export); err != nil {
			return err
		}
		if found {
			if err := checkWriteReceipt(receipt, currentCase, digest); err != nil {
				return err
			}
			if err := validateWriteReceiptTarget(ctx, receiptTx, coreWriteReceipts, currentCase, receipt); err != nil {
				return err
			}
			if err := json.Unmarshal(receipt.Response, &record); err != nil || record.ID == "" || record.RunID != runID || record.AttemptID != attemptID {
				return errors.New("invalid report idempotency receipt")
			}
			return nil
		}
		if key != "" {
			unavailable, err := reportReceiptReplayUnavailable(ctx, receiptTx, currentUser, currentCase, route, key)
			if err != nil {
				return err
			}
			if unavailable {
				return fault.Conflict("历史报告请求键缺少可信来源，不能重放；请通过受控查看路径读取既有报告")
			}
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
		if key != "" {
			return insertWriteReceipt(ctx, receiptTx, coreWriteReceipts, currentUser, currentCase, route, key, digest, record)
		}
		return nil
	}); err != nil {
		return ReportRecord{}, err
	}
	return record, nil
}

func frozenLegalReportProjection(snapshot RunSnapshot, runID, attemptID string) (*FrozenLegalReviewProjection, error) {
	knowledge := snapshot.GovernedKnowledge
	if knowledge == nil {
		return nil, nil
	}
	if len(knowledge.LegalAuthorities) == 0 && knowledge.EvidenceCutoffAt == "" && knowledge.EvidenceCutoffMode == "" &&
		knowledge.ExplicitEvidenceCutoffAt == "" && knowledge.LegalReuseFingerprint == "" && knowledge.LegalAuthorities == nil {
		return nil, nil
	}
	if runID == "" || snapshot.RunID != runID || attemptID == "" {
		return nil, errors.New("frozen legal review projection lacks exact run or attempt identity")
	}
	sealed, err := sealKnowledgeSnapshot(*knowledge)
	if err != nil || sealed.SnapshotSHA256 != knowledge.SnapshotSHA256 || !validLegalReuseFingerprint(knowledge) {
		return nil, errors.New("frozen legal review identity is invalid")
	}
	return &FrozenLegalReviewProjection{RunID: runID, AttemptID: attemptID, EvidenceCutoffAt: knowledge.EvidenceCutoffAt, EvidenceCutoffMode: knowledge.EvidenceCutoffMode,
		ExplicitEvidenceCutoffAt: knowledge.ExplicitEvidenceCutoffAt,
		Authorities:              append([]FrozenLegalAuthority{}, knowledge.LegalAuthorities...)}, nil
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
	st, err := s.snapshotCaseReport(context.Background(), currentUser, caseID, reportID)
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
	var envelope reportEnvelope
	if err := json.Unmarshal(reportJSON, &envelope); err != nil {
		return nil, err
	}
	model := reportHTMLModel{
		CaseTitle: caseTitle, ReportJSON: string(reportJSON),
		BusinessLine: reportLabel("business_line", envelope.Case.BusinessLine),
		Scenario:     reportLabel("scenario", envelope.Case.Scenario),
		ContractType: reportLabel("contract_type", envelope.Case.ContractType),
		Scope:        reportLabel("review_scope", ""), CoverageNotice: envelope.CoverageNotice,
	}
	if envelope.RunSnapshot.ReviewScope != nil {
		model.Scope = reportLabel("review_scope", envelope.RunSnapshot.ReviewScope.ID)
	}
	for _, item := range envelope.Evaluations {
		model.Evaluations = append(model.Evaluations, reportHTMLCheck{
			Name: reportLabel("capability", item.CapabilityID), Relation: reportLabel("relation", item.Relation), Reason: item.Reason,
		})
	}
	for _, item := range envelope.Findings {
		model.Findings = append(model.Findings, reportHTMLFinding{
			Origin: reportLabel("origin", item.Origin), Claim: item.Claim, Relation: reportLabel("relation", item.Relation), UnverifiedReason: item.UnverifiedReason,
		})
	}
	for _, item := range envelope.Decisions {
		actor := item.ActorName
		if strings.TrimSpace(actor) == "" {
			actor = "姓名未报告"
		}
		model.Decisions = append(model.Decisions, reportHTMLDecision{Action: reportLabel("decision", item.Action), Actor: actor, Reason: item.Reason})
	}
	tpl, err := template.New("business-report").Parse(businessReportHTML)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := tpl.Execute(&output, model); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// The UI/export contract fixture checks these presentation values against the web labels.
var reportLabelTables = map[string]map[string]string{
	"business_line": {"ENT": "政企业务", "政企业务": "政企业务"},
	"scenario": {"telecom_dedicated_line": "政企专线", "cloud_services": "云服务采购",
		"政企专线": "政企专线", "云服务采购": "云服务采购"},
	"contract_type": {"procurement_ict": "采购 / ICT", "network_engineering": "网络工程",
		"framework": "框架协议", "service": "服务合同", "unspecified": "未分类"},
	"review_scope": {"full_review": "完整审查", "single_contract_precheck": "单合同预审"},
	"capability": {
		"authorization_basis": "授权依据", "commercial_baseline": "商务条款",
		"warranty_months": "质保期限", "acceptance_schedule": "交付与验收时间",
		"counterparty_record": "主体登记信息", "amount": "金额", "party": "合同主体",
		"approval_authority": "审批或授权", "approval_validity": "审批有效期",
		"effective_scope": "服务范围", "scope": "技术与服务范围", "milestone": "履约里程碑",
		"explicit_start_event": "启动时间", "subject_id": "主体标识", "signing_party": "签署方",
		"payee": "收款方", "responsibility": "责任与义务",
		"dedicated_line.minimum_bandwidth": "最低保障带宽", "dedicated_line.monthly_availability": "月可用率",
		"payment.advance_ratio.ceiling": "预付款比例上限", "cloud_services.minimum_bandwidth": "最低保障带宽",
		"cloud_services.monthly_availability": "月可用率", "single_contract.payment_sum": "主合同中可识别的付款比例合计",
	},
	"relation": {"compatible": "一致", "conflict": "冲突", "insufficient": "信息不足"},
	"origin":   {"rule": "规则", "ai": "模型建议", "model": "模型建议", "manual": "人工"},
	"decision": {"confirm_risk": "确认风险", "false_positive": "误报", "insufficient_evidence": "证据不足", "accept_residual": "接受残余风险"},
}

// reportLabel is presentation only. These codes never change the frozen JSON.
func reportLabel(field, value string) string {
	if label, ok := reportLabelTables[field][value]; ok {
		return label
	}
	switch field {
	case "business_line":
		if value == "" || value == "unspecified" {
			return "业务条线未设置"
		}
		return "业务条线待核对"
	case "scenario":
		if value == "" || value == "unspecified" {
			return "业务场景未设置"
		}
		return "业务场景待核对"
	case "review_scope":
		if value == "" {
			return "审查范围身份未返回"
		}
		return "审查范围待核对"
	case "capability":
		if value == "" {
			return "审查项目未报告"
		}
		return "审查项目待核对"
	case "contract_type":
		return "合同类型待核对"
	case "relation":
		return "关系未识别"
	case "origin":
		return "来源待核对"
	case "decision":
		return "决定类型待核对"
	default:
		return "信息待核对"
	}
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
