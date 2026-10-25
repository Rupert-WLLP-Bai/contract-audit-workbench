package telecom_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
)

const slaBasis = "；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。"

func source(id, role, text string) rulepack.Source {
	sum := sha256.Sum256([]byte(text))
	return rulepack.Source{ID: id, Version: "parse-v1", TenantID: "tenant-a", CaseID: "case-a", Role: role, Digest: hex.EncodeToString(sum[:]), Text: text}
}

func request(bandwidth, sla, advance string) rulepack.Request {
	procurement := "政企专线采购依据\n电路 LINE-001：最低保障带宽 100 Mbit/s。\n电路 LINE-001：SLA 可用率 99.9%" + slaBasis + "\n电路 LINE-001：合同总额 100000 元，含税。"
	approval := "电路 LINE-001：审批依据，预付款上限 30%，预付款以合同总额为基础，含税。"
	draft := fmt.Sprintf("拟签合同 🧾\n电路 LINE-001：最低保障带宽 %s。\n电路 LINE-001：SLA 可用率 %s%s\n电路 LINE-001：预付款比例 %s，预付款以合同总额为基础，含税。\n电路 LINE-001：合同总额 100000 元，含税。", bandwidth, sla, slaBasis, advance)
	return rulepack.Request{Principal: access.Principal{UserID: "alice", TenantID: "tenant-a", Enabled: true, Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}}, Resource: access.Resource{TenantID: "tenant-a", CaseID: "case-a", OrgID: "org-a", OwnerID: "alice"}, BusinessLine: "ENT", Scenario: "telecom_dedicated_line", ContractType: "service", At: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), Sources: []rulepack.Source{source("procurement", "procurement", procurement), source("approval", "award", approval), source("draft", "draft_contract", draft), source("annex", "attachment", "电路 LINE-001：SLA 可用率 "+sla+slaBasis)}}
}

func cloudRequest(bandwidthOperator, slaOperator string) rulepack.Request {
	procurement := "云服务采购依据 📄\n" +
		"云服务 CLOUD-001：最低保障带宽" + bandwidthOperator + "100 Mbit/s。\n" +
		"云服务 CLOUD-001：SLA 可用率" + slaOperator + "99.95%" + slaBasis + "\n" +
		"云服务 CLOUD-001：合同总额 100000 元，含税。"
	approval := "云服务 CLOUD-001：审批依据，预付款上限 30%，预付款以合同总额为基础，含税。"
	draft := "云平台服务合同 🧾\n" +
		"云服务 CLOUD-001：最低保障带宽 50 Mbit/s。\n" +
		"云服务 CLOUD-001：SLA 可用率 99.9%" + slaBasis + "\n" +
		"云服务 CLOUD-001：预付款比例 40%，预付款以合同总额为基础，含税。\n" +
		"云服务 CLOUD-001：合同总额 100000 元，含税。"
	return rulepack.Request{
		Principal:    access.Principal{UserID: "alice", TenantID: "tenant-a", Enabled: true, Grants: []access.Grant{{Role: "case_owner", Scope: "tenant"}}},
		Resource:     access.Resource{TenantID: "tenant-a", CaseID: "case-a", OrgID: "org-a", OwnerID: "alice"},
		BusinessLine: "ENT", Scenario: "cloud_services", ContractType: "service",
		At:      time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC),
		Sources: []rulepack.Source{source("procurement", "procurement", procurement), source("approval", "award", approval), source("draft", "draft_contract", draft), source("annex", "attachment", "云服务 CLOUD-001：SLA 可用率 99.9%"+slaBasis)},
	}
}

func replaceSource(req *rulepack.Request, id, old, replacement string) {
	for i, s := range req.Sources {
		if s.ID == id {
			req.Sources[i] = source(s.ID, s.Role, strings.ReplaceAll(s.Text, old, replacement))
		}
	}
}

func result(t *testing.T, report rulepack.Report, id string) rulepack.Result {
	t.Helper()
	for _, item := range report.Results {
		if item.RuleID == id {
			return item
		}
	}
	t.Fatalf("required rule %s absent: %+v", id, report)
	return rulepack.Result{}
}

