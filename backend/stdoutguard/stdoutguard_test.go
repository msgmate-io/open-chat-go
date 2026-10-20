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
// "Any stdout-writing call" is matched on the *import path*, not on the literal
// "fmt"/"os" identifier, so a file that renames its imports ("import f \"fmt\"",
// a dot import) is still covered. The indirect forms count too:
// fmt.Fprintf(os.Stdout, ...) and os.Stdout.WriteString land in the container
// log exactly like fmt.Print does. Writes that merely look similar stay
// allowed: fmt.Fprintf(os.Stderr, ...) and fmt.Fprint(&buf, ...) are not stdout
// traffic, and neither is handing the os.Stdout file itself to another function.
// The documented limit of a syntax-only guard is that it cannot follow the file
// into a variable ("w := os.Stdout; w.Write(...)").
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
	"strconv"
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

// The standard-library names the guard reasons about: the fmt print helpers
// above and the os.Stdout file, which everything in the tree used to write to.
const (
	fmtPkg    = "fmt"
	stdoutPkg = "os"
	stdoutSel = "Stdout"
)

// importAliases maps every local name a file can use for an imported package to
// the package's import path: the alias when the file names one ("import f "
// fmt""), the last path element otherwise ("json" for encoding/json). "_"
// imports are dropped and "." imports are reported separately, because a dot
// import makes the package's exports bare identifiers in the file.
func importAliases(file *ast.File) (aliases map[string]string, dotFmt, dotOS bool) {
	aliases = map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			name = path[idx+1:]
		}
		if spec.Name != nil {
			switch spec.Name.Name {
			case ".":
				dotFmt = dotFmt || path == fmtPkg
				dotOS = dotOS || path == stdoutPkg
				continue
			case "_":
				continue
			}
			name = spec.Name.Name
		}
		aliases[name] = path
	}
	return aliases, dotFmt, dotOS
}

// stdoutViolations parses one file and returns every stdout-writing call in it.
// The rules are deliberately syntactic:
//   - a call of the fmt print helpers (matched through the import path, so
//     aliased and dot imports count),
//   - any reference to the os.Stdout file at all - writing to it directly
//     (os.Stdout.WriteString), handing it to fmt.Fprintln, or passing it into
//     any other helper,
//   - the builtin print/println (they write to stderr/stdout without going
//     through fmt at all).
//
// The matcher cannot follow os.Stdout into a variable ("w := os.Stdout;
// w.Write(...)") and it treats a local declaration that shadows an imported
// name as that import; both are documented limits of a syntax-only guard.
func stdoutViolations(fset *token.FileSet, file *ast.File) []string {
	aliases, dotFmt, dotOS := importAliases(file)
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			ident, ok := n.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch {
			case aliases[ident.Name] == stdoutPkg && n.Sel.Name == stdoutSel:
				violations = append(violations, fmt.Sprintf("%s: %s.%s is referenced; stdout writes bypass backend/logging",
					fset.Position(n.Pos()).String(), ident.Name, stdoutSel))
			case dotOS && ident.Name == stdoutSel:
				violations = append(violations, fmt.Sprintf("%s: dot-imported %s is referenced; stdout writes bypass backend/logging",
					fset.Position(n.Pos()).String(), stdoutSel))
			}
		case *ast.CallExpr:
			switch fun := n.Fun.(type) {
			case *ast.SelectorExpr:
				ident, ok := fun.X.(*ast.Ident)
				if !ok {
					return true
				}
				if aliases[ident.Name] == fmtPkg && stdoutFmtFuncs[fun.Sel.Name] {
					violations = append(violations, fmt.Sprintf("%s: %s.%s writes to stdout",
						fset.Position(n.Pos()).String(), ident.Name, fun.Sel.Name))
				}
			case *ast.Ident:
				switch {
				case fun.Name == "print" || fun.Name == "println":
					violations = append(violations, fmt.Sprintf("%s: builtin %s writes to stdout",
						fset.Position(n.Pos()).String(), fun.Name))
				case dotFmt && stdoutFmtFuncs[fun.Name]:
					violations = append(violations, fmt.Sprintf("%s: dot-imported fmt.%s writes to stdout",
						fset.Position(n.Pos()).String(), fun.Name))
				}
			}
		}
		return true
	})
	return violations
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

