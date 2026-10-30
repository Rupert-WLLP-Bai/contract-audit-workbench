// Command eval runs offline gold regression checks without PostgreSQL or an LLM.
//
//	go run ./cmd/eval -out artifacts/evaluation.json
//
// Exit codes: 0=all checks passed (or explicit -report-only), 1=gold mismatch or
// false/missing evidence, 2=invalid input/execution/output. JSON rates are [0,1].
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"contractaudit/internal/compare"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	datasetPath := flags.String("dataset", "", "optional evaluation JSON; defaults to embedded synthetic corpus")
	outputPath := flags.String("out", "", "JSON output path; empty writes to stdout")
	reportOnly := flags.Bool("report-only", false, "write mismatches but do not fail; forbidden for release acceptance")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "eval: unexpected positional argument")
		return 2
	}
	data, err := loadEvalDataset(*datasetPath)
	if err != nil {
		fmt.Fprintln(stderr, "eval:", err)
		return 2
	}
	report, err := compare.Run(data, nil)
	if err != nil {
		fmt.Fprintln(stderr, "eval:", err)
		return 2
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "eval:", err)
		return 2
	}
	encoded = append(encoded, '\n')
	if err := writeEvalOutput(*outputPath, stdout, encoded); err != nil {
		fmt.Fprintln(stderr, "eval:", err)
		return 2
	}
	if !report.Passed && !*reportOnly {
		fmt.Fprintln(stderr, "eval: regression gate failed; inspect case-level relation and evidence mismatches")
		return 1
	}
	return 0
}

func loadEvalDataset(path string) (compare.Dataset, error) {
	data, err := compare.DefaultDataset()
	if path != "" {
		var file *os.File
		file, err = os.Open(path)
		if err == nil {
			data, err = compare.Load(file)
			file.Close()
		}
	}
	return data, err
}

func writeEvalOutput(path string, stdout io.Writer, encoded []byte) error {
	if path == "" {
		_, err := stdout.Write(encoded)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}
