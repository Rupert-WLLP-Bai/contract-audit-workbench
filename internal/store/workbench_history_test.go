package store

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func workbenchHistoryProfile() caseProfileRecord {
	dueAt := "2026-11-01T00:00:00Z"
	return caseProfileRecord{
		TenantID: "tenant-1", OrgID: "org-1",
		Profile: CaseProfile{
			CaseID: "case-1", Number: "WB-001", Customer: "采购方", Supplier: "服务商",
			ContractCategory: "专线服务", OwnerID: "owner-1", DueAt: &dueAt,
			Version: 1, UpdatedAt: "2026-10-29T00:00:00Z",
		},
	}
}

func workbenchHistoryTask() workbenchTaskRecord {
	creator, findingID, dueAt := "owner-1", "finding-1", "2026-11-01T00:00:00Z"
	return workbenchTaskRecord{
		TenantID: "tenant-1", OrgID: "org-1",
		Task: WorkbenchTask{
			ID: "task-1", CaseID: "case-1", Title: "核对履约材料", Kind: "review",
			AssigneeID: "reviewer-1", DueAt: &dueAt, Status: "open", Priority: "medium",
			FindingID: &findingID, CreatedBy: &creator, Version: 1,
			CreatedAt: "2026-10-28T00:00:00Z", UpdatedAt: "2026-10-29T00:00:00Z",
		},
	}
}

func workbenchHistoryRelation() caseRelationRecord {
	return caseRelationRecord{
		TenantID: "tenant-1", OrgID: "org-1", RelatedOrgID: "org-2", PairKey: "case-1|case-2",
		Relation: CaseRelation{
			ID: "relation-1", CaseID: "case-1", RelatedCaseID: "case-2",
			Reason: "同一采购项目", CreatedBy: "owner-1", CreatedAt: "2026-10-29T00:00:00Z",
		},
	}
}

func assertWorkbenchHistoryResult(t *testing.T, previous, next state, want string) {
	t.Helper()
	before, err := json.Marshal([]state{previous, next})
	if err != nil {
		t.Fatalf("snapshot history inputs: %v", err)
	}
	err = validateWorkbenchHistory(previous, next)
	after, snapshotErr := json.Marshal([]state{previous, next})
	if snapshotErr != nil {
		t.Fatalf("snapshot history inputs after validation: %v", snapshotErr)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("history validation changed its input records")
	}
	got := ""
	if err != nil {
		got = err.Error()
	}
	if got != want {
		t.Fatalf("validateWorkbenchHistory() error = %q, want %q", got, want)
	}
}

func TestWorkbenchHistoryAcceptsNewAndUnchangedRecords(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		assertWorkbenchHistoryResult(t, state{}, state{}, "")
	})
	t.Run("new", func(t *testing.T) {
		next := state{
			Profiles:  []caseProfileRecord{workbenchHistoryProfile()},
			Tasks:     []workbenchTaskRecord{workbenchHistoryTask()},
			Relations: []caseRelationRecord{workbenchHistoryRelation()},
		}
		assertWorkbenchHistoryResult(t, state{}, next, "")
	})
	t.Run("unchanged_at_later_versions", func(t *testing.T) {
		profile, task, relation := workbenchHistoryProfile(), workbenchHistoryTask(), workbenchHistoryRelation()
		profile.Profile.Version, task.Task.Version = 4, 7
		previous := state{Profiles: []caseProfileRecord{profile}, Tasks: []workbenchTaskRecord{task}, Relations: []caseRelationRecord{relation}}
		next := state{Profiles: []caseProfileRecord{profile}, Tasks: []workbenchTaskRecord{task}, Relations: []caseRelationRecord{relation}}
		assertWorkbenchHistoryResult(t, previous, next, "")
	})
}

func TestWorkbenchHistoryProfileCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*caseProfileRecord)
		want   string
	}{
		{"case_id", func(r *caseProfileRecord) { r.Profile.CaseID = "" }, "case profile is incomplete"},
		{"number", func(r *caseProfileRecord) { r.Profile.Number = "" }, "case profile is incomplete"},
		{"zero_version", func(r *caseProfileRecord) { r.Profile.Version = 0 }, "case profile is incomplete"},
		{"negative_version", func(r *caseProfileRecord) { r.Profile.Version = -1 }, "case profile is incomplete"},
		{"blank_customer", func(r *caseProfileRecord) { r.Profile.Customer = " \t " }, "case profile is incomplete"},
		{"long_customer", func(r *caseProfileRecord) { r.Profile.Customer = strings.Repeat("客", 201) }, "case profile is incomplete"},
		{"blank_supplier", func(r *caseProfileRecord) { r.Profile.Supplier = " \t " }, "case profile is incomplete"},
		{"long_supplier", func(r *caseProfileRecord) { r.Profile.Supplier = strings.Repeat("供", 201) }, "case profile is incomplete"},
		{"blank_category", func(r *caseProfileRecord) { r.Profile.ContractCategory = " \t " }, "case profile is incomplete"},
		{"long_category", func(r *caseProfileRecord) { r.Profile.ContractCategory = strings.Repeat("类", 121) }, "case profile is incomplete"},
		{"owner", func(r *caseProfileRecord) { r.Profile.OwnerID = "" }, "case profile is incomplete"},
		{"maximum_customer", func(r *caseProfileRecord) { r.Profile.Customer = " " + strings.Repeat("客", 200) + " " }, ""},
		{"maximum_supplier", func(r *caseProfileRecord) { r.Profile.Supplier = " " + strings.Repeat("供", 200) + " " }, ""},
		{"maximum_category", func(r *caseProfileRecord) { r.Profile.ContractCategory = " " + strings.Repeat("类", 120) + " " }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := workbenchHistoryProfile()
			tc.change(&profile)
			next := state{Profiles: []caseProfileRecord{profile}}
			assertWorkbenchHistoryResult(t, state{}, next, tc.want)
			assertWorkbenchHistoryResult(t, next, next, tc.want)
		})
	}
}

func TestWorkbenchHistoryTaskCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*workbenchTaskRecord)
		want   string
	}{
		{"id", func(r *workbenchTaskRecord) { r.Task.ID = "" }, "workbench task is incomplete"},
		{"case_id", func(r *workbenchTaskRecord) { r.Task.CaseID = "" }, "workbench task is incomplete"},
		{"zero_version", func(r *workbenchTaskRecord) { r.Task.Version = 0 }, "workbench task is incomplete"},
		{"negative_version", func(r *workbenchTaskRecord) { r.Task.Version = -1 }, "workbench task is incomplete"},
		{"blank_title", func(r *workbenchTaskRecord) { r.Task.Title = " \t " }, "workbench task is incomplete"},
		{"long_title", func(r *workbenchTaskRecord) { r.Task.Title = strings.Repeat("审", 201) }, "workbench task is incomplete"},
		{"kind", func(r *workbenchTaskRecord) { r.Task.Kind = "unknown" }, "workbench task is incomplete"},
		{"status", func(r *workbenchTaskRecord) { r.Task.Status = "unknown" }, "workbench task is incomplete"},
		{"priority", func(r *workbenchTaskRecord) { r.Task.Priority = "unknown" }, "workbench task is incomplete"},
		{"assignee", func(r *workbenchTaskRecord) { r.Task.AssigneeID = "" }, "workbench task is incomplete"},
		{"created_at", func(r *workbenchTaskRecord) { r.Task.CreatedAt = "" }, "workbench task is incomplete"},
		{"updated_at", func(r *workbenchTaskRecord) { r.Task.UpdatedAt = "" }, "workbench task is incomplete"},
		{"maximum_title", func(r *workbenchTaskRecord) { r.Task.Title = " " + strings.Repeat("审", 200) + " " }, ""},
		{"decision", func(r *workbenchTaskRecord) { r.Task.Kind = "decision" }, ""},
		{"remediation", func(r *workbenchTaskRecord) { r.Task.Kind = "remediation" }, ""},
		{"general", func(r *workbenchTaskRecord) { r.Task.Kind = "general" }, ""},
		{"in_progress", func(r *workbenchTaskRecord) { r.Task.Status = "in_progress" }, ""},
		{"completed", func(r *workbenchTaskRecord) { r.Task.Status = "completed" }, ""},
		{"high_priority", func(r *workbenchTaskRecord) { r.Task.Priority = "high" }, ""},
		{"low_priority", func(r *workbenchTaskRecord) { r.Task.Priority = "low" }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := workbenchHistoryTask()
			tc.change(&task)
			next := state{Tasks: []workbenchTaskRecord{task}}
			assertWorkbenchHistoryResult(t, state{}, next, tc.want)
			assertWorkbenchHistoryResult(t, next, next, tc.want)
		})
	}
}

func TestWorkbenchHistoryRelationCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*caseRelationRecord)
		want   string
	}{
		{"id", func(r *caseRelationRecord) { r.Relation.ID = "" }, "case relation is incomplete"},
		{"case_id", func(r *caseRelationRecord) { r.Relation.CaseID = "" }, "case relation is incomplete"},
		{"related_case_id", func(r *caseRelationRecord) { r.Relation.RelatedCaseID = "" }, "case relation is incomplete"},
		{"self_relation", func(r *caseRelationRecord) { r.Relation.RelatedCaseID = r.Relation.CaseID }, "case relation is incomplete"},
		{"blank_reason", func(r *caseRelationRecord) { r.Relation.Reason = " \t " }, "case relation is incomplete"},
		{"long_reason", func(r *caseRelationRecord) { r.Relation.Reason = strings.Repeat("因", 1001) }, "case relation is incomplete"},
		{"creator", func(r *caseRelationRecord) { r.Relation.CreatedBy = "" }, "case relation is incomplete"},
		{"created_at", func(r *caseRelationRecord) { r.Relation.CreatedAt = "" }, "case relation is incomplete"},
		{"related_org", func(r *caseRelationRecord) { r.RelatedOrgID = "" }, "case relation is incomplete"},
		{"pair_key", func(r *caseRelationRecord) { r.PairKey = "" }, "case relation is incomplete"},
		{"maximum_reason", func(r *caseRelationRecord) { r.Relation.Reason = " " + strings.Repeat("因", 1000) + " " }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relation := workbenchHistoryRelation()
			tc.change(&relation)
			next := state{Relations: []caseRelationRecord{relation}}
			assertWorkbenchHistoryResult(t, state{}, next, tc.want)
			assertWorkbenchHistoryResult(t, next, next, tc.want)
		})
	}
}

func TestWorkbenchHistoryRejectsDuplicateIDsAcrossTenants(t *testing.T) {
	profile, task, relation := workbenchHistoryProfile(), workbenchHistoryTask(), workbenchHistoryRelation()
	otherProfile, otherTask, otherRelation := profile, task, relation
	otherProfile.TenantID, otherTask.TenantID, otherRelation.TenantID = "tenant-2", "tenant-2", "tenant-2"
	otherProfile.Profile.Number = "WB-002"
	for _, tc := range []struct {
		name           string
		previous, next state
		want           string
	}{
		{"profiles", state{Profiles: []caseProfileRecord{profile}}, state{Profiles: []caseProfileRecord{profile, otherProfile}}, "case profile is duplicated"},
		{"tasks", state{Tasks: []workbenchTaskRecord{task}}, state{Tasks: []workbenchTaskRecord{task, otherTask}}, "workbench task is duplicated"},
		{"relations", state{Relations: []caseRelationRecord{relation}}, state{Relations: []caseRelationRecord{relation, otherRelation}}, "case relation is duplicated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertWorkbenchHistoryResult(t, state{}, tc.next, tc.want)
			assertWorkbenchHistoryResult(t, tc.previous, tc.next, tc.want)
		})
	}
}

func TestWorkbenchHistoryProfileNumberOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, tenant, org, number, want string
	}{
		{"same_tenant_and_org", "tenant-1", "org-1", "wb-001", "case profile number is duplicated within the tenant"},
		{"same_tenant_other_org", "tenant-1", "org-2", "wb-001", "case profile number is duplicated within the tenant"},
		{"other_tenant", "tenant-2", "org-1", "wb-001", ""},
		{"stored_number_is_not_trimmed", "tenant-1", "org-1", "WB-001 ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, second := workbenchHistoryProfile(), workbenchHistoryProfile()
			second.Profile.CaseID, second.Profile.Number = "case-2", tc.number
			second.TenantID, second.OrgID = tc.tenant, tc.org
			next := state{Profiles: []caseProfileRecord{first, second}}
			assertWorkbenchHistoryResult(t, state{}, next, tc.want)
			assertWorkbenchHistoryResult(t, next, next, tc.want)
		})
	}
}

func TestWorkbenchHistoryProfileVersionAndScope(t *testing.T) {
	t.Run("initial_version", func(t *testing.T) {
		profile := workbenchHistoryProfile()
		profile.Profile.Version = 2
		assertWorkbenchHistoryResult(t, state{}, state{Profiles: []caseProfileRecord{profile}}, "new case profile must begin at version one")
	})
	prior := workbenchHistoryProfile()
	prior.Profile.Version = 4
	for _, tc := range []struct {
		name   string
		change func(*caseProfileRecord)
		want   string
	}{
		{"next_version", func(r *caseProfileRecord) {}, ""},
		{"same_version", func(r *caseProfileRecord) { r.Profile.Version = 4 }, "case profile scope or version changed unexpectedly"},
		{"older_version", func(r *caseProfileRecord) { r.Profile.Version = 3 }, "case profile scope or version changed unexpectedly"},
		{"skipped_version", func(r *caseProfileRecord) { r.Profile.Version = 6 }, "case profile scope or version changed unexpectedly"},
		{"tenant", func(r *caseProfileRecord) { r.TenantID = "tenant-2" }, "case profile scope or version changed unexpectedly"},
		{"org", func(r *caseProfileRecord) { r.OrgID = "org-2" }, "case profile scope or version changed unexpectedly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := prior
			next.Profile.Version, next.Profile.Customer = 5, "新采购方"
			tc.change(&next)
			assertWorkbenchHistoryResult(t, state{Profiles: []caseProfileRecord{prior}}, state{Profiles: []caseProfileRecord{next}}, tc.want)
		})
	}
}

func TestWorkbenchHistoryNewTaskRequiresInitialVersionAndCreator(t *testing.T) {
	blank, empty, padded := " \t ", "", " owner-1 "
	for _, tc := range []struct {
		name    string
		version int
		creator *string
		want    string
	}{
		{"nil_creator", 1, nil, "new workbench task must begin at version one with a creator"},
		{"empty_creator", 1, &empty, "new workbench task must begin at version one with a creator"},
		{"blank_creator", 1, &blank, "new workbench task must begin at version one with a creator"},
		{"later_version", 2, &padded, "new workbench task must begin at version one with a creator"},
		{"padded_creator", 1, &padded, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := workbenchHistoryTask()
			task.Task.Version, task.Task.CreatedBy = tc.version, tc.creator
			assertWorkbenchHistoryResult(t, state{}, state{Tasks: []workbenchTaskRecord{task}}, tc.want)
		})
	}
}

