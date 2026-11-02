// File metadata (manifest mirror):
// Purpose: 实现 `clmSourceObservationAAD`, `clmSourceObservationBinding`, `sealCLMSourceObservation`, `unsealCLMSourceObservation` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-01
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"

	"contractaudit/internal/clmenvelope"
)

const (
	clmSourceObservationCipherPrefix = "clmsrc1:"
	clmSourceObservationPurpose      = "contract.source.observation"
	clmSourceObservationField        = "source.raw_body"
)

func clmSourceObservationAAD(tenantID, sourceID, eventID, observationID string) ([]byte, error) {
	fields := []string{"clm-source-observation-v1", tenantID, sourceID, eventID, observationID}
	out := make([]byte, 0, 1024)
	for i, field := range fields {
		max := 300
		if i == 0 {
			max = 64
		}
		if strings.TrimSpace(field) == "" || len(field) > max || strings.TrimSpace(field) != field {
			return nil, errors.New("source observation encryption context is invalid")
		}
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(field)))
		out = append(out, size[:]...)
		out = append(out, field...)
	}
	return out, nil
}

func clmSourceObservationBinding(tenantID, sourceID, eventID, observationID string) clmenvelope.Binding {
	// Source/event identifiers are included in the object identity so ciphertext
	// cannot be transplanted to another captured observation.
	return clmenvelope.Binding{TenantID: tenantID, ObjectID: sourceID + ":" + eventID + ":" + observationID,
		Field: clmSourceObservationField, Purpose: clmSourceObservationPurpose, Epoch: clmDataEpoch}
}

func sealCLMSourceObservation(ctx context.Context, raw []byte, tenantID, sourceID, eventID, observationID string) (string, error) {
	if _, err := clmSourceObservationAAD(tenantID, sourceID, eventID, observationID); err != nil {
		return "", err
	}
	provider, err := clmProvider(tenantID)
	if err != nil {
		return "", err
	}
	sealed, err := clmenvelope.Seal(ctx, provider, clmSourceObservationBinding(tenantID, sourceID, eventID, observationID), raw)
	if err != nil {
		return "", errCLMKeyUnavailable
	}
	return sealed, nil
}

// unsealCLMSourceObservation supports the legacy clmsrc1 format only for
// already-persisted captures. New writes always use the tenant/object/field
// bound clmv2 envelope above.
func unsealCLMSourceObservation(ctx context.Context, ciphertext, tenantID, sourceID, eventID, observationID string) ([]byte, error) {
	provider, err := clmProvider(tenantID)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(ciphertext, clmenvelope.PrefixV2) {
		plain, err := clmenvelope.Open(ctx, provider, clmSourceObservationBinding(tenantID, sourceID, eventID, observationID), ciphertext)
		if err != nil {
			return nil, errCLMStoredValue
		}
		return plain, nil
	}
	if !strings.HasPrefix(ciphertext, clmSourceObservationCipherPrefix) {
		return nil, errors.New("source observation ciphertext has an unknown format")
	}
	ref, err := clmLegacyRef()
	if err != nil {
		return nil, errCLMKeyUnavailable
	}
	key, err := provider.ResolveLegacy(ctx, tenantID, clmPurpose, ref)
	if err != nil || key.Ref != ref || len(key.Material) != 32 {
		return nil, errCLMKeyUnavailable
	}
	defer wipeSourceKey(key.Material)
	block, err := aes.NewCipher(key.Material)
	if err != nil {
		return nil, errCLMKeyUnavailable
	}
	aeadCipher, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errCLMKeyUnavailable
	}
	aad, err := clmSourceObservationAAD(tenantID, sourceID, eventID, observationID)
	if err != nil {
		return nil, err
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(ciphertext, clmSourceObservationCipherPrefix))
	if err != nil || len(sealed) < aeadCipher.NonceSize()+aeadCipher.Overhead() {
		return nil, errors.New("source observation ciphertext is malformed")
	}
	plain, err := aeadCipher.Open(nil, sealed[:aeadCipher.NonceSize()], sealed[aeadCipher.NonceSize():], aad)
	if err != nil {
		return nil, errors.New("source observation ciphertext authentication failed")
	}
	return plain, nil
}

func wipeSourceKey(raw []byte) {
	for i := range raw {
		raw[i] = 0
	}
}
