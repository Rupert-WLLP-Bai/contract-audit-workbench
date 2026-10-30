package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/docir"
	"contractaudit/internal/model"
	"contractaudit/internal/objectstore"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// PrepareDocumentRequest binds preparation to the exact durable job claim and immutable parse selection.
type PrepareDocumentRequest struct {
	// TenantID and OrgID are the job claim's trusted tenant and organization.
	// They scope the row-level-security read; every authority row read below
	// must still match them plus the job, worker, generation and selection.
	TenantID   string                `json:"tenant_id"`
	OrgID      string                `json:"org_id"`
	JobID      string                `json:"job_id"`
	WorkerID   string                `json:"worker_id"`
	Generation int64                 `json:"generation"`
	RunID      string                `json:"run_id"`
	AttemptID  string                `json:"attempt_id"`
	DocumentID string                `json:"document_id"`
	Selection  AttemptParseSelection `json:"selection"`
}

// DocumentPreparer delegates worker-side parsing to the document owner.
type DocumentPreparer interface {
	PrepareAttemptDocument(context.Context, PrepareDocumentRequest) (FrozenDocument, error)
}

var ErrRemoteDocumentPreparationRequired = errors.New("remote document preparation is required")

// UseDocumentPreparer installs the authenticated remote preparation client.
func (s *Store) UseDocumentPreparer(preparer DocumentPreparer) {
	s.mu.Lock()
	s.documentPreparer = preparer
	s.mu.Unlock()
}

type documentPrepareAuthority struct {
	Document  Document
	Selection AttemptParseSelection
	Claim     JobClaim
	Parsed    *ParsedDocument
}

func (s *Store) PrepareAttemptDocument(ctx context.Context, request PrepareDocumentRequest) (FrozenDocument, error) {
	if ctx == nil || request.TenantID == "" || request.OrgID == "" || request.JobID == "" || request.WorkerID == "" || request.Generation <= 0 ||
		request.RunID == "" || request.AttemptID == "" || request.DocumentID == "" ||
		request.Selection.AttemptID != request.AttemptID || request.Selection.RunID != request.RunID ||
		request.Selection.DocumentID != request.DocumentID || request.Selection.ParsedDocumentID == "" {
		return FrozenDocument{}, errors.New("document preparation identity is invalid")
	}
	if err := ctx.Err(); err != nil {
		return FrozenDocument{}, err
	}
	authority, err := s.loadDocumentPrepareAuthority(ctx, request)
	if err != nil {
		return FrozenDocument{}, err
	}
	if authority.Parsed != nil {
		if err := s.recheckDocumentPrepareAuthority(ctx, request, authority.Parsed.ArtifactDigest); err != nil {
			return FrozenDocument{}, err
		}
		return frozenFromParsed(authority.Document, *authority.Parsed), nil
	}
	models := s.currentModels()
	parserID, parserVersion, configHash := parserConfigurationFor(authority.Document, models)
	if parserID != request.Selection.ParserID || parserVersion != request.Selection.ParserVersion ||
		configHash != request.Selection.ConfigHash || request.Selection.NormalizationVersion != docir.NormalizationVersion {
		return FrozenDocument{}, errors.New("pinned parser configuration is unavailable")
	}
	scope := model.WithDelegatedScope(ctx, modelScope(model.DelegatedScope{
		Kind: "document", TenantID: authority.Claim.TenantID, OrgID: authority.Claim.OrgID,
		CaseID: authority.Claim.CaseID, ActorID: authority.Claim.ActorID, DocumentID: authority.Document.ID,
		JobID: authority.Claim.JobID, RunID: authority.Claim.RunID, AttemptID: authority.Claim.AttemptID,
		WorkerID: authority.Claim.WorkerID, Generation: authority.Claim.Generation,
		ParseSelectionID: request.Selection.ParsedDocumentID,
	}, models))
	interpreted := s.interpretUnpersistedWithModels(scope, authority.Document, models)
	parsed, err := makeParsedWithSelection(authority.Document, interpreted, request.Selection)
	if err != nil {
		return FrozenDocument{}, err
	}
	if request.Selection.ArtifactDigest != "" && request.Selection.ArtifactDigest != parsed.ArtifactDigest {
		return FrozenDocument{}, errors.New("pinned parse artifact digest changed")
	}
	winner, err := s.persistPreparedParsedDocument(ctx, request, parsed)
	if err != nil {
		return FrozenDocument{}, err
	}
	if err := s.recheckDocumentPrepareAuthority(ctx, request, winner.ArtifactDigest); err != nil {
		return FrozenDocument{}, err
	}
	if err := verifyParsed(winner, authority.Document); err != nil {
		return FrozenDocument{}, err
	}
	return frozenFromParsed(authority.Document, winner), nil
}