func TestWorkbenchHistoryTaskVersionAndImmutableFields(t *testing.T) {
	prior := workbenchHistoryTask()
	prior.Task.Version = 4
	for _, tc := range []struct {
		name   string
		change func(*workbenchTaskRecord)
		want   string
	}{
		{"next_version", func(r *workbenchTaskRecord) {}, ""},
		{"same_version", func(r *workbenchTaskRecord) { r.Task.Version = 4 }, "workbench task scope, creator, or version changed unexpectedly"},
		{"older_version", func(r *workbenchTaskRecord) { r.Task.Version = 3 }, "workbench task scope, creator, or version changed unexpectedly"},
		{"skipped_version", func(r *workbenchTaskRecord) { r.Task.Version = 6 }, "workbench task scope, creator, or version changed unexpectedly"},
		{"tenant", func(r *workbenchTaskRecord) { r.TenantID = "tenant-2" }, "workbench task scope, creator, or version changed unexpectedly"},
		{"org", func(r *workbenchTaskRecord) { r.OrgID = "org-2" }, "workbench task scope, creator, or version changed unexpectedly"},
		{"case", func(r *workbenchTaskRecord) { r.Task.CaseID = "case-2" }, "workbench task scope, creator, or version changed unexpectedly"},
		{"created_at", func(r *workbenchTaskRecord) { r.Task.CreatedAt = "2026-10-29T00:00:00Z" }, "workbench task scope, creator, or version changed unexpectedly"},
		{"creator", func(r *workbenchTaskRecord) { creator := "owner-2"; r.Task.CreatedBy = &creator }, "workbench task scope, creator, or version changed unexpectedly"},
		{"removed_creator", func(r *workbenchTaskRecord) { r.Task.CreatedBy = nil }, "workbench task scope, creator, or version changed unexpectedly"},
		{"equal_creator_new_pointer", func(r *workbenchTaskRecord) { creator := "owner-1"; r.Task.CreatedBy = &creator }, ""},
		{"changed_finding", func(r *workbenchTaskRecord) { finding := "finding-2"; r.Task.FindingID = &finding }, ""},
		{"removed_finding", func(r *workbenchTaskRecord) { r.Task.FindingID = nil }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := prior
			next.Task.Version, next.Task.Title = 5, "补充核对履约材料"
			tc.change(&next)
			assertWorkbenchHistoryResult(t, state{Tasks: []workbenchTaskRecord{prior}}, state{Tasks: []workbenchTaskRecord{next}}, tc.want)
		})
	}
}

func TestWorkbenchHistoryPreservesLegacyTaskCreators(t *testing.T) {
	blank, empty := " \t ", ""
	for _, tc := range []struct {
		name    string
		creator *string
	}{
		{"nil", nil},
		{"empty", &empty},
		{"blank", &blank},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prior := workbenchHistoryTask()
			prior.Task.Version, prior.Task.CreatedBy = 4, tc.creator
			previous := state{Tasks: []workbenchTaskRecord{prior}}
			assertWorkbenchHistoryResult(t, previous, state{Tasks: []workbenchTaskRecord{prior}}, "")
			next := prior
			next.Task.Version, next.Task.Title = 5, "保留旧创建者的更新"
			assertWorkbenchHistoryResult(t, previous, state{Tasks: []workbenchTaskRecord{next}}, "")
			creator := "owner-1"
			next.Task.CreatedBy = &creator
			assertWorkbenchHistoryResult(t, previous, state{Tasks: []workbenchTaskRecord{next}}, "workbench task scope, creator, or version changed unexpectedly")
		})
	}
}

