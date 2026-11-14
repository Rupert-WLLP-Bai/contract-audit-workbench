// File metadata (manifest mirror):
// Purpose: Provides a restricted Core and Document fixture to exercise original reads through the production HTTP service boundary.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"testing"
)

// ClauseOriginalHTTPFixture exposes only the real role stores and one small
// original needed by the external production-handler test.
type ClauseOriginalHTTPFixture struct {
	Core, Document            *Store
	Token, CaseID, DocumentID string
	Body                      []byte
}

func NewClauseOriginalHTTPFixture(t *testing.T) ClauseOriginalHTTPFixture {
	t.Helper()
	fixture, ok := newDurableCaseFixture(t)
	if !ok {
		t.Skip("TEST_DATABASE_URL and MIGRATOR_DATABASE_URL are required")
	}
	body := []byte("真实文档原文")
	docID, err := fixture.document.AddDocument(WithAdmissionSessionToken(context.Background(), fixture.sessionToken), fixture.admin, fixture.caseID, "attachment", "original.txt", "text/plain", body)
	if err != nil {
		t.Fatal(err)
	}
	return ClauseOriginalHTTPFixture{Core: fixture.core, Document: fixture.document, Token: fixture.sessionToken, CaseID: fixture.caseID, DocumentID: docID, Body: body}
}
