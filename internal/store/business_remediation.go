package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
)

const maxRemediationItems = 32

// CreateRemediation opens a versioned remediation against exact immutable
// finding identities from one frozen terminal attempt.
func (s *Store) CreateRemediation(user accounts.User, caseID string, input RemediationInput) (Remediation, error) {
	if s.ReadOnly() {
		return Remediation{}, fault.New(503, "read_only", "只读模式下不能创建整改")
	}
	currentUser, err := trustedUser(user)
	if err != nil {
		return Remediation{}, err
	}
	if !accounts.CanDecide(currentUser.Role) {
		return Remediation{}, fault.Forbidden("当前角色不能创建整改")
	}
	input.RunID, input.AttemptID = strings.TrimSpace(input.RunID), strings.TrimSpace(input.AttemptID)
	input.AssigneeID, input.Measure = strings.TrimSpace(input.AssigneeID), strings.TrimSpace(input.Measure)
	if input.RunID == "" || input.AttemptID == "" || len(input.FindingIDs) == 0 || len(input.FindingIDs) > maxRemediationItems ||
		!uniqueNonempty(input.FindingIDs) || !validBusinessText(input.Measure, 1000) {
		return Remediation{}, fault.Invalid("整改范围、措施或责任信息无效")
	}
	if input.AssigneeID != "" {
		assignee, ok := accounts.ByID(input.AssigneeID)
		if !ok || !assignee.Enabled || assignee.TenantID != currentUser.TenantID {
			return Remediation{}, fault.Invalid("整改责任人不存在或不属于当前租户")
		}
	}
	dueAt, err := normalizeDueAt(input.DueAt)
	if err != nil {
		return Remediation{}, err
	}
	st, err := s.snapshotAs(currentUser)
	if err != nil {
		return Remediation{}, err
	}
	caseItem, err := s.visibleCase(&st, currentUser, caseID)
	if err != nil {
		return Remediation{}, err
	}
	if err := businessWritable(caseItem); err != nil {
		return Remediation{}, err
	}
	if err := requireCaseAction(caseItem, currentUser, access.Decide); err != nil {
		return Remediation{}, err
	}
	run, attempt, _, _, err := s.validateFrozenTerminalAttempt(st, caseItem, input.RunID, input.AttemptID)
	if err != nil {
		return Remediation{}, err
	}
	if run.CaseID != caseID {
		return Remediation{}, fault.NotFound("没有找到对应审查记录")
	}
	findings := make(map[string]Finding, len(input.FindingIDs))
	for _, finding := range st.findingsForAttempt(input.RunID, input.AttemptID) {
		findings[finding.ID] = finding
	}
	for _, findingID := range input.FindingIDs {
		finding, ok := findings[findingID]
		if !ok || finding.CaseID != caseID || finding.RunID != input.RunID || finding.AttemptID != input.AttemptID {
			return Remediation{}, fault.Invalid("整改只能关联所选执行记录中的风险")
		}
		if _, ok := evaluationsForFinding(st, input.RunID, input.AttemptID, findingID); !ok {
			return Remediation{}, fault.Conflict("该风险缺少本次审查保存的规则检查证据，不能创建整改")
		}
	}
	id, err := newID("rem_")
	if err != nil {
		return Remediation{}, err
	}
	now := stamp()
	record := Remediation{
		ID: id, CaseID: caseID, RunID: run.ID, AttemptID: attempt.ID,
		FindingIDs: append([]string(nil), input.FindingIDs...), AssigneeID: input.AssigneeID,
		DueAt: dueAt, Measure: input.Measure, Status: "open", Version: 1,
		DocumentIDs: []string{}, Reason: "", CreatedAt: now, UpdatedAt: now,
	}
	wrapper := remediationRecord{Record: cloneRemediation(record), TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID}
	wrapper.History = appendRemediationRevision(wrapper.History, record, now)
	if err := s.updateCaseAs(currentUser, caseID, func(next *state) error {
		currentCase, err := s.requireBusinessWrite(next, currentUser, caseID)
		if err != nil {
			return err
		}
		if err := requireCaseAction(currentCase, currentUser, access.Decide); err != nil {
			return err
		}
		if currentRun, ok := next.runByID(input.RunID); !ok || currentRun.CaseID != caseID {
			return fault.Conflict("审查记录刚被更新，不能创建整改")
		}
		if currentAttempt, ok := next.attemptByID(input.AttemptID); !ok || currentAttempt.RunID != input.RunID ||
			currentAttempt.OutputHash == nil || attempt.OutputHash == nil || *currentAttempt.OutputHash != *attempt.OutputHash {
			return fault.Conflict("审查结果刚被更新，不能创建整改")
		}
		for _, findingID := range input.FindingIDs {
			found := false
			for _, finding := range next.findingsForAttempt(input.RunID, input.AttemptID) {
				if finding.ID == findingID && finding.CaseID == caseID {
					found = true
					break
				}
			}
			if !found {
				return fault.Conflict("风险刚被更新，不能创建整改")
			}
		}
		next.Remediations = append(next.Remediations, wrapper)
		for i := range next.Cases {
			if next.Cases[i].ID == caseID {
				next.Cases[i].UpdatedAt = now
			}
		}
		return nil
	}); err != nil {
		return Remediation{}, err
	}
	return cloneRemediation(record), nil
}

