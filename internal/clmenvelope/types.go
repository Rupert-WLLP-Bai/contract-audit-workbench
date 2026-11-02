// File metadata (manifest mirror):
// Purpose: 实现 `KeyKind`, `KeyRef`, `Binding`, `Key` 等密文封装、读取或密钥版本操作，绑定租户、对象和用途。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package clmenvelope implements versioned CLM field encryption. It supplies
// cryptographic binding, not actor authentication or database authorization.
package clmenvelope

import (
	"context"
	"errors"
)

const (
	PrefixV2                   = "clmv2:"
	LegacyPrefix               = "clmv1:"
	Algorithm                  = "AES-256-GCM"
	FingerprintAlgorithm       = "hmac-sha256-clm-field-v2"
	LegacyFingerprintAlgorithm = "hmac-sha256-clm-diff-v1"
	MaxPlaintextBytes          = 16 << 20
)

var (
	ErrKeyUnavailable    = errors.New("CLM envelope key unavailable")
	ErrInvalidEnvelope   = errors.New("invalid CLM envelope or binding")
	ErrLegacyKeyRequired = errors.New("legacy CLM read requires an explicit configured legacy key reference")
)

type KeyKind string

const (
	WrapKey        KeyKind = "wrap"
	FingerprintKey KeyKind = "fingerprint"
	LegacyDataKey  KeyKind = "legacy_data"
)

type KeyRef struct {
	ID      string `json:"id"`
	Version uint64 `json:"version"`
}

// Binding identifies the immutable logical owner/field, not a mutable storage
// location or KEK version. Epoch changes only on an explicit new data seal.
type Binding struct {
	TenantID string `json:"tenant_id"`
	ObjectID string `json:"object_id"`
	Field    string `json:"field"`
	Purpose  string `json:"purpose"`
	Epoch    uint64 `json:"epoch"`
}
type Key struct {
	Ref      KeyRef
	Material []byte
}
type KeyProvider interface {
	Current(context.Context, string, string, KeyKind) (Key, error)
	Resolve(context.Context, string, string, KeyKind, KeyRef) (Key, error)
}

// Legacy resolution is separate so normal Open/Seal never silently select an
// old data key. Legacy v1 has no cryptographically verifiable object binding.
type LegacyKeyProvider interface {
	ResolveLegacy(context.Context, string, string, KeyRef) (Key, error)
}
type Dependency struct {
	TenantID string  `json:"tenant_id"`
	Purpose  string  `json:"purpose"`
	Kind     KeyKind `json:"kind"`
	Ref      KeyRef  `json:"ref"`
}
type FingerprintValue struct {
	Algorithm   string `json:"algorithm"`
	Ref         KeyRef `json:"key_ref"`
	Digest      string `json:"digest"`
	LegacyKeyID string `json:"legacy_key_id,omitempty"`
}
