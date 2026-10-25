package compare_test

import (
	"embed"
	"strings"
	"testing"
	"unicode"

	"contractaudit/internal/docir"
	"contractaudit/internal/docversion"
	"contractaudit/internal/engine"
)

//go:embed testdata/d0
var d0 embed.FS

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := d0.ReadFile("testdata/d0/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ready(id, role, text string) engine.Source {
	return engine.Source{ID: id, Role: role, ParseStatus: "ready", Text: text}
}

// piAgentIdentity is the document hash in pi-agent-dev
// packages/audit/src/plaintext-adapter.ts and document-ir.ts: split on a blank
// line, drop a block that trims to empty, strip trailing whitespace, join with
// a blank line, and SHA-256 that canonical text. The original file bytes are
// not the identity.
func piAgentIdentity(raw string) string {
	var blocks []string
	for _, part := range strings.Split(raw, "\n\n") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		blocks = append(blocks, strings.TrimRightFunc(part, unicode.IsSpace))
	}
	return docversion.Hash([]byte(strings.Join(blocks, "\n\n"))).String()
}

// piAgentPaintsBlock is audit-case-workbench.tsx: a DOCUMENT_SPAN block id is
// added to problemBlockIds and the block is painted. quotedText is not checked
// against the block text.
func piAgentPaintsBlock(blockID string) bool {
	return blockID != ""
}

// naiveBox hashes normalized text and always emits a rectangle. These fixtures
// have no measured page image, so the rectangle is not a text coordinate.
func naiveBox(normalized string) (string, [4]int) {
	return docversion.Hash([]byte(normalized)).String(), [4]int{0, 0, 100, 20}
}

// naiveRelation treats a missing side as agreement. One observed month, or
// equal months, is "compatible".
func naiveRelation(values []int) string {
	if len(values) < 2 {
		return "compatible"
	}
	for _, value := range values[1:] {
		if value != values[0] {
			return "conflict"
		}
	}
	return "compatible"
}

func calledNoConflict(relation, claim string) bool {
	if relation == "compatible" || relation == "no_conflict" || relation == "COMPLIANT" {
		return true
	}
	for _, phrase := range []string{"没有冲突", "无冲突", "不冲突", "no conflict"} {
		if strings.Contains(claim, phrase) {
			return true
		}
	}
	return false
}

type scored struct {
	blockID   string
	blockText string
	quote     string
	text      string
	raw       string
	start     int
	end       int
	status    string
	page      *int
}

func (s scored) oursFalse() bool {
	if s.status != docir.StatusLocated {
		return s.page != nil || s.start != 0 || s.end != 0
	}
	got, ok := docir.Slice(s.text, s.start, s.end)
	if !ok || got != s.quote {
		return true
	}
	if s.page != nil {
		source := s.raw
		if source == "" {
			source = s.text
		}
		page, ok := docir.Build(source).PageAt(s.start, s.end)
		if !ok || page != *s.page {
			return true
		}
	}
	return false
}

func blockOf(ir docir.IR, start, end int) (string, string) {
	for _, block := range ir.Blocks {
		if start >= block.Start && end <= block.End {
			return block.ID, block.Text
		}
	}
	if len(ir.Blocks) == 0 {
		return "", ""
	}
	return ir.Blocks[0].ID, ir.Blocks[0].Text
}

func scoreLocated(text, quote string, page *int, start, end int) scored {
	ir := docir.Build(text)
	id, block := blockOf(ir, start, end)
	return scored{
		blockID: id, blockText: block, quote: quote, text: ir.NormalizedText, raw: text,
		start: start, end: end, status: docir.StatusLocated, page: page,
	}
}

