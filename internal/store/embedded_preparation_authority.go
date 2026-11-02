package store

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

func validPreparationHash(v string) bool {
	if len(v) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(v)
	return err == nil && hex.EncodeToString(decoded) == v
}

// preparationSelectionHash commits to every field in database selection order.
func preparationSelectionHash(selections []AttemptParseSelection) string {
	if len(selections) == 0 {
		return ""
	}
	for i, item := range selections {
		if item.SelectionOrder <= 0 || (i > 0 && selections[i-1].SelectionOrder >= item.SelectionOrder) {
			return ""
		}
	}
	raw, err := json.Marshal(selections)
	if err != nil {
		return ""
	}
	return sha256Bytes(raw)
}

func (s *Store) validEmbeddedAuthorityState(st state, scope model.DelegatedScope) bool {
	if len(st.Attempts) != 1 || len(st.Runs) != 1 || len(st.Snapshots) != 1 {
		return false
	}
	a, r := st.Attempts[0], st.Runs[0]
	if a.ID != scope.AttemptID || a.RunID != scope.RunID || a.ActorID != scope.ActorID || r.ID != scope.RunID || r.CaseID != scope.CaseID || !attemptActive(a.State) || !attemptActive(r.State) {
		return false
	}
	selections := st.parseSelectionsForAttempt(a.ID)
	hash := preparationSelectionHash(selections)
	if hash == "" || !validEmbeddedSelectionSnapshot(st, a, selections) {
		return false
	}
	if scope.Kind == "execution_prepare" {
		return a.HistoryKind == "preparing" && a.InputHash == nil && len(st.AttemptInputs) == 0 && hash == scope.InputSHA256
	}
	if scope.Kind != "execution" || a.HistoryKind != "frozen" || a.InputHash == nil || len(st.AttemptInputs) != 1 {
		return false
	}
	input := st.AttemptInputs[0]
	actual, err := frozenInputHash(st.Snapshots[0], input.Documents)
	if err != nil || input.AttemptID != a.ID || input.RunID != a.RunID || input.FrozenAt == "" || input.InputHash != actual || *a.InputHash != actual || !attemptSelectionsMatchFrozen(selections, input.Documents) || !frozenInputMatchesSnapshot(st.Snapshots[0], input.Documents) || !validEmbeddedFrozenDocuments(st, input.Documents) {
		return false
	}
	return scope.AdmissionRevision == "" || scope.InputSHA256 == actual
}

// Restore only the proof attached to the original admitted user.
func embeddedAdmissionContext(ctx context.Context, user accounts.User) (context.Context, commandProof, error) {
	p, ok := user.CommandAuthority()
	if !ok || p.SessionDigest == "" || p.ContextVersion == "" || p.Revision == "" || len(p.OperationScope) != 0 {
		return ctx, commandProof{}, errAttemptAuthorizationRevoked
	}
	proof := commandProof{digest: p.SessionDigest, version: p.ContextVersion, revision: p.Revision, support: p.SupportID}
	if existing, present := contextProof(ctx); present && len(existing.operation) == 0 && (existing.digest != proof.digest || existing.version != proof.version || existing.revision != proof.revision) {
		return ctx, commandProof{}, errAttemptAuthorizationRevoked
	}
	ctx = model.WithDelegatedScope(ctx, model.DelegatedScope{})
	ctx = context.WithValue(ctx, commandProofKey{}, proof)
	return commandContext(ctx, user), proof, nil
}

func (s *Store) embeddedAuthorityState(ctx context.Context, user accounts.User, scope model.DelegatedScope) (state, error) {
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return state{}, err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, userScope(user)); err != nil {
		return state{}, err
	}
	st, err := s.loadScopedAttemptState(ctx, tx, JobClaim{TenantID: scope.TenantID, OrgID: scope.OrgID, CaseID: scope.CaseID}, scope.RunID, scope.AttemptID)
	if err != nil {
		return state{}, err
	}
	if len(st.Snapshots) != 1 {
		return state{}, errAttemptAuthorizationRevoked
	}
	if err = loadPreparationMaterials(ctx, tx, &st, JobClaim{TenantID: scope.TenantID, OrgID: scope.OrgID, CaseID: scope.CaseID}, st.Snapshots[0].DocumentIDs); err != nil {
		return state{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return state{}, err
	}
	return st, nil
}

