package engine

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	PaymentTermsObserved     = "payment_terms_observed"
	PaymentTermsUnrecognized = "payment_terms_unrecognized"
	PaymentTermsNoText       = "payment_terms_parse_unavailable"
)

type PaymentTermsObservation struct {
	ReasonCode string
	Reason     string
	Anchors    []PaymentTermReference
}

type PaymentTermReference struct {
	Anchor
	TermKinds []string
}

const paymentRatioTerm = "installment_ratio"
const maximumPaymentRatios = 24
const percentage = `(?:[０-９0-9]+(?:[.．][０-９0-9]+)?\s*[%％]|百分之\s*[０-９0-9]+(?:[.．][０-９0-9]+)?)`

var (
	paymentSentence = regexp.MustCompile(`[^。！？；;\r\n]+[。！？；;]?`)
	paymentAction   = regexp.MustCompile(`结算|支付|付款|付清`)
	billingAction   = regexp.MustCompile(`计费|计价|结算|核算费用|计算费用|费用计算`)
	pricingBasis    = regexp.MustCompile(`据实|按量|实际(?:使用|用量|调用|查询|核验|验证|服务|发生|完成|交易|业务|数量|次数|消费|产生)|(?:单价|结算价格).{0,26}(?:次数|数量|用量|账单)|(?:次数|数量|用量|账单).{0,26}(?:单价|结算价格)`)
	reconcile       = regexp.MustCompile(`对账|核对|确认账单|确认结算|确认无误|发票|票据|验收`)
	paymentTrigger  = regexp.MustCompile(`后|之日起|经.{0,26}确认|作为.{0,12}依据|以.{0,24}为准|前须|前应|凭.{0,12}发票`)
	paymentCycle    = regexp.MustCompile(`每(?:个)?(?:自然)?月|次月|按月|逐月|月度|每季度|按季度|每(?:半)?年|按年|结算周期`)
	paymentDeadline = regexp.MustCompile(`[０-９0-9一二两三四五六七八九十百]+\s*(?:个\s*)?(?:工作日|自然日|日|天|月)(?:\s*(?:内|前|之内|以内))`)
	paymentMethod   = regexp.MustCompile(`银行转账|银行汇款|电汇|银行承兑|支票|现金|汇(?:入|至).{0,18}(?:账户|账号)|支付(?:至|到).{0,18}(?:账户|账号)`)
	installment     = regexp.MustCompile(`(?:预付款|首付款|首期款|进度款|尾款|质保金|分期付款)(?:比例)?\s*[:：为]?\s*` + percentage +
		`|(?:支付|付清|支付至)\s*(?:合同(?:总价款|总金额|总价|价款|金额)|订单金额|应付价款)(?:的)?\s*` + percentage +
		`|(?:验收(?:通过|合格)后|设备到货后|到货后|质保期满后?)\s*[,，]?\s*(?:支付)?\s*` + percentage +
		`|` + percentage + `\s*(?:(?:作为|为)\s*)?(?:预付款|首付款|首期款|进度款|尾款|质保金)`)
	nonInstallment  = regexp.MustCompile(`税率|违约金|违约率|折扣|利率|滞纳金|手续费率|赔偿比例`)
	templatePayment = regexp.MustCompile(`仅为示例|以下示例|待填|另行(?:协商|约定)|未约定|不(?:按|以|采用)|\t.+\d+$`)
)

