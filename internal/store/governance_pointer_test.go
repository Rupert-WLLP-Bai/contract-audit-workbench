package store

import (
	"strings"
	"testing"
)

func TestPostgresReturningOldNewSelectsTheServerCapability(t *testing.T) {
	if postgresReturningOldNew(160009) || postgresReturningOldNew(170009) {
		t.Fatal("PostgreSQL 16 and 17 cannot return OLD and NEW from one statement")
	}
	if !postgresReturningOldNew(180000) || !postgresReturningOldNew(180006) {
		t.Fatal("PostgreSQL 18 must return OLD and NEW from the pointer statement")
	}
	upsert := rulePackPointerUpsertSQL(true)
	disable := rulePackPointerDisableSQL(true)
	for _, statement := range []string{upsert, disable} {
		if !strings.Contains(statement, "RETURNING old.version, old.active, new.version, new.active") {
			t.Fatalf("PostgreSQL 18 pointer SQL lost the OLD/NEW projection: %s", statement)
		}
	}
	for _, statement := range []string{rulePackPointerUpsertSQL(false), rulePackPointerDisableSQL(false)} {
		if strings.Contains(statement, "old.") || strings.Contains(statement, "new.") {
			t.Fatalf("older PostgreSQL pointer SQL used OLD/NEW syntax: %s", statement)
		}
		if !strings.Contains(statement, "RETURNING version, active") {
			t.Fatalf("older PostgreSQL pointer SQL lost the post-image: %s", statement)
		}
	}
}
