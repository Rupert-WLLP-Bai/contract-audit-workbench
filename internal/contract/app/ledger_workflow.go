package app

import (
	"context"
	"time"

	"contractaudit/internal/contract/ledger"
)

// LedgerWorkflowReader supplies Approval-owned in-review facts for a bounded batch.
type LedgerWorkflowReader interface {
	InReview(context.Context, string, []string) (map[string]bool, error)
}

type workflowLedgerReader struct {
	contracts LedgerReader
	workflows LedgerWorkflowReader
}

// WithLedgerWorkflow composes workflow facts before the consumer counts rows.
func WithLedgerWorkflow(contracts LedgerReader, workflows LedgerWorkflowReader) LedgerReader {
	return workflowLedgerReader{contracts: contracts, workflows: workflows}
}

func (r workflowLedgerReader) Read(ctx context.Context, tenant string, filter ledger.Filter, afterSet bool, afterKey time.Time, afterID string, limit int) ([]ledger.Row, error) {
	wanted := filter.WorkflowState
	filter.WorkflowState = ""
	result := make([]ledger.Row, 0, limit)
	for len(result) < limit {
		rows, err := r.contracts.Read(ctx, tenant, filter, afterSet, afterKey, afterID, limit)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			if row.Aggregate.AuthorityMode != "external" {
				ids = append(ids, row.Aggregate.ContractID)
			}
		}
		states, err := r.workflows.InReview(ctx, tenant, ids)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			afterSet, afterKey, afterID = true, row.SortKey, row.Item.ContractID
			inReview := states[row.Aggregate.ContractID]
			workflow := "not_started"
			if row.Aggregate.AuthorityMode == "external" {
				workflow = "external_observed"
			} else if inReview {
				workflow = "in_review"
			}
			if wanted != "" && workflow != wanted {
				continue
			}
			item, err := ledger.ItemFromAggregate(row.Aggregate, row.Content, inReview)
			if err != nil {
				return nil, err
			}
			item.SigningDate = row.SigningDate
			row.Item = item
			row.Content = nil
			result = append(result, row)
			if len(result) == limit {
				break
			}
		}
		if len(rows) < limit {
			break
		}
	}
	return result, nil
}
