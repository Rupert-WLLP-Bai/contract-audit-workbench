// File metadata (manifest mirror):
// Purpose: Defines unexecuted regression cases for continuous authorized inbox pagination across all three ordering views and hidden rows.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	model "contractaudit/internal/approval/model"
	contract "contractaudit/internal/contract/model"
)

type inboxTestRows []model.InboxCandidate

func (rows inboxTestRows) LoadInboxCandidates(_ context.Context, _, _, view string, after bool, key time.Time, id string, limit int) ([]model.InboxCandidate, error) {
	out := []model.InboxCandidate{}
	for _, row := range rows {
		at, rowID := model.InboxKey(view, row)
		if after && ((view == "todo" && (at.Before(key) || at.Equal(key) && rowID <= id)) || (view != "todo" && (at.After(key) || at.Equal(key) && rowID >= id))) {
			continue
		}
		out = append(out, row)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

type inboxTestContracts struct{}

func (inboxTestContracts) LoadContract(_ context.Context, tenant, id string) (contract.Aggregate, error) {
	return contract.Aggregate{TenantID: tenant, ContractID: id, AuthorityMode: "native", ValidityState: "active"}, nil
}
func (inboxTestContracts) LoadApprovalSnapshotOptional(context.Context, string, string, string) ([]byte, error) {
	return []byte(`{}`), nil
}

type inboxTestAccess map[string]bool

func (a inboxTestAccess) CanRead(record contract.Aggregate) bool { return a[record.ContractID] }
func (a inboxTestAccess) CanDecide(contract.Aggregate) bool      { return false }

func TestInboxSkipsUnreadableCandidatesWithoutLosingNextVisibleRow(t *testing.T) {
	actor, _ := accounts.ByID("demo-admin")
	ids := []string{"c6052c81-8b05-49ce-89e9-f6e9c6403c10", "c6052c81-8b05-49ce-89e9-f6e9c6403c11", "c6052c81-8b05-49ce-89e9-f6e9c6403c12", "c6052c81-8b05-49ce-89e9-f6e9c6403c13"}
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, view := range []string{"todo", "done", "initiated"} {
		t.Run(view, func(t *testing.T) {
			rows := inboxTestRows{}
			for i, id := range ids {
				offset := i
				if view != "todo" {
					offset = len(ids) - 1 - i
				}
				at := base.Add(time.Duration(offset) * time.Second)
				rows = append(rows, model.InboxCandidate{Round: model.RoundRead{ContractID: id, ApprovalRoundID: id, StartedAt: at}, Task: &model.ReadTaskRow{TaskID: id, ActivatedAt: &at}, Decision: &model.ReadDecisionRow{DecisionID: id, DecidedAt: at}})
			}
			access := inboxTestAccess{ids[1]: true, ids[3]: true}
			first, err := ListInbox(context.Background(), actor, view, 1, "", rows, inboxTestContracts{}, access)
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Items) != 1 || first.Items[0].ContractID != ids[1] || !first.HasMore || first.NextCursor == nil {
				t.Fatalf("first page = %+v", first)
			}
			next, err := ListInbox(context.Background(), actor, view, 1, *first.NextCursor, rows, inboxTestContracts{}, access)
			if err != nil {
				t.Fatal(err)
			}
			if len(next.Items) != 1 || next.Items[0].ContractID != ids[3] || next.HasMore {
				t.Fatalf("next page = %+v", next)
			}
		})
	}
}
