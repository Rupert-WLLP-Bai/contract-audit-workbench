import type { BusinessAgentReview } from "./business-agent-review";
import type { FrozenLegalReviewProjection } from "./legal-citations";
import type { WireSchemas } from "./wire.generated";

/** Client-side view of the review API. Null means the server omitted the field. */

export type SessionAudience = "client" | "admin";

export interface Actor {
  id: string;
  display_name: string;
  role: string;
  tenant_id: string;
  org_id: string;
  audience: SessionAudience;
}

export interface ReviewRunSummary {
  id: string;
  state: string;
  updated_at: string;
}

export interface CaseSummary {
  id: string;
  title: string;
  contract_type: string;
  document_count: number;
  pending_decisions: number;
  latest_run: ReviewRunSummary | null;
  updated_at: string;
  business_line?: string;
  scenario?: string;
  business_status?: string;
  archived_at?: string | null;
}

export type ParsedDocumentStatus = "ready" | "failed";

export interface ParsedDocumentMetadata {
  id: string;
  document_id: string;
  original_digest: string;
  parser_id: string;
  parser_version: string;
  config_hash: string;
  normalization_version: string;
  normalized_digest: string | null;
  ir_digest: string | null;
  artifact_digest: string;
  created_at: string;
  status: ParsedDocumentStatus;
}

export interface DocumentVersion {
  id: string;
  role: string;
  filename: string;
  media_type: string;
  digest: string;
  size: number;
  parse_status: string;
  parse_error: string | null;
  created_at: string;
  parsed_document: ParsedDocumentMetadata | null;
  superseded?: boolean;
}

export type MaterialPurpose = WireSchemas["MaterialProjection"]["purpose"];
export type MaterialLegacyStatus = WireSchemas["MaterialProjection"]["legacy_status"];
export type MaterialProjection = WireSchemas["MaterialProjection"];
export type MaterialVersionProjection = WireSchemas["MaterialVersionProjection"];

// Older servers may omit the suggested initial selection from the response.
export type MaterialSelectionProjection = Omit<
  WireSchemas["MaterialSelectionProjection"],
  "candidate_document_ids"
> & {
  candidate_document_ids?: WireSchemas["MaterialSelectionProjection"]["candidate_document_ids"];
};

export interface UploadDocumentInput {
  file: File;
  role: MaterialProjection["role"];
  intent: "add" | "replace";
  purpose?: MaterialPurpose;
  material_id?: string;
  replaces_document_id?: string;
  idempotency_key: string;
}

export interface UploadDocumentResult {
  id: string;
  material_id: string;
  version: number;
  replayed: boolean;
}

export type ActivityItem = WireSchemas["Activity"];
export type FindingCounts = WireSchemas["FindingCounts"];

export interface CaseDetail {
  id: string;
  title: string;
  contract_type: string;
  created_at: string;
  updated_at: string;
  documents: DocumentVersion[];
  /** Absent only when the connected server predates the material projection API. */
  materials?: MaterialProjection[];
  material_versions?: MaterialVersionProjection[];
  material_selection?: MaterialSelectionProjection;
  members: WorkbenchMember[];
  latest_run: ReviewRunSummary | null;
  finding_counts: FindingCounts | null;
  activity: ActivityItem[];
  revision_pending?: boolean;
  configuration_pending?: boolean;
  /** Server-derived comparison of current subject context with the latest frozen review. */
  subject_context_changed_after_latest_run?: boolean;
  business_line: string;
  scenario: string;
  business_status: string;
  generations: GenerationRecord[];
  remediations: Remediation[];
  reports: ReportRecord[];
  archives: ArchiveRecord[];
  archived_at: string | null;
}

export type TextOffset = WireSchemas["TextOffset"];

export interface BoundingBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

export type AnchorStatus =
  | "located"
  | "anchor_mismatch"
  | "unlocatable"
  | "missing";

export interface EvidenceAnchor {
  id: string;
  document_version_id: string;
  parsed_document_id: string | null;
  normalized_digest: string | null;
  quote: string;
  status: AnchorStatus;
  page: number | null;
  text_offset: TextOffset | null;
  bbox: BoundingBox | null;
  mismatch_reason: string | null;
}

// The UI displays only these fields of the server's decision history.
export type HumanDecision = Pick<
  WireSchemas["Decision"], "id" | "action" | "reason" | "actor" | "decided_at"
>;

export interface FindingSeveritySource {
  policy_id: string;
  policy_version: string;
  clause: string;
  reason: string;
}

export interface Finding {
  id: string;
  origin: string;
  domain: string;
  claim: string;
  relation: string;
  confidence: string | null;
  verification_status: FindingVerification;
  evidence: EvidenceAnchor[];
  unverified_reason: string | null;
  version: number | null;
  severity: string | null;
  severity_source: FindingSeveritySource | null;
  decisions: HumanDecision[];
}

