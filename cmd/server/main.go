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
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"contractaudit/internal/bootstrap"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/systemhealth"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
	return listen(ctx, httpapi.Handler(app.Store, options(app, origins())))
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
	handler, err := httpapi.ServiceHandler(app.Store, options(app, origins()), "core", strings.TrimSpace(os.Getenv("GATEWAY_ROLE_TOKEN")))
	if err != nil {
		return err
	}
	logStartup(app)
	return listen(ctx, handler)
}

func options(app *bootstrap.Runtime, cors []string) httpapi.Options {
	return httpapi.Options{
		SystemHealth: systemhealth.FromEnv(app.Store.Ready),
		CORSOrigins:  cors, Models: app.Models, OIDC: app.OIDC,
		Durable: app.Durable, SecureCookies: app.SecureCookies,
	}
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
	addr := env("ADDR", "127.0.0.1:8080")
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
	server.BaseContext = func(_ net.Listener) context.Context { return ctx }
	finished := make(chan error, 1)
	go func() { finished <- server.ListenAndServe() }()
	select {
	case err := <-finished:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		if err := <-finished; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
