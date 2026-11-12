package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	clmcontract "contractaudit/internal/contract/model"
)

func TestPrepareReplaceDraftKeepsOriginalContractIDInDigestPayload(t *testing.T) {
	input := clmcontract.ReplaceContractDraftInput{
		ContractID: "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", ExpectedRowVersion: 7,
		Draft:          clmcontract.DraftContent{Contract: &clmcontract.ContractBasics{Name: "  合同  "}},
		IdempotencyKey: "replace-key-001",
	}
	prepared, err := PrepareReplaceDraft(input)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ContractID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("normalized path ID = %q", prepared.ContractID)
	}
	if prepared.Input.ContractID != input.ContractID {
		t.Fatalf("payload ContractID changed: %q", prepared.Input.ContractID)
	}
	if prepared.Input.Draft.Contract.Name != "合同" {
		t.Fatalf("draft was not normalized before digest: %q", prepared.Input.Draft.Contract.Name)
	}
	legacyPayload := struct {
		ContractID         string
		ExpectedRowVersion int64
		Draft              clmcontract.DraftContent
		IdempotencyKey     string
	}{input.ContractID, input.ExpectedRowVersion, prepared.Input.Draft, input.IdempotencyKey}
	want, err := clmcontract.RequestDigest(ReplaceDraftRoute, "PUT",
		"/api/contracts/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/draft", input.ExpectedRowVersion, legacyPayload)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.RequestDigest != want {
		t.Fatalf("digest = %q, want legacy payload digest %q", prepared.RequestDigest, want)
	}
}

func TestLoadDraftDetailPrefersFrozenRevisionAndRetainsWarningsAndReviewGate(t *testing.T) {
	frozen := clmcontract.DraftContent{Contract: &clmcontract.ContractBasics{Name: "冻结版本"}}
	working := clmcontract.DraftContent{Contract: &clmcontract.ContractBasics{Name: "工作草稿"}}
	frozenBytes, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	workingBytes, err := json.Marshal(working)
	if err != nil {
		t.Fatal(err)
	}
	revisionID := "rev-1"
	roundID := "round-1"
	importedAt := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	record := clmcontract.Aggregate{
		TenantID: "tenant", ContractID: "contract", AuthorityMode: "native",
		LifecycleState: "registered", ValidityState: "active", RowVersion: 3,
		CurrentRevisionID: &revisionID, SourceSystem: str("legacy"),
		ExternalContractID: str("source-1"), SourceVersion: str("v1"),
	}
	queries := &draftQueriesStub{facts: clmcontract.DraftDetailFacts{
		HasRevision: true, RevisionSnapshot: frozenBytes, HasDraft: true, DraftContent: workingBytes,
		LegacyImport: &clmcontract.DraftLegacyImport{ApprovalHistoryStatus: "unknown", ImportedAt: importedAt},
	}}
	workflow := workflowReaderStub{facts: ContractWorkflowFacts{LatestState: "in_review", LatestRoundID: &roundID, InReview: true}}
	detail, err := LoadDraftDetail(context.Background(), record, queries, workflow, identityCodec{}, draftAccessStub{canRead: true, canManage: true})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary.Name != "冻结版本" || detail.Draft == nil || detail.Draft.Contract.Name != "冻结版本" {
		t.Fatalf("detail did not use frozen revision: %#v", detail)
	}
	if detail.Summary.WorkflowState != "in_review" || detail.ActiveRoundID == nil || *detail.ActiveRoundID != roundID {
		t.Fatalf("workflow projection = %#v", detail)
	}
	if len(detail.AllowedCommands) != 0 {
		t.Fatalf("in-review contract exposes commands: %#v", detail.AllowedCommands)
	}
	if len(detail.Warnings) != 1 || detail.Warnings[0] != "approval_history_unknown" {
		t.Fatalf("warnings = %#v", detail.Warnings)
	}
	if detail.SourceObservedAt == nil || !detail.SourceObservedAt.Equal(importedAt) {
		t.Fatalf("legacy import time = %#v", detail.SourceObservedAt)
	}
}

