package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

// ExperienceUsageInput identifies one finding and one exact frozen experience.
// ExpectedAction is a reviewer's stated direction, not a model conclusion.
type ExperienceUsageInput struct {
	CaseID         string       `json:"case_id"`
	RunID          string       `json:"run_id"`
	AttemptID      string       `json:"attempt_id"`
	FindingID      string       `json:"finding_id"`
	Ref            KnowledgeRef `json:"ref"`
	ExpectedAction string       `json:"expected_action"`
}

type ExperienceUsage struct {
	TenantID       string       `json:"-"`
	OrgID          string       `json:"-"`
	CaseID         string       `json:"case_id"`
	RunID          string       `json:"run_id"`
	AttemptID      string       `json:"attempt_id"`
	FindingID      string       `json:"finding_id"`
	Ref            KnowledgeRef `json:"ref"`
	SnapshotSHA256 string       `json:"snapshot_sha256"`
	InputSHA256    string       `json:"input_sha256"`
	ExpectedAction string       `json:"expected_action"`
	RecordedBy     string       `json:"recorded_by"`
	UsageSHA256    string       `json:"usage_sha256"`
	RecordedAt     time.Time    `json:"recorded_at"`
}

type ExperienceUsageOption struct {
	Ref                   KnowledgeRef     `json:"ref"`
	Title                 string           `json:"title"`
	Body                  string           `json:"body"`
	Summary               string           `json:"summary"`
	CheckAction           string           `json:"check_action"`
	MinimumEvidence       string           `json:"minimum_evidence"`
	ReviewedApplicability []string         `json:"reviewed_applicability"`
	ReviewedExclusions    []string         `json:"reviewed_exclusions"`
	Counterexamples       []string         `json:"counterexamples"`
	Usage                 *ExperienceUsage `json:"usage"`
}

type ExperienceUsageOptions struct {
	Items     []ExperienceUsageOption `json:"items"`
	CanRecord bool                    `json:"can_record"`
}

type experienceUsageReview struct {
	Snapshot *ReviewKnowledgeSnapshot
	InputSHA string
	Latest   bool
	Terminal bool
	Decided  bool
}

func validExperienceUsageIdentity(input ExperienceUsageInput) bool {
	return experienceIdentity(input.CaseID) && experienceIdentity(input.RunID) &&
		experienceIdentity(input.AttemptID) && experienceIdentity(input.FindingID)
}

// The digest binds the actor's explicit statement to the exact immutable
// attempt input, knowledge version and finding. RecordedAt is database-owned.
func experienceUsageHash(usage ExperienceUsage) (string, error) {
	raw, err := json.Marshal(struct {
		Encoding       string       `json:"encoding"`
		TenantID       string       `json:"tenant_id"`
		OrgID          string       `json:"org_id"`
		CaseID         string       `json:"case_id"`
		RunID          string       `json:"run_id"`
		AttemptID      string       `json:"attempt_id"`
		FindingID      string       `json:"finding_id"`
		Ref            KnowledgeRef `json:"ref"`
		SnapshotSHA256 string       `json:"snapshot_sha256"`
		InputSHA256    string       `json:"input_sha256"`
		ExpectedAction string       `json:"expected_action"`
		RecordedBy     string       `json:"recorded_by"`
	}{"experience-finding-usage/v1", usage.TenantID, usage.OrgID, usage.CaseID, usage.RunID, usage.AttemptID, usage.FindingID,
		usage.Ref, usage.SnapshotSHA256, usage.InputSHA256, usage.ExpectedAction, usage.RecordedBy})
	if err != nil {
		return "", err
	}
	return sha256Bytes(raw), nil
}

