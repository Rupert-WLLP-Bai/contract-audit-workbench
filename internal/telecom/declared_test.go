// File metadata (manifest mirror):
// Purpose: Exercises the declared-compiler pack validation, host-vocabulary fact binding and frozen-pack execution over the existing typed telecom extractor.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-15
// Updated: 2026-11-15
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package telecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/rulepack"
)

const declaredSLA = "；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。"

func declaredSource(id, role, text string) rulepack.Source {
	sum := sha256.Sum256([]byte(text))
	return rulepack.Source{ID: id, Version: "parse-v1", TenantID: "tenant-a", CaseID: "case-a", Role: role, Digest: hex.EncodeToString(sum[:]), Text: text}
}

func declaredRequest() rulepack.Request {
	procurement := "政企专线采购依据\n电路 LINE-001：最低保障带宽 100 Mbit/s。\n电路 LINE-001：SLA 可用率 99.9%" + declaredSLA + "\n电路 LINE-001：合同总额 100000 元，含税。"
	approval := "电路 LINE-001：审批依据，预付款上限 30%，预付款以合同总额为基础，含税。"
	draft := "拟签合同\n电路 LINE-001：最低保障带宽 80 Mbit/s。\n电路 LINE-001：SLA 可用率 99.5%" + declaredSLA + "\n电路 LINE-001：预付款比例 20%，预付款以合同总额为基础，含税。\n电路 LINE-001：合同总额 100000 元，含税。"
	return rulepack.Request{
		Principal:    access.Principal{UserID: "alice", TenantID: "tenant-a", Enabled: true, Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}},
		Resource:     access.Resource{TenantID: "tenant-a", CaseID: "case-a", OrgID: "org-a", OwnerID: "alice"},
		BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service",
		At: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC),
		Sources: []rulepack.Source{
			declaredSource("procurement", "procurement", procurement),
			declaredSource("approval", "award", approval),
			declaredSource("draft", "draft_contract", draft),
			declaredSource("annex", "attachment", "电路 LINE-001：SLA 可用率 99.5%"+declaredSLA),
		},
	}
}

func declaredTestPack(t *testing.T, request rulepack.Request) *rulepack.Pack {
	t.Helper()
	raw, err := Snapshot(request)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rulepack.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return pack
}

func TestDeclaredComparatorIndexMapsEveryCompiler(t *testing.T) {
	cases := map[string]int{
		rulepack.ComparatorMinimumBandwidth:      0,
		rulepack.ComparatorMonthlySLA:            1,
		rulepack.ComparatorAdvancePaymentCeiling: 2,
		"unknown":                                -1,
		"":                                       -1,
	}
	for comparator, want := range cases {
		if got := declaredComparatorIndex(comparator); got != want {
			t.Fatalf("comparator %q mapped to %d, want %d", comparator, got, want)
		}
	}
}

func TestDeclaredScopeAndExtractionRequestKeepBoundaries(t *testing.T) {
	request := declaredRequest()
	scope := declaredScope(request)
	if scope.BusinessLine != "ENT" || scope.Scenario != "telecom_dedicated_line" || scope.ContractType != "service" {
		t.Fatalf("declared scope is %+v", scope)
	}
	request.Facts = []rulepack.Fact{{ID: "fact-1"}}
	proxy := declaredExtractionRequest(request)
	if proxy.BusinessLine != "ENT" || proxy.Scenario != "telecom_dedicated_line" || proxy.ContractType != "service" {
		t.Fatalf("extraction request is %+v", proxy)
	}
	if proxy.Principal.UserID != request.Principal.UserID || proxy.Resource.CaseID != request.Resource.CaseID || len(proxy.Facts) != 1 || len(proxy.Sources) != len(request.Sources) {
		t.Fatalf("extraction request lost principal, resource or sources: %+v", proxy)
	}
}

