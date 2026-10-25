package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

func validateWorkbenchHistory(previous, next state) error {
	oldProfiles := make(map[string]caseProfileRecord, len(previous.Profiles))
	seenNumbers := make(map[string]string, len(next.Profiles))
	seenCases := make(map[string]bool, len(next.Profiles))
	for _, item := range previous.Profiles {
		oldProfiles[item.Profile.CaseID] = item
	}
	for _, item := range next.Profiles {
		profile := item.Profile
		if profile.CaseID == "" || profile.Number == "" || profile.Version < 1 || !validBusinessText(profile.Customer, 200) ||
			!validBusinessText(profile.Supplier, 200) || !validBusinessText(profile.ContractCategory, 120) || profile.OwnerID == "" {
			return errors.New("case profile is incomplete")
		}
		if seenCases[profile.CaseID] {
			return errors.New("case profile is duplicated")
		}
		seenCases[profile.CaseID] = true
		key := item.TenantID + "\x00" + strings.ToLower(profile.Number)
		if owner, exists := seenNumbers[key]; exists && owner != profile.CaseID {
			return errors.New("case profile number is duplicated within the tenant")
		}
		seenNumbers[key] = profile.CaseID
		prior, existed := oldProfiles[profile.CaseID]
		if reflect.DeepEqual(prior, item) {
			continue
		}
		if existed {
			if item.TenantID != prior.TenantID || item.OrgID != prior.OrgID || profile.Version != prior.Profile.Version+1 {
				return errors.New("case profile scope or version changed unexpectedly")
			}
		} else if profile.Version != 1 {
			return errors.New("new case profile must begin at version one")
		}
	}
	if len(next.Profiles) != len(previous.Profiles) && len(next.Profiles) < len(previous.Profiles) {
		return errors.New("case profile was removed")
	}

	oldTasks := make(map[string]workbenchTaskRecord, len(previous.Tasks))
	for _, item := range previous.Tasks {
		oldTasks[item.Task.ID] = item
	}
	seenTasks := make(map[string]bool, len(next.Tasks))
	for _, item := range next.Tasks {
		task := item.Task
		if task.ID == "" || task.CaseID == "" || task.Version < 1 || !validBusinessText(task.Title, 200) ||
			!validTaskKind(task.Kind) || !validTaskStatus(task.Status) || !validTaskPriority(task.Priority) || task.AssigneeID == "" || task.CreatedAt == "" || task.UpdatedAt == "" {
			return errors.New("workbench task is incomplete")
		}
		if seenTasks[task.ID] {
			return errors.New("workbench task is duplicated")
		}
		seenTasks[task.ID] = true
		prior, existed := oldTasks[task.ID]
		if reflect.DeepEqual(prior, item) {
			continue
		}
		if existed {
			if item.TenantID != prior.TenantID || item.OrgID != prior.OrgID || task.CaseID != prior.Task.CaseID ||
				task.CreatedAt != prior.Task.CreatedAt || !reflect.DeepEqual(task.CreatedBy, prior.Task.CreatedBy) || task.Version != prior.Task.Version+1 {
				return errors.New("workbench task scope, creator, or version changed unexpectedly")
			}
		} else if task.Version != 1 || task.CreatedBy == nil || strings.TrimSpace(*task.CreatedBy) == "" {
			return errors.New("new workbench task must begin at version one with a creator")
		}
	}
	if len(next.Tasks) < len(previous.Tasks) {
		return errors.New("workbench task was removed")
	}

	oldRelations := make(map[string]caseRelationRecord, len(previous.Relations))
	seenRelations := make(map[string]bool, len(next.Relations))
	for _, item := range previous.Relations {
		oldRelations[item.Relation.ID] = item
	}
	for _, item := range next.Relations {
		relation := item.Relation
		if relation.ID == "" || relation.CaseID == "" || relation.RelatedCaseID == "" || relation.CaseID == relation.RelatedCaseID ||
			!validBusinessText(relation.Reason, 1000) || relation.CreatedBy == "" || relation.CreatedAt == "" || item.RelatedOrgID == "" || item.PairKey == "" {
			return errors.New("case relation is incomplete")
		}
		if seenRelations[relation.ID] {
			return errors.New("case relation is duplicated")
		}
		seenRelations[relation.ID] = true
		if prior, existed := oldRelations[relation.ID]; existed && !reflect.DeepEqual(prior, item) {
			return errors.New("case relation is immutable")
		}
	}
	return nil
}

func validTaskKind(kind string) bool {
	switch kind {
	case "review", "decision", "remediation", "general":
		return true
	default:
		return false
	}
}

func validTaskStatus(status string) bool {
	switch status {
	case "open", "in_progress", "completed":
		return true
	default:
		return false
	}
}

func validTaskPriority(priority string) bool {
	switch priority {
	case "high", "medium", "low":
		return true
	default:
		return false
	}
}

