package agentflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func sourceEvidence(id, role, text string) Evidence {
	return Evidence{ID: id, TenantID: "tenant-a", CaseID: "case-a", Version: "v1", Role: role,
		Text: text, SHA256: TextHash(text), DocumentID: "doc-" + id, ParsedDocumentID: "parsed-" + id,
		NormalizedDigest: TextHash(text)}
}

func sourceFinding(t *testing.T, domain Domain, capability string, evidence []Evidence) Finding {
	t.Helper()
	gap := Gap{ID: "gap-" + capability, Domain: domain, Capability: capability, Question: "比较本案冻结来源中的明确字段"}
	for _, tool := range SourceFactTools() {
		if tool.Spec().Domain != domain || !containsCapability(tool.Spec().Capabilities, capability) {
			continue
		}
		findings, err := tool.Execute(context.Background(), ToolInput{TenantID: "tenant-a", CaseID: "case-a", ActorID: "reviewer", Gap: gap, Evidence: evidence})
		if err != nil || len(findings) != 1 {
			t.Fatalf("source tool result = %+v, %v", findings, err)
		}
		if err := validateFindings(context.Background(), findings, domain, capability, evidence); err != nil {
			t.Fatalf("source tool emitted unverified findings: %+v, %v", findings, err)
		}
		return findings[0]
	}
	t.Fatalf("no source tool supports %s/%s", domain, capability)
	return Finding{}
}

func TestSourceAmountComparison(t *testing.T) {
	cases := []struct {
		name        string
		procurement string
		contract    string
		want        string
	}{
		{"equivalent scaled ceiling and contract amount", "审批金额：100万元。", "合同总价：1,000,000元。", "compatible"},
		{"amount exceeds approval ceiling", "采购预算：100万元。", "合同金额：120万元。", "conflict"},
		{"unsupported number words", "审批金额约一百万元。", "合同总价：100万元。", "unsupported"},
		{"unapproved amount is not accepted", "审批金额未经批准：100万元。", "合同总价：100万元。", "unsupported"},
		{"different currencies are unsupported", "审批金额：100万元。", "合同总价：100美元。", "unsupported"},
		{"missing fields", "采购文件列明服务内容。", "拟签合同列明服务内容。", "insufficient"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			finding := sourceFinding(t, Compliance, "amount", []Evidence{
				sourceEvidence("procurement", "procurement", test.procurement),
				sourceEvidence("contract", "draft_contract", test.contract),
			})
			if finding.Relation != test.want {
				t.Fatalf("relation = %q, want %q: %+v", finding.Relation, test.want, finding)
			}
			if test.name == "equivalent scaled ceiling and contract amount" &&
				(len(finding.Facts) != 2 || finding.Facts[0].Value != "1000000" || finding.Facts[1].Value != "1000000") {
				t.Fatalf("scaled facts = %+v", finding.Facts)
			}
		})
	}
}

func TestSourceApprovalAuthorityAndScopeComparison(t *testing.T) {
	cases := []struct {
		name        string
		capability  string
		procurement string
		contract    string
		want        string
	}{
		{"approval matches", "approval_authority", "审批部门：采购委员会。", "授权主体：采购委员会。", "compatible"},
		{"approval differs", "approval_authority", "审批部门：采购委员会。", "授权主体：运营委员会。", "conflict"},
		{"approval missing", "approval_authority", "采购材料已归档。", "合同材料已归档。", "insufficient"},
		{"approval unsupported", "approval_authority", "审批权限依内部流程核验。", "授权主体：运营委员会。", "unsupported"},
		{"scope matches", "effective_scope", "适用范围：专线服务A。", "授权范围：专线服务A。", "compatible"},
		{"scope differs", "effective_scope", "适用范围：专线服务A。", "授权范围：专线服务B。", "conflict"},
		{"party same role matches", "party", "采购人：北方运营有限公司。", "买方：北方运营有限公司。", "compatible"},
		{"party same role differs", "party", "采购人：北方运营有限公司。", "买方：南方运营有限公司。", "conflict"},
		{"party unmapped roles remain insufficient", "party", "采购人：北方运营有限公司。", "甲方：北方运营有限公司。", "insufficient"},
		{"party missing", "party", "采购材料已归档。", "合同材料已归档。", "insufficient"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			finding := sourceFinding(t, Compliance, test.capability, []Evidence{
				sourceEvidence("procurement", "procurement", test.procurement),
				sourceEvidence("contract", "draft_contract", test.contract),
			})
			if finding.Relation != test.want {
				t.Fatalf("relation = %q, want %q: %+v", finding.Relation, test.want, finding)
			}
		})
	}
}

