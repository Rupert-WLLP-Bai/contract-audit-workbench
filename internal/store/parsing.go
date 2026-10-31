package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/docir"
	"contractaudit/internal/documentparse"
	"contractaudit/internal/docversion"
)

const parsedPayloadVersion = "parsed-document/v1"
const parseFailureCleanupTimeout = 2 * time.Second

func parseFailurePersistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.Background(), parseFailureCleanupTimeout)
}

type ParsedDocument struct {
	ID                   string   `json:"id"`
	SelectionOrder       int64    `json:"selection_order"`
	DocumentID           string   `json:"document_id"`
	OriginalDigest       string   `json:"original_digest"`
	ParserID             string   `json:"parser_id"`
	ParserVersion        string   `json:"parser_version"`
	ConfigHash           string   `json:"config_hash"`
	NormalizationVersion string   `json:"normalization_version"`
	NormalizedDigest     string   `json:"normalized_digest,omitempty"`
	IRDigest             string   `json:"ir_digest,omitempty"`
	ArtifactDigest       string   `json:"artifact_digest"`
	CreatedAt            string   `json:"created_at"`
	Status               string   `json:"status"`
	Text                 string   `json:"text"`
	IR                   docir.IR `json:"ir"`
	ParseError           string   `json:"parse_error"`
	FromOCR              bool     `json:"from_ocr"`
}

type parsedPayload struct {
	Version              string   `json:"version"`
	ID                   string   `json:"id"`
	SelectionOrder       int64    `json:"selection_order"`
	DocumentID           string   `json:"document_id"`
	OriginalDigest       string   `json:"original_digest"`
	ParserID             string   `json:"parser_id"`
	ParserVersion        string   `json:"parser_version"`
	ConfigHash           string   `json:"config_hash"`
	NormalizationVersion string   `json:"normalization_version"`
	NormalizedDigest     *string  `json:"normalized_digest"`
	IRDigest             *string  `json:"ir_digest"`
	CreatedAt            string   `json:"created_at"`
	Status               string   `json:"status"`
	Text                 string   `json:"text"`
	IR                   docir.IR `json:"ir"`
	ParseError           string   `json:"parse_error"`
	FromOCR              bool     `json:"from_ocr"`
}

func (s *Store) parserConfiguration(doc Document) (string, string, string) {
	return parserConfigurationFor(doc, s.currentModels())
}

// DocumentParseKind returns the parser capacity class derived from the
// filename and server-recorded media type used by the parser.
func DocumentParseKind(filename, media string) string {
	switch fileKind(filename, media) {
	case kindImage, kindPDF, kindDOCX:
		return "ocr"
	}
	return "cpu"
}

func parserConfigurationFor(doc Document, models Models) (parserID, parserVersion, configHash string) {
	parserID = "document-inspection"
	parserVersion = docir.ExtractionVersion
	configuration := []string{"parsed-configuration/v1", parserID, parserVersion, docir.NormalizationVersion}
	kind := fileKind(doc.Filename, doc.MediaType)
	if kind == kindImage || kind == kindPDF || kind == kindDOCX {
		parserID, parserVersion = "ocr", "unconfigured"
		modelConfig := "ocr-disabled/v1"
		if models != nil {
			if identity := frozenConfigurationID(models); identity != "" {
				modelConfig = identity
			}
			if named, ok := models.(interface{ OCRName() string }); ok {
				parserVersion = named.OCRName()
			} else {
				parserVersion = models.Name()
			}
		}
		switch kind {
		case kindPDF:
			parserID, parserVersion = "pdf", documentparse.PDFVersion
		case kindDOCX:
			parserID, parserVersion = "docx", documentparse.DOCXVersion
		}
		configHash = ocrConfigurationHash(parserID, parserVersion, docir.NormalizationVersion, modelConfig)
		return parserID, parserVersion, configHash
	} else if kind == kindBinary {
		parserVersion = "document-inspection-v1"
		configuration = []string{"parsed-configuration/v1", parserID, parserVersion, docir.ExtractionVersion, docir.NormalizationVersion}
	}
	hash, _ := canonicalDigest(configuration)
	return parserID, parserVersion, hash
}

func ocrConfigurationHash(parserID, parserVersion, normalizationVersion, modelIdentity string) string {
	hash, _ := canonicalDigest([]string{"parsed-configuration/v1", parserID, parserVersion, docir.ExtractionVersion, normalizationVersion, modelIdentity})
	return hash
}

func (s *Store) ensureParsed(ctx context.Context, user accounts.User, doc Document, force bool) (ParsedDocument, error) {
	return s.ensureParsedWithModels(ctx, user, doc, force, s.currentModels())
}

