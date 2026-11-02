package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"contractaudit/internal/config"
	"contractaudit/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	args := os.Args[1:]
	if len(args) > 0 && strings.HasPrefix(args[0], "-test.") {
		args = nil
	}
	if err := runArgs(ctx, args); err != nil {
		fmt.Fprintf(os.Stderr, "database migration failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	switch os.Getenv("SERVICE_DATABASE_ROLES") {
	case "", "false":
		return nil
	case "true":
		return store.MigrateServiceRoles(ctx)
	default:
		return fmt.Errorf("SERVICE_DATABASE_ROLES must be true or false")
	}
}

func runArgs(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return run(ctx)
	}
	if len(args) == 1 && args[0] == "provision-offline-migrator" {
		return provisionOfflineMigrator(ctx)
	}
	if len(args) == 1 && args[0] == "provision-clm-maintenance" {
		return provisionCLMMaintenanceRole(ctx)
	}
	if len(args) == 1 && args[0] == "provision-tenant-identity" {
		return provisionTenantIdentityOwner(ctx)
	}
	return fmt.Errorf("unknown migrate command; supported commands: provision-offline-migrator, provision-clm-maintenance, provision-tenant-identity")
}

func provisionCLMMaintenanceRole(ctx context.Context) error {
	adminDSN := strings.TrimSpace(os.Getenv("CLM_ADMIN_DATABASE_URL"))
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if adminDSN == "" || migratorDSN == "" {
		return fmt.Errorf("CLM_ADMIN_DATABASE_URL and MIGRATOR_DATABASE_URL are required for maintenance owner provisioning")
	}
	selection, err := config.FromProcess()
	if err != nil {
		return err
	}
	adminIdentity, err := config.ParseDSN(adminDSN)
	if err != nil || adminIdentity != selection.Database {
		return fmt.Errorf("CLM_ADMIN_DATABASE_URL must select the APP_ENV database")
	}
	migratorIdentity, err := config.ParseDSN(migratorDSN)
	if err != nil || migratorIdentity != selection.Database {
		return fmt.Errorf("MIGRATOR_DATABASE_URL must select the APP_ENV database")
	}
	schema := strings.TrimSpace(os.Getenv("DATABASE_SCHEMA"))
	if schema == "" {
		schema = "public"
	}
	return store.ProvisionCLMMaintenanceRole(ctx, adminDSN, migratorDSN, schema)
}

func provisionTenantIdentityOwner(ctx context.Context) error {
	adminDSN := strings.TrimSpace(os.Getenv("AUTH_ADMIN_DATABASE_URL"))
	migratorDSN := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if adminDSN == "" || migratorDSN == "" {
		return fmt.Errorf("AUTH_ADMIN_DATABASE_URL and MIGRATOR_DATABASE_URL are required for AUTH_FENCE owner provisioning")
	}
	selection, err := config.FromProcess()
	if err != nil {
		return err
	}
	adminIdentity, err := config.ParseDSN(adminDSN)
	if err != nil || adminIdentity != selection.Database {
		return fmt.Errorf("AUTH_ADMIN_DATABASE_URL must select the APP_ENV database")
	}
	migratorIdentity, err := config.ParseDSN(migratorDSN)
	if err != nil || migratorIdentity != selection.Database {
		return fmt.Errorf("MIGRATOR_DATABASE_URL must select the APP_ENV database")
	}
	schema := strings.TrimSpace(os.Getenv("DATABASE_SCHEMA"))
	if schema == "" {
		schema = "public"
	}
	return store.ProvisionTenantIdentityOwner(ctx, adminDSN, migratorDSN, schema)
}

func provisionOfflineMigrator(ctx context.Context) error {
	admin := strings.TrimSpace(os.Getenv("AUTH_ADMIN_DATABASE_URL"))
	migrator := strings.TrimSpace(os.Getenv("MIGRATOR_DATABASE_URL"))
	if admin == "" || migrator == "" {
		return fmt.Errorf("AUTH_ADMIN_DATABASE_URL and MIGRATOR_DATABASE_URL are required for offline provisioning")
	}
	selection, err := config.FromProcess()
	if err != nil {
		return err
	}
	for _, raw := range []string{admin, migrator} {
		target, e := config.ParseDSN(raw)
		if e != nil || target != selection.Database {
			return fmt.Errorf("offline provisioning DSNs must select the APP_ENV database")
		}
	}
	schema := strings.TrimSpace(os.Getenv("DATABASE_SCHEMA"))
	if schema == "" {
		schema = "public"
	}
	return store.ProvisionOfflineMigrator(ctx, admin, migrator, schema, strings.TrimSpace(os.Getenv("APP_ENV")) != "" && (selection.Mode == config.ModeTest || selection.Mode == config.ModeDevelopment))
}
