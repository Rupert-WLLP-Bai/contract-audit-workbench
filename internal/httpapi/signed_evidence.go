package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"contractaudit/internal/evidencebundle"
	"contractaudit/internal/evidencepdf"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

type evidenceHistory[T any] struct {
	Cutoff string `json:"cutoff"`
	Scope string `json:"scope"`
	Records []T `json:"records"`
}

type evidenceOriginalIndex struct {
	CaseID string `json:"case_id"`
	RunID string `json:"run_id"`
	AttemptID string `json:"attempt_id"`
	Entries []store.EvidenceOriginal `json:"entries"`
}

func (h *handler) downloadEvidenceBundle(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	caseID, reportID := r.PathValue("caseID"), r.PathValue("reportID")
	data, err := h.store.EvidenceBundleDataForReport(r.Context(), user, caseID, reportID)
	if err != nil {
		writeErr(w, err)
		return
	}
	key, err := evidencebundle.PrivateKeyFromFile(os.Getenv("EVIDENCE_SIGNING_KEY_FILE"))
	if err != nil {
		writeErr(w, fault.New(http.StatusServiceUnavailable, "evidence_signing_unavailable", "证据包签名密钥未配置或不可用"))
		return
	}
	pdf, err := evidencepdf.Render(r.Context(), os.Getenv("EVIDENCE_PDF_FONT_FILE"), evidencePDFSections(caseID, data))
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		writeErr(w, fault.New(http.StatusServiceUnavailable, "evidence_pdf_unavailable", "证据包 PDF 字体未配置、不可用或缺少所需文字"))
		return
	}
	cutoff := data.HistoryCutoff.Format(time.RFC3339Nano)
	const historyScope = "rows visible in one database statement with created_at at or before cutoff; later commits are excluded"
	decisionsJSON, err := json.Marshal(evidenceHistory[store.Decision]{Cutoff: cutoff, Scope: historyScope, Records: data.Decisions})
	if err != nil {
		writeErr(w, err)
		return
	}
	manualJSON, err := json.Marshal(evidenceHistory[store.EvidenceManualFinding]{Cutoff: cutoff, Scope: historyScope, Records: data.ManualFindings})
	if err != nil {
		writeErr(w, err)
		return
	}
	indexJSON, err := json.Marshal(evidenceOriginalIndex{CaseID: caseID, RunID: data.Record.RunID, AttemptID: data.Record.AttemptID, Entries: data.Originals})
	if err != nil {
		writeErr(w, err)
		return
	}
	identity, err := evidenceIdentity(caseID, data)
	if err != nil {
		writeErr(w, fault.Conflict("追加历史记录的时间无效"))
		return
	}
	sources := []evidencebundle.Source{
		{Path: evidencebundle.ReportPath, Data: data.ReportJSON},
		{Path: evidencebundle.PDFPath, Data: pdf},
		{Path: evidencebundle.DecisionsPath, Data: decisionsJSON},
		{Path: evidencebundle.ManualFindingsPath, Data: manualJSON},
		{Path: evidencebundle.OriginalsIndexPath, Data: indexJSON},
	}
	for _, entry := range data.Originals {
		original := entry
		sources = append(sources, evidencebundle.Source{Path: original.Path, Open: func() (io.ReadCloser, error) {
			if err := r.Context().Err(); err != nil {
				return nil, err
			}
			doc, body, err := h.store.OriginalContext(r.Context(), user, caseID, original.DocumentID)
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(body)
			if doc.ID != original.DocumentID || doc.CaseID != caseID || doc.Digest != original.SHA256 ||
				doc.Size != original.Size || int64(len(body)) != original.Size || hex.EncodeToString(sum[:]) != original.SHA256 {
				return nil, fault.Conflict("原件与冻结摘要或大小不一致")
			}
			if err := r.Context().Err(); err != nil {
				return nil, err
			}
			return io.NopCloser(bytes.NewReader(body)), nil
		}})
	}
	// Nothing is sent until every original, the PDF, and the manifest signature
	// are complete; a failed build never returns a partially signed ZIP.
	tmp, err := os.CreateTemp("", "contract-evidence-*.zip")
	if err != nil {
		writeErr(w, err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := evidencebundle.Write(r.Context(), tmp, identity, sources, key); err != nil {
		if r.Context().Err() != nil {
			return
		}
		writeErr(w, err)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		writeErr(w, err)
		return
	}
	info, err := tmp.Stat()
	if err != nil {
		writeErr(w, err)
		return
	}
	// Access may have changed while original bytes and the PDF were produced.
	latest, _, err := h.store.ReadReport(user, caseID, reportID, "json")
	if err != nil {
		writeErr(w, err)
		return
	}
	if latest != data.Record {
		writeErr(w, fault.Conflict("报告身份在导出期间发生变化"))
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "evidence-" + data.Record.ID + ".zip"}))
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, tmp)
}

