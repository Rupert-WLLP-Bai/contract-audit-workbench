package store

// knowledgeAcceptanceSchemaSQL extends the execution-owned, append-only run
// snapshot. Existing rows remain NULL so their serialized identity and hashes
// do not change. Register this after review_run_snapshots is created; its
// existing tenant RLS and execution read/insert grants apply to the new column.
const knowledgeAcceptanceSchemaSQL = `
ALTER TABLE review_run_snapshots ADD COLUMN IF NOT EXISTS governed_knowledge bytea;
`
