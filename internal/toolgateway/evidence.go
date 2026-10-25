package toolgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/store"
	"contractaudit/internal/telecom"
)

type toolArguments struct {
	caseID       string
	runID        string
	attemptID    string
	capabilityID string
	deadline     time.Time
}

type evidencePayload struct {
	SchemaVersion string         `json:"schema_version"`
	ToolVersion   string         `json:"tool_version"`
	CaseID        string         `json:"case_id"`
	RunID         string         `json:"run_id"`
	AttemptID     string         `json:"attempt_id"`
	CapabilityID  string         `json:"capability_id"`
	Status        string         `json:"status"`
	Facts         []evidenceFact `json:"facts"`
}

type evidenceFact struct {
	RuleID            string             `json:"rule_id"`
	ObjectRef         string             `json:"object_ref"`
	Value             string             `json:"value"`
	Unit              string             `json:"unit"`
	MeasurementWindow string             `json:"measurement_window,omitempty"`
	TimeZone          string             `json:"time_zone,omitempty"`
	Calendar          string             `json:"calendar,omitempty"`
	OutageDefinition  string             `json:"outage_definition,omitempty"`
	Exclusions        []string           `json:"exclusions,omitempty"`
	Basis             string             `json:"basis,omitempty"`
	Citations         []evidenceCitation `json:"citations"`
}

type evidenceCitation struct {
	DocumentID           string `json:"document_id"`
	Filename             string `json:"filename"`
	DocumentRole         string `json:"document_role"`
	OriginalDigest       string `json:"original_digest"`
	ParsedDocumentID     string `json:"parsed_document_id"`
	ParserID             string `json:"parser_id"`
	ParserVersion        string `json:"parser_version"`
	ConfigHash           string `json:"config_hash"`
	NormalizationVersion string `json:"normalization_version"`
	NormalizedDigest     string `json:"normalized_digest"`
	ArtifactDigest       string `json:"artifact_digest"`
	SourceVersion        string `json:"source_version"`
	SourceDigest         string `json:"source_digest"`
	Start                int    `json:"start"`
	End                  int    `json:"end"`
	Quote                string `json:"quote"`
}

type frozenSource struct {
	document store.Document
	parsed   *store.ParsedDocument
	text     string
}

var capabilityRuleIDs = map[string]string{
	"dedicated_line.minimum_bandwidth":    "ENT-TECH-BW-001",
	"dedicated_line.monthly_availability": "ENT-TECH-SLA-001",
	"payment.advance_ratio.ceiling":       "ENT-COM-ADV-001",
}

