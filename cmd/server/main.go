// Command server runs the core API service.
//
// In a distributed deployment the process starts with SERVICE_ROLE=core and
// serves only the operations the core role owns, authenticated to the edge
// gateway with GATEWAY_ROLE_TOKEN. Without SERVICE_ROLE it keeps the original
// single-process mode: one process, one database, embedded execution. That mode
// is a development compatibility path only, so a production deployment can
// never silently run one process as every service at once.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"time"

	capabilitypacks "contractaudit/capability-packs"
	"contractaudit/internal/auditext"
	"contractaudit/internal/bootstrap"
	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/clmoutbox"
	"contractaudit/internal/config"
	"contractaudit/internal/externalread"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/outboxrelay"
	"contractaudit/internal/process"
	"contractaudit/internal/store"
	"contractaudit/internal/systemhealth"
)

func main() {
	process.Main(run)
}

func run() error {
	ctx, stop := process.SignalContext()
	defer stop()
	switch role := strings.TrimSpace(os.Getenv("SERVICE_ROLE")); role {
	case "core":
		return serveCore(ctx)
	case "":
		if !developmentCompatibility() {
			return errors.New("SERVICE_ROLE=core is required outside development and test")
		}
		log.Printf("SERVICE_ROLE is unset: running the single-process development compatibility mode")
		return serveLegacy(ctx)
	default:
		return fmt.Errorf("unknown SERVICE_ROLE %q: this executable serves only the core role", role)
	}
}

// developmentCompatibility reports whether the single-process mode is
// explicitly on. An empty APP_ENV means development, matching config.Select.
func developmentCompatibility() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) {
	case "", "development", "dev", "test":
		return true
	default:
		return false
	}
}

func serveLegacy(ctx context.Context) error {
	app, err := bootstrap.Open(ctx, true)
	if err != nil {
		return err
	}
	defer app.Close()
	logStartup(app)
	apiOptions := options(app, origins())
	apiOptions.IntakeUploader = store.NewDirectCaseIntakeUploader(app.Store)
	return listen(ctx, app.Boundary("core", httpapi.Handler(app.Store, apiOptions)))
}

func serveCore(ctx context.Context) error {
	app, err := bootstrap.OpenService(ctx, "core")
	if err != nil {
		return err
	}
	defer app.Close()
	if !app.Durable {
		return errors.New("the core service requires durable execution")
	}
	// The service filter rejects any operation the core role does not own,
	// requires the edge credential, and mounts the unified probe surface.
	apiOptions := options(app, origins())
	apiOptions.PaymentReceiptSources, err = paymentReceiptSourcesFromEnv()
	if err != nil {
		return err
	}
	apiOptions.AssetReceiptSources, err = assetReceiptSourcesFromEnv()
	if err != nil {
		return err
	}
	apiOptions.ContractSourceCredentials, err = contractSourceCredentialsFromEnv()
	if err != nil {
		return err
	}
	apiOptions.IntakeUploader, err = bootstrap.ConfiguredIntakeUploader()
	if err != nil {
		return err
	}
	clmConfig, agedInReviewAfter, err := clmOutboxConfigFromEnv()
	if err != nil {
		return err
	}
	handler, err := httpapi.ServiceHandler(app.Store, apiOptions, "core", strings.TrimSpace(os.Getenv("GATEWAY_ROLE_TOKEN")))
	if err != nil {
		return err
	}
	logStartup(app)
	dialCLMBroker := func(dialCtx context.Context, relayConfig outboxrelay.Config) (clmoutbox.Broker, error) {
		return outboxrelay.Dial(dialCtx, relayConfig)
	}
	relayCtx, stopRelay := context.WithCancel(ctx)
	defer stopRelay()
	clmDone := make(chan error, 1)
	go func() {
		clmDone <- clmoutbox.Serve(relayCtx, app.Store, clmConfig, app.Store, agedInReviewAfter, dialCLMBroker)
	}()
	listenErr := listen(ctx, app.Boundary("core", handler))
	stopRelay()
	relayErr := <-clmDone
	if listenErr != nil {
		return listenErr
	}
	if relayErr != nil && !errors.Is(relayErr, context.Canceled) {
		return fmt.Errorf("CLM outbox service stopped: %w", relayErr)
	}
	return nil
}

