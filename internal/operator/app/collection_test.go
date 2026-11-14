// File metadata (manifest mirror):
// Purpose: Pins legacy immutable collection replay before connector, writability and clock access.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"testing"
	"time"

	"contractaudit/internal/operator/model"
)

type legacyCollectionRepo struct {
	CollectionRepository
	snapshot model.SnapshotRecord
	body     []byte
}

func (r *legacyCollectionRepo) LockClaim(context.Context, model.CollectionIdentity) (model.CollectionClaim, bool, error) {
	return model.CollectionClaim{}, false, nil
}
func (r *legacyCollectionRepo) SnapshotByKey(context.Context, model.CollectionIdentity) (model.SnapshotRecord, bool, error) {
	return r.snapshot, true, nil
}
func (r *legacyCollectionRepo) Observations(context.Context, string, string) (ObservationCursor, error) {
	return emptyCollectionCursor{}, nil
}
func (r *legacyCollectionRepo) InsertLegacyClaim(_ context.Context, _ model.CollectionLeaseToken, _ string, body []byte) error {
	r.body = body
	return nil
}

type emptyCollectionCursor struct{}

func (emptyCollectionCursor) Next() bool               { return false }
func (emptyCollectionCursor) ScanRaw() ([]byte, error) { panic("no row") }
func (emptyCollectionCursor) Err() error               { return nil }
func (emptyCollectionCursor) Close()                   {}

type deniedCollectionWritable struct{}

func (deniedCollectionWritable) CheckWritable() error { panic("legacy replay must precede writable") }

type unusedCollectionClock struct{}

func (unusedCollectionClock) Now() time.Time { panic("legacy replay must not read the clock") }
func TestLegacyCollectionReplayBeforeWritableAndConnector(t *testing.T) {
	businessAt := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	binding := model.OperatorObjectBinding{Version: "v1", ExternalObject: model.OperatorExternalObject{ConnectorID: "c", ExternalObjectID: "e"}}
	digest, err := collectionHash(struct {
		CaseID, BindingVersion, ConnectorID, ExternalObjectID string
		BusinessAt                                            time.Time
	}{"case", "v1", "c", "e", businessAt})
	if err != nil {
		t.Fatal(err)
	}
	repo := &legacyCollectionRepo{snapshot: model.SnapshotRecord{Value: model.OperatorSnapshotSet{SnapshotSetID: "set"}, RequestSHA256: digest}}
	result, err := AcquireCollection(context.Background(), repo, deniedCollectionWritable{}, CollectionRequest{Identity: model.CollectionIdentity{CaseID: "case"}, Binding: binding, BusinessAt: businessAt, CollectionID: "new"}, unusedCollectionClock{})
	if err != nil || !result.Replay || result.Snapshot.CollectionID != "new" || len(repo.body) == 0 {
		t.Fatalf("legacy replay: %#v %v", result, err)
	}
}
