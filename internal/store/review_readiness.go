package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

const ReviewScopeFull = "full_review"

// ReviewReadiness is a read-only projection of the current case inputs. Its
// revision is a concurrency hint; callers must still use the normal acceptance
// endpoint, which re-resolves authorization and dependencies transactionally.
type ReviewReadiness struct {
	CaseID                   string                    `json:"case_id"`
	Revision                 string                    `json:"revision"`
	BusinessAt               *time.Time                `json:"business_at,omitempty"`
	CanStart                 bool                      `json:"can_start"`
	MaterialSelectionVersion int64                     `json:"material_selection_version"`
	Materials                []ReviewReadinessMaterial `json:"materials"`
	Scopes                   []ReviewReadinessScope    `json:"scopes"`
}

type ReviewReadinessMaterial struct {
	IdentityKind string               `json:"identity_kind"`
	MaterialID   string               `json:"material_id"`
	Version      int                  `json:"version"`
	DocumentID   string               `json:"document_id"`
	Filename     string               `json:"filename"`
	Role         string               `json:"role"`
	Purpose      string               `json:"purpose"`
	Selected     bool                 `json:"selected"`
	Parse        ReviewReadinessParse `json:"parse"`
}

type ReviewReadinessParse struct {
	Status           string `json:"status"`
	ParsedDocumentID string `json:"parsed_document_id,omitempty"`
	ParserID         string `json:"parser_id,omitempty"`
	ParserVersion    string `json:"parser_version,omitempty"`
	NextAction       string `json:"next_action"`
}

type ReviewReadinessScope struct {
	ID       string                   `json:"id"`
	Status   string                   `json:"status"`
	CanStart bool                     `json:"can_start"`
	RulePack *ReviewReadinessRulePack `json:"rule_pack,omitempty"`
	Checks   []ReviewReadinessCheck   `json:"checks"`
	Blockers []ReviewReadinessBlocker `json:"blockers"`
}

type ReviewReadinessRulePack struct {
	PackID         string `json:"pack_id"`
	Version        string `json:"version"`
	SnapshotDigest string `json:"snapshot_digest"`
}

type ReviewReadinessCheck struct {
	RuleID              string   `json:"rule_id"`
	Status              string   `json:"status"`
	MissingDependencies []string `json:"missing_dependencies"`
	NextAction          string   `json:"next_action"`
}

type ReviewReadinessBlocker struct {
	Code                string   `json:"code"`
	Status              string   `json:"status"`
	Message             string   `json:"message"`
	MissingDependencies []string `json:"missing_dependencies"`
	NextAction          string   `json:"next_action"`
}

type reviewReadinessInputs struct {
	CaseID                   string
	BusinessAt               time.Time
	MaterialSelectionVersion int64
	Materials                []ReviewReadinessMaterial
	CanRead                  bool
	CanStartRole             bool
	Writable                 bool
	SelectionConfirmed       bool
	MinimumMaterials         int
	BusinessAtProvided       bool
	RulePack                 *ReviewReadinessRulePack
	AcceptanceBlockers       []ReviewReadinessBlocker
	Checks                   []ReviewReadinessCheck
	RevisionInputs           any
}

// evaluateReviewReadiness is the shared pure gate used by both the readiness
// projection and the acceptance path. Per-rule missing evidence is reported
// as not_executed without rejecting the whole scope; admission blockers mirror
// conditions that prevent StartReview from creating an attempt.
func evaluateReviewReadiness(input reviewReadinessInputs) ReviewReadinessScope {
	blockers := append([]ReviewReadinessBlocker(nil), input.AcceptanceBlockers...)
	if !input.CanRead {
		blockers = append(blockers, readinessBlocker("case_read_required", "blocked", "需要案件读取权限", nil, "申请案件读取权限"))
	}
	if !input.CanStartRole {
		blockers = append(blockers, readinessBlocker("review_start_role_required", "blocked", "当前角色不能发起审查", nil, "请由有权限的经办人发起审查"))
	}
	if !input.Writable {
		blockers = append(blockers, readinessBlocker("case_not_writable", "blocked", "案件当前不可受理新审查", nil, "检查案件归档状态或服务维护状态"))
	}
	if !input.BusinessAtProvided {
		blockers = append(blockers, readinessBlocker("business_at_required", "missing", "需要先确定本次审查的业务时间", []string{"business_at"}, "填写合同履行或审查适用的业务时间"))
	}
	if !input.SelectionConfirmed {
		blockers = append(blockers, readinessBlocker("material_selection_pending", "blocked", "本次审查材料集合尚未确认", nil, "确认本次审查要使用的材料"))
	}
	if input.MinimumMaterials < 1 {
		input.MinimumMaterials = 2
	}
	if len(input.Materials) < input.MinimumMaterials {
		blockers = append(blockers, readinessBlocker("required_materials_missing", "missing", "当前完整审查需要更多材料", []string{"review_materials"}, "补充所需材料后重新检查准备状态"))
	}

	checks := append([]ReviewReadinessCheck(nil), input.Checks...)
	sort.Slice(checks, func(i, j int) bool { return checks[i].RuleID < checks[j].RuleID })
	for i := range checks {
		sort.Strings(checks[i].MissingDependencies)
	}
	sort.Slice(blockers, func(i, j int) bool {
		if blockers[i].Code != blockers[j].Code {
			return blockers[i].Code < blockers[j].Code
		}
		return blockers[i].Message < blockers[j].Message
	})
	canStart := len(blockers) == 0
	status := "blocked"
	if canStart {
		status = "ready"
	}
	if input.Materials == nil {
		input.Materials = []ReviewReadinessMaterial{}
	}
	if checks == nil {
		checks = []ReviewReadinessCheck{}
	}
	if blockers == nil {
		blockers = []ReviewReadinessBlocker{}
	}
	return ReviewReadinessScope{ID: ReviewScopeFull, Status: status, CanStart: canStart,
		RulePack: input.RulePack, Checks: checks, Blockers: blockers}
}

func readinessBlocker(code, status, message string, missing []string, next string) ReviewReadinessBlocker {
	copyMissing := append([]string(nil), missing...)
	sort.Strings(copyMissing)
	return ReviewReadinessBlocker{Code: code, Status: status, Message: message,
		MissingDependencies: copyMissing, NextAction: next}
}

type reviewReadinessRevisionInput struct {
	CaseID                   string                    `json:"case_id"`
	BusinessAt               time.Time                 `json:"business_at"`
	CaseUpdatedAt            string                    `json:"case_updated_at"`
	MaterialSelectionVersion int64                     `json:"material_selection_version"`
	Materials                []ReviewReadinessMaterial `json:"materials"`
	Scope                    ReviewReadinessScope      `json:"scope"`
	Additional               any                       `json:"additional,omitempty"`
}

func reviewReadinessRevision(input reviewReadinessRevisionInput) (string, error) {
	materials := append([]ReviewReadinessMaterial(nil), input.Materials...)
	sort.Slice(materials, func(i, j int) bool {
		if materials[i].MaterialID != materials[j].MaterialID {
			return materials[i].MaterialID < materials[j].MaterialID
		}
		if materials[i].Version != materials[j].Version {
			return materials[i].Version < materials[j].Version
		}
		return materials[i].DocumentID < materials[j].DocumentID
	})
	input.Materials = materials
	input.BusinessAt = input.BusinessAt.UTC()
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
