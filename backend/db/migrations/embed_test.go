package migrations

import (
	"context"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmbeddedMigrationFiles(t *testing.T) {
	migrations, err := migrationFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) == 0 {
		t.Fatal("embedded migration catalog is empty")
	}
}

func TestMigrationFilesAllowsVersionGaps(t *testing.T) {
	source := migrationFixture(
		"000047_cleanup.down.sql", "000001_class.up.sql",
		"000047_cleanup.up.sql", "000001_class.down.sql",
	)
	source["README.md"] = &fstest.MapFile{}
	got, err := migrationFiles(source)
	if err != nil {
		t.Fatal(err)
	}
	want := []migration{
		{version: 1, upFile: "000001_class.up.sql", downFile: "000001_class.down.sql"},
		{version: 47, upFile: "000047_cleanup.up.sql", downFile: "000047_cleanup.down.sql"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migrationFiles() = %#v, want %#v", got, want)
	}
}

func TestMigrationFilesSortsByNumericVersion(t *testing.T) {
	source := migrationFixture("10_later.up.sql", "10_later.down.sql", "2_earlier.up.sql", "2_earlier.down.sql")
	got, err := migrationFiles(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].version != 2 || got[1].version != 10 {
		t.Fatalf("migrationFiles() = %#v, want numeric order 2, 10", got)
	}
}

func TestMigrationFilesRejectsInvalidCatalogBeforeDatabaseAccess(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string
	}{
		{
			name:  "duplicate migration pairs",
			files: []string{"000044_agent.up.sql", "000044_agent.down.sql", "000044_college.up.sql", "000044_college.down.sql"},
			want:  `duplicate migration version 44: down files "000044_agent.down.sql" and "000044_college.down.sql"`,
		},
		{
			name:  "duplicate up without duplicate down",
			files: []string{"000044_agent.up.sql", "000044_agent.down.sql", "000044_college.up.sql"},
			want:  `duplicate migration version 44: up files "000044_agent.up.sql" and "000044_college.up.sql"`,
		},
		{
			name:  "duplicate down without duplicate up",
			files: []string{"000045_cleanup.up.sql", "000045_cleanup.down.sql", "000045_roster.down.sql"},
			want:  `duplicate migration version 45: down files "000045_cleanup.down.sql" and "000045_roster.down.sql"`,
		},
		{
			name:  "numeric aliases still collide",
			files: []string{"000044_agent.up.sql", "000044_agent.down.sql", "44_college.up.sql"},
			want:  `duplicate migration version 44: up files "000044_agent.up.sql" and "44_college.up.sql"`,
		},
		{
			name:  "missing down",
			files: []string{"000044_agent.up.sql"},
			want:  `migration version 44: "000044_agent.up.sql" has no matching down file`,
		},
		{
			name:  "missing up",
			files: []string{"000044_agent.down.sql"},
			want:  `migration version 44: "000044_agent.down.sql" has no matching up file`,
		},
		{
			name:  "unrelated rollback",
			files: []string{"000044_agent.up.sql", "000044_college.down.sql"},
			want:  `migration version 44 has mismatched up/down files: "000044_agent.up.sql" and "000044_college.down.sql"`,
		},
		{
			name:  "missing version prefix",
			files: []string{"agent.up.sql", "agent.down.sql"},
			want:  `migration filename "agent.down.sql" has no numeric prefix`,
		},
		{
			name:  "invalid version prefix",
			files: []string{"invalid_agent.up.sql", "invalid_agent.down.sql"},
			want:  `migration filename "invalid_agent.down.sql":`,
		},
	}
	runners := map[string]func(context.Context, *pgxpool.Pool, fs.FS) error{
		"up":   up,
		"down": downOne,
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := migrationFixture(tt.files...)
			// Earlier valid migrations must not run before a later conflict
			// is discovered, including when the database is otherwise empty.
			source["000001_baseline.up.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
			source["000001_baseline.down.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
			if _, err := migrationFiles(source); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("migrationFiles() error = %v, want %q", err, tt.want)
			}
			for name, run := range runners {
				t.Run(name, func(t *testing.T) {
					// A nil pool panics on Acquire, proving invalid catalogs are
					// rejected before even acquiring a database connection.
					if err := run(context.Background(), nil, source); err == nil || !strings.Contains(err.Error(), tt.want) {
						t.Fatalf("runner error = %v, want %q", err, tt.want)
					}
				})
			}
		})
	}
}

func migrationFixture(names ...string) fstest.MapFS {
	source := make(fstest.MapFS, len(names))
	for _, name := range names {
		source[name] = &fstest.MapFile{Data: []byte("SELECT 1;")}
	}
	return source
}
