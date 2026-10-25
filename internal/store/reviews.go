package store

import (
	"context"
	"errors"

	"contractaudit/internal/access"

	"contractaudit/internal/accounts"
	"contractaudit/internal/engine"
	"contractaudit/internal/fault"
	"contractaudit/internal/model"
)

var decisionActions = map[string]string{
	"confirm_risk":          "confirmed",
	"false_positive":        "false_positive",
	"insufficient_evidence": "insufficient_evidence",
	"accept_residual":       "accepted_residual",
}

func sourcesOf(docs []Interpreted) []engine.Source {
	out := make([]engine.Source, len(docs))
	for i, doc := range docs {
		status := "failed"
		if doc.Ready {
			status = "ready"
		}
		out[i] = engine.Source{
			ID: doc.Document.ID, Role: doc.Document.Role, ParseStatus: status,
			Text: doc.Text, FromOCR: doc.FromOCR, IR: doc.IR,
		}
	}
	return out
}

func readyCount(docs []Interpreted) int {
	n := 0
	for _, doc := range docs {
		if doc.Ready {
			n++
		}
	}
	return n
}

func decided(findings []Finding, st *state) bool {
	for _, finding := range findings {
		if len(st.decisions(finding.ID)) > 0 {
			return true
		}
	}
	return false
}

func publicModelError(err error) string {
	switch {
	case errors.Is(err, model.ErrUnusable):
		return "模型输出无法解析成质保结论，已保留可定位的规则结果；模型审查尚未完成。"
	case errors.Is(err, engine.ErrRejected):
		return "模型输出不能对应到材料原文，已保留可定位的规则结果；模型审查尚未完成。"
	case errors.Is(err, errModelNotConfigured):
		return "审查模型未配置，已保留可定位的规则结果；模型审查尚未完成。"
	case errors.Is(err, model.ErrRequest):
		return "调用审查模型失败，已保留可定位的规则结果；模型审查尚未完成。"
	default:
		return "审查模型没有完成质保核对，已保留可定位的规则结果。"
	}
}

func publicAttemptFailure(code string) string {
	switch code {
	case "model_output_unusable":
		return publicModelError(model.ErrUnusable)
	case "model_evidence_rejected":
		return publicModelError(engine.ErrRejected)
	case "model_not_configured":
		return publicModelError(errModelNotConfigured)
	case "model_request_failed":
		return publicModelError(model.ErrRequest)
	case "document_parse_incomplete":
		return "有材料解析失败，结果不能当成完整审查。"
	case "model_timeout", "model_cancelled", "model_incomplete":
		return publicModelError(nil)
	default:
		return "审查未完整完成；可定位的规则结果仍然保留。"
	}
}