export interface ClauseNode {
  id: string;
  heading: string;
  text_offset: TextOffset;
  page: number | null;
}

export interface ReviewDocument extends DocumentVersion {
  parsed_document_id: string | null;
  text: string | null;
  clauses: ClauseNode[];
}

export type FindingVerification = "verified" | "unverified" | "unknown";
export interface ReviewScopeIdentity {
  id: string;
  rule_set_id: string;
  rule_set_version: string;
  rule_set_hash: string;
}


export interface ExecutionAttempt {
  id: string;
  run_id: string;
  ordinal: number;
  retry_of_attempt_id: string | null;
  state: string;
  accepted_at: string | null;
  started_at: string | null;
  finished_at: string | null;
  parsed_document_ids: string[];
  input_hash: string | null;
  output_hash: string | null;
  error_code: string | null;
  review_scope?: ReviewScopeIdentity;
  history_kind: string;
  frozen_result_available?: boolean;
}

export interface ExecutionUsageSubtotals {
  input_tokens: number | null;
  output_tokens: number | null;
  total_tokens: number | null;
}

export interface ExecutionUsage {
  input_tokens: number | null;
  output_tokens: number | null;
  total_tokens: number | null;
  calls: number;
  unknown_calls: number;
  sources: string[];
  known_subtotals: ExecutionUsageSubtotals;
  cost: null;
}
export interface ExecutionCallUsage {
  source: string;
  input_tokens: number | null;
  output_tokens: number | null;
  total_tokens: number | null;
}

export interface ModelCallTraceMessage {
  role: string;
  content: string;
  truncated: boolean;
}

/** Bounded physical model call record. It is diagnostic data, not evidence. */
export interface ModelCallTrace {
  requested_model: string;
  reported_model: string;
  status: string;
  error_code: string;
  started_at: string;
  finished_at: string;
  request_sha256: string;
  request_bytes: number;
  request_messages: ModelCallTraceMessage[];
  request_truncated: boolean;
  response_sha256: string;
  response_bytes: number;
  response_text: string;
  response_truncated: boolean;
}

export interface ExecutionEvent {
  seq: number;
  attempt_id: string;
  span_id: string | null;
  parent_span_id: string | null;
  node_kind: string;
  phase: string;
  event_type: string;
  at: string | null;
  duration_ms: number | null;
  status: string | null;
  error_code: string | null;
  model_ref: string | null;
  usage: ExecutionCallUsage | null;
  trace?: ModelCallTrace | null;
}

export type AgentReviewDomain =
  | "compliance"
  | "commercial"
  | "technical"
  | "fulfillment"
  | "counterparty";
export type AgentPlannerMode =
  | "fixed_chain"
  | "deterministic_planner"
  | "model_fixture"
  | "model_live";
export type AgentReviewStatus =
  | "queued"
  | "planning"
  | "running"
  | "completed"
  | "partial"
  | "blocked_live"
  | "cancelled"
  | "failed"
  | "rejected";
export type AgentReviewCapabilityStatus =
  | "supported"
  | "completed"
  | "insufficient"
  | "partial"
  | "unknown"
  | "unsupported"
  | "not_applicable";
export type AgentReviewDomainStatus =
  | "not_requested"
  | AgentReviewCapabilityStatus;
export type AgentReviewGapStatus =
  | "open"
  | "resolved"
  | "completed"
  | "insufficient"
  | "unsupported"
  | "unknown";
export type AgentReviewCallStatus =
  | "planned"
  | "running"
  | "completed"
  | "failed"
  | "denied"
  | "cancelled"
  | "insufficient"
  | "unsupported";
export type AgentReviewRoundStatus =
  | "accepted"
  | "guard_rejected"
  | "completed"
  | "partial"
  | "no_progress"
  | "budget_exhausted"
  | "cancelled"
  | "failed";
export type AgentReviewPlanStatus =
  | "not_planned"
  | "accepted"
  | "guard_rejected"
  | "executing"
  | "completed"
  | "partial"
  | "fallback";
export type AgentReviewEvidenceVerification =
  | "verified"
  | "typed_semantic_and_frozen_quote_verified"
  | "unknown"
  | "rejected";
export type AgentReviewAnchorStatus = "located" | "unlocatable";
export type AgentReviewEventKind =
  | "planning"
  | "guard"
  | "tool_call"
  | "tool_started"
  | "tool_observed"
  | "tool_completed"
  | "tool_failed"
  | "knowledge_read_started"
  | "knowledge_read_completed"
  | "knowledge_read_failed"
  | "run_cancelled"
  | "evidence"
  | "budget"
  | "terminal"
  | "authorization"
  | "input"
  | "gap_unsupported"
  | "plan_rejected"
  | "planner_fallback"
  | "plan"
  | "plan_accepted"
  | "tool_failed"
  | "tool_completed";
