package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"contractaudit/internal/clmenvelope"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// Restricted supplier fields (docs/04 敏感级别 restricted) are sealed with an
// independent CLM data key before any persistence. Plaintext never enters
// JSONB, receipts, logs or error messages. The key is read from CLM_DATA_KEY
// (base64 of exactly 32 bytes); it is deliberately separate from every other
// configuration secret, and LLM_CONFIG_KEY must never be reused here. Without
// a usable key, operations whose content carries restricted fields fail
// explicitly instead of falling back to a derived or empty key.
const (
	clmEnvelopeKeysEnv = "CLM_ENVELOPE_KEYS"
	clmLegacyRefEnv    = "CLM_LEGACY_DATA_KEY_REF"
	clmPurpose         = "contract.supplier.restricted"
	clmDataEpoch       = 1
)

var errCLMKeyUnavailable = fault.New(503, "clm_data_key_unavailable",
	"合同敏感字段数据密钥不可用，已拒绝处理受限字段")

var errCLMStoredValue = errors.New("合同敏感字段存储格式无效或与数据密钥不匹配")

func clmProvider(tenant string) (*clmenvelope.LocalProvider, error) {
	raw := strings.TrimSpace(os.Getenv(clmEnvelopeKeysEnv))
	if raw == "" {
		return nil, errCLMKeyUnavailable
	}
	provider, err := clmenvelope.ParseLocalProvider([]byte(raw))
	if err != nil {
		return nil, errCLMKeyUnavailable
	}
	return provider, nil
}

func clmLegacyRef() (clmenvelope.KeyRef, error) {
	parts := strings.Split(strings.TrimSpace(os.Getenv(clmLegacyRefEnv)), ":")
	if len(parts) != 2 || parts[0] == "" {
		return clmenvelope.KeyRef{}, clmenvelope.ErrLegacyKeyRequired
	}
	version, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || version == 0 {
		return clmenvelope.KeyRef{}, clmenvelope.ErrLegacyKeyRequired
	}
	return clmenvelope.KeyRef{ID: parts[0], Version: version}, nil
}

func clmFieldBinding(tenant, objectID, field string) clmenvelope.Binding {
	return clmenvelope.Binding{TenantID: tenant, ObjectID: objectID, Field: field, Purpose: clmPurpose, Epoch: clmDataEpoch}
}

func sealCLMValue(ctx context.Context, tenant, objectID, field, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	provider, err := clmProvider(tenant)
	if err != nil {
		return "", err
	}
	sealed, err := clmenvelope.Seal(ctx, provider, clmFieldBinding(tenant, objectID, field), []byte(plaintext))
	if err != nil {
		return "", errCLMKeyUnavailable
	}
	return sealed, nil
}

func unsealCLMValue(ctx context.Context, tenant, objectID, field, sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	provider, err := clmProvider(tenant)
	if err != nil {
		return "", err
	}
	binding := clmFieldBinding(tenant, objectID, field)
	var plaintext []byte
	switch {
	case strings.HasPrefix(sealed, clmenvelope.PrefixV2):
		plaintext, err = clmenvelope.Open(ctx, provider, binding, sealed)
	case strings.HasPrefix(sealed, clmenvelope.LegacyPrefix):
		var ref clmenvelope.KeyRef
		ref, err = clmLegacyRef()
		if err == nil {
			plaintext, err = clmenvelope.OpenLegacy(ctx, provider, binding, sealed, ref)
		}
	default:
		return "", errors.New("restricted contract field is not sealed")
	}
	if err != nil {
		if errors.Is(err, clmenvelope.ErrKeyUnavailable) || errors.Is(err, clmenvelope.ErrLegacyKeyRequired) {
			return "", errCLMKeyUnavailable
		}
		return "", errCLMStoredValue
	}
	return string(plaintext), nil
}

// supplierRestrictedFields lists the restricted supplier fields (docs/04) in a
// fixed order so sealing, unsealing and masking always cover the same set.
var supplierRestrictedFields = []struct {
	path   string
	get    func(*Supplier) string
	set    func(*Supplier, string)
	masked string
}{
	{"supplier.payee_name", func(s *Supplier) string { return s.PayeeName }, func(s *Supplier, v string) { s.PayeeName = v }, "•••"},
	{"supplier.bank_name", func(s *Supplier) string { return s.BankName }, func(s *Supplier, v string) { s.BankName = v }, "••••"},
	{"supplier.bank_account", func(s *Supplier) string { return s.BankAccount }, func(s *Supplier, v string) { s.BankAccount = v }, "••••••••"},
	{"supplier.tax_identifier", func(s *Supplier) string { return s.TaxIdentifier }, func(s *Supplier, v string) { s.TaxIdentifier = v }, "••••••"},
}

