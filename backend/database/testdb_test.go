package database

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestSetupTestDatabaseClosesPoolWhenTestEnds pins the ordering the helper
// exists for (ci#920): the pool close is registered after t.TempDir, so it runs
// first and RemoveAll finds a quiesced directory instead of racing the WAL
// sidecars.
func TestSetupTestDatabaseClosesPoolWhenTestEnds(t *testing.T) {
	var sqlDB *sql.DB

	t.Run("inner", func(t *testing.T) {
		DB := SetupTestDatabase(t, DBConfig{
			Backend:  "sqlite",
			FilePath: filepath.Join(t.TempDir(), "setup_test_database.db"),
			ResetDB:  true,
		})
		if DB == nil {
			t.Fatal("expected a database handle")
		}

		var err error
		sqlDB, err = DB.DB()
		if err != nil {
			t.Fatalf("failed to unwrap sql.DB: %v", err)
		}
		if err := sqlDB.Ping(); err != nil {
			t.Fatalf("pool should be open inside the test: %v", err)
		}
	})

	if err := sqlDB.Ping(); err == nil {
		t.Fatal("expected the pool to be closed once the test ended (ci#920)")
	}
}

// TestCloseTestDBToleratesASiteClosingItsOwnPool documents that a site which
// closes its pool explicitly can still register the helper: database/sql Close
// is idempotent, so there is no double-close error.
func TestCloseTestDBToleratesASiteClosingItsOwnPool(t *testing.T) {
	DB := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "close_test_db.db"),
		ResetDB:  true,
	})

	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatalf("failed to unwrap sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("second close must be a no-op, got: %v", err)
	}
}

// poolOpenCalls are the ways a Go test in this module opens a SQLite pool
// without going through SetupTestDatabase. Each one has to be paired with a
// registered pool close (see the ci#920 note in DEVELOPMENT.md).
var poolOpenCalls = []string{"SetupDatabase(", "gorm.Open(", "openSQLite("}

// poolCloseCalls are the accepted ways to make sure the pool does not outlive
// the test: the shared helpers.
var poolCloseCalls = []string{"SetupTestDatabase(", "CloseTestDB("}

// unwrappedPoolCloseCalls are the fallback for a site that needs the *sql.DB
// handle for something else (holding a write lock, asserting a pragma): it has
// to unwrap the pool and close it itself. Both halves are required, so an
// unrelated `.Close()` on some other handle cannot mask a leaked pool - which is
// exactly how `api/chats/browser_token_access_test.go` slipped the first sweep.
var unwrappedPoolCloseCalls = []string{".DB()", ".Close()"}

// knownTempDirPoolViolations carries the files that still bind t.TempDir() to a
// SQLite pool without a registered close, keyed to the PR that owns the fix.
// The guard stays green while another PR is in flight, but it cannot be used as
// a permanent hiding place: TestTempDirSQLiteHelpersCloseTheirPool fails on an
// entry that no longer violates, so whoever lands the fix is told to drop it.
var knownTempDirPoolViolations = map[string]string{
	"backend/api/chats/SharedChatBadge_test.go":      "#919",
	"backend/api/chats/browser_token_access_test.go": "#919",
	"backend/api/chats/create_test.go":               "#919",
}