func clmOutboxConfigFromEnv() (outboxrelay.Config, *time.Duration, error) {
	config := outboxrelay.Config{
		URL:   strings.TrimSpace(os.Getenv("NATS_URL")),
		Batch: 32, Lease: 30 * time.Second, Poll: 500 * time.Millisecond,
		PublishTimeout: 5 * time.Second, MaxBackoff: time.Minute, Concurrency: 1,
		Stream: clmoutbox.DefaultStream, Subject: clmoutbox.DefaultSubject, Replicas: 1,
	}
	if value := strings.TrimSpace(os.Getenv("CLM_OUTBOX_STREAM")); value != "" {
		if !validCLMStreamName(value) {
			return outboxrelay.Config{}, nil, errors.New("CLM_OUTBOX_STREAM is invalid")
		}
		config.Stream = value
	}
	if value := strings.TrimSpace(os.Getenv("CLM_OUTBOX_SUBJECT")); value != "" {
		if !validCLMSubject(value) {
			return outboxrelay.Config{}, nil, errors.New("CLM_OUTBOX_SUBJECT is invalid")
		}
		config.Subject = value
	}
	if config.Stream == outboxrelay.Stream || config.Subject == outboxrelay.Subject {
		return outboxrelay.Config{}, nil, errors.New("CLM outbox stream and subject must be isolated from Execution")
	}
	if raw := strings.TrimSpace(os.Getenv("CLM_OUTBOX_STREAM_REPLICAS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 5 {
			return outboxrelay.Config{}, nil, errors.New("CLM_OUTBOX_STREAM_REPLICAS must be an integer in 1..5")
		}
		config.Replicas = value
	}
	var agedAfter *time.Duration
	if raw := strings.TrimSpace(os.Getenv("CLM_APPROVAL_AGED_AFTER")); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 || value > 365*24*time.Hour {
			return outboxrelay.Config{}, nil, errors.New("CLM_APPROVAL_AGED_AFTER must be a duration in (0,365 days]")
		}
		agedAfter = &value
	}
	return config, agedAfter, nil
}