func (s *Store) UpdateCaseProfile(user accounts.User, caseID string, input CaseProfileInput) (CaseProfile, error) {
	if s.ReadOnly() {
		return CaseProfile{}, fault.New(503, "read_only", "只读模式下不能更新案件资料")
	}
	current, err := trustedUser(user)
	if err != nil {
		return CaseProfile{}, err
	}
	caseID = strings.TrimSpace(caseID)
	input.Number, input.Customer, input.Supplier = strings.TrimSpace(input.Number), strings.TrimSpace(input.Customer), strings.TrimSpace(input.Supplier)
	input.ContractCategory, input.OwnerID = strings.TrimSpace(input.ContractCategory), strings.TrimSpace(input.OwnerID)
	if caseID == "" || input.ExpectedVersion < 0 || input.Number == "" || runeCount(input.Number) > 64 ||
		!validBusinessText(input.Customer, 200) || !validBusinessText(input.Supplier, 200) ||
		!validBusinessText(input.ContractCategory, 120) || input.OwnerID == "" {
		return CaseProfile{}, fault.Invalid("案件资料字段无效")
	}
	dueAt, err := normalizeNullableRFC3339(input.DueAt)
	if err != nil {
		return CaseProfile{}, fault.Invalid("案件期限必须是RFC3339时间")
	}
	st, err := s.snapshotAs(current)
	if err != nil {
		return CaseProfile{}, err
	}
	item, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return CaseProfile{}, err
	}
	if err := businessWritable(item); err != nil {
		return CaseProfile{}, err
	}
	if err := requireCaseAction(item, current, access.Manage); err != nil {
		return CaseProfile{}, err
	}
	if !isCaseMember(item, input.OwnerID) || !enabledTenantUser(input.OwnerID, current.TenantID) {
		return CaseProfile{}, fault.Invalid("案件负责人必须是当前租户内已启用的案件成员")
	}
	prior, exists := profileFor(st, item)
	if exists && prior.Version != input.ExpectedVersion || !exists && input.ExpectedVersion != 0 {
		return CaseProfile{}, fault.Conflict("案件资料版本已变化，请刷新后重试")
	}
	for _, other := range st.Profiles {
		if other.Profile.CaseID != caseID && other.TenantID == item.TenantID && strings.EqualFold(other.Profile.Number, input.Number) {
			return CaseProfile{}, fault.Conflict("案件编号已被当前租户使用")
		}
	}
	now := stamp()
	version := 1
	if exists {
		version = prior.Version + 1
	}
	profile := CaseProfile{CaseID: caseID, Number: input.Number, Customer: input.Customer, Supplier: input.Supplier,
		ContractCategory: input.ContractCategory, OwnerID: input.OwnerID, DueAt: dueAt, Version: version, UpdatedAt: now}
	record := caseProfileRecord{Profile: profile, TenantID: item.TenantID, OrgID: item.OwnerOrgID}
	if err := s.updateCaseAs(current, caseID, func(next *state) error {
		currentCase, err := s.requireBusinessWrite(next, current, caseID)
		if err != nil {
			return err
		}
		if err := requireCaseAction(currentCase, current, access.Manage); err != nil {
			return err
		}
		prior, exists := profileFor(*next, currentCase)
		if exists && prior.Version != input.ExpectedVersion || !exists && input.ExpectedVersion != 0 {
			return fault.Conflict("案件资料版本已变化，请刷新后重试")
		}
		for _, other := range next.Profiles {
			if other.Profile.CaseID != caseID && other.TenantID == currentCase.TenantID && strings.EqualFold(other.Profile.Number, input.Number) {
				return fault.Conflict("案件编号已被当前租户使用")
			}
		}
		if exists {
			for i := range next.Profiles {
				if next.Profiles[i].Profile.CaseID == caseID {
					next.Profiles[i] = record
					break
				}
			}
		} else {
			next.Profiles = append(next.Profiles, record)
		}
		if err := appendCaseWorkbenchActivity(next, current, currentCase, "更新了案件资料", "case.profile.updated", now); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return CaseProfile{}, err
	}
	return profile, nil
}

func (s *Store) CreateWorkbenchTask(user accounts.User, caseID string, input WorkbenchTaskInput) (WorkbenchTask, error) {
	if s.ReadOnly() {
		return WorkbenchTask{}, fault.New(503, "read_only", "只读模式下不能创建任务")
	}
	current, err := trustedUser(user)
	if err != nil {
		return WorkbenchTask{}, err
	}
	caseID = strings.TrimSpace(caseID)
	task, err := normalizeWorkbenchTaskInput(input, current, caseID, false)
	if err != nil {
		return WorkbenchTask{}, err
	}
	st, err := s.snapshotAs(current)
	if err != nil {
		return WorkbenchTask{}, err
	}
	item, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return WorkbenchTask{}, err
	}
	if err := businessWritable(item); err != nil {
		return WorkbenchTask{}, err
	}
	if err := requireCaseAction(item, current, access.Manage); err != nil {
		return WorkbenchTask{}, err
	}
	if err := validateTaskReferences(st, item, task, current); err != nil {
		return WorkbenchTask{}, err
	}
	id, err := newID("task_")
	if err != nil {
		return WorkbenchTask{}, err
	}
	now := stamp()
	task.ID, task.CaseID, task.Version, task.CreatedAt, task.UpdatedAt, task.CreatedBy = id, caseID, 1, now, now, stringPointer(current.ID)
	record := workbenchTaskRecord{Task: task, TenantID: item.TenantID, OrgID: item.OwnerOrgID}
	if err := s.updateCaseAs(current, caseID, func(next *state) error {
		currentCase, err := s.requireBusinessWrite(next, current, caseID)
		if err != nil {
			return err
		}
		if err := requireCaseAction(currentCase, current, access.Manage); err != nil {
			return err
		}
		if err := validateTaskReferences(*next, currentCase, task, current); err != nil {
			return err
		}
		next.Tasks = append(next.Tasks, record)
		return appendCaseWorkbenchActivity(next, current, currentCase, "创建了任务："+task.Title, "case.task.created", now)
	}); err != nil {
		return WorkbenchTask{}, err
	}
	return task, nil
}

