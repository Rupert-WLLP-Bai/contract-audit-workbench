// File metadata (manifest mirror):
// Purpose: 实现 `unmarshalExtensions`, `UnmarshalJSON`, `plain`, `UnmarshalJSON` 等具体接口与边界逻辑，供 internal/apidocs 的调用方使用。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package apidocs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"contractaudit/internal/apidocs/contracts"
)

// Unmarshal helpers retain OpenAPI vendor extensions while decoding the
// hand-maintained source contract into its typed representation.
func unmarshalExtensions(data []byte, target any, extensions *map[string]json.RawMessage) error {
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key, value := range fields {
		if len(key) > 2 && key[:2] == "x-" {
			if *extensions == nil {
				*extensions = map[string]json.RawMessage{}
			}
			(*extensions)[key] = bytes.Clone(value)
		}
	}
	return nil
}
func (v *Contract) UnmarshalJSON(b []byte) error {
	type plain Contract
	return unmarshalExtensions(b, (*plain)(v), &v.Extensions)
}
func (v *PathItem) UnmarshalJSON(b []byte) error {
	type plain PathItem
	return unmarshalExtensions(b, (*plain)(v), &v.Extensions)
}
func (v *Operation) UnmarshalJSON(b []byte) error {
	type plain Operation
	return unmarshalExtensions(b, (*plain)(v), &v.Extensions)
}
func (v *Parameter) UnmarshalJSON(b []byte) error {
	type plain Parameter
	return unmarshalExtensions(b, (*plain)(v), &v.Extensions)
}
func (v *RequestBody) UnmarshalJSON(b []byte) error {
	type plain RequestBody
	return unmarshalExtensions(b, (*plain)(v), &v.Extensions)
}
func (v *Response) UnmarshalJSON(b []byte) error {
	type plain Response
	return unmarshalExtensions(b, (*plain)(v), &v.Extensions)
}
func (v *Header) UnmarshalJSON(b []byte) error {
	type plain Header
	return unmarshalExtensions(b, (*plain)(v), &v.Extensions)
}
func (v *MediaType) UnmarshalJSON(b []byte) error {
	type plain MediaType
	return unmarshalExtensions(b, (*plain)(v), &v.Extensions)
}
func (v *Schema) UnmarshalJSON(b []byte) error {
	type plain Schema
	if err := unmarshalExtensions(b, (*plain)(v), &v.Extensions); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	if raw, ok := fields["properties"]; ok {
		var values map[string]json.RawMessage
		if json.Unmarshal(raw, &values) == nil && len(values) == 0 {
			v.EmptyProperties = true
		}
	}
	if raw, ok := fields["required"]; ok {
		var values []string
		if json.Unmarshal(raw, &values) == nil && len(values) == 0 {
			v.EmptyRequired = true
		}
	}
	return nil
}

// Contract is the typed source model for the public OpenAPI document. Its
// generated value is embedded by openapi.go; openapi.json is an artifact.
type Contract struct {
	OpenAPI           string                     `json:"openapi"`
	JSONSchemaDialect string                     `json:"jsonSchemaDialect,omitempty"`
	Info              Info                       `json:"info"`
	Servers           []Server                   `json:"servers,omitempty"`
	Tags              []Tag                      `json:"tags,omitempty"`
	Security          []SecurityRequirement      `json:"security,omitempty"`
	Paths             map[string]PathItem        `json:"paths"`
	Components        Components                 `json:"components"`
	Extensions        map[string]json.RawMessage `json:"-"`
}

type Info struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
}
type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}
type Tag struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
type SecurityRequirement map[string][]string