func parseToolArguments(raw json.RawMessage) (toolArguments, *rpcFailure) {
	fields, err := decodeJSONObject(raw)
	if err != nil {
		return toolArguments{}, invalidParams("arguments must be one JSON object.")
	}
	for key := range fields {
		switch key {
		case "case_id", "run_id", "attempt_id", "capability_id", "deadline":
		default:
			return toolArguments{}, invalidParams("arguments contain an unsupported field.")
		}
	}
	var args toolArguments
	args.caseID, err = stringField(fields, "case_id")
	if err != nil || args.caseID == "" {
		return toolArguments{}, invalidParams("All evidence selection fields are required strings.")
	}
	args.runID, err = stringField(fields, "run_id")
	if err != nil || args.runID == "" {
		return toolArguments{}, invalidParams("All evidence selection fields are required strings.")
	}
	args.attemptID, err = stringField(fields, "attempt_id")
	if err != nil || args.attemptID == "" {
		return toolArguments{}, invalidParams("All evidence selection fields are required strings.")
	}
	args.capabilityID, err = stringField(fields, "capability_id")
	if err != nil || args.capabilityID == "" {
		return toolArguments{}, invalidParams("All evidence selection fields are required strings.")
	}
	if !validIdentifier(args.caseID) || !validIdentifier(args.runID) || !validIdentifier(args.attemptID) {
		return toolArguments{}, invalidParams("Case, run, and attempt identities must be bounded identifiers.")
	}
	if _, ok := capabilityRuleIDs[args.capabilityID]; !ok {
		return toolArguments{}, invalidParams("Capability is not supported by this tool version.")
	}
	deadlineText, err := stringField(fields, "deadline")
	if err != nil || len(deadlineText) > 64 {
		return toolArguments{}, invalidParams("A bounded RFC3339 deadline is required.")
	}
	deadline, err := time.Parse(time.RFC3339Nano, deadlineText)
	now := time.Now()
	if err != nil || !deadline.After(now) || deadline.After(now.Add(time.Duration(maxCallDeadline)*time.Second)) {
		return toolArguments{}, &rpcFailure{status: http.StatusBadRequest, code: codeInvalidParams, message: "Deadline must be in the future and no more than 60 seconds away.", data: map[string]string{"code": "invalid_deadline"}}
	}
	args.deadline = deadline
	return args, nil
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for i := range value {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

// logStoreFailure keeps the raw storage error in the server log and never in the
// client response, so an operator can see the SQLSTATE behind a 500 without the
// caller learning anything about the store.
func logStoreFailure(operation string, err error) {
	if err == nil {
		return
	}
	message := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(message) > 400 {
		message = message[:400]
	}
	log.Printf("tool gateway %s failed: %s", operation, message)
}

func (h *handler) callFrozenEvidence(parent context.Context, user accounts.User, args toolArguments) ([]byte, *rpcFailure) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithDeadline(parent, args.deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, storeFailure(ctx, err)
	}

	workspace, err := h.store.WorkspaceAttempt(user, args.caseID, args.runID, args.attemptID)
	if err != nil {
		logStoreFailure("workspace_attempt", err)
		return nil, storeFailure(ctx, err)
	}
	if failure := validateWorkspaceBinding(user, args, workspace); failure != nil {
		return nil, failure
	}
	if workspace.Case.BusinessLine != "ENT" || workspace.Case.Scenario != "telecom_dedicated_line" || workspace.Case.ContractType != "service" {
		return nil, &rpcFailure{status: http.StatusUnprocessableEntity, code: codeInvalidParams, message: "This capability is only available for enterprise dedicated-line service cases.", data: map[string]string{"code": "unsupported_case_scope"}}
	}
	if err := ctx.Err(); err != nil {
		return nil, storeFailure(ctx, err)
	}

	callID, err := h.store.ReserveToolCall(ctx, user, store.ToolCallRequest{
		CaseID: args.caseID, RunID: args.runID, AttemptID: args.attemptID,
		CapabilityID: args.capabilityID, ToolVersion: ToolVersion, Deadline: args.deadline,
	})
	if err != nil {
		logStoreFailure("reserve_tool_call", err)
		if callID != "" {
			if finishErr := h.finishCall(user, callID, completionStatus(parent, ctx), 0); finishErr != nil {
				return nil, &rpcFailure{status: http.StatusServiceUnavailable, code: codeInternalError, message: "Tool-call accounting could not be completed."}
			}
		}
		return nil, storeFailure(ctx, err)
	}
	if callID == "" {
		return nil, &rpcFailure{status: http.StatusInternalServerError, code: codeInternalError, message: "Gateway storage returned an invalid reservation."}
	}

	status := "failed"
	var outputBytes int64
	finish := func() *rpcFailure {
		if failure := h.finishCall(user, callID, status, outputBytes); failure != nil {
			return &rpcFailure{status: http.StatusServiceUnavailable, code: codeInternalError, message: "Tool-call accounting could not be completed."}
		}
		return nil
	}
	fail := func(failure *rpcFailure) ([]byte, *rpcFailure) {
		status = completionStatus(parent, ctx)
		if finishFailure := finish(); finishFailure != nil {
			return nil, finishFailure
		}
		return nil, failure
	}

	payload, err := buildEvidencePayload(ctx, user, args, workspace)
	if err != nil {
		return fail(evidenceFailure(ctx, err))
	}
	if err := ctx.Err(); err != nil {
		return fail(storeFailure(ctx, err))
	}

	current, err := h.store.WorkspaceAttempt(user, args.caseID, args.runID, args.attemptID)
	if err != nil {
		logStoreFailure("workspace_recheck", err)
		return fail(storeFailure(ctx, err))
	}
	if failure := validateWorkspaceBinding(user, args, current); failure != nil {
		return fail(failure)
	}
	if workspaceSignature(workspace) != workspaceSignature(current) {
		return fail(&rpcFailure{status: http.StatusConflict, code: codeInternalError, message: "Frozen attempt evidence changed during the read.", data: map[string]string{"code": "frozen_attempt_changed"}})
	}
	if err := ctx.Err(); err != nil {
		return fail(storeFailure(ctx, err))
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return fail(&rpcFailure{status: http.StatusInternalServerError, code: codeInternalError, message: "Evidence result could not be serialized."})
	}
	if len(encoded) > maxToolResult {
		return fail(&rpcFailure{status: http.StatusRequestEntityTooLarge, code: codeInternalError, message: "Evidence result exceeds the configured output limit.", data: map[string]string{"code": "tool_output_too_large"}})
	}
	if err := ctx.Err(); err != nil {
		return fail(storeFailure(ctx, err))
	}
	outputBytes = int64(len(encoded))
	status = "completed"
	if failure := finish(); failure != nil {
		return nil, failure
	}
	return encoded, nil
}

func completionStatus(parent, callCtx context.Context) string {
	if parent != nil && errors.Is(parent.Err(), context.Canceled) {
		return "cancelled"
	}
	if callCtx != nil && errors.Is(callCtx.Err(), context.Canceled) {
		return "cancelled"
	}
	return "failed"
}

func validateWorkspaceBinding(user accounts.User, args toolArguments, workspace store.Workspace) *rpcFailure {
	tenantID := accounts.Tenant(user)
	if workspace.Case.ID != args.caseID || workspace.Case.TenantID != tenantID ||
		workspace.Snapshot.RunID != args.runID || workspace.Execution == nil || workspace.Execution.Attempt == nil ||
		workspace.Execution.Attempt.ID != args.attemptID || workspace.Execution.Attempt.RunID != args.runID ||
		workspace.Execution.Attempt.HistoryKind != "frozen" || workspace.Execution.Attempt.InputHash == nil || *workspace.Execution.Attempt.InputHash == "" {
		return &rpcFailure{status: http.StatusNotFound, code: codeInternalError, message: "No matching frozen attempt is available."}
	}
	return nil
}

func evidenceFailure(ctx context.Context, err error) *rpcFailure {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return storeFailure(ctx, err)
	}
	var public *fault.Error
	if errors.As(err, &public) {
		return storeFailure(ctx, err)
	}
	if errors.Is(err, errFrozenIdentity) {
		return &rpcFailure{status: http.StatusConflict, code: codeInternalError, message: "Frozen evidence identity is incomplete or inconsistent.", data: map[string]string{"code": "frozen_evidence_integrity"}}
	}
	if errors.Is(err, errEvidenceLimit) {
		return &rpcFailure{status: http.StatusRequestEntityTooLarge, code: codeInternalError, message: "Frozen evidence exceeds the configured processing limit.", data: map[string]string{"code": "evidence_too_large"}}
	}
	return &rpcFailure{status: http.StatusInternalServerError, code: codeInternalError, message: "Frozen evidence could not be read."}
}