func (s *Store) ensureParsedWithModels(ctx context.Context, user accounts.User, doc Document, force bool, models Models) (ParsedDocument, error) {
	if err := s.acquireParseLock(ctx); err != nil {
		return ParsedDocument{}, err
	}
	parseLocked := true
	defer func() {
		if parseLocked {
			s.releaseParseLock()
		}
	}()
	_, _, configHash := parserConfigurationFor(doc, models)
	if !force {
		st, err := s.snapshotScoped(ctx, userScope(user))
		if err != nil {
			return ParsedDocument{}, err
		}
		if parsed, ok := st.latestParsed(doc.ID, configHash); ok {
			s.releaseParseLock()
			parseLocked = false
			if err := s.verifyParsedRecordContext(ctx, parsed, doc); err != nil {
				return ParsedDocument{}, err
			}
			return parsed, nil
		}
	}
	selection, err := s.newParseSelection(ctx, doc, models)
	s.releaseParseLock()
	parseLocked = false
	if err != nil {
		return ParsedDocument{}, err
	}

	interpreted := s.interpretUnpersistedWithModels(ctx, doc, models)
	if contextErr := ctx.Err(); contextErr != nil {
		interpreted = Interpreted{Document: doc, ParseError: contextErr.Error(), FromOCR: interpreted.FromOCR}
	}
	parsed, err := makeParsedWithSelection(doc, interpreted, selection)
	if err != nil {
		return ParsedDocument{}, err
	}
	persistCtx, persistCancel := parseFailurePersistenceContext(ctx)
	defer persistCancel()
	err = s.scopedCaseMutation(persistCtx, user, doc.CaseID, func(st *state) error {
		if _, err := s.writableCase(st, user, doc.CaseID, access.Manage); err != nil {
			return err
		}
		if !force {
			if winner, ok := st.latestParsed(doc.ID, configHash); ok && winner.SelectionOrder >= selection.SelectionOrder {
				if winner.SelectionOrder == selection.SelectionOrder && winner.ID != selection.ParsedDocumentID {
					return errors.New("parse selection generation has conflicting artifact identities")
				}
				parsed = winner
				return nil
			}
		}
		st.ParsedDocuments = append(st.ParsedDocuments, parsed)
		return nil
	})
	if err != nil {
		return ParsedDocument{}, err
	}
	if err := s.verifyParsedRecordContext(persistCtx, parsed, doc); err != nil {
		return ParsedDocument{}, err
	}
	return parsed, nil
}

func (s *Store) reserveSelectionOrder(ctx context.Context) (int64, error) {
	var order int64
	err := s.pool.QueryRow(ctx, `SELECT nextval('parsed_selection_order_seq')`).Scan(&order)
	if err != nil && ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return order, err
}

func (s *Store) newParseSelection(ctx context.Context, doc Document, models Models) (AttemptParseSelection, error) {
	id, err := newID("parsed_")
	if err != nil {
		return AttemptParseSelection{}, err
	}
	order, err := s.reserveSelectionOrder(ctx)
	if err != nil {
		return AttemptParseSelection{}, err
	}
	parserID, parserVersion, configHash := parserConfigurationFor(doc, models)
	return AttemptParseSelection{
		DocumentID: doc.ID, OriginalDigest: doc.Digest, ParsedDocumentID: id, SelectionOrder: order,
		ParserID: parserID, ParserVersion: parserVersion, ConfigHash: configHash,
		NormalizationVersion: docir.NormalizationVersion,
	}, nil
}

func (s *Store) reserveParseSelection(ctx context.Context, doc Document, models Models) (AttemptParseSelection, error) {
	if err := s.acquireParseLock(ctx); err != nil {
		return AttemptParseSelection{}, err
	}
	defer s.releaseParseLock()
	return s.newParseSelection(ctx, doc, models)
}

func makeParsedWithSelection(doc Document, interpreted Interpreted, selection AttemptParseSelection) (ParsedDocument, error) {
	if selection.DocumentID != doc.ID || selection.OriginalDigest != doc.Digest || selection.ParsedDocumentID == "" || selection.SelectionOrder <= 0 {
		return ParsedDocument{}, errors.New("parse selection identity is invalid")
	}
	status := "failed"
	text, ir := "", docir.IR{}
	var normalizedDigest, irDigest *string
	if interpreted.Ready && len(interpreted.IR.Blocks) > 0 {
		status = "ready"
		text, ir = interpreted.Text, interpreted.IR
		nd := ir.NormalizedDigest
		normalizedDigest = &nd
		irHash, err := canonicalDigest(ir)
		if err != nil {
			return ParsedDocument{}, err
		}
		irDigest = &irHash
	}
	parsed := ParsedDocument{
		ID: selection.ParsedDocumentID, SelectionOrder: selection.SelectionOrder, DocumentID: doc.ID, OriginalDigest: doc.Digest,
		ParserID: selection.ParserID, ParserVersion: selection.ParserVersion, ConfigHash: selection.ConfigHash,
		NormalizationVersion: selection.NormalizationVersion, CreatedAt: stamp(), Status: status, Text: text, IR: ir,
		FromOCR: interpreted.FromOCR,
	}
	if normalizedDigest != nil {
		parsed.NormalizedDigest = *normalizedDigest
	}
	if irDigest != nil {
		parsed.IRDigest = *irDigest
	}
	if status == "failed" {
		parsed.ParseError = interpreted.ParseError
		if parsed.ParseError == "" {
			parsed.ParseError = "文件不能解析成可定位正文。"
		}
	}
	payload, err := parsed.canonicalPayload()
	if err != nil {
		return ParsedDocument{}, err
	}
	parsed.ArtifactDigest = digestBytes(payload)
	return parsed, nil
}

