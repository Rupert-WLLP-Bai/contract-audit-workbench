package store

import (
	"context"
	"encoding/json"
	"errors"

	"contractaudit/internal/clmenvelope"
	"contractaudit/internal/fault"
)

// Restricted supplier fields never leave the server as plaintext in a version
// diff. Because AES-GCM uses a fresh nonce per seal, two identical plaintext
// values produce different ciphertext, so ciphertext equality is meaningless.
// Instead each frozen version stores an HMAC fingerprint of every restricted
// field computed from the plaintext at freeze time. Diffing two versions
// compares those fingerprints and reports only changed/unchanged; the value and
// the mask never appear in the response.
const clmRestrictedFingerprintAlgorithm = clmenvelope.FingerprintAlgorithm
const clmLegacyRestrictedFingerprintAlgorithm = clmenvelope.LegacyFingerprintAlgorithm

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
	KeyRef    *clmenvelope.KeyRef                      `json:"key_ref,omitempty"`
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

// computeRestrictedFingerprints is called on the plaintext snapshot inside the
// freeze transaction, before the sealed bytes are copied into the revision.
// When no restricted field carries a value, the key is not required: every
// field is recorded absent and the diff can still compare presence.
func computeRestrictedFingerprints(draft *DraftContent) (clmRestrictedFingerprintSet, error) {
	set := clmRestrictedFingerprintSet{
		Algorithm: clmLegacyRestrictedFingerprintAlgorithm,
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
	return clmRestrictedFingerprintSet{}, errCLMKeyUnavailable
}

func computeRestrictedFingerprintsBound(ctx context.Context, draft *DraftContent, binding clmenvelope.Binding, baseline *clmRestrictedFingerprintSet) (clmRestrictedFingerprintSet, error) {
	if baseline != nil && baseline.Algorithm == clmLegacyRestrictedFingerprintAlgorithm {
		set := clmRestrictedFingerprintSet{Algorithm: clmLegacyRestrictedFingerprintAlgorithm, Fields: make(map[string]clmRestrictedFingerprintState, len(clmRestrictedFieldPaths))}
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
		ref, err := clmLegacyRef()
		if err != nil {
			return clmRestrictedFingerprintSet{}, errCLMKeyUnavailable
		}
		provider, err := clmProvider(binding.TenantID)
		if err != nil {
			return clmRestrictedFingerprintSet{}, err
		}
		for _, path := range clmRestrictedFieldPaths {
			value := clmRestrictedFieldValue[path](draft)
			if value == "" {
				set.Fields[path] = clmRestrictedFingerprintState{Present: false}
				continue
			}
			fieldBinding := binding
			fieldBinding.Field = path
			fingerprint, err := clmenvelope.FingerprintLegacy(ctx, provider, fieldBinding, ref, []byte(value))
			if err != nil {
				return clmRestrictedFingerprintSet{}, errCLMKeyUnavailable
			}
			set.KeyID = fingerprint.LegacyKeyID
			set.Fields[path] = clmRestrictedFingerprintState{Present: true, Digest: fingerprint.Digest}
		}
		return set, nil
	}
	set := clmRestrictedFingerprintSet{Algorithm: clmRestrictedFingerprintAlgorithm, Fields: make(map[string]clmRestrictedFingerprintState, len(clmRestrictedFieldPaths))}
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
	provider, err := clmProvider(binding.TenantID)
	if err != nil {
		return clmRestrictedFingerprintSet{}, err
	}
	for _, path := range clmRestrictedFieldPaths {
		value := clmRestrictedFieldValue[path](draft)
		if value == "" {
			set.Fields[path] = clmRestrictedFingerprintState{Present: false}
			continue
		}
		fieldBinding := binding
		fieldBinding.Field = path
		fingerprint, err := clmenvelope.Fingerprint(ctx, provider, fieldBinding, []byte(value))
		if err != nil {
			return clmRestrictedFingerprintSet{}, errCLMKeyUnavailable
		}
		if set.KeyRef == nil {
			ref := fingerprint.Ref
			set.KeyRef = &ref
		} else if *set.KeyRef != fingerprint.Ref {
			return clmRestrictedFingerprintSet{}, errors.New("fingerprint key changed during one snapshot")
		}
		set.Fields[path] = clmRestrictedFingerprintState{Present: true, Digest: fingerprint.Digest}
	}
	return set, nil
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
	if set.Algorithm != clmRestrictedFingerprintAlgorithm && set.Algorithm != clmLegacyRestrictedFingerprintAlgorithm || len(set.Fields) != len(clmRestrictedFieldPaths) {
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
	anyPresent := false
	for _, state := range set.Fields {
		anyPresent = anyPresent || state.Present
	}
	if set.Algorithm == clmRestrictedFingerprintAlgorithm && anyPresent && set.KeyRef == nil {
		return clmRestrictedFingerprintSet{}, fault.Conflict("冻结版本的受限字段指纹无效")
	}
	if set.KeyRef != nil {
		if set.KeyRef.Version == 0 || len(set.KeyRef.ID) == 0 || len(set.KeyRef.ID) > 128 {
			return clmRestrictedFingerprintSet{}, fault.Conflict("冻结版本的受限字段指纹无效")
		}
		for _, char := range set.KeyRef.ID {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
				return clmRestrictedFingerprintSet{}, fault.Conflict("冻结版本的受限字段指纹无效")
			}
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
	if set.Algorithm == clmRestrictedFingerprintAlgorithm && set.KeyRef != nil && (set.KeyRef.ID == "" || set.KeyRef.Version == 0) {
		return clmRestrictedFingerprintSet{}, fault.Conflict("冻结版本的受限字段指纹无效")
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
	return unsealDraftForFreezeBound(context.Background(), raw, "", "")
}

func unsealDraftForFreezeBound(ctx context.Context, raw []byte, tenant, objectID string) (DraftContent, error) {
	var probe DraftContent
	if err := unmarshalStoredDraft(raw, &probe); err != nil {
		return DraftContent{}, err
	}
	if draftSealedRestrictedPresent(&probe) {
		if tenant == "" || objectID == "" {
			return DraftContent{}, errCLMKeyUnavailable
		}
	}
	draft, err := unsealStoredDraftBound(ctx, raw, tenant, objectID)
	if err != nil {
		if errors.Is(err, errCLMKeyUnavailable) {
			return DraftContent{}, errCLMKeyUnavailable
		}
		return DraftContent{}, err
	}
	return draft, nil
}
