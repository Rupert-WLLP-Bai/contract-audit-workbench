// Package bootstrap assembles shared API, worker, and tool-gateway dependencies.
package bootstrap

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/config"
	"contractaudit/internal/identity"
	"contractaudit/internal/model"
	"contractaudit/internal/objectstore"
	"contractaudit/internal/process"
	"contractaudit/internal/store"
	"contractaudit/internal/telemetry"
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

	stopModelWatch context.CancelFunc
	modelWatchDone <-chan struct{}
}

// Boundary wraps a process HTTP surface with the uniform HTTP boundary: the
// bounded request identity, W3C trace context, panic recovery, structured
// access log, and RED metrics. It reuses the exporter the Store already owns
// instead of building a second one, and works unchanged when telemetry is
// disabled because the boundary keeps every behavior except metric export.
func (r *Runtime) Boundary(service string, next http.Handler) http.Handler {
	return telemetry.HTTPBoundary(r.Store.Telemetry(), service, telemetry.HTTPOptions{}, next)
}

// Open validates process configuration and initializes all shared dependencies
// before a caller starts accepting traffic. Optional OIDC credentials are read
// only when includeOIDC is true and OIDC_ISSUER is configured. Production
// refuses to start without a loaded enterprise identity directory and, for
// identity-serving processes, a configured OIDC issuer; development and test
// keep the demonstration identities.
func Open(ctx context.Context, includeOIDC bool) (*Runtime, error) {
	return openConfigured(ctx, includeOIDC, "")
}

type runtimeSettings struct {
	selection          config.Selection
	databaseMode       string
	executionMode      string
	secureCookies      bool
	readOnly           bool
	objects            *objectstore.S3
	modelConfig        model.Config
	experienceAcceptor store.ExperienceResultAcceptor
	oidcConfig         identity.Config
	oidcConfigured     bool
}

func openConfigured(ctx context.Context, includeOIDC bool, service string) (*Runtime, error) {
	if err := startupContextReady(ctx); err != nil {
		return nil, err
	}

	settings, err := loadRuntimeSettings(includeOIDC, service)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	st, err := openRuntimeStore(settings)
	if err != nil {
		return nil, err
	}
	closeOnFailure := func(err error) (*Runtime, error) {
		_ = st.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return closeOnFailure(err)
	}
	models, revision, err := wireRuntimeStore(ctx, st, settings, service)
	if err != nil {
		return closeOnFailure(err)
	}
	oidc, err := openRuntimeOIDC(settings, st)
	if err != nil {
		return closeOnFailure(err)
	}
	if err := ctx.Err(); err != nil {
		return closeOnFailure(err)
	}
	// The watcher starts only after every startup step succeeded, so a failed
	// startup cannot leave a goroutine polling against a closed store.
	modelWatchStop, modelWatchDone := watchConfiguredLLMEndpoint(ctx, st, settings, service, revision)
	return &Runtime{
		Store: st, Models: models, OIDC: oidc, Selection: settings.selection,
		Durable: settings.executionMode == executionModeDurable, SecureCookies: settings.secureCookies,
		stopModelWatch: modelWatchStop, modelWatchDone: modelWatchDone,
	}, nil
}

func startupContextReady(ctx context.Context) error {
	if ctx == nil {
		return errors.New("startup context is required")
	}
	return ctx.Err()
}

func openRuntimeOIDC(settings runtimeSettings, st *store.Store) (*identity.Manager, error) {
	if !settings.oidcConfigured {
		return nil, nil
	}
	return identity.NewManager(settings.oidcConfig, accounts.BySubject, st)
}

type runtimeModes struct {
	selection config.Selection
	database  string
	execution string
}

func loadRuntimeSettings(includeOIDC bool, service string) (runtimeSettings, error) {
	// The directory must load before the production environment gate below:
	// ValidateEnvironment requires this process's loaded identity source.
	identityFile := strings.TrimSpace(os.Getenv("IDENTITY_DIRECTORY_FILE"))
	if err := accounts.ConfigureDirectory(identityFile); err != nil {
		return runtimeSettings{}, fmt.Errorf("trusted identity directory configuration failed: %w", err)
	}
	modes, err := loadRuntimeModes(service)
	if err != nil {
		return runtimeSettings{}, err
	}
	secureCookies, readOnly, err := loadRuntimeToggles(modes.selection)
	if err != nil {
		return runtimeSettings{}, err
	}
	dependencies, err := loadRuntimeDependencies(includeOIDC, service, identityFile, modes.selection.Mode)
	if err != nil {
		return runtimeSettings{}, err
	}
	return runtimeSettings{
		selection: modes.selection, databaseMode: modes.database, executionMode: modes.execution,
		secureCookies: secureCookies, readOnly: readOnly,
		objects: dependencies.objects, modelConfig: dependencies.modelConfig,
		experienceAcceptor: dependencies.experienceAcceptor,
		oidcConfig:         dependencies.oidcConfig, oidcConfigured: dependencies.oidcConfigured,
	}, nil
}

