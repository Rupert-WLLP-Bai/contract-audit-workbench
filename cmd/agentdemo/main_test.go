package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"contractaudit/internal/agentflow"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("../../testdata/runtime/scenarios.json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestFrozenSyntheticScenarios(t *testing.T) {
	body := fixture(t)
	data, err := loadDataset(body)
	if err != nil {
		t.Fatal(err)
	}
	result, err := execute(context.Background(), data, agentflow.TextHash(string(body)))
	if err != nil || !result.Passed || result.Total != 6 || result.PassedCount != 6 {
		t.Fatalf("result = %+v; err = %v", result, err)
	}
	if result.Results[2].Report.ToolCalls != 0 || result.Results[3].Report.ToolCalls != 0 {
		t.Fatal("security rejection executed tools")
	}
}

func TestCLIExitCodesAndReport(t *testing.T) {
	input := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(input, fixture(t), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "nested", "result.json")
	var log bytes.Buffer
	if code := run([]string{"-in", input, "-out", output}, &log, &log); code != 0 {
		t.Fatalf("code=%d %s", code, log.String())
	}
	body, err := os.ReadFile(output)
	if err != nil || !bytes.Contains(body, []byte("NOT_RUN")) {
		t.Fatal("missing honest report", err)
	}
	var data dataset
	if err := json.Unmarshal(fixture(t), &data); err != nil {
		t.Fatal(err)
	}
	data.Scenarios[0].Expected.ToolCalls = 4
	bad, _ := json.Marshal(data)
	if err := os.WriteFile(input, bad, 0600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"-in", input, "-out", output}, &log, &log); code != 2 {
		t.Fatalf("reused report path exit code = %d, want 2", code)
	}
	preserved, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(preserved, body) {
		t.Fatalf("existing report changed after output collision: err=%v", err)
	}
	mismatchOutput := output + ".mismatch"
	if code := run([]string{"-in", input, "-out", mismatchOutput}, &log, &log); code != 1 {
		t.Fatal("mismatch did not fail with a fresh output path", code)
	}
	if code := run([]string{"-in", input + ".missing"}, &log, &log); code != 2 {
		t.Fatal("missing input", code)
	}
	if code := run([]string{"-unknown"}, &log, &log); code != 2 {
		t.Fatal("unknown flag", code)
	}
}

func TestRejectMalformedOrRelabelledDataset(t *testing.T) {
	body := fixture(t)
	for _, invalid := range [][]byte{
		[]byte("{}"),
		append(append([]byte{}, body...), []byte("{}")...),
		[]byte(strings.Replace(string(body), "D0_SYNTHETIC_CONTROL_REGRESSION", "real_accuracy", 1)),
		[]byte(strings.Replace(string(body), "\"title\":", "\"unknown\":", 1)),
		[]byte(strings.Replace(string(body), "\"planner_mode\": \"scope\"", "\"planner_mode\": \"live_model\"", 1)),
		[]byte(strings.Replace(string(body), "\"conflict\"", "\"automatically_legal\"", 1)),
		[]byte(strings.Repeat("x", 4*1024*1024+1)),
	} {
		if _, err := loadDataset(invalid); err == nil {
			t.Fatal("invalid dataset accepted")
		}
	}
}

func TestAssessorCatchesMissingEvidenceAndWrongStatus(t *testing.T) {
	problems := assess(expected{Status: "completed", ToolCalls: 1, ErrorKind: "none", Fallback: true,
		Relations: map[agentflow.Domain][]string{agentflow.Technical: {"conflict"}}, Unresolved: []string{"missing"}},
		agentflow.Report{Status: "partial"}, agentflow.ErrDenied)
	if len(problems) != 6 {
		t.Fatalf("missing assertions: %v", problems)
	}
}
