package store

import (
	"context"
	"encoding/json"
	"strconv"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/documentparse"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

// validateEmbeddedOCRScope resolves the complete selected document identity and
// the live original preparation ticket. The child never confers review rights.
func (s *Store) validateEmbeddedOCRScope(ctx context.Context, u accounts.User, child model.DelegatedScope) error {
	if s.serviceOwner != "" || (s.sel.Mode != "development" && s.sel.Mode != "test") || child.Kind != "document" || child.Action != "embedded_prepare_parse" || child.TenantID != u.TenantID || child.ActorID != u.ID || child.OrgID == "" || child.CaseID == "" || child.RunID == "" || child.AttemptID == "" || child.DocumentID == "" || child.JobID != "" || child.WorkerID != "" || child.Generation != 0 || child.ParseSelectionID == "" || !validPreparationHash(child.InputSHA256) || !validPreparationHash(child.ParseSelectionSHA256) || !validPreparationHash(child.ParentTicketDigest) || !validPreparationHash(child.ParseConfigSHA256) || child.AdmissionRevision == "" {
		return errAttemptAuthorizationRevoked
	}
	p, ok := u.CommandAuthority()
	if !ok || p.SessionDigest == "" || p.ContextVersion == "" || p.Revision != child.AdmissionRevision {
		return errAttemptAuthorizationRevoked
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, userScope(u)); err != nil {
		return err
	}
	var digest string
	var version int64
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT session_digest,context_version,scope FROM operation_authority_tickets WHERE ticket_digest=$1 AND tenant_id=$2 AND expires_at>clock_timestamp()`, child.ParentTicketDigest, child.TenantID).Scan(&digest, &version, &raw)
	var parent model.DelegatedScope
	if err != nil || digest != p.SessionDigest || strconv.FormatInt(version, 10) != p.ContextVersion || json.Unmarshal(raw, &parent) != nil || parent.Kind != "execution_prepare" || parent.Action != "embedded_prepare" || parent.AdmissionRevision != child.AdmissionRevision || parent.TenantLiveEpoch != child.TenantLiveEpoch || parent.TenantID != child.TenantID || parent.OrgID != child.OrgID || parent.CaseID != child.CaseID || parent.ActorID != child.ActorID || parent.RunID != child.RunID || parent.AttemptID != child.AttemptID || parent.JobID != "" {
		return errAttemptAuthorizationRevoked
	}
	st, err := s.loadScopedAttemptState(model.WithDelegatedScope(ctx, model.DelegatedScope{}), tx, JobClaim{TenantID: child.TenantID, OrgID: child.OrgID, CaseID: child.CaseID}, child.RunID, child.AttemptID)
	if err != nil || !s.validEmbeddedAuthorityState(st, parent) || len(st.Cases) != 1 || st.Cases[0].ArchivedAt != nil || !authorizeCase(st.Cases[0], u, access.Review) {
		return errAttemptAuthorizationRevoked
	}
	var hasJob bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM worker_jobs WHERE tenant_id=$1 AND org_id=$2 AND run_id=$3 AND attempt_id=$4)`, child.TenantID, child.OrgID, child.RunID, child.AttemptID).Scan(&hasJob); err != nil || hasJob {
		return errAttemptAuthorizationRevoked
	}
	found := false
	for _, sel := range st.AttemptParseSelections {
		if sel.DocumentID == child.DocumentID && sel.OriginalDigest == child.InputSHA256 && sel.ParsedDocumentID == child.ParseSelectionID && parseSelectionFingerprint(sel) == child.ParseSelectionSHA256 && sel.ConfigHash == child.ParseConfigSHA256 {
			if found {
				return errAttemptAuthorizationRevoked
			}
			found = true
		}
	}
	if !found {
		return errAttemptAuthorizationRevoked
	}
	doc, found := st.documentByIdentity(child.DocumentID, child.InputSHA256)
	if !found {
		return errAttemptAuthorizationRevoked
	}
	switch fileKind(doc.Filename, doc.MediaType) {
	case kindImage:
		if child.OCRPayloadSHA256 != "" || child.OCRPage != 0 || child.OCRPart != "" {
			return errAttemptAuthorizationRevoked
		}
	case kindPDF:
		if !validPreparationHash(child.OCRPayloadSHA256) || child.OCRPage < 1 || child.OCRPart != "" {
			return errAttemptAuthorizationRevoked
		}
	case kindDOCX:
		if !validPreparationHash(child.OCRPayloadSHA256) || child.OCRPage != 0 || child.OCRPart == "" || len(child.OCRPart) > 1024 {
			return errAttemptAuthorizationRevoked
		}
	default:
		return errAttemptAuthorizationRevoked
	}
	return tx.Commit(ctx)
}

