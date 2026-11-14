// File metadata (manifest mirror):
// Purpose: Loads server-owned Subject source configuration and preserves Case authorization, configuration locking and transaction boundaries around Subject source use cases.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-14
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	subjectApp "contractaudit/internal/subject/app"
	subjectModel "contractaudit/internal/subject/model"
)

// ConfigureSubjectSourceManifest installs an explicit, server-owned, versioned
// controlled dataset. The caller must load this from deployment configuration;
// a missing configuration intentionally leaves the directory empty.
func (s *Store) ConfigureSubjectSourceManifest(raw []byte) error {
	var manifest SubjectSourceManifest
	decoded, err := subjectApp.DecodeConfiguredSubjectSourceManifest(raw)
	if err != nil {
		return err
	}
	manifest = decoded
	seenIDs := map[string]bool{}
	for i := range manifest.Records {
		record, err := subjectModel.NormalizeSubjectSourceRecord(manifest.Records[i], time.Now().UTC(), seenIDs)
		if err != nil {
			return err
		}
		manifest.Records[i] = record
		seenIDs[record.RecordID] = true
	}
	subjectModel.SortSubjectSourceRecords(&manifest)
	s.subjectSourcesMu.Lock()
	if previous := s.subjectSources; previous != nil {
		if manifest.SourceVersion < previous.SourceVersion {
			s.subjectSourcesMu.Unlock()
			return errors.New("subject source manifest version cannot move backwards")
		}
		if manifest.SourceVersion == previous.SourceVersion {
			oldPayload, _ := json.Marshal(previous)
			newPayload, _ := json.Marshal(manifest)
			if string(oldPayload) != string(newPayload) {
				s.subjectSourcesMu.Unlock()
				return errors.New("subject source manifest content changed without a version increment")
			}
		}
	}
	s.subjectSources = &manifest
	s.subjectSourcesMu.Unlock()
	return nil
}

func configureSubjectSourceFromEnvironment(s *Store) error {
	path := strings.TrimSpace(os.Getenv("CAW_SUBJECT_SOURCE_MANIFEST"))
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read configured subject source manifest: %w", err)
	}
	if err := s.ConfigureSubjectSourceManifest(raw); err != nil {
		return fmt.Errorf("configure subject source manifest: %w", err)
	}
	return nil
}

func (s *Store) configuredSubjectSource() *SubjectSourceManifest {
	s.subjectSourcesMu.RLock()
	defer s.subjectSourcesMu.RUnlock()
	return subjectModel.CloneSubjectSourceManifest(s.subjectSources)
}

func (s *Store) ListConfiguredSubjectSources(ctx context.Context, user accounts.User, caseID string) (SubjectSourceDirectoryResult, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectSourceDirectoryResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectSourceDirectoryResult{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectSourceDirectoryResult{}, err
	}
	caseItem, err := s.visibleCase(&st, current, strings.TrimSpace(caseID))
	if err != nil {
		return SubjectSourceDirectoryResult{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Read); err != nil {
		return SubjectSourceDirectoryResult{}, err
	}
	return subjectApp.SubjectSourceDirectory(s.configuredSubjectSource()), nil
}

func (s *Store) CreateSubjectSourceSnapshot(ctx context.Context, user accounts.User, caseID string, input SubjectSourceSnapshotCreateInput, key string) (SubjectSourceSnapshotResult, bool, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectSourceSnapshotResult{}, false, err
	}
	if s.ReadOnly() {
		return SubjectSourceSnapshotResult{}, false, fault.New(503, "read_only", "只读模式下不能创建来源快照")
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectSourceSnapshotResult{}, false, err
	}
	caseID = strings.TrimSpace(caseID)
	input.SourceID = strings.TrimSpace(input.SourceID)
	if err := validateIdempotencyKey(key); err != nil {
		return SubjectSourceSnapshotResult{}, false, err
	}
	if err := subjectApp.ValidateSourceSnapshotInput(input); err != nil {
		return SubjectSourceSnapshotResult{}, false, err
	}
	requestHash, err := subjectApp.RequestHash(input)
	if err != nil {
		return SubjectSourceSnapshotResult{}, false, err
	}
	var result SubjectSourceSnapshotResult
	replayed := false
	err = s.updateCaseAsContext(ctx, current, caseID, func(st *state) error {
		caseItem, err := s.writableCase(st, current, caseID, access.Manage)
		if err != nil {
			return err
		}
		snapshot, actor := subjectCommandSnapshot(st), subjectCommandActor(current)
		result, replayed, err = subjectApp.ReplaySourceSnapshot(snapshot, actor, caseID, key, requestHash)
		if err != nil || replayed {
			return err
		}
		live := s.configuredSubjectSource()
		delta, created, err := subjectApp.CreateSourceSnapshot(snapshot, actor, subjectApp.CaseIdentity{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID}, input, live, key, requestHash, subjectCommandClock{})
		result = created
		if err != nil {
			return err
		}
		applySubjectCommandDelta(st, delta)
		return nil
	})
	return result, replayed, err
}

