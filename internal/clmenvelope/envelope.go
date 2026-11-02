package clmenvelope

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

type envelope struct {
	Version    int     `json:"version"`
	Algorithm  string  `json:"algorithm"`
	Binding    Binding `json:"binding"`
	WrapKey    KeyRef  `json:"wrap_key"`
	Nonce      string  `json:"nonce"`
	Ciphertext string  `json:"ciphertext"`
	WrappedDEK string  `json:"wrapped_dek"`
}

func validText(s string) bool {
	if len(s) == 0 || len(s) > 1024 || strings.TrimSpace(s) == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validRef(ref KeyRef) bool {
	if len(ref.ID) == 0 || len(ref.ID) > 128 || ref.Version == 0 {
		return false
	}
	for _, r := range ref.ID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
func validBinding(b Binding) bool {
	return validText(b.TenantID) && validText(b.ObjectID) && validText(b.Field) && validText(b.Purpose) && b.Epoch > 0
}
func appendPart(dst []byte, s string) []byte {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(s)))
	dst = append(dst, size[:]...)
	return append(dst, s...)
}
func bindingAAD(b Binding) []byte {
	out := []byte("clm-envelope/data/v2\x00")
	for _, s := range []string{b.TenantID, b.ObjectID, b.Field, b.Purpose} {
		out = appendPart(out, s)
	}
	var epoch [8]byte
	binary.BigEndian.PutUint64(epoch[:], b.Epoch)
	return append(out, epoch[:]...)
}
func wrapAAD(e envelope) []byte {
	out := append([]byte("clm-envelope/wrap/v2\x00"), bindingAAD(e.Binding)...)
	out = appendPart(out, e.Algorithm)
	out = appendPart(out, e.WrapKey.ID)
	var version [8]byte
	binary.BigEndian.PutUint64(version[:], e.WrapKey.Version)
	out = append(out, version[:]...)
	hash := sha256.Sum256([]byte(e.Nonce + "\x00" + e.Ciphertext))
	return append(out, hash[:]...)
}
func aead(material []byte) (cipher.AEAD, error) {
	if len(material) != 32 {
		return nil, ErrKeyUnavailable
	}
	block, err := aes.NewCipher(material)
	if err != nil {
		return nil, ErrKeyUnavailable
	}
	return cipher.NewGCM(block)
}
func wipe(raw []byte) {
	for i := range raw {
		raw[i] = 0
	}
}
func useKey(k Key, ref KeyRef) (cipher.AEAD, error) {
	if k.Ref != ref || !validRef(k.Ref) || len(k.Material) != 32 {
		return nil, ErrKeyUnavailable
	}
	return aead(k.Material)
}
func randomBytes(size int) ([]byte, error) {
	raw := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return nil, ErrKeyUnavailable
	}
	return raw, nil
}
func encodeEnvelope(e envelope) (string, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	return PrefixV2 + base64.RawURLEncoding.EncodeToString(raw), nil
}
func decodeEnvelope(sealed string) (envelope, error) {
	var e envelope
	if !strings.HasPrefix(sealed, PrefixV2) || len(sealed) > (MaxPlaintextBytes*2+16384) {
		return e, ErrInvalidEnvelope
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(sealed, PrefixV2))
	if err != nil {
		return e, ErrInvalidEnvelope
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&e) != nil || d.Decode(new(any)) != io.EOF {
		return envelope{}, ErrInvalidEnvelope
	}
	canonical, err := json.Marshal(e)
	if err != nil || !bytes.Equal(raw, canonical) || e.Version != 2 || e.Algorithm != Algorithm || !validBinding(e.Binding) || !validRef(e.WrapKey) {
		return envelope{}, ErrInvalidEnvelope
	}
	return e, nil
}
func envelopeBytes(e envelope) (nonce, ciphertext, wrapped []byte, err error) {
	nonce, err = base64.RawURLEncoding.Strict().DecodeString(e.Nonce)
	if err != nil || len(nonce) != 12 {
		return nil, nil, nil, ErrInvalidEnvelope
	}
	ciphertext, err = base64.RawURLEncoding.Strict().DecodeString(e.Ciphertext)
	if err != nil || len(ciphertext) < 16 || len(ciphertext) > MaxPlaintextBytes+16 {
		return nil, nil, nil, ErrInvalidEnvelope
	}
	wrapped, err = base64.RawURLEncoding.Strict().DecodeString(e.WrappedDEK)
	if err != nil || len(wrapped) != 12+32+16 {
		return nil, nil, nil, ErrInvalidEnvelope
	}
	return nonce, ciphertext, wrapped, nil
}