func TestTelecomContractualComparisonsAndUnicodeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, bandwidth, sla, advance string
		want                          rulepack.Relation
	}{
		{"equal-boundaries", "100 Mbit/s", "99.9%", "30%", rulepack.RelationCompatible},
		{"unit-conversion-and-higher-service", "0.1 Gbit/s", "99.95%", "20%", rulepack.RelationCompatible},
		{"three-contract-conflicts", "50 Mbit/s", "99.5%", "40%", rulepack.RelationConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := request(tc.bandwidth, tc.sla, tc.advance)
			report, err := telecom.Execute(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"ENT-TECH-BW-001", "ENT-TECH-SLA-001", "ENT-COM-ADV-001"} {
				item := result(t, report, id)
				if item.Relation != tc.want {
					t.Fatalf("%s = %s (%s), want %s", id, item.Relation, item.ReasonCode, tc.want)
				}
				for _, citation := range item.Citations {
					for _, src := range req.Sources {
						if src.ID == citation.SourceID {
							runes := []rune(src.Text)
							if citation.Start < 0 || citation.End > len(runes) || string(runes[citation.Start:citation.End]) != citation.Quote {
								t.Fatalf("invalid frozen Unicode citation: %+v", citation)
							}
							if src.ID == "draft" && citation.Start == len("拟签合同 🧾\n") {
								t.Fatal("citation used UTF-8 bytes")
							}
						}
					}
				}
			}
			raw, err := telecom.Snapshot(req)
			if err != nil {
				t.Fatal(err)
			}
			pack, err := rulepack.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if pack.Hash() != report.PackHash {
				t.Fatal("attempt manifest does not bind evaluated rules")
			}
		})
	}
}

func TestHistoricalCompilerSnapshotsRemainReadable(t *testing.T) {
	for _, ref := range []string{telecom.CompilerRef, telecom.HistoricalCompilerRef, "telecom-contract-compiler/v4"} {
		if !telecom.IsSyntheticCompiler(ref) {
			t.Fatalf("frozen compiler identity is unreadable: %s", ref)
		}
	}
	for _, ref := range []string{"telecom-contract-compiler/v2", ""} {
		if telecom.IsSyntheticCompiler(ref) {
			t.Fatalf("unknown compiler accepted: %q", ref)
		}
	}
}

func TestWrappedClausesKeepObjectValueUnitAndBasis(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("break-%q", newline), func(t *testing.T) {
			req := request("50 Mbit/s", "99.5%", "40%")
			replaceSource(&req, "draft", "电路 LINE-001：最低保障带宽 50 Mbit/s。", "电路 LINE-001："+newline+"最低保障带宽 50 Mbit/s。")
			replaceSource(&req, "draft", "电路 LINE-001：SLA 可用率 99.5%", "电路 LINE-001："+newline+"SLA 可用率 99.5%")
			replaceSource(&req, "draft", "电路 LINE-001：预付款比例 40%，预付款以合同总额为基础，含税。", "电路 LINE-001："+newline+"预付款比例 40%，"+newline+"预付款以合同总额为基础，含税。")
			facts, err := telecom.Extract(req)
			if err != nil {
				t.Fatal(err)
			}
			draft := sourceText(t, req, "draft")
			want := map[string][]string{
				"ENT-TECH-BW-001":  {"LINE-001", "最低保障带宽", "50 Mbit/s"},
				"ENT-TECH-SLA-001": {"LINE-001", "99.5%", "自然月", "gregorian", "all_service_unavailability", "无排除项"},
				"ENT-COM-ADV-001":  {"LINE-001", "40%", "预付款以合同总额为基础"},
			}
			found := map[string]bool{}
			for _, fact := range facts {
				need, ok := want[fact.RuleID]
				if !ok || len(fact.Citations) != 1 {
					continue
				}
				found[fact.RuleID] = true
				cite := fact.Citations[0]
				runes := []rune(draft)
				if cite.Start < 0 || cite.End > len(runes) || string(runes[cite.Start:cite.End]) != cite.Quote {
					t.Fatalf("%s citation is not a source slice: %+v", fact.RuleID, cite)
				}
				for _, piece := range need {
					if !strings.Contains(cite.Quote, piece) {
						t.Fatalf("%s citation dropped %q: %q", fact.RuleID, piece, cite.Quote)
					}
				}
			}
			for id := range want {
				if !found[id] {
					t.Fatalf("wrapped draft did not extract %s: %+v", id, facts)
				}
			}
			report, err := telecom.Execute(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"ENT-TECH-BW-001", "ENT-TECH-SLA-001", "ENT-COM-ADV-001"} {
				got := result(t, report, id)
				if got.Relation != rulepack.RelationConflict {
					t.Fatalf("wrapped %s = %s (%s), want conflict supported by the citation", id, got.Relation, got.ReasonCode)
				}
			}
		})
	}
}

