package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/docversion"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

// ManualFinding is a human-authored, append-only observation. It is separate
// from a frozen attempt's machine findings and from human decision history.
type ManualFinding struct {
	ID        string                `json:"id"`
	CaseID    string                `json:"case_id"`
	RunID     string                `json:"run_id"`
	AttemptID string                `json:"attempt_id"`
	ActorID   string                `json:"actor_id"`
	ActorName string                `json:"actor_name"`
	Claim     string                `json:"claim"`
	Severity  string                `json:"severity"`
	Evidence  ExperienceEvidenceRef `json:"evidence"`
	CreatedAt time.Time             `json:"created_at"`
}

type ManualFindingInput struct {
	CaseID         string                `json:"case_id"`
	RunID          string                `json:"run_id"`
	AttemptID      string                `json:"attempt_id"`
	Claim          string                `json:"claim"`
	Severity       string                `json:"severity"`
	Evidence       ExperienceEvidenceRef `json:"evidence"`
	IdempotencyKey string                `json:"idempotency_key"`
}

func validateManualFindingInput(input *ManualFindingInput) error {
	input.Claim = strings.TrimSpace(input.Claim)
	ref := input.Evidence
	if !experienceIdentity(input.CaseID) || !experienceIdentity(input.RunID) || !experienceIdentity(input.AttemptID) ||
		!experienceIdentity(input.IdempotencyKey) || len(input.IdempotencyKey) > 80 ||
		!experienceIdentity(ref.DocumentID) || !experienceIdentity(ref.ParsedDocumentID) ||
		runeCount(input.Claim) < 2 || runeCount(input.Claim) > 1000 ||
		ref.Quote == "" || runeCount(ref.Quote) > 4096 || ref.Start < 0 || ref.End <= ref.Start || runeCount(ref.Quote) != ref.End-ref.Start {
		return fault.Invalid("请填写人工发现，并选择本次执行中的有效原文")
	}
	switch input.Severity {
	case "high", "medium", "low", "info":
	default:
		return fault.Invalid("请选择有效的严重程度")
	}
	if ref.NormalizedDigest != "" {
		if _, err := docversion.ParseDigest(ref.NormalizedDigest); err != nil {
			return fault.Invalid("原文摘要无效")
		}
	}
	return nil
}

const manualFindingColumns = `id,case_id,run_id,attempt_id,actor_id,actor_name,claim,severity,
 document_id,parsed_document_id,normalized_digest,start_offset,end_offset,quote,created_at,request_sha256`

func scanManualFinding(row pgx.Row) (ManualFinding, string, error) {
	var item ManualFinding
	var hash string
	err := row.Scan(&item.ID, &item.CaseID, &item.RunID, &item.AttemptID, &item.ActorID, &item.ActorName,
		&item.Claim, &item.Severity, &item.Evidence.DocumentID, &item.Evidence.ParsedDocumentID,
		&item.Evidence.NormalizedDigest, &item.Evidence.Start, &item.Evidence.End, &item.Evidence.Quote, &item.CreatedAt, &hash)
	return item, hash, err
}