export type AgentReviewRelation =
  | "compatible"
  | "conflict"
  | "insufficient"
  | "not_applicable";

export interface AgentReviewSourceSnapshot {
  document_ids: string[];
  parsed_document_ids: string[];
  parse_selection_hash: string;
  parser_configuration_id: string;
}

export interface AgentReviewRulePackIdentity {
  id: string;
  version: string;
  content_sha256: string;
}

export interface AgentReviewPlanner {
  identity: string;
  configuration_id: string;
  model_configuration_id: string | null;
  prompt_version: string;
  prompt_sha256: string;
  schema_version: string;
  runtime_version: string;
  tool_catalog_version: string;
  fallback_used: boolean;
  fallback_code: string | null;
  limits: {
    max_rounds: number;
    max_calls: number;
    max_duration_ms: number;
    max_evidence_bytes: number;
  };
}

export interface AgentReviewDomainScope {
  domain: AgentReviewDomain;
  capability_ids: string[];
  source_ids: string[];
}

export interface AgentReviewCapability {
  capability_id: string;
  mandatory: boolean;
  status: AgentReviewCapabilityStatus;
  relation: AgentReviewRelation | null;
  reason_code: string;
  evidence_ids: string[];
  canonical_evaluation_ids: string[];
}

export interface AgentReviewDomainCoverage {
  domain: AgentReviewDomain;
  status: AgentReviewDomainStatus;
  capabilities: AgentReviewCapability[];
}

export interface AgentReviewGap {
  gap_id: string;
  domain: AgentReviewDomain;
  capability_id: string;
  mandatory: boolean;
  status: AgentReviewGapStatus;
  reason_code: string;
  evidence_ids: string[];
}

export interface AgentReviewPlannedCall {
  call_id: string;
  tool_id: string;
  tool_version: string;
  domain: AgentReviewDomain;
  capability_id: string;
  gap_id: string;
  status: AgentReviewCallStatus;
  reason_code: string | null;
  evidence_ids: string[];
}

export interface AgentReviewPlanRound {
  round: number;
  status: AgentReviewRoundStatus;
  gap_ids: string[];
  calls: AgentReviewPlannedCall[];
  new_evidence_ids: string[] | null;
  resolved_gap_ids: string[] | null;
  termination_code: string | null;
}

export interface AgentReviewPlan {
  status: AgentReviewPlanStatus;
  rejection_code: string | null;
  calls: AgentReviewPlannedCall[];
  rounds: AgentReviewPlanRound[];
}

export interface AgentReviewEvidence {
  evidence_id: string;
  semantic_type: string;
  value: string;
  unit: string | null;
  currency: string | null;
  verification: AgentReviewEvidenceVerification;
  anchor: {
    id: string;
    document_id: string;
    parsed_document_id: string;
    normalized_digest: string;
    quote: string;
    status: AgentReviewAnchorStatus;
    start: number;
    end: number;
    reason: string | null;
    page: number | null;
    element_id: string | null;
  };
}

export interface AgentReviewEvent {
  event_id?: string;
  sequence: number;
  round: number;
  kind: AgentReviewEventKind;
  tool_id?: string;
  gap_id?: string;
  call_id: string | null;
  domain: AgentReviewDomain | null;
  code: string;
  occurred_at?: string | null;
  duration_ms: number;
}

/** One validated tool result. It is a reported claim, not a conclusion. */
export interface AgentReviewObservation {
  round: number;
  call_id: string;
  gap_id: string;
  tool_id: string;
  reported_relation: string;
  reason_code?: string;
  summary: string;
  claim_status: string;
  fact_ids: string[];
  located_evidence_ids: string[];
  truncated: boolean;
}

/** One bounded span read from frozen governed knowledge. */
export interface AgentReviewKnowledgeRead {
  knowledge_id: string;
  version_id: string;
  content_sha256: string;
  start: number;
  end: number;
  text_sha256: string;
  text: string;
}

export type AgentReviewAdvisoryStatus =
  | "completed"
  | "unavailable"
  | "not_applicable"
  | "model_mismatch"
  | "invalid_response"
  | "budget_exhausted"
  | "persistence_failed"
  | "config_mismatch";

export type AgentReviewAdvisoryBand =
  | "relevant"
  | "uncertain"
  | "not_relevant"
  | "unknown";

export type AgentReviewAdvisoryMode = "advisory" | "fixture";

export type AgentReviewAdvisoryProvider =
  | "model-gateway"
  | "local-fixture"
  | "direct";

export interface AgentReviewKnowledgeAdvisoryCandidate {
  knowledge_id: string;
  version_id: string;
  content_sha256: string;
  alias: string;
  probability: number | null;
  band: AgentReviewAdvisoryBand;
  rank_before: number;
  rank_after: number;
  reason: string | null;
}