func decodeFrozenSubjectSourceSnapshot(item SubjectSourceSnapshot) (SubjectSourceManifest, error) {
	return subjectApp.DecodeFrozenSource(item)
}

func subjectSourceSnapshotResult(item SubjectSourceSnapshot) (SubjectSourceSnapshotResult, error) {
	return subjectApp.SourceSnapshotResult(item)
}

func (s *Store) ListSubjectSourceSnapshots(ctx context.Context, user accounts.User, caseID, cursor string, limit int) (SubjectSourceSnapshotListResult, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectSourceSnapshotListResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectSourceSnapshotListResult{}, err
	}
	caseID = strings.TrimSpace(caseID)
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectSourceSnapshotListResult{}, err
	}
	caseItem, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return SubjectSourceSnapshotListResult{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Read); err != nil {
		return SubjectSourceSnapshotListResult{}, err
	}
	return subjectApp.ListSourceSnapshots(subjectCommandSnapshot(&st), subjectCommandActor(current), caseID, cursor, limit)
}

func (s *Store) GetSubjectSourceSnapshot(ctx context.Context, user accounts.User, caseID, snapshotID string) (SubjectSourceSnapshotDetail, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectSourceSnapshotDetail{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectSourceSnapshotDetail{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectSourceSnapshotDetail{}, err
	}
	caseItem, err := s.visibleCase(&st, current, strings.TrimSpace(caseID))
	if err != nil {
		return SubjectSourceSnapshotDetail{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Read); err != nil {
		return SubjectSourceSnapshotDetail{}, err
	}
	return subjectApp.GetSourceSnapshot(subjectCommandSnapshot(&st), subjectCommandActor(current), caseItem.ID, strings.TrimSpace(snapshotID))
}

func (s *Store) VerifySubjectFromControlledSource(ctx context.Context, user accounts.User, caseID, snapshotID string, input SubjectSourceVerifyInput, key string) (SubjectSourceVerifyResult, bool, error) {
	ctx = commandContext(ctx, user)
	if err := ctx.Err(); err != nil {
		return SubjectSourceVerifyResult{}, false, err
	}
	if s.ReadOnly() {
		return SubjectSourceVerifyResult{}, false, fault.New(503, "read_only", "只读模式下不能核验主体来源")
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectSourceVerifyResult{}, false, err
	}
	caseID, snapshotID, input.SubjectID = strings.TrimSpace(caseID), strings.TrimSpace(snapshotID), strings.TrimSpace(input.SubjectID)
	if input.SubjectID == "" || input.ExpectedSubjectVersion < 1 {
		return SubjectSourceVerifyResult{}, false, fault.Invalid("主体及预期版本必填")
	}
	if err := validateIdempotencyKey(key); err != nil {
		return SubjectSourceVerifyResult{}, false, err
	}
	targetID := caseID + "/" + snapshotID + "/" + input.SubjectID
	requestHash, err := subjectApp.RequestHash(input)
	if err != nil {
		return SubjectSourceVerifyResult{}, false, err
	}
	var result SubjectSourceVerifyResult
	replayed := false
	err = s.updateCaseAsContext(ctx, current, caseID, func(st *state) error {
		caseItem, err := s.writableCase(st, current, caseID, access.Manage)
		if err != nil {
			return err
		}
		snapshot, actor := subjectCommandSnapshot(st), subjectCommandActor(current)
		result, replayed, err = subjectApp.ReplaySourceVerification(snapshot, actor, targetID, key, requestHash)
		if err != nil || replayed {
			return err
		}
		caseIdentity := subjectApp.CaseIdentity{TenantID: caseItem.TenantID, OrgID: caseItem.OwnerOrgID, CaseID: caseID}
		prepared, err := subjectApp.PrepareSourceVerification(snapshot, actor, caseIdentity, snapshotID)
		if err != nil {
			return err
		}
		readable := subjectReadableBindingCases(st, current)
		delta, verified, err := subjectApp.VerifySource(snapshot, actor, caseIdentity, prepared, input, readable, key, requestHash, subjectCommandClock{})
		result = verified
		if err != nil {
			return err
		}
		applySubjectCommandDelta(st, delta)
		return nil
	})
	return result, replayed, err
}
