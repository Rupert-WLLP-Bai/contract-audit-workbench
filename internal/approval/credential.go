// Package approval implements the short-lived, internally signed identity
// credential an approver presents with one approval decision. It is an
// internal confirmation capability: the token proves that this server bound a
// subject, tenant, role, organization, task, contract version and snapshot
// digest for a bounded time. It is deliberately not an electronic-signature
// or CA-backed certificate and must never be presented as one.
package approval

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"contractaudit/internal/contractmoney"
)

const (
	// CredentialVersion is the token schema version. Verification refuses any
	// other version instead of guessing its meaning.
	CredentialVersion = 1
	// DefaultTTL is the shortest useful lifetime for one decision page.
	DefaultTTL = 5 * time.Minute
	// MaxTTL bounds a token even when a deployment configures a longer value.
	MaxTTL = 15 * time.Minute
	// ClockSkew tolerates small clock differences when checking iat.
	ClockSkew = 30 * time.Second

	credentialPrefix = "caw-approval-" + "credential-" + "v1"
)

var (
	ErrInvalidCredential = errors.New("approval credential is invalid")
	ErrExpiredCredential = errors.New("approval credential has expired")
	ErrUnknownKey        = errors.New("approval credential key is unknown")

	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
)

// Claims are the facts the server binds into one approval credential. The
// caller cannot supply any of them: they are resolved from trusted identity
// and the locked task row, then signed here. Version fields bind both the task
// and the contract aggregate so a decision made against an older page cannot
// be replayed after either row changes.
type Claims struct {
	Version            int    `json:"v"`
	KeyID              string `json:"kid"`
	CredentialID       string `json:"cid"`
	TenantID           string `json:"tenant_id"`
	SubjectID          string `json:"subject_id"`
	Role               string `json:"role"`
	OrgID              string `json:"org_id"`
	TaskID             string `json:"task_id"`
	ContractID         string `json:"contract_id"`
	ApprovalRoundID    string `json:"approval_round_id"`
	ContractRevisionID string `json:"contract_revision_id"`
	RevisionNumber     int    `json:"revision_number"`
	SnapshotDigest     string `json:"snapshot_digest"`
	TaskRowVersion     int64  `json:"task_row_version"`
	ContractRowVersion int64  `json:"contract_row_version"`
	AmountLimit        string `json:"amount_limit"`
	Currency           string `json:"currency"`
	IssuedAt           int64  `json:"iat"`
	ExpiresAt          int64  `json:"exp"`
}

// Keyring holds the active signing key and any verification-only predecessors.
// Rotation adds a new active key; old tokens remain verifiable until their
// short TTL elapses and the predecessor is removed.
type Keyring struct {
	activeKeyID string
	keys        map[string][]byte
	ttl         time.Duration
	now         func() time.Time
	random      io.Reader
}

// NewKeyring validates key material and builds a signer. Every key must be at
// least 32 bytes; an empty keyring is not a usable default, so callers must
// explicitly decide to enable approval credentials.
func NewKeyring(activeKeyID string, keys map[string][]byte, ttl time.Duration) (*Keyring, error) {
	activeKeyID = strings.TrimSpace(activeKeyID)
	if activeKeyID == "" {
		return nil, errors.New("approval credential active key id is required")
	}
	if ttl < time.Second || ttl > MaxTTL {
		return nil, fmt.Errorf("approval credential ttl must be within [1s,%s]", MaxTTL)
	}
	copied := make(map[string][]byte, len(keys))
	for id, key := range keys {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, errors.New("approval credential key id is required")
		}
		if len(key) < 32 {
			return nil, fmt.Errorf("approval credential key %q must be at least 32 bytes", id)
		}
		copied[id] = append([]byte(nil), key...)
	}
	if _, ok := copied[activeKeyID]; !ok {
		return nil, errors.New("approval credential active key is missing")
	}
	return &Keyring{
		activeKeyID: activeKeyID, keys: copied, ttl: ttl, now: time.Now, random: rand.Reader,
	}, nil
}

