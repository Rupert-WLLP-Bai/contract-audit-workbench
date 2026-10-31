CREATE INDEX IF NOT EXISTS model_usage_budget_window
 ON model_usage (COALESCE(reserved_at, occurred_at)) WHERE NOT budget_released;
CREATE INDEX IF NOT EXISTS model_usage_tenant_budget_window
 ON model_usage (tenant_id, COALESCE(reserved_at, occurred_at)) WHERE NOT budget_released;