func (p ParsedDocument) canonicalPayload() ([]byte, error) {
	var normalized, irDigest *string
	if p.Status == "ready" {
		normalized, irDigest = &p.NormalizedDigest, &p.IRDigest
	}
	return json.Marshal(parsedPayload{
		Version: parsedPayloadVersion, ID: p.ID, SelectionOrder: p.SelectionOrder, DocumentID: p.DocumentID, OriginalDigest: p.OriginalDigest,
		ParserID: p.ParserID, ParserVersion: p.ParserVersion, ConfigHash: p.ConfigHash,
		NormalizationVersion: p.NormalizationVersion, NormalizedDigest: normalized, IRDigest: irDigest,
		CreatedAt: p.CreatedAt, Status: p.Status, Text: p.Text, IR: p.IR,
		ParseError: p.ParseError, FromOCR: p.FromOCR,
	})
}

func (p ParsedDocument) canonicalJSON() (string, error) {
	payload, err := p.canonicalPayload()
	return string(payload), err
}

func verifyParsed(p ParsedDocument, doc Document) error {
	if p.ID == "" || p.DocumentID != doc.ID || p.OriginalDigest != doc.Digest || p.SelectionOrder <= 0 {
		return errors.New("parsed artifact original identity mismatch")
	}
	if _, err := docversion.ParseDigest(p.OriginalDigest); err != nil {
		return errors.New("parsed artifact original digest is invalid")
	}
	if _, err := docversion.ParseDigest(p.ConfigHash); err != nil || p.ParserID == "" || p.ParserVersion == "" || p.CreatedAt == "" {
		return errors.New("parsed artifact parser identity is invalid")
	}
	if p.Status != "ready" && p.Status != "failed" {
		return errors.New("parsed artifact status is invalid")
	}
	if !supportedNormalizationVersion(p.NormalizationVersion) {
		return errors.New("parsed artifact normalization version is unsupported")
	}
	if p.Status == "ready" {
		if p.Text == "" || len(p.IR.Blocks) == 0 || p.IR.NormalizedText != p.Text || p.IR.NormalizedDigest != p.NormalizedDigest || p.NormalizedDigest == "" || p.IRDigest == "" || p.ParseError != "" {
			return errors.New("parsed artifact ready payload is inconsistent")
		}
		if _, err := docversion.ParseDigest(p.NormalizedDigest); err != nil {
			return errors.New("parsed artifact normalized digest is invalid")
		}
		if _, err := docversion.ParseDigest(p.IRDigest); err != nil {
			return errors.New("parsed artifact IR digest is invalid")
		}
		if docversion.Hash([]byte(p.Text)).String() != p.NormalizedDigest {
			return errors.New("parsed artifact normalized digest mismatch")
		}
		nd, err := canonicalDigest(p.IR)
		if err != nil || nd != p.IRDigest {
			return errors.New("parsed artifact IR digest mismatch")
		}
	} else if p.Text != "" || p.NormalizedDigest != "" || p.IRDigest != "" || p.IR.NormalizedText != "" || p.IR.NormalizedDigest != "" || p.IR.CoordinateSpace != "" || p.IR.ExtractionVersion != "" || len(p.IR.Pages) != 0 || len(p.IR.Blocks) != 0 || p.ParseError == "" {
		return errors.New("failed parsed artifact contains fabricated text")
	}
	payload, err := p.canonicalPayload()
	if err != nil {
		return err
	}
	if digestBytes(payload) != p.ArtifactDigest {
		return errors.New("parsed artifact digest mismatch")
	}
	return nil
}

func supportedNormalizationVersion(version string) bool {
	switch version {
	case "text-normalization-v1":
		return true
	default:
		return false
	}
}