// SubmitRemediation fixes the exact current draft IDs for this submission.
func (s *Store) SubmitRemediation(user accounts.User, caseID, itemID string, input RemediationSubmitInput) (Remediation, error) {
	if s.ReadOnly() {
		return Remediation{}, fault.New(503, "read_only", "只读模式下不能提交整改")
	}
	currentUser, err := trustedUser(user)
	if err != nil {
		return Remediation{}, err
	}
	if !accounts.CanDecide(currentUser.Role) {
		return Remediation{}, fault.Forbidden("当前角色不能提交整改")
	}
	itemID = strings.TrimSpace(itemID)
	input.Reason = strings.TrimSpace(input.Reason)
	if itemID == "" || input.ExpectedVersion < 1 || !validBusinessText(input.Reason, 2000) ||
		len(input.DocumentIDs) != 1 || !uniqueNonempty(input.DocumentIDs) {
		return Remediation{}, fault.Invalid("提交整改需要理由、版本和一个明确的修改稿")
	}
	input.DocumentIDs = append([]string(nil), input.DocumentIDs...)
	st, err := s.snapshotAs(currentUser)
	if err != nil {
		return Remediation{}, err
	}
	caseItem, err := s.visibleCase(&st, currentUser, caseID)
	if err != nil {
		return Remediation{}, err
	}
	if err := businessWritable(caseItem); err != nil {
		return Remediation{}, err
	}
	if err := requireCaseAction(caseItem, currentUser, access.Decide); err != nil {
		return Remediation{}, err
	}
	wrapperIndex := remediationIndex(st, currentUser, caseItem, itemID)
	if wrapperIndex < 0 {
		return Remediation{}, fault.NotFound("没有找到对应整改")
	}
	current := cloneRemediation(st.Remediations[wrapperIndex].Record)
	if current.Version != input.ExpectedVersion {
		return Remediation{}, fault.Conflict("整改版本已变化，请刷新后重试")
	}
	if current.Status != "open" && current.Status != "reopened" {
		return Remediation{}, fault.Conflict("当前整改状态不能提交")
	}
	if err := submittedDraftMatches(st, caseID, input.DocumentIDs); err != nil {
		return Remediation{}, err
	}
	now := stamp()
	current.Status = "submitted"
	current.DocumentIDs = append([]string(nil), input.DocumentIDs...)
	current.Reason = input.Reason
	current.VerificationRunID = nil
	current.VerificationAttemptID = nil
	current.Version++
	current.UpdatedAt = now
	if err := s.updateCaseAs(currentUser, caseID, func(next *state) error {
		currentCase, err := s.requireBusinessWrite(next, currentUser, caseID)
		if err != nil {
			return err
		}
		if err := requireCaseAction(currentCase, currentUser, access.Decide); err != nil {
			return err
		}
		index := remediationIndex(*next, currentUser, currentCase, itemID)
		if index < 0 {
			return fault.NotFound("没有找到对应整改")
		}
		stored := &next.Remediations[index]
		if stored.Record.Version != input.ExpectedVersion || stored.Record.Status != "open" && stored.Record.Status != "reopened" {
			return fault.Conflict("整改状态或版本已变化，请刷新后重试")
		}
		if err := submittedDraftMatches(*next, caseID, input.DocumentIDs); err != nil {
			return err
		}
		stored.Record = cloneRemediation(current)
		stored.History = appendRemediationRevision(stored.History, current, now)
		for i := range next.Cases {
			if next.Cases[i].ID == caseID {
				next.Cases[i].UpdatedAt = now
			}
		}
		return nil
	}); err != nil {
		return Remediation{}, err
	}
	return current, nil
}

