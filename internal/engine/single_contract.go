// File metadata (manifest mirror):
// Purpose: Defines deterministic single-contract precheck capabilities, supported rules and compatible identities for persisted review executions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-19
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package engine

import (
	"encoding/json"
	"fmt"
)

const SingleContractPrecheckRule = "single-payment-sum-v1"
const SingleContractPaymentTermsRule = "single-payment-terms-v2"
const SingleContractPaymentTermsCapability = "single_contract.payment_terms"

const singleContractPrecheckManifestV1 = `{"id":"single-contract-precheck","version":"1","scope":"single_contract_precheck","status":"published","rules":[{"id":"single-payment-sum-v1","domain":"payment_sum"}]}`

// SingleContractPrecheckManifest is the exact, published mapping for new
// reviews. Older mappings remain available to the attempts that froze them.
const SingleContractPrecheckManifest = `{"id":"single-contract-precheck","version":"2","scope":"single_contract_precheck","status":"published","rules":[{"id":"single-payment-terms-v2","domain":"payment_terms"}]}`

var (
	singleContractRuleSetHash  = sha256hex(SingleContractPrecheckManifest)
	singleContractPlaybookHash = sha256hex("single-contract-precheck/v2\n" + SingleContractPrecheckManifest)
	singleContractV1RuleHash   = sha256hex(singleContractPrecheckManifestV1)
	singleContractV1PlayHash   = sha256hex("single-contract-precheck/v1\n" + singleContractPrecheckManifestV1)
)

type SingleContractMapping struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Scope   string `json:"scope"`
	Status  string `json:"status"`
	Rules   []struct {
		ID     string `json:"id"`
		Domain string `json:"domain"`
	} `json:"rules"`
}

// PublishedSingleContractMapping resolves the immutable release and refuses an
// unrecognized identity instead of silently executing another rule version.
func PublishedSingleContractMapping(hash string) (SingleContractMapping, error) {
	var manifest, version, rule, domain string
	switch hash {
	case singleContractV1RuleHash:
		manifest, version, rule, domain = singleContractPrecheckManifestV1, "1", SingleContractPrecheckRule, "payment_sum"
	case singleContractRuleSetHash:
		manifest, version, rule, domain = SingleContractPrecheckManifest, "2", SingleContractPaymentTermsRule, "payment_terms"
	}
	var mapping SingleContractMapping
	if manifest == "" || json.Unmarshal([]byte(manifest), &mapping) != nil ||
		mapping.ID != "single-contract-precheck" || mapping.Version != version || mapping.Scope != "single_contract_precheck" ||
		mapping.Status != "published" || len(mapping.Rules) != 1 || mapping.Rules[0].ID != rule || mapping.Rules[0].Domain != domain {
		return SingleContractMapping{}, fmt.Errorf("single-contract rule mapping is unavailable")
	}
	return mapping, nil
}

func SingleContractRuleSetHash() string  { return singleContractRuleSetHash }
func SingleContractPlaybookHash() string { return singleContractPlaybookHash }

func SingleContractPlaybookMatches(ruleHash, playbookHash string) bool {
	return (ruleHash == singleContractRuleSetHash && playbookHash == singleContractPlaybookHash) ||
		(ruleHash == singleContractV1RuleHash && playbookHash == singleContractV1PlayHash)
}

func HasSingleContractIdentity(ruleHash, playbookHash string) bool {
	return ruleHash == singleContractRuleSetHash || ruleHash == singleContractV1RuleHash ||
		playbookHash == singleContractPlaybookHash || playbookHash == singleContractV1PlayHash
}

// EvaluateSingleContractPaymentSum computes only the payment phrases this
// typed extractor recognizes in the primary contract. The extractor cannot
// establish that all payments share a basis or that no unrecognized clause
// exists, so even a total of 100% remains an observation needing review.
func EvaluateSingleContractPaymentSum(doc Source) Evaluation {
	groups := paymentGroups([]Source{doc})
	ev := Evaluation{Domain: "payment_sum", RuleVersion: SingleContractPrecheckRule, Relation: RelInsufficient,
		Facts: paymentFacts(groups), Missing: missingAnchors([]Source{doc})}
	if doc.ParseStatus != "ready" {
		ev.Reason = "主合同未完成解析，付款比例检查未执行；请重新解析并核对原文。"
	} else if len(groups) == 0 {
		ev.Reason = "未识别到支持的付款比例表达；不能推断合同未约定付款，需人工核对原文。"
	} else {
		ev.Reason = fmt.Sprintf("主合同中识别到的付款比例合计 %d%%；规则只识别部分固定表达，无法确认是否遗漏条款或付款基数一致，请人工核对。", groups[0].total)
	}
	return ev
}