func (s *Store) UpdateWorkbenchTask(user accounts.User, caseID, taskID string, input WorkbenchTaskInput) (WorkbenchTask, error) {
	if s.ReadOnly() {
		return WorkbenchTask{}, fault.New(503, "read_only", "只读模式下不能更新任务")
	}
	current, err := trustedUser(user)
	if err != nil {
		return WorkbenchTask{}, err
	}
	caseID, taskID = strings.TrimSpace(caseID), strings.TrimSpace(taskID)
	task, err := normalizeWorkbenchTaskInput(input, current, caseID, true)
	if err != nil {
		return WorkbenchTask{}, err
	}
	if taskID == "" || input.ExpectedVersion < 1 {
		return WorkbenchTask{}, fault.Invalid("任务编号或预期版本无效")
	}
	st, err := s.snapshotAs(current)
	if err != nil {
		return WorkbenchTask{}, err
	}
	item, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return WorkbenchTask{}, err
	}
	if err := businessWritable(item); err != nil {
		return WorkbenchTask{}, err
	}
	if err := requireCaseAction(item, current, access.Manage); err != nil {
		return WorkbenchTask{}, err
	}
	prior, exists := taskFor(st, item, taskID)
	if !exists {
		return WorkbenchTask{}, fault.NotFound("没有找到对应任务")
	}
	if prior.Version != input.ExpectedVersion {
		return WorkbenchTask{}, fault.Conflict("任务版本已变化，请刷新后重试")
	}
	task.ID, task.CaseID, task.Version, task.CreatedAt, task.CreatedBy = prior.ID, caseID, prior.Version+1, prior.CreatedAt, prior.CreatedBy
	task.UpdatedAt = stamp()
	record := workbenchTaskRecord{Task: task, TenantID: item.TenantID, OrgID: item.OwnerOrgID}
	if err := s.updateCaseAs(current, caseID, func(next *state) error {
		currentCase, err := s.requireBusinessWrite(next, current, caseID)
		if err != nil {
			return err
		}
		if err := requireCaseAction(currentCase, current, access.Manage); err != nil {
			return err
		}
		index := taskRecordIndex(*next, currentCase, taskID)
		if index < 0 || next.Tasks[index].Task.Version != input.ExpectedVersion {
			return fault.Conflict("任务版本已变化，请刷新后重试")
		}
		if err := validateTaskReferences(*next, currentCase, task, current); err != nil {
			return err
		}
		next.Tasks[index] = record
		return appendCaseWorkbenchActivity(next, current, currentCase, "更新了任务："+task.Title, "case.task.updated", task.UpdatedAt)
	}); err != nil {
		return WorkbenchTask{}, err
	}
	return task, nil
}

func normalizeWorkbenchTaskInput(input WorkbenchTaskInput, user accounts.User, caseID string, update bool) (WorkbenchTask, error) {
	task := WorkbenchTask{Title: strings.TrimSpace(input.Title), Kind: strings.TrimSpace(input.Kind), AssigneeID: strings.TrimSpace(input.AssigneeID),
		Status: strings.TrimSpace(input.Status), Priority: strings.TrimSpace(input.Priority), FindingID: trimOptionalString(input.FindingID)}
	if !validBusinessText(task.Title, 200) || !validTaskKind(task.Kind) || !validTaskStatus(task.Status) || !validTaskPriority(task.Priority) || task.AssigneeID == "" {
		return WorkbenchTask{}, fault.Invalid("任务内容、类型、状态、优先级或责任人无效")
	}
	if update && input.ExpectedVersion < 1 {
		return WorkbenchTask{}, fault.Invalid("任务更新需要预期版本")
	}
	if !update && input.ExpectedVersion != 0 {
		return WorkbenchTask{}, fault.Invalid("新任务不能指定预期版本")
	}
	dueAt, err := normalizeNullableRFC3339(input.DueAt)
	if err != nil {
		return WorkbenchTask{}, fault.Invalid("任务期限必须是RFC3339时间")
	}
	task.DueAt = dueAt
	return task, nil
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func validateTaskReferences(st state, item Case, task WorkbenchTask, user accounts.User) error {
	if !enabledTenantUser(task.AssigneeID, user.TenantID) || !isCaseMember(item, task.AssigneeID) {
		return fault.Invalid("任务责任人必须是当前租户内已启用的案件成员")
	}
	if task.FindingID == nil {
		return nil
	}
	for _, finding := range st.Findings {
		if finding.ID == *task.FindingID && finding.CaseID == item.ID {
			return nil
		}
	}
	return fault.Invalid("任务引用的风险不存在或不属于当前案件")
}

func (s *Store) CreateCaseRelation(user accounts.User, caseID string, input CaseRelationInput) (CaseRelation, error) {
	if s.ReadOnly() {
		return CaseRelation{}, fault.New(503, "read_only", "只读模式下不能关联案件")
	}
	current, err := trustedUser(user)
	if err != nil {
		return CaseRelation{}, err
	}
	caseID, input.RelatedCaseID = strings.TrimSpace(caseID), strings.TrimSpace(input.RelatedCaseID)
	input.Reason = strings.TrimSpace(input.Reason)
	if caseID == "" || input.RelatedCaseID == "" || caseID == input.RelatedCaseID || !validBusinessText(input.Reason, 1000) {
		return CaseRelation{}, fault.Invalid("关联案件或说明无效")
	}
	st, err := s.snapshotAs(current)
	if err != nil {
		return CaseRelation{}, err
	}
	item, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return CaseRelation{}, err
	}
	if err := businessWritable(item); err != nil {
		return CaseRelation{}, err
	}
	if err := requireCaseAction(item, current, access.Manage); err != nil {
		return CaseRelation{}, err
	}
	related, err := s.visibleCase(&st, current, input.RelatedCaseID)
	if err != nil {
		return CaseRelation{}, err
	}
	key := relationPairKey(item, related)
	for _, record := range st.Relations {
		if record.TenantID == item.TenantID && record.PairKey == key {
			return CaseRelation{}, fault.Conflict("案件关联已存在")
		}
	}
	id, err := newID("rel_")
	if err != nil {
		return CaseRelation{}, err
	}
	now := stamp()
	relation := CaseRelation{ID: id, CaseID: caseID, RelatedCaseID: related.ID, Reason: input.Reason, CreatedBy: current.ID, CreatedAt: now}
	record := caseRelationRecord{Relation: relation, TenantID: item.TenantID, OrgID: item.OwnerOrgID, RelatedOrgID: related.OwnerOrgID, PairKey: key}
	if err := s.updateCaseAs(current, caseID, func(next *state) error {
		currentCase, err := s.requireBusinessWrite(next, current, caseID)
		if err != nil {
			return err
		}
		if err := requireCaseAction(currentCase, current, access.Manage); err != nil {
			return err
		}
		currentRelated, err := s.visibleCase(next, current, related.ID)
		if err != nil {
			return err
		}
		for _, existing := range next.Relations {
			if existing.TenantID == currentCase.TenantID && existing.PairKey == key {
				return fault.Conflict("案件关联已存在")
			}
		}
		record.RelatedOrgID = currentRelated.OwnerOrgID
		next.Relations = append(next.Relations, record)
		return appendCaseWorkbenchActivity(next, current, currentCase, "关联了案件 "+related.Title, "case.relation.created", now)
	}); err != nil {
		return CaseRelation{}, err
	}
	return relation, nil
}

