package store

import (
	"bytes"
	"testing"
)

func TestCLMSourceObservationCipherBindsObjectContext(t *testing.T) {
	testCLMDataKey(t)
	raw := []byte(`{"bank_account":"6222020200001234567"}`)
	ciphertext, err := sealCLMSourceObservation(raw, "tenant-a", "erp-a", "event-a", "observation-a")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := unsealCLMSourceObservation(ciphertext, "tenant-a", "erp-a", "event-a", "observation-a")
	if err != nil || !bytes.Equal(opened, raw) {
		t.Fatalf("round trip mismatch: %q err=%v", opened, err)
	}
	for _, context := range [][4]string{
		{"tenant-b", "erp-a", "event-a", "observation-a"},
		{"tenant-a", "erp-b", "event-a", "observation-a"},
		{"tenant-a", "erp-a", "event-b", "observation-a"},
		{"tenant-a", "erp-a", "event-a", "observation-b"},
	} {
		if _, err := unsealCLMSourceObservation(ciphertext, context[0], context[1], context[2], context[3]); err == nil {
			t.Fatalf("ciphertext opened under mismatched context %q", context)
		}
	}
	t.Setenv("CLM_DATA_KEY", "")
	if _, err := sealCLMSourceObservation(raw, "tenant-a", "erp-a", "event-a", "observation-a"); err == nil {
		t.Fatal("missing data key must refuse source payload persistence")
	}
}
