package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"contractaudit/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
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
