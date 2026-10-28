//go:build browser

package e2e_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/model"
	"contractaudit/internal/store"
)

func TestBrowserWarrantyConflictAndConfirm(t *testing.T) {
	chrome := chromiumBinary(t)
	if _, err := exec.LookPath("bun"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := store.OpenTest(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.UseModels(&browserModels{})
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	// This journey covers the legacy warranty conflict, retry and reparse
	// flows, which are exercised on cases whose business scope is unspecified.
	// The ENT governed journey is covered by TestBrowserTelecomC1BFullJourney.
	caseID, err := st.CreateCase(admin, "采购质保核对", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	webPort := freePort(t)
	api := &http.Server{Handler: httpapi.Handler(st, httpapi.Options{
		CORSOrigins: []string{"http://127.0.0.1:" + webPort},
	})}
	go func() { _ = api.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = api.Shutdown(ctx)
	})

	root := filepath.Clean(filepath.Join("..", "web"))
	vite := exec.Command("bun", "run", "dev", "--", "--port", webPort, "--strictPort", "--host", "127.0.0.1")
	vite.Dir = root
	vite.Env = append(os.Environ(), "VITE_DEV_API_PROXY=http://"+listener.Addr().String())
	vite.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := vite.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopGroup(vite) })
	waitHTTP(t, "http://127.0.0.1:"+webPort+"/")
	cdpPort := freePort(t)

	procurement := filepath.Join(dir, "purchase.txt")
	draft := filepath.Join(dir, "draft.txt")
	revised := filepath.Join(dir, "draft-revised.txt")
	if err := os.WriteFile(procurement, []byte("采购文件要求质保36个月。"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draft, []byte("拟签合同约定质保24个月。"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(revised, []byte("拟签合同约定质保36个月。"), 0o600); err != nil {
		t.Fatal(err)
	}
	screenshotDir := os.Getenv("E2E_SCREENSHOT_DIR")
	if screenshotDir == "" {
		screenshotDir = filepath.Join("..", "artifacts", "screenshots")
	}
	screenshots, err := filepath.Abs(screenshotDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(screenshots, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	script := exec.CommandContext(ctx, "bun", "e2e/review.ts")
	script.Dir = root
	script.Env = append(os.Environ(),
		"CHROMIUM_BIN="+chrome,
		"E2E_WEB=http://127.0.0.1:"+webPort,
		"E2E_CASE_ID="+caseID,
		"E2E_PROCUREMENT="+procurement,
		"E2E_DRAFT="+draft,
		"E2E_REVISED="+revised,
		"E2E_CDP_PORT="+cdpPort,
		"E2E_CHROME_PROFILE="+filepath.Join(dir, "chrome"),
		"E2E_EXECUTION_SCREENSHOT="+filepath.Join(screenshots, "execution-inspector.png"),
		"E2E_DOCS_SCREENSHOT="+filepath.Join(screenshots, "scalar-portal.png"),
		"E2E_PARSING_SCREENSHOT="+filepath.Join(screenshots, "parsed-source.png"),
	)
	out, err := script.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal(err)
	}
}

type browserModels struct {
	warrantyCalls int
}

func (*browserModels) Name() string { return "script-model" }

func (m *browserModels) ConfigurationID() string { return m.Name() }

func (m *browserModels) Warranty(_ context.Context, docs []model.Material) (model.Warranty, error) {
	m.warrantyCalls++
	if m.warrantyCalls == 1 {
		return model.Warranty{}, errors.New("controlled synthetic first-attempt failure")
	}
	return warrantyOf(docs)
}

func (*browserModels) Transcribe(context.Context, string, []byte) (string, error) {
	return "", model.ErrUnusable
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(port)
}

func waitHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("server did not answer %s", url)
}

func stopGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_, _ = cmd.Process.Wait()
}

// chromiumBinary resolves the browser without baking in one workstation path:
// CHROMIUM_BIN wins, then the Playwright-managed Chromium in the web workspace,
// then a conventional system package. The Go test itself only needs the path;
// the Bun journey receives it through the environment.
func chromiumBinary(t *testing.T) string {
	t.Helper()
	if value := strings.TrimSpace(os.Getenv("CHROMIUM_BIN")); value != "" {
		if _, err := os.Stat(value); err != nil {
			t.Fatalf("CHROMIUM_BIN is not an executable file: %s", value)
		}
		return value
	}
	discover := exec.Command("bun", "-e",
		`import { chromium } from "playwright"; process.stdout.write(chromium.executablePath())`)
	discover.Dir = filepath.Clean(filepath.Join("..", "web"))
	if output, err := discover.Output(); err == nil {
		if value := strings.TrimSpace(string(output)); value != "" {
			if _, statErr := os.Stat(value); statErr == nil {
				return value
			}
		}
	}
	for _, candidate := range []string{
		"/usr/bin/chromium", "/usr/bin/chromium-browser",
		"/usr/bin/google-chrome", "/usr/bin/google-chrome-stable",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Fatal("Chromium unavailable: run `cd web && bunx playwright install chromium`, or set CHROMIUM_BIN")
	return ""
}
