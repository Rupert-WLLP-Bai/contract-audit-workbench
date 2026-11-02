package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

// OperationAuthorityResolver is a trusted Core channel, installed only by the
// process composition root. A caller-supplied User is never an implementation.
type OperationAuthorityResolver interface {
	ResolveOperation(context.Context, model.DelegatedScope) (accounts.User, error)
}

func (s *Store) UseOperationAuthority(r OperationAuthorityResolver) { s.operationAuthority = r }

func stableOperation(scope model.DelegatedScope) model.DelegatedScope {
	scope.OperationTicket = ""
	scope.ConfigurationID = ""
	scope.ProviderURL = ""
	return scope
}

func (s *Store) operationUser(ctx context.Context, scope model.DelegatedScope) (accounts.User, error) {
	if s.operationAuthority != nil {
		return s.operationAuthority.ResolveOperation(ctx, scope)
	}

	// Demo identities are available only in non-production stores. The ordinary
	// SQL lease/case checks still run at each caller; enterprise work never falls
	// back to the directory when its authority channel is missing.
	if s.sel.Mode != "production" {
		u, ok := accounts.ByID(scope.ActorID)
		if ok && u.Enabled && u.Issuer == "" && u.Subject == "" && (u.TenantID == accounts.LocalDemoTenant || u.TenantID == accounts.LocalDemoTenant2) && u.TenantID == scope.TenantID && u.OrgID == scope.OrgID {
			return u, nil
		}
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}

	return s.ResolveOperationAuthority(ctx, scope, "worker")
}