func loadExperienceUsageReview(ctx context.Context, tx pgx.Tx, item Case, input ExperienceUsageInput) (experienceUsageReview, error) {
	var out experienceUsageReview
	var raw []byte
	var historyKind, state string
	err := tx.QueryRow(ctx, `SELECT s.governed_knowledge, a.input_hash, a.history_kind, a.state,
		(r.id=(SELECT rr.id FROM review_runs rr WHERE rr.tenant_id=r.tenant_id AND rr.org_id=r.org_id AND rr.case_id=r.case_id ORDER BY rr.created_at DESC,rr.seq DESC LIMIT 1)
		 AND a.ordinal=(SELECT max(aa.ordinal) FROM review_attempts aa WHERE aa.tenant_id=a.tenant_id AND aa.org_id=a.org_id AND aa.run_id=a.run_id)),
		EXISTS(SELECT 1 FROM decisions d WHERE d.tenant_id=f.tenant_id AND d.org_id=f.org_id AND d.case_id=f.case_id AND d.run_id=f.run_id AND d.finding_id=f.id)
		FROM review_runs r
		JOIN review_attempts a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id
		JOIN review_attempt_inputs i ON i.tenant_id=a.tenant_id AND i.org_id=a.org_id AND i.run_id=a.run_id AND i.attempt_id=a.id AND i.input_hash=a.input_hash
		JOIN review_run_snapshots s ON s.tenant_id=r.tenant_id AND s.org_id=r.org_id AND s.run_id=r.id
		JOIN findings f ON f.tenant_id=r.tenant_id AND f.org_id=r.org_id AND f.case_id=r.case_id AND f.run_id=r.id AND f.attempt_id=a.id
		WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 AND r.id=$4 AND a.id=$5 AND f.id=$6`,
		item.TenantID, item.OwnerOrgID, item.ID, input.RunID, input.AttemptID, input.FindingID).
		Scan(&raw, &out.InputSHA, &historyKind, &state, &out.Latest, &out.Decided)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound("没有找到对应的冻结审查发现")
	}
	if err != nil {
		return out, err
	}
	if historyKind != "frozen" || !validKnowledgeHash(out.InputSHA) {
		return out, fault.Conflict("该审查没有可核对的冻结输入")
	}
	switch state {
	case "completed", "partial", "failed", "cancelled", "interrupted":
		out.Terminal = true
	}
	out.Snapshot, err = decodeGovernedKnowledge(raw)
	if err != nil {
		return out, fault.Conflict("审查冻结知识记录的摘要无效")
	}
	if out.Snapshot != nil && (out.Snapshot.Scope.CaseID != item.ID || out.Snapshot.Scope.OrgID != item.OwnerOrgID) {
		return out, fault.Conflict("冻结知识记录与案件范围不一致")
	}
	return out, nil
}

func usageSelection(snapshot *ReviewKnowledgeSnapshot, ref KnowledgeRef) (GovernedKnowledgeSelection, bool) {
	if snapshot != nil {
		for _, selected := range snapshot.OptionalCandidates {
			if selected.Kind == "experience" && selected.Ref == ref {
				return selected, true
			}
		}
	}
	return GovernedKnowledgeSelection{}, false
}

func scanExperienceUsage(row pgx.Row, tenantID, orgID string) (ExperienceUsage, error) {
	usage := ExperienceUsage{TenantID: tenantID, OrgID: orgID}
	err := row.Scan(&usage.CaseID, &usage.RunID, &usage.AttemptID, &usage.FindingID,
		&usage.Ref.KnowledgeID, &usage.Ref.VersionID, &usage.Ref.ContentSHA256,
		&usage.SnapshotSHA256, &usage.InputSHA256, &usage.ExpectedAction, &usage.RecordedBy,
		&usage.UsageSHA256, &usage.RecordedAt)
	if err != nil {
		return ExperienceUsage{}, err
	}
	hash, err := experienceUsageHash(usage)
	if err != nil || hash != usage.UsageSHA256 {
		return ExperienceUsage{}, fault.Conflict("经验使用记录的冻结摘要无效")
	}
	return usage, nil
}

const experienceUsageColumns = `case_id,run_id,attempt_id,finding_id,knowledge_id,version_id,content_sha256,
 snapshot_sha256,input_sha256,expected_action,recorded_by,usage_sha256,recorded_at`

func (s *Store) experienceUsageReady(ctx context.Context) error {
	if ctx == nil || s == nil || s.pool == nil {
		return fault.New(503, "experience_unavailable", "经验使用记录不可用")
	}
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return fault.Forbidden("只有 Core 服务可以读取或登记经验使用")
	}
	return nil
}

