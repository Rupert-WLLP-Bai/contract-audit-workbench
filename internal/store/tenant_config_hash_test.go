// File metadata (manifest mirror):
// Purpose: 实现 `TestTenantConfigFrozenHashVersions` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestTenantConfigFrozenHashVersions(t *testing.T) {
	legacy := RunSnapshot{RunID: "run", DocumentIDs: []string{"doc"}, RuleSetHash: "rules", PlaybookHash: "playbook", ModelRef: "model", ComparatorRef: "compare"}
	documents := []FrozenDocument{{ParsedDocumentID: "parse", Text: "frozen"}}
	normalized := scopeSnapshot(legacy)
	normalized.RunID = ""
	oldBytes, err := json.Marshal(struct {
		Snapshot  RunSnapshot      `json:"snapshot"`
		Documents []FrozenDocument `json:"documents"`
	}{normalized, documents})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(oldBytes)
	got, err := frozenInputHash(legacy, documents)
	if err != nil || got != hex.EncodeToString(sum[:]) {
		t.Fatalf("legacy input bytes changed: hash=%s err=%v", got, err)
	}
	// Pre-version historical rows may already contain adoption metadata. Their
	// input still uses the original byte contract.
	legacy.AdoptedTenantConfigVersionID = "tcv_old"
	legacy.AdoptedTenantConfigDigest = strings.Repeat("a", 64)
	oldAdopted, err := frozenInputHash(legacy, documents)
	if err != nil || oldAdopted != got {
		t.Fatalf("legacy adoption changed old hash: %s %v", oldAdopted, err)
	}
	first := legacy
	first.InputHashVersion = 2
	firstHash, err := frozenInputHash(first, documents)
	if err != nil || firstHash == got {
		t.Fatalf("new hash did not bind adoption: %s %v", firstHash, err)
	}
	second := first
	second.AdoptedTenantConfigVersionID = "tcv_next"
	secondHash, err := frozenInputHash(second, documents)
	if err != nil || secondHash == firstHash || sameRunConfiguration(first, second) {
		t.Fatalf("different adoption reused input identity: %s %v", secondHash, err)
	}
	stateCopy, err := (state{Snapshots: []RunSnapshot{first}}).clone()
	if err != nil {
		t.Fatal(err)
	}
	restored := stateCopy.Snapshots[0]
	retried := withFrozenGovernance(RunSnapshot{}, restored)
	retryHash, err := frozenInputHash(restored, documents)
	if err != nil || retryHash != firstHash || retried.InputHashVersion != 2 || retried.AdoptedTenantConfigVersionID != first.AdoptedTenantConfigVersionID || retried.AdoptedTenantConfigDigest != first.AdoptedTenantConfigDigest {
		t.Fatalf("retry lost frozen adoption identity: snapshot=%+v err=%v", retried, err)
	}
	t.Run("live send control preserves frozen retry", func(t *testing.T) {
		st := openModelBudgetStore(t)
		ctx := context.Background()
		if _, err := st.pool.Exec(ctx, `INSERT INTO identity_tenants(tenant_id,display_name,status) VALUES ('hash-test','Hash test','active')`); err != nil {
			t.Fatal(err)
		}
		check := func(want bool) {
			t.Helper()
			var enabled bool
			if err := st.pool.QueryRow(ctx, `SELECT tenant_model_send_fence('hash-test',1,false)`).Scan(&enabled); err != nil || enabled != want {
				t.Fatalf("live control enabled=%v want=%v err=%v", enabled, want, err)
			}
		}
		check(true)
		if _, err := st.pool.Exec(ctx, `UPDATE identity_tenants SET model_sends_enabled=false WHERE tenant_id='hash-test'`); err != nil {
			t.Fatal(err)
		}
		check(false)
		after, err := frozenInputHash(restored, documents)
		if err != nil || after != firstHash {
			t.Fatalf("live control changed retry input hash: %s %v", after, err)
		}
		if _, err := st.pool.Exec(ctx, `UPDATE identity_tenants SET model_sends_enabled=true WHERE tenant_id='hash-test'`); err != nil {
			t.Fatal(err)
		}
		check(true)
	})

}
