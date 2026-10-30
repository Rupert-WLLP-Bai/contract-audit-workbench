package legalnorm

import "fmt"

type statusFormat string

const (
	statusNPCJSON statusFormat = "npc-json-v1"
	statusMOJHTML statusFormat = "moj-html-v1"
	statusFixture statusFormat = "fixture-json-v1"
)

type sourceAdapter struct {
	sourceID                string
	versionID               string
	title                   string
	officialURL             string
	promulgatedOn           string
	effectiveFrom           string
	statusEffectiveAt       string
	statusFormat            statusFormat
	statusRecordID          string
	registryTitle           string
	baselineRawSHA256       string
	extractorID             string
	extractorVersion        string
	canonicalizationVersion string
	fixture                 bool
}

var officialAdapters = map[string]sourceAdapter{
	"S1_civil_code_contract": {
		sourceID: "CN-CIVIL-CODE", versionID: "2020-05-28",
		title: "中华人民共和国民法典（第三编 合同）", officialURL: "https://www.cac.gov.cn/2020-06/01/c_15925617772683192.htm",
		registryTitle:     "中华人民共和国民法典",
		baselineRawSHA256: "1253542d80bd1276c4eb237c7ab223589b770f8e6e0463b878d2a9d362393f6b",
		promulgatedOn:     "2020-05-28", effectiveFrom: "2021-01-01",
		statusEffectiveAt: "2021-01-01", statusRecordID: "ff808081729d1efe01729d50b5c500bf",
		statusFormat: statusNPCJSON, extractorID: "cac-civil-code-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
	},
	"S2_tender_law_2017": {
		sourceID: "CN-TENDER-LAW", versionID: "2017-12-27-amended",
		title: "中华人民共和国招标投标法", officialURL: "https://www.ccgp.gov.cn/zcfg/gjfg/201311/t20131113_3591998.htm",
		registryTitle:     "中华人民共和国招标投标法",
		baselineRawSHA256: "c13e04be96a48ab9d6201fddd1cc6b1e2ce079f52949140d913766465d89b069",
		promulgatedOn:     "2017-12-27", effectiveFrom: "2017-12-28",
		statusEffectiveAt: "2017-12-28", statusRecordID: "2c909fdd678bf17901678bf88f170b31",
		statusFormat: statusNPCJSON, extractorID: "ccgp-tender-law-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
	},
	"S3_government_procurement_law_2014": {
		sourceID: "CN-GOV-PROCUREMENT-LAW", versionID: "2014-08-31-amended",
		title: "中华人民共和国政府采购法", officialURL: "https://www.ccgp.gov.cn/zcfg/gjfg/201310/t20131029_3587339.htm",
		registryTitle:     "中华人民共和国政府采购法",
		baselineRawSHA256: "ce8892810b1c8ee08f278448649a8697e61a4d3f7a87cd15da59b91cf49a73ca",
		promulgatedOn:     "2014-08-31", effectiveFrom: "2014-08-31",
		statusEffectiveAt: "2014-08-31", statusRecordID: "2c909fdd678bf17901678bf77e170753",
		statusFormat: statusNPCJSON, extractorID: "ccgp-government-procurement-law-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
	},
	"S4_sme_payment_regulation_2025": {
		sourceID: "CN-SME-PAYMENT-REG", versionID: "2025-03-17-revised",
		title: "保障中小企业款项支付条例", officialURL: "https://www.gov.cn/gongbao/2025/issue_11966/202504/content_7017465.html",
		registryTitle:     "保障中小企业款项支付条例",
		baselineRawSHA256: "0482da8f865bff850e60969b898e9d12623f29435aed82136232aab71d114f86",
		promulgatedOn:     "2025-03-17", effectiveFrom: "2025-06-01",
		statusEffectiveAt: "2025-06-01",
		statusFormat:      statusMOJHTML, extractorID: "gov-sme-payment-regulation-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
	},
	"S5_tender_implementing_regulation_2019": {
		sourceID: "CN-TENDER-IMPLEMENTING-REG", versionID: "2019-03-02-revised",
		title: "中华人民共和国招标投标法实施条例", officialURL: "https://xzfg.moj.gov.cn/front/law/detail?LawID=339",
		registryTitle:     "中华人民共和国招标投标法实施条例",
		baselineRawSHA256: "6d85e3c258596444872a0835871b9f172d710dd2343337519424a506a8806667",
		promulgatedOn:     "2019-03-02", effectiveFrom: "2019-03-02",
		statusEffectiveAt: "2019-03-02",
		statusFormat:      statusMOJHTML, extractorID: "moj-tender-implementing-regulation-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
	},
}

func adapterForSnapshot(snapshotID string) (sourceAdapter, error) {
	adapter, ok := officialAdapters[snapshotID]
	if !ok {
		return sourceAdapter{}, fmt.Errorf("unsupported normative source snapshot %q", snapshotID)
	}
	return adapter, nil
}

func adapterForSource(sourceID, versionID string) (sourceAdapter, error) {
	for _, adapter := range officialAdapters {
		if adapter.sourceID == sourceID && adapter.versionID == versionID {
			return adapter, nil
		}
	}
	return sourceAdapter{}, fmt.Errorf("unsupported normative source edition %q/%q", sourceID, versionID)
}
