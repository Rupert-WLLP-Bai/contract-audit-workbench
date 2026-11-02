package store

import (
	"context"
	"errors"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// ExperienceDistillationStatus links an authorized task to its accepted draft.
// A completed task is never represented as an approved or published version.
type ExperienceDistillationStatus struct {
	JobID       string `json:"job_id"`
	FeedbackID  string `json:"feedback_id"`
	State       string `json:"state"`
	FailureCode string `json:"failure_code,omitempty"`
	CandidateID string `json:"candidate_id,omitempty"`
	VersionID   string `json:"version_id,omitempty"`
}

func (s *Store) GetExperienceDistillation(ctx context.Context, user accounts.User, caseID, feedbackID, jobID string) (ExperienceDistillationStatus, error) {
	ctx = commandContext(ctx, user)
	if !experienceIdentity(feedbackID) || !experienceIdentity(jobID) {
		return ExperienceDistillationStatus{}, fault.Invalid("经验提炼任务编号无效")
	}
	tx, current, item, err := s.beginExperienceCase(ctx, user, caseID, access.Read)
	if err != nil {
		return ExperienceDistillationStatus{}, err
	}
	defer tx.Rollback(context.Background())
	out := ExperienceDistillationStatus{JobID: jobID, FeedbackID: feedbackID}
	var candidateID, versionID, baseVersionID, failureCode *string
	var candidateCount int64
	err = tx.QueryRow(ctx, `SELECT j.state,c.candidate_id,c.version_id,c.base_version_id,f.error_code,COALESCE(c.total,0)
		FROM worker_jobs j
		JOIN experience_distill_inputs i ON i.job_id=j.id AND i.tenant_id=j.tenant_id AND i.org_id=j.org_id AND i.case_id=j.case_id
		LEFT JOIN LATERAL (
			SELECT v.candidate_id,v.version_id,v.base_version_id,count(*) OVER () AS total
			FROM experience_candidate_versions v
			WHERE v.tenant_id=i.tenant_id AND v.org_id=i.org_id AND v.case_id=i.case_id
				AND v.feedback_id=i.feedback_id AND v.job_id=j.id AND v.input_sha256=i.input_sha256
			ORDER BY v.generation DESC LIMIT 1
		) c ON true
		LEFT JOIN experience_distill_failures f ON f.job_id=j.id AND f.generation=j.generation
			AND f.tenant_id=j.tenant_id AND f.org_id=j.org_id AND f.case_id=j.case_id
		WHERE j.id=$1 AND j.kind='experience_distill' AND i.tenant_id=$2 AND i.org_id=$3 AND i.case_id=$4 AND i.feedback_id=$5`,
		jobID, item.TenantID, item.OwnerOrgID, item.ID, feedbackID).Scan(&out.State, &candidateID, &versionID, &baseVersionID, &failureCode, &candidateCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExperienceDistillationStatus{}, fault.NotFound("没有找到对应记录")
	}
	if err != nil {
		return ExperienceDistillationStatus{}, err
	}
	if candidateCount > 1 || (candidateID == nil) != (versionID == nil) {
		return ExperienceDistillationStatus{}, fault.Conflict("经验候选的编号不完整")
	}
	if candidateID != nil {
		base := ""
		if baseVersionID != nil {
			base = *baseVersionID
		}
		if err := authorizeExperienceAncestors(ctx, tx, current, item, *candidateID, base); err != nil {
			return ExperienceDistillationStatus{}, err
		}
		out.CandidateID, out.VersionID = *candidateID, *versionID
	}
	if failureCode != nil {
		out.FailureCode = *failureCode
	}
	if out.State == "completed" && out.CandidateID == "" {
		return ExperienceDistillationStatus{}, fault.Conflict("已完成的提炼任务缺少候选记录")
	}
	if _, err := trustedUser(current); err != nil {
		return ExperienceDistillationStatus{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExperienceDistillationStatus{}, err
	}
	return out, nil
}
