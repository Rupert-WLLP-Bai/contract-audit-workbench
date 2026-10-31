package apidocs_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"contractaudit/internal/apidocs"
)

func TestContractIsValidJSONWithInternalReferences(t *testing.T) {
	var spec map[string]any
	if err := json.Unmarshal(apidocs.Document(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec["openapi"] != "3.1.1" {
		t.Fatal("unexpected OpenAPI version")
	}
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if raw, ok := v["$ref"].(string); ok {
				if !strings.HasPrefix(raw, "#/") {
					t.Fatalf("external reference breaks offline contract: %s", raw)
				}
				var found any = spec
				for _, part := range strings.Split(strings.TrimPrefix(raw, "#/"), "/") {
					object, ok := found.(map[string]any)
					if !ok {
						t.Fatalf("invalid reference: %s", raw)
					}
					found, ok = object[part]
					if !ok {
						t.Fatalf("missing reference: %s", raw)
					}
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(spec)
	components, ok := spec["components"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI components are missing")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI component schemas are missing")
	}
	var walkSchema func(any)
	walkSchema = func(value any) {
		switch schema := value.(type) {
		case map[string]any:
			if raw, ok := schema["required"]; ok {
				required, ok := raw.([]any)
				if !ok {
					t.Fatalf("OpenAPI schema required keyword is not an array: %T", raw)
				}
				for _, field := range required {
					if _, ok := field.(string); !ok {
						t.Fatalf("OpenAPI schema required keyword contains a non-string: %T", field)
					}
				}
			}
			for _, keyword := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
				if nested, ok := schema[keyword].(map[string]any); ok {
					for _, child := range nested {
						walkSchema(child)
					}
				}
			}
			for _, keyword := range []string{"items", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "not", "if", "then", "else", "contains", "propertyNames"} {
				if child, ok := schema[keyword]; ok {
					walkSchema(child)
				}
			}
			for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
				if children, ok := schema[keyword].([]any); ok {
					for _, child := range children {
						walkSchema(child)
					}
				}
			}
		case []any:
			for _, child := range schema {
				walkSchema(child)
			}
		}
	}
	var findSchemas func(any)
	findSchemas = func(value any) {
		switch object := value.(type) {
		case map[string]any:
			for key, child := range object {
				if key == "schema" {
					walkSchema(child)
					continue
				}
				findSchemas(child)
			}
		case []any:
			for _, child := range object {
				findSchemas(child)
			}
		}
	}
	findSchemas(spec)
	for _, schema := range schemas {
		walkSchema(schema)
	}
	copy := apidocs.Document()
	copy[0] = '!'
	if !json.Valid(apidocs.Document()) {
		t.Fatal("caller mutated embedded contract")
	}
}

func TestEveryPublicHTTPOperationHasResponsesObject(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(apidocs.Document(), &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("OpenAPI paths are missing")
	}
	methods := map[string]bool{
		"get": true, "put": true, "post": true, "delete": true,
		"options": true, "head": true, "patch": true, "trace": true,
	}
	checked := 0
	for path, pathItem := range spec.Paths {
		for method, rawOperation := range pathItem {
			if !methods[method] {
				continue
			}
			var operation map[string]json.RawMessage
			if err := json.Unmarshal(rawOperation, &operation); err != nil {
				t.Fatalf("%s %s is not an operation object: %v", method, path, err)
			}
			var responses map[string]json.RawMessage
			rawResponses, ok := operation["responses"]
			if !ok || json.Unmarshal(rawResponses, &responses) != nil || len(responses) == 0 {
				t.Fatalf("%s %s must have a nonempty responses object", method, path)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no public HTTP operations were checked")
	}
}

func TestAgentReviewSchemaMatchesApprovedWireContract(t *testing.T) {
	var spec map[string]any
	if err := json.Unmarshal(apidocs.Document(), &spec); err != nil {
		t.Fatal(err)
	}
	components := spec["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	statusEnum := func(schemaName string) []any {
		schema := schemas[schemaName].(map[string]any)
		properties := schema["properties"].(map[string]any)
		status := properties["status"].(map[string]any)
		return status["enum"].([]any)
	}
	assertEnum := func(schemaName string, want ...string) {
		t.Helper()
		got := statusEnum(schemaName)
		if len(got) != len(want) {
			t.Fatalf("%s status enum=%v, want %v", schemaName, got, want)
		}
		for i, value := range want {
			if got[i] != value {
				t.Fatalf("%s status enum=%v, want %v", schemaName, got, want)
			}
		}
	}
	assertEnum("AgentReview", "queued", "planning", "running", "completed", "partial", "blocked_live", "cancelled", "failed", "rejected")
	assertEnum("AgentReviewPlan", "not_planned", "accepted", "guard_rejected", "executing", "completed", "partial", "fallback")

	event := schemas["AgentReviewEvent"].(map[string]any)
	properties := event["properties"].(map[string]any)
	round := properties["round"].(map[string]any)
	if round["type"] != "integer" || round["minimum"] != float64(0) || round["maximum"] != float64(3) {
		t.Fatalf("AgentReviewEvent.round schema=%v, want integer bounds 0..3", round)
	}
	required := event["required"].([]any)
	for _, field := range required {
		if field == "round" {
			return
		}
	}
	t.Fatal("AgentReviewEvent.round is not required")
}

func TestContractGETAndHEADDoNotExposeCacheOrSniffing(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		r := httptest.NewRequest(method, "/openapi.json", nil)
		w := httptest.NewRecorder()
		apidocs.Serve(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
			w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("headers: %d %v", w.Code, w.Header())
		}
		if method == http.MethodGet && !bytes.Equal(w.Body.Bytes(), apidocs.Document()) {
			t.Fatal("served contract differs from source")
		}
		if method == http.MethodHead && w.Body.Len() != 0 {
			t.Fatal("HEAD returned a body")
		}
	}
}
