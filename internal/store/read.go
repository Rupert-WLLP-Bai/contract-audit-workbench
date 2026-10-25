package store

import (
	"bytes"
	"context"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

// documentWriteCase resolves the caller and writable case before document work
// enters a parser capacity gate. Mutations still authorize again when committing.
func (s *Store) documentWriteCase(ctx context.Context, user accounts.User, caseID string, action access.Action) (accounts.User, Case, error) {
	if ctx == nil {
		return accounts.User{}, Case{}, fault.Invalid("文档操作上下文无效")
	}
	if err := ctx.Err(); err != nil {
		return accounts.User{}, Case{}, err
	}
	if s.ReadOnly() {
		return accounts.User{}, Case{}, fault.New(503, "read_only", "只读模式下不能执行文档操作")
	}
	currentUser, err := trustedUser(user)
	if err != nil {
		return accounts.User{}, Case{}, err
	}
	if !accounts.CanUpload(currentUser.Role) {
		return accounts.User{}, Case{}, fault.Forbidden("当前角色不能执行文档操作")
	}
	seen, err := s.snapshotScoped(ctx, userScope(currentUser))
	if err != nil {
		return accounts.User{}, Case{}, err
	}
	item, err := s.writableCase(&seen, currentUser, caseID, action)
	if err != nil {
		return accounts.User{}, Case{}, err
	}
	return currentUser, *item, nil
}

// DocumentUploadKind checks current upload and case-management authority before
// selecting capacity for the upload's initial parse. Images stay pending until
// an explicit reparse or delegated review preparation.
func (s *Store) DocumentUploadKind(ctx context.Context, user accounts.User, caseID, filename, media string) (string, error) {
	if _, _, err := s.documentWriteCase(ctx, user, caseID, access.Manage); err != nil {
		return "", err
	}
	kind := DocumentParseKind(filename, media)
	if kind == "ocr" {
		return "none", nil
	}
	return kind, nil
}

// GenerationParseKind checks current review authority and the supported
// business scope before selecting CPU parser capacity for the generated draft.
func (s *Store) GenerationParseKind(ctx context.Context, user accounts.User, caseID string) (string, error) {
	_, item, err := s.documentWriteCase(ctx, user, caseID, access.Review)
	if err != nil {
		return "", err
	}
	if !isTelecomDedicatedLineCase(item) && !isCloudServicesCase(item) {
		return "", fault.Invalid("合同模板与案件业务场景不匹配")
	}
	return "cpu", nil
}

// DocumentParseKindContext resolves parser capacity only after checking the
// same upload authority and case-management access required by reparse.
func (s *Store) DocumentParseKindContext(ctx context.Context, user accounts.User, caseID, docID string) (string, error) {
	if ctx == nil {
		return "", fault.Invalid("解析类型上下文无效")
	}
	if s.ReadOnly() {
		return "", fault.New(503, "read_only", "只读模式下不能重新解析材料")
	}
	currentUser, err := trustedUser(user)
	if err != nil {
		return "", err
	}
	if !accounts.CanUpload(currentUser.Role) {
		return "", fault.Forbidden("当前角色不能重新解析")
	}
	seen, err := s.snapshotScoped(ctx, userScope(currentUser))
	if err != nil {
		return "", err
	}
	if _, err := s.writableCase(&seen, currentUser, caseID, access.Manage); err != nil {
		return "", err
	}
	for _, doc := range seen.documents(caseID) {
		if doc.ID == docID {
			return DocumentParseKind(doc.Filename, doc.MediaType), nil
		}
	}
	return "", fault.NotFound("没有找到对应记录")
}

func textual(filename, media string, body []byte) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".pdf", ".docx", ".doc", ".xlsx", ".xls", ".png", ".jpg", ".jpeg", ".gif", ".zip", ".bin":
		return false
	}
	if !utf8.Valid(body) || bytes.Contains(body, []byte{0}) {
		return false
	}
	mt := strings.ToLower(strings.TrimSpace(media))
	if i := strings.Index(mt, ";"); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	switch {
	case strings.HasPrefix(mt, "text/"), ext == ".txt", ext == ".md", mt == "", mt == "application/octet-stream":
		return true
	default:
		return false
	}
}

func (s *Store) interpret(doc Document) Interpreted {
	return s.interpretStored(doc)
}

func (st *state) caseByID(id string) (Case, bool) {
	for _, item := range st.Cases {
		if item.ID == id {
			return item, true
		}
	}
	return Case{}, false
}

func (st *state) documents(caseID string) []Document {
	var out []Document
	for _, item := range st.Documents {
		if item.CaseID == caseID {
			out = append(out, item)
		}
	}
	return out
}

// currentDocuments keeps the latest stored version of each role.
// An older version stays in the case for the run that snapshotted it.
func currentDocuments(docs []Document) []Document {
	latest := map[string]int{}
	for i, doc := range docs {
		latest[doc.Role] = i
	}
	indexes := make([]int, 0, len(latest))
	for _, idx := range latest {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	out := make([]Document, 0, len(indexes))
	for _, idx := range indexes {
		out = append(out, docs[idx])
	}
	return out
}

func sameDocumentSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]string(nil), a...)
	right := append([]string(nil), b...)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (st *state) documentsByIDs(caseID string, ids []string) []Document {
	byID := map[string]Document{}
	for _, doc := range st.documents(caseID) {
		byID[doc.ID] = doc
	}
	out := make([]Document, 0, len(ids))
	for _, id := range ids {
		if doc, ok := byID[id]; ok {
			out = append(out, doc)
		}
	}
	return out
}

