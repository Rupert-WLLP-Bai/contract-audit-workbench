package approval_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/approval"
)

const testKey = "approval-test-key-0123456789abcdef"

func claimsFixture(now time.Time) approval.Claims {
	return approval.Claims{
		TenantID:           "local-demo",
		SubjectID:          "demo-reviewer",
		Role:               "reviewer",
		OrgID:              "demo-org",
		TaskID:             "8b0f57a2-9a4c-4db1-b4bb-1f74f6f52f01",
		ContractID:         "0a9a0dd1-98f7-4b0a-a7db-a7b4b821a001",
		ApprovalRoundID:    "4a6e9d43-6f4f-4a90-9b88-3b4a8d5db102",
		ContractRevisionID: "f6c2e8c8-0f8b-4f3e-a1d4-9f6f2a0e2103",
		RevisionNumber:     2,
		SnapshotDigest:     strings.Repeat("a", 64),
		TaskRowVersion:     3,
		ContractRowVersion: 7,
		AmountLimit:        "100000.00",
		Currency:           "CNY",
	}
}

func TestIssueAndVerifyBindsIdentityTimeRoleVersionAndDigest(t *testing.T) {
	keyring, err := approval.NewKeyring("k1", map[string][]byte{"k1": []byte(testKey)}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	token, issued, err := keyring.Issue(claimsFixture(now))
	if err != nil {
		t.Fatal(err)
	}
	if issued.SubjectID != "demo-reviewer" || issued.TaskID == "" || issued.SnapshotDigest != strings.Repeat("a", 64) {
		t.Fatalf("issued claims lost identity binding: %+v", issued)
	}
	if !strings.HasPrefix(token, "caw-approval-credential-v1.") || strings.Count(token, ".") != 2 {
		t.Fatalf("unexpected credential envelope: %.40s", token)
	}
	verified, err := keyring.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if verified.TaskID != issued.TaskID || verified.ContractRevisionID != issued.ContractRevisionID ||
		verified.SnapshotDigest != issued.SnapshotDigest || verified.TaskRowVersion != issued.TaskRowVersion ||
		verified.ContractRowVersion != issued.ContractRowVersion || verified.AmountLimit != "100000.00" {
		t.Fatalf("verified claims changed: %+v", verified)
	}
	if verified.ExpiresAt <= verified.IssuedAt || time.Unix(verified.ExpiresAt, 0).Sub(time.Unix(verified.IssuedAt, 0)) != time.Minute {
		t.Fatalf("validity window is not the configured TTL: %+v", verified)
	}
}

func TestVerifyRejectsPayloadTamperingAndForeignKeys(t *testing.T) {
	keyring, err := approval.NewKeyring("k1", map[string][]byte{"k1": []byte(testKey)}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := keyring.Issue(claimsFixture(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	claims["snapshot_digest"] = strings.Repeat("b", 64)
	altered, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(altered) + "." + parts[2]
	if _, err := keyring.Verify(tampered); !errors.Is(err, approval.ErrInvalidCredential) {
		t.Fatalf("tampered payload accepted: %v", err)
	}

	other, err := approval.NewKeyring("k2", map[string][]byte{"k2": []byte(strings.Repeat("z", 40))}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Verify(token); !errors.Is(err, approval.ErrUnknownKey) {
		t.Fatalf("foreign key id accepted: %v", err)
	}
	foreign, _, err := other.Issue(claimsFixture(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.Verify(foreign); !errors.Is(err, approval.ErrUnknownKey) {
		t.Fatalf("token from another keyring accepted: %v", err)
	}
	if _, err := keyring.Verify("not-a-token"); !errors.Is(err, approval.ErrInvalidCredential) {
		t.Fatalf("malformed token accepted: %v", err)
	}
}

func TestValidateClaimsRejectsIncompleteOrOverlongBindings(t *testing.T) {
	now := time.Now().UTC()
	base := claimsFixture(now)
	base.Version = approval.CredentialVersion
	base.KeyID = "k1"
	base.CredentialID = "credential-nonce-1"
	base.IssuedAt = now.Unix()
	base.ExpiresAt = now.Add(time.Minute).Unix()
	cases := map[string]func(approval.Claims) approval.Claims{
		"missing subject": func(c approval.Claims) approval.Claims { c.SubjectID = ""; return c },
		"missing task":    func(c approval.Claims) approval.Claims { c.TaskID = ""; return c },
		"bad digest":      func(c approval.Claims) approval.Claims { c.SnapshotDigest = "not-a-digest"; return c },
		"bad currency":    func(c approval.Claims) approval.Claims { c.Currency = "cny"; return c },
		"bad amount":      func(c approval.Claims) approval.Claims { c.AmountLimit = "1e6"; return c },
		"zero version":    func(c approval.Claims) approval.Claims { c.TaskRowVersion = 0; return c },
		"overlong ttl": func(c approval.Claims) approval.Claims {
			c.ExpiresAt = c.IssuedAt + int64(approval.MaxTTL/time.Second) + 1
			return c
		},
		"future issued": func(c approval.Claims) approval.Claims {
			c.IssuedAt = now.Add(time.Hour).Unix()
			c.ExpiresAt = c.IssuedAt + 60
			return c
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if err := approval.ValidateClaims(mutate(base), now); err == nil {
				t.Fatal("invalid claims were accepted")
			}
		})
	}
	expired := base
	expired.IssuedAt = now.Add(-10 * time.Minute).Unix()
	expired.ExpiresAt = now.Add(-5 * time.Minute).Unix()
	if err := approval.ValidateClaims(expired, now); !errors.Is(err, approval.ErrExpiredCredential) {
		t.Fatalf("expired claims error = %v", err)
	}
	if err := approval.ValidateClaims(base, now); err != nil {
		t.Fatalf("valid claims rejected: %v", err)
	}
}

func TestKeyringFromEnvIsOffByDefaultAndFailsClosed(t *testing.T) {
	t.Setenv("CAW_APPROVAL_CREDENTIAL_KEY", "")
	t.Setenv("CAW_APPROVAL_CREDENTIAL_KID", "")
	t.Setenv("CAW_APPROVAL_CREDENTIAL_TTL", "")
	keyring, err := approval.KeyringFromEnv()
	if err != nil || keyring != nil {
		t.Fatalf("unset key should leave the capability off: %v %v", keyring, err)
	}
	t.Setenv("CAW_APPROVAL_CREDENTIAL_KEY", "short")
	if _, err := approval.KeyringFromEnv(); err == nil {
		t.Fatal("short key material must fail closed")
	}
	t.Setenv("CAW_APPROVAL_CREDENTIAL_KEY", testKey)
	t.Setenv("CAW_APPROVAL_CREDENTIAL_KID", "internal-v1")
	keyring, err = approval.KeyringFromEnv()
	if err != nil || keyring == nil {
		t.Fatalf("valid environment did not enable the keyring: %v", err)
	}
}