// cloneDraftContent copies the content envelope. Restricted values are always
// replaced through copySupplier, so the shared slice headers are never
// mutated through the copy.
func cloneDraftContent(draft DraftContent) DraftContent {
	return draft
}

func copySupplier(supplier *Supplier) *Supplier {
	if supplier == nil {
		return nil
	}
	copied := *supplier
	return &copied
}

// draftHasRestrictedFields reports whether any restricted supplier field
// carries a value, so callers can refuse early when the data key is absent.
func draftHasRestrictedFields(draft *DraftContent) bool {
	if draft == nil || draft.Supplier == nil {
		return false
	}
	for _, field := range supplierRestrictedFields {
		if field.get(draft.Supplier) != "" {
			return true
		}
	}
	return false
}

// sealDraftContent returns a copy whose restricted fields are sealed, together
// with the exact JSON bytes that may be persisted and their SHA-256 digest.
func sealDraftContent(draft DraftContent) (DraftContent, []byte, string, error) {
	return sealDraftContentBound(context.Background(), draft, "", "")
}

func sealDraftContentBound(ctx context.Context, draft DraftContent, tenant, objectID string) (DraftContent, []byte, string, error) {
	if draftHasRestrictedFields(&draft) {
		if tenant == "" || objectID == "" {
			return DraftContent{}, nil, "", errCLMKeyUnavailable
		}
		if _, err := clmProvider(tenant); err != nil {
			return DraftContent{}, nil, "", err
		}
	}
	sealed := cloneDraftContent(draft)
	sealed.Supplier = copySupplier(draft.Supplier)
	if sealed.Supplier != nil {
		for _, field := range supplierRestrictedFields {
			value, err := sealCLMValue(ctx, tenant, objectID, field.path, field.get(sealed.Supplier))
			if err != nil {
				return DraftContent{}, nil, "", err
			}
			field.set(sealed.Supplier, value)
		}
	}
	encoded, err := json.Marshal(sealed)
	if err != nil {
		return DraftContent{}, nil, "", err
	}
	return sealed, encoded, sha256Bytes(encoded), nil
}

// canonicalStoredJSON returns the exact text PostgreSQL will hand back for a
// jsonb value. Both revision snapshots are persisted as jsonb, which
// reorders object keys and inserts separators, so a digest computed on the
// pre-insert Go bytes would never match SELECT output. Normalising through
// jsonb first keeps snapshot_digest verifiable from the stored row.
func canonicalStoredJSON(ctx context.Context, tx pgx.Tx, raw []byte) ([]byte, error) {
	var canonical string
	if err := tx.QueryRow(ctx, `SELECT ($1::jsonb)::text`, string(raw)).Scan(&canonical); err != nil {
		return nil, err
	}
	return []byte(canonical), nil
}

// unmarshalStoredDraft decodes persisted content bytes strictly; a corrupted
// or unexpectedly shaped row fails instead of projecting partially.
func unmarshalStoredDraft(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("stored contract content is not a single object")
	}
	return nil
}

// unsealStoredDraft loads persisted content and restores restricted fields.
// Persisted plaintext in a restricted field is treated as corruption and
// rejected instead of silently returned.
func unsealStoredDraft(raw []byte) (DraftContent, error) {
	return unsealStoredDraftBound(context.Background(), raw, "", "")
}

func unsealStoredDraftBound(ctx context.Context, raw []byte, tenant, objectID string) (DraftContent, error) {
	var stored DraftContent
	if err := unmarshalStoredDraft(raw, &stored); err != nil {
		return DraftContent{}, err
	}
	if stored.Supplier == nil {
		return stored, nil
	}
	for _, field := range supplierRestrictedFields {
		value := field.get(stored.Supplier)
		if value == "" {
			continue
		}
		if tenant == "" || objectID == "" {
			return DraftContent{}, errCLMKeyUnavailable
		}
		plaintext, err := unsealCLMValue(ctx, tenant, objectID, field.path, value)
		if err != nil {
			if errors.Is(err, errCLMKeyUnavailable) {
				return DraftContent{}, errCLMKeyUnavailable
			}
			return DraftContent{}, errCLMStoredValue
		}
		field.set(stored.Supplier, plaintext)
	}
	return stored, nil
}

// projectDraftForViewer returns the draft a viewer may see. The caller must
// already have unsealed storage and checked contract manage authority for the
// reveal decision. Masked viewers receive fixed masks that carry no plaintext
// fragment; absent fields stay absent so a mask never invents data.
func projectDraftForViewer(draft DraftContent, revealRestricted bool) DraftContent {
	if revealRestricted {
		return draft
	}
	projected := cloneDraftContent(draft)
	projected.Supplier = copySupplier(draft.Supplier)
	if projected.Supplier == nil {
		return projected
	}
	for _, field := range supplierRestrictedFields {
		if field.get(projected.Supplier) != "" {
			field.set(projected.Supplier, field.masked)
		}
	}
	return projected
}