func (st *state) runDocuments(run Run) []Document {
	ids := st.snapshotOf(run.ID).DocumentIDs
	if len(ids) == 0 {
		return st.documents(run.CaseID)
	}
	return st.documentsByIDs(run.CaseID, ids)
}

func (st *state) previousRun(run Run) (Run, bool) {
	idx := -1
	for i, item := range st.Runs {
		if item.ID == run.ID && item.CaseID == run.CaseID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Run{}, false
	}
	var found Run
	ok := false
	for i := 0; i < idx; i++ {
		item := st.Runs[i]
		if item.CaseID != run.CaseID {
			continue
		}
		if !ok || item.CreatedAt >= found.CreatedAt {
			found = item
			ok = true
		}
	}
	return found, ok
}

func (st *state) latestRun(caseID string) (Run, bool) {
	var found Run
	ok := false
	for _, item := range st.Runs {
		if item.CaseID != caseID {
			continue
		}
		if !ok || item.CreatedAt >= found.CreatedAt {
			found = item
			ok = true
		}
	}
	return found, ok
}

func (st *state) evaluations(runID string) []ConstraintEval {
	var out []ConstraintEval
	for _, item := range st.Evaluations {
		if item.RunID == runID {
			out = append(out, item)
		}
	}
	if out == nil {
		out = []ConstraintEval{}
	}
	return out
}

func (st *state) findings(runID string) []Finding {
	var out []Finding
	for _, item := range st.Findings {
		if item.RunID == runID {
			out = append(out, item)
		}
	}
	return out
}

func (st *state) decisions(findingID string) []Decision {
	var out []Decision
	for _, item := range st.Decisions {
		if item.FindingID == findingID {
			out = append(out, item)
		}
	}
	return out
}

func (st *state) runEvents(runID string) []Event {
	var out []Event
	for _, item := range st.Events {
		if item.RunID == runID {
			out = append(out, item)
		}
	}
	return out
}

func (s *Store) interpretAll(docs []Document) []Interpreted {
	out := make([]Interpreted, len(docs))
	for i, doc := range docs {
		out[i] = s.interpret(doc)
	}
	return out
}

// RunView is the state shown to clients, derived from documents and decisions.
type RunView struct {
	Run            Run
	State          string
	PartialReason  string
	FailureMessage string
	Counts         Counts
}

func (s *Store) viewRun(st *state, run Run, selectedAttemptID ...string) RunView {
	attemptID := ""
	if len(selectedAttemptID) > 0 {
		attemptID = selectedAttemptID[0]
	}
	var attempt *Attempt
	if attemptID == "" {
		if latest, ok := st.latestAttempt(run.ID); ok {
			attemptID = latest.ID
		}
	}
	if attemptID != "" {
		if found, ok := st.attemptByID(attemptID); ok && found.RunID == run.ID {
			attempt = &found
		}
	}
	docs := st.documentsForAttempt(run, attemptID)
	findings := st.findings(run.ID)
	if attemptID != "" {
		findings = st.findingsForAttempt(run.ID, attemptID)
	}
	failed := false
	for _, doc := range docs {
		if doc.ParseError != "" {
			failed = true
		}
	}
	counts := Counts{Total: len(findings)}
	for _, finding := range findings {
		switch finding.Relation {
		case "conflict":
			counts.Conflict++
		case "insufficient":
			counts.Insufficient++
		}
		if len(st.decisions(finding.ID)) == 0 {
			counts.Undecided++
		}
	}
	stateName := run.State
	if attempt != nil {
		stateName = attempt.State
	}
	displayRun := run
	displayRun.State = stateName
	view := RunView{Run: displayRun, Counts: counts}
	switch stateName {
	case "accepted", "running", "cancelled", "interrupted":
		view.State = stateName
		return view
	case "failed":
		view.State = "failed"
		view.FailureMessage = run.FailureMessage
		if attempt != nil {
			view.FailureMessage = "这次尝试未能完成，请查看对应执行事件。"
		}
		if view.FailureMessage == "" {
			view.FailureMessage = "这次运行失败。"
		}
		return view
	case "partial":
		view.State = "partial"
		view.PartialReason = run.PartialReason
		if attempt != nil {
			code := ""
			if attempt.ErrorCode != nil {
				code = *attempt.ErrorCode
			}
			view.PartialReason = publicAttemptFailure(code)
			if failed && code != "document_parse_incomplete" {
				view.PartialReason += " 有材料解析失败，结果不能当成完整审查。"
			}
		}
		if view.PartialReason == "" {
			view.PartialReason = "审查未完整完成；可定位的规则结果仍然保留。"
		}
		return view
	}
	if failed {
		view.State = "partial"
		view.PartialReason = "有材料解析失败，结果不能当成完整审查。"
		return view
	}
	if counts.Undecided > 0 {
		view.State = "awaiting_human"
		return view
	}
	view.State = "completed"
	return view
}