func evidenceIdentity(caseID string, data store.EvidenceBundleData) (evidencebundle.Identity, error) {
	cutoff := data.HistoryCutoff.UTC().Format(time.RFC3339Nano)
	identity := evidencebundle.Identity{CaseID: caseID, ReportID: data.Record.ID, ReportVersion: data.Record.Version,
		RunID: data.Record.RunID, AttemptID: data.Record.AttemptID, InputSHA256: data.Record.InputHash,
		OutputSHA256: data.Record.OutputHash, DecisionSnapshotSHA256: data.Record.DecisionSnapshotHash,
		ExportedAt: time.Now().UTC().Format(time.RFC3339Nano), HistoryCutoff: cutoff, Decisions: make([]evidencebundle.HistoryRecord, 0, len(data.Decisions)),
		ManualFindings: make([]evidencebundle.HistoryRecord, 0, len(data.ManualFindings))}
	for _, decision := range data.Decisions {
		created, err := time.Parse(time.RFC3339Nano, decision.CreatedAt)
		if err != nil {
			return evidencebundle.Identity{}, err
		}
		identity.Decisions = append(identity.Decisions, evidencebundle.HistoryRecord{ID: decision.ID, CreatedAt: created.UTC().Format(time.RFC3339Nano)})
	}
	for _, finding := range data.ManualFindings {
		identity.ManualFindings = append(identity.ManualFindings, evidencebundle.HistoryRecord{ID: finding.ID, CreatedAt: finding.CreatedAt.UTC().Format(time.RFC3339Nano)})
	}
	return identity, nil
}

func evidencePDFSections(caseID string, data store.EvidenceBundleData) []evidencepdf.Section {
	cutoff := data.HistoryCutoff.UTC().Format(time.RFC3339Nano)
	identity := evidencepdf.Section{Title: "审查证据包", Lines: []string{
		"案件：" + data.CaseTitle + "；编号：" + caseID,
		fmt.Sprintf("报告 %s，版本 %d，创建于 %s", data.Record.ID, data.Record.Version, data.Record.CreatedAt),
		"审查：" + data.Record.RunID + "；执行：" + data.Record.AttemptID,
		"冻结输入 SHA-256：" + data.Record.InputHash,
		"冻结输出 SHA-256：" + data.Record.OutputHash,
		"报告决定快照 SHA-256：" + data.Record.DecisionSnapshotHash,
		fmt.Sprintf("冻结发现 %d 项，评估 %d 项，报告决定 %d 条，原件 %d 份。", len(data.Findings), len(data.Evaluations), len(data.FrozenDecisions), len(data.Originals)),
		"本文件作为证据包 PDF；使用信任的公钥或指纹验证 ZIP 清单后确认本 PDF 与原件的完整性。",
	}}
	documents := evidencepdf.Section{Title: "冻结原件", Lines: []string{}}
	for _, original := range data.Originals {
		documents.Lines = append(documents.Lines, fmt.Sprintf("%s；材料 %s；解析 %s；%d 字节；SHA-256 %s", original.Filename, original.DocumentID, original.ParsedDocumentID, original.Size, original.SHA256))
	}
	results := evidencepdf.Section{Title: "报告冻结的检查与引用", Lines: []string{}}
	for _, evaluation := range data.Evaluations {
		results.Lines = append(results.Lines, fmt.Sprintf("检查 %s；规则 %s；结果 %s；%s", evaluation.ID, evaluation.RuleID, evaluation.Relation, evaluation.Reason))
		for _, evidence := range evaluation.Evidence {
			ref := evidence.Ref
			results.Lines = append(results.Lines, fmt.Sprintf("引用 %s / %s [%d,%d)：%s", ref.DocumentID, ref.ParsedDocumentID, ref.Start, ref.End, ref.Quote))
		}
	}
	for _, finding := range data.Findings {
		results.Lines = append(results.Lines, fmt.Sprintf("发现 %s；结果 %s；%s", finding.ID, finding.Relation, finding.Claim))
		for _, anchor := range finding.Anchors {
			if anchor.Status == "located" {
				results.Lines = append(results.Lines, fmt.Sprintf("引用 %s / %s [%d,%d)：%s", anchor.DocumentID, anchor.ParsedDocumentID, anchor.Start, anchor.End, anchor.Quote))
			} else {
				results.Lines = append(results.Lines, "引用未定位："+anchor.Reason)
			}
		}
	}
	frozen := evidencepdf.Section{Title: "报告创建时冻结的人工决定", Lines: decisionPDFLines(data.FrozenDecisions)}
	appended := evidencepdf.Section{Title: "导出时可见的追加记录", Lines: []string{"数据库记录截止时刻：" + cutoff + "。本节保留该次数据库快照可见的记录；不包含之后提交的记录。"}}
	appended.Lines = append(appended.Lines, decisionPDFLines(data.Decisions)...)
	for _, finding := range data.ManualFindings {
		ref := finding.Evidence
		appended.Lines = append(appended.Lines, fmt.Sprintf("人工发现 %s；%s；%s；%s", finding.ID, finding.ActorName, finding.CreatedAt.UTC().Format(time.RFC3339Nano), finding.Claim),
			fmt.Sprintf("引用 %s / %s [%d,%d)：%s", ref.DocumentID, ref.ParsedDocumentID, ref.Start, ref.End, ref.Quote))
	}
	return []evidencepdf.Section{identity, documents, results, frozen, appended}
}

func decisionPDFLines(decisions []store.Decision) []string {
	if len(decisions) == 0 {
		return []string{"没有已记录的人工决定。"}
	}
	lines := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		lines = append(lines, strings.Join([]string{"决定 " + decision.ID, "发现 " + decision.FindingID,
			"处理人 " + decision.ActorName, "动作 " + decision.Action, "结论 " + decision.NewDisposition,
			"时间 " + decision.CreatedAt, "理由 " + decision.Reason}, "；"))
	}
	return lines
}
