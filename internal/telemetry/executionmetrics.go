package telemetry

// Metric dimensions are a closed projection of server-owned execution stages
// and Eino component kinds. The original metadata remains on the trace; new
// stages must be deliberately added here before they get their own series.
func metricPhase(value string) string {
	switch value {
	case "audit_review_execution_v1", "eino_component",
		"input_preparation", "deterministic_rules", "model_review", "citation_verification",
		"single_contract_rules", "agent_review", "authorized_input", "fact_extraction",
		"tool_evidence", "rulepack", "evidence_guard", "persistence",
		"verified_finding", "model_request", "evidence_tool", "agent_review_result",
		"acceptance", "execution", "terminal_persistence", "cancellation", "restart_recovery",
		"authorization", "input", "gap_unsupported", "run_cancelled", "plan",
		"plan_rejected", "planner_fallback", "plan_accepted", "tool_started",
		"tool_observed", "tool_failed", "tool_completed", "knowledge_read_started",
		"knowledge_read_failed", "knowledge_read_completed":
		return value
	default:
		return "other"
	}
}

func metricNodeKind(value string) string {
	switch value {
	case "attempt", "phase", "model_call", "citation_verification", "agent_review", "tool_call", "eino",
		"Unknown", "Graph", "Workflow", "Chain", "Passthrough", "ToolsNode", "AgenticToolsNode", "Lambda",
		"ChatTemplate", "AgenticChatTemplate", "ChatModel", "AgenticModel", "Embedding", "Indexer",
		"Retriever", "Loader", "DocumentTransformer", "Tool":
		return value
	default:
		return "other"
	}
}

func metricStatus(value string) string {
	switch value {
	case "accepted", "running", "completed", "partial", "failed", "cancelled", "interrupted", "verified", "denied", "skipped":
		return value
	default:
		return "other"
	}
}
