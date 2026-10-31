package store

import (
	"bytes"
	"context"
	"errors"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

// readUploadReceipt holds the case lock through current authorization and the
// receipt read, so a revoked member cannot recover a previously accepted upload.
func (s *Store) readUploadReceipt(ctx context.Context, user accounts.User, caseID, key string, digest [32]byte) (DocumentUploadResult, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DocumentUploadResult{}, false, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(user)); err != nil {
		return DocumentUploadResult{}, false, err
	}
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return DocumentUploadResult{}, false, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return DocumentUploadResult{}, false, err
	}
	if !accounts.CanUpload(current.Role) {
		return DocumentUploadResult{}, false, fault.Forbidden("当前角色不能执行文档操作")
	}
	item, err := loadKnowledgeCase(ctx, tx, current, caseID)
	if err != nil {
		return DocumentUploadResult{}, false, err
	}
	if err := requireCaseAction(item, current, access.Manage); err != nil {
		return DocumentUploadResult{}, false, err
	}
	if err := businessWritable(item); err != nil {
		return DocumentUploadResult{}, false, err
	}
	var priorHash []byte
	var version *int
	var result DocumentUploadResult
	err = tx.QueryRow(ctx, `SELECT i.request_hash,i.document_id,i.material_id,v.version
		FROM document_upload_idempotency i LEFT JOIN material_versions v
		ON v.tenant_id=i.tenant_id AND v.org_id=i.org_id AND v.case_id=i.case_id AND v.material_id=i.material_id AND v.document_id=i.document_id
		WHERE i.tenant_id=$1 AND i.org_id=$2 AND i.case_id=$3 AND i.actor_id=$4 AND i.idempotency_key=$5`,
		item.TenantID, item.OwnerOrgID, item.ID, current.ID, key).Scan(&priorHash, &result.DocumentID, &result.MaterialID, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return DocumentUploadResult{}, false, nil
	}
	if err != nil {
		return DocumentUploadResult{}, false, err
	}
	if !bytes.Equal(priorHash, digest[:]) {
		return DocumentUploadResult{}, false, fault.Conflict("Idempotency-Key已用于不同上传内容")
	}
	if version == nil {
		return DocumentUploadResult{}, false, errors.New("upload idempotency record has no material version")
	}
	result.Version, result.Replayed = *version, true
	if err := tx.Commit(ctx); err != nil {
		return DocumentUploadResult{}, false, err
	}
	return result, true, nil
}
