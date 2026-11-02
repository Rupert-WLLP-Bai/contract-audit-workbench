package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// DocumentIntakeAuthority is the minimal Core projection Document needs after
// redeeming a short-lived, opaque intake ticket over its authenticated Core
// connection. It is never constructed from request fields.
type DocumentIntakeAuthority struct {
	ActorID      string         `json:"actor_id"`
	TenantID     string         `json:"tenant_id"`
	OrgID        string         `json:"org_id"`
	Role         string         `json:"role"`
	Issuer       string         `json:"issuer,omitempty"`
	Subject      string         `json:"subject,omitempty"`
	PrincipalID  string         `json:"principal_id,omitempty"`
	MembershipID string         `json:"membership_id,omitempty"`
	Grants       []access.Grant `json:"grants"`
	CaseID       string         `json:"case_id"`
	OperationID  string         `json:"operation_id"`
	UploadKey    string         `json:"upload_key"`
	Filename     string         `json:"filename"`
	MediaType    string         `json:"media_type"`
	BodySHA256   string         `json:"body_sha256"`
	Action       string         `json:"action"`
}

// CreateDocumentIntakeTicket binds one just-created intake operation to the
// presented current session and exact upload bytes. The raw ticket is returned
// once; only its SHA-256 digest is persisted.
func (s *Store) CreateDocumentIntakeTicket(ctx context.Context, user accounts.User, sessionToken string, operationID, caseID, uploadKey string, body []byte) (string, error) {
	ctx = commandContext(ctx, user)
	current, err := trustedUser(user)
	if err != nil || sessionToken == "" || operationID == "" || caseID == "" || uploadKey == "" {
		return "", fault.Unauthenticated("建案授权已失效")
	}
	if !accounts.CanCreate(current.Role) || !accounts.CanUpload(current.Role) {
		return "", fault.Forbidden("当前角色不能上传合同")
	}
	ticket, err := randomIdentityToken("iat_")
	if err != nil {
		return "", err
	}
	ticketDigest := identityTokenDigest(ticket)
	bodyHash := caseIntakeDigest(body)
	sessionDigest := sessionTokenDigest(sessionToken)
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := setLocalSessionScope(ctx, tx, sessionDigest); err != nil {
		return "", err
	}
	readSession := func() (session, error) {
		var item session
		err := tx.QueryRow(ctx, `SELECT token,actor_id,issuer,subject,tenant_id,org_id,audience,expires_at,principal_id,membership_id
			FROM sessions WHERE token=$1`, sessionDigest).Scan(&item.Token, &item.ActorID, &item.Issuer, &item.Subject, &item.TenantID, &item.OrgID, &item.Audience, &item.ExpiresAt, &item.PrincipalID, &item.MembershipID)
		return item, err
	}
	storedSession, err := readSession()
	if err != nil {
		return "", fault.Unauthenticated("建案授权已失效")
	}
	if storedSession.PrincipalID != nil || storedSession.MembershipID != nil {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, s.schema+":tenant-identity"); err != nil {
			return "", err
		}
		storedSession, err = readSession()
		if err != nil || storedSession.PrincipalID == nil || storedSession.MembershipID == nil {
			return "", fault.Unauthenticated("建案授权已失效")
		}
	}
	if storedSession.Audience != "client" || storedSession.ActorID != current.ID || storedSession.TenantID != accounts.Tenant(current) ||
		storedSession.OrgID != current.OrgID || !timeTextAfterNow(storedSession.ExpiresAt) {
		return "", fault.Unauthenticated("建案授权已失效")
	}
	if storedSession.PrincipalID != nil {
		var principalID, actorID, issuer, subject, audience, membershipID, tenantID, tenantName, orgID, orgName, role, expiresAt string
		var jurisdictionID *string
		var grantsJSON []byte
		err := tx.QueryRow(ctx, `SELECT principal_id,actor_id,issuer,subject,audience,membership_id,tenant_id,tenant_name,jurisdiction_id,
			org_unit_id,org_unit_name,role_code,grants,expires_at FROM tenant_identity_session_user($1)`, sessionDigest).Scan(
			&principalID, &actorID, &issuer, &subject, &audience, &membershipID, &tenantID, &tenantName, &jurisdictionID, &orgID, &orgName, &role, &grantsJSON, &expiresAt)
		if err != nil || storedSession.PrincipalID == nil || storedSession.MembershipID == nil ||
			principalID != *storedSession.PrincipalID || membershipID != *storedSession.MembershipID || actorID != current.ID ||
			tenantID != accounts.Tenant(current) || orgID != current.OrgID || audience != "client" {
			return "", fault.Unauthenticated("建案授权已失效")
		}
		profile, ok := accounts.ByID(actorID)
		if !ok || !profile.Enabled || profile.Issuer != issuer || profile.Subject != subject {
			return "", fault.Unauthenticated("建案授权已失效")
		}
		var grants []access.Grant
		if json.Unmarshal(grantsJSON, &grants) != nil {
			return "", fault.Unauthenticated("建案授权已失效")
		}
		current = accounts.MembershipUser(actorID, profile.DisplayName, role, tenantID, orgID, issuer, subject, grants)
		current.PrincipalID, current.MembershipID = principalID, membershipID
	} else {
		profile, ok := accounts.ByID(storedSession.ActorID)
		if !ok || !profile.Enabled || profile.Issuer != "" || profile.Subject != "" ||
			profile.TenantID != storedSession.TenantID || profile.OrgID != storedSession.OrgID {
			return "", fault.Unauthenticated("建案授权已失效")
		}
		current = profile
	}
	if !accounts.CanCreate(current.Role) || !accounts.CanUpload(current.Role) {
		return "", fault.Forbidden("当前角色不能上传合同")
	}
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return "", err
	}
	var status, actualUploadKey, members string
	var requestJSON []byte
	var item Case
	var archivedAt *string
	err = tx.QueryRow(ctx, `SELECT operation.status,operation.upload_idempotency_key,request.request_json,
		case_row.id,case_row.tenant_id,case_row.org_id,case_row.owner_id,case_row.member_ids::text,case_row.business_status,case_row.archived_at::text,case_row.title
		FROM case_intake_operations AS operation
	JOIN case_intake_requests AS request ON request.tenant_id=operation.tenant_id AND request.operation_id=operation.operation_id
	JOIN cases AS case_row ON case_row.tenant_id=operation.tenant_id AND case_row.org_id=operation.org_id AND case_row.id=operation.case_id
	WHERE operation.tenant_id=$1 AND operation.actor_id=$2 AND operation.operation_id=$3 AND operation.case_id=$4`,
		accounts.Tenant(current), current.ID, operationID, caseID).Scan(&status, &actualUploadKey, &requestJSON,
		&item.ID, &item.TenantID, &item.OwnerOrgID, &item.OwnerID, &members, &item.BusinessStatus, &archivedAt, &item.Title)
	if err != nil || (status != caseIntakePendingStatus && status != caseIntakeCompletedStatus) || actualUploadKey != uploadKey || archivedAt != nil {
		return "", fault.NotFound("建案操作不存在")
	}
	if json.Unmarshal([]byte(members), &item.MemberIDs) != nil {
		return "", fault.NotFound("建案操作不存在")
	}
	var identity CaseIntakeRequestIdentity
	if json.Unmarshal(requestJSON, &identity) != nil || identity.FileSHA256 != bodyHash || !authorizeCase(item, current, access.Manage) {
		return "", fault.Forbidden("当前身份无权继续此建案上传")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO intake_authority_tickets
		(ticket_digest,tenant_id,operation_id,session_digest,principal_id,membership_id,actor_id,org_id,case_id,upload_key,filename,media_type,body_sha256,action,expires_at,context_version)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'create_primary_contract',clock_timestamp()+interval '2 minutes',$14)`,
		ticketDigest, accounts.Tenant(current), operationID, sessionDigest, storedSession.PrincipalID, storedSession.MembershipID,
		current.ID, current.OrgID, caseID, uploadKey, identity.FileName, identity.MediaType, bodyHash, func() int64 {
			p, _ := current.CommandAuthority()
			v, _ := strconv.ParseInt(p.ContextVersion, 10, 64)
			return v
		}()); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return ticket, nil
}

// ResolveDocumentIntakeTicket accepts one exact opaque ticket and current file
// digest, re-resolves its session and case authorization, then returns only the
// projection needed by the Document service.
func (s *Store) ResolveDocumentIntakeTicket(ctx context.Context, ticket, bodySHA256 string) (DocumentIntakeAuthority, error) {
	digest := identityTokenDigest(ticket)
	if digest == "" || len(bodySHA256) != 64 {
		return DocumentIntakeAuthority{}, fault.Unauthenticated("建案授权已失效")
	}
	var authority DocumentIntakeAuthority
	var sessionDigest, tenantID, action string
	var principalID, membershipID *string
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx, `SELECT tenant_id,operation_id,session_digest,principal_id,membership_id,actor_id,org_id,case_id,upload_key,filename,media_type,body_sha256,action,expires_at
		FROM tenant_identity_intake_ticket($1)`, digest).Scan(&tenantID, &authority.OperationID, &sessionDigest, &principalID, &membershipID,
		&authority.ActorID, &authority.OrgID, &authority.CaseID, &authority.UploadKey, &authority.Filename, &authority.MediaType, &authority.BodySHA256, &action, &expiresAt)
	if err != nil || action != "create_primary_contract" || authority.BodySHA256 != bodySHA256 || !expiresAt.After(time.Now()) {
		return DocumentIntakeAuthority{}, fault.Unauthenticated("建案授权已失效")
	}
	_, user, err := s.sessionFromDigest(ctx, sessionDigest)
	if err != nil || user.ID != authority.ActorID || accounts.Tenant(user) != tenantID || user.OrgID != authority.OrgID ||
		user.PrincipalID != stringOrEmpty(principalID) || user.MembershipID != stringOrEmpty(membershipID) ||
		!accounts.CanCreate(user.Role) || !accounts.CanUpload(user.Role) {
		return DocumentIntakeAuthority{}, fault.Unauthenticated("建案授权已失效")
	}
	if err := s.authorizeCurrentIntakeTicketCase(ctx, user, authority); err != nil {
		return DocumentIntakeAuthority{}, err
	}
	var captured *int64
	if err = s.pool.QueryRow(ctx, `SELECT context_version FROM intake_authority_tickets WHERE ticket_digest=$1`, identityTokenDigest(ticket)).Scan(&captured); err != nil || captured == nil {
		return DocumentIntakeAuthority{}, fault.Forbidden("建案上传上下文不可用")
	}
	proof, ok := user.CommandAuthority()
	if !ok || strconv.FormatInt(*captured, 10) != proof.ContextVersion {
		return DocumentIntakeAuthority{}, fault.New(409, "context_stale", "建案上传上下文已变更")
	}
	authority.TenantID, authority.Role, authority.Issuer, authority.Subject = tenantID, user.Role, user.Issuer, user.Subject
	authority.PrincipalID, authority.MembershipID = user.PrincipalID, user.MembershipID
	authority.Grants, authority.Action = user.Grants, action
	return authority, nil
}

func (s *Store) authorizeCurrentIntakeTicketCase(ctx context.Context, user accounts.User, authority DocumentIntakeAuthority) error {
	ctx = commandContext(ctx, user)
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(user)); err != nil {
		return err
	}
	var item Case
	var members string
	var archivedAt *string
	var status, uploadKey string
	err = tx.QueryRow(ctx, `SELECT operation.status,operation.upload_idempotency_key,
		case_row.id,case_row.tenant_id,case_row.org_id,case_row.owner_id,case_row.member_ids::text,case_row.business_status,case_row.archived_at::text,case_row.title
		FROM case_intake_operations AS operation
	JOIN cases AS case_row ON case_row.tenant_id=operation.tenant_id AND case_row.org_id=operation.org_id AND case_row.id=operation.case_id
	WHERE operation.tenant_id=$1 AND operation.actor_id=$2 AND operation.operation_id=$3 AND operation.case_id=$4`,
		accounts.Tenant(user), user.ID, authority.OperationID, authority.CaseID).Scan(&status, &uploadKey, &item.ID, &item.TenantID, &item.OwnerOrgID,
		&item.OwnerID, &members, &item.BusinessStatus, &archivedAt, &item.Title)
	if err != nil || (status != caseIntakePendingStatus && status != caseIntakeCompletedStatus) || uploadKey != authority.UploadKey || archivedAt != nil ||
		json.Unmarshal([]byte(members), &item.MemberIDs) != nil || !authorizeCase(item, user, access.Manage) {
		return fault.Forbidden("当前身份无权继续此建案上传")
	}
	return tx.Commit(ctx)
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func timeTextAfterNow(value string) bool {
	timestamp, err := time.Parse(timeLayout, value)
	return err == nil && timestamp.After(time.Now().UTC())
}
