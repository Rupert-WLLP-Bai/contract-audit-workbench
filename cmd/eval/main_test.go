package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"contractaudit/internal/compare"
	"contractaudit/internal/engine"
)

func TestCLIProducesVerifiedReport(t *testing.T) {
	var out, errout bytes.Buffer
	if code := run(nil, &out, &errout); code != 0 {
		t.Fatalf("exit %d: %s", code, errout.String())
	}
	var report compare.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || !report.Passed || report.Overall.Scenarios != 62 {
		t.Fatalf("report %+v %v", report, err)
	}
	path := filepath.Join(t.TempDir(), "nested", "report.json")
	if code := run([]string{"-out", path}, &out, &errout); code != 0 {
		t.Fatalf("file output exit %d: %s", code, errout.String())
	}
	if data, err := os.ReadFile(path); err != nil || !json.Valid(data) {
		t.Fatalf("invalid output %v", err)
	}
}

func TestCLIUsesAnExternalDatasetFile(t *testing.T) {
	dataset, err := compare.DefaultDataset()
	if err != nil {
		t.Fatal("load a valid scenario for the external dataset")
	}
	dataset.ID = "external-worker-check"
	dataset.Scenarios = dataset.Scenarios[:1]
	raw, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal("encode the external evaluation dataset")
	}
	path := filepath.Join(t.TempDir(), "external.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal("write the external evaluation dataset")
	}

	var out, errout bytes.Buffer
	if code := run([]string{"-dataset", path}, &out, &errout); code != 0 {
		t.Fatalf("external dataset exit %d: %s", code, errout.String())
	}
	var report compare.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Overall.Scenarios != 1 {
		t.Fatalf("external dataset report scenarios=%d err=%v, want 1", report.Overall.Scenarios, err)
	}
}

func TestMainUsesCLIExitCodes(t *testing.T) {
	if mode := os.Getenv("CAW_EVAL_MAIN_CHILD"); mode != "" {
		if mode == "unknown-flag" {
			os.Args = []string{"eval", "-unknown"}
		} else {
			os.Args = []string{"eval"}
		}
		main()
		t.Fatal("main returned instead of exiting with the CLI result")
	}

	t.Run("writes a report and exits successfully", func(t *testing.T) {
		output, err := runEvalMainSubprocess(t, "success")
		if err != nil || !json.Valid(output) {
			t.Fatalf("eval main output valid=%t err=%v", json.Valid(output), err)
		}
	})
	t.Run("preserves the invalid flag exit code", func(t *testing.T) {
		output, err := runEvalMainSubprocess(t, "unknown-flag")
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Fatalf("eval main error exit = %v, output=%s; want exit 2", err, output)
		}
		if !strings.Contains(string(output), "flag provided but not defined: -unknown") {
			t.Fatalf("eval main output lost the invalid flag error: %s", output)
		}
	})
}

func runEvalMainSubprocess(t *testing.T, mode string) ([]byte, error) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestMainUsesCLIExitCodes$")
	command.Env = replaceEvalTestEnvironment(os.Environ(), map[string]string{"CAW_EVAL_MAIN_CHILD": mode})
	return command.CombinedOutput()
}

func replaceEvalTestEnvironment(environment []string, replacements map[string]string) []string {
	filtered := make([]string, 0, len(environment)+len(replacements))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := replacements[name]; !replaced {
			filtered = append(filtered, entry)
		}
	}
	for name, value := range replacements {
		filtered = append(filtered, name+"="+value)
	}
	return filtered
}

func TestCLIMismatchFailsUnlessExplicitlyReportOnly(t *testing.T) {
	dataset, err := compare.DefaultDataset()
	if err != nil {
		t.Fatal(err)
	}
	dataset.Scenarios[0].Expected = engine.RelCompatible
	raw, _ := json.Marshal(dataset)
	path := filepath.Join(t.TempDir(), "changed-gold.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if got := run([]string{"-dataset", path}, &out, &errout); got != 1 {
		t.Fatalf("mismatch exit %d", got)
	}
	if got := run([]string{"-dataset", path, "-report-only"}, &out, &errout); got != 0 {
		t.Fatalf("report-only exit %d", got)
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

type shortNilWriter struct{}

func (shortNilWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestCLIInputAndOutputErrorsFail(t *testing.T) {
	var out, errout bytes.Buffer
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("broken JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-unknown"}, {"positional"}, {"-dataset", file + "-missing"}, {"-dataset", file}, {"-out", filepath.Join(file, "nested")}, {"-out", t.TempDir()},
	} {
		if got := run(args, &out, &errout); got != 2 {
			t.Fatalf("args %v returned %d", args, got)
		}
	}
	if got := run(nil, failedWriter{}, &errout); got != 2 {
		t.Fatalf("write error returned %d", got)
	}
	if got := run(nil, shortNilWriter{}, &errout); got != 0 {
		t.Fatalf("short stdout write without an error returned %d, want 0", got)
	}
}