func sourceText(t *testing.T, req rulepack.Request, id string) string {
	t.Helper()
	for _, source := range req.Sources {
		if source.ID == id {
			return source.Text
		}
	}
	t.Fatalf("source %s missing", id)
	return ""
}

func TestCloudSLALowerBoundAuthoritySupportsThresholdAndKeepsCitation(t *testing.T) {
	req := cloudRequest("不低于", "不低于")
	report, err := telecom.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"CLOUD-TECH-BW-001", "CLOUD-TECH-SLA-001", "CLOUD-COM-ADV-001"} {
		if got := result(t, report, id); got.Relation != rulepack.RelationConflict {
			t.Fatalf("%s = %s (%s), want conflict", id, got.Relation, got.ReasonCode)
		}
	}
	sla := result(t, report, "CLOUD-TECH-SLA-001")
	for _, citation := range sla.Citations {
		if citation.SourceID != "procurement" {
			continue
		}
		runes := []rune(req.Sources[0].Text)
		if citation.Start < 0 || citation.End > len(runes) || citation.Start >= citation.End ||
			string(runes[citation.Start:citation.End]) != citation.Quote || !strings.Contains(citation.Quote, "SLA 可用率不低于99.95%") {
			t.Fatalf("cloud SLA authority citation changed: %+v", citation)
		}
		return
	}
	t.Fatal("cloud SLA result omitted its procurement citation")
}

func TestCloudWrappedSLAAuthorityPreservesCitationAndCodepointRange(t *testing.T) {
	req := cloudRequest("不低于", "不低于")
	replaceSource(&req, "procurement", "公历 gregorian", "公\n\n历 gregorian")
	replaceSource(&req, "draft", "公历 gregorian", "公\n\n历 gregorian")
	replaceSource(&req, "annex", "公历 gregorian", "公\n\n历 gregorian")
	report, err := telecom.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	sla := result(t, report, "CLOUD-TECH-SLA-001")
	if sla.Relation != rulepack.RelationConflict {
		t.Fatalf("wrapped lower-bound authority = %s (%s), want conflict", sla.Relation, sla.ReasonCode)
	}
	for _, citation := range sla.Citations {
		if citation.SourceID != "procurement" {
			continue
		}
		runes := []rune(req.Sources[0].Text)
		if citation.Start < 0 || citation.End > len(runes) || citation.Start >= citation.End ||
			string(runes[citation.Start:citation.End]) != citation.Quote ||
			!strings.Contains(citation.Quote, "公\n\n历 gregorian") ||
			!strings.Contains(citation.Quote, "SLA 可用率不低于99.95%") {
			t.Fatalf("wrapped SLA authority citation changed or has invalid codepoint range: %+v", citation)
		}
		return
	}
	t.Fatal("wrapped SLA result omitted its procurement citation")
}

