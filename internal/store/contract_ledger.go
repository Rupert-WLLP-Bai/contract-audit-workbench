// File metadata (manifest mirror):
// Purpose: Contract ledger reads the existing aggregate and its sealed content without adding a second projection table. Every row is authorized through the same case-scope policy as the… Related `store` operations include normalizeLedgerText, ledgerAmountScaled, normalizeLedgerAmount, normalizeLedgerDate.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-05
// Revision: r4
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	contractapp "contractaudit/internal/contract/app"
	"contractaudit/internal/contract/ledger"
	contractpostgres "contractaudit/internal/contract/postgres"
	"contractaudit/internal/fault"
	platformpostgres "contractaudit/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
)

// Contract ledger data models and query rules are owned by internal/contract/ledger.
// Store aliases keep the current HTTP and saved-view consumers source compatible.
const (
	contractLedgerDefaultLimit   = ledger.DefaultLimit
	contractLedgerMaxLimit       = ledger.MaxLimit
	contractLedgerCursorVer      = ledger.CursorVersion
	contractLedgerSortCreated    = ledger.SortCreated
	contractExportMaxRows        = ledger.ExportMaxRows
	contractExportMaxScanned     = ledger.ExportMaxScanned
	contractExportFormatCSV      = ledger.ExportFormatCSV
	contractExportRowsBatch      = ledger.ExportRowsBatch
	contractLedgerContentAmount  = ledger.ContentAmountPattern
	contractLedgerTimeLayout     = ledger.TimeLayout
	contractLedgerDateLayout     = ledger.DateLayout
	contractLedgerCursorMaxBytes = ledger.CursorMaxBytes
)

type ContractLedgerFilter = ledger.Filter
type ContractLedgerItem = ledger.Item
type ContractLedgerPage = ledger.Page
type ContractLedgerQuery = ledger.Query
type contractLedgerCursor = ledger.Cursor
type ContractSavedView = ledger.SavedView
type ContractSavedViewInput = ledger.SavedViewInput
type ContractSavedViewUpdateInput = ledger.SavedViewUpdateInput

type ContractExportInput = ledger.ExportInput
type ContractExport = ledger.Export
type ContractExportDownload = ledger.ExportDownload

func ledgerAmountScaled(value string) (int64, bool) { return ledger.AmountScaled(value) }
func validateContractLedgerFilter(filter *ContractLedgerFilter) error {
	return ledger.ValidateFilter(filter)
}

func normalizeContractLedgerColumns(columns []string) ([]string, error) {
	return ledger.NormalizeSavedViewColumns(columns)
}

func validateContractLedgerSort(sort string) (string, error) { return ledger.ValidateSort(sort) }
func contractLedgerFilterDigest(filter ContractLedgerFilter, sort string) (string, error) {
	return ledger.FilterDigest(filter, sort)
}
func encodeContractLedgerCursor(current accounts.User, sort, digest string, key time.Time, contractID string) string {
	return ledger.EncodeCursor(ledger.CursorIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}, sort, digest, key, contractID)
}
func decodeContractLedgerCursor(value string, current accounts.User, sort, digest string) (*contractLedgerCursor, error) {
	return ledger.DecodeCursor(value, ledger.CursorIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}, sort, digest)
}

func (s *Store) beginContractLedgerRead(ctx context.Context, user accounts.User) (pgx.Tx, accounts.User, error) {
	ctx = commandContext(ctx, user)
	if !s.HasDatabase() {
		return nil, accounts.User{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return nil, accounts.User{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, accounts.User{}, err
	}
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		rollbackCLM(tx)
		return nil, accounts.User{}, err
	}
	return tx, current, nil
}

func contractLedgerItemFrom(record clmContractRecord, content []byte, inReview bool) (ContractLedgerItem, error) {
	return ledger.ItemFromAggregate(record, content, inReview)
}

// ListContractLedger returns one authorized keyset page. SQL narrows by the
// validated filter and the case-scope policy authorizes every returned row.
func (s *Store) ListContractLedger(ctx context.Context, user accounts.User, query ContractLedgerQuery) (ContractLedgerPage, error) {
	ctx = commandContext(ctx, user)
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ContractLedgerPage{}, err
	}
	defer rollbackCLM(tx)
	identity := ledger.CursorIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}
	reader := contractpostgres.NewLedgerReader(platformpostgres.Bind(tx))
	page, err := contractapp.ListContractLedger(ctx, identity, query, reader, storeLedgerAccess{tx: tx, user: current})
	if err != nil {
		return ContractLedgerPage{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return ContractLedgerPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractLedgerPage{}, err
	}
	return page, nil
}

// ListContractSavedViews returns only the caller's own personal views.
func (s *Store) ListContractSavedViews(ctx context.Context, user accounts.User) ([]ContractSavedView, error) {
	ctx = commandContext(ctx, user)
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return nil, err
	}
	defer rollbackCLM(tx)
	identity := ledger.SavedViewIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}
	repository := contractpostgres.NewSavedViewCommands(platformpostgres.Bind(tx))
	out, err := contractapp.ListSavedViews(ctx, identity, repository)
	if err != nil {
		return nil, err
	}
	if _, err := trustedUser(current); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateContractSavedView stores one personal view. The owner and tenant come
