// File metadata (manifest mirror):
// Purpose: 实现 `LocalKeyConfig`, `LocalKeyEntry`, `localKeyID`, `localScope` 等密文封装、读取或密钥版本操作，绑定租户、对象和用途。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package clmenvelope

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
)

// LocalKeyConfig is trusted offline configuration. There is no URL lookup,
// wildcard tenant, derived default, or fallback to a process-global secret.
type LocalKeyConfig struct {
	Version int             `json:"version"`
	Keys    []LocalKeyEntry `json:"keys"`
}
type LocalKeyEntry struct {
	TenantID       string  `json:"tenant_id"`
	Purpose        string  `json:"purpose"`
	Kind           KeyKind `json:"kind"`
	Ref            KeyRef  `json:"ref"`
	MaterialBase64 string  `json:"material_base64"`
	Active         bool    `json:"active"`
}
type localKeyID struct {
	tenant, purpose string
	kind            KeyKind
	ref             KeyRef
}
type localScope struct {
	tenant, purpose string
	kind            KeyKind
}
type LocalProvider struct {
	keys   map[localKeyID][]byte
	active map[localScope]KeyRef
}

// ParseLocalProvider accepts one strict configuration object supplied by the
// composition root. Errors never include material or the raw configuration.
func ParseLocalProvider(raw []byte) (*LocalProvider, error) {
	if len(raw) > 1<<20 {
		return nil, ErrKeyUnavailable
	}
	var config LocalKeyConfig
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&config) != nil || d.Decode(new(any)) != io.EOF {
		return nil, ErrKeyUnavailable
	}
	return NewLocalProvider(config)
}
func NewLocalProvider(config LocalKeyConfig) (*LocalProvider, error) {
	if config.Version != 1 || len(config.Keys) == 0 || len(config.Keys) > 4096 {
		return nil, ErrKeyUnavailable
	}
	p := &LocalProvider{keys: map[localKeyID][]byte{}, active: map[localScope]KeyRef{}}
	for _, entry := range config.Keys {
		if !validText(entry.TenantID) || !validText(entry.Purpose) || !validRef(entry.Ref) || (entry.Kind != WrapKey && entry.Kind != FingerprintKey && entry.Kind != LegacyDataKey) || entry.Kind == LegacyDataKey && entry.Active {
			return nil, ErrKeyUnavailable
		}
		material, err := base64.StdEncoding.Strict().DecodeString(entry.MaterialBase64)
		if err != nil {
			material, err = base64.RawURLEncoding.Strict().DecodeString(entry.MaterialBase64)
		}
		if err != nil || len(material) != 32 {
			return nil, ErrKeyUnavailable
		}
		id := localKeyID{entry.TenantID, entry.Purpose, entry.Kind, entry.Ref}
		if _, exists := p.keys[id]; exists {
			return nil, ErrKeyUnavailable
		}
		for oldID, old := range p.keys {
			if oldID.kind != entry.Kind && bytes.Equal(old, material) {
				return nil, ErrKeyUnavailable
			}
		}
		p.keys[id] = append([]byte(nil), material...)
		if entry.Active {
			scope := localScope{entry.TenantID, entry.Purpose, entry.Kind}
			if _, exists := p.active[scope]; exists {
				return nil, ErrKeyUnavailable
			}
			p.active[scope] = entry.Ref
		}
	}
	return p, nil
}
func (p *LocalProvider) Current(ctx context.Context, tenant, purpose string, kind KeyKind) (Key, error) {
	if ctx == nil || ctx.Err() != nil || p == nil || kind == LegacyDataKey {
		return Key{}, ErrKeyUnavailable
	}
	ref, ok := p.active[localScope{tenant, purpose, kind}]
	if !ok {
		return Key{}, ErrKeyUnavailable
	}
	return p.Resolve(ctx, tenant, purpose, kind, ref)
}
func (p *LocalProvider) Resolve(ctx context.Context, tenant, purpose string, kind KeyKind, ref KeyRef) (Key, error) {
	if ctx == nil || ctx.Err() != nil || p == nil || kind != WrapKey && kind != FingerprintKey {
		return Key{}, ErrKeyUnavailable
	}
	material, ok := p.keys[localKeyID{tenant, purpose, kind, ref}]
	if !ok {
		return Key{}, ErrKeyUnavailable
	}
	return Key{Ref: ref, Material: append([]byte(nil), material...)}, nil
}
func (p *LocalProvider) ResolveLegacy(ctx context.Context, tenant, purpose string, ref KeyRef) (Key, error) {
	if ctx == nil || ctx.Err() != nil || p == nil {
		return Key{}, ErrKeyUnavailable
	}
	material, ok := p.keys[localKeyID{tenant, purpose, LegacyDataKey, ref}]
	if !ok {
		return Key{}, ErrKeyUnavailable
	}
	return Key{Ref: ref, Material: append([]byte(nil), material...)}, nil
}
