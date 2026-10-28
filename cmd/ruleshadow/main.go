// ruleshadow runs a synthetic Eino decision experiment independently of case HTTP.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"contractaudit/internal/access"

	"contractaudit/internal/decision"
	"contractaudit/internal/jev"
	"contractaudit/internal/model"
	"contractaudit/internal/rulepack"

	"github.com/cloudwego/eino/compose"
)

type benchmarkCase struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Expected string `json:"expected"`
}

type dataset struct {
	SchemaVersion  string               `json:"schema_version"`
	Classification string               `json:"classification"`
	Candidates     []decision.Candidate `json:"candidates"`
	Cases          []benchmarkCase      `json:"cases"`
}

type execution struct {
	Input          decision.Input
	Decision       decision.Result
	Chat           *model.ChoiceResult
	Jev            *jev.Answer
	Rules          rulepack.Report
	RuleDurationMS float64
}

type row struct {
	ID               string              `json:"id"`
	Expected         string              `json:"expected"`
	Decision         decision.Result     `json:"decision"`
	Chat             *model.ChoiceResult `json:"chat,omitempty"`
	Jev              *jev.Answer         `json:"jev,omitempty"`
	DurationMS       float64             `json:"duration_ms"`
	RuleDurationMS   float64             `json:"rule_duration_ms"`
	RuleOutputSHA256 string              `json:"rule_output_sha256"`
	RulesUnchanged   bool                `json:"rules_unchanged"`
}

type output struct {
	SchemaVersion  string          `json:"schema_version"`
	Classification string          `json:"classification"`
	GeneratedAt    time.Time       `json:"generated_at"`
	DatasetSHA256  string          `json:"dataset_sha256"`
	RequestSHA256  string          `json:"request_sha256"`
	Mode           decision.Mode   `json:"mode"`
	Backend        string          `json:"backend"`
	Baseline       rulepack.Report `json:"baseline"`
	BaselineSHA256 string          `json:"baseline_sha256"`
	PublishedPack  json.RawMessage `json:"published_pack"`
	Rows           []row           `json:"rows"`
	Passed         bool            `json:"passed"`
	Limitations    []string        `json:"limitations"`
}

func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func readJSON(path string, target any) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 2*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 2*1024*1024 {
		return nil, errors.New("fixture exceeds 2 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing fixture content")
	}
	return body, nil
}

