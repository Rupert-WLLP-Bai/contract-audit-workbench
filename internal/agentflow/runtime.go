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
	request                Request
	report                 Report
	gaps                   map[string]Gap
	resolved               map[string]bool
	gapStates              map[string]string
	callEvidenceCounts     map[string]int
	previousCallByPair     map[string]PriorCall
	gapEvidenceIDs         map[string]map[string]bool
	findingsByGap          map[string][]Finding
	evidenceIDs            map[string]bool
	factsByID              map[string]SemanticFact
	verifiedFactsByID      map[string]VerifiedFact
	toolObservations       []ToolObservation
	knowledgeObservations  []KnowledgeReadObservation
	knowledgeReadAttempts  map[KnowledgeReadRequest]bool
	plannerKnowledge       map[KnowledgeIdentity]bool
	knowledgeReadCalls     int
	knowledgeReaderEnabled bool
	omittedObservations    int
	sink                   func(context.Context, Event) error
	sinkContext            context.Context
	sinkErr                error
	clock                  func() time.Time
	roundEvidenceIDs       []string
	roundResolvedIDs       []string
	activeGaps             []Gap
	round                  int
	roundProgress          bool
	termination            string
	observations           PlannerUsage
	usageIncomplete        bool
	evidenceBytesLeft      int
}