func (s *Store) verifyParsedRecordContext(ctx context.Context, p ParsedDocument, doc Document) (result error) {
	if ctx == nil {
		return errors.New("parse verification context is nil")
	}
	defer func() {
		if result != nil && ctx.Err() != nil {
			result = ctx.Err()
		}
	}()
	if err := verifyParsed(p, doc); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	if err := setLocalScope(ctx, tx, dbScope{ActorID: "document-verifier", Role: "api", TenantID: doc.TenantID, OrgID: doc.OrgID, CaseID: doc.CaseID}); err != nil {
		return err
	}
	var id, documentID, originalDigest, parserID, parserVersion, configHash, normalizationVersion string
	var normalizedDigest, irDigest *string
	var artifactDigest, createdAt, status, payload string
	var selectionOrder int64
	err = tx.QueryRow(ctx, `SELECT id, selection_order, document_id, original_digest, parser_id, parser_version, config_hash, normalization_version, normalized_digest, ir_digest, artifact_digest, created_at, status, payload
		FROM parsed_documents WHERE id=$1 AND document_id=$2 AND tenant_id=$3 AND org_id=$4`, p.ID, doc.ID, doc.TenantID, doc.OrgID).Scan(
		&id, &selectionOrder, &documentID, &originalDigest, &parserID, &parserVersion, &configHash, &normalizationVersion,
		&normalizedDigest, &irDigest, &artifactDigest, &createdAt, &status, &payload,
	)
	if err != nil {
		return fmt.Errorf("parsed artifact record is unavailable: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	stored, err := parsePayload(payload)
	if err != nil {
		return err
	}
	wantNormalized, wantIR := "", ""
	if normalizedDigest != nil {
		wantNormalized = *normalizedDigest
	}
	if irDigest != nil {
		wantIR = *irDigest
	}
	if stored.ID != id || stored.SelectionOrder != selectionOrder || stored.SelectionOrder != p.SelectionOrder || stored.DocumentID != documentID || stored.OriginalDigest != originalDigest ||
		stored.ParserID != parserID || stored.ParserVersion != parserVersion || stored.ConfigHash != configHash ||
		stored.NormalizationVersion != normalizationVersion || stored.NormalizedDigest != wantNormalized ||
		stored.IRDigest != wantIR || stored.ArtifactDigest != artifactDigest || stored.CreatedAt != createdAt || stored.Status != status ||
		stored.ArtifactDigest != p.ArtifactDigest {
		return errors.New("parsed artifact metadata does not match its canonical payload")
	}
	return verifyParsed(stored, doc)
}

func appendOnlyEqual[T any](before, after []T) bool {
	if len(after) < len(before) {
		return false
	}
	for i := range before {
		left, errLeft := json.Marshal(before[i])
		right, errRight := json.Marshal(after[i])
		if errLeft != nil || errRight != nil || string(left) != string(right) {
			return false
		}
	}
	return true
}

func parsePayload(payload string) (ParsedDocument, error) {
	var raw parsedPayload
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return ParsedDocument{}, err
	}
	if raw.Version != parsedPayloadVersion {
		return ParsedDocument{}, fmt.Errorf("unsupported parsed artifact payload version %q", raw.Version)
	}
	p := ParsedDocument{
		ID: raw.ID, SelectionOrder: raw.SelectionOrder, DocumentID: raw.DocumentID, OriginalDigest: raw.OriginalDigest, ParserID: raw.ParserID,
		ParserVersion: raw.ParserVersion, ConfigHash: raw.ConfigHash, NormalizationVersion: raw.NormalizationVersion,
		CreatedAt: raw.CreatedAt, Status: raw.Status, Text: raw.Text, IR: raw.IR, ParseError: raw.ParseError, FromOCR: raw.FromOCR,
	}
	if raw.NormalizedDigest != nil {
		p.NormalizedDigest = *raw.NormalizedDigest
	}
	if raw.IRDigest != nil {
		p.IRDigest = *raw.IRDigest
	}
	p.ArtifactDigest = digestBytes([]byte(payload))
	canonical, err := p.canonicalJSON()
	if err != nil {
		return ParsedDocument{}, err
	}
	if canonical != payload {
		return ParsedDocument{}, errors.New("parsed artifact payload is not canonical")
	}
	return p, nil
}

func canonicalDigest(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digestBytes(payload), nil
}

func digestBytes(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func (st state) latestParsed(documentID, configHash string) (ParsedDocument, bool) {
	var latest ParsedDocument
	ok := false
	for _, parsed := range st.ParsedDocuments {
		if parsed.DocumentID != documentID || (configHash != "" && parsed.ConfigHash != configHash) {
			continue
		}
		if !ok || parsed.SelectionOrder > latest.SelectionOrder {
			latest, ok = parsed, true
		}
	}
	return latest, ok
}

func (st state) parsedByID(id string) (ParsedDocument, bool) {
	for i := len(st.ParsedDocuments) - 1; i >= 0; i-- {
		if st.ParsedDocuments[i].ID == id {
			return st.ParsedDocuments[i], true
		}
	}
	return ParsedDocument{}, false
}

func (st state) documentByIdentity(id, digest string) (Document, bool) {
	for _, doc := range st.Documents {
		if doc.ID == id && doc.Digest == digest {
			return doc, true
		}
	}
	return Document{}, false
}

func parsedInterpreted(doc Document, p ParsedDocument) Interpreted {
	return Interpreted{
		Document: doc, Ready: p.Status == "ready", FromOCR: p.FromOCR,
		ParseError: p.ParseError, Text: p.Text, IR: p.IR, ParsedDocument: &p,
	}
}

func (s *Store) interpretUnpersisted(ctx context.Context, doc Document) Interpreted {
	return s.interpretUnpersistedWithModels(ctx, doc, s.currentModels())
}

func (s *Store) interpretUnpersistedWithModels(ctx context.Context, doc Document, models Models) Interpreted {
	if err := ctx.Err(); err != nil {
		return Interpreted{Document: doc, ParseError: err.Error(), FromOCR: fileKind(doc.Filename, doc.MediaType) == kindImage}
	}
	body, err := s.getObject(ctx, doc)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return Interpreted{Document: doc, ParseError: contextErr.Error(), FromOCR: fileKind(doc.Filename, doc.MediaType) == kindImage}
		}
		return Interpreted{Document: doc, ParseError: "原文没有找到，不能展示正文。"}
	}
	switch fileKind(doc.Filename, doc.MediaType) {
	case kindImage:
		interpreted := s.interpretImageWithModels(ctx, doc, body, models)
		if contextErr := ctx.Err(); contextErr != nil {
			return Interpreted{Document: doc, ParseError: contextErr.Error(), FromOCR: true}
		}
		return interpreted
	case kindPDF, kindDOCX:
		var ocr documentparse.OCR
		if models != nil {
			ocr = func(ctx context.Context, media string, body []byte) (string, error) {
				return transcribe(ctx, models, Document{MediaType: media}, body)
			}
		}
		parse := documentparse.ParsePDF
		if fileKind(doc.Filename, doc.MediaType) == kindDOCX {
			parse = documentparse.ParseDOCX
		}
		result, err := parse(ctx, body, ocr)
		if err != nil {
			return Interpreted{Document: doc, ParseError: err.Error(), FromOCR: result.FromOCR}
		}
		if len(result.IR.Blocks) == 0 {
			return Interpreted{Document: doc, ParseError: "文件里没有可定位的正文。", FromOCR: result.FromOCR}
		}
		return Interpreted{Document: doc, Ready: true, Text: result.IR.NormalizedText, IR: result.IR, FromOCR: result.FromOCR}
	case kindBinary:
		return Interpreted{Document: doc, ParseError: binaryReason(doc.Filename)}
	}
	if !textual(doc.Filename, doc.MediaType, body) {
		return Interpreted{Document: doc, ParseError: binaryReason(doc.Filename)}
	}
	ir := docir.Build(string(body))
	if contextErr := ctx.Err(); contextErr != nil {
		return Interpreted{Document: doc, ParseError: contextErr.Error()}
	}
	if len(ir.Blocks) == 0 {
		return Interpreted{Document: doc, ParseError: "文件里没有可定位的正文。"}
	}
	return Interpreted{Document: doc, Ready: true, Text: ir.NormalizedText, IR: ir}
}

