// File metadata (manifest mirror):
// Purpose: 实现 `setFingerprintRotationKeys`, `TestRestrictedFingerprintsKeepFrozenKeyAcrossRotation`, `TestRestrictedFingerprintsRejectRotationWithoutFrozenKey` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
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
	"encoding/base64"
	"encoding/json"
	"testing"

	"contractaudit/internal/clmenvelope"
)

func setFingerprintRotationKeys(t *testing.T, active clmenvelope.KeyRef, retainOld bool) {
	t.Helper()
	oldMaterial := make([]byte, 32)
	newMaterial := make([]byte, 32)
	for i := range oldMaterial {
		oldMaterial[i] = 0x31
		newMaterial[i] = 0x72
	}
	oldRef := clmenvelope.KeyRef{ID: "fingerprint-v1", Version: 1}
	newRef := clmenvelope.KeyRef{ID: "fingerprint-v2", Version: 2}
	oldEntry := clmenvelope.LocalKeyEntry{
		TenantID: "tenant-a", Purpose: clmPurpose, Kind: clmenvelope.FingerprintKey,
		Ref: oldRef, MaterialBase64: base64.StdEncoding.EncodeToString(oldMaterial), Active: active == oldRef,
	}
	newEntry := clmenvelope.LocalKeyEntry{
		TenantID: "tenant-a", Purpose: clmPurpose, Kind: clmenvelope.FingerprintKey,
		Ref: newRef, MaterialBase64: base64.StdEncoding.EncodeToString(newMaterial), Active: active == newRef,
	}
	keys := []clmenvelope.LocalKeyEntry{newEntry}
	if active == oldRef || retainOld {
		// When retained, the old key resolves by its original reference but is
		// inactive after rotation.
		keys = append(keys, oldEntry)
	}
	config, err := json.Marshal(clmenvelope.LocalKeyConfig{Version: 1, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(clmEnvelopeKeysEnv, string(config))
}

func TestRestrictedFingerprintsKeepFrozenKeyAcrossRotation(t *testing.T) {
	ctx := context.Background()
	oldRef := clmenvelope.KeyRef{ID: "fingerprint-v1", Version: 1}
	newRef := clmenvelope.KeyRef{ID: "fingerprint-v2", Version: 2}
	draft := freezeCompleteDraft()
	binding := clmFieldBinding("tenant-a", "contract-a", "supplier.bank_account")

	setFingerprintRotationKeys(t, oldRef, false)
	baseline, err := computeRestrictedFingerprintsBound(ctx, &draft, binding, nil)
	if err != nil {
		t.Fatalf("freeze baseline fingerprints: %v", err)
	}
	if baseline.KeyRef == nil || *baseline.KeyRef != oldRef {
		t.Fatalf("baseline key ref = %+v, want %+v", baseline.KeyRef, oldRef)
	}

	setFingerprintRotationKeys(t, newRef, true)
	afterRotation, err := computeRestrictedFingerprintsBound(ctx, &draft, binding, &baseline)
	if err != nil {
		t.Fatalf("fingerprint unchanged draft after rotation with old key retained: %v", err)
	}
	if afterRotation.KeyRef == nil || *afterRotation.KeyRef != *baseline.KeyRef {
		t.Fatalf("unchanged draft changed fingerprint key ref: before=%+v after=%+v", baseline.KeyRef, afterRotation.KeyRef)
	}
	newFreeze, err := computeRestrictedFingerprintsBound(ctx, &draft, binding, nil)
	if err != nil || newFreeze.KeyRef == nil || *newFreeze.KeyRef != newRef {
		t.Fatalf("new freeze did not use current key: ref=%+v err=%v", newFreeze.KeyRef, err)
	}
	if afterRotation.Fields["supplier.bank_account"] != baseline.Fields["supplier.bank_account"] {
		t.Fatalf("unchanged draft fingerprint changed after active-key rotation: before=%+v after=%+v",
			baseline.Fields["supplier.bank_account"], afterRotation.Fields["supplier.bank_account"])
	}
}

func TestRestrictedFingerprintsRejectRotationWithoutFrozenKey(t *testing.T) {
	ctx := context.Background()
	oldRef := clmenvelope.KeyRef{ID: "fingerprint-v1", Version: 1}
	newRef := clmenvelope.KeyRef{ID: "fingerprint-v2", Version: 2}
	draft := freezeCompleteDraft()
	binding := clmFieldBinding("tenant-a", "contract-a", "supplier.bank_account")

	setFingerprintRotationKeys(t, oldRef, false)
	baseline, err := computeRestrictedFingerprintsBound(ctx, &draft, binding, nil)
	if err != nil {
		t.Fatalf("freeze baseline fingerprints: %v", err)
	}
	setFingerprintRotationKeys(t, newRef, false)
	if _, err := computeRestrictedFingerprintsBound(ctx, &draft, binding, &baseline); err == nil {
		t.Fatal("missing frozen fingerprint key must fail closed; do not recompute with the active key")
	}
	draft.Supplier = nil
	if _, err := computeRestrictedFingerprintsBound(ctx, &draft, binding, &baseline); err == nil {
		t.Fatal("deleting all restricted values must not bypass the frozen key requirement")
	}
}
