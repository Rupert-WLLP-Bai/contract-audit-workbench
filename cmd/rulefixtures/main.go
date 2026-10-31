// rulefixtures runs synthetic, versioned author samples with the typed engine.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"contractaudit/internal/capabilitypack"
	"contractaudit/internal/rulepack"
)

type output struct {
	Suite      rulepack.FixtureRun         `json:"suite"`
	Comparison *rulepack.FixtureComparison `json:"comparison,omitempty"`
}

func loadPack(path string) (*rulepack.Pack, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 1024*1024 {
		return nil, errors.New("pack exceeds 1 MiB")
	}
	return rulepack.Parse(raw)
}

func run(args []string, out io.Writer) (bool, error) {
	flags := flag.NewFlagSet("rulefixtures", flag.ContinueOnError)
	packPath := flags.String("pack", "", "candidate manifest or snapshot JSON")
	packHash := flags.String("expect-hash", "", "optional exact candidate content SHA-256")
	capabilityPath := flags.String("capability-pack", "", "optional capability manifest whose fixture_suite references this suite")
	comparePath := flags.String("compare", "", "baseline manifest or snapshot JSON")
	compareVersion := flags.String("compare-version", "", "exact baseline version")
	compareHash := flags.String("compare-hash", "", "exact baseline content SHA-256")
	if err := flags.Parse(args); err != nil {
		return false, err
	}
	if *packPath == "" || flags.NArg() != 0 {
		return false, errors.New("-pack is required")
	}
	if (*comparePath == "") != (*compareVersion == "" && *compareHash == "") ||
		(*comparePath != "" && (*compareVersion == "" || *compareHash == "")) {
		return false, errors.New("-compare, -compare-version and -compare-hash must be supplied together")
	}
	pack, err := loadPack(*packPath)
	if err != nil {
		return false, err
	}
	if *packHash != "" && pack.Hash() != *packHash {
		return false, errors.New("candidate content hash mismatch")
	}
	if *capabilityPath != "" {
		file, err := os.Open(*capabilityPath)
		if err != nil {
			return false, err
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, capabilitypack.MaxManifestBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return false, readErr
		}
		if closeErr != nil {
			return false, closeErr
		}
		capability, err := capabilitypack.Parse(raw)
		if err != nil {
			return false, err
		}
		manifest := pack.Manifest()
		if manifest.FixtureSuite == nil || capability.FixtureSuite != manifest.FixtureSuite.ID ||
			capability.RulePackRef.ID != manifest.ID || capability.RulePackRef.Version != manifest.Version {
			return false, errors.New("capability fixture_suite or pinned rule pack does not match candidate")
		}
	}
	suite, err := rulepack.RunFixtureSuite(context.Background(), pack)
	if err != nil {
		return false, err
	}
	result := output{Suite: suite}
	if *comparePath != "" {
		baseline, err := loadPack(*comparePath)
		if err != nil {
			return false, err
		}
		if baseline.Manifest().Version != *compareVersion || baseline.Hash() != *compareHash {
			return false, errors.New("baseline version or content hash mismatch")
		}
		comparison, err := rulepack.CompareFixtureSuite(context.Background(), pack, baseline, suite)
		if err != nil {
			return false, err
		}
		result.Comparison = &comparison
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return false, err
	}
	return suite.Passed, nil
}

func main() {
	passed, err := run(os.Args[1:], os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if !passed {
		os.Exit(1)
	}
}