var (
	errFrozenIdentity = errors.New("frozen evidence identity mismatch")
	errEvidenceLimit  = errors.New("evidence processing limit exceeded")
)

func buildEvidencePayload(ctx context.Context, user accounts.User, args toolArguments, workspace store.Workspace) (evidencePayload, error) {
	if err := ctx.Err(); err != nil {
		return evidencePayload{}, err
	}
	attempt := workspace.Execution.Attempt
	pinned := make(map[string]struct{}, len(attempt.ParsedDocumentIDs))
	for _, id := range attempt.ParsedDocumentIDs {
		if id == "" {
			return evidencePayload{}, errFrozenIdentity
		}
		if _, exists := pinned[id]; exists {
			return evidencePayload{}, errFrozenIdentity
		}
		pinned[id] = struct{}{}
	}
	if len(pinned) > maxWorkspaceDocs {
		return evidencePayload{}, errEvidenceLimit
	}

	sources := make([]frozenSource, 0, len(pinned))
	ruleSources := make([]rulepack.Source, 0, len(pinned))
	seenPinned := make(map[string]struct{}, len(pinned))
	totalBytes := 0
	for _, item := range workspace.Documents {
		if err := ctx.Err(); err != nil {
			return evidencePayload{}, err
		}
		parsed := item.ParsedDocument
		if parsed == nil {
			continue
		}
		if _, selected := pinned[parsed.ID]; !selected {
			continue
		}
		if _, exists := seenPinned[parsed.ID]; exists {
			return evidencePayload{}, errFrozenIdentity
		}
		seenPinned[parsed.ID] = struct{}{}
		if item.Document.CaseID != args.caseID || item.Document.TenantID != accounts.Tenant(user) || item.Document.Digest == "" ||
			parsed.DocumentID != item.Document.ID || parsed.OriginalDigest != item.Document.Digest || parsed.ArtifactDigest == "" {
			return evidencePayload{}, errFrozenIdentity
		}
		if item.Document.Role != "draft_contract" || !item.Ready || parsed.Status != "ready" {
			continue
		}
		if parsed.ParserID == "" || parsed.ParserVersion == "" || parsed.ConfigHash == "" ||
			parsed.NormalizationVersion == "" || parsed.NormalizedDigest == "" || parsed.Text == "" || item.Text != parsed.Text {
			return evidencePayload{}, errFrozenIdentity
		}
		if !utf8.ValidString(parsed.Text) {
			return evidencePayload{}, errFrozenIdentity
		}
		digest := sha256.Sum256([]byte(parsed.Text))
		normalizedDigest := hex.EncodeToString(digest[:])
		if normalizedDigest != parsed.NormalizedDigest {
			return evidencePayload{}, errFrozenIdentity
		}
		totalBytes += len(parsed.Text)
		if totalBytes > maxSourceBytes {
			return evidencePayload{}, errEvidenceLimit
		}
		sources = append(sources, frozenSource{document: item.Document, parsed: parsed, text: parsed.Text})
		ruleSources = append(ruleSources, rulepack.Source{
			ID: item.Document.ID, Version: parsed.ID, TenantID: item.Document.TenantID, CaseID: item.Document.CaseID,
			Role: item.Document.Role, Digest: normalizedDigest, Text: parsed.Text,
		})
	}
	if len(seenPinned) != len(pinned) {
		return evidencePayload{}, errFrozenIdentity
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].document.ID < sources[j].document.ID })
	sort.Slice(ruleSources, func(i, j int) bool { return ruleSources[i].ID < ruleSources[j].ID })

	facts, err := telecom.Extract(rulepack.Request{
		BusinessLine: workspace.Case.BusinessLine,
		Scenario:     workspace.Case.Scenario,
		ContractType: workspace.Case.ContractType,
		Sources:      ruleSources,
	})
	if err != nil {
		return evidencePayload{}, err
	}
	wantedRule := capabilityRuleIDs[args.capabilityID]
	selected := make([]rulepack.Fact, 0, len(facts))
	for _, fact := range facts {
		if err := ctx.Err(); err != nil {
			return evidencePayload{}, err
		}
		if fact.RuleID == wantedRule {
			selected = append(selected, fact)
		}
	}
	if len(selected) > maxFacts {
		return evidencePayload{}, errEvidenceLimit
	}
	sourceByID := make(map[string]frozenSource, len(sources))
	for _, source := range sources {
		sourceByID[source.document.ID] = source
	}
	evidence := evidencePayload{
		SchemaVersion: "frozen-evidence/v1", ToolVersion: ToolVersion,
		CaseID: args.caseID, RunID: args.runID, AttemptID: args.attemptID, CapabilityID: args.capabilityID,
		Status: "no_supported_statement", Facts: make([]evidenceFact, 0, len(selected)),
	}
	for _, fact := range selected {
		if err := ctx.Err(); err != nil {
			return evidencePayload{}, err
		}
		item := evidenceFact{
			RuleID: fact.RuleID, ObjectRef: fact.ObjectRef, Value: fact.Value, Unit: fact.Unit,
			MeasurementWindow: fact.MeasurementWindow, TimeZone: fact.TimeZone, Calendar: fact.Calendar,
			OutageDefinition: fact.OutageDefinition, Exclusions: fact.Exclusions, Basis: fact.Basis,
			Citations: make([]evidenceCitation, 0, len(fact.Citations)),
		}
		for _, cite := range fact.Citations {
			source, ok := sourceByID[cite.SourceID]
			if !ok || cite.CaseID != args.caseID || cite.TenantID != accounts.Tenant(user) ||
				cite.SourceVersion != source.parsed.ID || cite.SourceDigest != source.parsed.NormalizedDigest {
				return evidencePayload{}, errFrozenIdentity
			}
			runes := []rune(source.text)
			if cite.Start < 0 || cite.End < cite.Start || cite.End > len(runes) || cite.End-cite.Start > maxQuoteRunes || string(runes[cite.Start:cite.End]) != cite.Quote {
				return evidencePayload{}, errFrozenIdentity
			}
			item.Citations = append(item.Citations, evidenceCitation{
				DocumentID: source.document.ID, Filename: source.document.Filename, DocumentRole: source.document.Role,
				OriginalDigest: source.document.Digest, ParsedDocumentID: source.parsed.ID,
				ParserID: source.parsed.ParserID, ParserVersion: source.parsed.ParserVersion,
				ConfigHash: source.parsed.ConfigHash, NormalizationVersion: source.parsed.NormalizationVersion,
				NormalizedDigest: source.parsed.NormalizedDigest, ArtifactDigest: source.parsed.ArtifactDigest,
				SourceVersion: cite.SourceVersion, SourceDigest: cite.SourceDigest,
				Start: cite.Start, End: cite.End, Quote: cite.Quote,
			})
		}
		if len(item.Citations) == 0 {
			return evidencePayload{}, errFrozenIdentity
		}
		evidence.Facts = append(evidence.Facts, item)
	}
	sort.Slice(evidence.Facts, func(i, j int) bool {
		left, right := evidence.Facts[i], evidence.Facts[j]
		if left.RuleID != right.RuleID {
			return left.RuleID < right.RuleID
		}
		if left.Citations[0].DocumentID != right.Citations[0].DocumentID {
			return left.Citations[0].DocumentID < right.Citations[0].DocumentID
		}
		return left.Citations[0].Start < right.Citations[0].Start
	})
	if len(evidence.Facts) > 0 {
		evidence.Status = "found"
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return evidencePayload{}, fmt.Errorf("encode evidence payload: %w", err)
	}
	if len(encoded) > maxToolResult {
		return evidencePayload{}, errEvidenceLimit
	}
	return evidence, nil
}