func (s *Store) mintEmbeddedOCRAuthority(ctx context.Context, user accounts.User, parent model.DelegatedScope, doc Document, selection AttemptParseSelection) (model.DelegatedScope, error) {
	return s.mintEmbeddedOCRPayload(ctx, user, parent, doc, selection, nil)
}

// A non-nil derived payload is accepted only inside the parser's callback.
func (s *Store) mintEmbeddedOCRPayload(ctx context.Context, user accounts.User, parent model.DelegatedScope, doc Document, selection AttemptParseSelection, payload []byte) (model.DelegatedScope, error) {
	var source documentparse.OCRSource
	if payload != nil {
		var ok bool
		source, ok = documentparse.OCRSourceFromContext(ctx)
		if !ok || len(payload) == 0 || !(source.Kind == "pdf" && fileKind(doc.Filename, doc.MediaType) == kindPDF && source.Page > 0 && source.Part == "" || source.Kind == "docx" && fileKind(doc.Filename, doc.MediaType) == kindDOCX && source.Page == 0 && source.Part != "") {
			return model.DelegatedScope{}, errAttemptAuthorizationRevoked
		}
	}
	ctx, proof, err := embeddedAdmissionContext(ctx, user)
	if err != nil || parent.Kind != "execution_prepare" || parent.Action != "embedded_prepare" || parent.OperationTicket == "" || parent.AdmissionRevision != proof.revision || parent.ActorID != user.ID || selection.AttemptID != parent.AttemptID || selection.RunID != parent.RunID || selection.DocumentID != doc.ID || selection.OriginalDigest != doc.Digest || doc.TenantID != parent.TenantID || doc.OrgID != parent.OrgID || doc.CaseID != parent.CaseID {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	if _, err = s.ResolveOperationAuthority(ctx, parent, "worker"); err != nil {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	child := model.DelegatedScope{TenantLiveEpoch: parent.TenantLiveEpoch, Kind: "document", Action: "embedded_prepare_parse", TenantID: parent.TenantID, OrgID: parent.OrgID, CaseID: parent.CaseID, ActorID: parent.ActorID, RunID: parent.RunID, AttemptID: parent.AttemptID, DocumentID: doc.ID, InputSHA256: doc.Digest, ParseSelectionID: selection.ParsedDocumentID, ParseSelectionSHA256: parseSelectionFingerprint(selection), ParseConfigSHA256: selection.ConfigHash, ParentTicketDigest: identityTokenDigest(parent.OperationTicket), AdmissionRevision: proof.revision}
	if payload != nil {
		child.OCRPayloadSHA256 = digestBytes(payload)
		child.OCRPage = source.Page
		child.OCRPart = source.Part
	}
	if err = s.validateEmbeddedOCRScope(ctx, user, child); err != nil {
		return model.DelegatedScope{}, err
	}
	raw, err := randomIdentityToken("op_")
	if err != nil {
		return model.DelegatedScope{}, err
	}
	encoded, err := json.Marshal(stableOperation(child))
	if err != nil {
		return model.DelegatedScope{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return model.DelegatedScope{}, err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, userScope(user)); err != nil {
		return model.DelegatedScope{}, err
	}
	if err = s.fenceAttemptEpoch(ctx, tx, parent); err != nil {
		return model.DelegatedScope{}, err
	}
	var parentScope []byte
	var parentLive bool
	if err = tx.QueryRow(ctx, `SELECT scope, expires_at>clock_timestamp() FROM operation_authority_tickets WHERE ticket_digest=$1 AND tenant_id=$2 AND org_id=$3 FOR SHARE`, child.ParentTicketDigest, parent.TenantID, parent.OrgID).Scan(&parentScope, &parentLive); err != nil || !parentLive {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	var saved model.DelegatedScope
	if json.Unmarshal(parentScope, &saved) != nil || saved != stableOperation(parent) {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	tag, err := tx.Exec(ctx, `INSERT INTO operation_authority_tickets(ticket_digest,tenant_id,org_id,session_digest,principal_id,membership_id,scope,context_version,expires_at,tenant_live_epoch)
 SELECT $1,tenant_id,org_id,session_digest,principal_id,membership_id,$2::jsonb,context_version,LEAST(expires_at,statement_timestamp()+interval '5 minutes'),tenant_live_epoch FROM operation_authority_tickets WHERE ticket_digest=$3 AND tenant_id=$4 AND session_digest=$5 AND context_version=$6 AND expires_at>clock_timestamp()`, identityTokenDigest(raw), encoded, child.ParentTicketDigest, child.TenantID, proof.digest, proof.version)
	if err != nil || tag.RowsAffected() != 1 {
		return model.DelegatedScope{}, errAttemptAuthorizationRevoked
	}
	if err = tx.Commit(ctx); err != nil {
		return model.DelegatedScope{}, err
	}
	child.OperationTicket = raw
	return child, nil
}