func (s *Store) interpretStored(doc Document) Interpreted {
	st, err := s.snapshotScoped(context.Background(), dbScope{ActorID: "document-reader", Role: "api", TenantID: doc.TenantID, OrgID: doc.OrgID, CaseID: doc.CaseID})
	if err != nil {
		return Interpreted{Document: doc, ParseError: "已保存解析状态暂不可读取。"}
	}
	parsed, ok := st.latestParsed(doc.ID, "")
	if !ok {
		return Interpreted{Document: doc, ParsePending: true}
	}
	if err := s.verifyParsedRecordContext(context.Background(), parsed, doc); err != nil {
		return Interpreted{Document: doc, ParseError: "已保存解析产物完整性校验失败：" + err.Error()}
	}
	return parsedInterpreted(doc, parsed)
}

func frozenFromParsed(doc Document, parsed ParsedDocument) FrozenDocument {
	var normalizedHash *string
	if parsed.NormalizedDigest != "" {
		normalizedHash = &parsed.NormalizedDigest
	}
	return FrozenDocument{
		Document: doc, Ready: parsed.Status == "ready", FromOCR: parsed.FromOCR,
		ParseError: parsed.ParseError, Text: parsed.Text, IR: parsed.IR,
		OriginalHash: parsed.OriginalDigest, NormalizedHash: normalizedHash,
		ParserRef: parsed.ParserID + ":" + parsed.ParserVersion, ParsedDocumentID: parsed.ID,
		SelectionOrder: parsed.SelectionOrder, ParsedArtifactDigest: parsed.ArtifactDigest, ConfigHash: parsed.ConfigHash,
		ParserID: parsed.ParserID, ParserVersion: parsed.ParserVersion,
		NormalizationVersion: parsed.NormalizationVersion, IRDigest: parsed.IRDigest,
		ParsedCreatedAt: parsed.CreatedAt, ParsedStatus: parsed.Status,
	}
}

