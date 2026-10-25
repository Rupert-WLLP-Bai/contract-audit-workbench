package httpapi_test

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"contractaudit/internal/apidocs"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"
)

func openAPI(t *testing.T) map[string]any {
	t.Helper()
	var spec map[string]any
	if err := json.Unmarshal(apidocs.Document(), &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestOpenAPITracksAllRegisteredRoutesAndJSONFields(t *testing.T) {
	spec := openAPI(t)
	file, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The unified probe surface is registered in health.go, so route coverage has
	// to read both files: a probe path the spec declares must be registered
	// there, and the extra aliases it registers are the same surface.
	health, err := parser.ParseFile(token.NewFileSet(), "health.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	routes := map[string]string{}
	for _, parsed := range []*ast.File{file, health} {
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "HandleFunc" {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				t.Fatal("route coverage check needs a literal route or an explicit new registry reader")
			}
			route, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			handler := ""
			switch target := call.Args[1].(type) {
			case *ast.SelectorExpr:
				handler = target.Sel.Name
			case *ast.Ident:
				handler = target.Name
			}
			routes[route] = handler
			return true
		})
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	jsonFields := map[string][]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn, func(node ast.Node) bool {
			value, ok := node.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "body" {
				return true
			}
			body, ok := value.Type.(*ast.StructType)
			if !ok {
				return true
			}
			fields := []string{}
			for _, field := range body.Fields.List {
				tag, err := strconv.Unquote(field.Tag.Value)
				if err != nil {
					t.Fatal(err)
				}
				fields = append(fields, reflect.StructTag(tag).Get("json"))
			}
			sort.Strings(fields)
			jsonFields[fn.Name.Name] = fields
			return true
		})
	}
	seenIDs := map[string]bool{}
	seenRoutes := map[string]bool{}
	for path, raw := range spec["paths"].(map[string]any) {
		for method, rawOp := range raw.(map[string]any) {
			key := strings.ToUpper(method) + " " + path
			handler, ok := routes[key]
			if !ok {
				t.Fatalf("spec lists an unimplemented route: %s", key)
			}
			seenRoutes[key] = true
			op := rawOp.(map[string]any)
			id, ok := op["operationId"].(string)
			if !ok || id == "" || seenIDs[id] {
				t.Fatalf("missing or duplicate operationId for %s", key)
			}
			seenIDs[id] = true
			if expected, ok := jsonFields[handler]; ok {
				request := op["requestBody"].(map[string]any)
				content := request["content"].(map[string]any)["application/json"].(map[string]any)
				name := strings.TrimPrefix(content["schema"].(map[string]any)["$ref"].(string), "#/components/schemas/")
				schema := schemas[name].(map[string]any)
				props := schema["properties"].(map[string]any)
				actual := []string{}
				for prop := range props {
					actual = append(actual, prop)
				}
				sort.Strings(actual)
				if !reflect.DeepEqual(actual, expected) || schema["additionalProperties"] != false || request["required"] != true {
					t.Fatalf("%s request shape drift: spec %v, Go %v", key, actual, expected)
				}
			}
		}
	}
	// The management probe surface (httpapi.HealthPaths) is mounted by every
	// binary and is deliberately not part of the public business contract beyond
	// /health: it must not be exposed through the Portal. This is a narrow
	// exclusion of that one shared metadata surface, not an exemption for API
	// routes — every actual /api operation must still appear in the spec.
	management := map[string]bool{}
	for _, path := range httpapi.HealthPaths {
		management[http.MethodGet+" "+path] = true
	}
	if len(seenRoutes) != len(routes) {
		for route := range routes {
			if seenRoutes[route] || management[route] {
				continue
			}
			t.Errorf("registered route absent from spec: %s", route)
		}
	}
}