func TestLoadDraftDetailKeepsMissingExternalObservationUnknown(t *testing.T) {
	record := clmcontract.Aggregate{TenantID: "tenant", ContractID: "external", AuthorityMode: "external", LifecycleState: "draft"}
	detail, err := LoadDraftDetail(context.Background(), record, &draftQueriesStub{}, workflowReaderStub{}, identityCodec{}, draftAccessStub{canRead: true})
	if err != nil {
		t.Fatal(err)
	}
	if detail.SourceObservedState != "unknown" || detail.Summary.WorkflowState != "external_observed" {
		t.Fatalf("external projection = %#v", detail)
	}
}

func TestReplaceDraftReplayRechecksCurrentVisibility(t *testing.T) {
	prepared, err := PrepareReplaceDraft(clmcontract.ReplaceContractDraftInput{
		ContractID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ExpectedRowVersion: 1,
		Draft:          clmcontract.DraftContent{Contract: &clmcontract.ContractBasics{Name: "合同"}},
		IdempotencyKey: "replace-key-002",
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := &replaceRepositoryStub{
		found:   true,
		receipt: clmcontract.WriteReceipt{ContractID: prepared.ContractID, RequestDigest: prepared.RequestDigest},
		record:  clmcontract.Aggregate{TenantID: "tenant", ContractID: prepared.ContractID},
	}
	_, err = ReplaceDraft(context.Background(), CreateDraftPrincipal{TenantID: "tenant", ActorID: "actor"}, prepared,
		repository, identityCodec{}, draftAccessStub{canRead: false}, workflowReaderStub{})
	if err == nil {
		t.Fatal("replayed receipt was returned after current visibility was revoked")
	}
	if repository.loadContractCalls != 1 {
		t.Fatalf("loaded contract %d times, want current visibility check", repository.loadContractCalls)
	}
}

type draftQueriesStub struct {
	facts   clmcontract.DraftDetailFacts
	rows    []clmcontract.DraftRecoveryRow
	loadErr error
}

func (r *draftQueriesStub) LoadDraftDetailFacts(context.Context, clmcontract.Aggregate) (clmcontract.DraftDetailFacts, error) {
	return r.facts, r.loadErr
}

func (r *draftQueriesStub) ListMyDraftRows(context.Context, string, string, int) ([]clmcontract.DraftRecoveryRow, error) {
	return r.rows, r.loadErr
}

type workflowReaderStub struct {
	facts ContractWorkflowFacts
	err   error
}

func (r workflowReaderStub) ReadContractWorkflow(context.Context, string, string) (ContractWorkflowFacts, error) {
	return r.facts, r.err
}

type draftAccessStub struct{ canRead, canManage bool }

func (a draftAccessStub) CanRead(context.Context, clmcontract.Aggregate) bool { return a.canRead }
func (a draftAccessStub) CanManage(clmcontract.Aggregate) bool                { return a.canManage }

type identityCodec struct{}

func (identityCodec) Seal(_ context.Context, _, _ string, draft clmcontract.DraftContent) ([]byte, error) {
	return json.Marshal(draft)
}
func (identityCodec) Open(_ context.Context, _, _ string, sealed []byte) (clmcontract.DraftContent, error) {
	var draft clmcontract.DraftContent
	if err := clmcontract.UnmarshalStoredDraft(sealed, &draft); err != nil {
		return clmcontract.DraftContent{}, err
	}
	return draft, nil
}
func (identityCodec) Project(draft clmcontract.DraftContent, _ bool) clmcontract.DraftContent {
	return draft
}

type replaceRepositoryStub struct {
	DraftCommandRepository
	found             bool
	receipt           clmcontract.WriteReceipt
	record            clmcontract.Aggregate
	loadContractCalls int
}

func (*replaceRepositoryStub) LockWriteReceipt(context.Context, string, string, string, string) error {
	return nil
}
func (r *replaceRepositoryStub) LoadWriteReceipt(context.Context, string, string, string, string) (clmcontract.WriteReceipt, bool, error) {
	return r.receipt, r.found, nil
}
func (r *replaceRepositoryStub) LoadContract(context.Context, string, string) (clmcontract.Aggregate, error) {
	r.loadContractCalls++
	return r.record, nil
}
func (*replaceRepositoryStub) LockContract(context.Context, string) error { return nil }
func (*replaceRepositoryStub) ReplaceDraft(context.Context, clmcontract.Aggregate, int64, []byte, string) (clmcontract.Aggregate, error) {
	return clmcontract.Aggregate{}, errors.New("unexpected replacement")
}

func str(value string) *string { return &value }