// only from the authenticated session.
func (s *Store) CreateContractSavedView(ctx context.Context, user accounts.User, input ContractSavedViewInput) (ContractSavedView, error) {
	ctx = commandContext(ctx, user)
	prepared, err := contractapp.PrepareSavedView(input)
	if err != nil {
		return ContractSavedView{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能保存视图", nil)
	if err != nil {
		return ContractSavedView{}, err
	}
	defer rollbackCLM(tx)
	identity := ledger.SavedViewIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}
	repository := contractpostgres.NewSavedViewCommands(platformpostgres.Bind(tx))
	item, err := contractapp.CreateSavedView(ctx, identity, prepared, repository)
	if err != nil {
		return ContractSavedView{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return ContractSavedView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractSavedView{}, err
	}
	return item, nil
}

// ReplaceContractSavedView replaces one personal view under optimistic
// concurrency.
func (s *Store) ReplaceContractSavedView(ctx context.Context, user accounts.User, input ContractSavedViewUpdateInput) (ContractSavedView, error) {
	ctx = commandContext(ctx, user)
	viewID, expectedRowVersion, replacement, err := contractapp.PrepareSavedViewReplacement(input)
	if err != nil {
		return ContractSavedView{}, err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能修改视图", nil)
	if err != nil {
		return ContractSavedView{}, err
	}
	defer rollbackCLM(tx)
	identity := ledger.SavedViewIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}
	repository := contractpostgres.NewSavedViewCommands(platformpostgres.Bind(tx))
	item, err := contractapp.ReplaceSavedView(ctx, identity, viewID, expectedRowVersion, replacement, repository)
	if err != nil {
		return ContractSavedView{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return ContractSavedView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractSavedView{}, err
	}
	return item, nil
}

// DeleteContractSavedView removes one personal view under optimistic
// concurrency. Preferences are not audit facts, so a hard delete is allowed.
func (s *Store) DeleteContractSavedView(ctx context.Context, user accounts.User, viewID string, expectedRowVersion int64) error {
	ctx = commandContext(ctx, user)
	normalized, err := contractapp.PrepareSavedViewDeletion(viewID, expectedRowVersion)
	if err != nil {
		return err
	}
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能删除视图", nil)
	if err != nil {
		return err
	}
	defer rollbackCLM(tx)
	identity := ledger.SavedViewIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}
	repository := contractpostgres.NewSavedViewCommands(platformpostgres.Bind(tx))
	if err := contractapp.DeleteSavedView(ctx, identity, normalized, expectedRowVersion, repository); err != nil {
		return err
	}
	if _, err := trustedUser(current); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CreateContractExport keeps identity, transaction, RLS scope and commit at the
// Store boundary; Contract app owns request and snapshot behavior.
func (s *Store) CreateContractExport(ctx context.Context, user accounts.User, input ContractExportInput) (ContractExport, error) {
	ctx = commandContext(ctx, user)
	prepared, err := contractapp.PrepareContractExport(input)
	if err != nil {
		return ContractExport{}, err
	}
	tx, current, err := s.beginCLMWriteIsolated(ctx, user, "只读模式下不能创建导出", nil,
		pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return ContractExport{}, err
	}
	defer rollbackCLM(tx)
	identity := ledger.ExportIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}
	executor := platformpostgres.Bind(tx)
	repository := contractpostgres.NewLedgerExports(executor)
	reader := contractpostgres.NewLedgerReader(executor)
	accessSnapshot := contractapp.ExportAuthorizationSnapshot{
		PolicyVersion: access.PolicyVersion, Role: current.Role, Grants: current.Grants,
	}
	item, err := contractapp.CreateContractExport(ctx, identity, prepared, accessSnapshot,
		repository, reader, storeContractExportAccess{tx: tx, user: current})
	if err != nil {
		return ContractExport{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return ContractExport{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractExport{}, err
	}
	return item, nil
}

// LoadContractExportForDownload re-authorizes every frozen contract at pickup.
// The artifact adapter returns only the caller tenant's frozen data; Contract
// app validates its shape before Store's current per-contract policy bridge.
func (s *Store) LoadContractExportForDownload(ctx context.Context, user accounts.User, exportID string) (ContractExportDownload, error) {
	ctx = commandContext(ctx, user)
	normalized, err := contractapp.NormalizeContractExportID(exportID)
	if err != nil {
		return ContractExportDownload{}, err
	}
	tx, current, err := s.beginContractLedgerRead(ctx, user)
	if err != nil {
		return ContractExportDownload{}, err
	}
	defer rollbackCLM(tx)
	identity := ledger.ExportIdentity{TenantID: accounts.Tenant(current), ActorID: current.ID}
	executor := platformpostgres.Bind(tx)
	repository := contractpostgres.NewLedgerExports(executor)
	contracts := contractpostgres.NewDraftCommands(executor)
	access := storeContractExportAccess{tx: tx, user: current}
	item, err := contractapp.LoadContractExportForDownload(ctx, identity, normalized, repository, contracts, access)
	if err != nil {
		return ContractExportDownload{}, err
	}
	if _, err := trustedUser(current); err != nil {
		return ContractExportDownload{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractExportDownload{}, err
	}
	return item, nil
}

// ReauthorizeContractExportDownload is a legacy identity-only check. It does
// not recheck export ownership or contract scope and must not be used as the
// download authorization boundary; callers must reload with
// LoadContractExportForDownload after rendering and before sending bytes.
func ReauthorizeContractExportDownload(user accounts.User) error {
	if _, err := trustedUser(user); err != nil {
		return fault.NotFound("没有找到对应的导出")
	}
	return nil
}

// RenderContractExportCSV preserves the Store-facing helper used by HTTP.
func RenderContractExportCSV(columns []string, rows []map[string]string) ([]byte, error) {
	return ledger.RenderExportCSV(columns, rows)
}