// ObserveSingleContractPaymentTerms locates concrete terms in the selected
// primary contract. It does not infer compliance, a total or omitted terms.
func ObserveSingleContractPaymentTerms(doc Source) PaymentTermsObservation {
	if doc.ParseStatus != "ready" || doc.ir().NormalizedText == "" {
		return PaymentTermsObservation{ReasonCode: PaymentTermsNoText, Reason: "主合同没有可读取的冻结文本，付款与结算原文核对未执行。"}
	}
	text := doc.ir().NormalizedText
	best := make(map[string][]Anchor)
	ratioSeen := make(map[string]bool)
	for _, loc := range paymentSentence.FindAllStringIndex(text, -1) {
		raw := text[loc[0]:loc[1]]
		quote := strings.TrimSpace(raw)
		if quote == "" || utf8.RuneCountInString(quote) > 260 || templatePayment.MatchString(quote) {
			continue
		}
		action := paymentAction.MatchString(quote)
		reconciled := reconcile.MatchString(quote) && paymentTrigger.MatchString(quote)
		matches := [5]bool{
			pricingBasis.MatchString(quote) && billingAction.MatchString(quote),
			paymentCycle.MatchString(quote) && (action || reconcile.MatchString(quote)),
			reconciled && action,
			paymentDeadline.MatchString(quote) && action,
			paymentMethod.MatchString(quote) && action,
		}
		quoteStart := loc[0] + strings.Index(raw, quote)
		var bound Anchor
		for i, matched := range matches {
			kind := paymentTermKinds[i]
			if !matched || len(best[kind]) > 0 {
				continue
			}
			if bound.Status != "located" {
				bound, _ = bindSpan(doc, quote, quoteStart)
			}
			if bound.Status == "located" {
				best[kind] = []Anchor{bound}
			}
		}
		if len(best[paymentRatioTerm]) >= maximumPaymentRatios {
			continue
		}
		for _, match := range installment.FindAllStringIndex(quote, -1) {
			phrase := quote[match[0]:match[1]]
			key := strings.Join(strings.Fields(phrase), "")
			if ratioSeen[key] || nonInstallment.MatchString(ratioContext(quote, match[0], match[1])) {
				continue
			}
			anchor, ok := bindSpan(doc, phrase, quoteStart+match[0])
			if ok {
				ratioSeen[key] = true
				best[paymentRatioTerm] = append(best[paymentRatioTerm], anchor)
				if len(best[paymentRatioTerm]) == maximumPaymentRatios {
					break
				}
			}
		}
	}
	return paymentTermsResult(best)
}

func ratioContext(sentence string, matchStart, matchEnd int) string {
	start := 0
	if comma := strings.LastIndexAny(sentence[:matchStart], "，,"); comma >= 0 {
		start = comma
	}
	end := len(sentence)
	if comma := strings.IndexAny(sentence[matchEnd:], "，,"); comma >= 0 {
		end = matchEnd + comma
	}
	return sentence[start:end]
}

var paymentTermKinds = [...]string{"pricing_basis", "settlement_cycle", "payment_trigger", "payment_deadline", "payment_method", paymentRatioTerm}
var paymentTermLabels = [...]string{"计价与结算口径", "对账或结算周期", "付款前置条件", "付款期限", "付款方式", "明示分期比例"}

func PaymentTermLabel(kind string) string {
	for index, value := range paymentTermKinds {
		if value == kind {
			return paymentTermLabels[index]
		}
	}
	return ""
}

func paymentTermsResult(best map[string][]Anchor) PaymentTermsObservation {
	if len(best) == 0 {
		return PaymentTermsObservation{ReasonCode: PaymentTermsUnrecognized, Reason: "本规则未定位支持的付款与结算约定，覆盖仍需核对。"}
	}
	seen := map[[2]int]int{}
	var anchors []PaymentTermReference
	var labels []string
	for i, kind := range paymentTermKinds {
		values := best[kind]
		if len(values) == 0 {
			continue
		}
		labels = append(labels, paymentTermLabels[i])
		for _, anchor := range values {
			key := [2]int{anchor.Start, anchor.End}
			if existing, ok := seen[key]; ok {
				anchors[existing].TermKinds = append(anchors[existing].TermKinds, kind)
				continue
			}
			seen[key] = len(anchors)
			anchors = append(anchors, PaymentTermReference{Anchor: anchor, TermKinds: []string{kind}})
		}
	}
	reason := "已定位主合同原文中的" + strings.Join(labels, "、") + "。"
	if len(best[paymentRatioTerm]) == 0 {
		reason += "本规则未定位明示分期付款比例。"
	}
	reason += "核对范围及付款基数仍需确认。"
	return PaymentTermsObservation{ReasonCode: PaymentTermsObserved, Reason: reason, Anchors: anchors}
}