const documentPrepareAuthoritySQL = `SELECT
	j.tenant_id, j.org_id, j.case_id, j.run_id, j.attempt_id, j.actor_id, j.kind, j.state, j.generation, j.worker_id,
	r.case_id, r.state,
	a.run_id, a.state, a.actor_id,
	c.tenant_id, c.org_id, c.owner_id, c.member_ids,
	d.id, d.tenant_id, d.org_id, d.case_id, d.role, d.filename, d.media_type, d.digest, d.size, d.origin, d.storage_backend, d.created_at,
	p.attempt_id, p.run_id, p.document_id, p.original_digest, p.parsed_document_id, p.selection_order, p.parser_id, p.parser_version, p.config_hash, p.normalization_version, p.artifact_digest,
	pd.id, pd.selection_order, pd.document_id, pd.original_digest, pd.parser_id, pd.parser_version, pd.config_hash, pd.normalization_version, pd.normalized_digest, pd.ir_digest, pd.artifact_digest, pd.created_at, pd.status, pd.payload
FROM worker_jobs AS j
JOIN review_runs AS r ON r.id=j.run_id
JOIN review_attempts AS a ON a.id=j.attempt_id AND a.run_id=j.run_id
JOIN cases AS c ON c.id=j.case_id
JOIN review_attempt_parse_selections AS p ON p.attempt_id=a.id AND p.run_id=r.id
JOIN documents AS d ON d.id=p.document_id AND d.case_id=c.id
LEFT JOIN parsed_documents AS pd ON pd.id=p.parsed_document_id
WHERE j.id=$1 AND j.worker_id=$2 AND j.generation=$3 AND j.state='running' AND j.lease_until > clock_timestamp()
	AND j.kind='review' AND j.run_id=$4 AND j.attempt_id=$5 AND p.document_id=$6 AND p.parsed_document_id=$7
	AND j.tenant_id=$8 AND j.org_id=$9`

