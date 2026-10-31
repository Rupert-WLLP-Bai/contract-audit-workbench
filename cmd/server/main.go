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
	"errors"
	"fmt"
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
	"contractaudit/internal/config"
	"contractaudit/internal/externalread"
	"contractaudit/internal/httpapi"
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
	return listen(ctx, httpapi.Handler(app.Store, apiOptions))
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
	apiOptions.IntakeUploader, err = bootstrap.ConfiguredIntakeUploader()
	if err != nil {
		return err
	}
	handler, err := httpapi.ServiceHandler(app.Store, apiOptions, "core", strings.TrimSpace(os.Getenv("GATEWAY_ROLE_TOKEN")))
	if err != nil {
		return err
	}
	logStartup(app)
	return listen(ctx, handler)
}

func options(app *bootstrap.Runtime, cors []string) httpapi.Options {
	enabled, registry, connectors, catalog := operatorAuditConfig(app.Selection.Mode)
	return httpapi.Options{
		SystemHealth: systemhealth.FromEnv(app.Store.Ready, app.Selection.Database.URL()),
		CORSOrigins:  cors, Models: app.Models, OIDC: app.OIDC,
		Durable: app.Durable, SecureCookies: app.SecureCookies,
		ExperienceWorkerCredential: strings.TrimSpace(os.Getenv("WORKER_CORE_TOKEN")),
		OperatorAuditEnabled:       enabled,
		CapabilityPacks:            registry,
		ExternalConnectors:         connectors,
		ExternalConnectorInfo:      catalog,
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

// origins reads the extra browser origins the deployment allows. An empty value
// keeps the handler's development defaults.
func origins() []string {
	extra := strings.TrimSpace(os.Getenv("CORS_ORIGIN"))
	if extra == "" {
		return nil
	}
	result := []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	for _, origin := range strings.Split(extra, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			result = append(result, origin)
		}
	}
	return result
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