func TestCloudMinimumAuthoritiesRejectUpperBounds(t *testing.T) {
	report, err := telecom.Execute(context.Background(), cloudRequest("不高于", "不高于"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"CLOUD-TECH-BW-001", "CLOUD-TECH-SLA-001"} {
		got := result(t, report, id)
		if got.Relation != rulepack.RelationInsufficient || got.ReasonCode != "authority_semantics_unsupported" {
			t.Fatalf("unsupported upper-bound authority for %s = %+v, want insufficient", id, got)
		}
	}
}

func TestCloudAuthorityBoundDisagreementIsAmbiguous(t *testing.T) {
	req := cloudRequest("", "不低于")
	req.Sources = append(req.Sources, source("conflicting-procurement", "procurement", "云服务 CLOUD-001：SLA 可用率不高于99.95%"+slaBasis))
	report, err := telecom.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := result(t, report, "CLOUD-TECH-SLA-001")
	if got.Relation != rulepack.RelationInsufficient || got.ReasonCode != "contractual_authority_ambiguous" {
		t.Fatalf("opposing authority bounds = %+v, want ambiguous", got)
	}
}

func TestCloudAdvanceCeilingBoundAuthorityIsSupported(t *testing.T) {
	req := cloudRequest("", "")
	replaceSource(&req, "approval", "预付款上限 30%", "预付款上限不得超过30%")
	report, err := telecom.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := result(t, report, "CLOUD-COM-ADV-001")
	if got.Relation != rulepack.RelationConflict {
		t.Fatalf("bounded advance ceiling = %s (%s), want conflict", got.Relation, got.ReasonCode)
	}
}

func TestCloudMeasuredFactStillRejectsNormativeBound(t *testing.T) {
	req := cloudRequest("不低于", "不低于")
	replaceSource(&req, "draft", "SLA 可用率 99.9%", "SLA 可用率不低于99.9%")
	report, err := telecom.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := result(t, report, "CLOUD-TECH-SLA-001")
	if got.Relation != rulepack.RelationInsufficient || got.ReasonCode != "evidence_does_not_support_fact" {
		t.Fatalf("bounded measured SLA fact = %+v, want conservative evidence rejection", got)
	}
}

func TestTelecomIncomparableEvidenceCannotPass(t *testing.T) {
	for _, tc := range []struct {
		name, rule string
		alter      func(*rulepack.Request)
	}{
		{"different-circuit", "ENT-TECH-BW-001", func(r *rulepack.Request) { replaceSource(r, "draft", "LINE-001", "LINE-002") }},
		{"missing-window", "ENT-TECH-SLA-001", func(r *rulepack.Request) { replaceSource(r, "draft", "自然月", "未指定") }},
		{"different-exclusions", "ENT-TECH-SLA-001", func(r *rulepack.Request) {
			replaceSource(r, "annex", "无排除项", "排除项：planned_maintenance")
		}},
		{"missing-annex", "ENT-TECH-SLA-001", func(r *rulepack.Request) { r.Sources = r.Sources[:3] }},
		{"different-tax-basis", "ENT-COM-ADV-001", func(r *rulepack.Request) { replaceSource(r, "draft", "含税", "不含税") }},
		{"different-total", "ENT-COM-ADV-001", func(r *rulepack.Request) { replaceSource(r, "draft", "100000 元", "90000 元") }},
		{"missing-approval", "ENT-COM-ADV-001", func(r *rulepack.Request) { replaceSource(r, "approval", "审批依据", "参考意见") }},
		{"ambiguous-authority", "ENT-TECH-BW-001", func(r *rulepack.Request) {
			r.Sources = append(r.Sources, source("other-proc", "procurement", "电路 LINE-001：最低保障带宽 200 Mbit/s。"))
		}},
		{"negated-authority", "ENT-TECH-BW-001", func(r *rulepack.Request) {
			replaceSource(r, "procurement", "最低保障带宽 100 Mbit/s", "未承诺最低保障带宽 100 Mbit/s")
		}},
		{"out-of-range-authority", "ENT-TECH-SLA-001", func(r *rulepack.Request) { replaceSource(r, "procurement", "99.9%", "199.9%") }},
		{"missing-all-authority", "ENT-TECH-BW-001", func(r *rulepack.Request) { r.Sources = r.Sources[2:] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := request("100 Mbit/s", "99.9%", "20%")
			tc.alter(&req)
			report, err := telecom.Execute(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			got := result(t, report, tc.rule)
			if got.Relation != rulepack.RelationInsufficient {
				t.Fatalf("%s (%s), want insufficient", got.Relation, got.ReasonCode)
			}
		})
	}
}

func TestTelecomAnnexConflictAndForgedFacts(t *testing.T) {
	req := request("100 Mbit/s", "99.9%", "20%")
	replaceSource(&req, "annex", "99.9%", "99.5%")
	report, err := telecom.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := result(t, report, "ENT-TECH-SLA-001"); got.Relation != rulepack.RelationConflict {
		t.Fatalf("contradictory annex: %+v", got)
	}
	req = request("50 Mbit/s", "99.9%", "20%")
	req.Facts, err = telecom.Extract(req)
	if err != nil {
		t.Fatal(err)
	}
	for i := range req.Facts {
		if req.Facts[i].RuleID == "ENT-TECH-BW-001" {
			req.Facts[i].Value = "100"
		}
	}
	report, err = telecom.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := result(t, report, "ENT-TECH-BW-001"); got.Relation != rulepack.RelationInsufficient {
		t.Fatalf("forged value passed: %+v", got)
	}
}

func TestTelecomAuthorizationAndFrozenSourceIdentity(t *testing.T) {
	req := request("100 Mbit/s", "99.9%", "20%")
	req.Principal.Grants = nil
	if _, err := telecom.Execute(context.Background(), req); !errors.Is(err, rulepack.ErrUnauthorized) {
		t.Fatalf("ungranted principal: %v", err)
	}
	req = request("100 Mbit/s", "99.9%", "20%")
	req.Sources[2].Text += "tamper"
	if _, err := telecom.Execute(context.Background(), req); !errors.Is(err, rulepack.ErrIdentity) {
		t.Fatalf("changed frozen bytes: %v", err)
	}
	req = request("100 Mbit/s", "99.9%", "20%")
	req.Sources = req.Sources[2:]
	req.Sources = append(req.Sources, req.Sources[0])
	if _, err := telecom.Execute(context.Background(), req); !errors.Is(err, rulepack.ErrIdentity) {
		t.Fatalf("duplicate source without authority: %v", err)
	}
	req = request("100 Mbit/s", "99.9%", "20%")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := telecom.Execute(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if utf8.RuneCountInString(req.Sources[2].Text) == len(req.Sources[2].Text) {
		t.Fatal("fixture must exercise Unicode code points")
	}
}

func publishedPackWithBandwidth(t *testing.T, req rulepack.Request, bandwidth string) *rulepack.Pack {
	t.Helper()
	raw, err := telecom.Snapshot(req)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rulepack.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest := pack.Manifest()
	manifest.Lifecycle = rulepack.Lifecycle{State: rulepack.StateDraft}
	for i := range manifest.Rules {
		if manifest.Rules[i].RuleID == "ENT-TECH-BW-001" {
			manifest.Rules[i].Threshold.Value = bandwidth
		}
	}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	pack, err = rulepack.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pack, err = pack.Validate()
	if err != nil {
		t.Fatal(err)
	}
	pack, err = pack.Approve("telecom-test-admin", pack.Hash())
	if err != nil {
		t.Fatal(err)
	}
	pack, err = pack.Publish()
	if err != nil {
		t.Fatal(err)
	}
	return pack
}

func TestExecuteFrozenUsesPublishedPolicyAndKeepsTelecomGuards(t *testing.T) {
	requestForRun := request("100 Mbit/s", "99.9%", "20%")
	pack := publishedPackWithBandwidth(t, requestForRun, "200")
	report, err := telecom.ExecuteFrozen(context.Background(), requestForRun, pack)
	if err != nil {
		t.Fatal(err)
	}
	if report.PackHash != pack.Hash() || result(t, report, "ENT-TECH-BW-001").Relation != rulepack.RelationConflict {
		t.Fatalf("frozen policy threshold was not executed: report=%+v", report)
	}
	synthetic, err := telecom.Execute(context.Background(), requestForRun)
	if err != nil || result(t, synthetic, "ENT-TECH-BW-001").Relation != rulepack.RelationCompatible {
		t.Fatalf("synthetic adapter did not remain source-derived: report=%+v err=%v", synthetic, err)
	}

	for _, test := range []struct {
		name, ruleID, reason string
		alter                func(*rulepack.Request)
	}{
		{"missing contractual authority", "ENT-TECH-BW-001", "contractual_authority_missing", func(req *rulepack.Request) { req.Sources = req.Sources[2:] }},
		{"circuit object mismatch", "ENT-TECH-BW-001", "circuit_object_mismatch", func(req *rulepack.Request) { replaceSource(req, "draft", "LINE-001", "LINE-002") }},
		{"tax basis mismatch", "ENT-COM-ADV-001", "amount_or_tax_basis_incomparable", func(req *rulepack.Request) {
			replaceSource(req, "draft", "100000 元，含税", "100000 元，不含税")
		}},
		{"missing sla annex", "ENT-TECH-SLA-001", "sla_annex_missing", func(req *rulepack.Request) { req.Sources = req.Sources[:3] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := request("100 Mbit/s", "99.9%", "20%")
			test.alter(&candidate)
			got, err := telecom.ExecuteFrozen(context.Background(), candidate, pack)
			if err != nil {
				t.Fatal(err)
			}
			item := result(t, got, test.ruleID)
			if item.Relation != rulepack.RelationInsufficient || item.ReasonCode != test.reason {
				t.Fatalf("frozen guard result = %+v, want insufficient/%s", item, test.reason)
			}
		})
	}
}
