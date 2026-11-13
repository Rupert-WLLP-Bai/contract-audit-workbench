package app

import (
	"context"

	"contractaudit/internal/contract/ledger"
	clmcontract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

// SavedViewRepository exposes only owner-scoped saved-view CRUD.
type SavedViewRepository interface {
	List(context.Context, ledger.SavedViewIdentity) ([]ledger.SavedView, error)
	Create(context.Context, ledger.SavedViewIdentity, ledger.SavedView) (ledger.SavedView, error)
	Replace(context.Context, ledger.SavedViewIdentity, string, int64, ledger.SavedView) (ledger.SavedView, error)
	Delete(context.Context, ledger.SavedViewIdentity, string, int64) error
}

// PrepareSavedView validates the public create request before Store opens a transaction.
func PrepareSavedView(input ledger.SavedViewInput) (ledger.SavedView, error) {
	filter := input.Filter
	name, columns, err := ledger.NormalizeSavedViewInput(input.Name, &filter, input.Columns)
	if err != nil {
		return ledger.SavedView{}, err
	}
	viewID, err := clmcontract.NewContractUUID()
	if err != nil {
		return ledger.SavedView{}, err
	}
	return ledger.SavedView{ViewID: viewID, Name: name, Filter: filter, Columns: columns}, nil
}

// PrepareSavedViewReplacement validates the owner-supplied ID, CAS version and replacement fields.
func PrepareSavedViewReplacement(input ledger.SavedViewUpdateInput) (string, int64, ledger.SavedView, error) {
	viewID, err := ledger.NormalizeSavedViewID(input.ViewID)
	if err != nil {
		return "", 0, ledger.SavedView{}, err
	}
	if input.ExpectedRowVersion < 1 {
		return "", 0, ledger.SavedView{}, fault.Invalid("替换视图必须携带当前 row_version")
	}
	filter := input.Filter
	name, columns, err := ledger.NormalizeSavedViewInput(input.Name, &filter, input.Columns)
	if err != nil {
		return "", 0, ledger.SavedView{}, err
	}
	return viewID, input.ExpectedRowVersion, ledger.SavedView{
		ViewID: viewID, Name: name, Filter: filter, Columns: columns,
	}, nil
}

// PrepareSavedViewDeletion validates the ID and positive expected row version.
func PrepareSavedViewDeletion(viewID string, expectedRowVersion int64) (string, error) {
	normalized, err := ledger.NormalizeSavedViewID(viewID)
	if err != nil {
		return "", err
	}
	if expectedRowVersion < 1 {
		return "", fault.Invalid("删除视图必须携带当前 row_version")
	}
	return normalized, nil
}

// ListSavedViews asks the adapter for only the supplied owner's rows.
func ListSavedViews(ctx context.Context, identity ledger.SavedViewIdentity, repository SavedViewRepository) ([]ledger.SavedView, error) {
	return repository.List(ctx, identity)
}

// CreateSavedView persists a prepared preference under the resolved owner identity.
func CreateSavedView(ctx context.Context, identity ledger.SavedViewIdentity, view ledger.SavedView,
	repository SavedViewRepository) (ledger.SavedView, error) {
	return repository.Create(ctx, identity, view)
}

// ReplaceSavedView applies an owner-scoped row-version replacement.
func ReplaceSavedView(ctx context.Context, identity ledger.SavedViewIdentity, viewID string,
	expectedRowVersion int64, replacement ledger.SavedView, repository SavedViewRepository) (ledger.SavedView, error) {
	return repository.Replace(ctx, identity, viewID, expectedRowVersion, replacement)
}

// DeleteSavedView applies an owner-scoped row-version deletion.
func DeleteSavedView(ctx context.Context, identity ledger.SavedViewIdentity, viewID string,
	expectedRowVersion int64, repository SavedViewRepository) error {
	return repository.Delete(ctx, identity, viewID, expectedRowVersion)
}