var capabilityID = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,79}$`)
var normalizedDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

const (
	maxEvidenceBytes       = MaxPlannerEvidenceBytes
	maxCitationsPerFinding = 32
	maxCitationsTotal      = 256
	maxQuoteBytes          = 256 * 1024
	maxReviewGaps          = 128
	maxPlanningGaps        = 16
)

func New(ctx context.Context, config Config) (*Runtime, error) {
	config = defaultRuntimeConfig(config)
	if err := validateRuntimeConfiguration(config); err != nil {
		return nil, err
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	registered, err := registerRuntimeTools(config.Tools)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{config: config, tools: registered.tools, specs: registered.specs, specByID: registered.specByID}
	configuration, err := runtimePlannerConfiguration(config, runtime.specs)
	if err != nil {
		return nil, err
	}
	runtime.config.PlannerConfiguration = configuration
	runtime.runnable, err = compileRuntime(ctx, runtime)
	if err != nil {
		return nil, err
	}
	return runtime, nil
}

func defaultRuntimeConfig(config Config) Config {
	return defaultRuntimeToolTimeout(defaultRuntimeLimits(defaultPlannerConfig(config)))
}

func defaultPlannerConfig(config Config) Config {
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
	return config
}

func defaultRuntimeLimits(config Config) Config {
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
	return config
}

func defaultRuntimeToolTimeout(config Config) Config {
	if config.ToolTimeout == 0 {
		config.ToolTimeout = min(config.Timeout, 10*time.Second)
	}
	return config
}

func validateRuntimeConfiguration(config Config) error {
	if !validRuntimePlanner(config) || !validRuntimeLimits(config) || !validRuntimeTimeouts(config) {
		return errors.New("invalid runtime configuration")
	}
	return nil
}

func validRuntimePlanner(config Config) bool {
	return config.Planner != nil && strings.TrimSpace(config.Planner.Name()) != ""
}

func validRuntimeLimits(config Config) bool {
	return config.MaxCalls >= 1 && config.MaxCalls <= 16 && config.MaxRounds >= 1 && config.MaxRounds <= MaxPlannerRounds &&
		config.MaxEvidenceBytes >= 1 && config.MaxEvidenceBytes <= MaxPlannerEvidenceBytes
}

func validRuntimeTimeouts(config Config) bool {
	return config.Timeout >= time.Second && config.Timeout <= MaxPlannerTimeout && config.ToolTimeout > 0 && config.ToolTimeout <= config.Timeout
}

type registeredRuntimeTools struct {
	tools    map[string]Tool
	specs    []ToolSpec
	specByID map[string]ToolSpec
}

func registerRuntimeTools(tools []Tool) (registeredRuntimeTools, error) {
	registered := registeredRuntimeTools{tools: map[string]Tool{}, specByID: map[string]ToolSpec{}}
	for _, tool := range tools {
		if tool == nil {
			return registeredRuntimeTools{}, errors.New("nil tool")
		}
		spec := tool.Spec()
		if !validRegisteredTool(spec) {
			return registeredRuntimeTools{}, errors.New("only versioned read-only domain tools can be registered")
		}
		if err := validateToolCapabilities(spec.Capabilities); err != nil {
			return registeredRuntimeTools{}, err
		}
		if _, exists := registered.tools[spec.ID]; exists {
			return registeredRuntimeTools{}, errors.New("duplicate tool id")
		}
		spec.Capabilities = slices.Clone(spec.Capabilities)
		registered.tools[spec.ID] = tool
		registered.specs = append(registered.specs, spec)
		registered.specByID[spec.ID] = spec
	}
	if len(registered.specs) == 0 {
		return registeredRuntimeTools{}, errors.New("no tools configured")
	}
	return registered, nil
}

func validRegisteredTool(spec ToolSpec) bool {
	return spec.ID != "" && spec.Version != "" && validDomain(spec.Domain) && spec.Effect == "read"
}

func validateToolCapabilities(capabilities []string) error {
	if len(capabilities) == 0 || len(capabilities) > 16 {
		return errors.New("tool requires explicit bounded capabilities")
	}
	seen := make(map[string]bool, len(capabilities))
	for _, capability := range capabilities {
		if !capabilityID.MatchString(capability) || seen[capability] {
			return errors.New("invalid or duplicate tool capability")
		}
		seen[capability] = true
	}
	return nil
}

func compileRuntime(ctx context.Context, runtime *Runtime) (compose.Runnable[*execution, *execution], error) {
	chain := compose.NewChain[*execution, *execution]()
	chain.AppendLambda(compose.InvokableLambda(runtime.plan), compose.WithNodeName("scope_planning"))
	chain.AppendLambda(compose.InvokableLambda(runtime.guard), compose.WithNodeName("plan_guard"))
	chain.AppendLambda(compose.InvokableLambda(runtime.execute), compose.WithNodeName("bounded_read_tools"))
	return chain.Compile(ctx, compose.WithGraphName("operator_audit_controlled_v1"), compose.WithMaxRunSteps(8))
}

func (r *Runtime) Run(ctx context.Context, request Request) (Report, error) {
	state := newExecution(r, ctx, request)
	if err := r.prepareRun(ctx, state); err != nil {
		return state.report, err
	}
	if err := initializeExecutionState(state, r.specs); err != nil {
		return state.report, err
	}
	previousRound, err := acceptExecutionHistory(state)
	if err != nil {
		return state.report, err
	}
	remainingTime := remainingPlannerTime(r.config.Timeout, state.request.PriorElapsedMS)
	bounded, cancel := context.WithTimeout(ctx, remainingTime)
	defer cancel()
	calls, reads, runErr := r.runRounds(bounded, state, previousRound, remainingTime)
	finalizeRunReport(state, calls, reads, runErr, remainingTime, r.config)
	if runErr != nil {
		return state.report, runErr
	}
	return state.report, nil
}

func newExecution(r *Runtime, ctx context.Context, request Request) *execution {
	return &execution{
		request: request, gaps: map[string]Gap{}, resolved: map[string]bool{}, gapStates: map[string]string{},
		callEvidenceCounts: map[string]int{}, previousCallByPair: map[string]PriorCall{},
		gapEvidenceIDs: map[string]map[string]bool{}, findingsByGap: map[string][]Finding{}, evidenceIDs: map[string]bool{}, factsByID: map[string]SemanticFact{},
		verifiedFactsByID: map[string]VerifiedFact{}, knowledgeReadAttempts: map[KnowledgeReadRequest]bool{},
		knowledgeReadCalls: request.PriorKnowledgeReadCount, knowledgeReaderEnabled: r.config.ReadKnowledge != nil,
		sink: r.config.EventSink, sinkContext: ctx, clock: r.config.Clock,
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
}

func (r *Runtime) prepareRun(ctx context.Context, state *execution) error {
	if err := validateRunAccessAndRequest(ctx, state, r.config); err != nil {
		return err
	}
	if err := validateRunEvidenceContext(ctx, state, r.config); err != nil {
		return err
	}
	if err := r.validateRunPriorObservations(ctx, state); err != nil {
		return err
	}
	return freezeRunRequest(state, r.config)
}

func validateRunAccessAndRequest(ctx context.Context, state *execution, config Config) error {
	if ctx.Err() != nil {
		state.report.Status = "cancelled"
		return ctx.Err()
	}
	decision := access.Check(access.Review, state.request.Principal, state.request.Case, config.Clock())
	if !decision.Allowed {
		if err := state.event("authorization", "", "", decision.Reason, 0); err != nil {
			return err
		}
		return ErrDenied
	}
	if err := validateRequest(state.request); err != nil {
		return rejectRunInput(state, "input", "invalid_frozen_input", err)
	}
	return nil
}

func validateRunEvidenceContext(ctx context.Context, state *execution, config Config) error {
	totalEvidenceBytes := requestEvidenceBytes(state.request)
	reservedKnowledgeBytes := state.request.PriorKnowledgeReadCount * KnowledgeReadMaxBytes
	if reservedKnowledgeBytes > config.MaxEvidenceBytes || totalEvidenceBytes > config.MaxEvidenceBytes-reservedKnowledgeBytes {
		return rejectRunInput(state, "input", "input_budget_exceeded", ErrInput)
	}
	if err := validatePlannerContext(ctx, state.request); err != nil {
		return rejectRunInput(state, "input", "invalid_frozen_context", err)
	}
	if err := validateAdvisoryAgainstConfiguration(state.request.KnowledgeSelection, config.PlannerConfiguration); err != nil {
		return rejectRunInput(state, "input", "invalid_frozen_advisory", err)
	}
	return nil
}

func rejectRunInput(state *execution, kind, code string, cause error) error {
	if err := state.event(kind, "", "", code, 0); err != nil {
		return err
	}
	return cause
}

func (r *Runtime) validateRunPriorObservations(ctx context.Context, state *execution) error {
	if err := r.validatePriorToolResults(ctx, state.request); err != nil {
		return rejectRunInput(state, "input", "invalid_prior_observation", err)
	}
	return nil
}

func freezeRunRequest(state *execution, config Config) error {
	body, err := json.Marshal(state.request)
	if err != nil || len(body) > config.MaxEvidenceBytes+(2<<20) {
		return rejectRunInput(state, "input", "input_budget_exceeded", ErrInput)
	}
	var frozen Request
	if err := json.Unmarshal(body, &frozen); err != nil {
		return ErrInput
	}
	state.request = frozen
	totalEvidenceBytes := requestEvidenceBytes(frozen)
	reservedKnowledgeBytes := frozen.PriorKnowledgeReadCount * KnowledgeReadMaxBytes
	state.evidenceBytesLeft = config.MaxEvidenceBytes - totalEvidenceBytes - reservedKnowledgeBytes
	state.report.InputSHA256 = TextHash(string(body))
	return nil
}

func initializeExecutionState(state *execution, specs []ToolSpec) error {
	initializeDomainResults(state)
	if err := initializeGapStates(state, specs); err != nil {
		return err
	}
	seedVerifiedFacts(state)
	return nil
}

func initializeDomainResults(state *execution) {
	for _, domain := range Domains() {
		status := "not_requested"
		if slices.Contains(state.request.Scope, domain) {
			status = "unknown"
		}
		state.report.Domains = append(state.report.Domains, DomainResult{Domain: domain, Status: status, Findings: []Finding{}})
	}
}

func initializeGapStates(state *execution, specs []ToolSpec) error {
	for _, gap := range state.request.Gaps {
		state.gaps[gap.ID] = gap
		state.gapStates[gap.ID] = "open"
		if gap.IncompleteReason != "" {
			state.gapStates[gap.ID] = "unknown:" + gap.IncompleteReason
		}
		state.gapEvidenceIDs[gap.ID] = map[string]bool{}
		state.findingsByGap[gap.ID] = []Finding{}
		if gapHasRegisteredCapability(gap, specs) {
			continue
		}
		state.gapStates[gap.ID] = "unsupported:capability_not_registered"
		if err := state.eventCall("gap_unsupported", "", gap.Domain, "capability_not_registered", 0, "", gap.ID); err != nil {
			return err
		}
		state.domain(gap.Domain).Status = "partial"
		state.report.Status = "partial"
	}
	return nil
}

func gapHasRegisteredCapability(gap Gap, specs []ToolSpec) bool {
	for _, spec := range specs {
		if spec.Domain == gap.Domain && slices.Contains(spec.Capabilities, gap.Capability) {
			return true
		}
	}
	return false
}

func seedVerifiedFacts(state *execution) {
	for _, verified := range state.request.VerifiedFacts {
		fact := cloneFact(verified.Fact)
		verified.Fact = fact
		state.factsByID[fact.ID] = fact
		state.verifiedFactsByID[fact.ID] = verified
		state.evidenceIDs[fact.ID] = true
		state.gapEvidenceIDs[verified.GapID][fact.ID] = true
	}
}

func acceptExecutionHistory(state *execution) (int, error) {
	previousRound, err := state.acceptPriorToolResults()
	if err != nil {
		if sinkErr := state.event("input", "", "", "inconsistent_prior_observation", 0); sinkErr != nil {
			return 0, sinkErr
		}
		return 0, err
	}
	state.report.ToolCalls += state.request.PriorKnowledgeReadCount
	appendCanonicalFindings(state)
	state.reuseCanonicalChecks()
	return previousRound, nil
}

func appendCanonicalFindings(state *execution) {
	for _, evaluation := range state.request.CanonicalEvaluations {
		findings := state.domain(evaluation.Domain)
		finding := Finding{
			Domain: evaluation.Domain, Capability: evaluation.CapabilityID,
			RuleID: "canonical:" + evaluation.ID, Relation: evaluation.Relation,
			Summary: evaluation.ReasonCode, ReasonCode: evaluation.ReasonCode, Citations: []Citation{},
		}
		findings.Findings = append(findings.Findings, finding)
	}
}

func remainingPlannerTime(timeout time.Duration, elapsed *int64) time.Duration {
	remaining := timeout
	if elapsed == nil {
		return remaining
	}
	if *elapsed >= remaining.Milliseconds() {
		return 0
	}
	return remaining - time.Duration(*elapsed)*time.Millisecond
}

type roundResults struct {
	calls []Call
	reads []KnowledgeReadRequest
	err   error
}

func (r *Runtime) runRounds(ctx context.Context, state *execution, previousRound int, remainingTime time.Duration) ([]Call, []KnowledgeReadRequest, error) {
	result := roundResults{calls: make([]Call, 0, r.config.MaxCalls), reads: make([]KnowledgeReadRequest, 0, KnowledgeReadMaxCalls)}
	for round := previousRound + 1; round <= r.config.MaxRounds && state.report.ToolCalls < r.config.MaxCalls && remainingTime > 0; round++ {
		if !r.runRound(ctx, state, round, &result) {
			break
		}
	}
	return result.calls, result.reads, result.err
}

func (r *Runtime) runRound(ctx context.Context, state *execution, round int, result *roundResults) bool {
	state.round = round
	state.activeGaps = state.planningGaps()
	if len(state.activeGaps) == 0 {
		return false
	}
	state.roundProgress = false
	state.roundEvidenceIDs = []string{}
	state.roundResolvedIDs = []string{}
	state.report.Plan = Plan{Calls: []Call{}}
	appendRoundEntry(state, round)
	_, result.err = r.runnable.Invoke(ctx, state)
	if state.sinkErr != nil {
		result.err = state.sinkErr
	}
	index := len(state.report.Rounds) - 1
	result.calls = append(result.calls, state.report.Plan.Calls...)
	result.reads = append(result.reads, state.report.Plan.KnowledgeReads...)
	state.report.Rounds[index].NewEvidenceIDs = slices.Clone(state.roundEvidenceIDs)
	state.report.Rounds[index].ResolvedGapIDs = slices.Clone(state.roundResolvedIDs)
	if shouldRecordCancellation(ctx, state, result.err) {
		if sinkErr := state.event("run_cancelled", "", "", "cancelled_or_deadline", 0); sinkErr != nil {
			result.err = errors.Join(result.err, sinkErr)
		}
	}
	if result.err != nil {
		finishFailedRound(ctx, state, index, result.err)
		return false
	}
	return finishSuccessfulRound(state, round, index, r.config)
}

func appendRoundEntry(state *execution, round int) {
	entry := PlanRound{Round: round, Status: "accepted", GapIDs: make([]string, 0, len(state.activeGaps)),
		Calls: []PlannedCall{}, NewEvidenceIDs: []string{}, ResolvedGapIDs: []string{}}
	for _, gap := range state.activeGaps {
		entry.GapIDs = append(entry.GapIDs, gap.ID)
	}
	state.report.Rounds = append(state.report.Rounds, entry)
}

func shouldRecordCancellation(ctx context.Context, state *execution, err error) bool {
	return err != nil && state.sinkErr == nil && (ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

func finishFailedRound(ctx context.Context, state *execution, index int, err error) {
	if state.termination == "guard_rejected" {
		state.report.Rounds[index].Status = "guard_rejected"
		state.report.Rounds[index].TerminationCode = new("invalid_or_unauthorized_plan")
		if state.round == 1 && state.report.ToolCalls == 0 {
			state.report.Status = "rejected"
		} else {
			state.report.Status = "partial"
		}
		return
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		state.report.Rounds[index].Status = "cancelled"
		state.report.Rounds[index].TerminationCode = new("cancelled_or_deadline")
		state.report.Status = "cancelled"
		return
	}
	state.report.Rounds[index].Status = "failed"
	state.report.Rounds[index].TerminationCode = new("runtime_failure")
	state.report.Status = "partial"
}

func finishSuccessfulRound(state *execution, round, index int, config Config) bool {
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
		state.activeGaps = nil
	case state.report.ToolCalls >= config.MaxCalls:
		state.report.Rounds[index].Status = "budget_exhausted"
		state.report.Rounds[index].TerminationCode = new("max_calls_reached")
		state.report.Status = "partial"
		state.activeGaps = nil
	case round == config.MaxRounds:
		state.report.Rounds[index].Status = "partial"
		state.report.Rounds[index].TerminationCode = new("max_rounds_reached")
		state.report.Status = "partial"
		state.activeGaps = nil
	default:
		state.report.Rounds[index].Status = "partial"
	}
	return state.activeGaps != nil
}

func finalizeRunReport(state *execution, calls []Call, reads []KnowledgeReadRequest, runErr error, remainingTime time.Duration, config Config) {
	state.reconcileCanonicalChecks()
	finalizeEmptyPriorRun(state, remainingTime, config.MaxCalls)
	finalizeOpenGapsFromLastRound(state)
	state.report.Plan = Plan{Calls: calls, KnowledgeReads: reads}
	state.report.UnresolvedGapIDs = unresolvedGapIDs(state)
	finalizeReportStatus(state, runErr)
	state.report.Projection = buildAgentReview(state, config)
	if state.report.Projection != nil {
		state.report.Status = state.report.Projection.Status
		state.report.UnresolvedGapIDs = slices.Clone(state.report.Projection.UnresolvedGapIDs)
	}
}

func finalizeEmptyPriorRun(state *execution, remainingTime time.Duration, maxCalls int) {
	if len(state.report.Rounds) != 0 || (len(state.request.PriorObservations) == 0 && state.request.PriorKnowledgeReadCount == 0) {
		return
	}
	reason := "max_rounds_reached"
	if remainingTime <= 0 {
		reason = "max_duration_reached"
	}
	if state.report.ToolCalls >= maxCalls {
		reason = "max_calls_reached"
	}
	state.report.Limitations = append(state.report.Limitations, "Planning stopped: "+reason)
	for _, gap := range state.request.Gaps {
		if state.gapStates[gap.ID] == "open" {
			state.gapStates[gap.ID] = "unknown:" + reason
		}
	}
	state.report.Status = "partial"
}

func finalizeOpenGapsFromLastRound(state *execution) {
	if len(state.report.Rounds) == 0 {
		return
	}
	last := state.report.Rounds[len(state.report.Rounds)-1]
	if last.TerminationCode == nil {
		return
	}
	for _, gap := range state.request.Gaps {
		if state.gapStates[gap.ID] == "open" {
			state.gapStates[gap.ID] = "unknown:" + *last.TerminationCode
		}
	}
}

func unresolvedGapIDs(state *execution) []string {
	ids := make([]string, 0, len(state.request.Gaps))
	for _, gap := range state.request.Gaps {
		if !state.resolved[gap.ID] {
			ids = append(ids, gap.ID)
		}
	}
	return ids
}

func finalizeReportStatus(state *execution, runErr error) {
	if len(state.report.UnresolvedGapIDs) == 0 && len(state.report.Rounds) == 0 && runErr == nil {
		state.report.Status = "completed"
	}
	if len(state.report.UnresolvedGapIDs) > 0 && state.report.Status == "completed" {
		state.report.Status = "partial"
	}
}

func requestEvidenceBytes(request Request) int {
	total := 0
	if !addEvidenceSources(&total, request.Evidence) || !addKnowledgeReferenceBytes(&total, request.Knowledge) ||
		!addVerifiedFactBytes(&total, request.VerifiedFacts) || !addPriorFindingBytes(&total, request.PriorObservations) {
		return maxEvidenceBytes + 1
	}
	return total
}

func addEvidenceSources(total *int, evidence []Evidence) bool {
	for _, item := range evidence {
		if !addEvidenceBytes(total, len(item.Text)) {
			return false
		}
	}
	return true
}

func addKnowledgeReferenceBytes(total *int, references []KnowledgeReference) bool {
	for _, ref := range references {
		if !addEvidenceFields(total, ref.Summary, ref.Applicability, ref.Exclusions, ref.CheckAction, ref.MinimumEvidence,
			ref.Counterexamples, ref.SemanticChecks, ref.SourceSpans) {
			return false
		}
	}
	return true
}

func addVerifiedFactBytes(total *int, facts []VerifiedFact) bool {
	for _, verified := range facts {
		if !addEvidenceFields(total, verified.Fact.Value, verified.Fact.Unit, verified.Fact.Currency, verified.Fact.Citation.Quote) {
			return false
		}
	}
	return true
}

func addPriorFindingBytes(total *int, prior []PriorToolResult) bool {
	for _, result := range prior {
		if !addEvidenceBytes(total, returnedEvidenceBytes(result.Findings)) {
			return false
		}
	}
	return true
}

func addEvidenceFields(total *int, fields ...string) bool {
	for _, field := range fields {
		if !addEvidenceBytes(total, len(field)) {
			return false
		}
	}
	return true
}

func addEvidenceBytes(total *int, count int) bool {
	if count > maxEvidenceBytes-*total {
		*total = maxEvidenceBytes + 1
		return false
	}
	*total += count
	return true
}

func validateRequest(request Request) error {
	if !validRequestShape(request) || !validRequestScope(request.Scope) || !validRequestGaps(request.Scope, request.Gaps) || !validRequestEvidence(request) {
		return ErrInput
	}
	return nil
}

func validRequestShape(request Request) bool {
	return validRequestIdentityAndBounds(request) && validRequestHistoryBounds(request)
}

func validRequestIdentityAndBounds(request Request) bool {
	return boundedIdentity(request.RequestID) && len(request.Scope) > 0 && len(request.Scope) <= 5 &&
		len(request.Gaps) > 0 && len(request.Gaps) <= maxReviewGaps && len(request.Evidence) <= 40 &&
		len(request.Knowledge) <= MaxKnowledgeRecords && len(request.VerifiedFacts) <= maxContextReferences &&
		len(request.CanonicalEvaluations) <= maxContextReferences
}

func validRequestHistoryBounds(request Request) bool {
	return len(request.PriorObservations) <= DefaultPlannerCalls && request.PriorKnowledgeReadCount >= 0 &&
		request.PriorKnowledgeReadCount <= KnowledgeReadMaxCalls &&
		(request.PriorKnowledgeReadCount == 0 || request.KnowledgeSelection != nil)
}

func validRequestScope(scope []Domain) bool {
	seen := make(map[Domain]bool, len(scope))
	for _, domain := range scope {
		if !validDomain(domain) || seen[domain] {
			return false
		}
		seen[domain] = true
	}
	return true
}

func validRequestGaps(scope []Domain, gaps []Gap) bool {
	scopes := make(map[Domain]bool, len(scope))
	for _, domain := range scope {
		scopes[domain] = true
	}
	seen := make(map[string]bool, len(gaps))
	for _, gap := range gaps {
		if !validRequestGap(gap, scopes, seen) {
			return false
		}
		seen[gap.ID] = true
	}
	return true
}

func validRequestGap(gap Gap, scopes map[Domain]bool, seen map[string]bool) bool {
	return validRequestGapIdentity(gap, scopes, seen) && validRequestGapQuestion(gap) && validRequestGapReason(gap.IncompleteReason) && !gap.QuestionTruncated
}

func validRequestGapIdentity(gap Gap, scopes map[Domain]bool, seen map[string]bool) bool {
	return boundedIdentity(gap.ID) && capabilityID.MatchString(gap.Capability) && !seen[gap.ID] && scopes[gap.Domain]
}

func validRequestGapQuestion(gap Gap) bool {
	return len(strings.TrimSpace(gap.Question)) > 0 && len(gap.Question) <= 4096 && utf8.ValidString(gap.Question)
}

func validRequestGapReason(reason string) bool {
	return reason == "" || capabilityID.MatchString(reason)
}

func validRequestEvidence(request Request) bool {
	ids := make(map[string]bool, len(request.Evidence))
	total := 0
	for _, evidence := range request.Evidence {
		total += len(evidence.Text)
		// Reject the size before hashing or normalizing attacker-controlled text.
		if total > maxEvidenceBytes || !validEvidenceIdentity(evidence, request, ids) || !validEvidenceText(evidence) {
			return false
		}
		ids[evidence.ID] = true
	}
	return true
}

func validEvidenceIdentity(evidence Evidence, request Request, seen map[string]bool) bool {
	return evidence.ID != "" && len(evidence.ID) <= 128 && evidence.Version != "" && len(evidence.Version) <= 128 &&
		len(evidence.Role) <= 64 && !seen[evidence.ID] && evidence.TenantID == request.Case.TenantID &&
		evidence.CaseID == request.Case.CaseID && utf8.ValidString(evidence.Text)
}

func validEvidenceText(evidence Evidence) bool {
	return evidence.SHA256 == TextHash(evidence.Text) && docir.Build(evidence.Text).NormalizedText == evidence.Text
}

func (r *Runtime) plan(ctx context.Context, state *execution) (*execution, error) {
	input, err := r.plannerInputForRun(ctx, state)
	if err != nil {
		return state, err
	}
	plan, err := r.requestPlan(ctx, state, input)
	recordKnowledgePlanningRound(state, input, err)
	if ctx.Err() != nil {
		return state, ctx.Err()
	}
	if err != nil {
		plan, err = r.fallbackPlan(ctx, state, input, err)
		if err != nil {
			return state, err
		}
	}
	state.report.Plan = plan
	if err := state.event("plan", "", "", "candidate_plan", 0); err != nil {
		return state, err
	}
	return state, nil
}

func (r *Runtime) plannerInputForRun(ctx context.Context, state *execution) (PlanInput, error) {
	if r.config.AuthorizePlanning != nil {
		if err := r.config.AuthorizePlanning(ctx); err != nil {
			return PlanInput{}, fmt.Errorf("%w: %w", ErrPlanningAuthorization, err)
		}
	}
	input := state.plannerInput(r.config.MaxCalls - state.report.ToolCalls)
	state.plannerKnowledge = make(map[KnowledgeIdentity]bool, len(input.Knowledge))
	for _, ref := range input.Knowledge {
		state.plannerKnowledge[KnowledgeIdentity{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID, ContentSHA256: ref.ContentSHA256}] = true
	}
	for _, spec := range r.specs {
		if slices.Contains(input.Scope, spec.Domain) {
			spec.Capabilities = slices.Clone(spec.Capabilities)
			input.Tools = append(input.Tools, spec)
		}
	}
	return input, nil
}

func (r *Runtime) requestPlan(ctx context.Context, state *execution, input PlanInput) (Plan, error) {
	if observedPlanner, ok := r.config.Planner.(ObservedPlanner); ok {
		plan, observation, err := observedPlanner.PlanObserved(ctx, input)
		state.recordPlannerObservation(observation)
		return plan, err
	}
	plan, err := r.config.Planner.Plan(ctx, input)
	state.observations.PlannerCalls++
	return plan, err
}

func recordKnowledgePlanningRound(state *execution, input PlanInput, err error) {
	selection := state.request.KnowledgeSelection
	if selection == nil {
		return
	}
	round := KnowledgePlanningRound{Round: state.round, ReferencesProvided: []KnowledgeIdentity{}, ReadObservationsProvided: []KnowledgeReadIdentity{}, Outcome: "returned"}
	if err != nil {
		round.Outcome = "failed"
	}
	for _, ref := range input.Knowledge {
		round.ReferencesProvided = append(round.ReferencesProvided, KnowledgeIdentity{KnowledgeID: ref.KnowledgeID, VersionID: ref.VersionID, ContentSHA256: ref.ContentSHA256})
	}
	for _, observation := range input.KnowledgeObservations {
		round.ReadObservationsProvided = append(round.ReadObservationsProvided, observation.KnowledgeReadIdentity)
	}
	selection.PlannerRounds = append(selection.PlannerRounds, round)
}

func (r *Runtime) fallbackPlan(ctx context.Context, state *execution, input PlanInput, plannerErr error) (Plan, error) {
	if errors.Is(plannerErr, ErrPlan) {
		state.termination = "guard_rejected"
		if err := state.event("plan_rejected", "", "", "invalid_model_plan", 0); err != nil {
			return Plan{}, err
		}
		return Plan{}, ErrPlan
	}
	state.report.FallbackUsed = true
	if err := state.event("planner_fallback", "", "", "planner_unavailable", 0); err != nil {
		return Plan{}, err
	}
	plan, _ := (ScopePlanner{}).Plan(ctx, input)
	return plan, nil
}

func (r *Runtime) guard(_ context.Context, state *execution) (*execution, error) {
	calls := state.report.Plan.Calls
	r.recordCurrentRoundCalls(state, calls)
	active := activeGapIDs(state.activeGaps)
	if !validPlanGuardEnvelope(r, state, calls) {
		return rejectGuardPlan(state, "", "", "", "allowlist_scope_or_budget")
	}
	seen := make(map[string]bool, len(calls))
	for _, call := range calls {
		if !validGuardedCall(r, state, call, active, seen) {
			return rejectGuardPlan(state, call.ToolID, call.Domain, call.GapID, "allowlist_scope_or_budget")
		}
		seen[call.GapID] = true
	}
	if mandatoryGapPriorityViolation(r, state, calls, seen) {
		return rejectGuardPlan(state, "", "", "", "mandatory_gap_not_prioritized")
	}
	if err := state.event("plan_accepted", "", "", "all_calls_validated_before_execution", 0); err != nil {
		return state, err
	}
	return state, nil
}

func (r *Runtime) recordCurrentRoundCalls(state *execution, calls []Call) {
	index := len(state.report.Rounds) - 1
	if index >= 0 {
		state.report.Rounds[index].Calls = r.plannedCalls(state, calls)
	}
}

func activeGapIDs(gaps []Gap) map[string]bool {
	active := make(map[string]bool, len(gaps))
	for _, gap := range gaps {
		active[gap.ID] = true
	}
	return active
}

func validPlanGuardEnvelope(runtime *Runtime, state *execution, calls []Call) bool {
	return calls != nil && state.report.ToolCalls+len(calls)+len(state.report.Plan.KnowledgeReads) <= runtime.config.MaxCalls && runtime.validateKnowledgeReads(state) == nil
}

func validGuardedCall(runtime *Runtime, state *execution, call Call, active, seen map[string]bool) bool {
	spec, exists := runtime.specByID[call.ToolID]
	gap, known := state.gaps[call.GapID]
	return exists && known && active[call.GapID] && !seen[call.GapID] && validCallRetryEvidence(state, call, gap) &&
		validCallScope(spec, gap, call, state.request.Scope) && validCallReason(call.Reason)
}

func validCallRetryEvidence(state *execution, call Call, gap Gap) bool {
	priorEvidenceCount, wasCalled := state.callEvidenceCounts[call.GapID+"\x00"+call.ToolID]
	return !wasCalled || len(state.gapEvidenceIDs[gap.ID]) > priorEvidenceCount
}

func validCallScope(spec ToolSpec, gap Gap, call Call, scope []Domain) bool {
	return slices.Contains(scope, call.Domain) && spec.Domain == call.Domain &&
		slices.Contains(spec.Capabilities, gap.Capability) && gap.Domain == call.Domain
}

func validCallReason(reason string) bool {
	return strings.TrimSpace(reason) != "" && len(reason) <= 1024
}

func mandatoryGapPriorityViolation(runtime *Runtime, state *execution, calls []Call, seen map[string]bool) bool {
	for _, gap := range state.activeGaps {
		if !mandatoryGapNeedsCall(runtime, state, gap, seen) {
			continue
		}
		for _, call := range calls {
			if !state.gaps[call.GapID].Mandatory {
				return true
			}
		}
	}
	return false
}

func mandatoryGapNeedsCall(runtime *Runtime, state *execution, gap Gap, seen map[string]bool) bool {
	if !gap.Mandatory || seen[gap.ID] {
		return false
	}
	for _, spec := range runtime.specs {
		priorEvidenceCount, wasCalled := state.callEvidenceCounts[gap.ID+"\x00"+spec.ID]
		newEvidence := len(state.gapEvidenceIDs[gap.ID]) > priorEvidenceCount
		if spec.Domain == gap.Domain && slices.Contains(spec.Capabilities, gap.Capability) && (!wasCalled || newEvidence) {
			return true
		}
	}
	return false
}

func rejectGuardPlan(state *execution, tool string, domain Domain, gapID, code string) (*execution, error) {
	state.termination = "guard_rejected"
	var err error
	if tool == "" {
		err = state.event("plan_rejected", "", "", code, 0)
	} else {
		err = state.eventCall("plan_rejected", tool, domain, code, 0, "", gapID)
	}
	if err != nil {
		return state, err
	}
	return state, ErrPlan
}

func (r *Runtime) execute(ctx context.Context, state *execution) (*execution, error) {
	if err := r.executeKnowledgeReads(ctx, state); err != nil {
		return state, err
	}
	for _, call := range state.report.Plan.Calls {
		stop, err := r.executeToolCall(ctx, state, call)
		if err != nil {
			return state, err
		}
		if stop {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	return state, nil
}

func (r *Runtime) executeToolCall(ctx context.Context, state *execution, call Call) (bool, error) {
	callID := stableCallID(state.request.RequestID, state.round, call)
	started, err := r.beginToolCall(ctx, state, call, callID)
	if err != nil {
		return false, err
	}
	findings, bytes, fatal, err := r.runTool(ctx, state, call, callID, started)
	if fatal {
		return false, err
	}
	if err != nil {
		stop, failureErr := r.finishToolFailure(ctx, state, call, callID, started, err)
		if failureErr != nil {
			return false, failureErr
		}
		if errors.Is(err, ErrDenied) {
			return false, err
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return stop, nil
	}
	return false, r.finishToolSuccess(state, call, callID, started, findings, bytes)
}

func (r *Runtime) beginToolCall(ctx context.Context, state *execution, call Call, callID string) (time.Time, error) {
	if ctx.Err() != nil {
		state.updatePlannedCall(callID, "cancelled", "cancelled_or_deadline", nil)
		return time.Time{}, ctx.Err()
	}
	if !r.reviewAccessAllowed(state) {
		state.updatePlannedCall(callID, "denied", "authorization_revoked", nil)
		if err := state.eventCall("authorization", call.ToolID, call.Domain, "grant_expired_before_tool", 0, callID, call.GapID); err != nil {
			return time.Time{}, err
		}
		return time.Time{}, ErrDenied
	}
	if err := state.eventCall("tool_started", call.ToolID, call.Domain, "read_action_started", 0, callID, call.GapID); err != nil {
		return time.Time{}, err
	}
	state.updatePlannedCall(callID, "running", "", nil)
	return time.Now(), nil
}

func (r *Runtime) reviewAccessAllowed(state *execution) bool {
	return access.Check(access.Review, state.request.Principal, state.request.Case, r.config.Clock()).Allowed
}

func (r *Runtime) runTool(ctx context.Context, state *execution, call Call, callID string, started time.Time) ([]Finding, int, bool, error) {
	toolCtx, cancel := context.WithTimeout(ctx, r.config.ToolTimeout)
	state.report.ToolCalls++
	findings, err := r.tools[call.ToolID].Execute(toolCtx, ToolInput{
		TenantID: state.request.Case.TenantID, CaseID: state.request.Case.CaseID, ActorID: state.request.Principal.UserID,
		Gap: state.gaps[call.GapID], Evidence: slices.Clone(state.request.Evidence), PriorFindings: cloneFindings(state.findingsByGap[call.GapID]),
	})
	if toolCtx.Err() != nil {
		err = toolCtx.Err()
	}
	cancel()
	if err == nil && !r.reviewAccessAllowed(state) {
		err = ErrDenied
	}
	if err == nil {
		err = validateFindings(ctx, findings, call.Domain, state.gaps[call.GapID].Capability, state.request.Evidence)
	}
	returnedBytes, err := validateReturnedToolFindings(state, findings, err)
	if err == nil {
		clearUnverifiedLocations(findings)
		err = state.validateFactConsistency(findings)
	}
	if err == nil {
		if sinkErr := state.eventCall("tool_observed", call.ToolID, call.Domain, "validated_tool_observation", time.Since(started).Milliseconds(), callID, call.GapID); sinkErr != nil {
			return nil, 0, true, sinkErr
		}
		state.recordToolObservations(call, callID, findings)
		if !verifiedComparator(r.tools[call.ToolID]) {
			markClaimsUnverified(findings)
		}
		err = state.recordFindings(call.GapID, findings)
	}
	return findings, returnedBytes, false, err
}

func validateReturnedToolFindings(state *execution, findings []Finding, err error) (int, error) {
	if err != nil {
		return 0, err
	}
	returnedBytes := returnedEvidenceBytes(findings)
	if returnedBytes > state.evidenceBytesLeft {
		state.termination = "evidence_budget_exhausted"
		return 0, ErrEvidence
	}
	return returnedBytes, nil
}

func (r *Runtime) finishToolFailure(ctx context.Context, state *execution, call Call, callID string, started time.Time, err error) (bool, error) {
	pair := call.GapID + "\x00" + call.ToolID
	state.callEvidenceCounts[pair] = len(state.gapEvidenceIDs[call.GapID])
	state.previousCallByPair[pair] = PriorCall{ToolID: call.ToolID, GapID: call.GapID}
	status, code := toolFailureStatus(err, state.termination)
	state.gapStates[call.GapID] = "unknown:" + code
	state.domain(call.Domain).Status = "partial"
	state.updatePlannedCall(callID, status, code, nil)
	if sinkErr := state.eventCall("tool_failed", call.ToolID, call.Domain, code, time.Since(started).Milliseconds(), callID, call.GapID); sinkErr != nil {
		return false, sinkErr
	}
	if errors.Is(err, ErrDenied) {
		return false, nil
	}
	if ctx.Err() != nil {
		return false, nil
	}
	return state.termination == "evidence_budget_exhausted", nil
}

func toolFailureStatus(err error, termination string) (string, string) {
	status, code := "failed", "tool_error_or_unverified_result"
	if errors.Is(err, ErrDenied) {
		return "denied", "authorization_revoked_after_tool"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled", "cancelled_or_deadline"
	}
	if termination == "evidence_budget_exhausted" {
		code = "evidence_budget_exhausted"
	}
	return status, code
}

func (r *Runtime) finishToolSuccess(state *execution, call Call, callID string, started time.Time, findings []Finding, returnedBytes int) error {
	state.evidenceBytesLeft -= returnedBytes
	newState, resolved := gapOutcome(findings)
	if state.gapStates[call.GapID] != newState {
		state.gapStates[call.GapID] = newState
		state.roundProgress = true
	}
	if !state.resolved[call.GapID] && resolved {
		state.roundResolvedIDs = append(state.roundResolvedIDs, call.GapID)
		state.roundProgress = true
	}
	state.resolved[call.GapID] = resolved
	evidenceIDs := state.findingIDs(findings)
	pair := call.GapID + "\x00" + call.ToolID
	state.callEvidenceCounts[pair] = len(state.gapEvidenceIDs[call.GapID])
	state.previousCallByPair[pair] = PriorCall{ToolID: call.ToolID, GapID: call.GapID}
	domain := state.domain(call.Domain)
	domain.Status = completedDomainStatus(newState)
	domain.Findings = append(domain.Findings, findings...)
	state.updatePlannedCall(callID, "completed", "", evidenceIDs)
	if err := state.eventCall("tool_completed", call.ToolID, call.Domain, "validated_tool_result", time.Since(started).Milliseconds(), callID, call.GapID); err != nil {
		state.resolved[call.GapID] = false
		state.gapStates[call.GapID] = "unknown:event_sink_failed"
		state.domain(call.Domain).Status = "partial"
		state.updatePlannedCall(callID, "failed", "event_sink_failed", nil)
		return err
	}
	return nil
}

func completedDomainStatus(gapState string) string {
	switch gapState {
	case "resolved":
		return "completed"
	default:
		return "partial"
	}
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

func (state *execution) eventCall(kind, tool string, domain Domain, code string, milliseconds int64, callID, gapID string) error {
	sequence := len(state.report.Events) + 1
	if state.request.PriorEventCount > 0 && state.request.PriorEventCount <= 1024 {
		sequence += state.request.PriorEventCount
	}
	event := Event{EventID: "event_" + TextHash(fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%s", state.request.RequestID, sequence, state.round, kind, callID))[:24],
		Sequence: sequence, Round: state.round, Kind: kind, ToolID: tool, CallID: callID, GapID: gapID,
		Domain: domain, Code: code, DurationMS: milliseconds, OccurredAt: state.clock().UTC()}
	state.report.Events = append(state.report.Events, event)
	if state.sink != nil {
		if err := state.sink(state.sinkContext, event); err != nil {
			state.sinkErr = errors.Join(ErrEventSink, err)
			return state.sinkErr
		}
	}
	return nil
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

func (state *execution) validateFactConsistency(findings []Finding) error {
	seen := map[string]SemanticFact{}
	for _, finding := range findings {
		for _, fact := range finding.Facts {
			if prior, exists := state.factsByID[fact.ID]; exists && prior != fact {
				return ErrEvidence
			}
			if prior, exists := seen[fact.ID]; exists && prior != fact {
				return ErrEvidence
			}
			seen[fact.ID] = fact
		}
	}
	return nil
}

func (state *execution) recordFindings(gapID string, findings []Finding) error {
	for _, finding := range findings {
		for _, citation := range finding.Citations {
			state.recordEvidenceReference(gapID, citation.EvidenceID)
		}
		for _, fact := range finding.Facts {
			if _, exists := state.factsByID[fact.ID]; exists {
				continue
			}
			state.factsByID[fact.ID] = cloneFact(fact)
			state.verifiedFactsByID[fact.ID] = VerifiedFact{GapID: gapID, Domain: finding.Domain, Fact: cloneFact(fact)}
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

// Only the built-in deterministic comparators implement the finite semantic
// checks this runtime can attest. Other read-only tools can supply validated
// observations and typed facts, but a located quote cannot verify a claim.
func verifiedComparator(tool Tool) bool {
	switch tool.(type) {
	case baselineTool, sourceFactTool:
		return true
	default:
		return false
	}
}

func markClaimsUnverified(findings []Finding) {
	for i := range findings {
		switch findings[i].Relation {
		case "compatible", "conflict", "not_applicable":
			findings[i].Relation = "unknown"
			findings[i].ReasonCode = "claim_requires_semantic_verification"
		}
	}
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
	state := collectGapOutcome(findings)
	if outcome, found := prioritizedGapReason(state.insufficient, state.unsupported, state.unknown); found {
		return outcome, false
	}
	if state.decision || state.notApplicable {
		return "resolved", true
	}
	return "insufficient:no_verified_result", false
}

type gapOutcomeState struct {
	insufficient  string
	unsupported   string
	unknown       string
	decision      bool
	notApplicable bool
}

func collectGapOutcome(findings []Finding) gapOutcomeState {
	var state gapOutcomeState
	for _, finding := range findings {
		switch finding.Relation {
		case "compatible", "conflict":
			state.decision = true
		case "not_applicable":
			state.notApplicable = true
		case "insufficient":
			state.insufficient = finding.ReasonCode
		case "unsupported":
			state.unsupported = finding.ReasonCode
		case "unknown":
			state.unknown = finding.ReasonCode
		}
	}
	return state
}

func prioritizedGapReason(insufficient, unsupported, unknown string) (string, bool) {
	if insufficient != "" {
		return "insufficient:" + insufficient, true
	}
	if unsupported != "" {
		return "unsupported:" + unsupported, true
	}
	if unknown != "" {
		return "unknown:" + unknown, true
	}
	return "", false
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
	// This runtime has no frozen IR page/element map with which to verify these.
	citation.Page = nil
	citation.ElementID = ""
	return citation
}

func buildAgentReview(state *execution, config Config) *AgentReview {
	projection := newReviewProjection(state, config.PlannerConfiguration)
	appendProjectedCalls(projection, state)
	projectReviewScope(projection, state)
	projectReviewDomains(projection, state)
	applyKnowledgePolicyProjection(projection)
	projectVerifiedFacts(projection, state)
	projectReviewEvents(projection, state)
	projectToolObservations(projection, state)
	return projection
}

func projectToolObservations(projection *AgentReview, state *execution) {
	for _, observation := range state.toolObservations {
		observation.FactIDs = slices.Clone(observation.FactIDs)
		observation.EvidenceIDs = slices.Clone(observation.EvidenceIDs)
		projection.Observations = append(projection.Observations, observation)
	}
	for _, observation := range state.knowledgeObservations {
		projection.KnowledgeReads = append(projection.KnowledgeReads, observation)
	}
}

func newReviewProjection(state *execution, configuration PlannerConfiguration) *AgentReview {
	mode := plannerProjectionMode(configuration.Mode)
	fallbackCode := plannerFallbackCode(state.report.FallbackUsed)
	modelID := optionalString(configuration.ModelConfigurationID)
	rejectionCode := planRejectionCode(state.termination)
	return &AgentReview{
		SchemaVersion: AgentReviewSchemaVersion, AttemptID: state.request.AttemptID,
		Status: state.report.Status, Mode: mode, SourceSnapshot: cloneSourceSnapshot(state.request.SourceSnapshot),
		RulePack: cloneRulePackIdentity(state.request.RulePack), KnowledgeSelection: state.request.KnowledgeSelection,
		Planner: PlannerReport{Identity: state.report.Planner, ConfigurationID: configuration.ConfigurationID,
			ModelConfigurationID: modelID, PromptVersion: configuration.PromptVersion, PromptSHA256: configuration.PromptSHA256,
			SchemaVersion: configuration.SchemaVersion, RuntimeVersion: configuration.RuntimeVersion,
			ToolCatalogVersion: configuration.ToolCatalogVersion, FallbackUsed: state.report.FallbackUsed,
			FallbackCode: fallbackCode, Limits: configuration.Limits},
		Scope: []DomainScope{}, Domains: []DomainCoverage{}, Gaps: []GapReport{},
		Plan: AgentPlan{Status: planProjectionStatus(state.report.Status, state.report.Rounds, state.report.FallbackUsed, rejectionCode != nil),
			RejectionCode: rejectionCode, Calls: []PlannedCall{}, Rounds: cloneRounds(state.report.Rounds)},
		Evidence: []VerifiedEvidence{}, Usage: state.projectedUsage(),
		UnresolvedGapIDs: slices.Clone(state.report.UnresolvedGapIDs), Events: []AgentReviewEvent{},
	}
}

func plannerProjectionMode(mode string) string {
	if mode == "" {
		return ModeDeterministic
	}
	return mode
}

func plannerFallbackCode(fallbackUsed bool) *string {
	if fallbackUsed {
		return new("planner_unavailable_used_deterministic_fallback")
	}
	return nil
}

func planRejectionCode(termination string) *string {
	if termination == "guard_rejected" {
		return new("invalid_or_unauthorized_plan")
	}
	return nil
}

func appendProjectedCalls(projection *AgentReview, state *execution) {
	if state.report.Plan.Calls == nil {
		projection.Plan.Calls = []PlannedCall{}
	}
	for _, round := range state.report.Rounds {
		for _, call := range round.Calls {
			projection.Plan.Calls = append(projection.Plan.Calls, clonePlannedCall(call))
		}
	}
}

func projectReviewScope(projection *AgentReview, state *execution) {
	sourceIDs := make([]string, 0, len(state.request.Evidence))
	for _, evidence := range state.request.Evidence {
		sourceIDs = append(sourceIDs, evidence.ID)
	}
	slices.Sort(sourceIDs)
	sourceIDs = slices.Compact(sourceIDs)
	for _, domain := range state.request.Scope {
		capabilityIDs := make([]string, 0)
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
}

func applyKnowledgePolicyProjection(projection *AgentReview) {
	if projection.KnowledgeSelection == nil || projection.KnowledgeSelection.PolicyStatus == "available" {
		return
	}
	if projection.Status == "completed" {
		projection.Status = "partial"
	}
	for i := range projection.Domains {
		if projection.Domains[i].Domain == Compliance && projection.Domains[i].Status == "completed" {
			projection.Domains[i].Status = "partial"
		}
	}
}

func projectVerifiedFacts(projection *AgentReview, state *execution) {
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
				End: citation.End, Page: cloneIntPointer(citation.Page), ElementID: optionalString(citation.ElementID)}})
	}
}

func projectReviewEvents(projection *AgentReview, state *execution) {
	for _, event := range state.report.Events {
		projection.Events = append(projection.Events, projectedReviewEvent(event))
	}
}

func projectedReviewEvent(event Event) AgentReviewEvent {
	var callID *string
	if event.CallID != "" {
		callID = new(event.CallID)
	}
	var eventDomain *Domain
	if event.Domain != "" {
		eventDomain = new(event.Domain)
	}
	return AgentReviewEvent{EventID: event.EventID, Sequence: event.Sequence, Round: event.Round, Kind: event.Kind,
		ToolID: event.ToolID, GapID: event.GapID, CallID: callID, Domain: eventDomain, Code: event.Code,
		OccurredAt: new(event.OccurredAt), DurationMS: event.DurationMS}
}

func projectReviewDomains(projection *AgentReview, state *execution) {
	for _, domain := range state.report.Domains {
		coverage := projectReviewDomain(domain, projection, state)
		projection.Domains = append(projection.Domains, coverage)
		if coverage.Status == "partial" && projection.Status == "completed" {
			projection.Status = "partial"
		}
	}
}

func projectReviewDomain(domain DomainResult, projection *AgentReview, state *execution) DomainCoverage {
	coverage := DomainCoverage{Domain: domain.Domain, Status: domain.Status, Capabilities: []CapabilityResult{}}
	if !slices.Contains(state.request.Scope, domain.Domain) {
		return coverage
	}
	appendProjectedGaps(&coverage, projection, domain.Domain, state)
	appendProjectedCanonicalEvaluations(&coverage, domain.Domain, state)
	appendProjectedKnowledgeChecks(&coverage, projection, domain.Domain, state)
	finalizeDomainCoverage(&coverage)
	return coverage
}

func appendProjectedGaps(coverage *DomainCoverage, projection *AgentReview, domain Domain, state *execution) {
	for _, gap := range state.request.Gaps {
		if gap.Domain != domain {
			continue
		}
		result := projectedGapCapability(gap, state)
		coverage.Capabilities = append(coverage.Capabilities, result)
		projection.Gaps = append(projection.Gaps, GapReport{GapID: gap.ID, Domain: gap.Domain, Capability: gap.Capability,
			Mandatory: result.Mandatory, Status: capabilityGapStatus(result.Status), ReasonCode: result.ReasonCode,
			EvidenceIDs: slices.Clone(result.EvidenceIDs)})
	}
}

func projectedGapCapability(gap Gap, state *execution) CapabilityResult {
	status, reason := projectedGapState(state.gapStates[gap.ID])
	result := CapabilityResult{CapabilityID: gap.Capability, Mandatory: gap.Mandatory, Status: status,
		ReasonCode: reason, EvidenceIDs: []string{}, CanonicalEvaluationIDs: []string{}}
	if status == "resolved" {
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
	appendProjectedGapEvaluations(&result, gap, state)
	return result
}

func appendProjectedGapEvaluations(result *CapabilityResult, gap Gap, state *execution) {
	for _, evaluation := range state.request.CanonicalEvaluations {
		if evaluation.Domain != gap.Domain || evaluation.CapabilityID != gap.Capability {
			continue
		}
		result.CanonicalEvaluationIDs = append(result.CanonicalEvaluationIDs, evaluation.ID)
		result.Mandatory = result.Mandatory || evaluation.Mandatory
		result.Status, result.ReasonCode = state.canonicalEvaluationStatus(evaluation)
		if result.Status == "completed" || result.Status == evaluation.Relation {
			result.Relation = new(evaluation.Relation)
		} else {
			result.Relation = nil
		}
		result.EvidenceIDs = append(result.EvidenceIDs, evaluation.EvidenceIDs...)
	}
	if len(result.CanonicalEvaluationIDs) > 1 {
		result.Status, result.ReasonCode, result.Relation = "unknown", "multiple_canonical_results_require_review", nil
	}
	result.EvidenceIDs = sortedUnique(result.EvidenceIDs)
	result.CanonicalEvaluationIDs = sortedUnique(result.CanonicalEvaluationIDs)
}

func sortedUnique(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
}

func capabilityGapStatus(status string) string {
	if status == "completed" || status == "not_applicable" {
		return "resolved"
	}
	return status
}

func appendProjectedCanonicalEvaluations(coverage *DomainCoverage, domain Domain, state *execution) {
	for _, evaluation := range state.request.CanonicalEvaluations {
		if evaluation.Domain != domain || hasProjectionGap(state.request.Gaps, domain, evaluation.CapabilityID) {
			continue
		}
		coverage.Capabilities = append(coverage.Capabilities, projectedCanonicalCapability(evaluation, state))
	}
}

func hasProjectionGap(gaps []Gap, domain Domain, capability string) bool {
	for _, gap := range gaps {
		if gap.Domain == domain && gap.Capability == capability {
			return true
		}
	}
	return false
}

func projectedCanonicalCapability(evaluation CanonicalEvaluation, state *execution) CapabilityResult {
	status, reason := state.canonicalEvaluationStatus(evaluation)
	var relation *string
	if status == "completed" || status == evaluation.Relation {
		relation = new(evaluation.Relation)
	}
	return CapabilityResult{CapabilityID: evaluation.CapabilityID, Mandatory: evaluation.Mandatory, Status: status, Relation: relation,
		ReasonCode: reason, EvidenceIDs: sortedUnique(append([]string{}, evaluation.EvidenceIDs...)), CanonicalEvaluationIDs: []string{evaluation.ID}}
}

func appendProjectedKnowledgeChecks(coverage *DomainCoverage, projection *AgentReview, domain Domain, state *execution) {
	if domain != Compliance {
		return
	}
	for _, check := range knowledgePolicyChecks(state.request.KnowledgeSelection) {
		coverage.Capabilities = append(coverage.Capabilities, CapabilityResult{CapabilityID: check.Capability, Mandatory: true,
			Status: check.Status, ReasonCode: check.Reason, EvidenceIDs: []string{}, CanonicalEvaluationIDs: []string{}})
		projection.Gaps = append(projection.Gaps, GapReport{GapID: check.GapID, Domain: Compliance, Capability: check.Capability,
			Mandatory: true, Status: check.Status, ReasonCode: check.Reason, EvidenceIDs: []string{}})
		projection.UnresolvedGapIDs = append(projection.UnresolvedGapIDs, check.GapID)
	}
}

func finalizeDomainCoverage(coverage *DomainCoverage) {
	if len(coverage.Capabilities) == 0 {
		return
	}
	coverage.Status = "completed"
	for _, capability := range coverage.Capabilities {
		if capability.Status != "completed" && capability.Status != "not_applicable" {
			coverage.Status = "partial"
			return
		}
	}
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
	return clonePointer(value)
}

func cloneIntPointer(value *int) *int {
	return clonePointer(value)
}

func cloneStringPointer(value *string) *string {
	return clonePointer(value)
}

func clonePointer[T any](value *T) *T {
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
	byID, runesByID, err := indexFindingEvidence(ctx, evidence)
	if err != nil {
		return err
	}
	totals := findingValidationTotals{}
	for _, finding := range findings {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateOneFinding(ctx, finding, domain, capability, byID, runesByID, &totals); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

type findingValidationTotals struct {
	citations  int
	facts      int
	quoteBytes int
}

func indexFindingEvidence(ctx context.Context, evidence []Evidence) (map[string]Evidence, map[string][]rune, error) {
	byID, runesByID := map[string]Evidence{}, map[string][]rune{}
	for _, item := range evidence {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		byID[item.ID] = item
		runesByID[item.ID] = []rune(item.Text)
	}
	return byID, runesByID, nil
}

func validateOneFinding(ctx context.Context, finding Finding, domain Domain, capability string, byID map[string]Evidence, runesByID map[string][]rune, totals *findingValidationTotals) error {
	totals.citations += len(finding.Citations)
	if !validFindingEnvelope(finding, domain, capability, totals.citations) ||
		!validFindingRelation(finding.Relation, finding.ReasonCode, len(finding.Citations)) {
		return ErrEvidence
	}
	if err := validateFindingCitations(ctx, finding.Citations, byID, runesByID, totals); err != nil {
		return err
	}
	totals.facts += len(finding.Facts)
	if totals.facts > 128 {
		return ErrEvidence
	}
	return validateFindingFacts(ctx, finding, byID)
}

func validFindingEnvelope(finding Finding, domain Domain, capability string, citationCount int) bool {
	return validFindingIdentity(finding, domain, capability) && validFindingText(finding) && validFindingCitationTotals(finding, citationCount)
}

func validFindingIdentity(finding Finding, domain Domain, capability string) bool {
	return finding.Domain == domain && finding.Capability == capability && finding.RuleID != "" && len(finding.RuleID) <= 128
}

func validFindingText(finding Finding) bool {
	return finding.Summary != "" && len(finding.Summary) <= 8192 && utf8.ValidString(finding.Summary) &&
		len(finding.ReasonCode) <= 128 && utf8.ValidString(finding.ReasonCode)
}

func validFindingCitationTotals(finding Finding, citationCount int) bool {
	return len(finding.Citations) <= maxCitationsPerFinding && citationCount <= maxCitationsTotal
}

func validFindingRelation(relation, reason string, citations int) bool {
	switch relation {
	case "compatible", "conflict":
		return citations > 0
	case "insufficient", "unsupported", "unknown":
		return reason != "" && len(reason) <= 128
	case "not_applicable":
		return true
	default:
		return false
	}
}

func validateFindingCitations(ctx context.Context, citations []Citation, byID map[string]Evidence, runesByID map[string][]rune, totals *findingValidationTotals) error {
	for _, citation := range citations {
		if err := ctx.Err(); err != nil {
			return err
		}
		totals.quoteBytes += len(citation.Quote)
		if totals.quoteBytes > maxQuoteBytes || !validFindingCitation(citation, byID, runesByID) {
			return ErrEvidence
		}
	}
	return nil
}

func validFindingCitation(citation Citation, byID map[string]Evidence, runesByID map[string][]rune) bool {
	item, exists := byID[citation.EvidenceID]
	text := runesByID[citation.EvidenceID]
	return exists && citation.Version == item.Version && citation.Quote != "" &&
		citation.Start >= 0 && citation.End > citation.Start && citation.End <= len(text) &&
		string(text[citation.Start:citation.End]) == citation.Quote
}

func validateFindingFacts(ctx context.Context, finding Finding, byID map[string]Evidence) error {
	for _, fact := range finding.Facts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateSemanticFact(fact, finding, byID); err != nil {
			return err
		}
	}
	return nil
}

func validateSemanticFact(fact SemanticFact, finding Finding, evidence map[string]Evidence) error {
	if !validSemanticFactIdentity(fact, finding.Domain) {
		return ErrEvidence
	}
	citation := fact.Citation
	item, exists := evidence[citation.EvidenceID]
	if !validFactCitation(citation, item, exists) {
		return ErrEvidence
	}
	if !factValueMatchesQuote(fact, citation.Quote) || !findingAnchorsFact(finding, citation) {
		return ErrEvidence
	}
	if !validSemanticFactMeasure(fact) || !validSemanticFactValue(fact) {
		return ErrEvidence
	}
	return nil
}

func validSemanticFactIdentity(fact SemanticFact, domain Domain) bool {
	return capabilityID.MatchString(fact.ID) && validSemanticFactType(domain, fact.Type) &&
		strings.TrimSpace(fact.Value) != "" && len(fact.Value) <= 4096 && utf8.ValidString(fact.Value) &&
		len(fact.Unit) <= 64 && len(fact.Currency) <= 16
}

func validFactCitation(citation Citation, item Evidence, exists bool) bool {
	return exists && validFactCitationSource(citation, item) && validFactCitationDigest(citation, item)
}

func validFactCitationSource(citation Citation, item Evidence) bool {
	return citation.Version == item.Version && citation.DocumentID != "" && citation.DocumentID == item.DocumentID &&
		citation.ParsedDocumentID != "" && citation.ParsedDocumentID == item.ParsedDocumentID
}

func validFactCitationDigest(citation Citation, item Evidence) bool {
	return citation.Status == "located" &&
		citation.NormalizedDigest != "" && citation.NormalizedDigest == item.NormalizedDigest &&
		normalizedDigest.MatchString(citation.NormalizedDigest)
}

func factValueMatchesQuote(fact SemanticFact, quote string) bool {
	if sourceSemanticType(fact.Type) {
		value, unit, currency, ok := normalizeSemanticQuote(fact.Type, quote)
		return ok && value == fact.Value && unit == fact.Unit && currency == fact.Currency
	}
	return fact.Type == "amount" || fact.Type == "liability_cap" || strings.Contains(quote, fact.Value)
}

func findingAnchorsFact(finding Finding, citation Citation) bool {
	for _, candidate := range finding.Citations {
		if sameFactCitation(candidate, citation) {
			return true
		}
	}
	return false
}

func sameFactCitation(left, right Citation) bool {
	return left.EvidenceID == right.EvidenceID && left.Version == right.Version && left.Quote == right.Quote &&
		left.Start == right.Start && left.End == right.End && left.DocumentID == right.DocumentID &&
		left.ParsedDocumentID == right.ParsedDocumentID && left.NormalizedDigest == right.NormalizedDigest && left.Status == right.Status
}

func validSemanticFactMeasure(fact SemanticFact) bool {
	if isAmountFactType(fact.Type) {
		amount, ok := new(big.Rat).SetString(fact.Value)
		return ok && amount.Sign() >= 0 && fact.Unit != "" && validCurrency(fact.Currency)
	}
	if isSourceDateFactType(fact.Type) {
		return (fact.Unit == "date" || fact.Unit == "instant") && fact.Currency == ""
	}
	return fact.Unit == "" && fact.Currency == ""
}

func validSemanticFactValue(fact SemanticFact) bool {
	return !(fact.Type == "subject_id" || strings.HasPrefix(fact.Type, "subject_id_")) ||
		!strings.ContainsAny(fact.Value, " \t\r\n")
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
func (state *execution) event(kind, tool string, domain Domain, code string, milliseconds int64) error {
	return state.eventCall(kind, tool, domain, code, milliseconds, "", "")
}

func (state *execution) domain(want Domain) *DomainResult {
	for i := range state.report.Domains {
		if state.report.Domains[i].Domain == want {
			return &state.report.Domains[i]
		}
	}
	panic(fmt.Sprintf("validated domain missing: %s", want))
}

// validateAdvisoryAgainstConfiguration keeps a trace from claiming a different
// advisory model, threshold or feature than the accepted run. A nil frozen
// configuration is the legacy deterministic path; it must not carry an
// advisory trace.
func validateAdvisoryAgainstConfiguration(selection *KnowledgeSelectionRecord, planner PlannerConfiguration) error {
	if selection == nil || selection.Advisory == nil {
		return nil
	}
	if !validAdvisoryTraceIdentity(selection.Advisory, planner) {
		return ErrInput
	}
	return validateCompletedAdvisoryTrace(selection.Advisory, planner.Advisory)
}

func validAdvisoryTraceIdentity(advisory *KnowledgeAdvisoryRecord, planner PlannerConfiguration) bool {
	configuration := planner.Advisory
	return configuration != nil && configuration.Supports(AdvisoryFeatureKnowledgeRerank) &&
		advisory.ConfigurationID == planner.ConfigurationID && advisory.Mode == configuration.Mode &&
		advisory.Provider == configuration.Provider && advisory.QuestionSetVersion == configuration.QuestionSetVersion &&
		advisory.Egress == configuration.Egress && (advisory.ModelRequested == "" || advisory.ModelRequested == configuration.Model)
}

func validateCompletedAdvisoryTrace(advisory *KnowledgeAdvisoryRecord, configuration *AdvisoryConfiguration) error {
	if advisory.Status != "completed" {
		return nil
	}
	if advisory.ModelObserved != configuration.Model {
		return ErrInput
	}
	for _, candidate := range advisory.Candidates {
		if !validAdvisoryCandidateBand(candidate, configuration) {
			return ErrInput
		}
	}
	return nil
}

func validAdvisoryCandidateBand(candidate KnowledgeAdvisoryCandidate, configuration *AdvisoryConfiguration) bool {
	if candidate.Probability == nil {
		return candidate.Band == "unknown"
	}
	band, err := advisoryBandFor(*candidate.Probability, configuration.ThresholdHigh, configuration.ThresholdLow)
	return err == nil && candidate.Band == band
}

func advisoryBandFor(probability, high, low float64) (string, error) {
	if !validAdvisoryProbability(probability) || !validAdvisoryThresholds(high, low) {
		return "", ErrInput
	}
	switch {
	case probability >= high:
		return "relevant", nil
	case probability <= low:
		return "not_relevant", nil
	default:
		return "uncertain", nil
	}
}

// The closed interval already rejects NaN and both infinities: NaN compares
// false against every bound, and an infinity fails the bound on its own side.
func validAdvisoryProbability(value float64) bool {
	return value >= 0 && value <= 1
}

func validAdvisoryThresholds(high, low float64) bool {
	return low > 0 && high < 1 && low < high
}