// Seal always emits v2 with an independent random DEK. Missing keys, including
// for an empty plaintext, never produce a plaintext or legacy fallback.
func Seal(ctx context.Context, p KeyProvider, b Binding, plaintext []byte) (string, error) {
	if ctx == nil || ctx.Err() != nil || p == nil || !validBinding(b) || len(plaintext) > MaxPlaintextBytes {
		return "", ErrInvalidEnvelope
	}
	key, err := p.Current(ctx, b.TenantID, b.Purpose, WrapKey)
	if err != nil {
		return "", ErrKeyUnavailable
	}
	defer wipe(key.Material)
	kek, err := useKey(key, key.Ref)
	if err != nil {
		return "", err
	}
	dek, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	defer wipe(dek)
	data, err := aead(dek)
	if err != nil {
		return "", err
	}
	nonce, err := randomBytes(data.NonceSize())
	if err != nil {
		return "", err
	}
	e := envelope{Version: 2, Algorithm: Algorithm, Binding: b, WrapKey: key.Ref, Nonce: base64.RawURLEncoding.EncodeToString(nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(data.Seal(nil, nonce, plaintext, bindingAAD(b)))}
	wrapNonce, err := randomBytes(kek.NonceSize())
	if err != nil {
		return "", err
	}
	e.WrappedDEK = base64.RawURLEncoding.EncodeToString(kek.Seal(wrapNonce, wrapNonce, dek, wrapAAD(e)))
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return encodeEnvelope(e)
}
func unwrap(ctx context.Context, p KeyProvider, b Binding, e envelope) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil || p == nil || !validBinding(b) || e.Binding != b {
		return nil, ErrInvalidEnvelope
	}
	_, _, wrapped, err := envelopeBytes(e)
	if err != nil {
		return nil, err
	}
	key, err := p.Resolve(ctx, b.TenantID, b.Purpose, WrapKey, e.WrapKey)
	if err != nil {
		return nil, ErrKeyUnavailable
	}
	defer wipe(key.Material)
	kek, err := useKey(key, e.WrapKey)
	if err != nil {
		return nil, err
	}
	dek, err := kek.Open(nil, wrapped[:kek.NonceSize()], wrapped[kek.NonceSize():], wrapAAD(e))
	if err != nil || len(dek) != 32 {
		wipe(dek)
		return nil, ErrInvalidEnvelope
	}
	return dek, nil
}

// Open requires a v2 envelope and caller-selected binding. Legacy requires the
// separate explicit OpenLegacy API; neither path accepts stored plaintext.
func Open(ctx context.Context, p KeyProvider, b Binding, sealed string) ([]byte, error) {
	e, err := decodeEnvelope(sealed)
	if err != nil {
		return nil, err
	}
	dek, err := unwrap(ctx, p, b, e)
	if err != nil {
		return nil, err
	}
	defer wipe(dek)
	nonce, ciphertext, _, err := envelopeBytes(e)
	if err != nil {
		return nil, err
	}
	data, err := aead(dek)
	if err != nil {
		return nil, err
	}
	plaintext, err := data.Open(nil, nonce, ciphertext, bindingAAD(b))
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	if ctx.Err() != nil {
		wipe(plaintext)
		return nil, ctx.Err()
	}
	return plaintext, nil
}