// VerifyRemediation closes only when the new frozen terminal attempt compares
// every selected capability and object as compatible. Incomparable evidence
// is persisted as reopened and returned as a conflict for human follow-up.
func (s *Store) VerifyRemediation(user accounts.User, caseID, itemID string, input RemediationVerifyInput) (Remediation, error) {
	if s.ReadOnly() {
		return Remediation{}, fault.New(503, "read_only", "只读模式下不能复核整改")
	}
	currentUser, err := trustedUser(user)
	if err != nil {
		return Remediation{}, err
	}
	if !accounts.CanDecide(currentUser.Role) {
		return Remediation{}, fault.Forbidden("当前角色不能复核整改")
	}
	itemID, input.RunID, input.AttemptID = strings.TrimSpace(itemID), strings.TrimSpace(input.RunID), strings.TrimSpace(input.AttemptID)
	input.Reason = strings.TrimSpace(input.Reason)
	if itemID == "" || input.ExpectedVersion < 1 || input.RunID == "" || input.AttemptID == "" || !validBusinessText(input.Reason, 2000) {
		return Remediation{}, fault.Invalid("复核需要新的审查执行记录、理由和预期版本")
	}
	st, err := s.snapshotAs(currentUser)
	if err != nil {
		return Remediation{}, err
	}
	caseItem, err := s.visibleCase(&st, currentUser, caseID)
	if err != nil {
		return Remediation{}, err
	}
	if err := businessWritable(caseItem); err != nil {
		return Remediation{}, err
	}
	if err := requireCaseAction(caseItem, currentUser, access.Decide); err != nil {
		return Remediation{}, err
	}
	index := remediationIndex(st, currentUser, caseItem, itemID)
	if index < 0 {
		return Remediation{}, fault.NotFound("没有找到对应整改")
	}
	current := cloneRemediation(st.Remediations[index].Record)
	if current.Version != input.ExpectedVersion {
		return Remediation{}, fault.Conflict("整改版本已变化，请刷新后重试")
	}
	if current.Status != "submitted" {
		return Remediation{}, fault.Conflict("当前整改状态不能复核")
	}
	compatible, recordVerification, reason := s.assessRemediationVerification(st, caseItem, current, input)
	now := stamp()
	current.Version++
	current.UpdatedAt = now
	current.Reason = input.Reason
	if recordVerification {
		runID, attemptID := input.RunID, input.AttemptID
		current.VerificationRunID = &runID
		current.VerificationAttemptID = &attemptID
	}
	if compatible {
		current.Status = "closed"
	} else {
		current.Status = "reopened"
	}
	if err := s.updateCaseAs(currentUser, caseID, func(next *state) error {
		currentCase, err := s.requireBusinessWrite(next, currentUser, caseID)
		if err != nil {
			return err
		}
		if err := requireCaseAction(currentCase, currentUser, access.Decide); err != nil {
			return err
		}
		currentIndex := remediationIndex(*next, currentUser, currentCase, itemID)
		if currentIndex < 0 {
			return fault.NotFound("没有找到对应整改")
		}
		stored := &next.Remediations[currentIndex]
		if stored.Record.Version != input.ExpectedVersion || stored.Record.Status != "submitted" {
			return fault.Conflict("整改状态或版本已变化，请刷新后重试")
		}
		stored.Record = cloneRemediation(current)
		stored.History = appendRemediationRevision(stored.History, current, now)
		for i := range next.Cases {
			if next.Cases[i].ID == caseID {
				next.Cases[i].UpdatedAt = now
			}
		}
		return nil
	}); err != nil {
		return Remediation{}, err
	}
	if !compatible {
		if reason == "" {
			reason = "复核证据不足或不可比较"
		}
		return Remediation{}, fault.Conflict(reason + "；整改已重新打开")
	}
	return cloneRemediation(current), nil
}