func workspaceSignature(workspace store.Workspace) string {
	attempt := workspace.Execution.Attempt
	pinned := make(map[string]struct{}, len(attempt.ParsedDocumentIDs))
	for _, id := range attempt.ParsedDocumentIDs {
		pinned[id] = struct{}{}
	}
	items := make([]store.Interpreted, 0, len(pinned))
	for _, item := range workspace.Documents {
		if item.ParsedDocument == nil {
			continue
		}
		if _, selected := pinned[item.ParsedDocument.ID]; selected {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Document.ID < items[j].Document.ID })
	var builder strings.Builder
	builder.Grow(128 + len(items)*256)
	inputHash := ""
	if attempt.InputHash != nil {
		inputHash = *attempt.InputHash
	}
	fmt.Fprintf(&builder, "%s\x00%s\x00%s\x00%s\x00%s\x00", workspace.Case.ID, workspace.Case.TenantID, workspace.Snapshot.RunID, attempt.ID, inputHash)
	for _, item := range items {
		parsed := item.ParsedDocument
		fmt.Fprintf(&builder, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00", item.Document.ID, item.Document.Digest, parsed.ID, parsed.OriginalDigest, parsed.ParserID, parsed.ParserVersion, parsed.ConfigHash, parsed.NormalizationVersion, parsed.NormalizedDigest+":"+parsed.ArtifactDigest)
	}
	return builder.String()
}
