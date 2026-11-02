package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"contractaudit/internal/accounts"
	"contractaudit/internal/config"
	"contractaudit/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "identity import failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("identity-import", flag.ContinueOnError)
	path := flags.String("file", "", "explicit identity crosswalk JSON file")
	apply := flags.Bool("apply", false, "apply the validated crosswalk; omit for dry-run")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*path) == "" {
		return errorsUsage()
	}
	adminDSN := strings.TrimSpace(os.Getenv("AUTH_ADMIN_DATABASE_URL"))
	if adminDSN == "" {
		return fmt.Errorf("AUTH_ADMIN_DATABASE_URL is required for the offline identity import")
	}
	directory := strings.TrimSpace(os.Getenv("IDENTITY_DIRECTORY_FILE"))
	if directory == "" {
		return fmt.Errorf("IDENTITY_DIRECTORY_FILE is required to verify each explicit actor/issuer/subject binding")
	}
	if err := accounts.ConfigureDirectory(directory); err != nil {
		return fmt.Errorf("load identity directory for exact crosswalk validation: %w", err)
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		return fmt.Errorf("read identity crosswalk file: %w", err)
	}
	document, err := store.DecodeIdentityCrosswalk(raw)
	if err != nil {
		return err
	}
	selection, err := config.FromProcess()
	if err != nil {
		return err
	}
	schema := strings.TrimSpace(os.Getenv("DATABASE_SCHEMA"))
	if schema == "" {
		schema = "public"
	}
	summary, err := store.ImportIdentityCrosswalk(ctx, adminDSN, schema, selection.Database.Name, document, *apply)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	result := struct {
		DryRun    bool                        `json:"dry_run"`
		SourceSHA string                      `json:"source_sha256"`
		Summary   store.IdentityImportSummary `json:"summary"`
	}{DryRun: !*apply, SourceSHA: hex.EncodeToString(digest[:]), Summary: summary}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func errorsUsage() error {
	return fmt.Errorf("usage: go run ./cmd/identity-import --file <explicit-crosswalk.json> [--apply]; default is dry-run")
}
