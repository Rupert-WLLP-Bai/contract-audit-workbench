// File metadata (manifest mirror):
// Purpose: Checks ledger pagination across invisible rows and cursor continuation bound to the authorized query.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"contractaudit/internal/contract/ledger"
	clmcontract "contractaudit/internal/contract/model"
)

type ledgerRows []ledger.Row

func (r ledgerRows) Read(_ context.Context, _ string, _ ledger.Filter, afterSet bool, afterKey time.Time, afterID string, limit int) ([]ledger.Row, error) {
	out := make([]ledger.Row, 0, limit)
	for _, row := range r {
		if afterSet && (row.SortKey.After(afterKey) || row.SortKey.Equal(afterKey) && row.Item.ContractID >= afterID) {
			continue
		}
		out = append(out, row)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

type ledgerAccess map[string]bool

func (a ledgerAccess) CanRead(_ context.Context, record clmcontract.Aggregate) bool {
	return a[record.ContractID]
}

func TestListContractLedgerPaginatesAfterPerRowAuthorization(t *testing.T) {
	base := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	rows := ledgerRows{
		ledgerTestRow("00000000-0000-4000-8000-000000000005", base.Add(5*time.Second)),
		ledgerTestRow("00000000-0000-4000-8000-000000000004", base.Add(4*time.Second)),
		ledgerTestRow("00000000-0000-4000-8000-000000000003", base.Add(3*time.Second)),
		ledgerTestRow("00000000-0000-4000-8000-000000000002", base.Add(2*time.Second)),
		ledgerTestRow("00000000-0000-4000-8000-000000000001", base.Add(time.Second)),
	}
	identity := ledger.CursorIdentity{TenantID: "tenant-1", ActorID: "actor-1"}
	access := ledgerAccess{
		rows[1].Item.ContractID: true,
		rows[3].Item.ContractID: true,
		rows[4].Item.ContractID: true,
	}
	page, err := ListContractLedger(context.Background(), identity, ledger.Query{Limit: 2}, rows, access)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ContractID != rows[1].Item.ContractID || page.Items[1].ContractID != rows[3].Item.ContractID {
		t.Fatalf("authorized page = %+v", page.Items)
	}
	if !page.HasMore || page.NextCursor == nil {
		t.Fatalf("later authorized row must produce a continuation cursor: %+v", page)
	}
	next, err := ListContractLedger(context.Background(), identity,
		ledger.Query{Limit: 2, Cursor: *page.NextCursor}, rows, access)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].ContractID != rows[4].Item.ContractID || next.HasMore || next.NextCursor != nil {
		t.Fatalf("continuation page = %+v", next)
	}
}

func ledgerTestRow(contractID string, createdAt time.Time) ledger.Row {
	record := clmcontract.Aggregate{TenantID: "tenant-1", ContractID: contractID, CreatedAt: createdAt}
	return ledger.Row{Aggregate: record, Item: ledger.Item{ContractID: contractID, CreatedAt: createdAt}, SortKey: createdAt}
}

type boundedWorkflowFacts struct {
	inReview map[string]bool
	maxBatch int
	calls    int
}

func (f *boundedWorkflowFacts) InReview(_ context.Context, _ string, ids []string) (map[string]bool, error) {
	f.calls++
	if len(ids) > f.maxBatch {
		f.maxBatch = len(ids)
	}
	result := make(map[string]bool)
	for _, id := range ids {
		result[id] = f.inReview[id]
	}
	return result, nil
}

func TestWorkflowLedgerReaderSkipsNonmatchingMalformedContentAndKeepsBatchesBounded(t *testing.T) {
	base := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	rows := make(ledgerRows, 0, 20003)
	for i := 0; i < 20003; i++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", 20003-i)
		row := ledgerTestRow(id, base.Add(time.Duration(20003-i)*time.Second))
		row.Aggregate.AuthorityMode = "native"
		row.Content = []byte("{")
		if i >= 20001 {
			row.Content = []byte("{}")
		}
		rows = append(rows, row)
	}
	facts := &boundedWorkflowFacts{inReview: map[string]bool{rows[20001].Item.ContractID: true, rows[20002].Item.ContractID: true}}
	reader := WithLedgerWorkflow(rows, facts)
	filter := ledger.Filter{WorkflowState: "in_review"}
	first, err := reader.Read(context.Background(), "tenant-1", filter, false, time.Time{}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].Item.ContractID != rows[20001].Item.ContractID || first[1].Item.ContractID != rows[20002].Item.ContractID {
		t.Fatalf("filtered candidates = %+v", first)
	}
	if facts.calls < 2 || facts.maxBatch > 50 {
		t.Fatalf("workflow calls=%d max batch=%d", facts.calls, facts.maxBatch)
	}
	next, err := reader.Read(context.Background(), "tenant-1", filter, true, first[0].SortKey, first[0].Item.ContractID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].Item.ContractID != first[1].Item.ContractID {
		t.Fatalf("cursor continuation = %+v", next)
	}
}
