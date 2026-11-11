package migrate

import (
	"strings"
	"testing"
)

func TestWithMigrationsTable(t *testing.T) {
	got, err := withMigrationsTable("postgres://u:p@h:5432/db?sslmode=disable", "fork_schema_migrations")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "x-migrations-table=fork_schema_migrations") || !strings.Contains(got, "sslmode=disable") {
		t.Fatalf("got %s", got)
	}
}
