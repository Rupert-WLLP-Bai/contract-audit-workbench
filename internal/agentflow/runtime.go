package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"contractaudit/internal/access"
	"contractaudit/internal/docir"

	"github.com/cloudwego/eino/compose"
)

type Runtime struct {
	config   Config
	tools    map[string]Tool
	specs    []ToolSpec
	specByID map[string]ToolSpec
	runnable compose.Runnable[*execution, *execution]
}

type execution struct {
	request            Request
	report             Report
	gaps               map[string]Gap
	resolved           map[string]bool
	gapStates          map[string]string
	callEvidenceCounts map[string]int
	previousCallByPair map[string]PriorCall
	gapEvidenceIDs     map[string]map[string]bool
	findingsByGap      map[string][]Finding
	evidenceIDs        map[string]bool
	factsByID          map[string]SemanticFact
	roundEvidenceIDs   []string
	roundResolvedIDs   []string
	activeGaps         []Gap
	round              int
	roundProgress      bool
	termination        string
	observations       PlannerUsage
	usageIncomplete    bool
	evidenceBytesLeft  int
}

var capabilityID = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,79}$`)
var normalizedDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

const (
	maxEvidenceBytes       = MaxPlannerEvidenceBytes
	maxCitationsPerFinding = 32
	maxCitationsTotal      = 256
	maxQuoteBytes          = 256 * 1024
)

func New(ctx context.Context, config Config) (*Runtime, error) {
	if config.PlannerConfiguration.ConfigurationID != "" {
		limits := config.PlannerConfiguration.Limits
		if config.MaxCalls == 0 {
			config.MaxCalls = limits.MaxCalls
		}
		if config.MaxRounds == 0 {
			config.MaxRounds = limits.MaxRounds
		}
		if config.Timeout <= 0 {
			config.Timeout = time.Duration(limits.MaxDurationMS) * time.Millisecond
		}
		if config.MaxEvidenceBytes == 0 {
			config.MaxEvidenceBytes = limits.MaxEvidenceBytes
		}
	}
	if config.MaxCalls == 0 {
		config.MaxCalls = DefaultPlannerCalls
	}
	if config.MaxRounds == 0 {
		config.MaxRounds = DefaultPlannerRounds
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultPlannerTimeout
	}
	if config.MaxEvidenceBytes == 0 {
		config.MaxEvidenceBytes = DefaultEvidenceBytes
	}
	if config.ToolTimeout == 0 {
		config.ToolTimeout = min(config.Timeout, 10*time.Second)
	}
	if config.Planner == nil || strings.TrimSpace(config.Planner.Name()) == "" ||
		config.MaxCalls < 1 || config.MaxCalls > 16 || config.MaxRounds < 1 || config.MaxRounds > MaxPlannerRounds ||
		config.MaxEvidenceBytes < 1 || config.MaxEvidenceBytes > MaxPlannerEvidenceBytes ||
		config.Timeout < time.Second || config.Timeout > MaxPlannerTimeout ||
		config.ToolTimeout <= 0 || config.ToolTimeout > config.Timeout {
		return nil, errors.New("invalid runtime configuration")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	runtime := &Runtime{config: config, tools: map[string]Tool{}, specByID: map[string]ToolSpec{}}
	for _, tool := range config.Tools {
		if tool == nil {
			return nil, errors.New("nil tool")
		}
		spec := tool.Spec()
		if spec.ID == "" || spec.Version == "" || !validDomain(spec.Domain) || spec.Effect != "read" {
			return nil, errors.New("only versioned read-only domain tools can be registered")
		}
		if len(spec.Capabilities) == 0 || len(spec.Capabilities) > 16 {
			return nil, errors.New("tool requires explicit bounded capabilities")
		}
		seenCapabilities := map[string]bool{}
		for _, capability := range spec.Capabilities {
			if !capabilityID.MatchString(capability) || seenCapabilities[capability] {
				return nil, errors.New("invalid or duplicate tool capability")
			}
			seenCapabilities[capability] = true
		}
		spec.Capabilities = slices.Clone(spec.Capabilities)
		if _, exists := runtime.tools[spec.ID]; exists {
			return nil, errors.New("duplicate tool id")
		}
		runtime.tools[spec.ID] = tool
		runtime.specs = append(runtime.specs, spec)
		runtime.specByID[spec.ID] = spec
	}
	if len(runtime.specs) == 0 {
		return nil, errors.New("no tools configured")
	}
	configuration, err := runtimePlannerConfiguration(config, runtime.specs)
	if err != nil {
		return nil, err
	}
	runtime.config.PlannerConfiguration = configuration
	chain := compose.NewChain[*execution, *execution]()
	chain.AppendLambda(compose.InvokableLambda(runtime.plan), compose.WithNodeName("scope_planning"))
	chain.AppendLambda(compose.InvokableLambda(runtime.guard), compose.WithNodeName("plan_guard"))
	chain.AppendLambda(compose.InvokableLambda(runtime.execute), compose.WithNodeName("bounded_read_tools"))
	runnable, err := chain.Compile(ctx, compose.WithGraphName("operator_audit_controlled_v1"), compose.WithMaxRunSteps(8))
	if err != nil {
		return nil, err
	}
	runtime.runnable = runnable
	return runtime, nil
}

func (r *Runtime) Run(ctx context.Context, request Request) (Report, error) {
	state := &execution{
		request: request, gaps: map[string]Gap{}, resolved: map[string]bool{}, gapStates: map[string]string{},
		callEvidenceCounts: map[string]int{}, previousCallByPair: map[string]PriorCall{},
		gapEvidenceIDs: map[string]map[string]bool{}, findingsByGap: map[string][]Finding{}, evidenceIDs: map[string]bool{}, factsByID: map[string]SemanticFact{},
		report: Report{
			SchemaVersion: "operator-flow-report/v2", RuntimeVersion: RuntimeVersion,
			Planner: r.config.Planner.Name(), RequestID: request.RequestID, PolicyVersion: access.PolicyVersion,
			Status: "rejected", Plan: Plan{Calls: []Call{}}, Rounds: []PlanRound{},
			Domains: []DomainResult{}, Events: []Event{}, UnresolvedGapIDs: []string{},
			Limitations: []string{"Planning selects only registered read-only tools; it does not decide the audit result.",
				"Located quotes prove frozen source identity; typed semantic verification is required for new facts.",
				"A source with no verified candidate remains unknown or insufficient."},
		},
	}
	if ctx.Err() != nil {
		state.report.Status = "cancelled"
		return state.report, ctx.Err()
	}
	decision := access.Check(access.Review, request.Principal, request.Case, r.config.Clock())
	if !decision.Allowed {
		state.event("authorization", "", "", decision.Reason, 0)
		return state.report, ErrDenied
	}
	if err := validateRequest(request); err != nil {
		state.event("input", "", "", "invalid_frozen_input", 0)
		return state.report, err
	}
	// Deep-copy all input slices so adapters cannot mutate the caller's records.
	totalEvidenceBytes := requestEvidenceBytes(request)
	if totalEvidenceBytes > r.config.MaxEvidenceBytes {
		state.event("input", "", "", "input_budget_exceeded", 0)
		return state.report, ErrInput
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > r.config.MaxEvidenceBytes+(64<<10) {
		state.event("input", "", "", "input_budget_exceeded", 0)
		return state.report, ErrInput
	}
	var frozen Request
	if err = json.Unmarshal(body, &frozen); err != nil {
		return state.report, ErrInput
	}
	state.request = frozen
	state.evidenceBytesLeft = r.config.MaxEvidenceBytes - totalEvidenceBytes
	state.report.InputSHA256 = TextHash(string(body))
	for _, domain := range Domains() {
		status := "not_requested"
		if slices.Contains(request.Scope, domain) {
			status = "unknown"
		}
		state.report.Domains = append(state.report.Domains, DomainResult{Domain: domain, Status: status, Findings: []Finding{}})
	}
	for _, gap := range state.request.Gaps {
		state.gaps[gap.ID] = gap
		state.gapStates[gap.ID] = "open"
		state.gapEvidenceIDs[gap.ID] = map[string]bool{}
		state.findingsByGap[gap.ID] = []Finding{}
		supported := false
		for _, spec := range r.specs {
			if spec.Domain == gap.Domain && slices.Contains(spec.Capabilities, gap.Capability) {
				supported = true
				break
			}
		}
		if !supported {
			state.gapStates[gap.ID] = "unsupported:capability_not_registered"
			state.event("gap_unsupported", "", gap.Domain, "capability_not_registered", 0)
			state.domain(gap.Domain).Status = "partial"
			state.report.Status = "partial"
		}
	}
	for _, evaluation := range state.request.CanonicalEvaluations {
		findings := state.domain(evaluation.Domain)
		finding := Finding{
			Domain: evaluation.Domain, Capability: evaluation.CapabilityID,
			RuleID: "canonical:" + evaluation.ID, Relation: evaluation.Relation,
			Summary: evaluation.ReasonCode, ReasonCode: evaluation.ReasonCode,
			Citations: []Citation{},
		}
		findings.Findings = append(findings.Findings, finding)
	}

	bounded, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()
	allCalls := make([]Call, 0, r.config.MaxCalls)
	var runErr error
	for round := 1; round <= r.config.MaxRounds; round++ {
		state.round = round
		state.activeGaps = state.remainingGaps()
		if len(state.activeGaps) == 0 {
			break
		}
		state.roundProgress = false
		state.roundEvidenceIDs = []string{}
		state.roundResolvedIDs = []string{}
		state.report.Plan = Plan{Calls: []Call{}}
		entry := PlanRound{Round: round, Status: "accepted", GapIDs: make([]string, 0, len(state.activeGaps)),
			Calls: []PlannedCall{}, NewEvidenceIDs: []string{}, ResolvedGapIDs: []string{}}
		for _, gap := range state.activeGaps {
			entry.GapIDs = append(entry.GapIDs, gap.ID)
		}
		state.report.Rounds = append(state.report.Rounds, entry)
		_, runErr = r.runnable.Invoke(bounded, state)
		index := len(state.report.Rounds) - 1
		allCalls = append(allCalls, state.report.Plan.Calls...)
		state.report.Rounds[index].NewEvidenceIDs = slices.Clone(state.roundEvidenceIDs)
		state.report.Rounds[index].ResolvedGapIDs = slices.Clone(state.roundResolvedIDs)
		if runErr != nil {
			if state.termination == "guard_rejected" {
				state.report.Rounds[index].Status = "guard_rejected"
				state.report.Rounds[index].TerminationCode = new("invalid_or_unauthorized_plan")
				if round == 1 && state.report.ToolCalls == 0 {
					state.report.Status = "rejected"
				} else {
					state.report.Status = "partial"
				}
			} else if bounded.Err() != nil || errors.Is(runErr, context.Canceled) {
				state.report.Rounds[index].Status = "cancelled"
				state.report.Rounds[index].TerminationCode = new("cancelled_or_deadline")
				state.report.Status = "cancelled"
			} else {
				state.report.Rounds[index].Status = "failed"
				state.report.Rounds[index].TerminationCode = new("runtime_failure")
				state.report.Status = "partial"
			}
			break
		}
		unresolved := state.remainingGaps()
		switch {
		case state.termination == "evidence_budget_exhausted":
			state.report.Rounds[index].Status = "budget_exhausted"
			state.report.Rounds[index].TerminationCode = new("evidence_budget_exhausted")
			state.report.Status = "partial"
			state.activeGaps = nil
		case len(unresolved) == 0:
			state.report.Rounds[index].Status = "completed"
			if state.report.FallbackUsed {
				state.report.Status = "partial"
			} else {
				state.report.Status = "completed"
			}
		case !state.roundProgress:
			state.report.Rounds[index].Status = "no_progress"
			state.report.Rounds[index].TerminationCode = new("no_new_evidence_or_gap_change")
			state.report.Status = "partial"
			runErr = nil
			state.activeGaps = nil
		case state.report.ToolCalls >= r.config.MaxCalls:
			state.report.Rounds[index].Status = "budget_exhausted"
			state.report.Rounds[index].TerminationCode = new("max_calls_reached")
			state.report.Status = "partial"
			state.activeGaps = nil
		case round == r.config.MaxRounds:
			state.report.Rounds[index].Status = "partial"
			state.report.Rounds[index].TerminationCode = new("max_rounds_reached")
			state.report.Status = "partial"
			state.activeGaps = nil
		default:
			state.report.Rounds[index].Status = "partial"
		}
		if state.activeGaps == nil {
			break
		}
	}
	state.report.Plan = Plan{Calls: allCalls}
	state.report.UnresolvedGapIDs = make([]string, 0, len(state.request.Gaps))
	for _, gap := range state.request.Gaps {
		if !state.resolved[gap.ID] {
			state.report.UnresolvedGapIDs = append(state.report.UnresolvedGapIDs, gap.ID)
		}
	}
	if len(state.report.UnresolvedGapIDs) > 0 && state.report.Status == "completed" {
		state.report.Status = "partial"
	}
	state.report.Projection = buildAgentReview(state, r.config)
	if state.report.Projection != nil {
		state.report.Status = state.report.Projection.Status
	}
	if runErr != nil {
		return state.report, runErr
	}
	return state.report, nil
}

func requestEvidenceBytes(request Request) int {
	total := 0
	for _, evidence := range request.Evidence {
		if len(evidence.Text) > maxEvidenceBytes-total {
			return maxEvidenceBytes + 1
		}
		total += len(evidence.Text)
	}
	return total
}
func validateRequest(request Request) error {
	if request.RequestID == "" || len(request.RequestID) > 128 || len(request.Scope) == 0 || len(request.Scope) > 5 ||
		len(request.Gaps) == 0 || len(request.Gaps) > 16 || len(request.Evidence) > 40 {
		return ErrInput
	}
	scopes := map[Domain]bool{}
	for _, domain := range request.Scope {
		if !validDomain(domain) || scopes[domain] {
			return ErrInput
		}
		scopes[domain] = true
	}
	gaps := map[string]bool{}
	for _, gap := range request.Gaps {
		if gap.ID == "" || len(gap.ID) > 128 || !capabilityID.MatchString(gap.Capability) ||
			gaps[gap.ID] || !scopes[gap.Domain] || len(strings.TrimSpace(gap.Question)) == 0 ||
			len(gap.Question) > 4096 || !utf8.ValidString(gap.Question) {
			return ErrInput
		}
		gaps[gap.ID] = true
	}
	ids, total := map[string]bool{}, 0
	for _, evidence := range request.Evidence {
		total += len(evidence.Text)
		// Reject the size before hashing or normalizing attacker-controlled text.
		if total > maxEvidenceBytes {
			return ErrInput
		}
		if evidence.ID == "" || len(evidence.ID) > 128 || evidence.Version == "" || len(evidence.Version) > 128 ||
			len(evidence.Role) > 64 || ids[evidence.ID] ||
			evidence.TenantID != request.Case.TenantID || evidence.CaseID != request.Case.CaseID ||
			!utf8.ValidString(evidence.Text) || evidence.SHA256 != TextHash(evidence.Text) ||
			docir.Build(evidence.Text).NormalizedText != evidence.Text {
			return ErrInput
		}
		ids[evidence.ID] = true
	}
	return nil
}

func (r *Runtime) plan(ctx context.Context, state *execution) (*execution, error) {
	input := PlanInput{
		Scope: slices.Clone(state.request.Scope), Gaps: slices.Clone(state.activeGaps),
		PreviouslyCalled: state.plannerHistory(), RemainingCalls: r.config.MaxCalls - state.report.ToolCalls,
	}
	for _, spec := range r.specs {
		if slices.Contains(input.Scope, spec.Domain) {
			spec.Capabilities = slices.Clone(spec.Capabilities)
			input.Tools = append(input.Tools, spec)
		}
	}
	var plan Plan
	var err error
	if observedPlanner, ok := r.config.Planner.(ObservedPlanner); ok {
		var observation PlannerObservation
		plan, observation, err = observedPlanner.PlanObserved(ctx, input)
		state.recordPlannerObservation(observation)
	} else {
		plan, err = r.config.Planner.Plan(ctx, input)
		state.observations.PlannerCalls++
	}
	if ctx.Err() != nil {
		return state, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, ErrPlan) {
			state.termination = "guard_rejected"
			state.event("plan_rejected", "", "", "invalid_model_plan", 0)
			return state, ErrPlan
		}
		state.report.FallbackUsed = true
		state.event("planner_fallback", "", "", "planner_unavailable", 0)
		plan, _ = (ScopePlanner{}).Plan(ctx, input)
	}
	state.report.Plan = plan
	state.event("plan", "", "", "candidate_plan", 0)
	return state, nil
}

func (r *Runtime) guard(_ context.Context, state *execution) (*execution, error) {
	calls := state.report.Plan.Calls
	index := len(state.report.Rounds) - 1
	if index >= 0 {
		state.report.Rounds[index].Calls = r.plannedCalls(state, calls)
	}
	active := make(map[string]bool, len(state.activeGaps))
	for _, gap := range state.activeGaps {
		active[gap.ID] = true
	}
	seen := make(map[string]bool, len(calls))
	if calls == nil || state.report.ToolCalls+len(calls) > r.config.MaxCalls {
		state.termination = "guard_rejected"
		state.event("plan_rejected", "", "", "allowlist_scope_or_budget", 0)
		return state, ErrPlan
	}
	for _, call := range calls {
		spec, exists := r.specByID[call.ToolID]
		gap, known := state.gaps[call.GapID]
		pair := call.GapID + "\x00" + call.ToolID
		priorEvidenceCount, wasCalled := state.callEvidenceCounts[pair]
		newEvidenceSinceCall := len(state.gapEvidenceIDs[gap.ID]) > priorEvidenceCount
		if !exists || !known || !active[call.GapID] || seen[call.GapID] ||
			(wasCalled && !newEvidenceSinceCall) ||
			!slices.Contains(state.request.Scope, call.Domain) || spec.Domain != call.Domain ||
			!slices.Contains(spec.Capabilities, gap.Capability) || gap.Domain != call.Domain ||
			strings.TrimSpace(call.Reason) == "" || len(call.Reason) > 1024 {
			state.termination = "guard_rejected"
			state.event("plan_rejected", call.ToolID, call.Domain, "allowlist_scope_or_budget", 0)
			return state, ErrPlan
		}
		seen[call.GapID] = true
	}
	state.event("plan_accepted", "", "", "all_calls_validated_before_execution", 0)
	return state, nil
}

func (r *Runtime) execute(ctx context.Context, state *execution) (*execution, error) {
	for _, call := range state.report.Plan.Calls {
		callID := stableCallID(state.request.RequestID, state.round, call)
		if ctx.Err() != nil {
			state.updatePlannedCall(callID, "cancelled", "cancelled_or_deadline", nil)
			return state, ctx.Err()
		}
		if !access.Check(access.Review, state.request.Principal, state.request.Case, r.config.Clock()).Allowed {
			state.updatePlannedCall(callID, "denied", "authorization_revoked", nil)
			state.eventCall("authorization", call.ToolID, call.Domain, "grant_expired_before_tool", 0, callID)
			return state, ErrDenied
		}
		state.updatePlannedCall(callID, "running", "", nil)
		toolCtx, cancel := context.WithTimeout(ctx, r.config.ToolTimeout)
		started := time.Now()
		state.report.ToolCalls++
		pair := call.GapID + "\x00" + call.ToolID
		findings, err := r.tools[call.ToolID].Execute(toolCtx, ToolInput{
			TenantID: state.request.Case.TenantID, CaseID: state.request.Case.CaseID,
			ActorID: state.request.Principal.UserID, Gap: state.gaps[call.GapID],
			Evidence: slices.Clone(state.request.Evidence), PriorFindings: cloneFindings(state.findingsByGap[call.GapID]),
		})
		if toolCtx.Err() != nil {
			err = toolCtx.Err()
		}
		cancel()
		if err == nil && !access.Check(access.Review, state.request.Principal, state.request.Case, r.config.Clock()).Allowed {
			err = ErrDenied
		}
		if err == nil {
			err = validateFindings(ctx, findings, call.Domain, state.gaps[call.GapID].Capability, state.request.Evidence)
		}
		returnedBytes := 0
		if err == nil {
			returnedBytes = returnedEvidenceBytes(findings)
			if returnedBytes > state.evidenceBytesLeft {
				err = ErrEvidence
				state.termination = "evidence_budget_exhausted"
			}
		}
		if err == nil {
			err = state.recordFindings(call.GapID, findings)
		}
		if err != nil {
			state.callEvidenceCounts[pair] = len(state.gapEvidenceIDs[call.GapID])
			state.previousCallByPair[pair] = PriorCall{ToolID: call.ToolID, GapID: call.GapID}
			status, code := "failed", "tool_error_or_unverified_result"
			if errors.Is(err, ErrDenied) {
				status, code = "denied", "authorization_revoked_after_tool"
			} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				status, code = "cancelled", "cancelled_or_deadline"
			} else if state.termination == "evidence_budget_exhausted" {
				code = "evidence_budget_exhausted"
			}
			state.gapStates[call.GapID] = "unknown:" + code
			state.domain(call.Domain).Status = "partial"
			state.updatePlannedCall(callID, status, code, nil)
			state.eventCall("tool_failed", call.ToolID, call.Domain, code, time.Since(started).Milliseconds(), callID)
			if errors.Is(err, ErrDenied) {
				return state, err
			}
			if ctx.Err() != nil {
				return state, ctx.Err()
			}
			if state.termination == "evidence_budget_exhausted" {
				break
			}
			continue
		}

		state.evidenceBytesLeft -= returnedBytes
		newState, resolved := gapOutcome(findings)
		if previous := state.gapStates[call.GapID]; previous != newState {
			state.gapStates[call.GapID] = newState
			state.roundProgress = true
		}
		wasResolved := state.resolved[call.GapID]
		state.resolved[call.GapID] = resolved
		if resolved && !wasResolved {
			state.roundResolvedIDs = append(state.roundResolvedIDs, call.GapID)
			state.roundProgress = true
		}
		evidenceIDs := state.findingIDs(findings)
		state.callEvidenceCounts[pair] = len(state.gapEvidenceIDs[call.GapID])
		state.previousCallByPair[pair] = PriorCall{ToolID: call.ToolID, GapID: call.GapID}
		domain := state.domain(call.Domain)
		if newState == "resolved" {
			domain.Status = "completed"
		} else {
			domain.Status = "partial"
		}
		domain.Findings = append(domain.Findings, findings...)
		state.updatePlannedCall(callID, "completed", "", evidenceIDs)
		state.eventCall("tool_completed", call.ToolID, call.Domain, "verified_frozen_source_facts", time.Since(started).Milliseconds(), callID)
	}
	if ctx.Err() != nil {
		return state, ctx.Err()
	}
	return state, nil
}

func (state *execution) remainingGaps() []Gap {
	out := make([]Gap, 0, len(state.request.Gaps))
	for _, gap := range state.request.Gaps {
		if !state.resolved[gap.ID] && !strings.HasPrefix(state.gapStates[gap.ID], "unsupported:") {
			out = append(out, gap)
		}
	}
	return out
}

func (state *execution) eventCall(kind, tool string, domain Domain, code string, milliseconds int64, callID string) {
	state.report.Events = append(state.report.Events, Event{Sequence: len(state.report.Events) + 1,
		Round: state.round, Kind: kind, ToolID: tool, CallID: callID, Domain: domain, Code: code, DurationMS: milliseconds})
}
func (r *Runtime) plannedCalls(state *execution, calls []Call) []PlannedCall {
	out := make([]PlannedCall, 0, len(calls))
	for _, call := range calls {
		gap := state.gaps[call.GapID]
		spec := r.specByID[call.ToolID]
		out = append(out, PlannedCall{
			CallID: stableCallID(state.request.RequestID, state.round, call), ToolID: call.ToolID,
			ToolVersion: spec.Version, Domain: call.Domain, CapabilityID: gap.Capability, GapID: call.GapID,
			Status: "planned", ReasonCode: nil, EvidenceIDs: []string{},
		})
	}
	return out
}

func stableCallID(requestID string, round int, call Call) string {
	identity := fmt.Sprintf("%s\x00%d\x00%s\x00%s", requestID, round, call.ToolID, call.GapID)
	return "call_" + TextHash(identity)[:24]
}

func (state *execution) plannerHistory() []PriorCall {
	pairs := make([]string, 0, len(state.previousCallByPair))
	for pair := range state.previousCallByPair {
		pairs = append(pairs, pair)
	}
	sort.Strings(pairs)
	out := make([]PriorCall, 0, len(pairs))
	for _, pair := range pairs {
		prior := state.previousCallByPair[pair]
		prior.NewEvidenceAvailable = len(state.gapEvidenceIDs[prior.GapID]) > state.callEvidenceCounts[pair]
		out = append(out, prior)
	}
	return out
}

func (state *execution) recordPlannerObservation(observation PlannerObservation) {
	state.observations.PlannerCalls++
	if observation.InputTokens == nil || observation.OutputTokens == nil || observation.TotalTokens == nil ||
		*observation.InputTokens < 0 || *observation.OutputTokens < 0 || *observation.TotalTokens < 0 {
		state.usageIncomplete = true
		return
	}
	if state.observations.InputTokens == nil {
		state.observations.InputTokens = new(int64)
		state.observations.OutputTokens = new(int64)
		state.observations.TotalTokens = new(int64)
	}
	*state.observations.InputTokens += *observation.InputTokens
	*state.observations.OutputTokens += *observation.OutputTokens
	*state.observations.TotalTokens += *observation.TotalTokens
}

func (state *execution) recordFindings(gapID string, findings []Finding) error {
	for _, finding := range findings {
		for _, fact := range finding.Facts {
			if prior, exists := state.factsByID[fact.ID]; exists {
				priorJSON, _ := json.Marshal(prior)
				factJSON, _ := json.Marshal(fact)
				if string(priorJSON) != string(factJSON) {
					return ErrEvidence
				}
				continue
			}
		}
	}
	for _, finding := range findings {
		for _, citation := range finding.Citations {
			state.recordEvidenceReference(gapID, citation.EvidenceID)
		}
		for _, fact := range finding.Facts {
			if _, exists := state.factsByID[fact.ID]; exists {
				continue
			}
			state.factsByID[fact.ID] = cloneFact(fact)
			state.evidenceIDs[fact.ID] = true
			if state.gapEvidenceIDs[gapID] == nil {
				state.gapEvidenceIDs[gapID] = map[string]bool{}
			}
			if !state.gapEvidenceIDs[gapID][fact.ID] {
				state.gapEvidenceIDs[gapID][fact.ID] = true
				state.roundEvidenceIDs = append(state.roundEvidenceIDs, fact.ID)
				state.roundProgress = true
			}
		}
		state.findingsByGap[gapID] = append(state.findingsByGap[gapID], cloneFinding(finding))
	}
	return nil
}

// Count retained quote and typed-value bytes, including copies returned in later
// calls. The initial frozen source text and every accepted result share one limit.
func returnedEvidenceBytes(findings []Finding) int {
	total := 0
	for _, finding := range findings {
		for _, citation := range finding.Citations {
			total += len(citation.Quote)
		}
		for _, fact := range finding.Facts {
			total += len(fact.Value) + len(fact.Unit) + len(fact.Currency) + len(fact.Citation.Quote)
		}
	}
	return total
}

func (state *execution) recordEvidenceReference(gapID, evidenceID string) {
	if state.gapEvidenceIDs[gapID] == nil {
		state.gapEvidenceIDs[gapID] = map[string]bool{}
	}
	state.evidenceIDs[evidenceID] = true
	if state.gapEvidenceIDs[gapID][evidenceID] {
		return
	}
	state.gapEvidenceIDs[gapID][evidenceID] = true
	state.roundEvidenceIDs = append(state.roundEvidenceIDs, evidenceID)
	state.roundProgress = true
}
func (state *execution) findingIDs(findings []Finding) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, finding := range findings {
		for _, citation := range finding.Citations {
			if !seen[citation.EvidenceID] {
				seen[citation.EvidenceID] = true
				out = append(out, citation.EvidenceID)
			}
		}
		for _, fact := range finding.Facts {
			if !seen[fact.ID] {
				seen[fact.ID] = true
				out = append(out, fact.ID)
			}
		}
	}
	slices.Sort(out)
	return out
}

func (state *execution) updatePlannedCall(callID, status, reasonCode string, evidenceIDs []string) {
	index := len(state.report.Rounds) - 1
	if index < 0 {
		return
	}
	for i := range state.report.Rounds[index].Calls {
		call := &state.report.Rounds[index].Calls[i]
		if call.CallID != callID {
			continue
		}
		call.Status = status
		call.ReasonCode = nil
		if reasonCode != "" {
			call.ReasonCode = new(reasonCode)
		}
		if evidenceIDs != nil {
			call.EvidenceIDs = slices.Clone(evidenceIDs)
		}
		return
	}
}

func gapOutcome(findings []Finding) (string, bool) {
	insufficientReason := ""
	unsupportedReason := ""
	unknownReason := ""
	decision := false
	notApplicable := false
	for _, finding := range findings {
		switch finding.Relation {
		case "compatible", "conflict":
			decision = true
		case "not_applicable":
			notApplicable = true
		case "insufficient":
			insufficientReason = finding.ReasonCode
		case "unsupported":
			unsupportedReason = finding.ReasonCode
		case "unknown":
			unknownReason = finding.ReasonCode
		}
	}
	if insufficientReason != "" {
		return "insufficient:" + insufficientReason, false
	}
	if unsupportedReason != "" {
		return "unsupported:" + unsupportedReason, false
	}
	if unknownReason != "" {
		return "unknown:" + unknownReason, false
	}
	if decision || notApplicable {
		return "resolved", true
	}
	return "insufficient:no_verified_result", false
}

func cloneFindings(findings []Finding) []Finding {
	out := make([]Finding, len(findings))
	for i, finding := range findings {
		out[i] = cloneFinding(finding)
	}
	return out
}

func cloneFinding(finding Finding) Finding {
	facts := make([]SemanticFact, len(finding.Facts))
	for i, fact := range finding.Facts {
		facts[i] = cloneFact(fact)
	}
	finding.Facts = facts
	citations := make([]Citation, len(finding.Citations))
	for i, citation := range finding.Citations {
		citations[i] = cloneCitation(citation)
	}
	finding.Citations = citations
	return finding
}

func cloneFact(fact SemanticFact) SemanticFact {
	fact.Citation = cloneCitation(fact.Citation)
	return fact
}

func cloneCitation(citation Citation) Citation {
	if citation.Page != nil {
		citation.Page = new(*citation.Page)
	}
	return citation
}

func buildAgentReview(state *execution, config Config) *AgentReview {
	if len(state.report.Rounds) == 0 && len(state.report.Events) == 0 && state.report.ToolCalls == 0 {
		return nil
	}
	configuration := config.PlannerConfiguration
	mode := configuration.Mode
	if mode == "" {
		mode = ModeDeterministic
	}
	var fallbackCode, modelID *string
	if state.report.FallbackUsed {
		fallbackCode = new("planner_unavailable_used_deterministic_fallback")
	}
	if configuration.ModelConfigurationID != "" {
		modelID = new(configuration.ModelConfigurationID)
	}
	var rejectionCode *string
	if state.termination == "guard_rejected" {
		rejectionCode = new("invalid_or_unauthorized_plan")
	}
	projection := &AgentReview{
		SchemaVersion: AgentReviewSchemaVersion, AttemptID: state.request.AttemptID,
		Status: state.report.Status, Mode: mode, SourceSnapshot: cloneSourceSnapshot(state.request.SourceSnapshot),
		RulePack: cloneRulePackIdentity(state.request.RulePack),
		Planner: PlannerReport{Identity: state.report.Planner, ConfigurationID: configuration.ConfigurationID,
			ModelConfigurationID: modelID, PromptVersion: configuration.PromptVersion, PromptSHA256: configuration.PromptSHA256,
			SchemaVersion: configuration.SchemaVersion, RuntimeVersion: configuration.RuntimeVersion,
			ToolCatalogVersion: configuration.ToolCatalogVersion, FallbackUsed: state.report.FallbackUsed,
			FallbackCode: fallbackCode, Limits: configuration.Limits},
		Scope: []DomainScope{}, Domains: []DomainCoverage{}, Gaps: []GapReport{},
		Plan:     AgentPlan{Status: planProjectionStatus(state.report.Status, state.report.Rounds, state.report.FallbackUsed, rejectionCode != nil), RejectionCode: rejectionCode, Calls: []PlannedCall{}, Rounds: cloneRounds(state.report.Rounds)},
		Evidence: []VerifiedEvidence{}, Usage: state.projectedUsage(),
		UnresolvedGapIDs: slices.Clone(state.report.UnresolvedGapIDs), Events: []AgentReviewEvent{},
	}
	if state.report.Plan.Calls == nil {
		projection.Plan.Calls = []PlannedCall{}
	}
	for _, round := range state.report.Rounds {
		for _, call := range round.Calls {
			projection.Plan.Calls = append(projection.Plan.Calls, clonePlannedCall(call))
		}
	}
	sourceIDs := make([]string, 0, len(state.request.Evidence))
	for _, evidence := range state.request.Evidence {
		sourceIDs = append(sourceIDs, evidence.ID)
	}
	slices.Sort(sourceIDs)
	sourceIDs = slices.Compact(sourceIDs)
	for _, domain := range state.request.Scope {
		capabilityIDs := []string{}
		for _, gap := range state.request.Gaps {
			if gap.Domain == domain {
				capabilityIDs = append(capabilityIDs, gap.Capability)
			}
		}
		for _, evaluation := range state.request.CanonicalEvaluations {
			if evaluation.Domain == domain {
				capabilityIDs = append(capabilityIDs, evaluation.CapabilityID)
			}
		}
		slices.Sort(capabilityIDs)
		capabilityIDs = slices.Compact(capabilityIDs)
		projection.Scope = append(projection.Scope, DomainScope{Domain: domain, CapabilityIDs: capabilityIDs, SourceIDs: slices.Clone(sourceIDs)})
	}
	for _, domain := range state.report.Domains {
		coverage := DomainCoverage{Domain: domain.Domain, Status: domain.Status, Capabilities: []CapabilityResult{}}
		if !slices.Contains(state.request.Scope, domain.Domain) {
			projection.Domains = append(projection.Domains, coverage)
			continue
		}
		for _, gap := range state.request.Gaps {
			if gap.Domain != domain.Domain {
				continue
			}
			gapStatus, reasonCode := projectedGapState(state.gapStates[gap.ID])
			result := CapabilityResult{CapabilityID: gap.Capability, Status: gapStatus,
				ReasonCode: reasonCode, EvidenceIDs: []string{}, CanonicalEvaluationIDs: []string{}}
			if gapStatus == "resolved" {
				result.Status = "completed"
			}
			for _, finding := range state.findingsByGap[gap.ID] {
				if result.Relation == nil && (finding.Relation == "compatible" || finding.Relation == "conflict") {
					result.Relation = new(finding.Relation)
				}
				if finding.ReasonCode != "" {
					result.ReasonCode = finding.ReasonCode
				}
				result.EvidenceIDs = append(result.EvidenceIDs, state.findingIDs([]Finding{finding})...)
			}
			for _, evaluation := range state.request.CanonicalEvaluations {
				if evaluation.Domain != gap.Domain || evaluation.CapabilityID != gap.Capability {
					continue
				}
				result.CanonicalEvaluationIDs = append(result.CanonicalEvaluationIDs, evaluation.ID)
				result.Relation = new(evaluation.Relation)
				result.Status = canonicalProjectionStatus(evaluation.Relation)
				result.ReasonCode = evaluation.ReasonCode
				result.EvidenceIDs = append(result.EvidenceIDs, evaluation.EvidenceIDs...)
			}
			slices.Sort(result.EvidenceIDs)
			result.EvidenceIDs = slices.Compact(result.EvidenceIDs)
			slices.Sort(result.CanonicalEvaluationIDs)
			result.CanonicalEvaluationIDs = slices.Compact(result.CanonicalEvaluationIDs)
			coverage.Capabilities = append(coverage.Capabilities, result)
			if result.Status == "completed" || result.Status == "not_applicable" {
				gapStatus = "resolved"
			} else {
				gapStatus = result.Status
			}
			projection.Gaps = append(projection.Gaps, GapReport{GapID: gap.ID, Domain: gap.Domain,
				Capability: gap.Capability, Status: gapStatus, ReasonCode: result.ReasonCode,
				EvidenceIDs: slices.Clone(result.EvidenceIDs)})
		}
		for _, evaluation := range state.request.CanonicalEvaluations {
			if evaluation.Domain != domain.Domain {
				continue
			}
			matched := false
			for _, capability := range coverage.Capabilities {
				if capability.CapabilityID == evaluation.CapabilityID {
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			evidenceIDs := append([]string{}, evaluation.EvidenceIDs...)
			slices.Sort(evidenceIDs)
			evidenceIDs = slices.Compact(evidenceIDs)
			coverage.Capabilities = append(coverage.Capabilities, CapabilityResult{
				CapabilityID: evaluation.CapabilityID, Status: canonicalProjectionStatus(evaluation.Relation),
				Relation: new(evaluation.Relation), ReasonCode: evaluation.ReasonCode,
				EvidenceIDs: evidenceIDs, CanonicalEvaluationIDs: []string{evaluation.ID},
			})
		}
		if len(coverage.Capabilities) > 0 {
			coverage.Status = "completed"
			for _, capability := range coverage.Capabilities {
				if capability.Status != "completed" && capability.Status != "not_applicable" {
					coverage.Status = "partial"
					break
				}
			}
			if coverage.Status == "partial" && projection.Status == "completed" {
				projection.Status = "partial"
			}
		}
		projection.Domains = append(projection.Domains, coverage)
	}
	factIDs := make([]string, 0, len(state.factsByID))
	for id := range state.factsByID {
		factIDs = append(factIDs, id)
	}
	slices.Sort(factIDs)
	for _, id := range factIDs {
		fact := state.factsByID[id]
		citation := fact.Citation
		projection.Evidence = append(projection.Evidence, VerifiedEvidence{EvidenceID: fact.ID,
			SemanticType: fact.Type, Value: fact.Value, Unit: optionalString(fact.Unit), Currency: optionalString(fact.Currency),
			Verification: "typed_semantic_and_frozen_quote_verified",
			Anchor: EvidenceAnchor{ID: fact.ID, DocumentID: citation.DocumentID, ParsedDocumentID: citation.ParsedDocumentID,
				NormalizedDigest: citation.NormalizedDigest, Quote: citation.Quote, Status: "located", Start: citation.Start,
				End: citation.End, Page: cloneIntPointer(citation.Page), ElementID: optionalString(citation.ElementID)},
		})
	}
	for _, event := range state.report.Events {
		var callID *string
		if event.CallID != "" {
			callID = new(event.CallID)
		}
		var eventDomain *Domain
		if event.Domain != "" {
			eventDomain = new(event.Domain)
		}
		projection.Events = append(projection.Events, AgentReviewEvent{Sequence: event.Sequence, Round: event.Round, Kind: event.Kind,
			CallID: callID, Domain: eventDomain, Code: event.Code, DurationMS: event.DurationMS})
	}
	return projection
}

func projectedGapState(value string) (string, string) {
	if value == "resolved" {
		return "resolved", "verified_result"
	}
	if value == "" || value == "open" {
		return "unknown", "not_executed"
	}
	status, reason, found := strings.Cut(value, ":")
	if !found {
		return "unknown", value
	}
	switch status {
	case "resolved":
		return "resolved", "verified_result"
	case "insufficient", "unsupported", "unknown":
		return status, reason
	default:
		return "unknown", "unrecognized_outcome"
	}
}

func canonicalProjectionStatus(relation string) string {
	switch relation {
	case "compatible", "conflict":
		return "completed"
	case "insufficient", "unsupported", "unknown", "not_applicable":
		return relation
	default:
		return "unknown"
	}
}
func planProjectionStatus(status string, rounds []PlanRound, fallbackUsed, guardRejected bool) string {
	if guardRejected {
		return "guard_rejected"
	}
	if len(rounds) == 0 {
		return "not_planned"
	}
	if fallbackUsed {
		return "fallback"
	}
	switch status {
	case "completed":
		return "completed"
	case "planning", "running":
		return "executing"
	default:
		return "partial"
	}
}

func (state *execution) projectedUsage() PlannerUsage {
	usage := cloneUsage(state.observations)
	if state.usageIncomplete {
		usage.InputTokens, usage.OutputTokens, usage.TotalTokens = nil, nil, nil
	}
	return usage
}

func cloneRounds(rounds []PlanRound) []PlanRound {
	out := make([]PlanRound, len(rounds))
	for i, round := range rounds {
		out[i] = round
		out[i].GapIDs = slices.Clone(round.GapIDs)
		out[i].NewEvidenceIDs = slices.Clone(round.NewEvidenceIDs)
		out[i].ResolvedGapIDs = slices.Clone(round.ResolvedGapIDs)
		out[i].TerminationCode = cloneStringPointer(round.TerminationCode)
		out[i].Calls = make([]PlannedCall, len(round.Calls))
		for j, call := range round.Calls {
			out[i].Calls[j] = clonePlannedCall(call)
		}
	}
	return out
}

func clonePlannedCall(call PlannedCall) PlannedCall {
	call.ReasonCode = cloneStringPointer(call.ReasonCode)
	call.EvidenceIDs = slices.Clone(call.EvidenceIDs)
	return call
}

func cloneSourceSnapshot(source *SourceSnapshot) *SourceSnapshot {
	if source == nil {
		return nil
	}
	copy := *source
	copy.DocumentIDs = slices.Clone(source.DocumentIDs)
	copy.ParsedDocumentIDs = slices.Clone(source.ParsedDocumentIDs)
	return &copy
}

func cloneRulePackIdentity(identity *RulePackIdentity) *RulePackIdentity {
	if identity == nil {
		return nil
	}
	copy := *identity
	return &copy
}

func cloneUsage(usage PlannerUsage) PlannerUsage {
	usage.InputTokens = cloneInt64Pointer(usage.InputTokens)
	usage.OutputTokens = cloneInt64Pointer(usage.OutputTokens)
	usage.TotalTokens = cloneInt64Pointer(usage.TotalTokens)
	return usage
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	return new(*value)
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	return new(*value)
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	return new(*value)
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return new(value)
}
func validateFindings(ctx context.Context, findings []Finding, domain Domain, capability string, evidence []Evidence) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(findings) == 0 || len(findings) > 100 {
		return ErrEvidence
	}
	byID := map[string]Evidence{}
	runesByID := map[string][]rune{}
	for _, item := range evidence {
		if err := ctx.Err(); err != nil {
			return err
		}
		byID[item.ID] = item
		runesByID[item.ID] = []rune(item.Text)
	}
	citations, factCount, quoteBytes := 0, 0, 0
	for _, finding := range findings {
		if err := ctx.Err(); err != nil {
			return err
		}
		citations += len(finding.Citations)
		if finding.Domain != domain || finding.Capability != capability || finding.RuleID == "" ||
			len(finding.RuleID) > 128 || finding.Summary == "" || len(finding.Summary) > 8192 ||
			len(finding.Citations) > maxCitationsPerFinding || citations > maxCitationsTotal {
			return ErrEvidence
		}
		switch finding.Relation {
		case "compatible", "conflict":
			if len(finding.Citations) == 0 {
				return ErrEvidence
			}
		case "insufficient", "unsupported", "unknown":
			if finding.ReasonCode == "" || len(finding.ReasonCode) > 128 {
				return ErrEvidence
			}
		case "not_applicable":
		default:
			return ErrEvidence
		}
		for _, citation := range finding.Citations {
			if err := ctx.Err(); err != nil {
				return err
			}
			quoteBytes += len(citation.Quote)
			if quoteBytes > maxQuoteBytes {
				return ErrEvidence
			}
			item, exists := byID[citation.EvidenceID]
			text := runesByID[citation.EvidenceID]
			if !exists || citation.Version != item.Version || citation.Quote == "" ||
				citation.Start < 0 || citation.End <= citation.Start || citation.End > len(text) ||
				string(text[citation.Start:citation.End]) != citation.Quote {
				return ErrEvidence
			}
		}
		factCount += len(finding.Facts)
		if factCount > 128 {
			return ErrEvidence
		}
		for _, fact := range finding.Facts {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := validateSemanticFact(fact, finding, byID); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func validateSemanticFact(fact SemanticFact, finding Finding, evidence map[string]Evidence) error {
	if !capabilityID.MatchString(fact.ID) || !validSemanticFactType(finding.Domain, fact.Type) ||
		strings.TrimSpace(fact.Value) == "" || len(fact.Value) > 4096 || !utf8.ValidString(fact.Value) ||
		len(fact.Unit) > 64 || len(fact.Currency) > 16 {
		return ErrEvidence
	}
	citation := fact.Citation
	item, exists := evidence[citation.EvidenceID]
	if !exists || citation.Version != item.Version || citation.Status != "located" ||
		citation.DocumentID == "" || citation.DocumentID != item.DocumentID ||
		citation.ParsedDocumentID == "" || citation.ParsedDocumentID != item.ParsedDocumentID ||
		citation.NormalizedDigest == "" || citation.NormalizedDigest != item.NormalizedDigest ||
		!normalizedDigest.MatchString(citation.NormalizedDigest) {
		return ErrEvidence
	}
	if sourceSemanticType(fact.Type) {
		value, unit, currency, ok := normalizeSemanticQuote(fact.Type, citation.Quote)
		if !ok || value != fact.Value || unit != fact.Unit || currency != fact.Currency {
			return ErrEvidence
		}
	} else if fact.Type != "amount" && fact.Type != "liability_cap" && !strings.Contains(citation.Quote, fact.Value) {
		return ErrEvidence
	}
	anchored := false
	for _, candidate := range finding.Citations {
		if candidate.EvidenceID == citation.EvidenceID && candidate.Version == citation.Version &&
			candidate.Quote == citation.Quote && candidate.Start == citation.Start && candidate.End == citation.End &&
			candidate.DocumentID == citation.DocumentID && candidate.ParsedDocumentID == citation.ParsedDocumentID &&
			candidate.NormalizedDigest == citation.NormalizedDigest && candidate.Status == citation.Status {
			anchored = true
			break
		}
	}
	if !anchored {
		return ErrEvidence
	}
	if isAmountFactType(fact.Type) {
		amount, ok := new(big.Rat).SetString(fact.Value)
		if !ok || amount.Sign() < 0 || fact.Unit == "" || !validCurrency(fact.Currency) {
			return ErrEvidence
		}
	} else if isSourceDateFactType(fact.Type) {
		if (fact.Unit != "date" && fact.Unit != "instant") || fact.Currency != "" {
			return ErrEvidence
		}
	} else if fact.Unit != "" || fact.Currency != "" {
		return ErrEvidence
	}
	if (fact.Type == "subject_id" || strings.HasPrefix(fact.Type, "subject_id_")) && strings.ContainsAny(fact.Value, " \t\r\n") {
		return ErrEvidence
	}
	return nil
}

func validSemanticFactType(domain Domain, kind string) bool {
	switch domain {
	case Compliance:
		return slices.Contains([]string{"amount", "amount_total", "amount_ceiling", "party", "party_a", "party_b", "party_buyer", "party_seller", "party_service_provider", "party_customer", "approval_authority", "effective_scope", "approval_valid_from", "approval_valid_until", "contract_signing_date", "contract_effective_date", "contract_review_basis_date"}, kind)
	case Fulfillment:
		return slices.Contains([]string{"milestone", "milestone_delivery", "milestone_acceptance", "milestone_mobilization", "milestone_start", "milestone_activation", "milestone_launch", "explicit_start_event"}, kind)
	case Counterparty:
		return slices.Contains([]string{"subject_id", "subject_id_party_a", "subject_id_party_b", "subject_id_party_buyer", "subject_id_party_seller", "subject_id_party_service_provider", "subject_id_party_customer", "signing_party", "payee", "responsibility"}, kind)
	case Commercial:
		return slices.Contains([]string{"amount", "amount_total", "amount_ceiling", "payment_term", "liability_cap"}, kind)
	case Technical:
		return slices.Contains([]string{"bandwidth", "availability", "latency", "scope"}, kind)
	default:
		return false
	}
}

func isAmountFactType(kind string) bool {
	return kind == "amount" || kind == "amount_total" || kind == "amount_ceiling" || kind == "liability_cap"
}

func isSourceDateFactType(kind string) bool {
	return kind == "approval_valid_from" || kind == "approval_valid_until" ||
		kind == "contract_signing_date" || kind == "contract_effective_date" || kind == "contract_review_basis_date"
}

func validCurrency(value string) bool {
	switch value {
	case "CNY", "RMB", "USD", "EUR":
		return true
	default:
		return false
	}
}
func (state *execution) event(kind, tool string, domain Domain, code string, milliseconds int64) {
	state.report.Events = append(state.report.Events, Event{Sequence: len(state.report.Events) + 1,
		Round: state.round, Kind: kind, ToolID: tool, Domain: domain, Code: code, DurationMS: milliseconds})
}

func (state *execution) domain(want Domain) *DomainResult {
	for i := range state.report.Domains {
		if state.report.Domains[i].Domain == want {
			return &state.report.Domains[i]
		}
	}
	panic(fmt.Sprintf("validated domain missing: %s", want))
}
