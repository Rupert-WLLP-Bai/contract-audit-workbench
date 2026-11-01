-- 0020: read-path indexes for model budget reserve scanning and the narrow
-- attempt transition path. Both changes are append-only index additions; they
-- do not alter row content, scope identities or frozen outputs.

-- The reserve path counts per-minute budget under one global advisory lock, so
-- those scans must not degrade with ledger size: the COALESCE expression and
-- the NOT budget_released predicate below mirror model_budget_reserve exactly.
CREATE INDEX IF NOT EXISTS model_usage_budget_window ON model_usage (COALESCE(reserved_at, occurred_at)) WHERE NOT budget_released;
CREATE INDEX IF NOT EXISTS model_usage_tenant_budget_window ON model_usage (tenant_id, COALESCE(reserved_at, occurred_at)) WHERE NOT budget_released;

-- attempt_finalize.go reads exactly one attempt's findings and constraint
-- evaluations with `WHERE attempt_id=$1 AND run_id=$2 ORDER BY seq`, and both
-- tables are append-only. The existing scope identities on these tables lead
-- with tenant_id/org_id/case_id, so that predicate cannot use them and every
-- finalize or cancel would sequentially scan both tables. seq is the trailing
-- column so the ordered index scan satisfies ORDER BY seq without a sort.
CREATE INDEX IF NOT EXISTS findings_attempt_scope ON findings (attempt_id, run_id, seq);
CREATE INDEX IF NOT EXISTS constraint_evaluations_attempt_scope ON constraint_evaluations (attempt_id, run_id, seq);
