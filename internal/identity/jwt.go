package identity

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

type idTokenClaims struct {
	Issuer          string
	Subject         string
	Audience        []string
	AuthorizedParty string
	Nonce           string
	ExpiresAt       float64
	IssuedAt        float64
	NotBefore       *float64
}

type jwtHeader struct {
	Algorithm string   `json:"alg"`
	KeyID     string   `json:"kid"`
	Critical  []string `json:"crit"`
	B64       *bool    `json:"b64"`
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	KeyType   string   `json:"kty"`
	KeyID     string   `json:"kid"`
	Use       string   `json:"use"`
	Algorithm string   `json:"alg"`
	KeyOps    []string `json:"key_ops"`
	Modulus   string   `json:"n"`
	Exponent  string   `json:"e"`
}

func (m *Manager) verifyIDToken(ctx context.Context, metadata providerMetadata, token, clientID, expectedNonce string) (idTokenClaims, error) {
	if len(token) == 0 || len(token) > maxProviderJSON {
		return idTokenClaims{}, ErrAuthentication
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return idTokenClaims{}, ErrAuthentication
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return idTokenClaims{}, ErrAuthentication
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm != "RS256" || header.KeyID == "" || len(header.Critical) != 0 || (header.B64 != nil && !*header.B64) {
		return idTokenClaims{}, ErrAuthentication
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return idTokenClaims{}, ErrAuthentication
	}
	claims, err := decodeIDClaims(payloadBytes)
	if err != nil {
		return idTokenClaims{}, ErrAuthentication
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return idTokenClaims{}, ErrAuthentication
	}
	jwksBytes, err := m.getJSON(ctx, metadata.JWKSURI)
	if err != nil {
		return idTokenClaims{}, ErrAuthentication
	}
	var document jwksDocument
	if err := json.Unmarshal(jwksBytes, &document); err != nil || len(document.Keys) == 0 || len(document.Keys) > 4096 {
		return idTokenClaims{}, ErrAuthentication
	}
	publicKey, err := rsaKeyForID(document.Keys, header.KeyID)
	if err != nil {
		return idTokenClaims{}, ErrAuthentication
	}
	signed := []byte(parts[0] + "." + parts[1])
	digest := sha256.Sum256(signed)
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return idTokenClaims{}, ErrAuthentication
	}
	if !validIDClaims(claims, metadata.Issuer, clientID, expectedNonce, time.Now()) {
		return idTokenClaims{}, ErrAuthentication
	}
	return claims, nil
}

func decodeIDClaims(data []byte) (idTokenClaims, error) {
	var raw struct {
		Issuer          string          `json:"iss"`
		Subject         string          `json:"sub"`
		Audience        json.RawMessage `json:"aud"`
		AuthorizedParty string          `json:"azp"`
		Nonce           string          `json:"nonce"`
		ExpiresAt       json.RawMessage `json:"exp"`
		IssuedAt        json.RawMessage `json:"iat"`
		NotBefore       json.RawMessage `json:"nbf"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return idTokenClaims{}, err
	}
	audience, err := decodeAudience(raw.Audience)
	if err != nil {
		return idTokenClaims{}, err
	}
	expiresAt, err := decodeNumericDate(raw.ExpiresAt)
	if err != nil {
		return idTokenClaims{}, err
	}
	issuedAt, err := decodeNumericDate(raw.IssuedAt)
	if err != nil {
		return idTokenClaims{}, err
	}
	var notBefore *float64
	if len(raw.NotBefore) != 0 {
		value, err := decodeNumericDate(raw.NotBefore)
		if err != nil {
			return idTokenClaims{}, err
		}
		notBefore = &value
	}
	return idTokenClaims{
		Issuer: raw.Issuer, Subject: raw.Subject, Audience: audience,
		AuthorizedParty: raw.AuthorizedParty, Nonce: raw.Nonce,
		ExpiresAt: expiresAt, IssuedAt: issuedAt, NotBefore: notBefore,
	}, nil
}

func decodeAudience(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("missing audience")
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		if single == "" {
			return nil, errors.New("empty audience")
		}
		return []string{single}, nil
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil || len(multiple) == 0 {
		return nil, errors.New("invalid audience")
	}
	for _, value := range multiple {
		if value == "" {
			return nil, errors.New("empty audience")
		}
	}
	return multiple, nil
}

func decodeNumericDate(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 {
		return 0, errors.New("missing numeric date")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return 0, err
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("invalid numeric date")
	}
	return value, nil
}

func validIDClaims(claims idTokenClaims, issuer, clientID, expectedNonce string, now time.Time) bool {
	if claims.Issuer != issuer || claims.Subject == "" || len(claims.Subject) > 1024 || claims.Nonce == "" || claims.AuthorizedParty != "" && claims.AuthorizedParty != clientID {
		return false
	}
	hasAudience := false
	for _, audience := range claims.Audience {
		if audience == clientID {
			hasAudience = true
		}
	}
	if !hasAudience || (len(claims.Audience) > 1 && claims.AuthorizedParty != clientID) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(expectedNonce)) != 1 {
		return false
	}
	nowSeconds := float64(now.Unix())
	if claims.ExpiresAt <= nowSeconds || claims.IssuedAt > nowSeconds+60 || claims.IssuedAt <= 0 {
		return false
	}
	if claims.NotBefore != nil && *claims.NotBefore > nowSeconds+60 {
		return false
	}
	return true
}

func rsaKeyForID(keys []jwk, keyID string) (*rsa.PublicKey, error) {
	var match *jwk
	for index := range keys {
		if keys[index].KeyID != keyID {
			continue
		}
		if match != nil {
			return nil, errors.New("duplicate matching key ID")
		}
		match = &keys[index]
	}
	if match == nil || match.KeyType != "RSA" || (match.Use != "" && match.Use != "sig") || (match.Algorithm != "" && match.Algorithm != "RS256") || !supportsVerify(match.KeyOps) {
		return nil, errors.New("no usable signing key")
	}
	modulusBytes, err := base64.RawURLEncoding.DecodeString(match.Modulus)
	if err != nil || len(modulusBytes) < 256 || len(modulusBytes) > 1024 {
		return nil, errors.New("invalid RSA modulus")
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(match.Exponent)
	if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
		return nil, errors.New("invalid RSA exponent")
	}
	exponent := 0
	for _, value := range exponentBytes {
		exponent = exponent<<8 | int(value)
	}
	if exponent < 3 || exponent%2 == 0 {
		return nil, errors.New("invalid RSA exponent")
	}
	modulus := new(big.Int).SetBytes(modulusBytes)
	if modulus.BitLen() < 2048 || modulus.BitLen() > 8192 {
		return nil, errors.New("invalid RSA key size")
	}
	return &rsa.PublicKey{N: modulus, E: exponent}, nil
}

func supportsVerify(operations []string) bool {
	if len(operations) == 0 {
		return true
	}
	for _, operation := range operations {
		if operation == "verify" {
			return true
		}
	}
	return false
}
