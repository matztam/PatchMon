package migrate

// fork: matztam/PatchMon keeps its own schema changes in a second migration
// track with its own version table. golang-migrate stores a single version per
// table, so numbering fork migrations into the upstream track would make the
// next upstream migration look already applied and get skipped.

import (
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed fork_migrations/*.sql
var forkMigrationsFS embed.FS

// ForkMigrationsTable is the version table of the fork migration track.
const ForkMigrationsTable = "fork_schema_migrations"

// Run applies the upstream migrations, then the fork migrations.
func Run(databaseURL string, log *slog.Logger) error {
	if err := runUpstream(databaseURL, log); err != nil {
		return err
	}
	return runFork(databaseURL, log)
}

func runFork(databaseURL string, log *slog.Logger) error {
	forkURL, err := withMigrationsTable(EnsureSSLMode(databaseURL), ForkMigrationsTable)
	if err != nil {
		return errors.New("fork migrations: invalid database URL")
	}
	source, err := iofs.New(forkMigrationsFS, "fork_migrations")
	if err != nil {
		return fmt.Errorf("create embedded fork migrate source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, forkURL)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return errors.New("create fork migrate instance: invalid database URL")
		}
		return fmt.Errorf("create fork migrate instance: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		fmt.Fprintf(os.Stderr, "[migrate] fork migrations failed: %v\n", err)
		return fmt.Errorf("fork migration up: %w", err)
	} else if err == nil {
		version, _, _ := m.Version()
		_, _ = fmt.Fprintf(os.Stdout, "[migrate] fork migrations applied (version %d)\n", version)
		log.Info("fork migrations applied", "version", version)
	}
	return nil
}

// withMigrationsTable sets golang-migrate's x-migrations-table parameter.
func withMigrationsTable(databaseURL, table string) (string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("x-migrations-table", table)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
