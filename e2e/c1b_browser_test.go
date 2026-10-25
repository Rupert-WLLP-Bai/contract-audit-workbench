//go:build browser

package e2e_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"
)

func TestBrowserTelecomC1BFullJourney(t *testing.T) {
	chrome := os.Getenv("CHROMIUM_BIN")
	if chrome == "" {
		chrome = "/usr/bin/chromium"
	}
	if _, err := os.Stat(chrome); err != nil {
		t.Fatal("Chromium unavailable: set CHROMIUM_BIN or run make browser-install")
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("Bun unavailable: install Bun 1.4.3 and dependencies in web")
	}

	dir := t.TempDir()
	st, err := store.OpenTest(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(e2eModels{})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	webPort := freePort(t)
	webURL := "http://127.0.0.1:" + webPort
	api := &http.Server{Handler: httpapi.Handler(st, httpapi.Options{CORSOrigins: []string{webURL}})}
	go func() { _ = api.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = api.Shutdown(ctx)
	})

	webRoot := filepath.Clean(filepath.Join("..", "web"))
	vite := exec.Command(bun, "run", "dev", "--", "--port", webPort, "--strictPort", "--host", "127.0.0.1")
	vite.Dir = webRoot
	vite.Env = append(os.Environ(), "VITE_DEV_API_PROXY=http://"+listener.Addr().String())
	vite.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := vite.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopGroup(vite) })
	waitHTTP(t, webURL+"/")

	dataset, err := filepath.Abs(filepath.Join("..", "testdata", "telecom-c1b-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	script := exec.CommandContext(ctx, bun, "e2e/c1b-journey.ts")
	script.Dir = webRoot
	script.Env = append(os.Environ(),
		"CHROMIUM_BIN="+chrome,
		"E2E_WEB="+webURL,
		"E2E_TELECOM_DATASET="+dataset,
		"E2E_TELECOM_SCENARIO=conflict",
	)
	out, err := script.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		if ctx.Err() != nil {
			t.Fatalf("C1-B Playwright journey timed out after 300s: %s", out)
		}
		t.Fatalf("C1-B Playwright journey failed: %v\n%s", err, out)
	}
}