// ListExperienceUsageOptions exposes only exact candidates from the stored
// snapshot, after checking current target, source and ancestor permissions.
func (s *Store) ListExperienceUsageOptions(ctx context.Context, user accounts.User, input ExperienceUsageInput) (ExperienceUsageOptions, error) {
	out := ExperienceUsageOptions{Items: []ExperienceUsageOption{}}
	if err := s.experienceUsageReady(ctx); err != nil {
		return out, err
	}
	if !validExperienceUsageIdentity(input) {
		return out, fault.Invalid("审查发现编号不完整")
	}
	current, err := trustedUser(user)
	if err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return out, err
	}
	item, err := loadKnowledgeCase(ctx, tx, current, input.CaseID)
	if err != nil {
		return out, err
	}
	review, err := loadExperienceUsageReview(ctx, tx, item, input)
	if err != nil {
		return out, err
	}
	out.CanRecord = !s.ReadOnly() && review.Latest && review.Terminal && !review.Decided &&
		accounts.CanDecide(current.Role) && authorizeCase(item, current, access.Decide) && businessWritable(item) == nil
	if review.Snapshot != nil {
		for _, candidate := range review.Snapshot.OptionalCandidates {
			if candidate.Kind != "experience" {
				continue
			}
			version, _, _, _, err := loadGovernedKnowledge(ctx, tx, item.TenantID, candidate.Ref.KnowledgeID, candidate.Ref.VersionID)
			if err != nil || version.Ref != candidate.Ref || version.Content.Kind != "experience" {
				return ExperienceUsageOptions{}, fault.Conflict("冻结经验版本无法验证")
			}
			if err := authorizeKnowledgeRead(ctx, tx, current, version.Content); err != nil {
				var problem *fault.Error
				if errors.As(err, &problem) && (problem.Status == 403 || problem.Status == 404) {
					continue
				}
				return ExperienceUsageOptions{}, err
			}
			stored, err := scanExperienceUsage(tx.QueryRow(ctx, `SELECT `+experienceUsageColumns+` FROM experience_finding_usage
				WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND run_id=$4 AND attempt_id=$5 AND finding_id=$6 AND knowledge_id=$7 AND version_id=$8`,
				item.TenantID, item.OwnerOrgID, item.ID, input.RunID, input.AttemptID, input.FindingID, candidate.Ref.KnowledgeID, candidate.Ref.VersionID), item.TenantID, item.OwnerOrgID)
			var usage *ExperienceUsage
			if err == nil {
				if stored.Ref != candidate.Ref || stored.SnapshotSHA256 != review.Snapshot.SnapshotSHA256 || stored.InputSHA256 != review.InputSHA {
					return ExperienceUsageOptions{}, fault.Conflict("经验使用记录与冻结审查不一致")
				}
				usage = &stored
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return ExperienceUsageOptions{}, err
			}
			if !version.Active {
				continue
			}
			out.Items = append(out.Items, ExperienceUsageOption{Ref: candidate.Ref, Title: version.Content.Title,
				Body: version.Content.Body, Summary: candidate.Summary, CheckAction: candidate.CheckAction,
				MinimumEvidence: candidate.MinimumEvidence, ReviewedApplicability: candidate.ReviewedApplicability,
				ReviewedExclusions: candidate.ReviewedExclusions, Counterexamples: candidate.Counterexamples, Usage: usage})
		}
	}
	if _, err := trustedUser(current); err != nil {
		return ExperienceUsageOptions{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExperienceUsageOptions{}, err
	}
	return out, nil
}