// scanStdoutWriters returns one "path:line: detail" entry for every stdout
// write it finds in the non-test .go files under root, skipping exempt/skip
// directories.
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

		violations = append(violations, stdoutViolations(fset, file)...)
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

// TestStdoutGuardScansTheWholeTree keeps the guard from mistaking a broken walk
// for a clean tree: the scanner and the walk-sharing helper must see the same
// non-exempt .go files, and that set must be non-trivial. A walk that silently
// visited nothing would otherwise pass every stdout check vacuously (ci#645).
func TestStdoutGuardScansTheWholeTree(t *testing.T) {
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

	// Every scanner entry must name a real source file: a matcher that reports
	// a path the walk never visits is as broken as one that visits nothing.
	for _, violation := range scanStdoutWriters(t, root) {
		file := violationFile(violation)
		if !strings.HasSuffix(file, ".go") {
			t.Fatalf("violation %q does not name a source file", violation)
		}
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(file))); statErr != nil {
			t.Fatalf("violation %q names a file the walk never saw: %v", violation, statErr)
		}
	}
}

// violationFile extracts the repo-relative file part of a "path:line: detail"
// entry produced by stdoutViolations.
func violationFile(violation string) string {
	location, _, found := strings.Cut(violation, ": ")
	if !found {
		return violation
	}
	if idx := strings.LastIndex(location, ":"); idx >= 0 {
		return location[:idx]
	}
	return location
}

// TestStdoutViolationsClassifiesSources pins the matcher itself: the forms that
// reach the container log must be flagged and the lookalikes that do not must
// stay quiet, so a widened rule cannot start failing clean code (ci#645).
func TestStdoutViolationsClassifiesSources(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   int
	}{
		{
			name: "bare fmt helpers",
			source: `package p

import "fmt"

func f() { fmt.Print("a"); fmt.Print("*"); fmt.Println("b") }
`,
			want: 3,
		},
		{
			name: "aliased fmt import",
			source: `package p

import f "fmt"

func f2() { f.Printf("%d\n", 1) }
`,
			want: 1,
		},
		{
			name: "dot fmt import",
			source: `package p

import . "fmt"

func f3() { Println("dot") }
`,
			want: 1,
		},
		{
			name: "direct stdout write",
			source: `package p

import "os"

func f4() { _, _ = os.Stdout.WriteString("x") }
`,
			want: 1,
		},
		{
			name: "stdout handed to a printer",
			source: `package p

import (
	"fmt"
	"os"
)

func f5() { fmt.Fprintln(os.Stdout, "x") }
`,
			want: 1,
		},
		{
			name: "builtin println",
			source: `package p

func f6() { println("builtin") }
`,
			want: 1,
		},
		{
			name: "stderr is fine",
			source: `package p

import (
	"fmt"
	"os"
)

func f7() { fmt.Fprintf(os.Stderr, "warn: %v\n", 1) }
`,
			want: 0,
		},
		{
			name: "buffer and builder writers are fine",
			source: `package p

import (
	"bytes"
	"fmt"
	"strings"
)

func f8() {
	var b bytes.Buffer
	fmt.Fprintf(&b, "x")
	fmt.Fprint(&strings.Builder{}, "y")
}
`,
			want: 0,
		},
		{
			name: "the logging sink is fine",
			source: `package p

import "backend/logging"

func f9() { logging.Infof("kept") }
`,
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "fixture.go", tc.source, 0)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			got := stdoutViolations(fset, file)
			if len(got) != tc.want {
				t.Fatalf("stdoutViolations() reported %d violation(s), want %d: %v", len(got), tc.want, got)
			}
		})
	}
}
