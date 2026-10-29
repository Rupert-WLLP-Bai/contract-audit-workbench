package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const stateCookieAAD = "contractaudit/oidc-state/v1"

type statePayload struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Audience string `json:"audience"`
	IssuedAt int64  `json:"issued_at"`
}

func (m *Manager) sealState(payload statePayload) (string, error) {
	block, err := aes.NewCipher(m.config.StateEncryptionKey)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, plaintext, []byte(stateCookieAAD))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (m *Manager) openState(value string) (statePayload, error) {
	if !validStateCookieShape(value) {
		return statePayload{}, errors.New("invalid state cookie")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return statePayload{}, errors.New("invalid state cookie")
	}
	plaintext, err := m.openSealedState(sealed)
	if err != nil {
		return statePayload{}, errors.New("invalid state cookie")
	}
	payload, err := decodeStatePayload(plaintext)
	if err != nil {
		return statePayload{}, errors.New("invalid state cookie")
	}
	if _, _, err := m.clientCredentials(payload.Audience); err != nil {
		return statePayload{}, errors.New("invalid state cookie")
	}
	return payload, nil
}

func validStateCookieShape(value string) bool {
	return len(value) != 0 && len(value) <= 4096 && !strings.Contains(value, "=")
}

func (m *Manager) openSealedState(sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(m.config.StateEncryptionKey)
	if err != nil {
		return nil, errors.New("invalid state cookie")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("invalid state cookie")
	}
	nonce := sealed[:aead.NonceSize()]
	return aead.Open(nil, nonce, sealed[aead.NonceSize():], []byte(stateCookieAAD))
}

func decodeStatePayload(plaintext []byte) (statePayload, error) {
	var payload statePayload
	if err := json.Unmarshal(plaintext, &payload); err != nil || payload.State == "" || payload.Nonce == "" || payload.Verifier == "" || payload.IssuedAt == 0 {
		return statePayload{}, errors.New("invalid state cookie")
	}
	return payload, nil
}
