package app

import (
	"context"
	"time"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

const (
	defaultMyDraftLimit = 25
	maxMyDraftLimit     = 100
)

type ContractWorkflowFacts struct {
	LatestState   string
	LatestRoundID *string
	InReview      bool
}

// ContractWorkflowReader is a read-only contract for Approval-owned facts.
// Its implementation may use the Store's authorized transaction bridge.
type ContractWorkflowReader interface {
	ReadContractWorkflow(context.Context, string, string) (ContractWorkflowFacts, error)
}

// DraftQueryRepository returns facts from Contract-owned tables only.
type DraftQueryRepository interface {
	LoadDraftDetailFacts(context.Context, clmcontract.Aggregate) (clmcontract.DraftDetailFacts, error)
	ListMyDraftRows(context.Context, string, string, int) ([]clmcontract.DraftRecoveryRow, error)
}

// LoadDraftDetail applies Contract's detail precedence and projection rules to
// rows loaded through the Contract query port. The caller owns current identity,
// scope installation and transaction lifetime.
func LoadDraftDetail(ctx context.Context, record clmcontract.Aggregate, repository DraftQueryRepository,
	workflow ContractWorkflowReader, codec DraftContentCodec, access DraftAccess) (clmcontract.DraftDetail, error) {
	if !access.CanRead(ctx, record) {
		return clmcontract.DraftDetail{}, fault.NotFound("没有找到对应记录")
	}
	facts, err := repository.LoadDraftDetailFacts(ctx, record)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	var content []byte
	var hasContent bool
	if record.CurrentRevisionID != nil && record.LifecycleState != "draft" {
		if !facts.HasRevision {
			return clmcontract.DraftDetail{}, fault.NotFound("没有找到对应记录")
		}
		content, hasContent = facts.RevisionSnapshot, true
	} else if facts.HasDraft {
		content, hasContent = facts.DraftContent, true
	} else if record.CurrentRevisionID != nil {
		if !facts.HasRevision {
			return clmcontract.DraftDetail{}, fault.NotFound("没有找到对应记录")
		}
		content, hasContent = facts.RevisionSnapshot, true
	}

	name := ""
	var amount, currency *string
	if hasContent {
		name, amount, currency, err = clmcontract.DraftSummaryFacts(content)
		if err != nil {
			return clmcontract.DraftDetail{}, err
		}
	}
	detail := clmcontract.DraftDetail{
		Summary:           clmcontract.SummaryFromDraft(record, name, amount, currency),
		CurrentRevisionID: record.CurrentRevisionID,
		ActiveRoundID:     nil,
		AllowedCommands:   clmcontract.DraftCommands(record, access.CanManage(record)),
		SourceRef:         sourceRef(record),
		Warnings:          []string{},
	}
	if record.SourceSystem != nil {
		detail.SourceID = *record.SourceSystem
	}
	if record.AuthorityMode == "external" {
		detail.Summary.ContractState = "external_observed"
		detail.Summary.WorkflowState = "external_observed"
		detail.SourceObservedState = "unknown"
		if facts.SourceObservation != nil {
			detail.SourceObservedState = facts.SourceObservation.State
			detail.SourceObservationDisposition = facts.SourceObservation.Disposition
			detail.SourceObservedAt = timePointer(facts.SourceObservation.ObservedAt)
		}
	} else if detail.SourceRef != nil {
		if facts.LegacyImport == nil {
			return clmcontract.DraftDetail{}, fault.NotFound("没有找到对应记录")
		}
		detail.SourceObservedAt = timePointer(facts.LegacyImport.ImportedAt)
		if facts.LegacyImport.ApprovalHistoryStatus == "unknown" {
			detail.Warnings = append(detail.Warnings, "approval_history_unknown")
		}
	}

	workflowState := "not_started"
	if record.AuthorityMode == "external" {
		workflowState = "external_observed"
	} else {
		workflowFacts, err := workflow.ReadContractWorkflow(ctx, record.TenantID, record.ContractID)
		if err != nil {
			return clmcontract.DraftDetail{}, err
		}
		if workflowFacts.LatestState != "" {
			workflowState = workflowFacts.LatestState
		}
		if workflowState == "in_review" {
			detail.ActiveRoundID = workflowFacts.LatestRoundID
			detail.AllowedCommands = []string{}
		}
	}
	detail.Summary.WorkflowState = workflowState
	if hasContent {
		plaintext, err := codec.Open(ctx, record.TenantID, record.ContractID, content)
		if err != nil {
			return clmcontract.DraftDetail{}, err
		}
		projected := codec.Project(plaintext, access.CanManage(record))
		detail.Draft = &projected
	}
	return detail, nil
}

// ListMyDrafts narrows SQL candidates to the caller's active native drafts,
// then rechecks each aggregate against current Read authority before
// projecting any title or amount. Because the candidate query is bounded by
// limit, inaccessible rows can make this recovery page shorter than requested.
func ListMyDrafts(ctx context.Context, tenantID, actorID string, limit int,
	repository DraftQueryRepository, access DraftAccess) ([]clmcontract.ContractDraftSummary, error) {
	if limit <= 0 {
		limit = defaultMyDraftLimit
	}
	if limit > maxMyDraftLimit {
		limit = maxMyDraftLimit
	}
	rows, err := repository.ListMyDraftRows(ctx, tenantID, actorID, limit)
	if err != nil {
		return nil, err
	}
	items := make([]clmcontract.ContractDraftSummary, 0, len(rows))
	for _, row := range rows {
		if len(items) == limit {
			break
		}
		if !access.CanRead(ctx, row.Record) {
			continue
		}
		updatedAt := row.UpdatedAt
		summary := clmcontract.SummaryFromDraft(row.Record, row.Name, row.Amount, row.Currency)
		items = append(items, clmcontract.ContractDraftSummary{
			ContractID: summary.ContractID, AuthorityMode: summary.AuthorityMode, Name: summary.Name,
			Number: summary.Number, Amount: summary.Amount, Currency: summary.Currency,
			ContractState: "draft", WorkflowState: "not_started", RowVersion: summary.RowVersion,
			CreatedAt: summary.CreatedAt, UpdatedAt: &updatedAt,
		})
	}
	return items, nil
}

func sourceRef(record clmcontract.Aggregate) *clmcontract.SourceRef {
	if record.SourceSystem == nil || record.ExternalContractID == nil || record.SourceVersion == nil {
		return nil
	}
	return &clmcontract.SourceRef{System: *record.SourceSystem, ID: *record.ExternalContractID, SourceVersion: *record.SourceVersion}
}

func timePointer(value time.Time) *time.Time { return &value }
