package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// bootSourceFiles are the boot paths that arm the self-message schedules.
var bootSourceFiles = []string{"server.go", "worker.go"}

// ci#747 RC1: the self-message scheduler only keeps an idle interaction alive
// when the boot path actually arms it. The scheduler shipped in #852 was dead
// code (no caller), so `OPEN_CHAT_SELF_MESSAGE_SCHEDULES` was never read and no
// actor was ever scheduled. This guard fails loudly if that wiring is dropped
// again, which is exactly the regression that made the lead idle forever.
func TestServerBootArmsSelfMessageSchedules(t *testing.T) {
	for _, name := range bootSourceFiles {
		boot := readBootSource(t, name)
		if !strings.Contains(boot, "msgmate.StartSelfMessageRuntime(") {
			t.Errorf("backend/cmd/%s no longer starts the self-message runtime: an idle interaction would never self-resume (ci#747 RC1)", name)
		}
		if !strings.Contains(boot, "msgmate.SelfMessageScheduleEnvVar") {
			t.Errorf("backend/cmd/%s no longer reports the configured schedules env var (ci#747 RC1)", name)
		}
		if strings.Contains(boot, "self-message scheduler failed to start: %w") {
			t.Errorf("backend/cmd/%s aborts the boot when the configured self-message bot is unknown (ci#785)", name)
		}
	}

	// ci#785 / ci#744: exactly one start per boot path. r57 shipped a second,
	// unconditional start in server.go whose `if err != nil { return err }`
	// aborted the boot of every GitHub Actions deployment (whose database has no
	// configured bot user), so the HTTP port was never opened. A second call is
	// how that regression looks; a fatal error return is how it behaves.
	if got := strings.Count(readBootSource(t, "server.go"), "msgmate.StartSelfMessageRuntime("); got != 1 {
		t.Errorf("backend/cmd/server.go calls msgmate.StartSelfMessageRuntime %d times, want exactly 1: a duplicate start re-introduces the fatal boot path (ci#785)", got)
	}
}

// TestSelfMessageBootNeverReturnsRuntimeError is the behavioural half of the
// ci#785 guard: no `if` in either boot path may both test the self-message
// runtime result and return, because that turns an unseeded/unknown configured
// bot into a dead backend (the HTTP port never opens). The r56 fix only made
// server.go non-fatal while worker.go kept aborting, so the AST check covers
// every boot file.
func TestSelfMessageBootNeverReturnsRuntimeError(t *testing.T) {
	for _, name := range bootSourceFiles {
		source := readBootSource(t, name)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, source, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ifStmt, ok := node.(*ast.IfStmt)
			if !ok {
				return true
			}
			if !testsSelfMessageRuntime(ifStmt.Init) && !testsSelfMessageRuntime(ifStmt.Cond) {
				return true
			}
			if hasReturn(ifStmt.Body) || hasReturn(ifStmt.Else) {
				t.Errorf("%s:%d: the self-message boot path still returns on error; an unknown configured bot must degrade to a log line and keep serving (ci#785)",
					name, fset.Position(ifStmt.Pos()).Line)
			}
			return true
		})
	}
}

// testsSelfMessageRuntime reports whether a statement/expression tree refers to
// the self-message runtime start (or its error value), i.e. whether the
// enclosing if is the fatal boot decision.
func testsSelfMessageRuntime(node ast.Node) bool {
	if node == nil {
		return false
	}
	found := false
	ast.Inspect(node, func(inner ast.Node) bool {
		switch typed := inner.(type) {
		case *ast.CallExpr:
			if sel, ok := typed.Fun.(*ast.SelectorExpr); ok {
				if sel.Sel.Name == "StartSelfMessageRuntime" || sel.Sel.Name == "RegisterConfiguredBots" {
					found = true
				}
			}
		case *ast.Ident:
			if strings.Contains(typed.Name, "selfMessage") || typed.Name == "selfErr" {
				found = true
			}
		}
		return !found
	})
	return found
}

// hasReturn reports whether a statement subtree contains a return statement.
func hasReturn(stmt ast.Stmt) bool {
	if stmt == nil {
		return false
	}
	found := false
	ast.Inspect(stmt, func(node ast.Node) bool {
		if _, ok := node.(*ast.ReturnStmt); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

func readBootSource(t *testing.T, name string) string {
	t.Helper()
	source, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(source)
}
