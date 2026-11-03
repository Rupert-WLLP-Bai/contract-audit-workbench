// File metadata (manifest mirror):
// Purpose: Maintains typed Go contract schemas for OpenAPI generation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-03
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

// Package contracts maintains the Go schemas used to generate OpenAPI.
package contracts

import "reflect"

type SupportGrant struct {
	Id           string   `json:"id" openapi:"{\"type\":\"string\"}"`
	Tenant_id    string   `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Org_id       string   `json:"org_id" openapi:"{\"type\":\"string\"}"`
	Case_id      string   `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Principal_id string   `json:"principal_id" openapi:"{\"type\":\"string\"}"`
	Actions      []string `json:"actions" openapi:"{\"type\":\"array\",\"minItems\":1,\"maxItems\":1}" openapi_items:"{\"type\":\"string\",\"enum\":[\"case.read\"]}"`
	Reason       string   `json:"reason" openapi:"{\"type\":\"string\"}"`
	Expires_at   string   `json:"expires_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Created_by   string   `json:"created_by" openapi:"{\"type\":\"string\"}"`
	Approved_by  *string  `json:"approved_by" openapi:"{\"type\":\"string\",\"nullable\":true}"`
	Status       string   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"pending\",\"approved\",\"revoked\",\"expired\"]}"`
	Revision     string   `json:"revision" openapi:"{\"type\":\"string\",\"pattern\":\"^[1-9][0-9]{0,18}$\"}"`
}
type SupportGrantList struct {
	Grants []SupportGrant `json:"grants" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/SupportGrant\"}"`
}
type SupportGrantRequest struct {
	Principal_id string   `json:"principal_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Actions      []string `json:"actions" openapi:"{\"type\":\"array\",\"minItems\":1,\"maxItems\":1}" openapi_items:"{\"type\":\"string\",\"enum\":[\"case.read\"]}"`
	Reason       string   `json:"reason" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":1000}"`
	Expires_at   string   `json:"expires_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type SupportGrantCommand struct {
	Expected_revision string `json:"expected_revision" openapi:"{\"type\":\"string\",\"pattern\":\"^[1-9][0-9]{0,18}$\"}"`
}
type SupportCaseView struct {
	Grant_id        string `json:"grant_id" openapi:"{\"type\":\"string\"}"`
	Case_id         string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Tenant_id       string `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Org_id          string `json:"org_id" openapi:"{\"type\":\"string\"}"`
	Title           string `json:"title" openapi:"{\"type\":\"string\"}"`
	Contract_type   string `json:"contract_type" openapi:"{\"type\":\"string\"}"`
	Business_status string `json:"business_status" openapi:"{\"type\":\"string\"}"`
	Created_at      string `json:"created_at" openapi:"{\"type\":\"string\"}"`
	Updated_at      string `json:"updated_at" openapi:"{\"type\":\"string\"}"`
	Read_only       bool   `json:"read_only" openapi:"{\"type\":\"boolean\",\"enum\":[true]}"`
}
type ActiveContext struct {
	Membership_id   *string `json:"membership_id" openapi:"{\"type\":\"string\",\"nullable\":true,\"description\":\"Null only for the existing local non-enterprise session.\"}"`
	Tenant_id       string  `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Tenant_name     string  `json:"tenant_name" openapi:"{\"type\":\"string\"}"`
	Org_id          string  `json:"org_id" openapi:"{\"type\":\"string\"}"`
	Org_name        string  `json:"org_name" openapi:"{\"type\":\"string\"}"`
	Context_version string  `json:"context_version" openapi:"{\"type\":\"string\",\"pattern\":\"^[1-9][0-9]{0,18}$\",\"description\":\"Canonical positive PostgreSQL bigint decimal string; compare as opaque CAS string, never JS number.\"}"`
}
type ContextList struct {
	Memberships            []MembershipChoice `json:"memberships" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/MembershipChoice\"}"`
	Context_switch_enabled bool               `json:"context_switch_enabled" openapi:"{\"type\":\"boolean\"}"`
}
type ContextSwitchRequest struct {
	Membership_id            string `json:"membership_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Expected_context_version string `json:"expected_context_version" openapi:"{\"type\":\"string\",\"pattern\":\"^[1-9][0-9]{0,18}$\",\"description\":\"Canonical positive PostgreSQL bigint decimal string; compare as opaque CAS string, never JS number.\"}"`
}
type MembershipChoice struct {
	Id              string `json:"id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Tenant_id       string `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Tenant_name     string `json:"tenant_name" openapi:"{\"type\":\"string\"}"`
	Jurisdiction_id string `json:"jurisdiction_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Org_id          string `json:"org_id" openapi:"{\"type\":\"string\"}"`
	Org_name        string `json:"org_name" openapi:"{\"type\":\"string\"}"`
	Role            string `json:"role" openapi:"{\"type\":\"string\"}"`
}
type PendingMembershipList struct {
	Memberships []MembershipChoice `json:"memberships" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/MembershipChoice\"}"`
}
type PendingMembershipSelectionRequest struct {
	Membership_id string `json:"membership_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Audience      string `json:"audience" openapi:"{\"type\":\"string\",\"enum\":[\"client\",\"admin\"]}"`
}
type ContractAuditGateReason struct {
	Code   string `json:"code" openapi:"{\"type\":\"string\",\"enum\":[\"submitted_material_missing\",\"attempt_input_missing\",\"attempt_input_hash_mismatch\",\"attempt_not_completed\",\"attempt_partial\",\"model_failure\",\"run_not_completed\",\"run_failure\",\"attempt_document_identity_missing\",\"foreign_document\",\"document_digest_mismatch\",\"attempt_document_not_ready\",\"attempt_omits_submitted_material\",\"finding_without_anchor\",\"anchor_not_located\",\"anchor_document_not_in_attempt\",\"anchor_quote_missing\",\"anchor_digest_mismatch\",\"anchor_parse_mismatch\"]}"`
	Detail string `json:"detail,omitempty" openapi:"{\"type\":\"string\"}"`
}
type ContractAuditBindRequest struct {
	Review_run_id        string `json:"review_run_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Review_attempt_id    string `json:"review_attempt_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Expected_row_version int64  `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ContractAuditBinding struct {
	Binding_id             string                    `json:"binding_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id   string                    `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Review_run_id          string                    `json:"review_run_id" openapi:"{\"type\":\"string\"}"`
	Review_attempt_id      string                    `json:"review_attempt_id" openapi:"{\"type\":\"string\"}"`
	Attempt_input_hash     string                    `json:"attempt_input_hash" openapi:"{\"type\":\"string\"}"`
	Snapshot_digest        string                    `json:"snapshot_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Materials_digest       string                    `json:"materials_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Run_state              string                    `json:"run_state" openapi:"{\"type\":\"string\"}"`
	Attempt_state          string                    `json:"attempt_state" openapi:"{\"type\":\"string\"}"`
	Gate_state             string                    `json:"gate_state" openapi:"{\"type\":\"string\",\"enum\":[\"blocked\",\"needs_human\"]}"`
	Gate_reasons           []ContractAuditGateReason `json:"gate_reasons" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractAuditGateReason\"}"`
	Finding_count          int64                     `json:"finding_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Verified_finding_count int64                     `json:"verified_finding_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Model_failure          bool                      `json:"model_failure" openapi:"{\"type\":\"boolean\"}"`
	Bound_by               string                    `json:"bound_by" openapi:"{\"type\":\"string\"}"`
	Bound_at               string                    `json:"bound_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractAuditBindingList struct {
	Contract_revision_id string                 `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Items                []ContractAuditBinding `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractAuditBinding\"}"`
}
type ApprovalInboxItem struct {
	View                      string   `json:"view" openapi:"{\"type\":\"string\",\"enum\":[\"todo\",\"done\",\"initiated\"]}"`
	Contract_id               string   `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_name             string   `json:"contract_name" openapi:"{\"type\":\"string\"}"`
	Contract_number           any      `json:"contract_number" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Contract_state            string   `json:"contract_state" openapi:"{\"type\":\"string\"}"`
	Contract_revision_id      string   `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Approval_round_id         string   `json:"approval_round_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Round_number              int64    `json:"round_number" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Round_state               string   `json:"round_state" openapi:"{\"type\":\"string\",\"enum\":[\"in_review\",\"returned\",\"withdrawn\",\"approved\",\"terminated\"]}"`
	Round_started_at          string   `json:"round_started_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Round_ended_at            any      `json:"round_ended_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Task_id                   any      `json:"task_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Node_order                any      `json:"node_order" openapi:"{\"type\":[\"integer\",\"null\"],\"minimum\":1}"`
	Node_code                 any      `json:"node_code" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Role_code                 any      `json:"role_code" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Assignee_subject_id       any      `json:"assignee_subject_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Assignee_display          any      `json:"assignee_display" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Task_state                any      `json:"task_state" openapi:"{\"anyOf\":[{\"type\":\"string\",\"enum\":[\"queued\",\"active\",\"approved\",\"returned\",\"cancelled\"]},{\"type\":\"null\"}]}"`
	Task_row_version          any      `json:"task_row_version" openapi:"{\"type\":[\"integer\",\"null\"],\"minimum\":1}"`
	Task_activated_at         any      `json:"task_activated_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Task_decided_at           any      `json:"task_decided_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Decision_id               any      `json:"decision_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Decision_action           any      `json:"decision_action" openapi:"{\"anyOf\":[{\"type\":\"string\",\"enum\":[\"approve\",\"return\"]},{\"type\":\"null\"}]}"`
	Decision_actor_subject_id any      `json:"decision_actor_subject_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Decision_actor_display    any      `json:"decision_actor_display" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Decision_opinion          any      `json:"decision_opinion" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Decision_decided_at       any      `json:"decision_decided_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Allowed_actions           []string `json:"allowed_actions" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\",\"enum\":[\"approve\",\"return\"]}"`
}
type ApprovalInboxPage struct {
	Items       []ApprovalInboxItem `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ApprovalInboxItem\"}"`
	Next_cursor any                 `json:"next_cursor" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Has_more    bool                `json:"has_more" openapi:"{\"type\":\"boolean\"}"`
}
type ApprovalRoundReadSummary struct {
	Approval_round_id         string `json:"approval_round_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Round_number              int64  `json:"round_number" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Contract_revision_id      string `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	State                     string `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"in_review\",\"returned\",\"withdrawn\",\"approved\",\"terminated\"]}"`
	Workflow_template_id      string `json:"workflow_template_id" openapi:"{\"type\":\"string\"}"`
	Workflow_template_version string `json:"workflow_template_version" openapi:"{\"type\":\"string\"}"`
	Initiated_by              string `json:"initiated_by" openapi:"{\"type\":\"string\"}"`
	Initiated_by_display      string `json:"initiated_by_display" openapi:"{\"type\":\"string\"}"`
	Started_at                string `json:"started_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Ended_at                  any    `json:"ended_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Previous_round_id         any    `json:"previous_round_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Row_version               int64  `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ApprovalReadTask struct {
	Task_id             string `json:"task_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Node_order          int64  `json:"node_order" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Node_code           string `json:"node_code" openapi:"{\"type\":\"string\"}"`
	Role_code           string `json:"role_code" openapi:"{\"type\":\"string\"}"`
	Assignee_subject_id string `json:"assignee_subject_id" openapi:"{\"type\":\"string\"}"`
	Assignee_display    string `json:"assignee_display" openapi:"{\"type\":\"string\"}"`
	State               string `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"queued\",\"active\",\"approved\",\"returned\",\"cancelled\"]}"`
	Row_version         int64  `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Activated_at        any    `json:"activated_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Decided_at          any    `json:"decided_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
}
type ApprovalReadDecision struct {
	Decision_id      string `json:"decision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Task_id          string `json:"task_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Node_order       int64  `json:"node_order" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Node_code        string `json:"node_code" openapi:"{\"type\":\"string\"}"`
	Action           string `json:"action" openapi:"{\"type\":\"string\",\"enum\":[\"approve\",\"return\"]}"`
	Actor_subject_id string `json:"actor_subject_id" openapi:"{\"type\":\"string\"}"`
	Actor_display    string `json:"actor_display" openapi:"{\"type\":\"string\"}"`
	Opinion          string `json:"opinion" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":2000}"`
	Decided_at       string `json:"decided_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ApprovalRoundReadDetail struct {
	Contract_id             string                   `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_name           string                   `json:"contract_name" openapi:"{\"type\":\"string\"}"`
	Contract_number         any                      `json:"contract_number" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Contract_state          string                   `json:"contract_state" openapi:"{\"type\":\"string\"}"`
	Contract_row_version    int64                    `json:"contract_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Current_revision_id     any                      `json:"current_revision_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Round                   ApprovalRoundReadSummary `json:"round" openapi:"{\"$ref\":\"#/components/schemas/ApprovalRoundReadSummary\"}"`
	Tasks                   []ApprovalReadTask       `json:"tasks" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ApprovalReadTask\"}"`
	Decisions               []ApprovalReadDecision   `json:"decisions" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ApprovalReadDecision\"}"`
	Active_task_id          any                      `json:"active_task_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Risk_disposition_state  string                   `json:"risk_disposition_state" openapi:"{\"type\":\"string\"}"`
	Risk_disposition_reason string                   `json:"risk_disposition_reason,omitempty" openapi:"{\"type\":\"string\"}"`
	Previous_round          any                      `json:"previous_round" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ApprovalRoundReadSummary\"},{\"type\":\"null\"}]}"`
	Revision_changed        bool                     `json:"revision_changed" openapi:"{\"type\":\"boolean\"}"`
	Revision_diff           any                      `json:"revision_diff" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ContractVersionDiff\"},{\"type\":\"null\"}]}"`
	Allowed_actions         []string                 `json:"allowed_actions" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\",\"enum\":[\"approve\",\"return\"]}"`
}
type ApprovalTaskReadDetail struct {
	Selected_task_id string                    `json:"selected_task_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Round_detail     ApprovalRoundReadDetail   `json:"round_detail" openapi:"{\"$ref\":\"#/components/schemas/ApprovalRoundReadDetail\"}"`
	Materials        []ContractVersionMaterial `json:"materials" openapi:"{\"type\":\"array\",\"description\":\"冻结版本材料与该任务提交时白名单的交集；空数组表示默认拒绝，不回退为全部材料。\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractVersionMaterial\"}"`
}
type ApprovalRoundHistoryItem struct {
	Round          ApprovalRoundReadSummary `json:"round" openapi:"{\"$ref\":\"#/components/schemas/ApprovalRoundReadSummary\"}"`
	Active_task    any                      `json:"active_task" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ApprovalReadTask\"},{\"type\":\"null\"}]}"`
	Task_count     int64                    `json:"task_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Decision_count int64                    `json:"decision_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type ApprovalRoundHistoryPage struct {
	Items       []ApprovalRoundHistoryItem `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ApprovalRoundHistoryItem\"}"`
	Next_cursor any                        `json:"next_cursor" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Has_more    bool                       `json:"has_more" openapi:"{\"type\":\"boolean\"}"`
}
type ContractLedgerFilter struct {
	Text               string `json:"text,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Supplier_name      string `json:"supplier_name,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Department_id      string `json:"department_id,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Contract_state     string `json:"contract_state,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"draft\",\"ready_to_sign\",\"registered\",\"performing\",\"closed\",\"terminated\",\"external_observed\"]}"`
	Workflow_state     string `json:"workflow_state,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"not_started\",\"in_review\",\"external_observed\"]}"`
	Authority_mode     string `json:"authority_mode,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"native\",\"external\"]}"`
	Validity_state     string `json:"validity_state,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"active\",\"voided\"]}"`
	Project_type       string `json:"project_type,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Procurement_method string `json:"procurement_method,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Amount_min         string `json:"amount_min,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,15})\\\\.[0-9]{2}$\"}"`
	Amount_max         string `json:"amount_max,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,15})\\\\.[0-9]{2}$\"}"`
	Signing_date_from  string `json:"signing_date_from,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
	Signing_date_to    string `json:"signing_date_to,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
	Created_from       string `json:"created_from,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Created_to         string `json:"created_to,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractLedgerItem struct {
	Contract_id        string `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Authority_mode     string `json:"authority_mode" openapi:"{\"type\":\"string\",\"enum\":[\"native\",\"external\"]}"`
	Name               string `json:"name" openapi:"{\"type\":\"string\"}"`
	Number             any    `json:"number" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Amount             any    `json:"amount" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Currency           any    `json:"currency" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Contract_state     string `json:"contract_state" openapi:"{\"type\":\"string\",\"enum\":[\"draft\",\"ready_to_sign\",\"registered\",\"performing\",\"closed\",\"terminated\",\"external_observed\"]}"`
	Workflow_state     string `json:"workflow_state" openapi:"{\"type\":\"string\",\"enum\":[\"not_started\",\"in_review\",\"external_observed\"]}"`
	Supplier_name      any    `json:"supplier_name,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Department_id      any    `json:"department_id,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Project_type       any    `json:"project_type,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Procurement_method any    `json:"procurement_method,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Signing_date       any    `json:"signing_date,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Source_system      any    `json:"source_system,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Owner_org_id       string `json:"owner_org_id" openapi:"{\"type\":\"string\"}"`
	Row_version        int64  `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Created_at         string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Updated_at         string `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractLedgerPage struct {
	Items       []ContractLedgerItem `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractLedgerItem\"}"`
	Next_cursor any                  `json:"next_cursor" openapi:"{\"type\":[\"string\",\"null\"],\"maxLength\":2048}"`
	Has_more    bool                 `json:"has_more" openapi:"{\"type\":\"boolean\"}"`
}
type ContractSavedView struct {
	View_id     string               `json:"view_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Name        string               `json:"name" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":60}"`
	Filter      ContractLedgerFilter `json:"filter" openapi:"{\"$ref\":\"#/components/schemas/ContractLedgerFilter\"}"`
	Columns     []string             `json:"columns" openapi:"{\"type\":\"array\",\"maxItems\":20}" openapi_items:"{\"type\":\"string\",\"enum\":[\"contract_number\",\"contract_name\",\"authority_mode\",\"supplier_name\",\"amount\",\"currency\",\"department_id\",\"project_type\",\"procurement_method\",\"contract_state\",\"workflow_state\",\"signing_date\",\"source_system\",\"created_at\",\"updated_at\"]}"`
	Row_version int64                `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Created_at  string               `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Updated_at  string               `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractSavedViewList struct {
	Items []ContractSavedView `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractSavedView\"}"`
}
type ContractSavedViewRequest struct {
	Name    string               `json:"name" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":60}"`
	Filter  ContractLedgerFilter `json:"filter,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ContractLedgerFilter\"}"`
	Columns []string             `json:"columns,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":20}" openapi_items:"{\"type\":\"string\",\"enum\":[\"contract_number\",\"contract_name\",\"authority_mode\",\"supplier_name\",\"amount\",\"currency\",\"department_id\",\"project_type\",\"procurement_method\",\"contract_state\",\"workflow_state\",\"signing_date\",\"source_system\",\"created_at\",\"updated_at\"]}"`
}
type ContractSavedViewReplaceRequest struct {
	Expected_row_version int64                `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Name                 string               `json:"name" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":60}"`
	Filter               ContractLedgerFilter `json:"filter,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ContractLedgerFilter\"}"`
	Columns              []string             `json:"columns,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":20}" openapi_items:"{\"type\":\"string\",\"enum\":[\"contract_number\",\"contract_name\",\"authority_mode\",\"supplier_name\",\"amount\",\"currency\",\"department_id\",\"project_type\",\"procurement_method\",\"contract_state\",\"workflow_state\",\"signing_date\",\"source_system\",\"created_at\",\"updated_at\"]}"`
}
type ContractSavedViewDeleteResult struct {
	Deleted bool `json:"deleted" openapi:"{\"type\":\"boolean\",\"const\":true}"`
}
type ContractExportRequest struct {
	Filter  ContractLedgerFilter `json:"filter" openapi:"{\"$ref\":\"#/components/schemas/ContractLedgerFilter\"}"`
	Columns []string             `json:"columns,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":20}" openapi_items:"{\"type\":\"string\",\"enum\":[\"contract_number\",\"contract_name\",\"authority_mode\",\"supplier_name\",\"amount\",\"currency\",\"department_id\",\"project_type\",\"procurement_method\",\"contract_state\",\"workflow_state\",\"signing_date\",\"source_system\",\"created_at\",\"updated_at\"]}"`
	Format  string               `json:"format,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"csv\"],\"default\":\"csv\"}"`
}
type ContractExport struct {
	Export_id          string   `json:"export_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	State              string   `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"ready\"]}"`
	Format             string   `json:"format" openapi:"{\"type\":\"string\",\"enum\":[\"csv\"]}"`
	Columns            []string `json:"columns" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\",\"enum\":[\"contract_number\",\"contract_name\",\"authority_mode\",\"supplier_name\",\"amount\",\"currency\",\"department_id\",\"project_type\",\"procurement_method\",\"contract_state\",\"workflow_state\",\"signing_date\",\"source_system\",\"created_at\",\"updated_at\"]}"`
	Row_count          int64    `json:"row_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Created_at         string   `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Expires_at         string   `json:"expires_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Download_available bool     `json:"download_available" openapi:"{\"type\":\"boolean\"}"`
	Replayed           bool     `json:"replayed" openapi:"{\"type\":\"boolean\"}"`
}
type DraftContent struct {
	Contract         ContractBasics        `json:"contract" openapi:"{\"$ref\":\"#/components/schemas/ContractBasics\"}"`
	Buyer            DraftBuyer            `json:"buyer,omitempty" openapi:"{\"$ref\":\"#/components/schemas/DraftBuyer\"}"`
	Supplier         DraftSupplier         `json:"supplier,omitempty" openapi:"{\"$ref\":\"#/components/schemas/DraftSupplier\"}"`
	Items            []DraftItem           `json:"items,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":1000}" openapi_items:"{\"$ref\":\"#/components/schemas/DraftItem\"}"`
	Assets           []DraftAssetPlan      `json:"assets,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":1000}" openapi_items:"{\"$ref\":\"#/components/schemas/DraftAssetPlan\"}"`
	Funding          []DraftFunding        `json:"funding,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":100}" openapi_items:"{\"$ref\":\"#/components/schemas/DraftFunding\"}"`
	Payments         []DraftPaymentPlan    `json:"payments,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":1000}" openapi_items:"{\"$ref\":\"#/components/schemas/DraftPaymentPlan\"}"`
	Delivery_plans   []DraftDeliveryPlan   `json:"delivery_plans,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":1000}" openapi_items:"{\"$ref\":\"#/components/schemas/DraftDeliveryPlan\"}"`
	Acceptance_plans []DraftAcceptancePlan `json:"acceptance_plans,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":1000}" openapi_items:"{\"$ref\":\"#/components/schemas/DraftAcceptancePlan\"}"`
	Materials        []MaterialRef         `json:"materials,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":500}" openapi_items:"{\"$ref\":\"#/components/schemas/MaterialRef\"}"`
}
type ContractBasics struct {
	Project_number              string         `json:"project_number,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Project_type                string         `json:"project_type,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Name                        string         `json:"name,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Number                      string         `json:"number,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Classification              string         `json:"classification,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Category                    string         `json:"category,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Grade                       string         `json:"grade,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Drafting_party              string         `json:"drafting_party,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"buyer\",\"supplier\",\"joint\",\"other\"]}"`
	Department_id               string         `json:"department_id,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Handler_user_id             string         `json:"handler_user_id,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Handler_contact             string         `json:"handler_contact,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Related_parties             []PartyRef     `json:"related_parties,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":50}" openapi_items:"{\"$ref\":\"#/components/schemas/PartyRef\"}"`
	Amount                      string         `json:"amount,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,15})\\\\.[0-9]{2}$\",\"description\":\"合同金额，两位小数的规范十进制字符串。\"}"`
	Currency                    string         `json:"currency,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^[A-Z]{3}$\"}"`
	Settlement_mode             string         `json:"settlement_mode,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100,\"description\":\"结算模式声明；固定总价、框架和按量的恒等式由提交校验执行，不由草稿推断。\"}"`
	Prices_include_tax          bool           `json:"prices_include_tax,omitempty" openapi:"{\"type\":\"boolean\",\"description\":\"单价是否含税的声明；未知时留空，不能按不含税处理。\"}"`
	Is_ppp_project              any            `json:"is_ppp_project,omitempty" openapi:"{\"type\":[\"boolean\",\"null\"]}"`
	Is_ndrc_engineering_project any            `json:"is_ndrc_engineering_project,omitempty" openapi:"{\"type\":[\"boolean\",\"null\"]}"`
	Is_fixed_asset              any            `json:"is_fixed_asset,omitempty" openapi:"{\"type\":[\"boolean\",\"null\"]}"`
	Procurement_result_package  ProcurementRef `json:"procurement_result_package,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ProcurementRef\"}"`
	Procurement_method          string         `json:"procurement_method,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Signing_date                string         `json:"signing_date,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
	Performance_start_date      string         `json:"performance_start_date,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
	Performance_end_date        string         `json:"performance_end_date,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
	Performance_location        string         `json:"performance_location,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":1000}"`
	Business_terms_verified     bool           `json:"business_terms_verified,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Performance_bond_required   any            `json:"performance_bond_required,omitempty" openapi:"{\"type\":[\"boolean\",\"null\"]}"`
	Remark                      string         `json:"remark,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":4000}"`
}
type DraftBuyer struct {
	Purchasing_unit UnitRef `json:"purchasing_unit,omitempty" openapi:"{\"$ref\":\"#/components/schemas/UnitRef\"}"`
	Contact_name    string  `json:"contact_name,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Contact_phone   string  `json:"contact_phone,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
}
type DraftSupplier struct {
	Enterprise_type  string   `json:"enterprise_type,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Name             string   `json:"name,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Credit_code      string   `json:"credit_code,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Enterprise_scale string   `json:"enterprise_scale,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Accepting_entity string   `json:"accepting_entity,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Region           string   `json:"region,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Address          string   `json:"address,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":1000}"`
	Special_nature   []string `json:"special_nature,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":20}" openapi_items:"{\"type\":\"string\",\"maxLength\":100}"`
	Payee_name       string   `json:"payee_name,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200,\"description\":\"受限字段：加密存储，按授权明文显示，其他可见者为固定掩码。\"}"`
	Bank_name        string   `json:"bank_name,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200,\"description\":\"受限字段：加密存储，按授权明文显示，其他可见者为固定掩码。\"}"`
	Bank_account     string   `json:"bank_account,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":64,\"description\":\"受限字段：保留前导零、加密存储、脱敏展示。\"}"`
	Contact_name     string   `json:"contact_name,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Contact_phone    string   `json:"contact_phone,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Tax_identifier   string   `json:"tax_identifier,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100,\"description\":\"受限字段：加密存储，不与信用代码无条件互相覆盖。\"}"`
}
type DraftItem struct {
	Item_id       string      `json:"item_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Name          string      `json:"name,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Catalog       string      `json:"catalog,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Brand         string      `json:"brand,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Specification string      `json:"specification,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":1000}"`
	Attributes    []Attribute `json:"attributes,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":50}" openapi_items:"{\"$ref\":\"#/components/schemas/Attribute\"}"`
	Quantity      string      `json:"quantity,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,11})\\\\.[0-9]{6}$\"}"`
	Unit          string      `json:"unit,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":40}"`
	Unit_price    string      `json:"unit_price,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,13})\\\\.[0-9]{4}$\"}"`
	Budget_amount string      `json:"budget_amount,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,15})\\\\.[0-9]{2}$\"}"`
	Line_amount   string      `json:"line_amount,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,15})\\\\.[0-9]{2}$\"}"`
	Tax_rate      string      `json:"tax_rate,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0\\\\.[0-9]{4}|1\\\\.0000)$\",\"description\":\"税率比例；未知留空，不按零处理。\"}"`
}
type DraftAssetPlan struct {
	Asset_plan_id     string `json:"asset_plan_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Item_id           string `json:"item_id,omitempty" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Name              string `json:"name,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Code              string `json:"code,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Brand             string `json:"brand,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Specification     string `json:"specification,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":1000}"`
	Origin            string `json:"origin,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Manufacturer      string `json:"manufacturer,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Quantity          string `json:"quantity,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,11})\\\\.[0-9]{6}$\"}"`
	Unit_price        string `json:"unit_price,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,13})\\\\.[0-9]{4}$\"}"`
	Department_code   string `json:"department_code,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Department_name   string `json:"department_name,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Custodian_user_id string `json:"custodian_user_id,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Remark            string `json:"remark,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":2000}"`
}
type DraftFunding struct {
	Nature       string `json:"nature,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Channel      string `json:"channel,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Amount       string `json:"amount,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,15})\\\\.[0-9]{2}$\"}"`
	Funding_year int64  `json:"funding_year,omitempty" openapi:"{\"type\":\"integer\",\"minimum\":1900,\"maximum\":2999}"`
	Cost_center  string `json:"cost_center,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Quota_number string `json:"quota_number,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
}
type DraftPaymentPlan struct {
	Sequence       int64            `json:"sequence" openapi:"{\"type\":\"integer\",\"minimum\":1,\"maximum\":1000}"`
	Is_advance     bool             `json:"is_advance,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Description    string           `json:"description,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":2000}"`
	Planned_date   string           `json:"planned_date,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
	Advance_amount string           `json:"advance_amount,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,15})\\\\.[0-9]{2}$\"}"`
	Unit_amount    string           `json:"unit_amount,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,15})\\\\.[0-9]{2}$\"}"`
	Fiscal_amount  string           `json:"fiscal_amount,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,15})\\\\.[0-9]{2}$\"}"`
	Amount         string           `json:"amount,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,15})\\\\.[0-9]{2}$\"}"`
	Ratio          string           `json:"ratio,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0\\\\.[0-9]{6}|1\\\\.000000)$\"}"`
	Ratio_base     string           `json:"ratio_base,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":200,\"description\":\"本期比例的计算基数声明；未声明时不得默认等于合同金额。\"}"`
	Condition      PaymentCondition `json:"condition,omitempty" openapi:"{\"$ref\":\"#/components/schemas/PaymentCondition\"}"`
}
type DraftDeliveryPlan struct {
	Sequence     int64  `json:"sequence" openapi:"{\"type\":\"integer\",\"minimum\":1,\"maximum\":1000}"`
	Item_id      string `json:"item_id,omitempty" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Planned_date string `json:"planned_date,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
	Description  string `json:"description,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":2000}"`
	Quantity     string `json:"quantity,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,11})\\\\.[0-9]{6}$\"}"`
}
type DraftAcceptancePlan struct {
	Sequence     int64    `json:"sequence" openapi:"{\"type\":\"integer\",\"minimum\":1,\"maximum\":1000}"`
	Planned_date string   `json:"planned_date,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
	Description  string   `json:"description,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":2000}"`
	Criteria     string   `json:"criteria,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":2000}"`
	Item_ids     []string `json:"item_ids,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":1000}" openapi_items:"{\"type\":\"string\",\"format\":\"uuid\"}"`
}
type PaymentCondition struct {
	Type                  string   `json:"type" openapi:"{\"type\":\"string\",\"enum\":[\"signed_contract\",\"all_goods_accepted\",\"partial_acceptance\",\"service_period_verified\",\"custom_policy\"]}"`
	Evidence_requirements []string `json:"evidence_requirements,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":30}" openapi_items:"{\"type\":\"string\",\"maxLength\":100}"`
	Due_rule              DueRule  `json:"due_rule,omitempty" openapi:"{\"$ref\":\"#/components/schemas/DueRule\"}"`
	Policy_ref            string   `json:"policy_ref,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
}
type DueRule struct {
	Trigger    string `json:"trigger" openapi:"{\"type\":\"string\",\"enum\":[\"fixed_date\",\"contract_signed\",\"acceptance_passed\",\"invoice_verified\",\"other_policy_event\"]}"`
	Days_after int64  `json:"days_after,omitempty" openapi:"{\"type\":\"integer\",\"minimum\":0,\"maximum\":36500}"`
	Calendar   string `json:"calendar,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"calendar_days\",\"business_days\"]}"`
	Policy_ref string `json:"policy_ref,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
}
type PartyRef struct {
	Party_id           string                 `json:"party_id" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Role               string                 `json:"role" openapi:"{\"type\":\"string\",\"enum\":[\"buyer\",\"supplier\",\"guarantor\",\"consortium_member\",\"other\"]}"`
	Name_snapshot      string                 `json:"name_snapshot" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Binding_id         string                 `json:"binding_id,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100,\"description\":\"来源案件中的主体绑定 ID；不填时服务端要求唯一有效绑定。\"}"`
	Reference_snapshot PartyReferenceSnapshot `json:"reference_snapshot,omitempty" openapi:"{\"$ref\":\"#/components/schemas/PartyReferenceSnapshot\",\"readOnly\":true}"`
}
type UnitRef struct {
	Id   string `json:"id" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Name string `json:"name" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
}
type ProcurementRef struct {
	System          string                    `json:"system" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Result_id       string                    `json:"result_id" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Package_id      string                    `json:"package_id" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Snapshot_ref    string                    `json:"snapshot_ref" openapi:"{\"type\":\"string\",\"maxLength\":100,\"description\":\"同一合同快照中采购或中标材料的 material_version_id。\"}"`
	Source_snapshot ProcurementSourceSnapshot `json:"source_snapshot,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ProcurementSourceSnapshot\",\"readOnly\":true}"`
}
type Attribute struct {
	Name  string `json:"name" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Value string `json:"value" openapi:"{\"type\":\"string\",\"maxLength\":500}"`
	Unit  string `json:"unit,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":40}"`
}
type MaterialRef struct {
	Material_id         string `json:"material_id" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Material_version_id string `json:"material_version_id" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Sha256              string `json:"sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Purpose             string `json:"purpose" openapi:"{\"type\":\"string\",\"enum\":[\"draft\",\"signed_contract\",\"supporting\",\"acceptance\",\"payment\",\"other\"]}"`
}
type ContractSourceCapability struct {
	Contract_id       string `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Source_id         string `json:"source_id" openapi:"{\"type\":\"string\"}"`
	Source_kind       string `json:"source_kind" openapi:"{\"type\":\"string\",\"enum\":[\"mock\",\"real\",\"unconfigured\"]}"`
	Authority_mode    string `json:"authority_mode" openapi:"{\"type\":\"string\",\"const\":\"external\"}"`
	Read_only         bool   `json:"read_only" openapi:"{\"type\":\"boolean\",\"const\":true}"`
	Can_read_contract bool   `json:"can_read_contract" openapi:"{\"type\":\"boolean\"}"`
	Version_ordering  string `json:"version_ordering" openapi:"{\"type\":\"string\",\"enum\":[\"opaque\",\"monotonic-sequence\",\"unknown\"]}"`
	Connection_status string `json:"connection_status" openapi:"{\"type\":\"string\",\"const\":\"not_probed\"}"`
}
type ContractSourceObservationRequest struct {
	Event_id             string       `json:"event_id" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":300}"`
	External_contract_id string       `json:"external_contract_id" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":200}"`
	Source_version       string       `json:"source_version" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":200}"`
	Source_sequence      any          `json:"source_sequence,omitempty" openapi:"{\"type\":[\"integer\",\"null\"],\"minimum\":1,\"maximum\":9223372036854775807}"`
	Source_state         string       `json:"source_state" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Occurred_at          string       `json:"occurred_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Case_org_id          string       `json:"case_org_id" openapi:"{\"type\":\"string\"}"`
	Case_id              string       `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Snapshot             DraftContent `json:"snapshot" openapi:"{\"$ref\":\"#/components/schemas/DraftContent\"}"`
}
type ContractSourceObservationResult struct {
	Observation_id         string `json:"observation_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_id            string `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Source_event_id        string `json:"source_event_id" openapi:"{\"type\":\"string\"}"`
	Source_kind            string `json:"source_kind" openapi:"{\"type\":\"string\",\"enum\":[\"mock\",\"real\"]}"`
	Authority_mode         string `json:"authority_mode" openapi:"{\"type\":\"string\",\"const\":\"external\"}"`
	Disposition            string `json:"disposition" openapi:"{\"type\":\"string\",\"enum\":[\"applied\",\"observation_only\",\"unknown\"]}"`
	Replayed               bool   `json:"replayed" openapi:"{\"type\":\"boolean\"}"`
	Source_version         string `json:"source_version" openapi:"{\"type\":\"string\"}"`
	Current_source_version any    `json:"current_source_version" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Contract_revision_id   string `json:"contract_revision_id,omitempty" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Recorded_at            string `json:"recorded_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type SourceRef struct {
	System         string `json:"system" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Id             string `json:"id" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Source_version string `json:"source_version" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
}
type ContractSummary struct {
	Contract_id    string `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Authority_mode string `json:"authority_mode" openapi:"{\"type\":\"string\",\"enum\":[\"native\",\"external\"]}"`
	Name           string `json:"name" openapi:"{\"type\":\"string\"}"`
	Number         any    `json:"number,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Amount         any    `json:"amount,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Currency       any    `json:"currency,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Contract_state string `json:"contract_state" openapi:"{\"type\":\"string\",\"enum\":[\"draft\",\"ready_to_sign\",\"registered\",\"performing\",\"closed\",\"terminated\",\"external_observed\"]}"`
	Workflow_state string `json:"workflow_state" openapi:"{\"type\":\"string\",\"enum\":[\"not_started\",\"in_review\",\"returned\",\"withdrawn\",\"approved\",\"terminated\",\"external_observed\"]}"`
	Payment_state  any    `json:"payment_state,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Row_version    int64  `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Created_at     string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type SigningJSONValue any
type ContractSigningParty struct {
	Role string `json:"role" openapi:"{\"type\":\"string\",\"enum\":[\"buyer\",\"supplier\",\"witness\",\"other\"]}"`
	Name string `json:"name" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":200}"`
}
type ContractDeliveryRequest struct {
	Expected_row_version   int64         `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Contract_revision_id   string        `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Delivery_plan_sequence int64         `json:"delivery_plan_sequence,omitempty" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Item_id                string        `json:"item_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Batch_reference        string        `json:"batch_reference" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":200}"`
	Quantity               string        `json:"quantity" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,11})(\\\\.[0-9]{1,6})?$\"}"`
	Actual_at              string        `json:"actual_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Location               string        `json:"location" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":1000}"`
	Description            string        `json:"description,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":4000}"`
	Evidence               []MaterialRef `json:"evidence" openapi:"{\"type\":\"array\",\"minItems\":1,\"maxItems\":50}" openapi_items:"{\"$ref\":\"#/components/schemas/MaterialRef\"}"`
}
type ContractDelivery struct {
	Delivery_id            string                    `json:"delivery_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_id            string                    `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id   string                    `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Delivery_plan_sequence int64                     `json:"delivery_plan_sequence,omitempty" openapi:"{\"type\":\"integer\"}"`
	Item_id                string                    `json:"item_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Item_name              string                    `json:"item_name" openapi:"{\"type\":\"string\"}"`
	Batch_reference        string                    `json:"batch_reference" openapi:"{\"type\":\"string\"}"`
	Quantity               string                    `json:"quantity" openapi:"{\"type\":\"string\"}"`
	Unit                   string                    `json:"unit" openapi:"{\"type\":\"string\"}"`
	Actual_at              string                    `json:"actual_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Location               string                    `json:"location" openapi:"{\"type\":\"string\"}"`
	Description            string                    `json:"description" openapi:"{\"type\":\"string\"}"`
	Evidence               []ContractVersionMaterial `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractVersionMaterial\"}"`
	Evidence_redacted      bool                      `json:"evidence_redacted,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Recorded_by            string                    `json:"recorded_by" openapi:"{\"type\":\"string\"}"`
	Recorded_at            string                    `json:"recorded_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractFulfillmentItemSummary struct {
	Contract_revision_id          string `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Item_id                       string `json:"item_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Item_name                     string `json:"item_name" openapi:"{\"type\":\"string\"}"`
	Unit                          string `json:"unit,omitempty" openapi:"{\"type\":\"string\"}"`
	Planned_quantity              string `json:"planned_quantity,omitempty" openapi:"{\"type\":\"string\"}"`
	Delivered_quantity            string `json:"delivered_quantity" openapi:"{\"type\":\"string\"}"`
	Qualified_quantity            string `json:"qualified_quantity" openapi:"{\"type\":\"string\"}"`
	Unallocated_quantity          string `json:"unallocated_quantity" openapi:"{\"type\":\"string\"}"`
	Pending_reinspection_quantity string `json:"pending_reinspection_quantity" openapi:"{\"type\":\"string\"}"`
}
type ContractLifecycleOverview struct{}

type ContractDeliveryList struct {
	Items       []ContractDelivery               `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractDelivery\"}"`
	Summary     []ContractFulfillmentItemSummary `json:"summary" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractFulfillmentItemSummary\"}"`
	Row_version int64                            `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ContractAcceptanceDefectInput struct {
	Prior_defect_id string `json:"prior_defect_id,omitempty" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Description     string `json:"description" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":4000}"`
	Responsible     string `json:"responsible" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":300}"`
	Remediation_due string `json:"remediation_due" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
	Failed_quantity string `json:"failed_quantity" openapi:"{\"type\":\"string\"}"`
}
type ContractAcceptanceDefect struct {
	Defect_id       string `json:"defect_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Prior_defect_id string `json:"prior_defect_id,omitempty" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Description     string `json:"description" openapi:"{\"type\":\"string\"}"`
	Responsible     string `json:"responsible" openapi:"{\"type\":\"string\"}"`
	Remediation_due string `json:"remediation_due" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
	Failed_quantity string `json:"failed_quantity" openapi:"{\"type\":\"string\"}"`
	Recorded_by     string `json:"recorded_by" openapi:"{\"type\":\"string\"}"`
	Recorded_at     string `json:"recorded_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractAcceptanceRequest struct {
	Expected_row_version     int64                           `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Contract_revision_id     string                          `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Delivery_id              string                          `json:"delivery_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Acceptance_plan_sequence int64                           `json:"acceptance_plan_sequence,omitempty" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Scope_start              string                          `json:"scope_start" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,11})(\\\\.[0-9]{1,6})?$\"}"`
	Scope_end                string                          `json:"scope_end" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,11})(\\\\.[0-9]{1,6})?$\"}"`
	Accepted_quantity        string                          `json:"accepted_quantity" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,11})(\\\\.[0-9]{1,6})?$\",\"description\":\"已通过数量，按本次半开区间的起点开始占用前缀；其余后缀为失败数量。复验范围必须等于父事实的失败后缀。\"}"`
	Reinspection_of          string                          `json:"reinspection_of,omitempty" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Rectification_ids        []string                        `json:"rectification_ids,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Actual_at                string                          `json:"actual_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Standard                 string                          `json:"standard" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":4000}"`
	Inspector_note           string                          `json:"inspector_note,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":4000}"`
	Defects                  []ContractAcceptanceDefectInput `json:"defects,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":100}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractAcceptanceDefectInput\"}"`
	Evidence                 []MaterialRef                   `json:"evidence" openapi:"{\"type\":\"array\",\"minItems\":1,\"maxItems\":50}" openapi_items:"{\"$ref\":\"#/components/schemas/MaterialRef\"}"`
}
type ContractRectificationRequest struct {
	Expected_row_version int64         `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Contract_revision_id string        `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Defect_id            string        `json:"defect_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Description          string        `json:"description" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":4000}"`
	Evidence             []MaterialRef `json:"evidence" openapi:"{\"type\":\"array\",\"minItems\":1,\"maxItems\":50}" openapi_items:"{\"$ref\":\"#/components/schemas/MaterialRef\"}"`
}
type ContractRectification struct {
	Rectification_id     string                    `json:"rectification_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_id          string                    `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id string                    `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Acceptance_id        string                    `json:"acceptance_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Defect_id            string                    `json:"defect_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Description          string                    `json:"description" openapi:"{\"type\":\"string\"}"`
	Evidence             []ContractVersionMaterial `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractVersionMaterial\"}"`
	Evidence_redacted    bool                      `json:"evidence_redacted,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Recorded_by          string                    `json:"recorded_by" openapi:"{\"type\":\"string\"}"`
	Recorded_at          string                    `json:"recorded_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractAcceptance struct {
	Acceptance_id            string                     `json:"acceptance_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_id              string                     `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id     string                     `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Delivery_id              string                     `json:"delivery_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Item_id                  string                     `json:"item_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Item_name                string                     `json:"item_name" openapi:"{\"type\":\"string\"}"`
	Acceptance_plan_sequence int64                      `json:"acceptance_plan_sequence,omitempty" openapi:"{\"type\":\"integer\"}"`
	Scope_start              string                     `json:"scope_start" openapi:"{\"type\":\"string\"}"`
	Scope_end                string                     `json:"scope_end" openapi:"{\"type\":\"string\"}"`
	Quantity                 string                     `json:"quantity" openapi:"{\"type\":\"string\"}"`
	Accepted_quantity        string                     `json:"accepted_quantity" openapi:"{\"type\":\"string\"}"`
	Failed_quantity          string                     `json:"failed_quantity" openapi:"{\"type\":\"string\"}"`
	Unit                     string                     `json:"unit" openapi:"{\"type\":\"string\"}"`
	Result                   string                     `json:"result" openapi:"{\"type\":\"string\",\"enum\":[\"passed\",\"partial\",\"failed\"]}"`
	Reinspection_of          string                     `json:"reinspection_of,omitempty" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Rectification_ids        []string                   `json:"rectification_ids,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Actual_at                string                     `json:"actual_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Standard                 string                     `json:"standard" openapi:"{\"type\":\"string\"}"`
	Inspector_note           string                     `json:"inspector_note" openapi:"{\"type\":\"string\"}"`
	Evidence                 []ContractVersionMaterial  `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractVersionMaterial\"}"`
	Evidence_redacted        bool                       `json:"evidence_redacted,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Defects                  []ContractAcceptanceDefect `json:"defects" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractAcceptanceDefect\"}"`
	Rectifications           []ContractRectification    `json:"rectifications" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractRectification\"}"`
	Recorded_by              string                     `json:"recorded_by" openapi:"{\"type\":\"string\"}"`
	Recorded_at              string                     `json:"recorded_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractAcceptanceList struct {
	Items       []ContractAcceptance             `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractAcceptance\"}"`
	Summary     []ContractFulfillmentItemSummary `json:"summary" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractFulfillmentItemSummary\"}"`
	Row_version int64                            `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ContractSigningCheckRequest struct {
	Baseline_revision_id  string                  `json:"baseline_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Signed_evidence       MaterialRef             `json:"signed_evidence" openapi:"{\"$ref\":\"#/components/schemas/MaterialRef\"}"`
	Signed_content        DraftContent            `json:"signed_content" openapi:"{\"$ref\":\"#/components/schemas/DraftContent\"}"`
	Manual_comparison     ManualSigningComparison `json:"manual_comparison" openapi:"{\"$ref\":\"#/components/schemas/ManualSigningComparison\"}"`
	Legacy_approval_basis string                  `json:"legacy_approval_basis,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":2000}"`
}
type ContractSigningFieldChange struct {
	Path   string           `json:"path" openapi:"{\"type\":\"string\"}"`
	Change string           `json:"change" openapi:"{\"type\":\"string\",\"enum\":[\"added\",\"removed\",\"changed\"]}"`
	Before SigningJSONValue `json:"before,omitempty" openapi:"{\"$ref\":\"#/components/schemas/SigningJSONValue\"}"`
	After  SigningJSONValue `json:"after,omitempty" openapi:"{\"$ref\":\"#/components/schemas/SigningJSONValue\"}"`
}
type ContractSigningRestrictedChange struct {
	Path    string `json:"path" openapi:"{\"type\":\"string\"}"`
	Changed any    `json:"changed" openapi:"{\"type\":[\"boolean\",\"null\"]}"`
	Reason  string `json:"reason,omitempty" openapi:"{\"type\":\"string\"}"`
}
type ManualSigningComparison struct {
	Confirmed              bool   `json:"confirmed" openapi:"{\"type\":\"boolean\",\"const\":true}"`
	Signed_material_digest string `json:"signed_material_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Contract_revision_id   string `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Scope                  string `json:"scope" openapi:"{\"type\":\"string\",\"const\":\"all_structured_business_fields_and_restricted_fields\"}"`
}
type ContractSigningCheckResult struct {
	Contract_id          string                            `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Baseline_revision_id string                            `json:"baseline_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Comparison_method    string                            `json:"comparison_method" openapi:"{\"type\":\"string\",\"const\":\"structured_signed_terms/v1\"}"`
	Comparison_result    string                            `json:"comparison_result" openapi:"{\"type\":\"string\",\"enum\":[\"transcription_matches_revision\",\"material_difference\",\"comparison_unavailable\"]}"`
	Can_register         bool                              `json:"can_register" openapi:"{\"type\":\"boolean\"}"`
	Verification_status  string                            `json:"verification_status" openapi:"{\"type\":\"string\",\"enum\":[\"human_transcription_confirmed\",\"not_verified_against_source\"]}"`
	Approval_status      string                            `json:"approval_status" openapi:"{\"type\":\"string\",\"enum\":[\"approved\",\"negative_history\",\"no_history\",\"legacy_history_unknown_with_basis\"]}"`
	Fields               []ContractSigningFieldChange      `json:"fields" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractSigningFieldChange\"}"`
	Restricted_fields    []ContractSigningRestrictedChange `json:"restricted_fields" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractSigningRestrictedChange\"}"`
	Warnings             []string                          `json:"warnings" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type ContractSigningRegistrationRequest struct {
	Expected_row_version      int64                   `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Baseline_revision_id      string                  `json:"baseline_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Signing_date              string                  `json:"signing_date" openapi:"{\"type\":\"string\",\"format\":\"date\",\"minimum\":\"1900-01-01\"}"`
	Signed_evidence           MaterialRef             `json:"signed_evidence" openapi:"{\"$ref\":\"#/components/schemas/MaterialRef\"}"`
	Signed_content            DraftContent            `json:"signed_content" openapi:"{\"$ref\":\"#/components/schemas/DraftContent\"}"`
	Manual_comparison         ManualSigningComparison `json:"manual_comparison" openapi:"{\"$ref\":\"#/components/schemas/ManualSigningComparison\"}"`
	Legacy_approval_basis     string                  `json:"legacy_approval_basis,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":2000}"`
	Parties                   []ContractSigningParty  `json:"parties" openapi:"{\"type\":\"array\",\"minItems\":1,\"maxItems\":20}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractSigningParty\"}"`
	Signature_evidence_status string                  `json:"signature_evidence_status,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"unknown\",\"document_only\",\"external_evidence\"],\"default\":\"document_only\"}"`
	Signature_evidence_ref    string                  `json:"signature_evidence_ref,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":500}"`
}
type ContractSigningRegistration struct {
	Registration_id           string                  `json:"registration_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_id               string                  `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id      string                  `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Signing_date              string                  `json:"signing_date" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
	Signed_material           ContractVersionMaterial `json:"signed_material" openapi:"{\"$ref\":\"#/components/schemas/ContractVersionMaterial\"}"`
	Parties                   []ContractSigningParty  `json:"parties" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractSigningParty\"}"`
	Signature_evidence_status string                  `json:"signature_evidence_status" openapi:"{\"type\":\"string\",\"enum\":[\"unknown\",\"document_only\",\"external_evidence\"]}"`
	Signature_evidence_ref    string                  `json:"signature_evidence_ref,omitempty" openapi:"{\"type\":\"string\"}"`
	Approval_basis            string                  `json:"approval_basis" openapi:"{\"type\":\"string\",\"enum\":[\"legacy_manual_basis\",\"approved_round\"]}"`
	Approval_basis_note       string                  `json:"approval_basis_note,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":2000}"`
	Approval_evidence_status  string                  `json:"approval_evidence_status" openapi:"{\"type\":\"string\",\"enum\":[\"legacy_unknown_with_manual_basis\",\"approved_round\"]}"`
	Approval_round_id         any                     `json:"approval_round_id" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"uuid\"}"`
	Comparison_method         string                  `json:"comparison_method" openapi:"{\"type\":\"string\",\"const\":\"structured_signed_terms/v1\"}"`
	Comparison_result         string                  `json:"comparison_result" openapi:"{\"type\":\"string\",\"const\":\"human_transcription_confirmed\"}"`
	Verification_status       string                  `json:"verification_status" openapi:"{\"type\":\"string\",\"const\":\"human_transcription_confirmed\"}"`
	Comparison_confirmed_by   string                  `json:"comparison_confirmed_by" openapi:"{\"type\":\"string\"}"`
	Comparison_confirmed_at   string                  `json:"comparison_confirmed_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Comparison_scope          string                  `json:"comparison_scope" openapi:"{\"type\":\"string\",\"const\":\"all_structured_business_fields_and_restricted_fields\"}"`
	Warnings                  []string                `json:"warnings" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Registered_by             string                  `json:"registered_by" openapi:"{\"type\":\"string\"}"`
	Registered_at             string                  `json:"registered_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Contract_state            string                  `json:"contract_state,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"registered\"]}"`
	Row_version               int64                   `json:"row_version,omitempty" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ContractSigningRegistrationList struct {
	Items []ContractSigningRegistration `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractSigningRegistration\"}"`
}
type ContractSupplementRequest struct {
	Expected_row_version int64        `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Parent_revision_id   string       `json:"parent_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Relation_type        string       `json:"relation_type" openapi:"{\"type\":\"string\",\"enum\":[\"supplementary_agreement\",\"change_order\"]}"`
	Reason               string       `json:"reason" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":2000}"`
	Draft                DraftContent `json:"draft" openapi:"{\"$ref\":\"#/components/schemas/DraftContent\"}"`
}
type ContractSupplement struct {
	Parent_contract_id     string `json:"parent_contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Supplement_contract_id string `json:"supplement_contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Parent_revision_id     string `json:"parent_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Relation_type          string `json:"relation_type" openapi:"{\"type\":\"string\",\"enum\":[\"supplementary_agreement\",\"change_order\"]}"`
	Reason                 string `json:"reason" openapi:"{\"type\":\"string\"}"`
	Linked_by              string `json:"linked_by" openapi:"{\"type\":\"string\"}"`
	Linked_at              string `json:"linked_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Contract_state         string `json:"contract_state" openapi:"{\"type\":\"string\",\"enum\":[\"draft\",\"registered\",\"performing\",\"closed\",\"terminated\"]}"`
	Signing_date           string `json:"signing_date,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date\"}"`
}
type ContractSupplementResult struct {
	Supplement ContractSupplement `json:"supplement" openapi:"{\"$ref\":\"#/components/schemas/ContractSupplement\"}"`
	Contract   ContractDetail     `json:"contract" openapi:"{\"$ref\":\"#/components/schemas/ContractDetail\"}"`
}
type ContractSupplementList struct {
	Parent any                  `json:"parent,omitempty" openapi:"{\"oneOf\":[{\"$ref\":\"#/components/schemas/ContractSupplement\"},{\"type\":\"null\"}]}"`
	Items  []ContractSupplement `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractSupplement\"}"`
}
type ContractDetail struct {
	Summary                        ContractSummary             `json:"summary" openapi:"{\"$ref\":\"#/components/schemas/ContractSummary\"}"`
	Current_revision_id            any                         `json:"current_revision_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Draft                          DraftContent                `json:"draft,omitempty" openapi:"{\"$ref\":\"#/components/schemas/DraftContent\"}"`
	Active_round_id                any                         `json:"active_round_id,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Allowed_commands               []string                    `json:"allowed_commands" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Source_ref                     SourceRef                   `json:"source_ref,omitempty" openapi:"{\"$ref\":\"#/components/schemas/SourceRef\"}"`
	Source_observed_at             string                      `json:"source_observed_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Signing_registration           ContractSigningRegistration `json:"signing_registration,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ContractSigningRegistration\"}"`
	Warnings                       []string                    `json:"warnings" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Source_observed_state          string                      `json:"source_observed_state,omitempty" openapi:"{\"type\":\"string\"}"`
	Source_observation_disposition string                      `json:"source_observation_disposition,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"applied\",\"observation_only\",\"unknown\"]}"`
	Source_id                      string                      `json:"source_id,omitempty" openapi:"{\"type\":\"string\"}"`
}
type ContractVersionFreezeRequest struct {
	Expected_row_version int64  `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Case_id              string `json:"case_id,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":100,\"description\":\"原生草稿无来源案件链接时必填；已链接合同传入时必须与唯一来源案件一致。\"}"`
}
type ContractVersion struct {
	Contract_revision_id string `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Revision_number      int64  `json:"revision_number" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Amount               any    `json:"amount" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Currency             any    `json:"currency" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Snapshot_digest      string `json:"snapshot_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Materials_digest     string `json:"materials_digest,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Content_fingerprint  string `json:"content_fingerprint,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Amounts_reconciled   bool   `json:"amounts_reconciled" openapi:"{\"type\":\"boolean\"}"`
	Material_count       int64  `json:"material_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Frozen_by            string `json:"frozen_by" openapi:"{\"type\":\"string\"}"`
	Frozen_at            string `json:"frozen_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Audit_state          string `json:"audit_state" openapi:"{\"type\":\"string\",\"enum\":[\"unbound\",\"current\",\"stale\"]}"`
	Audit_gate_state     string `json:"audit_gate_state" openapi:"{\"type\":\"string\",\"enum\":[\"not_applicable\",\"blocked\",\"needs_human\"]}"`
}
type ContractVersionFreezeResult struct {
	Version             ContractVersion `json:"version" openapi:"{\"$ref\":\"#/components/schemas/ContractVersion\"}"`
	Contract_state      string          `json:"contract_state" openapi:"{\"type\":\"string\",\"enum\":[\"draft\",\"ready_to_sign\",\"registered\",\"performing\",\"closed\",\"terminated\"]}"`
	Current_revision_id string          `json:"current_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Row_version         int64           `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Allowed_commands    []string        `json:"allowed_commands" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Source_case_id      string          `json:"source_case_id" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
}
type ContractVersionList struct {
	Items []ContractVersion `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractVersion\"}"`
}
type ContractPaymentEligibilityRequest struct {
	Contract_revision_id string `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Payment_sequence     int64  `json:"payment_sequence" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Expected_row_version int64  `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ContractPaymentEligibility struct {
	Check_id             string   `json:"check_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_id          string   `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id string   `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Payment_sequence     int64    `json:"payment_sequence" openapi:"{\"type\":\"integer\"}"`
	Status               string   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"eligible\",\"blocked\",\"unknown\"]}"`
	Reasons              []string `json:"reasons" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Evidence             []string `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Checked_at           string   `json:"checked_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Contract_row_version int64    `json:"contract_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Initiates_payment    bool     `json:"initiates_payment" openapi:"{\"type\":\"boolean\",\"const\":false}"`
}
type ContractPaymentReceiptRequest struct {
	Contract_id          string `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id string `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Payment_sequence     int64  `json:"payment_sequence" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Event_id             string `json:"event_id" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":300}"`
	Event_type           string `json:"event_type" openapi:"{\"type\":\"string\",\"enum\":[\"payment\",\"reversal\"]}"`
	Original_event_id    string `json:"original_event_id,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":300}"`
	Amount               string `json:"amount" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,15})(\\\\.[0-9]{1,2})?$\"}"`
	Currency             string `json:"currency" openapi:"{\"type\":\"string\",\"pattern\":\"^[A-Z]{3}$\"}"`
	Occurred_at          string `json:"occurred_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractPaymentReceipt struct {
	Receipt_id               string `json:"receipt_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_id              string `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id     string `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Payment_sequence         int64  `json:"payment_sequence" openapi:"{\"type\":\"integer\"}"`
	Source_id                string `json:"source_id" openapi:"{\"type\":\"string\"}"`
	Source_key_id            string `json:"source_key_id" openapi:"{\"type\":\"string\"}"`
	Source_event_id          string `json:"source_event_id" openapi:"{\"type\":\"string\"}"`
	Event_type               string `json:"event_type" openapi:"{\"type\":\"string\",\"enum\":[\"payment\",\"reversal\"]}"`
	Original_source_event_id string `json:"original_source_event_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Amount                   string `json:"amount" openapi:"{\"type\":\"string\"}"`
	Currency                 string `json:"currency" openapi:"{\"type\":\"string\"}"`
	Occurred_at              string `json:"occurred_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Recorded_at              string `json:"recorded_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Payload_digest           string `json:"payload_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
}
type ContractPaymentSummary struct {
	Contract_id          string                   `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id string                   `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Payment_sequence     int64                    `json:"payment_sequence" openapi:"{\"type\":\"integer\"}"`
	Planned_amount       string                   `json:"planned_amount,omitempty" openapi:"{\"type\":\"string\"}"`
	Currency             string                   `json:"currency,omitempty" openapi:"{\"type\":\"string\"}"`
	Net_paid             string                   `json:"net_paid" openapi:"{\"type\":\"string\"}"`
	State                string                   `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"unpaid\",\"partial\",\"paid\",\"overpaid\",\"unknown\"]}"`
	Receipts             []ContractPaymentReceipt `json:"receipts" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractPaymentReceipt\"}"`
}
type ContractAssetHandoverRequest struct {
	Expected_row_version int64         `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Contract_revision_id string        `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Asset_plan_id        string        `json:"asset_plan_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Delivery_id          string        `json:"delivery_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Acceptance_id        string        `json:"acceptance_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Scope_start          string        `json:"scope_start" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,11})(\\\\.[0-9]{1,6})?$\"}"`
	Scope_end            string        `json:"scope_end" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,11})(\\\\.[0-9]{1,6})?$\"}"`
	Handed_over_at       string        `json:"handed_over_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Location             string        `json:"location" openapi:"{\"type\":\"string\",\"maxLength\":1000}"`
	Transfer_note        string        `json:"transfer_note" openapi:"{\"type\":\"string\",\"maxLength\":2000}"`
	Evidence             []MaterialRef `json:"evidence,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/MaterialRef\"}"`
}
type ContractAssetReceiptRequest struct {
	Contract_id       string `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Handover_id       string `json:"handover_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Event_id          string `json:"event_id" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":300}"`
	Event_type        string `json:"event_type" openapi:"{\"type\":\"string\",\"enum\":[\"receipt\",\"reversal\"]}"`
	Original_event_id string `json:"original_event_id,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":300}"`
	Asset_code        string `json:"asset_code" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Quantity          string `json:"quantity" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]{0,11})(\\\\.[0-9]{1,6})?$\"}"`
	Unit              string `json:"unit" openapi:"{\"type\":\"string\",\"maxLength\":100}"`
	Occurred_at       string `json:"occurred_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractAssetReceipt struct {
	Receipt_id               string `json:"receipt_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_id              string `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Handover_id              string `json:"handover_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Source_id                string `json:"source_id" openapi:"{\"type\":\"string\"}"`
	Source_key_id            string `json:"source_key_id" openapi:"{\"type\":\"string\"}"`
	Source_event_id          string `json:"source_event_id" openapi:"{\"type\":\"string\"}"`
	Event_type               string `json:"event_type" openapi:"{\"type\":\"string\",\"enum\":[\"receipt\",\"reversal\"]}"`
	Original_source_event_id string `json:"original_source_event_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Asset_code               string `json:"asset_code" openapi:"{\"type\":\"string\"}"`
	Quantity                 string `json:"quantity" openapi:"{\"type\":\"string\"}"`
	Unit                     string `json:"unit" openapi:"{\"type\":\"string\"}"`
	Occurred_at              string `json:"occurred_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Recorded_at              string `json:"recorded_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Payload_digest           string `json:"payload_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
}
type ContractAssetHandover struct {
	Handover_id                    string                    `json:"handover_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_id                    string                    `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id           string                    `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Asset_plan_id                  string                    `json:"asset_plan_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Delivery_id                    string                    `json:"delivery_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Item_id                        string                    `json:"item_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Acceptance_id                  string                    `json:"acceptance_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Scope_start                    string                    `json:"scope_start" openapi:"{\"type\":\"string\"}"`
	Scope_end                      string                    `json:"scope_end" openapi:"{\"type\":\"string\"}"`
	Quantity                       string                    `json:"quantity" openapi:"{\"type\":\"string\"}"`
	Unit                           string                    `json:"unit" openapi:"{\"type\":\"string\"}"`
	Handed_over_at                 string                    `json:"handed_over_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Location                       string                    `json:"location" openapi:"{\"type\":\"string\"}"`
	Transfer_note                  string                    `json:"transfer_note" openapi:"{\"type\":\"string\"}"`
	Evidence                       []ContractVersionMaterial `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractVersionMaterial\"}"`
	Evidence_redacted              bool                      `json:"evidence_redacted,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Department_code_claim          string                    `json:"department_code_claim,omitempty" openapi:"{\"type\":\"string\"}"`
	Department_name_claim          string                    `json:"department_name_claim,omitempty" openapi:"{\"type\":\"string\"}"`
	Department_verification        string                    `json:"department_verification" openapi:"{\"type\":\"string\",\"enum\":[\"unknown\",\"verified\"]}"`
	Custodian_user_id              string                    `json:"custodian_user_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Custodian_display_name         string                    `json:"custodian_display_name,omitempty" openapi:"{\"type\":\"string\"}"`
	Custodian_org_id               string                    `json:"custodian_org_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Custodian_directory_source     string                    `json:"custodian_directory_source,omitempty" openapi:"{\"type\":\"string\"}"`
	Custodian_directory_revision   string                    `json:"custodian_directory_revision,omitempty" openapi:"{\"type\":\"string\"}"`
	Custodian_directory_checked_at string                    `json:"custodian_directory_checked_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Custodian_verification         string                    `json:"custodian_verification" openapi:"{\"type\":\"string\",\"enum\":[\"unknown\",\"verified\"]}"`
	Assignment_verification        string                    `json:"assignment_verification" openapi:"{\"type\":\"string\",\"enum\":[\"unknown\",\"verified\"]}"`
	Booking_status                 string                    `json:"booking_status" openapi:"{\"type\":\"string\",\"enum\":[\"pending_booking\",\"partial_booking\",\"booked\",\"unknown\"]}"`
	Recorded_by                    string                    `json:"recorded_by" openapi:"{\"type\":\"string\"}"`
	Recorded_at                    string                    `json:"recorded_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Contract_row_version           int64                     `json:"contract_row_version" openapi:"{\"type\":\"integer\"}"`
	Receipts                       []ContractAssetReceipt    `json:"receipts" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractAssetReceipt\"}"`
}
type ContractAssetPlanSummary struct {
	Contract_revision_id    string                  `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Asset_plan_id           string                  `json:"asset_plan_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Item_id                 string                  `json:"item_id,omitempty" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Name                    string                  `json:"name,omitempty" openapi:"{\"type\":\"string\"}"`
	Planned_quantity        string                  `json:"planned_quantity,omitempty" openapi:"{\"type\":\"string\"}"`
	Unit                    string                  `json:"unit,omitempty" openapi:"{\"type\":\"string\"}"`
	Department_code         string                  `json:"department_code,omitempty" openapi:"{\"type\":\"string\"}"`
	Department_name         string                  `json:"department_name,omitempty" openapi:"{\"type\":\"string\"}"`
	Department_verification string                  `json:"department_verification" openapi:"{\"type\":\"string\",\"enum\":[\"unknown\",\"verified\"]}"`
	Custodian_user_id       string                  `json:"custodian_user_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Fixed_asset_status      string                  `json:"fixed_asset_status" openapi:"{\"type\":\"string\",\"enum\":[\"unknown\",\"not_applicable\",\"true\"]}"`
	Plan_status             string                  `json:"plan_status" openapi:"{\"type\":\"string\",\"enum\":[\"unknown\",\"known\"]}"`
	Handovers               []ContractAssetHandover `json:"handovers" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractAssetHandover\"}"`
}
type ContractAssetList struct {
	Contract_id        string                     `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Row_version        int64                      `json:"row_version" openapi:"{\"type\":\"integer\"}"`
	Fixed_asset_status string                     `json:"fixed_asset_status" openapi:"{\"type\":\"string\",\"enum\":[\"unknown\",\"not_applicable\",\"true\"]}"`
	Items              []ContractAssetPlanSummary `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractAssetPlanSummary\"}"`
}
type ContractVersionMaterial struct {
	Material_id         string `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Material_version_id string `json:"material_version_id" openapi:"{\"type\":\"string\"}"`
	Material_version    int64  `json:"material_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Filename            string `json:"filename" openapi:"{\"type\":\"string\"}"`
	Digest              string `json:"digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Purpose             string `json:"purpose" openapi:"{\"type\":\"string\",\"enum\":[\"draft\",\"signed_contract\",\"supporting\",\"acceptance\",\"payment\",\"other\"]}"`
	Case_id             string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Case_org_id         string `json:"case_org_id" openapi:"{\"type\":\"string\"}"`
}
type ContractVersionDetail struct {
	Version   ContractVersion           `json:"version" openapi:"{\"$ref\":\"#/components/schemas/ContractVersion\"}"`
	Draft     DraftContent              `json:"draft,omitempty" openapi:"{\"$ref\":\"#/components/schemas/DraftContent\"}"`
	Materials []ContractVersionMaterial `json:"materials" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractVersionMaterial\"}"`
}
type ContractVersionFieldChange struct {
	Path           string `json:"path" openapi:"{\"type\":\"string\"}"`
	Category       string `json:"category" openapi:"{\"type\":\"string\",\"enum\":[\"amount\",\"payment\",\"subject_matter\",\"material\",\"party\",\"term\",\"other\"]}"`
	Classification string `json:"classification" openapi:"{\"type\":\"string\",\"enum\":[\"internal\",\"confidential\",\"restricted\"]}"`
	Change         string `json:"change" openapi:"{\"type\":\"string\",\"enum\":[\"added\",\"removed\",\"changed\"]}"`
	Before         any    `json:"before,omitempty"`
	After          any    `json:"after,omitempty"`
}
type ContractVersionRestrictedChange struct {
	Path    string `json:"path" openapi:"{\"type\":\"string\"}"`
	Changed any    `json:"changed" openapi:"{\"type\":[\"boolean\",\"null\"]}"`
	Reason  string `json:"reason,omitempty" openapi:"{\"type\":\"string\"}"`
}
type ContractVersionDiffSummary struct {
	Changed_fields     int64 `json:"changed_fields" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Added_fields       int64 `json:"added_fields" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Removed_fields     int64 `json:"removed_fields" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Restricted_changed int64 `json:"restricted_changed" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Restricted_unknown int64 `json:"restricted_unknown" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type ContractVersionDiff struct {
	Contract_id          string                            `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	From_revision_id     any                               `json:"from_revision_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	To_revision_id       string                            `json:"to_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	From_revision_number any                               `json:"from_revision_number" openapi:"{\"type\":[\"integer\",\"null\"]}"`
	To_revision_number   int64                             `json:"to_revision_number" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Changed              bool                              `json:"changed" openapi:"{\"type\":\"boolean\"}"`
	Fields               []ContractVersionFieldChange      `json:"fields" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractVersionFieldChange\"}"`
	Restricted_fields    []ContractVersionRestrictedChange `json:"restricted_fields" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractVersionRestrictedChange\"}"`
	Amount_terms         []ContractVersionFieldChange      `json:"amount_terms" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractVersionFieldChange\"}"`
	Summary              ContractVersionDiffSummary        `json:"summary" openapi:"{\"$ref\":\"#/components/schemas/ContractVersionDiffSummary\"}"`
}
type ApprovalDecisionRequest struct {
	Decision                   string `json:"decision" openapi:"{\"type\":\"string\",\"enum\":[\"approve\",\"return\"]}"`
	Comment                    string `json:"comment,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":2000,\"description\":\"退回时必填，最多 2000 字符。\"}"`
	Expected_round_row_version int64  `json:"expected_round_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Credential                 string `json:"credential,omitempty" openapi:"{\"type\":\"string\",\"description\":\"启用审批内部凭证时必填；由 credentials 接口签发，绑定任务、冻结版本与行版本。\"}"`
}
type ApprovalCredentialRequest struct {
}
type ApprovalCredentialClaims struct {
	Task_id              string `json:"task_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_id          string `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Approval_round_id    string `json:"approval_round_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id string `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Revision_number      int64  `json:"revision_number" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Snapshot_digest      string `json:"snapshot_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Task_row_version     int64  `json:"task_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Contract_row_version int64  `json:"contract_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Amount_limit         string `json:"amount_limit" openapi:"{\"type\":\"string\"}"`
	Currency             string `json:"currency" openapi:"{\"type\":\"string\",\"pattern\":\"^[A-Z]{3}$\"}"`
	Role                 string `json:"role" openapi:"{\"type\":\"string\"}"`
	Org_id               string `json:"org_id" openapi:"{\"type\":\"string\"}"`
	Expires_at           string `json:"expires_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ApprovalCredentialResult struct {
	Credential string                   `json:"credential" openapi:"{\"type\":\"string\",\"description\":\"短时 HMAC 内部凭证；不是 CA 证书或电子签名。\"}"`
	Claims     ApprovalCredentialClaims `json:"claims" openapi:"{\"$ref\":\"#/components/schemas/ApprovalCredentialClaims\"}"`
}
type ApprovalTransferRequest struct {
	To_user_id                string `json:"to_user_id" openapi:"{\"type\":\"string\"}"`
	Reason                    string `json:"reason" openapi:"{\"type\":\"string\",\"maxLength\":1000}"`
	Expected_task_row_version int64  `json:"expected_task_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ApprovalTransferResult struct {
	Transfer_id    string `json:"transfer_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Task_id        string `json:"task_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	From_user_id   string `json:"from_user_id" openapi:"{\"type\":\"string\"}"`
	To_user_id     string `json:"to_user_id" openapi:"{\"type\":\"string\"}"`
	Actor_id       string `json:"actor_id" openapi:"{\"type\":\"string\"}"`
	Row_version    int64  `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Transferred_at string `json:"transferred_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ApprovalDecisionResult struct {
	Decision_id    string        `json:"decision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Decision       string        `json:"decision" openapi:"{\"type\":\"string\",\"enum\":[\"approve\",\"return\"]}"`
	Round          ApprovalRound `json:"round" openapi:"{\"$ref\":\"#/components/schemas/ApprovalRound\"}"`
	Task           ApprovalTask  `json:"task" openapi:"{\"$ref\":\"#/components/schemas/ApprovalTask\"}"`
	Next_task      any           `json:"next_task" openapi:"{\"oneOf\":[{\"$ref\":\"#/components/schemas/ApprovalTask\"},{\"type\":\"null\"}]}"`
	Contract_state string        `json:"contract_state" openapi:"{\"type\":\"string\"}"`
	Workflow_state string        `json:"workflow_state" openapi:"{\"type\":\"string\",\"enum\":[\"in_review\",\"approved\",\"returned\"]}"`
	Row_version    int64         `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ApprovalSubmissionRequest struct {
	Expected_row_version int64  `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Template_id          string `json:"template_id,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":64,\"description\":\"可选的当前生效模板；省略时使用租户默认模板。\"}"`
}
type ApprovalRound struct {
	Approval_round_id         string `json:"approval_round_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Contract_revision_id      string `json:"contract_revision_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Round_number              int64  `json:"round_number" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	State                     string `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"in_review\",\"returned\",\"withdrawn\",\"approved\",\"terminated\"]}"`
	Workflow_template_id      string `json:"workflow_template_id" openapi:"{\"type\":\"string\"}"`
	Workflow_template_version string `json:"workflow_template_version" openapi:"{\"type\":\"string\"}"`
	Routing_digest            string `json:"routing_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Row_version               int64  `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Started_at                string `json:"started_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Ended_at                  any    `json:"ended_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
}
type ApprovalTask struct {
	Approval_task_id  string   `json:"approval_task_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Node_code         string   `json:"node_code" openapi:"{\"type\":\"string\"}"`
	Node_order        int64    `json:"node_order" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Title             string   `json:"title" openapi:"{\"type\":\"string\"}"`
	Role_code         string   `json:"role_code" openapi:"{\"type\":\"string\"}"`
	Final             bool     `json:"final" openapi:"{\"type\":\"boolean\"}"`
	State             string   `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"pending\",\"active\",\"approved\",\"returned\",\"cancelled\"]}"`
	Assignee_user_ids []string `json:"assignee_user_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type ApprovalSubmissionResult struct {
	Round            ApprovalRound  `json:"round" openapi:"{\"$ref\":\"#/components/schemas/ApprovalRound\"}"`
	Tasks            []ApprovalTask `json:"tasks" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ApprovalTask\"}"`
	Contract_state   string         `json:"contract_state" openapi:"{\"type\":\"string\"}"`
	Workflow_state   string         `json:"workflow_state" openapi:"{\"type\":\"string\"}"`
	Row_version      int64          `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Allowed_commands []string       `json:"allowed_commands" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type WorkflowTemplateNodeInput struct {
	Node_code string `json:"node_code" openapi:"{\"type\":\"string\"}"`
	Title     string `json:"title" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":120}"`
	Role_code string `json:"role_code" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-z][a-z0-9_]{2,63}$\"}"`
	Final     bool   `json:"final" openapi:"{\"type\":\"boolean\"}"`
}
type WorkflowTemplateNode struct {
	Node_code  string `json:"node_code" openapi:"{\"type\":\"string\"}"`
	Node_order int64  `json:"node_order" openapi:"{\"type\":\"integer\",\"minimum\":1,\"maximum\":7}"`
	Title      string `json:"title" openapi:"{\"type\":\"string\"}"`
	Role_code  string `json:"role_code" openapi:"{\"type\":\"string\"}"`
	Final      bool   `json:"final" openapi:"{\"type\":\"boolean\"}"`
}
type WorkflowTemplatePublishRequest struct {
	Template_id                   string                      `json:"template_id" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-z][a-z0-9._-]{2,63}$\"}"`
	Template_version              string                      `json:"template_version" openapi:"{\"type\":\"string\",\"pattern\":\"^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$\"}"`
	Name                          string                      `json:"name" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":120}"`
	Start_action                  string                      `json:"start_action" openapi:"{\"type\":\"string\",\"const\":\"submit_contract\"}"`
	Start_action_is_approval_node bool                        `json:"start_action_is_approval_node" openapi:"{\"type\":\"boolean\",\"const\":false}"`
	Execution                     string                      `json:"execution" openapi:"{\"type\":\"string\",\"const\":\"sequential\"}"`
	Nodes                         []WorkflowTemplateNodeInput `json:"nodes" openapi:"{\"type\":\"array\",\"minItems\":7,\"maxItems\":7}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkflowTemplateNodeInput\"}"`
}
type WorkflowTemplate struct {
	Template_id                   string                 `json:"template_id" openapi:"{\"type\":\"string\"}"`
	Template_version              string                 `json:"template_version" openapi:"{\"type\":\"string\"}"`
	Name                          string                 `json:"name" openapi:"{\"type\":\"string\"}"`
	Start_action                  string                 `json:"start_action" openapi:"{\"type\":\"string\"}"`
	Start_action_is_approval_node bool                   `json:"start_action_is_approval_node" openapi:"{\"type\":\"boolean\"}"`
	Execution                     string                 `json:"execution" openapi:"{\"type\":\"string\"}"`
	Nodes                         []WorkflowTemplateNode `json:"nodes" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkflowTemplateNode\"}"`
	Template_digest               string                 `json:"template_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Published_by                  string                 `json:"published_by" openapi:"{\"type\":\"string\"}"`
	Published_at                  string                 `json:"published_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type WorkflowRoleBindingInput struct {
	Org_scope string   `json:"org_scope" openapi:"{\"type\":\"string\",\"maxLength\":100,\"description\":\"空字符串表示租户范围；否则必须等于合同所属组织。\"}"`
	User_ids  []string `json:"user_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type WorkflowRoleReplaceRequest struct {
	Bindings []WorkflowRoleBindingInput `json:"bindings" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkflowRoleBindingInput\"}"`
}
type WorkflowRoleBinding struct {
	Org_scope string   `json:"org_scope" openapi:"{\"type\":\"string\"}"`
	User_ids  []string `json:"user_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type WorkflowRole struct {
	Role_code   string                `json:"role_code" openapi:"{\"type\":\"string\"}"`
	Row_version int64                 `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Bindings    []WorkflowRoleBinding `json:"bindings" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkflowRoleBinding\"}"`
	Updated_by  string                `json:"updated_by" openapi:"{\"type\":\"string\"}"`
	Updated_at  string                `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractDraftSummary struct {
	Contract_id    string `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Authority_mode string `json:"authority_mode" openapi:"{\"type\":\"string\",\"enum\":[\"native\",\"external\"]}"`
	Name           string `json:"name" openapi:"{\"type\":\"string\"}"`
	Number         any    `json:"number,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Amount         any    `json:"amount,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Currency       any    `json:"currency,omitempty" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Contract_state string `json:"contract_state" openapi:"{\"type\":\"string\"}"`
	Workflow_state string `json:"workflow_state" openapi:"{\"type\":\"string\"}"`
	Row_version    int64  `json:"row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Created_at     string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Updated_at     string `json:"updated_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractDraftList struct {
	Items []ContractDraftSummary `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractDraftSummary\"}"`
}
type ContractDraftCreateRequest struct {
	Expected_row_version int64        `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"const\":0,\"description\":\"新建必须为 0；非 0 返回 422。\"}"`
	Authority_mode       string       `json:"authority_mode" openapi:"{\"type\":\"string\",\"const\":\"native\"}"`
	Draft                DraftContent `json:"draft" openapi:"{\"$ref\":\"#/components/schemas/DraftContent\"}"`
}
type ContractDraftReplaceRequest struct {
	Expected_row_version int64        `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Replacement_draft    DraftContent `json:"replacement_draft" openapi:"{\"$ref\":\"#/components/schemas/DraftContent\"}"`
}
type ContractLegacyImportRequest struct {
	Expected_row_version          int64         `json:"expected_row_version" openapi:"{\"type\":\"integer\",\"const\":0}"`
	Authority_mode                string        `json:"authority_mode" openapi:"{\"type\":\"string\",\"const\":\"native\"}"`
	Source_ref                    SourceRef     `json:"source_ref" openapi:"{\"$ref\":\"#/components/schemas/SourceRef\"}"`
	Source_evidence               MaterialRef   `json:"source_evidence" openapi:"{\"$ref\":\"#/components/schemas/MaterialRef\"}"`
	Snapshot                      DraftContent  `json:"snapshot" openapi:"{\"$ref\":\"#/components/schemas/DraftContent\"}"`
	Approval_history_status       string        `json:"approval_history_status" openapi:"{\"type\":\"string\",\"enum\":[\"unknown\",\"evidence_attached\"]}"`
	Historical_approval_materials []MaterialRef `json:"historical_approval_materials" openapi:"{\"type\":\"array\",\"maxItems\":100}" openapi_items:"{\"$ref\":\"#/components/schemas/MaterialRef\"}"`
	Reason                        string        `json:"reason" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":2000}"`
}
type PartyReferenceSnapshot struct {
	Binding_id           string `json:"binding_id" openapi:"{\"type\":\"string\"}"`
	Binding_version      int64  `json:"binding_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Case_role            string `json:"case_role" openapi:"{\"type\":\"string\",\"enum\":[\"customer\",\"supplier\",\"contractor\",\"service_provider\",\"other\"]}"`
	Binding_state        string `json:"binding_state" openapi:"{\"type\":\"string\",\"enum\":[\"active\",\"inactive\",\"pending_confirmation\"]}"`
	Subject_id           string `json:"subject_id" openapi:"{\"type\":\"string\"}"`
	Subject_version      int64  `json:"subject_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Subject_payload_hash string `json:"subject_payload_hash" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Verification_state   string `json:"verification_state" openapi:"{\"type\":\"string\"}"`
	Display_name         string `json:"display_name" openapi:"{\"type\":\"string\",\"maxLength\":200}"`
	Captured_at          string `json:"captured_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ProcurementSourceSnapshot struct {
	System              string `json:"system" openapi:"{\"type\":\"string\"}"`
	Result_id           string `json:"result_id" openapi:"{\"type\":\"string\"}"`
	Package_id          string `json:"package_id" openapi:"{\"type\":\"string\"}"`
	Snapshot_ref        string `json:"snapshot_ref" openapi:"{\"type\":\"string\"}"`
	Material_id         string `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Material_version_id string `json:"material_version_id" openapi:"{\"type\":\"string\"}"`
	Material_version    int64  `json:"material_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Material_role       string `json:"material_role" openapi:"{\"type\":\"string\",\"enum\":[\"procurement\",\"award\"]}"`
	Sha256              string `json:"sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Captured_at         string `json:"captured_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ContractSubjectReference struct {
	Party_id             string `json:"party_id" openapi:"{\"type\":\"string\"}"`
	Role                 string `json:"role" openapi:"{\"type\":\"string\",\"enum\":[\"buyer\",\"supplier\",\"guarantor\",\"consortium_member\",\"other\"]}"`
	Name_snapshot        string `json:"name_snapshot" openapi:"{\"type\":\"string\"}"`
	Binding_id           string `json:"binding_id" openapi:"{\"type\":\"string\"}"`
	Binding_version      int64  `json:"binding_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Case_role            string `json:"case_role" openapi:"{\"type\":\"string\",\"enum\":[\"customer\",\"supplier\",\"contractor\",\"service_provider\",\"other\"]}"`
	Binding_state        string `json:"binding_state" openapi:"{\"type\":\"string\",\"enum\":[\"active\",\"inactive\",\"pending_confirmation\"]}"`
	Subject_version      int64  `json:"subject_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Subject_payload_hash string `json:"subject_payload_hash" openapi:"{\"type\":\"string\"}"`
	Verification_state   string `json:"verification_state" openapi:"{\"type\":\"string\"}"`
	Frozen               bool   `json:"frozen" openapi:"{\"type\":\"boolean\"}"`
}
type ContractMaterialReference struct {
	Material_id         string `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Material_version_id string `json:"material_version_id" openapi:"{\"type\":\"string\"}"`
	Sha256              string `json:"sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Purpose             string `json:"purpose" openapi:"{\"type\":\"string\"}"`
	Material_version    int64  `json:"material_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Material_role       string `json:"material_role" openapi:"{\"type\":\"string\",\"enum\":[\"procurement\",\"award\",\"draft_contract\",\"attachment\"]}"`
	Filename            string `json:"filename" openapi:"{\"type\":\"string\"}"`
	Media_type          string `json:"media_type" openapi:"{\"type\":\"string\"}"`
	Frozen              bool   `json:"frozen" openapi:"{\"type\":\"boolean\"}"`
	Original_path       string `json:"original_path" openapi:"{\"type\":\"string\"}"`
}
type ContractReferenceCheck struct {
	Contract_id          string                      `json:"contract_id" openapi:"{\"type\":\"string\",\"format\":\"uuid\"}"`
	Source               string                      `json:"source" openapi:"{\"type\":\"string\",\"enum\":[\"draft\",\"revision\"]}"`
	Contract_revision_id any                         `json:"contract_revision_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Source_case_id       string                      `json:"source_case_id" openapi:"{\"type\":\"string\"}"`
	Checked_at           string                      `json:"checked_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Subjects             []ContractSubjectReference  `json:"subjects" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractSubjectReference\"}"`
	Materials            []ContractMaterialReference `json:"materials" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ContractMaterialReference\"}"`
	Procurement_result   ProcurementSourceSnapshot   `json:"procurement_result,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ProcurementSourceSnapshot\"}"`
	Warnings             []string                    `json:"warnings" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type DirectoryStatusSnapshot struct {
	Revision                       string `json:"revision" openapi:"{\"type\":\"string\"}"`
	Last_good_revision             string `json:"last_good_revision" openapi:"{\"type\":\"string\"}"`
	Checked_at                     string `json:"checked_at" openapi:"{\"type\":\"string\"}"`
	Loaded_at                      string `json:"loaded_at" openapi:"{\"type\":\"string\"}"`
	Refresh_error_code             string `json:"refresh_error_code" openapi:"{\"type\":\"string\"}"`
	Mode                           string `json:"mode" openapi:"{\"type\":\"string\",\"enum\":[\"local_demo\",\"enterprise\"]}"`
	State                          string `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"local_demo\",\"ready\",\"unavailable\"]}"`
	Local_verification_interval_ms int64  `json:"local_verification_interval_ms" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ModelBudgetCapacity struct {
	Max_in_flight     int64 `json:"max_in_flight" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Calls_per_minute  int64 `json:"calls_per_minute" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Tokens_per_minute int64 `json:"tokens_per_minute" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ModelBudgetGatewayStatus struct {
	Schema_version any                 `json:"schema_version" openapi:"{\"const\":\"model-budget/v1\"}"`
	Revision       string              `json:"revision" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Mode           string              `json:"mode" openapi:"{\"type\":\"string\"}"`
	Window_seconds any                 `json:"window_seconds" openapi:"{\"const\":60}"`
	Tenant         ModelBudgetCapacity `json:"tenant" openapi:"{\"$ref\":\"#/components/schemas/ModelBudgetCapacity\"}"`
	Global         ModelBudgetCapacity `json:"global" openapi:"{\"$ref\":\"#/components/schemas/ModelBudgetCapacity\"}"`
}
type TenantModelUsage struct {
	Active_calls        int64  `json:"active_calls" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Window_calls        int64  `json:"window_calls" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Charged_tokens      int64  `json:"charged_tokens" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Reserved_tokens     int64  `json:"reserved_tokens" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Unknown_usage_calls int64  `json:"unknown_usage_calls" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Observed_at         string `json:"observed_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Window_start        string `json:"window_start" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Window_seconds      any    `json:"window_seconds" openapi:"{\"const\":60}"`
}
type TenantRuntimeControl struct {
	Status     string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"active\",\"suspended\",\"closed\"]}"`
	Live_epoch string `json:"live_epoch" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]*)$\",\"description\":\"0 仅用于非生产 local demo；受管租户从 1 开始。\"}"`
}
type TenantExecutionQueue struct {
	Policy_hash            string `json:"policy_hash" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Policy_version         string `json:"policy_version" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":80}"`
	Tenant_weight          int64  `json:"tenant_weight" openapi:"{\"type\":\"integer\",\"minimum\":1,\"maximum\":100}"`
	Tenant_in_flight_limit int64  `json:"tenant_in_flight_limit" openapi:"{\"type\":\"integer\",\"minimum\":1,\"maximum\":1024}"`
	Tenant_pending_limit   int64  `json:"tenant_pending_limit" openapi:"{\"type\":\"integer\",\"minimum\":1,\"maximum\":100000}"`
	Tenant_live            bool   `json:"tenant_live" openapi:"{\"type\":\"boolean\",\"description\":\"仅当 tenant_control.status 为 active 时为 true；与目录可用状态独立。\"}"`
}
type TenantCostWindow struct {
	Period_key         string `json:"period_key" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9]{4}-[0-9]{2}(-[0-9]{2})?$\"}"`
	Limit_minor        any    `json:"limit_minor" openapi:"{\"type\":[\"string\",\"null\"],\"pattern\":\"^(0|[1-9][0-9]*)$\"}"`
	Reserved_minor     any    `json:"reserved_minor" openapi:"{\"type\":[\"string\",\"null\"],\"pattern\":\"^(0|[1-9][0-9]*)$\"}"`
	Settled_minor      any    `json:"settled_minor" openapi:"{\"type\":[\"string\",\"null\"],\"pattern\":\"^(0|[1-9][0-9]*)$\"}"`
	Unknown_hold_minor any    `json:"unknown_hold_minor" openapi:"{\"type\":[\"string\",\"null\"],\"pattern\":\"^(0|[1-9][0-9]*)$\"}"`
	Known              bool   `json:"known" openapi:"{\"type\":\"boolean\",\"description\":\"金额分项未知时对应minor字段为null；不得以0替代未知。\"}"`
}
type TenantCost struct {
	Currency         any              `json:"currency" openapi:"{\"const\":\"USD\"}"`
	Minor_unit_scale any              `json:"minor_unit_scale" openapi:"{\"const\":6}"`
	Timezone         any              `json:"timezone" openapi:"{\"const\":\"UTC\"}"`
	Day              TenantCostWindow `json:"day" openapi:"{\"$ref\":\"#/components/schemas/TenantCostWindow\"}"`
	Month            TenantCostWindow `json:"month" openapi:"{\"$ref\":\"#/components/schemas/TenantCostWindow\"}"`
}
type TenantConfigRef struct {
	Id      string `json:"id" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":160}"`
	Version string `json:"version" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":160}"`
	Digest  string `json:"digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
}
type TenantConfigCatalogEntry struct {
	Id      string `json:"id" openapi:"{\"type\":\"string\"}"`
	Version string `json:"version" openapi:"{\"type\":\"string\"}"`
	Digest  string `json:"digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Title   string `json:"title" openapi:"{\"type\":\"string\"}"`
	Kind    string `json:"kind" openapi:"{\"type\":\"string\",\"enum\":[\"public_template\",\"private_policy\"]}"`
}
type TenantModelCatalogEntry struct {
	Id             string `json:"id" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Revision       string `json:"revision" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]*)$\"}"`
	Provider_label string `json:"provider_label" openapi:"{\"type\":\"string\"}"`
	Chat_model     string `json:"chat_model" openapi:"{\"type\":\"string\"}"`
	Ocr_model      string `json:"ocr_model" openapi:"{\"type\":\"string\"}"`
}
type TenantCredentialRef struct {
	Id    any    `json:"id" openapi:"{\"const\":\"platform/llm-endpoint/active\"}"`
	Label string `json:"label" openapi:"{\"type\":\"string\"}"`
}
type TenantConfigSelection struct {
	Public_template_ref TenantConfigRef   `json:"public_template_ref" openapi:"{\"$ref\":\"#/components/schemas/TenantConfigRef\"}"`
	Private_policy_refs []TenantConfigRef `json:"private_policy_refs" openapi:"{\"type\":\"array\",\"maxItems\":100}" openapi_items:"{\"$ref\":\"#/components/schemas/TenantConfigRef\"}"`
	Model_catalog_ref   string            `json:"model_catalog_ref" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Credential_ref      any               `json:"credential_ref" openapi:"{\"type\":[\"string\",\"null\"],\"enum\":[\"platform/llm-endpoint/active\",null]}"`
	Egress_mode         any               `json:"egress_mode" openapi:"{\"const\":\"approved_only\"}"`
}
type AdoptedTenantConfig struct {
	Config_version      string                     `json:"config_version" openapi:"{\"type\":\"string\",\"pattern\":\"^[1-9][0-9]*$\"}"`
	Version_id          string                     `json:"version_id" openapi:"{\"type\":\"string\",\"pattern\":\"^tcv_[a-zA-Z0-9_-]{8,120}$\"}"`
	Digest              string                     `json:"digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Public_template     TenantConfigCatalogEntry   `json:"public_template" openapi:"{\"$ref\":\"#/components/schemas/TenantConfigCatalogEntry\"}"`
	Private_policy_refs []TenantConfigCatalogEntry `json:"private_policy_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/TenantConfigCatalogEntry\"}"`
	Model_catalog_ref   string                     `json:"model_catalog_ref" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Credential_ref      any                        `json:"credential_ref" openapi:"{\"type\":[\"string\",\"null\"],\"enum\":[\"platform/llm-endpoint/active\",null]}"`
	Egress_mode         any                        `json:"egress_mode" openapi:"{\"const\":\"approved_only\"}"`
	Adopted_at          string                     `json:"adopted_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Adopted_by          string                     `json:"adopted_by" openapi:"{\"type\":\"string\"}"`
}
type TenantConfigView struct {
	Schema_version          any                          `json:"schema_version" openapi:"{\"const\":\"tenant-config/v1\"}"`
	Tenant_id               string                       `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Revision                string                       `json:"revision" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]*)$\"}"`
	Current                 any                          `json:"current" openapi:"{\"oneOf\":[{\"$ref\":\"#/components/schemas/AdoptedTenantConfig\"},{\"type\":\"null\"}]}"`
	Public_template_catalog []TenantConfigCatalogEntry   `json:"public_template_catalog" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/TenantConfigCatalogEntry\"}"`
	Private_policy_catalog  []TenantConfigCatalogEntry   `json:"private_policy_catalog" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/TenantConfigCatalogEntry\"}"`
	Model_catalog           []TenantModelCatalogEntry    `json:"model_catalog" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/TenantModelCatalogEntry\"}"`
	Credential_refs         []TenantCredentialRef        `json:"credential_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/TenantCredentialRef\"}"`
	Live_control            TenantConfigViewLive_control `json:"live_control" openapi:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"enabled\",\"revision\",\"updated_at\"]}"`
}
type TenantConfigPreviewRequest struct {
	Expected_revision string                `json:"expected_revision" openapi:"{\"type\":\"string\",\"pattern\":\"^(0|[1-9][0-9]*)$\"}"`
	Selection         TenantConfigSelection `json:"selection" openapi:"{\"$ref\":\"#/components/schemas/TenantConfigSelection\"}"`
}
type TenantConfigDiff struct {
	Path   string `json:"path" openapi:"{\"type\":\"string\"}"`
	Change string `json:"change" openapi:"{\"type\":\"string\",\"enum\":[\"added\",\"removed\",\"changed\"]}"`
	Before any    `json:"before" openapi:"{\"type\":[\"string\",\"null\"]}"`
	After  any    `json:"after" openapi:"{\"type\":[\"string\",\"null\"]}"`
}
type TenantConfigValidationError struct {
	Path string `json:"path" openapi:"{\"type\":\"string\"}"`
	Code string `json:"code" openapi:"{\"type\":\"string\"}"`
}
type TenantConfigPreview struct {
	Valid  bool                          `json:"valid" openapi:"{\"type\":\"boolean\"}"`
	Diff   []TenantConfigDiff            `json:"diff" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/TenantConfigDiff\"}"`
	Errors []TenantConfigValidationError `json:"errors" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/TenantConfigValidationError\"}"`
}
type TenantConfigAdoptionRequest TenantConfigPreviewRequest
type TenantConfigAdoptionResult struct {
	Revision       string              `json:"revision" openapi:"{\"type\":\"string\",\"pattern\":\"^[1-9][0-9]*$\"}"`
	Adopted_config AdoptedTenantConfig `json:"adopted_config" openapi:"{\"$ref\":\"#/components/schemas/AdoptedTenantConfig\"}"`
}
type TenantLiveControlRequest struct {
	Expected_revision string `json:"expected_revision" openapi:"{\"type\":\"string\",\"pattern\":\"^[1-9][0-9]*$\"}"`
	Enabled           bool   `json:"enabled" openapi:"{\"type\":\"boolean\"}"`
}
type TenantLiveControlResult struct {
	Revision string `json:"revision" openapi:"{\"type\":\"string\",\"pattern\":\"^[1-9][0-9]*$\"}"`
	Enabled  bool   `json:"enabled" openapi:"{\"type\":\"boolean\"}"`
}
type TenantOperations struct {
	Schema_version         any                     `json:"schema_version" openapi:"{\"const\":\"tenant-operations/v3\"}"`
	Tenant_id              string                  `json:"tenant_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Directory              DirectoryStatusSnapshot `json:"directory" openapi:"{\"$ref\":\"#/components/schemas/DirectoryStatusSnapshot\"}"`
	Login_rate_limit_scope any                     `json:"login_rate_limit_scope" openapi:"{\"const\":\"process\"}"`
	Budget_state           string                  `json:"budget_state" openapi:"{\"type\":\"string\",\"enum\":[\"ready\",\"unconfigured\",\"unavailable\"]}"`
	Budget                 any                     `json:"budget" openapi:"{\"oneOf\":[{\"$ref\":\"#/components/schemas/ModelBudgetGatewayStatus\"},{\"type\":\"null\"}]}"`
	Usage                  TenantModelUsage        `json:"usage" openapi:"{\"$ref\":\"#/components/schemas/TenantModelUsage\"}"`
	Tenant_control         TenantRuntimeControl    `json:"tenant_control" openapi:"{\"$ref\":\"#/components/schemas/TenantRuntimeControl\"}"`
	Execution_queue        TenantExecutionQueue    `json:"execution_queue" openapi:"{\"$ref\":\"#/components/schemas/TenantExecutionQueue\"}"`
	Cost                   TenantCost              `json:"cost" openapi:"{\"$ref\":\"#/components/schemas/TenantCost\"}"`
}
type IntakeRequestIdentity struct {
	Title         string `json:"title" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":120}"`
	Contract_type string `json:"contract_type" openapi:"{\"type\":\"string\"}"`
	Business_line string `json:"business_line" openapi:"{\"type\":\"string\"}"`
	Scenario      string `json:"scenario" openapi:"{\"type\":\"string\"}"`
	File_name     string `json:"file_name" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	File_size     int64  `json:"file_size" openapi:"{\"type\":\"integer\",\"minimum\":0,\"maximum\":33554432}"`
	Media_type    string `json:"media_type" openapi:"{\"type\":\"string\",\"description\":\"原请求规范化后的媒体类型，续办时保持原值。\"}"`
	File_sha256   string `json:"file_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
}
type PendingIntake struct {
	Operation_id string `json:"operation_id" openapi:"{\"type\":\"string\"}"`
	Case_id      string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Title        string `json:"title" openapi:"{\"type\":\"string\"}"`
	State        any    `json:"state" openapi:"{\"const\":\"upload_pending\"}"`
	File_name    string `json:"file_name" openapi:"{\"type\":\"string\",\"description\":\"历史缺失请求标识时为空字符串。\"}"`
	File_size    any    `json:"file_size" openapi:"{\"type\":[\"integer\",\"null\"],\"minimum\":0,\"maximum\":33554432}"`
	Created_at   string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Updated_at   string `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Error_code   string `json:"error_code" openapi:"{\"type\":\"string\"}"`
	Can_resume   bool   `json:"can_resume" openapi:"{\"type\":\"boolean\"}"`
	Can_archive  bool   `json:"can_archive" openapi:"{\"type\":\"boolean\"}"`
	Reason_code  string `json:"reason_code" openapi:"{\"type\":\"string\",\"enum\":[\"\",\"legacy_request_unavailable\",\"request_identity_invalid\",\"forbidden\",\"read_only\",\"case_not_writable\",\"completed\",\"archived\"]}"`
}
type PendingIntakesPage struct {
	Items       []PendingIntake `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/PendingIntake\"}"`
	Next_cursor any             `json:"next_cursor" openapi:"{\"type\":[\"string\",\"null\"]}"`
}
type IntakeContinuation struct {
	Operation_id    string `json:"operation_id" openapi:"{\"type\":\"string\"}"`
	Case_id         string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	State           string `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"upload_pending\",\"completed\"]}"`
	Request         any    `json:"request" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/IntakeRequestIdentity\"},{\"type\":\"null\"}]}"`
	Idempotency_key any    `json:"idempotency_key" openapi:"{\"type\":[\"string\",\"null\"],\"minLength\":1,\"maxLength\":200,\"description\":\"只在原操作人可以继续上传时返回。\"}"`
	Can_resume      bool   `json:"can_resume" openapi:"{\"type\":\"boolean\"}"`
	Can_archive     bool   `json:"can_archive" openapi:"{\"type\":\"boolean\"}"`
	Reason_code     string `json:"reason_code" openapi:"{\"type\":\"string\",\"enum\":[\"\",\"legacy_request_unavailable\",\"request_identity_invalid\",\"forbidden\",\"read_only\",\"case_not_writable\",\"completed\",\"archived\"]}"`
}
type IntakeArchiveResult struct {
	Operation_id string `json:"operation_id" openapi:"{\"type\":\"string\"}"`
	Case_id      string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Archived_at  string `json:"archived_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type IntakeOperation struct {
	Operation_id     string `json:"operation_id" openapi:"{\"type\":\"string\"}"`
	Case_id          string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	State            string `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"upload_pending\",\"completed\"]}"`
	Material_id      string `json:"material_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Material_version int64  `json:"material_version,omitempty" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Document_id      string `json:"document_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Retryable        bool   `json:"retryable" openapi:"{\"type\":\"boolean\"}"`
	Error_code       string `json:"error_code,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"document_upload_unavailable\"]}"`
}
type IntakeCreateRequest struct {
	Title         string `json:"title" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":120,\"description\":\"服务端去除首尾空白后按 Unicode 码点计数。\"}"`
	Contract_type string `json:"contract_type" openapi:"{\"type\":\"string\",\"enum\":[\"procurement_ict\",\"network_engineering\",\"service\",\"unspecified\"]}"`
	Business_line string `json:"business_line,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"ENT\",\"unspecified\"],\"description\":\"与 scenario 同时提供；两者均省略时记录为 unspecified。\"}"`
	Scenario      string `json:"scenario,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"telecom_dedicated_line\",\"cloud_services\",\"unspecified\"],\"description\":\"ENT 支持 telecom_dedicated_line 或 cloud_services；云服务使用 service 合同类型。\"}"`
	File          string `json:"file" openapi:"{\"type\":\"string\",\"format\":\"binary\",\"description\":\"一份初始主合同，最大 32 MiB；完整 multipart 请求上限 33 MiB。\"}"`
	Intent        any    `json:"intent,omitempty" openapi:"{\"const\":\"add\",\"description\":\"服务端固定为新增，不接受其他意图。\"}"`
	Role          any    `json:"role,omitempty" openapi:"{\"const\":\"draft_contract\",\"description\":\"服务端固定材料角色。\"}"`
	Purpose       any    `json:"purpose,omitempty" openapi:"{\"const\":\"primary_contract\",\"description\":\"服务端固定主合同用途。\"}"`
}
type ManualFindingEvidence struct {
	Document_id        string `json:"document_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Parsed_document_id string `json:"parsed_document_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Normalized_digest  string `json:"normalized_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Start              int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End                int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Quote              string `json:"quote" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":4096}"`
}
type ManualFindingEvidenceInput struct {
	Document_id        string `json:"document_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Parsed_document_id string `json:"parsed_document_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Normalized_digest  string `json:"normalized_digest,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Start              int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End                int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Quote              string `json:"quote" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":4096}"`
}
type ManualFindingRequest struct {
	Claim           string                     `json:"claim" openapi:"{\"type\":\"string\",\"minLength\":2,\"maxLength\":1000}"`
	Severity        string                     `json:"severity" openapi:"{\"type\":\"string\",\"enum\":[\"high\",\"medium\",\"low\",\"info\"]}"`
	Evidence        ManualFindingEvidenceInput `json:"evidence" openapi:"{\"$ref\":\"#/components/schemas/ManualFindingEvidenceInput\"}"`
	Idempotency_key string                     `json:"idempotency_key,omitempty" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":80}"`
}
type ManualFinding struct {
	Id         string                `json:"id" openapi:"{\"type\":\"string\"}"`
	Case_id    string                `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Run_id     string                `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id string                `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Actor_id   string                `json:"actor_id" openapi:"{\"type\":\"string\"}"`
	Actor_name string                `json:"actor_name" openapi:"{\"type\":\"string\"}"`
	Claim      string                `json:"claim" openapi:"{\"type\":\"string\"}"`
	Severity   string                `json:"severity" openapi:"{\"type\":\"string\",\"enum\":[\"high\",\"medium\",\"low\",\"info\"]}"`
	Evidence   ManualFindingEvidence `json:"evidence" openapi:"{\"$ref\":\"#/components/schemas/ManualFindingEvidence\"}"`
	Created_at string                `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type RuleShadowSampleRef struct {
	Case_id    string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Run_id     string `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id string `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
}
type RuleShadowSampleOption struct {
	Case_id     string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Run_id      string `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id  string `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Finished_at string `json:"finished_at" openapi:"{\"type\":\"string\"}"`
	State       string `json:"state" openapi:"{\"type\":\"string\"}"`
}
type RuleShadowChange struct {
	Rule_id            string `json:"rule_id" openapi:"{\"type\":\"string\"}"`
	Kind               string `json:"kind" openapi:"{\"type\":\"string\",\"enum\":[\"changed\",\"removed\",\"unavailable\"]}"`
	Base_relation      string `json:"base_relation" openapi:"{\"type\":\"string\"}"`
	Candidate_relation string `json:"candidate_relation" openapi:"{\"type\":\"string\"}"`
	Base_reason        string `json:"base_reason" openapi:"{\"type\":\"string\"}"`
	Candidate_reason   string `json:"candidate_reason" openapi:"{\"type\":\"string\"}"`
	Detail             string `json:"detail" openapi:"{\"type\":\"string\"}"`
}
type RuleShadowSampleResult struct {
	Case_id                   string             `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Run_id                    string             `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id                string             `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Case_authorized           bool               `json:"case_authorized" openapi:"{\"type\":\"boolean\"}"`
	Status                    string             `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"completed\",\"partial\",\"failed\"]}"`
	Error_code                string             `json:"error_code" openapi:"{\"type\":\"string\"}"`
	Message                   string             `json:"message" openapi:"{\"type\":\"string\"}"`
	Input_sha256              string             `json:"input_sha256" openapi:"{\"type\":\"string\"}"`
	Output_sha256             string             `json:"output_sha256" openapi:"{\"type\":\"string\"}"`
	Knowledge_sha256          string             `json:"knowledge_sha256" openapi:"{\"type\":\"string\"}"`
	Rule_request_sha256       string             `json:"rule_request_sha256" openapi:"{\"type\":\"string\"}"`
	Extractor_ref             string             `json:"extractor_ref" openapi:"{\"type\":\"string\"}"`
	Base_pack_id              string             `json:"base_pack_id" openapi:"{\"type\":\"string\"}"`
	Base_pack_version         string             `json:"base_pack_version" openapi:"{\"type\":\"string\"}"`
	Base_pack_sha256          string             `json:"base_pack_sha256" openapi:"{\"type\":\"string\"}"`
	Base_pack_snapshot_sha256 string             `json:"base_pack_snapshot_sha256" openapi:"{\"type\":\"string\"}"`
	Changes                   []RuleShadowChange `json:"changes" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/RuleShadowChange\"}"`
}
type RuleShadowEnvelope struct {
	Report_sha256 string                   `json:"report_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Report        RuleShadowEnvelopeReport `json:"report" openapi:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"schema_version\",\"id\",\"created_at\",\"created_by\",\"candidate\",\"samples\",\"completed\",\"partial\",\"failed\"]}"`
}
type OperatorCollectionJob struct {
	Collection_id   string  `json:"collection_id" openapi:"{\"type\":\"string\"}"`
	Case_id         string  `json:"case_id" openapi:"{\"type\":\"string\"}"`
	State           string  `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"in_progress\",\"retryable\",\"completed\"]}"`
	Retry_after     *string `json:"retry_after,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\",\"nullable\":true}"`
	Snapshot_set_id *string `json:"snapshot_set_id,omitempty" openapi:"{\"type\":\"string\",\"nullable\":true}"`
	Snapshot_set    any     `json:"snapshot_set,omitempty" openapi:"{\"type\":\"object\",\"nullable\":true,\"description\":\"completed时返回的不可变外部快照集\"}"`
}
type OperatorTemporalProof any
type OperatorObservation struct {
	Schema_version         string                `json:"schema_version" openapi:"{\"type\":\"string\"}"`
	Snapshot_id            string                `json:"snapshot_id" openapi:"{\"type\":\"string\"}"`
	Tenant_id              string                `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Case_id                string                `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Connector_id           string                `json:"connector_id" openapi:"{\"type\":\"string\"}"`
	Source_system          string                `json:"source_system" openapi:"{\"type\":\"string\"}"`
	Object_binding_version string                `json:"object_binding_version" openapi:"{\"type\":\"string\"}"`
	External_object_id     string                `json:"external_object_id" openapi:"{\"type\":\"string\"}"`
	Record_id              string                `json:"record_id" openapi:"{\"type\":\"string\"}"`
	Record_version         *string               `json:"record_version" openapi:"{\"type\":\"string\",\"nullable\":true}"`
	Observed_at            string                `json:"observed_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Source_updated_at      *string               `json:"source_updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\",\"nullable\":true}"`
	Effective_from         string                `json:"effective_from" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Effective_until        string                `json:"effective_until" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Availability           string                `json:"availability" openapi:"{\"type\":\"string\",\"enum\":[\"available\",\"unavailable\",\"stale\",\"rejected\"]}"`
	Reason_code            string                `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Payload_ref            *string               `json:"payload_ref" openapi:"{\"type\":\"string\",\"nullable\":true}"`
	Fields                 []any                 `json:"fields" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"object\"}"`
	Data_class             string                `json:"data_class" openapi:"{\"type\":\"string\",\"enum\":[\"synthetic\"]}"`
	Temporal_proof         OperatorTemporalProof `json:"temporal_proof,omitempty" openapi:"{\"$ref\":\"#/components/schemas/OperatorTemporalProof\"}"`
}
type OperatorSnapshotSet struct {
	Case_id            string                `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Snapshot_set_id    string                `json:"snapshot_set_id" openapi:"{\"type\":\"string\"}"`
	Binding_version    string                `json:"binding_version" openapi:"{\"type\":\"string\"}"`
	Connector_id       string                `json:"connector_id" openapi:"{\"type\":\"string\"}"`
	External_object_id string                `json:"external_object_id" openapi:"{\"type\":\"string\"}"`
	Source_system      string                `json:"source_system" openapi:"{\"type\":\"string\"}"`
	Business_at        string                `json:"business_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Status             string                `json:"status" openapi:"{\"type\":\"string\"}"`
	Reason_code        string                `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Collected_by       string                `json:"collected_by" openapi:"{\"type\":\"string\"}"`
	Collected_at       string                `json:"collected_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Observations       []OperatorObservation `json:"observations" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/OperatorObservation\"}"`
}
type OperatorCollectionResponse struct {
	Job_id             string              `json:"job_id" openapi:"{\"type\":\"string\"}"`
	Collection_id      string              `json:"collection_id" openapi:"{\"type\":\"string\"}"`
	Snapshot_set_id    string              `json:"snapshot_set_id" openapi:"{\"type\":\"string\"}"`
	State              string              `json:"state" openapi:"{\"type\":\"string\"}"`
	Connector_id       string              `json:"connector_id" openapi:"{\"type\":\"string\"}"`
	External_object_id string              `json:"external_object_id" openapi:"{\"type\":\"string\"}"`
	Accepted_at        string              `json:"accepted_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Snapshot_set       OperatorSnapshotSet `json:"snapshot_set" openapi:"{\"$ref\":\"#/components/schemas/OperatorSnapshotSet\"}"`
}
type OperatorSnapshotList struct {
	Items []any                 `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"allOf\":[{\"$ref\":\"#/components/schemas/OperatorObservation\"},{\"type\":\"object\",\"required\":[\"snapshot_set_id\"],\"properties\":{\"snapshot_set_id\":{\"type\":\"string\"}}}]}"`
	Sets  []OperatorSnapshotSet `json:"sets" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/OperatorSnapshotSet\"}"`
}
type OperatorEvaluation struct {
	Evaluation_id          string                     `json:"evaluation_id" openapi:"{\"type\":\"string\"}"`
	Pack                   OperatorEvaluationPack     `json:"pack" openapi:"{\"type\":\"object\",\"required\":[\"id\",\"version\"]}"`
	Source_review_id       string                     `json:"source_review_id" openapi:"{\"type\":\"string\"}"`
	Source_attempt_id      string                     `json:"source_attempt_id" openapi:"{\"type\":\"string\"}"`
	Contract_version       string                     `json:"contract_version" openapi:"{\"type\":\"string\"}"`
	Rule_pack_version      string                     `json:"rule_pack_version" openapi:"{\"type\":\"string\"}"`
	Object_binding_version string                     `json:"object_binding_version" openapi:"{\"type\":\"string\"}"`
	Snapshot_set_id        string                     `json:"snapshot_set_id" openapi:"{\"type\":\"string\"}"`
	Business_at            string                     `json:"business_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Evidence_cutoff_at     string                     `json:"evidence_cutoff_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Machine_status         string                     `json:"machine_status" openapi:"{\"type\":\"string\"}"`
	Checks                 []any                      `json:"checks" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
	Contract_facts         []any                      `json:"contract_facts" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
	Scope                  OperatorEvaluationScope    `json:"scope,omitempty" openapi:"{\"$ref\":\"#/components/schemas/OperatorEvaluationScope\"}"`
	Coverage               OperatorEvaluationCoverage `json:"coverage,omitempty" openapi:"{\"$ref\":\"#/components/schemas/OperatorEvaluationCoverage\"}"`
	Objects                []OperatorEvaluationObject `json:"objects,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":100}" openapi_items:"{\"$ref\":\"#/components/schemas/OperatorEvaluationObject\"}"`
	Data_class             string                     `json:"data_class" openapi:"{\"type\":\"string\"}"`
	Created_at             string                     `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type OperatorEvaluationScope struct {
	Kind               string   `json:"kind" openapi:"{\"type\":\"string\",\"enum\":[\"whole_case\",\"explicit_subset\"]}"`
	Service_object_ids []string `json:"service_object_ids,omitempty" openapi:"{\"type\":\"array\",\"minItems\":1,\"maxItems\":100,\"uniqueItems\":true}" openapi_items:"{\"type\":\"string\"}"`
}
type OperatorEvaluationCoverage struct {
	Scope_kind         string `json:"scope_kind" openapi:"{\"type\":\"string\"}"`
	Candidate_count    int64  `json:"candidate_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Evaluated_count    int64  `json:"evaluated_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Unresolved_count   int64  `json:"unresolved_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Insufficient_count int64  `json:"insufficient_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Status             string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"complete\",\"partial\"]}"`
	Reason_code        string `json:"reason_code,omitempty" openapi:"{\"type\":\"string\"}"`
}
type OperatorEvaluationObject struct {
	Object_ref             string                          `json:"object_ref" openapi:"{\"type\":\"string\"}"`
	Source                 *OperatorEvaluationObjectSource `json:"source" openapi:"{\"type\":\"object\",\"nullable\":true}"`
	Service_object_id      *string                         `json:"service_object_id" openapi:"{\"type\":\"string\",\"nullable\":true}"`
	Object_version         *int64                          `json:"object_version" openapi:"{\"type\":\"integer\",\"nullable\":true}"`
	Association_version    *int64                          `json:"association_version" openapi:"{\"type\":\"integer\",\"nullable\":true}"`
	Association_run_id     string                          `json:"association_run_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Association_attempt_id string                          `json:"association_attempt_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Mapping_id             *string                         `json:"mapping_id" openapi:"{\"type\":\"string\",\"nullable\":true}"`
	Mapping_version        *int64                          `json:"mapping_version" openapi:"{\"type\":\"integer\",\"nullable\":true}"`
	Binding_version        *string                         `json:"binding_version" openapi:"{\"type\":\"string\",\"nullable\":true}"`
	Snapshot_set_id        *string                         `json:"snapshot_set_id" openapi:"{\"type\":\"string\",\"nullable\":true}"`
	Snapshot_collected_at  string                          `json:"snapshot_collected_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Status                 string                          `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"evaluated\",\"unresolved\",\"insufficient\",\"not_applicable\"]}"`
	Reason_code            string                          `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Facts_sha256           string                          `json:"facts_sha256,omitempty" openapi:"{\"type\":\"string\"}"`
	Fact_refs              []ServiceObjectFactRef          `json:"fact_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ServiceObjectFactRef\"}"`
	Facts                  []any                           `json:"facts" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
	Checks                 []any                           `json:"checks" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
}
type EvidenceRequestCreate struct {
	Case_id                string `json:"case_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Task_id                string `json:"task_id" openapi:"{\"type\":\"string\"}"`
	Task_version           int64  `json:"task_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Source_run_id          string `json:"source_run_id" openapi:"{\"type\":\"string\"}"`
	Source_attempt_id      string `json:"source_attempt_id" openapi:"{\"type\":\"string\"}"`
	Source_evaluation_id   string `json:"source_evaluation_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Source_gap_id          string `json:"source_gap_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Question               string `json:"question" openapi:"{\"type\":\"string\"}"`
	Material_role          string `json:"material_role,omitempty" openapi:"{\"type\":\"string\"}"`
	Verification_condition string `json:"verification_condition" openapi:"{\"type\":\"string\",\"enum\":[\"compatible\",\"conflict\",\"resolved\"]}"`
	Idempotency_key        string `json:"idempotency_key,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":80}"`
}
type EvidenceReceiptCreate struct {
	Case_id          string `json:"case_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Request_id       string `json:"request_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Document_id      string `json:"document_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Answer           string `json:"answer,omitempty" openapi:"{\"type\":\"string\"}"`
	Expected_version int64  `json:"expected_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Idempotency_key  string `json:"idempotency_key,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":80}"`
}
type EvidenceRequestVerify struct {
	Case_id          string                  `json:"case_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Request_id       string                  `json:"request_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Receipt_id       string                  `json:"receipt_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Run_id           string                  `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id       string                  `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Evaluation_id    string                  `json:"evaluation_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Gap_id           string                  `json:"gap_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Evidence         []ExperienceEvidenceRef `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ExperienceEvidenceRef\"}"`
	Reason           string                  `json:"reason" openapi:"{\"type\":\"string\"}"`
	Expected_version int64                   `json:"expected_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Idempotency_key  string                  `json:"idempotency_key,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":80}"`
}
type EvidenceSourceEvaluation struct {
	Id            string   `json:"id" openapi:"{\"type\":\"string\"}"`
	Run_id        string   `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id    string   `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Case_id       string   `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Domain        string   `json:"domain" openapi:"{\"type\":\"string\"}"`
	Rule_id       string   `json:"rule_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Rule_version  string   `json:"rule_version" openapi:"{\"type\":\"string\"}"`
	Capability_id string   `json:"capability_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Mandatory     bool     `json:"mandatory,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Relation      string   `json:"relation" openapi:"{\"type\":\"string\"}"`
	Reason_code   string   `json:"reason_code,omitempty" openapi:"{\"type\":\"string\"}"`
	Reason        string   `json:"reason" openapi:"{\"type\":\"string\"}"`
	Fact_ids      []string `json:"fact_ids,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Object_refs   []string `json:"object_refs,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Finding_id    string   `json:"finding_id,omitempty" openapi:"{\"type\":\"string\"}"`
}
type EvidenceSourceGap struct {
	Gap_id        string   `json:"gap_id" openapi:"{\"type\":\"string\"}"`
	Domain        string   `json:"domain" openapi:"{\"type\":\"string\"}"`
	Capability_id string   `json:"capability_id" openapi:"{\"type\":\"string\"}"`
	Mandatory     bool     `json:"mandatory,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Status        string   `json:"status" openapi:"{\"type\":\"string\"}"`
	Reason_code   string   `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Evidence_ids  []string `json:"evidence_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
}
type EvidenceRequestVerification struct {
	Run_id        string                   `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id    string                   `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Evaluation_id string                   `json:"evaluation_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Relation      string                   `json:"relation,omitempty" openapi:"{\"type\":\"string\"}"`
	Gap_id        string                   `json:"gap_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Gap_status    string                   `json:"gap_status,omitempty" openapi:"{\"type\":\"string\"}"`
	Receipt_id    string                   `json:"receipt_id" openapi:"{\"type\":\"string\"}"`
	Evidence      []ExperienceEvidenceView `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ExperienceEvidenceView\"}"`
	Reason        string                   `json:"reason" openapi:"{\"type\":\"string\"}"`
	Verified_by   string                   `json:"verified_by" openapi:"{\"type\":\"string\"}"`
	Verified_at   string                   `json:"verified_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type EvidenceRequest struct {
	Id                     string                      `json:"id" openapi:"{\"type\":\"string\"}"`
	Case_id                string                      `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Task_id                string                      `json:"task_id" openapi:"{\"type\":\"string\"}"`
	Task_version           int64                       `json:"task_version" openapi:"{\"type\":\"integer\"}"`
	Assignee_id            string                      `json:"assignee_id" openapi:"{\"type\":\"string\"}"`
	Due_at                 string                      `json:"due_at" openapi:"{\"type\":\"string\"}"`
	Source_run_id          string                      `json:"source_run_id" openapi:"{\"type\":\"string\"}"`
	Source_attempt_id      string                      `json:"source_attempt_id" openapi:"{\"type\":\"string\"}"`
	Source_evaluation      EvidenceSourceEvaluation    `json:"source_evaluation,omitempty" openapi:"{\"$ref\":\"#/components/schemas/EvidenceSourceEvaluation\"}"`
	Source_gap             EvidenceSourceGap           `json:"source_gap,omitempty" openapi:"{\"$ref\":\"#/components/schemas/EvidenceSourceGap\"}"`
	Question               string                      `json:"question" openapi:"{\"type\":\"string\"}"`
	Material_role          string                      `json:"material_role,omitempty" openapi:"{\"type\":\"string\"}"`
	Verification_condition string                      `json:"verification_condition" openapi:"{\"type\":\"string\"}"`
	Status                 string                      `json:"status" openapi:"{\"type\":\"string\"}"`
	Version                int64                       `json:"version" openapi:"{\"type\":\"integer\"}"`
	Created_by             string                      `json:"created_by" openapi:"{\"type\":\"string\"}"`
	Created_at             string                      `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Updated_at             string                      `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Verification           EvidenceRequestVerification `json:"verification,omitempty" openapi:"{\"$ref\":\"#/components/schemas/EvidenceRequestVerification\"}"`
}
type EvidenceRequestList struct {
	Items []EvidenceRequest `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/EvidenceRequest\"}"`
}
type EvidenceReceipt struct {
	Id           string `json:"id" openapi:"{\"type\":\"string\"}"`
	Request_id   string `json:"request_id" openapi:"{\"type\":\"string\"}"`
	Case_id      string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Document_id  string `json:"document_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Answer       string `json:"answer,omitempty" openapi:"{\"type\":\"string\"}"`
	Submitted_by string `json:"submitted_by" openapi:"{\"type\":\"string\"}"`
	Created_at   string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type EvidenceReceiptResult struct {
	Request EvidenceRequest `json:"request" openapi:"{\"$ref\":\"#/components/schemas/EvidenceRequest\"}"`
	Receipt EvidenceReceipt `json:"receipt" openapi:"{\"$ref\":\"#/components/schemas/EvidenceReceipt\"}"`
}
type EvidenceRequestEvent struct {
	Type        string          `json:"type" openapi:"{\"type\":\"string\"}"`
	Actor_id    string          `json:"actor_id" openapi:"{\"type\":\"string\"}"`
	Occurred_at string          `json:"occurred_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Request     EvidenceRequest `json:"request" openapi:"{\"$ref\":\"#/components/schemas/EvidenceRequest\"}"`
	Receipt     EvidenceReceipt `json:"receipt,omitempty" openapi:"{\"$ref\":\"#/components/schemas/EvidenceReceipt\"}"`
}
type EvidenceRequestHistory struct {
	Items []EvidenceRequestEvent `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/EvidenceRequestEvent\"}"`
}
type ServiceObjectCreateRequest struct {
	Object_type   string                                  `json:"object_type" openapi:"{\"type\":\"string\",\"enum\":[\"dedicated_line\",\"service\"]}"`
	Display_name  string                                  `json:"display_name" openapi:"{\"type\":\"string\"}"`
	Business_code string                                  `json:"business_code,omitempty" openapi:"{\"type\":\"string\"}"`
	Aliases       []ServiceObjectCreateRequestAliasesItem `json:"aliases,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"kind\",\"value\"]}"`
	Basis_summary string                                  `json:"basis_summary,omitempty" openapi:"{\"type\":\"string\"}"`
}
type ServiceObjectMutationReceipt struct {
	Service_object_id   string `json:"service_object_id" openapi:"{\"type\":\"string\"}"`
	Version             int64  `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Association_version int64  `json:"association_version" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type ServiceObjectSummary struct {
	Service_object_id           string               `json:"service_object_id" openapi:"{\"type\":\"string\"}"`
	Object_type                 string               `json:"object_type" openapi:"{\"type\":\"string\",\"enum\":[\"dedicated_line\",\"service\"]}"`
	State                       string               `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"pending_confirmation\",\"confirmed\",\"inactive\"]}"`
	Current_version             int64                `json:"current_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Current_association_version int64                `json:"current_association_version" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Display_name                string               `json:"display_name" openapi:"{\"type\":\"string\"}"`
	Business_code               string               `json:"business_code" openapi:"{\"type\":\"string\"}"`
	Aliases                     []ServiceObjectAlias `json:"aliases" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ServiceObjectAlias\"}"`
	Current_association         any                  `json:"current_association" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ServiceObjectAssociationPin\"},{\"type\":\"null\"}]}"`
	Can_manage                  bool                 `json:"can_manage" openapi:"{\"type\":\"boolean\"}"`
	Needs_reassociation         bool                 `json:"needs_reassociation" openapi:"{\"type\":\"boolean\"}"`
}
type ServiceObjectAlias struct {
	Kind  string `json:"kind" openapi:"{\"type\":\"string\"}"`
	Value string `json:"value" openapi:"{\"type\":\"string\"}"`
}
type ServiceObjectAssociationPin struct {
	Association_version int64  `json:"association_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Object_version      int64  `json:"object_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Material_id         string `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Material_version    int64  `json:"material_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Document_id         string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id  string `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
	Source_run_id       string `json:"source_run_id" openapi:"{\"type\":\"string\"}"`
	Source_attempt_id   string `json:"source_attempt_id" openapi:"{\"type\":\"string\"}"`
	Object_ref          string `json:"object_ref" openapi:"{\"type\":\"string\"}"`
	Facts_sha256        string `json:"facts_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Confirmed_by        string `json:"confirmed_by" openapi:"{\"type\":\"string\"}"`
	Confirmed_at        string `json:"confirmed_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ServiceObjectVersion struct {
	Service_object_id  string               `json:"service_object_id" openapi:"{\"type\":\"string\"}"`
	Version            int64                `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Object_type        string               `json:"object_type" openapi:"{\"type\":\"string\",\"enum\":[\"dedicated_line\",\"service\"]}"`
	Display_name       string               `json:"display_name" openapi:"{\"type\":\"string\"}"`
	Business_code      string               `json:"business_code" openapi:"{\"type\":\"string\"}"`
	Aliases            []ServiceObjectAlias `json:"aliases" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ServiceObjectAlias\"}"`
	State              string               `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"pending_confirmation\",\"confirmed\",\"inactive\"]}"`
	Basis_refs         []any                `json:"basis_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
	Basis_summary      string               `json:"basis_summary" openapi:"{\"type\":\"string\"}"`
	Change_decision_id string               `json:"change_decision_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Created_by         string               `json:"created_by" openapi:"{\"type\":\"string\"}"`
	Created_at         string               `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Effective_from     string               `json:"effective_from" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Payload_sha256     string               `json:"payload_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
}
type ServiceObjectFactRef struct {
	Fact                  ServiceObjectFact `json:"fact" openapi:"{\"$ref\":\"#/components/schemas/ServiceObjectFact\"}"`
	Document_id           string            `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id    string            `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
	Canonical_fact_sha256 string            `json:"canonical_fact_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
}
type ServiceObjectFact struct {
	Id                 string                      `json:"id" openapi:"{\"type\":\"string\"}"`
	Rule_id            string                      `json:"rule_id" openapi:"{\"type\":\"string\"}"`
	Dimension          string                      `json:"dimension" openapi:"{\"type\":\"string\"}"`
	Object_ref         string                      `json:"object_ref" openapi:"{\"type\":\"string\"}"`
	Value              string                      `json:"value" openapi:"{\"type\":\"string\"}"`
	Unit               string                      `json:"unit" openapi:"{\"type\":\"string\"}"`
	Source_role        string                      `json:"source_role" openapi:"{\"type\":\"string\"}"`
	Measurement_window string                      `json:"measurement_window,omitempty" openapi:"{\"type\":\"string\"}"`
	Time_zone          string                      `json:"time_zone,omitempty" openapi:"{\"type\":\"string\"}"`
	Calendar           string                      `json:"calendar,omitempty" openapi:"{\"type\":\"string\"}"`
	Outage_definition  string                      `json:"outage_definition,omitempty" openapi:"{\"type\":\"string\"}"`
	Exclusions         []string                    `json:"exclusions,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Basis              string                      `json:"basis,omitempty" openapi:"{\"type\":\"string\"}"`
	Applicable         bool                        `json:"applicable,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Citations          []ServiceObjectFactCitation `json:"citations" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ServiceObjectFactCitation\"}"`
}
type ServiceObjectFactCitation struct {
	Source_id      string `json:"source_id" openapi:"{\"type\":\"string\"}"`
	Source_version string `json:"source_version" openapi:"{\"type\":\"string\"}"`
	Tenant_id      string `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Case_id        string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Source_digest  string `json:"source_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Start          int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End            int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Quote          string `json:"quote" openapi:"{\"type\":\"string\"}"`
}
type ServiceObjectAssociation struct {
	Service_object_id           string                 `json:"service_object_id" openapi:"{\"type\":\"string\"}"`
	Association_version         int64                  `json:"association_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Object_version              int64                  `json:"object_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Material_id                 string                 `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Material_version            int64                  `json:"material_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Document_id                 string                 `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id          string                 `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
	Source_run_id               string                 `json:"source_run_id" openapi:"{\"type\":\"string\"}"`
	Source_attempt_id           string                 `json:"source_attempt_id" openapi:"{\"type\":\"string\"}"`
	Object_ref                  string                 `json:"object_ref" openapi:"{\"type\":\"string\"}"`
	Facts                       []ServiceObjectFactRef `json:"facts" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ServiceObjectFactRef\"}"`
	Basis_refs                  []any                  `json:"basis_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
	Confirmed_by                string                 `json:"confirmed_by" openapi:"{\"type\":\"string\"}"`
	Confirmed_at                string                 `json:"confirmed_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Identity_change_decision_id string                 `json:"identity_change_decision_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Facts_sha256                string                 `json:"facts_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Created_at                  string                 `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Payload_sha256              string                 `json:"payload_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
}
type ServiceObjectDetail struct {
	Service_object_id   string               `json:"service_object_id" openapi:"{\"type\":\"string\"}"`
	Current_version     int64                `json:"current_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Identity            ServiceObjectVersion `json:"identity" openapi:"{\"$ref\":\"#/components/schemas/ServiceObjectVersion\"}"`
	Association         any                  `json:"association" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ServiceObjectAssociation\"},{\"type\":\"null\"}]}"`
	Mapping             any                  `json:"mapping" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ServiceObjectMappingPin\"},{\"type\":\"null\"}]}"`
	Can_manage          bool                 `json:"can_manage" openapi:"{\"type\":\"boolean\"}"`
	Needs_reassociation bool                 `json:"needs_reassociation" openapi:"{\"type\":\"boolean\"}"`
}
type ServiceObjectMappingPin struct {
	Mapping_id          string                                 `json:"mapping_id" openapi:"{\"type\":\"string\"}"`
	Mapping_version     int64                                  `json:"mapping_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Binding_version     string                                 `json:"binding_version" openapi:"{\"type\":\"string\"}"`
	Object_version      int64                                  `json:"object_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Association_version int64                                  `json:"association_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	External_object     ServiceObjectMappingPinExternal_object `json:"external_object" openapi:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"connector_id\",\"external_object_id\"]}"`
	Effective_from      string                                 `json:"effective_from" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Effective_until     string                                 `json:"effective_until" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ServiceObjectVersionPage struct {
	Items       []ServiceObjectVersion `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ServiceObjectVersion\"}"`
	Next_cursor any                    `json:"next_cursor" openapi:"{\"type\":[\"string\",\"null\"]}"`
}
type ServiceObjectAssociationPage struct {
	Items       []ServiceObjectAssociation `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ServiceObjectAssociation\"}"`
	Next_cursor any                        `json:"next_cursor" openapi:"{\"type\":[\"string\",\"null\"]}"`
}
type ServiceObjectAssociationConfirmRequest struct {
	Confirmed                    any                                          `json:"confirmed" openapi:"{\"const\":true}"`
	Expected_object_version      int64                                        `json:"expected_object_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Expected_association_version int64                                        `json:"expected_association_version" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Source                       ServiceObjectAssociationConfirmRequestSource `json:"source" openapi:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"run_id\",\"attempt_id\",\"material_id\",\"material_version\",\"document_id\",\"parsed_document_id\"]}"`
	Object_ref                   string                                       `json:"object_ref" openapi:"{\"type\":\"string\"}"`
	Facts_sha256                 string                                       `json:"facts_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Candidate_sha256             string                                       `json:"candidate_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
}
type ServiceObjectAssociationPreview struct {
	Service_object_id            string                                       `json:"service_object_id" openapi:"{\"type\":\"string\"}"`
	Expected_object_version      int64                                        `json:"expected_object_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Expected_association_version int64                                        `json:"expected_association_version" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Run_id                       string                                       `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id                   string                                       `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Material_id                  string                                       `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Material_version             int64                                        `json:"material_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Document_id                  string                                       `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id           string                                       `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
	Candidate_sha256             string                                       `json:"candidate_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Options                      []ServiceObjectAssociationPreviewOptionsItem `json:"options" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"required\":[\"object_ref\",\"facts\",\"facts_sha256\",\"basis_refs\"]}"`
}
type OperatorMappingChangeCAS struct {
	Expected_mapping_version     int64  `json:"expected_mapping_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Expected_binding_version     string `json:"expected_binding_version" openapi:"{\"type\":\"string\"}"`
	Expected_object_version      int64  `json:"expected_object_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Expected_association_version int64  `json:"expected_association_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type OperatorMappingChangePatch struct {
	Kind                 string                                    `json:"kind" openapi:"{\"type\":\"string\",\"enum\":[\"replace_mapping\",\"retire_mapping\",\"reactivate_mapping\",\"correct_identity\",\"correct_identity_and_mapping\",\"retire_service_object\"]}"`
	External_object      OperatorMappingChangePatchExternal_object `json:"external_object,omitempty" openapi:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"connector_id\",\"external_object_id\"]}"`
	Identity             OperatorServiceIdentityPatch              `json:"identity,omitempty" openapi:"{\"$ref\":\"#/components/schemas/OperatorServiceIdentityPatch\"}"`
	Contract_association ServiceObjectAssociationConfirmRequest    `json:"contract_association,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ServiceObjectAssociationConfirmRequest\"}"`
	Effective_from       string                                    `json:"effective_from" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Effective_until      string                                    `json:"effective_until" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type OperatorServiceIdentityPatch struct {
	Object_type   string                                    `json:"object_type" openapi:"{\"type\":\"string\",\"enum\":[\"dedicated_line\",\"service\"]}"`
	Display_name  string                                    `json:"display_name" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":256}"`
	Business_code string                                    `json:"business_code" openapi:"{\"type\":\"string\",\"maxLength\":256}"`
	Aliases       []OperatorServiceIdentityPatchAliasesItem `json:"aliases" openapi:"{\"type\":\"array\",\"maxItems\":50}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"kind\",\"value\"]}"`
	Basis_summary string                                    `json:"basis_summary" openapi:"{\"type\":\"string\",\"maxLength\":1000}"`
	Basis_refs    []MaterialAnchor                          `json:"basis_refs" openapi:"{\"type\":\"array\",\"minItems\":1,\"maxItems\":100}" openapi_items:"{\"$ref\":\"#/components/schemas/MaterialAnchor\"}"`
	State         string                                    `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"confirmed\",\"inactive\"]}"`
}
type MaterialAnchor struct {
	Kind               any    `json:"kind" openapi:"{\"const\":\"material_anchor\"}"`
	Material_id        string `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Material_version   int64  `json:"material_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Document_id        string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id string `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
	Start              int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End                int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Quote_digest       string `json:"quote_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
}
type OperatorMappingChangePreview struct {
	Mapping_id        string                          `json:"mapping_id" openapi:"{\"type\":\"string\"}"`
	Service_object_id string                          `json:"service_object_id" openapi:"{\"type\":\"string\"}"`
	Cas               OperatorMappingChangeCAS        `json:"cas" openapi:"{\"$ref\":\"#/components/schemas/OperatorMappingChangeCAS\"}"`
	Before            any                             `json:"before" openapi:"{\"type\":\"object\"}"`
	After             any                             `json:"after" openapi:"{\"type\":\"object\"}"`
	Identity_after    ServiceObjectVersion            `json:"identity_after,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ServiceObjectVersion\"}"`
	Association_after OperatorMappingAssociationAfter `json:"association_after,omitempty" openapi:"{\"$ref\":\"#/components/schemas/OperatorMappingAssociationAfter\"}"`
	Before_sha256     string                          `json:"before_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	After_sha256      string                          `json:"after_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Impact_sha256     string                          `json:"impact_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Basis_refs        []MaterialAnchor                `json:"basis_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/MaterialAnchor\"}"`
}
type OperatorMappingAssociationAfter struct {
	Service_object_id   string                 `json:"service_object_id" openapi:"{\"type\":\"string\"}"`
	Object_version      int64                  `json:"object_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Association_version int64                  `json:"association_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Material_id         string                 `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Material_version    int64                  `json:"material_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Document_id         string                 `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id  string                 `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
	Source_run_id       string                 `json:"source_run_id" openapi:"{\"type\":\"string\"}"`
	Source_attempt_id   string                 `json:"source_attempt_id" openapi:"{\"type\":\"string\"}"`
	Object_ref          string                 `json:"object_ref" openapi:"{\"type\":\"string\"}"`
	Facts               []ServiceObjectFactRef `json:"facts" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ServiceObjectFactRef\"}"`
	Facts_sha256        string                 `json:"facts_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Basis_refs          []any                  `json:"basis_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
}
type OperatorMappingChangeRequest struct {
	Request_id                   string                     `json:"request_id" openapi:"{\"type\":\"string\"}"`
	Case_id                      string                     `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Mapping_id                   string                     `json:"mapping_id" openapi:"{\"type\":\"string\"}"`
	Service_object_id            string                     `json:"service_object_id" openapi:"{\"type\":\"string\"}"`
	Kind                         string                     `json:"kind" openapi:"{\"type\":\"string\"}"`
	Expected_mapping_version     int64                      `json:"expected_mapping_version" openapi:"{\"type\":\"integer\"}"`
	Expected_binding_version     string                     `json:"expected_binding_version" openapi:"{\"type\":\"string\"}"`
	Expected_object_version      int64                      `json:"expected_object_version" openapi:"{\"type\":\"integer\"}"`
	Expected_association_version int64                      `json:"expected_association_version" openapi:"{\"type\":\"integer\"}"`
	Before                       any                        `json:"before" openapi:"{\"type\":\"object\"}"`
	Before_sha256                string                     `json:"before_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	After                        any                        `json:"after" openapi:"{\"type\":\"object\"}"`
	After_sha256                 string                     `json:"after_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Patch                        OperatorMappingChangePatch `json:"patch" openapi:"{\"$ref\":\"#/components/schemas/OperatorMappingChangePatch\"}"`
	Patch_sha256                 string                     `json:"patch_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Basis_refs                   []MaterialAnchor           `json:"basis_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/MaterialAnchor\"}"`
	Impact_sha256                string                     `json:"impact_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	State                        string                     `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"pending_decision\",\"approved\",\"rejected\"]}"`
	Proposed_by                  any                        `json:"proposed_by" openapi:"{\"type\":\"object\"}"`
	Created_at                   string                     `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type OperatorMappingChangeDecision struct {
	Decision_id string `json:"decision_id" openapi:"{\"type\":\"string\"}"`
	Request_id  string `json:"request_id" openapi:"{\"type\":\"string\"}"`
	Decision    string `json:"decision" openapi:"{\"type\":\"string\",\"enum\":[\"approved\",\"rejected\"]}"`
	Reason      string `json:"reason" openapi:"{\"type\":\"string\"}"`
	Decided_by  any    `json:"decided_by" openapi:"{\"type\":\"object\"}"`
	Created_at  string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ReviewReadiness struct {
	Case_id                    string                    `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Revision                   string                    `json:"revision" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Business_at                string                    `json:"business_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Can_start                  bool                      `json:"can_start" openapi:"{\"type\":\"boolean\"}"`
	Material_selection_version int64                     `json:"material_selection_version" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Materials                  []ReviewReadinessMaterial `json:"materials" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ReviewReadinessMaterial\"}"`
	Scopes                     []ReviewReadinessScope    `json:"scopes" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ReviewReadinessScope\"}"`
}
type ReviewReadinessMaterial struct {
	Identity_kind string               `json:"identity_kind" openapi:"{\"type\":\"string\"}"`
	Material_id   string               `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Version       int64                `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Document_id   string               `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Filename      string               `json:"filename" openapi:"{\"type\":\"string\"}"`
	Role          string               `json:"role" openapi:"{\"type\":\"string\"}"`
	Purpose       string               `json:"purpose" openapi:"{\"type\":\"string\"}"`
	Selected      bool                 `json:"selected" openapi:"{\"type\":\"boolean\"}"`
	Parse         ReviewReadinessParse `json:"parse" openapi:"{\"$ref\":\"#/components/schemas/ReviewReadinessParse\"}"`
}
type ReviewReadinessParse struct {
	Status             string `json:"status" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id string `json:"parsed_document_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Parser_id          string `json:"parser_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Parser_version     string `json:"parser_version,omitempty" openapi:"{\"type\":\"string\"}"`
	Next_action        string `json:"next_action" openapi:"{\"type\":\"string\"}"`
}
type ReviewReadinessScope struct {
	Id        string                   `json:"id" openapi:"{\"type\":\"string\",\"enum\":[\"full_review\",\"single_contract_precheck\"]}"`
	Status    string                   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"ready\",\"blocked\"]}"`
	Can_start bool                     `json:"can_start" openapi:"{\"type\":\"boolean\"}"`
	Rule_pack ReviewReadinessRulePack  `json:"rule_pack,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ReviewReadinessRulePack\"}"`
	Rule_set  ReviewScopeIdentity      `json:"rule_set,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ReviewScopeIdentity\"}"`
	Checks    []ReviewReadinessCheck   `json:"checks" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ReviewReadinessCheck\"}"`
	Blockers  []ReviewReadinessBlocker `json:"blockers" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ReviewReadinessBlocker\"}"`
}
type ReviewScopeIdentity struct {
	Id               string `json:"id" openapi:"{\"type\":\"string\",\"const\":\"single_contract_precheck\"}"`
	Rule_set_id      string `json:"rule_set_id" openapi:"{\"type\":\"string\"}"`
	Rule_set_version string `json:"rule_set_version" openapi:"{\"type\":\"string\"}"`
	Rule_set_hash    string `json:"rule_set_hash" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
}
type ReviewReadinessRulePack struct {
	Pack_id         string `json:"pack_id" openapi:"{\"type\":\"string\"}"`
	Version         string `json:"version" openapi:"{\"type\":\"string\"}"`
	Snapshot_digest string `json:"snapshot_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
}
type ReviewReadinessCheck struct {
	Rule_id              string   `json:"rule_id" openapi:"{\"type\":\"string\"}"`
	Status               string   `json:"status" openapi:"{\"type\":\"string\"}"`
	Missing_dependencies []string `json:"missing_dependencies" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Next_action          string   `json:"next_action" openapi:"{\"type\":\"string\"}"`
}
type ReviewReadinessBlocker struct {
	Code                 string   `json:"code" openapi:"{\"type\":\"string\"}"`
	Status               string   `json:"status" openapi:"{\"type\":\"string\"}"`
	Message              string   `json:"message" openapi:"{\"type\":\"string\"}"`
	Missing_dependencies []string `json:"missing_dependencies" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Next_action          string   `json:"next_action" openapi:"{\"type\":\"string\"}"`
}
type ReviewStartRequest struct {
	Scope              string `json:"scope,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"full_review\",\"single_contract_precheck\"],\"default\":\"full_review\",\"description\":\"单合同预审仅执行版本固定的单合同检查，不替代完整审查。\"}"`
	Business_at        string `json:"business_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\",\"description\":\"已治理案件必填，带时区的 RFC3339Nano 业务时间；界面按北京时间输入并提交 UTC。\"}"`
	Evidence_cutoff_at string `json:"evidence_cutoff_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\",\"description\":\"可选法律证据截止时间；省略时由受理事务采样。显式值按UTC微秒规范化且不得晚于受理时刻。\"}"`
}
type ReviewDiagnostics struct {
	Case_id      string                          `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Run_id       string                          `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Execution    any                             `json:"execution" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/Execution\"},{\"type\":\"null\"}]}"`
	Snapshot     Snapshot                        `json:"snapshot" openapi:"{\"$ref\":\"#/components/schemas/Snapshot\"}"`
	Audit_events []AuditEvent                    `json:"audit_events" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AuditEvent\"}"`
	Documents    []Document                      `json:"documents" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Document\"}"`
	Evidence     []ReviewDiagnosticsEvidenceItem `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"finding_id\",\"anchors\"]}"`
}
type ExperienceEvidenceRef struct {
	Document_id        string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id string `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
	Normalized_digest  string `json:"normalized_digest,omitempty" openapi:"{\"type\":\"string\"}"`
	Quote              string `json:"quote" openapi:"{\"type\":\"string\"}"`
	Start              int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End                int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ExperienceEvidenceView struct {
	Document_id        string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id string `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
	Quote              string `json:"quote" openapi:"{\"type\":\"string\"}"`
	Start              int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End                int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type ExperienceFeedbackRequest struct {
	Case_id         string                  `json:"case_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Run_id          string                  `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id      string                  `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Finding_id      string                  `json:"finding_id" openapi:"{\"type\":\"string\"}"`
	Decision_id     string                  `json:"decision_id" openapi:"{\"type\":\"string\"}"`
	Reason          string                  `json:"reason" openapi:"{\"type\":\"string\"}"`
	Evidence_refs   []ExperienceEvidenceRef `json:"evidence_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ExperienceEvidenceRef\"}"`
	Idempotency_key string                  `json:"idempotency_key,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":80}"`
}
type ExperienceFeedback struct {
	Feedback_id          string                   `json:"feedback_id" openapi:"{\"type\":\"string\"}"`
	Tenant_id            string                   `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Org_id               string                   `json:"org_id" openapi:"{\"type\":\"string\"}"`
	Case_id              string                   `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Run_id               string                   `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id           string                   `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Finding_id           string                   `json:"finding_id" openapi:"{\"type\":\"string\"}"`
	Decision_id          string                   `json:"decision_id" openapi:"{\"type\":\"string\"}"`
	Decision_action      string                   `json:"decision_action" openapi:"{\"type\":\"string\"}"`
	Decision_disposition string                   `json:"decision_disposition" openapi:"{\"type\":\"string\"}"`
	Reason               string                   `json:"reason" openapi:"{\"type\":\"string\"}"`
	Evidence_refs        []ExperienceEvidenceView `json:"evidence_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ExperienceEvidenceView\"}"`
	Applicant_id         string                   `json:"applicant_id" openapi:"{\"type\":\"string\"}"`
	Created_at           string                   `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ExperienceDistillationRequest struct {
	Case_id         string `json:"case_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Feedback_id     string `json:"feedback_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Candidate_id    string `json:"candidate_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Base_version_id string `json:"base_version_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Idempotency_key string `json:"idempotency_key,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":80}"`
}
type ExperienceDistillationReceipt struct {
	Job_id string `json:"job_id" openapi:"{\"type\":\"string\"}"`
}
type ExperienceDistillationStatus struct {
	Job_id       string `json:"job_id" openapi:"{\"type\":\"string\"}"`
	Feedback_id  string `json:"feedback_id" openapi:"{\"type\":\"string\"}"`
	State        string `json:"state" openapi:"{\"type\":\"string\"}"`
	Failure_code string `json:"failure_code,omitempty" openapi:"{\"type\":\"string\"}"`
	Candidate_id string `json:"candidate_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Version_id   string `json:"version_id,omitempty" openapi:"{\"type\":\"string\"}"`
}
type GovernedKnowledgeRef struct {
	Knowledge_id   string `json:"knowledge_id" openapi:"{\"type\":\"string\"}"`
	Version_id     string `json:"version_id" openapi:"{\"type\":\"string\"}"`
	Content_sha256 string `json:"content_sha256" openapi:"{\"type\":\"string\"}"`
}
type ExperienceUsageRequest struct {
	Case_id         string               `json:"case_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Run_id          string               `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id      string               `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Finding_id      string               `json:"finding_id" openapi:"{\"type\":\"string\"}"`
	Ref             GovernedKnowledgeRef `json:"ref" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Expected_action string               `json:"expected_action" openapi:"{\"type\":\"string\",\"enum\":[\"\",\"confirm_risk\",\"false_positive\"]}"`
}
type ExperienceUsage struct {
	Case_id         string               `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Run_id          string               `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id      string               `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Finding_id      string               `json:"finding_id" openapi:"{\"type\":\"string\"}"`
	Ref             GovernedKnowledgeRef `json:"ref" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Snapshot_sha256 string               `json:"snapshot_sha256" openapi:"{\"type\":\"string\"}"`
	Input_sha256    string               `json:"input_sha256" openapi:"{\"type\":\"string\"}"`
	Expected_action string               `json:"expected_action" openapi:"{\"type\":\"string\",\"enum\":[\"\",\"confirm_risk\",\"false_positive\"]}"`
	Recorded_by     string               `json:"recorded_by" openapi:"{\"type\":\"string\"}"`
	Usage_sha256    string               `json:"usage_sha256" openapi:"{\"type\":\"string\"}"`
	Recorded_at     string               `json:"recorded_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ExperienceUsageOption struct {
	Ref                    GovernedKnowledgeRef `json:"ref" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Title                  string               `json:"title" openapi:"{\"type\":\"string\"}"`
	Body                   string               `json:"body" openapi:"{\"type\":\"string\"}"`
	Summary                string               `json:"summary" openapi:"{\"type\":\"string\"}"`
	Check_action           string               `json:"check_action" openapi:"{\"type\":\"string\"}"`
	Minimum_evidence       string               `json:"minimum_evidence" openapi:"{\"type\":\"string\"}"`
	Reviewed_applicability []string             `json:"reviewed_applicability" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Reviewed_exclusions    []string             `json:"reviewed_exclusions" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Counterexamples        []string             `json:"counterexamples" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Usage                  any                  `json:"usage" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ExperienceUsage\"},{\"type\":\"null\"}]}"`
}
type ExperienceUsageOptions struct {
	Items      []ExperienceUsageOption `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ExperienceUsageOption\"}"`
	Can_record bool                    `json:"can_record" openapi:"{\"type\":\"boolean\"}"`
}
type ExperienceMetricsSample struct {
	Case_id              string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Run_id               string `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id           string `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Finding_id           string `json:"finding_id" openapi:"{\"type\":\"string\"}"`
	Input_sha256         string `json:"input_sha256" openapi:"{\"type\":\"string\"}"`
	Snapshot_sha256      string `json:"snapshot_sha256" openapi:"{\"type\":\"string\"}"`
	Usage_sha256         string `json:"usage_sha256" openapi:"{\"type\":\"string\"}"`
	Expected_action      string `json:"expected_action" openapi:"{\"type\":\"string\",\"enum\":[\"\",\"confirm_risk\",\"false_positive\"]}"`
	Decision_id          string `json:"decision_id" openapi:"{\"type\":\"string\"}"`
	Decision_action      string `json:"decision_action" openapi:"{\"type\":\"string\"}"`
	Decision_disposition string `json:"decision_disposition" openapi:"{\"type\":\"string\"}"`
	Outcome              string `json:"outcome" openapi:"{\"type\":\"string\",\"enum\":[\"pending\",\"adopted\",\"overturned\",\"other\"]}"`
	Used_at              string `json:"used_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type ExperienceMetrics struct {
	Ref                GovernedKnowledgeRef      `json:"ref" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Has_been_published bool                      `json:"has_been_published" openapi:"{\"type\":\"boolean\"}"`
	Usage_count        int64                     `json:"usage_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Pending_count      int64                     `json:"pending_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Adopted_count      int64                     `json:"adopted_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Overturned_count   int64                     `json:"overturned_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Other_count        int64                     `json:"other_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Samples            []ExperienceMetricsSample `json:"samples" openapi:"{\"type\":\"array\",\"maxItems\":20}" openapi_items:"{\"$ref\":\"#/components/schemas/ExperienceMetricsSample\"}"`
}
type PolicyThresholdCandidate struct {
	Comparator string `json:"comparator" openapi:"{\"type\":\"string\"}"`
	Value      string `json:"value" openapi:"{\"type\":\"string\"}"`
	Unit       string `json:"unit" openapi:"{\"type\":\"string\"}"`
	Basis      string `json:"basis,omitempty" openapi:"{\"type\":\"string\"}"`
	Start      int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End        int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Quote      string `json:"quote" openapi:"{\"type\":\"string\"}"`
}
type PolicyRuleThreshold struct {
	Value string `json:"value" openapi:"{\"type\":\"string\"}"`
	Unit  string `json:"unit" openapi:"{\"type\":\"string\"}"`
	Basis string `json:"basis,omitempty" openapi:"{\"type\":\"string\"}"`
}
type PolicyRuleComparison struct {
	Rule_id        string                   `json:"rule_id" openapi:"{\"type\":\"string\"}"`
	Comparator     string                   `json:"comparator" openapi:"{\"type\":\"string\"}"`
	Candidate      PolicyThresholdCandidate `json:"candidate,omitempty" openapi:"{\"$ref\":\"#/components/schemas/PolicyThresholdCandidate\"}"`
	Rule_threshold PolicyRuleThreshold      `json:"rule_threshold" openapi:"{\"$ref\":\"#/components/schemas/PolicyRuleThreshold\"}"`
	Status         string                   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"match\",\"different\",\"ambiguous\",\"unexpressible\"]}"`
	Reason         string                   `json:"reason,omitempty" openapi:"{\"type\":\"string\"}"`
}
type PolicyTextIssue struct {
	Start  int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End    int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Quote  string `json:"quote" openapi:"{\"type\":\"string\"}"`
	Reason string `json:"reason" openapi:"{\"type\":\"string\"}"`
}
type GovernedPolicyPreview struct {
	Ref           GovernedKnowledgeRef   `json:"ref" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Extractor_ref string                 `json:"extractor_ref" openapi:"{\"type\":\"string\"}"`
	Comparisons   []PolicyRuleComparison `json:"comparisons" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/PolicyRuleComparison\"}"`
	Issues        []PolicyTextIssue      `json:"issues" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/PolicyTextIssue\"}"`
	Semantic_only []PolicyTextIssue      `json:"semantic_only" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/PolicyTextIssue\"}"`
	Approvable    bool                   `json:"approvable" openapi:"{\"type\":\"boolean\"}"`
}
type GovernedRulePackRef struct {
	Pack_id          string `json:"pack_id" openapi:"{\"type\":\"string\"}"`
	Version          string `json:"version" openapi:"{\"type\":\"string\"}"`
	Content_sha256   string `json:"content_sha256" openapi:"{\"type\":\"string\"}"`
	Snapshot_sha256  string `json:"snapshot_sha256" openapi:"{\"type\":\"string\"}"`
	Compiler_ref     string `json:"compiler_ref" openapi:"{\"type\":\"string\"}"`
	Pointer_revision int64  `json:"pointer_revision" openapi:"{\"type\":\"integer\"}"`
}
type GovernedKnowledgeSelection struct {
	Ref                      GovernedKnowledgeRef         `json:"ref" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Kind                     string                       `json:"kind" openapi:"{\"type\":\"string\"}"`
	Source_type              string                       `json:"source_type" openapi:"{\"type\":\"string\"}"`
	Scope                    GovernedKnowledgeScope       `json:"scope" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeScope\"}"`
	Required                 bool                         `json:"required" openapi:"{\"type\":\"boolean\"}"`
	Summary                  string                       `json:"summary" openapi:"{\"type\":\"string\"}"`
	Valid_from               string                       `json:"valid_from" openapi:"{\"type\":\"string\"}"`
	Valid_until              string                       `json:"valid_until" openapi:"{\"type\":\"string\"}"`
	Exception_basis          string                       `json:"exception_basis,omitempty" openapi:"{\"type\":\"string\"}"`
	Policy_refs              []GovernedKnowledgeRef       `json:"policy_refs,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Applicability            []GovernedKnowledgeCondition `json:"applicability" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeCondition\"}"`
	Exclusions               []GovernedKnowledgeCondition `json:"exclusions" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeCondition\"}"`
	Reviewed_applicability   []string                     `json:"reviewed_applicability,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Reviewed_exclusions      []string                     `json:"reviewed_exclusions,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Check_action             string                       `json:"check_action,omitempty" openapi:"{\"type\":\"string\"}"`
	Minimum_evidence         string                       `json:"minimum_evidence,omitempty" openapi:"{\"type\":\"string\"}"`
	Counterexamples          []string                     `json:"counterexamples,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Semantic_only            []PolicyTextIssue            `json:"semantic_only,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/PolicyTextIssue\"}"`
	Requires_fact_evaluation bool                         `json:"requires_fact_evaluation,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Pointer_revision         int64                        `json:"pointer_revision" openapi:"{\"type\":\"integer\"}"`
	Binding                  GovernedKnowledgeBinding     `json:"binding" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeBinding\"}"`
	Selection_reason         string                       `json:"selection_reason" openapi:"{\"type\":\"string\"}"`
}
type ReviewKnowledgeSnapshot struct {
	Encoding                    string                                `json:"encoding" openapi:"{\"type\":\"string\"}"`
	Business_at                 string                                `json:"business_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Scope                       GovernedKnowledgeScope                `json:"scope" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeScope\"}"`
	Authorization_revision      string                                `json:"authorization_revision" openapi:"{\"type\":\"string\"}"`
	Rule_pack                   GovernedRulePackRef                   `json:"rule_pack" openapi:"{\"$ref\":\"#/components/schemas/GovernedRulePackRef\"}"`
	Required_policies           []GovernedKnowledgeSelection          `json:"required_policies" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeSelection\"}"`
	Optional_candidates         []GovernedKnowledgeSelection          `json:"optional_candidates" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeSelection\"}"`
	Excluded                    []ReviewKnowledgeSnapshotExcludedItem `json:"excluded" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"ref\",\"reason\"]}"`
	Optional_truncated          bool                                  `json:"optional_truncated" openapi:"{\"type\":\"boolean\"}"`
	Snapshot_sha256             string                                `json:"snapshot_sha256" openapi:"{\"type\":\"string\"}"`
	Evidence_cutoff_at          string                                `json:"evidence_cutoff_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Evidence_cutoff_mode        string                                `json:"evidence_cutoff_mode,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"explicit\",\"acceptance_time\"]}"`
	Explicit_evidence_cutoff_at string                                `json:"explicit_evidence_cutoff_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Legal_authorities           []FrozenLegalAuthority                `json:"legal_authorities,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/FrozenLegalAuthority\"}"`
	Legal_reuse_fingerprint     string                                `json:"legal_reuse_fingerprint,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
}
type FrozenLegalAuthority struct {
	Citation_id              string `json:"citation_id" openapi:"{\"type\":\"string\",\"pattern\":\"^[a-f0-9]{64}$\"}"`
	Rule_id                  string `json:"rule_id" openapi:"{\"type\":\"string\"}"`
	Authority_source_id      string `json:"authority_source_id" openapi:"{\"type\":\"string\"}"`
	Authority_source_version string `json:"authority_source_version" openapi:"{\"type\":\"string\"}"`
	Rulepack_id              string `json:"rulepack_id" openapi:"{\"type\":\"string\"}"`
	Rulepack_version         string `json:"rulepack_version" openapi:"{\"type\":\"string\"}"`
	Rulepack_content_sha256  string `json:"rulepack_content_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Exact_reference          any    `json:"exact_reference,omitempty" openapi:"{\"type\":\"object\"}"`
	Article                  any    `json:"article,omitempty" openapi:"{\"type\":\"object\"}"`
	Outcome                  string `json:"outcome" openapi:"{\"type\":\"string\",\"enum\":[\"applicable\",\"not_applicable\",\"unknown\"]}"`
	Outcome_reason           string `json:"outcome_reason,omitempty" openapi:"{\"type\":\"string\"}"`
	Conditions               []any  `json:"conditions" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
	Selected_fact_versions   []any  `json:"selected_fact_versions" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
	Missing_dependencies     []any  `json:"missing_dependencies" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
	Legacy_rule_gap          bool   `json:"legacy_rule_gap,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Legal_review_status      string `json:"legal_review_status" openapi:"{\"type\":\"string\",\"enum\":[\"NOT_RUN\"]}"`
	Reviewer                 any    `json:"reviewer" openapi:"{\"type\":[\"string\",\"null\"]}"`
}
type ContractCitation struct {
	Case_id            string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Run_id             string `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id         string `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Evidence_id        string `json:"evidence_id" openapi:"{\"type\":\"string\"}"`
	Document_id        string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id string `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
	Filename           string `json:"filename" openapi:"{\"type\":\"string\"}"`
	Original_digest    string `json:"original_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Parse_digest       string `json:"parse_digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Start              int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End                int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Quote              string `json:"quote" openapi:"{\"type\":\"string\"}"`
	Text               string `json:"text" openapi:"{\"type\":\"string\"}"`
	Material_id        any    `json:"material_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Material_version   any    `json:"material_version" openapi:"{\"type\":[\"integer\",\"null\"],\"minimum\":0}"`
}
type FrozenLegalReviewProjection struct {
	Run_id                      string                 `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id                  string                 `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Evidence_cutoff_at          string                 `json:"evidence_cutoff_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Evidence_cutoff_mode        string                 `json:"evidence_cutoff_mode" openapi:"{\"type\":\"string\",\"enum\":[\"explicit\",\"acceptance_time\"]}"`
	Explicit_evidence_cutoff_at string                 `json:"explicit_evidence_cutoff_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Authorities                 []FrozenLegalAuthority `json:"authorities" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/FrozenLegalAuthority\"}"`
}
type ExperienceCandidateContent struct {
	Schema_version   string                 `json:"schema_version" openapi:"{\"type\":\"string\"}"`
	Applicability    []string               `json:"applicability" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Exclusions       []string               `json:"exclusions" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Check_action     string                 `json:"check_action" openapi:"{\"type\":\"string\"}"`
	Minimum_evidence []string               `json:"minimum_evidence" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Counterexamples  []string               `json:"counterexamples" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Valid_from       string                 `json:"valid_from" openapi:"{\"type\":\"string\"}"`
	Valid_until      string                 `json:"valid_until" openapi:"{\"type\":\"string\"}"`
	Policy_refs      []GovernedKnowledgeRef `json:"policy_refs" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Review_diff      string                 `json:"review_diff" openapi:"{\"type\":\"string\"}"`
}
type ExperienceCandidate struct {
	Tenant_id       string                     `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Org_id          string                     `json:"org_id" openapi:"{\"type\":\"string\"}"`
	Case_id         string                     `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Feedback_id     string                     `json:"feedback_id" openapi:"{\"type\":\"string\"}"`
	Candidate_id    string                     `json:"candidate_id" openapi:"{\"type\":\"string\"}"`
	Version_id      string                     `json:"version_id" openapi:"{\"type\":\"string\"}"`
	Base_version_id string                     `json:"base_version_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Content_sha256  string                     `json:"content_sha256" openapi:"{\"type\":\"string\"}"`
	Job_id          string                     `json:"job_id" openapi:"{\"type\":\"string\"}"`
	Generation      int64                      `json:"generation" openapi:"{\"type\":\"integer\"}"`
	Input_sha256    string                     `json:"input_sha256" openapi:"{\"type\":\"string\"}"`
	Result_sha256   string                     `json:"result_sha256" openapi:"{\"type\":\"string\"}"`
	Model_ref       string                     `json:"model_ref" openapi:"{\"type\":\"string\"}"`
	Template_ref    string                     `json:"template_ref" openapi:"{\"type\":\"string\"}"`
	Content         ExperienceCandidateContent `json:"content" openapi:"{\"$ref\":\"#/components/schemas/ExperienceCandidateContent\"}"`
	Created_at      string                     `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type GovernedKnowledgeScope struct {
	Org_id        string `json:"org_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Case_id       string `json:"case_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Business_line string `json:"business_line" openapi:"{\"type\":\"string\"}"`
	Scenario      string `json:"scenario" openapi:"{\"type\":\"string\"}"`
	Contract_type string `json:"contract_type" openapi:"{\"type\":\"string\"}"`
}
type GovernedKnowledgeCondition struct {
	Field  string `json:"field" openapi:"{\"type\":\"string\"}"`
	Equals string `json:"equals" openapi:"{\"type\":\"string\"}"`
}
type GovernedKnowledgeSourceSpan struct {
	Rule_id string `json:"rule_id" openapi:"{\"type\":\"string\"}"`
	Start   int64  `json:"start" openapi:"{\"type\":\"integer\"}"`
	End     int64  `json:"end" openapi:"{\"type\":\"integer\"}"`
	Quote   string `json:"quote" openapi:"{\"type\":\"string\"}"`
}
type GovernedKnowledgeBinding struct {
	Pack_id                 string                        `json:"pack_id" openapi:"{\"type\":\"string\"}"`
	Pack_version            string                        `json:"pack_version" openapi:"{\"type\":\"string\"}"`
	Pack_content_hash       string                        `json:"pack_content_hash" openapi:"{\"type\":\"string\"}"`
	Pack_snapshot_digest    string                        `json:"pack_snapshot_digest" openapi:"{\"type\":\"string\"}"`
	Compiler_ref            string                        `json:"compiler_ref" openapi:"{\"type\":\"string\"}"`
	Extraction_compiler_ref string                        `json:"extraction_compiler_ref,omitempty" openapi:"{\"type\":\"string\"}"`
	Rule_ids                []string                      `json:"rule_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Source_spans            []GovernedKnowledgeSourceSpan `json:"source_spans" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeSourceSpan\"}"`
	Candidate_diff          string                        `json:"candidate_diff" openapi:"{\"type\":\"string\"}"`
	Validation_evidence     string                        `json:"validation_evidence" openapi:"{\"type\":\"string\"}"`
}
type GovernedKnowledgeContent struct {
	Encoding                 string                       `json:"encoding,omitempty" openapi:"{\"type\":\"string\"}"`
	Knowledge_id             string                       `json:"knowledge_id" openapi:"{\"type\":\"string\"}"`
	Version                  string                       `json:"version" openapi:"{\"type\":\"string\"}"`
	Kind                     string                       `json:"kind" openapi:"{\"type\":\"string\",\"enum\":[\"policy\",\"guidance\",\"exception\",\"experience\"]}"`
	Title                    string                       `json:"title" openapi:"{\"type\":\"string\"}"`
	Body                     string                       `json:"body" openapi:"{\"type\":\"string\"}"`
	Summary                  string                       `json:"summary" openapi:"{\"type\":\"string\"}"`
	Source_type              string                       `json:"source_type" openapi:"{\"type\":\"string\"}"`
	Source_reference         string                       `json:"source_reference" openapi:"{\"type\":\"string\"}"`
	Source_case_id           string                       `json:"source_case_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Source_org_id            string                       `json:"source_org_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Source_feedback_id       string                       `json:"source_feedback_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Candidate_id             string                       `json:"candidate_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Candidate_version_id     string                       `json:"candidate_version_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Candidate_content_sha256 string                       `json:"candidate_content_sha256,omitempty" openapi:"{\"type\":\"string\"}"`
	Candidate_review_diff    string                       `json:"candidate_review_diff,omitempty" openapi:"{\"type\":\"string\"}"`
	Reviewed_applicability   []string                     `json:"reviewed_applicability,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Reviewed_exclusions      []string                     `json:"reviewed_exclusions,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Scope                    GovernedKnowledgeScope       `json:"scope" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeScope\"}"`
	Valid_from               string                       `json:"valid_from" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Valid_until              string                       `json:"valid_until" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Applicability            []GovernedKnowledgeCondition `json:"applicability,omitempty" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeCondition\"}"`
	Exclusions               []GovernedKnowledgeCondition `json:"exclusions,omitempty" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeCondition\"}"`
	Binding                  GovernedKnowledgeBinding     `json:"binding" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeBinding\"}"`
	Exception_basis          string                       `json:"exception_basis,omitempty" openapi:"{\"type\":\"string\"}"`
	Check_action             string                       `json:"check_action,omitempty" openapi:"{\"type\":\"string\"}"`
	Minimum_evidence         string                       `json:"minimum_evidence,omitempty" openapi:"{\"type\":\"string\"}"`
	Counterexamples          []string                     `json:"counterexamples,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Policy_refs              []GovernedKnowledgeRef       `json:"policy_refs,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
}
type GovernedKnowledgeCreateRequest struct {
	Content         GovernedKnowledgeContent `json:"content" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeContent\"}"`
	Idempotency_key string                   `json:"idempotency_key,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":80}"`
}
type GovernedKnowledgeCommandRequest struct {
	Knowledge_id              string `json:"knowledge_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Version_id                string `json:"version_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Action                    string `json:"action" openapi:"{\"type\":\"string\",\"enum\":[\"submit\",\"validate\",\"approve\",\"publish\",\"withdraw\",\"rollback\"]}"`
	Expected_revision         int64  `json:"expected_revision" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Expected_content_sha256   string `json:"expected_content_sha256" openapi:"{\"type\":\"string\"}"`
	Expected_pointer_revision int64  `json:"expected_pointer_revision,omitempty" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Reason                    string `json:"reason" openapi:"{\"type\":\"string\"}"`
	Cross_case_reviewed       bool   `json:"cross_case_reviewed,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Idempotency_key           string `json:"idempotency_key,omitempty" openapi:"{\"type\":\"string\",\"maxLength\":80}"`
}
type GovernedKnowledgeVersion struct {
	Ref                 GovernedKnowledgeRef     `json:"ref" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Content             GovernedKnowledgeContent `json:"content" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeContent\"}"`
	Status              string                   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"draft\",\"submitted\",\"validated\",\"approved\",\"published\",\"withdrawn\"]}"`
	Revision            int64                    `json:"revision" openapi:"{\"type\":\"integer\"}"`
	Approved_digest     string                   `json:"approved_digest,omitempty" openapi:"{\"type\":\"string\"}"`
	Approval_actor_id   string                   `json:"approval_actor_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Approval_reason     string                   `json:"approval_reason,omitempty" openapi:"{\"type\":\"string\"}"`
	Cross_case_reviewed bool                     `json:"cross_case_reviewed" openapi:"{\"type\":\"boolean\"}"`
	Active              bool                     `json:"active" openapi:"{\"type\":\"boolean\"}"`
	Pointer_revision    int64                    `json:"pointer_revision" openapi:"{\"type\":\"integer\"}"`
	Created_by          string                   `json:"created_by" openapi:"{\"type\":\"string\"}"`
	Created_at          string                   `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type GovernedKnowledgeSummary struct {
	Ref              GovernedKnowledgeRef   `json:"ref" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Version          string                 `json:"version" openapi:"{\"type\":\"string\"}"`
	Title            string                 `json:"title" openapi:"{\"type\":\"string\"}"`
	Kind             string                 `json:"kind" openapi:"{\"type\":\"string\"}"`
	Source_type      string                 `json:"source_type" openapi:"{\"type\":\"string\"}"`
	Scope            GovernedKnowledgeScope `json:"scope" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeScope\"}"`
	Valid_from       string                 `json:"valid_from" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Valid_until      string                 `json:"valid_until" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Status           string                 `json:"status" openapi:"{\"type\":\"string\"}"`
	Revision         int64                  `json:"revision" openapi:"{\"type\":\"integer\"}"`
	Pointer_revision int64                  `json:"pointer_revision" openapi:"{\"type\":\"integer\"}"`
	Active           bool                   `json:"active" openapi:"{\"type\":\"boolean\"}"`
}
type GovernedKnowledgeList struct {
	Items []GovernedKnowledgeSummary `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeSummary\"}"`
}
type GovernedKnowledgeHistoryEntry struct {
	Sequence             int64                `json:"sequence" openapi:"{\"type\":\"integer\"}"`
	Ref                  GovernedKnowledgeRef `json:"ref" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Action               string               `json:"action" openapi:"{\"type\":\"string\"}"`
	Revision             int64                `json:"revision" openapi:"{\"type\":\"integer\"}"`
	Pointer_revision     int64                `json:"pointer_revision" openapi:"{\"type\":\"integer\"}"`
	Actor_id             string               `json:"actor_id" openapi:"{\"type\":\"string\"}"`
	Reason               string               `json:"reason" openapi:"{\"type\":\"string\"}"`
	Previous_version_id  string               `json:"previous_version_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Previous_active      bool                 `json:"previous_active" openapi:"{\"type\":\"boolean\"}"`
	Resulting_version_id string               `json:"resulting_version_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Resulting_active     bool                 `json:"resulting_active" openapi:"{\"type\":\"boolean\"}"`
	Occurred_at          string               `json:"occurred_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type GovernedKnowledgeHistory struct {
	Items []GovernedKnowledgeHistoryEntry `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeHistoryEntry\"}"`
}
type CaseProfile struct {
	Case_id           string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Number            string `json:"number" openapi:"{\"type\":\"string\"}"`
	Customer          string `json:"customer" openapi:"{\"type\":\"string\"}"`
	Supplier          string `json:"supplier" openapi:"{\"type\":\"string\"}"`
	Contract_category string `json:"contract_category" openapi:"{\"type\":\"string\"}"`
	Owner_id          string `json:"owner_id" openapi:"{\"type\":\"string\"}"`
	Due_at            any    `json:"due_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Version           int64  `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Updated_at        string `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type CaseProfileInput struct {
	Number            string `json:"number" openapi:"{\"type\":\"string\"}"`
	Customer          string `json:"customer" openapi:"{\"type\":\"string\"}"`
	Supplier          string `json:"supplier" openapi:"{\"type\":\"string\"}"`
	Contract_category string `json:"contract_category" openapi:"{\"type\":\"string\"}"`
	Owner_id          string `json:"owner_id" openapi:"{\"type\":\"string\"}"`
	Due_at            any    `json:"due_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Expected_version  int64  `json:"expected_version" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type WorkbenchTask struct {
	Id          string `json:"id" openapi:"{\"type\":\"string\"}"`
	Case_id     string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Title       string `json:"title" openapi:"{\"type\":\"string\"}"`
	Kind        string `json:"kind" openapi:"{\"type\":\"string\",\"enum\":[\"review\",\"decision\",\"remediation\",\"general\"]}"`
	Assignee_id string `json:"assignee_id" openapi:"{\"type\":\"string\"}"`
	Due_at      any    `json:"due_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Status      string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"open\",\"in_progress\",\"completed\"]}"`
	Priority    string `json:"priority" openapi:"{\"type\":\"string\",\"enum\":[\"high\",\"medium\",\"low\"]}"`
	Finding_id  any    `json:"finding_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Created_by  any    `json:"created_by" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Version     int64  `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Created_at  string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Updated_at  string `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type WorkbenchSnapshotTask struct {
	Id           string `json:"id" openapi:"{\"type\":\"string\"}"`
	Case_id      string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Title        string `json:"title" openapi:"{\"type\":\"string\"}"`
	Kind         string `json:"kind" openapi:"{\"type\":\"string\",\"enum\":[\"review\",\"decision\",\"remediation\",\"general\"]}"`
	Assignee_id  string `json:"assignee_id" openapi:"{\"type\":\"string\"}"`
	Due_at       any    `json:"due_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Status       string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"open\",\"in_progress\",\"completed\"]}"`
	Priority     string `json:"priority" openapi:"{\"type\":\"string\",\"enum\":[\"high\",\"medium\",\"low\"]}"`
	Finding_id   any    `json:"finding_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Created_by   any    `json:"created_by" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Version      int64  `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Created_at   string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Updated_at   string `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Can_complete bool   `json:"can_complete,omitempty" openapi:"{\"type\":\"boolean\"}"`
}
type WorkbenchTaskInput struct {
	Title            string `json:"title" openapi:"{\"type\":\"string\"}"`
	Kind             string `json:"kind" openapi:"{\"type\":\"string\",\"enum\":[\"review\",\"decision\",\"remediation\",\"general\"]}"`
	Assignee_id      string `json:"assignee_id" openapi:"{\"type\":\"string\"}"`
	Due_at           any    `json:"due_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Status           string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"open\",\"in_progress\",\"completed\"]}"`
	Priority         string `json:"priority" openapi:"{\"type\":\"string\",\"enum\":[\"high\",\"medium\",\"low\"]}"`
	Finding_id       any    `json:"finding_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Expected_version int64  `json:"expected_version,omitempty" openapi:"{\"type\":\"integer\",\"const\":0}"`
}
type WorkbenchTaskUpdateInput struct {
	Title            string `json:"title" openapi:"{\"type\":\"string\"}"`
	Kind             string `json:"kind" openapi:"{\"type\":\"string\",\"enum\":[\"review\",\"decision\",\"remediation\",\"general\"]}"`
	Assignee_id      string `json:"assignee_id" openapi:"{\"type\":\"string\"}"`
	Due_at           any    `json:"due_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Status           string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"open\",\"in_progress\",\"completed\"]}"`
	Priority         string `json:"priority" openapi:"{\"type\":\"string\",\"enum\":[\"high\",\"medium\",\"low\"]}"`
	Finding_id       any    `json:"finding_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Expected_version int64  `json:"expected_version" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type CaseRelation struct {
	Id              string `json:"id" openapi:"{\"type\":\"string\"}"`
	Case_id         string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Related_case_id string `json:"related_case_id" openapi:"{\"type\":\"string\"}"`
	Reason          string `json:"reason" openapi:"{\"type\":\"string\"}"`
	Created_by      string `json:"created_by" openapi:"{\"type\":\"string\"}"`
	Created_at      string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type CaseRelationInput struct {
	Related_case_id string `json:"related_case_id" openapi:"{\"type\":\"string\"}"`
	Reason          string `json:"reason" openapi:"{\"type\":\"string\"}"`
}
type Knowledge struct {
	Id           string `json:"id" openapi:"{\"type\":\"string\"}"`
	Title        string `json:"title" openapi:"{\"type\":\"string\"}"`
	Version      string `json:"version" openapi:"{\"type\":\"string\"}"`
	Version_id   string `json:"version_id" openapi:"{\"type\":\"string\",\"minLength\":64,\"maxLength\":64,\"pattern\":\"^[0-9a-f]{64}$\",\"description\":\"可信服务身份的租户、制度ID和展示版本组成的JSON字符串数组的SHA-256小写十六进制摘要。\"}"`
	Body         string `json:"body" openapi:"{\"type\":\"string\"}"`
	Source_type  string `json:"source_type" openapi:"{\"type\":\"string\",\"enum\":[\"synthetic_policy\",\"reference\"]}"`
	Effective_at any    `json:"effective_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Created_by   string `json:"created_by" openapi:"{\"type\":\"string\"}"`
	Created_at   string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type KnowledgeInput struct {
	Id           string `json:"id,omitempty" openapi:"{\"type\":\"string\"}"`
	Title        string `json:"title" openapi:"{\"type\":\"string\"}"`
	Version      string `json:"version" openapi:"{\"type\":\"string\"}"`
	Body         string `json:"body" openapi:"{\"type\":\"string\"}"`
	Source_type  string `json:"source_type" openapi:"{\"type\":\"string\",\"enum\":[\"synthetic_policy\",\"reference\"]}"`
	Effective_at any    `json:"effective_at,omitempty" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
}
type SeveritySource struct {
	Policy_id      string `json:"policy_id" openapi:"{\"type\":\"string\"}"`
	Policy_version string `json:"policy_version" openapi:"{\"type\":\"string\"}"`
	Clause         string `json:"clause" openapi:"{\"type\":\"string\"}"`
	Reason         string `json:"reason" openapi:"{\"type\":\"string\"}"`
}
type WorkbenchDashboard struct {
	Visible_cases        int64                            `json:"visible_cases" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	My_pending_tasks     int64                            `json:"my_pending_tasks" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Overdue_tasks        int64                            `json:"overdue_tasks" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Pending_review_cases int64                            `json:"pending_review_cases" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Remediation_cases    int64                            `json:"remediation_cases" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Completed_reviews    int64                            `json:"completed_reviews" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Average_duration_ms  any                              `json:"average_duration_ms" openapi:"{\"type\":[\"integer\",\"null\"],\"minimum\":0}"`
	Progress             []WorkbenchDashboardProgressItem `json:"progress" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"status\",\"count\"]}"`
	Risks                []WorkbenchDashboardRisksItem    `json:"risks" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"severity\",\"count\"]}"`
}
type WorkbenchModel struct {
	Name     string `json:"name" openapi:"{\"type\":\"string\"}"`
	Ocr_name string `json:"ocr_name" openapi:"{\"type\":\"string\"}"`
	Status   string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"configured\",\"unconfigured\"]}"`
	Label    string `json:"label" openapi:"{\"type\":\"string\"}"`
}
type WorkbenchTool struct {
	Id           string   `json:"id" openapi:"{\"type\":\"string\"}"`
	Name         string   `json:"name" openapi:"{\"type\":\"string\"}"`
	Description  string   `json:"description" openapi:"{\"type\":\"string\"}"`
	Capabilities []string `json:"capabilities" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Calls        int64    `json:"calls" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Last_status  any      `json:"last_status" openapi:"{\"type\":[\"string\",\"null\"]}"`
}
type WorkbenchRun struct {
	Id          string `json:"id" openapi:"{\"type\":\"string\"}"`
	Case_id     string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Case_title  string `json:"case_title" openapi:"{\"type\":\"string\"}"`
	State       string `json:"state" openapi:"{\"type\":\"string\"}"`
	Created_at  string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Updated_at  string `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Attempt_id  any    `json:"attempt_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Started_at  any    `json:"started_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Finished_at any    `json:"finished_at" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"}"`
	Usage       any    `json:"usage" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ExecutionUsage\"},{\"type\":\"null\"}]}"`
}
type WorkbenchAudit struct {
	Id         string `json:"id" openapi:"{\"type\":\"string\"}"`
	Case_id    any    `json:"case_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Case_title any    `json:"case_title" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Actor_id   string `json:"actor_id" openapi:"{\"type\":\"string\"}"`
	Actor_name string `json:"actor_name" openapi:"{\"type\":\"string\"}"`
	Action     string `json:"action" openapi:"{\"type\":\"string\"}"`
	At         string `json:"at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Summary    string `json:"summary" openapi:"{\"type\":\"string\"}"`
}
type Workbench struct {
	Schema_version string                  `json:"schema_version" openapi:"{\"type\":\"string\",\"const\":\"workbench/v1\"}"`
	Generated_at   string                  `json:"generated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Period         string                  `json:"period" openapi:"{\"type\":\"string\",\"enum\":[\"day\",\"week\",\"month\"]}"`
	Dashboard      WorkbenchDashboard      `json:"dashboard" openapi:"{\"$ref\":\"#/components/schemas/WorkbenchDashboard\"}"`
	Cases          []WorkbenchCase         `json:"cases" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkbenchCase\"}"`
	Profiles       []CaseProfile           `json:"profiles" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/CaseProfile\"}"`
	Tasks          []WorkbenchSnapshotTask `json:"tasks" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkbenchSnapshotTask\"}"`
	Relations      []CaseRelation          `json:"relations" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/CaseRelation\"}"`
	Knowledge      []Knowledge             `json:"knowledge" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Knowledge\"}"`
	Members        []WorkbenchMember       `json:"members" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkbenchMember\"}"`
	Remediations   []WorkbenchRemediation  `json:"remediations" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkbenchRemediation\"}"`
	Reports        []WorkbenchReport       `json:"reports" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkbenchReport\"}"`
	Documents      []WorkbenchDocument     `json:"documents" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkbenchDocument\"}"`
	Runs           []WorkbenchRun          `json:"runs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkbenchRun\"}"`
	Audit          []WorkbenchAudit        `json:"audit" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkbenchAudit\"}"`
	Templates      []Template              `json:"templates" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Template\"}"`
	Tools          []WorkbenchTool         `json:"tools" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/WorkbenchTool\"}"`
	Model          WorkbenchModel          `json:"model" openapi:"{\"$ref\":\"#/components/schemas/WorkbenchModel\"}"`
}
type WorkbenchCase struct {
	Can_create_task   bool   `json:"can_create_task,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Id                string `json:"id" openapi:"{\"type\":\"string\"}"`
	Title             string `json:"title" openapi:"{\"type\":\"string\"}"`
	Contract_type     string `json:"contract_type" openapi:"{\"type\":\"string\"}"`
	Document_count    int64  `json:"document_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Pending_decisions int64  `json:"pending_decisions" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Latest_run        any    `json:"latest_run" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/RunSummary\"},{\"type\":\"null\"}]}"`
	Updated_at        string `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Business_line     string `json:"business_line" openapi:"{\"type\":\"string\"}"`
	Scenario          string `json:"scenario" openapi:"{\"type\":\"string\"}"`
	Business_status   string `json:"business_status" openapi:"{\"type\":\"string\"}"`
	Archived_at       any    `json:"archived_at" openapi:"{\"anyOf\":[{\"type\":\"string\",\"format\":\"date-time\"},{\"type\":\"null\"}]}"`
	Profile           any    `json:"profile" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/CaseProfile\"},{\"type\":\"null\"}]}"`
	Severity          any    `json:"severity" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Progress          any    `json:"progress" openapi:"{\"type\":[\"number\",\"null\"],\"minimum\":0,\"maximum\":100}"`
}
type WorkbenchDocument struct {
	Id                     string `json:"id" openapi:"{\"type\":\"string\"}"`
	Role                   string `json:"role" openapi:"{\"type\":\"string\",\"enum\":[\"procurement\",\"award\",\"draft_contract\",\"attachment\"]}"`
	Filename               string `json:"filename" openapi:"{\"type\":\"string\"}"`
	Media_type             string `json:"media_type" openapi:"{\"type\":\"string\"}"`
	Digest                 string `json:"digest" openapi:"{\"type\":\"string\"}"`
	Size                   int64  `json:"size" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Parse_status           string `json:"parse_status" openapi:"{\"type\":\"string\",\"enum\":[\"pending\",\"ready\",\"failed\"]}"`
	Parse_error            any    `json:"parse_error" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Created_at             string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Superseded             bool   `json:"superseded" openapi:"{\"type\":\"boolean\"}"`
	Coordinate_space       string `json:"coordinate_space,omitempty" openapi:"{\"type\":\"string\",\"const\":\"unicode_codepoint\"}"`
	Normalized_text_digest string `json:"normalized_text_digest,omitempty" openapi:"{\"type\":\"string\"}"`
	Parsed_document        any    `json:"parsed_document" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ParsedDocument\"},{\"type\":\"null\"}]}"`
	Case_title             string `json:"case_title" openapi:"{\"type\":\"string\"}"`
	Case_id                string `json:"case_id" openapi:"{\"type\":\"string\"}"`
}
type WorkbenchRemediation struct {
	Id                      string   `json:"id" openapi:"{\"type\":\"string\"}"`
	Case_id                 string   `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Run_id                  string   `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id              string   `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Finding_ids             []string `json:"finding_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Assignee_id             string   `json:"assignee_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Due_at                  string   `json:"due_at,omitempty" openapi:"{\"type\":\"string\"}"`
	Measure                 string   `json:"measure" openapi:"{\"type\":\"string\"}"`
	Status                  string   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"open\",\"submitted\",\"closed\",\"reopened\"]}"`
	Version                 int64    `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Document_ids            []string `json:"document_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Reason                  string   `json:"reason" openapi:"{\"type\":\"string\"}"`
	Verification_run_id     string   `json:"verification_run_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Verification_attempt_id string   `json:"verification_attempt_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Created_at              string   `json:"created_at" openapi:"{\"type\":\"string\"}"`
	Updated_at              string   `json:"updated_at" openapi:"{\"type\":\"string\"}"`
	Case_title              string   `json:"case_title" openapi:"{\"type\":\"string\"}"`
}
type WorkbenchReport struct {
	Id                     string `json:"id" openapi:"{\"type\":\"string\"}"`
	Version                int64  `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Run_id                 string `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id             string `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Input_hash             string `json:"input_hash" openapi:"{\"type\":\"string\"}"`
	Output_hash            string `json:"output_hash" openapi:"{\"type\":\"string\"}"`
	Decision_snapshot_hash string `json:"decision_snapshot_hash" openapi:"{\"type\":\"string\"}"`
	Digest                 string `json:"digest" openapi:"{\"type\":\"string\"}"`
	Created_at             string `json:"created_at" openapi:"{\"type\":\"string\"}"`
	Case_title             string `json:"case_title" openapi:"{\"type\":\"string\"}"`
	Case_id                string `json:"case_id" openapi:"{\"type\":\"string\"}"`
}
type WorkbenchMember struct {
	Id           string `json:"id" openapi:"{\"type\":\"string\"}"`
	Display_name string `json:"display_name" openapi:"{\"type\":\"string\"}"`
	Role         string `json:"role" openapi:"{\"type\":\"string\"}"`
	Tenant_id    string `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Org_id       string `json:"org_id" openapi:"{\"type\":\"string\"}"`
}
type Template struct {
	Id      string          `json:"id" openapi:"{\"type\":\"string\"}"`
	Name    string          `json:"name" openapi:"{\"type\":\"string\"}"`
	Version string          `json:"version" openapi:"{\"type\":\"string\"}"`
	Digest  string          `json:"digest" openapi:"{\"type\":\"string\"}"`
	Fields  []TemplateField `json:"fields" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/TemplateField\"}"`
}
type TemplateField struct {
	Key      string `json:"key" openapi:"{\"type\":\"string\"}"`
	Label    string `json:"label" openapi:"{\"type\":\"string\"}"`
	Required bool   `json:"required" openapi:"{\"type\":\"boolean\"}"`
	Type     string `json:"type" openapi:"{\"type\":\"string\"}"`
}
type TemplateList struct {
	Items []Template `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Template\"}"`
}
type CreateGenerationRequest struct {
	Template_id          string         `json:"template_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Fields               map[string]any `json:"fields" openapi:"{\"type\":\"object\",\"additionalProperties\":{\"type\":\"string\"}}"`
	Intent               string         `json:"intent,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"add\",\"replace\"],\"default\":\"add\"}"`
	Material_id          string         `json:"material_id,omitempty" openapi:"{\"type\":\"string\",\"description\":\"intent=replace 时必填\"}"`
	Replaces_document_id string         `json:"replaces_document_id,omitempty" openapi:"{\"type\":\"string\",\"description\":\"intent=replace 时的当前版本 CAS 目标\"}"`
}
type GenerationRecord struct {
	Id               string `json:"id" openapi:"{\"type\":\"string\"}"`
	Document_id      string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Material_id      string `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Version          int64  `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Replayed         bool   `json:"replayed" openapi:"{\"type\":\"boolean\"}"`
	Template_id      string `json:"template_id" openapi:"{\"type\":\"string\"}"`
	Template_version string `json:"template_version" openapi:"{\"type\":\"string\"}"`
	Digest           string `json:"digest" openapi:"{\"type\":\"string\"}"`
	Created_at       string `json:"created_at" openapi:"{\"type\":\"string\"}"`
}
type Remediation struct {
	Id                      string   `json:"id" openapi:"{\"type\":\"string\"}"`
	Case_id                 string   `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Run_id                  string   `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id              string   `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Finding_ids             []string `json:"finding_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Assignee_id             string   `json:"assignee_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Due_at                  string   `json:"due_at,omitempty" openapi:"{\"type\":\"string\"}"`
	Measure                 string   `json:"measure" openapi:"{\"type\":\"string\"}"`
	Status                  string   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"open\",\"submitted\",\"closed\",\"reopened\"]}"`
	Version                 int64    `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Document_ids            []string `json:"document_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Reason                  string   `json:"reason" openapi:"{\"type\":\"string\"}"`
	Verification_run_id     string   `json:"verification_run_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Verification_attempt_id string   `json:"verification_attempt_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Created_at              string   `json:"created_at" openapi:"{\"type\":\"string\"}"`
	Updated_at              string   `json:"updated_at" openapi:"{\"type\":\"string\"}"`
}
type RemediationInput struct {
	Run_id      string   `json:"run_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Attempt_id  string   `json:"attempt_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Finding_ids []string `json:"finding_ids" openapi:"{\"type\":\"array\",\"minItems\":1}" openapi_items:"{\"type\":\"string\"}"`
	Assignee_id string   `json:"assignee_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Due_at      string   `json:"due_at,omitempty" openapi:"{\"type\":\"string\"}"`
	Measure     string   `json:"measure" openapi:"{\"type\":\"string\",\"minLength\":1}"`
}
type RemediationSubmitInput struct {
	Document_ids     []string `json:"document_ids" openapi:"{\"type\":\"array\",\"minItems\":1}" openapi_items:"{\"type\":\"string\"}"`
	Reason           string   `json:"reason" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Expected_version int64    `json:"expected_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type RemediationVerifyInput struct {
	Run_id           string `json:"run_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Attempt_id       string `json:"attempt_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Reason           string `json:"reason" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Expected_version int64  `json:"expected_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type CreateReportRequest struct {
	Run_id     string `json:"run_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Attempt_id string `json:"attempt_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
}
type ReportRecord struct {
	Id                     string `json:"id" openapi:"{\"type\":\"string\"}"`
	Version                int64  `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Run_id                 string `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id             string `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Input_hash             string `json:"input_hash" openapi:"{\"type\":\"string\"}"`
	Output_hash            string `json:"output_hash" openapi:"{\"type\":\"string\"}"`
	Decision_snapshot_hash string `json:"decision_snapshot_hash" openapi:"{\"type\":\"string\"}"`
	Digest                 string `json:"digest" openapi:"{\"type\":\"string\"}"`
	Created_at             string `json:"created_at" openapi:"{\"type\":\"string\"}"`
}
type ReportDocument struct {
	Agent_review        any    `json:"agent_review,omitempty" openapi:"{\"oneOf\":[{\"type\":\"object\",\"additionalProperties\":true},{\"type\":\"null\"}],\"description\":\"同一次执行保存的五个方面的智能审查记录；较早的执行记录可能为 null。\"}"`
	Agent_review_bytes  string `json:"agent_review_bytes,omitempty" openapi:"{\"type\":\"string\",\"contentEncoding\":\"base64\",\"contentMediaType\":\"application/json\",\"description\":\"agent_review_sha256 所校验的原始投影字节，供精确复核。\"}"`
	Agent_review_sha256 string `json:"agent_review_sha256,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\",\"description\":\"agent_review_bytes 原始字节的 SHA-256；输出摘要使用同一值。\"}"`
}
type ArchiveRequest struct {
	Report_id string `json:"report_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Reason    string `json:"reason" openapi:"{\"type\":\"string\",\"minLength\":1}"`
}
type ArchiveRecord struct {
	Id          string `json:"id" openapi:"{\"type\":\"string\"}"`
	Case_id     string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Report_id   string `json:"report_id" openapi:"{\"type\":\"string\"}"`
	Reason      string `json:"reason" openapi:"{\"type\":\"string\"}"`
	Archived_at string `json:"archived_at" openapi:"{\"type\":\"string\"}"`
}
type EmptyObject struct {
}
type ID struct {
	Id string `json:"id" openapi:"{\"type\":\"string\"}"`
}
type Actor struct {
	Id           string `json:"id" openapi:"{\"type\":\"string\"}"`
	Display_name string `json:"display_name" openapi:"{\"type\":\"string\"}"`
	Role         string `json:"role" openapi:"{\"type\":\"string\",\"enum\":[\"admin\",\"reviewer\",\"clerk\",\"viewer\"]}"`
	Tenant_id    string `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Org_id       string `json:"org_id" openapi:"{\"type\":\"string\"}"`
}
type Session struct {
	Actor                  Actor         `json:"actor" openapi:"{\"$ref\":\"#/components/schemas/Actor\"}"`
	Audience               string        `json:"audience" openapi:"{\"type\":\"string\",\"enum\":[\"client\",\"admin\"]}"`
	Active_context         ActiveContext `json:"active_context" openapi:"{\"$ref\":\"#/components/schemas/ActiveContext\"}"`
	Csrf_token             string        `json:"csrf_token" openapi:"{\"type\":\"string\",\"minLength\":32,\"description\":\"Private unpredictable exact-session token. Never broadcast or put in URL.\"}"`
	Context_switch_enabled bool          `json:"context_switch_enabled" openapi:"{\"type\":\"boolean\"}"`
	Principal_id           *string       `json:"principal_id" openapi:"{\"type\":\"string\",\"nullable\":true,\"description\":\"Current authenticated PostgreSQL principal UUID; null only for local demo. Actor.id remains the legacy actor ID.\"}"`
}
type LoginRequest struct {
	Username string `json:"username" openapi:"{\"type\":\"string\"}"`
	Password string `json:"password" openapi:"{\"type\":\"string\",\"writeOnly\":true}"`
	Audience string `json:"audience,omitempty" openapi:"{\"type\":\"string\",\"enum\":[\"client\",\"admin\"],\"description\":\"缺省为 client；admin 需要当前平台管理权限。\"}"`
}
type CreateCaseRequest struct {
	Title         string `json:"title" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":120,\"description\":\"服务端去除首尾空白后按 Unicode 码点计数。\"}"`
	Contract_type string `json:"contract_type" openapi:"{\"type\":\"string\",\"pattern\":\"^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$\",\"description\":\"扩展合同类型须对应本租户已发布且生效的规则包；旧版未指定业务范围的类型仍可使用。\"}"`
	Business_line string `json:"business_line,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$\",\"description\":\"与 scenario 同时提供；两者均省略时记录为 unspecified，不从客户端推断租户。扩展范围须有本租户已发布且生效的规则包。\"}"`
	Scenario      string `json:"scenario,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$\",\"description\":\"与 business_line 同时提供；云服务使用 service 合同类型，扩展场景须在已发布规则包中声明。\"}"`
}
type AddMemberRequest struct {
	Actor_id string `json:"actor_id" openapi:"{\"type\":\"string\"}"`
}
type DecisionRequest struct {
	Action           string `json:"action" openapi:"{\"type\":\"string\",\"enum\":[\"confirm_risk\",\"false_positive\",\"insufficient_evidence\",\"accept_residual\"]}"`
	Reason           string `json:"reason" openapi:"{\"type\":\"string\",\"minLength\":2,\"maxLength\":1000,\"description\":\"服务端去除首尾空白后按 Unicode 码点计数。\"}"`
	Expected_version int64  `json:"expected_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type Error struct {
	Error ErrorError `json:"error" openapi:"{\"type\":\"object\",\"required\":[\"code\",\"message\",\"retryable\"],\"additionalProperties\":false}"`
}
type RunSummary struct {
	Id         string `json:"id" openapi:"{\"type\":\"string\"}"`
	State      string `json:"state" openapi:"{\"type\":\"string\",\"description\":\"持久任务受理后，审查状态为 accepted；任务在 worker_jobs 中以 queued 排队，Worker 领取后变为 running。随后状态反映执行结果：completed、awaiting_human、partial、failed、cancelled 或 interrupted。租约过期后，其他 Worker 可以用新代次接管，旧代次的结果会被拒绝；这不代表跨故障域高可用或灾难恢复。\"}"`
	Updated_at string `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type CaseSummary struct {
	Id                string `json:"id" openapi:"{\"type\":\"string\"}"`
	Title             string `json:"title" openapi:"{\"type\":\"string\"}"`
	Contract_type     string `json:"contract_type" openapi:"{\"type\":\"string\"}"`
	Document_count    int64  `json:"document_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Pending_decisions int64  `json:"pending_decisions" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Latest_run        any    `json:"latest_run" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/RunSummary\"},{\"type\":\"null\"}]}"`
	Updated_at        string `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Business_line     string `json:"business_line" openapi:"{\"type\":\"string\"}"`
	Scenario          string `json:"scenario" openapi:"{\"type\":\"string\"}"`
	Business_status   string `json:"business_status" openapi:"{\"type\":\"string\"}"`
	Archived_at       any    `json:"archived_at" openapi:"{\"anyOf\":[{\"type\":\"string\",\"format\":\"date-time\"},{\"type\":\"null\"}]}"`
}
type CaseList struct {
	Items []CaseSummary `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/CaseSummary\"}"`
}
type ParsedDocument struct {
	Id                    string `json:"id" openapi:"{\"type\":\"string\"}"`
	Document_id           string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Original_digest       string `json:"original_digest" openapi:"{\"type\":\"string\"}"`
	Parser_id             string `json:"parser_id" openapi:"{\"type\":\"string\"}"`
	Parser_version        string `json:"parser_version" openapi:"{\"type\":\"string\"}"`
	Config_hash           string `json:"config_hash" openapi:"{\"type\":\"string\"}"`
	Normalization_version string `json:"normalization_version" openapi:"{\"type\":\"string\"}"`
	Normalized_digest     any    `json:"normalized_digest" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Ir_digest             any    `json:"ir_digest" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Artifact_digest       string `json:"artifact_digest" openapi:"{\"type\":\"string\"}"`
	Created_at            string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Status                string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"ready\",\"failed\"]}"`
}
type Document struct {
	Id                     string `json:"id" openapi:"{\"type\":\"string\"}"`
	Role                   string `json:"role" openapi:"{\"type\":\"string\",\"enum\":[\"procurement\",\"award\",\"draft_contract\",\"attachment\"]}"`
	Filename               string `json:"filename" openapi:"{\"type\":\"string\"}"`
	Media_type             string `json:"media_type" openapi:"{\"type\":\"string\"}"`
	Digest                 string `json:"digest" openapi:"{\"type\":\"string\"}"`
	Size                   int64  `json:"size" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Parse_status           string `json:"parse_status" openapi:"{\"type\":\"string\",\"enum\":[\"pending\",\"ready\",\"failed\"]}"`
	Parse_error            any    `json:"parse_error" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Created_at             string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Superseded             bool   `json:"superseded" openapi:"{\"type\":\"boolean\"}"`
	Coordinate_space       string `json:"coordinate_space,omitempty" openapi:"{\"type\":\"string\",\"const\":\"unicode_codepoint\"}"`
	Normalized_text_digest string `json:"normalized_text_digest,omitempty" openapi:"{\"type\":\"string\"}"`
	Parsed_document        any    `json:"parsed_document" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ParsedDocument\"},{\"type\":\"null\"}]}"`
}
type TextOffset struct {
	Start int64 `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End   int64 `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type Clause struct {
	Id          string     `json:"id" openapi:"{\"type\":\"string\"}"`
	Heading     string     `json:"heading" openapi:"{\"type\":\"string\"}"`
	Text_offset TextOffset `json:"text_offset" openapi:"{\"$ref\":\"#/components/schemas/TextOffset\"}"`
	Page        any        `json:"page" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":1},{\"type\":\"null\"}]}"`
}
type ReviewDocument struct {
	Id                 string   `json:"id" openapi:"{\"type\":\"string\"}"`
	Role               string   `json:"role" openapi:"{\"type\":\"string\",\"enum\":[\"procurement\",\"award\",\"draft_contract\",\"attachment\"]}"`
	Filename           string   `json:"filename" openapi:"{\"type\":\"string\"}"`
	Media_type         string   `json:"media_type" openapi:"{\"type\":\"string\"}"`
	Size               int64    `json:"size" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Parse_status       string   `json:"parse_status" openapi:"{\"type\":\"string\",\"enum\":[\"pending\",\"ready\",\"failed\"]}"`
	Parse_error        any      `json:"parse_error" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Created_at         string   `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Superseded         bool     `json:"superseded" openapi:"{\"type\":\"boolean\"}"`
	Coordinate_space   string   `json:"coordinate_space,omitempty" openapi:"{\"type\":\"string\",\"const\":\"unicode_codepoint\"}"`
	Text               any      `json:"text" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Clauses            []Clause `json:"clauses" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Clause\"}"`
	Parsed_document    any      `json:"parsed_document" openapi:"{\"type\":\"null\"}"`
	Parsed_document_id any      `json:"parsed_document_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
}
type FindingCounts struct {
	Total        int64 `json:"total" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Undecided    int64 `json:"undecided" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Conflict     int64 `json:"conflict" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Insufficient int64 `json:"insufficient" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type Activity struct {
	Id      string `json:"id" openapi:"{\"type\":\"string\"}"`
	At      string `json:"at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Summary string `json:"summary" openapi:"{\"type\":\"string\"}"`
}
type CaseDetail struct {
	Id                                       string                      `json:"id" openapi:"{\"type\":\"string\"}"`
	Materials                                []MaterialProjection        `json:"materials,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/MaterialProjection\"}"`
	Material_versions                        []MaterialVersionProjection `json:"material_versions,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/MaterialVersionProjection\"}"`
	Material_selection                       MaterialSelectionProjection `json:"material_selection,omitempty" openapi:"{\"$ref\":\"#/components/schemas/MaterialSelectionProjection\"}"`
	Business_line                            string                      `json:"business_line" openapi:"{\"type\":\"string\"}"`
	Scenario                                 string                      `json:"scenario" openapi:"{\"type\":\"string\"}"`
	Business_status                          string                      `json:"business_status" openapi:"{\"type\":\"string\"}"`
	Archived_at                              any                         `json:"archived_at" openapi:"{\"anyOf\":[{\"type\":\"string\",\"format\":\"date-time\"},{\"type\":\"null\"}]}"`
	Generations                              []GenerationRecord          `json:"generations" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/GenerationRecord\"}"`
	Remediations                             []Remediation               `json:"remediations" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Remediation\"}"`
	Reports                                  []ReportRecord              `json:"reports" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ReportRecord\"}"`
	Archives                                 []ArchiveRecord             `json:"archives" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ArchiveRecord\"}"`
	Title                                    string                      `json:"title" openapi:"{\"type\":\"string\"}"`
	Contract_type                            string                      `json:"contract_type" openapi:"{\"type\":\"string\"}"`
	Document_count                           int64                       `json:"document_count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Pending_decisions                        int64                       `json:"pending_decisions" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Latest_run                               any                         `json:"latest_run" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/RunSummary\"},{\"type\":\"null\"}]}"`
	Updated_at                               string                      `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Created_at                               string                      `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Documents                                []Document                  `json:"documents" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Document\"}"`
	Revision_pending                         bool                        `json:"revision_pending" openapi:"{\"type\":\"boolean\"}"`
	Configuration_pending                    bool                        `json:"configuration_pending" openapi:"{\"type\":\"boolean\"}"`
	Subject_context_changed_after_latest_run bool                        `json:"subject_context_changed_after_latest_run" openapi:"{\"type\":\"boolean\",\"description\":\"当前主体上下文与最近一次审查冻结的主体上下文不同，需要发起新审查。\"}"`
	Finding_counts                           any                         `json:"finding_counts" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/FindingCounts\"},{\"type\":\"null\"}]}"`
	Activity                                 []Activity                  `json:"activity" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Activity\"}"`
	Members                                  []Actor                     `json:"members" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Actor\"}"`
}
type Anchor struct {
	Id                  string `json:"id" openapi:"{\"type\":\"string\"}"`
	Document_version_id string `json:"document_version_id" openapi:"{\"type\":\"string\"}"`
	Quote               string `json:"quote" openapi:"{\"type\":\"string\"}"`
	Status              string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"located\",\"missing\",\"unlocatable\"]}"`
	Page                any    `json:"page" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":1},{\"type\":\"null\"}]}"`
	Text_offset         any    `json:"text_offset" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/TextOffset\"},{\"type\":\"null\"}]}"`
	Bbox                any    `json:"bbox" openapi:"{\"type\":\"null\",\"description\":\"当前 API 不输出推测的 PDF 边界框。\"}"`
	Element_id          any    `json:"element_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Mismatch_reason     any    `json:"mismatch_reason" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Parsed_document_id  any    `json:"parsed_document_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
}
type Decision struct {
	Id              string `json:"id" openapi:"{\"type\":\"string\"}"`
	Action          string `json:"action" openapi:"{\"type\":\"string\"}"`
	Reason          string `json:"reason" openapi:"{\"type\":\"string\"}"`
	Actor           string `json:"actor" openapi:"{\"type\":\"string\"}"`
	Decided_at      string `json:"decided_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Old_disposition string `json:"old_disposition" openapi:"{\"type\":\"string\"}"`
	New_disposition string `json:"new_disposition" openapi:"{\"type\":\"string\"}"`
}
type PriorDecision struct {
	Id         string `json:"id" openapi:"{\"type\":\"string\"}"`
	Action     string `json:"action" openapi:"{\"type\":\"string\"}"`
	Reason     string `json:"reason" openapi:"{\"type\":\"string\"}"`
	Actor      string `json:"actor" openapi:"{\"type\":\"string\"}"`
	Decided_at string `json:"decided_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type MaterialProjection struct {
	Material_id         string `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Role                string `json:"role" openapi:"{\"type\":\"string\",\"enum\":[\"procurement\",\"award\",\"draft_contract\",\"attachment\"]}"`
	Purpose             string `json:"purpose" openapi:"{\"type\":\"string\",\"enum\":[\"primary_contract\",\"supplement\",\"procurement\",\"award\",\"acceptance\",\"invoice\",\"correspondence\",\"other\"]}"`
	Legacy_status       string `json:"legacy_status" openapi:"{\"type\":\"string\",\"enum\":[\"native\",\"pending_confirmation\",\"confirmed\"]}"`
	Legacy_selected     bool   `json:"legacy_selected" openapi:"{\"type\":\"boolean\"}"`
	Selected            bool   `json:"selected" openapi:"{\"type\":\"boolean\"}"`
	Current_document_id string `json:"current_document_id" openapi:"{\"type\":\"string\"}"`
	Current_version     int64  `json:"current_version" openapi:"{\"type\":\"integer\"}"`
}
type MaterialSelectionInput struct {
	Version      int64    `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Document_ids []string `json:"document_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\",\"minLength\":1}"`
}
type MaterialVersionProjection struct {
	Material_id          string `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Version              int64  `json:"version" openapi:"{\"type\":\"integer\"}"`
	Document_id          string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Previous_document_id any    `json:"previous_document_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Created_at           string `json:"created_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type MaterialSelectionProjection struct {
	Version                int64    `json:"version" openapi:"{\"type\":\"integer\"}"`
	Status                 string   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"pending_confirmation\",\"confirmed\",\"not_applicable\"]}"`
	Document_ids           []string `json:"document_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Candidate_document_ids []string `json:"candidate_document_ids" openapi:"{\"type\":\"array\",\"description\":\"仅pending_confirmation时提供的服务端候选；不能视为已确认材料。\"}" openapi_items:"{\"type\":\"string\"}"`
	Primary_material_id    any      `json:"primary_material_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
}
type Finding struct {
	Severity               any        `json:"severity,omitempty" openapi:"{\"type\":[\"string\",\"null\"],\"enum\":[\"high\",\"medium\",\"low\",\"info\",null],\"description\":\"根据本次审查保存的适用制度独立分级，不会从规则比较结果或技术状态自动推导。历史记录未分级时为 null。\"}"`
	Severity_source        any        `json:"severity_source,omitempty" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/SeveritySource\"},{\"type\":\"null\"}]}"`
	Id                     string     `json:"id" openapi:"{\"type\":\"string\"}"`
	Origin                 string     `json:"origin" openapi:"{\"type\":\"string\"}"`
	Domain                 string     `json:"domain" openapi:"{\"type\":\"string\",\"description\":\"现有有限核验规则域，不等同目标产品的五个审计视角。\"}"`
	Claim                  string     `json:"claim" openapi:"{\"type\":\"string\"}"`
	Relation               string     `json:"relation" openapi:"{\"type\":\"string\",\"enum\":[\"compatible\",\"conflict\",\"insufficient\",\"not_applicable\"]}"`
	Confidence             any        `json:"confidence" openapi:"{\"type\":\"null\"}"`
	Evidence               []Anchor   `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Anchor\"}"`
	Disposition_suggestion any        `json:"disposition_suggestion" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Unverified_reason      any        `json:"unverified_reason" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Version                int64      `json:"version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Decisions              []Decision `json:"decisions" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Decision\"}"`
	Revision_mark          any        `json:"revision_mark" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
}
type Run struct {
	Id              string `json:"id" openapi:"{\"type\":\"string\"}"`
	State           string `json:"state" openapi:"{\"type\":\"string\",\"description\":\"持久任务受理后，审查状态为 accepted；任务在 worker_jobs 中以 queued 排队，Worker 领取后变为 running。随后状态反映执行结果：completed、awaiting_human、partial、failed、cancelled 或 interrupted。租约过期后，其他 Worker 可以用新代次接管，旧代次的结果会被拒绝；这不代表跨故障域高可用或灾难恢复。\"}"`
	Updated_at      string `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Case_id         string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Started_at      string `json:"started_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Partial_reason  any    `json:"partial_reason" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Failure_message any    `json:"failure_message" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
}
type Snapshot struct {
	Adopted_tenant_config any                 `json:"adopted_tenant_config" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/AdoptedTenantConfigIdentity\"},{\"type\":\"null\"}],\"description\":\"该次新运行实际冻结的租户配置版本；历史未记录时为null，不从当前配置推断。\"}"`
	Document_ids          []string            `json:"document_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Rule_set_hash         string              `json:"rule_set_hash" openapi:"{\"type\":\"string\"}"`
	Playbook_hash         string              `json:"playbook_hash" openapi:"{\"type\":\"string\"}"`
	Model_ref             any                 `json:"model_ref" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Comparator_ref        any                 `json:"comparator_ref" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Review_scope          ReviewScopeIdentity `json:"review_scope,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ReviewScopeIdentity\"}"`
	Governed_knowledge    any                 `json:"governed_knowledge" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ReviewKnowledgeSnapshot\"},{\"type\":\"null\"}]}"`
}
type Evaluation struct {
	Id            string   `json:"id" openapi:"{\"type\":\"string\"}"`
	Domain        string   `json:"domain" openapi:"{\"type\":\"string\"}"`
	Relation      string   `json:"relation" openapi:"{\"type\":\"string\",\"enum\":[\"compatible\",\"conflict\",\"insufficient\",\"not_applicable\"]}"`
	Rule_version  string   `json:"rule_version" openapi:"{\"type\":\"string\"}"`
	Reason        string   `json:"reason" openapi:"{\"type\":\"string\"}"`
	Finding_id    any      `json:"finding_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Rule_id       string   `json:"rule_id" openapi:"{\"type\":\"string\"}"`
	Capability_id string   `json:"capability_id" openapi:"{\"type\":\"string\"}"`
	Reason_code   string   `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Fact_ids      []string `json:"fact_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Object_refs   []string `json:"object_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type AuditEvent struct {
	Event_id        string   `json:"event_id" openapi:"{\"type\":\"string\"}"`
	Run_id          string   `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Actor           string   `json:"actor" openapi:"{\"type\":\"string\"}"`
	Seq             int64    `json:"seq" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Event_type      string   `json:"event_type" openapi:"{\"type\":\"string\"}"`
	Prev_hash       string   `json:"prev_hash" openapi:"{\"type\":\"string\"}"`
	Hash            string   `json:"hash" openapi:"{\"type\":\"string\"}"`
	Correlation_ids []string `json:"correlation_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type Revision struct {
	Mark                string   `json:"mark" openapi:"{\"type\":\"string\",\"enum\":[\"新增\",\"持续\",\"消除\",\"未能判断\"]}"`
	Finding_id          any      `json:"finding_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Previous_finding_id any      `json:"previous_finding_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Domain              string   `json:"domain" openapi:"{\"type\":\"string\"}"`
	Relation            string   `json:"relation" openapi:"{\"type\":\"string\"}"`
	Claim               string   `json:"claim" openapi:"{\"type\":\"string\"}"`
	Evidence            []Anchor `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Anchor\"}"`
	Prior_decision      any      `json:"prior_decision" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/PriorDecision\"},{\"type\":\"null\"}]}"`
}
type Workspace struct {
	Run                        Run                         `json:"run" openapi:"{\"$ref\":\"#/components/schemas/Run\"}"`
	Documents                  []ReviewDocument            `json:"documents" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ReviewDocument\"}"`
	Findings                   []Finding                   `json:"findings" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Finding\"}"`
	Evaluations                []Evaluation                `json:"evaluations" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Evaluation\"}"`
	Snapshot                   BusinessSnapshot            `json:"snapshot" openapi:"{\"$ref\":\"#/components/schemas/BusinessSnapshot\"}"`
	Legal_authorities          FrozenLegalReviewProjection `json:"legal_authorities,omitempty" openapi:"{\"$ref\":\"#/components/schemas/FrozenLegalReviewProjection\"}"`
	Audit_events               []AuditEvent                `json:"audit_events" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AuditEvent\"}"`
	Previous_run_id            any                         `json:"previous_run_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Revision_comparison_reason any                         `json:"revision_comparison_reason" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Revisions                  []Revision                  `json:"revisions" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Revision\"}"`
	Execution                  any                         `json:"execution" openapi:"{\"type\":\"null\"}"`
	Review_attempts            any                         `json:"review_attempts" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/BusinessAttempts\"},{\"type\":\"null\"}]}"`
	Agent_review               any                         `json:"agent_review" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/BusinessAgentReview\"},{\"type\":\"null\"}]}"`
	Business_events            []BusinessAuditEvent        `json:"business_events" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/BusinessAuditEvent\"}"`
}
type BusinessSnapshot struct {
	Document_ids          []string            `json:"document_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Business_at           any                 `json:"business_at,omitempty" openapi:"{\"type\":[\"string\",\"null\"],\"format\":\"date-time\",\"description\":\"该次审查尝试冻结的业务时间；历史记录未保存时为null，不取当前查询时间。\"}"`
	Review_scope          ReviewScopeIdentity `json:"review_scope,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ReviewScopeIdentity\"}"`
	Adopted_tenant_config any                 `json:"adopted_tenant_config" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/AdoptedTenantConfigIdentity\"},{\"type\":\"null\"}],\"description\":\"该次新运行实际冻结的租户配置版本；历史未记录时为null，不从当前配置推断。\"}"`
}
type AdoptedTenantConfigIdentity struct {
	Version_id string `json:"version_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Digest     string `json:"digest" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
}
type BusinessAttempt struct {
	Id                      string              `json:"id" openapi:"{\"type\":\"string\"}"`
	Run_id                  string              `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Ordinal                 int64               `json:"ordinal" openapi:"{\"type\":\"integer\"}"`
	Retry_of_attempt_id     any                 `json:"retry_of_attempt_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	State                   string              `json:"state" openapi:"{\"type\":\"string\"}"`
	Accepted_at             string              `json:"accepted_at" openapi:"{\"type\":\"string\"}"`
	Started_at              any                 `json:"started_at" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Finished_at             any                 `json:"finished_at" openapi:"{\"type\":[\"string\",\"null\"]}"`
	History_kind            string              `json:"history_kind" openapi:"{\"type\":\"string\"}"`
	Parsed_document_ids     []string            `json:"parsed_document_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Review_scope            ReviewScopeIdentity `json:"review_scope,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ReviewScopeIdentity\"}"`
	Frozen_result_available bool                `json:"frozen_result_available" openapi:"{\"type\":\"boolean\",\"description\":\"冻结输入和结果均已保存，可继续校验来源后参与复审对照。\"}"`
}
type BusinessAttempts struct {
	Attempt  any               `json:"attempt" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/BusinessAttempt\"},{\"type\":\"null\"}]}"`
	Attempts []BusinessAttempt `json:"attempts" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/BusinessAttempt\"}"`
}
type BusinessAuditEvent struct {
	Event_id        string   `json:"event_id" openapi:"{\"type\":\"string\"}"`
	Run_id          string   `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Actor           string   `json:"actor" openapi:"{\"type\":\"string\"}"`
	Seq             int64    `json:"seq" openapi:"{\"type\":\"integer\"}"`
	Event_type      string   `json:"event_type" openapi:"{\"type\":\"string\"}"`
	Correlation_ids []string `json:"correlation_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
}
type BusinessAgentScope struct {
	Domain         string   `json:"domain" openapi:"{\"type\":\"string\"}"`
	Capability_ids []string `json:"capability_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Source_ids     []string `json:"source_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
}
type BusinessAgentCapability struct {
	Capability_id            string   `json:"capability_id" openapi:"{\"type\":\"string\"}"`
	Mandatory                bool     `json:"mandatory" openapi:"{\"type\":\"boolean\"}"`
	Status                   string   `json:"status" openapi:"{\"type\":\"string\"}"`
	Relation                 any      `json:"relation" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Reason_code              string   `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Evidence_ids             []string `json:"evidence_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Canonical_evaluation_ids []string `json:"canonical_evaluation_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
}
type BusinessAgentDomain struct {
	Domain       string                    `json:"domain" openapi:"{\"type\":\"string\"}"`
	Status       string                    `json:"status" openapi:"{\"type\":\"string\"}"`
	Capabilities []BusinessAgentCapability `json:"capabilities" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/BusinessAgentCapability\"}"`
}
type BusinessAgentGap struct {
	Gap_id        string   `json:"gap_id" openapi:"{\"type\":\"string\"}"`
	Domain        string   `json:"domain" openapi:"{\"type\":\"string\"}"`
	Capability_id string   `json:"capability_id" openapi:"{\"type\":\"string\"}"`
	Mandatory     bool     `json:"mandatory" openapi:"{\"type\":\"boolean\"}"`
	Status        string   `json:"status" openapi:"{\"type\":\"string\"}"`
	Reason_code   string   `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Evidence_ids  []string `json:"evidence_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
}
type BusinessEvidenceAnchor struct {
	Id                 string `json:"id" openapi:"{\"type\":\"string\"}"`
	Document_id        string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id string `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
	Quote              string `json:"quote" openapi:"{\"type\":\"string\"}"`
	Status             string `json:"status" openapi:"{\"type\":\"string\"}"`
	Start              int64  `json:"start" openapi:"{\"type\":\"integer\"}"`
	End                int64  `json:"end" openapi:"{\"type\":\"integer\"}"`
	Reason             any    `json:"reason" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Page               any    `json:"page" openapi:"{\"type\":[\"integer\",\"null\"]}"`
	Element_id         any    `json:"element_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
}
type BusinessAgentEvidence struct {
	Evidence_id   string                 `json:"evidence_id" openapi:"{\"type\":\"string\"}"`
	Semantic_type string                 `json:"semantic_type" openapi:"{\"type\":\"string\"}"`
	Value         string                 `json:"value" openapi:"{\"type\":\"string\"}"`
	Unit          any                    `json:"unit" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Currency      any                    `json:"currency" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Verification  string                 `json:"verification" openapi:"{\"type\":\"string\"}"`
	Anchor        BusinessEvidenceAnchor `json:"anchor" openapi:"{\"$ref\":\"#/components/schemas/BusinessEvidenceAnchor\"}"`
}
type BusinessAgentReview struct {
	Attempt_id         any                         `json:"attempt_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Status             any                         `json:"status" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Rulepack           BusinessAgentReviewRulepack `json:"rulepack" openapi:"{\"type\":[\"object\",\"null\"],\"additionalProperties\":false,\"required\":[\"id\",\"version\"]}"`
	Scope              []BusinessAgentScope        `json:"scope" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/BusinessAgentScope\"}"`
	Domains            []BusinessAgentDomain       `json:"domains" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/BusinessAgentDomain\"}"`
	Gaps               []BusinessAgentGap          `json:"gaps" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/BusinessAgentGap\"}"`
	Evidence           []BusinessAgentEvidence     `json:"evidence" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/BusinessAgentEvidence\"}"`
	Unresolved_gap_ids []string                    `json:"unresolved_gap_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Observations       []BusinessAgentObservation  `json:"observations" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"$ref\":\"#/components/schemas/BusinessAgentObservation\"}"`
}
type BusinessAgentObservation struct {
	Round                int64    `json:"round" openapi:"{\"type\":\"integer\"}"`
	Call_id              string   `json:"call_id" openapi:"{\"type\":\"string\"}"`
	Gap_id               string   `json:"gap_id" openapi:"{\"type\":\"string\"}"`
	Tool_id              string   `json:"tool_id" openapi:"{\"type\":\"string\"}"`
	Reported_relation    string   `json:"reported_relation" openapi:"{\"type\":\"string\"}"`
	Reason_code          string   `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Summary              string   `json:"summary" openapi:"{\"type\":\"string\"}"`
	Claim_status         string   `json:"claim_status" openapi:"{\"type\":\"string\"}"`
	Fact_ids             []string `json:"fact_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Located_evidence_ids []string `json:"located_evidence_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Truncated            bool     `json:"truncated" openapi:"{\"type\":\"boolean\"}"`
}
type Settings struct {
	Model            SettingsModel          `json:"model" openapi:"{\"type\":\"object\",\"required\":[\"status\",\"label\"],\"additionalProperties\":false}"`
	Queue            SettingsQueue          `json:"queue" openapi:"{\"type\":\"object\",\"required\":[\"depth\",\"oldest_wait_ms\"],\"additionalProperties\":false}"`
	Redaction_policy string                 `json:"redaction_policy" openapi:"{\"type\":\"string\"}"`
	Users            []Actor                `json:"users" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/Actor\"}"`
	Operator_audit   SettingsOperator_audit `json:"operator_audit" openapi:"{\"type\":\"object\",\"required\":[\"enabled\",\"connectors\"],\"additionalProperties\":false}"`
	Execution        SettingsExecution      `json:"execution,omitempty" openapi:"{\"type\":\"object\",\"required\":[\"total_attempts\",\"running_attempts\",\"completed_attempts\",\"partial_attempts\",\"failed_attempts\",\"cancelled_attempts\",\"model_calls\",\"unknown_usage_calls\",\"queue_depth\"],\"additionalProperties\":false}"`
}
type StreamEvent struct {
	Seq   int64  `json:"seq" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Type  string `json:"type" openapi:"{\"type\":\"string\",\"enum\":[\"state\",\"decision\"]}"`
	State string `json:"state" openapi:"{\"type\":\"string\"}"`
}
type Health struct {
	Status string `json:"status" openapi:"{\"type\":\"string\",\"const\":\"ok\"}"`
}
type OpenAPIDocument struct {
	Openapi    string `json:"openapi" openapi:"{\"type\":\"string\",\"const\":\"3.1.1\"}"`
	Info       any    `json:"info" openapi:"{\"type\":\"object\"}"`
	Paths      any    `json:"paths" openapi:"{\"type\":\"object\"}"`
	Components any    `json:"components" openapi:"{\"type\":\"object\"}"`
}
type ReviewAccepted struct {
	Id         string `json:"id" openapi:"{\"type\":\"string\"}"`
	Attempt_id any    `json:"attempt_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
}
type NewReviewJobAccepted struct {
	Id         string `json:"id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Attempt_id string `json:"attempt_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Job_id     string `json:"job_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
}
type ExecutionUsage struct {
	Input_tokens    any      `json:"input_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}"`
	Output_tokens   any      `json:"output_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}"`
	Total_tokens    any      `json:"total_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}"`
	Calls           int64    `json:"calls" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Unknown_calls   int64    `json:"unknown_calls" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Sources         []string `json:"sources" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Known_subtotals any      `json:"known_subtotals" openapi:"{\"anyOf\":[{\"type\":\"object\",\"properties\":{\"input_tokens\":{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]},\"output_tokens\":{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]},\"total_tokens\":{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}},\"required\":[\"input_tokens\",\"output_tokens\",\"total_tokens\"],\"additionalProperties\":false},{\"type\":\"null\"}]}"`
	Cost            any      `json:"cost" openapi:"{\"type\":\"null\"}"`
}
type ReviewAttempt struct {
	Review_scope        ReviewScopeIdentity `json:"review_scope,omitempty" openapi:"{\"$ref\":\"#/components/schemas/ReviewScopeIdentity\"}"`
	Id                  string              `json:"id" openapi:"{\"type\":\"string\"}"`
	Run_id              string              `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Actor_id            string              `json:"actor_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Ordinal             int64               `json:"ordinal" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Retry_of_attempt_id any                 `json:"retry_of_attempt_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	State               string              `json:"state" openapi:"{\"type\":\"string\"}"`
	Accepted_at         string              `json:"accepted_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Started_at          any                 `json:"started_at" openapi:"{\"anyOf\":[{\"type\":\"string\",\"format\":\"date-time\"},{\"type\":\"null\"}]}"`
	Finished_at         any                 `json:"finished_at" openapi:"{\"anyOf\":[{\"type\":\"string\",\"format\":\"date-time\"},{\"type\":\"null\"}]}"`
	Input_hash          any                 `json:"input_hash" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Output_hash         any                 `json:"output_hash" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Error_code          any                 `json:"error_code" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	History_kind        string              `json:"history_kind" openapi:"{\"type\":\"string\"}"`
	Parsed_document_ids []string            `json:"parsed_document_ids" openapi:"{\"type\":\"array\",\"description\":\"本次执行保存的输入解析结果 ID，不包含版本对照额外使用的材料；尚未准备或较早来源信息不足时为空。\"}" openapi_items:"{\"type\":\"string\"}"`
}
type ExecutionCallUsage struct {
	Source        string `json:"source" openapi:"{\"type\":\"string\"}"`
	Input_tokens  any    `json:"input_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}"`
	Output_tokens any    `json:"output_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}"`
	Total_tokens  any    `json:"total_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}"`
}
type ExecutionEvent struct {
	Seq            int64  `json:"seq" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Attempt_id     string `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Span_id        any    `json:"span_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Parent_span_id any    `json:"parent_span_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Node_kind      string `json:"node_kind" openapi:"{\"type\":\"string\"}"`
	Phase          string `json:"phase" openapi:"{\"type\":\"string\"}"`
	Event_type     string `json:"event_type" openapi:"{\"type\":\"string\"}"`
	At             string `json:"at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Recorded_at    string `json:"recorded_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Event_key      string `json:"event_key,omitempty" openapi:"{\"type\":\"string\"}"`
	Generation     int64  `json:"generation,omitempty" openapi:"{\"type\":\"integer\"}"`
	Duration_ms    any    `json:"duration_ms" openapi:"{\"anyOf\":[{\"type\":\"number\",\"minimum\":0},{\"type\":\"null\"}]}"`
	Status         string `json:"status" openapi:"{\"type\":\"string\"}"`
	Error_code     any    `json:"error_code" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Model_ref      any    `json:"model_ref" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Usage          any    `json:"usage" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ExecutionCallUsage\"},{\"type\":\"null\"}]}"`
	Trace          any    `json:"trace,omitempty" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/ModelCallTrace\"},{\"type\":\"null\"}]}"`
}
type ModelCallTrace struct {
	Requested_model    string                  `json:"requested_model" openapi:"{\"type\":\"string\"}"`
	Reported_model     string                  `json:"reported_model,omitempty" openapi:"{\"type\":\"string\"}"`
	Status             string                  `json:"status" openapi:"{\"type\":\"string\"}"`
	Error_code         string                  `json:"error_code,omitempty" openapi:"{\"type\":\"string\"}"`
	Started_at         string                  `json:"started_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Finished_at        string                  `json:"finished_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Request_sha256     string                  `json:"request_sha256" openapi:"{\"type\":\"string\"}"`
	Request_bytes      int64                   `json:"request_bytes" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Request_messages   []ModelCallTraceMessage `json:"request_messages" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ModelCallTraceMessage\"}"`
	Request_truncated  bool                    `json:"request_truncated" openapi:"{\"type\":\"boolean\"}"`
	Response_sha256    string                  `json:"response_sha256,omitempty" openapi:"{\"type\":\"string\"}"`
	Response_bytes     int64                   `json:"response_bytes" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Response_text      string                  `json:"response_text,omitempty" openapi:"{\"type\":\"string\"}"`
	Response_truncated bool                    `json:"response_truncated" openapi:"{\"type\":\"boolean\"}"`
}
type ModelCallTraceMessage struct {
	Role      string `json:"role" openapi:"{\"type\":\"string\"}"`
	Content   string `json:"content" openapi:"{\"type\":\"string\"}"`
	Truncated bool   `json:"truncated" openapi:"{\"type\":\"boolean\"}"`
}
type Execution struct {
	Schema_version            string           `json:"schema_version" openapi:"{\"type\":\"string\",\"const\":\"audit-execution/v1\"}"`
	Attempt                   ReviewAttempt    `json:"attempt" openapi:"{\"$ref\":\"#/components/schemas/ReviewAttempt\"}"`
	Attempts                  []ReviewAttempt  `json:"attempts" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ReviewAttempt\"}"`
	Events                    []ExecutionEvent `json:"events" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/ExecutionEvent\"}"`
	Usage                     ExecutionUsage   `json:"usage" openapi:"{\"$ref\":\"#/components/schemas/ExecutionUsage\"}"`
	All_attempts_usage        ExecutionUsage   `json:"all_attempts_usage" openapi:"{\"$ref\":\"#/components/schemas/ExecutionUsage\"}"`
	First_verified_finding_ms any              `json:"first_verified_finding_ms" openapi:"{\"anyOf\":[{\"type\":\"number\",\"minimum\":0},{\"type\":\"null\"}]}"`
	Duration_ms               any              `json:"duration_ms" openapi:"{\"anyOf\":[{\"type\":\"number\",\"minimum\":0},{\"type\":\"null\"}]}"`
	Queue_wait_ms             any              `json:"queue_wait_ms" openapi:"{\"type\":\"null\"}"`
	Agent_review              any              `json:"agent_review" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/AgentReview\"},{\"type\":\"null\"}]}"`
}
type RulePackSummary struct {
	Id                      string   `json:"id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Version                 string   `json:"version" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Tenant_id               string   `json:"tenant_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Status                  string   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"draft\",\"validated\",\"approved\",\"published\"]}"`
	Revision                int64    `json:"revision" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Content_hash            string   `json:"content_hash" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Snapshot_sha256         string   `json:"snapshot_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Effective_from          string   `json:"effective_from" openapi:"{\"type\":\"string\"}"`
	Effective_until         string   `json:"effective_until" openapi:"{\"type\":\"string\"}"`
	Authority_sources       []string `json:"authority_sources" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Is_active               bool     `json:"is_active" openapi:"{\"type\":\"boolean\"}"`
	Active_pointer_revision int64    `json:"active_pointer_revision" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type RulePackDetail struct {
	Pack     RulePackSummary          `json:"pack" openapi:"{\"$ref\":\"#/components/schemas/RulePackSummary\"}"`
	Manifest RulePackManifestSnapshot `json:"manifest" openapi:"{\"$ref\":\"#/components/schemas/RulePackManifestSnapshot\"}"`
}
type RulePackList struct {
	Items []RulePackSummary `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackSummary\"}"`
}
type RulePackHistory struct {
	Items []RulePackHistoryEntry `json:"items" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackHistoryEntry\"}"`
}
type RulePackHistoryEntry struct {
	Sequence          int64  `json:"sequence" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Pack_id           string `json:"pack_id" openapi:"{\"type\":\"string\"}"`
	Version           string `json:"version" openapi:"{\"type\":\"string\"}"`
	Action            string `json:"action" openapi:"{\"type\":\"string\",\"enum\":[\"create\",\"edit\",\"validate\",\"approve\",\"publish\",\"disable\",\"rollback\",\"test\"]}"`
	Actor_id          string `json:"actor_id" openapi:"{\"type\":\"string\"}"`
	Content_hash      string `json:"content_hash" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Pointer_revision  int64  `json:"pointer_revision" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Created_at        string `json:"created_at" openapi:"{\"type\":\"string\"}"`
	Previous_version  any    `json:"previous_version" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Previous_active   any    `json:"previous_active" openapi:"{\"type\":[\"boolean\",\"null\"]}"`
	Resulting_version any    `json:"resulting_version" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Resulting_active  any    `json:"resulting_active" openapi:"{\"type\":[\"boolean\",\"null\"]}"`
}
type RulePackCreateRequest struct {
	Manifest RulePackManifestInput `json:"manifest" openapi:"{\"$ref\":\"#/components/schemas/RulePackManifestInput\"}"`
}
type RulePackEditRequest struct {
	Expected_revision int64                 `json:"expected_revision" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Manifest          RulePackManifestInput `json:"manifest" openapi:"{\"$ref\":\"#/components/schemas/RulePackManifestInput\"}"`
}
type RulePackRevisionRequest struct {
	Expected_revision int64 `json:"expected_revision" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
}
type RulePackApproveRequest struct {
	Expected_revision     int64  `json:"expected_revision" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Expected_content_hash string `json:"expected_content_hash" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
}
type RulePackPointerRequest struct {
	Expected_revision         int64 `json:"expected_revision" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Expected_pointer_revision int64 `json:"expected_pointer_revision" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type RulePackRollbackRequest struct {
	Version                   string `json:"version" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Expected_pointer_revision int64  `json:"expected_pointer_revision" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type RulePackTestSource struct {
	Id      string `json:"id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Version string `json:"version" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Role    string `json:"role" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Text    string `json:"text" openapi:"{\"type\":\"string\"}"`
}
type RulePackTestCitation struct {
	Source_id      string `json:"source_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Source_version string `json:"source_version" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Start          int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End            int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Quote          string `json:"quote" openapi:"{\"type\":\"string\"}"`
}
type RulePackTestFact struct {
	Id                 string                 `json:"id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Rule_id            string                 `json:"rule_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Dimension          string                 `json:"dimension" openapi:"{\"type\":\"string\"}"`
	Object_ref         string                 `json:"object_ref" openapi:"{\"type\":\"string\"}"`
	Value              string                 `json:"value" openapi:"{\"type\":\"string\"}"`
	Unit               string                 `json:"unit" openapi:"{\"type\":\"string\"}"`
	Source_role        string                 `json:"source_role" openapi:"{\"type\":\"string\"}"`
	Measurement_window string                 `json:"measurement_window,omitempty" openapi:"{\"type\":\"string\"}"`
	Time_zone          string                 `json:"time_zone,omitempty" openapi:"{\"type\":\"string\"}"`
	Calendar           string                 `json:"calendar,omitempty" openapi:"{\"type\":\"string\"}"`
	Outage_definition  string                 `json:"outage_definition,omitempty" openapi:"{\"type\":\"string\"}"`
	Exclusions         []string               `json:"exclusions,omitempty" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Basis              string                 `json:"basis,omitempty" openapi:"{\"type\":\"string\"}"`
	Applicable         bool                   `json:"applicable,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Citations          []RulePackTestCitation `json:"citations" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackTestCitation\"}"`
}
type RulePackFixtureSuite struct {
	Schema_version any                   `json:"schema_version" openapi:"{\"const\":\"rulepack-fixture-suite/v1\"}"`
	Id             string                `json:"id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Pack_id        string                `json:"pack_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Pack_version   string                `json:"pack_version" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Data_class     any                   `json:"data_class" openapi:"{\"const\":\"synthetic\"}"`
	Cases          []RulePackFixtureCase `json:"cases" openapi:"{\"type\":\"array\",\"minItems\":3,\"maxItems\":32}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackFixtureCase\"}"`
}
type RulePackFixtureCase struct {
	Id       string                    `json:"id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Title    string                    `json:"title" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Kind     any                       `json:"kind" openapi:"{\"enum\":[\"positive\",\"negative\",\"insufficient\"]}"`
	Input    RulePackFixtureInput      `json:"input" openapi:"{\"$ref\":\"#/components/schemas/RulePackFixtureInput\"}"`
	Expected []RulePackFixtureExpected `json:"expected" openapi:"{\"type\":\"array\",\"minItems\":1}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackFixtureExpected\"}"`
}
type RulePackFixtureInput struct {
	Business_line   string                                    `json:"business_line" openapi:"{\"type\":\"string\"}"`
	Scenario        string                                    `json:"scenario" openapi:"{\"type\":\"string\"}"`
	Contract_type   string                                    `json:"contract_type" openapi:"{\"type\":\"string\"}"`
	At              string                                    `json:"at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Sources         []RulePackTestSource                      `json:"sources" openapi:"{\"type\":\"array\",\"minItems\":1,\"maxItems\":16}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackTestSource\"}"`
	Facts           []RulePackTestFact                        `json:"facts" openapi:"{\"type\":\"array\",\"maxItems\":128}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackTestFact\"}"`
	Authority_gates []RulePackFixtureInputAuthority_gatesItem `json:"authority_gates,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":128}" openapi_items:"{\"type\":\"object\",\"required\":[\"rule_id\",\"source_id\",\"source_version\",\"outcome\"],\"additionalProperties\":false}"`
}
type RulePackFixtureExpected struct {
	Rule_id      string                 `json:"rule_id" openapi:"{\"type\":\"string\"}"`
	Rule_version string                 `json:"rule_version" openapi:"{\"type\":\"string\"}"`
	Relation     any                    `json:"relation" openapi:"{\"enum\":[\"compatible\",\"conflict\",\"insufficient\",\"not_applicable\"]}"`
	Reason_code  string                 `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Citations    []RulePackTestCitation `json:"citations" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackTestCitation\"}"`
}
type RulePackFixtureResponse struct {
	Revision         int64                             `json:"revision" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Content_hash     string                            `json:"content_hash" openapi:"{\"type\":\"string\"}"`
	Suite            RulePackFixtureRun                `json:"suite" openapi:"{\"$ref\":\"#/components/schemas/RulePackFixtureRun\"}"`
	Compare_revision int64                             `json:"compare_revision,omitempty" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Comparison       RulePackFixtureResponseComparison `json:"comparison,omitempty" openapi:"{\"type\":\"object\",\"required\":[\"baseline\",\"changes\"],\"additionalProperties\":false}"`
}
type RulePackFixtureRun struct {
	Suite_id     string                        `json:"suite_id" openapi:"{\"type\":\"string\"}"`
	Pack_id      string                        `json:"pack_id" openapi:"{\"type\":\"string\"}"`
	Pack_version string                        `json:"pack_version" openapi:"{\"type\":\"string\"}"`
	Pack_hash    string                        `json:"pack_hash" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Passed       bool                          `json:"passed" openapi:"{\"type\":\"boolean\"}"`
	Cases        []RulePackFixtureRunCasesItem `json:"cases" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"required\":[\"id\",\"title\",\"kind\",\"passed\",\"failures\"],\"additionalProperties\":false}"`
}
type RulePackFixtureChange struct {
	Case_id      string               `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Rule_id      string               `json:"rule_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Before       RulePackReportResult `json:"before,omitempty" openapi:"{\"$ref\":\"#/components/schemas/RulePackReportResult\"}"`
	After        RulePackReportResult `json:"after,omitempty" openapi:"{\"$ref\":\"#/components/schemas/RulePackReportResult\"}"`
	Before_error string               `json:"before_error,omitempty" openapi:"{\"type\":\"string\"}"`
	After_error  string               `json:"after_error,omitempty" openapi:"{\"type\":\"string\"}"`
}
type RulePackTestRequest struct {
	Expected_revision         int64                `json:"expected_revision" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Run_fixture_suite         bool                 `json:"run_fixture_suite,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Expected_content_hash     string               `json:"expected_content_hash,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Compare_version           string               `json:"compare_version,omitempty" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Compare_expected_revision int64                `json:"compare_expected_revision,omitempty" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Compare_expected_hash     string               `json:"compare_expected_hash,omitempty" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Business_line             string               `json:"business_line,omitempty" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Scenario                  string               `json:"scenario,omitempty" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Contract_type             string               `json:"contract_type,omitempty" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	At                        string               `json:"at,omitempty" openapi:"{\"type\":\"string\"}"`
	Sources                   []RulePackTestSource `json:"sources,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":16}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackTestSource\"}"`
	Facts                     []RulePackTestFact   `json:"facts,omitempty" openapi:"{\"type\":\"array\",\"maxItems\":128}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackTestFact\"}"`
}
type RulePackTestResult struct {
	Revision     int64          `json:"revision" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Content_hash string         `json:"content_hash" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Report       RulePackReport `json:"report" openapi:"{\"$ref\":\"#/components/schemas/RulePackReport\"}"`
}
type AgentReviewLimits struct {
	Max_rounds         int64 `json:"max_rounds" openapi:"{\"type\":\"integer\",\"minimum\":1,\"maximum\":3}"`
	Max_calls          int64 `json:"max_calls" openapi:"{\"type\":\"integer\",\"minimum\":1,\"maximum\":16}"`
	Max_duration_ms    int64 `json:"max_duration_ms" openapi:"{\"type\":\"integer\",\"minimum\":1000,\"maximum\":120000}"`
	Max_evidence_bytes int64 `json:"max_evidence_bytes" openapi:"{\"type\":\"integer\",\"minimum\":1,\"maximum\":8388608}"`
}
type AgentReviewPlanner struct {
	Identity               string            `json:"identity" openapi:"{\"type\":\"string\"}"`
	Configuration_id       string            `json:"configuration_id" openapi:"{\"type\":\"string\"}"`
	Model_configuration_id any               `json:"model_configuration_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Prompt_version         string            `json:"prompt_version" openapi:"{\"type\":\"string\"}"`
	Prompt_sha256          string            `json:"prompt_sha256" openapi:"{\"type\":\"string\"}"`
	Schema_version         string            `json:"schema_version" openapi:"{\"type\":\"string\"}"`
	Runtime_version        string            `json:"runtime_version" openapi:"{\"type\":\"string\"}"`
	Tool_catalog_version   string            `json:"tool_catalog_version" openapi:"{\"type\":\"string\"}"`
	Fallback_used          bool              `json:"fallback_used" openapi:"{\"type\":\"boolean\"}"`
	Fallback_code          any               `json:"fallback_code" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Limits                 AgentReviewLimits `json:"limits" openapi:"{\"$ref\":\"#/components/schemas/AgentReviewLimits\"}"`
}
type AgentReviewRulePackIdentity struct {
	Id             any `json:"id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Version        any `json:"version" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Content_sha256 any `json:"content_sha256" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
}
type AgentReviewSourceSnapshot struct {
	Document_ids            []string `json:"document_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Parsed_document_ids     []string `json:"parsed_document_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Parse_selection_hash    string   `json:"parse_selection_hash" openapi:"{\"type\":\"string\"}"`
	Parser_configuration_id string   `json:"parser_configuration_id" openapi:"{\"type\":\"string\"}"`
}
type AgentReviewDomainScope struct {
	Domain         string   `json:"domain" openapi:"{\"type\":\"string\",\"enum\":[\"compliance\",\"commercial\",\"technical\",\"fulfillment\",\"counterparty\"]}"`
	Capability_ids []string `json:"capability_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Source_ids     []string `json:"source_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type AgentReviewCapability struct {
	Capability_id            string   `json:"capability_id" openapi:"{\"type\":\"string\"}"`
	Mandatory                bool     `json:"mandatory,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Status                   string   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"supported\",\"completed\",\"insufficient\",\"partial\",\"unknown\",\"unsupported\",\"not_applicable\"]}"`
	Relation                 any      `json:"relation" openapi:"{\"anyOf\":[{\"type\":\"string\",\"enum\":[\"compatible\",\"conflict\",\"insufficient\",\"not_applicable\"]},{\"type\":\"null\"}]}"`
	Reason_code              string   `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Evidence_ids             []string `json:"evidence_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Canonical_evaluation_ids []string `json:"canonical_evaluation_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type AgentReviewDomainCoverage struct {
	Domain       string                  `json:"domain" openapi:"{\"type\":\"string\",\"enum\":[\"compliance\",\"commercial\",\"technical\",\"fulfillment\",\"counterparty\"]}"`
	Status       string                  `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"not_requested\",\"supported\",\"completed\",\"insufficient\",\"partial\",\"unknown\",\"unsupported\",\"not_applicable\"]}"`
	Capabilities []AgentReviewCapability `json:"capabilities" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AgentReviewCapability\"}"`
}
type AgentReviewGap struct {
	Gap_id        string   `json:"gap_id" openapi:"{\"type\":\"string\"}"`
	Domain        string   `json:"domain" openapi:"{\"type\":\"string\",\"enum\":[\"compliance\",\"commercial\",\"technical\",\"fulfillment\",\"counterparty\"]}"`
	Capability_id string   `json:"capability_id" openapi:"{\"type\":\"string\"}"`
	Mandatory     bool     `json:"mandatory,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Status        string   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"open\",\"resolved\",\"completed\",\"insufficient\",\"unsupported\",\"unknown\"]}"`
	Reason_code   string   `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Evidence_ids  []string `json:"evidence_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type AgentReviewCall struct {
	Call_id       string   `json:"call_id" openapi:"{\"type\":\"string\"}"`
	Tool_id       string   `json:"tool_id" openapi:"{\"type\":\"string\"}"`
	Tool_version  string   `json:"tool_version" openapi:"{\"type\":\"string\"}"`
	Domain        string   `json:"domain" openapi:"{\"type\":\"string\",\"enum\":[\"compliance\",\"commercial\",\"technical\",\"fulfillment\",\"counterparty\"]}"`
	Capability_id string   `json:"capability_id" openapi:"{\"type\":\"string\"}"`
	Gap_id        string   `json:"gap_id" openapi:"{\"type\":\"string\"}"`
	Status        string   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"planned\",\"running\",\"completed\",\"failed\",\"denied\",\"cancelled\",\"insufficient\",\"unsupported\"]}"`
	Reason_code   any      `json:"reason_code" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Evidence_ids  []string `json:"evidence_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type AgentReviewRound struct {
	Round            int64             `json:"round" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Status           string            `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"accepted\",\"guard_rejected\",\"completed\",\"partial\",\"no_progress\",\"budget_exhausted\",\"cancelled\",\"failed\"]}"`
	Gap_ids          []string          `json:"gap_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Calls            []AgentReviewCall `json:"calls" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AgentReviewCall\"}"`
	New_evidence_ids []string          `json:"new_evidence_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Resolved_gap_ids []string          `json:"resolved_gap_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Termination_code any               `json:"termination_code" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
}
type AgentReviewPlan struct {
	Status         string             `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"not_planned\",\"accepted\",\"guard_rejected\",\"executing\",\"completed\",\"partial\",\"fallback\"]}"`
	Rejection_code any                `json:"rejection_code" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Calls          []AgentReviewCall  `json:"calls" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AgentReviewCall\"}"`
	Rounds         []AgentReviewRound `json:"rounds" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AgentReviewRound\"}"`
}
type AgentReviewAnchor struct {
	Id                 string `json:"id" openapi:"{\"type\":\"string\"}"`
	Document_id        string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id string `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
	Normalized_digest  string `json:"normalized_digest" openapi:"{\"type\":\"string\"}"`
	Quote              string `json:"quote" openapi:"{\"type\":\"string\"}"`
	Status             string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"located\",\"unlocatable\"]}"`
	Start              int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End                int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Reason             any    `json:"reason" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Page               any    `json:"page" openapi:"{\"anyOf\":[{\"type\":\"integer\"},{\"type\":\"null\"}]}"`
	Element_id         any    `json:"element_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
}
type AgentReviewEvidence struct {
	Evidence_id   string            `json:"evidence_id" openapi:"{\"type\":\"string\"}"`
	Semantic_type string            `json:"semantic_type" openapi:"{\"type\":\"string\"}"`
	Value         string            `json:"value" openapi:"{\"type\":\"string\"}"`
	Unit          any               `json:"unit" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Currency      any               `json:"currency" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Verification  string            `json:"verification" openapi:"{\"type\":\"string\",\"enum\":[\"verified\",\"typed_semantic_and_frozen_quote_verified\",\"unknown\",\"rejected\"]}"`
	Anchor        AgentReviewAnchor `json:"anchor" openapi:"{\"$ref\":\"#/components/schemas/AgentReviewAnchor\"}"`
}
type AgentReviewUsage struct {
	Planner_calls int64 `json:"planner_calls" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Input_tokens  any   `json:"input_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\"},{\"type\":\"null\"}]}"`
	Output_tokens any   `json:"output_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\"},{\"type\":\"null\"}]}"`
	Total_tokens  any   `json:"total_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\"},{\"type\":\"null\"}]}"`
}
type AgentReviewEvent struct {
	Event_id    string `json:"event_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Occurred_at string `json:"occurred_at,omitempty" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Sequence    int64  `json:"sequence" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Round       int64  `json:"round" openapi:"{\"type\":\"integer\",\"minimum\":0,\"maximum\":3}"`
	Kind        string `json:"kind" openapi:"{\"type\":\"string\",\"enum\":[\"planning\",\"guard\",\"tool_call\",\"evidence\",\"budget\",\"terminal\",\"authorization\",\"input\",\"gap_unsupported\",\"plan_rejected\",\"planner_fallback\",\"plan\",\"plan_accepted\",\"tool_failed\",\"tool_completed\",\"tool_started\",\"tool_observed\",\"run_cancelled\",\"knowledge_read_started\",\"knowledge_read_completed\",\"knowledge_read_failed\"]}"`
	Call_id     any    `json:"call_id" openapi:"{\"anyOf\":[{\"type\":\"string\"},{\"type\":\"null\"}]}"`
	Tool_id     string `json:"tool_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Gap_id      string `json:"gap_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Domain      any    `json:"domain" openapi:"{\"anyOf\":[{\"type\":\"string\",\"enum\":[\"compliance\",\"commercial\",\"technical\",\"fulfillment\",\"counterparty\"]},{\"type\":\"null\"}]}"`
	Code        string `json:"code" openapi:"{\"type\":\"string\"}"`
	Duration_ms int64  `json:"duration_ms" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type AgentReviewObservation struct {
	Round                int64    `json:"round" openapi:"{\"type\":\"integer\"}"`
	Call_id              string   `json:"call_id" openapi:"{\"type\":\"string\"}"`
	Gap_id               string   `json:"gap_id" openapi:"{\"type\":\"string\"}"`
	Tool_id              string   `json:"tool_id" openapi:"{\"type\":\"string\"}"`
	Reported_relation    string   `json:"reported_relation" openapi:"{\"type\":\"string\"}"`
	Reason_code          string   `json:"reason_code,omitempty" openapi:"{\"type\":\"string\"}"`
	Summary              string   `json:"summary" openapi:"{\"type\":\"string\"}"`
	Claim_status         string   `json:"claim_status" openapi:"{\"type\":\"string\"}"`
	Fact_ids             []string `json:"fact_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Located_evidence_ids []string `json:"located_evidence_ids" openapi:"{\"type\":[\"array\",\"null\"]}" openapi_items:"{\"type\":\"string\"}"`
	Truncated            bool     `json:"truncated" openapi:"{\"type\":\"boolean\"}"`
}
type AgentReviewKnowledgeRead struct {
	Knowledge_id   string `json:"knowledge_id" openapi:"{\"type\":\"string\"}"`
	Version_id     string `json:"version_id" openapi:"{\"type\":\"string\"}"`
	Content_sha256 string `json:"content_sha256" openapi:"{\"type\":\"string\"}"`
	Start          int64  `json:"start" openapi:"{\"type\":\"integer\"}"`
	End            int64  `json:"end" openapi:"{\"type\":\"integer\"}"`
	Text_sha256    string `json:"text_sha256" openapi:"{\"type\":\"string\"}"`
	Text           string `json:"text" openapi:"{\"type\":\"string\"}"`
}
type AgentKnowledgeSelection struct {
	Snapshot_sha256 string                                      `json:"snapshot_sha256" openapi:"{\"type\":\"string\"}"`
	Algorithm       string                                      `json:"algorithm" openapi:"{\"type\":\"string\"}"`
	Policy_status   string                                      `json:"policy_status" openapi:"{\"type\":\"string\",\"enum\":[\"available\",\"unknown\"]}"`
	Policy_reason   string                                      `json:"policy_reason" openapi:"{\"type\":\"string\"}"`
	Input           AgentKnowledgeSelectionInput                `json:"input" openapi:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"business_line\",\"scenario\",\"contract_type\",\"gaps\",\"evidence\"]}"`
	Candidates      []AgentKnowledgeSelectionCandidatesItem     `json:"candidates" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"knowledge_id\",\"version_id\",\"content_sha256\",\"kind\",\"required\",\"score\",\"decision\",\"reason\",\"truncated\",\"requires_fact_evaluation\"]}"`
	Budget          AgentKnowledgeSelectionBudget               `json:"budget" openapi:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"reference_limit\",\"field_bytes\",\"evidence_runes_per_document\",\"required_count\",\"optional_candidates\",\"optional_selected\",\"optional_omitted\",\"acceptance_omitted\",\"acceptance_truncated\",\"content_bytes_limit\",\"content_bytes_used\",\"read_calls_limit\",\"read_max_runes\",\"read_max_bytes\",\"prior_read_calls\",\"prior_read_reserved_bytes\"]}"`
	Planner_rounds  []AgentKnowledgeSelectionPlanner_roundsItem `json:"planner_rounds" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"round\",\"references_provided\",\"read_observations_provided\",\"outcome\"]}"`
	Reads           []KnowledgeReadRecord                       `json:"reads" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/KnowledgeReadRecord\"}"`
}
type KnowledgeReadIdentity struct {
	Knowledge_id   string `json:"knowledge_id" openapi:"{\"type\":\"string\"}"`
	Version_id     string `json:"version_id" openapi:"{\"type\":\"string\"}"`
	Content_sha256 string `json:"content_sha256" openapi:"{\"type\":\"string\"}"`
	Text_sha256    string `json:"text_sha256" openapi:"{\"type\":\"string\"}"`
	Start          int64  `json:"start" openapi:"{\"type\":\"integer\"}"`
	End            int64  `json:"end" openapi:"{\"type\":\"integer\"}"`
}
type KnowledgeReadRecord struct {
	Knowledge_id   string `json:"knowledge_id" openapi:"{\"type\":\"string\"}"`
	Version_id     string `json:"version_id" openapi:"{\"type\":\"string\"}"`
	Content_sha256 string `json:"content_sha256" openapi:"{\"type\":\"string\"}"`
	Round          int64  `json:"round" openapi:"{\"type\":\"integer\"}"`
	Start          int64  `json:"start" openapi:"{\"type\":\"integer\"}"`
	End            int64  `json:"end" openapi:"{\"type\":\"integer\"}"`
	Bytes          int64  `json:"bytes" openapi:"{\"type\":\"integer\"}"`
	Status         string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"completed\",\"failed\"]}"`
	Text_sha256    string `json:"text_sha256" openapi:"{\"type\":\"string\"}"`
}
type AgentReview struct {
	Schema_version      string                      `json:"schema_version" openapi:"{\"type\":\"string\",\"const\":\"five-domain-review/v1\"}"`
	Attempt_id          string                      `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Status              string                      `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"queued\",\"planning\",\"running\",\"completed\",\"partial\",\"blocked_live\",\"cancelled\",\"failed\",\"rejected\"]}"`
	Mode                string                      `json:"mode" openapi:"{\"type\":\"string\",\"enum\":[\"fixed_chain\",\"deterministic_planner\",\"model_fixture\",\"model_live\"]}"`
	Source_snapshot     any                         `json:"source_snapshot" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/AgentReviewSourceSnapshot\"},{\"type\":\"null\"}]}"`
	Rulepack            any                         `json:"rulepack" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/AgentReviewRulePackIdentity\"},{\"type\":\"null\"}]}"`
	Planner             AgentReviewPlanner          `json:"planner" openapi:"{\"$ref\":\"#/components/schemas/AgentReviewPlanner\"}"`
	Knowledge_selection AgentKnowledgeSelection     `json:"knowledge_selection,omitempty" openapi:"{\"$ref\":\"#/components/schemas/AgentKnowledgeSelection\"}"`
	Scope               []AgentReviewDomainScope    `json:"scope" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AgentReviewDomainScope\"}"`
	Domains             []AgentReviewDomainCoverage `json:"domains" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AgentReviewDomainCoverage\"}"`
	Gaps                []AgentReviewGap            `json:"gaps" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AgentReviewGap\"}"`
	Plan                AgentReviewPlan             `json:"plan" openapi:"{\"$ref\":\"#/components/schemas/AgentReviewPlan\"}"`
	Evidence            []AgentReviewEvidence       `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AgentReviewEvidence\"}"`
	Usage               AgentReviewUsage            `json:"usage" openapi:"{\"$ref\":\"#/components/schemas/AgentReviewUsage\"}"`
	Unresolved_gap_ids  []string                    `json:"unresolved_gap_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Events              []AgentReviewEvent          `json:"events" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AgentReviewEvent\"}"`
	Observations        []AgentReviewObservation    `json:"observations,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AgentReviewObservation\"}"`
	Knowledge_reads     []AgentReviewKnowledgeRead  `json:"knowledge_reads,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/AgentReviewKnowledgeRead\"}"`
}
type RulePackLifecycle struct {
	State        string `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"draft\",\"validated\",\"approved\",\"published\"]}"`
	Verification any    `json:"verification,omitempty" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/RulePackVerification\"},{\"type\":\"null\"}]}"`
	Approval     any    `json:"approval,omitempty" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/RulePackApproval\"},{\"type\":\"null\"}]}"`
	Release      any    `json:"release,omitempty" openapi:"{\"anyOf\":[{\"$ref\":\"#/components/schemas/RulePackRelease\"},{\"type\":\"null\"}]}"`
}
type RulePackVerification struct {
	Manifest_hash    string `json:"manifest_hash" openapi:"{\"type\":\"string\"}"`
	Verifier_version string `json:"verifier_version" openapi:"{\"type\":\"string\"}"`
	Passed           bool   `json:"passed" openapi:"{\"type\":\"boolean\"}"`
}
type RulePackApproval struct {
	Manifest_hash     string `json:"manifest_hash" openapi:"{\"type\":\"string\"}"`
	Verification_hash string `json:"verification_hash" openapi:"{\"type\":\"string\"}"`
	Actor_id          string `json:"actor_id" openapi:"{\"type\":\"string\"}"`
}
type RulePackRelease struct {
	Manifest_hash string `json:"manifest_hash" openapi:"{\"type\":\"string\"}"`
	Approval_hash string `json:"approval_hash" openapi:"{\"type\":\"string\"}"`
	Release_hash  string `json:"release_hash" openapi:"{\"type\":\"string\"}"`
}
type RulePackManifestInput struct {
	Schema_version  string               `json:"schema_version" openapi:"{\"type\":\"string\",\"const\":\"rulepack/v1\"}"`
	Id              string               `json:"id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Version         string               `json:"version" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Tenant_id       string               `json:"tenant_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Effective_from  string               `json:"effective_from" openapi:"{\"type\":\"string\"}"`
	Effective_until string               `json:"effective_until" openapi:"{\"type\":\"string\"}"`
	Rules           []any                `json:"rules" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
	Fixture_suite   RulePackFixtureSuite `json:"fixture_suite,omitempty" openapi:"{\"$ref\":\"#/components/schemas/RulePackFixtureSuite\"}"`
}
type RulePackManifestSnapshot struct {
	Schema_version  string               `json:"schema_version" openapi:"{\"type\":\"string\",\"const\":\"rulepack/v1\"}"`
	Id              string               `json:"id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Version         string               `json:"version" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Tenant_id       string               `json:"tenant_id" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Effective_from  string               `json:"effective_from" openapi:"{\"type\":\"string\"}"`
	Effective_until string               `json:"effective_until" openapi:"{\"type\":\"string\"}"`
	Rules           []any                `json:"rules" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
	Fixture_suite   RulePackFixtureSuite `json:"fixture_suite,omitempty" openapi:"{\"$ref\":\"#/components/schemas/RulePackFixtureSuite\"}"`
	Lifecycle       RulePackLifecycle    `json:"lifecycle" openapi:"{\"$ref\":\"#/components/schemas/RulePackLifecycle\"}"`
}
type RulePackReportCitation struct {
	Source_id      string `json:"source_id" openapi:"{\"type\":\"string\"}"`
	Source_version string `json:"source_version" openapi:"{\"type\":\"string\"}"`
	Tenant_id      string `json:"tenant_id" openapi:"{\"type\":\"string\"}"`
	Case_id        string `json:"case_id" openapi:"{\"type\":\"string\"}"`
	Source_digest  string `json:"source_digest" openapi:"{\"type\":\"string\"}"`
	Start          int64  `json:"start" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	End            int64  `json:"end" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Quote          string `json:"quote" openapi:"{\"type\":\"string\"}"`
}
type RulePackReportResult struct {
	Rule_id                 string                   `json:"rule_id" openapi:"{\"type\":\"string\"}"`
	Rule_version            string                   `json:"rule_version" openapi:"{\"type\":\"string\"}"`
	Domain                  string                   `json:"domain" openapi:"{\"type\":\"string\",\"enum\":[\"compliance\",\"commercial\",\"technical\",\"fulfillment\",\"counterparty\"]}"`
	Capability_id           string                   `json:"capability_id" openapi:"{\"type\":\"string\"}"`
	Mandatory               bool                     `json:"mandatory" openapi:"{\"type\":\"boolean\"}"`
	Relation                string                   `json:"relation" openapi:"{\"type\":\"string\",\"enum\":[\"compatible\",\"conflict\",\"insufficient\",\"not_applicable\"]}"`
	Reason_code             string                   `json:"reason_code" openapi:"{\"type\":\"string\"}"`
	Legal_authority_outcome string                   `json:"legal_authority_outcome,omitempty" openapi:"{\"type\":\"string\"}"`
	Legal_authority_reason  string                   `json:"legal_authority_reason,omitempty" openapi:"{\"type\":\"string\"}"`
	Fact_ids                []string                 `json:"fact_ids,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Citations               []RulePackReportCitation `json:"citations,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackReportCitation\"}"`
}
type RulePackReport struct {
	Pack_id      string                 `json:"pack_id" openapi:"{\"type\":\"string\"}"`
	Pack_version string                 `json:"pack_version" openapi:"{\"type\":\"string\"}"`
	Pack_hash    string                 `json:"pack_hash" openapi:"{\"type\":\"string\"}"`
	Relations    []string               `json:"relations" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\",\"enum\":[\"compatible\",\"conflict\",\"insufficient\",\"not_applicable\"]}"`
	Results      []RulePackReportResult `json:"results" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackReportResult\"}"`
}
type SystemHealth struct {
	Schema_version string                         `json:"schema_version" openapi:"{\"type\":\"string\",\"const\":\"system-health/v1\"}"`
	Observed_at    string                         `json:"observed_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Mode           string                         `json:"mode" openapi:"{\"type\":\"string\"}"`
	Source         string                         `json:"source" openapi:"{\"type\":\"string\"}"`
	Cluster        any                            `json:"cluster" openapi:"{\"oneOf\":[{\"type\":\"null\"},{\"$ref\":\"#/components/schemas/SystemHealthCluster\"}]}"`
	Services       []SystemHealthServicesItem     `json:"services" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"name\",\"category\",\"kind\",\"detail\",\"target\",\"status\",\"depends_on\",\"container\",\"probe\"]}"`
	Architecture   []SystemHealthArchitectureItem `json:"architecture" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"name\",\"category\",\"target\",\"observed\",\"state\",\"verification\",\"service_ids\",\"reference\"]}"`
}
type SystemHealthCluster struct {
	Status      string             `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"ready\",\"unavailable\"]}"`
	Namespace   string             `json:"namespace" openapi:"{\"type\":\"string\"}"`
	Observed_at string             `json:"observed_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
	Detail      string             `json:"detail" openapi:"{\"type\":\"string\"}"`
	Nodes       []SystemHealthNode `json:"nodes" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/SystemHealthNode\"}"`
}
type SystemHealthNode struct {
	Name    string `json:"name" openapi:"{\"type\":\"string\"}"`
	Status  string `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"ready\",\"unavailable\",\"unknown\"]}"`
	Version string `json:"version" openapi:"{\"type\":\"string\"}"`
}
type LLMEndpointView struct {
	Configured   bool     `json:"configured" openapi:"{\"type\":\"boolean\"}"`
	Name         string   `json:"name" openapi:"{\"type\":\"string\"}"`
	Protocol     string   `json:"protocol" openapi:"{\"type\":\"string\",\"enum\":[\"openai-compatible\"]}"`
	Base_url     string   `json:"base_url" openapi:"{\"type\":\"string\"}"`
	Models       []string `json:"models" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Review_model string   `json:"review_model" openapi:"{\"type\":\"string\"}"`
	Ocr_model    string   `json:"ocr_model" openapi:"{\"type\":\"string\"}"`
	Has_api_key  bool     `json:"has_api_key" openapi:"{\"type\":\"boolean\"}"`
	Revision     int64    `json:"revision" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Updated_at   string   `json:"updated_at" openapi:"{\"type\":\"string\"}"`
	Updated_by   string   `json:"updated_by" openapi:"{\"type\":\"string\"}"`
}
type LLMEndpointProbeRequest struct {
	Protocol      string `json:"protocol" openapi:"{\"type\":\"string\",\"enum\":[\"openai-compatible\"]}"`
	Base_url      string `json:"base_url" openapi:"{\"type\":\"string\"}"`
	Api_key       string `json:"api_key,omitempty" openapi:"{\"type\":\"string\",\"description\":\"可选。仅在地址和协议与已保存端点一致时，为空才复用服务端已保存的密钥；不会写入日志或响应。\"}"`
	Clear_api_key bool   `json:"clear_api_key,omitempty" openapi:"{\"type\":\"boolean\",\"description\":\"为 true 时明确不使用已保存的密钥。\"}"`
}
type LLMEndpointProbeResult struct {
	Models     []string `json:"models" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Latency_ms int64    `json:"latency_ms" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type LLMEndpointInput struct {
	Name              string   `json:"name" openapi:"{\"type\":\"string\",\"minLength\":1,\"maxLength\":80}"`
	Protocol          string   `json:"protocol" openapi:"{\"type\":\"string\",\"enum\":[\"openai-compatible\"]}"`
	Base_url          string   `json:"base_url" openapi:"{\"type\":\"string\"}"`
	Models            []string `json:"models" openapi:"{\"type\":\"array\",\"minItems\":1,\"maxItems\":512}" openapi_items:"{\"type\":\"string\"}"`
	Review_model      string   `json:"review_model,omitempty" openapi:"{\"type\":\"string\"}"`
	Ocr_model         string   `json:"ocr_model,omitempty" openapi:"{\"type\":\"string\"}"`
	Api_key           string   `json:"api_key,omitempty" openapi:"{\"type\":\"string\",\"description\":\"可选。非空时封存并替换已存密钥；为空且 clear_api_key=false 时保留，但更换 base_url 或 protocol 时必须重新输入或清除。\"}"`
	Clear_api_key     bool     `json:"clear_api_key,omitempty" openapi:"{\"type\":\"boolean\"}"`
	Expected_revision int64    `json:"expected_revision,omitempty" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type LLMModelSelectionInput struct {
	Review_model      string `json:"review_model" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Ocr_model         string `json:"ocr_model" openapi:"{\"type\":\"string\",\"minLength\":1}"`
	Expected_revision int64  `json:"expected_revision,omitempty" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type TenantConfigViewLive_control struct {
	Enabled    bool   `json:"enabled" openapi:"{\"type\":\"boolean\"}"`
	Revision   string `json:"revision" openapi:"{\"type\":\"string\",\"pattern\":\"^[1-9][0-9]*$\"}"`
	Updated_at string `json:"updated_at" openapi:"{\"type\":\"string\",\"format\":\"date-time\"}"`
}
type RuleShadowEnvelopeReport struct {
	Schema_version any                               `json:"schema_version" openapi:"{\"const\":\"rulepack-shadow-report/v1\"}"`
	Id             string                            `json:"id" openapi:"{\"type\":\"string\"}"`
	Created_at     string                            `json:"created_at" openapi:"{\"type\":\"string\"}"`
	Created_by     string                            `json:"created_by" openapi:"{\"type\":\"string\"}"`
	Candidate      RuleShadowEnvelopeReportCandidate `json:"candidate" openapi:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"version\",\"revision\",\"content_sha256\",\"snapshot_sha256\"]}"`
	Samples        []RuleShadowSampleResult          `json:"samples" openapi:"{\"type\":\"array\",\"maxItems\":20}" openapi_items:"{\"$ref\":\"#/components/schemas/RuleShadowSampleResult\"}"`
	Completed      int64                             `json:"completed" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Partial        int64                             `json:"partial" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Failed         int64                             `json:"failed" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type OperatorSnapshotListItemsItemAllof1 struct {
	Snapshot_set_id string `json:"snapshot_set_id" openapi:"{\"type\":\"string\"}"`
}
type OperatorEvaluationPack struct {
	Id      string `json:"id" openapi:"{\"type\":\"string\"}"`
	Version string `json:"version" openapi:"{\"type\":\"string\"}"`
}
type OperatorEvaluationObjectSource struct {
	Run_id                 string `json:"run_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Attempt_id             string `json:"attempt_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Association_run_id     string `json:"association_run_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Association_attempt_id string `json:"association_attempt_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Document_id            string `json:"document_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id     string `json:"parsed_document_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Material_id            string `json:"material_id,omitempty" openapi:"{\"type\":\"string\"}"`
	Material_version       int64  `json:"material_version,omitempty" openapi:"{\"type\":\"integer\"}"`
	Purpose                string `json:"purpose,omitempty" openapi:"{\"type\":\"string\"}"`
	Role                   string `json:"role,omitempty" openapi:"{\"type\":\"string\"}"`
}
type ServiceObjectCreateRequestAliasesItem struct {
	Kind  string `json:"kind" openapi:"{\"type\":\"string\"}"`
	Value string `json:"value" openapi:"{\"type\":\"string\"}"`
}
type ServiceObjectMappingPinExternal_object struct {
	Connector_id       string `json:"connector_id" openapi:"{\"type\":\"string\"}"`
	External_object_id string `json:"external_object_id" openapi:"{\"type\":\"string\"}"`
}
type ServiceObjectAssociationConfirmRequestSource struct {
	Run_id             string `json:"run_id" openapi:"{\"type\":\"string\"}"`
	Attempt_id         string `json:"attempt_id" openapi:"{\"type\":\"string\"}"`
	Material_id        string `json:"material_id" openapi:"{\"type\":\"string\"}"`
	Material_version   int64  `json:"material_version" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Document_id        string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id string `json:"parsed_document_id" openapi:"{\"type\":\"string\"}"`
}
type ServiceObjectAssociationPreviewOptionsItem struct {
	Object_ref   string `json:"object_ref" openapi:"{\"type\":\"string\"}"`
	Facts        []any  `json:"facts" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
	Facts_sha256 string `json:"facts_sha256" openapi:"{\"type\":\"string\",\"pattern\":\"^[0-9a-f]{64}$\"}"`
	Basis_refs   []any  `json:"basis_refs" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\"}"`
}
type OperatorMappingChangePatchExternal_object struct {
	Connector_id       string `json:"connector_id" openapi:"{\"type\":\"string\"}"`
	External_object_id string `json:"external_object_id" openapi:"{\"type\":\"string\"}"`
}
type OperatorServiceIdentityPatchAliasesItem struct {
	Kind  string `json:"kind" openapi:"{\"type\":\"string\",\"maxLength\":64}"`
	Value string `json:"value" openapi:"{\"type\":\"string\",\"maxLength\":256}"`
}
type ReviewDiagnosticsEvidenceItem struct {
	Finding_id string                                     `json:"finding_id" openapi:"{\"type\":\"string\"}"`
	Anchors    []ReviewDiagnosticsEvidenceItemAnchorsItem `json:"anchors" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"document_id\",\"parsed_document_id\",\"normalized_digest\"]}"`
}
type ReviewKnowledgeSnapshotExcludedItem struct {
	Ref    GovernedKnowledgeRef `json:"ref" openapi:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Reason string               `json:"reason" openapi:"{\"type\":\"string\"}"`
}
type WorkbenchDashboardProgressItem struct {
	Status string `json:"status" openapi:"{\"type\":\"string\"}"`
	Count  int64  `json:"count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type WorkbenchDashboardRisksItem struct {
	Severity string `json:"severity" openapi:"{\"type\":\"string\",\"enum\":[\"high\",\"medium\",\"low\",\"info\",\"unrated\"]}"`
	Count    int64  `json:"count" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type ErrorError struct {
	Code         string   `json:"code" openapi:"{\"type\":\"string\"}"`
	Message      string   `json:"message" openapi:"{\"type\":\"string\"}"`
	Retryable    bool     `json:"retryable" openapi:"{\"type\":\"boolean\"}"`
	Next_actions []string `json:"next_actions,omitempty" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\",\"enum\":[\"separate_case\",\"explicit_replace\"]}"`
}
type BusinessAgentReviewRulepack struct {
	Id      string `json:"id" openapi:"{\"type\":\"string\"}"`
	Version string `json:"version" openapi:"{\"type\":\"string\"}"`
}
type SettingsModel struct {
	Status string `json:"status" openapi:"{\"type\":\"string\"}"`
	Label  string `json:"label" openapi:"{\"type\":\"string\"}"`
}
type SettingsQueue struct {
	Depth          any `json:"depth" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}"`
	Oldest_wait_ms any `json:"oldest_wait_ms" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}"`
}
type SettingsOperator_audit struct {
	Enabled    bool                                   `json:"enabled" openapi:"{\"type\":\"boolean\"}"`
	Connectors []SettingsOperator_auditConnectorsItem `json:"connectors" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"required\":[\"connector_id\",\"source_system\",\"allowed_objects\"],\"additionalProperties\":false}"`
}
type SettingsExecution struct {
	Total_attempts      int64 `json:"total_attempts" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Running_attempts    int64 `json:"running_attempts" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Completed_attempts  int64 `json:"completed_attempts" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Partial_attempts    int64 `json:"partial_attempts" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Failed_attempts     int64 `json:"failed_attempts" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Cancelled_attempts  int64 `json:"cancelled_attempts" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Model_calls         int64 `json:"model_calls" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Unknown_usage_calls int64 `json:"unknown_usage_calls" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
	Queue_depth         any   `json:"queue_depth" openapi:"{\"type\":\"null\"}"`
}
type ExecutionUsageKnown_subtotalsAnyof0 struct {
	Input_tokens  any `json:"input_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}"`
	Output_tokens any `json:"output_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}"`
	Total_tokens  any `json:"total_tokens" openapi:"{\"anyOf\":[{\"type\":\"integer\",\"minimum\":0},{\"type\":\"null\"}]}"`
}
type RulePackFixtureInputAuthority_gatesItem struct {
	Rule_id           string `json:"rule_id" openapi:"{\"type\":\"string\"}"`
	Source_id         string `json:"source_id" openapi:"{\"type\":\"string\"}"`
	Source_version    string `json:"source_version" openapi:"{\"type\":\"string\"}"`
	Outcome           string `json:"outcome" openapi:"{\"type\":\"string\"}"`
	Reason            string `json:"reason,omitempty" openapi:"{\"type\":\"string\"}"`
	Reference_missing bool   `json:"reference_missing,omitempty" openapi:"{\"type\":\"boolean\"}"`
}
type RulePackFixtureResponseComparison struct {
	Baseline RulePackFixtureRun      `json:"baseline" openapi:"{\"$ref\":\"#/components/schemas/RulePackFixtureRun\"}"`
	Changes  []RulePackFixtureChange `json:"changes" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/RulePackFixtureChange\"}"`
}
type RulePackFixtureRunCasesItem struct {
	Id       string         `json:"id" openapi:"{\"type\":\"string\"}"`
	Title    string         `json:"title" openapi:"{\"type\":\"string\"}"`
	Kind     string         `json:"kind" openapi:"{\"type\":\"string\"}"`
	Passed   bool           `json:"passed" openapi:"{\"type\":\"boolean\"}"`
	Failures []string       `json:"failures" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Report   RulePackReport `json:"report,omitempty" openapi:"{\"$ref\":\"#/components/schemas/RulePackReport\"}"`
}
type AgentKnowledgeSelectionInput struct {
	Business_line string                                     `json:"business_line" openapi:"{\"type\":\"string\"}"`
	Scenario      string                                     `json:"scenario" openapi:"{\"type\":\"string\"}"`
	Contract_type string                                     `json:"contract_type" openapi:"{\"type\":\"string\"}"`
	Gaps          []AgentKnowledgeSelectionInputGapsItem     `json:"gaps" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"gap_id\",\"domain\",\"capability\",\"question\"]}"`
	Evidence      []AgentKnowledgeSelectionInputEvidenceItem `json:"evidence" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"evidence_id\",\"role\",\"sha256\",\"scanned_runes\",\"total_runes\"]}"`
}
type AgentKnowledgeSelectionCandidatesItem struct {
	Knowledge_id             string `json:"knowledge_id" openapi:"{\"type\":\"string\"}"`
	Version_id               string `json:"version_id" openapi:"{\"type\":\"string\"}"`
	Content_sha256           string `json:"content_sha256" openapi:"{\"type\":\"string\"}"`
	Kind                     string `json:"kind" openapi:"{\"type\":\"string\"}"`
	Required                 bool   `json:"required" openapi:"{\"type\":\"boolean\"}"`
	Score                    int64  `json:"score" openapi:"{\"type\":\"integer\"}"`
	Decision                 string `json:"decision" openapi:"{\"type\":\"string\",\"enum\":[\"selected\",\"omitted\"]}"`
	Reason                   string `json:"reason" openapi:"{\"type\":\"string\",\"enum\":[\"required_policy\",\"relevance\",\"no_relevance\",\"planner_budget\",\"acceptance_optional_budget\"]}"`
	Truncated                bool   `json:"truncated" openapi:"{\"type\":\"boolean\"}"`
	Requires_fact_evaluation bool   `json:"requires_fact_evaluation" openapi:"{\"type\":\"boolean\"}"`
}
type AgentKnowledgeSelectionBudget struct {
	Reference_limit             int64 `json:"reference_limit" openapi:"{\"type\":\"integer\"}"`
	Field_bytes                 int64 `json:"field_bytes" openapi:"{\"type\":\"integer\"}"`
	Evidence_runes_per_document int64 `json:"evidence_runes_per_document" openapi:"{\"type\":\"integer\"}"`
	Required_count              int64 `json:"required_count" openapi:"{\"type\":\"integer\"}"`
	Optional_candidates         int64 `json:"optional_candidates" openapi:"{\"type\":\"integer\"}"`
	Optional_selected           int64 `json:"optional_selected" openapi:"{\"type\":\"integer\"}"`
	Optional_omitted            int64 `json:"optional_omitted" openapi:"{\"type\":\"integer\"}"`
	Acceptance_omitted          int64 `json:"acceptance_omitted" openapi:"{\"type\":\"integer\"}"`
	Acceptance_truncated        bool  `json:"acceptance_truncated" openapi:"{\"type\":\"boolean\",\"description\":\"当 acceptance_omitted 大于零时为 true。\"}"`
	Content_bytes_limit         int64 `json:"content_bytes_limit" openapi:"{\"type\":\"integer\"}"`
	Content_bytes_used          int64 `json:"content_bytes_used" openapi:"{\"type\":\"integer\"}"`
	Read_calls_limit            int64 `json:"read_calls_limit" openapi:"{\"type\":\"integer\"}"`
	Read_max_runes              int64 `json:"read_max_runes" openapi:"{\"type\":\"integer\"}"`
	Read_max_bytes              int64 `json:"read_max_bytes" openapi:"{\"type\":\"integer\"}"`
	Prior_read_calls            int64 `json:"prior_read_calls" openapi:"{\"type\":\"integer\"}"`
	Prior_read_reserved_bytes   int64 `json:"prior_read_reserved_bytes" openapi:"{\"type\":\"integer\"}"`
}
type AgentKnowledgeSelectionPlanner_roundsItem struct {
	Round                      int64                   `json:"round" openapi:"{\"type\":\"integer\"}"`
	References_provided        []GovernedKnowledgeRef  `json:"references_provided" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/GovernedKnowledgeRef\"}"`
	Read_observations_provided []KnowledgeReadIdentity `json:"read_observations_provided" openapi:"{\"type\":\"array\"}" openapi_items:"{\"$ref\":\"#/components/schemas/KnowledgeReadIdentity\"}"`
	Outcome                    string                  `json:"outcome" openapi:"{\"type\":\"string\",\"enum\":[\"returned\",\"failed\"]}"`
}
type SystemHealthServicesItem struct {
	Id         string   `json:"id" openapi:"{\"type\":\"string\"}"`
	Name       string   `json:"name" openapi:"{\"type\":\"string\"}"`
	Category   string   `json:"category" openapi:"{\"type\":\"string\"}"`
	Kind       string   `json:"kind" openapi:"{\"type\":\"string\"}"`
	Detail     string   `json:"detail" openapi:"{\"type\":\"string\"}"`
	Target     string   `json:"target" openapi:"{\"type\":\"string\"}"`
	Status     string   `json:"status" openapi:"{\"type\":\"string\",\"enum\":[\"ready\",\"unavailable\",\"unknown\",\"unconfigured\",\"stopped\",\"starting\",\"completed\"]}"`
	Depends_on []string `json:"depends_on" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Container  any      `json:"container" openapi:"{\"oneOf\":[{\"type\":\"null\"},{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"name\",\"image\",\"state\",\"health\",\"started_at\",\"restarts\"],\"properties\":{\"id\":{\"type\":\"string\"},\"name\":{\"type\":\"string\"},\"image\":{\"type\":\"string\"},\"state\":{\"type\":\"string\"},\"health\":{\"type\":\"string\"},\"started_at\":{\"type\":\"string\"},\"restarts\":{\"type\":\"integer\",\"minimum\":0}}}]}"`
	Probe      any      `json:"probe" openapi:"{\"oneOf\":[{\"type\":\"null\"},{\"type\":\"object\",\"required\":[\"kind\",\"status\",\"duration_ms\"],\"properties\":{\"kind\":{\"type\":\"string\"},\"status\":{\"type\":\"string\"},\"duration_ms\":{\"type\":\"integer\",\"minimum\":0}}}]}"`
}
type SystemHealthArchitectureItem struct {
	Id           string   `json:"id" openapi:"{\"type\":\"string\"}"`
	Name         string   `json:"name" openapi:"{\"type\":\"string\"}"`
	Category     string   `json:"category" openapi:"{\"type\":\"string\",\"enum\":[\"platform\",\"storage\",\"messaging\",\"identity\",\"observability\",\"application\"]}"`
	Target       string   `json:"target" openapi:"{\"type\":\"string\"}"`
	Observed     string   `json:"observed" openapi:"{\"type\":\"string\"}"`
	State        string   `json:"state" openapi:"{\"type\":\"string\",\"enum\":[\"observed\",\"not_connected\"]}"`
	Verification string   `json:"verification" openapi:"{\"type\":\"string\",\"const\":\"NOT_RUN\"}"`
	Service_ids  []string `json:"service_ids" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
	Reference    string   `json:"reference" openapi:"{\"type\":\"string\",\"format\":\"uri\"}"`
}
type RuleShadowEnvelopeReportCandidate struct {
	Id              string `json:"id" openapi:"{\"type\":\"string\"}"`
	Version         string `json:"version" openapi:"{\"type\":\"string\"}"`
	Revision        int64  `json:"revision" openapi:"{\"type\":\"integer\",\"minimum\":1}"`
	Content_sha256  string `json:"content_sha256" openapi:"{\"type\":\"string\"}"`
	Snapshot_sha256 string `json:"snapshot_sha256" openapi:"{\"type\":\"string\"}"`
}
type ReviewDiagnosticsEvidenceItemAnchorsItem struct {
	Id                 string `json:"id" openapi:"{\"type\":\"string\"}"`
	Document_id        string `json:"document_id" openapi:"{\"type\":\"string\"}"`
	Parsed_document_id any    `json:"parsed_document_id" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Normalized_digest  any    `json:"normalized_digest" openapi:"{\"type\":[\"string\",\"null\"]}"`
}
type SettingsOperator_auditConnectorsItem struct {
	Connector_id    string   `json:"connector_id" openapi:"{\"type\":\"string\"}"`
	Source_system   any      `json:"source_system" openapi:"{\"type\":[\"string\",\"null\"]}"`
	Allowed_objects []string `json:"allowed_objects" openapi:"{\"type\":\"array\"}" openapi_items:"{\"type\":\"string\"}"`
}
type AgentKnowledgeSelectionInputGapsItem struct {
	Gap_id     string `json:"gap_id" openapi:"{\"type\":\"string\"}"`
	Domain     string `json:"domain" openapi:"{\"type\":\"string\"}"`
	Capability string `json:"capability" openapi:"{\"type\":\"string\"}"`
	Question   string `json:"question" openapi:"{\"type\":\"string\"}"`
}
type AgentKnowledgeSelectionInputEvidenceItem struct {
	Evidence_id   string `json:"evidence_id" openapi:"{\"type\":\"string\"}"`
	Role          string `json:"role" openapi:"{\"type\":\"string\"}"`
	Sha256        string `json:"sha256" openapi:"{\"type\":\"string\"}"`
	Scanned_runes int64  `json:"scanned_runes" openapi:"{\"type\":\"integer\"}"`
	Total_runes   int64  `json:"total_runes" openapi:"{\"type\":\"integer\"}"`
}
type SystemHealthServicesItemContainerOneof1 struct {
	Id         string `json:"id" openapi:"{\"type\":\"string\"}"`
	Name       string `json:"name" openapi:"{\"type\":\"string\"}"`
	Image      string `json:"image" openapi:"{\"type\":\"string\"}"`
	State      string `json:"state" openapi:"{\"type\":\"string\"}"`
	Health     string `json:"health" openapi:"{\"type\":\"string\"}"`
	Started_at string `json:"started_at" openapi:"{\"type\":\"string\"}"`
	Restarts   int64  `json:"restarts" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}
type SystemHealthServicesItemProbeOneof1 struct {
	Kind        string `json:"kind" openapi:"{\"type\":\"string\"}"`
	Status      string `json:"status" openapi:"{\"type\":\"string\"}"`
	Duration_ms int64  `json:"duration_ms" openapi:"{\"type\":\"integer\",\"minimum\":0}"`
}

var SchemaRules = map[string]string{
	"SupportGrant":                           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"tenant_id\",\"org_id\",\"case_id\",\"principal_id\",\"actions\",\"reason\",\"expires_at\",\"created_by\",\"approved_by\",\"status\",\"revision\"]}",
	"SupportGrantList":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"grants\"]}",
	"SupportGrantRequest":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"principal_id\",\"actions\",\"reason\",\"expires_at\"]}",
	"SupportGrantCommand":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_revision\"]}",
	"SupportCaseView":                        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"grant_id\",\"case_id\",\"tenant_id\",\"org_id\",\"title\",\"contract_type\",\"business_status\",\"created_at\",\"updated_at\",\"read_only\"]}",
	"ActiveContext":                          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"membership_id\",\"tenant_id\",\"tenant_name\",\"org_id\",\"org_name\",\"context_version\"]}",
	"ContextList":                            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"memberships\",\"context_switch_enabled\"]}",
	"ContextSwitchRequest":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"membership_id\",\"expected_context_version\"]}",
	"MembershipChoice":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"tenant_id\",\"tenant_name\",\"org_id\",\"org_name\",\"role\"]}",
	"PendingMembershipList":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"memberships\"]}",
	"PendingMembershipSelectionRequest":      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"membership_id\",\"audience\"]}",
	"ContractAuditGateReason":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"code\"]}",
	"ContractAuditBindRequest":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"review_run_id\",\"review_attempt_id\",\"expected_row_version\"]}",
	"ContractAuditBinding":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"binding_id\",\"contract_revision_id\",\"review_run_id\",\"review_attempt_id\",\"attempt_input_hash\",\"snapshot_digest\",\"materials_digest\",\"run_state\",\"attempt_state\",\"gate_state\",\"gate_reasons\",\"finding_count\",\"verified_finding_count\",\"model_failure\",\"bound_by\",\"bound_at\"]}",
	"ContractAuditBindingList":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_revision_id\",\"items\"]}",
	"ApprovalInboxItem":                      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"view\",\"contract_id\",\"contract_name\",\"contract_number\",\"contract_state\",\"contract_revision_id\",\"approval_round_id\",\"round_number\",\"round_state\",\"round_started_at\",\"round_ended_at\",\"task_id\",\"node_order\",\"node_code\",\"role_code\",\"assignee_subject_id\",\"assignee_display\",\"task_state\",\"task_row_version\",\"task_activated_at\",\"task_decided_at\",\"decision_id\",\"decision_action\",\"decision_actor_subject_id\",\"decision_actor_display\",\"decision_opinion\",\"decision_decided_at\",\"allowed_actions\"]}",
	"ApprovalInboxPage":                      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\",\"next_cursor\",\"has_more\"]}",
	"ApprovalRoundReadSummary":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"approval_round_id\",\"round_number\",\"contract_revision_id\",\"state\",\"workflow_template_id\",\"workflow_template_version\",\"initiated_by\",\"initiated_by_display\",\"started_at\",\"ended_at\",\"previous_round_id\",\"row_version\"]}",
	"ApprovalReadTask":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"task_id\",\"node_order\",\"node_code\",\"role_code\",\"assignee_subject_id\",\"assignee_display\",\"state\",\"row_version\",\"activated_at\",\"decided_at\"]}",
	"ApprovalReadDecision":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"decision_id\",\"task_id\",\"node_order\",\"node_code\",\"action\",\"actor_subject_id\",\"actor_display\",\"opinion\",\"decided_at\"]}",
	"ApprovalRoundReadDetail":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"contract_name\",\"contract_number\",\"contract_state\",\"contract_row_version\",\"current_revision_id\",\"round\",\"tasks\",\"decisions\",\"active_task_id\",\"risk_disposition_state\",\"previous_round\",\"revision_changed\",\"revision_diff\",\"allowed_actions\"]}",
	"ApprovalTaskReadDetail":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"selected_task_id\",\"round_detail\",\"materials\"]}",
	"ApprovalRoundHistoryItem":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"round\",\"active_task\",\"task_count\",\"decision_count\"]}",
	"ApprovalRoundHistoryPage":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\",\"next_cursor\",\"has_more\"]}",
	"ContractLedgerFilter":                   "{\"type\":\"object\",\"additionalProperties\":false}",
	"ContractLedgerItem":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"authority_mode\",\"name\",\"number\",\"amount\",\"currency\",\"contract_state\",\"workflow_state\",\"owner_org_id\",\"row_version\",\"created_at\",\"updated_at\"]}",
	"ContractLedgerPage":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\",\"next_cursor\",\"has_more\"]}",
	"ContractSavedView":                      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"view_id\",\"name\",\"filter\",\"columns\",\"row_version\",\"created_at\",\"updated_at\"]}",
	"ContractSavedViewList":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\"]}",
	"ContractSavedViewRequest":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"name\"]}",
	"ContractSavedViewReplaceRequest":        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\",\"name\"]}",
	"ContractSavedViewDeleteResult":          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"deleted\"]}",
	"ContractExportRequest":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"filter\"]}",
	"ContractExport":                         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"export_id\",\"state\",\"format\",\"columns\",\"row_count\",\"created_at\",\"expires_at\",\"download_available\",\"replayed\"]}",
	"DraftContent":                           "{\"type\":\"object\",\"additionalProperties\":false,\"description\":\"可编辑草稿或冻结合同版本的结构化内容。草稿允许不完整，仅校验已提供值的格式、长度、枚举和数量上限；必填完整性属于提交命令。\",\"required\":[\"contract\"]}",
	"ContractBasics":                         "{\"type\":\"object\",\"additionalProperties\":false}",
	"DraftBuyer":                             "{\"type\":\"object\",\"additionalProperties\":false}",
	"DraftSupplier":                          "{\"type\":\"object\",\"additionalProperties\":false}",
	"DraftItem":                              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"item_id\"]}",
	"DraftAssetPlan":                         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"asset_plan_id\"]}",
	"DraftFunding":                           "{\"type\":\"object\",\"additionalProperties\":false}",
	"DraftPaymentPlan":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"sequence\"]}",
	"DraftDeliveryPlan":                      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"sequence\"]}",
	"DraftAcceptancePlan":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"sequence\"]}",
	"PaymentCondition":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"type\"]}",
	"DueRule":                                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"trigger\"]}",
	"PartyRef":                               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"party_id\",\"role\",\"name_snapshot\"]}",
	"UnitRef":                                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"name\"]}",
	"ProcurementRef":                         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"system\",\"result_id\",\"package_id\",\"snapshot_ref\"]}",
	"Attribute":                              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"name\",\"value\"]}",
	"MaterialRef":                            "{\"type\":\"object\",\"additionalProperties\":false,\"description\":\"引用真实案件中的一个材料版本；material_version_id 是该版本文档的全局唯一 ID，服务端按租户核对摘要后才允许引用。\",\"required\":[\"material_id\",\"material_version_id\",\"sha256\",\"purpose\"]}",
	"ContractSourceCapability":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"source_id\",\"source_kind\",\"authority_mode\",\"read_only\",\"can_read_contract\",\"version_ordering\",\"connection_status\"]}",
	"ContractSourceObservationRequest":       "{\"type\":\"object\",\"additionalProperties\":false,\"description\":\"严格来源事件。case 字段是服务器已配置 exact case scope 内的选择器；不接受 tenant、source、authority 或 authenticated 声明。材料/采购本地引用当前不接受。\",\"required\":[\"event_id\",\"external_contract_id\",\"source_version\",\"source_state\",\"occurred_at\",\"case_org_id\",\"case_id\",\"snapshot\"]}",
	"ContractSourceObservationResult":        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"observation_id\",\"contract_id\",\"source_event_id\",\"source_kind\",\"authority_mode\",\"disposition\",\"source_version\",\"current_source_version\",\"recorded_at\",\"replayed\"]}",
	"SourceRef":                              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"system\",\"id\",\"source_version\"]}",
	"ContractSummary":                        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"authority_mode\",\"name\",\"contract_state\",\"workflow_state\",\"row_version\",\"created_at\"]}",
	"SigningJSONValue":                       "{\"oneOf\":[{\"type\":\"string\"},{\"type\":\"number\"},{\"type\":\"boolean\"},{\"type\":\"null\"},{\"type\":\"array\",\"items\":{\"$ref\":\"#/components/schemas/SigningJSONValue\"}},{\"type\":\"object\",\"additionalProperties\":{\"$ref\":\"#/components/schemas/SigningJSONValue\"}}]}",
	"ContractSigningParty":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"role\",\"name\"]}",
	"ContractDeliveryRequest":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\",\"contract_revision_id\",\"item_id\",\"batch_reference\",\"quantity\",\"actual_at\",\"location\",\"evidence\"]}",
	"ContractDelivery":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"delivery_id\",\"contract_id\",\"contract_revision_id\",\"item_id\",\"item_name\",\"batch_reference\",\"quantity\",\"unit\",\"actual_at\",\"location\",\"description\",\"evidence\",\"recorded_by\",\"recorded_at\"]}",
	"ContractFulfillmentItemSummary":         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_revision_id\",\"item_id\",\"item_name\",\"delivered_quantity\",\"qualified_quantity\",\"unallocated_quantity\",\"pending_reinspection_quantity\"]}",
	"ContractLifecycleOverview":              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract\",\"observed_at\",\"completeness\",\"source_observed_at\",\"plans\",\"deliveries\",\"acceptances\",\"payments\",\"assets\"],\"properties\":{\"contract\":{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"current_revision_id\",\"row_version\",\"authority_mode\"],\"properties\":{\"contract_id\":{\"type\":\"string\",\"format\":\"uuid\"},\"current_revision_id\":{\"type\":[\"string\",\"null\"],\"format\":\"uuid\"},\"row_version\":{\"type\":\"integer\"},\"authority_mode\":{\"type\":\"string\"}}},\"observed_at\":{\"type\":\"string\",\"format\":\"date-time\"},\"completeness\":{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"plans\",\"deliveries\",\"acceptances\",\"payments\",\"assets\",\"disputes\",\"warranty\",\"other_obligations\"],\"properties\":{\"plans\":{\"type\":\"string\",\"enum\":[\"known\",\"unknown\",\"not_applicable\"]},\"deliveries\":{\"type\":\"string\",\"enum\":[\"known\",\"unknown\",\"not_applicable\"]},\"acceptances\":{\"type\":\"string\",\"enum\":[\"known\",\"unknown\",\"not_applicable\"]},\"payments\":{\"type\":\"string\",\"enum\":[\"known\",\"unknown\",\"not_applicable\"]},\"assets\":{\"type\":\"string\",\"enum\":[\"known\",\"unknown\",\"not_applicable\"]},\"disputes\":{\"type\":\"string\",\"enum\":[\"known\",\"unknown\",\"not_applicable\"]},\"warranty\":{\"type\":\"string\",\"enum\":[\"known\",\"unknown\",\"not_applicable\"]},\"other_obligations\":{\"type\":\"string\",\"enum\":[\"known\",\"unknown\",\"not_applicable\"]}}},\"source_observed_at\":{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"finance\",\"erp\",\"contract_recorded_at\"],\"properties\":{\"finance\":{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"},\"erp\":{\"type\":[\"string\",\"null\"],\"format\":\"date-time\"},\"contract_recorded_at\":{\"type\":[\"string\",\"null\"],\"format\":\"date-time\",\"description\":\"最后一次合同来源观察或导入在本系统的记录时间；不是外部业务发生时间，不证明当前版本被再次确认。\"}}},\"plans\":{\"type\":[\"object\",\"null\"],\"additionalProperties\":false,\"required\":[\"delivery_plans\",\"acceptance_plans\",\"payments\",\"assets\"],\"properties\":{\"delivery_plans\":{\"type\":[\"array\",\"null\"],\"items\":{\"$ref\":\"#/components/schemas/DraftDeliveryPlan\"}},\"acceptance_plans\":{\"type\":[\"array\",\"null\"],\"items\":{\"$ref\":\"#/components/schemas/DraftAcceptancePlan\"}},\"payments\":{\"type\":[\"array\",\"null\"],\"items\":{\"$ref\":\"#/components/schemas/DraftPaymentPlan\"}},\"assets\":{\"type\":[\"array\",\"null\"],\"items\":{\"$ref\":\"#/components/schemas/DraftAssetPlan\"}}}},\"deliveries\":{\"$ref\":\"#/components/schemas/ContractDeliveryList\"},\"acceptances\":{\"$ref\":\"#/components/schemas/ContractAcceptanceList\"},\"payments\":{\"type\":[\"array\",\"null\"],\"items\":{\"$ref\":\"#/components/schemas/ContractPaymentSummary\"}},\"assets\":{\"$ref\":\"#/components/schemas/ContractAssetList\"}}}",
	"ContractDeliveryList":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\",\"summary\",\"row_version\"]}",
	"ContractAcceptanceDefectInput":          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"description\",\"responsible\",\"remediation_due\",\"failed_quantity\"]}",
	"ContractAcceptanceDefect":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"defect_id\",\"description\",\"responsible\",\"remediation_due\",\"failed_quantity\",\"recorded_by\",\"recorded_at\"]}",
	"ContractAcceptanceRequest":              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\",\"contract_revision_id\",\"delivery_id\",\"scope_start\",\"scope_end\",\"accepted_quantity\",\"actual_at\",\"standard\",\"evidence\"]}",
	"ContractRectificationRequest":           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\",\"contract_revision_id\",\"defect_id\",\"description\",\"evidence\"]}",
	"ContractRectification":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"rectification_id\",\"contract_id\",\"contract_revision_id\",\"acceptance_id\",\"defect_id\",\"description\",\"evidence\",\"recorded_by\",\"recorded_at\"]}",
	"ContractAcceptance":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"acceptance_id\",\"contract_id\",\"contract_revision_id\",\"delivery_id\",\"item_id\",\"item_name\",\"scope_start\",\"scope_end\",\"quantity\",\"accepted_quantity\",\"failed_quantity\",\"unit\",\"result\",\"actual_at\",\"standard\",\"inspector_note\",\"evidence\",\"defects\",\"rectifications\",\"recorded_by\",\"recorded_at\"]}",
	"ContractAcceptanceList":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\",\"summary\",\"row_version\"]}",
	"ContractSigningCheckRequest":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"baseline_revision_id\",\"signed_evidence\",\"signed_content\",\"manual_comparison\"]}",
	"ContractSigningFieldChange":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"path\",\"change\"]}",
	"ContractSigningRestrictedChange":        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"path\",\"changed\"]}",
	"ManualSigningComparison":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"confirmed\",\"signed_material_digest\",\"contract_revision_id\",\"scope\"]}",
	"ContractSigningCheckResult":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"baseline_revision_id\",\"comparison_method\",\"comparison_result\",\"can_register\",\"verification_status\",\"approval_status\",\"fields\",\"restricted_fields\",\"warnings\"]}",
	"ContractSigningRegistrationRequest":     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\",\"baseline_revision_id\",\"signing_date\",\"signed_evidence\",\"signed_content\",\"manual_comparison\",\"parties\"]}",
	"ContractSigningRegistration":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"registration_id\",\"contract_id\",\"contract_revision_id\",\"signing_date\",\"signed_material\",\"parties\",\"signature_evidence_status\",\"approval_basis\",\"approval_evidence_status\",\"approval_round_id\",\"comparison_method\",\"comparison_result\",\"verification_status\",\"comparison_confirmed_by\",\"comparison_confirmed_at\",\"comparison_scope\",\"warnings\",\"registered_by\",\"registered_at\"]}",
	"ContractSigningRegistrationList":        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\"]}",
	"ContractSupplementRequest":              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\",\"parent_revision_id\",\"relation_type\",\"reason\",\"draft\"]}",
	"ContractSupplement":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"parent_contract_id\",\"supplement_contract_id\",\"parent_revision_id\",\"relation_type\",\"reason\",\"linked_by\",\"linked_at\",\"contract_state\"]}",
	"ContractSupplementResult":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"supplement\",\"contract\"]}",
	"ContractSupplementList":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\"]}",
	"ContractDetail":                         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"summary\",\"current_revision_id\",\"allowed_commands\",\"warnings\"]}",
	"ContractVersionFreezeRequest":           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\"]}",
	"ContractVersion":                        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_revision_id\",\"revision_number\",\"amount\",\"currency\",\"snapshot_digest\",\"amounts_reconciled\",\"material_count\",\"frozen_by\",\"frozen_at\",\"audit_state\",\"audit_gate_state\"]}",
	"ContractVersionFreezeResult":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"version\",\"contract_state\",\"current_revision_id\",\"row_version\",\"allowed_commands\",\"source_case_id\"]}",
	"ContractVersionList":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\"]}",
	"ContractPaymentEligibilityRequest":      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_revision_id\",\"payment_sequence\",\"expected_row_version\"]}",
	"ContractPaymentEligibility":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"check_id\",\"contract_id\",\"contract_revision_id\",\"payment_sequence\",\"status\",\"reasons\",\"evidence\",\"checked_at\",\"contract_row_version\",\"initiates_payment\"]}",
	"ContractPaymentReceiptRequest":          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"contract_revision_id\",\"payment_sequence\",\"event_id\",\"event_type\",\"amount\",\"currency\",\"occurred_at\"]}",
	"ContractPaymentReceipt":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"receipt_id\",\"contract_id\",\"contract_revision_id\",\"payment_sequence\",\"source_id\",\"source_key_id\",\"source_event_id\",\"event_type\",\"amount\",\"currency\",\"occurred_at\",\"recorded_at\",\"payload_digest\"]}",
	"ContractPaymentSummary":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"contract_revision_id\",\"payment_sequence\",\"net_paid\",\"state\",\"receipts\"]}",
	"ContractAssetHandoverRequest":           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\",\"contract_revision_id\",\"asset_plan_id\",\"delivery_id\",\"acceptance_id\",\"scope_start\",\"scope_end\",\"handed_over_at\",\"location\",\"transfer_note\"]}",
	"ContractAssetReceiptRequest":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"handover_id\",\"event_id\",\"event_type\",\"asset_code\",\"quantity\",\"unit\",\"occurred_at\"]}",
	"ContractAssetReceipt":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"receipt_id\",\"contract_id\",\"handover_id\",\"source_id\",\"source_key_id\",\"source_event_id\",\"event_type\",\"asset_code\",\"quantity\",\"unit\",\"occurred_at\",\"recorded_at\",\"payload_digest\"]}",
	"ContractAssetHandover":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"handover_id\",\"contract_id\",\"contract_revision_id\",\"asset_plan_id\",\"delivery_id\",\"item_id\",\"acceptance_id\",\"scope_start\",\"scope_end\",\"quantity\",\"unit\",\"handed_over_at\",\"location\",\"transfer_note\",\"evidence\",\"department_verification\",\"custodian_verification\",\"assignment_verification\",\"booking_status\",\"recorded_by\",\"recorded_at\",\"contract_row_version\",\"receipts\"]}",
	"ContractAssetPlanSummary":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_revision_id\",\"asset_plan_id\",\"department_verification\",\"fixed_asset_status\",\"plan_status\",\"handovers\"]}",
	"ContractAssetList":                      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"row_version\",\"fixed_asset_status\",\"items\"]}",
	"ContractVersionMaterial":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"material_id\",\"material_version_id\",\"material_version\",\"filename\",\"digest\",\"purpose\",\"case_id\",\"case_org_id\"]}",
	"ContractVersionDetail":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"version\",\"materials\"]}",
	"ContractVersionFieldChange":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"path\",\"category\",\"classification\",\"change\"]}",
	"ContractVersionRestrictedChange":        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"path\",\"changed\"]}",
	"ContractVersionDiffSummary":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"changed_fields\",\"added_fields\",\"removed_fields\",\"restricted_changed\",\"restricted_unknown\"]}",
	"ContractVersionDiff":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"from_revision_id\",\"to_revision_id\",\"from_revision_number\",\"to_revision_number\",\"changed\",\"fields\",\"restricted_fields\",\"amount_terms\",\"summary\"]}",
	"ApprovalDecisionRequest":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"decision\",\"expected_round_row_version\"]}",
	"ApprovalCredentialRequest":              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[],\"properties\":{}}",
	"ApprovalCredentialClaims":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"task_id\",\"contract_id\",\"approval_round_id\",\"contract_revision_id\",\"revision_number\",\"snapshot_digest\",\"task_row_version\",\"contract_row_version\",\"amount_limit\",\"currency\",\"role\",\"org_id\",\"expires_at\"]}",
	"ApprovalCredentialResult":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"credential\",\"claims\"]}",
	"ApprovalTransferRequest":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"to_user_id\",\"reason\",\"expected_task_row_version\"]}",
	"ApprovalTransferResult":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"transfer_id\",\"task_id\",\"from_user_id\",\"to_user_id\",\"actor_id\",\"row_version\",\"transferred_at\"]}",
	"ApprovalDecisionResult":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"decision_id\",\"decision\",\"round\",\"task\",\"next_task\",\"contract_state\",\"workflow_state\",\"row_version\"]}",
	"ApprovalSubmissionRequest":              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\"]}",
	"ApprovalRound":                          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"approval_round_id\",\"contract_revision_id\",\"round_number\",\"state\",\"workflow_template_id\",\"workflow_template_version\",\"routing_digest\",\"row_version\",\"started_at\",\"ended_at\"]}",
	"ApprovalTask":                           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"approval_task_id\",\"node_code\",\"node_order\",\"title\",\"role_code\",\"final\",\"state\",\"assignee_user_ids\"]}",
	"ApprovalSubmissionResult":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"round\",\"tasks\",\"contract_state\",\"workflow_state\",\"row_version\",\"allowed_commands\"]}",
	"WorkflowTemplateNodeInput":              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"node_code\",\"title\",\"role_code\",\"final\"]}",
	"WorkflowTemplateNode":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"node_code\",\"node_order\",\"title\",\"role_code\",\"final\"]}",
	"WorkflowTemplatePublishRequest":         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"template_id\",\"template_version\",\"name\",\"start_action\",\"start_action_is_approval_node\",\"execution\",\"nodes\"]}",
	"WorkflowTemplate":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"template_id\",\"template_version\",\"name\",\"start_action\",\"start_action_is_approval_node\",\"execution\",\"nodes\",\"template_digest\",\"published_by\",\"published_at\"]}",
	"WorkflowRoleBindingInput":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"org_scope\",\"user_ids\"]}",
	"WorkflowRoleReplaceRequest":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"bindings\"]}",
	"WorkflowRoleBinding":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"org_scope\",\"user_ids\"]}",
	"WorkflowRole":                           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"role_code\",\"row_version\",\"bindings\",\"updated_by\",\"updated_at\"]}",
	"ContractDraftSummary":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"authority_mode\",\"name\",\"contract_state\",\"workflow_state\",\"row_version\",\"created_at\"]}",
	"ContractDraftList":                      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\"]}",
	"ContractDraftCreateRequest":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\",\"authority_mode\",\"draft\"]}",
	"ContractDraftReplaceRequest":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\",\"replacement_draft\"]}",
	"ContractLegacyImportRequest":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_row_version\",\"authority_mode\",\"source_ref\",\"source_evidence\",\"snapshot\",\"approval_history_status\",\"historical_approval_materials\",\"reason\"]}",
	"PartyReferenceSnapshot":                 "{\"type\":\"object\",\"additionalProperties\":false,\"readOnly\":true,\"required\":[\"binding_id\",\"binding_version\",\"case_role\",\"binding_state\",\"subject_id\",\"subject_version\",\"subject_payload_hash\",\"verification_state\",\"display_name\",\"captured_at\"]}",
	"ProcurementSourceSnapshot":              "{\"type\":\"object\",\"additionalProperties\":false,\"readOnly\":true,\"required\":[\"system\",\"result_id\",\"package_id\",\"snapshot_ref\",\"material_id\",\"material_version_id\",\"material_version\",\"material_role\",\"sha256\",\"captured_at\"]}",
	"ContractSubjectReference":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"party_id\",\"role\",\"name_snapshot\",\"binding_id\",\"binding_version\",\"case_role\",\"binding_state\",\"subject_version\",\"subject_payload_hash\",\"verification_state\",\"frozen\"]}",
	"ContractMaterialReference":              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"material_id\",\"material_version_id\",\"sha256\",\"purpose\",\"material_version\",\"material_role\",\"filename\",\"media_type\",\"frozen\",\"original_path\"]}",
	"ContractReferenceCheck":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"contract_id\",\"source\",\"contract_revision_id\",\"source_case_id\",\"checked_at\",\"subjects\",\"materials\",\"warnings\"]}",
	"DirectoryStatusSnapshot":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"revision\",\"last_good_revision\",\"checked_at\",\"loaded_at\",\"refresh_error_code\",\"mode\",\"state\",\"local_verification_interval_ms\"]}",
	"ModelBudgetCapacity":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"max_in_flight\",\"calls_per_minute\",\"tokens_per_minute\"]}",
	"ModelBudgetGatewayStatus":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"schema_version\",\"revision\",\"mode\",\"window_seconds\",\"tenant\",\"global\"]}",
	"TenantModelUsage":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"active_calls\",\"window_calls\",\"charged_tokens\",\"reserved_tokens\",\"unknown_usage_calls\",\"observed_at\",\"window_start\",\"window_seconds\"]}",
	"TenantRuntimeControl":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"status\",\"live_epoch\"]}",
	"TenantExecutionQueue":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"policy_hash\",\"policy_version\",\"tenant_weight\",\"tenant_in_flight_limit\",\"tenant_pending_limit\",\"tenant_live\"]}",
	"TenantCostWindow":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"period_key\",\"limit_minor\",\"reserved_minor\",\"settled_minor\",\"unknown_hold_minor\",\"known\"]}",
	"TenantCost":                             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"currency\",\"minor_unit_scale\",\"timezone\",\"day\",\"month\"]}",
	"TenantConfigRef":                        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"version\",\"digest\"]}",
	"TenantConfigCatalogEntry":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"version\",\"digest\",\"title\",\"kind\"]}",
	"TenantModelCatalogEntry":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"revision\",\"provider_label\",\"chat_model\",\"ocr_model\"]}",
	"TenantCredentialRef":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"label\"]}",
	"TenantConfigSelection":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"public_template_ref\",\"private_policy_refs\",\"model_catalog_ref\",\"credential_ref\",\"egress_mode\"]}",
	"AdoptedTenantConfig":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"config_version\",\"version_id\",\"digest\",\"public_template\",\"private_policy_refs\",\"model_catalog_ref\",\"credential_ref\",\"egress_mode\",\"adopted_at\",\"adopted_by\"]}",
	"TenantConfigView":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"schema_version\",\"tenant_id\",\"revision\",\"current\",\"public_template_catalog\",\"private_policy_catalog\",\"model_catalog\",\"credential_refs\",\"live_control\"]}",
	"TenantConfigPreviewRequest":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_revision\",\"selection\"]}",
	"TenantConfigDiff":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"path\",\"change\",\"before\",\"after\"]}",
	"TenantConfigValidationError":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"path\",\"code\"]}",
	"TenantConfigPreview":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"valid\",\"diff\",\"errors\"]}",
	"TenantConfigAdoptionRequest":            "{\"$ref\":\"#/components/schemas/TenantConfigPreviewRequest\"}",
	"TenantConfigAdoptionResult":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"revision\",\"adopted_config\"]}",
	"TenantLiveControlRequest":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_revision\",\"enabled\"]}",
	"TenantLiveControlResult":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"revision\",\"enabled\"]}",
	"TenantOperations":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"schema_version\",\"tenant_id\",\"directory\",\"login_rate_limit_scope\",\"budget_state\",\"budget\",\"usage\",\"tenant_control\",\"execution_queue\",\"cost\"]}",
	"IntakeRequestIdentity":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"title\",\"contract_type\",\"business_line\",\"scenario\",\"file_name\",\"file_size\",\"media_type\",\"file_sha256\"]}",
	"PendingIntake":                          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"operation_id\",\"case_id\",\"title\",\"state\",\"file_name\",\"file_size\",\"created_at\",\"updated_at\",\"error_code\",\"can_resume\",\"can_archive\",\"reason_code\"]}",
	"PendingIntakesPage":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\",\"next_cursor\"]}",
	"IntakeContinuation":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"operation_id\",\"case_id\",\"state\",\"request\",\"idempotency_key\",\"can_resume\",\"can_archive\",\"reason_code\"]}",
	"IntakeArchiveResult":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"operation_id\",\"case_id\",\"archived_at\"]}",
	"IntakeOperation":                        "{\"type\":\"object\",\"description\":\"建案操作的扁平公共结构。completed 时 material_id/material_version/document_id 存在且 retryable=false；upload_pending 时 retryable=true 且 error_code 只包含安全的续办代码。\",\"required\":[\"operation_id\",\"case_id\",\"state\",\"retryable\"],\"additionalProperties\":false}",
	"IntakeCreateRequest":                    "{\"type\":\"object\",\"required\":[\"title\",\"contract_type\",\"file\"],\"additionalProperties\":false}",
	"ManualFindingEvidence":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"document_id\",\"parsed_document_id\",\"normalized_digest\",\"start\",\"end\",\"quote\"],\"description\":\"该次执行冻结原文的 Unicode 码点半开区间 [start,end)。\"}",
	"ManualFindingEvidenceInput":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"document_id\",\"parsed_document_id\",\"start\",\"end\",\"quote\"]}",
	"ManualFindingRequest":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"claim\",\"severity\",\"evidence\"],\"description\":\"必须在请求头或正文提供幂等键；同时提供时必须相同。\"}",
	"ManualFinding":                          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"case_id\",\"run_id\",\"attempt_id\",\"actor_id\",\"actor_name\",\"claim\",\"severity\",\"evidence\",\"created_at\"]}",
	"RuleShadowSampleRef":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"case_id\",\"run_id\",\"attempt_id\"]}",
	"RuleShadowSampleOption":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"case_id\",\"run_id\",\"attempt_id\",\"finished_at\",\"state\"]}",
	"RuleShadowChange":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"rule_id\",\"kind\",\"base_relation\",\"candidate_relation\",\"base_reason\",\"candidate_reason\",\"detail\"]}",
	"RuleShadowSampleResult":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"case_id\",\"run_id\",\"attempt_id\",\"case_authorized\",\"status\",\"error_code\",\"message\",\"input_sha256\",\"output_sha256\",\"knowledge_sha256\",\"rule_request_sha256\",\"extractor_ref\",\"base_pack_id\",\"base_pack_version\",\"base_pack_sha256\",\"base_pack_snapshot_sha256\",\"changes\"]}",
	"RuleShadowEnvelope":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"report\",\"report_sha256\"]}",
	"OperatorCollectionJob":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"collection_id\",\"case_id\",\"state\"]}",
	"OperatorTemporalProof":                  "{\"oneOf\":[{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"kind\",\"source_as_of\"],\"properties\":{\"kind\":{\"const\":\"current_state\"},\"source_catalog_id\":{\"type\":\"string\"},\"dataset_version\":{\"type\":\"string\"},\"record_version\":{\"type\":\"string\"},\"source_as_of\":{\"type\":\"string\",\"format\":\"date-time\"}}},{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"kind\",\"source_catalog_id\",\"dataset_version\",\"record_version\",\"source_as_of\",\"valid_from\",\"valid_until\"],\"properties\":{\"kind\":{\"const\":\"historical_interval\"},\"source_catalog_id\":{\"type\":\"string\"},\"dataset_version\":{\"type\":\"string\"},\"record_version\":{\"type\":\"string\"},\"source_as_of\":{\"type\":\"string\",\"format\":\"date-time\"},\"valid_from\":{\"type\":\"string\",\"format\":\"date-time\"},\"valid_until\":{\"type\":\"string\",\"format\":\"date-time\"}}}]}",
	"OperatorObservation":                    "{\"type\":\"object\",\"required\":[\"schema_version\",\"snapshot_id\",\"tenant_id\",\"case_id\",\"connector_id\",\"source_system\",\"object_binding_version\",\"external_object_id\",\"record_id\",\"record_version\",\"observed_at\",\"source_updated_at\",\"effective_from\",\"effective_until\",\"availability\",\"reason_code\",\"payload_ref\",\"fields\",\"data_class\"]}",
	"OperatorSnapshotSet":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"case_id\",\"snapshot_set_id\",\"binding_version\",\"connector_id\",\"external_object_id\",\"source_system\",\"business_at\",\"status\",\"reason_code\",\"collected_by\",\"collected_at\",\"observations\"]}",
	"OperatorCollectionResponse":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"job_id\",\"collection_id\",\"snapshot_set_id\",\"state\",\"connector_id\",\"external_object_id\",\"accepted_at\",\"snapshot_set\"]}",
	"OperatorSnapshotList":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\",\"sets\"]}",
	"OperatorEvaluation":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"evaluation_id\",\"pack\",\"source_review_id\",\"source_attempt_id\",\"contract_version\",\"rule_pack_version\",\"object_binding_version\",\"snapshot_set_id\",\"business_at\",\"machine_status\",\"checks\",\"contract_facts\",\"data_class\",\"created_at\"]}",
	"OperatorEvaluationScope":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"kind\"],\"description\":\"whole_case 不接受对象列表或单对象映射；explicit_subset 必须给出对象列表。\"}",
	"OperatorEvaluationCoverage":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"scope_kind\",\"candidate_count\",\"evaluated_count\",\"unresolved_count\",\"insufficient_count\",\"status\"]}",
	"OperatorEvaluationObject":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"object_ref\",\"source\",\"service_object_id\",\"object_version\",\"association_version\",\"mapping_id\",\"mapping_version\",\"binding_version\",\"snapshot_set_id\",\"status\",\"reason_code\",\"fact_refs\",\"facts\",\"checks\"]}",
	"EvidenceRequestCreate":                  "{\"type\":\"object\",\"additionalProperties\":false,\"description\":\"结果为 compatible 本身不足以完成核验；如果缺少回执与同一检查之间的有效关联，后续核验返回 409，请求保持 received。\",\"required\":[\"task_id\",\"task_version\",\"source_run_id\",\"source_attempt_id\",\"question\",\"verification_condition\"]}",
	"EvidenceReceiptCreate":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_version\"]}",
	"EvidenceRequestVerify":                  "{\"type\":\"object\",\"additionalProperties\":false,\"description\":\"结果为 compatible 本身不足以完成核验；如果缺少回执与同一检查之间的有效关联，返回 409，请求保持 received。\",\"required\":[\"run_id\",\"attempt_id\",\"evidence\",\"reason\",\"expected_version\"]}",
	"EvidenceSourceEvaluation":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"run_id\",\"attempt_id\",\"case_id\",\"domain\",\"rule_version\",\"relation\",\"reason\"]}",
	"EvidenceSourceGap":                      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"gap_id\",\"domain\",\"capability_id\",\"status\",\"reason_code\",\"evidence_ids\"]}",
	"EvidenceRequestVerification":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"run_id\",\"attempt_id\",\"receipt_id\",\"evidence\",\"reason\",\"verified_by\",\"verified_at\"]}",
	"EvidenceRequest":                        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"case_id\",\"task_id\",\"task_version\",\"assignee_id\",\"due_at\",\"source_run_id\",\"source_attempt_id\",\"question\",\"verification_condition\",\"status\",\"version\",\"created_by\",\"created_at\",\"updated_at\"]}",
	"EvidenceRequestList":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\"]}",
	"EvidenceReceipt":                        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"request_id\",\"case_id\",\"submitted_by\",\"created_at\"]}",
	"EvidenceReceiptResult":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"request\",\"receipt\"]}",
	"EvidenceRequestEvent":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"type\",\"actor_id\",\"occurred_at\",\"request\"]}",
	"EvidenceRequestHistory":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\"]}",
	"ServiceObjectCreateRequest":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"object_type\",\"display_name\"]}",
	"ServiceObjectMutationReceipt":           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"service_object_id\",\"version\",\"association_version\"]}",
	"ServiceObjectSummary":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"service_object_id\",\"object_type\",\"state\",\"current_version\",\"current_association_version\",\"display_name\",\"business_code\",\"aliases\",\"current_association\",\"can_manage\",\"needs_reassociation\"]}",
	"ServiceObjectAlias":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"kind\",\"value\"]}",
	"ServiceObjectAssociationPin":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"association_version\",\"object_version\",\"material_id\",\"material_version\",\"document_id\",\"parsed_document_id\",\"source_run_id\",\"source_attempt_id\",\"object_ref\",\"facts_sha256\",\"confirmed_by\",\"confirmed_at\"]}",
	"ServiceObjectVersion":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"service_object_id\",\"version\",\"object_type\",\"display_name\",\"business_code\",\"aliases\",\"state\",\"basis_refs\",\"basis_summary\",\"created_by\",\"created_at\",\"effective_from\",\"payload_sha256\"]}",
	"ServiceObjectFactRef":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"fact\",\"document_id\",\"parsed_document_id\",\"canonical_fact_sha256\"]}",
	"ServiceObjectFact":                      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"rule_id\",\"dimension\",\"object_ref\",\"value\",\"unit\",\"source_role\",\"citations\"]}",
	"ServiceObjectFactCitation":              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"source_id\",\"source_version\",\"tenant_id\",\"case_id\",\"source_digest\",\"start\",\"end\",\"quote\"]}",
	"ServiceObjectAssociation":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"service_object_id\",\"association_version\",\"object_version\",\"material_id\",\"material_version\",\"document_id\",\"parsed_document_id\",\"source_run_id\",\"source_attempt_id\",\"object_ref\",\"facts\",\"basis_refs\",\"confirmed_by\",\"confirmed_at\",\"facts_sha256\",\"created_at\",\"payload_sha256\"]}",
	"ServiceObjectDetail":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"service_object_id\",\"current_version\",\"identity\",\"association\",\"mapping\",\"can_manage\",\"needs_reassociation\"]}",
	"ServiceObjectMappingPin":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"mapping_id\",\"mapping_version\",\"binding_version\",\"object_version\",\"association_version\",\"external_object\",\"effective_from\",\"effective_until\"]}",
	"ServiceObjectVersionPage":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\",\"next_cursor\"]}",
	"ServiceObjectAssociationPage":           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\",\"next_cursor\"]}",
	"ServiceObjectAssociationConfirmRequest": "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"confirmed\",\"expected_object_version\",\"expected_association_version\",\"source\",\"object_ref\",\"facts_sha256\",\"candidate_sha256\"]}",
	"ServiceObjectAssociationPreview":        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"service_object_id\",\"expected_object_version\",\"expected_association_version\",\"run_id\",\"attempt_id\",\"material_id\",\"material_version\",\"document_id\",\"parsed_document_id\",\"candidate_sha256\",\"options\"]}",
	"OperatorMappingChangeCAS":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"expected_mapping_version\",\"expected_binding_version\",\"expected_object_version\",\"expected_association_version\"]}",
	"OperatorMappingChangePatch":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"kind\",\"effective_from\",\"effective_until\"],\"description\":\"contract_association 仅用于 replace_mapping，须由另一主体批准；目标材料、解析和事实在审批时再次校验。\"}",
	"OperatorServiceIdentityPatch":           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"object_type\",\"display_name\",\"business_code\",\"aliases\",\"basis_summary\",\"basis_refs\",\"state\"]}",
	"MaterialAnchor":                         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"kind\",\"material_id\",\"material_version\",\"document_id\",\"parsed_document_id\",\"start\",\"end\",\"quote_digest\"]}",
	"OperatorMappingChangePreview":           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"mapping_id\",\"service_object_id\",\"cas\",\"before\",\"after\",\"before_sha256\",\"after_sha256\",\"impact_sha256\",\"basis_refs\"]}",
	"OperatorMappingAssociationAfter":        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"service_object_id\",\"object_version\",\"association_version\",\"material_id\",\"material_version\",\"document_id\",\"parsed_document_id\",\"source_run_id\",\"source_attempt_id\",\"object_ref\",\"facts\",\"facts_sha256\",\"basis_refs\"]}",
	"OperatorMappingChangeRequest":           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"request_id\",\"case_id\",\"mapping_id\",\"service_object_id\",\"kind\",\"expected_mapping_version\",\"expected_binding_version\",\"expected_object_version\",\"expected_association_version\",\"before\",\"before_sha256\",\"after\",\"after_sha256\",\"patch\",\"patch_sha256\",\"basis_refs\",\"impact_sha256\",\"state\",\"proposed_by\",\"created_at\"]}",
	"OperatorMappingChangeDecision":          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"decision_id\",\"request_id\",\"decision\",\"reason\",\"decided_by\",\"created_at\"]}",
	"ReviewReadiness":                        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"case_id\",\"revision\",\"can_start\",\"material_selection_version\",\"materials\",\"scopes\"]}",
	"ReviewReadinessMaterial":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"identity_kind\",\"material_id\",\"version\",\"document_id\",\"filename\",\"role\",\"purpose\",\"selected\",\"parse\"]}",
	"ReviewReadinessParse":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"status\",\"next_action\"]}",
	"ReviewReadinessScope":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"status\",\"can_start\",\"checks\",\"blockers\"]}",
	"ReviewScopeIdentity":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"rule_set_id\",\"rule_set_version\",\"rule_set_hash\"]}",
	"ReviewReadinessRulePack":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"pack_id\",\"version\",\"snapshot_digest\"]}",
	"ReviewReadinessCheck":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"rule_id\",\"status\",\"missing_dependencies\",\"next_action\"]}",
	"ReviewReadinessBlocker":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"code\",\"status\",\"message\",\"missing_dependencies\",\"next_action\"]}",
	"ReviewStartRequest":                     "{\"type\":\"object\",\"additionalProperties\":false}",
	"ReviewDiagnostics":                      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"case_id\",\"run_id\",\"execution\",\"snapshot\",\"audit_events\",\"documents\",\"evidence\"]}",
	"ExperienceEvidenceRef":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"document_id\",\"parsed_document_id\",\"quote\",\"start\",\"end\"],\"description\":\"位置使用本次审查保存的解析正文中的 Unicode 码点半开区间 [start,end)。normalized_digest 可以省略，服务器会解析并校验；响应不返回该摘要。\"}",
	"ExperienceEvidenceView":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"document_id\",\"parsed_document_id\",\"quote\",\"start\",\"end\"]}",
	"ExperienceFeedbackRequest":              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"run_id\",\"attempt_id\",\"finding_id\",\"decision_id\",\"reason\",\"evidence_refs\"]}",
	"ExperienceFeedback":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"feedback_id\",\"tenant_id\",\"org_id\",\"case_id\",\"run_id\",\"attempt_id\",\"finding_id\",\"decision_id\",\"decision_action\",\"decision_disposition\",\"reason\",\"evidence_refs\",\"applicant_id\",\"created_at\"]}",
	"ExperienceDistillationRequest":          "{\"type\":\"object\",\"additionalProperties\":false}",
	"ExperienceDistillationReceipt":          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"job_id\"]}",
	"ExperienceDistillationStatus":           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"job_id\",\"feedback_id\",\"state\"]}",
	"GovernedKnowledgeRef":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"knowledge_id\",\"version_id\",\"content_sha256\"]}",
	"ExperienceUsageRequest":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"run_id\",\"attempt_id\",\"finding_id\",\"ref\",\"expected_action\"]}",
	"ExperienceUsage":                        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"case_id\",\"run_id\",\"attempt_id\",\"finding_id\",\"ref\",\"snapshot_sha256\",\"input_sha256\",\"expected_action\",\"recorded_by\",\"usage_sha256\",\"recorded_at\"]}",
	"ExperienceUsageOption":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"ref\",\"title\",\"body\",\"summary\",\"check_action\",\"minimum_evidence\",\"reviewed_applicability\",\"reviewed_exclusions\",\"counterexamples\",\"usage\"]}",
	"ExperienceUsageOptions":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\",\"can_record\"]}",
	"ExperienceMetricsSample":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"case_id\",\"run_id\",\"attempt_id\",\"finding_id\",\"input_sha256\",\"snapshot_sha256\",\"usage_sha256\",\"expected_action\",\"decision_id\",\"decision_action\",\"decision_disposition\",\"outcome\",\"used_at\"]}",
	"ExperienceMetrics":                      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"ref\",\"has_been_published\",\"usage_count\",\"pending_count\",\"adopted_count\",\"overturned_count\",\"other_count\",\"samples\"]}",
	"PolicyThresholdCandidate":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"comparator\",\"value\",\"unit\",\"start\",\"end\",\"quote\"]}",
	"PolicyRuleThreshold":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"value\",\"unit\"]}",
	"PolicyRuleComparison":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"rule_id\",\"comparator\",\"rule_threshold\",\"status\"]}",
	"PolicyTextIssue":                        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"start\",\"end\",\"quote\",\"reason\"]}",
	"GovernedPolicyPreview":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"ref\",\"extractor_ref\",\"comparisons\",\"issues\",\"semantic_only\",\"approvable\"]}",
	"GovernedRulePackRef":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"pack_id\",\"version\",\"content_sha256\",\"snapshot_sha256\",\"compiler_ref\",\"pointer_revision\"]}",
	"GovernedKnowledgeSelection":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"ref\",\"kind\",\"source_type\",\"scope\",\"required\",\"summary\",\"valid_from\",\"valid_until\",\"applicability\",\"exclusions\",\"pointer_revision\",\"binding\",\"selection_reason\"]}",
	"ReviewKnowledgeSnapshot":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"encoding\",\"business_at\",\"scope\",\"authorization_revision\",\"rule_pack\",\"required_policies\",\"optional_candidates\",\"excluded\",\"optional_truncated\",\"snapshot_sha256\"]}",
	"FrozenLegalAuthority":                   "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"citation_id\",\"rule_id\",\"authority_source_id\",\"authority_source_version\",\"rulepack_id\",\"rulepack_version\",\"rulepack_content_sha256\",\"outcome\",\"conditions\",\"selected_fact_versions\",\"missing_dependencies\",\"legal_review_status\",\"reviewer\"]}",
	"ContractCitation":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"case_id\",\"run_id\",\"attempt_id\",\"evidence_id\",\"document_id\",\"parsed_document_id\",\"filename\",\"original_digest\",\"parse_digest\",\"start\",\"end\",\"quote\",\"text\",\"material_id\",\"material_version\"]}",
	"FrozenLegalReviewProjection":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"run_id\",\"attempt_id\",\"evidence_cutoff_at\",\"evidence_cutoff_mode\",\"authorities\"]}",
	"ExperienceCandidateContent":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"schema_version\",\"applicability\",\"exclusions\",\"check_action\",\"minimum_evidence\",\"counterexamples\",\"valid_from\",\"valid_until\",\"policy_refs\",\"review_diff\"]}",
	"ExperienceCandidate":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"tenant_id\",\"org_id\",\"case_id\",\"feedback_id\",\"candidate_id\",\"version_id\",\"content_sha256\",\"job_id\",\"generation\",\"input_sha256\",\"result_sha256\",\"model_ref\",\"template_ref\",\"content\",\"created_at\"]}",
	"GovernedKnowledgeScope":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"business_line\",\"scenario\",\"contract_type\"]}",
	"GovernedKnowledgeCondition":             "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"field\",\"equals\"]}",
	"GovernedKnowledgeSourceSpan":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"rule_id\",\"start\",\"end\",\"quote\"]}",
	"GovernedKnowledgeBinding":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"pack_id\",\"pack_version\",\"pack_content_hash\",\"pack_snapshot_digest\",\"compiler_ref\",\"rule_ids\",\"source_spans\",\"candidate_diff\",\"validation_evidence\"]}",
	"GovernedKnowledgeContent":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"knowledge_id\",\"version\",\"kind\",\"title\",\"body\",\"summary\",\"source_type\",\"source_reference\",\"scope\",\"valid_from\",\"valid_until\",\"binding\"]}",
	"GovernedKnowledgeCreateRequest":         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"content\"]}",
	"GovernedKnowledgeCommandRequest":        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"action\",\"expected_revision\",\"expected_content_sha256\",\"reason\"]}",
	"GovernedKnowledgeVersion":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"ref\",\"content\",\"status\",\"revision\",\"cross_case_reviewed\",\"active\",\"pointer_revision\",\"created_by\",\"created_at\"]}",
	"GovernedKnowledgeSummary":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"ref\",\"version\",\"title\",\"kind\",\"source_type\",\"scope\",\"valid_from\",\"valid_until\",\"status\",\"revision\",\"pointer_revision\",\"active\"]}",
	"GovernedKnowledgeList":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\"]}",
	"GovernedKnowledgeHistoryEntry":          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"sequence\",\"ref\",\"action\",\"revision\",\"pointer_revision\",\"actor_id\",\"reason\",\"previous_active\",\"resulting_active\",\"occurred_at\"]}",
	"GovernedKnowledgeHistory":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"items\"]}",
	"CaseProfile":                            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"case_id\",\"number\",\"customer\",\"supplier\",\"contract_category\",\"owner_id\",\"due_at\",\"version\",\"updated_at\"]}",
	"CaseProfileInput":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"number\",\"customer\",\"supplier\",\"contract_category\",\"owner_id\",\"due_at\",\"expected_version\"]}",
	"WorkbenchTask":                          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"case_id\",\"title\",\"kind\",\"assignee_id\",\"due_at\",\"status\",\"priority\",\"finding_id\",\"created_by\",\"version\",\"created_at\",\"updated_at\"]}",
	"WorkbenchSnapshotTask":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"case_id\",\"title\",\"kind\",\"assignee_id\",\"due_at\",\"status\",\"priority\",\"finding_id\",\"created_by\",\"version\",\"created_at\",\"updated_at\"]}",
	"WorkbenchTaskInput":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"title\",\"kind\",\"assignee_id\",\"due_at\",\"status\",\"priority\",\"finding_id\"]}",
	"WorkbenchTaskUpdateInput":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"title\",\"kind\",\"assignee_id\",\"due_at\",\"status\",\"priority\",\"finding_id\",\"expected_version\"]}",
	"CaseRelation":                           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"case_id\",\"related_case_id\",\"reason\",\"created_by\",\"created_at\"]}",
	"CaseRelationInput":                      "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"related_case_id\",\"reason\"]}",
	"Knowledge":                              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"title\",\"version\",\"version_id\",\"body\",\"source_type\",\"effective_at\",\"created_by\",\"created_at\"]}",
	"KnowledgeInput":                         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"title\",\"version\",\"body\",\"source_type\"]}",
	"SeveritySource":                         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"policy_id\",\"policy_version\",\"clause\",\"reason\"]}",
	"WorkbenchDashboard":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"visible_cases\",\"my_pending_tasks\",\"overdue_tasks\",\"pending_review_cases\",\"remediation_cases\",\"completed_reviews\",\"average_duration_ms\",\"progress\",\"risks\"]}",
	"WorkbenchModel":                         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"name\",\"ocr_name\",\"status\",\"label\"]}",
	"WorkbenchTool":                          "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"name\",\"description\",\"capabilities\",\"calls\",\"last_status\"]}",
	"WorkbenchRun":                           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"case_id\",\"case_title\",\"state\",\"created_at\",\"updated_at\",\"attempt_id\",\"started_at\",\"finished_at\",\"usage\"]}",
	"WorkbenchAudit":                         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"case_id\",\"case_title\",\"actor_id\",\"actor_name\",\"action\",\"at\",\"summary\"]}",
	"Workbench":                              "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"schema_version\",\"generated_at\",\"period\",\"dashboard\",\"cases\",\"profiles\",\"tasks\",\"relations\",\"knowledge\",\"members\",\"remediations\",\"reports\",\"documents\",\"runs\",\"audit\",\"templates\",\"tools\",\"model\"]}",
	"WorkbenchCase":                          "{\"type\":\"object\",\"required\":[\"id\",\"title\",\"contract_type\",\"document_count\",\"pending_decisions\",\"latest_run\",\"updated_at\",\"business_line\",\"scenario\",\"business_status\",\"archived_at\",\"profile\",\"severity\",\"progress\"],\"additionalProperties\":false}",
	"WorkbenchDocument":                      "{\"type\":\"object\",\"required\":[\"id\",\"role\",\"filename\",\"media_type\",\"digest\",\"size\",\"parse_status\",\"parse_error\",\"created_at\",\"superseded\",\"parsed_document\",\"case_title\",\"case_id\"],\"additionalProperties\":false}",
	"WorkbenchRemediation":                   "{\"type\":\"object\",\"required\":[\"id\",\"case_id\",\"run_id\",\"attempt_id\",\"finding_ids\",\"measure\",\"status\",\"version\",\"document_ids\",\"reason\",\"created_at\",\"updated_at\",\"case_title\"],\"additionalProperties\":false}",
	"WorkbenchReport":                        "{\"type\":\"object\",\"required\":[\"id\",\"version\",\"run_id\",\"attempt_id\",\"input_hash\",\"output_hash\",\"decision_snapshot_hash\",\"digest\",\"created_at\",\"case_title\",\"case_id\"],\"additionalProperties\":false}",
	"WorkbenchMember":                        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"display_name\",\"role\",\"tenant_id\",\"org_id\"]}",
	"Template":                               "{\"type\":\"object\",\"required\":[\"id\",\"name\",\"version\",\"digest\",\"fields\"],\"additionalProperties\":false}",
	"TemplateField":                          "{\"type\":\"object\",\"required\":[\"key\",\"label\",\"required\",\"type\"],\"additionalProperties\":false}",
	"TemplateList":                           "{\"type\":\"object\",\"required\":[\"items\"],\"additionalProperties\":false}",
	"CreateGenerationRequest":                "{\"type\":\"object\",\"required\":[\"template_id\",\"fields\"],\"additionalProperties\":false}",
	"GenerationRecord":                       "{\"type\":\"object\",\"required\":[\"id\",\"document_id\",\"material_id\",\"version\",\"replayed\",\"template_id\",\"template_version\",\"digest\",\"created_at\"],\"additionalProperties\":false}",
	"Remediation":                            "{\"type\":\"object\",\"required\":[\"id\",\"case_id\",\"run_id\",\"attempt_id\",\"finding_ids\",\"measure\",\"status\",\"version\",\"document_ids\",\"reason\",\"created_at\",\"updated_at\"],\"additionalProperties\":false}",
	"RemediationInput":                       "{\"type\":\"object\",\"required\":[\"run_id\",\"attempt_id\",\"finding_ids\",\"measure\"],\"additionalProperties\":false}",
	"RemediationSubmitInput":                 "{\"type\":\"object\",\"required\":[\"document_ids\",\"reason\",\"expected_version\"],\"additionalProperties\":false}",
	"RemediationVerifyInput":                 "{\"type\":\"object\",\"required\":[\"run_id\",\"attempt_id\",\"reason\",\"expected_version\"],\"additionalProperties\":false}",
	"CreateReportRequest":                    "{\"type\":\"object\",\"required\":[\"run_id\",\"attempt_id\"],\"additionalProperties\":false}",
	"ReportRecord":                           "{\"type\":\"object\",\"required\":[\"id\",\"version\",\"run_id\",\"attempt_id\",\"input_hash\",\"output_hash\",\"decision_snapshot_hash\",\"digest\",\"created_at\"],\"additionalProperties\":false}",
	"ReportDocument":                         "{\"type\":\"object\",\"additionalProperties\":true,\"description\":\"已保存报告的 JSON 原始字节；字节摘要记录在 ReportRecord.digest，字段由生成报告时的版本决定。\"}",
	"ArchiveRequest":                         "{\"type\":\"object\",\"required\":[\"report_id\",\"reason\"],\"additionalProperties\":false}",
	"ArchiveRecord":                          "{\"type\":\"object\",\"required\":[\"id\",\"case_id\",\"report_id\",\"reason\",\"archived_at\"],\"additionalProperties\":false}",
	"EmptyObject":                            "{\"type\":\"object\",\"required\":[],\"additionalProperties\":false,\"properties\":{}}",
	"ID":                                     "{\"type\":\"object\",\"required\":[\"id\"],\"additionalProperties\":false}",
	"Actor":                                  "{\"type\":\"object\",\"required\":[\"id\",\"display_name\",\"role\",\"tenant_id\",\"org_id\"],\"additionalProperties\":false}",
	"Session":                                "{\"type\":\"object\",\"required\":[\"actor\",\"audience\",\"active_context\",\"csrf_token\",\"context_switch_enabled\",\"principal_id\"],\"additionalProperties\":false}",
	"LoginRequest":                           "{\"type\":\"object\",\"required\":[\"username\",\"password\"],\"additionalProperties\":false}",
	"CreateCaseRequest":                      "{\"type\":\"object\",\"required\":[\"title\",\"contract_type\"],\"additionalProperties\":false}",
	"AddMemberRequest":                       "{\"type\":\"object\",\"required\":[\"actor_id\"],\"additionalProperties\":false}",
	"DecisionRequest":                        "{\"type\":\"object\",\"required\":[\"action\",\"reason\",\"expected_version\"],\"additionalProperties\":false}",
	"Error":                                  "{\"type\":\"object\",\"required\":[\"error\"],\"additionalProperties\":false}",
	"RunSummary":                             "{\"type\":\"object\",\"required\":[\"id\",\"state\",\"updated_at\"],\"additionalProperties\":false}",
	"CaseSummary":                            "{\"type\":\"object\",\"required\":[\"id\",\"title\",\"contract_type\",\"document_count\",\"pending_decisions\",\"latest_run\",\"updated_at\",\"business_line\",\"scenario\",\"business_status\",\"archived_at\"],\"additionalProperties\":false}",
	"CaseList":                               "{\"type\":\"object\",\"required\":[\"items\"],\"additionalProperties\":false}",
	"ParsedDocument":                         "{\"type\":\"object\",\"description\":\"每次独立保存的解析结果的元数据。摘要分别对应原件、规范化文本、IR 和解析结果版本；历史来源缺失时，文档的 parsed_document 为 null。\",\"required\":[\"id\",\"document_id\",\"original_digest\",\"parser_id\",\"parser_version\",\"config_hash\",\"normalization_version\",\"normalized_digest\",\"ir_digest\",\"artifact_digest\",\"created_at\",\"status\"],\"additionalProperties\":false}",
	"Document":                               "{\"type\":\"object\",\"required\":[\"id\",\"role\",\"filename\",\"media_type\",\"digest\",\"size\",\"parse_status\",\"parse_error\",\"created_at\",\"superseded\",\"parsed_document\"],\"additionalProperties\":false}",
	"TextOffset":                             "{\"type\":\"object\",\"required\":[\"start\",\"end\"],\"additionalProperties\":false,\"description\":\"规范化文本的 Unicode 码点区间 [start,end)，不是 UTF-8 字节或 JavaScript UTF-16 索引。\"}",
	"Clause":                                 "{\"type\":\"object\",\"required\":[\"id\",\"heading\",\"text_offset\",\"page\"],\"additionalProperties\":false}",
	"ReviewDocument":                         "{\"type\":\"object\",\"required\":[\"id\",\"role\",\"filename\",\"media_type\",\"size\",\"parse_status\",\"parse_error\",\"created_at\",\"superseded\",\"text\",\"clauses\",\"parsed_document\",\"parsed_document_id\"],\"additionalProperties\":false}",
	"FindingCounts":                          "{\"type\":\"object\",\"required\":[\"total\",\"undecided\",\"conflict\",\"insufficient\"],\"additionalProperties\":false}",
	"Activity":                               "{\"type\":\"object\",\"required\":[\"id\",\"at\",\"summary\"],\"additionalProperties\":false}",
	"CaseDetail":                             "{\"type\":\"object\",\"required\":[\"id\",\"title\",\"contract_type\",\"document_count\",\"pending_decisions\",\"latest_run\",\"updated_at\",\"created_at\",\"documents\",\"revision_pending\",\"configuration_pending\",\"subject_context_changed_after_latest_run\",\"finding_counts\",\"activity\",\"members\",\"business_line\",\"scenario\",\"business_status\",\"archived_at\",\"generations\",\"remediations\",\"reports\",\"archives\"],\"additionalProperties\":false}",
	"Anchor":                                 "{\"type\":\"object\",\"required\":[\"id\",\"document_version_id\",\"quote\",\"status\",\"page\",\"text_offset\",\"bbox\",\"element_id\",\"mismatch_reason\",\"parsed_document_id\"],\"additionalProperties\":false}",
	"Decision":                               "{\"type\":\"object\",\"required\":[\"id\",\"action\",\"reason\",\"actor\",\"decided_at\",\"old_disposition\",\"new_disposition\"],\"additionalProperties\":false}",
	"PriorDecision":                          "{\"type\":\"object\",\"required\":[\"id\",\"action\",\"reason\",\"actor\",\"decided_at\"],\"additionalProperties\":false}",
	"MaterialProjection":                     "{\"type\":\"object\",\"required\":[\"material_id\",\"role\",\"purpose\",\"legacy_status\",\"legacy_selected\",\"selected\",\"current_document_id\",\"current_version\"],\"additionalProperties\":false}",
	"MaterialSelectionInput":                 "{\"type\":\"object\",\"required\":[\"version\",\"document_ids\"],\"additionalProperties\":false}",
	"MaterialVersionProjection":              "{\"type\":\"object\",\"required\":[\"material_id\",\"version\",\"document_id\",\"previous_document_id\",\"created_at\"],\"additionalProperties\":false}",
	"MaterialSelectionProjection":            "{\"type\":\"object\",\"required\":[\"version\",\"status\",\"document_ids\",\"candidate_document_ids\",\"primary_material_id\"],\"additionalProperties\":false}",
	"Finding":                                "{\"type\":\"object\",\"required\":[\"id\",\"origin\",\"domain\",\"claim\",\"relation\",\"confidence\",\"evidence\",\"disposition_suggestion\",\"unverified_reason\",\"version\",\"decisions\",\"revision_mark\"],\"additionalProperties\":false}",
	"Run":                                    "{\"type\":\"object\",\"required\":[\"id\",\"state\",\"updated_at\",\"case_id\",\"started_at\",\"partial_reason\",\"failure_message\"],\"additionalProperties\":false}",
	"Snapshot":                               "{\"type\":\"object\",\"required\":[\"document_ids\",\"rule_set_hash\",\"playbook_hash\",\"model_ref\",\"comparator_ref\",\"governed_knowledge\"],\"additionalProperties\":false}",
	"Evaluation":                             "{\"type\":\"object\",\"required\":[\"id\",\"domain\",\"relation\",\"rule_version\",\"reason\",\"finding_id\",\"rule_id\",\"capability_id\",\"reason_code\",\"fact_ids\",\"object_refs\"],\"additionalProperties\":false}",
	"AuditEvent":                             "{\"type\":\"object\",\"required\":[\"event_id\",\"run_id\",\"actor\",\"seq\",\"event_type\",\"prev_hash\",\"hash\",\"correlation_ids\"],\"additionalProperties\":false}",
	"Revision":                               "{\"type\":\"object\",\"required\":[\"mark\",\"finding_id\",\"previous_finding_id\",\"domain\",\"relation\",\"claim\",\"evidence\",\"prior_decision\"],\"additionalProperties\":false}",
	"Workspace":                              "{\"type\":\"object\",\"required\":[\"run\",\"documents\",\"findings\",\"evaluations\",\"snapshot\",\"audit_events\",\"previous_run_id\",\"revision_comparison_reason\",\"revisions\",\"execution\",\"review_attempts\",\"agent_review\",\"business_events\"],\"additionalProperties\":false}",
	"BusinessSnapshot":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"document_ids\",\"adopted_tenant_config\"]}",
	"AdoptedTenantConfigIdentity":            "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"version_id\",\"digest\"]}",
	"BusinessAttempt":                        "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"run_id\",\"ordinal\",\"retry_of_attempt_id\",\"state\",\"accepted_at\",\"started_at\",\"finished_at\",\"history_kind\",\"parsed_document_ids\",\"frozen_result_available\"]}",
	"BusinessAttempts":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"attempt\",\"attempts\"]}",
	"BusinessAuditEvent":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"event_id\",\"run_id\",\"actor\",\"seq\",\"event_type\",\"correlation_ids\"]}",
	"BusinessAgentScope":                     "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"domain\",\"capability_ids\",\"source_ids\"]}",
	"BusinessAgentCapability":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"capability_id\",\"mandatory\",\"status\",\"relation\",\"reason_code\",\"evidence_ids\",\"canonical_evaluation_ids\"]}",
	"BusinessAgentDomain":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"domain\",\"status\",\"capabilities\"]}",
	"BusinessAgentGap":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"gap_id\",\"domain\",\"capability_id\",\"mandatory\",\"status\",\"reason_code\",\"evidence_ids\"]}",
	"BusinessEvidenceAnchor":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"id\",\"document_id\",\"parsed_document_id\",\"quote\",\"status\",\"start\",\"end\",\"reason\",\"page\",\"element_id\"]}",
	"BusinessAgentEvidence":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"evidence_id\",\"semantic_type\",\"value\",\"unit\",\"currency\",\"verification\",\"anchor\"]}",
	"BusinessAgentReview":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"attempt_id\",\"status\",\"rulepack\",\"scope\",\"domains\",\"gaps\",\"evidence\",\"unresolved_gap_ids\",\"observations\"]}",
	"BusinessAgentObservation":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"round\",\"call_id\",\"gap_id\",\"tool_id\",\"reported_relation\",\"reason_code\",\"summary\",\"claim_status\",\"fact_ids\",\"located_evidence_ids\",\"truncated\"]}",
	"Settings":                               "{\"type\":\"object\",\"required\":[\"model\",\"queue\",\"redaction_policy\",\"users\",\"operator_audit\"],\"additionalProperties\":false,\"description\":\"模型探测约5秒；没有真实队列时depth/wait均为null。execution仅管理员可见且只含运维计数；管理员不因此取得合同正文。\"}",
	"StreamEvent":                            "{\"type\":\"object\",\"required\":[\"seq\",\"type\",\"state\"],\"additionalProperties\":false}",
	"Health":                                 "{\"type\":\"object\",\"required\":[\"status\"],\"additionalProperties\":false,\"description\":\"仅进程存活检查；不检查数据库、RustFS、模型或生产就绪。\"}",
	"OpenAPIDocument":                        "{\"type\":\"object\",\"required\":[\"openapi\",\"info\",\"paths\",\"components\"]}",
	"ReviewAccepted":                         "{\"type\":\"object\",\"required\":[\"id\",\"attempt_id\"],\"additionalProperties\":false}",
	"NewReviewJobAccepted":                   "{\"type\":\"object\",\"required\":[\"id\",\"attempt_id\",\"job_id\"],\"additionalProperties\":false}",
	"ExecutionUsage":                         "{\"type\":\"object\",\"required\":[\"input_tokens\",\"output_tokens\",\"total_tokens\",\"calls\",\"unknown_calls\",\"sources\",\"known_subtotals\",\"cost\"],\"additionalProperties\":false}",
	"ReviewAttempt":                          "{\"type\":\"object\",\"required\":[\"id\",\"run_id\",\"ordinal\",\"retry_of_attempt_id\",\"state\",\"accepted_at\",\"started_at\",\"finished_at\",\"input_hash\",\"output_hash\",\"error_code\",\"history_kind\",\"parsed_document_ids\"],\"additionalProperties\":false}",
	"ExecutionCallUsage":                     "{\"type\":\"object\",\"description\":\"单次物理模型调用实际响应的用量；source为来源，不包含跨调用或跨attempt的汇总字段。\",\"required\":[\"source\",\"input_tokens\",\"output_tokens\",\"total_tokens\"],\"additionalProperties\":false}",
	"ExecutionEvent":                         "{\"type\":\"object\",\"required\":[\"seq\",\"attempt_id\",\"span_id\",\"parent_span_id\",\"node_kind\",\"phase\",\"event_type\",\"at\",\"duration_ms\",\"status\",\"error_code\",\"model_ref\",\"usage\"],\"additionalProperties\":false}",
	"ModelCallTrace":                         "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"requested_model\",\"status\",\"started_at\",\"finished_at\",\"request_sha256\",\"request_bytes\",\"request_messages\",\"request_truncated\",\"response_bytes\",\"response_truncated\"]}",
	"ModelCallTraceMessage":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"role\",\"content\",\"truncated\"]}",
	"Execution":                              "{\"type\":\"object\",\"required\":[\"schema_version\",\"attempt\",\"attempts\",\"events\",\"usage\",\"all_attempts_usage\",\"first_verified_finding_ms\",\"duration_ms\",\"queue_wait_ms\",\"agent_review\"],\"additionalProperties\":false}",
	"RulePackSummary":                        "{\"type\":\"object\",\"required\":[\"id\",\"version\",\"tenant_id\",\"status\",\"revision\",\"content_hash\",\"snapshot_sha256\",\"effective_from\",\"effective_until\",\"authority_sources\",\"is_active\",\"active_pointer_revision\"],\"additionalProperties\":false}",
	"RulePackDetail":                         "{\"type\":\"object\",\"required\":[\"pack\",\"manifest\"],\"additionalProperties\":false}",
	"RulePackList":                           "{\"type\":\"object\",\"required\":[\"items\"],\"additionalProperties\":false}",
	"RulePackHistory":                        "{\"type\":\"object\",\"required\":[\"items\"],\"additionalProperties\":false}",
	"RulePackHistoryEntry":                   "{\"type\":\"object\",\"required\":[\"sequence\",\"pack_id\",\"version\",\"action\",\"actor_id\",\"content_hash\",\"pointer_revision\",\"created_at\",\"previous_version\",\"previous_active\",\"resulting_version\",\"resulting_active\"],\"additionalProperties\":false}",
	"RulePackCreateRequest":                  "{\"type\":\"object\",\"required\":[\"manifest\"],\"additionalProperties\":false}",
	"RulePackEditRequest":                    "{\"type\":\"object\",\"required\":[\"expected_revision\",\"manifest\"],\"additionalProperties\":false}",
	"RulePackRevisionRequest":                "{\"type\":\"object\",\"required\":[\"expected_revision\"],\"additionalProperties\":false}",
	"RulePackApproveRequest":                 "{\"type\":\"object\",\"required\":[\"expected_revision\",\"expected_content_hash\"],\"additionalProperties\":false}",
	"RulePackPointerRequest":                 "{\"type\":\"object\",\"required\":[\"expected_revision\",\"expected_pointer_revision\"],\"additionalProperties\":false}",
	"RulePackRollbackRequest":                "{\"type\":\"object\",\"required\":[\"version\",\"expected_pointer_revision\"],\"additionalProperties\":false}",
	"RulePackTestSource":                     "{\"type\":\"object\",\"required\":[\"id\",\"version\",\"role\",\"text\"],\"additionalProperties\":false}",
	"RulePackTestCitation":                   "{\"type\":\"object\",\"required\":[\"source_id\",\"source_version\",\"start\",\"end\",\"quote\"],\"additionalProperties\":false}",
	"RulePackTestFact":                       "{\"type\":\"object\",\"required\":[\"id\",\"rule_id\",\"dimension\",\"object_ref\",\"value\",\"unit\",\"source_role\",\"citations\"],\"additionalProperties\":false}",
	"RulePackFixtureSuite":                   "{\"type\":\"object\",\"description\":\"随规则包版本和内容摘要冻结的合成数据；suite ID 可与能力包 fixture_suite 字符串引用对应。\",\"required\":[\"schema_version\",\"id\",\"pack_id\",\"pack_version\",\"data_class\",\"cases\"],\"additionalProperties\":false}",
	"RulePackFixtureCase":                    "{\"type\":\"object\",\"required\":[\"id\",\"title\",\"kind\",\"input\",\"expected\"],\"additionalProperties\":false}",
	"RulePackFixtureInput":                   "{\"type\":\"object\",\"required\":[\"business_line\",\"scenario\",\"contract_type\",\"at\",\"sources\",\"facts\"],\"additionalProperties\":false}",
	"RulePackFixtureExpected":                "{\"type\":\"object\",\"required\":[\"rule_id\",\"rule_version\",\"relation\",\"reason_code\",\"citations\"],\"additionalProperties\":false}",
	"RulePackFixtureResponse":                "{\"type\":\"object\",\"required\":[\"revision\",\"content_hash\",\"suite\"],\"dependentRequired\":{\"comparison\":[\"compare_revision\"],\"compare_revision\":[\"comparison\"]},\"additionalProperties\":false}",
	"RulePackFixtureRun":                     "{\"type\":\"object\",\"required\":[\"suite_id\",\"pack_id\",\"pack_version\",\"pack_hash\",\"passed\",\"cases\"],\"additionalProperties\":false}",
	"RulePackFixtureChange":                  "{\"type\":\"object\",\"required\":[\"case_id\"],\"additionalProperties\":false}",
	"RulePackTestRequest":                    "{\"type\":\"object\",\"description\":\"合成样例试算输入。只含用户显式提供的合成来源与事实，服务端派生租户、案件/组织身份与来源摘要；不接受 principal、grant 或案件查询。\",\"required\":[\"expected_revision\"],\"dependentRequired\":{\"compare_version\":[\"compare_expected_revision\",\"compare_expected_hash\"],\"compare_expected_revision\":[\"compare_version\",\"compare_expected_hash\"],\"compare_expected_hash\":[\"compare_version\",\"compare_expected_revision\"]},\"oneOf\":[{\"required\":[\"business_line\",\"scenario\",\"contract_type\",\"at\",\"sources\",\"facts\"],\"properties\":{\"run_fixture_suite\":{\"const\":false}},\"not\":{\"anyOf\":[{\"required\":[\"expected_content_hash\"]},{\"required\":[\"compare_version\"]}]}},{\"required\":[\"run_fixture_suite\",\"expected_content_hash\"],\"properties\":{\"run_fixture_suite\":{\"const\":true}},\"not\":{\"anyOf\":[{\"required\":[\"business_line\"]},{\"required\":[\"scenario\"]},{\"required\":[\"contract_type\"]},{\"required\":[\"at\"]},{\"required\":[\"sources\"]},{\"required\":[\"facts\"]}]}}],\"additionalProperties\":false}",
	"RulePackTestResult":                     "{\"type\":\"object\",\"required\":[\"revision\",\"content_hash\",\"report\"],\"additionalProperties\":false}",
	"AgentReviewLimits":                      "{\"type\":\"object\",\"required\":[\"max_rounds\",\"max_calls\",\"max_duration_ms\",\"max_evidence_bytes\"],\"additionalProperties\":false}",
	"AgentReviewPlanner":                     "{\"type\":\"object\",\"required\":[\"identity\",\"configuration_id\",\"model_configuration_id\",\"prompt_version\",\"prompt_sha256\",\"schema_version\",\"runtime_version\",\"tool_catalog_version\",\"fallback_used\",\"fallback_code\",\"limits\"],\"additionalProperties\":false}",
	"AgentReviewRulePackIdentity":            "{\"type\":\"object\",\"required\":[\"id\",\"version\",\"content_sha256\"],\"additionalProperties\":false}",
	"AgentReviewSourceSnapshot":              "{\"type\":\"object\",\"required\":[\"document_ids\",\"parsed_document_ids\",\"parse_selection_hash\",\"parser_configuration_id\"],\"additionalProperties\":false}",
	"AgentReviewDomainScope":                 "{\"type\":\"object\",\"required\":[\"domain\",\"capability_ids\",\"source_ids\"],\"additionalProperties\":false}",
	"AgentReviewCapability":                  "{\"type\":\"object\",\"required\":[\"capability_id\",\"status\",\"relation\",\"reason_code\",\"evidence_ids\",\"canonical_evaluation_ids\"],\"additionalProperties\":false}",
	"AgentReviewDomainCoverage":              "{\"type\":\"object\",\"required\":[\"domain\",\"status\",\"capabilities\"],\"additionalProperties\":false}",
	"AgentReviewGap":                         "{\"type\":\"object\",\"required\":[\"gap_id\",\"domain\",\"capability_id\",\"status\",\"reason_code\",\"evidence_ids\"],\"additionalProperties\":false}",
	"AgentReviewCall":                        "{\"type\":\"object\",\"required\":[\"call_id\",\"tool_id\",\"tool_version\",\"domain\",\"capability_id\",\"gap_id\",\"status\",\"reason_code\",\"evidence_ids\"],\"additionalProperties\":false}",
	"AgentReviewRound":                       "{\"type\":\"object\",\"required\":[\"round\",\"status\",\"gap_ids\",\"calls\",\"new_evidence_ids\",\"resolved_gap_ids\",\"termination_code\"],\"additionalProperties\":false}",
	"AgentReviewPlan":                        "{\"type\":\"object\",\"required\":[\"status\",\"rejection_code\",\"calls\",\"rounds\"],\"additionalProperties\":false}",
	"AgentReviewAnchor":                      "{\"type\":\"object\",\"required\":[\"id\",\"document_id\",\"parsed_document_id\",\"normalized_digest\",\"quote\",\"status\",\"start\",\"end\",\"reason\",\"page\",\"element_id\"],\"additionalProperties\":false}",
	"AgentReviewEvidence":                    "{\"type\":\"object\",\"required\":[\"evidence_id\",\"semantic_type\",\"value\",\"unit\",\"currency\",\"verification\",\"anchor\"],\"additionalProperties\":false}",
	"AgentReviewUsage":                       "{\"type\":\"object\",\"required\":[\"planner_calls\",\"input_tokens\",\"output_tokens\",\"total_tokens\"],\"additionalProperties\":false}",
	"AgentReviewEvent":                       "{\"type\":\"object\",\"required\":[\"sequence\",\"round\",\"kind\",\"call_id\",\"domain\",\"code\",\"duration_ms\"],\"additionalProperties\":false}",
	"AgentReviewObservation":                 "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"round\",\"call_id\",\"gap_id\",\"tool_id\",\"reported_relation\",\"summary\",\"claim_status\",\"fact_ids\",\"located_evidence_ids\",\"truncated\"]}",
	"AgentReviewKnowledgeRead":               "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"knowledge_id\",\"version_id\",\"content_sha256\",\"start\",\"end\",\"text_sha256\",\"text\"]}",
	"AgentKnowledgeSelection":                "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"snapshot_sha256\",\"algorithm\",\"policy_status\",\"policy_reason\",\"input\",\"candidates\",\"budget\",\"planner_rounds\",\"reads\"]}",
	"KnowledgeReadIdentity":                  "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"knowledge_id\",\"version_id\",\"content_sha256\",\"text_sha256\",\"start\",\"end\"]}",
	"KnowledgeReadRecord":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"knowledge_id\",\"version_id\",\"content_sha256\",\"round\",\"start\",\"end\",\"bytes\",\"status\",\"text_sha256\"]}",
	"AgentReview":                            "{\"type\":\"object\",\"required\":[\"schema_version\",\"attempt_id\",\"status\",\"mode\",\"source_snapshot\",\"rulepack\",\"planner\",\"scope\",\"domains\",\"gaps\",\"plan\",\"evidence\",\"usage\",\"unresolved_gap_ids\",\"events\"],\"additionalProperties\":false}",
	"RulePackLifecycle":                      "{\"type\":\"object\",\"required\":[\"state\"],\"additionalProperties\":false}",
	"RulePackVerification":                   "{\"type\":\"object\",\"required\":[\"manifest_hash\",\"verifier_version\",\"passed\"],\"additionalProperties\":false}",
	"RulePackApproval":                       "{\"type\":\"object\",\"required\":[\"manifest_hash\",\"verification_hash\",\"actor_id\"],\"additionalProperties\":false}",
	"RulePackRelease":                        "{\"type\":\"object\",\"required\":[\"manifest_hash\",\"approval_hash\",\"release_hash\"],\"additionalProperties\":false}",
	"RulePackManifestInput":                  "{\"type\":\"object\",\"description\":\"严格遵循 rulepack/v1 的请求 manifest。创建时可以省略 lifecycle 或只填写 draft；服务器确定租户和操作者，不接受客户端提交的校验、批准或发布记录。rules[].required_fact.exclusions 对 monthly_sla 必须是字符串数组（可以为空）；对非 SLA 比较器为 null（不适用）。响应中保存的版本见 RulePackSnapshot。\",\"required\":[\"schema_version\",\"id\",\"version\",\"tenant_id\",\"effective_from\",\"effective_until\",\"rules\"],\"additionalProperties\":false}",
	"RulePackManifestSnapshot":               "{\"type\":\"object\",\"description\":\"已保存的规则包版本。响应返回原样保存的 manifest 和生命周期证明，不能作为客户端请求体提交。rules[].required_fact.exclusions 对 monthly_sla 是字符串数组（可以为空）；对非 SLA 比较器为 null（不适用）。\",\"required\":[\"schema_version\",\"id\",\"version\",\"tenant_id\",\"effective_from\",\"effective_until\",\"rules\",\"lifecycle\"],\"additionalProperties\":false}",
	"RulePackReportCitation":                 "{\"type\":\"object\",\"required\":[\"source_id\",\"source_version\",\"tenant_id\",\"case_id\",\"source_digest\",\"start\",\"end\",\"quote\"],\"additionalProperties\":false}",
	"RulePackReportResult":                   "{\"type\":\"object\",\"required\":[\"rule_id\",\"rule_version\",\"domain\",\"capability_id\",\"mandatory\",\"relation\",\"reason_code\"],\"additionalProperties\":false}",
	"RulePackReport":                         "{\"type\":\"object\",\"required\":[\"pack_id\",\"pack_version\",\"pack_hash\",\"relations\",\"results\"],\"additionalProperties\":false}",
	"SystemHealth":                           "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"schema_version\",\"observed_at\",\"mode\",\"source\",\"cluster\",\"services\",\"architecture\"]}",
	"SystemHealthCluster":                    "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"status\",\"namespace\",\"observed_at\",\"detail\",\"nodes\"]}",
	"SystemHealthNode":                       "{\"type\":\"object\",\"additionalProperties\":false,\"required\":[\"name\",\"status\",\"version\"]}",
	"LLMEndpointView":                        "{\"type\":\"object\",\"required\":[\"configured\",\"name\",\"protocol\",\"base_url\",\"models\",\"review_model\",\"ocr_model\",\"has_api_key\",\"revision\",\"updated_at\",\"updated_by\"],\"additionalProperties\":false,\"description\":\"平台端点视图。api_key、secret_ciphertext 不在任何响应中出现；未配置时 revision=0，已保存时从 1 开始。\"}",
	"LLMEndpointProbeRequest":                "{\"type\":\"object\",\"required\":[\"protocol\",\"base_url\"],\"additionalProperties\":false}",
	"LLMEndpointProbeResult":                 "{\"type\":\"object\",\"required\":[\"models\",\"latency_ms\"],\"additionalProperties\":false}",
	"LLMEndpointInput":                       "{\"type\":\"object\",\"required\":[\"name\",\"protocol\",\"base_url\",\"models\"],\"additionalProperties\":false,\"description\":\"省略 review_model/ocr_model 时，仅保留仍存在于新模型列表中的旧选择；否则端点保存但不启用。\"}",
	"LLMModelSelectionInput":                 "{\"type\":\"object\",\"required\":[\"review_model\",\"ocr_model\"],\"additionalProperties\":false}",
	"TenantMoneyWindow":                      "{\"type\":\"object\",\"required\":[\"period_key\",\"limit_minor\",\"reserved_minor\",\"settled_minor\",\"unknown_hold_minor\",\"exposure_minor\"],\"additionalProperties\":false}",
	"TenantMoneyPolicy":                      "{\"type\":\"object\",\"required\":[\"schema_version\",\"tenant_id\",\"revision\",\"configured\",\"currency\",\"minor_unit_scale\",\"timezone\",\"price_state\",\"day_limit_minor\",\"month_limit_minor\",\"day\",\"month\"],\"additionalProperties\":false}",
	"TenantMoneyPolicyInput":                 "{\"type\":\"object\",\"required\":[\"expected_revision\",\"day_limit_minor\",\"month_limit_minor\"],\"additionalProperties\":false}",
	"PlatformModelPriceInput":                "{\"type\":\"object\",\"required\":[\"provider_config_id\",\"model_ref\",\"input_micro_usd_per_million\",\"output_micro_usd_per_million\"],\"additionalProperties\":false}",
	"PlatformModelMoneyPolicyInput":          "{\"type\":\"object\",\"required\":[\"expected_revision\",\"global_day_limit_minor\",\"global_month_limit_minor\",\"prices\"],\"additionalProperties\":false}",
	"PlatformModelMoneyPolicyResult":         "{\"type\":\"object\",\"required\":[\"schema_version\",\"revision\",\"prices_added\"],\"additionalProperties\":false}",
	"PlatformModelMoneyPolicyView":           "{\"type\":\"object\",\"required\":[\"schema_version\",\"revision\",\"global_day_limit_minor\",\"global_month_limit_minor\",\"price_snapshot_count\"],\"additionalProperties\":false}",
	"TenantMoneyAdjustmentInput":             "{\"type\":\"object\",\"required\":[\"lease_id\",\"kind\",\"amount_minor\",\"idempotency_key\",\"reason\"],\"additionalProperties\":false}",
	"TenantMoneyAdjustmentResult":            "{\"type\":\"object\",\"required\":[\"event_id\",\"lease_id\",\"kind\",\"amount_minor\"],\"additionalProperties\":false}",
}

func Types() map[string]reflect.Type {
	types := map[string]reflect.Type{
		"SupportGrant":                           reflect.TypeOf((*SupportGrant)(nil)).Elem(),
		"SupportGrantList":                       reflect.TypeOf((*SupportGrantList)(nil)).Elem(),
		"SupportGrantRequest":                    reflect.TypeOf((*SupportGrantRequest)(nil)).Elem(),
		"SupportGrantCommand":                    reflect.TypeOf((*SupportGrantCommand)(nil)).Elem(),
		"SupportCaseView":                        reflect.TypeOf((*SupportCaseView)(nil)).Elem(),
		"ActiveContext":                          reflect.TypeOf((*ActiveContext)(nil)).Elem(),
		"ContextList":                            reflect.TypeOf((*ContextList)(nil)).Elem(),
		"ContextSwitchRequest":                   reflect.TypeOf((*ContextSwitchRequest)(nil)).Elem(),
		"MembershipChoice":                       reflect.TypeOf((*MembershipChoice)(nil)).Elem(),
		"PendingMembershipList":                  reflect.TypeOf((*PendingMembershipList)(nil)).Elem(),
		"PendingMembershipSelectionRequest":      reflect.TypeOf((*PendingMembershipSelectionRequest)(nil)).Elem(),
		"ContractAuditGateReason":                reflect.TypeOf((*ContractAuditGateReason)(nil)).Elem(),
		"ContractAuditBindRequest":               reflect.TypeOf((*ContractAuditBindRequest)(nil)).Elem(),
		"ContractAuditBinding":                   reflect.TypeOf((*ContractAuditBinding)(nil)).Elem(),
		"ContractAuditBindingList":               reflect.TypeOf((*ContractAuditBindingList)(nil)).Elem(),
		"ApprovalInboxItem":                      reflect.TypeOf((*ApprovalInboxItem)(nil)).Elem(),
		"ApprovalInboxPage":                      reflect.TypeOf((*ApprovalInboxPage)(nil)).Elem(),
		"ApprovalRoundReadSummary":               reflect.TypeOf((*ApprovalRoundReadSummary)(nil)).Elem(),
		"ApprovalReadTask":                       reflect.TypeOf((*ApprovalReadTask)(nil)).Elem(),
		"ApprovalReadDecision":                   reflect.TypeOf((*ApprovalReadDecision)(nil)).Elem(),
		"ApprovalRoundReadDetail":                reflect.TypeOf((*ApprovalRoundReadDetail)(nil)).Elem(),
		"ApprovalTaskReadDetail":                 reflect.TypeOf((*ApprovalTaskReadDetail)(nil)).Elem(),
		"ApprovalRoundHistoryItem":               reflect.TypeOf((*ApprovalRoundHistoryItem)(nil)).Elem(),
		"ApprovalRoundHistoryPage":               reflect.TypeOf((*ApprovalRoundHistoryPage)(nil)).Elem(),
		"ContractLedgerFilter":                   reflect.TypeOf((*ContractLedgerFilter)(nil)).Elem(),
		"ContractLedgerItem":                     reflect.TypeOf((*ContractLedgerItem)(nil)).Elem(),
		"ContractLedgerPage":                     reflect.TypeOf((*ContractLedgerPage)(nil)).Elem(),
		"ContractSavedView":                      reflect.TypeOf((*ContractSavedView)(nil)).Elem(),
		"ContractSavedViewList":                  reflect.TypeOf((*ContractSavedViewList)(nil)).Elem(),
		"ContractSavedViewRequest":               reflect.TypeOf((*ContractSavedViewRequest)(nil)).Elem(),
		"ContractSavedViewReplaceRequest":        reflect.TypeOf((*ContractSavedViewReplaceRequest)(nil)).Elem(),
		"ContractSavedViewDeleteResult":          reflect.TypeOf((*ContractSavedViewDeleteResult)(nil)).Elem(),
		"ContractExportRequest":                  reflect.TypeOf((*ContractExportRequest)(nil)).Elem(),
		"ContractExport":                         reflect.TypeOf((*ContractExport)(nil)).Elem(),
		"DraftContent":                           reflect.TypeOf((*DraftContent)(nil)).Elem(),
		"ContractBasics":                         reflect.TypeOf((*ContractBasics)(nil)).Elem(),
		"DraftBuyer":                             reflect.TypeOf((*DraftBuyer)(nil)).Elem(),
		"DraftSupplier":                          reflect.TypeOf((*DraftSupplier)(nil)).Elem(),
		"DraftItem":                              reflect.TypeOf((*DraftItem)(nil)).Elem(),
		"DraftAssetPlan":                         reflect.TypeOf((*DraftAssetPlan)(nil)).Elem(),
		"DraftFunding":                           reflect.TypeOf((*DraftFunding)(nil)).Elem(),
		"DraftPaymentPlan":                       reflect.TypeOf((*DraftPaymentPlan)(nil)).Elem(),
		"DraftDeliveryPlan":                      reflect.TypeOf((*DraftDeliveryPlan)(nil)).Elem(),
		"DraftAcceptancePlan":                    reflect.TypeOf((*DraftAcceptancePlan)(nil)).Elem(),
		"PaymentCondition":                       reflect.TypeOf((*PaymentCondition)(nil)).Elem(),
		"DueRule":                                reflect.TypeOf((*DueRule)(nil)).Elem(),
		"PartyRef":                               reflect.TypeOf((*PartyRef)(nil)).Elem(),
		"UnitRef":                                reflect.TypeOf((*UnitRef)(nil)).Elem(),
		"ProcurementRef":                         reflect.TypeOf((*ProcurementRef)(nil)).Elem(),
		"Attribute":                              reflect.TypeOf((*Attribute)(nil)).Elem(),
		"MaterialRef":                            reflect.TypeOf((*MaterialRef)(nil)).Elem(),
		"ContractSourceCapability":               reflect.TypeOf((*ContractSourceCapability)(nil)).Elem(),
		"ContractSourceObservationRequest":       reflect.TypeOf((*ContractSourceObservationRequest)(nil)).Elem(),
		"ContractSourceObservationResult":        reflect.TypeOf((*ContractSourceObservationResult)(nil)).Elem(),
		"SourceRef":                              reflect.TypeOf((*SourceRef)(nil)).Elem(),
		"ContractSummary":                        reflect.TypeOf((*ContractSummary)(nil)).Elem(),
		"SigningJSONValue":                       reflect.TypeOf((*SigningJSONValue)(nil)).Elem(),
		"ContractSigningParty":                   reflect.TypeOf((*ContractSigningParty)(nil)).Elem(),
		"ContractDeliveryRequest":                reflect.TypeOf((*ContractDeliveryRequest)(nil)).Elem(),
		"ContractDelivery":                       reflect.TypeOf((*ContractDelivery)(nil)).Elem(),
		"ContractFulfillmentItemSummary":         reflect.TypeOf((*ContractFulfillmentItemSummary)(nil)).Elem(),
		"ContractLifecycleOverview":              reflect.TypeOf((*ContractLifecycleOverview)(nil)).Elem(),
		"ContractDeliveryList":                   reflect.TypeOf((*ContractDeliveryList)(nil)).Elem(),
		"ContractAcceptanceDefectInput":          reflect.TypeOf((*ContractAcceptanceDefectInput)(nil)).Elem(),
		"ContractAcceptanceDefect":               reflect.TypeOf((*ContractAcceptanceDefect)(nil)).Elem(),
		"ContractAcceptanceRequest":              reflect.TypeOf((*ContractAcceptanceRequest)(nil)).Elem(),
		"ContractRectificationRequest":           reflect.TypeOf((*ContractRectificationRequest)(nil)).Elem(),
		"ContractRectification":                  reflect.TypeOf((*ContractRectification)(nil)).Elem(),
		"ContractAcceptance":                     reflect.TypeOf((*ContractAcceptance)(nil)).Elem(),
		"ContractAcceptanceList":                 reflect.TypeOf((*ContractAcceptanceList)(nil)).Elem(),
		"ContractSigningCheckRequest":            reflect.TypeOf((*ContractSigningCheckRequest)(nil)).Elem(),
		"ContractSigningFieldChange":             reflect.TypeOf((*ContractSigningFieldChange)(nil)).Elem(),
		"ContractSigningRestrictedChange":        reflect.TypeOf((*ContractSigningRestrictedChange)(nil)).Elem(),
		"ManualSigningComparison":                reflect.TypeOf((*ManualSigningComparison)(nil)).Elem(),
		"ContractSigningCheckResult":             reflect.TypeOf((*ContractSigningCheckResult)(nil)).Elem(),
		"ContractSigningRegistrationRequest":     reflect.TypeOf((*ContractSigningRegistrationRequest)(nil)).Elem(),
		"ContractSigningRegistration":            reflect.TypeOf((*ContractSigningRegistration)(nil)).Elem(),
		"ContractSigningRegistrationList":        reflect.TypeOf((*ContractSigningRegistrationList)(nil)).Elem(),
		"ContractSupplementRequest":              reflect.TypeOf((*ContractSupplementRequest)(nil)).Elem(),
		"ContractSupplement":                     reflect.TypeOf((*ContractSupplement)(nil)).Elem(),
		"ContractSupplementResult":               reflect.TypeOf((*ContractSupplementResult)(nil)).Elem(),
		"ContractSupplementList":                 reflect.TypeOf((*ContractSupplementList)(nil)).Elem(),
		"ContractDetail":                         reflect.TypeOf((*ContractDetail)(nil)).Elem(),
		"ContractVersionFreezeRequest":           reflect.TypeOf((*ContractVersionFreezeRequest)(nil)).Elem(),
		"ContractVersion":                        reflect.TypeOf((*ContractVersion)(nil)).Elem(),
		"ContractVersionFreezeResult":            reflect.TypeOf((*ContractVersionFreezeResult)(nil)).Elem(),
		"ContractVersionList":                    reflect.TypeOf((*ContractVersionList)(nil)).Elem(),
		"ContractPaymentEligibilityRequest":      reflect.TypeOf((*ContractPaymentEligibilityRequest)(nil)).Elem(),
		"ContractPaymentEligibility":             reflect.TypeOf((*ContractPaymentEligibility)(nil)).Elem(),
		"ContractPaymentReceiptRequest":          reflect.TypeOf((*ContractPaymentReceiptRequest)(nil)).Elem(),
		"ContractPaymentReceipt":                 reflect.TypeOf((*ContractPaymentReceipt)(nil)).Elem(),
		"ContractPaymentSummary":                 reflect.TypeOf((*ContractPaymentSummary)(nil)).Elem(),
		"ContractAssetHandoverRequest":           reflect.TypeOf((*ContractAssetHandoverRequest)(nil)).Elem(),
		"ContractAssetReceiptRequest":            reflect.TypeOf((*ContractAssetReceiptRequest)(nil)).Elem(),
		"ContractAssetReceipt":                   reflect.TypeOf((*ContractAssetReceipt)(nil)).Elem(),
		"ContractAssetHandover":                  reflect.TypeOf((*ContractAssetHandover)(nil)).Elem(),
		"ContractAssetPlanSummary":               reflect.TypeOf((*ContractAssetPlanSummary)(nil)).Elem(),
		"ContractAssetList":                      reflect.TypeOf((*ContractAssetList)(nil)).Elem(),
		"ContractVersionMaterial":                reflect.TypeOf((*ContractVersionMaterial)(nil)).Elem(),
		"ContractVersionDetail":                  reflect.TypeOf((*ContractVersionDetail)(nil)).Elem(),
		"ContractVersionFieldChange":             reflect.TypeOf((*ContractVersionFieldChange)(nil)).Elem(),
		"ContractVersionRestrictedChange":        reflect.TypeOf((*ContractVersionRestrictedChange)(nil)).Elem(),
		"ContractVersionDiffSummary":             reflect.TypeOf((*ContractVersionDiffSummary)(nil)).Elem(),
		"ContractVersionDiff":                    reflect.TypeOf((*ContractVersionDiff)(nil)).Elem(),
		"ApprovalDecisionRequest":                reflect.TypeOf((*ApprovalDecisionRequest)(nil)).Elem(),
		"ApprovalCredentialRequest":              reflect.TypeOf((*ApprovalCredentialRequest)(nil)).Elem(),
		"ApprovalCredentialClaims":               reflect.TypeOf((*ApprovalCredentialClaims)(nil)).Elem(),
		"ApprovalCredentialResult":               reflect.TypeOf((*ApprovalCredentialResult)(nil)).Elem(),
		"ApprovalTransferRequest":                reflect.TypeOf((*ApprovalTransferRequest)(nil)).Elem(),
		"ApprovalTransferResult":                 reflect.TypeOf((*ApprovalTransferResult)(nil)).Elem(),
		"ApprovalDecisionResult":                 reflect.TypeOf((*ApprovalDecisionResult)(nil)).Elem(),
		"ApprovalSubmissionRequest":              reflect.TypeOf((*ApprovalSubmissionRequest)(nil)).Elem(),
		"ApprovalRound":                          reflect.TypeOf((*ApprovalRound)(nil)).Elem(),
		"ApprovalTask":                           reflect.TypeOf((*ApprovalTask)(nil)).Elem(),
		"ApprovalSubmissionResult":               reflect.TypeOf((*ApprovalSubmissionResult)(nil)).Elem(),
		"WorkflowTemplateNodeInput":              reflect.TypeOf((*WorkflowTemplateNodeInput)(nil)).Elem(),
		"WorkflowTemplateNode":                   reflect.TypeOf((*WorkflowTemplateNode)(nil)).Elem(),
		"WorkflowTemplatePublishRequest":         reflect.TypeOf((*WorkflowTemplatePublishRequest)(nil)).Elem(),
		"WorkflowTemplate":                       reflect.TypeOf((*WorkflowTemplate)(nil)).Elem(),
		"WorkflowRoleBindingInput":               reflect.TypeOf((*WorkflowRoleBindingInput)(nil)).Elem(),
		"WorkflowRoleReplaceRequest":             reflect.TypeOf((*WorkflowRoleReplaceRequest)(nil)).Elem(),
		"WorkflowRoleBinding":                    reflect.TypeOf((*WorkflowRoleBinding)(nil)).Elem(),
		"WorkflowRole":                           reflect.TypeOf((*WorkflowRole)(nil)).Elem(),
		"ContractDraftSummary":                   reflect.TypeOf((*ContractDraftSummary)(nil)).Elem(),
		"ContractDraftList":                      reflect.TypeOf((*ContractDraftList)(nil)).Elem(),
		"ContractDraftCreateRequest":             reflect.TypeOf((*ContractDraftCreateRequest)(nil)).Elem(),
		"ContractDraftReplaceRequest":            reflect.TypeOf((*ContractDraftReplaceRequest)(nil)).Elem(),
		"ContractLegacyImportRequest":            reflect.TypeOf((*ContractLegacyImportRequest)(nil)).Elem(),
		"PartyReferenceSnapshot":                 reflect.TypeOf((*PartyReferenceSnapshot)(nil)).Elem(),
		"ProcurementSourceSnapshot":              reflect.TypeOf((*ProcurementSourceSnapshot)(nil)).Elem(),
		"ContractSubjectReference":               reflect.TypeOf((*ContractSubjectReference)(nil)).Elem(),
		"ContractMaterialReference":              reflect.TypeOf((*ContractMaterialReference)(nil)).Elem(),
		"ContractReferenceCheck":                 reflect.TypeOf((*ContractReferenceCheck)(nil)).Elem(),
		"DirectoryStatusSnapshot":                reflect.TypeOf((*DirectoryStatusSnapshot)(nil)).Elem(),
		"ModelBudgetCapacity":                    reflect.TypeOf((*ModelBudgetCapacity)(nil)).Elem(),
		"ModelBudgetGatewayStatus":               reflect.TypeOf((*ModelBudgetGatewayStatus)(nil)).Elem(),
		"TenantModelUsage":                       reflect.TypeOf((*TenantModelUsage)(nil)).Elem(),
		"TenantRuntimeControl":                   reflect.TypeOf((*TenantRuntimeControl)(nil)).Elem(),
		"TenantExecutionQueue":                   reflect.TypeOf((*TenantExecutionQueue)(nil)).Elem(),
		"TenantCostWindow":                       reflect.TypeOf((*TenantCostWindow)(nil)).Elem(),
		"TenantCost":                             reflect.TypeOf((*TenantCost)(nil)).Elem(),
		"TenantConfigRef":                        reflect.TypeOf((*TenantConfigRef)(nil)).Elem(),
		"TenantConfigCatalogEntry":               reflect.TypeOf((*TenantConfigCatalogEntry)(nil)).Elem(),
		"TenantModelCatalogEntry":                reflect.TypeOf((*TenantModelCatalogEntry)(nil)).Elem(),
		"TenantCredentialRef":                    reflect.TypeOf((*TenantCredentialRef)(nil)).Elem(),
		"TenantConfigSelection":                  reflect.TypeOf((*TenantConfigSelection)(nil)).Elem(),
		"AdoptedTenantConfig":                    reflect.TypeOf((*AdoptedTenantConfig)(nil)).Elem(),
		"TenantConfigView":                       reflect.TypeOf((*TenantConfigView)(nil)).Elem(),
		"TenantConfigPreviewRequest":             reflect.TypeOf((*TenantConfigPreviewRequest)(nil)).Elem(),
		"TenantConfigDiff":                       reflect.TypeOf((*TenantConfigDiff)(nil)).Elem(),
		"TenantConfigValidationError":            reflect.TypeOf((*TenantConfigValidationError)(nil)).Elem(),
		"TenantConfigPreview":                    reflect.TypeOf((*TenantConfigPreview)(nil)).Elem(),
		"TenantConfigAdoptionRequest":            reflect.TypeOf((*TenantConfigAdoptionRequest)(nil)).Elem(),
		"TenantConfigAdoptionResult":             reflect.TypeOf((*TenantConfigAdoptionResult)(nil)).Elem(),
		"TenantLiveControlRequest":               reflect.TypeOf((*TenantLiveControlRequest)(nil)).Elem(),
		"TenantLiveControlResult":                reflect.TypeOf((*TenantLiveControlResult)(nil)).Elem(),
		"TenantOperations":                       reflect.TypeOf((*TenantOperations)(nil)).Elem(),
		"IntakeRequestIdentity":                  reflect.TypeOf((*IntakeRequestIdentity)(nil)).Elem(),
		"PendingIntake":                          reflect.TypeOf((*PendingIntake)(nil)).Elem(),
		"PendingIntakesPage":                     reflect.TypeOf((*PendingIntakesPage)(nil)).Elem(),
		"IntakeContinuation":                     reflect.TypeOf((*IntakeContinuation)(nil)).Elem(),
		"IntakeArchiveResult":                    reflect.TypeOf((*IntakeArchiveResult)(nil)).Elem(),
		"IntakeOperation":                        reflect.TypeOf((*IntakeOperation)(nil)).Elem(),
		"IntakeCreateRequest":                    reflect.TypeOf((*IntakeCreateRequest)(nil)).Elem(),
		"ManualFindingEvidence":                  reflect.TypeOf((*ManualFindingEvidence)(nil)).Elem(),
		"ManualFindingEvidenceInput":             reflect.TypeOf((*ManualFindingEvidenceInput)(nil)).Elem(),
		"ManualFindingRequest":                   reflect.TypeOf((*ManualFindingRequest)(nil)).Elem(),
		"ManualFinding":                          reflect.TypeOf((*ManualFinding)(nil)).Elem(),
		"RuleShadowSampleRef":                    reflect.TypeOf((*RuleShadowSampleRef)(nil)).Elem(),
		"RuleShadowSampleOption":                 reflect.TypeOf((*RuleShadowSampleOption)(nil)).Elem(),
		"RuleShadowChange":                       reflect.TypeOf((*RuleShadowChange)(nil)).Elem(),
		"RuleShadowSampleResult":                 reflect.TypeOf((*RuleShadowSampleResult)(nil)).Elem(),
		"RuleShadowEnvelope":                     reflect.TypeOf((*RuleShadowEnvelope)(nil)).Elem(),
		"OperatorCollectionJob":                  reflect.TypeOf((*OperatorCollectionJob)(nil)).Elem(),
		"OperatorTemporalProof":                  reflect.TypeOf((*OperatorTemporalProof)(nil)).Elem(),
		"OperatorObservation":                    reflect.TypeOf((*OperatorObservation)(nil)).Elem(),
		"OperatorSnapshotSet":                    reflect.TypeOf((*OperatorSnapshotSet)(nil)).Elem(),
		"OperatorCollectionResponse":             reflect.TypeOf((*OperatorCollectionResponse)(nil)).Elem(),
		"OperatorSnapshotList":                   reflect.TypeOf((*OperatorSnapshotList)(nil)).Elem(),
		"OperatorEvaluation":                     reflect.TypeOf((*OperatorEvaluation)(nil)).Elem(),
		"OperatorEvaluationScope":                reflect.TypeOf((*OperatorEvaluationScope)(nil)).Elem(),
		"OperatorEvaluationCoverage":             reflect.TypeOf((*OperatorEvaluationCoverage)(nil)).Elem(),
		"OperatorEvaluationObject":               reflect.TypeOf((*OperatorEvaluationObject)(nil)).Elem(),
		"EvidenceRequestCreate":                  reflect.TypeOf((*EvidenceRequestCreate)(nil)).Elem(),
		"EvidenceReceiptCreate":                  reflect.TypeOf((*EvidenceReceiptCreate)(nil)).Elem(),
		"EvidenceRequestVerify":                  reflect.TypeOf((*EvidenceRequestVerify)(nil)).Elem(),
		"EvidenceSourceEvaluation":               reflect.TypeOf((*EvidenceSourceEvaluation)(nil)).Elem(),
		"EvidenceSourceGap":                      reflect.TypeOf((*EvidenceSourceGap)(nil)).Elem(),
		"EvidenceRequestVerification":            reflect.TypeOf((*EvidenceRequestVerification)(nil)).Elem(),
		"EvidenceRequest":                        reflect.TypeOf((*EvidenceRequest)(nil)).Elem(),
		"EvidenceRequestList":                    reflect.TypeOf((*EvidenceRequestList)(nil)).Elem(),
		"EvidenceReceipt":                        reflect.TypeOf((*EvidenceReceipt)(nil)).Elem(),
		"EvidenceReceiptResult":                  reflect.TypeOf((*EvidenceReceiptResult)(nil)).Elem(),
		"EvidenceRequestEvent":                   reflect.TypeOf((*EvidenceRequestEvent)(nil)).Elem(),
		"EvidenceRequestHistory":                 reflect.TypeOf((*EvidenceRequestHistory)(nil)).Elem(),
		"ServiceObjectCreateRequest":             reflect.TypeOf((*ServiceObjectCreateRequest)(nil)).Elem(),
		"ServiceObjectMutationReceipt":           reflect.TypeOf((*ServiceObjectMutationReceipt)(nil)).Elem(),
		"ServiceObjectSummary":                   reflect.TypeOf((*ServiceObjectSummary)(nil)).Elem(),
		"ServiceObjectAlias":                     reflect.TypeOf((*ServiceObjectAlias)(nil)).Elem(),
		"ServiceObjectAssociationPin":            reflect.TypeOf((*ServiceObjectAssociationPin)(nil)).Elem(),
		"ServiceObjectVersion":                   reflect.TypeOf((*ServiceObjectVersion)(nil)).Elem(),
		"ServiceObjectFactRef":                   reflect.TypeOf((*ServiceObjectFactRef)(nil)).Elem(),
		"ServiceObjectFact":                      reflect.TypeOf((*ServiceObjectFact)(nil)).Elem(),
		"ServiceObjectFactCitation":              reflect.TypeOf((*ServiceObjectFactCitation)(nil)).Elem(),
		"ServiceObjectAssociation":               reflect.TypeOf((*ServiceObjectAssociation)(nil)).Elem(),
		"ServiceObjectDetail":                    reflect.TypeOf((*ServiceObjectDetail)(nil)).Elem(),
		"ServiceObjectMappingPin":                reflect.TypeOf((*ServiceObjectMappingPin)(nil)).Elem(),
		"ServiceObjectVersionPage":               reflect.TypeOf((*ServiceObjectVersionPage)(nil)).Elem(),
		"ServiceObjectAssociationPage":           reflect.TypeOf((*ServiceObjectAssociationPage)(nil)).Elem(),
		"ServiceObjectAssociationConfirmRequest": reflect.TypeOf((*ServiceObjectAssociationConfirmRequest)(nil)).Elem(),
		"ServiceObjectAssociationPreview":        reflect.TypeOf((*ServiceObjectAssociationPreview)(nil)).Elem(),
		"OperatorMappingChangeCAS":               reflect.TypeOf((*OperatorMappingChangeCAS)(nil)).Elem(),
		"OperatorMappingChangePatch":             reflect.TypeOf((*OperatorMappingChangePatch)(nil)).Elem(),
		"OperatorServiceIdentityPatch":           reflect.TypeOf((*OperatorServiceIdentityPatch)(nil)).Elem(),
		"MaterialAnchor":                         reflect.TypeOf((*MaterialAnchor)(nil)).Elem(),
		"OperatorMappingChangePreview":           reflect.TypeOf((*OperatorMappingChangePreview)(nil)).Elem(),
		"OperatorMappingAssociationAfter":        reflect.TypeOf((*OperatorMappingAssociationAfter)(nil)).Elem(),
		"OperatorMappingChangeRequest":           reflect.TypeOf((*OperatorMappingChangeRequest)(nil)).Elem(),
		"OperatorMappingChangeDecision":          reflect.TypeOf((*OperatorMappingChangeDecision)(nil)).Elem(),
		"ReviewReadiness":                        reflect.TypeOf((*ReviewReadiness)(nil)).Elem(),
		"ReviewReadinessMaterial":                reflect.TypeOf((*ReviewReadinessMaterial)(nil)).Elem(),
		"ReviewReadinessParse":                   reflect.TypeOf((*ReviewReadinessParse)(nil)).Elem(),
		"ReviewReadinessScope":                   reflect.TypeOf((*ReviewReadinessScope)(nil)).Elem(),
		"ReviewScopeIdentity":                    reflect.TypeOf((*ReviewScopeIdentity)(nil)).Elem(),
		"ReviewReadinessRulePack":                reflect.TypeOf((*ReviewReadinessRulePack)(nil)).Elem(),
		"ReviewReadinessCheck":                   reflect.TypeOf((*ReviewReadinessCheck)(nil)).Elem(),
		"ReviewReadinessBlocker":                 reflect.TypeOf((*ReviewReadinessBlocker)(nil)).Elem(),
		"ReviewStartRequest":                     reflect.TypeOf((*ReviewStartRequest)(nil)).Elem(),
		"ReviewDiagnostics":                      reflect.TypeOf((*ReviewDiagnostics)(nil)).Elem(),
		"ExperienceEvidenceRef":                  reflect.TypeOf((*ExperienceEvidenceRef)(nil)).Elem(),
		"ExperienceEvidenceView":                 reflect.TypeOf((*ExperienceEvidenceView)(nil)).Elem(),
		"ExperienceFeedbackRequest":              reflect.TypeOf((*ExperienceFeedbackRequest)(nil)).Elem(),
		"ExperienceFeedback":                     reflect.TypeOf((*ExperienceFeedback)(nil)).Elem(),
		"ExperienceDistillationRequest":          reflect.TypeOf((*ExperienceDistillationRequest)(nil)).Elem(),
		"ExperienceDistillationReceipt":          reflect.TypeOf((*ExperienceDistillationReceipt)(nil)).Elem(),
		"ExperienceDistillationStatus":           reflect.TypeOf((*ExperienceDistillationStatus)(nil)).Elem(),
		"GovernedKnowledgeRef":                   reflect.TypeOf((*GovernedKnowledgeRef)(nil)).Elem(),
		"ExperienceUsageRequest":                 reflect.TypeOf((*ExperienceUsageRequest)(nil)).Elem(),
		"ExperienceUsage":                        reflect.TypeOf((*ExperienceUsage)(nil)).Elem(),
		"ExperienceUsageOption":                  reflect.TypeOf((*ExperienceUsageOption)(nil)).Elem(),
		"ExperienceUsageOptions":                 reflect.TypeOf((*ExperienceUsageOptions)(nil)).Elem(),
		"ExperienceMetricsSample":                reflect.TypeOf((*ExperienceMetricsSample)(nil)).Elem(),
		"ExperienceMetrics":                      reflect.TypeOf((*ExperienceMetrics)(nil)).Elem(),
		"PolicyThresholdCandidate":               reflect.TypeOf((*PolicyThresholdCandidate)(nil)).Elem(),
		"PolicyRuleThreshold":                    reflect.TypeOf((*PolicyRuleThreshold)(nil)).Elem(),
		"PolicyRuleComparison":                   reflect.TypeOf((*PolicyRuleComparison)(nil)).Elem(),
		"PolicyTextIssue":                        reflect.TypeOf((*PolicyTextIssue)(nil)).Elem(),
		"GovernedPolicyPreview":                  reflect.TypeOf((*GovernedPolicyPreview)(nil)).Elem(),
		"GovernedRulePackRef":                    reflect.TypeOf((*GovernedRulePackRef)(nil)).Elem(),
		"GovernedKnowledgeSelection":             reflect.TypeOf((*GovernedKnowledgeSelection)(nil)).Elem(),
		"ReviewKnowledgeSnapshot":                reflect.TypeOf((*ReviewKnowledgeSnapshot)(nil)).Elem(),
		"FrozenLegalAuthority":                   reflect.TypeOf((*FrozenLegalAuthority)(nil)).Elem(),
		"ContractCitation":                       reflect.TypeOf((*ContractCitation)(nil)).Elem(),
		"FrozenLegalReviewProjection":            reflect.TypeOf((*FrozenLegalReviewProjection)(nil)).Elem(),
		"ExperienceCandidateContent":             reflect.TypeOf((*ExperienceCandidateContent)(nil)).Elem(),
		"ExperienceCandidate":                    reflect.TypeOf((*ExperienceCandidate)(nil)).Elem(),
		"GovernedKnowledgeScope":                 reflect.TypeOf((*GovernedKnowledgeScope)(nil)).Elem(),
		"GovernedKnowledgeCondition":             reflect.TypeOf((*GovernedKnowledgeCondition)(nil)).Elem(),
		"GovernedKnowledgeSourceSpan":            reflect.TypeOf((*GovernedKnowledgeSourceSpan)(nil)).Elem(),
		"GovernedKnowledgeBinding":               reflect.TypeOf((*GovernedKnowledgeBinding)(nil)).Elem(),
		"GovernedKnowledgeContent":               reflect.TypeOf((*GovernedKnowledgeContent)(nil)).Elem(),
		"GovernedKnowledgeCreateRequest":         reflect.TypeOf((*GovernedKnowledgeCreateRequest)(nil)).Elem(),
		"GovernedKnowledgeCommandRequest":        reflect.TypeOf((*GovernedKnowledgeCommandRequest)(nil)).Elem(),
		"GovernedKnowledgeVersion":               reflect.TypeOf((*GovernedKnowledgeVersion)(nil)).Elem(),
		"GovernedKnowledgeSummary":               reflect.TypeOf((*GovernedKnowledgeSummary)(nil)).Elem(),
		"GovernedKnowledgeList":                  reflect.TypeOf((*GovernedKnowledgeList)(nil)).Elem(),
		"GovernedKnowledgeHistoryEntry":          reflect.TypeOf((*GovernedKnowledgeHistoryEntry)(nil)).Elem(),
		"GovernedKnowledgeHistory":               reflect.TypeOf((*GovernedKnowledgeHistory)(nil)).Elem(),
		"CaseProfile":                            reflect.TypeOf((*CaseProfile)(nil)).Elem(),
		"CaseProfileInput":                       reflect.TypeOf((*CaseProfileInput)(nil)).Elem(),
		"WorkbenchTask":                          reflect.TypeOf((*WorkbenchTask)(nil)).Elem(),
		"WorkbenchSnapshotTask":                  reflect.TypeOf((*WorkbenchSnapshotTask)(nil)).Elem(),
		"WorkbenchTaskInput":                     reflect.TypeOf((*WorkbenchTaskInput)(nil)).Elem(),
		"WorkbenchTaskUpdateInput":               reflect.TypeOf((*WorkbenchTaskUpdateInput)(nil)).Elem(),
		"CaseRelation":                           reflect.TypeOf((*CaseRelation)(nil)).Elem(),
		"CaseRelationInput":                      reflect.TypeOf((*CaseRelationInput)(nil)).Elem(),
		"Knowledge":                              reflect.TypeOf((*Knowledge)(nil)).Elem(),
		"KnowledgeInput":                         reflect.TypeOf((*KnowledgeInput)(nil)).Elem(),
		"SeveritySource":                         reflect.TypeOf((*SeveritySource)(nil)).Elem(),
		"WorkbenchDashboard":                     reflect.TypeOf((*WorkbenchDashboard)(nil)).Elem(),
		"WorkbenchModel":                         reflect.TypeOf((*WorkbenchModel)(nil)).Elem(),
		"WorkbenchTool":                          reflect.TypeOf((*WorkbenchTool)(nil)).Elem(),
		"WorkbenchRun":                           reflect.TypeOf((*WorkbenchRun)(nil)).Elem(),
		"WorkbenchAudit":                         reflect.TypeOf((*WorkbenchAudit)(nil)).Elem(),
		"Workbench":                              reflect.TypeOf((*Workbench)(nil)).Elem(),
		"WorkbenchCase":                          reflect.TypeOf((*WorkbenchCase)(nil)).Elem(),
		"WorkbenchDocument":                      reflect.TypeOf((*WorkbenchDocument)(nil)).Elem(),
		"WorkbenchRemediation":                   reflect.TypeOf((*WorkbenchRemediation)(nil)).Elem(),
		"WorkbenchReport":                        reflect.TypeOf((*WorkbenchReport)(nil)).Elem(),
		"WorkbenchMember":                        reflect.TypeOf((*WorkbenchMember)(nil)).Elem(),
		"Template":                               reflect.TypeOf((*Template)(nil)).Elem(),
		"TemplateField":                          reflect.TypeOf((*TemplateField)(nil)).Elem(),
		"TemplateList":                           reflect.TypeOf((*TemplateList)(nil)).Elem(),
		"CreateGenerationRequest":                reflect.TypeOf((*CreateGenerationRequest)(nil)).Elem(),
		"GenerationRecord":                       reflect.TypeOf((*GenerationRecord)(nil)).Elem(),
		"Remediation":                            reflect.TypeOf((*Remediation)(nil)).Elem(),
		"RemediationInput":                       reflect.TypeOf((*RemediationInput)(nil)).Elem(),
		"RemediationSubmitInput":                 reflect.TypeOf((*RemediationSubmitInput)(nil)).Elem(),
		"RemediationVerifyInput":                 reflect.TypeOf((*RemediationVerifyInput)(nil)).Elem(),
		"CreateReportRequest":                    reflect.TypeOf((*CreateReportRequest)(nil)).Elem(),
		"ReportRecord":                           reflect.TypeOf((*ReportRecord)(nil)).Elem(),
		"ReportDocument":                         reflect.TypeOf((*ReportDocument)(nil)).Elem(),
		"ArchiveRequest":                         reflect.TypeOf((*ArchiveRequest)(nil)).Elem(),
		"ArchiveRecord":                          reflect.TypeOf((*ArchiveRecord)(nil)).Elem(),
		"EmptyObject":                            reflect.TypeOf((*EmptyObject)(nil)).Elem(),
		"ID":                                     reflect.TypeOf((*ID)(nil)).Elem(),
		"Actor":                                  reflect.TypeOf((*Actor)(nil)).Elem(),
		"Session":                                reflect.TypeOf((*Session)(nil)).Elem(),
		"LoginRequest":                           reflect.TypeOf((*LoginRequest)(nil)).Elem(),
		"CreateCaseRequest":                      reflect.TypeOf((*CreateCaseRequest)(nil)).Elem(),
		"AddMemberRequest":                       reflect.TypeOf((*AddMemberRequest)(nil)).Elem(),
		"DecisionRequest":                        reflect.TypeOf((*DecisionRequest)(nil)).Elem(),
		"Error":                                  reflect.TypeOf((*Error)(nil)).Elem(),
		"RunSummary":                             reflect.TypeOf((*RunSummary)(nil)).Elem(),
		"CaseSummary":                            reflect.TypeOf((*CaseSummary)(nil)).Elem(),
		"CaseList":                               reflect.TypeOf((*CaseList)(nil)).Elem(),
		"ParsedDocument":                         reflect.TypeOf((*ParsedDocument)(nil)).Elem(),
		"Document":                               reflect.TypeOf((*Document)(nil)).Elem(),
		"TextOffset":                             reflect.TypeOf((*TextOffset)(nil)).Elem(),
		"Clause":                                 reflect.TypeOf((*Clause)(nil)).Elem(),
		"ReviewDocument":                         reflect.TypeOf((*ReviewDocument)(nil)).Elem(),
		"FindingCounts":                          reflect.TypeOf((*FindingCounts)(nil)).Elem(),
		"Activity":                               reflect.TypeOf((*Activity)(nil)).Elem(),
		"CaseDetail":                             reflect.TypeOf((*CaseDetail)(nil)).Elem(),
		"Anchor":                                 reflect.TypeOf((*Anchor)(nil)).Elem(),
		"Decision":                               reflect.TypeOf((*Decision)(nil)).Elem(),
		"PriorDecision":                          reflect.TypeOf((*PriorDecision)(nil)).Elem(),
		"MaterialProjection":                     reflect.TypeOf((*MaterialProjection)(nil)).Elem(),
		"MaterialSelectionInput":                 reflect.TypeOf((*MaterialSelectionInput)(nil)).Elem(),
		"MaterialVersionProjection":              reflect.TypeOf((*MaterialVersionProjection)(nil)).Elem(),
		"MaterialSelectionProjection":            reflect.TypeOf((*MaterialSelectionProjection)(nil)).Elem(),
		"Finding":                                reflect.TypeOf((*Finding)(nil)).Elem(),
		"Run":                                    reflect.TypeOf((*Run)(nil)).Elem(),
		"Snapshot":                               reflect.TypeOf((*Snapshot)(nil)).Elem(),
		"Evaluation":                             reflect.TypeOf((*Evaluation)(nil)).Elem(),
		"AuditEvent":                             reflect.TypeOf((*AuditEvent)(nil)).Elem(),
		"Revision":                               reflect.TypeOf((*Revision)(nil)).Elem(),
		"Workspace":                              reflect.TypeOf((*Workspace)(nil)).Elem(),
		"BusinessSnapshot":                       reflect.TypeOf((*BusinessSnapshot)(nil)).Elem(),
		"AdoptedTenantConfigIdentity":            reflect.TypeOf((*AdoptedTenantConfigIdentity)(nil)).Elem(),
		"BusinessAttempt":                        reflect.TypeOf((*BusinessAttempt)(nil)).Elem(),
		"BusinessAttempts":                       reflect.TypeOf((*BusinessAttempts)(nil)).Elem(),
		"BusinessAuditEvent":                     reflect.TypeOf((*BusinessAuditEvent)(nil)).Elem(),
		"BusinessAgentScope":                     reflect.TypeOf((*BusinessAgentScope)(nil)).Elem(),
		"BusinessAgentCapability":                reflect.TypeOf((*BusinessAgentCapability)(nil)).Elem(),
		"BusinessAgentDomain":                    reflect.TypeOf((*BusinessAgentDomain)(nil)).Elem(),
		"BusinessAgentGap":                       reflect.TypeOf((*BusinessAgentGap)(nil)).Elem(),
		"BusinessEvidenceAnchor":                 reflect.TypeOf((*BusinessEvidenceAnchor)(nil)).Elem(),
		"BusinessAgentEvidence":                  reflect.TypeOf((*BusinessAgentEvidence)(nil)).Elem(),
		"BusinessAgentReview":                    reflect.TypeOf((*BusinessAgentReview)(nil)).Elem(),
		"BusinessAgentObservation":               reflect.TypeOf((*BusinessAgentObservation)(nil)).Elem(),
		"Settings":                               reflect.TypeOf((*Settings)(nil)).Elem(),
		"StreamEvent":                            reflect.TypeOf((*StreamEvent)(nil)).Elem(),
		"Health":                                 reflect.TypeOf((*Health)(nil)).Elem(),
		"OpenAPIDocument":                        reflect.TypeOf((*OpenAPIDocument)(nil)).Elem(),
		"ReviewAccepted":                         reflect.TypeOf((*ReviewAccepted)(nil)).Elem(),
		"NewReviewJobAccepted":                   reflect.TypeOf((*NewReviewJobAccepted)(nil)).Elem(),
		"ExecutionUsage":                         reflect.TypeOf((*ExecutionUsage)(nil)).Elem(),
		"ReviewAttempt":                          reflect.TypeOf((*ReviewAttempt)(nil)).Elem(),
		"ExecutionCallUsage":                     reflect.TypeOf((*ExecutionCallUsage)(nil)).Elem(),
		"ExecutionEvent":                         reflect.TypeOf((*ExecutionEvent)(nil)).Elem(),
		"ModelCallTrace":                         reflect.TypeOf((*ModelCallTrace)(nil)).Elem(),
		"ModelCallTraceMessage":                  reflect.TypeOf((*ModelCallTraceMessage)(nil)).Elem(),
		"Execution":                              reflect.TypeOf((*Execution)(nil)).Elem(),
		"RulePackSummary":                        reflect.TypeOf((*RulePackSummary)(nil)).Elem(),
		"RulePackDetail":                         reflect.TypeOf((*RulePackDetail)(nil)).Elem(),
		"RulePackList":                           reflect.TypeOf((*RulePackList)(nil)).Elem(),
		"RulePackHistory":                        reflect.TypeOf((*RulePackHistory)(nil)).Elem(),
		"RulePackHistoryEntry":                   reflect.TypeOf((*RulePackHistoryEntry)(nil)).Elem(),
		"RulePackCreateRequest":                  reflect.TypeOf((*RulePackCreateRequest)(nil)).Elem(),
		"RulePackEditRequest":                    reflect.TypeOf((*RulePackEditRequest)(nil)).Elem(),
		"RulePackRevisionRequest":                reflect.TypeOf((*RulePackRevisionRequest)(nil)).Elem(),
		"RulePackApproveRequest":                 reflect.TypeOf((*RulePackApproveRequest)(nil)).Elem(),
		"RulePackPointerRequest":                 reflect.TypeOf((*RulePackPointerRequest)(nil)).Elem(),
		"RulePackRollbackRequest":                reflect.TypeOf((*RulePackRollbackRequest)(nil)).Elem(),
		"RulePackTestSource":                     reflect.TypeOf((*RulePackTestSource)(nil)).Elem(),
		"RulePackTestCitation":                   reflect.TypeOf((*RulePackTestCitation)(nil)).Elem(),
		"RulePackTestFact":                       reflect.TypeOf((*RulePackTestFact)(nil)).Elem(),
		"RulePackFixtureSuite":                   reflect.TypeOf((*RulePackFixtureSuite)(nil)).Elem(),
		"RulePackFixtureCase":                    reflect.TypeOf((*RulePackFixtureCase)(nil)).Elem(),
		"RulePackFixtureInput":                   reflect.TypeOf((*RulePackFixtureInput)(nil)).Elem(),
		"RulePackFixtureExpected":                reflect.TypeOf((*RulePackFixtureExpected)(nil)).Elem(),
		"RulePackFixtureResponse":                reflect.TypeOf((*RulePackFixtureResponse)(nil)).Elem(),
		"RulePackFixtureRun":                     reflect.TypeOf((*RulePackFixtureRun)(nil)).Elem(),
		"RulePackFixtureChange":                  reflect.TypeOf((*RulePackFixtureChange)(nil)).Elem(),
		"RulePackTestRequest":                    reflect.TypeOf((*RulePackTestRequest)(nil)).Elem(),
		"RulePackTestResult":                     reflect.TypeOf((*RulePackTestResult)(nil)).Elem(),
		"AgentReviewLimits":                      reflect.TypeOf((*AgentReviewLimits)(nil)).Elem(),
		"AgentReviewPlanner":                     reflect.TypeOf((*AgentReviewPlanner)(nil)).Elem(),
		"AgentReviewRulePackIdentity":            reflect.TypeOf((*AgentReviewRulePackIdentity)(nil)).Elem(),
		"AgentReviewSourceSnapshot":              reflect.TypeOf((*AgentReviewSourceSnapshot)(nil)).Elem(),
		"AgentReviewDomainScope":                 reflect.TypeOf((*AgentReviewDomainScope)(nil)).Elem(),
		"AgentReviewCapability":                  reflect.TypeOf((*AgentReviewCapability)(nil)).Elem(),
		"AgentReviewDomainCoverage":              reflect.TypeOf((*AgentReviewDomainCoverage)(nil)).Elem(),
		"AgentReviewGap":                         reflect.TypeOf((*AgentReviewGap)(nil)).Elem(),
		"AgentReviewCall":                        reflect.TypeOf((*AgentReviewCall)(nil)).Elem(),
		"AgentReviewRound":                       reflect.TypeOf((*AgentReviewRound)(nil)).Elem(),
		"AgentReviewPlan":                        reflect.TypeOf((*AgentReviewPlan)(nil)).Elem(),
		"AgentReviewAnchor":                      reflect.TypeOf((*AgentReviewAnchor)(nil)).Elem(),
		"AgentReviewEvidence":                    reflect.TypeOf((*AgentReviewEvidence)(nil)).Elem(),
		"AgentReviewUsage":                       reflect.TypeOf((*AgentReviewUsage)(nil)).Elem(),
		"AgentReviewEvent":                       reflect.TypeOf((*AgentReviewEvent)(nil)).Elem(),
		"AgentReviewObservation":                 reflect.TypeOf((*AgentReviewObservation)(nil)).Elem(),
		"AgentReviewKnowledgeRead":               reflect.TypeOf((*AgentReviewKnowledgeRead)(nil)).Elem(),
		"AgentKnowledgeSelection":                reflect.TypeOf((*AgentKnowledgeSelection)(nil)).Elem(),
		"KnowledgeReadIdentity":                  reflect.TypeOf((*KnowledgeReadIdentity)(nil)).Elem(),
		"KnowledgeReadRecord":                    reflect.TypeOf((*KnowledgeReadRecord)(nil)).Elem(),
		"AgentReview":                            reflect.TypeOf((*AgentReview)(nil)).Elem(),
		"RulePackLifecycle":                      reflect.TypeOf((*RulePackLifecycle)(nil)).Elem(),
		"RulePackVerification":                   reflect.TypeOf((*RulePackVerification)(nil)).Elem(),
		"RulePackApproval":                       reflect.TypeOf((*RulePackApproval)(nil)).Elem(),
		"RulePackRelease":                        reflect.TypeOf((*RulePackRelease)(nil)).Elem(),
		"RulePackManifestInput":                  reflect.TypeOf((*RulePackManifestInput)(nil)).Elem(),
		"RulePackManifestSnapshot":               reflect.TypeOf((*RulePackManifestSnapshot)(nil)).Elem(),
		"RulePackReportCitation":                 reflect.TypeOf((*RulePackReportCitation)(nil)).Elem(),
		"RulePackReportResult":                   reflect.TypeOf((*RulePackReportResult)(nil)).Elem(),
		"RulePackReport":                         reflect.TypeOf((*RulePackReport)(nil)).Elem(),
		"SystemHealth":                           reflect.TypeOf((*SystemHealth)(nil)).Elem(),
		"SystemHealthCluster":                    reflect.TypeOf((*SystemHealthCluster)(nil)).Elem(),
		"SystemHealthNode":                       reflect.TypeOf((*SystemHealthNode)(nil)).Elem(),
		"LLMEndpointView":                        reflect.TypeOf((*LLMEndpointView)(nil)).Elem(),
		"LLMEndpointProbeRequest":                reflect.TypeOf((*LLMEndpointProbeRequest)(nil)).Elem(),
		"LLMEndpointProbeResult":                 reflect.TypeOf((*LLMEndpointProbeResult)(nil)).Elem(),
		"LLMEndpointInput":                       reflect.TypeOf((*LLMEndpointInput)(nil)).Elem(),
		"LLMModelSelectionInput":                 reflect.TypeOf((*LLMModelSelectionInput)(nil)).Elem(),
		"TenantMoneyWindow":                      reflect.TypeOf((*TenantMoneyWindow)(nil)).Elem(),
		"TenantMoneyPolicy":                      reflect.TypeOf((*TenantMoneyPolicy)(nil)).Elem(),
		"TenantMoneyPolicyInput":                 reflect.TypeOf((*TenantMoneyPolicyInput)(nil)).Elem(),
		"PlatformModelPriceInput":                reflect.TypeOf((*PlatformModelPriceInput)(nil)).Elem(),
		"PlatformModelMoneyPolicyInput":          reflect.TypeOf((*PlatformModelMoneyPolicyInput)(nil)).Elem(),
		"PlatformModelMoneyPolicyResult":         reflect.TypeOf((*PlatformModelMoneyPolicyResult)(nil)).Elem(),
		"PlatformModelMoneyPolicyView":           reflect.TypeOf((*PlatformModelMoneyPolicyView)(nil)).Elem(),
		"TenantMoneyAdjustmentInput":             reflect.TypeOf((*TenantMoneyAdjustmentInput)(nil)).Elem(),
		"TenantMoneyAdjustmentResult":            reflect.TypeOf((*TenantMoneyAdjustmentResult)(nil)).Elem(),
	}
	types["TenantProvisionOperation"] = reflect.TypeOf((*TenantProvisionOperation)(nil)).Elem()
	types["TenantProvisionStepView"] = reflect.TypeOf((*TenantProvisionStepView)(nil)).Elem()
	types["TenantProvisionOperationsView"] = reflect.TypeOf((*TenantProvisionOperationsView)(nil)).Elem()
	types["TenantProvisionInput"] = reflect.TypeOf((*TenantProvisionInput)(nil)).Elem()
	types["TenantProvisionOperationResult"] = reflect.TypeOf((*TenantProvisionOperationResult)(nil)).Elem()
	types["TenantProvisionStepInput"] = reflect.TypeOf((*TenantProvisionStepInput)(nil)).Elem()
	types["JurisdictionOperationsView"] = reflect.TypeOf((*JurisdictionOperationsView)(nil)).Elem()
	types["JurisdictionOperationsSummary"] = reflect.TypeOf((*JurisdictionOperationsSummary)(nil)).Elem()
	types["TenantOperationsSummary"] = reflect.TypeOf((*TenantOperationsSummary)(nil)).Elem()
	types["OperationsMetricSummary"] = reflect.TypeOf((*OperationsMetricSummary)(nil)).Elem()
	types["OperationsCostView"] = reflect.TypeOf((*OperationsCostView)(nil)).Elem()
	types["OperationsQuotaView"] = reflect.TypeOf((*OperationsQuotaView)(nil)).Elem()
	types["OperationsPoolView"] = reflect.TypeOf((*OperationsPoolView)(nil)).Elem()
	return types
}
