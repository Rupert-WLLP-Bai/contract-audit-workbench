CREATE INDEX IF NOT EXISTS findings_attempt_scope ON findings (attempt_id, run_id, seq);
CREATE INDEX IF NOT EXISTS constraint_evaluations_attempt_scope ON constraint_evaluations (attempt_id, run_id, seq);
