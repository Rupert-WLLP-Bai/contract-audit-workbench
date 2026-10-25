package store

import (
	"encoding/json"
	"testing"

	"contractaudit/internal/docir"
	"contractaudit/internal/model"
)

func TestCloudModelDraftsBindExactPolicyAndContractEvidence(t *testing.T) {
	quote := "服务中断的赔偿按照损失计算。"
	text := "第九条 服务中断时，" + quote
	ir := docir.Build(text)
	document := Interpreted{
		Document: Document{ID: "contract-1", Role: "draft_contract", Digest: "source-digest"},
		Ready:    true, Text: text, IR: ir,
		ParsedDocument: &ParsedDocument{
			ID: "parsed-contract-1", DocumentID: "contract-1", OriginalDigest: "source-digest",
			NormalizedDigest: ir.NormalizedDigest, ArtifactDigest: "artifact-digest", Status: "ready",
		},
	}
	policy := model.CloudPolicy{ID: "cloud-procurement-policy", Version: "1.0.0", Body: "2.4 服务中断责任"}
	review := model.CloudReview{Findings: []model.CloudFinding{{
		Domain: "liability", PolicyClause: "2.4", Claim: "赔偿责任缺少累计上限。", DocumentID: document.Document.ID, Quote: quote,
	}}}
	drafts, sources, err := cloudModelDrafts(review, []Interpreted{document}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || len(sources) != 1 || drafts[0].Origin != "model" || drafts[0].Relation != "conflict" ||
		drafts[0].Claim != review.Findings[0].Claim || len(drafts[0].Anchors) != 1 || drafts[0].Anchors[0].Quote != quote {
		t.Fatalf("unexpected semantic draft: drafts=%+v sources=%+v", drafts, sources)
	}
	if sources[0].PolicyID != policy.ID || sources[0].PolicyVersion != policy.Version || sources[0].Clause != "3.2" {
		t.Fatalf("severity provenance was not assigned from policy classification: %+v", sources[0])
	}

	invalid := review
	invalid.Findings = append([]model.CloudFinding(nil), review.Findings...)
	invalid.Findings[0].PolicyClause = "2.5"
	if _, _, err := cloudModelDrafts(invalid, []Interpreted{document}, policy); err == nil {
		t.Fatal("mismatched policy clause was accepted")
	}
	invalid = review
	invalid.Findings = append([]model.CloudFinding(nil), review.Findings...)
	invalid.Findings[0].Quote = "不存在于合同的引用"
	if _, _, err := cloudModelDrafts(invalid, []Interpreted{document}, policy); err == nil {
		t.Fatal("unmatched contract quote was accepted")
	}
	otherDocument := document
	otherDocument.Document.Role = "procurement"
	if _, _, err := cloudModelDrafts(review, []Interpreted{otherDocument}, policy); err == nil {
		t.Fatal("procurement evidence was accepted as a contract citation")
	}
}

func TestLegacyFindingSeveritySerializesAsNull(t *testing.T) {
	body, err := json.Marshal(Finding{ID: "legacy-finding"})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"severity", "severity_source"} {
		if value, exists := payload[field]; !exists || value != nil {
			t.Fatalf("legacy %s = %#v, want explicit null: %s", field, value, body)
		}
	}
}
