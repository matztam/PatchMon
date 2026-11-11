package migrate_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/PatchMon/PatchMon/server-source-code/internal/migrate"
	"github.com/PatchMon/PatchMon/server-source-code/internal/pgtest"
)

// highestMigration returns the largest version number among the embedded files in dir.
func highestMigration(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	highest := 0
	for _, e := range entries {
		n, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err == nil && n > highest {
			highest = n
		}
	}
	return highest
}

// The fork track must keep its own version, so the upstream version stays at
// the newest upstream migration and future upstream migrations still apply.
func TestForkMigrationsUseSeparateTrack(t *testing.T) {
	url := pgtest.URL(t) // already migrated by Run
	d := pgtest.Open(t)
	ctx := context.Background()

	var upstream, fork int
	if err := d.RawQueryRow(ctx, `SELECT version FROM schema_migrations`).Scan(&upstream); err != nil {
		t.Fatal(err)
	}
	if err := d.RawQueryRow(ctx, `SELECT version FROM `+migrate.ForkMigrationsTable).Scan(&fork); err != nil {
		t.Fatal(err)
	}
	if want := highestMigration(t, "migrations"); upstream != want {
		t.Fatalf("upstream version %d, want %d", upstream, want)
	}
	if want := highestMigration(t, "fork_migrations"); fork != want {
		t.Fatalf("fork version %d, want %d", fork, want)
	}

	var tables int
	if err := d.RawQueryRow(ctx, `SELECT COUNT(*) FROM information_schema.tables
		WHERE table_name IN ('host_reboot_state', 'patch_policy_reboot_settings', 'reboot_requests')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 3 {
		t.Fatalf("found %d reboot tables, want 3", tables)
	}

	// Running again is a no-op.
	if err := migrate.Run(url, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("second Run: %v", err)
	}
}