func (s *Store) ResolveOperationAuthority(ctx context.Context, scope model.DelegatedScope, caller string) (accounts.User, error) {
	if scope.TenantID == "" || scope.OrgID == "" || scope.CaseID == "" || scope.ActorID == "" ||
		(caller != "worker" && caller != "document" && caller != "model") ||
		(caller == "document" && scope.Kind != "document") ||
		(caller == "worker" && scope.Kind != "execution" && scope.Kind != "experience_distill") {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	if scope.JobID == "" {
		return s.resolveOperationTicket(ctx, scope)
	}
	if scope.OperationTicket != "" || scope.Action != "" {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	phase := "worker_start"
	authority, err := s.ResolveWorkerClaimAuthority(ctx, ToolInvocationContext{TenantID: scope.TenantID, OrgID: scope.OrgID, JobID: scope.JobID, WorkerID: scope.WorkerID, Generation: scope.Generation, CaseID: scope.CaseID, RunID: scope.RunID, AttemptID: scope.AttemptID}, phase)
	if err != nil || authority.User.ID != scope.ActorID {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	u := authority.User
	if !accounts.CanStart(u.Role) {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return accounts.User{}, err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, userScope(u)); err != nil {
		return accounts.User{}, err
	}
	if scope.Kind != "experience_distill" && (caller == "model" || scope.Kind == "document") {
		var running bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM review_runs r JOIN review_attempts a ON a.run_id=r.id AND a.tenant_id=r.tenant_id AND a.org_id=r.org_id WHERE r.id=$1 AND a.id=$2 AND r.state='running' AND a.state IN ('accepted','running'))`, scope.RunID, scope.AttemptID).Scan(&running); err != nil || !running {
			return accounts.User{}, errAttemptAuthorizationRevoked
		}
	}
	var matches bool
	switch scope.Kind {
	case "execution":
		if scope.RunID == "" || scope.AttemptID == "" || scope.DocumentID != "" || scope.ParseSelectionID != "" || scope.InputSHA256 != "" {
			return accounts.User{}, errAttemptAuthorizationRevoked
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM worker_jobs j JOIN review_attempts a ON a.id=j.attempt_id AND a.run_id=j.run_id AND a.tenant_id=j.tenant_id AND a.org_id=j.org_id WHERE j.id=$1 AND j.kind='review' AND ($2::text<>'model' OR (a.history_kind='frozen' AND a.input_hash IS NOT NULL)))`, scope.JobID, caller).Scan(&matches)
	case "document":
		if scope.RunID == "" || scope.AttemptID == "" || scope.DocumentID == "" || scope.ParseSelectionID == "" {
			return accounts.User{}, errAttemptAuthorizationRevoked
		}

		var selection AttemptParseSelection
		err = tx.QueryRow(ctx, `SELECT p.attempt_id,p.run_id,p.document_id,p.original_digest,p.parsed_document_id,p.selection_order,p.parser_id,p.parser_version,p.config_hash,p.normalization_version,p.artifact_digest
   FROM review_attempt_parse_selections p JOIN documents d ON d.id=p.document_id AND d.tenant_id=p.tenant_id AND d.org_id=p.org_id
   WHERE p.tenant_id=$1 AND p.org_id=$2 AND p.run_id=$3 AND p.attempt_id=$4 AND p.document_id=$5 AND p.parsed_document_id=$6 AND d.case_id=$7`, scope.TenantID, scope.OrgID, scope.RunID, scope.AttemptID, scope.DocumentID, scope.ParseSelectionID, scope.CaseID).Scan(&selection.AttemptID, &selection.RunID, &selection.DocumentID, &selection.OriginalDigest, &selection.ParsedDocumentID, &selection.SelectionOrder, &selection.ParserID, &selection.ParserVersion, &selection.ConfigHash, &selection.NormalizationVersion, &selection.ArtifactDigest)
		matches = err == nil && scope.ParseSelectionSHA256 == parseSelectionFingerprint(selection)

	case "experience_distill":
		if scope.RunID != "" || scope.AttemptID != "" || len(scope.InputSHA256) != 64 || scope.DocumentID != "" || scope.ParseSelectionID != "" {
			return accounts.User{}, errAttemptAuthorizationRevoked
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM worker_jobs j JOIN experience_distill_inputs i ON i.job_id=j.id
   JOIN experience_feedback f ON f.tenant_id=i.tenant_id AND f.org_id=i.org_id AND f.case_id=i.case_id AND f.feedback_id=i.feedback_id
   WHERE j.id=$1 AND j.kind='experience_distill' AND i.tenant_id=$2 AND i.org_id=$3 AND i.case_id=$4 AND i.actor_id=$5 AND i.input_sha256=$6 AND f.applicant_id=i.actor_id)`, scope.JobID, scope.TenantID, scope.OrgID, scope.CaseID, scope.ActorID, scope.InputSHA256).Scan(&matches)
	default:
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	if err != nil || !matches {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	var item Case
	var members []byte
	err = tx.QueryRow(ctx, `SELECT id,tenant_id,org_id,owner_id,member_ids FROM cases WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND archived_at IS NULL`, scope.CaseID, scope.TenantID, scope.OrgID).Scan(&item.ID, &item.TenantID, &item.OwnerOrgID, &item.OwnerID, &members)
	if err != nil || json.Unmarshal(members, &item.MemberIDs) != nil || !authorizeCase(item, u, access.Review) {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	return u, tx.Commit(ctx)
}

// CreateOperationTicket is a Core-only, exact-session admission operation. It
// cannot create an actor-only capability or a durable job credential.
func (s *Store) CreateOperationTicket(ctx context.Context, user accounts.User, scope model.DelegatedScope) (string, error) {
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return "", errors.New("only Core can issue operation tickets")
	}
	if !user.HasResolvedMembership() {
		return "", nil
	}
	digest := admissionSessionDigest(ctx)
	if digest == "" {
		return "", errAttemptAuthorizationRevoked
	}
	_, current, err := s.sessionFromDigest(ctx, digest)
	if err != nil || current.ID != user.ID || current.PrincipalID != user.PrincipalID || current.MembershipID != user.MembershipID {
		return "", errAttemptAuthorizationRevoked
	}
	if err = s.validateNonJobOperation(ctx, current, scope); err != nil {
		return "", err
	}
	raw, err := randomIdentityToken("op_")
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(stableOperation(scope))
	if err != nil {
		return "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, userScope(current)); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO operation_authority_tickets(ticket_digest,tenant_id,org_id,session_digest,principal_id,membership_id,scope) VALUES($1,$2,$3,$4,$5::uuid,$6::uuid,$7::jsonb)`, identityTokenDigest(raw), current.TenantID, current.OrgID, digest, current.PrincipalID, current.MembershipID, encoded)
	if err != nil {
		return "", err
	}
	return raw, tx.Commit(ctx)
}

func (s *Store) validateNonJobOperation(ctx context.Context, u accounts.User, scope model.DelegatedScope) error {
	if scope.JobID != "" || scope.WorkerID != "" || scope.Generation != 0 || scope.ParseSelectionID != "" || scope.ParseSelectionSHA256 != "" || scope.ActorID != u.ID || scope.TenantID != u.TenantID || scope.OrgID == "" {
		return errAttemptAuthorizationRevoked
	}
	action := access.Manage
	if scope.Kind == "document" && !accounts.CanUpload(u.Role) {
		return errAttemptAuthorizationRevoked
	}
	if scope.Kind == "execution" && !accounts.CanStart(u.Role) {
		return errAttemptAuthorizationRevoked
	}
	switch {
	case scope.Kind == "document" && (scope.Action == "reparse" || scope.Action == "upload_parse") && scope.DocumentID != "" && scope.RunID == "" && scope.AttemptID == "":
	case scope.Kind == "execution" && scope.Action == "embedded_review" && scope.RunID != "" && scope.AttemptID != "" && scope.DocumentID == "":
		action = access.Review
	default:
		return errAttemptAuthorizationRevoked
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, userScope(u)); err != nil {
		return err
	}
	var item Case
	var members []byte
	err = tx.QueryRow(ctx, `SELECT id,tenant_id,org_id,owner_id,member_ids FROM cases WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND archived_at IS NULL`, scope.CaseID, u.TenantID, scope.OrgID).Scan(&item.ID, &item.TenantID, &item.OwnerOrgID, &item.OwnerID, &members)
	if err != nil || json.Unmarshal(members, &item.MemberIDs) != nil || !authorizeCase(item, u, action) {
		return errAttemptAuthorizationRevoked
	}
	var valid bool
	if scope.Kind == "document" && scope.Action == "upload_parse" {
		valid = len(scope.InputSHA256) == 64 && scope.ParseSelectionID == ""
	} else if scope.Kind == "document" {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE id=$1 AND tenant_id=$2 AND org_id=$3 AND case_id=$4 AND digest=$5)`, scope.DocumentID, scope.TenantID, scope.OrgID, scope.CaseID, scope.InputSHA256).Scan(&valid)
	} else {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM review_attempts a JOIN review_runs r ON r.id=a.run_id AND r.tenant_id=a.tenant_id AND r.org_id=a.org_id WHERE a.id=$1 AND r.id=$2 AND r.case_id=$3 AND a.actor_id=$4 AND a.history_kind='frozen' AND a.input_hash IS NOT NULL AND a.state IN ('accepted','running') AND r.state IN ('accepted','running'))`, scope.AttemptID, scope.RunID, scope.CaseID, u.ID).Scan(&valid)
	}
	if err != nil || !valid {
		return errAttemptAuthorizationRevoked
	}
	return tx.Commit(ctx)
}

