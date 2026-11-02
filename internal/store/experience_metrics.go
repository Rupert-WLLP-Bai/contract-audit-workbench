package store

import (
	"context"
	"encoding/json"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"github.com/jackc/pgx/v5"
)

const experienceMetricsSampleLimit = 20

type ExperienceMetricsSample struct {
	CaseID              string    `json:"case_id"`
	RunID               string    `json:"run_id"`
	AttemptID           string    `json:"attempt_id"`
	FindingID           string    `json:"finding_id"`
	InputSHA256         string    `json:"input_sha256"`
	SnapshotSHA256      string    `json:"snapshot_sha256"`
	UsageSHA256         string    `json:"usage_sha256"`
	ExpectedAction      string    `json:"expected_action"`
	DecisionID          string    `json:"decision_id"`
	DecisionAction      string    `json:"decision_action"`
	DecisionDisposition string    `json:"decision_disposition"`
	Outcome             string    `json:"outcome"`
	UsedAt              time.Time `json:"used_at"`
}

type ExperienceMetrics struct {
	Ref              KnowledgeRef              `json:"ref"`
	HasBeenPublished bool                      `json:"has_been_published"`
	UsageCount       int                       `json:"usage_count"`
	PendingCount     int                       `json:"pending_count"`
	AdoptedCount     int                       `json:"adopted_count"`
	OverturnedCount  int                       `json:"overturned_count"`
	OtherCount       int                       `json:"other_count"`
	Samples          []ExperienceMetricsSample `json:"samples"`
}

// ExperienceMetricsForVersion never aggregates across version or content
// digests. The source and all its ancestors are authorized first; each target
// case is checked before its record contributes to either counts or samples.
func (s *Store) ExperienceMetricsForVersion(ctx context.Context, user accounts.User, knowledgeID, versionID string) (ExperienceMetrics, error) {
	ctx = commandContext(ctx, user)
	out := ExperienceMetrics{Samples: []ExperienceMetricsSample{}}
	if err := s.experienceUsageReady(ctx); err != nil {
		return out, err
	}
	admin, err := trustedAdmin(user)
	if err != nil {
		return out, err
	}
	if !validResourceID(knowledgeID) || !validKnowledgeHash(versionID) {
		return out, fault.NotFound("没有找到对应经验版本")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(admin)); err != nil {
		return out, err
	}
	tenant := accounts.Tenant(admin)
	version, _, _, _, err := loadGovernedKnowledge(ctx, tx, tenant, knowledgeID, versionID)
	if err != nil {
		return out, err
	}
	if version.Content.Kind != "experience" {
		return out, fault.NotFound("没有找到对应经验版本")
	}
	if err := authorizeKnowledgeReadWithCaseLocks(ctx, tx, admin, version.Content, false); err != nil {
		return out, err
	}
	out.Ref = version.Ref
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM governed_knowledge_history
		WHERE tenant_id=$1 AND knowledge_id=$2 AND
		((version_id=$3 AND action='publish') OR (action='rollback' AND resulting_version_id=$3 AND resulting_active)))`,
		tenant, knowledgeID, versionID).Scan(&out.HasBeenPublished); err != nil {
		return ExperienceMetrics{}, err
	}
	rows, err := tx.Query(ctx, `SELECT u.org_id,u.case_id,u.run_id,u.attempt_id,u.finding_id,
		u.knowledge_id,u.version_id,u.content_sha256,u.snapshot_sha256,u.input_sha256,
		u.expected_action,u.recorded_by,u.usage_sha256,u.recorded_at,c.owner_id,c.member_ids,
		COALESCE(d.id,''),COALESCE(d.action,''),COALESCE(d.new_disposition,'')
		FROM experience_finding_usage u
		JOIN cases c ON c.tenant_id=u.tenant_id AND c.org_id=u.org_id AND c.id=u.case_id
		LEFT JOIN LATERAL (SELECT id,action,new_disposition FROM decisions
			WHERE tenant_id=u.tenant_id AND org_id=u.org_id AND case_id=u.case_id AND run_id=u.run_id AND finding_id=u.finding_id
			ORDER BY seq DESC LIMIT 1) d ON true
		WHERE u.tenant_id=$1 AND u.knowledge_id=$2 AND u.version_id=$3
		ORDER BY u.recorded_at DESC,u.attempt_id,u.finding_id`, tenant, knowledgeID, versionID)
	if err != nil {
		return ExperienceMetrics{}, err
	}
	for rows.Next() {
		usage := ExperienceUsage{TenantID: tenant}
		item := Case{TenantID: tenant}
		var memberJSON string
		var decisionID, action, disposition string
		if err := rows.Scan(&usage.OrgID, &usage.CaseID, &usage.RunID, &usage.AttemptID, &usage.FindingID,
			&usage.Ref.KnowledgeID, &usage.Ref.VersionID, &usage.Ref.ContentSHA256, &usage.SnapshotSHA256,
			&usage.InputSHA256, &usage.ExpectedAction, &usage.RecordedBy, &usage.UsageSHA256, &usage.RecordedAt,
			&item.OwnerID, &memberJSON, &decisionID, &action, &disposition); err != nil {
			rows.Close()
			return ExperienceMetrics{}, err
		}
		item.ID, item.OwnerOrgID = usage.CaseID, usage.OrgID
		if err := json.Unmarshal([]byte(memberJSON), &item.MemberIDs); err != nil {
			rows.Close()
			return ExperienceMetrics{}, err
		}
		if !authorizeCase(item, admin, access.Read) {
			continue
		}
		hash, err := experienceUsageHash(usage)
		if err != nil || hash != usage.UsageSHA256 || usage.Ref != version.Ref {
			rows.Close()
			return ExperienceMetrics{}, fault.Conflict("经验使用记录的冻结摘要无效")
		}
		outcome := experienceDecisionOutcome(usage.ExpectedAction, decisionID, action, disposition)
		out.UsageCount++
		switch outcome {
		case "pending":
			out.PendingCount++
		case "adopted":
			out.AdoptedCount++
		case "overturned":
			out.OverturnedCount++
		default:
			out.OtherCount++
		}
		if len(out.Samples) < experienceMetricsSampleLimit {
			out.Samples = append(out.Samples, ExperienceMetricsSample{CaseID: usage.CaseID, RunID: usage.RunID,
				AttemptID: usage.AttemptID, FindingID: usage.FindingID, InputSHA256: usage.InputSHA256,
				SnapshotSHA256: usage.SnapshotSHA256, UsageSHA256: usage.UsageSHA256,
				ExpectedAction: usage.ExpectedAction, DecisionID: decisionID, DecisionAction: action,
				DecisionDisposition: disposition, Outcome: outcome, UsedAt: usage.RecordedAt})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ExperienceMetrics{}, err
	}
	if _, err := trustedAdmin(admin); err != nil {
		return ExperienceMetrics{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExperienceMetrics{}, err
	}
	return out, nil
}

// experienceDecisionOutcome compares a recorded experience direction with the
// latest explicit human decision. A procedural reference has no direction and
// cannot be classified as adopted or overturned. Decisions about insufficient
// evidence or residual risk are retained separately rather than treated as a
// binary result.
func experienceDecisionOutcome(expectedAction, decisionID, action, disposition string) string {
	if decisionID == "" {
		return "pending"
	}
	actual, known := decisionActions[action]
	if !known || actual != disposition || (expectedAction != "confirm_risk" && expectedAction != "false_positive") {
		return "other"
	}
	if action == expectedAction {
		return "adopted"
	}
	if action == "confirm_risk" || action == "false_positive" {
		return "overturned"
	}
	return "other"
}