func (s *Store) assessRemediationVerification(st state, caseItem Case, remediation Remediation, input RemediationVerifyInput) (bool, bool, string) {
	originalRun, originalAttempt, originalInput, originalManifest, err := s.validateFrozenTerminalAttempt(st, caseItem, remediation.RunID, remediation.AttemptID)
	if err != nil || originalRun.CaseID != caseItem.ID {
		return false, false, "整改关联的原审查记录不完整，不能关闭"
	}
	verifyRun, verifyAttempt, verifyInput, verifyManifest, err := s.validateFrozenTerminalAttempt(st, caseItem, input.RunID, input.AttemptID)
	if err != nil || verifyRun.CaseID != caseItem.ID || verifyAttempt.ID == originalAttempt.ID {
		return false, false, "需要同一案件中已完成或部分完成的新审查执行记录，不能关闭"
	}
	if verifyAttempt.HistoryKind != "frozen" || originalAttempt.HistoryKind != "frozen" ||
		verifyAttempt.RulePackHash != originalAttempt.RulePackHash || verifyAttempt.RulePackSnapshotDigest != originalAttempt.RulePackSnapshotDigest ||
		verifyAttempt.RulePackCompilerRef != originalAttempt.RulePackCompilerRef ||
		!sameRunConfiguration(st.snapshotOf(originalRun.ID), st.snapshotOf(verifyRun.ID)) ||
		verifyManifest.ID != originalManifest.ID || verifyManifest.Version != originalManifest.Version || verifyManifest.TenantID != originalManifest.TenantID {
		return false, true, "原审查与复核的规则包或执行配置无法比较，不能关闭"
	}
	if !sameRemediationSources(originalInput, verifyInput, remediation.DocumentIDs) {
		return false, true, "提交的修改稿或原始证据缺失、已变化，不能关闭"
	}
	if err := s.verifyFrozenDocuments(st, verifyInput.Documents); err != nil {
		return false, true, "复核所用的材料或解析证据无效，不能关闭"
	}
	originalRules := ruleCapabilities(originalManifest)
	verifyRules := ruleCapabilities(verifyManifest)
	verifyEvals := st.evaluationsForAttempt(input.RunID, input.AttemptID)
	for _, findingID := range remediation.FindingIDs {
		selected, ok := evaluationsForFinding(st, remediation.RunID, remediation.AttemptID, findingID)
		if !ok || len(selected) != 1 {
			return false, true, "原风险缺少唯一对应的检查项目和对象证据，不能关闭"
		}
		before := selected[0]
		capability, ruleOK := originalRules[before.RuleID]
		if !ruleOK || before.RuleID == "" || before.CapabilityID == "" || capability != before.CapabilityID || len(before.ObjectRefs) == 0 || !uniqueNonempty(before.ObjectRefs) {
			return false, true, "原风险缺少当时保存的规则包检查项目或对象编号，不能关闭"
		}
		if verifyRules[before.RuleID] != before.CapabilityID {
			return false, true, "复核使用的规则包没有保留原风险的检查项目，不能关闭"
		}
		matched := 0
		for _, after := range verifyEvals {
			if after.RuleID != before.RuleID || after.RuleVersion != before.RuleVersion || after.CapabilityID != before.CapabilityID {
				continue
			}
			if !sameStringSet(before.ObjectRefs, after.ObjectRefs) {
				continue
			}
			matched++
			if after.Relation != "compatible" {
				return false, true, "相同规则包检查项目和对象的结果仍不一致，不能关闭"
			}
		}
		if matched != 1 {
			return false, true, "复核结果没有唯一匹配原风险的规则包检查项目和对象，不能关闭"
		}
	}
	return true, true, ""
}

