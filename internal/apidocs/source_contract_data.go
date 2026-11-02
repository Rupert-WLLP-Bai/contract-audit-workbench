// File metadata (manifest mirror):
// Purpose: Editable OpenAPI document metadata not inferable from DTO types or routes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Editable OpenAPI document metadata not inferable from DTO types or routes.
package apidocs

import "encoding/json"

var sourceContract = Contract{
	OpenAPI:           "3.1.1",
	JSONSchemaDialect: "https://spec.openapis.org/oas/3.1/dialect/base",
	Info: Info{
		Title:       "合同智能审计工作台 · 当前 HTTP API",
		Description: "本接口文档对应当前 Go 服务，涵盖登录、案件与成员、材料、模板、审查、人工判断、任务、制度、整改、报告和规则管理和平台模型端点配置。JSON 写入请求有大小限制，只接受一个对象，并拒绝未知字段和多余内容；服务器会检查权限、版本和 Origin。文档请求面板使用当前会话发送同源请求；尚未实现的接口不列入 paths。",
		Version:     "0.3.0",
	},
	Servers: []Server{{URL: "/", Description: "当前同源 API；开发时由 Vite 代理。"}},
	Tags: []Tag{
		{Name: "会话", Description: "演示会话与 OIDC 授权码登录路由；身份提供方由部署配置提供。"},
		{Name: "案件", Description: "案件与成员可见性。"},
		{Name: "材料", Description: "原件、材料版本与当前解析行为。"},
		{Name: "审查", Description: "现有规则审查、工作区和事件。"},
		{Name: "人工复核", Description: "记录审查人员的判断。提交时检查版本，并防止同一请求重复执行。"},
		{Name: "服务对象", Description: "案件内服务对象身份、合同关联及其不可变版本。"},
		{Name: "合同", Description: "合同草稿、本人草稿恢复与已签存量导入；合同与案件、审查尝试和冻结合同版本分开保存。"},
		{Name: "系统", Description: "存活、配置状态与接口契约。"},
	},
	Security: []SecurityRequirement{{"sessionCookie": {}}},
	Paths:    sourcePaths,
	Components: Components{
		Schemas:   sourceSchemas(),
		Responses: sourceResponses,
		SecuritySchemes: map[string]SecurityScheme{
			"adminSessionCookie": {Type: "apiKey", Description: "由 admin audience 会话设置的 HttpOnly Cookie。", Name: "admin_session", In: "cookie"},
			"sessionCookie":      {Type: "apiKey", Description: "由 POST /api/session 设置的 HttpOnly Cookie。浏览器管理，无需也不应在文档页粘贴。", Name: "session", In: "cookie"},
		},
	},
	Extensions: map[string]json.RawMessage{
		"x-implementation-scope": json.RawMessage(`"Implemented API surface only; future and unsupported operations are intentionally omitted."`),
		"x-source-files":         json.RawMessage(`"internal/httpapi/**/*.go"`),
	},
}