func TestSourceFulfillmentMilestoneAndStartEvent(t *testing.T) {
	cases := []struct {
		name       string
		capability string
		contract   string
		acceptance string
		want       string
	}{
		{"milestone matches normalized date", "milestone", "验收日期：2026-11-01。", "验收日期：2026年11月1日。", "compatible"},
		{"milestone differs", "milestone", "验收日期：2026-11-01。", "验收日期：2026-11-15。", "conflict"},
		{"same date but different milestone", "milestone", "交付日期：2026-11-01。", "验收日期：2026-11-01。", "insufficient"},
		{"unparsed milestone expression", "milestone", "验收日期：合同生效后。", "验收日期：2026-11-01。", "unsupported"},
		{"milestone unsupported", "milestone", "合同约定验收后办理结算。", "验收日期：2026-11-01。", "unsupported"},
		{"start event matches", "explicit_start_event", "服务期限自设备签收之日起开始计算。", "服务期限自设备签收之日起开始计算。", "compatible"},
		{"start event differs", "explicit_start_event", "服务期限自设备签收之日起开始计算。", "服务期限自验收之日起开始计算。", "conflict"},
		{"start event missing", "explicit_start_event", "服务期限为三年。", "服务期限为三年。", "insufficient"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			finding := sourceFinding(t, Fulfillment, test.capability, []Evidence{
				sourceEvidence("contract", "draft_contract", test.contract),
				sourceEvidence("acceptance", "attachment", test.acceptance),
			})
			if finding.Relation != test.want {
				t.Fatalf("relation = %q, want %q: %+v", finding.Relation, test.want, finding)
			}
		})
	}
}

func TestSourceFulfillmentDoesNotSubstituteProcurementForAcceptance(t *testing.T) {
	finding := sourceFinding(t, Fulfillment, "milestone", []Evidence{
		sourceEvidence("contract", "draft_contract", "验收日期：2026-11-01。"),
		sourceEvidence("procurement", "procurement", "验收日期：2026-11-01。"),
	})
	if finding.Relation != "insufficient" || len(finding.Facts) != 0 {
		t.Fatalf("procurement source was used as acceptance evidence: %+v", finding)
	}
}

