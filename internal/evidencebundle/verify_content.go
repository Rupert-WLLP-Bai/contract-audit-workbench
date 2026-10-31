package evidencebundle

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type frozenOriginal struct {
	Document struct {
		ID string `json:"id"`
		CaseID string `json:"case_id"`
		Filename string `json:"filename"`
		MediaType string `json:"media_type"`
		Digest string `json:"digest"`
		Size int64 `json:"size"`
	} `json:"document"`
	OriginalHash string `json:"original_hash"`
	ParsedDocumentID string `json:"parsed_document_id"`
}

func verifyContents(manifest Manifest, files map[string]*zip.File) error {
	id := manifest.Identity
	var report struct {
		Schema string `json:"schema_version"`
		ReportID string `json:"report_id"`
		ReportVersion int `json:"report_version"`
		Case struct { ID string `json:"id"` } `json:"case"`
		Run struct { ID string `json:"id"`; CaseID string `json:"case_id"` } `json:"run"`
		Attempt struct { ID string `json:"id"`; RunID string `json:"run_id"` } `json:"attempt"`
		Input struct { AttemptID string `json:"attempt_id"`; RunID string `json:"run_id"`; InputHash string `json:"input_hash"`; Documents []frozenOriginal `json:"documents"` } `json:"input"`
		InputHash string `json:"input_hash"`
		OutputHash string `json:"output_hash"`
		DecisionHash string `json:"decision_snapshot_hash"`
		Decisions json.RawMessage `json:"decisions"`
		RulePack struct {
			Availability string `json:"availability"`
			SnapshotDigest string `json:"snapshot_sha256"`
			Snapshot json.RawMessage `json:"snapshot"`
		} `json:"rulepack"`
	}
	if err := decodeJSONEntry(files[ReportPath], &report); err != nil || report.Schema != "case-report/v1" ||
		report.ReportID != id.ReportID || report.ReportVersion != id.ReportVersion || report.Case.ID != id.CaseID ||
		report.Run.ID != id.RunID || report.Run.CaseID != id.CaseID || report.Attempt.ID != id.AttemptID ||
		report.Attempt.RunID != id.RunID || report.Input.AttemptID != id.AttemptID || report.Input.RunID != id.RunID ||
		report.Input.InputHash != id.InputSHA256 || report.InputHash != id.InputSHA256 || report.OutputHash != id.OutputSHA256 ||
		report.DecisionHash != id.DecisionSnapshotSHA256 {
		return errors.New("signed report identity does not match the manifest")
	}
	if digest(report.Decisions) != id.DecisionSnapshotSHA256 {
		return errors.New("frozen report decision snapshot hash mismatch")
	}
	if report.RulePack.Availability == "frozen" {
		if !validHash(report.RulePack.SnapshotDigest) || digest(report.RulePack.Snapshot) != report.RulePack.SnapshotDigest {
			return errors.New("frozen rule snapshot hash mismatch")
		}
	} else if report.RulePack.Availability != "unavailable" || string(report.RulePack.Snapshot) != "null" || report.RulePack.SnapshotDigest != "" {
		return errors.New("invalid unavailable rule snapshot")
	}
	if err := verifyHistory(files[DecisionsPath], id.HistoryCutoff, id.Decisions); err != nil {
		return fmt.Errorf("decision history: %w", err)
	}
	if err := verifyHistory(files[ManualFindingsPath], id.HistoryCutoff, id.ManualFindings); err != nil {
		return fmt.Errorf("manual finding history: %w", err)
	}
	if err := verifyOriginals(manifest, files[OriginalsIndexPath], report.Input.Documents); err != nil {
		return err
	}
	reader, err := files[PDFPath].Open()
	if err != nil {
		return err
	}
	var header [5]byte
	_, err = io.ReadFull(reader, header[:])
	closeErr := reader.Close()
	if err != nil || closeErr != nil || string(header[:]) != "%PDF-" {
		return errors.New("signed PDF entry lacks a PDF header")
	}
	return nil
}

func verifyHistory(file *zip.File, cutoff string, selected []HistoryRecord) error {
	var history struct {
		Cutoff string `json:"cutoff"`
		Scope string `json:"scope"`
		Records []struct {
			ID string `json:"id"`
			CreatedAt string `json:"created_at"`
		} `json:"records"`
	}
	if err := decodeJSONEntry(file, &history); err != nil || history.Cutoff != cutoff || history.Scope == "" || len(history.Records) != len(selected) {
		return errors.New("appendix does not match its signed cutoff and record list")
	}
	for index, record := range history.Records {
		actual, actualErr := time.Parse(time.RFC3339Nano, record.CreatedAt)
		expected, expectedErr := time.Parse(time.RFC3339Nano, selected[index].CreatedAt)
		if actualErr != nil || expectedErr != nil || record.ID != selected[index].ID || !actual.Equal(expected) {
			return errors.New("appendix record identity does not match the signed manifest")
		}
	}
	return nil
}

func verifyOriginals(manifest Manifest, file *zip.File, frozen []frozenOriginal) error {
	var index struct {
		CaseID string `json:"case_id"`
		RunID string `json:"run_id"`
		AttemptID string `json:"attempt_id"`
		Entries []struct {
			Path string `json:"path"`
			DocumentID string `json:"document_id"`
			ParsedDocumentID string `json:"parsed_document_id"`
			Filename string `json:"filename"`
			MediaType string `json:"media_type"`
			SHA256 string `json:"sha256"`
			Size int64 `json:"size"`
		} `json:"entries"`
	}
	if err := decodeJSONEntry(file, &index); err != nil || index.CaseID != manifest.Identity.CaseID ||
		index.RunID != manifest.Identity.RunID || index.AttemptID != manifest.Identity.AttemptID {
		return errors.New("original inventory identity does not match the manifest")
	}
	entries := make(map[string]Entry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		if strings.HasPrefix(entry.Path, "originals/") && entry.Path != OriginalsIndexPath {
			entries[entry.Path] = entry
		}
	}
	if index.Entries == nil || len(index.Entries) != len(entries) || len(index.Entries) != len(frozen) {
		return errors.New("original inventory does not list every original")
	}
	for i, original := range index.Entries {
		entry, ok := entries[original.Path]
		doc := frozen[i]
		if !ok || original.Path != fmt.Sprintf("originals/%06d.bin", i+1) || original.DocumentID == "" ||
			entry.Size != original.Size || entry.SHA256 != original.SHA256 || original.DocumentID != doc.Document.ID ||
			doc.Document.CaseID != manifest.Identity.CaseID || original.ParsedDocumentID != doc.ParsedDocumentID ||
			original.Size != doc.Document.Size || original.SHA256 != doc.Document.Digest || doc.OriginalHash != doc.Document.Digest ||
			original.Filename != doc.Document.Filename || original.MediaType != doc.Document.MediaType {
			return errors.New("original inventory size or SHA-256 does not match the manifest")
		}
	}
	return nil
}

func decodeJSONEntry(file *zip.File, target any) error {
	if file == nil {
		return errors.New("JSON entry is missing")
	}
	r, err := file.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("JSON entry contains trailing data")
	}
	return nil
}
