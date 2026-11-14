// File metadata (manifest mirror):
// Purpose: Pins history cursor identity checks, query ordering, and non-nil empty pages.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"contractaudit/internal/legal/model"
)

type historyTestRepo struct {
	headCalls, listCalls, versionCalls int
	head                               int64
}

func (r *historyTestRepo) ReadHead(context.Context, RecordIdentity, string) (int64, error) {
	r.headCalls++
	return r.head, nil
}
func (r *historyTestRepo) ListVersionNumbers(context.Context, RecordIdentity, string, int64, bool, int) ([]int64, error) {
	r.listCalls++
	return nil, nil
}
func (r *historyTestRepo) ReadVersion(context.Context, RecordIdentity, string, int64) (model.LegalApplicabilityFactVersion, error) {
	r.versionCalls++
	return model.LegalApplicabilityFactVersion{}, nil
}
func TestHistoryCursorBindingAfterHeadAndEmptyItems(t *testing.T) {
	id := RecordIdentity{TenantID: "tenant", OrgID: "org", CaseID: "case"}
	repo := &historyTestRepo{head: 2}
	raw, _ := json.Marshal(historyCursor{Version: 1, TenantID: "other", CaseID: "case", Dimension: "buyer_kind", BeforeVersion: 1})
	input := model.LegalApplicabilityHistoryInput{Cursor: base64.RawURLEncoding.EncodeToString(raw)}
	_, err := ReadApplicabilityHistory(context.Background(), repo, id, "buyer_kind", input, 20)
	if err == nil || repo.headCalls != 1 || repo.listCalls != 0 {
		t.Fatalf("head=%d list=%d err=%v", repo.headCalls, repo.listCalls, err)
	}
	repo.head = 0
	input.Cursor = ""
	got, err := ReadApplicabilityHistory(context.Background(), repo, id, "buyer_kind", input, 20)
	if err != nil || got.Items == nil || len(got.Items) != 0 || got.NextCursor != nil || got.CurrentVersion != 0 {
		t.Fatalf("empty history=%+v err=%v", got, err)
	}
}
