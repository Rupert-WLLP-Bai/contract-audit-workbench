// File metadata (manifest mirror):
// Purpose: Validates and groups Legal applicability fact rows in stream order before reading the next row or trailing database error.
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
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/legal/model"
)

type FactFamilyRow struct {
	Head int64
	Fact model.LegalApplicabilityFactVersion
}
type FactFamilyCursor interface {
	Next() (FactFamilyRow, bool, error)
	Close()
}
type FactFamilyReader interface {
	OpenFactFamilies(context.Context, RecordIdentity) (FactFamilyCursor, error)
}

// LoadFactFamilies validates each scanned row before advancing the cursor.
func LoadFactFamilies(ctx context.Context, repo FactFamilyReader, id RecordIdentity) ([]FactFamily, error) {
	rows, err := repo.OpenFactFamilies(ctx, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	families := make(map[string]*FactFamily)
	for {
		row, ok, err := rows.Next()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		fact := row.Fact
		basis, err := model.VerifyApplicabilityBasisManifest(fact.BasisRefs, fact.BasisSHA256)
		if err != nil {
			return nil, fault.Conflict("法律适用事实材料依据摘要不匹配")
		}
		fact.BasisRefs = basis
		fact.ValidFrom = fact.ValidFrom.UTC().Truncate(time.Microsecond)
		fact.CreatedAt = fact.CreatedAt.UTC().Truncate(time.Microsecond)
		fact.TenantID, fact.OrgID, fact.CaseID = id.TenantID, id.OrgID, id.CaseID
		family := families[fact.Dimension]
		if family == nil {
			family = &FactFamily{Dimension: fact.Dimension, Head: row.Head}
			families[fact.Dimension] = family
		}
		if family.Head != row.Head {
			return nil, errors.New("legal applicability head changed within snapshot")
		}
		family.Versions = append(family.Versions, fact)
	}
	out := make([]FactFamily, 0, len(families))
	for _, family := range families {
		out = append(out, *family)
	}
	return out, nil
}