func run() error {
	packPath := flag.String("pack", "internal/rulepack/testdata/demo.json", "draft synthetic manifest or published snapshot")
	requestPath := flag.String("request", "internal/rulepack/testdata/demo-request.json", "trusted local synthetic identity, sources and typed facts")
	datasetPath := flag.String("dataset", "testdata/decision-zh-v1.json", "fixed Chinese focus-classification dataset")
	outPath := flag.String("out", "artifacts/ruleshadow.json", "machine evidence output")
	mode := flag.String("mode", "disabled", "disabled or shadow")
	backend := flag.String("backend", "chat", "shadow backend: chat or jev")
	chatURL := flag.String("chat-url", "", "explicit OpenAI-compatible chat base URL")
	chatModel := flag.String("chat-model", "group/auto-deepseek-v4-flash", "configured chat route for synthetic comparison")
	jevURL := flag.String("jev-url", "", "override TypeSafe Jev base URL; defaults to TYPESAFE_JEV_BASE_URL")
	jevModel := flag.String("jev-model", "", "override TypeSafe Jev model alias; defaults to TYPESAFE_JEV_MODEL")
	timeout := flag.Duration("timeout", 10*time.Second, "bounded individual decision timeout")
	publishDemo := flag.Bool("publish-demo", false, "explicitly simulate lifecycle only for synthetic_demo authorities")
	flag.Parse()
	if *mode != "disabled" && *mode != "shadow" {
		return errors.New("CLI supports disabled or shadow only")
	}
	if *backend != "chat" && *backend != "jev" {
		return errors.New("unknown shadow backend")
	}
	if *mode == "shadow" && *backend == "chat" && *chatURL == "" {
		return errors.New("chat shadow requires an explicit chat URL")
	}
	packBody, err := os.ReadFile(*packPath)
	if err != nil {
		return err
	}
	pack, err := rulepack.Parse(packBody)
	if err != nil {
		return err
	}
	if *publishDemo {
		for _, rule := range pack.Manifest().Rules {
			for _, authority := range rule.AuthoritySources {
				if authority.Kind != "synthetic_demo" {
					return errors.New("demo lifecycle requires exclusively synthetic authorities")
				}
			}
		}
		pack, err = pack.Validate()
		if err != nil {
			return err
		}
		pack, err = pack.Approve("synthetic-demo-actor", pack.Hash())
		if err != nil {
			return err
		}
		pack, err = pack.Publish()
		if err != nil {
			return err
		}
	}
	var request rulepack.Request
	requestBody, err := readJSON(*requestPath, &request)
	if err != nil {
		return err
	}
	var data dataset
	datasetBody, err := readJSON(*datasetPath, &data)
	if err != nil {
		return err
	}
	if data.SchemaVersion != "decision-benchmark/v1" || len(data.Cases) == 0 || len(data.Cases) > 200 {
		return errors.New("invalid bounded decision dataset")
	}
	chatClient := model.New(model.Config{BaseURL: *chatURL, ChatModel: *chatModel})
	var jevClient *jev.Client
	if *mode == "shadow" && *backend == "jev" {
		config := jev.FromEnv()
		config.Timeout = *timeout
		if *jevURL != "" {
			config.BaseURL = *jevURL
		}
		if *jevModel != "" {
			config.Model = *jevModel
		}
		jevClient, err = jev.New(config)
		if err != nil {
			return err
		}
	}
	ctx := context.Background()
	baseline, err := pack.Execute(ctx, request)
	if err != nil {
		return err
	}
	baselineJSON, err := json.Marshal(baseline)
	if err != nil {
		return err
	}
	snapshot, err := pack.Snapshot()
	if err != nil {
		return err
	}
	report := output{SchemaVersion: "ruleshadow-report/v1", Classification: data.Classification,
		GeneratedAt: time.Now().UTC(), DatasetSHA256: digest(datasetBody), RequestSHA256: digest(requestBody),
		Mode: decision.Mode(*mode), Baseline: baseline, BaselineSHA256: digest(baselineJSON), PublishedPack: snapshot,
		Rows: make([]row, 0, len(data.Cases)), Passed: true,
		Backend: *backend,
		Limitations: []string{"D0 developer-visible synthetic cases; not independent business validation",
			"Typed facts are explicitly supplied fixtures, not free-text extraction",
			"Choice accuracy and deterministic numerical relations are different tasks",
			"An observed response model label is not a reviewed checkpoint pin",
			"In-memory Eino execution only; no case HTTP, persistent attempts or business approvals",
			"Passed means mandatory RulePack output invariance; model availability and quality are reported separately"}}
	if *backend == "jev" && *mode == "shadow" {
		report.Limitations = append(report.Limitations,
			"Jev is a hosted TypeSafe service; this benchmark sends only the synthetic D0 states and never contract text",
			"Jev returns calibrated choice probabilities; they remain suggestions, not audit conclusions",
			"The Jev backend is default-off and does not use the model gateway budget ledger")
	}
	if *mode == "disabled" {
		report.Backend = "rules_only"
	}
	chain := compose.NewChain[*execution, *execution]()
	chain.AppendLambda(compose.InvokableLambda(func(ctx context.Context, state *execution) (*execution, error) {
		switch {
		case *mode == "shadow" && *backend == "chat":
			state.Decision, state.Chat = chatShadow(ctx, chatClient, state.Input, *chatModel, *timeout)
		case *mode == "shadow" && *backend == "jev":
			state.Decision, state.Jev = jevShadow(ctx, jevClient, state.Input, *timeout)
		default:
			state.Decision = decision.DisabledResult(state.Input)
		}
		return state, nil
	}), compose.WithNodeName("decision_choice_shadow"))
	chain.AppendLambda(compose.InvokableLambda(func(ctx context.Context, state *execution) (*execution, error) {
		started := time.Now()
		result, executeErr := pack.Execute(ctx, request)
		state.Rules = result
		state.RuleDurationMS = float64(time.Since(started).Nanoseconds()) / 1e6
		return state, executeErr
	}), compose.WithNodeName("mandatory_rulepack"))
	runnable, err := chain.Compile(ctx, compose.WithGraphName("rulepack_decision_shadow_v1"))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, item := range data.Cases {
		if item.ID == "" || seen[item.ID] {
			return errors.New("missing or duplicate case ID")
		}
		seen[item.ID] = true
		started := time.Now()
		state, err := runnable.Invoke(ctx, &execution{Input: decision.Input{
			RequestID: item.ID, DecisionID: "decision-" + item.ID, RunID: "run-synthetic", AttemptID: "attempt-" + item.ID,
			TraceID: "trace-" + item.ID, Principal: request.Principal, Resource: request.Resource,
			Capability: decision.CapabilityChoice, State: item.State, AllowedCandidates: data.Candidates,
			RulePackVersion: pack.Manifest().Version, RulePackHash: pack.Hash(),
			PolicyVersion: access.PolicyVersion}})
		if err != nil {
			return err
		}
		durationMS := float64(time.Since(started).Nanoseconds()) / 1e6
		body, err := json.Marshal(state.Rules)
		if err != nil {
			return err
		}
		unchanged := bytes.Equal(body, baselineJSON) && len(state.Decision.AcceptedRules) == 0
		report.Passed = report.Passed && unchanged
		report.Rows = append(report.Rows, row{ID: item.ID, Expected: item.Expected, Decision: state.Decision, Chat: state.Chat, Jev: state.Jev,
			DurationMS: durationMS, RuleDurationMS: state.RuleDurationMS, RuleOutputSHA256: digest(body), RulesUnchanged: unchanged})
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, append(body, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("ruleshadow: mode=%s cases=%d mandatory_output_unchanged=%t evidence=%s\n", *mode, len(report.Rows), report.Passed, *outPath)
	if !report.Passed {
		return errors.New("decision changed mandatory rule output")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ruleshadow:", err)
		os.Exit(1)
	}
}