func (s *Store) prepareEmbeddedAuthority(ctx context.Context, user accounts.User, item Case, runID, attemptID string) (model.DelegatedScope, error) {
	ctx, proof, err := embeddedAdmissionContext(ctx, user)
	if err != nil || s.serviceOwner != "" || (s.sel.Mode != "development" && s.sel.Mode != "test") {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	scope := model.DelegatedScope{Kind: "execution_prepare", Action: "embedded_prepare", TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID, ActorID: user.ID, RunID: runID, AttemptID: attemptID, AdmissionRevision: proof.revision}
	st, err := s.embeddedAuthorityState(ctx, user, scope)
	if err != nil || len(st.Attempts) != 1 {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	if st.Attempts[0].HistoryKind == "frozen" {
		if len(st.AttemptInputs) != 1 {
			return model.DelegatedScope{}, errAttemptAuthorizationRevoked
		}
		scope.Kind, scope.Action, scope.InputSHA256 = "execution", "embedded_review", st.AttemptInputs[0].InputHash
	} else {
		scope.InputSHA256 = preparationSelectionHash(st.parseSelectionsForAttempt(attemptID))
	}
	if !s.validEmbeddedAuthorityState(st, scope) {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	ticket, err := s.CreateOperationTicket(ctx, user, scope)
	if err != nil || ticket == "" {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	scope.OperationTicket = ticket
	return scope, nil
}

func (s *Store) promoteEmbeddedAuthority(ctx context.Context, user accounts.User, original model.DelegatedScope) (model.DelegatedScope, error) {
	ctx, proof, err := embeddedAdmissionContext(ctx, user)
	if err != nil || s.serviceOwner != "" || (s.sel.Mode != "development" && s.sel.Mode != "test") || original.Kind != "execution_prepare" || original.Action != "embedded_prepare" || original.OperationTicket == "" || original.AdmissionRevision != proof.revision || original.ActorID != user.ID || original.TenantID != user.TenantID {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	// The original ticket's exact scope and session remain mandatory after freeze.
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return model.DelegatedScope{}, err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, userScope(user)); err != nil {
		return model.DelegatedScope{}, err
	}
	var digest string
	var encoded []byte
	err = tx.QueryRow(ctx, `SELECT session_digest,scope FROM operation_authority_tickets WHERE ticket_digest=$1 AND tenant_id=$2 AND expires_at>clock_timestamp()`, identityTokenDigest(original.OperationTicket), original.TenantID).Scan(&digest, &encoded)
	var saved model.DelegatedScope
	if err != nil || digest != proof.digest || json.Unmarshal(encoded, &saved) != nil || saved != stableOperation(original) {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	if err = tx.Commit(ctx); err != nil {
		return model.DelegatedScope{}, err
	}
	st, err := s.embeddedAuthorityState(ctx, user, original)
	if err != nil || preparationSelectionHash(st.parseSelectionsForAttempt(original.AttemptID)) != original.InputSHA256 || len(st.AttemptInputs) != 1 {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	next := original
	next.Kind, next.Action, next.OperationTicket, next.InputSHA256 = "execution", "embedded_review", "", st.AttemptInputs[0].InputHash
	if !s.validEmbeddedAuthorityState(st, next) {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	ticket, err := s.CreateOperationTicket(ctx, user, next)
	if err != nil || ticket == "" {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	next.OperationTicket = ticket
	return next, nil
}

// These checks use the already scoped, immutable rows in the same read transaction.
func validEmbeddedSelectionSnapshot(st state, a Attempt, selections []AttemptParseSelection) bool {
	snapshot := st.snapshotOf(a.RunID)
	if len(selections) == 0 || len(selections) != len(snapshot.DocumentIDs) {
		return false
	}
	frozen := make([]FrozenDocument, len(selections))
	docs, parsed := make([]string, len(selections)), make([]string, len(selections))
	seenDocs, seenParsed := map[string]bool{}, map[string]bool{}
	for i, sel := range selections {
		if sel.AttemptID != a.ID || sel.RunID != a.RunID || sel.SelectionOrder <= 0 || !validParsedID(sel.ParsedDocumentID) || sel.ParserID == "" || sel.ParserVersion == "" || sel.ConfigHash == "" || !supportedNormalizationVersion(sel.NormalizationVersion) || seenDocs[sel.DocumentID] || seenParsed[sel.ParsedDocumentID] {
			return false
		}
		doc, ok := st.documentByIdentity(sel.DocumentID, sel.OriginalDigest)
		if !ok {
			return false
		}
		seenDocs[sel.DocumentID], seenParsed[sel.ParsedDocumentID] = true, true
		docs[i], parsed[i], frozen[i] = sel.DocumentID, sel.ParsedDocumentID, frozenFromSelection(doc, sel)
		if p, ok := st.parsedByID(sel.ParsedDocumentID); ok {
			if p.SelectionOrder != sel.SelectionOrder || p.DocumentID != sel.DocumentID || p.OriginalDigest != sel.OriginalDigest || p.ParserID != sel.ParserID || p.ParserVersion != sel.ParserVersion || p.ConfigHash != sel.ConfigHash || p.NormalizationVersion != sel.NormalizationVersion || (sel.ArtifactDigest != "" && sel.ArtifactDigest != p.ArtifactDigest) || verifyParsed(p, doc) != nil {
				return false
			}
		} else if sel.ArtifactDigest != "" {
			return false
		}
	}
	return sameDocumentSet(snapshot.DocumentIDs, docs) && sameDocumentSet(snapshot.ParsedDocumentIDs, parsed) && snapshot.ParseSelectionHash == selectionHash(frozen) && snapshot.ParserConfigurationID == frozenConfigHash(frozen)
}

func validEmbeddedFrozenDocuments(st state, docs []FrozenDocument) bool {
	for _, f := range docs {
		p, ok := st.parsedByID(f.ParsedDocumentID)
		doc, docOK := st.documentByIdentity(f.Document.ID, f.OriginalHash)
		if !ok || !docOK || verifyParsed(p, doc) != nil {
			return false
		}
		// StorageBackend is deliberately excluded from frozen JSON; compare the
		// persisted identity and material with the scoped current original.
		f.Document.StorageBackend = doc.StorageBackend
		if !preparationFrozenMatches(st, doc, p, f) {
			return false
		}
	}
	return true
}
