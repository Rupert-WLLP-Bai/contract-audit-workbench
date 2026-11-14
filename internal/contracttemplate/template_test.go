// File metadata (manifest mirror):
// Purpose: Checks that plain-text template rendering rejects malformed field sets, nested placeholders, invalid dates/decimals and control-character injection while preserving repeated substitutions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package contracttemplate_test

import (
	"testing"

	"contractaudit/internal/contracttemplate"
)

func TestRenderTemplateContract(t *testing.T) {
	content := contracttemplate.Content{
		Body: "{{name}} / {{name}}：{{amount}} 元，日期 {{date}}",
		Fields: []contracttemplate.Field{
			{Key: "name", Label: "名称", Type: "string", Required: true},
			{Key: "amount", Label: "金额", Type: "decimal", Required: true},
			{Key: "date", Label: "日期", Type: "date", Required: true},
		},
	}
	cases := []struct {
		name  string
		input map[string]string
		want  string
	}{
		{"valid repeated field", map[string]string{"name": "甲方", "amount": "10.25", "date": "2024-02-29"}, "甲方 / 甲方：10.25 元，日期 2024-02-29"},
		{"missing field", map[string]string{"name": "甲方", "amount": "10.25"}, ""},
		{"extra field", map[string]string{"name": "甲方", "amount": "10.25", "date": "2024-02-29", "extra": "x"}, ""},
		{"wrong field same count", map[string]string{"name": "甲方", "amount": "10.25", "extra": "x"}, ""},
		{"invalid decimal", map[string]string{"name": "甲方", "amount": "1e3", "date": "2024-02-29"}, ""},
		{"invalid date", map[string]string{"name": "甲方", "amount": "10", "date": "2023-02-29"}, ""},
		{"nested expression", map[string]string{"name": "{{amount}}", "amount": "10", "date": "2024-02-29"}, ""},
		{"embedded newline", map[string]string{"name": "甲\n方", "amount": "10", "date": "2024-02-29"}, ""},
		{"leading newline", map[string]string{"name": "\n甲方", "amount": "10", "date": "2024-02-29"}, ""},
		{"trailing tab", map[string]string{"name": "甲方\t", "amount": "10", "date": "2024-02-29"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := contracttemplate.Render(content, tc.input)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("expected rejected input, got %q", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Render() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestValidateTemplateFieldReferences(t *testing.T) {
	fields := []contracttemplate.Field{{Key: "name", Label: "名称", Type: "string", Required: true}}
	for _, tc := range []struct{ name, body string }{
		{"unknown placeholder", "{{name}} {{other}}"},
		{"nested placeholder", "{{name{{other}}}}"},
		{"declared field absent", "正文"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := contracttemplate.Validate(contracttemplate.Content{Body: tc.body, Fields: fields}); err == nil {
				t.Fatal("expected invalid template")
			}
		})
	}
}