func validCLMStreamName(value string) bool {
	if value == "" || len(value) > 127 {
		return false
	}
	for _, c := range value {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func validCLMSubject(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	for _, token := range strings.Split(value, ".") {
		if token == "" {
			return false
		}
		for _, c := range token {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func contractSourceCredentialsFromEnv() ([]httpapi.ContractSourceCredential, error) {
	raw := strings.TrimSpace(os.Getenv("CONTRACT_SOURCE_CREDENTIALS_JSON"))
	if raw == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var sources []httpapi.ContractSourceCredential
	if err := decoder.Decode(&sources); err != nil {
		return nil, errors.New("contract source configuration is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || len(sources) == 0 {
		return nil, errors.New("contract source configuration is invalid")
	}
	return sources, nil
}

func assetReceiptSourcesFromEnv() ([]httpapi.AssetReceiptSourceCredential, error) {
	raw := strings.TrimSpace(os.Getenv("ASSET_RECEIPT_SOURCES_JSON"))
	if raw == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var sources []httpapi.AssetReceiptSourceCredential
	if err := decoder.Decode(&sources); err != nil {
		return nil, errors.New("asset receipt source configuration is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || len(sources) == 0 {
		return nil, errors.New("asset receipt source configuration is invalid")
	}
	return sources, nil
}

func paymentReceiptSourcesFromEnv() ([]httpapi.PaymentReceiptSourceCredential, error) {
	raw := strings.TrimSpace(os.Getenv("PAYMENT_RECEIPT_SOURCES_JSON"))
	if raw == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var sources []httpapi.PaymentReceiptSourceCredential
	if err := decoder.Decode(&sources); err != nil {
		return nil, errors.New("payment receipt source configuration is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("payment receipt source configuration is invalid")
	}
	if len(sources) == 0 {
		return nil, errors.New("payment receipt source configuration is invalid")
	}
	return sources, nil
}

func options(app *bootstrap.Runtime, cors []string) httpapi.Options {
	enabled, registry, connectors, catalog := operatorAuditConfig(app.Selection.Mode)
	return httpapi.Options{
		SystemHealth: systemhealth.FromEnv(app.Store.Ready, app.Selection.Database.URL()),
		CORSOrigins:  cors, Models: app.Models, OIDC: app.OIDC,
		Durable: app.Durable, SecureCookies: app.SecureCookies,
		ExperienceWorkerCredential:   strings.TrimSpace(os.Getenv("WORKER_CORE_TOKEN")),
		DocumentIntakeCredential:     strings.TrimSpace(os.Getenv("CORE_DOCUMENT_TOKEN")),
		ToolClaimAuthorityCredential: strings.TrimSpace(os.Getenv("TOOL_CORE_AUTHORITY_TOKEN")),
		ModelAuthorityCredential:     strings.TrimSpace(os.Getenv("CORE_MODEL_AUTHORITY_TOKEN")),
		OperatorAuditEnabled:         enabled,
		CapabilityPacks:              registry,
		ExternalConnectors:           connectors,
		ExternalConnectorInfo:        catalog,
	}
}

// operatorAuditConfig keeps the extension default-off. Fixture mode is allowed
// only in development/test; production must configure an explicit read-only
// endpoint and object allow-list.
func operatorAuditConfig(mode string) (bool, *capabilitypack.Registry, map[string]auditext.ReadConnector, []httpapi.OperatorConnectorInfo) {
	if !operatorAuditEnabled() {
		return false, nil, nil, nil
	}
	registry, ok := operatorAuditRegistry()
	if !ok {
		return false, nil, nil, nil
	}
	connectors, catalog, ok := operatorAuditEvidence(mode)
	if !ok {
		return false, nil, nil, nil
	}
	return true, registry, connectors, catalog
}

func contractAuditBindingsEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("CONTRACT_AUDIT_BINDINGS_ENABLED"))
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		log.Printf("contract audit bindings disabled: CONTRACT_AUDIT_BINDINGS_ENABLED is not a boolean")
		return false
	}
	return enabled
}

func operatorAuditEnabled() bool {
	rawEnabled := strings.TrimSpace(os.Getenv("OPERATOR_AUDIT_ENABLED"))
	if rawEnabled == "" {
		return false
	}
	enabled, err := strconv.ParseBool(rawEnabled)
	if err != nil {
		log.Printf("operator audit disabled: OPERATOR_AUDIT_ENABLED is not a boolean")
		return false
	}
	return enabled
}

func operatorAuditRegistry() (*capabilitypack.Registry, bool) {
	raws, err := capabilitypacks.Manifests()
	if err != nil {
		log.Printf("operator audit disabled: embedded capability packs: %v", err)
		return nil, false
	}
	registry, err := capabilitypack.NewRegistry(capabilitypack.RegistryConfig{
		AllowedCapabilities: []string{"operator.oss.read_config"},
		AllowedConnectors:   []string{"fixture.oss"},
		AllowedComparators:  []string{"builtin.configured_bandwidth_at_least/v1", "existing.minimum_bandwidth"},
	})
	if err != nil {
		log.Printf("operator audit disabled: capability registry: %v", err)
		return nil, false
	}
	if !registerOperatorAuditManifests(registry, raws) {
		return nil, false
	}
	return registry, true
}

func registerOperatorAuditManifests(registry *capabilitypack.Registry, raws [][]byte) bool {
	for _, raw := range raws {
		manifest, err := capabilitypack.Parse(raw)
		if err != nil {
			log.Printf("operator audit disabled: embedded capability pack: %v", err)
			return false
		}
		if err := registry.Register(manifest); err != nil {
			log.Printf("operator audit disabled: register capability pack: %v", err)
			return false
		}
	}
	return true
}

func operatorAuditEvidence(mode string) (map[string]auditext.ReadConnector, []httpapi.OperatorConnectorInfo, bool) {
	connectors := map[string]auditext.ReadConnector{}
	evidenceMode := strings.ToLower(strings.TrimSpace(os.Getenv("EXTERNAL_EVIDENCE_MODE")))
	if evidenceMode == "" || evidenceMode == "off" {
		return connectors, nil, true
	}
	config, ok := operatorAuditExternalEvidenceConfig(mode, evidenceMode)
	if !ok {
		return nil, nil, false
	}
	connector, err := externalread.New(config)
	if err != nil {
		log.Printf("operator audit disabled: external connector: %v", err)
		return nil, nil, false
	}
	connectors[config.ConnectorID] = connector
	catalog := []httpapi.OperatorConnectorInfo{{ConnectorID: config.ConnectorID, SourceSystem: config.SourceSystem, AllowedObjects: config.AllowedObjects}}
	return connectors, catalog, true
}

func operatorAuditExternalEvidenceConfig(mode, evidenceMode string) (externalread.Config, bool) {
	connectorID := process.Env("EXTERNAL_EVIDENCE_CONNECTOR_ID", "fixture.oss")
	sourceSystem := process.Env("EXTERNAL_EVIDENCE_SOURCE_SYSTEM", "fixture-oss-east")
	baseURL := strings.TrimSpace(os.Getenv("EXTERNAL_EVIDENCE_BASE_URL"))
	allowed := splitList(os.Getenv("EXTERNAL_EVIDENCE_ALLOWED_OBJECTS"))
	payloadPrefix := process.Env("EXTERNAL_EVIDENCE_PAYLOAD_PREFIX", "fixture://oss/")
	switch evidenceMode {
	case "fixture":
		if mode != config.ModeDevelopment && mode != config.ModeTest {
			log.Printf("operator audit disabled: fixture external evidence is not allowed in %q", mode)
			return externalread.Config{}, false
		}
		if len(allowed) == 0 {
			allowed = []string{"fixture-line-001"}
		}
		path := process.Env("EXTERNAL_EVIDENCE_FIXTURE_FILE", "testdata/operatoraudit/raw/oss-bandwidth.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			log.Printf("operator audit disabled: fixture file: %v", err)
			return externalread.Config{}, false
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
		}))
		baseURL = server.URL
	case "live":
		// A real operator system needs a separate data-processing approval and a
		// non-synthetic observation contract. This release only accepts the
		// explicitly synthetic fixture path.
		log.Printf("operator audit disabled: live external evidence requires a separate approved data path")
		return externalread.Config{}, false
	default:
		log.Printf("operator audit disabled: unknown EXTERNAL_EVIDENCE_MODE %q", evidenceMode)
		return externalread.Config{}, false
	}
	return externalread.Config{
		ConnectorID: connectorID, BaseURL: baseURL, SourceSystem: sourceSystem,
		AllowedObjects: allowed, PayloadPrefix: payloadPrefix,
		Freshness: 0, Timeout: 5 * time.Second, MaxResponseBytes: 1 << 20,
	}, true
}

func splitList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// origins applies the deployment's browser origin list and environment defaults.
func origins() []string {
	return config.BrowserOrigins(os.Getenv("CORS_ORIGIN"), os.Getenv("APP_ENV"))
}

func logStartup(app *bootstrap.Runtime) {
	selection := app.Selection
	host, _ := os.Hostname()
	log.Printf("instance %s pid %d", host, os.Getpid())
	log.Printf("database %s at %s:%s (%s)", selection.Database.Name, selection.Database.Host, selection.Database.Port, selection.Mode)
	log.Printf("review model %s, ocr model %s", app.Models.Name(), app.Models.OCRName())
}

func listen(ctx context.Context, handler http.Handler) error {
	addr := process.Env("ADDR", "127.0.0.1:8080")
	log.Printf("listening on %s", addr)
	server := &http.Server{
		Addr: addr, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		// SSE is a long-lived response. A global WriteTimeout would terminate
		// valid streams; a reverse proxy should enforce its own client limits.
	}
	return process.Serve(ctx, server, 10*time.Second)
}