// KeyringFromEnv enables the capability only when CAW_APPROVAL_CREDENTIAL_KEY
// is configured. A missing key is reported as (nil,nil) so the caller can keep
// the feature off; malformed material is an error so the process fails closed.
// CAW_APPROVAL_CREDENTIAL_PREVIOUS_KEYS accepts comma-separated kid:key pairs
// for a bounded verification-only overlap during rotation.
func KeyringFromEnv() (*Keyring, error) {
	secret := os.Getenv("CAW_APPROVAL_CREDENTIAL_KEY")
	if strings.TrimSpace(secret) == "" {
		return nil, nil
	}
	keyID := strings.TrimSpace(os.Getenv("CAW_APPROVAL_CREDENTIAL_KID"))
	if keyID == "" {
		keyID = "internal-v1"
	}
	keys := map[string][]byte{keyID: []byte(secret)}
	for _, pair := range strings.Split(os.Getenv("CAW_APPROVAL_CREDENTIAL_PREVIOUS_KEYS"), ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, errors.New("CAW_APPROVAL_CREDENTIAL_PREVIOUS_KEYS must contain kid:key pairs")
		}
		keys[strings.TrimSpace(parts[0])] = []byte(strings.TrimSpace(parts[1]))
	}
	ttl := DefaultTTL
	if raw := strings.TrimSpace(os.Getenv("CAW_APPROVAL_CREDENTIAL_TTL")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, errors.New("CAW_APPROVAL_CREDENTIAL_TTL must be a duration")
		}
		ttl = parsed
	}
	return NewKeyring(keyID, keys, ttl)
}

// Issue signs one credential for the supplied trusted claims. It overwrites
// the schema version, key id, credential id and validity window; callers cannot
// choose their own nonce or lifetime.
func (k *Keyring) Issue(claims Claims) (string, Claims, error) {
	if k == nil {
		return "", Claims{}, errors.New("approval credential signer is not configured")
	}
	now := k.now().UTC()
	claims.Version = CredentialVersion
	claims.KeyID = k.activeKeyID
	claims.IssuedAt = now.Unix()
	claims.ExpiresAt = now.Add(k.ttl).Unix()
	var err error
	if claims.CredentialID, err = randomID(k.random); err != nil {
		return "", Claims{}, err
	}
	if err := ValidateClaims(claims, now); err != nil {
		return "", Claims{}, err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", Claims{}, err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	signature := sign(k.keys[claims.KeyID], []byte(credentialPrefix+"."+body))
	token := credentialPrefix + "." + body + "." + base64.RawURLEncoding.EncodeToString(signature)
	return token, claims, nil
}

// Verify checks the signature, schema and validity window. It never logs or
// returns the token. Signature failures and unknown keys deliberately share
// ErrInvalidCredential's 403 mapping so a caller cannot probe key ids.
func (k *Keyring) Verify(token string) (Claims, error) {
	if k == nil {
		return Claims{}, ErrInvalidCredential
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != credentialPrefix {
		return Claims{}, ErrInvalidCredential
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidCredential
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrInvalidCredential
	}
	var claims Claims
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil {
		return Claims{}, ErrInvalidCredential
	}
	key, ok := k.keys[claims.KeyID]
	if !ok {
		return Claims{}, ErrUnknownKey
	}
	expected := sign(key, []byte(parts[0]+"."+parts[1]))
	if !hmac.Equal(signature, expected) {
		return Claims{}, ErrInvalidCredential
	}
	now := k.now().UTC()
	if err := ValidateClaims(claims, now); err != nil {
		return Claims{}, err
	}
	return claims, nil
}

// ValidateClaims is the single claim-shape gate used by both Issue and Verify.
// It rejects incomplete identities, malformed digests/money, inverted or
// over-long validity windows and future-issued tokens.
func ValidateClaims(claims Claims, now time.Time) error {
	if claims.Version != CredentialVersion || strings.TrimSpace(claims.KeyID) == "" ||
		strings.TrimSpace(claims.CredentialID) == "" {
		return ErrInvalidCredential
	}
	for _, value := range []string{
		claims.TenantID, claims.SubjectID, claims.Role, claims.OrgID,
		claims.TaskID, claims.ContractID, claims.ApprovalRoundID, claims.ContractRevisionID,
	} {
		if strings.TrimSpace(value) == "" {
			return ErrInvalidCredential
		}
	}
	if claims.RevisionNumber < 1 || claims.TaskRowVersion < 1 || claims.ContractRowVersion < 1 ||
		!digestPattern.MatchString(claims.SnapshotDigest) || !currencyPattern.MatchString(claims.Currency) {
		return ErrInvalidCredential
	}
	if _, err := contractmoney.Parse(contractmoney.MoneySpec, claims.AmountLimit); err != nil {
		return ErrInvalidCredential
	}
	if claims.ExpiresAt <= claims.IssuedAt || claims.ExpiresAt-claims.IssuedAt > int64(MaxTTL/time.Second) {
		return ErrInvalidCredential
	}
	if now.Unix() >= claims.ExpiresAt {
		return ErrExpiredCredential
	}
	if time.Unix(claims.IssuedAt, 0).After(now.Add(ClockSkew)) {
		return ErrInvalidCredential
	}
	return nil
}

func sign(key, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}

func randomID(reader io.Reader) (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(reader, raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
