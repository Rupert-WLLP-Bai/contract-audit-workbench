package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/contract/ledger"
	clmcontract "contractaudit/internal/contract/model"
	platform "contractaudit/internal/platform/postgres"
)

// LedgerReader loads candidate rows from Contract-owned tables. Store performs
// the current per-object authorization checks before a row is returned.
type LedgerReader struct{ tx platform.Executor }

func NewLedgerReader(tx platform.Executor) *LedgerReader { return &LedgerReader{tx: tx} }

func (r *LedgerReader) Read(ctx context.Context, tenant string, filter ledger.Filter,
	afterSet bool, afterKey time.Time, afterID string, limit int) ([]ledger.Row, error) {
	where := &strings.Builder{}
	where.WriteString(`WHERE k.tenant_id=$1`)
	args := []any{tenant}
	appendLedgerFilter(where, &args, filter)
	if afterSet {
		args = append(args, afterKey.UTC(), afterID)
		where.WriteString(fmt.Sprintf(` AND (k.created_at,k.contract_id) < ($%d::timestamptz,$%d::uuid)`, len(args)-1, len(args)))
	}
	args = append(args, limit)
	query := `SELECT k.tenant_id,k.contract_id,k.owner_org_id,k.authority_mode,k.contract_number,
		k.lifecycle_state,k.validity_state,k.row_version,k.created_by,k.created_at,k.updated_at,
		k.source_system,k.in_review,k.content,k.signing_date
	FROM (
		SELECT c.tenant_id,c.contract_id,c.owner_org_id,c.authority_mode,c.contract_number,
			c.lifecycle_state,c.validity_state,c.row_version,c.created_by,c.created_at,c.updated_at,
			c.source_system,COALESCE(d.content,r.snapshot) AS content,
			COALESCE((SELECT registration.signing_date::text FROM clm_contract_signing_registrations registration
				WHERE registration.tenant_id=c.tenant_id AND registration.contract_id=c.contract_id),
				COALESCE(d.content->'contract'->>'signing_date',r.snapshot->'contract'->>'signing_date')) AS signing_date,
			EXISTS(SELECT 1 FROM clm_approval_rounds ar
				WHERE ar.tenant_id=c.tenant_id AND ar.contract_id=c.contract_id AND ar.state='in_review') AS in_review
		FROM clm_contracts c
		LEFT JOIN clm_contract_drafts d ON d.tenant_id=c.tenant_id AND d.contract_id=c.contract_id
		LEFT JOIN clm_contract_revisions r ON r.tenant_id=c.tenant_id AND r.contract_id=c.contract_id
			AND r.contract_revision_id=c.current_revision_id
	) k ` + where.String() + ` ORDER BY k.created_at DESC,k.contract_id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := r.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ledger.Row{}
	for rows.Next() {
		var record clmcontract.Aggregate
		var content []byte
		var inReview bool
		var signingDate *string
		if err := rows.Scan(&record.TenantID, &record.ContractID, &record.OwnerOrgID, &record.AuthorityMode,
			&record.ContractNumber, &record.LifecycleState, &record.ValidityState, &record.RowVersion,
			&record.CreatedBy, &record.CreatedAt, &record.UpdatedAt, &record.SourceSystem, &inReview, &content, &signingDate); err != nil {
			return nil, err
		}
		item, err := ledger.ItemFromAggregate(record, content, inReview)
		if err != nil {
			return nil, err
		}
		item.SigningDate = signingDate
		out = append(out, ledger.Row{Aggregate: record, Item: item, SortKey: record.CreatedAt})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func appendLedgerFilter(where *strings.Builder, args *[]any, filter ledger.Filter) {
	add := func(predicate string, value any) {
		*args = append(*args, value)
		where.WriteString(fmt.Sprintf(predicate, len(*args)))
	}
	if filter.Text != "" {
		*args = append(*args, filter.Text)
		position := len(*args)
		where.WriteString(fmt.Sprintf(` AND (position(lower($%d) in lower(coalesce(k.content->'contract'->>'name','')))>0`+
			` OR position(lower($%d) in lower(coalesce(k.contract_number,'')))>0`+
			` OR position(lower($%d) in lower(coalesce(k.content->'contract'->>'number','')))>0)`, position, position, position))
	}
	if filter.SupplierName != "" {
		add(` AND position(lower($%d) in lower(coalesce(k.content->'supplier'->>'name','')))>0`, filter.SupplierName)
	}
	if filter.DepartmentID != "" {
		add(` AND k.content->'contract'->>'department_id'=$%d`, filter.DepartmentID)
	}
	if filter.ProjectType != "" {
		add(` AND k.content->'contract'->>'project_type'=$%d`, filter.ProjectType)
	}
	if filter.ProcurementMethod != "" {
		add(` AND k.content->'contract'->>'procurement_method'=$%d`, filter.ProcurementMethod)
	}
	if filter.ContractState != "" {
		add(` AND (CASE WHEN k.authority_mode='external' THEN 'external_observed' ELSE k.lifecycle_state END)=$%d`, filter.ContractState)
	}
	if filter.AuthorityMode != "" {
		add(` AND k.authority_mode=$%d`, filter.AuthorityMode)
	}
	if filter.ValidityState != "" {
		add(` AND k.validity_state=$%d`, filter.ValidityState)
	}
	if filter.WorkflowState == "in_review" {
		where.WriteString(` AND k.authority_mode='native' AND k.in_review`)
	} else if filter.WorkflowState == "not_started" {
		where.WriteString(` AND k.authority_mode='native' AND NOT k.in_review`)
	} else if filter.WorkflowState == "external_observed" {
		where.WriteString(` AND k.authority_mode='external'`)
	}
	if filter.AmountMin != nil {
		add(` AND (CASE WHEN k.content->'contract'->>'amount' ~ '`+ledger.ContentAmountPattern+`' THEN (k.content->'contract'->>'amount')::numeric END) >= $%d::numeric`, *filter.AmountMin)
	}
	if filter.AmountMax != nil {
		add(` AND (CASE WHEN k.content->'contract'->>'amount' ~ '`+ledger.ContentAmountPattern+`' THEN (k.content->'contract'->>'amount')::numeric END) <= $%d::numeric`, *filter.AmountMax)
	}
	if filter.SigningDateFrom != nil {
		add(` AND k.signing_date >= $%d`, *filter.SigningDateFrom)
	}
	if filter.SigningDateTo != nil {
		add(` AND k.signing_date <= $%d`, *filter.SigningDateTo)
	}
	if filter.CreatedFrom != nil {
		add(` AND k.created_at >= $%d::timestamptz`, filter.CreatedFrom.UTC())
	}
	if filter.CreatedTo != nil {
		add(` AND k.created_at <= $%d::timestamptz`, filter.CreatedTo.UTC())
	}
}