func TestWorkbenchHistoryRelationsAreImmutableByExistingID(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*caseRelationRecord)
	}{
		{"tenant", func(r *caseRelationRecord) { r.TenantID = "tenant-2" }},
		{"org", func(r *caseRelationRecord) { r.OrgID = "org-3" }},
		{"related_org", func(r *caseRelationRecord) { r.RelatedOrgID = "org-3" }},
		{"pair_key", func(r *caseRelationRecord) { r.PairKey = "another-pair" }},
		{"case", func(r *caseRelationRecord) { r.Relation.CaseID = "case-3" }},
		{"related_case", func(r *caseRelationRecord) { r.Relation.RelatedCaseID = "case-3" }},
		{"reason", func(r *caseRelationRecord) { r.Relation.Reason = "另一关联理由" }},
		{"creator", func(r *caseRelationRecord) { r.Relation.CreatedBy = "owner-2" }},
		{"created_at", func(r *caseRelationRecord) { r.Relation.CreatedAt = "2026-10-30T00:00:00Z" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prior := workbenchHistoryRelation()
			next := prior
			tc.change(&next)
			assertWorkbenchHistoryResult(t, state{Relations: []caseRelationRecord{prior}}, state{Relations: []caseRelationRecord{next}}, "case relation is immutable")
		})
	}
}

func TestWorkbenchHistoryDeletionDependsOnCollection(t *testing.T) {
	profile, task, relation := workbenchHistoryProfile(), workbenchHistoryTask(), workbenchHistoryRelation()
	profile.Profile.Version, task.Task.Version = 4, 7
	replacementProfile, replacementTask, replacementRelation := workbenchHistoryProfile(), workbenchHistoryTask(), workbenchHistoryRelation()
	replacementProfile.Profile.CaseID, replacementTask.Task.ID, replacementRelation.Relation.ID = "case-3", "task-2", "relation-2"
	for _, tc := range []struct {
		name           string
		previous, next state
		want           string
	}{
		{"profile_removed", state{Profiles: []caseProfileRecord{profile}}, state{}, "case profile was removed"},
		{"profile_replaced_at_equal_count", state{Profiles: []caseProfileRecord{profile}}, state{Profiles: []caseProfileRecord{replacementProfile}}, ""},
		{"task_removed", state{Tasks: []workbenchTaskRecord{task}}, state{}, "workbench task was removed"},
		{"task_replaced_at_equal_count", state{Tasks: []workbenchTaskRecord{task}}, state{Tasks: []workbenchTaskRecord{replacementTask}}, ""},
		{"relation_removed", state{Relations: []caseRelationRecord{relation}}, state{}, ""},
		{"relation_replaced_at_equal_count", state{Relations: []caseRelationRecord{relation}}, state{Relations: []caseRelationRecord{replacementRelation}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertWorkbenchHistoryResult(t, tc.previous, tc.next, tc.want)
		})
	}
}

