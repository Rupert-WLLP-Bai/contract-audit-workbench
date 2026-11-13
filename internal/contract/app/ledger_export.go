package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/contract/ledger"
	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

type ContractExportRepository interface {
	LockIdempotencyKey(context.Context, ledger.ExportIdentity, string) error
	LoadByIdempotencyKey(context.Context, ledger.ExportIdentity, string) (ledger.ExportReplay, bool, error)
	Insert(context.Context, ledger.ExportInsert) (ledger.Export, error)
	Load(context.Context, ledger.ExportIdentity, string) (ledger.ExportArtifact, bool, error)
}

type ContractExportAccess interface {
	CanRead(context.Context, clmcontract.Aggregate) bool
	CanExport(context.Context, clmcontract.Aggregate) bool
}

type ContractExportAggregateReader interface {
	LoadContract(context.Context, string, string) (clmcontract.Aggregate, error)
}

type ExportAuthorizationSnapshot struct {
	PolicyVersion string
	Role          string
	Grants        []access.Grant
}

type PreparedContractExport struct {
	Input  ledger.ExportInput
	Digest string
}

// PrepareContractExport performs request normalization before the Store opens
// its Repeatable Read transaction.
func PrepareContractExport(input ledger.ExportInput) (PreparedContractExport, error) {
	if err := clmcontract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return PreparedContractExport{}, err
	}
	input.Format = strings.TrimSpace(input.Format)
	if input.Format == "" {
		input.Format = ledger.ExportFormatCSV
	}
	if input.Format != ledger.ExportFormatCSV {
		return PreparedContractExport{}, fault.Invalid("本版导出只支持 csv")
	}
	if err := ledger.ValidateFilter(&input.Filter); err != nil {
		return PreparedContractExport{}, err
	}
	columns, err := ledger.NormalizeExportColumns(input.Columns)
	if err != nil {
		return PreparedContractExport{}, err
	}
	input.Columns = columns
	digest, err := contractExportRequestDigest(input.Filter, columns, input.Format)
	if err != nil {
		return PreparedContractExport{}, err
	}
	return PreparedContractExport{Input: input, Digest: digest}, nil
}

func contractExportRequestDigest(filter ledger.Filter, columns []string, format string) (string, error) {
	return jsonDigest(struct {
		Schema  string        `json:"schema"`
		Filter  ledger.Filter `json:"filter"`
		Columns []string      `json:"columns"`
		Format  string        `json:"format"`
	}{Schema: "clm-contract-export/v1", Filter: filter, Columns: columns, Format: format})
}

// CreateContractExport orchestrates idempotency, current authorization,
// bounded scanning and one immutable snapshot insert in the caller's tx.
func CreateContractExport(ctx context.Context, identity ledger.ExportIdentity, prepared PreparedContractExport,
	authorization ExportAuthorizationSnapshot, repository ContractExportRepository,
	reader LedgerReader, access ContractExportAccess) (ledger.Export, error) {
	if err := repository.LockIdempotencyKey(ctx, identity, prepared.Input.IdempotencyKey); err != nil {
		return ledger.Export{}, err
	}
	existing, found, err := repository.LoadByIdempotencyKey(ctx, identity, prepared.Input.IdempotencyKey)
	if err != nil {
		return ledger.Export{}, err
	}
	if found {
		if err := json.Unmarshal(existing.ColumnsJSON, &existing.Export.Columns); err != nil {
			return ledger.Export{}, fault.Conflict("导出列设置无法读取")
		}
		if existing.RequestDigest != prepared.Digest {
			return ledger.Export{}, fault.Conflict("Idempotency-Key已用于不同导出请求")
		}
		existing.Export.State = "ready"
		existing.Export.DownloadAvailable = existing.Export.ExpiresAt.After(time.Now().UTC())
		existing.Export.Replayed = true
		return existing.Export, nil
	}

	rows, contractIDs, err := collectContractExportRows(ctx, identity.TenantID, prepared.Input.Filter,
		prepared.Input.Columns, reader, access)
	if err != nil {
		return ledger.Export{}, err
	}
	rowsJSON, err := json.Marshal(rows)
	if err != nil {
		return ledger.Export{}, err
	}
	idsJSON, err := json.Marshal(contractIDs)
	if err != nil {
		return ledger.Export{}, err
	}
	filterJSON, err := json.Marshal(prepared.Input.Filter)
	if err != nil {
		return ledger.Export{}, err
	}
	columnsJSON, err := json.Marshal(prepared.Input.Columns)
	if err != nil {
		return ledger.Export{}, err
	}
	grantsDigest, err := exportGrantsDigest(authorization)
	if err != nil {
		return ledger.Export{}, err
	}
	exportID, err := clmcontract.NewContractUUID()
	if err != nil {
		return ledger.Export{}, err
	}
	item, err := repository.Insert(ctx, ledger.ExportInsert{
		TenantID: identity.TenantID, ExportID: exportID, ActorID: identity.ActorID,
		IdempotencyKey: prepared.Input.IdempotencyKey, RequestDigest: prepared.Digest,
		FilterJSON: filterJSON, ColumnsJSON: columnsJSON, Format: prepared.Input.Format,
		ContractIDs: idsJSON, RowsJSON: rowsJSON, RowCount: len(rows),
		PolicyVersion: authorization.PolicyVersion, GrantsDigest: grantsDigest,
	})
	if err != nil {
		return ledger.Export{}, err
	}
	item.Columns = append([]string(nil), prepared.Input.Columns...)
	item.DownloadAvailable = true
	item.Replayed = false
	return item, nil
}

