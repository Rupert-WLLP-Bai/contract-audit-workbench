package bootstrap

import "testing"

const (
	envFallbackTestAccessKey = "AKIDEXAMPLE"
	envFallbackTestSecret    = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
)

func TestConfiguredObjectStorePrefersPrimaryCredentialsOverLegacyFallback(t *testing.T) {
	for _, test := range []struct {
		name          string
		primaryAccess string
		legacyAccess  string
		primarySecret string
		legacySecret  string
	}{
		{
			name:          "configured primary credentials win",
			primaryAccess: envFallbackTestAccessKey,
			primarySecret: envFallbackTestSecret,
		},
		{
			name:         "legacy credentials are used when primary is blank",
			legacyAccess: envFallbackTestAccessKey,
			legacySecret: envFallbackTestSecret,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for key, value := range map[string]string{
				"S3_ENDPOINT":       "https://objects.example.invalid",
				"S3_BUCKET":         "private-bucket",
				"S3_REGION":         "us-east-1",
				"S3_ACCESS_KEY":     test.primaryAccess,
				"S3_SECRET_KEY":     test.primarySecret,
				"RUSTFS_ACCESS_KEY": test.legacyAccess,
				"RUSTFS_SECRET_KEY": test.legacySecret,
			} {
				t.Setenv(key, value)
			}

			objectStore, err := configuredObjectStore()
			if err != nil || objectStore == nil {
				t.Fatalf("configuredObjectStore() = (%v, %v), want a valid store", objectStore, err)
			}
		})
	}
}