func TestWorkbenchHistoryFirstErrorOrder(t *testing.T) {
	profile, task, relation := workbenchHistoryProfile(), workbenchHistoryTask(), workbenchHistoryRelation()
	badProfile, badTask, badRelation := profile, task, relation
	badProfile.Profile.Customer, badTask.Task.Title, badRelation.Relation.Reason = "", "", ""
	changedProfile, changedTask, changedRelation := profile, task, relation
	changedProfile.Profile.Customer, changedTask.Task.Title, changedRelation.Relation.Reason = "新采购方", "新标题", "新理由"
	versionedProfile, versionedTask := profile, task
	versionedProfile.Profile.Version, versionedTask.Task.Version = 2, 2
	secondProfile, duplicateProfile := profile, versionedProfile
	secondProfile.Profile.CaseID, secondProfile.Profile.Number = "case-2", "WB-002"
	duplicateProfile.Profile.Number = "wb-002"
	numberConflict := versionedProfile
	numberConflict.Profile.CaseID, numberConflict.Profile.Number = "case-2", "wb-001"
	otherBadProfile, otherBadTask, otherBadRelation := badProfile, badTask, badRelation
	otherBadProfile.Profile.CaseID, otherBadTask.Task.ID, otherBadRelation.Relation.ID = "case-2", "task-2", "relation-2"
	for _, tc := range []struct {
		name           string
		previous, next state
		want           string
	}{
		{"profiles_before_tasks_and_relations", state{}, state{Profiles: []caseProfileRecord{badProfile}, Tasks: []workbenchTaskRecord{badTask}, Relations: []caseRelationRecord{badRelation}}, "case profile is incomplete"},
		{"profile_removal_before_tasks", state{Profiles: []caseProfileRecord{profile}}, state{Tasks: []workbenchTaskRecord{badTask}}, "case profile was removed"},
		{"tasks_before_relations", state{}, state{Tasks: []workbenchTaskRecord{badTask}, Relations: []caseRelationRecord{badRelation}}, "workbench task is incomplete"},
		{"task_removal_before_relations", state{Tasks: []workbenchTaskRecord{task}}, state{Relations: []caseRelationRecord{badRelation}}, "workbench task was removed"},
		{"profile_incomplete_before_duplicate", state{}, state{Profiles: []caseProfileRecord{profile, badProfile}}, "case profile is incomplete"},
		{"profile_duplicate_before_version", state{}, state{Profiles: []caseProfileRecord{profile, versionedProfile}}, "case profile is duplicated"},
		{"profile_duplicate_before_number", state{}, state{Profiles: []caseProfileRecord{profile, secondProfile, duplicateProfile}}, "case profile is duplicated"},
		{"profile_number_before_initial_version", state{}, state{Profiles: []caseProfileRecord{profile, numberConflict}}, "case profile number is duplicated within the tenant"},
		{"profile_first_record_before_later_incomplete", state{Profiles: []caseProfileRecord{profile}}, state{Profiles: []caseProfileRecord{changedProfile, otherBadProfile}}, "case profile scope or version changed unexpectedly"},
		{"profile_reversed_records", state{Profiles: []caseProfileRecord{profile}}, state{Profiles: []caseProfileRecord{otherBadProfile, changedProfile}}, "case profile is incomplete"},
		{"profile_incomplete_before_removal", state{Profiles: []caseProfileRecord{profile, otherBadProfile}}, state{Profiles: []caseProfileRecord{badProfile}}, "case profile is incomplete"},
		{"task_incomplete_before_duplicate", state{}, state{Tasks: []workbenchTaskRecord{task, badTask}}, "workbench task is incomplete"},
		{"task_duplicate_before_initial_version", state{}, state{Tasks: []workbenchTaskRecord{task, versionedTask}}, "workbench task is duplicated"},
		{"task_first_record_before_later_incomplete", state{Tasks: []workbenchTaskRecord{task}}, state{Tasks: []workbenchTaskRecord{changedTask, otherBadTask}}, "workbench task scope, creator, or version changed unexpectedly"},
		{"task_reversed_records", state{Tasks: []workbenchTaskRecord{task}}, state{Tasks: []workbenchTaskRecord{otherBadTask, changedTask}}, "workbench task is incomplete"},
		{"task_incomplete_before_removal", state{Tasks: []workbenchTaskRecord{task, otherBadTask}}, state{Tasks: []workbenchTaskRecord{badTask}}, "workbench task is incomplete"},
		{"relation_incomplete_before_duplicate", state{}, state{Relations: []caseRelationRecord{relation, badRelation}}, "case relation is incomplete"},
		{"relation_duplicate_before_immutable", state{Relations: []caseRelationRecord{relation}}, state{Relations: []caseRelationRecord{relation, changedRelation}}, "case relation is duplicated"},
		{"relation_first_record_before_later_incomplete", state{Relations: []caseRelationRecord{relation}}, state{Relations: []caseRelationRecord{changedRelation, otherBadRelation}}, "case relation is immutable"},
		{"relation_reversed_records", state{Relations: []caseRelationRecord{relation}}, state{Relations: []caseRelationRecord{otherBadRelation, changedRelation}}, "case relation is incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertWorkbenchHistoryResult(t, tc.previous, tc.next, tc.want)
		})
	}
}
