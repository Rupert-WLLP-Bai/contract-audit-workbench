// File metadata (manifest mirror):
// Purpose: Checks optional authorized case-list business identity fields remain exact and absent when no profile projection is available, while stable case identity is retained.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-04
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"testing"

	"contractaudit/internal/store"
)

func TestCaseSummaryIdentityProjectionIsOptionalAndExact(t *testing.T) {
	item := store.CaseSummary{Case: store.Case{ID: "case-full", Title: "同名案件"}}
	legacy := summaryJSON(item)
	if _, ok := legacy["case_number"]; ok {
		t.Fatal("legacy case unexpectedly received a business number")
	}
	item.Identity = &store.CaseListIdentity{Number: "案-01", Customer: "客户", Supplier: "供应商", OwnerName: "负责人"}
	got := summaryJSON(item)
	for key, want := range map[string]string{"id": "case-full", "case_number": "案-01", "customer": "客户", "supplier": "供应商", "owner_name": "负责人"} {
		if got[key] != want {
			t.Fatalf("%s = %v, want %q", key, got[key], want)
		}
	}
}
