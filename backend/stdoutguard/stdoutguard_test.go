package stdoutguard

// Guard for the ci#645 "reduce log heaviness" work: server code must not write
// to stdout directly. Bare fmt.Print*/println calls and os.Stdout writes bypass
// backend/logging, so they ignore LOG_LEVEL/LOG_FORMAT and cannot be quieted
// down on a busy deployment (docker-compose.ci.yaml pins LOG_LEVEL=info). They
// also dump raw payloads - provider response bodies, full message arrays, every
// registered tool definition, every serving route - into the container log on
// every turn.
//
// Everything that used to do that now goes through logging.Debugf/Infof, which
// the deployment can switch off with LOG_LEVEL=info (see backend/logging). This
// test keeps the tree from drifting back: it parses the non-test sources and
// fails on any stdout-writing call, so a new debug print has to pick a level
// instead of reaching for fmt.Print.
//
// Three trees are deliberately exempt:
//   - backend/logging: the log sink itself defaults to os.Stdout.
//   - backend/cmd: process entrypoints wire child stdin/stdout/stderr.
//   - backend/scripts: one-shot report tools whose whole job is to print.
//
// The test is intentionally cheap: no build, no network, just go/parser over the
// source tree, so it can run in the default Go suite.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// exemptDirs are the directories (relative to backend/) whose non-test sources
// may write to stdout: the log sink, the process entrypoints and the report
// scripts.
var exemptDirs = []string{
	"logging",
	"cmd",
	"scripts",
}

// skipDirs are directories never worth descending into.
var skipDirs = map[string]bool{
	"vendor":       true,
	"node_modules": true,
	"testdata":     true,
}

// stdoutFmtFuncs are the fmt helpers that write to os.Stdout.
var stdoutFmtFuncs = map[string]bool{
	"Print":   true,
	"Printf":  true,
	"Println": true,
}

// backendRoot walks up from a directory until it finds the backend/ tree (the
// directory that holds go.mod next to a cmd/ tree).
func backendRoot(t *testing.T, start string) string {
	t.Helper()
	dir, err := filepath.Abs(start)
	if err != nil {
		t.Fatalf("resolve %s: %v", start, err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "cmd")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no backend tree above %s", start)
		}
		dir = parent
	}
}

func exempt(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, dir := range exemptDirs {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// scanStdoutWriters returns "path:line: detail" for every stdout-writing call it
// finds in the non-test .go files under root, skipping exempt/skip directories.
func scanStdoutWriters(t *testing.T, root string) []string {
	t.Helper()
	fset := token.NewFileSet()
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if exempt(rel) {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			violations = append(violations, fmt.Sprintf("%s: parse: %v", filepath.ToSlash(rel), parseErr))
			return nil
		}

		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch {
			case pkg.Name == "fmt" && stdoutFmtFuncs[sel.Sel.Name]:
				violations = append(violations, fmt.Sprintf("%s: %s.%s writes to stdout",
					fset.Position(call.Pos()).String(), pkg.Name, sel.Sel.Name))
			case pkg.Name == "os" && sel.Sel.Name == "Stdout":
				violations = append(violations, fmt.Sprintf("%s: os.Stdout write",
					fset.Position(call.Pos()).String()))
			}
			return true
		})

		// The builtin println/print write to stderr/stdout and are invisible to
		// the selector walk above (they are plain idents).
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for lineNo, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			for _, builtin := range []string{"println", "print"} {
				if !strings.HasPrefix(trimmed, builtin+"(") {
					continue
				}
				violations = append(violations, fmt.Sprintf("%s:%d: builtin %s writes to stdout",
					filepath.ToSlash(rel), lineNo+1, builtin))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(violations)
	return violations
}

// TestNoStdoutWritesInBackendServerCode fails when a non-exempt source file
// writes to stdout, because such a call cannot be silenced by LOG_LEVEL and is
// exactly the log heaviness ci#645 tracks (ci#645).
func TestNoStdoutWritesInBackendServerCode(t *testing.T) {
	root := backendRoot(t, ".")
	violations := scanStdoutWriters(t, root)
	if len(violations) == 0 {
		return
	}
	t.Errorf("%d stdout-writing call(s) outside %v; route them through backend/logging (ci#645):\n\t%s",
		len(violations), exemptDirs, strings.Join(violations, "\n\t"))
}

// TestStdoutGuardScansFiles keeps the guard honest: a scanner that accidentally
// walks nothing would pass vacuously, so assert it actually sees the tree.
func TestStdoutGuardScansFiles(t *testing.T) {
	root := backendRoot(t, ".")
	scanned := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if exempt(rel) {
			return nil
		}
		scanned++
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if scanned < 100 {
		t.Fatalf("guard scanned only %d files under %s; the walk is broken, not the tree", scanned, root)
	}
}