/** Suggestion-only evidence for one governed knowledge rerank. */
export interface AgentReviewKnowledgeAdvisory {
  mode: AgentReviewAdvisoryMode;
  feature: "knowledge_rerank";
  provider: AgentReviewAdvisoryProvider;
  configuration_id: string;
  model_requested: string | null;
  model_observed: string | null;
  question_set_version: string;
  egress: "minimized";
  request_sha256: string | null;
  judgment_id: string | null;
  status: AgentReviewAdvisoryStatus;
  error_code: string | null;
  truncated: boolean;
  candidates: AgentReviewKnowledgeAdvisoryCandidate[];
}

/**
 * The diagnostic projection of governed knowledge selection. Only fields used
 * to explain the advisory are typed here; candidate identities and provider
 * input stay in the frozen audit record and are not rendered as conclusions.
 */
export interface AgentReviewKnowledgeSelection {
  snapshot_sha256: string;
  algorithm: string;
  policy_status: string;
  policy_reason: string;
  advisory: AgentReviewKnowledgeAdvisory | null;
}

export interface AgentReview {
  schema_version: "five-domain-review/v1";
  attempt_id: string;
  status: AgentReviewStatus;
  mode: AgentPlannerMode;
  source_snapshot: AgentReviewSourceSnapshot | null;
  rulepack: AgentReviewRulePackIdentity | null;
  knowledge_selection: AgentReviewKnowledgeSelection | null;
  planner: AgentReviewPlanner;
  scope: AgentReviewDomainScope[];
  domains: AgentReviewDomainCoverage[];
  gaps: AgentReviewGap[];
  plan: AgentReviewPlan;
  evidence: AgentReviewEvidence[];
  usage: {
    planner_calls: number;
    input_tokens: number | null;
    output_tokens: number | null;
    total_tokens: number | null;
  };
  unresolved_gap_ids: string[];
  events: AgentReviewEvent[];
  observations?: AgentReviewObservation[];
  knowledge_reads?: AgentReviewKnowledgeRead[];
}

export interface Execution {
  schema_version: "audit-execution/v1";
  attempt: ExecutionAttempt;
  attempts: ExecutionAttempt[];
  events: ExecutionEvent[];
  agent_review: AgentReview | null;
  usage: ExecutionUsage | null;
  all_attempts_usage: ExecutionUsage | null;
  duration_ms: number | null;
  first_verified_finding_ms: number | null;
  queue_wait_ms: number | null;
}

export interface ReviewAttempts {
  attempt: ExecutionAttempt;
  attempts: ExecutionAttempt[];
}

export interface ReviewDiagnostics {
  case_id: string;
  run_id: string;
  execution: Execution | null;
  snapshot: RunSnapshot | null;
  audit_events: AuditEvent[];
}


export interface AcceptedReview {
  id: string;
  attempt_id: string;
  job_id?: string;
}

export interface ExecutionOperationalMetadata {
  total_attempts: number;
  running_attempts: number;
  completed_attempts: number;
  partial_attempts: number;
  failed_attempts: number;
  cancelled_attempts: number;
  model_calls: number;
  unknown_usage_calls: number;
  queue_depth: number | null;
}

export interface ReviewRun extends ReviewRunSummary {
  case_id: string;
  started_at: string | null;
  model_ref: string | null;
  partial_reason: string | null;
  failure_message: string | null;
}

export interface ReviewWorkspace {
  run: ReviewRun;
  documents: ReviewDocument[];
  findings: Finding[];
  review_attempts: ReviewAttempts | null;
  agent_review?: BusinessAgentReview | null;
  execution: Execution | null;
  snapshot?: RunSnapshot | null;
  evaluations?: ConstraintEvaluation[];
  audit_events: AuditEvent[];
  business_events?: BusinessAuditEvent[];
  previous_run_id?: string | null;
  revisions?: RevisionComparison[];
  revision_comparison_reason?: string | null;
  legal_authorities?: FrozenLegalReviewProjection;
}

export type DecisionAction = WireSchemas["DecisionRequest"]["action"];

export type DecisionInput = Omit<WireSchemas["DecisionRequest"], "expected_version"> & {
  expectedVersion: WireSchemas["DecisionRequest"]["expected_version"];
};

export interface RunSnapshot {
  document_ids: string[];
  rule_set_hash: string;
  playbook_hash: string;
  model_ref: string | null;
  review_scope?: ReviewScopeIdentity;
  comparator_ref: string | null;
}

export interface ConstraintEvaluation {
  id: string;
  domain: string;
  relation: string;
  rule_version: string;
  reason: string;
  rule_id: string;
  capability_id: string;
  object_refs: string[];
  finding_id: string | null;
  reason_code: string;
  fact_ids: string[];
}