type PathItem struct {
	Parameters []Parameter                `json:"parameters,omitempty"`
	Get        *Operation                 `json:"get,omitempty"`
	Post       *Operation                 `json:"post,omitempty"`
	Put        *Operation                 `json:"put,omitempty"`
	Patch      *Operation                 `json:"patch,omitempty"`
	Delete     *Operation                 `json:"delete,omitempty"`
	Options    *Operation                 `json:"options,omitempty"`
	Head       *Operation                 `json:"head,omitempty"`
	Trace      *Operation                 `json:"trace,omitempty"`
	Extensions map[string]json.RawMessage `json:"-"`
}
type Operation struct {
	OperationID string                     `json:"operationId,omitempty"`
	Tags        []string                   `json:"tags,omitempty"`
	Summary     string                     `json:"summary,omitempty"`
	Description string                     `json:"description,omitempty"`
	Parameters  []Parameter                `json:"parameters,omitempty"`
	RequestBody *RequestBody               `json:"requestBody,omitempty"`
	Responses   map[string]Response        `json:"responses"`
	Security    []SecurityRequirement      `json:"security,omitempty"`
	Extensions  map[string]json.RawMessage `json:"-"`
}
type Parameter struct {
	Name        string                     `json:"name"`
	In          string                     `json:"in"`
	Description string                     `json:"description,omitempty"`
	Required    bool                       `json:"required,omitempty"`
	Deprecated  bool                       `json:"deprecated,omitempty"`
	Style       string                     `json:"style,omitempty"`
	Explode     *bool                      `json:"explode,omitempty"`
	Schema      Schema                     `json:"schema,omitempty"`
	Extensions  map[string]json.RawMessage `json:"-"`
}
type RequestBody struct {
	Description string                     `json:"description,omitempty"`
	Required    bool                       `json:"required,omitempty"`
	Content     map[string]MediaType       `json:"content"`
	Extensions  map[string]json.RawMessage `json:"-"`
}
type Response struct {
	Ref         string                     `json:"$ref,omitempty"`
	Description string                     `json:"description,omitempty"`
	Headers     map[string]Header          `json:"headers,omitempty"`
	Content     map[string]MediaType       `json:"content,omitempty"`
	Extensions  map[string]json.RawMessage `json:"-"`
}
type Header struct {
	Description string                     `json:"description,omitempty"`
	Required    bool                       `json:"required,omitempty"`
	Deprecated  bool                       `json:"deprecated,omitempty"`
	Schema      Schema                     `json:"schema,omitempty"`
	Extensions  map[string]json.RawMessage `json:"-"`
}
type MediaType struct {
	Schema     Schema                     `json:"schema,omitempty"`
	Example    json.RawMessage            `json:"example,omitempty"`
	Examples   map[string]json.RawMessage `json:"examples,omitempty"`
	Extensions map[string]json.RawMessage `json:"-"`
}
type Components struct {
	Schemas         map[string]Schema         `json:"schemas"`
	Responses       map[string]Response       `json:"responses,omitempty"`
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes,omitempty"`
}
type SecurityScheme struct {
	Type         string `json:"type"`
	Description  string `json:"description,omitempty"`
	Name         string `json:"name,omitempty"`
	In           string `json:"in,omitempty"`
	Scheme       string `json:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty"`
}

