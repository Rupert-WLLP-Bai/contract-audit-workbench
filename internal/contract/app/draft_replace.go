package app

import (
	"context"
	"encoding/json"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"
)

const ReplaceDraftRoute = "clm.replace_draft"

type PreparedReplaceDraft struct {
	ContractID    string
	Input         clmcontract.ReplaceContractDraftInput
	RequestDigest string
}

// PrepareReplaceDraft normalizes the resource path and validates the complete
// replacement before a caller opens its authorized transaction. The payload
// itself deliberately retains its original ContractID for digest compatibility.
func PrepareReplaceDraft(input clmcontract.ReplaceContractDraftInput) (PreparedReplaceDraft, error) {
	contractID, err := clmcontract.NormalizeContractUUID(input.ContractID)
	if err != nil {
		return PreparedReplaceDraft{}, err
	}
	if input.ExpectedRowVersion < 1 {
		return PreparedReplaceDraft{}, fault.Invalid("替换草稿必须携带当前合同 row_version")
	}
	if err := clmcontract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return PreparedReplaceDraft{}, err
	}
	clmcontract.SanitizeDraftReferences(&input.Draft)
	if err := clmcontract.ValidateDraftContent(&input.Draft); err != nil {
		return PreparedReplaceDraft{}, err
	}
	if err := clmcontract.ValidateDraftAmounts(&input.Draft, contractmoney.StageDraft); err != nil {
		return PreparedReplaceDraft{}, err
	}
	digest, err := clmcontract.RequestDigest(ReplaceDraftRoute, "PUT",
		"/api/contracts/"+contractID+"/draft", input.ExpectedRowVersion, input)
	if err != nil {
		return PreparedReplaceDraft{}, err
	}
	return PreparedReplaceDraft{ContractID: contractID, Input: input, RequestDigest: digest}, nil
}

// DraftReplacementRepository adds the Contract-owned CAS write and lock to
// the existing #287 receipt/aggregate command port.
type DraftReplacementRepository interface {
	DraftCommandRepository
	LockContract(context.Context, string) error
	ReplaceDraft(context.Context, clmcontract.Aggregate, int64, []byte, string) (clmcontract.Aggregate, error)
}

// ReplaceDraft applies one complete draft replacement inside the caller's
// already authorized transaction. It owns ordering and projection but never
// starts, commits, or rolls back that transaction.
func ReplaceDraft(ctx context.Context, principal CreateDraftPrincipal, prepared PreparedReplaceDraft,
	repository DraftReplacementRepository, codec DraftContentCodec, access DraftAccess,
	workflow ContractWorkflowReader) (clmcontract.DraftDetail, error) {
	input := prepared.Input
	if err := repository.LockWriteReceipt(ctx, principal.TenantID, principal.ActorID, ReplaceDraftRoute, input.IdempotencyKey); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	receipt, found, err := repository.LoadWriteReceipt(ctx, principal.TenantID, principal.ActorID, ReplaceDraftRoute, input.IdempotencyKey)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if found {
		return replayReplacedDraft(ctx, principal, prepared, receipt, repository, codec, access)
	}

	if err := repository.LockContract(ctx, prepared.ContractID); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	record, err := repository.LoadContract(ctx, principal.TenantID, prepared.ContractID)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if !access.CanRead(ctx, record) {
		return clmcontract.DraftDetail{}, fault.NotFound("没有找到对应记录")
	}
	if !access.CanManage(record) {
		return clmcontract.DraftDetail{}, fault.Forbidden("当前授权不能修改此合同草稿")
	}
	if record.AuthorityMode != "native" {
		return clmcontract.DraftDetail{}, fault.Forbidden("外部权威合同不允许本地修改草稿")
	}
	if record.LifecycleState != "draft" || record.ValidityState != "active" {
		return clmcontract.DraftDetail{}, fault.Conflict("合同当前状态不允许修改草稿")
	}
	if record.RowVersion != input.ExpectedRowVersion {
		return clmcontract.DraftDetail{}, fault.Conflict("合同内容已被他人修改，请刷新后确认")
	}
	workflowFacts, err := workflow.ReadContractWorkflow(ctx, principal.TenantID, prepared.ContractID)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if workflowFacts.InReview {
		return clmcontract.DraftDetail{}, fault.Conflict("合同已有进行中的会签轮次，不能修改草稿")
	}

	sealedBytes, err := codec.Seal(ctx, record.TenantID, record.ContractID, input.Draft)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	updated, err := repository.ReplaceDraft(ctx, record, input.ExpectedRowVersion, sealedBytes, principal.ActorID)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	name, amount, currency, err := clmcontract.DraftSummaryFacts(sealedBytes)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	var sealedDraft clmcontract.DraftContent
	if err := clmcontract.UnmarshalStoredDraft(sealedBytes, &sealedDraft); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	stored := clmcontract.DraftDetail{
		Summary:         clmcontract.SummaryFromDraft(updated, name, amount, currency),
		AllowedCommands: clmcontract.DraftCommands(updated, true),
		Draft:           &sealedDraft,
		Warnings:        []string{},
	}
	response, err := json.Marshal(stored)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if err := repository.InsertWriteReceipt(ctx, principal.TenantID, principal.ActorID, ReplaceDraftRoute,
		input.IdempotencyKey, prepared.RequestDigest, prepared.ContractID, 200, response); err != nil {
		return clmcontract.DraftDetail{}, err
	}

	plaintext, err := codec.Open(ctx, updated.TenantID, updated.ContractID, sealedBytes)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	projected := codec.Project(plaintext, access.CanManage(updated))
	stored.Draft = &projected
	return stored, nil
}

func replayReplacedDraft(ctx context.Context, principal CreateDraftPrincipal, prepared PreparedReplaceDraft,
	receipt clmcontract.WriteReceipt, repository DraftCommandRepository, codec DraftContentCodec,
	access DraftAccess) (clmcontract.DraftDetail, error) {
	if receipt.RequestDigest != prepared.RequestDigest {
		return clmcontract.DraftDetail{}, fault.Conflict("Idempotency-Key已用于不同请求载荷")
	}
	if receipt.ContractID != prepared.ContractID {
		return clmcontract.DraftDetail{}, fault.Conflict("Idempotency-Key已用于其他合同")
	}
	record, err := repository.LoadContract(ctx, principal.TenantID, receipt.ContractID)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if !access.CanRead(ctx, record) {
		return clmcontract.DraftDetail{}, fault.NotFound("没有找到对应记录")
	}
	var result clmcontract.DraftDetail
	if err := clmcontract.UnmarshalStoredDraft(receipt.Response, &result); err != nil {
		return clmcontract.DraftDetail{}, err
	}
	if result.Draft == nil {
		return result, nil
	}
	sealedBytes, err := json.Marshal(result.Draft)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	plaintext, err := codec.Open(ctx, record.TenantID, record.ContractID, sealedBytes)
	if err != nil {
		return clmcontract.DraftDetail{}, err
	}
	projected := codec.Project(plaintext, access.CanManage(record))
	result.Draft = &projected
	return result, nil
}