export interface RevisionComparison {
  mark: string;
  finding_id: string | null;
  previous_finding_id: string | null;
  domain: string;
  relation: string;
  claim: string;
  prior_decision: HumanDecision | null;
}

export interface SettingsUser {
  id: string;
  display_name: string;
  role: string;
}

export interface OperatorConnectorInfo {
  connector_id: string;
  source_system: string | null;
  allowed_objects: string[];
}

export interface OperatorAuditSettings {
  enabled: boolean;
  connectors: OperatorConnectorInfo[];
}

export interface SettingsView {
  model: { status: string; label: string };
  queue: { depth: number | null; oldest_wait_ms: number | null };
  redaction_policy: string;
  users: SettingsUser[];
  execution?: ExecutionOperationalMetadata | null;
  operator_audit?: OperatorAuditSettings | null;
}

export interface LLMEndpointView {
  configured: boolean;
  name: string;
  protocol: string;
  base_url: string;
  models: string[];
  review_model: string;
  ocr_model: string;
  has_api_key: boolean;
  revision: number;
  updated_at: string;
  updated_by: string;
}

export interface LLMProbeResult {
  models: string[];
  latency_ms: number;
}

export interface LLMEndpointInput {
  name: string;
  protocol: string;
  base_url: string;
  models: string[];
  api_key?: string;
  clear_api_key?: boolean;
  expected_revision?: number;
}

export type LLMModelSelectionInput = WireSchemas["LLMModelSelectionInput"];
export interface GenerationRecord {
  id: string;
  document_id: string;
  material_id?: string;
  version?: number;
  replayed?: boolean;
  template_id: string;
  template_version: string;
  digest: string;
  created_at: string;
}

