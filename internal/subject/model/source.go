package model

import (
	"errors"
	"sort"
	"strings"
	"time"
)

func ValidateSubjectSourceManifestMetadata(manifest SubjectSourceManifest) error {
	if manifest.SourceID != SyntheticSubjectSourceID || manifest.SourceVersion < 1 ||
		strings.TrimSpace(manifest.DisplayName) == "" || manifest.VerificationScope != "synthetic_controlled" ||
		len(manifest.Records) == 0 || len(manifest.Records) > 1000 {
		return errors.New("subject source manifest metadata is invalid")
	}
	return nil
}

func NormalizeSubjectSourceRecord(record SubjectSourceRecord, now time.Time, seenIDs map[string]bool) (SubjectSourceRecord, error) {
	record.RecordID = strings.TrimSpace(record.RecordID)
	record.DisplayName = strings.TrimSpace(record.DisplayName)
	record.USCC = strings.ToUpper(strings.TrimSpace(record.USCC))
	if record.RecordID == "" || record.DisplayName == "" || !ValidUSCCFormat(record.USCC) ||
		record.SourceAsOf.IsZero() || record.SourceAsOf.After(now) || record.ValidFrom.IsZero() ||
		(record.ValidUntil != nil && !record.ValidUntil.After(record.ValidFrom)) || seenIDs[record.RecordID] {
		return SubjectSourceRecord{}, errors.New("subject source record is invalid or duplicated")
	}
	record.SourceAsOf = record.SourceAsOf.UTC()
	record.ValidFrom = record.ValidFrom.UTC()
	if record.ValidUntil != nil {
		value := record.ValidUntil.UTC()
		record.ValidUntil = &value
	}
	return record, nil
}

func SortSubjectSourceRecords(manifest *SubjectSourceManifest) {
	sort.Slice(manifest.Records, func(i, j int) bool { return manifest.Records[i].RecordID < manifest.Records[j].RecordID })
}
