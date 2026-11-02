package apidocs

import "encoding/json"

func init() {
	ref := func(name string) Schema { return Schema{Ref: "#/components/schemas/" + name} }
	jsonBody := func(name string) *RequestBody {
		return &RequestBody{Required: true, Content: map[string]MediaType{"application/json": {Schema: ref(name)}}}
	}
	jsonResponse := func(name, description string) Response {
		return Response{Description: description, Content: map[string]MediaType{"application/json": {Schema: ref(name)}}}
	}
	query := func(name string, schema Schema, required bool) Parameter {
		return Parameter{Name: name, In: "query", Required: required, Schema: schema}
	}
	path := func(name string) Parameter {
		return Parameter{Name: name, In: "path", Required: true, Schema: Schema{Type: json.RawMessage(`"string"`)}}
	}
	sourcePaths["/api/admin/platform/tenant-provision-operations"] = PathItem{
		Get: &Operation{OperationID: "listTenantProvisionOperations", Tags: []string{"系统"}, Summary: "分页读取租户开通与恢复操作", Parameters: []Parameter{
			query("cursor", Schema{Type: json.RawMessage(`"string"`)}, false),
			query("limit", Schema{Type: json.RawMessage(`"integer"`), Minimum: json.RawMessage("1"), Maximum: json.RawMessage("100")}, false),
		}, Responses: map[string]Response{"200": jsonResponse("TenantProvisionOperationsView", "当前平台租户操作页")}},
		Post: &Operation{OperationID: "startTenantProvisionOperation", Tags: []string{"系统"}, Summary: "提交幂等的租户开通或恢复操作", Parameters: []Parameter{{Name: "Idempotency-Key", In: "header", Required: true, Schema: Schema{Type: json.RawMessage(`"string"`)}}}, RequestBody: jsonBody("TenantProvisionInput"), Responses: map[string]Response{"202": jsonResponse("TenantProvisionOperationResult", "操作已受理")}},
	}
	sourcePaths["/api/admin/platform/tenant-provision-operations/{operationID}"] = PathItem{
		Parameters: []Parameter{path("operationID")},
		Get:        &Operation{OperationID: "readTenantProvisionOperation", Tags: []string{"系统"}, Summary: "读取租户开通或恢复操作及步骤回执", Responses: map[string]Response{"200": jsonResponse("TenantProvisionOperationResult", "操作当前状态")}},
	}
	sourcePaths["/api/admin/platform/tenant-provision-operations/{operationID}/step"] = PathItem{
		Parameters: []Parameter{path("operationID")},
		Post:       &Operation{OperationID: "advanceTenantProvisionOperation", Tags: []string{"系统"}, Summary: "追加租户操作步骤回执", RequestBody: jsonBody("TenantProvisionStepInput"), Responses: map[string]Response{"200": jsonResponse("TenantProvisionOperationResult", "操作步骤已记录")}},
	}
	sourcePaths["/api/admin/jurisdiction/operations"] = PathItem{
		Get: &Operation{OperationID: "readJurisdictionOperations", Tags: []string{"系统"}, Summary: "读取辖区级运营汇总", Parameters: []Parameter{
			query("from_date", Schema{Type: json.RawMessage(`"string"`), Format: "date"}, true),
			query("to_date", Schema{Type: json.RawMessage(`"string"`), Format: "date"}, true),
		}, Responses: map[string]Response{"200": jsonResponse("JurisdictionOperationsView", "辖区与租户范围的汇总投影")}},
	}
}
