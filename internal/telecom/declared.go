package telecom

import (
	"context"
	"crypto/sha256"
	"fmt"

	"contractaudit/internal/rulepack"
)

// DeclaredCompilerRef pins the bounded fact extraction and authority checks for
// data-declared business scopes. It is frozen separately from the RulePack.
const DeclaredCompilerRef = "declared-typed-extractor/v1"

// ValidateDeclaredPack checks that a published pack can be executed by the
// host's existing typed extractors for exactly the selected business scope.
// The pack's lifecycle and hashes must also be checked by the trusted loader.
func ValidateDeclaredPack(pack *rulepack.Pack, scope rulepack.BusinessScope, tenantID string) error {
	if pack == nil || tenantID == "" {
		return rulepack.ErrInvalidManifest
	}
	manifest := pack.Manifest()
	if manifest.Lifecycle.State != rulepack.StatePublished || manifest.TenantID != tenantID || len(manifest.Rules) == 0 {
		return rulepack.ErrInvalidManifest
	}
	for _, rule := range manifest.Rules {
		if rule.TenantID != tenantID || rule.BusinessScope != scope || declaredComparatorIndex(rule.Comparator) < 0 ||
			len(rule.RequiredFact.SourceRoles) != 1 || rule.RequiredFact.SourceRoles[0] != "draft_contract" {
			return rulepack.ErrInvalidManifest
		}
	}
	return nil
}

func declaredComparatorIndex(comparator string) int {
	switch comparator {
	case rulepack.ComparatorMinimumBandwidth:
		return 0
	case rulepack.ComparatorMonthlySLA:
		return 1
	case rulepack.ComparatorAdvancePaymentCeiling:
		return 2
	default:
		return -1
	}
}

func declaredScope(request rulepack.Request) rulepack.BusinessScope {
	return rulepack.BusinessScope{BusinessLine: request.BusinessLine, Scenario: request.Scenario, ContractType: request.ContractType}
}

// declaredExtractionRequest selects only the host-owned extraction vocabulary;
// the actual RulePack and its business scope remain unchanged for execution.
func declaredExtractionRequest(request rulepack.Request) rulepack.Request {
	request.BusinessLine = "ENT"
	request.Scenario = "telecom_dedicated_line"
	request.ContractType = "service"
	return request
}

// ExtractDeclared binds facts extracted from the frozen sources to the rule
// identities in the published pack. No patterns or executable code are loaded
// from the declaration.
func ExtractDeclared(request rulepack.Request, pack *rulepack.Pack) ([]rulepack.Fact, error) {
	if err := ValidateDeclaredPack(pack, declaredScope(request), request.Resource.TenantID); err != nil {
		return nil, err
	}
	if err := rulepack.ValidateSources(request); err != nil {
		return nil, err
	}
	proxy := declaredExtractionRequest(request)
	facts, err := Extract(proxy)
	if err != nil {
		return nil, err
	}
	byComparator := make(map[string][]rulepack.Fact, len(ruleDefinitions))
	for _, fact := range facts {
		byComparator[fact.RuleID] = append(byComparator[fact.RuleID], fact)
	}
	result := make([]rulepack.Fact, 0, len(facts))
	for _, rule := range pack.Manifest().Rules {
		index := declaredComparatorIndex(rule.Comparator)
		for _, original := range byComparator[ruleDefinitions[index].id] {
			fact := original
			identity := sha256.Sum256([]byte(rule.RuleID + "\x00" + original.ID))
			fact.ID = fmt.Sprintf("fact/%x", identity)
			fact.RuleID = rule.RuleID
			result = append(result, fact)
		}
	}
	return result, nil
}

// ExecuteDeclared evaluates the exact published pack and applies the same
// source-bound authority, object, amount and SLA safeguards as the existing
// typed extractor. Missing authority remains an insufficient result.
func ExecuteDeclared(ctx context.Context, request rulepack.Request, pack *rulepack.Pack) (rulepack.Report, error) {
	if err := validateExecutionContext(ctx, request); err != nil {
		return rulepack.Report{}, err
	}
	if request.At.IsZero() {
		return rulepack.Report{}, rulepack.ErrInvalidRequest
	}
	if err := ValidateDeclaredPack(pack, declaredScope(request), request.Resource.TenantID); err != nil {
		return rulepack.Report{}, err
	}
	if err := rulepack.ValidateSources(request); err != nil {
		return rulepack.Report{}, err
	}
	if request.Facts == nil {
		facts, err := ExtractDeclared(request, pack)
		if err != nil {
			return rulepack.Report{}, err
		}
		request.Facts = facts
	}
	report, err := pack.Execute(ctx, request)
	if err != nil {
		return rulepack.Report{}, err
	}
	proxy := declaredExtractionRequest(request)
	proxy.Scenario = "cloud_services"
	var baselines [3]clause
	var reasons [3]string
	var extracted [3]bool
	manifest := pack.Manifest()
	declarations := make(map[string]rulepack.Rule, len(manifest.Rules))
	for _, rule := range manifest.Rules {
		declarations[rule.RuleID] = rule
	}
	for i, result := range report.Results {
		if err := ctx.Err(); err != nil {
			return rulepack.Report{}, err
		}
		if legalAuthorityBlocks(result) {
			continue
		}
		declaration, found := declarations[result.RuleID]
		if !found {
			return rulepack.Report{}, rulepack.ErrInvalidManifest
		}
		index := declaredComparatorIndex(declaration.Comparator)
		if !extracted[index] {
			baselines[index], reasons[index] = baseline(proxy, index)
			extracted[index] = true
		}
		base, reason := baselines[index], reasons[index]
		if reason != "" {
			report.Results[i].Relation = rulepack.RelationInsufficient
			report.Results[i].ReasonCode = reason
			report.Results[i].FactIDs = nil
			report.Results[i].Citations = nil
			continue
		}
		for _, fact := range request.Facts {
			if fact.RuleID == declaration.RuleID {
				checkBaselineFact(&report.Results[i], fact, base, request, index == 2)
			}
		}
		if index == 1 {
			var definitions [3]ruleDefinition
			copy(definitions[:], ruleDefinitions)
			definitions[1].id = declaration.RuleID
			checkSLAResult(&report.Results[i], proxy, definitions[:], base)
		}
		report.Results[i].Citations = append(report.Results[i].Citations, base.fact.Citations...)
	}
	return report, nil
}
