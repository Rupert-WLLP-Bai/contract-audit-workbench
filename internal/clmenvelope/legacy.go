package clmenvelope

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// OpenLegacy exactly preserves clmv1's fixed AAD and nonce-prefixed raw-URL
// base64 layout. Its lookup is tenant/purpose-scoped, but v1 itself cannot prove
// tenant/object/field identity. The caller must authorize the original row and
// explicitly supply its historical key reference; no attribution is invented.
func OpenLegacy(ctx context.Context, p LegacyKeyProvider, b Binding, sealed string, ref KeyRef) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil || p == nil || !validBinding(b) || !validRef(ref) {
		return nil, ErrLegacyKeyRequired
	}
	if !strings.HasPrefix(sealed, LegacyPrefix) || len(sealed) > MaxPlaintextBytes*2+1024 {
		return nil, ErrInvalidEnvelope
	}
	key, err := p.ResolveLegacy(ctx, b.TenantID, b.Purpose, ref)
	if err != nil {
		return nil, ErrKeyUnavailable
	}
	defer wipe(key.Material)
	aead, err := useKey(key, ref)
	if err != nil {
		return nil, err
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(sealed, LegacyPrefix))
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() || len(raw) > MaxPlaintextBytes+aead.NonceSize()+aead.Overhead() {
		return nil, ErrInvalidEnvelope
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(LegacyPrefix))
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	if ctx.Err() != nil {
		wipe(plain)
		return nil, ctx.Err()
	}
	return plain, nil
}

// FingerprintLegacy preserves the old derivation, truncated KeyID and path-NUL-
// value bytes for comparing existing v1 records only. New fingerprints use an
// independently configured FingerprintKey, never this data-key derivation.
func FingerprintLegacy(ctx context.Context, p LegacyKeyProvider, b Binding, ref KeyRef, plaintext []byte) (FingerprintValue, error) {
	if ctx == nil || ctx.Err() != nil || p == nil || !validBinding(b) || !validRef(ref) || len(plaintext) > MaxPlaintextBytes {
		return FingerprintValue{}, ErrLegacyKeyRequired
	}
	key, err := p.ResolveLegacy(ctx, b.TenantID, b.Purpose, ref)
	if err != nil || key.Ref != ref || len(key.Material) != 32 {
		return FingerprintValue{}, ErrKeyUnavailable
	}
	defer wipe(key.Material)
	derive := hmac.New(sha256.New, key.Material)
	derive.Write([]byte(LegacyFingerprintAlgorithm))
	derived := derive.Sum(nil)
	defer wipe(derived)
	mac := hmac.New(sha256.New, derived)
	mac.Write([]byte(b.Field))
	mac.Write([]byte{0})
	mac.Write(plaintext)
	return FingerprintValue{Algorithm: LegacyFingerprintAlgorithm, Ref: ref, LegacyKeyID: hex.EncodeToString(derived[:8]), Digest: hex.EncodeToString(mac.Sum(nil))}, nil
}
