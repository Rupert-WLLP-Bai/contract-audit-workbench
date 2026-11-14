// File metadata (manifest mirror):
// Purpose: Builds authorized contract ledger pages through Contract read ports and per-row current access checks.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"time"

	"contractaudit/internal/contract/ledger"
	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

type LedgerReader interface {
	Read(context.Context, string, ledger.Filter, bool, time.Time, string, int) ([]ledger.Row, error)
}

type LedgerAccess interface {
	CanRead(context.Context, clmcontract.Aggregate) bool
}

// ListContractLedger validates one query and fills a keyset page from rows
// authorized by the Store boundary. The SQL adapter only narrows candidates;
// authorization is checked before each row enters the response page.
func ListContractLedger(ctx context.Context, identity ledger.CursorIdentity, query ledger.Query,
	reader LedgerReader, access LedgerAccess) (ledger.Page, error) {
	limit := query.Limit
	if limit == 0 {
		limit = ledger.DefaultLimit
	}
	if limit < 1 || limit > ledger.MaxLimit {
		return ledger.Page{}, fault.Invalid("limit 必须在 1 到 100 之间")
	}
	query.Limit = limit
	if err := ledger.ValidateFilter(&query.Filter); err != nil {
		return ledger.Page{}, err
	}
	sortBy, err := ledger.ValidateSort(query.Sort)
	if err != nil {
		return ledger.Page{}, err
	}
	digest, err := ledger.FilterDigest(query.Filter, sortBy)
	if err != nil {
		return ledger.Page{}, err
	}
	before, err := ledger.DecodeCursor(query.Cursor, identity, sortBy, digest)
	if err != nil {
		return ledger.Page{}, err
	}
	page := ledger.Page{Items: []ledger.Item{}}
	afterSet, afterID := false, ""
	var afterKey, lastKey time.Time
	if before != nil {
		afterSet, afterKey, afterID = true, before.Key, before.ContractID
	}
	for {
		rows, err := reader.Read(ctx, identity.TenantID, query.Filter, afterSet, afterKey, afterID, limit+1)
		if err != nil {
			return ledger.Page{}, err
		}
		foundExtra := false
		for _, row := range rows {
			afterSet, afterKey, afterID = true, row.SortKey, row.Item.ContractID
			if !access.CanRead(ctx, row.Aggregate) {
				continue
			}
			if len(page.Items) == limit {
				foundExtra = true
				break
			}
			page.Items = append(page.Items, row.Item)
			lastKey = row.SortKey
		}
		if foundExtra {
			cursor := ledger.EncodeCursor(identity, sortBy, digest, lastKey, page.Items[len(page.Items)-1].ContractID)
			page.NextCursor, page.HasMore = &cursor, true
			break
		}
		if len(rows) < limit+1 {
			break
		}
	}
	return page, nil
}
