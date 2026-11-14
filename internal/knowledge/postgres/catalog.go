// File metadata (manifest mirror):
// Purpose: Reads the original global published policy summary through a Query-only capability for Platform tenant configuration.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-13
// Updated: 2026-11-13
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package postgres

import (
	"context"

	"contractaudit/internal/knowledge/app"

	"github.com/jackc/pgx/v5"
)

// Queryer is the read-only capability needed for Platform's exact policy
// summary. The caller supplies its existing transaction-bound query surface.
type Queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type CatalogReader struct {
	query Queryer
}

func NewCatalogReader(query Queryer) *CatalogReader {
	return &CatalogReader{query: query}
}

var _ app.PublishedGlobalPolicyReader = (*CatalogReader)(nil)

func (r *CatalogReader) ListPublishedGlobalPolicies(ctx context.Context, tenant string) ([]app.PublishedGlobalPolicySummary, error) {
	out := []app.PublishedGlobalPolicySummary{}
	rows, err := r.query.Query(ctx, `SELECT DISTINCT ON (v.knowledge_id) v.knowledge_id,v.version_id,v.content_sha256,v.display_version
		FROM governed_knowledge_versions v JOIN governed_knowledge_state state USING(tenant_id,knowledge_id,version_id)
		JOIN governed_knowledge_pointers pointer USING(tenant_id,knowledge_id,scope_key)
		WHERE v.tenant_id=$1 AND v.kind='policy' AND v.scope_org_id IS NULL AND v.scope_case_id IS NULL
		AND state.status='published' AND pointer.active ORDER BY v.knowledge_id,v.created_at DESC`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item app.PublishedGlobalPolicySummary
		if err := rows.Scan(&item.KnowledgeID, &item.VersionID, &item.ContentSHA256, &item.DisplayVersion); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