export interface Remediation {
  id: string;
  case_id: string;
  run_id: string;
  attempt_id: string;
  finding_ids: string[];
  assignee_id: string;
  due_at: string;
  measure: string;
  status: string;
  version: number;
  document_ids: string[];
  reason: string;
  verification_run_id: string | null;
  verification_attempt_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface ReportRecord {
  id: string;
  version: number;
  run_id: string;
  attempt_id: string;
  input_hash: string;
  output_hash: string;
  decision_snapshot_hash: string;
  digest: string;
  created_at: string;
}

export interface ArchiveRecord {
  id: string;
  case_id: string;
  report_id: string;
  reason: string;
  archived_at: string;
}

export interface TemplateField {
  key: string;
  label: string;
  required: boolean;
  type: string;
}

export interface Template {
  id: string;
  name: string;
  version: string;
  digest: string;
  fields: TemplateField[];
}

export interface BusinessAuditEvent {
  event_id: string;
  run_id: string;
  actor: string;
  seq: number;
  event_type: string;
  correlation_ids: string[];
}

export interface AuditEvent {
  event_id: string;
  run_id: string;
  actor: string;
  seq: number;
  event_type: string;
  prev_hash: string;
  hash: string;
  correlation_ids: string[];
}

export interface ReviewHistoryEntry {
  runId: string;
  workspace: ReviewWorkspace;
}

// The UI always supplies intent, assignee and due date even where the wire
// schema permits them to be omitted.
export type GenerationInput = WireSchemas["CreateGenerationRequest"] & {
  intent: NonNullable<WireSchemas["CreateGenerationRequest"]["intent"]>;
};
export type RemediationCreateInput = WireSchemas["RemediationInput"] &
  Required<Pick<WireSchemas["RemediationInput"], "assignee_id" | "due_at">>;
export type RemediationSubmitInput = WireSchemas["RemediationSubmitInput"];
export type RemediationVerifyInput = WireSchemas["RemediationVerifyInput"];
export type ReportCreateInput = WireSchemas["CreateReportRequest"];
export type ArchiveInput = WireSchemas["ArchiveRequest"];
export type WorkbenchPeriod = "day" | "week" | "month";

export type CaseProfile = WireSchemas["CaseProfile"];

export type SubjectRole =
  | "customer"
  | "supplier"
  | "contractor"
  | "service_provider"
  | "other";
export type SubjectVerificationState = "pending_verification" | "verified";
export type SubjectBindingState =
  | "active"
  | "inactive"
  | "pending_confirmation";
export type SubjectSourceKind =
  | "user_asserted"
  | "official_identifier"
  | "authorized_correction";
export type SubjectClaimSourceKind = SubjectSourceKind | "synthetic_controlled_snapshot";
export type SubjectVerificationScope = "synthetic_controlled" | "official_controlled" | "ordinary";
export type SubjectAliasKind = "legal" | "trade" | "former" | "other";

export interface SubjectPrincipal {
  user_id: string;
  issuer: string;
  subject: string;
}

export interface SubjectAlias {
  value: string;
  kind: SubjectAliasKind;
  source_note?: string;
}

export interface SubjectIdentifierInput {
  namespace: "cn.gov.samr.uscc";
  identifier_type: "unified_social_credit_code";
  value: string;
}

export interface SubjectIdentifierProjection {
  namespace: "cn.gov.samr.uscc";
  identifier_type: "unified_social_credit_code";
  value?: string;
  masked_value?: string;
  verification_scope?: SubjectVerificationScope;
  verification_state: "pending_verification" | "verified";
  source_kind: SubjectClaimSourceKind;
  source_time: string | null;
  payload_hash: string;
}

export interface MaskedSubjectIdentifier extends SubjectIdentifierProjection {
  masked_value: string;
}

export interface SubjectIdentityClaim extends SubjectIdentifierProjection {
}

export interface SubjectCreateInput {
  display_name: string;
  legal_name?: string;
  identifiers?: SubjectIdentifierInput[];
  aliases?: SubjectAlias[];
}

export interface SubjectSummary {
  subject_id: string;
  current_version: number;
  display_name: string;
  legal_name: string | null;
  verification_state: SubjectVerificationState | "inactive";
  verification_scope?: SubjectVerificationScope;
  aliases: SubjectAlias[];
  created_at: string;
  created_by: SubjectPrincipal;
  identifiers: MaskedSubjectIdentifier[];
}

export interface SubjectVersionProjection {
  subject_id: string;
  version: number;
  display_name: string;
  legal_name: string | null;
  verification_state: SubjectVerificationState;
  verification_scope?: SubjectVerificationScope;
  source_kind: SubjectSourceKind;
  basis_summary: string;
  payload_hash: string;
  identity_claims: SubjectIdentityClaim[];
  aliases: SubjectAlias[];
  created_by: SubjectPrincipal;
  created_at: string;
}

export interface SubjectDetail {
  subject_id: string;
  current_version: number;
  current: SubjectVersionProjection;
  identifiers: MaskedSubjectIdentifier[];
}

export interface SubjectCaseLinksProjection {
  items: SubjectCaseLinkProjection[];
  visible_count: number;
  next_cursor: string | null;
}

export interface SubjectCaseLinkProjection extends CaseRoleBindingProjection {
  case_title: string;
  business_status: string;
}

export interface SubjectChangeEventSummary {
  event_id: string;
  kind: "proposal_created" | "decision_approved" | "decision_rejected" | "correction_applied";
  request_id: string;
  case_id: string;
  actor: SubjectPrincipal;
  created_at: string;
}

export interface SubjectHistoryProjection {
  versions: SubjectVersionProjection[];
  events: SubjectChangeEventSummary[];
}

export interface CaseRoleBindingProjection {
  binding_id: string;
  binding_version: number;
  case_id: string;
  role: SubjectRole;
  binding_state: SubjectBindingState;
  source_kind: "user_asserted" | "legacy_label" | "authorized_correction";
  legacy_label: string | null;
  created_by: SubjectPrincipal;
  subject: SubjectVersionProjection;
  created_at: string;
}

export interface LegacySubjectProfile {
  version: number;
  customer: string | null;
  supplier: string | null;
}

export interface CaseSubjectsProjection {
  items: CaseRoleBindingProjection[];
  legacy_profile: LegacySubjectProfile | null;
  can_manage: boolean;
}

export interface CaseRoleCreateInput {
  role: SubjectRole;
  subject: SubjectCreateInput;
  legacy_binding?: {
    field: "customer" | "supplier";
    value: string;
    expected_profile_version: number;
  };
}

export interface BindExistingSubjectInput {
  role: SubjectRole;
  subject_id: string;
  expected_subject_version: number;
}

export interface CaseRoleCreateResult {
  binding_id: string;
  binding_version: number;
  subject_id: string;
  subject_version: number;
  role: SubjectRole;
  display_name: string;
  verification_state: SubjectVerificationState;
  source_kind: "user_asserted" | "legacy_label";
  legacy_label: string | null;
  binding_state: "active" | "pending_confirmation";
}

export interface BindExistingSubjectResult {
  binding_id: string;
  binding_version: number;
  subject_id: string;
  subject_version: number;
  role: SubjectRole;
  display_name: string;
  verification_state: SubjectVerificationState;
  binding_state: "active";
  created_at: string;
  source_kind: "user_asserted";
}

export interface SubjectChangeSummary {
  request_id: string;
  target_kind: "subject" | "case_subject_role";
  state: "pending_decision" | "approved" | "rejected" | "applied";
  source_case_id: string;
  target_subject_id: string;
  target_binding_id: string | null;
  expected_subject_version: number;
  expected_binding_version: number | null;
  payload_hash: string;
  proposer: SubjectPrincipal;
  proposed_by_display_name: string;
  can_decide: boolean;
  decision_blocked_reason: string | null;
  created_at: string;
}

export interface MaterialAnchorBasisInput {
  kind: "material_anchor";
  material_id: string;
  material_version: number;
  document_id: string;
  parsed_document_id: string;
  start: number;
  end: number;
  quote_digest: string;
}

export type SubjectChangeBasisInput = MaterialAnchorBasisInput;

export type SubjectChangeBasisRef =
  | (MaterialAnchorBasisInput & { filename: string; quote: string })
  | {
      kind: "controlled_source";
      snapshot_id: string;
      source_catalog_id: "synthetic.uscc.demo";
      source_version: number;
      source_digest: string;
      source_scope: "synthetic_controlled";
      captured_at: string;
      source_as_of: string;
      valid_from: string;
      valid_until: string | null;
    };

export interface SubjectSourceSnapshot {
  snapshot_id: string;
  source_catalog_id: "synthetic.uscc.demo";
  source_version: number;
  source_digest: string;
  captured_at: string;
  source_scope: "synthetic_controlled";
  record_count: number;
  label: string;
}

export interface SubjectSourceSnapshotRecord {
  snapshot_id: string;
  source_catalog_id: "synthetic.uscc.demo";
  source_version: number;
  source_digest: string;
  source_scope: "synthetic_controlled";
  captured_at: string;
  record_count: number;
  label: string;
}

export interface SubjectSourceSnapshotDetail extends SubjectSourceSnapshotRecord {
  case_id: string;
}

export interface SubjectSourceSnapshotList {
  items: SubjectSourceSnapshotRecord[];
  next_cursor: string | null;
}

export interface SubjectSourceDirectoryItem {
  source_id: "synthetic.uscc.demo";
  source_version: number;
  display_name: string;
  verification_scope: "synthetic_controlled";
}

export interface SubjectSourceDirectory {
  items: SubjectSourceDirectoryItem[];
}

export interface MaterialParsedTextPage {
  material_id: string;
  material_version: number;
  document_id: string;
  parsed_document_id: string;
  filename: string;
  original_digest: string;
  parse_digest: string;
  offset: number;
  total_runes: number;
  text: string;
  next_offset: number | null;
}

export interface MaterialParsedDocumentSummary {
  parsed_document_id: string;
  status: ParsedDocumentStatus;
  parser_id: string;
  parser_version: string;
  created_at: string;
  original_digest: string;
  normalized_digest: string | null;
}

export interface MaterialParsedDocumentList {
  material_id: string;
  material_version: number;
  document_id: string;
  items: MaterialParsedDocumentSummary[];
}

export interface SubjectSourceVerificationResult {
  request_id: string | null;
  snapshot_id: string;
  source_catalog_id: "synthetic.uscc.demo";
  source_version: number;
  source_digest: string;
  source_scope: "synthetic_controlled";
  source_as_of: string | null;
  valid_from: string | null;
  valid_until: string | null;
  match_outcome: "exact_match" | "no_match" | "ambiguous";
  state: "pending_decision" | "not_proposed";
  matched_record: { display_name: string; masked_identifier: string } | null;
}

export interface SubjectSourceSnapshotInput {
  source_catalog_id: "synthetic.uscc.demo";
  expected_source_version: number;
}

export interface SubjectSourceVerificationInput {
  subject_id: string;
  expected_subject_version: number;
}

export interface SubjectChangeDetail {
  summary: SubjectChangeSummary;
  proposed_by_display_name: string;
  can_decide: boolean;
  decision_blocked_reason: string | null;
  before: SubjectVersionProjection | CaseRoleBindingProjection;
  after: SubjectVersionProjection | CaseRoleBindingProjection;
  basis_refs: SubjectChangeBasisRef[];
  impact_etag: string;
  authorized_impact_cases: Array<{
    case_id: string;
    case_name: string;
    status: string;
  }>;
}

export interface SubjectChangeImpact {
  request_id: string;
  proposed_by_display_name: string;
  can_decide: boolean;
  decision_blocked_reason: string | null;
  target_version: number;
  payload_hash: string;
  before: SubjectVersionProjection | CaseRoleBindingProjection;
  after: SubjectVersionProjection | CaseRoleBindingProjection;
  basis_refs: SubjectChangeBasisRef[];
  impact_etag: string;
  visible_affected_count: number;
  affected_cases: Array<{
    case_id: string;
    case_name: string;
    status: string;
  }>;
}

export interface SubjectIdentityChangePatch {
  display_name?: string;
  legal_name?: string;
  identity_claims?: SubjectIdentifierInput[];
  aliases?: SubjectAlias[];
}

export interface CaseRoleChangePatch {
  subject_id?: string;
  expected_new_subject_version?: number;
  role?: SubjectRole;
  binding_state?: SubjectBindingState;
}

interface SubjectChangeProposalBase {
  expected_subject_version: number;
  expected_binding_version: number | null;
  basis_note: string;
  basis_refs: SubjectChangeBasisInput[];
}

export type SubjectChangeProposalInput = SubjectChangeProposalBase & (
  | {
      target: { kind: "subject"; subject_id: string; binding_id: null };
      patch: SubjectIdentityChangePatch;
    }
  | {
      target: { kind: "case_subject_role"; subject_id: string; binding_id: string };
      patch: CaseRoleChangePatch;
    }
);

export interface SubjectChangeProposalResult {
  request_id: string;
  target_kind: "subject" | "case_subject_role";
  expected_subject_version: number;
  expected_binding_version: number | null;
  payload_hash: string;
  state: "pending_decision";
  proposed_by_display_name: string;
  can_decide: boolean;
  decision_blocked_reason: string | null;
}

export interface SubjectChangeDecisionInput {
  decision: "approve" | "reject";
  expected_subject_version: number;
  expected_binding_version: number | null;
  expected_payload_hash: string;
  expected_impact_etag: string;
  reason: string;
}

export interface SubjectChangeDecisionResult {
  request_id: string;
  decision: "approved" | "rejected";
  applied_subject_version: number | null;
  binding_versions_appended: number;
}

export type WorkbenchTask = WireSchemas["WorkbenchTask"];

export interface CaseRelation {
  id: string;
  case_id: string;
  related_case_id: string;
  reason: string;
  created_by: string;
  created_at: string;
}

export interface Knowledge {
  id: string;
  version_id: string;
  title: string;
  version: string;
  body: string;
  source_type: WireSchemas["KnowledgeInput"]["source_type"];
  effective_at: string | null;
  created_by: string;
  created_at: string;
}

export interface WorkbenchMember {
  id: string;
  display_name: string;
  role: string;
  tenant_id: string;
  org_id: string;
}

export interface WorkbenchCase extends CaseSummary {
  profile: CaseProfile | null;
  severity: string | null;
  progress: number | null;
}

export interface WorkbenchRemediation extends Remediation {
  case_title: string;
}

export interface WorkbenchReport extends ReportRecord {
  case_id: string;
  case_title: string;
}

export interface WorkbenchDocument extends DocumentVersion {
  case_id: string;
  case_title: string;
}

export interface WorkbenchRun {
  id: string;
  case_id: string;
  case_title: string;
  state: string;
  created_at: string;
  updated_at: string;
  attempt_id: string | null;
  started_at: string | null;
  finished_at: string | null;
  usage: ExecutionUsage | null;
}

export interface WorkbenchAuditEvent {
  id: string;
  case_id: string | null;
  case_title: string | null;
  actor_id: string;
  actor_name: string;
  action: string;
  at: string;
  summary: string;
}

export interface WorkbenchDashboard {
  visible_cases: number;
  my_pending_tasks: number;
  overdue_tasks: number;
  pending_review_cases: number;
  remediation_cases: number;
  completed_reviews: number;
  average_duration_ms: number | null;
  progress: { status: string; count: number }[];
  risks: {
    severity: "high" | "medium" | "low" | "info" | "unrated";
    count: number;
  }[];
}

export interface WorkbenchSnapshot {
  schema_version: "workbench/v1";
  generated_at: string;
  period: WorkbenchPeriod;
  dashboard: WorkbenchDashboard;
  cases: WorkbenchCase[];
  profiles: CaseProfile[];
  tasks: WorkbenchTask[];
  relations: CaseRelation[];
  knowledge: Knowledge[];
  members: WorkbenchMember[];
  remediations: WorkbenchRemediation[];
  reports: WorkbenchReport[];
  documents: WorkbenchDocument[];
  runs: WorkbenchRun[];
  audit: WorkbenchAuditEvent[];
  templates: Template[];
  model: {
    name: string;
    ocr_name: string;
    status: "configured" | "unconfigured";
    label: string;
  };
  tools: {
    id: string;
    name: string;
    description: string;
    capabilities: string[];
    calls: number;
    last_status: string | null;
  }[];
}

export type CaseProfileInput = WireSchemas["CaseProfileInput"];
// The task-create UI does not send the optional initial expected_version.
export type WorkbenchTaskInput = Omit<WireSchemas["WorkbenchTaskInput"], "expected_version">;
export type WorkbenchTaskUpdateInput = WireSchemas["WorkbenchTaskUpdateInput"];
export type CaseRelationInput = WireSchemas["CaseRelationInput"];
export type KnowledgeInput = WireSchemas["KnowledgeInput"] &
  Required<Pick<WireSchemas["KnowledgeInput"], "effective_at">>;
