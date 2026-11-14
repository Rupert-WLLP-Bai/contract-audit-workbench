// File metadata (manifest mirror):
// Purpose: Exercises safe facts API projection and the distinction between advisory coverage and structured-output validity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"contractaudit/internal/agentflow"
	"contractaudit/internal/model"
)

func TestBusinessContractFactsJSONSeparatesStructuredOutputStatus(t *testing.T) {
	modelConfigurationID := strings.Repeat("a", 64)
	configuration, err := agentflow.ResolveContractFactsConfiguration(
		modelConfigurationID, "selected-chat",
		model.ContractFactsConfigurationID(modelConfigurationID, "selected-chat"),
	)
	if err != nil {
		t.Fatal(err)
	}
	source := agentflow.ContractFactsSource{
		DocumentID: "document-1", ParsedDocumentID: "parsed-1",
		OriginalDigest: strings.Repeat("b", 64), NormalizedDigest: strings.Repeat("c", 64),
	}
	for _, test := range []struct {
		failureCode string
		want        string
	}{
		{failureCode: "", want: "valid"},
		{failureCode: "contract_facts_output_invalid", want: "invalid"},
		{failureCode: "contract_facts_request_failed", want: "not_returned"},
	} {
		projection := agentflow.EmptyContractFactsProjection("attempt-1", *configuration, source, 4, false, test.failureCode)
		payload, err := json.Marshal(projection)
		if err != nil {
			t.Fatal(err)
		}
		body, ok := businessContractFactsJSON(payload).(map[string]any)
		if !ok || body["structured_output_status"] != test.want {
			t.Fatalf("failure code %q projected status %v, want %q", test.failureCode, body["structured_output_status"], test.want)
		}
		if _, exposed := body["failure_code"]; exposed {
			t.Fatalf("private failure code %q was exposed", test.failureCode)
		}
	}
}
