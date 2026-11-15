// File metadata (manifest mirror):
// Purpose: Registers public clause proposal, derived text, and explicit human decision OpenAPI operations.
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
	path := func(name string) Parameter {
		return Parameter{Name: name, In: "path", Required: true, Schema: Schema{Type: json.RawMessage(`"string"`)}}
	}
	key := Parameter{Name: "Idempotency-Key", In: "header", Required: true, Schema: Schema{Type: json.RawMessage(`"string"`)}}
	body := func(name string) *RequestBody {
		return &RequestBody{Required: true, Content: map[string]MediaType{"application/json": {Schema: ref(name)}}}
	}
	response := func(name, desc string) Response {
		return Response{Description: desc, Content: map[string]MediaType{"application/json": {Schema: ref(name)}}}
	}
	sourcePaths["/api/cases/{caseID}/clause-proposals"] = PathItem{Parameters: []Parameter{path("caseID")}, Get: &Operation{OperationID: "listClauseProposals", Tags: []string{"合同"}, Summary: "读取条款建议", Responses: map[string]Response{"200": response("ClauseProposalList", "建议及追加历史")}}, Post: &Operation{OperationID: "createClauseProposal", Tags: []string{"合同"}, Summary: "创建冻结来源条款建议", Parameters: []Parameter{key}, RequestBody: body("ClauseProposalCreate"), Responses: map[string]Response{"201": response("ClauseProposal", "已创建建议")}}}
	params := []Parameter{path("caseID"), path("proposalID")}
	sourcePaths["/api/cases/{caseID}/clause-proposals/{proposalID}/derived-text"] = PathItem{Parameters: params, Post: &Operation{OperationID: "deriveClauseText", Tags: []string{"合同"}, Summary: "生成派生纯文本", RequestBody: body("ClauseDerivedRequest"), Responses: map[string]Response{"200": response("ClauseDerivedResponse", "供人工确认的派生文本")}}}
	sourcePaths["/api/cases/{caseID}/clause-proposals/{proposalID}/decisions"] = PathItem{Parameters: params, Post: &Operation{OperationID: "decideClauseProposal", Tags: []string{"合同"}, Summary: "确认或拒绝建议", Parameters: []Parameter{key}, RequestBody: body("ClauseProposalDecision"), Responses: map[string]Response{"200": response("ClauseProposal", "处理结果")}}}
}