func (s *Store) DeleteCaseRelation(user accounts.User, caseID, relationID string) error {
	if s.ReadOnly() {
		return fault.New(503, "read_only", "只读模式下不能删除案件关联")
	}
	current, err := trustedUser(user)
	if err != nil {
		return err
	}
	caseID, relationID = strings.TrimSpace(caseID), strings.TrimSpace(relationID)
	if caseID == "" || relationID == "" {
		return fault.Invalid("案件或关联编号无效")
	}
	st, err := s.snapshotAs(current)
	if err != nil {
		return err
	}
	item, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return err
	}
	if err := businessWritable(item); err != nil {
		return err
	}
	if err := requireCaseAction(item, current, access.Manage); err != nil {
		return err
	}
	index := relationRecordIndex(st, item, relationID)
	if index < 0 {
		return fault.NotFound("没有找到对应案件关联")
	}
	related, err := s.visibleCase(&st, current, st.Relations[index].Relation.RelatedCaseID)
	if err != nil {
		return err
	}
	now := stamp()
	return s.updateCaseAs(current, caseID, func(next *state) error {
		currentCase, err := s.requireBusinessWrite(next, current, caseID)
		if err != nil {
			return err
		}
		if err := requireCaseAction(currentCase, current, access.Manage); err != nil {
			return err
		}
		currentRelated, err := s.visibleCase(next, current, related.ID)
		if err != nil {
			return err
		}
		currentIndex := relationRecordIndex(*next, currentCase, relationID)
		if currentIndex < 0 || next.Relations[currentIndex].RelatedOrgID != currentRelated.OwnerOrgID {
			return fault.Conflict("案件关联刚被修改，请刷新后重试")
		}
		next.Relations = append(next.Relations[:currentIndex], next.Relations[currentIndex+1:]...)
		return appendCaseWorkbenchActivity(next, current, currentCase, "取消了与案件 "+currentRelated.Title+" 的关联", "case.relation.deleted", now)
	})
}

func (s *Store) CreateKnowledge(user accounts.User, input KnowledgeInput) (Knowledge, error) {
	if s.ReadOnly() {
		return Knowledge{}, fault.New(503, "read_only", "只读模式下不能保存制度")
	}
	current, err := trustedUser(user)
	if err != nil {
		return Knowledge{}, err
	}
	if !accounts.CanCreate(current.Role) {
		return Knowledge{}, fault.Forbidden("当前角色不能写入知识制度")
	}
	input.ID, input.Title, input.Version = strings.TrimSpace(input.ID), strings.TrimSpace(input.Title), strings.TrimSpace(input.Version)
	if input.ID == "" {
		input.ID, err = newID("knowledge_")
		if err != nil {
			return Knowledge{}, err
		}
	}
	if !validResourceID(input.ID) || !validBusinessText(input.Title, 200) || !validResourceID(input.Version) ||
		!validBusinessText(input.Body, 30000) || input.SourceType != "synthetic_policy" && input.SourceType != "reference" {
		return Knowledge{}, fault.Invalid("制度标识、标题、版本、来源或正文无效")
	}
	effectiveAt, err := normalizeNullableRFC3339(input.EffectiveAt)
	if err != nil {
		return Knowledge{}, fault.Invalid("生效时间必须是RFC3339时间")
	}
	st, err := s.snapshotAs(current)
	if err != nil {
		return Knowledge{}, err
	}
	for _, record := range st.Knowledge {
		if record.TenantID != current.TenantID || record.Item.ID != input.ID || record.Item.Version != input.Version {
			continue
		}
		candidate := Knowledge{ID: input.ID, Title: input.Title, Version: input.Version, Body: input.Body,
			SourceType: input.SourceType, EffectiveAt: effectiveAt, CreatedBy: current.ID}
		if sameKnowledgeContent(record.Item, candidate) {
			return record.Item, nil
		}
		return Knowledge{}, fault.Conflict("同一制度ID和版本已经保存了不同内容")
	}
	id := input.ID
	now := stamp()
	item := Knowledge{ID: id, Title: input.Title, Version: input.Version, Body: input.Body,
		SourceType: input.SourceType, EffectiveAt: effectiveAt, CreatedBy: current.ID, CreatedAt: now}
	record := knowledgeRecord{Item: item, TenantID: current.TenantID, OrgID: current.OrgID}
	if err := s.updateAs(current, func(next *state) error {
		for _, existing := range next.Knowledge {
			if existing.TenantID == current.TenantID && existing.Item.ID == item.ID && existing.Item.Version == item.Version {
				if sameKnowledgeContent(existing.Item, item) {
					return errKnowledgeDuplicate
				}
				return fault.Conflict("同一制度ID和版本已经保存了不同内容")
			}
		}
		next.Knowledge = append(next.Knowledge, record)
		return appendWorkbenchAudit(next, current, nil, "knowledge.created", "保存制度 "+item.Title+" v"+item.Version, now)
	}); err != nil {
		if errors.Is(err, errKnowledgeDuplicate) {
			latest, readErr := s.GetKnowledge(current, item.ID)
			if readErr == nil && latest.Version == item.Version && sameKnowledgeContent(latest, item) {
				return latest, nil
			}
		}
		return Knowledge{}, err
	}
	return item, nil
}