// Schema covers the OpenAPI 3.1 JSON Schema keywords used by this contract.
// Raw JSON is retained only for scalar or boolean-or-schema keyword values;
// object structure, properties, alternatives, references and constraints are
// represented by typed fields.
type Schema struct {
	Ref                  string                     `json:"$ref,omitempty"`
	Title                string                     `json:"title,omitempty"`
	Description          string                     `json:"description,omitempty"`
	Type                 json.RawMessage            `json:"type,omitempty"`
	Format               string                     `json:"format,omitempty"`
	Pattern              string                     `json:"pattern,omitempty"`
	Properties           map[string]Schema          `json:"properties,omitempty"`
	Required             []string                   `json:"required,omitempty"`
	Items                *Schema                    `json:"items,omitempty"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties,omitempty"`
	Enum                 []json.RawMessage          `json:"enum,omitempty"`
	Const                json.RawMessage            `json:"const,omitempty"`
	AnyOf                []Schema                   `json:"anyOf,omitempty"`
	OneOf                []Schema                   `json:"oneOf,omitempty"`
	AllOf                []Schema                   `json:"allOf,omitempty"`
	Not                  *Schema                    `json:"not,omitempty"`
	Minimum              json.RawMessage            `json:"minimum,omitempty"`
	Maximum              json.RawMessage            `json:"maximum,omitempty"`
	ExclusiveMinimum     json.RawMessage            `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum     json.RawMessage            `json:"exclusiveMaximum,omitempty"`
	MultipleOf           json.RawMessage            `json:"multipleOf,omitempty"`
	MinLength            json.RawMessage            `json:"minLength,omitempty"`
	MaxLength            json.RawMessage            `json:"maxLength,omitempty"`
	MinItems             json.RawMessage            `json:"minItems,omitempty"`
	MaxItems             json.RawMessage            `json:"maxItems,omitempty"`
	UniqueItems          json.RawMessage            `json:"uniqueItems,omitempty"`
	MinProperties        json.RawMessage            `json:"minProperties,omitempty"`
	MaxProperties        json.RawMessage            `json:"maxProperties,omitempty"`
	Nullable             json.RawMessage            `json:"nullable,omitempty"`
	Default              json.RawMessage            `json:"default,omitempty"`
	ReadOnly             json.RawMessage            `json:"readOnly,omitempty"`
	WriteOnly            json.RawMessage            `json:"writeOnly,omitempty"`
	Deprecated           json.RawMessage            `json:"deprecated,omitempty"`
	Examples             []json.RawMessage          `json:"examples,omitempty"`
	ContentEncoding      string                     `json:"contentEncoding,omitempty"`
	ContentMediaType     string                     `json:"contentMediaType,omitempty"`
	DependentRequired    map[string][]string        `json:"dependentRequired,omitempty"`
	Extensions           map[string]json.RawMessage `json:"-"`
	EmptyProperties      bool                       `json:"-"`
	EmptyRequired        bool                       `json:"-"`
}

func mergeExtensions(value any, extensions map[string]json.RawMessage) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(extensions) == 0 {
		return raw, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	for key, item := range extensions {
		if _, exists := object[key]; exists {
			return nil, fmt.Errorf("OpenAPI extension %q conflicts with a typed field", key)
		}
		object[key] = bytes.Clone(item)
	}
	return json.Marshal(object)
}
func (v Contract) MarshalJSON() ([]byte, error) {
	type plain Contract
	return mergeExtensions(plain(v), v.Extensions)
}
func (v PathItem) MarshalJSON() ([]byte, error) {
	type plain PathItem
	return mergeExtensions(plain(v), v.Extensions)
}
func (v Operation) MarshalJSON() ([]byte, error) {
	type plain Operation
	return mergeExtensions(plain(v), v.Extensions)
}
func (v Parameter) MarshalJSON() ([]byte, error) {
	type plain Parameter
	return mergeExtensions(plain(v), v.Extensions)
}
func (v RequestBody) MarshalJSON() ([]byte, error) {
	type plain RequestBody
	return mergeExtensions(plain(v), v.Extensions)
}
func (v Response) MarshalJSON() ([]byte, error) {
	type plain Response
	return mergeExtensions(plain(v), v.Extensions)
}
func (v Header) MarshalJSON() ([]byte, error) {
	type plain Header
	return mergeExtensions(plain(v), v.Extensions)
}
func (v MediaType) MarshalJSON() ([]byte, error) {
	type plain MediaType
	return mergeExtensions(plain(v), v.Extensions)
}
func (v Schema) MarshalJSON() ([]byte, error) {
	type plain Schema
	raw, err := mergeExtensions(plain(v), v.Extensions)
	if err != nil {
		return nil, err
	}
	if !v.EmptyProperties && !v.EmptyRequired {
		return raw, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if v.EmptyProperties {
		object["properties"] = json.RawMessage(`{}`)
	}
	if v.EmptyRequired {
		object["required"] = json.RawMessage(`[]`)
	}
	return json.Marshal(object)
}

// SourceContract returns the author-maintained, typed API contract.
func SourceContract() Contract { return sourceContract }

// Generate serializes the typed contract deterministically for go:embed.
func Generate() ([]byte, error) {
	data, err := json.MarshalIndent(sourceContract, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func buildSchemas() (map[string]Schema, error) {
	catalog := contracts.Types()
	schemas := make(map[string]Schema, len(catalog))
	for name, typ := range catalog {
		var rules map[string]json.RawMessage
		if raw := contracts.SchemaRules[name]; raw != "" {
			if err := json.Unmarshal([]byte(raw), &rules); err != nil {
				return nil, err
			}
		}
		value := map[string]any{}
		if _, isRef := rules["$ref"]; !isRef {
			var err error
			value, err = schemaFor(typ, true)
			if err != nil {
				return nil, fmt.Errorf("schema %s: %w", name, err)
			}
		}
		for key, raw := range rules {
			value[key] = raw
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var result Schema
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil, err
		}
		schemas[name] = result
	}
	return schemas, nil
}

func sourceSchemas() map[string]Schema {
	schemas, err := buildSchemas()
	if err != nil {
		panic(fmt.Sprintf("build typed OpenAPI schemas: %v", err))
	}
	return schemas
}

func schemaFor(typ reflect.Type, root bool) (map[string]any, error) {
	pointer := false
	for typ.Kind() == reflect.Pointer {
		pointer = true
		typ = typ.Elem()
	}
	if typ.Name() != "" && !root {
		for name, componentType := range contracts.Types() {
			if componentType == typ {
				return map[string]any{"$ref": "#/components/schemas/" + name}, nil
			}
		}
	}
	_ = pointer // Nullability is preserved by the field's OpenAPI tag.
	switch typ.Kind() {
	case reflect.Struct:
		properties := map[string]any{}
		required := []string{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if !field.IsExported() {
				continue
			}
			parts := strings.Split(field.Tag.Get("json"), ",")
			name := parts[0]
			if name == "" || name == "-" {
				continue
			}
			optional := false
			for _, option := range parts[1:] {
				if option == "omitempty" {
					optional = true
				}
			}
			child, err := schemaFor(field.Type, false)
			if err != nil {
				return nil, err
			}
			if raw := field.Tag.Get("openapi"); raw != "" {
				var annotations map[string]json.RawMessage
				if err := json.Unmarshal([]byte(raw), &annotations); err != nil {
					return nil, err
				}
				for key, item := range annotations {
					child[key] = item
				}
			}
			if contracts.NullableFields[typ.Name()+"."+field.Name] {
				child["nullable"] = true
			}
			if raw := field.Tag.Get("openapi_union"); raw != "" {
				var union map[string]json.RawMessage
				if err := json.Unmarshal([]byte(raw), &union); err != nil {
					return nil, err
				}
				for key, item := range union {
					child[key] = item
				}
			}
			if raw := field.Tag.Get("openapi_items"); raw != "" {
				var itemRules map[string]json.RawMessage
				if err := json.Unmarshal([]byte(raw), &itemRules); err != nil {
					return nil, err
				}
				items, ok := child["items"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s.%s has item metadata without an array", typ.Name(), name)
				}
				for key, value := range itemRules {
					items[key] = value
				}
			}
			properties[name] = child
			if !optional {
				required = append(required, name)
			}
		}
		result := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			result["required"] = required
		}
		return result, nil
	case reflect.Slice, reflect.Array:
		item, err := schemaFor(typ.Elem(), false)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": item}, nil
	case reflect.Map:
		if typ.Elem().Kind() == reflect.Interface {
			return map[string]any{"type": "object", "additionalProperties": true}, nil
		}
		additional, err := schemaFor(typ.Elem(), false)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": additional}, nil
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	default:
		return map[string]any{}, nil
	}
}
