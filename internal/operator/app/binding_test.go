// File metadata (manifest mirror):
// Purpose: Pins decoding stored response JSON into scanned binding values while retaining independent request digests.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"testing"

	"contractaudit/internal/operator/model"
)

func TestBindingReceiptDecodePreservesScannedFieldsAndDigest(t *testing.T) {
	row := MappingBindingRow{Payload: model.ServiceObjectMappingBindingPayload{CaseID: "case", Status: "confirmed"}, RequestSHA256: "stored", ResponseBody: []byte(`{"status":"inactive"}`)}
	record, err := DecodeBindingRecord(row, "bad receipt")
	if err != nil || record.Value.CaseID != "case" || record.Value.Status != "inactive" || record.RequestSHA256 != "stored" {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	row.ResponseBody = []byte(`{"status":`)
	if _, err := DecodeBindingRecord(row, "bad receipt"); err == nil {
		t.Fatal("invalid response body accepted")
	}
}
