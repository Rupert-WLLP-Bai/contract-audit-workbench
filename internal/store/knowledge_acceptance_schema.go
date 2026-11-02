// File metadata (manifest mirror):
// Purpose: knowledgeAcceptanceSchemaSQL extends the execution-owned, append-only run snapshot. Existing rows remain NULL so their serialized identity and hashes do not change. Register this… Related `store` operations include knowledgeAcceptanceSchemaSQL.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-28
// Updated: 2026-10-28
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

// knowledgeAcceptanceSchemaSQL extends the execution-owned, append-only run
// snapshot. Existing rows remain NULL so their serialized identity and hashes
// do not change. Register this after review_run_snapshots is created; its
// existing tenant RLS and execution read/insert grants apply to the new column.
const knowledgeAcceptanceSchemaSQL = `
ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS governed_knowledge bytea;
`