var errKnowledgeDuplicate = errors.New("identical knowledge version already stored")

func (s *Store) GetKnowledge(user accounts.User, knowledgeID string) (Knowledge, error) {
	current, err := trustedUser(user)
	if err != nil {
		return Knowledge{}, err
	}
	knowledgeID = strings.TrimSpace(knowledgeID)
	if knowledgeID == "" {
		return Knowledge{}, fault.Invalid("制度编号不能为空")
	}
	st, err := s.snapshotAs(current)
	if err != nil {
		return Knowledge{}, err
	}
	var latest *Knowledge
	for i := range st.Knowledge {
		item := &st.Knowledge[i]
		if item.TenantID != current.TenantID || item.Item.ID != knowledgeID {
			continue
		}
		if latest == nil || item.Item.CreatedAt > latest.CreatedAt {
			value := item.Item
			latest = &value
		}
	}
	if latest == nil {
		return Knowledge{}, fault.NotFound("没有找到对应制度")
	}
	return *latest, nil
}

func sameKnowledgeContent(left, right Knowledge) bool {
	left.CreatedBy, left.CreatedAt = "", ""
	right.CreatedBy, right.CreatedAt = "", ""
	return reflect.DeepEqual(left, right)
}

func profileFor(st state, item Case) (CaseProfile, bool) {
	for _, profile := range st.Profiles {
		if profile.Profile.CaseID == item.ID && profile.TenantID == item.TenantID && profile.OrgID == item.OwnerOrgID {
			return profile.Profile, true
		}
	}
	return CaseProfile{}, false
}

func taskFor(st state, item Case, id string) (WorkbenchTask, bool) {
	for _, task := range st.Tasks {
		if task.Task.ID == id && task.Task.CaseID == item.ID && task.TenantID == item.TenantID && task.OrgID == item.OwnerOrgID {
			return task.Task, true
		}
	}
	return WorkbenchTask{}, false
}

func taskRecordIndex(st state, item Case, id string) int {
	for i := range st.Tasks {
		if st.Tasks[i].Task.ID == id && st.Tasks[i].Task.CaseID == item.ID && st.Tasks[i].TenantID == item.TenantID && st.Tasks[i].OrgID == item.OwnerOrgID {
			return i
		}
	}
	return -1
}

func relationRecordIndex(st state, item Case, id string) int {
	for i := range st.Relations {
		if st.Relations[i].Relation.ID == id && st.Relations[i].Relation.CaseID == item.ID && st.Relations[i].TenantID == item.TenantID && st.Relations[i].OrgID == item.OwnerOrgID {
			return i
		}
	}
	return -1
}

func enabledTenantUser(userID, tenantID string) bool {
	user, ok := accounts.ByID(userID)
	return ok && user.Enabled && accounts.Tenant(user) == tenantID
}

func isCaseMember(item Case, userID string) bool {
	if item.OwnerID == userID {
		return true
	}
	for _, memberID := range item.MemberIDs {
		if memberID == userID {
			return true
		}
	}
	return false
}

func normalizeNullableRFC3339(value *string) (*string, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*value))
	if err != nil {
		return nil, err
	}
	canonical := parsed.UTC().Format(time.RFC3339Nano)
	return &canonical, nil
}

func appendCaseWorkbenchActivity(st *state, user accounts.User, item Case, summary, action, at string) error {
	activityID, err := newID("act_")
	if err != nil {
		return err
	}
	st.Activity = append(st.Activity, Activity{ID: activityID, CaseID: item.ID, At: at, Summary: summary})
	for i := range st.Cases {
		if st.Cases[i].ID == item.ID && st.Cases[i].TenantID == item.TenantID && st.Cases[i].OwnerOrgID == item.OwnerOrgID {
			st.Cases[i].UpdatedAt = at
		}
	}
	return appendWorkbenchAudit(st, user, &item, action, summary, at)
}

func appendWorkbenchAudit(st *state, user accounts.User, item *Case, action, summary, at string) error {
	id, err := newID("audit_")
	if err != nil {
		return err
	}
	event := WorkbenchAudit{ID: id, ActorID: user.ID, ActorName: user.DisplayName, Action: action, At: at, Summary: summary}
	if item != nil {
		caseID, title := item.ID, item.Title
		event.CaseID, event.CaseTitle = &caseID, &title
	}
	st.WorkbenchAudit = append(st.WorkbenchAudit, workbenchAuditRecord{Item: event, TenantID: user.TenantID, OrgID: user.OrgID})
	return nil
}

func relationPairKey(left, right Case) string {
	leftKey, rightKey := left.OwnerOrgID+"\x00"+left.ID, right.OwnerOrgID+"\x00"+right.ID
	if rightKey < leftKey {
		leftKey, rightKey = rightKey, leftKey
	}
	digest := sha256.Sum256([]byte(leftKey + "\x00" + rightKey))
	return hex.EncodeToString(digest[:])
}

