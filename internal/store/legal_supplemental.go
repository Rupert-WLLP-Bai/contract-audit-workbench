package store

import "fmt"

var historicalContractNotes = map[int]string{
	39:  "格式条款提供方的提示与说明义务，应结合相关法律事实发生时间判断。",
	40:  "格式条款效力的旧法规定不得直接作为现行法规则。",
	41:  "格式条款解释仅作旧法版本比较。",
	107: "旧违约责任规定须结合违约事实时间及过渡规则判断。",
	114: "旧违约金规定不得直接替代民法典第585条及现行司法解释。单源正文核对与跨来源核对分开：与审计署刊载文本严格逐字核对为FAIL，在Unicode码点零基偏移101处本来源为ASCII分号U+003B，审计署来源为全角分号U+FF1B；未改写正文。审计署快照：https://www.audit.gov.cn/n7/n34/n58/c109664/content.html，原始响应SHA-256：33807920b25020a2bcc37e58977e71bb368cf277b4e66f80be9b33ff81373d99。",
}

var timeEffectNotes = map[int]string{
	1:  "区分民法典施行后、施行前及跨期持续的法律事实。",
	2:  "施行前事实原则适用当时法律及司法解释，并核实本条例外条件。",
	3:  "当时法无规定时，核实民法典适用条件及不得明显减损权益等限制。",
	4:  "当时法有原则规定、民法典有具体规定时，核实旧法适用及裁判说理条件。",
	9:  "核实施行前合同、格式条款提示说明义务及效力认定事实。",
	20: "核实施行前成立、跨期履行的合同以及争议履行阶段。",
}

var contractGeneralNotes = map[int]string{
	1:  "解释合同条款须结合通常文义、合同性质目的、习惯、诚信及缔约履行证据。",
	4:  "核实招标、中标通知及书面合同订立事实。",
	9:  "核实是否预拟、未经协商及格式条款的反证。",
	10: "核实提示说明措施、条款内容及举证责任；不能仅凭勾选或弹窗推定。",
	60: "核实履行利益损失、合理成本及可得利益证据。",
	64: "核实当事人是否以反诉或抗辩请求调整违约金。",
	65: "核实约定违约金、损失及合同履行、过错等调整因素。",
}

// These are research cues from the archived supplemental article manifest,
// not adjudicated conditions and not substitutes for RulePack fact gates.
// Keeping them in the governed summary makes them part of the immutable
// content hash without introducing an unreviewed machine-applicability model.
func supplementalLegalSummary(sourceID, versionID string, articleNumber int, label string) string {
	var edition string
	var notes map[int]string
	switch {
	case sourceID == "CN-CONTRACT-LAW-HISTORY" && versionID == "1999-03-15-original":
		edition = "1999年合同法已于2021-01-01废止；仅供历史法律事实参考，须结合民法典第1260条、时间效力规定及个案事实判断。"
		notes = historicalContractNotes
	case sourceID == "CN-SPC-TIME-EFFECT" && versionID == "2020-12-29-fashi-2020-15":
		edition = "法释〔2020〕15号；调整民法典施行前后法律事实及跨期合同的时间效力，适用须核实事实与例外。"
		notes = timeEffectNotes
	case sourceID == "CN-SPC-CONTRACT-GENERAL-INTERPRETATION" && versionID == "2023-12-04-fashi-2023-13":
		edition = "法释〔2023〕13号；司法解释的适用须结合合同、诉讼及证据事实，不能自动推定。"
		notes = contractGeneralNotes
	default:
		return label
	}
	note, ok := notes[articleNumber]
	if !ok {
		return fmt.Sprintf("%s %s 逐条适用条件尚未整理；专业法律审核NOT_RUN，reviewer=null。", label, edition)
	}
	return fmt.Sprintf("%s %s 适用提示：%s 专业法律审核NOT_RUN，reviewer=null；个案适用未确定。", label, edition, note)
}