func exportGrantsDigest(authorization ExportAuthorizationSnapshot) (string, error) {
	return jsonDigest(struct {
		Policy string         `json:"policy"`
		Role   string         `json:"role"`
		Grants []access.Grant `json:"grants"`
	}{Policy: authorization.PolicyVersion, Role: authorization.Role, Grants: authorization.Grants})
}

func collectContractExportRows(ctx context.Context, tenantID string, filter ledger.Filter, columns []string,
	reader LedgerReader, access ContractExportAccess) ([]map[string]string, []string, error) {
	rowsOut := []map[string]string{}
	ids := []string{}
	afterSet, afterID := false, ""
	var afterKey time.Time
	scanned := 0
	for {
		rows, err := reader.Read(ctx, tenantID, filter, afterSet, afterKey, afterID, ledger.ExportRowsBatch)
		if err != nil {
			return nil, nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			scanned++
			afterSet, afterKey, afterID = true, row.SortKey, row.Item.ContractID
			if scanned > ledger.ExportMaxScanned {
				return nil, nil, fault.Invalid("筛选范围过大，请缩小条件后重试")
			}
			if !access.CanRead(ctx, row.Aggregate) {
				continue
			}
			if !access.CanExport(ctx, row.Aggregate) {
				return nil, nil, fault.Forbidden("当前授权不能导出筛选范围内的全部合同")
			}
			rowsOut = append(rowsOut, ledger.ExportRowValues(row.Item, columns))
			ids = append(ids, row.Item.ContractID)
			if len(rowsOut) > ledger.ExportMaxRows {
				return nil, nil, fault.Invalid("导出行数超过上限，请缩小筛选范围")
			}
		}
		if len(rows) < ledger.ExportRowsBatch {
			break
		}
	}
	if len(rowsOut) == 0 {
		return nil, nil, fault.Invalid("当前筛选条件下没有可导出的合同")
	}
	return rowsOut, ids, nil
}

// NormalizeContractExportID applies the existing Contract UUID policy before
// the Store opens its read transaction.
func NormalizeContractExportID(exportID string) (string, error) {
	return clmcontract.NormalizeContractUUID(exportID)
}

// LoadContractExportForDownload validates the stored shape and rechecks current
// Read and Export grants for every contract before returning any frozen rows.
func LoadContractExportForDownload(ctx context.Context, identity ledger.ExportIdentity, exportID string,
	repository ContractExportRepository, contracts ContractExportAggregateReader,
	access ContractExportAccess) (ledger.ExportDownload, error) {
	artifact, found, err := repository.Load(ctx, identity, exportID)
	if err != nil {
		return ledger.ExportDownload{}, err
	}
	if !found {
		return ledger.ExportDownload{}, fault.NotFound("没有找到对应的导出")
	}
	if artifact.ActorID != identity.ActorID || artifact.PolicyVersion == "" || !artifact.ExpiresAt.After(time.Now().UTC()) {
		return ledger.ExportDownload{}, fault.NotFound("没有找到对应的导出")
	}
	if artifact.Format != ledger.ExportFormatCSV {
		return ledger.ExportDownload{}, fault.Conflict("导出格式无法识别")
	}
	var columns []string
	var contractIDs []string
	var rows []map[string]string
	if err := json.Unmarshal(artifact.ColumnsJSON, &columns); err != nil {
		return ledger.ExportDownload{}, fault.Conflict("导出列设置无法读取")
	}
	normalizedColumns, err := ledger.NormalizeExportColumns(columns)
	if err != nil {
		return ledger.ExportDownload{}, fault.Conflict("导出列设置无法读取")
	}
	if err := json.Unmarshal(artifact.ContractIDs, &contractIDs); err != nil || len(contractIDs) != artifact.RowCount {
		return ledger.ExportDownload{}, fault.Conflict("导出内容无法读取")
	}
	if err := json.Unmarshal(artifact.RowsJSON, &rows); err != nil || len(rows) != artifact.RowCount {
		return ledger.ExportDownload{}, fault.Conflict("导出内容无法读取")
	}
	aggregates := make([]clmcontract.Aggregate, 0, len(contractIDs))
	for _, contractID := range contractIDs {
		aggregate, err := contracts.LoadContract(ctx, identity.TenantID, contractID)
		if err != nil {
			var faultErr *fault.Error
			if errors.As(err, &faultErr) && faultErr.Status == 404 {
				return ledger.ExportDownload{}, fault.NotFound("没有找到对应的导出")
			}
			return ledger.ExportDownload{}, err
		}
		aggregates = append(aggregates, aggregate)
	}
	for _, aggregate := range aggregates {
		if !access.CanRead(ctx, aggregate) || !access.CanExport(ctx, aggregate) {
			return ledger.ExportDownload{}, fault.NotFound("没有找到对应的导出")
		}
	}
	for _, row := range rows {
		if len(row) != len(normalizedColumns) {
			return ledger.ExportDownload{}, fault.Conflict("导出内容无法读取")
		}
		for _, column := range normalizedColumns {
			if _, ok := row[column]; !ok {
				return ledger.ExportDownload{}, fault.Conflict("导出内容无法读取")
			}
		}
	}
	return ledger.ExportDownload{ExportID: exportID, Format: artifact.Format, Columns: normalizedColumns,
		Rows: rows, ExpiresAt: artifact.ExpiresAt}, nil
}
