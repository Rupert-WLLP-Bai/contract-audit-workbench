// File metadata (manifest mirror):
// Purpose: Registers OpenAPI operations for contract reminders and the current-user reminder inbox.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package apidocs

import "encoding/json"

func init() {
	ref := func(name string) Schema { return Schema{Ref: "#/components/schemas/" + name} }
	body := func(name string) *RequestBody {
		return &RequestBody{Required: true, Content: map[string]MediaType{"application/json": {Schema: ref(name)}}}
	}
	response := func(name, description string) Response {
		return Response{Description: description, Content: map[string]MediaType{"application/json": {Schema: ref(name)}}}
	}
	path := func(name string) Parameter {
		return Parameter{Name: name, In: "path", Required: true, Schema: Schema{Type: json.RawMessage(`"string"`), Format: "uuid"}}
	}
	key := Parameter{Name: "Idempotency-Key", In: "header", Required: true, Schema: Schema{Type: json.RawMessage(`"string"`)}}
	sourcePaths["/api/contracts/{contractID}/reminders"] = PathItem{Parameters: []Parameter{path("contractID")}, Get: &Operation{OperationID: "listContractReminders", Tags: []string{"合同"}, Summary: "读取冻结计划与履约提醒", Responses: map[string]Response{"200": response("ReminderList", "可读取的冻结计划和提醒历史")}}, Post: &Operation{OperationID: "createContractReminder", Tags: []string{"合同"}, Summary: "确认履约提醒", Parameters: []Parameter{key}, RequestBody: body("ReminderCreateInput"), Responses: map[string]Response{"201": response("Reminder", "确认的提醒")}}}
	sourcePaths["/api/contracts/{contractID}/reminders/{reminderID}/cancel"] = PathItem{Parameters: []Parameter{path("contractID"), path("reminderID")}, Post: &Operation{OperationID: "cancelContractReminder", Tags: []string{"合同"}, Summary: "取消履约提醒", Parameters: []Parameter{key}, RequestBody: body("ReminderCancelInput"), Responses: map[string]Response{"200": response("Reminder", "已取消的提醒")}}}
	sourcePaths["/api/reminders"] = PathItem{Get: &Operation{OperationID: "listReminderInbox", Tags: []string{"合同"}, Summary: "读取当前用户站内提醒", Responses: map[string]Response{"200": response("ReminderInbox", "仍有权限的站内收件事实")}}}
}
