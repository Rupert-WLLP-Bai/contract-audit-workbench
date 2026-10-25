package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
}