func loadRuntimeModes(service string) (runtimeModes, error) {
	selection, err := serviceSelection(service)
	if err != nil {
		return runtimeModes{}, err
	}
	databaseMode, err := selectedDatabaseMode(selection.Mode)
	if err != nil {
		return runtimeModes{}, err
	}
	executionMode, err := selectedExecutionMode(selection.Mode)
	if err != nil {
		return runtimeModes{}, err
	}
	if service != "" {
		databaseMode = databaseModeApplication
		executionMode = executionModeDurable
	}
	return runtimeModes{selection: selection, database: databaseMode, execution: executionMode}, nil
}

func loadRuntimeToggles(selection config.Selection) (bool, bool, error) {
	secureCookies, err := readBoolean("COOKIE_SECURE", false)
	if err != nil {
		return false, false, err
	}
	readOnly, err := readBoolean("APP_READ_ONLY", false)
	if err != nil {
		return false, false, errors.New("APP_READ_ONLY must be true or false")
	}
	if selection.Mode == config.ModeProduction && !secureCookies {
		return false, false, errors.New("COOKIE_SECURE must be true in production")
	}
	if err := accounts.ValidateEnvironment(selection.Mode); err != nil {
		return false, false, err
	}
	return secureCookies, readOnly, nil
}

type runtimeDependencies struct {
	objects            *objectstore.S3
	modelConfig        model.Config
	experienceAcceptor store.ExperienceResultAcceptor
	oidcConfig         identity.Config
	oidcConfigured     bool
}

func loadRuntimeDependencies(includeOIDC bool, service, identityFile, mode string) (runtimeDependencies, error) {
	objects, err := configuredObjectsForService(service)
	if err != nil {
		return runtimeDependencies{}, err
	}
	modelConfig, err := serviceModelConfig(service)
	if err != nil {
		return runtimeDependencies{}, err
	}
	experienceAcceptor, err := configuredExperienceAcceptorForService(service)
	if err != nil {
		return runtimeDependencies{}, err
	}
	oidcConfig, oidcConfigured, err := configuredOIDCForService(includeOIDC, identityFile, mode)
	if err != nil {
		return runtimeDependencies{}, err
	}
	return runtimeDependencies{
		objects: objects, modelConfig: modelConfig, experienceAcceptor: experienceAcceptor,
		oidcConfig: oidcConfig, oidcConfigured: oidcConfigured,
	}, nil
}

func configuredObjectsForService(service string) (*objectstore.S3, error) {
	if service == "" || service == "document" {
		return configuredObjectStore()
	}
	return nil, nil
}

func configuredExperienceAcceptorForService(service string) (store.ExperienceResultAcceptor, error) {
	if service == "worker" {
		return configuredExperienceResultAcceptor()
	}
	return nil, nil
}

func configuredOIDCForService(includeOIDC bool, identityFile, mode string) (identity.Config, bool, error) {
	if !includeOIDC {
		return identity.Config{}, false, nil
	}
	oidcConfig, oidcConfigured, err := configuredOIDC()
	if err != nil {
		return identity.Config{}, false, err
	}
	if oidcConfigured && identityFile == "" {
		return identity.Config{}, false, errors.New("IDENTITY_DIRECTORY_FILE is required when OIDC_ISSUER is configured")
	}
	// An identity-serving process without a configured provider would leave the
	// fixed demonstration passwords as the only way to sign in, which production
	// never allows.
	if mode == config.ModeProduction && !oidcConfigured {
		return identity.Config{}, false, errors.New("OIDC_ISSUER is required in production")
	}
	return oidcConfig, oidcConfigured, nil
}

func openRuntimeStore(settings runtimeSettings) (*store.Store, error) {
	dataDir := process.Env("DATA_DIR", "var/lib")
	var st *store.Store
	var err error
	if settings.databaseMode == databaseModeApplication {
		st, err = store.OpenApplicationSelection(dataDir, settings.selection)
	} else {
		st, err = store.Open(dataDir)
	}
	if err != nil {
		if st != nil {
			_ = st.Close()
		}
		return nil, err
	}
	return st, nil
}

func wireRuntimeStore(ctx context.Context, st *store.Store, settings runtimeSettings, service string) (*model.Client, int64, error) {
	st.UseReadOnly(settings.readOnly)
	if err := applyStorePolicy(ctx, st, service); err != nil {
		return nil, 0, err
	}
	exporter, err := configuredTelemetry(service)
	if err != nil {
		return nil, 0, err
	}
	st.UseTelemetry(exporter)
	if settings.objects != nil {
		st.UseObjectStore(settings.objects)
	}
	// The stored endpoint overrides the deployment identity before the client
	// is built, so the configuration id a review freezes is the configured one.
	config, revision, storedDirect := configuredLLMEndpoint(ctx, st, settings, service)
	models := newModelClient(config, storedDirect)
	st.UseModels(models)
	if provider := configuredAdvisoryProvider(service, models); provider != nil {
		st.UseAdvisory(provider)
	}
	if service == "worker" {
		if err := wireWorkerDependencies(st, settings.experienceAcceptor); err != nil {
			return nil, 0, err
		}
	}
	return models, revision, nil
}

