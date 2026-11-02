package clmenvelope

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testEnvelopeProvider(t *testing.T) *LocalProvider {
	t.Helper()
	wrap := bytes.Repeat([]byte{0x21}, 32)
	fingerprint := bytes.Repeat([]byte{0x42}, 32)
	legacy := make([]byte, 32)
	for i := range legacy {
		legacy[i] = byte(i)
	}
	provider, err := NewLocalProvider(LocalKeyConfig{Version: 1, Keys: []LocalKeyEntry{
		{TenantID: "tenant-a", Purpose: "contract.supplier.restricted", Kind: WrapKey,
			Ref: KeyRef{ID: "wrap-a", Version: 2}, MaterialBase64: base64.StdEncoding.EncodeToString(wrap), Active: true},
		{TenantID: "tenant-a", Purpose: "contract.supplier.restricted", Kind: FingerprintKey,
			Ref: KeyRef{ID: "fingerprint-a", Version: 3}, MaterialBase64: base64.StdEncoding.EncodeToString(fingerprint), Active: true},
		{TenantID: "tenant-a", Purpose: "contract.supplier.restricted", Kind: LegacyDataKey,
			Ref: KeyRef{ID: "legacy-a", Version: 1}, MaterialBase64: base64.StdEncoding.EncodeToString(legacy)},
	}})
	if err != nil {
		t.Fatalf("create isolated key provider: %v", err)
	}
	return provider
}

func TestV2EnvelopeRoundTripAndRejectsTenantOrObjectMismatch(t *testing.T) {
	ctx := context.Background()
	provider := testEnvelopeProvider(t)
	binding := Binding{TenantID: "tenant-a", ObjectID: "contract-a", Field: "supplier.bank_account",
		Purpose: "contract.supplier.restricted", Epoch: 1}
	plaintext := []byte("synthetic restricted value")
	sealed, err := Seal(ctx, provider, binding, plaintext)
	if err != nil {
		t.Fatalf("seal v2 value: %v", err)
	}
	if !strings.HasPrefix(sealed, PrefixV2) {
		t.Fatalf("Seal returned prefix %q, want %q", sealed[:min(len(sealed), len(PrefixV2))], PrefixV2)
	}
	opened, err := Open(ctx, provider, binding, sealed)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("round trip: got %q, err=%v", opened, err)
	}
	if bytes.Contains([]byte(sealed), plaintext) {
		t.Fatal("sealed envelope contains plaintext")
	}
	for name, other := range map[string]Binding{
		"tenant": {TenantID: "tenant-b", ObjectID: binding.ObjectID, Field: binding.Field, Purpose: binding.Purpose, Epoch: binding.Epoch},
		"object": {TenantID: binding.TenantID, ObjectID: "contract-b", Field: binding.Field, Purpose: binding.Purpose, Epoch: binding.Epoch},
		"field":  {TenantID: binding.TenantID, ObjectID: binding.ObjectID, Field: "supplier.tax_identifier", Purpose: binding.Purpose, Epoch: binding.Epoch},
		"epoch":  {TenantID: binding.TenantID, ObjectID: binding.ObjectID, Field: binding.Field, Purpose: binding.Purpose, Epoch: 2},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Open(ctx, provider, other, sealed); !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("Open with mismatched %s binding error = %v, want ErrInvalidEnvelope", name, err)
			}
		})
	}
}

func TestRewrapPreservesFrozenDataCiphertext(t *testing.T) {
	ctx := context.Background()
	binding := Binding{TenantID: "tenant-a", ObjectID: "contract-a", Field: "supplier.bank_account", Purpose: "contract.supplier.restricted", Epoch: 1}
	plain := []byte("synthetic rewrap value")
	original, err := Seal(ctx, testEnvelopeProvider(t), binding, plain)
	if err != nil {
		t.Fatal(err)
	}
	nextRef := KeyRef{ID: "wrap-next", Version: 3}
	entries := []LocalKeyEntry{
		{TenantID: binding.TenantID, Purpose: binding.Purpose, Kind: WrapKey, Ref: KeyRef{ID: "wrap-a", Version: 2}, MaterialBase64: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x21}, 32))},
		{TenantID: binding.TenantID, Purpose: binding.Purpose, Kind: WrapKey, Ref: nextRef, MaterialBase64: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x63}, 32)), Active: true},
	}
	rotated, err := NewLocalProvider(LocalKeyConfig{Version: 1, Keys: entries})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := Rewrap(ctx, rotated, binding, original, nextRef)
	if err != nil {
		t.Fatal(err)
	}
	before, err := decodeEnvelope(original)
	if err != nil {
		t.Fatal(err)
	}
	after, err := decodeEnvelope(updated)
	if err != nil {
		t.Fatal(err)
	}
	if before.Binding != after.Binding || before.Nonce != after.Nonce || before.Ciphertext != after.Ciphertext || after.WrapKey != nextRef {
		t.Fatal("rewrap changed data identity or did not select the new wrapping key")
	}
	newOnly, err := NewLocalProvider(LocalKeyConfig{Version: 1, Keys: entries[1:]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, newOnly, binding, original); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("original ciphertext lost its old wrapping-key dependency: %v", err)
	}
	opened, err := Open(ctx, newOnly, binding, updated)
	if err != nil || !bytes.Equal(opened, plain) {
		t.Fatalf("rewrapped ciphertext: %q %v", opened, err)
	}
}

func TestOpenLegacyReadsFrozenV1Bytes(t *testing.T) {
	provider := testEnvelopeProvider(t)
	binding := Binding{TenantID: "tenant-a", ObjectID: "contract-a", Field: "supplier.bank_account",
		Purpose: "contract.supplier.restricted", Epoch: 1}
	// Frozen vector for the pre-v2 store sealCLMValue format: key 00..1f,
	// nonce 00..0b, plaintext "legacy restricted field", AAD "clmv1:".
	const frozen = "clmv1:AAECAwQFBgcICQoLK2exeqac4mnoMuP52IoMCOf24V2VFzvJaFLfM8C61I8hoseQ1RmJ"
	got, err := OpenLegacy(context.Background(), provider, binding, frozen, KeyRef{ID: "legacy-a", Version: 1})
	if err != nil || string(got) != "legacy restricted field" {
		t.Fatalf("legacy read got %q, err=%v", got, err)
	}
	if _, err := OpenLegacy(context.Background(), provider, Binding{TenantID: "tenant-b", ObjectID: binding.ObjectID,
		Field: binding.Field, Purpose: binding.Purpose, Epoch: binding.Epoch}, frozen, KeyRef{ID: "legacy-a", Version: 1}); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("legacy read under another tenant error = %v, want ErrKeyUnavailable", err)
	}
}
