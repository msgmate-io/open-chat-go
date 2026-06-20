package database

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// packageCloseHelperPattern is the third accepted form: a package-local close
// helper such as `closeChatsTestDB(t, DB)`, which is how the api/chats fix
// registers the pool close. Those files never call CloseTestDB themselves, so
// without this the guard would keep flagging them *after* their fix lands - the
// guard has to agree with the fix it is policing.
//
// It is a raw-text match like every other marker here, so it fires on the
// helper's *declaration* even if nothing calls it, and a literal spelling the
// name out would fake it. That is the per-file hole documented below, with one
// more accepted token - not a narrower guarantee than the rest.
var packageCloseHelperPattern = regexp.MustCompile(`close[A-Za-z0-9_]*TestDB\(`)

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
//
// The three `api/chats` entries (#919) were removed by the r56 consolidation:
// that PR merged into the same branch as this guard, and once its package-local
// `closeChatsTestDB` helper was on the tree those files no longer violate, so
// the exceptions had to go with the fix - which is exactly what this guard
// enforces. The map stays here (empty) as the only escape hatch.
var knownTempDirPoolViolations = map[string]string{}

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
//
// The predicate is deliberately per *file*: one accepted close marker exempts
// the whole file, so a file that migrates one helper to SetupTestDatabase while
// leaving a bare SetupDatabase in another helper is not caught. That is a known
// hole, not an oversight - closing it needs a per-function scan and
// string-literal stripping (the fixtures below embed the open calls as literals,
// so counting occurrences would flag this file). See the ci#920 note in
// DEVELOPMENT.md before tightening it.
//
// Vendored/private checkouts are skipped (dot-dirs, `clients/`, `ci/`, plus the
// build dirs): their tests belong to another repository and a failure there is
// not fixable from here.
func scanForTempDirPoolsWithoutClose(t *testing.T, root string) []string {
	t.Helper()

	var offenders []string
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// Vendored/private checkouts are not part of the public repo and
			// not ours to police - the same set docguard skips. `clients/`
			// and `.integrations/` hold the gitignored integration checkouts
			// `openchat-integrations sync` materializes, `ci/` is the private
			// profile checkout, and the dot-dirs cover the caches. CI flagged
			// 15 files under `clients/integrations/` before this: the public
			// module cannot fix a private integration's tests, so scanning
			// them only made the guard red for everyone who had synced.
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			switch entry.Name() {
			case "node_modules", "vendor", "frontend", "dist", "build",
				"clients", "ci":
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
		if containsAny(source, poolCloseCalls) || packageCloseHelperPattern.MatchString(source) || containsAll(source, unwrappedPoolCloseCalls) {
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
		// Fine: the api/chats fix shape - a package-local close helper, with no
		// direct SetupTestDatabase/CloseTestDB call in this file.
		"pkghelper_test.go": `package fixtures

import (
	"path/filepath"
	"testing"
)

func TestPkgHelper(t *testing.T) {
	DB := database.SetupDatabase(database.DBConfig{
		FilePath: filepath.Join(t.TempDir(), "pkghelper.db"),
	})
	closeFixturesTestDB(t, DB)
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

	// The same leaking shape under a vendored/private checkout must be skipped:
	// those tests live in another repository, so flagging them would leave the
	// guard permanently red for anyone who ran `openchat-integrations sync`.
	vendored := []string{
		filepath.Join("clients", "integrations", "mcp_integration", "bootstrap_test.go"),
		filepath.Join("ci", "integration", "leak_test.go"),
		filepath.Join(".integrations", "cached_test.go"),
	}
	for _, rel := range vendored {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("failed to create %s: %v", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(path, []byte(fixtures["leak_test.go"]), 0o600); err != nil {
			t.Fatalf("failed to write fixture %s: %v", rel, err)
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

// asyncDBWriterHandshakeMarkers are the accepted ways a non-test function that
// outlives its caller may reach a SQLite pool: it registers the work with the
// shared tracker (database.RegisterDetachedWork, drained by CloseTestDB) or it
// waits for the pool to go quiescent before using it. Anything else is a bare
// goroutine the ci#920 pool check cannot see, so the teardown can close the pool
// and let t.TempDir RemoveAll race a late write (ci#811).
var asyncDBWriterHandshakeMarkers = []string{
	"RegisterDetachedWork(",
	"DrainRegisteredDetachedWork(",
	"WaitForTestDBQuiescent(",
}

// knownAsyncDBWriterViolations is the debt list of files whose detached DB work
// is drained by their own stop function (cancel + <-done) instead of the shared
// tracker. An entry that stops matching fails the test, so the list cannot rot.
var knownAsyncDBWriterViolations = map[string]string{
	"backend/database/session_cleanup.go":  "session sweeper is stopped by its own stop func (cancel + <-done)",
	"backend/database/session_lifetime.go": "session refresh is stopped by its own stop func (cancel + <-done)",
}

var asyncDBGoroutinePattern = regexp.MustCompile(`^\s*go\s+\S`)

var dbHandleReferencePattern = regexp.MustCompile(`\bDB\b|gorm\.DB|sql\.DB|\.DB\b|\bdb\b`)

// hasBareDBGoroutine reports whether source starts a goroutine (a `go`
// statement, not a tracker call) whose statement or closure body mentions a
// database handle.
func hasBareDBGoroutine(source string) bool {
	lines := strings.Split(source, "\n")
	for i := 0; i < len(lines); i++ {
		if !asyncDBGoroutinePattern.MatchString(lines[i]) {
			continue
		}
		fragment := lines[i]
		depth := strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
		for j := i + 1; depth > 0 && j < len(lines); j++ {
			fragment += "\n" + lines[j]
			depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
		}
		if dbHandleReferencePattern.MatchString(fragment) {
			return true
		}
	}
	return false
}

// dirHasTempDirPoolTest reports whether dir holds a test file that points SQLite
// at t.TempDir(), i.e. a package the ci#811 teardown protects.
func dirHasTempDirPoolTest(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		source := string(content)
		if strings.Contains(source, "TempDir") && containsAny(source, poolOpenCalls) {
			return true
		}
	}
	return false
}

// scanForTempDirPackagesWithDetachedDBWriters returns the workspace-relative
// non-test files of every package with a TempDir SQLite pool test that start a
// goroutine touching a DB handle without an accepted handshake.
func scanForTempDirPackagesWithDetachedDBWriters(t *testing.T, root string) []string {
	t.Helper()

	qualified := map[string]bool{}
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && strings.HasPrefix(entry.Name(), ".") {
			return fs.SkipDir
		}
		switch entry.Name() {
		case "node_modules", "vendor", "frontend", "dist", "build", "clients", "ci":
			return fs.SkipDir
		}
		if dirHasTempDirPoolTest(path) {
			qualified[path] = true
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("failed to scan %s for detached DB writers: %v", root, walkErr)
	}

	var offenders []string
	for dir := range qualified {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("failed to list %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			full := filepath.Join(dir, name)
			content, err := os.ReadFile(full)
			if err != nil {
				t.Fatalf("failed to read %s: %v", full, err)
			}
			source := string(content)
			if !hasBareDBGoroutine(source) || containsAny(source, asyncDBWriterHandshakeMarkers) {
				continue
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				rel = full
			}
			offenders = append(offenders, filepath.ToSlash(rel))
		}
	}
	sort.Strings(offenders)
	return offenders
}

// TestTempDirPackagesHaveNoUntrackedDBWriters is the ci#811 guard: a package
// whose tests point SQLite at t.TempDir() must not start DB work in a bare
// goroutine the teardown cannot drain, because CloseTestDB would close the pool
// and the t.TempDir RemoveAll would then race the late write ("directory not
// empty").
func TestTempDirPackagesHaveNoUntrackedDBWriters(t *testing.T) {
	offenders := scanForTempDirPackagesWithDetachedDBWriters(t, filepath.Join("..", ".."))
	found := map[string]bool{}
	for _, path := range offenders {
		found[path] = true
		if _, known := knownAsyncDBWriterViolations[path]; !known {
			t.Errorf("ci#811: %s starts a goroutine that touches the SQLite pool without registering the work "+
				"(database.RegisterDetachedWork / WaitForTestDBQuiescent), so the test teardown can close the pool "+
				"and remove its t.TempDir while the write is still in flight", path)
		}
	}
	for path, owner := range knownAsyncDBWriterViolations {
		if !found[path] {
			t.Errorf("knownAsyncDBWriterViolations still lists %s (%s), but it no longer detaches DB work; "+
				"remove the entry", path, owner)
		}
	}
}

// TestScanForTempDirPackagesWithDetachedDBWritersClassifiesFixtures pins the
// ci#811 predicate against a synthetic tree so the guard cannot silently stop
// recognising the shape it was written for.
func TestScanForTempDirPackagesWithDetachedDBWritersClassifiesFixtures(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Both packages below look like a TempDir SQLite pool test package.
	poolTest := "package pkg\n\n" +
		"import (\n" +
		"\t\"path/filepath\"\n" +
		"\t\"testing\"\n" +
		")\n\n" +
		"func newDB(t *testing.T) *gorm.DB {\n" +
		"\t_ = filepath.Join(t.TempDir(), \"test.sqlite\")\n" +
		"\treturn SetupDatabase(t)\n" +
		"}\n"
	write("leak/leak_test.go", poolTest)
	write("tracked/tracked_test.go", poolTest)
	// Leak: bare goroutine writing through the pool, no handshake anywhere.
	write("leak/handler.go", "package pkg\n\n"+
		"func handler(DB *gorm.DB) {\n"+
		"\tgo func() {\n"+
		"\t\tDB.Exec(\"update chats set name = ?\", \"x\")\n"+
		"\t}()\n"+
		"}\n")
	// Tracked: the goroutine goes through the registered group.
	write("tracked/handler.go", "package pkg\n\n"+
		"var detached = RegisterDetachedWork(&DetachedWork{})\n\n"+
		"func handler(DB *gorm.DB) {\n"+
		"\tdetached.Go(func() {\n"+
		"\t\tDB.Exec(\"update chats set name = ?\", \"x\")\n"+
		"\t})\n"+
		"}\n")

	offenders := scanForTempDirPackagesWithDetachedDBWriters(t, root)
	if len(offenders) != 1 || offenders[0] != "leak/handler.go" {
		t.Fatalf("ci#811: scanner classified %v, want exactly [leak/handler.go]", offenders)
	}
}
