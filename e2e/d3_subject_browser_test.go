//go:build browser

package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"
)

func TestBrowserD3SubjectControlledSourceSOD(t *testing.T) {
	evidenceDir, repoRoot, webRoot := d3EvidenceDirectory(t)
	before, beforeDigest, err := d3SourceManifest(repoRoot)
	if err != nil {
		t.Fatalf("capture candidate source manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "source-manifest-before.json"), before, 0o600); err != nil {
		t.Fatal(err)
	}
	testCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]any{
		"test": t.Name(), "test_cwd": testCWD, "browser_cwd": webRoot,
		"source_root": repoRoot, "source_manifest_before_sha256": beforeDigest,
		"browser_script_status": "NOT_RUN", "browser_script_exit_code": nil,
		"go_test_exit_code": nil,
		"go_test_evidence": "outer runner must capture complete go test output and actual exit code",
	}
	t.Cleanup(func() {
		after, afterDigest, captureErr := d3SourceManifest(repoRoot)
		if captureErr != nil {
			result["source_manifest_after_error"] = captureErr.Error()
			t.Errorf("capture final candidate source manifest: %v", captureErr)
		} else {
			result["source_manifest_after_sha256"] = afterDigest
			if err := os.WriteFile(filepath.Join(evidenceDir, "source-manifest-after.json"), after, 0o600); err != nil {
				t.Errorf("write final candidate source manifest: %v", err)
			}
			if afterDigest != beforeDigest {
				t.Errorf("candidate source changed during browser journey: %s -> %s", beforeDigest, afterDigest)
			}
		}
		encoded, marshalErr := json.MarshalIndent(result, "", "  ")
		if marshalErr != nil {
			t.Errorf("encode D3 evidence metadata: %v", marshalErr)
			return
		}
		if err := os.WriteFile(filepath.Join(evidenceDir, "browser-run.json"), append(encoded, '\n'), 0o600); err != nil {
			t.Errorf("write D3 evidence metadata: %v", err)
		}
	})
	t.Logf("D3 browser evidence directory: %s", evidenceDir)
	chrome := chromiumBinary(t)
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("Bun unavailable: install Bun 1.4.3 and dependencies in web")
	}
	st, err := store.OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	admin, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("demo admin missing")
	}
	reviewer, ok := accounts.ByID("demo-reviewer")
	if !ok {
		t.Fatal("demo reviewer missing")
	}
	caseA, err := st.CreateCase(admin, "D3主体核验案件甲", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	caseB, err := st.CreateCase(admin, "D3主体核验案件乙", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	// Reviewer can initially see only A. B is granted later as synthetic setup,
	// after the browser has checked the hidden-case decision boundary.
	if err := st.AddMember(admin, caseA, reviewer.ID); err != nil {
		t.Fatalf("authorize independent reviewer in %s: %v", caseA, err)
	}
	if _, err := st.AddDocument(context.Background(), admin, caseA, "attachment", "subject-source-basis.txt", "text/plain", []byte("经核对，统一社会信用代码更正为91350100M000100Y44。")); err != nil {
		t.Fatalf("seed fixed subject basis: %v", err)
	}
	source := store.SubjectSourceManifest{
		SourceID: "synthetic.uscc.demo", SourceVersion: 1, DisplayName: "D3受控合成主体目录",
		VerificationScope: "synthetic_controlled",
		Records: []store.SubjectSourceRecord{{
			RecordID: "d3-company-001", DisplayName: "D3示例网络服务有限公司", USCC: "91350100M000100Y43",
			SourceAsOf: time.Now().UTC().Add(-time.Hour), ValidFrom: time.Now().UTC().AddDate(-1, 0, 0),
		}},
	}
	manifest, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ConfigureSubjectSourceManifest(manifest); err != nil {
		t.Fatalf("configure explicit controlled demo source: %v", err)
	}
	source.SourceVersion = 2
	source.Records[0].RecordID = "d3-company-002"
	source.Records[0].USCC = "91350100M000100Y44"
	manifestV2, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var fixtureBytes [32]byte
	if _, err := rand.Read(fixtureBytes[:]); err != nil {
		t.Fatal(err)
	}
	fixtureKey := hex.EncodeToString(fixtureBytes[:])
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	webPort := freePort(t)
	webURL := "http://127.0.0.1:" + webPort
	app := httpapi.Handler(st, httpapi.Options{CORSOrigins: []string{webURL}})
	mux := http.NewServeMux()
	// This loopback-only fixture endpoint changes synthetic input data, not a
	// subject, proposal, approval or any other behavior under test.
	mux.HandleFunc("POST /api/__d3_fixture__/subject-source-v2", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != webURL || r.Header.Get("X-D3-Fixture-Key") != fixtureKey {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if err := st.ConfigureSubjectSourceManifest(manifestV2); err != nil {
			http.Error(w, "could not configure synthetic source", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", app)
	api := &http.Server{Handler: mux}
	go func() { _ = api.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = api.Shutdown(ctx)
	})
	viteLog, err := os.Create(filepath.Join(evidenceDir, "vite.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = viteLog.Close() })
	vite := exec.Command(bun, "run", "dev", "--", "--port", webPort, "--strictPort", "--host", "127.0.0.1")
	vite.Dir = webRoot
	vite.Env = localBrowserEnv(append(os.Environ(), "VITE_DEV_API_PROXY=http://"+listener.Addr().String()))
	vite.Stdout, vite.Stderr = viteLog, viteLog
	vite.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := vite.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopGroup(vite) })
	waitHTTP(t, webURL+"/")
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	env := append(os.Environ(),
		"CHROMIUM_BIN="+chrome,
		"E2E_WEB="+webURL,
		"E2E_D3_CASE_A="+caseA,
		"E2E_D3_CASE_B="+caseB,
		"E2E_D3_FIXTURE_KEY="+fixtureKey,
		"E2E_D3_OUT="+evidenceDir,
		"E2E_SCREENSHOT_DIR="+evidenceDir,
	)
	cmd := exec.CommandContext(ctx, bun, "e2e/d3-subject-controlled-source.ts")
	cmd.Dir = webRoot
	cmd.Env = localBrowserEnv(env)
	result["browser_command"] = cmd.Args
	out, runErr := runBrowserCommand(ctx, cmd)
	if cmd.ProcessState != nil {
		if exitCode := cmd.ProcessState.ExitCode(); exitCode >= 0 {
			result["browser_script_exit_code"] = exitCode
		}
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			result["browser_script_signal"] = status.Signal().String()
		}
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "browser-script.log"), out, 0o600); err != nil {
		t.Fatalf("write complete browser script output: %v", err)
	}
	t.Log(string(out))
	if runErr != nil {
		result["browser_script_status"] = "FAIL"
		result["browser_script_error"] = runErr.Error()
		t.Fatalf("D3 subject/source journey failed: %v\n%s", runErr, out)
	}
	result["browser_script_status"] = "PASS"
	}
}

// Evidence must be outside the checkout, including when the supplied path is a symlink.
func d3EvidenceDirectory(t *testing.T) (string, string, string) {
	t.Helper()
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, err = filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	delivery := strings.TrimSpace(os.Getenv("CAW_DELIVERY_DIR"))
	if delivery == "" {
		t.Fatal("CAW_DELIVERY_DIR must name an existing directory outside the source checkout")
	}
	delivery, err = filepath.Abs(delivery)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err = filepath.EvalSymlinks(delivery)
	if err != nil {
		t.Fatalf("resolve CAW_DELIVERY_DIR: %v", err)
	}
	relative, err := filepath.Rel(repoRoot, delivery)
	if err != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		t.Fatal("CAW_DELIVERY_DIR must be outside the source checkout")
	}
	dir, err := os.MkdirTemp(delivery, "p8-d3-subject-")
	if err != nil {
		t.Fatalf("create external D3 evidence directory: %v", err)
	}
	return dir, repoRoot, filepath.Join(repoRoot, "web")
}

// Reuse the repository's executable-source identity definition before and after
// the journey, so the retained manifest pins the actual candidate's files.
func d3SourceManifest(repoRoot string) ([]byte, string, error) {
	const program = `import json, sys; sys.path.insert(0, sys.argv[1]); from source_identity import source_identity; print(json.dumps(source_identity(sys.argv[2]), ensure_ascii=False, sort_keys=True))`
	cmd := exec.Command("python3", "-B", "-c", program, filepath.Join(repoRoot, "scripts"), repoRoot)
	cmd.Dir = repoRoot
	output, err := cmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("source identity: %w", err)
	}
	var identity struct {
		Digest string `json:"digest"`
		Files  []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(output, &identity); err != nil {
		return nil, "", err
	}
	if identity.Digest == "" || len(identity.Files) == 0 {
		return nil, "", fmt.Errorf("source identity is incomplete")
	}
	return output, identity.Digest, nil
}
