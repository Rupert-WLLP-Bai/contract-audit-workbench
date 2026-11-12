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