// Rewrap changes only WrapKey and WrappedDEK. It does not rewrite data AAD,
// epoch, nonce or ciphertext and must be persisted as an explicit authorized
// operation, never an implicit read repair of frozen historical content.
func Rewrap(ctx context.Context, p KeyProvider, b Binding, sealed string, target KeyRef) (string, error) {
	if !validRef(target) {
		return "", ErrKeyUnavailable
	}
	e, err := decodeEnvelope(sealed)
	if err != nil {
		return "", err
	}
	dek, err := unwrap(ctx, p, b, e)
	if err != nil {
		return "", err
	}
	defer wipe(dek)
	nonce, ciphertext, _, err := envelopeBytes(e)
	if err != nil {
		return "", err
	}
	data, err := aead(dek)
	if err != nil {
		return "", err
	}
	plain, err := data.Open(nil, nonce, ciphertext, bindingAAD(b))
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	wipe(plain)
	key, err := p.Resolve(ctx, b.TenantID, b.Purpose, WrapKey, target)
	if err != nil {
		return "", ErrKeyUnavailable
	}
	defer wipe(key.Material)
	kek, err := useKey(key, target)
	if err != nil {
		return "", err
	}
	e.WrapKey = target
	wrapNonce, err := randomBytes(kek.NonceSize())
	if err != nil {
		return "", err
	}
	e.WrappedDEK = base64.RawURLEncoding.EncodeToString(kek.Seal(wrapNonce, wrapNonce, dek, wrapAAD(e)))
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return encodeEnvelope(e)
}
func Fingerprint(ctx context.Context, p KeyProvider, b Binding, plaintext []byte) (FingerprintValue, error) {
	if ctx == nil || ctx.Err() != nil || p == nil || !validBinding(b) || len(plaintext) > MaxPlaintextBytes {
		return FingerprintValue{}, ErrInvalidEnvelope
	}
	key, err := p.Current(ctx, b.TenantID, b.Purpose, FingerprintKey)
	return fingerprintWithKey(b, plaintext, key, key.Ref, err)
}

// FingerprintAt computes a v2 fingerprint with a previously frozen key reference.
func FingerprintAt(ctx context.Context, p KeyProvider, b Binding, ref KeyRef, plaintext []byte) (FingerprintValue, error) {
	if ctx == nil || ctx.Err() != nil || p == nil || !validBinding(b) || !validRef(ref) || len(plaintext) > MaxPlaintextBytes {
		return FingerprintValue{}, ErrInvalidEnvelope
	}
	key, err := p.Resolve(ctx, b.TenantID, b.Purpose, FingerprintKey, ref)
	return fingerprintWithKey(b, plaintext, key, ref, err)
}

func fingerprintWithKey(b Binding, plaintext []byte, key Key, ref KeyRef, keyErr error) (FingerprintValue, error) {
	defer wipe(key.Material)
	if keyErr != nil || key.Ref != ref || !validRef(key.Ref) || len(key.Material) != 32 {
		return FingerprintValue{}, ErrKeyUnavailable
	}
	mac := hmac.New(sha256.New, key.Material)
	mac.Write([]byte(FingerprintAlgorithm + "\x00"))
	mac.Write(bindingAAD(b))
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(plaintext)))
	mac.Write(size[:])
	mac.Write(plaintext)
	return FingerprintValue{Algorithm: FingerprintAlgorithm, Ref: key.Ref, Digest: hex.EncodeToString(mac.Sum(nil))}, nil
}

// Dependencies describes unverified header references for inventory only. An
// inventory with a legacy/unknown dependency must not authorize key deletion.
func Dependencies(sealed string) ([]Dependency, error) {
	if strings.HasPrefix(sealed, LegacyPrefix) {
		return nil, ErrLegacyKeyRequired
	}
	e, err := decodeEnvelope(sealed)
	if err != nil {
		return nil, err
	}
	if _, _, _, err = envelopeBytes(e); err != nil {
		return nil, err
	}
	return []Dependency{{TenantID: e.Binding.TenantID, Purpose: e.Binding.Purpose, Kind: WrapKey, Ref: e.WrapKey}}, nil
}
func LegacyDependency(b Binding, ref KeyRef) (Dependency, error) {
	if !validBinding(b) || !validRef(ref) {
		return Dependency{}, ErrLegacyKeyRequired
	}
	return Dependency{TenantID: b.TenantID, Purpose: b.Purpose, Kind: LegacyDataKey, Ref: ref}, nil
}
