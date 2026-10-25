package model

import (
	"errors"
	"testing"
)

func TestGatewayIDStripsCatalogPrefix(t *testing.T) {
	if got := GatewayID("panda-local/example/chat"); got != "example/chat" {
		t.Fatalf("chat %s", got)
	}
	if got := GatewayID("panda-local/example/vision"); got != "example/vision" {
		t.Fatalf("ocr %s", got)
	}
	if got := GatewayID("codex/gpt-6-luna"); got != "codex/gpt-6-luna" {
		t.Fatalf("bare %s", got)
	}
}

func TestParseWarrantyAcceptsFencedJSON(t *testing.T) {
	raw := "```json\n{\"claim\":\"两份月数不同\",\"relation\":\"conflict\",\"disposition\":\"confirm_risk\",\"quotes\":[{\"document_id\":\"a\",\"quote\":\"质保36个月\"}]}\n```"
	got, err := ParseWarranty(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Relation != "conflict" || got.Disposition != "confirm_risk" || len(got.Quotes) != 1 || got.Quotes[0].Quote != "质保36个月" {
		t.Fatalf("%+v", got)
	}
}

func TestParseWarrantyRejectsMalformedOutput(t *testing.T) {
	cases := []string{
		"不是 JSON",
		`{"claim":"","relation":"conflict","disposition":"confirm_risk","quotes":[{"document_id":"a","quote":"质保36个月"}]}`,
		`{"claim":"有冲突","relation":"maybe","disposition":"confirm_risk","quotes":[{"document_id":"a","quote":"质保36个月"}]}`,
		`{"claim":"有冲突","relation":"conflict","disposition":"shrug","quotes":[{"document_id":"a","quote":"质保36个月"}]}`,
		`{"claim":"有冲突","relation":"conflict","disposition":"confirm_risk","quotes":[]}`,
	}
	for _, raw := range cases {
		_, err := ParseWarranty(raw)
		if !errors.Is(err, ErrUnusable) {
			t.Fatalf("%q: %v", raw, err)
		}
	}
}

func TestParseCloudContractAcceptsExactPolicyCitations(t *testing.T) {
	raw := `{"findings":[{"domain":"liability","policy_clause":"2.4","claim":"服务中断赔偿责任缺少累计上限","document_id":"contract-1","quote":"服务中断的赔偿按照损失计算"}]}`
	got, err := ParseCloudContract(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Findings) != 1 || got.Findings[0].Domain != "liability" || got.Findings[0].PolicyClause != "2.4" ||
		got.Findings[0].DocumentID != "contract-1" || got.Findings[0].Quote != "服务中断的赔偿按照损失计算" {
		t.Fatalf("unexpected cloud review: %+v", got)
	}
}

func TestParseCloudContractRejectsInvalidDomainClauseOrEvidence(t *testing.T) {
	cases := []string{
		`{"findings":[{"domain":"liability","policy_clause":"2.5","claim":"责任缺口","document_id":"contract-1","quote":"合同原文"}]}`,
		`{"findings":[{"domain":"security","policy_clause":"2.5","claim":"安全缺口","document_id":"contract-1","quote":"合同原文"}]}`,
		`{"findings":[{"domain":"renewal","policy_clause":"2.7","claim":"续约缺口","document_id":"contract-1","quote":""}]}`,
		`{"findings":[{"domain":"liability","policy_clause":"2.4","claim":"责任缺口","document_id":"contract-1","quote":"原文","severity":"high"}]}`,
		`{"findings":[{"domain":"liability","policy_clause":"2.4","claim":"责任缺口","document_id":"contract-1","quote":"原文"}]} trailing`,
		`{"findings":[{"domain":"liability","policy_clause":"2.4","claim":"责任缺口","document_id":"contract-1","quote":"原文"},{"domain":"liability","policy_clause":"2.4","claim":"重复责任缺口","document_id":"contract-1","quote":"另一句原文"}]}`,
	}
	for _, raw := range cases {
		if _, err := ParseCloudContract(raw); !errors.Is(err, ErrUnusable) {
			t.Fatalf("%q: got error %v, want ErrUnusable", raw, err)
		}
	}
}