func frozenFromSelection(doc Document, selection AttemptParseSelection) FrozenDocument {
	return FrozenDocument{
		Document: doc, OriginalHash: doc.Digest, ParserRef: selection.ParserID + ":" + selection.ParserVersion,
		ParsedDocumentID: selection.ParsedDocumentID, SelectionOrder: selection.SelectionOrder,
		ParsedArtifactDigest: selection.ArtifactDigest, ConfigHash: selection.ConfigHash,
		ParserID: selection.ParserID, ParserVersion: selection.ParserVersion,
		NormalizationVersion: selection.NormalizationVersion,
	}
}

func selectionHash(docs []FrozenDocument) string {
	pairs := make([][]string, len(docs))
	for i, item := range docs {
		pairs[i] = []string{
			item.Document.ID, item.ParsedDocumentID, fmt.Sprint(item.SelectionOrder),
			item.ConfigHash, item.ParserID, item.ParserVersion, item.NormalizationVersion,
		}
	}
	for i := 1; i < len(pairs); i++ {
		for j := i; j > 0 && pairs[j][0] < pairs[j-1][0]; j-- {
			pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
		}
	}
	hash, _ := canonicalDigest(pairs)
	return hash
}

func parsedSelectionCurrent(st state, docs []FrozenDocument) bool {
	for _, frozen := range docs {
		if frozen.ParsedDocumentID == "" || frozen.SelectionOrder <= 0 {
			return false
		}
		latest, ok := st.latestParsed(frozen.Document.ID, frozen.ConfigHash)
		if ok && (latest.SelectionOrder > frozen.SelectionOrder ||
			(latest.SelectionOrder == frozen.SelectionOrder && latest.ID != frozen.ParsedDocumentID)) {
			return false
		}
	}
	return true
}

func frozenConfigHash(docs []FrozenDocument) string {
	configurations := make([]string, len(docs))
	for i, item := range docs {
		configurations[i] = item.ConfigHash
	}
	slices.Sort(configurations)
	hash, _ := canonicalDigest(slices.Compact(configurations))
	return hash
}
func (s *Store) acceptanceParsePlan(ctx context.Context, st state, docs []Document, models Models) ([]FrozenDocument, []AttemptParseSelection, error) {
	previous := map[string]AttemptParseSelection{}
	mayReusePrevious := false
	if len(docs) > 0 {
		if run, ok := st.latestRun(docs[0].CaseID); ok {
			previousSnapshot := st.snapshotOf(run.ID)
			configuration := make([]FrozenDocument, len(docs))
			for i, doc := range docs {
				_, _, configHash := parserConfigurationFor(doc, models)
				configuration[i].ConfigHash = configHash
			}
			compilerRef := ""
			if item, exists := st.caseByID(docs[0].CaseID); exists {
				compilerRef = telecomCompilerRefForCase(item)
			}
			candidate := currentSignatureFor(documentIDs(docs), configuration, models, compilerRef)
			// Reuse is decided against the full run configuration, which now includes
			// the frozen governed pack and planner identity. Compare those from the
			// previous snapshot itself so an unchanged pending start stays idempotent.
			candidate = withFrozenGovernance(candidate, previousSnapshot)
			mayReusePrevious = sameDocumentSet(previousSnapshot.DocumentIDs, candidate.DocumentIDs) &&
				sameRunConfiguration(previousSnapshot, candidate)
			if attempt, ok := st.latestAttempt(run.ID); ok {
				for _, selection := range st.parseSelectionsForAttempt(attempt.ID) {
					previous[selection.DocumentID] = selection
				}
			}
		}
	}
	if mayReusePrevious {
		for _, selection := range previous {
			if parsed, exists := st.latestParsed(selection.DocumentID, selection.ConfigHash); exists && parsed.SelectionOrder > selection.SelectionOrder {
				mayReusePrevious = false
				break
			}
		}
	}
	frozen := make([]FrozenDocument, len(docs))
	selections := make([]AttemptParseSelection, len(docs))
	for i, doc := range docs {
		parserID, parserVersion, configHash := parserConfigurationFor(doc, models)
		parsed, hasParsed := st.latestParsed(doc.ID, configHash)
		prior, hasPrior := previous[doc.ID]
		reusePrior := mayReusePrevious && hasPrior && prior.OriginalDigest == doc.Digest && prior.ConfigHash == configHash &&
			prior.ParserID == parserID && prior.ParserVersion == parserVersion && prior.NormalizationVersion == docir.NormalizationVersion &&
			prior.SelectionOrder > 0 && prior.ParsedDocumentID != "" && (!hasParsed || prior.SelectionOrder > parsed.SelectionOrder)
		if hasPrior && hasParsed && prior.ConfigHash == configHash && prior.SelectionOrder == parsed.SelectionOrder && prior.ParsedDocumentID != parsed.ID {
			return nil, nil, errors.New("parse selection generation has conflicting artifact identities")
		}
		var selection AttemptParseSelection
		if reusePrior {
			selection = prior
		} else if hasParsed {
			if err := s.verifyParsedRecordContext(ctx, parsed, doc); err != nil {
				return nil, nil, err
			}
			selection = AttemptParseSelection{
				DocumentID: doc.ID, OriginalDigest: doc.Digest, ParsedDocumentID: parsed.ID,
				SelectionOrder: parsed.SelectionOrder, ParserID: parsed.ParserID, ParserVersion: parsed.ParserVersion,
				ConfigHash: parsed.ConfigHash, NormalizationVersion: parsed.NormalizationVersion,
				ArtifactDigest: parsed.ArtifactDigest,
			}
		} else {
			var err error
			selection, err = s.newParseSelection(ctx, doc, models)
			if err != nil {
				return nil, nil, err
			}
		}
		selections[i] = selection
		if parsed, ok := st.parsedByID(selection.ParsedDocumentID); ok && parsed.SelectionOrder == selection.SelectionOrder {
			if err := s.verifyParsedRecordContext(ctx, parsed, doc); err != nil {
				return nil, nil, err
			}
			frozen[i] = frozenFromParsed(doc, parsed)
		} else {
			frozen[i] = frozenFromSelection(doc, selection)
		}
	}
	return frozen, selections, nil
}

