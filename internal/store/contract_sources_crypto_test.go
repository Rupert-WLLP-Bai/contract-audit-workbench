// File metadata (manifest mirror):
// Purpose: 实现 `TestCLMSourceObservationCipherBindsObjectContext` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"bytes"
	"context"
	"testing"
)

func TestCLMSourceObservationCipherBindsObjectContext(t *testing.T) {
	testCLMDataKey(t)
	ctx := context.Background()
	raw := []byte(`{"bank_account":"6222020200001234567"}`)
	ciphertext, err := sealCLMSourceObservation(ctx, raw, "tenant-a", "erp-a", "event-a", "observation-a")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := unsealCLMSourceObservation(ctx, ciphertext, "tenant-a", "erp-a", "event-a", "observation-a")
	if err != nil || !bytes.Equal(opened, raw) {
		t.Fatalf("round trip mismatch: %q err=%v", opened, err)
	}
	for _, context := range [][4]string{
		{"tenant-b", "erp-a", "event-a", "observation-a"},
		{"tenant-a", "erp-b", "event-a", "observation-a"},
		{"tenant-a", "erp-a", "event-b", "observation-a"},
		{"tenant-a", "erp-a", "event-a", "observation-b"},
	} {
		if _, err := unsealCLMSourceObservation(ctx, ciphertext, context[0], context[1], context[2], context[3]); err == nil {
			t.Fatalf("ciphertext opened under mismatched context %q", context)
		}
	}
	t.Setenv(clmEnvelopeKeysEnv, "")
	t.Setenv(clmLegacyRefEnv, "")
	if _, err := sealCLMSourceObservation(ctx, raw, "tenant-a", "erp-a", "event-a", "observation-a"); err == nil {
		t.Fatal("missing data key must refuse source payload persistence")
	}
}
