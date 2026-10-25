// Package bootstrap assembles shared API, worker, and tool-gateway dependencies.
package bootstrap

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/config"
	"contractaudit/internal/identity"
	"contractaudit/internal/model"
	"contractaudit/internal/objectstore"
	"contractaudit/internal/store"
)

const (
	databaseModeEmbedded    = "embedded"
	databaseModeApplication = "application"
	executionModeEmbedded   = "embedded"
	executionModeDurable    = "durable"
)

// Runtime is the complete set of shared dependencies needed by a process.
type Runtime struct {
	Store         *store.Store
	Models        *model.Client
	OIDC          *identity.Manager
	Selection     config.Selection
	Durable       bool
	SecureCookies bool
}

// Open validates process configuration and initializes all shared dependencies
// before a caller starts accepting traffic. Optional OIDC credentials are read
// only when includeOIDC is true and OIDC_ISSUER is configured.
func Open(ctx context.Context, includeOIDC bool) (*Runtime, error) {
	return openConfigured(ctx, includeOIDC, "")
}

func openConfigured(ctx context.Context, includeOIDC bool, service string) (*Runtime, error) {
	if ctx == nil {
		return nil, errors.New("startup context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	identityFile := strings.TrimSpace(os.Getenv("IDENTITY_DIRECTORY_FILE"))
	if err := accounts.ConfigureDirectory(identityFile); err != nil {
		return nil, errors.New("trusted identity directory configuration failed")
	}
	selection, err := serviceSelection(service)
	if err != nil {
		return nil, err
	}
	databaseMode, err := selectedDatabaseMode(selection.Mode)
	if err != nil {
		return nil, err
	}
	executionMode, err := selectedExecutionMode(selection.Mode)
	if err != nil {
		return nil, err
	}
	if service != "" {
		databaseMode = databaseModeApplication
		executionMode = executionModeDurable
	}
	secureCookies, err := readBoolean("COOKIE_SECURE", false)
	if err != nil {
		return nil, err
	}
	readOnly, err := readBoolean("APP_READ_ONLY", false)
	if err != nil {
		return nil, errors.New("APP_READ_ONLY must be true or false")
	}
	if selection.Mode == config.ModeProduction && !secureCookies {
		return nil, errors.New("COOKIE_SECURE must be true in production")
	}
	if err := accounts.ValidateEnvironment(selection.Mode); err != nil {
		return nil, err
	}

	var configuredObjects *objectstore.S3
	if service == "" || service == "document" {
		configuredObjects, err = configuredObjectStore()
		if err != nil {
			return nil, err
		}
	}
	modelConfig, err := serviceModelConfig(service)
	if err != nil {
		return nil, err
	}
	var oidcConfig identity.Config
	var oidcConfigured bool
	if includeOIDC {
		oidcConfig, oidcConfigured, err = configuredOIDC()
		if err != nil {
			return nil, err
		}
		if oidcConfigured && identityFile == "" {
			return nil, errors.New("IDENTITY_DIRECTORY_FILE is required when OIDC_ISSUER is configured")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	dataDir := processValue("DATA_DIR", "var/lib")
	var st *store.Store
	if databaseMode == databaseModeApplication {
		st, err = store.OpenApplicationSelection(dataDir, selection)
	} else {
		st, err = store.Open(dataDir)
	}
	if err != nil {
		if st != nil {
			_ = st.Close()
		}
		return nil, err
	}
	closeOnFailure := func(err error) (*Runtime, error) {
		_ = st.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return closeOnFailure(err)
	}
	st.UseReadOnly(readOnly)
	if service == "execution" || service == "worker" || service == "" {
		policy, err := configuredQueuePolicy()
		if err != nil {
			return closeOnFailure(err)
		}
		if err := st.UseQueuePolicy(policy); err != nil {
			return closeOnFailure(err)
		}
	}
	if service != "" {
		if err := st.UseServiceOwner(service); err != nil {
			return closeOnFailure(err)
		}
		if err := st.ValidateServiceDatabaseRole(ctx, service); err != nil {
			return closeOnFailure(err)
		}
	}
	exporter, err := configuredTelemetry(service)
	if err != nil {
		return closeOnFailure(err)
	}
	st.UseTelemetry(exporter)
	if configuredObjects != nil {
		st.UseObjectStore(configuredObjects)
	}
	models := model.New(modelConfig)
	st.UseModels(models)
	if service == "worker" {
		preparer, err := configuredDocumentPreparer()
		if err != nil {
			return closeOnFailure(err)
		}
		st.UseDocumentPreparer(preparer)
		reader, err := configuredToolEvidenceReader()
		if err != nil {
			return closeOnFailure(err)
		}
		if err := st.UseToolEvidenceReader(reader); err != nil {
			return closeOnFailure(err)
		}
	}

	var oidc *identity.Manager
	if oidcConfigured {
		oidc, err = identity.NewManager(oidcConfig, accounts.BySubject, st)
		if err != nil {
			return closeOnFailure(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return closeOnFailure(err)
	}
	return &Runtime{
		Store: st, Models: models, OIDC: oidc, Selection: selection,
		Durable: executionMode == executionModeDurable, SecureCookies: secureCookies,
	}, nil
}

// Close releases the store owned by this runtime. A nil runtime is safe to close.
func (r *Runtime) Close() error {
	if r == nil || r.Store == nil {
		return nil
	}
	return r.Store.Close()
}

func selectedDatabaseMode(appEnvironment string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("APP_DATABASE_MODE")))
	if value == "" {
		if appEnvironment == config.ModeProduction {
			value = databaseModeApplication
		} else {
			value = databaseModeEmbedded
		}
	}
	switch value {
	case databaseModeEmbedded:
		if appEnvironment == config.ModeProduction {
			return "", errors.New("APP_DATABASE_MODE=embedded is not allowed in production")
		}
		return value, nil
	case databaseModeApplication:
		return value, nil
	default:
		return "", errors.New("APP_DATABASE_MODE must be embedded or application")
	}
}

func selectedExecutionMode(appEnvironment string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("EXECUTION_MODE")))
	if value == "" {
		if appEnvironment == config.ModeProduction {
			value = executionModeDurable
		} else {
			value = executionModeEmbedded
		}
	}
	switch value {
	case executionModeEmbedded:
		if appEnvironment == config.ModeProduction {
			return "", errors.New("EXECUTION_MODE=embedded is not allowed in production")
		}
		return value, nil
	case executionModeDurable:
		return value, nil
	default:
		return "", errors.New("EXECUTION_MODE must be embedded or durable")
	}
}

func readBoolean(name string, fallback bool) (bool, error) {
	switch strings.TrimSpace(os.Getenv(name)) {
	case "":
		return fallback, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New(name + " must be true or false")
	}
}

func configuredObjectStore() (*objectstore.S3, error) {
	endpoint := processValue("S3_ENDPOINT", "")
	if endpoint == "" {
		return nil, nil
	}
	bucket := processValue("S3_BUCKET", "")
	accessKey := firstProcessValue("S3_ACCESS_KEY", "RUSTFS_ACCESS_KEY")
	secretKey := firstProcessValue("S3_SECRET_KEY", "RUSTFS_SECRET_KEY")
	if bucket == "" || accessKey == "" || secretKey == "" {
		return nil, errors.New("S3_BUCKET and S3 access and secret keys are required when S3_ENDPOINT is configured")
	}
	region := processValue("S3_REGION", "us-east-1")
	client, err := objectstore.New(objectstore.Config{
		Endpoint: endpoint, Bucket: bucket, Region: region,
		AccessKey: accessKey, SecretKey: secretKey,
	})
	if err != nil {
		return nil, errors.New("S3 configuration is invalid")
	}
	return client, nil
}

func configuredOIDC() (identity.Config, bool, error) {
	issuer := processValue("OIDC_ISSUER", "")
	if issuer == "" {
		return identity.Config{}, false, nil
	}
	stateKey, err := decodeStateKey(processValue("OIDC_STATE_KEY", ""))
	if err != nil {
		return identity.Config{}, false, err
	}
	config := identity.Config{
		Issuer:             issuer,
		ClientID:           processValue("OIDC_CLIENT_ID", ""),
		ClientSecret:       os.Getenv("OIDC_CLIENT_SECRET"),
		AdminClientID:      processValue("OIDC_ADMIN_CLIENT_ID", ""),
		AdminClientSecret:  os.Getenv("OIDC_ADMIN_CLIENT_SECRET"),
		RedirectURL:        processValue("OIDC_REDIRECT_URL", ""),
		AdminRedirectURL:   processValue("OIDC_ADMIN_REDIRECT_URL", ""),
		StateEncryptionKey: stateKey,
	}
	if strings.TrimSpace(config.ClientSecret) == "" || strings.TrimSpace(config.AdminClientSecret) == "" {
		return identity.Config{}, false, errors.New("OIDC client and admin credentials are required when OIDC_ISSUER is configured")
	}
	return config, true, nil
}

func decodeStateKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("OIDC_STATE_KEY must be base64 encoding of exactly 32 bytes")
	}
	return key, nil
}

func firstProcessValue(primary, legacy string) string {
	if value := processValue(primary, ""); value != "" {
		return value
	}
	return processValue(legacy, "")
}

func processValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
