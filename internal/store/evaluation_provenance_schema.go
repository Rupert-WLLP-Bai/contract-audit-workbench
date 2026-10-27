package store

// EvaluationProvenanceSchemaSQL adds exact verified fact citations to new
// canonical evaluations. Historical rows remain NULL, preserving their output
// hashes and reports. The existing evaluation RLS and grants cover the column.
const EvaluationProvenanceSchemaSQL = `
ALTER TABLE constraint_evaluations ADD COLUMN IF NOT EXISTS evidence jsonb;
`