func attemptSelectionsMatchFrozen(selections []AttemptParseSelection, docs []FrozenDocument) bool {
	if len(selections) != len(docs) {
		return false
	}
	byDocument := make(map[string]AttemptParseSelection, len(selections))
	for _, selection := range selections {
		if _, exists := byDocument[selection.DocumentID]; exists {
			return false
		}
		byDocument[selection.DocumentID] = selection
	}
	for _, frozen := range docs {
		selection, ok := byDocument[frozen.Document.ID]
		if !ok || selection.OriginalDigest != frozen.Document.Digest || selection.ParsedDocumentID != frozen.ParsedDocumentID ||
			selection.SelectionOrder != frozen.SelectionOrder || selection.ParserID != frozen.ParserID ||
			selection.ParserVersion != frozen.ParserVersion || selection.ConfigHash != frozen.ConfigHash ||
			selection.NormalizationVersion != frozen.NormalizationVersion ||
			(selection.ArtifactDigest != "" && selection.ArtifactDigest != frozen.ParsedArtifactDigest) {
			return false
		}
	}
	return true
}

func (s *Store) validateLoadedParseHistory(st state) error {
	for _, attempt := range st.Attempts {
		input, hasInput := st.attemptInput(attempt.ID)
		selections := st.parseSelectionsForAttempt(attempt.ID)
		switch attempt.HistoryKind {
		case "legacy_unfrozen":
			continue
		case "preparing":
			if hasInput {
				return fmt.Errorf("preparing attempt %s already has frozen input", attempt.ID)
			}
			if err := s.verifyAttemptSelectionSnapshot(st, attempt, selections); err != nil {
				return err
			}
		case "frozen":
			if err := s.verifyAttemptSelectionSnapshot(st, attempt, selections); err != nil {
				return err
			}
			if !hasInput || input.AttemptID != attempt.ID || input.RunID != attempt.RunID {
				return fmt.Errorf("frozen attempt %s has no matching input", attempt.ID)
			}
			snapshot := st.snapshotOf(attempt.RunID)
			inputHash, err := frozenInputHash(snapshot, input.Documents)
			if err != nil || inputHash != input.InputHash || !frozenInputMatchesSnapshot(snapshot, input.Documents) ||
				!attemptSelectionsMatchFrozen(selections, input.Documents) {
				return fmt.Errorf("frozen attempt %s input does not match its immutable selection", attempt.ID)
			}
			if err := s.verifyFrozenDocuments(st, input.Documents); err != nil {
				return fmt.Errorf("frozen attempt %s artifact integrity check: %w", attempt.ID, err)
			}
		default:
			if hasInput && attempt.HistoryKind != "legacy_unfrozen" {
				return fmt.Errorf("attempt %s has input without a recognized provenance state", attempt.ID)
			}
		}
	}
	return nil
}

