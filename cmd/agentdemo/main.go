// agentdemo executes synthetic control-regression scenarios without a database
// or a real model. Expected denials count as passing assertions, not successful
// business audits.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"contractaudit/internal/agentflow"
)

type expected struct {
	Status     string                        `json:"status"`
	ToolCalls  int                           `json:"tool_calls"`
	ErrorKind  string                        `json:"error_kind"`
	Fallback   bool                          `json:"fallback"`
	Relations  map[agentflow.Domain][]string `json:"contains_relations"`
	Unresolved []string                      `json:"unresolved_gap_ids"`
}

type scenario struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	PlannerMode string            `json:"planner_mode"`
	FixturePlan json.RawMessage   `json:"fixture_plan,omitempty"`
	Request     agentflow.Request `json:"request"`
	Expected    expected          `json:"expected"`
}

type dataset struct {
	SchemaVersion  string     `json:"schema_version"`
	Classification string     `json:"classification"`
	Scenarios      []scenario `json:"scenarios"`
}

type scenarioResult struct {
	ID         string           `json:"id"`
	Title      string           `json:"title"`
	Passed     bool             `json:"passed"`
	ErrorKind  string           `json:"error_kind"`
	Violations []string         `json:"violations"`
	Report     agentflow.Report `json:"report"`
}

type output struct {
	SchemaVersion  string           `json:"schema_version"`
	Classification string           `json:"classification"`
	DatasetSHA256  string           `json:"dataset_sha256"`
	RuntimeVersion string           `json:"runtime_version"`
	GeneratedAt    time.Time        `json:"generated_at"`
	LiveModel      string           `json:"live_model"`
	Passed         bool             `json:"passed"`
	Total          int              `json:"total"`
	PassedCount    int              `json:"passed_count"`
	Results        []scenarioResult `json:"results"`
	Limitations    []string         `json:"limitations"`
}

type fixturePlanner struct {
	mode string
	plan json.RawMessage
}

func (p fixturePlanner) Name() string { return "synthetic-fixture-" + p.mode }
func (p fixturePlanner) Plan(context.Context, agentflow.PlanInput) (agentflow.Plan, error) {
	if p.mode == "unavailable" {
		return agentflow.Plan{}, errors.New("synthetic unavailable planner")
	}
	return agentflow.ParsePlan(string(p.plan))
}

func loadDataset(body []byte) (dataset, error) {
	var data dataset
	if len(body) > 4*1024*1024 {
		return data, errors.New("dataset exceeds 4 MiB")
	}
	if err := decodeStrict(body, &data); err != nil {
		return data, err
	}
	if data.SchemaVersion != "agent-control-fixtures/v1" || data.Classification != "D0_SYNTHETIC_CONTROL_REGRESSION" ||
		len(data.Scenarios) == 0 || len(data.Scenarios) > 40 {
		return data, errors.New("invalid dataset identity or scenario count")
	}
	seen := map[string]bool{}
	for _, item := range data.Scenarios {
		if item.ID == "" || seen[item.ID] || item.Title == "" || item.Request.RequestID != item.ID ||
			!slices.Contains([]string{"scope", "fixture_json", "unavailable"}, item.PlannerMode) ||
			!slices.Contains([]string{"completed", "partial", "rejected", "cancelled"}, item.Expected.Status) ||
			!slices.Contains([]string{"none", "denied", "input", "plan", "cancelled"}, item.Expected.ErrorKind) ||
			item.Expected.ToolCalls < 0 || item.Expected.ToolCalls > 16 {
			return data, errors.New("invalid scenario metadata or expectation")
		}
		if item.PlannerMode == "fixture_json" && len(item.FixturePlan) == 0 {
			return data, errors.New("fixture_json requires an explicit plan")
		}
		for domain, relations := range item.Expected.Relations {
			if !slices.Contains(agentflow.Domains(), domain) || len(relations) == 0 {
				return data, errors.New("invalid expected domain")
			}
			for _, relation := range relations {
				if !slices.Contains([]string{"compatible", "conflict", "insufficient", "not_applicable"}, relation) {
					return data, errors.New("invalid expected relation")
				}
			}
		}
		seen[item.ID] = true
	}
	return data, nil
}

