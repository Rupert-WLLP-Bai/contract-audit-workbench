package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"contractaudit/internal/fault"
)

// Restricted supplier fields never leave the server as plaintext in a version
// diff. Because AES-GCM uses a fresh nonce per seal, two identical plaintext
// values produce different ciphertext, so ciphertext equality is meaningless.
// Instead each frozen version stores an HMAC fingerprint of every restricted
// field computed from the plaintext at freeze time. Diffing two versions
// compares those fingerprints and reports only changed/unchanged; the value and
// the mask never appear in the response.
const clmRestrictedFingerprintAlgorithm = "hmac-sha256-clm-diff-v1"

var clmRestrictedFieldPaths = []string{
	"supplier.payee_name",
	"supplier.bank_name",
	"supplier.bank_account",
	"supplier.tax_identifier",
}

var clmRestrictedFieldValue = map[string]func(*DraftContent) string{
	"supplier.payee_name":     func(d *DraftContent) string { return d.Supplier.PayeeName },
	"supplier.bank_name":      func(d *DraftContent) string { return d.Supplier.BankName },
	"supplier.bank_account":   func(d *DraftContent) string { return d.Supplier.BankAccount },
	"supplier.tax_identifier": func(d *DraftContent) string { return d.Supplier.TaxIdentifier },
}

// clmRestrictedFingerprintState records presence separately from the digest:
// an absent field has no digest and compares as equal to another absent field
// without needing the data key.
type clmRestrictedFingerprintState struct {
	Present bool   `json:"present"`
	Digest  string `json:"digest,omitempty"`
}

type clmRestrictedFingerprintSet struct {
	Algorithm string                                   `json:"algorithm"`
	KeyID     string                                   `json:"key_id,omitempty"`
	Fields    map[string]clmRestrictedFingerprintState `json:"fields"`
}

func clmRestrictedFieldPath(path string) bool {
	for _, candidate := range clmRestrictedFieldPaths {
		if path == candidate {
			return true
		}
	}
	return false
}

// clmVersionDiffKey derives a dedicated HMAC key from CLM_DATA_KEY. The raw
// data key is never used directly and never leaves this function. Rotating
// CLM_DATA_KEY changes KeyID; a diff then reports the restricted comparison as
// unknown instead of claiming a change it cannot prove.
func clmVersionDiffKey() ([]byte, string, error) {
	raw, err := clmDataKeyBytes()
	if err != nil {
		return nil, "", err
	}
	derive := hmac.New(sha256.New, raw)
	derive.Write([]byte(clmRestrictedFingerprintAlgorithm))
	derived := derive.Sum(nil)
	return derived, hex.EncodeToString(derived[:8]), nil
}

func clmRestrictedFingerprint(key []byte, path, value string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(path))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

// computeRestrictedFingerprints is called on the plaintext snapshot inside the
// freeze transaction, before the sealed bytes are copied into the revision.
// When no restricted field carries a value, the key is not required: every
// field is recorded absent and the diff can still compare presence.
func computeRestrictedFingerprints(draft *DraftContent) (clmRestrictedFingerprintSet, error) {
	set := clmRestrictedFingerprintSet{
		Algorithm: clmRestrictedFingerprintAlgorithm,
		Fields:    make(map[string]clmRestrictedFingerprintState, len(clmRestrictedFieldPaths)),
	}
	present := false
	if draft != nil && draft.Supplier != nil {
		for _, path := range clmRestrictedFieldPaths {
			if clmRestrictedFieldValue[path](draft) != "" {
				present = true
				break
			}
		}
	}
	if !present {
		for _, path := range clmRestrictedFieldPaths {
			set.Fields[path] = clmRestrictedFingerprintState{Present: false}
		}
		return set, nil
	}
	key, keyID, err := clmVersionDiffKey()
	if err != nil {
		return clmRestrictedFingerprintSet{}, err
	}
	set.KeyID = keyID
	for _, path := range clmRestrictedFieldPaths {
		value := clmRestrictedFieldValue[path](draft)
		if value == "" {
			set.Fields[path] = clmRestrictedFingerprintState{Present: false}
			continue
		}
		set.Fields[path] = clmRestrictedFingerprintState{Present: true, Digest: clmRestrictedFingerprint(key, path, value)}
	}
	return set, nil
}

func (set clmRestrictedFingerprintSet) anyPresent() bool {
	for _, state := range set.Fields {
		if state.Present {
			return true
		}
	}
	return false
}

func validCLMHex64(value string) bool {
	return digestPattern.MatchString(value)
}

// parseRestrictedFingerprints validates a stored seal. A malformed projection
// is a storage conflict, never a reason to guess a diff.
func parseRestrictedFingerprints(raw []byte) (clmRestrictedFingerprintSet, error) {
	var set clmRestrictedFingerprintSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return clmRestrictedFingerprintSet{}, fault.Conflict("冻结版本的受限字段指纹无效")
	}
	if set.Algorithm != clmRestrictedFingerprintAlgorithm || len(set.Fields) != len(clmRestrictedFieldPaths) {
		return clmRestrictedFingerprintSet{}, fault.Conflict("冻结版本的受限字段指纹无效")
	}
	for _, path := range clmRestrictedFieldPaths {
		state, ok := set.Fields[path]
		if !ok {
			return clmRestrictedFingerprintSet{}, fault.Conflict("冻结版本的受限字段指纹无效")
		}
		if state.Present && !validCLMHex64(state.Digest) {
			return clmRestrictedFingerprintSet{}, fault.Conflict("冻结版本的受限字段指纹无效")
		}
	}
	if set.KeyID != "" {
		// KeyID is a truncated digest: exactly 16 lowercase hex characters.
		if len(set.KeyID) != 16 {
			return clmRestrictedFingerprintSet{}, fault.Conflict("冻结版本的受限字段指纹无效")
		}
		for _, char := range set.KeyID {
			if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
				return clmRestrictedFingerprintSet{}, fault.Conflict("冻结版本的受限字段指纹无效")
			}
		}
	}
	return set, nil
}

// draftSealedRestrictedPresent reports whether the stored snapshot claims any
// restricted value. It never unseals and never logs the value.
func draftSealedRestrictedPresent(draft *DraftContent) bool {
	if draft == nil || draft.Supplier == nil {
		return false
	}
	for _, path := range clmRestrictedFieldPaths {
		if clmRestrictedFieldValue[path](draft) != "" {
			return true
		}
	}
	return false
}

// unsealDraftForFreeze restores the plaintext needed to run StageFinal and to
// fingerprint restricted fields. When restricted fields are present but the
// data key is unavailable, it returns the explicit 503 key error instead of a
// generic corruption error.
func unsealDraftForFreeze(raw []byte) (DraftContent, error) {
	var probe DraftContent
	if err := unmarshalStoredDraft(raw, &probe); err != nil {
		return DraftContent{}, err
	}
	if draftSealedRestrictedPresent(&probe) {
		if _, err := clmDataKey(); err != nil {
			return DraftContent{}, err
		}
	}
	draft, err := unsealStoredDraft(raw)
	if err != nil {
		if errors.Is(err, errCLMKeyUnavailable) {
			return DraftContent{}, errCLMKeyUnavailable
		}
		return DraftContent{}, err
	}
	return draft, nil
}