func TestSourceCounterpartyIdentityAndResponsibility(t *testing.T) {
	cases := []struct {
		name        string
		capability  string
		procurement string
		contract    string
		want        string
	}{
		{"stable ID matches scoped role", "subject_id", "采购文件乙方统一社会信用代码：91350100M000100Y43。", "合同乙方统一社会信用代码：91350100M000100Y43。", "compatible"},
		{"stable ID differs valid checksum", "subject_id", "采购文件乙方统一社会信用代码：91350100M000100Y43。", "合同乙方统一社会信用代码：91350100M000100Y56。", "conflict"},
		{"same ID with different roles", "subject_id", "采购文件甲方统一社会信用代码：91350100M000100Y43。", "合同乙方统一社会信用代码：91350100M000100Y43。", "insufficient"},
		{"unscoped stable ID", "subject_id", "统一社会信用代码：91350100M000100Y43。", "统一社会信用代码：91350100M000100Y43。", "insufficient"},
		{"invalid stable ID checksum", "subject_id", "统一社会信用代码：91350100M000100Y44。", "统一社会信用代码：91350100M000100Y44。", "unsupported"},
		{"signing party same-name text requires stable IDs", "signing_party", "签约方：北方运营有限公司。", "签署方：北方运营有限公司。", "insufficient"},
		{"signing party differs", "signing_party", "签约方：北方运营有限公司。", "签署方：南方运营有限公司。", "conflict"},
		{"signing party same name and linked ID matches", "signing_party", "甲方签约方：北方运营有限公司，统一社会信用代码：91350100M000100Y43。", "甲方签署方：北方运营有限公司，统一社会信用代码：91350100M000100Y43。", "compatible"},
		{"same signing name but different ID conflicts", "signing_party", "甲方签约方：北方运营有限公司，统一社会信用代码：91350100M000100Y43。", "甲方签署方：北方运营有限公司，统一社会信用代码：91350100M000100Y56。", "conflict"},
		{"same signing ID under different roles remains insufficient", "signing_party", "甲方签约方：北方运营有限公司，统一社会信用代码：91350100M000100Y43。", "乙方签署方：北方运营有限公司，统一社会信用代码：91350100M000100Y43。", "insufficient"},
		{"payee same-name text requires stable IDs", "payee", "收款方：北方运营有限公司。", "付款对象：北方运营有限公司。", "insufficient"},
		{"payee differs", "payee", "收款方：北方运营有限公司。", "付款对象：南方运营有限公司。", "conflict"},
		{"same payee name but changed stable ID conflicts", "payee", "乙方收款方：北方运营有限公司，统一社会信用代码：91350100M000100Y43。", "乙方付款对象：北方运营有限公司，统一社会信用代码：91350100M000100Y56。", "conflict"},
		{"payee entity changed", "payee", "乙方收款方：北方运营有限公司。", "乙方付款对象：南方运营有限公司。", "conflict"},
		{"responsibility matches", "responsibility", "供应商负责现场网络维护。", "供应商负责现场网络维护。", "compatible"},
		{"responsibility differs", "responsibility", "供应商负责现场网络维护。", "供应商负责设备维护。", "conflict"},
		{"responsibility missing", "responsibility", "供应商提供服务。", "供应商提供服务。", "insufficient"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			finding := sourceFinding(t, Counterparty, test.capability, []Evidence{
				sourceEvidence("procurement", "procurement", test.procurement),
				sourceEvidence("contract", "draft_contract", test.contract),
			})
			if finding.Relation != test.want {
				t.Fatalf("relation = %q, want %q: %+v", finding.Relation, test.want, finding)
			}
			if strings.Contains(finding.Summary, "外部登记") && finding.Relation == "compatible" {
				t.Fatalf("summary incorrectly implies external lookup: %+v", finding)
			}
		})
	}
}

func TestSourceApprovalValidityUsesExplicitReferenceDates(t *testing.T) {
	cases := []struct {
		name       string
		approval   string
		contract   string
		want       string
		wantReason string
	}{
		{"inclusive lower boundary", "审批有效期自2026-01-01至2026-12-31。", "合同签订日期：2026-01-01。", "compatible", "contract_reference_within_approval_validity"},
		{"inclusive upper boundary", "审批有效期自2026-01-01至2026-12-31。", "合同生效日期：2026-12-31。", "compatible", "contract_reference_within_approval_validity"},
		{"already expired at contract date", "审批有效期自2026-01-01至2026-12-31。", "合同签订日期：2025-12-31。", "conflict", "contract_reference_outside_approval_validity"},
		{"not yet effective at contract date", "审批有效期自2026-01-01至2026-12-31。", "合同签订日期：2027-01-01。", "conflict", "contract_reference_outside_approval_validity"},
		{"no explicit reference date does not use current date", "审批有效期自2026-01-01至2026-12-31。", "合同文本未提供明确日期。", "insufficient", "contract_reference_date_missing"},
		{"reference date mentioned without value", "审批有效期自2026-01-01至2026-12-31。", "合同签订日期待确认。", "unsupported", "contract_reference_date_unsupported"},
		{"approval interval missing an endpoint", "审批有效期自2026-01-01起。", "合同签订日期：2026-06-01。", "unsupported", "approval_validity_expression_unsupported"},
		{"reversed interval", "审批有效期自2026-12-31至2026-01-01。", "合同签订日期：2026-06-01。", "unsupported", "approval_validity_interval_invalid"},
		{"timestamp without timezone is unsupported", "审批有效期自2026-01-01T00:00:00至2026-12-31T23:59:59。", "合同签订时间：2026-06-01T12:00:00+08:00。", "unsupported", "approval_validity_expression_unsupported"},
		{"date precision cannot be mixed with timestamp", "审批有效期自2026-01-01T00:00:00+08:00至2026-12-31T23:59:59+08:00。", "合同签订日期：2026-06-01。", "unsupported", "approval_validity_timezone_or_precision_mismatch"},
		{"equivalent explicit timezone instants compare exactly", "审批有效期自2026-01-01T00:00:00+08:00至2026-12-31T23:59:59+08:00。", "合同签订时间：2025-12-31T16:00:00Z。", "compatible", "contract_reference_within_approval_validity"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			finding := sourceFinding(t, Compliance, "approval_validity", []Evidence{
				sourceEvidence("approval", "procurement", test.approval),
				sourceEvidence("contract", "draft_contract", test.contract),
			})
			if finding.Relation != test.want || finding.ReasonCode != test.wantReason {
				t.Fatalf("finding = %+v, want relation=%q reason=%q", finding, test.want, test.wantReason)
			}
			if test.want == "compatible" && (len(finding.Facts) != 3 || len(finding.Citations) != 3) {
				t.Fatalf("validity result omitted source-linked date facts/citations: %+v", finding)
			}
		})
	}
}

