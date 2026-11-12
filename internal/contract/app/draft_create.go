package app

import (
	"context"
	"encoding/json"
	"time"

	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/contractmoney"
	"contractaudit/internal/fault"
)

const CreateDraftRoute = "clm.create_draft"

// CreateDraftPrincipal is derived from a trusted, server-resolved identity by
// Store after its authorization gate and tenant scope have been installed.
type CreateDraftPrincipal struct {
	TenantID string
	ActorID  string
	OrgID    string
}

type PreparedCreateDraft struct {
	Input         clmcontract.CreateContractDraftInput
	RequestDigest string
}

// PrepareCreateDraft owns the pure command rules before Store opens its
// authorized transaction.
func PrepareCreateDraft(input clmcontract.CreateContractDraftInput) (PreparedCreateDraft, error) {
	if input.ExpectedRowVersion != 0 {
		return PreparedCreateDraft{}, fault.Invalid("创建合同草稿必须携带 expected_row_version=0")
	}
	if input.AuthorityMode != "native" {
		return PreparedCreateDraft{}, fault.Invalid("本版只能创建 native 权威模式的合同草稿")
	}
	if err := clmcontract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return PreparedCreateDraft{}, err
	}
	clmcontract.SanitizeDraftReferences(&input.Draft)
	if err := clmcontract.ValidateDraftContent(&input.Draft); err != nil {
		return PreparedCreateDraft{}, err
	}
	if err := clmcontract.ValidateDraftAmounts(&input.Draft, contractmoney.StageDraft); err != nil {
		return PreparedCreateDraft{}, err
	}
	digest, err := clmcontract.RequestDigest(CreateDraftRoute, "POST", "/api/contracts", input.ExpectedRowVersion, input)
	if err != nil {
		return PreparedCreateDraft{}, err
	}
	return PreparedCreateDraft{Input: input, RequestDigest: digest}, nil
}

type DraftCommandRepository interface {
	LockWriteReceipt(context.Context, string, string, string, string) error
	LoadWriteReceipt(context.Context, string, string, string, string) (clmcontract.WriteReceipt, bool, error)
	LoadContract(context.Context, string, string) (clmcontract.Aggregate, error)
	CreateDraftAggregate(context.Context, clmcontract.Aggregate, []byte, string) (time.Time, error)
	InsertWriteReceipt(context.Context, string, string, string, string, string, string, int, []byte) error
}

type DraftContentCodec interface {
	Seal(context.Context, string, string, clmcontract.DraftContent) ([]byte, error)
	Open(context.Context, string, string, []byte) (clmcontract.DraftContent, error)
	Project(clmcontract.DraftContent, bool) clmcontract.DraftContent
}

type DraftAccess interface {
	CanRead(context.Context, clmcontract.Aggregate) bool
	CanManage(clmcontract.Aggregate) bool
}

// CreateDraft executes the application command using caller-owned, already
// authorized transaction ports. It never opens, commits, or rolls back a DB
// transaction.
func CreateDraft(ctx context.Context, principal CreateDraftPrincipal, input PreparedCreateDraft,
	repository DraftCommandRepository, codec DraftContentCodec, access DraftAccess) (clmcontract.CreateDraftResult, error) {
	if err := repository.LockWriteReceipt(ctx, principal.TenantID, principal.ActorID, CreateDraftRoute, input.Input.IdempotencyKey); err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	receipt, found, err := repository.LoadWriteReceipt(ctx, principal.TenantID, principal.ActorID, CreateDraftRoute, input.Input.IdempotencyKey)
	if err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	if found {
		return replayCreatedDraft(ctx, principal, receipt, input.RequestDigest, repository, codec, access)
	}

	contractID, err := clmcontract.NewContractUUID()
	if err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	sealedBytes, err := codec.Seal(ctx, principal.TenantID, contractID, input.Input.Draft)
	if err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	var contractNumber *string
	if input.Input.Draft.Contract.Number != nil {
		contractNumber = clmcontract.OptionalText(*input.Input.Draft.Contract.Number)
	}
	record := clmcontract.Aggregate{
		TenantID: principal.TenantID, ContractID: contractID, OwnerOrgID: principal.OrgID,
		AuthorityMode: "native", ContractNumber: contractNumber, LifecycleState: "draft",
		ValidityState: "active", RowVersion: 1, CreatedBy: principal.ActorID,
	}
	record.CreatedAt, err = repository.CreateDraftAggregate(ctx, record, sealedBytes, principal.ActorID)
	if err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	record.UpdatedAt = record.CreatedAt
	name, amount, currency, err := clmcontract.DraftSummaryFacts(sealedBytes)
	if err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	sealedDraft := new(clmcontract.DraftContent)
	if err := clmcontract.UnmarshalStoredDraft(sealedBytes, sealedDraft); err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	result := clmcontract.CreateDraftResult{
		Summary:         clmcontract.SummaryFromDraft(record, name, amount, currency),
		AllowedCommands: clmcontract.DraftCommands(record, true),
		Draft:           sealedDraft,
		Warnings:        []string{},
	}
	response, err := json.Marshal(result)
	if err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	if err := repository.InsertWriteReceipt(ctx, principal.TenantID, principal.ActorID, CreateDraftRoute,
		input.Input.IdempotencyKey, input.RequestDigest, contractID, 201, response); err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	plaintext, err := codec.Open(ctx, principal.TenantID, contractID, sealedBytes)
	if err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	projected := codec.Project(plaintext, access.CanManage(record))
	result.Draft = &projected
	return result, nil
}

func replayCreatedDraft(ctx context.Context, principal CreateDraftPrincipal, receipt clmcontract.WriteReceipt, digest string,
	repository DraftCommandRepository, codec DraftContentCodec, access DraftAccess) (clmcontract.CreateDraftResult, error) {
	if receipt.RequestDigest != digest {
		return clmcontract.CreateDraftResult{}, fault.Conflict("Idempotency-Key已用于不同请求载荷")
	}
	record, err := repository.LoadContract(ctx, principal.TenantID, receipt.ContractID)
	if err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	if !access.CanRead(ctx, record) {
		return clmcontract.CreateDraftResult{}, fault.NotFound("没有找到对应记录")
	}
	var result clmcontract.CreateDraftResult
	if err := clmcontract.UnmarshalStoredDraft(receipt.Response, &result); err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	if result.Draft == nil {
		return result, nil
	}
	sealedBytes, err := json.Marshal(result.Draft)
	if err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	plaintext, err := codec.Open(ctx, record.TenantID, record.ContractID, sealedBytes)
	if err != nil {
		return clmcontract.CreateDraftResult{}, err
	}
	projected := codec.Project(plaintext, access.CanManage(record))
	result.Draft = &projected
	return result, nil
}