func (s *Store) validateFrozenTerminalAttempt(st state, caseItem Case, runID, attemptID string) (Run, Attempt, AttemptInput, rulepack.Manifest, error) {
	run, ok := st.runByID(runID)
	if !ok || run.CaseID != caseItem.ID {
		return Run{}, Attempt{}, AttemptInput{}, rulepack.Manifest{}, fault.Conflict("执行记录不存在，或不属于当前案件")
	}
	attempt, ok := st.attemptByID(attemptID)
	if !ok || attempt.RunID != runID || attempt.FinishedAt == nil ||
		(attempt.State != "completed" && attempt.State != "partial") {
		return Run{}, Attempt{}, AttemptInput{}, rulepack.Manifest{}, fault.Conflict("这次执行尚未形成可比较的最终证据")
	}
	if attempt.HistoryKind != "frozen" || attempt.InputHash == nil || *attempt.InputHash == "" || attempt.OutputHash == nil || *attempt.OutputHash == "" {
		return Run{}, Attempt{}, AttemptInput{}, rulepack.Manifest{}, fault.Conflict("之前的执行记录缺少保存的输入或输出编号")
	}
	input, ok := st.attemptInput(attemptID)
	if !ok || input.RunID != runID || input.AttemptID != attemptID || input.InputHash != *attempt.InputHash {
		return Run{}, Attempt{}, AttemptInput{}, rulepack.Manifest{}, fault.Conflict("这次执行保存的输入不存在或编号不匹配")
	}
	snapshot := st.snapshotOf(runID)
	inputHash, err := frozenInputHash(snapshot, input.Documents)
	if err != nil || inputHash != input.InputHash || !frozenInputMatchesSnapshot(snapshot, input.Documents) ||
		!attemptSelectionsMatchFrozen(st.parseSelectionsForAttempt(attemptID), input.Documents) {
		return Run{}, Attempt{}, AttemptInput{}, rulepack.Manifest{}, fault.Conflict("这次执行保存的输入未通过完整性校验")
	}
	if err := s.verifyFrozenDocuments(st, input.Documents); err != nil {
		return Run{}, Attempt{}, AttemptInput{}, rulepack.Manifest{}, fault.Conflict("这次执行使用的原件或解析结果编号未通过校验")
	}
	reviewDigest, err := s.agentReviewDigestFor(caseItem.TenantID, caseItem.OwnerOrgID, attemptID)
	if err != nil {
		return Run{}, Attempt{}, AttemptInput{}, rulepack.Manifest{}, fault.Conflict("这次执行的智能审查记录未通过校验")
	}
	outputHash, err := attemptOutputHash(st, runID, attemptID, reviewDigest)
	if err != nil || outputHash != *attempt.OutputHash {
		return Run{}, Attempt{}, AttemptInput{}, rulepack.Manifest{}, fault.Conflict("这次执行的规则结果摘要未通过校验")
	}
	manifest, err := validateRemediationRulePack(attempt, caseItem)
	if err != nil {
		return Run{}, Attempt{}, AttemptInput{}, rulepack.Manifest{}, fault.Conflict("之前的执行记录缺少有效的规则包记录")
	}
	return run, attempt, input, manifest, nil
}

func validateRemediationRulePack(attempt Attempt, caseItem Case) (rulepack.Manifest, error) {
	if attempt.RulePackCompilerRef == "" {
		return rulepack.Manifest{}, errors.New("missing rulepack compiler identity")
	}
	if len(attempt.RulePackSnapshot) == 0 {
		if attempt.RulePackHash != "" || attempt.RulePackSnapshotDigest != "" {
			return rulepack.Manifest{}, errors.New("absent RulePack snapshot has invented hashes")
		}
		return rulepack.Manifest{}, nil
	}
	if attempt.RulePackHash == "" || attempt.RulePackSnapshotDigest == "" || !json.Valid(attempt.RulePackSnapshot) {
		return rulepack.Manifest{}, errors.New("missing frozen rulepack identity")
	}
	digest := sha256.Sum256(attempt.RulePackSnapshot)
	if hex.EncodeToString(digest[:]) != attempt.RulePackSnapshotDigest {
		return rulepack.Manifest{}, errors.New("raw rulepack snapshot digest mismatch")
	}
	pack, err := rulepack.Parse(attempt.RulePackSnapshot)
	if err != nil {
		return rulepack.Manifest{}, err
	}
	if pack.Hash() != attempt.RulePackHash {
		return rulepack.Manifest{}, errors.New("normalized rulepack content hash mismatch")
	}
	manifest := pack.Manifest()
	if manifest.TenantID != caseItem.TenantID || manifest.Lifecycle.State != rulepack.StatePublished {
		return rulepack.Manifest{}, errors.New("rulepack scope or lifecycle mismatch")
	}
	for _, rule := range manifest.Rules {
		if rule.TenantID != caseItem.TenantID || rule.BusinessScope.BusinessLine != caseItem.BusinessLine ||
			rule.BusinessScope.Scenario != caseItem.Scenario || rule.BusinessScope.ContractType != caseItem.ContractType {
			return rulepack.Manifest{}, errors.New("rulepack business scope mismatch")
		}
	}
	return manifest, nil
}

func ruleCapabilities(manifest rulepack.Manifest) map[string]string {
	out := make(map[string]string, len(manifest.Rules))
	for _, rule := range manifest.Rules {
		out[rule.RuleID] = rule.CapabilityID
	}
	return out
}

func evaluationsForFinding(st state, runID, attemptID, findingID string) ([]ConstraintEval, bool) {
	var found []ConstraintEval
	for _, evaluation := range st.evaluationsForAttempt(runID, attemptID) {
		if evaluation.FindingID == findingID {
			found = append(found, evaluation)
		}
	}
	return found, len(found) != 0
}