func TestSourceSemanticFactsRejectTamperedValue(t *testing.T) {
	evidence := []Evidence{
		sourceEvidence("procurement", "procurement", "统一社会信用代码：91350100M000100Y43。"),
		sourceEvidence("contract", "draft_contract", "统一社会信用代码：91350100M000100Y43。"),
	}
	finding := sourceFinding(t, Counterparty, "subject_id", evidence)
	finding.Facts[0].Value = "91350100M000100Y44"
	if !errors.Is(validateFindings(context.Background(), []Finding{finding}, Counterparty, "subject_id", evidence), ErrEvidence) {
		t.Fatal("tampered source fact passed typed citation verification")
	}
}

func TestSourceToolsRejectForeignCaseEvidence(t *testing.T) {
	tool := SourceFactTools()[0]
	_, err := tool.Execute(context.Background(), ToolInput{
		TenantID: "tenant-a", CaseID: "case-a", ActorID: "reviewer",
		Gap: Gap{ID: "gap-amount", Domain: Compliance, Capability: "amount"},
		Evidence: []Evidence{
			sourceEvidence("procurement", "procurement", "审批金额：100万元。"),
			{ID: "foreign", TenantID: "tenant-a", CaseID: "case-b", Version: "v1", Role: "draft_contract",
				Text: "合同金额：100万元。", SHA256: TextHash("合同金额：100万元。"), DocumentID: "doc-foreign",
				ParsedDocumentID: "parsed-foreign", NormalizedDigest: TextHash("合同金额：100万元。")},
		},
	})
	if !errors.Is(err, ErrInput) {
		t.Fatalf("cross-case evidence error = %v", err)
	}
}
func TestSourceFactCitationBindsParsedIdentityAndCodepointOffsets(t *testing.T) {
	evidence := []Evidence{
		sourceEvidence("procurement", "procurement", "采购依据：审批金额100万元。"),
		sourceEvidence("contract", "draft_contract", "合同金额：100万元。"),
	}
	finding := sourceFinding(t, Compliance, "amount", evidence)
	citation := finding.Facts[0].Citation
	if citation.Start != 5 || citation.End-citation.Start != len([]rune(citation.Quote)) {
		t.Fatalf("citation offsets are not Unicode codepoints: %+v", citation)
	}
	finding.Facts[0].Citation.ParsedDocumentID = "parsed-other"
	if !errors.Is(validateFindings(context.Background(), []Finding{finding}, Compliance, "amount", evidence), ErrEvidence) {
		t.Fatal("fact citation bound to another parsed document was accepted")
	}
}

func TestSourceToolRejectsMissingFrozenParseIdentity(t *testing.T) {
	evidence := []Evidence{
		sourceEvidence("procurement", "procurement", "审批金额：100万元。"),
		sourceEvidence("contract", "draft_contract", "合同金额：100万元。"),
	}
	evidence[0].ParsedDocumentID = ""
	tool := SourceFactTools()[0]
	_, err := tool.Execute(context.Background(), ToolInput{
		TenantID: "tenant-a", CaseID: "case-a", ActorID: "reviewer",
		Gap: Gap{ID: "gap-amount", Domain: Compliance, Capability: "amount"}, Evidence: evidence,
	})
	if !errors.Is(err, ErrInput) {
		t.Fatalf("source without frozen parse identity error = %v", err)
	}
}
