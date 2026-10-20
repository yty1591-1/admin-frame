package migrate_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/admin-frame/admin-frame/pkg/migrate"
)

func TestParseName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		wantVer int64
		wantDsc string
		wantDir migrate.Direction
		wantErr bool
	}{
		{name: "000001_create_orders.up.sql", wantVer: 1, wantDsc: "create_orders", wantDir: migrate.DirectionUp},
		{name: "000042_add_idx.down.sql", wantVer: 42, wantDsc: "add_idx", wantDir: migrate.DirectionDown},
		{name: "1_bad.up.sql", wantErr: true},
		{name: "000000_zero.up.sql", wantErr: true},
		{name: "000001_Create.up.sql", wantErr: true},
		{name: "000001_cr..eate.up.sql", wantErr: true},
		{name: "000001_create.sql", wantErr: true},
		{name: "000001_create.up.sql.bak", wantErr: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			version, desc, direction, err := migrate.ParseName(tc.name)
			if tc.wantErr {
				if !errors.Is(err, migrate.ErrInvalidSource) {
					t.Fatalf("err=%v, want ErrInvalidSource", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if version != tc.wantVer || desc != tc.wantDsc || direction != tc.wantDir {
				t.Fatalf("got (%d, %q, %q), want (%d, %q, %q)", version, desc, direction, tc.wantVer, tc.wantDsc, tc.wantDir)
			}
		})
	}
}

func TestLoadDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	up1 := "CREATE TABLE orders (id BIGINT PRIMARY KEY);"
	down1 := "DROP TABLE orders;"
	up2 := "-- +migrate no-transaction\nCREATE INDEX CONCURRENTLY idx_orders ON orders (id);"
	down2 := "DROP INDEX CONCURRENTLY idx_orders;"
	writeMigration(t, dir, "000002_add_index.up.sql", up2)
	writeMigration(t, dir, "000001_create_orders.down.sql", down1)
	writeMigration(t, dir, "000002_add_index.down.sql", down2)
	writeMigration(t, dir, "000001_create_orders.up.sql", up1)

	migs, err := migrate.LoadDir(dir, migrate.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(migs) != 2 {
		t.Fatalf("len=%d, want 2", len(migs))
	}
	if migs[0].Version != 1 || migs[0].Description != "create_orders" || migs[0].Down != down1 {
		t.Fatalf("unexpected first migration: %+v", migs[0])
	}
	if migs[0].NoTx {
		t.Fatal("first migration should be transactional")
	}
	if migs[1].Version != 2 || migs[1].Description != "add_index" || !migs[1].NoTx {
		t.Fatalf("unexpected second migration: %+v", migs[1])
	}
	sum := sha256.Sum256([]byte(up1))
	if want := hex.EncodeToString(sum[:]); migs[0].Checksum != want {
		t.Fatalf("checksum=%s, want %s", migs[0].Checksum, want)
	}
}

func TestLoadFS(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"db/migrations/000001_init.up.sql":   &fstest.MapFile{Data: []byte("SELECT 1;")},
		"db/migrations/000001_init.down.sql": &fstest.MapFile{Data: []byte("SELECT 2;")},
		"db/migrations/README.md":            &fstest.MapFile{Data: []byte("ignored")},
	}
	migs, err := migrate.LoadFS(fsys, "db/migrations", migrate.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	if len(migs) != 1 || migs[0].Version != 1 {
		t.Fatalf("migs=%+v", migs)
	}
}

func TestLoadRejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		files   map[string]string
		opts    migrate.LoadOptions
		wantErr error
	}{
		{
			name:    "missing down",
			files:   map[string]string{"000001_create.up.sql": "SELECT 1;"},
			wantErr: migrate.ErrMissingDown,
		},
		{
			name:    "orphan down",
			files:   map[string]string{"000001_create.down.sql": "SELECT 1;"},
			wantErr: migrate.ErrInvalidSource,
		},
		{
			name: "mismatched description",
			files: map[string]string{
				"000001_create.up.sql": "SELECT 1;",
				"000001_drop.down.sql": "SELECT 2;",
			},
			wantErr: migrate.ErrInvalidSource,
		},
		{
			name: "empty up",
			files: map[string]string{
				"000001_create.up.sql":   "  \n\t\n",
				"000001_create.down.sql": "SELECT 2;",
			},
			wantErr: migrate.ErrInvalidSource,
		},
		{
			name:    "bad file name",
			files:   map[string]string{"000001create.up.sql": "SELECT 1;"},
			wantErr: migrate.ErrInvalidSource,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{}
			for name, body := range tc.files {
				fsys["migrations/"+name] = &fstest.MapFile{Data: []byte(body)}
			}
			_, err := migrate.LoadFS(fsys, "migrations", tc.opts)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestLoadFSRejectsEscapingDir(t *testing.T) {
	t.Parallel()
	if _, err := migrate.LoadFS(fstest.MapFS{}, "../etc", migrate.LoadOptions{}); !errors.Is(err, migrate.ErrInvalidSource) {
		t.Fatalf("err=%v, want ErrInvalidSource", err)
	}
}

func TestLoadAllowMissingDown(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"migrations/000001_create.up.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}
	migs, err := migrate.LoadFS(fsys, "migrations", migrate.LoadOptions{AllowMissingDown: true})
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	if len(migs) != 1 || migs[0].Down != "" {
		t.Fatalf("migs=%+v", migs)
	}
}

func TestValidateMigrations(t *testing.T) {
	t.Parallel()
	valid := []migrate.Migration{
		{Version: 1, Description: "a", Up: "SELECT 1;"},
		{Version: 2, Description: "b", Up: "SELECT 2;"},
	}
	if err := migrate.ValidateMigrations(valid); err != nil {
		t.Fatalf("valid: %v", err)
	}
	cases := [][]migrate.Migration{
		{{Version: 0, Description: "a", Up: "x"}},
		{{Version: 1, Description: "", Up: "x"}},
		{{Version: 1, Description: "a", Up: " "}},
		{{Version: 1, Description: "a", Up: "x"}, {Version: 1, Description: "a", Up: "y"}},
		{{Version: 2, Description: "a", Up: "x"}, {Version: 1, Description: "b", Up: "y"}},
	}
	for i, migs := range cases {
		if err := migrate.ValidateMigrations(migs); !errors.Is(err, migrate.ErrInvalidSource) {
			t.Fatalf("case %d: err=%v, want ErrInvalidSource", i, err)
		}
	}
}

func writeMigration(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
