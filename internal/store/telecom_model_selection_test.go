// File metadata (manifest mirror):
// Purpose: Checks declared case-specific model routing so dedicated-line reviews do not invoke the unrelated warranty model path.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import "testing"

func TestDedicatedLineSkipsWarrantyModelReview(t *testing.T) {
	calls := 0
	run := func() error { calls++; return nil }
	if err := runApplicableTelecomModelReview(Case{BusinessLine: "ENT", Scenario: "telecom_dedicated_line"}, run); err != nil || calls != 0 {
		t.Fatalf("dedicated-line model calls = %d, err = %v", calls, err)
	}
	if err := runApplicableTelecomModelReview(Case{BusinessLine: "ENT", Scenario: "cloud_services", ContractType: "service"}, run); err != nil || calls != 1 {
		t.Fatalf("cloud model calls = %d, err = %v", calls, err)
	}
}
