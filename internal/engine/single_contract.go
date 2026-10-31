package engine

import (
	"encoding/json"
	"fmt"
)

// SingleContractPrecheckManifest is the published, versioned mapping shipped
// with this binary. Its exact bytes identify the implementation; older mappings
// must remain available when a later version is introduced.
const SingleContractPrecheckManifest = `{"id":"single-contract-precheck","version":"1","scope":"single_contract_precheck","status":"published","rules":[{"id":"single-payment-sum-v1","domain":"payment_sum"}]}`

const SingleContractPrecheckRule = "single-payment-sum-v1"

var (
	singleContractRuleSetHash  = sha256hex(SingleContractPrecheckManifest)
	singleContractPlaybookHash = sha256hex("single-contract-precheck/v1\n" + SingleContractPrecheckManifest)
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
	var mapping SingleContractMapping
	if hash != SingleContractRuleSetHash() || json.Unmarshal([]byte(SingleContractPrecheckManifest), &mapping) != nil ||
		mapping.ID != "single-contract-precheck" || mapping.Version != "1" || mapping.Scope != "single_contract_precheck" ||
		mapping.Status != "published" || len(mapping.Rules) != 1 || mapping.Rules[0].ID != SingleContractPrecheckRule || mapping.Rules[0].Domain != "payment_sum" {
		return SingleContractMapping{}, fmt.Errorf("single-contract rule mapping is unavailable")
	}
	return mapping, nil
}

func SingleContractRuleSetHash() string  { return singleContractRuleSetHash }
func SingleContractPlaybookHash() string { return singleContractPlaybookHash }

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