func (s *Store) verifyAttemptSelectionSnapshot(st state, attempt Attempt, selections []AttemptParseSelection) error {
	snapshot := st.snapshotOf(attempt.RunID)
	if len(selections) == 0 || len(selections) != len(snapshot.DocumentIDs) {
		return fmt.Errorf("attempt %s has no complete pinned parse selection", attempt.ID)
	}
	frozen := make([]FrozenDocument, len(selections))
	documentIDs := make([]string, len(selections))
	parsedIDs := make([]string, len(selections))
	for i, selection := range selections {
		if selection.AttemptID != attempt.ID || selection.RunID != attempt.RunID || selection.SelectionOrder <= 0 ||
			!validParsedID(selection.ParsedDocumentID) || selection.ParserID == "" || selection.ParserVersion == "" ||
			selection.ConfigHash == "" || !supportedNormalizationVersion(selection.NormalizationVersion) {
			return fmt.Errorf("attempt %s contains invalid parse selection metadata", attempt.ID)
		}
		doc, ok := st.documentByIdentity(selection.DocumentID, selection.OriginalDigest)
		if !ok {
			return fmt.Errorf("attempt %s parse selection references a missing original", attempt.ID)
		}
		documentIDs[i] = selection.DocumentID
		parsedIDs[i] = selection.ParsedDocumentID
		frozen[i] = frozenFromSelection(doc, selection)
		if parsed, ok := st.parsedByID(selection.ParsedDocumentID); ok {
			if parsed.SelectionOrder != selection.SelectionOrder || parsed.DocumentID != selection.DocumentID ||
				parsed.OriginalDigest != selection.OriginalDigest || parsed.ParserID != selection.ParserID ||
				parsed.ParserVersion != selection.ParserVersion || parsed.ConfigHash != selection.ConfigHash ||
				parsed.NormalizationVersion != selection.NormalizationVersion ||
				(selection.ArtifactDigest != "" && parsed.ArtifactDigest != selection.ArtifactDigest) {
				return fmt.Errorf("attempt %s parse selection differs from its artifact", attempt.ID)
			}
			if err := s.verifyParsedRecordContext(context.Background(), parsed, doc); err != nil {
				return fmt.Errorf("attempt %s parse artifact integrity check: %w", attempt.ID, err)
			}
		} else if selection.ArtifactDigest != "" {
			return fmt.Errorf("attempt %s parse selection references a missing artifact", attempt.ID)
		}
	}
	if !sameDocumentSet(snapshot.DocumentIDs, documentIDs) || !sameDocumentSet(snapshot.ParsedDocumentIDs, parsedIDs) ||
		snapshot.ParseSelectionHash != selectionHash(frozen) || snapshot.ParserConfigurationID != frozenConfigHash(frozen) {
		return fmt.Errorf("attempt %s parse selection does not match its run snapshot", attempt.ID)
	}
	return nil
}
func (s *Store) verifyFrozenDocuments(st state, docs []FrozenDocument) error {
	for _, frozen := range docs {
		if !validParsedID(frozen.ParsedDocumentID) {
			if frozen.SelectionOrder != 0 || frozen.ParsedArtifactDigest != "" || frozen.ConfigHash != "" || frozen.ParserID != "" ||
				frozen.ParserVersion != "" || frozen.NormalizationVersion != "" || frozen.IRDigest != "" ||
				frozen.ParsedCreatedAt != "" || frozen.ParsedStatus != "" || frozen.NormalizedHash != nil {
				return errors.New("legacy frozen input contains partial parse provenance")
			}
			continue
		}
		parsed, ok := st.parsedByID(frozen.ParsedDocumentID)
		if !ok || parsed.ArtifactDigest != frozen.ParsedArtifactDigest {
			return errors.New("frozen input references a missing or different parsed artifact")
		}
		doc, ok := st.documentByIdentity(frozen.Document.ID, frozen.OriginalHash)
		if !ok || doc.Digest != frozen.Document.Digest {
			return errors.New("frozen input original identity is missing")
		}
		if err := s.verifyParsedRecordContext(context.Background(), parsed, doc); err != nil {
			return err
		}
		left, _ := json.Marshal(frozen.IR)
		right, _ := json.Marshal(parsed.IR)
		if frozen.Text != parsed.Text || frozen.Ready != (parsed.Status == "ready") || frozen.FromOCR != parsed.FromOCR ||
			frozen.ParseError != parsed.ParseError || string(left) != string(right) || frozen.SelectionOrder != parsed.SelectionOrder || frozen.ConfigHash != parsed.ConfigHash ||
			frozen.ParserID != parsed.ParserID || frozen.ParserVersion != parsed.ParserVersion ||
			frozen.ParsedCreatedAt != parsed.CreatedAt || frozen.ParsedStatus != parsed.Status || frozen.IRDigest != parsed.IRDigest {
			return errors.New("frozen input payload differs from its parsed artifact")
		}
		if parsed.NormalizedDigest == "" && frozen.NormalizedHash != nil || parsed.NormalizedDigest != "" &&
			(frozen.NormalizedHash == nil || *frozen.NormalizedHash != parsed.NormalizedDigest) {
			return errors.New("frozen input normalized digest differs from its parsed artifact")
		}
	}
	return nil
}

func (s *Store) currentParserConfigurationMatches(st state, docs []FrozenDocument) bool {
	for _, frozen := range docs {
		doc, ok := st.documentByIdentity(frozen.Document.ID, frozen.Document.Digest)
		if !ok {
			return false
		}
		_, _, currentHash := s.parserConfiguration(doc)
		if currentHash != frozen.ConfigHash {
			return false
		}
	}
	return true
}

func validParsedID(id string) bool { return strings.TrimSpace(id) != "" }
