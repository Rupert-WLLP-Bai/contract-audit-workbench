// File metadata (manifest mirror):
// Purpose: Reads Legal-owned fact heads and bounded descending version numbers for history pages.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package postgres

import (
	"context"
	"errors"

	"contractaudit/internal/legal/app"
	"github.com/jackc/pgx/v5"
)

func (p *Applicability) ReadHead(ctx context.Context, id app.RecordIdentity, dimension string) (int64, error) {
	var head int64
	err := p.db.QueryRow(ctx, `SELECT current_version FROM case_legal_applicability_facts WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4`, id.TenantID, id.OrgID, id.CaseID, dimension).Scan(&head)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return head, err
}
func (p *Applicability) ListVersionNumbers(ctx context.Context, id app.RecordIdentity, dimension string, before int64, exclusive bool, limit int) ([]int64, error) {
	query := `SELECT version FROM case_legal_applicability_fact_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4 AND version<=$5 ORDER BY version DESC LIMIT $6`
	if exclusive {
		query = `SELECT version FROM case_legal_applicability_fact_versions WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND dimension=$4 AND version<$5 ORDER BY version DESC LIMIT $6`
	}
	rows, err := p.db.Query(ctx, query, id.TenantID, id.OrgID, id.CaseID, dimension, before, limit)
	if err != nil {
		return nil, err
	}
	versions := make([]int64, 0, limit)
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return nil, err
		}
		versions = append(versions, version)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return versions, nil
}
