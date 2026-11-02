// File metadata (manifest mirror):
// Purpose: EvaluationProvenanceSchemaSQL adds exact verified fact citations to new canonical evaluations. Historical rows remain NULL, preserving their output hashes and reports. The… Related `store` operations include EvaluationProvenanceSchemaSQL.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-10-28
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

// EvaluationProvenanceSchemaSQL adds exact verified fact citations to new
// canonical evaluations. Historical rows remain NULL, preserving their output
// hashes and reports. The existing evaluation RLS and grants cover the column.
const EvaluationProvenanceSchemaSQL = `
ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS evidence jsonb;
`
