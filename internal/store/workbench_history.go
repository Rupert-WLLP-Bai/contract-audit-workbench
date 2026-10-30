package store

import (
	"errors"
	"reflect"
	"strings"
)

func validateWorkbenchHistory(previous, next state) error {
	if err := validateWorkbenchProfileHistory(previous.Profiles, next.Profiles); err != nil {
		return err
	}
	if err := validateWorkbenchTaskHistory(previous.Tasks, next.Tasks); err != nil {
		return err
	}
	return validateWorkbenchRelationHistory(previous.Relations, next.Relations)
}

func validateWorkbenchProfileHistory(previous, next []caseProfileRecord) error {
	oldProfiles := make(map[string]caseProfileRecord, len(previous))
	for _, item := range previous {
		oldProfiles[item.Profile.CaseID] = item
	}
	seenNumbers := make(map[string]string, len(next))
	seenCases := make(map[string]bool, len(next))
	for _, item := range next {
		profile := item.Profile
		if !validWorkbenchProfile(profile) {
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
		if err := validateWorkbenchProfileRevision(prior, item, existed); err != nil {
			return err
		}
	}
	if len(next) < len(previous) {
		return errors.New("case profile was removed")
	}
	return nil
}

func validWorkbenchProfile(profile CaseProfile) bool {
	return profile.CaseID != "" && profile.Number != "" && profile.Version >= 1 &&
		validBusinessText(profile.Customer, 200) && validBusinessText(profile.Supplier, 200) &&
		validBusinessText(profile.ContractCategory, 120) && profile.OwnerID != ""
}

func validateWorkbenchProfileRevision(prior, item caseProfileRecord, existed bool) error {
	if reflect.DeepEqual(prior, item) {
		return nil
	}
	if existed {
		if item.TenantID != prior.TenantID || item.OrgID != prior.OrgID || item.Profile.Version != prior.Profile.Version+1 {
			return errors.New("case profile scope or version changed unexpectedly")
		}
	} else if item.Profile.Version != 1 {
		return errors.New("new case profile must begin at version one")
	}
	return nil
}

func validateWorkbenchTaskHistory(previous, next []workbenchTaskRecord) error {
	oldTasks := make(map[string]workbenchTaskRecord, len(previous))
	for _, item := range previous {
		oldTasks[item.Task.ID] = item
	}
	seenTasks := make(map[string]bool, len(next))
	for _, item := range next {
		task := item.Task
		if !validWorkbenchTaskIdentity(task) || !validWorkbenchTaskDetails(task) {
			return errors.New("workbench task is incomplete")
		}
		if seenTasks[task.ID] {
			return errors.New("workbench task is duplicated")
		}
		seenTasks[task.ID] = true
		prior, existed := oldTasks[task.ID]
		if err := validateWorkbenchTaskRevision(prior, item, existed); err != nil {
			return err
		}
	}
	if len(next) < len(previous) {
		return errors.New("workbench task was removed")
	}
	return nil
}

func validWorkbenchTaskIdentity(task WorkbenchTask) bool {
	return task.ID != "" && task.CaseID != "" && task.Version >= 1 &&
		task.AssigneeID != "" && task.CreatedAt != "" && task.UpdatedAt != ""
}

func validWorkbenchTaskDetails(task WorkbenchTask) bool {
	return validBusinessText(task.Title, 200) && validTaskKind(task.Kind) &&
		validTaskStatus(task.Status) && validTaskPriority(task.Priority)
}

func validateWorkbenchTaskRevision(prior, item workbenchTaskRecord, existed bool) error {
	if reflect.DeepEqual(prior, item) {
		return nil
	}
	if existed {
		if !workbenchTaskRevisionPreserved(prior, item) {
			return errors.New("workbench task scope, creator, or version changed unexpectedly")
		}
	} else if item.Task.Version != 1 || item.Task.CreatedBy == nil || strings.TrimSpace(*item.Task.CreatedBy) == "" {
		return errors.New("new workbench task must begin at version one with a creator")
	}
	return nil
}

func workbenchTaskRevisionPreserved(prior, item workbenchTaskRecord) bool {
	return item.TenantID == prior.TenantID && item.OrgID == prior.OrgID && item.Task.CaseID == prior.Task.CaseID &&
		item.Task.CreatedAt == prior.Task.CreatedAt && reflect.DeepEqual(item.Task.CreatedBy, prior.Task.CreatedBy) &&
		item.Task.Version == prior.Task.Version+1
}

func validateWorkbenchRelationHistory(previous, next []caseRelationRecord) error {
	oldRelations := make(map[string]caseRelationRecord, len(previous))
	for _, item := range previous {
		oldRelations[item.Relation.ID] = item
	}
	seenRelations := make(map[string]bool, len(next))
	for _, item := range next {
		if !validWorkbenchRelation(item) {
			return errors.New("case relation is incomplete")
		}
		id := item.Relation.ID
		if seenRelations[id] {
			return errors.New("case relation is duplicated")
		}
		seenRelations[id] = true
		if prior, existed := oldRelations[id]; existed && !reflect.DeepEqual(prior, item) {
			return errors.New("case relation is immutable")
		}
	}
	return nil
}

func validWorkbenchRelation(item caseRelationRecord) bool {
	relation := item.Relation
	return relation.ID != "" && relation.CaseID != "" && relation.RelatedCaseID != "" && relation.CaseID != relation.RelatedCaseID &&
		validBusinessText(relation.Reason, 1000) && relation.CreatedBy != "" && relation.CreatedAt != "" && item.RelatedOrgID != "" && item.PairKey != ""
}