func TestValidateDeclaredPackRequiresPublishedExactScope(t *testing.T) {
	request := declaredRequest()
	pack := declaredTestPack(t, request)
	if err := ValidateDeclaredPack(pack, declaredScope(request), "tenant-a"); err != nil {
		t.Fatalf("published compiler pack was rejected: %v", err)
	}
	if err := ValidateDeclaredPack(nil, declaredScope(request), "tenant-a"); err == nil {
		t.Fatal("nil pack was accepted")
	}
	if err := ValidateDeclaredPack(pack, declaredScope(request), ""); err == nil {
		t.Fatal("empty tenant was accepted")
	}
	if err := ValidateDeclaredPack(pack, declaredScope(request), "tenant-b"); err == nil {
		t.Fatal("tenant mismatch was accepted")
	}
	other := declaredScope(request)
	other.Scenario = "cloud_services"
	if err := ValidateDeclaredPack(pack, other, "tenant-a"); err == nil {
		t.Fatal("scope mismatch was accepted")
	}
	raw, err := pack.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["lifecycle"] = map[string]any{"state": "draft"}
	draftRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := rulepack.Parse(draftRaw)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDeclaredPack(draft, declaredScope(request), "tenant-a"); err == nil {
		t.Fatal("draft pack was accepted")
	}
}

func TestExtractDeclaredBindsFactsToPublishedRules(t *testing.T) {
	request := declaredRequest()
	pack := declaredTestPack(t, request)
	facts, err := ExtractDeclared(request, pack)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) == 0 {
		t.Fatal("declared extraction returned no facts")
	}
	declared := make(map[string]bool, len(pack.Manifest().Rules))
	for _, rule := range pack.Manifest().Rules {
		declared[rule.RuleID] = true
	}
	for _, fact := range facts {
		if !declared[fact.RuleID] || !strings.HasPrefix(fact.ID, "fact/") || fact.Citations == nil {
			t.Fatalf("declared fact is not bound to the pack: %+v", fact)
		}
	}
	if _, err := ExtractDeclared(request, nil); err == nil {
		t.Fatal("nil pack was extracted")
	}
	tampered := request
	tampered.Sources = append([]rulepack.Source{}, request.Sources...)
	tampered.Sources[2].Text += "tampered"
	if _, err := ExtractDeclared(tampered, pack); !errors.Is(err, rulepack.ErrIdentity) {
		t.Fatalf("tampered source was extracted: %v", err)
	}
}

func TestExecuteDeclaredRunsFrozenPackAndRejectsInvalidContext(t *testing.T) {
	request := declaredRequest()
	pack := declaredTestPack(t, request)
	report, err := ExecuteDeclared(context.Background(), request, pack)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) == 0 || report.PackID != pack.Manifest().ID {
		t.Fatalf("declared execution returned %+v", report)
	}
	withFacts := request
	withFacts.Facts, err = ExtractDeclared(request, pack)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteDeclared(context.Background(), withFacts, pack); err != nil {
		t.Fatalf("pre-extracted facts were rejected: %v", err)
	}
	unauthorized := request
	unauthorized.Principal.Grants = nil
	if _, err := ExecuteDeclared(context.Background(), unauthorized, pack); !errors.Is(err, rulepack.ErrUnauthorized) {
		t.Fatalf("unauthorized execution returned %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ExecuteDeclared(cancelled, request, pack); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled execution returned %v", err)
	}
	noTime := request
	noTime.At = time.Time{}
	if _, err := ExecuteDeclared(context.Background(), noTime, pack); !errors.Is(err, rulepack.ErrInvalidRequest) {
		t.Fatalf("execution without evaluation time returned %v", err)
	}
	if _, err := ExecuteDeclared(context.Background(), request, nil); !errors.Is(err, rulepack.ErrInvalidManifest) {
		t.Fatalf("execution without a pack returned %v", err)
	}
	tampered := request
	tampered.Sources = append([]rulepack.Source{}, request.Sources...)
	tampered.Sources[2].Text += "tampered"
	if _, err := ExecuteDeclared(context.Background(), tampered, pack); !errors.Is(err, rulepack.ErrIdentity) {
		t.Fatalf("execution over tampered sources returned %v", err)
	}
}