func (s *Store) WorkbenchSnapshot(ctx context.Context, user accounts.User, period string, model WorkbenchModel) (WorkbenchSnapshot, error) {
	if period != "day" && period != "week" && period != "month" {
		return WorkbenchSnapshot{}, fault.Invalid("工作台周期只能是day、week或month")
	}
	current, err := trustedUser(user)
	if err != nil {
		return WorkbenchSnapshot{}, err
	}
	st, err := s.snapshotScoped(ctx, userScope(current))
	if err != nil {
		return WorkbenchSnapshot{}, err
	}
	now := time.Now().UTC()
	start := workbenchPeriodStart(now, period)
	visible := make(map[string]Case)
	orderedCases := make([]Case, 0, len(st.Cases))
	for i := len(st.Cases) - 1; i >= 0; i-- {
		item := st.Cases[i]
		if !caseReadAllowed(item, current) {
			continue
		}
		visible[item.ID] = item
		orderedCases = append(orderedCases, item)
	}
	out := WorkbenchSnapshot{
		SchemaVersion: "workbench/v1", GeneratedAt: now.Format(time.RFC3339Nano), Period: period,
		Cases: []WorkbenchCase{}, Profiles: []CaseProfile{}, Tasks: []WorkbenchTask{}, Relations: []CaseRelation{},
		Knowledge: []Knowledge{}, Members: []Actor{}, Remediations: []WorkbenchRemediation{}, Reports: []WorkbenchReport{},
		Documents: []WorkbenchDocument{}, Runs: []WorkbenchRun{}, Audit: []WorkbenchAudit{}, Templates: s.Templates(),
		Model: model, Tools: []WorkbenchTool{}, Health: []WorkbenchHealth{},
	}
	out.Dashboard = buildWorkbenchDashboard(st, orderedCases, current.ID, now, start, period)
	profilesByCase := make(map[string]CaseProfile)
	for _, record := range st.Profiles {
		caseItem, ok := visible[record.Profile.CaseID]
		if !ok || record.TenantID != current.TenantID || record.OrgID != caseItem.OwnerOrgID {
			continue
		}
		out.Profiles = append(out.Profiles, record.Profile)
		profilesByCase[record.Profile.CaseID] = record.Profile
	}
	for _, task := range st.Tasks {
		if item, ok := visible[task.Task.CaseID]; ok && task.TenantID == current.TenantID && task.OrgID == item.OwnerOrgID {
			out.Tasks = append(out.Tasks, task.Task)
		}
	}
	for _, relation := range st.Relations {
		if item, ok := visible[relation.Relation.CaseID]; ok && relation.TenantID == current.TenantID && relation.OrgID == item.OwnerOrgID {
			if _, relatedVisible := visible[relation.Relation.RelatedCaseID]; relatedVisible {
				out.Relations = append(out.Relations, relation.Relation)
			}
		}
	}
	for _, record := range st.Knowledge {
		if record.TenantID == current.TenantID {
			out.Knowledge = append(out.Knowledge, record.Item)
		}
	}
	for _, candidate := range accounts.List() {
		if candidate.Enabled && accounts.Tenant(candidate) == current.TenantID {
			out.Members = append(out.Members, Actor{ID: candidate.ID, DisplayName: candidate.DisplayName, Role: candidate.Role,
				TenantID: accounts.Tenant(candidate), OrgID: candidate.OrgID})
		}
	}
	for _, caseItem := range orderedCases {
		summary := s.summarize(&st, caseItem)
		var latest any
		if summary.Run != nil {
			latest = map[string]any{"id": summary.Run.ID, "state": summary.State, "updated_at": summary.Run.UpdatedAt}
		}
		severity := caseLatestSeverity(st, caseItem)
		progress := caseTaskProgress(out.Tasks, caseItem.ID)
		out.Cases = append(out.Cases, WorkbenchCase{
			ID: caseItem.ID, Title: caseItem.Title, ContractType: caseItem.ContractType, BusinessLine: caseItem.BusinessLine,
			Scenario: caseItem.Scenario, BusinessStatus: caseItem.BusinessStatus, ArchivedAt: caseItem.ArchivedAt,
			DocumentCount: summary.DocumentCount, PendingDecisions: summary.Pending, LatestRun: latest, UpdatedAt: caseItem.UpdatedAt,
			Profile: profilePointer(profilesByCase[caseItem.ID]), Severity: severity, Progress: progress,
		})
		out.Documents = append(out.Documents, workbenchDocumentsForCase(st, caseItem)...)
	}
	for _, remediation := range st.Remediations {
		item, ok := visible[remediation.Record.CaseID]
		if ok && remediation.TenantID == current.TenantID && remediation.OrgID == item.OwnerOrgID {
			out.Remediations = append(out.Remediations, WorkbenchRemediation{Remediation: remediation.Record, CaseTitle: item.Title})
		}
	}
	for _, report := range st.Reports {
		item, ok := visible[report.CaseID]
		if ok && report.TenantID == current.TenantID && report.OrgID == item.OwnerOrgID {
			out.Reports = append(out.Reports, WorkbenchReport{ReportRecord: report.Record, CaseID: report.CaseID, CaseTitle: item.Title})
		}
	}
	out.Runs = workbenchRuns(st, visible)
	out.Audit = workbenchAuditEvents(st, visible, current.TenantID)
	out.Tools, err = s.workbenchTools(ctx, current, visible)
	if err != nil {
		return WorkbenchSnapshot{}, err
	}
	model.Name, model.OCRName = strings.TrimSpace(model.Name), strings.TrimSpace(model.OCRName)
	modelStatus, modelDetail := "unconfigured", "未配置审查模型"
	if model.Name != "" {
		model.Status, model.Label = "configured", "审查模型已配置："+model.Name
		modelStatus, modelDetail = "unknown", "已读取模型配置；本次快照未探测外部端点"
	} else {
		model.Status, model.Label = "unconfigured", "未配置审查模型"
	}
	out.Model = model
	observedAt := now.Format(time.RFC3339Nano)
	out.Health = append(out.Health, WorkbenchHealth{ID: "database", Name: "PostgreSQL", Status: "ready", Detail: "快照数据已从当前持久schema读取", ObservedAt: observedAt})
	out.Health = append(out.Health, WorkbenchHealth{ID: "review_model", Name: model.Name, Status: modelStatus, Detail: modelDetail, ObservedAt: observedAt})
	sort.Slice(out.Profiles, func(i, j int) bool { return out.Profiles[i].Number < out.Profiles[j].Number })
	sort.Slice(out.Tasks, func(i, j int) bool { return out.Tasks[i].UpdatedAt > out.Tasks[j].UpdatedAt })
	sort.Slice(out.Relations, func(i, j int) bool { return out.Relations[i].CreatedAt > out.Relations[j].CreatedAt })
	sort.Slice(out.Knowledge, func(i, j int) bool {
		if out.Knowledge[i].ID == out.Knowledge[j].ID {
			return out.Knowledge[i].CreatedAt > out.Knowledge[j].CreatedAt
		}
		return out.Knowledge[i].ID < out.Knowledge[j].ID
	})
	sort.Slice(out.Members, func(i, j int) bool { return out.Members[i].DisplayName < out.Members[j].DisplayName })
	sort.Slice(out.Remediations, func(i, j int) bool { return out.Remediations[i].UpdatedAt > out.Remediations[j].UpdatedAt })
	sort.Slice(out.Reports, func(i, j int) bool { return out.Reports[i].CreatedAt > out.Reports[j].CreatedAt })
	sort.Slice(out.Documents, func(i, j int) bool { return out.Documents[i].CreatedAt > out.Documents[j].CreatedAt })
	sort.Slice(out.Runs, func(i, j int) bool { return out.Runs[i].CreatedAt > out.Runs[j].CreatedAt })
	sort.Slice(out.Audit, func(i, j int) bool { return out.Audit[i].At > out.Audit[j].At })
	return out, nil
}

