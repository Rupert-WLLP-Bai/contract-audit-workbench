package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"contractaudit/internal/fault"
	"contractaudit/internal/subject/model"
)

func DecodeConfiguredSubjectSourceManifest(raw []byte) (model.SubjectSourceManifest, error) {
	if len(raw) == 0 || len(raw) > 4<<20 {
		return model.SubjectSourceManifest{}, errors.New("subject source manifest size is invalid")
	}
	var manifest model.SubjectSourceManifest
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return model.SubjectSourceManifest{}, fmt.Errorf("decode subject source manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); err == nil {
		return model.SubjectSourceManifest{}, errors.New("subject source manifest has trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return model.SubjectSourceManifest{}, fmt.Errorf("subject source manifest has invalid trailing data: %w", err)
	}
	if err := model.ValidateSubjectSourceManifestMetadata(manifest); err != nil {
		return model.SubjectSourceManifest{}, err
	}
	return manifest, nil
}

func SubjectSourceDirectory(manifest *model.SubjectSourceManifest) model.SubjectSourceDirectoryResult {
	items := make([]model.SubjectSourceDirectoryItem, 0, 1)
	if manifest != nil {
		items = append(items, model.SubjectSourceDirectoryItem{SourceID: manifest.SourceID, SourceVersion: manifest.SourceVersion,
			DisplayName: manifest.DisplayName, VerificationScope: manifest.VerificationScope})
	}
	return model.SubjectSourceDirectoryResult{Items: items}
}

func SourceSnapshotResult(item model.SubjectSourceSnapshot) (model.SubjectSourceSnapshotResult, error) {
	manifest, err := DecodeFrozenSource(item)
	if err != nil {
		return model.SubjectSourceSnapshotResult{}, err
	}
	return model.SubjectSourceSnapshotResult{SnapshotID: item.SnapshotID, SourceID: item.SourceCatalogID, SourceVersion: item.SourceVersion,
		SourceDigest: item.SourceDigest, SourceScope: item.SourceScope, CapturedAt: item.CapturedAt,
		RecordCount: len(manifest.Records), Label: manifest.DisplayName}, nil
}

func ListSourceSnapshots(snapshot Snapshot, actor Actor, caseID, cursor string, limit int) (model.SubjectSourceSnapshotListResult, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		return model.SubjectSourceSnapshotListResult{}, fault.Invalid("每页来源快照最多为100")
	}
	items := make([]model.SubjectSourceSnapshot, 0)
	for _, item := range snapshot.Sources {
		if item.TenantID == actor.TenantID && item.CaseID == caseID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SnapshotID < items[j].SnapshotID })
	start := 0
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return model.SubjectSourceSnapshotListResult{}, fault.Invalid("来源快照游标无效")
		}
		start = sort.Search(len(items), func(i int) bool { return items[i].SnapshotID > string(decoded) })
	}
	if start >= len(items) {
		return model.SubjectSourceSnapshotListResult{Items: []model.SubjectSourceSnapshotResult{}}, nil
	}
	end := min(start+limit, len(items))
	result := model.SubjectSourceSnapshotListResult{Items: make([]model.SubjectSourceSnapshotResult, 0, end-start)}
	for _, item := range items[start:end] {
		projected, err := SourceSnapshotResult(item)
		if err != nil {
			return model.SubjectSourceSnapshotListResult{}, err
		}
		result.Items = append(result.Items, projected)
	}
	if end < len(items) {
		value := base64.RawURLEncoding.EncodeToString([]byte(items[end-1].SnapshotID))
		result.NextCursor = &value
	}
	return result, nil
}

func GetSourceSnapshot(snapshot Snapshot, actor Actor, caseID, snapshotID string) (model.SubjectSourceSnapshotDetail, error) {
	for _, item := range snapshot.Sources {
		if item.TenantID == actor.TenantID && item.CaseID == caseID && item.SnapshotID == snapshotID {
			projected, err := SourceSnapshotResult(item)
			if err != nil {
				return model.SubjectSourceSnapshotDetail{}, err
			}
			return model.SubjectSourceSnapshotDetail{SubjectSourceSnapshotResult: projected, CaseID: caseID}, nil
		}
	}
	return model.SubjectSourceSnapshotDetail{}, fault.NotFound("没有找到对应来源快照")
}
