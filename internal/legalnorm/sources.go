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
	effectiveUntil          string
	pinEffectiveUntil       bool
	statusEffectiveAt       string
	statusFormat            statusFormat
	statusRecordID          string
	registryTitle           string
	extractorID             string
	extractorVersion        string
	canonicalizationVersion string
	baseline                *officialBaseline
	fixture                 bool
}

type officialBaseline struct {
	OfficialURL          string
	SourceContentType    string
	SourceCapturedAt     string
	SourceResponseStatus int
	RawSHA256            string
	StatusEvidenceID     string
	StatusRegistryURL    string
	StatusRequestMethod  string
	StatusRequestBody    []byte
	StatusContentType    string
	StatusObservedAt     string
	StatusResponseStatus int
	StatusSHA256         string
	Publisher            string
	Issuer               string
	DocumentType         string
}

var officialAdapters = map[string]sourceAdapter{
	"S1_civil_code_contract": {
		sourceID: "CN-CIVIL-CODE", versionID: "2020-05-28",
		title: "中华人民共和国民法典（第三编 合同）", officialURL: "https://www.cac.gov.cn/2020-06/01/c_15925617772683192.htm",
		registryTitle: "中华人民共和国民法典",
		promulgatedOn: "2020-05-28", effectiveFrom: "2021-01-01",
		statusEffectiveAt: "2021-01-01", statusRecordID: "ff808081729d1efe01729d50b5c500bf",
		statusFormat: statusNPCJSON, extractorID: "cac-civil-code-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
		baseline: &officialBaseline{
			OfficialURL: "https://www.cac.gov.cn/2020-06/01/c_15925617772683192.htm", SourceContentType: "text/html; charset=utf-8",
			SourceCapturedAt: "2026-10-30T23:47:25+08:00", SourceResponseStatus: 200,
			RawSHA256:        "1253542d80bd1276c4eb237c7ab223589b770f8e6e0463b878d2a9d362393f6b",
			StatusEvidenceID: "S8A_npc_civil_code_status", StatusRegistryURL: "https://flk.npc.gov.cn/law-search/search/list",
			StatusRequestMethod: "POST", StatusRequestBody: []byte(`{"searchContent":"中华人民共和国民法典","searchType":1,"searchRange":1,"pageNum":1,"pageSize":50,"sxx":[],"sxrq":[],"flfgCodeId":[],"zdjgCodeId":[],"gbrqYear":[],"orderByParam":{"order":"-1","sort":""}}`),
			StatusContentType: "application/json", StatusObservedAt: "2026-10-31T00:01:34+08:00", StatusResponseStatus: 200,
			StatusSHA256: "7deedafcc53209809cd5d94de0c57b544baffb1b947ba8152cf2b6df61e3d6e4",
			Publisher:    "中国网信网", Issuer: "全国人民代表大会", DocumentType: "law",
		},
	},
	"S2_tender_law_2017": {
		sourceID: "CN-TENDER-LAW", versionID: "2017-12-27-amended",
		title: "中华人民共和国招标投标法", officialURL: "https://www.ccgp.gov.cn/zcfg/gjfg/201311/t20131113_3591998.htm",
		registryTitle: "中华人民共和国招标投标法",
		promulgatedOn: "2017-12-27", effectiveFrom: "2017-12-28",
		statusEffectiveAt: "2017-12-28", statusRecordID: "2c909fdd678bf17901678bf88f170b31",
		statusFormat: statusNPCJSON, extractorID: "ccgp-tender-law-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
		baseline: &officialBaseline{
			OfficialURL: "https://www.ccgp.gov.cn/zcfg/gjfg/201311/t20131113_3591998.htm", SourceContentType: "text/html",
			SourceCapturedAt: "2026-10-30T23:47:26+08:00", SourceResponseStatus: 200,
			RawSHA256:        "c13e04be96a48ab9d6201fddd1cc6b1e2ce079f52949140d913766465d89b069",
			StatusEvidenceID: "S8B_npc_tender_law_status", StatusRegistryURL: "https://flk.npc.gov.cn/law-search/search/list",
			StatusRequestMethod: "POST", StatusRequestBody: []byte(`{"searchContent":"中华人民共和国招标投标法","searchType":1,"searchRange":1,"pageNum":1,"pageSize":50,"sxx":[],"sxrq":[],"flfgCodeId":[],"zdjgCodeId":[],"gbrqYear":[],"orderByParam":{"order":"-1","sort":""}}`),
			StatusContentType: "application/json", StatusObservedAt: "2026-10-31T00:01:34+08:00", StatusResponseStatus: 200,
			StatusSHA256: "6c12a813462a51362b75e3228575d5a6a4ca6d54a7a45ed8cb05a7f1b1094764",
			Publisher:    "中国政府采购网", Issuer: "全国人民代表大会常务委员会", DocumentType: "law",
		},
	},
	"S3_government_procurement_law_2014": {
		sourceID: "CN-GOV-PROCUREMENT-LAW", versionID: "2014-08-31-amended",
		title: "中华人民共和国政府采购法", officialURL: "https://www.ccgp.gov.cn/zcfg/gjfg/201310/t20131029_3587339.htm",
		registryTitle: "中华人民共和国政府采购法",
		promulgatedOn: "2014-08-31", effectiveFrom: "2014-08-31",
		statusEffectiveAt: "2014-08-31", statusRecordID: "2c909fdd678bf17901678bf77e170753",
		statusFormat: statusNPCJSON, extractorID: "ccgp-government-procurement-law-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
		baseline: &officialBaseline{
			OfficialURL: "https://www.ccgp.gov.cn/zcfg/gjfg/201310/t20131029_3587339.htm", SourceContentType: "text/html",
			SourceCapturedAt: "2026-10-30T23:47:26+08:00", SourceResponseStatus: 200,
			RawSHA256:        "ce8892810b1c8ee08f278448649a8697e61a4d3f7a87cd15da59b91cf49a73ca",
			StatusEvidenceID: "S8C_npc_procurement_law_status", StatusRegistryURL: "https://flk.npc.gov.cn/law-search/search/list",
			StatusRequestMethod: "POST", StatusRequestBody: []byte(`{"searchContent":"中华人民共和国政府采购法","searchType":1,"searchRange":1,"pageNum":1,"pageSize":50,"sxx":[],"sxrq":[],"flfgCodeId":[],"zdjgCodeId":[],"gbrqYear":[],"orderByParam":{"order":"-1","sort":""}}`),
			StatusContentType: "application/json", StatusObservedAt: "2026-10-31T00:01:35+08:00", StatusResponseStatus: 200,
			StatusSHA256: "fc788d3fe4b48f5489e107abdd86d553f2e5d1e7bf0c06020349778df9accb2f",
			Publisher:    "中国政府采购网", Issuer: "全国人民代表大会常务委员会", DocumentType: "law",
		},
	},
	"S4_sme_payment_regulation_2025": {
		sourceID: "CN-SME-PAYMENT-REG", versionID: "2025-03-17-revised",
		title: "保障中小企业款项支付条例", officialURL: "https://www.gov.cn/gongbao/2025/issue_11966/202504/content_7017465.html",
		registryTitle: "保障中小企业款项支付条例",
		promulgatedOn: "2025-03-17", effectiveFrom: "2025-06-01",
		statusEffectiveAt: "2025-06-01",
		statusFormat:      statusMOJHTML, extractorID: "gov-sme-payment-regulation-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
		baseline: &officialBaseline{
			OfficialURL: "https://www.gov.cn/gongbao/2025/issue_11966/202504/content_7017465.html", SourceContentType: "text/html",
			SourceCapturedAt: "2026-10-30T23:47:27+08:00", SourceResponseStatus: 200,
			RawSHA256:        "0482da8f865bff850e60969b898e9d12623f29435aed82136232aab71d114f86",
			StatusEvidenceID: "S9B_moj_sme_payment_status", StatusRegistryURL: "https://xzfg.moj.gov.cn/SearchTitleFront?QueryAll=%E4%BF%9D%E9%9A%9C%E4%B8%AD%E5%B0%8F%E4%BC%81%E4%B8%9A%E6%AC%BE%E9%A1%B9%E6%94%AF%E4%BB%98%E6%9D%A1%E4%BE%8B",
			StatusRequestMethod: "GET", StatusRequestBody: []byte(""),
			StatusContentType: "text/html;charset=UTF-8", StatusObservedAt: "2026-10-31T00:01:36+08:00", StatusResponseStatus: 200,
			StatusSHA256: "55e786d64e1531899f0c367ff85f4c7f460bd009c77d694782f11cab280d21a2",
			Publisher:    "中国政府网（国务院公报）", Issuer: "国务院", DocumentType: "administrative_regulation",
		},
	},
	"S5_tender_implementing_regulation_2019": {
		sourceID: "CN-TENDER-IMPLEMENTING-REG", versionID: "2019-03-02-revised",
		title: "中华人民共和国招标投标法实施条例", officialURL: "https://xzfg.moj.gov.cn/front/law/detail?LawID=339",
		registryTitle: "中华人民共和国招标投标法实施条例",
		promulgatedOn: "2019-03-02", effectiveFrom: "2019-03-02",
		statusEffectiveAt: "2019-03-02",
		statusFormat:      statusMOJHTML, extractorID: "moj-tender-implementing-regulation-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
		baseline: &officialBaseline{
			OfficialURL: "https://xzfg.moj.gov.cn/front/law/detail?LawID=339", SourceContentType: "text/html;charset=UTF-8",
			SourceCapturedAt: "2026-10-30T23:47:28+08:00", SourceResponseStatus: 200,
			RawSHA256:        "6d85e3c258596444872a0835871b9f172d710dd2343337519424a506a8806667",
			StatusEvidenceID: "S9A_moj_tender_implementing_status", StatusRegistryURL: "https://xzfg.moj.gov.cn/SearchTitleFront?QueryAll=%E4%B8%AD%E5%8D%8E%E4%BA%BA%E6%B0%91%E5%85%B1%E5%92%8C%E5%9B%BD%E6%8B%9B%E6%A0%87%E6%8A%95%E6%A0%87%E6%B3%95%E5%AE%9E%E6%96%BD%E6%9D%A1%E4%BE%8B",
			StatusRequestMethod: "GET", StatusRequestBody: []byte(""),
			StatusContentType: "text/html;charset=UTF-8", StatusObservedAt: "2026-10-31T00:01:36+08:00", StatusResponseStatus: 200,
			StatusSHA256: "baa14e8b98555f3a5ddc769c308995a00018141a56ee05d31be10dda454a6b44",
			Publisher:    "司法部国家行政法规库", Issuer: "国务院", DocumentType: "administrative_regulation",
		},
	},
	"S10_contract_law_history": {
		sourceID: "CN-CONTRACT-LAW-HISTORY", versionID: "1999-03-15-original",
		title: "中华人民共和国合同法", officialURL: "https://ipc.court.gov.cn/zh-cn/news/view-387.html",
		registryTitle: "中华人民共和国合同法", promulgatedOn: "1999-03-15", effectiveFrom: "1999-10-01",
		effectiveUntil: "2021-01-01", pinEffectiveUntil: true,
		statusEffectiveAt: "1999-10-01", statusRecordID: "2c909fdd678bf17901678bf6053a0217",
		statusFormat: statusNPCJSON, extractorID: "spc-ipc-contract-history-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
		baseline: &officialBaseline{
			OfficialURL: "https://ipc.court.gov.cn/zh-cn/news/view-387.html", SourceContentType: "text/html; charset=UTF-8",
			SourceCapturedAt: "2026-10-30T23:58:34+08:00", SourceResponseStatus: 200,
			RawSHA256:        "34fbedabb99a3c671b50ceeb019c389976264b616cabeed41f266b0d2b350047",
			StatusEvidenceID: "S8D_npc_contract_law_status", StatusRegistryURL: "https://flk.npc.gov.cn/law-search/search/list",
			StatusRequestMethod: "POST", StatusRequestBody: []byte(`{"searchContent":"中华人民共和国合同法","searchType":1,"searchRange":1,"pageNum":1,"pageSize":50,"sxx":[],"sxrq":[],"flfgCodeId":[],"zdjgCodeId":[],"gbrqYear":[],"orderByParam":{"order":"-1","sort":""}}`),
			StatusContentType: "application/json", StatusObservedAt: "2026-10-31T00:01:35+08:00", StatusResponseStatus: 200,
			StatusSHA256: "7702e08f57cb3b1f130f3306648ece1c874f1e35e42a3886e64e1a1742de078b",
			Publisher:    "最高人民法院知识产权法庭", Issuer: "全国人民代表大会", DocumentType: "law",
		},
	},
	"S12_sc_time_effect_2020_15": {
		sourceID: "CN-SPC-TIME-EFFECT", versionID: "2020-12-29-fashi-2020-15",
		title: "最高人民法院关于适用《中华人民共和国民法典》时间效力的若干规定", officialURL: "https://www.court.gov.cn/fabu/xiangqing/282051.html",
		registryTitle: "最高人民法院关于适用《中华人民共和国民法典》时间效力的若干规定",
		promulgatedOn: "2020-12-29", effectiveFrom: "2021-01-01", pinEffectiveUntil: true,
		statusEffectiveAt: "2021-01-01", statusRecordID: "ff80808177e757ac017800561d541abb",
		statusFormat: statusNPCJSON, extractorID: "spc-time-effect-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
		baseline: &officialBaseline{
			OfficialURL: "https://www.court.gov.cn/fabu/xiangqing/282051.html", SourceContentType: "text/html; charset=utf-8",
			SourceCapturedAt: "2026-10-30T23:58:34+08:00", SourceResponseStatus: 200,
			RawSHA256:        "04438ce4f63060abfa5c2bb61dd34b6be7a965f761981aa5da4e2849c93f14bc",
			StatusEvidenceID: "S8E_npc_time_effect_status", StatusRegistryURL: "https://flk.npc.gov.cn/law-search/search/list",
			StatusRequestMethod: "POST", StatusRequestBody: []byte(`{"searchContent":"最高人民法院关于适用《中华人民共和国民法典》时间效力的若干规定","searchType":1,"searchRange":1,"pageNum":1,"pageSize":50,"sxx":[],"sxrq":[],"flfgCodeId":[],"zdjgCodeId":[],"gbrqYear":[],"orderByParam":{"order":"-1","sort":""}}`),
			StatusContentType: "application/json", StatusObservedAt: "2026-10-31T00:01:35+08:00", StatusResponseStatus: 200,
			StatusSHA256: "9e42fe13818fe443c2500c122ceb6eae0f95996e13eb04e5a52aa939c4dc1b9d",
			Publisher:    "最高人民法院", Issuer: "最高人民法院", DocumentType: "judicial_interpretation",
		},
	},
	"S13_sc_contract_general_2023_13": {
		sourceID: "CN-SPC-CONTRACT-GENERAL-INTERPRETATION", versionID: "2023-12-04-fashi-2023-13",
		title: "最高人民法院关于适用《中华人民共和国民法典》合同编通则若干问题的解释", officialURL: "https://www.court.gov.cn/zixun/xiangqing/419382.html",
		registryTitle: "最高人民法院关于适用《中华人民共和国民法典》合同编通则若干问题的解释",
		promulgatedOn: "2023-12-04", effectiveFrom: "2023-12-05", pinEffectiveUntil: true,
		statusEffectiveAt: "2023-12-05", statusRecordID: "ff8081818c24e05b018c814e6de45ab5",
		statusFormat: statusNPCJSON, extractorID: "spc-contract-general-html", extractorVersion: "1",
		canonicalizationVersion: CanonicalTextVersion,
		baseline: &officialBaseline{
			OfficialURL: "https://www.court.gov.cn/zixun/xiangqing/419382.html", SourceContentType: "text/html; charset=utf-8",
			SourceCapturedAt: "2026-10-30T23:58:35+08:00", SourceResponseStatus: 200,
			RawSHA256:        "60dd9e30c5eb6e9079cf113efd414fd6cd08827ca175bf62fa2e51f86d80155b",
			StatusEvidenceID: "S8F_npc_contract_general_status", StatusRegistryURL: "https://flk.npc.gov.cn/law-search/search/list",
			StatusRequestMethod: "POST", StatusRequestBody: []byte(`{"searchContent":"最高人民法院关于适用《中华人民共和国民法典》合同编通则若干问题的解释","searchType":1,"searchRange":1,"pageNum":1,"pageSize":50,"sxx":[],"sxrq":[],"flfgCodeId":[],"zdjgCodeId":[],"gbrqYear":[],"orderByParam":{"order":"-1","sort":""}}`),
			StatusContentType: "application/json", StatusObservedAt: "2026-10-31T00:01:35+08:00", StatusResponseStatus: 200,
			StatusSHA256: "fcced433ae2ec216fc6d0b62a1d18fde4f1be6124100e674983f86e3286379fc",
			Publisher:    "最高人民法院", Issuer: "最高人民法院", DocumentType: "judicial_interpretation",
		},
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