func workbenchPeriodStart(now time.Time, period string) time.Time {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	local := now.In(location)
	year, month, day := local.Date()
	switch period {
	case "day":
		return time.Date(year, month, day, 0, 0, 0, 0, location)
	case "week":
		daysSinceMonday := (int(local.Weekday()) + 6) % 7
		return time.Date(year, month, day-daysSinceMonday, 0, 0, 0, 0, location)
	default:
		return time.Date(year, month, 1, 0, 0, 0, 0, location)
	}
}

func buildWorkbenchDashboard(st state, cases []Case, actorID string, now, periodStart time.Time, period string) WorkbenchDashboard {
	dashboard := WorkbenchDashboard{VisibleCases: len(cases), Progress: []WorkbenchCount{}, Risks: []WorkbenchCount{
		{Severity: "high"}, {Severity: "medium"}, {Severity: "low"}, {Severity: "info"}, {Severity: "unrated"},
	}}
	visible := make(map[string]Case, len(cases))
	for _, item := range cases {
		visible[item.ID] = item
	}
	for _, task := range st.Tasks {
		if _, ok := visible[task.Task.CaseID]; !ok {
			continue
		}
		if task.Task.AssigneeID == actorID && task.Task.Status != "completed" {
			dashboard.MyPendingTasks++
		}
		if task.Task.Status != "completed" && task.Task.DueAt != nil {
			if dueAt, err := time.Parse(time.RFC3339Nano, *task.Task.DueAt); err == nil && dueAt.Before(now) {
				dashboard.OverdueTasks++
			}
		}
	}
	progressCounts := make(map[string]int)
	riskCounts := make(map[string]int)
	for _, category := range []string{"high", "medium", "low", "info", "unrated"} {
		riskCounts[category] = 0
	}
	remediationCases := make(map[string]bool)
	completedRuns := 0
	var durationTotal int64
	var durationCount int64
	for _, item := range cases {
		progressCounts[item.BusinessStatus]++
		if run, ok := st.latestRun(item.ID); ok {
			attempt, hasAttempt := latestAttemptForRun(st, run.ID)
			if hasAttempt && (attempt.State == "accepted" || attempt.State == "running") || run.State == "awaiting_human" {
				dashboard.PendingReviewCases++
			}
			if hasAttempt && attempt.State == "completed" && attempt.FinishedAt != nil {
				finishedAt, err := time.Parse(time.RFC3339Nano, *attempt.FinishedAt)
				if err == nil && !finishedAt.Before(periodStart) && finishedAt.Before(now) {
					completedRuns++
					if attempt.StartedAt != nil {
						startedAt, startErr := time.Parse(time.RFC3339Nano, *attempt.StartedAt)
						if startErr == nil && !finishedAt.Before(startedAt) {
							durationTotal += finishedAt.Sub(startedAt).Milliseconds()
							durationCount++
						}
					}
				}
			}
			if hasAttempt {
				for _, finding := range st.findingsForAttempt(run.ID, attempt.ID) {
					severity := "unrated"
					if finding.Severity != nil {
						severity = *finding.Severity
					}
					if _, exists := riskCounts[severity]; !exists {
						severity = "unrated"
					}
					riskCounts[severity]++
				}
			}
		}
	}
	for _, remediation := range st.Remediations {
		if _, ok := visible[remediation.Record.CaseID]; ok && remediation.Record.Status != "closed" {
			remediationCases[remediation.Record.CaseID] = true
		}
	}
	dashboard.RemediationCases = len(remediationCases)
	dashboard.CompletedReviews = completedRuns
	if durationCount > 0 {
		average := int64(math.Round(float64(durationTotal) / float64(durationCount)))
		dashboard.AverageDurationMS = &average
	}
	statuses := make([]string, 0, len(progressCounts))
	for status := range progressCounts {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	for _, status := range statuses {
		dashboard.Progress = append(dashboard.Progress, WorkbenchCount{Status: status, Count: progressCounts[status]})
	}
	for i := range dashboard.Risks {
		dashboard.Risks[i].Count = riskCounts[dashboard.Risks[i].Severity]
	}
	_ = period
	return dashboard
}

func latestAttemptForRun(st state, runID string) (Attempt, bool) {
	var latest Attempt
	found := false
	for _, attempt := range st.Attempts {
		if attempt.RunID == runID && (!found || attempt.Ordinal > latest.Ordinal) {
			latest, found = attempt, true
		}
	}
	return latest, found
}

func caseLatestSeverity(st state, item Case) *string {
	run, ok := st.latestRun(item.ID)
	if !ok {
		return nil
	}
	attempt, ok := latestAttemptForRun(st, run.ID)
	if !ok {
		return nil
	}
	findings := st.findingsForAttempt(run.ID, attempt.ID)
	if len(findings) == 0 {
		return nil
	}
	priority := map[string]int{"high": 4, "medium": 3, "low": 2, "info": 1}
	best := "unrated"
	for _, finding := range findings {
		if finding.Severity == nil {
			continue
		}
		if priority[*finding.Severity] > priority[best] {
			best = *finding.Severity
		}
	}
	return &best
}

func caseTaskProgress(tasks []WorkbenchTask, caseID string) *float64 {
	total, completed := 0, 0
	for _, task := range tasks {
		if task.CaseID == caseID {
			total++
			if task.Status == "completed" {
				completed++
			}
		}
	}
	if total == 0 {
		return nil
	}
	progress := float64(completed) * 100 / float64(total)
	return &progress
}

func profilePointer(profile CaseProfile) *CaseProfile {
	if profile.CaseID == "" {
		return nil
	}
	return &profile
}

func workbenchDocumentsForCase(st state, item Case) []WorkbenchDocument {
	documents := st.documents(item.ID)
	latest := make(map[string]string)
	for _, document := range documents {
		latest[document.Role] = document.ID
	}
	out := make([]WorkbenchDocument, 0, len(documents))
	for _, document := range documents {
		parsed, hasParsed := st.latestParsed(document.ID, "")
		entry := WorkbenchDocument{
			ID: document.ID, Role: document.Role, Filename: document.Filename, MediaType: document.MediaType,
			Digest: document.Digest, Size: document.Size, CaseID: item.ID, CaseTitle: item.Title,
			ParseStatus: "pending", Superseded: latest[document.Role] != document.ID, CreatedAt: document.CreatedAt,
		}
		if hasParsed {
			normalizedDigest := stringPointer(parsed.NormalizedDigest)
			irDigest := stringPointer(parsed.IRDigest)
			if parsed.NormalizedDigest == "" {
				normalizedDigest = nil
			}
			if parsed.IRDigest == "" {
				irDigest = nil
			}
			entry.ParsedDocument = &WorkbenchParsedDocument{
				ID: parsed.ID, DocumentID: parsed.DocumentID, OriginalDigest: parsed.OriginalDigest,
				ParserID: parsed.ParserID, ParserVersion: parsed.ParserVersion, ConfigHash: parsed.ConfigHash,
				NormalizationVersion: parsed.NormalizationVersion, NormalizedDigest: normalizedDigest,
				IRDigest: irDigest, ArtifactDigest: parsed.ArtifactDigest, CreatedAt: parsed.CreatedAt, Status: parsed.Status,
			}
			if parsed.Status == "ready" {
				entry.ParseStatus = "ready"
			} else {
				entry.ParseStatus = "failed"
				entry.ParseError = stringPointer(parsed.ParseError)
			}
		}
		out = append(out, entry)
	}
	return out
}

func workbenchRuns(st state, visible map[string]Case) []WorkbenchRun {
	out := make([]WorkbenchRun, 0)
	for _, run := range st.Runs {
		item, ok := visible[run.CaseID]
		if !ok {
			continue
		}
		attempt, hasAttempt := latestAttemptForRun(st, run.ID)
		entry := WorkbenchRun{ID: run.ID, CaseID: run.CaseID, CaseTitle: item.Title, State: run.State, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}
		if hasAttempt {
			attemptID := attempt.ID
			entry.AttemptID, entry.StartedAt, entry.FinishedAt = &attemptID, attempt.StartedAt, attempt.FinishedAt
			if execution := selectedExecution(st, run.ID, attempt.ID); execution != nil {
				usage := execution.Usage
				entry.Usage = &usage
			}
		}
		out = append(out, entry)
	}
	return out
}

func workbenchAuditEvents(st state, visible map[string]Case, tenantID string) []WorkbenchAudit {
	out := make([]WorkbenchAudit, 0, len(st.WorkbenchAudit)+len(st.Decisions))
	for _, record := range st.WorkbenchAudit {
		if record.TenantID != tenantID {
			continue
		}
		item := record.Item
		if item.CaseID != nil {
			caseItem, ok := visible[*item.CaseID]
			if !ok {
				continue
			}
			title := caseItem.Title
			item.CaseTitle = &title
		}
		out = append(out, item)
	}
	for _, decision := range st.Decisions {
		caseItem, ok := visible[decision.CaseID]
		if !ok {
			continue
		}
		caseID, title := caseItem.ID, caseItem.Title
		out = append(out, WorkbenchAudit{ID: decision.ID, CaseID: &caseID, CaseTitle: &title, ActorID: decision.ActorID,
			ActorName: decision.ActorName, Action: "finding." + decision.Action, At: decision.CreatedAt, Summary: decision.Reason})
	}
	return out
}

func (s *Store) workbenchTools(ctx context.Context, user accounts.User, visible map[string]Case) ([]WorkbenchTool, error) {
	caseIDs := make([]string, 0, len(visible))
	for id := range visible {
		caseIDs = append(caseIDs, id)
	}
	if len(caseIDs) == 0 {
		return []WorkbenchTool{}, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(user)); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT capability_id, count(*), (array_agg(status ORDER BY reserved_at DESC))[1]
		FROM tool_call_ledger WHERE tenant_id=$1 AND case_id=ANY($2::text[]) GROUP BY capability_id ORDER BY capability_id`, user.TenantID, caseIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkbenchTool{}
	for rows.Next() {
		var capability, status string
		var calls int
		if err := rows.Scan(&capability, &calls, &status); err != nil {
			return nil, err
		}
		lastStatus := status
		out = append(out, WorkbenchTool{ID: capability, Name: capability, Description: "持久工具调用账本中的实际能力", Capabilities: []string{capability}, Calls: calls, LastStatus: &lastStatus})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func validResourceID(value string) bool {
	if value == "" || len(value) > 120 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}