// configuredLLMEndpoint merges the platform endpoint with the deployment
// identity. Read-only deployments keep the deployment configuration. The
// boolean reports that a stored endpoint was adopted for direct calls.
func configuredLLMEndpoint(ctx context.Context, st llmEndpointStore, settings runtimeSettings, service string) (model.Config, int64, bool) {
	if settings.readOnly {
		return settings.modelConfig, 0, false
	}
	return loadStoredLLMEndpoint(ctx, st, service, settings.modelConfig)
}

// watchConfiguredLLMEndpoint applies later endpoint revisions. Read-only
// deployments and stores without a database are never polled.
func watchConfiguredLLMEndpoint(ctx context.Context, st llmEndpointStore, settings runtimeSettings, service string, revision int64) (context.CancelFunc, <-chan struct{}) {
	if settings.readOnly || !st.HasDatabase() {
		return nil, nil
	}
	return watchStoredLLMEndpoint(ctx, st, service, settings.modelConfig, revision)
}

func applyStorePolicy(ctx context.Context, st *store.Store, service string) error {
	if err := applyQueuePolicy(st, service); err != nil {
		return err
	}
	return applyServiceIdentity(ctx, st, service)
}

func applyQueuePolicy(st *store.Store, service string) error {
	if service != "core" && service != "execution" && service != "worker" && service != "" {
		return nil
	}
	policy, err := configuredQueuePolicy()
	if err != nil {
		return err
	}
	return st.UseQueuePolicy(policy)
}

func applyServiceIdentity(ctx context.Context, st *store.Store, service string) error {
	if service == "" {
		return nil
	}
	if err := st.UseServiceOwner(service); err != nil {
		return err
	}
	return st.ValidateServiceDatabaseRole(ctx, service)
}

func wireWorkerDependencies(st *store.Store, experienceAcceptor store.ExperienceResultAcceptor) error {
	preparer, err := configuredDocumentPreparer()
	if err != nil {
		return err
	}
	st.UseDocumentPreparer(preparer)
	reader, err := configuredToolEvidenceReader()
	if err != nil {
		return err
	}
	if err := st.UseToolEvidenceReader(reader); err != nil {
		return err
	}
	if experienceAcceptor != nil {
		st.UseExperienceResultAcceptor(experienceAcceptor)
	}
	return nil
}

// Close releases the store owned by this runtime. A nil runtime is safe to close.
func (r *Runtime) Close() error {
	if r == nil || r.Store == nil {
		return nil
	}
	if r.stopModelWatch != nil {
		r.stopModelWatch()
	}
	if r.modelWatchDone != nil {
		<-r.modelWatchDone
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
	endpoint := process.Env("S3_ENDPOINT", "")
	if endpoint == "" {
		return nil, nil
	}
	bucket := process.Env("S3_BUCKET", "")
	accessKey := firstProcessValue("S3_ACCESS_KEY", "RUSTFS_ACCESS_KEY")
	secretKey := firstProcessValue("S3_SECRET_KEY", "RUSTFS_SECRET_KEY")
	if bucket == "" || accessKey == "" || secretKey == "" {
		return nil, errors.New("S3_BUCKET and S3 access and secret keys are required when S3_ENDPOINT is configured")
	}
	region := process.Env("S3_REGION", "us-east-1")
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
	issuer := process.Env("OIDC_ISSUER", "")
	if issuer == "" {
		return identity.Config{}, false, nil
	}
	stateKey, err := decodeStateKey(process.Env("OIDC_STATE_KEY", ""))
	if err != nil {
		return identity.Config{}, false, err
	}
	config := identity.Config{
		Issuer:             issuer,
		ClientID:           process.Env("OIDC_CLIENT_ID", ""),
		ClientSecret:       os.Getenv("OIDC_CLIENT_SECRET"),
		AdminClientID:      process.Env("OIDC_ADMIN_CLIENT_ID", ""),
		AdminClientSecret:  os.Getenv("OIDC_ADMIN_CLIENT_SECRET"),
		RedirectURL:        process.Env("OIDC_REDIRECT_URL", ""),
		AdminRedirectURL:   process.Env("OIDC_ADMIN_REDIRECT_URL", ""),
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
	if value := process.Env(primary, ""); value != "" {
		return value
	}
	return process.Env(legacy, "")
}