// TestTempDirSQLiteHelpersCloseTheirPool is the ci#920 guard: it is the reason
// the audit cannot silently rot. Every *_test.go in the backend module that
// points SQLite at t.TempDir() and opens a pool itself must also reference one
// of the pool-close forms, so a new helper copied from an old one fails here
// instead of flaking CI with "TempDir RemoveAll cleanup: directory not empty".
//
// It is deliberately a source scan rather than a runtime check: the failure mode
// is a cleanup-ordering race that only shows up intermittently, so a static
// predicate is what actually holds the line.
func TestTempDirSQLiteHelpersCloseTheirPool(t *testing.T) {
	offenders := scanForTempDirPoolsWithoutClose(t, filepath.Join("..", ".."))

	var unexpected []string
	for _, path := range offenders {
		if _, tolerated := knownTempDirPoolViolations[path]; !tolerated {
			unexpected = append(unexpected, path)
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("these test files bind t.TempDir() to a SQLite pool without registering a close (ci#920):\n\t%s\n"+
			"use database.SetupTestDatabase(t, cfg) instead of database.SetupDatabase(cfg), or call "+
			"database.CloseTestDB(t, DB) right after gorm.Open/openSQLite",
			strings.Join(unexpected, "\n\t"))
	}

	// An exception for a file that has since been fixed is dead weight and hides
	// the next regression in that file, so it has to go with the fix.
	found := make(map[string]bool, len(offenders))
	for _, path := range offenders {
		found[path] = true
	}
	for path, owner := range knownTempDirPoolViolations {
		if found[path] {
			continue
		}
		t.Errorf("knownTempDirPoolViolations still lists %s (%s), but it no longer leaks a pool; "+
			"remove the entry", path, owner)
	}
}

// scanForTempDirPoolsWithoutClose returns the workspace-relative paths of every
// *_test.go file under root that opens a SQLite pool over t.TempDir() without
// referencing an accepted pool-close form.
func scanForTempDirPoolsWithoutClose(t *testing.T, root string) []string {
	t.Helper()

	var offenders []string
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", "frontend", "dist", "build":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source := string(content)
		if !strings.Contains(source, "TempDir") || !containsAny(source, poolOpenCalls) {
			return nil
		}
		if containsAny(source, poolCloseCalls) || containsAll(source, unwrappedPoolCloseCalls) {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			relative = path
		}
		offenders = append(offenders, filepath.ToSlash(relative))
		return nil
	})
	if walkErr != nil {
		t.Fatalf("failed to scan %s for TempDir SQLite helpers: %v", root, walkErr)
	}

	sort.Strings(offenders)
	return offenders
}

func containsAny(source string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(source, needle) {
			return true
		}
	}
	return false
}

// TestScanForTempDirPoolsWithoutCloseClassifiesFixtures pins the guard's
// predicate against a synthetic tree, so the scanner cannot quietly stop
// detecting the very shape it was written for (ci#920).
func TestScanForTempDirPoolsWithoutCloseClassifiesFixtures(t *testing.T) {
	root := t.TempDir()

	fixtures := map[string]string{
		// Leaks: opens the pool over t.TempDir() and never closes it.
		"leak_test.go": `package fixtures

import (
	"path/filepath"
	"testing"
)

func TestLeak(t *testing.T) {
	DB := database.SetupDatabase(database.DBConfig{
		FilePath: filepath.Join(t.TempDir(), "leak.db"),
	})
	_ = DB
}
`,
		// Masks: the only `.Close()` is on an unrelated handle, so the pool
		// still outlives the test. This is the api/chats shape.
		"masked_test.go": `package fixtures

import (
	"path/filepath"
	"testing"
)

func TestMasked(t *testing.T) {
	DB := database.SetupDatabase(database.DBConfig{
		FilePath: filepath.Join(t.TempDir(), "masked.db"),
	})
	_ = DB
	t.Cleanup(func() { _ = inspector.Close() })
}
`,
		// Fine: the shared helper registers the close.
		"helper_test.go": `package fixtures

import (
	"path/filepath"
	"testing"
)

func TestHelper(t *testing.T) {
	_ = database.SetupTestDatabase(t, database.DBConfig{
		FilePath: filepath.Join(t.TempDir(), "helper.db"),
	})
}
`,
		// Fine: unwraps the pool and closes it itself.
		"unwrapped_test.go": `package fixtures

import (
	"path/filepath"
	"testing"
)

func TestUnwrapped(t *testing.T) {
	DB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "unwrapped.db")))
	if err != nil {
		t.Fatal(err)
	}
	pool, err := DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
}
`,
		// Irrelevant: a TempDir test with no pool at all.
		"nopool_test.go": `package fixtures

import "testing"

func TestNoPool(t *testing.T) {
	_ = t.TempDir()
}
`,
	}
	for name, content := range fixtures {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatalf("failed to write fixture %s: %v", name, err)
		}
	}

	got := scanForTempDirPoolsWithoutClose(t, root)
	want := []string{"leak_test.go", "masked_test.go"}
	if len(got) != len(want) {
		t.Fatalf("scanner flagged %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scanner flagged %v, want %v", got, want)
		}
	}
}

func containsAll(source string, needles []string) bool {
	for _, needle := range needles {
		if !strings.Contains(source, needle) {
			return false
		}
	}
	return true
}