func (s *Store) loadDocumentPrepareAuthority(ctx context.Context, request PrepareDocumentRequest) (documentPrepareAuthority, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return documentPrepareAuthority{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, documentPrepareLookupScope(request)); err != nil {
		return documentPrepareAuthority{}, err
	}
	authority, err := scanDocumentPrepareAuthority(ctx, tx, documentPrepareAuthoritySQL, request)
	if err != nil {
		return documentPrepareAuthority{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return documentPrepareAuthority{}, err
	}
	return authority, nil
}

func documentPrepareLookupScope(request PrepareDocumentRequest) dbScope {
	return dbScope{ActorID: "document-service", Role: "document", TenantID: request.TenantID, OrgID: request.OrgID,
		JobID: request.JobID, WorkerID: request.WorkerID, Generation: request.Generation}
}

func scanDocumentPrepareAuthority(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, query string, request PrepareDocumentRequest) (documentPrepareAuthority, error) {
	var claim JobClaim
	var jobKind, jobState, runCaseID, runState, attemptRunID, attemptState, attemptActorID string
	var caseTenantID, caseOrgID, caseOwnerID, memberIDs string
	var document Document
	var selection AttemptParseSelection
	var parsedID, parsedDocumentID, parsedOriginalDigest, parsedParserID, parsedParserVersion sql.NullString
	var parsedConfigHash, parsedNormalization, parsedNormalizedDigest, parsedIRDigest sql.NullString
	var parsedArtifactDigest, parsedCreatedAt, parsedStatus, parsedPayload sql.NullString
	var parsedOrder pgtype.Int8
	err := q.QueryRow(ctx, query,
		request.JobID, request.WorkerID, request.Generation, request.RunID, request.AttemptID,
		request.DocumentID, request.Selection.ParsedDocumentID, request.TenantID, request.OrgID,
	).Scan(
		&claim.TenantID, &claim.OrgID, &claim.CaseID, &claim.RunID, &claim.AttemptID, &claim.ActorID, &jobKind, &jobState, &claim.Generation, &claim.WorkerID,
		&runCaseID, &runState, &attemptRunID, &attemptState, &attemptActorID,
		&caseTenantID, &caseOrgID, &caseOwnerID, &memberIDs,
		&document.ID, &document.TenantID, &document.OrgID, &document.CaseID, &document.Role, &document.Filename, &document.MediaType, &document.Digest, &document.Size, &document.Origin, &document.StorageBackend, &document.CreatedAt,
		&selection.AttemptID, &selection.RunID, &selection.DocumentID, &selection.OriginalDigest, &selection.ParsedDocumentID, &selection.SelectionOrder, &selection.ParserID, &selection.ParserVersion, &selection.ConfigHash, &selection.NormalizationVersion, &selection.ArtifactDigest,
		&parsedID, &parsedOrder, &parsedDocumentID, &parsedOriginalDigest, &parsedParserID, &parsedParserVersion, &parsedConfigHash, &parsedNormalization, &parsedNormalizedDigest, &parsedIRDigest, &parsedArtifactDigest, &parsedCreatedAt, &parsedStatus, &parsedPayload,
	)
	if err != nil {
		return documentPrepareAuthority{}, errors.New("document preparation authority is unavailable")
	}
	if jobState != "running" || claim.RunID != request.RunID || claim.AttemptID != request.AttemptID || claim.WorkerID != request.WorkerID || claim.Generation != request.Generation ||
		claim.TenantID != request.TenantID || claim.OrgID != request.OrgID ||
		runCaseID != claim.CaseID || runState != "running" || attemptRunID != request.RunID || !attemptActive(attemptState) || attemptActorID != claim.ActorID ||
		claim.CaseID == "" || claim.TenantID == "" || claim.OrgID == "" || claim.ActorID == "" || document.TenantID != claim.TenantID || document.OrgID != claim.OrgID || document.CaseID != claim.CaseID ||
		selection.AttemptID != request.AttemptID || selection.RunID != request.RunID || selection.DocumentID != request.DocumentID || selection.ParsedDocumentID != request.Selection.ParsedDocumentID ||
		selection != request.Selection {
		return documentPrepareAuthority{}, errors.New("document preparation authority is no longer current")
	}
	var caseItemMembers []string
	if err := json.Unmarshal([]byte(memberIDs), &caseItemMembers); err != nil {
		return documentPrepareAuthority{}, errors.New("current case grants are unavailable")
	}
	caseItem := Case{ID: claim.CaseID, TenantID: caseTenantID, OwnerOrgID: caseOrgID, OwnerID: caseOwnerID, MemberIDs: caseItemMembers}
	user, err := trustedUser(accounts.User{ID: claim.ActorID})
	if err != nil || accounts.Tenant(user) != claim.TenantID || caseTenantID != claim.TenantID || caseOrgID != claim.OrgID || !authorizeCase(caseItem, user, access.Review) {
		return documentPrepareAuthority{}, errors.New("current case review grant is unavailable")
	}
	claim.Kind = jobKind
	authority := documentPrepareAuthority{Document: document, Selection: selection, Claim: claim}
	if parsedID.Valid {
		parsed, err := parsePayload(parsedPayload.String)
		if err != nil || !parsedOrder.Valid || parsed.ID != parsedID.String || parsed.SelectionOrder != parsedOrder.Int64 ||
			parsed.DocumentID != parsedDocumentID.String || parsed.OriginalDigest != parsedOriginalDigest.String || parsed.ParserID != parsedParserID.String ||
			parsed.ParserVersion != parsedParserVersion.String || parsed.ConfigHash != parsedConfigHash.String || parsed.NormalizationVersion != parsedNormalization.String ||
			parsed.NormalizedDigest != parsedNormalizedDigest.String || parsed.IRDigest != parsedIRDigest.String || parsed.ArtifactDigest != parsedArtifactDigest.String ||
			parsed.CreatedAt != parsedCreatedAt.String || parsed.Status != parsedStatus.String || parsed.ArtifactDigest != selection.ArtifactDigest && selection.ArtifactDigest != "" ||
			verifyParsed(parsed, document) != nil {
			return documentPrepareAuthority{}, errors.New("stored parsed document failed integrity verification")
		}
		authority.Parsed = &parsed
	} else if selection.ArtifactDigest != "" {
		return documentPrepareAuthority{}, errors.New("pinned parsed document is unavailable")
	}
	return authority, nil
}

func (s *Store) persistPreparedParsedDocument(ctx context.Context, request PrepareDocumentRequest, parsed ParsedDocument) (ParsedDocument, error) {
	// This path inserts into parsed_documents directly inside its own transaction
	// rather than going through updateState, so validateServiceDelta never sees
	// it. It must therefore assert the ownership the delta guard would otherwise
	// enforce; otherwise the guard could be bypassed by any future caller. Only
	// the document service owns parsed artifacts, and it is the only service that
	// serves this preparation endpoint.
	if !ownedTable(s.serviceOwner, "parsed_documents") {
		return ParsedDocument{}, errors.New("service cannot persist parsed artifacts it does not own")
	}
	if err := verifyParsed(parsed, Document{ID: parsed.DocumentID, Digest: parsed.OriginalDigest}); err != nil {
		return ParsedDocument{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ParsedDocument{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, documentPrepareLookupScope(request)); err != nil {
		return ParsedDocument{}, err
	}
	// The document role can read execution and core authority, but cannot lock
	// their rows with FOR SHARE (which requires UPDATE privileges). Only the
	// immutable parse artifact is written here. Revalidate the live claim before
	// commit below and again before returning; the worker fences result adoption.
	authority, err := scanDocumentPrepareAuthority(ctx, tx, documentPrepareAuthoritySQL, request)
	if err != nil {
		return ParsedDocument{}, err
	}
	if authority.Parsed != nil {
		if err := tx.Commit(ctx); err != nil {
			return ParsedDocument{}, err
		}
		return *authority.Parsed, nil
	}
	if parsed.ID != request.Selection.ParsedDocumentID || parsed.SelectionOrder != request.Selection.SelectionOrder || parsed.DocumentID != request.DocumentID ||
		parsed.OriginalDigest != request.Selection.OriginalDigest || parsed.ParserID != request.Selection.ParserID || parsed.ParserVersion != request.Selection.ParserVersion ||
		parsed.ConfigHash != request.Selection.ConfigHash || parsed.NormalizationVersion != request.Selection.NormalizationVersion ||
		(request.Selection.ArtifactDigest != "" && parsed.ArtifactDigest != request.Selection.ArtifactDigest) {
		return ParsedDocument{}, errors.New("parsed document does not match the pinned selection")
	}
	if err := setLocalScope(ctx, tx, authority.Claim.scope()); err != nil {
		return ParsedDocument{}, err
	}
	payload, err := parsed.canonicalJSON()
	if err != nil {
		return ParsedDocument{}, err
	}
	var normalized, irDigest any
	if parsed.NormalizedDigest != "" {
		normalized = parsed.NormalizedDigest
	}
	if parsed.IRDigest != "" {
		irDigest = parsed.IRDigest
	}
	if _, err := tx.Exec(ctx, `INSERT INTO parsed_documents (selection_order, id, document_id, original_digest, parser_id, parser_version, config_hash, normalization_version, normalized_digest, ir_digest, artifact_digest, created_at, status, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT (id) DO NOTHING`,
		parsed.SelectionOrder, parsed.ID, parsed.DocumentID, parsed.OriginalDigest, parsed.ParserID, parsed.ParserVersion, parsed.ConfigHash, parsed.NormalizationVersion,
		normalized, irDigest, parsed.ArtifactDigest, parsed.CreatedAt, parsed.Status, payload); err != nil {
		return ParsedDocument{}, err
	}
	winnerAuthority, err := scanDocumentPrepareAuthority(ctx, tx, documentPrepareAuthoritySQL, request)
	if err != nil || winnerAuthority.Parsed == nil {
		return ParsedDocument{}, errors.New("parsed document commit could not be verified")
	}
	winner := *winnerAuthority.Parsed
	if err := tx.Commit(ctx); err != nil {
		return ParsedDocument{}, err
	}
	return winner, nil
}

func (s *Store) recheckDocumentPrepareAuthority(ctx context.Context, request PrepareDocumentRequest, artifactDigest string) error {
	authority, err := s.loadDocumentPrepareAuthority(ctx, request)
	if err != nil {
		return err
	}
	if authority.Parsed == nil || authority.Parsed.ArtifactDigest != artifactDigest {
		return errors.New("parsed document authority changed before return")
	}
	return nil
}

func (s *Store) remoteDocumentPreparer() (bool, DocumentPreparer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requireRemoteDocuments, s.documentPreparer
}

func parsedDocumentFromFrozen(frozen FrozenDocument) ParsedDocument {
	parsed := ParsedDocument{
		ID: frozen.ParsedDocumentID, SelectionOrder: frozen.SelectionOrder, DocumentID: frozen.Document.ID,
		OriginalDigest: frozen.OriginalHash, ParserID: frozen.ParserID, ParserVersion: frozen.ParserVersion,
		ConfigHash: frozen.ConfigHash, NormalizationVersion: frozen.NormalizationVersion,
		IRDigest: frozen.IRDigest, ArtifactDigest: frozen.ParsedArtifactDigest, CreatedAt: frozen.ParsedCreatedAt,
		Status: frozen.ParsedStatus, Text: frozen.Text, IR: frozen.IR, ParseError: frozen.ParseError, FromOCR: frozen.FromOCR,
	}
	if frozen.NormalizedHash != nil {
		parsed.NormalizedDigest = *frozen.NormalizedHash
	}
	return parsed
}

// DocumentPreparationKind validates the delegated claim and classifies work for
// independent CPU and OCR capacity controls.
func (s *Store) DocumentPreparationKind(ctx context.Context, request PrepareDocumentRequest) (string, error) {
	authority, err := s.loadDocumentPrepareAuthority(ctx, request)
	if err != nil {
		return "", err
	}
	return DocumentParseKind(authority.Document.Filename, authority.Document.MediaType), nil
}

// DocumentReady checks the database and authenticated object storage without
// modifying stored data.
func (s *Store) DocumentReady(ctx context.Context) error {
	if err := s.Ready(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	objects := s.objects
	s.mu.Unlock()
	if objects == nil {
		return errors.New("private object storage is unavailable")
	}
	_, err := objects.Get(ctx, "healthcheck/document-service")
	if errors.Is(err, objectstore.ErrObjectNotFound) {
		return nil
	}
	return err
}
