package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func TestSubjectCorrectionBasisRequiresSameCaseAndExactHistoricalParse(t *testing.T) {
	env, ok := newDurableRoleEnvironment(t)
	if !ok {
		t.Skip("isolated database and migrator required")
	}
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	core, document := env.openRole(t, "core"), env.openRole(t, "document")
	ctx := context.Background()
	caseA, err := core.CreateCase(admin, "依据来源A", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	caseB, err := core.CreateCase(admin, "更正来源B", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	subject, _, err := core.CreateCaseSubject(ctx, admin, caseB, CaseSubjectCreateInput{Role: SubjectRoleSupplier, Subject: SubjectCreateInput{DisplayName: "待核验主体"}}, "basis-scope-subject")
	if err != nil {
		t.Fatal(err)
	}
	const evidenceText = "该主体名称及履约材料仅作为本次更正依据。"
	uploadA, err := document.UploadMaterial(ctx, admin, caseA, "attachment", "source-a.txt", "text/plain", []byte(evidenceText), MaterialUploadOptions{IdempotencyKey: "basis-scope-upload-a"})
	if err != nil {
		t.Fatal(err)
	}
	uploadB, err := document.UploadMaterial(ctx, admin, caseB, "attachment", "source-b.txt", "text/plain", []byte(evidenceText), MaterialUploadOptions{IdempotencyKey: "basis-scope-upload-b"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := document.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	var sourceA, sourceB Document
	for _, item := range state.Documents {
		if item.ID == uploadA.DocumentID {
			sourceA = item
		}
		if item.ID == uploadB.DocumentID {
			sourceB = item
		}
	}
	if sourceA.ID == "" || sourceB.ID == "" {
		t.Fatal("uploaded basis source document missing")
	}
	parsedA, err := document.ensureParsed(ctx, admin, sourceA, false)
	if err != nil || parsedA.Status != "ready" {
		t.Fatalf("parse source A: parsed=%+v err=%v", parsedA, err)
	}
	parsedB1, err := document.ensureParsed(ctx, admin, sourceB, false)
	if err != nil || parsedB1.Status != "ready" {
		t.Fatalf("parse source B initial version: parsed=%+v err=%v", parsedB1, err)
	}
	parsedB2, err := document.ensureParsed(ctx, admin, sourceB, true)
	if err != nil || parsedB2.Status != "ready" || parsedB2.ID == parsedB1.ID {
		t.Fatalf("append a second parse for historical-anchor coverage: first=%+v second=%+v err=%v", parsedB1, parsedB2, err)
	}
	makeBasis := func(materialID string, version int, documentID, parsedID string) json.RawMessage {
		t.Helper()
		quote := []rune(evidenceText)
		digest := sha256.Sum256([]byte(string(quote)))
		payload, err := json.Marshal([]materialAnchorInput{{Kind: "material_anchor", MaterialID: materialID, MaterialVersion: version, DocumentID: documentID, ParsedDocumentID: parsedID, Start: 0, End: len(quote), QuoteDigest: hex.EncodeToString(digest[:])}})
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	baseInput := SubjectChangeCreateInput{Target: SubjectChangeTarget{Kind: "subject", SubjectID: subject.SubjectID}, ExpectedSubjectVersion: 1,
		Patch: json.RawMessage(`{"display_name":"更正后的主体名称"}`), BasisNote: "按固定材料依据核对主体名称"}
	for _, tc := range []struct {
		name   string
		caseID string
		basis  json.RawMessage
	}{
		{name: "cross-case material", caseID: caseB, basis: makeBasis(uploadA.MaterialID, uploadA.Version, uploadA.DocumentID, parsedA.ID)},
		{name: "wrong parse for pinned document", caseID: caseB, basis: makeBasis(uploadB.MaterialID, uploadB.Version, uploadB.DocumentID, parsedA.ID)},
		{name: "wrong quote digest", caseID: caseB, basis: func() json.RawMessage {
			var items []materialAnchorInput
			if err := json.Unmarshal(makeBasis(uploadB.MaterialID, uploadB.Version, uploadB.DocumentID, parsedB1.ID), &items); err != nil {
				t.Fatal(err)
			}
			items[0].QuoteDigest = strings.Repeat("0", 64)
			payload, err := json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
			return payload
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := baseInput
			input.BasisRefs = tc.basis
			if _, _, err := core.ProposeSubjectChange(ctx, admin, tc.caseID, input, "basis-invalid-"+tc.name); err == nil {
				t.Fatal("invalid case or parsed-document identity was accepted")
			} else {
				var apiErr *fault.Error
				if !errors.As(err, &apiErr) || apiErr.Status != 422 || apiErr.Code != "invalid" {
					t.Fatalf("basis mismatch did not return typed invalid request: %v", err)
				}
			}
		})
	}
	before, err := core.snapshotAs(admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range before.Runs {
		if run.CaseID == caseB {
			t.Fatal("basis test unexpectedly created a review run")
		}
	}
	for _, finding := range before.Findings {
		if finding.CaseID == caseB {
			t.Fatal("basis test unexpectedly created findings")
		}
	}
	input := baseInput
	input.BasisRefs = makeBasis(uploadB.MaterialID, uploadB.Version, uploadB.DocumentID, parsedB1.ID)
	proposal, replayed, err := core.ProposeSubjectChange(ctx, admin, caseB, input, "basis-valid-historical-parse")
	if err != nil || replayed || proposal.RequestID == "" {
		t.Fatalf("pinned historical parse could not support a pre-review correction: proposal=%+v replayed=%v err=%v", proposal, replayed, err)
	}
	var refs []materialAnchorProjection
	detail, err := core.GetSubjectChange(ctx, admin, caseB, proposal.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(detail.BasisRefs, &refs); err != nil || len(refs) != 1 || refs[0].ParsedDocumentID != parsedB1.ID || refs[0].Filename != "source-b.txt" || refs[0].Quote != evidenceText {
		t.Fatalf("proposal did not freeze the exact historical parse and source quote: refs=%+v err=%v", refs, err)
	}
	if _, err := core.Workspace(admin, caseB, "no-review-run"); err == nil {
		t.Fatal("proposal evidence flow unexpectedly created a review run")
	}
}