func manualFindingReplay(ctx context.Context, tx pgx.Tx, tenantID, actorID, key, requestHash string) (ManualFinding, bool, error) {
	item, existingHash, err := scanManualFinding(tx.QueryRow(ctx, `SELECT `+manualFindingColumns+` FROM manual_finding_records
		WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3`, tenantID, actorID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return ManualFinding{}, false, nil
	}
	if err != nil {
		return ManualFinding{}, false, err
	}
	if existingHash != requestHash {
		return ManualFinding{}, false, fault.Conflict("这个请求标识已用于另一条人工发现")
	}
	return item, true, nil
}

// CreateManualFinding verifies the exact attempt's input and parse selection
// inside the case authorization transaction, then appends one record.
func (s *Store) CreateManualFinding(ctx context.Context, user accounts.User, input ManualFindingInput) (ManualFinding, error) {
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return ManualFinding{}, fault.Forbidden("当前服务不能记录人工发现")
	}
	current, err := s.remediationDecisionUser(user, "只读模式下不能记录人工发现", "当前角色不能记录人工发现")
	if err != nil {
		return ManualFinding{}, err
	}
	if !experienceIdentity(input.CaseID) || !experienceIdentity(input.IdempotencyKey) || len(input.IdempotencyKey) > 80 {
		return ManualFinding{}, fault.Invalid("人工发现缺少有效的案件或请求标识")
	}
	input.Claim = strings.TrimSpace(input.Claim)
	raw, err := json.Marshal(struct {
		Schema string             `json:"schema"`
		Input  ManualFindingInput `json:"input"`
	}{Schema: "manual-finding-request/v1", Input: input})
	if err != nil {
		return ManualFinding{}, err
	}
	requestHash := sha256Bytes(raw)
	tx, current, item, err := s.beginExperienceCase(ctx, current, input.CaseID, access.Decide)
	if err != nil {
		return ManualFinding{}, err
	}
	defer tx.Rollback(context.Background())
	if err := businessWritable(item); err != nil {
		return ManualFinding{}, err
	}
	stored, found, err := manualFindingReplay(ctx, tx, item.TenantID, current.ID, input.IdempotencyKey, requestHash)
	if err != nil {
		return ManualFinding{}, err
	}
	if found {
		fresh, err := trustedUser(current)
		if err != nil || !accounts.CanDecide(fresh.Role) || !authorizeCase(item, fresh, access.Decide) {
			return ManualFinding{}, fault.NotFound("当前授权不能读取人工发现")
		}
		if err := tx.Commit(ctx); err != nil {
			return ManualFinding{}, err
		}
		return stored, nil
	}
	if err := validateManualFindingInput(&input); err != nil {
		return ManualFinding{}, err
	}
	var frozenJSON string
	err = tx.QueryRow(ctx, `SELECT i.documents FROM review_runs r
		JOIN review_attempts a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id
		JOIN review_attempt_inputs i ON i.tenant_id=a.tenant_id AND i.org_id=a.org_id AND i.run_id=r.id AND i.attempt_id=a.id AND i.input_hash=a.input_hash
		WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 AND r.id=$4 AND a.id=$5
		AND a.history_kind='frozen' AND a.input_hash IS NOT NULL AND a.input_hash<>''`,
		item.TenantID, item.OwnerOrgID, item.ID, input.RunID, input.AttemptID).Scan(&frozenJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManualFinding{}, fault.NotFound("没有找到对应的冻结执行记录")
	}
	if err != nil {
		return ManualFinding{}, err
	}
	var frozen []FrozenDocument
	if err := json.Unmarshal([]byte(frozenJSON), &frozen); err != nil {
		return ManualFinding{}, err
	}
	ref := input.Evidence
	if ref.NormalizedDigest == "" {
		for _, source := range frozen {
			if source.Document.ID == ref.DocumentID && source.ParsedDocumentID == ref.ParsedDocumentID && source.NormalizedHash != nil {
				ref.NormalizedDigest = *source.NormalizedHash
				break
			}
		}
	}
	if err := verifyFeedbackEvidence(ctx, tx, item, input.RunID, input.AttemptID, frozen, ref); err != nil {
		return ManualFinding{}, err
	}
	id, err := newID("manual_finding_")
	if err != nil {
		return ManualFinding{}, err
	}
	actorName := strings.TrimSpace(current.DisplayName)
	if actorName == "" {
		actorName = current.ID
	}
	stored, _, err = scanManualFinding(tx.QueryRow(ctx, `INSERT INTO manual_finding_records
		(tenant_id,org_id,case_id,run_id,attempt_id,id,actor_id,actor_name,claim,severity,document_id,parsed_document_id,normalized_digest,start_offset,end_offset,quote,idempotency_key,request_sha256)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (tenant_id,actor_id,idempotency_key) DO NOTHING RETURNING `+manualFindingColumns,
		item.TenantID, item.OwnerOrgID, item.ID, input.RunID, input.AttemptID, id, current.ID, actorName,
		input.Claim, input.Severity, ref.DocumentID, ref.ParsedDocumentID, ref.NormalizedDigest, ref.Start, ref.End, ref.Quote, input.IdempotencyKey, requestHash))
	if errors.Is(err, pgx.ErrNoRows) {
		stored, found, err = manualFindingReplay(ctx, tx, item.TenantID, current.ID, input.IdempotencyKey, requestHash)
		if err == nil && !found {
			return ManualFinding{}, fault.Conflict("无法确认人工发现的保存结果")
		}
	}
	if err != nil {
		return ManualFinding{}, err
	}
	fresh, err := trustedUser(current)
	if err != nil || !accounts.CanDecide(fresh.Role) || !authorizeCase(item, fresh, access.Decide) {
		return ManualFinding{}, fault.NotFound("当前授权不能记录人工发现")
	}
	if err := tx.Commit(ctx); err != nil {
		return ManualFinding{}, err
	}
	return stored, nil
}

// ListManualFindings returns only records from the exact requested attempt and
// rechecks current case-read permission; possession of an ID grants no access.
func (s *Store) ListManualFindings(ctx context.Context, user accounts.User, caseID, runID, attemptID string) ([]ManualFinding, error) {
	if !experienceIdentity(runID) || !experienceIdentity(attemptID) {
		return nil, fault.NotFound("没有找到对应执行记录")
	}
	tx, current, item, err := s.beginExperienceCase(ctx, user, caseID, access.Read)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM review_runs r JOIN review_attempts a
		ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id
		WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 AND r.id=$4 AND a.id=$5)`,
		item.TenantID, item.OwnerOrgID, caseID, runID, attemptID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, fault.NotFound("没有找到对应执行记录")
	}
	rows, err := tx.Query(ctx, `SELECT `+manualFindingColumns+` FROM manual_finding_records
		WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND run_id=$4 AND attempt_id=$5 ORDER BY created_at,id`,
		item.TenantID, item.OwnerOrgID, caseID, runID, attemptID)
	if err != nil {
		return nil, err
	}
	result := make([]ManualFinding, 0)
	for rows.Next() {
		finding, _, err := scanManualFinding(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, finding)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if !authorizeCase(item, current, access.Read) {
		return nil, fault.NotFound("没有找到对应记录")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