// Decide appends a human decision. It does not delete earlier decisions.
func (s *Store) Decide(user accounts.User, caseID, runID, findingID, key, action, reason string, expected int) error {
	if s.ReadOnly() {
		return fault.New(503, "read_only", "只读模式下不能作人工决定")
	}
	if !accounts.CanDecide(user.Role) {
		return fault.Forbidden("当前角色不能作人工决定")
	}
	reason = trimSpaceReason(reason)
	if runeCount(reason) < 2 || runeCount(reason) > 1000 {
		return fault.Invalid("理由需要 2 到 1000 个字")
	}
	disposition, ok := decisionActions[action]
	if !ok {
		return fault.Invalid("决定类型不在本版范围内")
	}
	if expected <= 0 {
		return fault.Invalid("缺少 expected_version")
	}
	if len(key) > 80 {
		return fault.Invalid("幂等键过长")
	}
	return s.updateCaseAs(user, caseID, func(st *state) error {
		if _, err := s.writableCase(st, user, caseID, access.Decide); err != nil {
			return err
		}
		latest, ok := st.latestRun(caseID)
		if !ok || latest.ID != runID {
			return fault.NotFound("没有找到对应记录")
		}
		reviewRun := latest
		if key != "" {
			for _, item := range st.Idempotency {
				if item.ActorID != user.ID || item.Key != key {
					continue
				}
				if item.FindingID == findingID && item.Action == action && item.Reason == reason && item.ExpectedVersion == expected {
					return nil
				}
				return fault.Conflict("这个幂等键已经用于另一次决定")
			}
		}
		latestAttempt, hasAttempt := st.latestAttempt(runID)
		if hasAttempt && attemptActive(latestAttempt.State) {
			return fault.Conflict("审查仍在进行，不能作人工决定")
		}
		if !hasAttempt && attemptActive(reviewRun.State) {
			return fault.Conflict("审查仍在进行，不能作人工决定")
		}
		idx := -1
		for i := range st.Findings {
			if st.Findings[i].ID == findingID && st.Findings[i].RunID == runID && st.Findings[i].CaseID == caseID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fault.NotFound("没有找到对应记录")
		}
		if hasAttempt && st.Findings[idx].AttemptID != latestAttempt.ID {
			return fault.NotFound("没有找到对应记录")
		}
		if st.Findings[idx].Version != expected {
			return fault.Conflict("该事项已被他人更新，请按最新内容重新确认")
		}
		prev := st.decisions(findingID)
		old := "pending"
		if len(prev) > 0 {
			old = prev[len(prev)-1].NewDisposition
		}
		decID, err := newID("dec_")
		if err != nil {
			return err
		}
		now := stamp()
		st.Decisions = append(st.Decisions, Decision{
			ID: decID, FindingID: findingID, RunID: runID, CaseID: caseID,
			ActorID: user.ID, ActorName: user.DisplayName, Action: action, Reason: reason,
			OldDisposition: old, NewDisposition: disposition, CreatedAt: now,
		})
		for i := range st.Cases {
			if st.Cases[i].ID == caseID {
				st.Cases[i].UpdatedAt = now
			}
		}
		if key != "" {
			st.Idempotency = append(st.Idempotency, Idem{
				Key: key, ActorID: user.ID, FindingID: findingID, DecisionID: decID,
				Action: action, Reason: reason, ExpectedVersion: expected,
			})
		}
		seq := 0
		stateName := "awaiting_human"
		view := s.viewRun(st, reviewRun)
		stateName = view.State
		for _, event := range st.Events {
			if event.RunID == runID && event.Seq > seq {
				seq = event.Seq
			}
		}
		st.Events = append(st.Events, Event{RunID: runID, Seq: seq + 1, Type: "decision", State: stateName})
		act, err := newID("act_")
		if err != nil {
			return err
		}
		if err := appendAudit(st, runID, user.ID, "human_decision", []string{caseID, findingID}); err != nil {
			return err
		}
		st.Activity = append(st.Activity, Activity{ID: act, CaseID: caseID, At: now, Summary: user.DisplayName + "记录了一条人工决定"})
		return nil
	})
}

// Revision is one comparison row against the previous run.
// Prior is the previous run's decision. It is not a decision on this run.
type Revision struct {
	Mark              string
	FindingID         string
	PreviousFindingID string
	Domain            string
	Relation          string
	Claim             string
	Anchors           []Anchor
	Prior             *Decision
}

// Workspace is the review screen.
type Workspace struct {
	Case                     Case
	Documents                []Interpreted
	View                     RunView
	Findings                 []Finding
	Evaluations              []ConstraintEval
	Snapshot                 RunSnapshot
	Audit                    []AuditRecord
	PreviousRunID            string
	Revisions                []Revision
	RevisionComparisonReason string
	Execution                *Execution `json:"execution"`
}