// RecordExperienceUsage records an explicit reference before any decision. A
// repeat of the same actor and statement returns the original immutable row.
func (s *Store) RecordExperienceUsage(ctx context.Context, user accounts.User, input ExperienceUsageInput) (ExperienceUsage, error) {
	if err := s.experienceUsageReady(ctx); err != nil {
		return ExperienceUsage{}, err
	}
	if s.ReadOnly() {
		return ExperienceUsage{}, fault.New(503, "read_only", "只读模式下不能登记经验使用")
	}
	if !validExperienceUsageIdentity(input) || !validResourceID(input.Ref.KnowledgeID) ||
		!validKnowledgeHash(input.Ref.VersionID) || !validKnowledgeHash(input.Ref.ContentSHA256) ||
		(input.ExpectedAction != "" && input.ExpectedAction != "confirm_risk" && input.ExpectedAction != "false_positive") {
		return ExperienceUsage{}, fault.Invalid("经验使用的审查、版本或预期方向无效")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ExperienceUsage{}, err
	}
	if !accounts.CanDecide(current.Role) {
		return ExperienceUsage{}, fault.Forbidden("当前角色不能登记经验使用")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExperienceUsage{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ExperienceUsage{}, err
	}
	// Preserve the established schema-then-case lock order for governance and
	// serialize with the first decision on this finding.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),0))`); err != nil {
		return ExperienceUsage{}, err
	}
	if err := scopedCaseLock(ctx, tx, input.CaseID); err != nil {
		return ExperienceUsage{}, err
	}
	item, err := loadKnowledgeCase(ctx, tx, current, input.CaseID)
	if err != nil {
		return ExperienceUsage{}, err
	}
	if !authorizeCase(item, current, access.Decide) {
		return ExperienceUsage{}, fault.Forbidden("当前授权不能登记经验使用")
	}
	review, err := loadExperienceUsageReview(ctx, tx, item, input)
	if err != nil {
		return ExperienceUsage{}, err
	}
	if _, found := usageSelection(review.Snapshot, input.Ref); !found {
		return ExperienceUsage{}, fault.NotFound("经验不在本次审查的冻结候选中")
	}
	version, _, _, _, err := loadGovernedKnowledge(ctx, tx, item.TenantID, input.Ref.KnowledgeID, input.Ref.VersionID)
	if err != nil || version.Ref != input.Ref || version.Content.Kind != "experience" {
		return ExperienceUsage{}, fault.Conflict("经验的精确版本或内容摘要不一致")
	}
	if err := authorizeKnowledgeRead(ctx, tx, current, version.Content); err != nil {
		return ExperienceUsage{}, err
	}
	usage := ExperienceUsage{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID, RunID: input.RunID, AttemptID: input.AttemptID,
		FindingID: input.FindingID, Ref: input.Ref, SnapshotSHA256: review.Snapshot.SnapshotSHA256,
		InputSHA256: review.InputSHA, ExpectedAction: input.ExpectedAction, RecordedBy: current.ID}
	usage.UsageSHA256, err = experienceUsageHash(usage)
	if err != nil {
		return ExperienceUsage{}, err
	}
	stored, err := scanExperienceUsage(tx.QueryRow(ctx, `SELECT `+experienceUsageColumns+` FROM experience_finding_usage
		WHERE tenant_id=$1 AND attempt_id=$2 AND knowledge_id=$3 AND version_id=$4 AND finding_id=$5`,
		item.TenantID, input.AttemptID, input.Ref.KnowledgeID, input.Ref.VersionID, input.FindingID), item.TenantID, item.OwnerOrgID)
	if err == nil {
		if stored.UsageSHA256 != usage.UsageSHA256 {
			return ExperienceUsage{}, fault.Conflict("该经验已按其他方向或由其他审查员登记")
		}
		if err := tx.Commit(ctx); err != nil {
			return ExperienceUsage{}, err
		}
		return stored, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ExperienceUsage{}, err
	}
	if !review.Latest || !review.Terminal || review.Decided {
		return ExperienceUsage{}, fault.Conflict("只能在最新终态审查的首次人工决定之前登记引用")
	}
	if err := businessWritable(item); err != nil {
		return ExperienceUsage{}, err
	}
	if !version.Active || version.Status != "published" || version.ApprovedDigest != input.Ref.ContentSHA256 {
		return ExperienceUsage{}, fault.Conflict("经验版本已撤回或不再生效")
	}
	stored, err = scanExperienceUsage(tx.QueryRow(ctx, `INSERT INTO experience_finding_usage
		(tenant_id,org_id,case_id,run_id,attempt_id,finding_id,knowledge_id,version_id,content_sha256,snapshot_sha256,input_sha256,expected_action,recorded_by,usage_sha256)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING `+experienceUsageColumns,
		item.TenantID, item.OwnerOrgID, usage.CaseID, usage.RunID, usage.AttemptID, usage.FindingID,
		usage.Ref.KnowledgeID, usage.Ref.VersionID, usage.Ref.ContentSHA256, usage.SnapshotSHA256,
		usage.InputSHA256, usage.ExpectedAction, usage.RecordedBy, usage.UsageSHA256), item.TenantID, item.OwnerOrgID)
	if err != nil {
		return ExperienceUsage{}, err
	}
	if _, err := trustedUser(current); err != nil || !authorizeCase(item, current, access.Decide) {
		return ExperienceUsage{}, fault.NotFound("没有找到对应记录")
	}
	if err := tx.Commit(ctx); err != nil {
		return ExperienceUsage{}, err
	}
	return stored, nil
}
