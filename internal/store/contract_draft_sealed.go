package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"contractaudit/internal/fault"
)

// Restricted supplier fields (docs/04 敏感级别 restricted) are sealed with an
// independent CLM data key before any persistence. Plaintext never enters
// JSONB, receipts, logs or error messages. The key is read from CLM_DATA_KEY
// (base64 of exactly 32 bytes); it is deliberately separate from every other
// configuration secret, and LLM_CONFIG_KEY must never be reused here. Without
// a usable key, operations whose content carries restricted fields fail
// explicitly instead of falling back to a derived or empty key.
const (
	clmDataKeyEnv        = "CLM_DATA_KEY"
	clmSealedValuePrefix = "clmv1:"
)

var errCLMKeyUnavailable = fault.New(503, "clm_data_key_unavailable",
	"合同敏感字段数据密钥不可用，已拒绝处理受限字段")

var errCLMStoredValue = errors.New("合同敏感字段存储格式无效或与数据密钥不匹配")

// clmDataKeyBytes parses the configured base64 key on every use. The parse is
// cheap, and refusing to cache keeps a reconfigured or cleared environment
// authoritative for every later call; a missing, malformed or wrong-length
// key is an explicit configuration failure, never a fallback.
func clmDataKeyBytes() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(clmDataKeyEnv))
	if raw == "" {
		return nil, errCLMKeyUnavailable
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(raw)
	}
	if err != nil || len(decoded) != 32 {
		return nil, errCLMKeyUnavailable
	}
	return decoded, nil
}

// clmDataKey returns the AEAD used to seal restricted supplier fields. The key
// bytes are parsed afresh each call; see clmDataKeyBytes.
func clmDataKey() (cipher.AEAD, error) {
	decoded, err := clmDataKeyBytes()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(decoded)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealCLMValue(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	aead, err := clmDataKey()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), []byte(clmSealedValuePrefix))
	return clmSealedValuePrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func unsealCLMValue(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if !strings.HasPrefix(sealed, clmSealedValuePrefix) {
		return "", errors.New("restricted contract field is not sealed")
	}
	aead, err := clmDataKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(sealed, clmSealedValuePrefix))
	if err != nil || len(raw) < aead.NonceSize() {
		return "", errors.New("restricted contract field seal is malformed")
	}
	nonce, ciphertext := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(clmSealedValuePrefix))
	if err != nil {
		return "", errors.New("restricted contract field seal does not match the configured key")
	}
	return string(plaintext), nil
}

// supplierRestrictedFields lists the restricted supplier fields (docs/04) in a
// fixed order so sealing, unsealing and masking always cover the same set.
var supplierRestrictedFields = []struct {
	get    func(*Supplier) string
	set    func(*Supplier, string)
	masked string
}{
	{func(s *Supplier) string { return s.PayeeName }, func(s *Supplier, v string) { s.PayeeName = v }, "•••"},
	{func(s *Supplier) string { return s.BankName }, func(s *Supplier, v string) { s.BankName = v }, "••••"},
	{func(s *Supplier) string { return s.BankAccount }, func(s *Supplier, v string) { s.BankAccount = v }, "••••••••"},
	{func(s *Supplier) string { return s.TaxIdentifier }, func(s *Supplier, v string) { s.TaxIdentifier = v }, "••••••"},
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
	if draftHasRestrictedFields(&draft) {
		if _, err := clmDataKey(); err != nil {
			return DraftContent{}, nil, "", err
		}
	}
	sealed := cloneDraftContent(draft)
	sealed.Supplier = copySupplier(draft.Supplier)
	if sealed.Supplier != nil {
		for _, field := range supplierRestrictedFields {
			value, err := sealCLMValue(field.get(sealed.Supplier))
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
		plaintext, err := unsealCLMValue(value)
		if err != nil {
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