func TestScenarioComparison(t *testing.T) {
	raw := readFixture(t, "trailing-spaces.txt")
	trimmed := strings.TrimRightFunc(raw, unicode.IsSpace)
	if raw == trimmed || !strings.HasSuffix(raw, "   ") {
		t.Fatalf("trailing fixture was not preserved: %q", raw)
	}
	origRaw := docversion.Hash([]byte(raw)).String()
	origTrim := docversion.Hash([]byte(trimmed)).String()
	normRaw := docir.Build(raw).NormalizedDigest
	normTrim := docir.Build(trimmed).NormalizedDigest
	piRaw := piAgentIdentity(raw)
	piTrim := piAgentIdentity(trimmed)
	originalChanged := boolNum(origRaw != origTrim)
	normalizedChanged := boolNum(normRaw != normTrim)
	piIdentityChanged := boolNum(piRaw != piTrim)

	compatibleDocs := []engine.Source{
		ready("proc", "procurement", readFixture(t, "compatible-procurement.txt")),
		ready("award", "award", readFixture(t, "compatible-award.txt")),
	}
	compatibleEval := engine.Evaluate(compatibleDocs)
	_, compatibleFindings, err := engine.AcceptWarranty(compatibleDocs, engine.Proposal{
		Claim: "质保冲突", Relation: "conflict", Disposition: "confirm_risk",
		Quotes: []engine.QuoteRef{{DocumentID: "proc", Quote: readFixture(t, "unlocatable-quote.txt")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	naiveCompatible := naiveRelation(valuesOf(compatibleEval))

	conflictDocs := []engine.Source{
		ready("proc", "procurement", readFixture(t, "conflict-procurement.txt")),
		ready("draft", "draft_contract", readFixture(t, "conflict-draft.txt")),
	}
	conflictEval, conflictFindings, err := engine.AcceptWarranty(conflictDocs, engine.Proposal{
		Claim: "没有冲突", Relation: "compatible", Disposition: "false_positive",
	})
	if err != nil {
		t.Fatal(err)
	}
	naiveConflict := naiveRelation(valuesOf(conflictEval))
	conflictVerified := 0
	conflictLocated := 0
	for _, anchor := range conflictAnchors(conflictFindings) {
		if anchor.Status != "located" {
			continue
		}
		conflictLocated++
		if !anchorFalse(anchor, conflictDocs) {
			conflictVerified++
		}
	}

	insufficientDocs := []engine.Source{
		ready("proc", "procurement", readFixture(t, "insufficient-procurement.txt")),
		ready("draft", "draft_contract", readFixture(t, "insufficient-draft.txt")),
	}
	insufficientEval, insufficientFindings, err := engine.AcceptWarranty(insufficientDocs, engine.Proposal{
		Claim: "没有冲突", Relation: "compatible", Disposition: "false_positive",
	})
	if err != nil {
		t.Fatal(err)
	}
	naiveInsufficient := naiveRelation(valuesOf(insufficientEval))
	insufficientCalled := 0
	if len(insufficientFindings) == 1 && calledNoConflict(insufficientFindings[0].Relation, insufficientFindings[0].Claim) {
		insufficientCalled = 1
	}
	naiveCalled := boolNum(calledNoConflict(naiveInsufficient, ""))

	var samples []scored
	samples = append(samples, anchorsToSamples(compatibleEval, compatibleDocs)...)
	samples = append(samples, findingSamples(conflictFindings, conflictDocs)...)
	samples = append(samples, findingSamples(insufficientFindings, insufficientDocs)...)

	body := readFixture(t, "unlocatable-body.txt")
	bodyIR := docir.Build(body)
	badQuote := strings.TrimSpace(readFixture(t, "unlocatable-quote.txt"))
	real := docir.Locate(bodyIR, "质保36个月")
	if real.Status != docir.StatusLocated {
		t.Fatalf("fixture quote missing: %+v", real)
	}
	miss := docir.Bind(bodyIR, badQuote, real.Start, real.End)
	id, block := blockOf(bodyIR, real.Start, real.End)
	samples = append(samples, scored{
		blockID: id, blockText: block, quote: badQuote, text: bodyIR.NormalizedText,
		status: miss.Status, page: miss.Page, start: miss.Start, end: miss.End,
	})

	paged := readFixture(t, "paged-procurement.txt")
	pagedIR := docir.Build(paged)
	pagedHit := docir.Locate(pagedIR, "质保期为36个月")
	samples = append(samples, scoreLocated(paged, pagedHit.Quote, pagedHit.Page, pagedHit.Start, pagedHit.End))

	tableText := readFixture(t, "table-warranty.txt")
	tableIR := docir.Build(tableText)
	var cell docir.Cell
	for _, item := range tableIR.Cells {
		if item.Text == "36个月" {
			cell = item
		}
	}
	if cell.Text != "36个月" {
		t.Fatalf("table cells %+v", tableIR.Cells)
	}
	samples = append(samples, scoreLocated(tableText, cell.Text, cell.Page, cell.Start, cell.End))

	oursFalse := 0
	naiveFalse := 0
	piFalse := 0
	oursBBox := 0
	naiveBBox := 0
	for _, sample := range samples {
		if sample.oursFalse() {
			oursFalse++
		}
		digest, box := naiveBox(sample.text)
		if digest == "" || box == [4]int{} {
			t.Fatal("naive box was not emitted")
		}
		naiveBBox++
		naiveFalse++
		if piAgentPaintsBlock(sample.blockID) && sample.blockText != sample.quote {
			piFalse++
		}
	}

	t.Logf("HASH original_trailing=%s", origRaw)
	t.Logf("HASH original_trimmed=%s", origTrim)
	t.Logf("HASH normalized_trailing=%s", normRaw)
	t.Logf("HASH normalized_trimmed=%s", normTrim)
	t.Logf("HASH pi_agent_trailing=%s", piRaw)
	t.Logf("HASH pi_agent_trimmed=%s", piTrim)
	t.Logf("METRIC original_digest_changed ours=%d pi_agent=%d", originalChanged, piIdentityChanged)
	t.Logf("METRIC normalized_digest_changed ours=%d pi_agent=%d", normalizedChanged, piIdentityChanged)
	t.Logf("METRIC false_anchors ours=%d naive=%d pi_agent=%d samples=%d", oursFalse, naiveFalse, piFalse, len(samples))
	t.Logf("METRIC bbox_emitted ours=%d naive=%d", oursBBox, naiveBBox)
	t.Logf("METRIC compatible_findings ours=%d naive=%d relation=%s", len(compatibleFindings), boolNum(naiveCompatible == "conflict"), compatibleEval.Relation)
	t.Logf("METRIC conflict_findings ours=%d naive=%d verified=%d/%d", len(conflictFindings), boolNum(naiveConflict == "conflict"), conflictVerified, conflictLocated)
	t.Logf("METRIC insufficient_findings ours=%d relation=%s called_no_conflict ours=%d naive=%d", len(insufficientFindings), insufficientEval.Relation, insufficientCalled, naiveCalled)
	t.Logf("METRIC paged_locate status=%s page=%v", pagedHit.Status, pageNum(pagedHit.Page))
	t.Logf("METRIC table_cell element=%s", tableIR.ElementAt(cell.Start, cell.End))

	if originalChanged != 1 || normalizedChanged != 0 || piIdentityChanged != 0 {
		t.Fatal("hash separation did not match the fixture")
	}
	if oursFalse != 0 || naiveFalse <= 0 || piFalse <= 0 {
		t.Fatal("false-anchor counts did not match the fixture")
	}
	if compatibleEval.Relation != engine.RelCompatible || len(compatibleFindings) != 0 || naiveCompatible != "compatible" {
		t.Fatal("compatible pair")
	}
	if len(conflictFindings) != 1 || conflictFindings[0].Relation != engine.RelConflict || conflictVerified != conflictLocated || conflictLocated == 0 {
		t.Fatalf("conflict %+v verified %d/%d", conflictFindings, conflictVerified, conflictLocated)
	}
	if calledNoConflict(conflictFindings[0].Relation, conflictFindings[0].Claim) {
		t.Fatal("conflict claim was labeled no conflict")
	}
	if len(insufficientFindings) != 1 || insufficientEval.Relation != engine.RelInsufficient || insufficientCalled != 0 || naiveCalled != 1 {
		t.Fatalf("insufficient eval=%+v findings=%+v", insufficientEval, insufficientFindings)
	}
	if pagedHit.Status != docir.StatusLocated || pagedHit.Page == nil || *pagedHit.Page != 2 {
		t.Fatalf("paged %+v", pagedHit)
	}
	if miss.Status != docir.StatusUnlocatable || miss.Page != nil || miss.Start != 0 || miss.End != 0 {
		t.Fatalf("unlocatable %+v", miss)
	}
}

func boolNum(ok bool) int {
	if ok {
		return 1
	}
	return 0
}

func pageNum(page *int) int {
	if page == nil {
		return 0
	}
	return *page
}

func valuesOf(ev engine.Evaluation) []int {
	out := make([]int, len(ev.Facts))
	for i, fact := range ev.Facts {
		out[i] = fact.Value
	}
	return out
}

func conflictAnchors(findings []engine.Draft) []engine.Anchor {
	if len(findings) == 0 {
		return nil
	}
	return findings[0].Anchors
}

func anchorFalse(anchor engine.Anchor, docs []engine.Source) bool {
	sample := anchorSample(anchor, docs)
	return sample.oursFalse()
}

func anchorsToSamples(ev engine.Evaluation, docs []engine.Source) []scored {
	var out []scored
	for _, fact := range ev.Facts {
		out = append(out, anchorSample(fact.Anchor, docs))
	}
	return out
}

func findingSamples(findings []engine.Draft, docs []engine.Source) []scored {
	var out []scored
	for _, finding := range findings {
		for _, anchor := range finding.Anchors {
			out = append(out, anchorSample(anchor, docs))
		}
	}
	return out
}

func anchorSample(anchor engine.Anchor, docs []engine.Source) scored {
	var text string
	for _, doc := range docs {
		if doc.ID == anchor.DocumentID {
			text = doc.Text
		}
	}
	ir := docir.Build(text)
	if anchor.Status != "located" {
		id, block := "", ""
		if len(ir.Blocks) > 0 {
			id, block = ir.Blocks[0].ID, ir.Blocks[0].Text
		}
		return scored{
			blockID: id, blockText: block, quote: anchor.Quote, text: ir.NormalizedText,
			status: anchor.Status, page: anchor.Page, start: anchor.Start, end: anchor.End,
		}
	}
	id, block := blockOf(ir, anchor.Start, anchor.End)
	return scored{
		blockID: id, blockText: block, quote: anchor.Quote, text: ir.NormalizedText,
		start: anchor.Start, end: anchor.End, status: anchor.Status, page: anchor.Page,
	}
}