func (s *Store) Workspace(user accounts.User, caseID, runID string, selectedAttemptID ...string) (Workspace, error) {
	if len(selectedAttemptID) > 1 {
		return Workspace{}, fault.Invalid("只能选择一个审查尝试")
	}
	selectedID := ""
	if len(selectedAttemptID) == 1 {
		selectedID = selectedAttemptID[0]
	}
	st, err := s.snapshotAs(user)
	if err != nil {
		return Workspace{}, err
	}
	item, err := s.visibleCase(&st, user, caseID)
	if err != nil {
		return Workspace{}, err
	}
	var run Run
	found := false
	for _, item := range st.Runs {
		if item.ID == runID && item.CaseID == caseID {
			run = item
			found = true
		}
	}
	if !found {
		return Workspace{}, fault.NotFound("没有找到对应记录")
	}
	executionView := selectedExecution(st, runID, selectedID)
	if executionView != nil && executionView.Attempt != nil {
		// The five-domain projection is read for this exact attempt only. It is
		// null when no agent-flow record was persisted; it is never derived from
		// the latest run state on GET.
		review, present, reviewErr := s.AgentReviewForAttempt(context.Background(), user, caseID, runID, executionView.Attempt.ID)
		if reviewErr != nil {
			return Workspace{}, reviewErr
		}
		if present {
			executionView.AgentReview = review
		}
	}
	if selectedID != "" && executionView == nil {
		return Workspace{}, fault.NotFound("没有找到对应记录")
	}
	attemptID := ""
	if executionView != nil {
		attemptID = executionView.Attempt.ID
	}
	if attemptID != "" {
		if input, ok := st.attemptInput(attemptID); ok {
			if attempt, exists := st.attemptByID(attemptID); exists && attempt.HistoryKind == "frozen" {
				if input.RunID != runID || input.AttemptID != attemptID || !frozenInputMatchesSnapshot(st.snapshotOf(runID), input.Documents) {
					return Workspace{}, fault.New(409, "parsed_artifact_corrupt", "冻结输入与运行快照不一致")
				}
				hash, hashErr := frozenInputHash(st.snapshotOf(runID), input.Documents)
				if hashErr != nil || hash != input.InputHash || s.verifyFrozenDocuments(st, input.Documents) != nil {
					return Workspace{}, fault.New(409, "parsed_artifact_corrupt", "冻结解析产物完整性校验失败")
				}
			}
		}
	}
	docs := st.documentsForAttempt(run, attemptID)
	prevID, revisions, extra, comparisonReason := s.compareRun(&st, run, docs, attemptID)
	if len(extra) > 0 {
		docs = append(append([]Interpreted{}, docs...), extra...)
	}
	if revisions == nil {
		revisions = []Revision{}
	}
	findings := st.findings(runID)
	evaluations := st.evaluations(runID)
	if attemptID != "" {
		findings = st.findingsForAttempt(runID, attemptID)
		evaluations = st.evaluationsForAttempt(runID, attemptID)
	}
	return Workspace{
		Case: item, Documents: docs,
		View: s.viewRun(&st, run, attemptID), Findings: findings,
		Evaluations:              evaluations,
		Snapshot:                 st.snapshotOf(runID),
		Audit:                    st.audits(runID),
		PreviousRunID:            prevID,
		Revisions:                revisions,
		RevisionComparisonReason: comparisonReason,
		Execution:                executionView,
	}, nil
}

// WorkspaceAttempt reads one exact attempt after rechecking the current directory grants.
func (s *Store) WorkspaceAttempt(user accounts.User, caseID, runID, attemptID string) (Workspace, error) {
	if attemptID == "" {
		return Workspace{}, fault.Invalid("审查尝试不能为空")
	}
	return s.Workspace(user, caseID, runID, attemptID)
}

func (s *Store) DecisionsFor(findingID string) []Decision {
	st := s.snapshot()
	return st.decisions(findingID)
}

func (s *Store) EventsAfter(user accounts.User, caseID, runID string, after int) ([]Event, error) {
	st, err := s.snapshotAs(user)
	if err != nil {
		return nil, err
	}
	if _, err := s.visibleCase(&st, user, caseID); err != nil {
		return nil, err
	}
	found := false
	for _, run := range st.Runs {
		if run.ID == runID && run.CaseID == caseID {
			found = true
		}
	}
	if !found {
		return nil, fault.NotFound("没有找到对应记录")
	}
	var out []Event
	for _, event := range st.runEvents(runID) {
		if event.Seq > after {
			out = append(out, event)
		}
	}
	return out, nil
}

func trimSpaceReason(reason string) string {
	out := []rune(reason)
	start, end := 0, len(out)
	for start < end && isSpace(out[start]) {
		start++
	}
	for end > start && isSpace(out[end-1]) {
		end--
	}
	return string(out[start:end])
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\n' || r == '\t' || r == '\r' || r == '　'
}