func TestOpenAPIRouteIsPublicAndBusinessAPIStillAuthenticates(t *testing.T) {
	w := httptest.NewRecorder()
	httpapi.Handler(nil, httpapi.Options{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if w.Code != http.StatusOK || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("schema metadata unavailable: %d", w.Code)
	}
	api := newAPI(t)
	status, body := api.do(t, http.MethodGet, "/api/cases", nil)
	if status != http.StatusUnauthorized {
		t.Fatal("public metadata must not open business data")
	}
	assertWireSchema(t, openAPI(t), "Error", body)
}

func TestOpenAPIResponseShapesAgainstRealHTTP(t *testing.T) {
	api := newAPI(t)
	spec := openAPI(t)
	status, body := api.do(t, http.MethodPost, "/api/session", map[string]any{"username": "demo-admin", "password": "admin-demo"})
	if status != http.StatusOK {
		t.Fatalf("login %d", status)
	}
	assertWireSchema(t, spec, "Session", body)
	for path, schema := range map[string]string{"/health": "Health", "/api/session": "Session", "/api/settings": "Settings", "/api/cases": "CaseList"} {
		status, body = api.do(t, http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatalf("%s %d", path, status)
		}
		assertWireSchema(t, spec, schema, body)
	}
	status, body = api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "接口契约回归", "contract_type": "procurement_ict"})
	if status != http.StatusCreated {
		t.Fatalf("create %d", status)
	}
	assertWireSchema(t, spec, "ID", body)
	caseID := body["id"].(string)
	api.upload(t, caseID, "procurement", "source.txt", "text/plain", []byte("采购文件质保36个月。"))
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同质保24个月。"))
	_, body = api.do(t, http.MethodGet, "/api/cases/"+caseID, nil)
	assertWireSchema(t, spec, "CaseDetail", body)
	_, body = api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	assertWireSchema(t, spec, "ReviewAccepted", body)
	runID := body["id"].(string)
	_, body = api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	assertWireSchema(t, spec, "Workspace", body)
	finding := body["findings"].([]any)[0].(map[string]any)
	status, _ = api.send(t, http.MethodPost, "/api/cases/"+caseID+"/reviews/"+runID+"/findings/"+finding["id"].(string)+"/decisions",
		map[string]any{"action": "confirm_risk", "reason": "双方原文证据充分", "expected_version": 1}, "contract-test")
	if status != http.StatusNoContent {
		t.Fatalf("decision %d", status)
	}
	_, body = api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+runID, nil)
	assertWireSchema(t, spec, "Workspace", body)
	api.upload(t, caseID, "draft_contract", "revised.txt", "text/plain", []byte("拟签合同质保36个月。"))
	_, body = api.do(t, http.MethodPost, "/api/cases/"+caseID+"/reviews", map[string]any{})
	nextID := body["id"].(string)
	_, body = api.do(t, http.MethodGet, "/api/cases/"+caseID+"/reviews/"+nextID, nil)
	assertWireSchema(t, spec, "Workspace", body)
	if len(body["revisions"].([]any)) == 0 {
		t.Fatal("test must exercise revision response, not only empty arrays")
	}
}

func TestDurableReviewAcceptanceMatchesPublicContract(t *testing.T) {
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := httptest.NewServer(httpapi.Handler(st, httpapi.Options{Durable: true}))
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	api := &testAPI{base: srv.URL, client: &http.Client{Jar: jar}}
	api.login(t, "demo-admin", "admin-demo")
	_, created := api.do(t, http.MethodPost, "/api/cases", map[string]any{"title": "持久审查受理契约", "contract_type": "procurement_ict"})
	caseID := created["id"].(string)
	api.upload(t, caseID, "procurement", "purchase.txt", "text/plain", []byte("采购文件质保36个月。"))
	api.upload(t, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同质保24个月。"))
	base := "/api/cases/" + caseID + "/reviews"
	spec := openAPI(t)
	checkResponse := func(path string, expected, status int, body map[string]any) {
		t.Helper()
		if status != expected {
			t.Fatalf("%s: HTTP %d, want %d: %v", path, status, expected, body)
		}
		operation := spec["paths"].(map[string]any)[path].(map[string]any)["post"].(map[string]any)
		response, ok := operation["responses"].(map[string]any)[strconv.Itoa(status)].(map[string]any)
		if !ok {
			t.Errorf("%s: actual HTTP %d response is absent from the public contract", path, status)
			return
		}
		schema := response["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		if err := wireShape(schema, body, spec["components"].(map[string]any)["schemas"].(map[string]any), path); err != nil {
			t.Error(err)
		}
	}
	durablePost := func(path string) (int, map[string]any) {
		status, raw := api.bytes(t, http.MethodPost, path, strings.NewReader("{}"), "application/json")
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("POST %s returned invalid JSON: %v", path, err)
		}
		return status, body
	}
	status, accepted := durablePost(base)
	checkResponse("/api/cases/{caseID}/reviews", http.StatusAccepted, status, accepted)
	runID, attemptID, jobID := accepted["id"].(string), accepted["attempt_id"].(string), accepted["job_id"].(string)
	status, retried := durablePost(base + "/" + runID + "/retry")
	checkResponse("/api/cases/{caseID}/reviews/{runID}/retry", http.StatusAccepted, status, retried)
	if retried["id"] != runID || retried["attempt_id"] != attemptID || retried["job_id"] != jobID {
		t.Fatalf("retry of an active durable review must return its existing job acceptance: %v", retried)
	}
}

