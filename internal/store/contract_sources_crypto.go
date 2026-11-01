package store

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

const clmSourceObservationCipherPrefix = "clmsrc1:"

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

func sealCLMSourceObservation(raw []byte, tenantID, sourceID, eventID, observationID string) (string, error) {
	aad, err := clmSourceObservationAAD(tenantID, sourceID, eventID, observationID)
	if err != nil {
		return "", err
	}
	aeadCipher, err := clmDataKey()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aeadCipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate source observation nonce: %w", err)
	}
	sealed := aeadCipher.Seal(nonce, nonce, raw, aad)
	return clmSourceObservationCipherPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func unsealCLMSourceObservation(ciphertext, tenantID, sourceID, eventID, observationID string) ([]byte, error) {
	if !strings.HasPrefix(ciphertext, clmSourceObservationCipherPrefix) {
		return nil, errors.New("source observation ciphertext has an unknown format")
	}
	aad, err := clmSourceObservationAAD(tenantID, sourceID, eventID, observationID)
	if err != nil {
		return nil, err
	}
	aeadCipher, err := clmDataKey()
	if err != nil {
		return nil, err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ciphertext, clmSourceObservationCipherPrefix))
	if err != nil || len(sealed) < aeadCipher.NonceSize()+aeadCipher.Overhead() {
		return nil, errors.New("source observation ciphertext is malformed")
	}
	nonce, encrypted := sealed[:aeadCipher.NonceSize()], sealed[aeadCipher.NonceSize():]
	plaintext, err := aeadCipher.Open(nil, nonce, encrypted, aad)
	if err != nil {
		return nil, errors.New("source observation ciphertext authentication failed")
	}
	return plaintext, nil
}