func sameRemediationSources(original, verification AttemptInput, submittedDraftIDs []string) bool {
	if len(submittedDraftIDs) == 0 || !uniqueNonempty(submittedDraftIDs) {
		return false
	}
	before := make(map[string]FrozenDocument, len(original.Documents))
	for _, frozen := range original.Documents {
		before[frozen.Document.ID] = frozen
	}
	after := make(map[string]FrozenDocument, len(verification.Documents))
	for _, frozen := range verification.Documents {
		if !frozen.Ready || frozen.ParseError != "" || frozen.ParsedDocumentID == "" {
			return false
		}
		after[frozen.Document.ID] = frozen
	}
	submitted := make(map[string]bool, len(submittedDraftIDs))
	for _, id := range submittedDraftIDs {
		frozen, ok := after[id]
		if !ok || frozen.Document.Role != "draft_contract" || frozen.Document.ID == "" || frozen.OriginalHash != frozen.Document.Digest {
			return false
		}
		submitted[id] = true
	}
	for id, frozen := range after {
		if frozen.Document.Role == "draft_contract" && !submitted[id] {
			return false
		}
	}
	for id, frozen := range before {
		if frozen.Document.Role == "draft_contract" {
			continue
		}
		current, ok := after[id]
		if !ok || current.Document.Digest != frozen.Document.Digest || current.ParsedDocumentID != frozen.ParsedDocumentID ||
			current.ParsedArtifactDigest != frozen.ParsedArtifactDigest || current.OriginalHash != frozen.OriginalHash {
			return false
		}
	}
	return true
}

func remediationIndex(st state, user accounts.User, caseItem Case, itemID string) int {
	for i := range st.Remediations {
		item := st.Remediations[i]
		if item.Record.ID == itemID && item.Record.CaseID == caseItem.ID && item.TenantID == caseItem.TenantID && item.OrgID == caseItem.OwnerOrgID {
			if caseReadAllowed(caseItem, user) {
				return i
			}
		}
	}
	return -1
}

func submittedDraftMatches(st state, caseID string, ids []string) error {
	if len(ids) != 1 || !uniqueNonempty(ids) {
		return fault.Invalid("整改提交必须绑定一个修改稿版本")
	}
	currentDraft := ""
	for _, doc := range currentDocuments(st.documents(caseID)) {
		if doc.Role == "draft_contract" {
			currentDraft = doc.ID
			break
		}
	}
	if currentDraft == "" || ids[0] != currentDraft {
		return fault.Conflict("提交的修改稿不是案件当前版本")
	}
	for _, doc := range st.documents(caseID) {
		if doc.ID == ids[0] && doc.Role == "draft_contract" && doc.Digest != "" {
			return nil
		}
	}
	return fault.Invalid("修改稿版本不存在或角色无效")
}

func appendRemediationRevision(history []remediationRevision, record Remediation, at string) []remediationRevision {
	return append(history, remediationRevision{Record: cloneRemediation(record), RecordedAt: at})
}

func cloneRemediation(record Remediation) Remediation {
	// History revisions are compared to the current projection with
	// reflect.DeepEqual, so an absent (nil) reference set must not become an
	// empty one and vice versa. Only a present set is copied.
	if record.FindingIDs != nil {
		record.FindingIDs = append([]string{}, record.FindingIDs...)
	}
	if record.DocumentIDs != nil {
		record.DocumentIDs = append([]string{}, record.DocumentIDs...)
	}
	if record.VerificationRunID != nil {
		value := *record.VerificationRunID
		record.VerificationRunID = &value
	}
	if record.VerificationAttemptID != nil {
		value := *record.VerificationAttemptID
		record.VerificationAttemptID = &value
	}
	return record
}

func normalizeDueAt(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	dueAt, err := time.Parse(time.RFC3339, value)
	if err != nil || !dueAt.After(time.Now().UTC()) {
		return "", fault.Invalid("整改期限必须是未来的 RFC3339 时间")
	}
	return dueAt.UTC().Format(timeLayout), nil
}

func validBusinessText(value string, maxRunes int) bool {
	return strings.TrimSpace(value) != "" && runeCount(strings.TrimSpace(value)) <= maxRunes
}

// uniqueNonempty reports whether values is a non-empty set of distinct,
// non-blank references. An absent reference set is never a match.
func uniqueNonempty(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) || !uniqueNonempty(left) || !uniqueNonempty(right) {
		return false
	}
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for i := range len(leftCopy) {
		if leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
}
