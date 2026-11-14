// File metadata (manifest mirror):
// Purpose: Pins first-row basis integrity errors ahead of later cursor errors and verifies cursor closure.
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
	"errors"
	"testing"

	"contractaudit/internal/legal/model"
)

type familyTestCursor struct {
	calls  int
	closed bool
}

func (c *familyTestCursor) Next() (FactFamilyRow, bool, error) {
	c.calls++
	if c.calls == 1 {
		return FactFamilyRow{Head: 1, Fact: model.LegalApplicabilityFactVersion{Dimension: "buyer_kind", BasisRefs: []byte(`[]`), BasisSHA256: "bad"}}, true, nil
	}
	return FactFamilyRow{}, false, errors.New("later rows error")
}
func (c *familyTestCursor) Close() { c.closed = true }

type familyTestReader struct{ cursor *familyTestCursor }

func (f familyTestReader) OpenFactFamilies(context.Context, RecordIdentity) (FactFamilyCursor, error) {
	return f.cursor, nil
}
func TestFactFamiliesBadBasisPrecedesLaterRowsError(t *testing.T) {
	cursor := &familyTestCursor{}
	_, err := LoadFactFamilies(context.Background(), familyTestReader{cursor}, RecordIdentity{})
	if err == nil || err.Error() != "法律适用事实材料依据摘要不匹配" || cursor.calls != 1 || !cursor.closed {
		t.Fatalf("calls=%d closed=%v err=%v", cursor.calls, cursor.closed, err)
	}
}