func (s *Store) resolveOperationTicket(ctx context.Context, scope model.DelegatedScope) (accounts.User, error) {
	if scope.OperationTicket == "" {
		u, ok := accounts.ByID(scope.ActorID)
		if ok && u.Enabled && u.Issuer == "" && u.Subject == "" && u.TenantID == scope.TenantID && u.OrgID == scope.OrgID {
			return u, s.validateNonJobOperation(ctx, u, scope)
		}
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	if strings.HasPrefix(scope.OperationTicket, "iat_") {
		if scope.Kind != "document" || scope.Action != "intake_parse" || scope.RunID != "" || scope.AttemptID != "" || scope.DocumentID == "" {
			return accounts.User{}, errAttemptAuthorizationRevoked
		}
		a, err := s.ResolveDocumentIntakeTicket(ctx, scope.OperationTicket, scope.InputSHA256)
		if err != nil || a.ActorID != scope.ActorID || a.TenantID != scope.TenantID || a.OrgID != scope.OrgID || a.CaseID != scope.CaseID {
			return accounts.User{}, errAttemptAuthorizationRevoked
		}
		u := accounts.MembershipUser(a.ActorID, "", a.Role, a.TenantID, a.OrgID, a.Issuer, a.Subject, a.Grants)
		u.PrincipalID = a.PrincipalID
		u.MembershipID = a.MembershipID
		if a.PrincipalID == "" {
			profile, ok := accounts.ByID(a.ActorID)
			if !ok || profile.Issuer != "" || profile.Subject != "" {
				return accounts.User{}, errAttemptAuthorizationRevoked
			}
			u = profile
		}
		// Atomically pin the generated parse-document identity before the first
		// model call; the original document is committed only after parsing succeeds.
		var sessionDigest string
		var principal, membership *string
		err = s.pool.QueryRow(ctx, `SELECT session_digest,principal_id::text,membership_id::text FROM tenant_identity_intake_ticket($1)`, identityTokenDigest(scope.OperationTicket)).Scan(&sessionDigest, &principal, &membership)
		if err != nil {
			return accounts.User{}, errAttemptAuthorizationRevoked
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return accounts.User{}, err
		}
		defer tx.Rollback(ctx)
		if err = setLocalScope(ctx, tx, userScope(u)); err != nil {
			return accounts.User{}, err
		}
		encoded, _ := json.Marshal(stableOperation(scope))
		digest := identityTokenDigest(scope.OperationTicket)
		_, err = tx.Exec(ctx, `INSERT INTO operation_authority_tickets(ticket_digest,tenant_id,org_id,session_digest,principal_id,membership_id,scope) VALUES($1,$2,$3,$4,$5::uuid,$6::uuid,$7::jsonb) ON CONFLICT(ticket_digest) DO NOTHING`, digest, scope.TenantID, scope.OrgID, sessionDigest, principal, membership, encoded)
		if err != nil {
			return accounts.User{}, err
		}
		var saved model.DelegatedScope
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT scope FROM operation_authority_tickets WHERE ticket_digest=$1 AND tenant_id=$2 AND expires_at>clock_timestamp()`, digest, scope.TenantID).Scan(&raw)
		if err != nil || json.Unmarshal(raw, &saved) != nil || !reflect.DeepEqual(saved, stableOperation(scope)) {
			return accounts.User{}, errAttemptAuthorizationRevoked
		}
		return u, tx.Commit(ctx)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return accounts.User{}, err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, dbScope{ActorID: "operation-authority", Role: "api", TenantID: scope.TenantID, OrgID: scope.OrgID}); err != nil {
		return accounts.User{}, err
	}
	var digest, principal, membership string
	var encoded []byte
	err = tx.QueryRow(ctx, `SELECT session_digest,principal_id,membership_id,scope FROM operation_authority_tickets WHERE ticket_digest=$1 AND tenant_id=$2 AND expires_at>clock_timestamp()`, identityTokenDigest(scope.OperationTicket), scope.TenantID).Scan(&digest, &principal, &membership, &encoded)
	if err != nil {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	var saved model.DelegatedScope
	if json.Unmarshal(encoded, &saved) != nil || !reflect.DeepEqual(saved, stableOperation(scope)) {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	if err = tx.Commit(ctx); err != nil {
		return accounts.User{}, err
	}
	_, u, err := s.sessionFromDigest(ctx, digest)
	if err != nil || u.PrincipalID != principal || u.MembershipID != membership {
		return accounts.User{}, errAttemptAuthorizationRevoked
	}
	return u, s.validateNonJobOperation(ctx, u, scope)
}

type operationTicketContextKey struct{}

func WithOperationTicket(ctx context.Context, ticket string) context.Context {
	return context.WithValue(ctx, operationTicketContextKey{}, ticket)
}
func operationTicket(ctx context.Context) string {
	v, _ := ctx.Value(operationTicketContextKey{}).(string)
	return v
}

func (s *Store) authorityForAttempt(ctx context.Context, runID, attemptID string) (accounts.User, error) {
	if claim, ok := s.claimForAttempt(attemptID); ok && claim.RunID == runID {
		scope := model.DelegatedScope{Kind: "execution", TenantID: claim.TenantID, OrgID: claim.OrgID, CaseID: claim.CaseID, ActorID: claim.ActorID, JobID: claim.JobID, WorkerID: claim.WorkerID, Generation: claim.Generation, RunID: runID, AttemptID: attemptID}
		if claim.JobID == "" {
			s.claimMu.Lock()
			scope = s.operationContexts[attemptID]
			s.claimMu.Unlock()
		}
		if scope.ActorID != "" {
			return s.operationUser(ctx, scope)
		}
	}
	// Pure local-demo tests and in-process fixtures may have no durable claim.
	// Enterprise identities always need the immutable operation binding above.
	return accounts.User{}, errAttemptAuthorizationRevoked
}

func (s *Store) authorizeAttemptExecution(st state, runID, attemptID string) (accounts.User, error) {
	u, err := s.authorityForAttempt(context.Background(), runID, attemptID)
	if err != nil {
		attempt, ok := st.attemptByID(attemptID)
		if !ok {
			return accounts.User{}, err
		}
		demo, ok := accounts.ByID(attempt.ActorID)
		if !ok || demo.Issuer != "" || demo.Subject != "" {
			return accounts.User{}, err
		}
		u = demo
	}
	return authorizeAttemptExecution(st, runID, attemptID, u)
}

type operationTicketIssuer interface {
	IssueOperationTicket(context.Context, string, model.DelegatedScope) (string, error)
}

func (s *Store) issueOperationTicket(ctx context.Context, user accounts.User, scope model.DelegatedScope) (string, error) {
	if !user.HasResolvedMembership() {
		return "", nil
	}
	if issuer, ok := s.operationAuthority.(operationTicketIssuer); ok {
		return issuer.IssueOperationTicket(ctx, admissionSessionDigest(ctx), scope)
	}
	return s.CreateOperationTicket(ctx, user, scope)
}
func (s *Store) IssueOperationTicketFromDigest(ctx context.Context, digest string, scope model.DelegatedScope) (string, error) {
	_, u, err := s.sessionFromDigest(ctx, digest)
	if err != nil {
		return "", err
	}
	if scope.Kind != "document" || (scope.Action != "reparse" && scope.Action != "upload_parse") {
		return "", errAttemptAuthorizationRevoked
	}
	return s.CreateOperationTicket(context.WithValue(ctx, admissionSessionContextKey{}, digest), u, scope)
}
