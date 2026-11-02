package evidencebundle

import (
	"crypto/ed25519"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validTenant(tenant string) bool {
	if tenant == "" || len(tenant) > 1024 || !utf8.ValidString(tenant) || strings.TrimSpace(tenant) != tenant {
		return false
	}
	for _, r := range tenant {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validTenantSchema(schema, tenant string) bool {
	switch schema {
	case SchemaV1:
		return tenant == ""
	case SchemaV2:
		return validTenant(tenant)
	default:
		return false
	}
}

// VerifyForTenant adds an exact signed-tenant assertion to ordinary trusted-key
// verification. A v1 bundle has unknown tenant and cannot satisfy this assertion.
// This offline assertion neither grants access nor revokes downloaded copies.
func VerifyForTenant(reader io.ReaderAt, size int64, trusted ed25519.PublicKey, fingerprint, expectedTenant string) (Manifest, error) {
	if !validTenant(expectedTenant) {
		return Manifest{}, errors.New("invalid expected tenant")
	}
	manifest, err := Verify(reader, size, trusted, fingerprint)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Schema != SchemaV2 {
		return Manifest{}, errors.New("legacy evidence tenant is unknown")
	}
	if manifest.Identity.TenantID != expectedTenant {
		return Manifest{}, errors.New("evidence tenant does not match expected tenant")
	}
	return manifest, nil
}