func decodeStrict(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var tail any
	if err := decoder.Decode(&tail); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func errorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, agentflow.ErrDenied):
		return "denied"
	case errors.Is(err, agentflow.ErrInput):
		return "input"
	case errors.Is(err, agentflow.ErrPlan):
		return "plan"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "cancelled"
	default:
		return "unexpected"
	}
}

func assess(want expected, report agentflow.Report, err error) []string {
	problems := []string{}
	if report.Status != want.Status {
		problems = append(problems, "status")
	}
	if report.ToolCalls != want.ToolCalls {
		problems = append(problems, "tool_calls")
	}
	if errorKind(err) != want.ErrorKind {
		problems = append(problems, "error_kind")
	}
	if report.FallbackUsed != want.Fallback {
		problems = append(problems, "fallback")
	}
	for domain, relations := range want.Relations {
		found := []string{}
		for _, result := range report.Domains {
			if result.Domain == domain {
				for _, finding := range result.Findings {
					found = append(found, finding.Relation)
				}
			}
		}
		for _, relation := range relations {
			if !slices.Contains(found, relation) {
				problems = append(problems, string(domain)+":"+relation)
			}
		}
	}
	for _, gap := range want.Unresolved {
		if !slices.Contains(report.UnresolvedGapIDs, gap) {
			problems = append(problems, "missing_gap:"+gap)
		}
	}
	slices.Sort(problems)
	return problems
}

func execute(ctx context.Context, data dataset, digest string) (output, error) {
	result := output{SchemaVersion: "agent-control-results/v1", Classification: data.Classification,
		DatasetSHA256: digest, RuntimeVersion: agentflow.RuntimeVersion, GeneratedAt: time.Now().UTC(),
		LiveModel: "NOT_RUN: deterministic rules and explicitly labelled planner fixtures",
		Passed:    true, Total: len(data.Scenarios), Results: []scenarioResult{},
		Limitations: []string{"Synthetic control regression; not real-contract accuracy or model quality.",
			"Independent Eino component; case HTTP, durable queues and identity integration remain planned.",
			"completed means the requested tool plan finished; unresolved evidence can remain.",
			"No Docker services, external registry, private model, or business UAT are exercised."}}
	for _, item := range data.Scenarios {
		var planner agentflow.Planner = agentflow.ScopePlanner{}
		if item.PlannerMode != "scope" {
			planner = fixturePlanner{item.PlannerMode, item.FixturePlan}
		}
		runtime, err := agentflow.New(ctx, agentflow.Config{Planner: planner, Tools: agentflow.BaselineTools(),
			MaxCalls: 5, Timeout: 5 * time.Second, ToolTimeout: time.Second})
		if err != nil {
			return result, err
		}
		report, err := runtime.Run(ctx, item.Request)
		problems := assess(item.Expected, report, err)
		passed := len(problems) == 0
		result.Results = append(result.Results, scenarioResult{ID: item.ID, Title: item.Title,
			Passed: passed, ErrorKind: errorKind(err), Violations: problems, Report: report})
		result.Passed = result.Passed && passed
		if passed {
			result.PassedCount++
		}
	}
	return result, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agentdemo", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("in", "testdata/runtime/scenarios.json", "synthetic scenario dataset")
	destination := flags.String("out", "artifacts/agent-demo.json", "JSON report path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}
	body, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	data, err := loadDataset(body)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	result, err := execute(context.Background(), data, agentflow.TextHash(string(body)))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err = os.MkdirAll(filepath.Dir(*destination), 0755); err == nil {
		err = writeReportExclusively(*destination, append(encoded, '\n'))
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintf(stdout, "D0 synthetic control regression: %d/%d; report: %s; live model NOT_RUN\n",
		result.PassedCount, result.Total, *destination)
	if !result.Passed {
		return 1
	}
	return 0
}

func writeReportExclusively(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