// This checks the JSON Schema keywords used by this contract against live wire
// payloads. It is deliberately a small test helper, not a general OAS validator.
func assertWireSchema(t *testing.T, spec map[string]any, name string, payload any) {
	t.Helper()
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	if err := wireShape(schemas[name].(map[string]any), payload, schemas, name); err != nil {
		t.Fatal(err)
	}
}

func wireShape(schema map[string]any, value any, schemas map[string]any, path string) error {
	if raw, ok := schema["$ref"].(string); ok {
		name := strings.TrimPrefix(raw, "#/components/schemas/")
		return wireShape(schemas[name].(map[string]any), value, schemas, path)
	}
	if options, ok := schema["anyOf"].([]any); ok {
		matched := false
		reasons := make([]string, 0, len(options))
		for _, option := range options {
			err := wireShape(option.(map[string]any), value, schemas, path)
			if err == nil {
				matched = true
				break
			}
			reasons = append(reasons, err.Error())
		}
		if !matched {
			return fmt.Errorf("%s: no anyOf branch matches (%s)", path, strings.Join(reasons, "; "))
		}
	}
	if c, ok := schema["const"]; ok && !reflect.DeepEqual(c, value) {
		return fmt.Errorf("%s: expected const %v, got %v", path, c, value)
	}
	if values, ok := schema["enum"].([]any); ok {
		found := false
		for _, candidate := range values {
			found = found || reflect.DeepEqual(candidate, value)
		}
		if !found {
			return fmt.Errorf("%s: %v not in enum %v", path, value, values)
		}
	}
	if types, ok := schema["type"].([]any); ok {
		if len(types) == 0 {
			return fmt.Errorf("%s: JSON Schema type union is empty", path)
		}
		names := make([]string, 0, len(types))
		seen := make(map[string]bool, len(types))
		for _, rawType := range types {
			name, ok := rawType.(string)
			if !ok || !wireSchemaTypeName(name) {
				return fmt.Errorf("%s: invalid JSON Schema type %v", path, rawType)
			}
			if seen[name] {
				return fmt.Errorf("%s: duplicate JSON Schema type %q", path, name)
			}
			seen[name] = true
			names = append(names, name)
		}
		matched := false
		reasons := make([]string, 0, len(names))
		for _, name := range names {
			if !wireSchemaTypeMatches(name, value) {
				continue
			}
			matched = true
			branch := make(map[string]any, len(schema))
			for keyword, definition := range schema {
				branch[keyword] = definition
			}
			branch["type"] = name
			if err := wireShape(branch, value, schemas, path); err == nil {
				return nil
			} else {
				reasons = append(reasons, err.Error())
			}
		}
		if !matched {
			return fmt.Errorf("%s: value of type %T is not in JSON Schema type union %v", path, value, names)
		}
		return fmt.Errorf("%s: no JSON Schema type union branch matches (%s)", path, strings.Join(reasons, "; "))
	}

	schemaType, hasType := schema["type"]
	if !hasType {
		schemaType = wireSchemaTypeForValue(value)
		if schemaType == "" {
			return fmt.Errorf("%s: unsupported JSON value type %T", path, value)
		}
	}

	switch schemaType {
	case "null":
		if value != nil {
			return fmt.Errorf("%s: expected null", path)
		}
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object", path)
		}
		if required, ok := schema["required"].([]any); ok {
			for _, field := range required {
				if _, exists := object[field.(string)]; !exists {
					return fmt.Errorf("%s: missing required %s", path, field)
				}
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for field, child := range object {
			if property, exists := properties[field]; exists {
				if err := wireShape(property.(map[string]any), child, schemas, path+"."+field); err != nil {
					return err
				}
			} else if schema["additionalProperties"] == false {
				return fmt.Errorf("%s: undocumented field %s", path, field)
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: expected array", path)
		}
		for i, item := range items {
			if err := wireShape(schema["items"].(map[string]any), item, schemas, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s: expected string", path)
		}
		if min, ok := schema["minLength"].(float64); ok && utf8.RuneCountInString(s) < int(min) {
			return fmt.Errorf("%s: string too short", path)
		}
		if max, ok := schema["maxLength"].(float64); ok && utf8.RuneCountInString(s) > int(max) {
			return fmt.Errorf("%s: string too long", path)
		}
		if schema["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				return fmt.Errorf("%s: invalid timestamp", path)
			}
		}
	case "number":
		n, ok := value.(float64)
		if !ok {
			return fmt.Errorf("%s: expected number", path)
		}
		if min, ok := schema["minimum"].(float64); ok && n < min {
			return fmt.Errorf("%s: number below minimum", path)
		}
	case "integer":
		n, ok := value.(float64)
		if !ok || math.Trunc(n) != n {
			return fmt.Errorf("%s: expected integer", path)
		}
		if min, ok := schema["minimum"].(float64); ok && n < min {
			return fmt.Errorf("%s: integer below minimum", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: expected boolean", path)
		}
	default:
		return fmt.Errorf("%s: unsupported test schema type %v", path, schemaType)
	}
	return nil
}

func wireSchemaTypeForValue(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	default:
		return ""
	}
}

func wireSchemaTypeName(name string) bool {
	switch name {
	case "null", "object", "array", "string", "number", "integer", "boolean":
		return true
	default:
		return false
	}
}

func wireSchemaTypeMatches(name string, value any) bool {
	switch name {
	case "null":
		return value == nil
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && math.Trunc(n) == n
	case "boolean":
		_, ok := value.(bool)
		return ok
	default:
		return false
	}
}

func TestWireShapeSupportsJSONSchemaTypeUnionsAndNull(t *testing.T) {
	schema := map[string]any{"type": []any{"string", "null"}, "format": "date-time"}
	for _, value := range []any{nil, "2026-10-24T12:00:00Z"} {
		if err := wireShape(schema, value, map[string]any{}, "value"); err != nil {
			t.Errorf("valid nullable value %v rejected: %v", value, err)
		}
	}
	for _, value := range []any{float64(1), true, "not-a-date"} {
		if err := wireShape(schema, value, map[string]any{}, "value"); err == nil {
			t.Errorf("invalid nullable value %v accepted", value)
		}
	}
	if err := wireShape(map[string]any{"type": []any{"string", "string"}}, "value", map[string]any{}, "value"); err == nil {
		t.Fatal("duplicate JSON Schema union types were accepted")
	}
	nullableInteger := map[string]any{"anyOf": []any{
		map[string]any{"type": "integer", "minimum": float64(0)},
		map[string]any{"type": "null"},
	}}
	for _, value := range []any{nil, float64(0)} {
		if err := wireShape(nullableInteger, value, map[string]any{}, "queue.oldest_wait_ms"); err != nil {
			t.Errorf("valid nullable integer %v rejected: %v", value, err)
		}
	}
	for _, value := range []any{float64(-1), "invalid"} {
		if err := wireShape(nullableInteger, value, map[string]any{}, "queue.oldest_wait_ms"); err == nil {
			t.Errorf("invalid nullable integer %v accepted", value)
		}
	}
}

// TestEveryPublicOperationHasExactlyOneOwningService pins the edge contract:
// the public OpenAPI operation set is partitioned by the shared routing table,
// so no operation is owned twice or left ownerless. Probe paths are served
// locally by every binary and are never proxied.
func TestEveryPublicOperationHasExactlyOneOwningService(t *testing.T) {
	spec := openAPI(t)
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("spec has no paths object")
	}
	probes := map[string]bool{}
	for _, path := range httpapi.HealthPaths {
		probes[path] = true
	}
	roles := []string{"core", "document", "execution"}
	checked := 0
	for path, entry := range paths {
		if probes[path] {
			continue
		}
		operations, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("path %s is not an object", path)
		}
		for method := range operations {
			switch method {
			case "get", "post", "put", "delete", "patch":
			default:
				continue
			}
			owners := 0
			for _, role := range roles {
				if httpapi.OperationOwnedBy(role, strings.ToUpper(method), path) {
					owners++
				}
			}
			if owners != 1 {
				t.Fatalf("%s %s is owned by %d services", strings.ToUpper(method), path, owners)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no public operation was checked")
	}
}
