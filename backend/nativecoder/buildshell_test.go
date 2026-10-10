package nativecoder

import (
	"reflect"
	"testing"
)

func TestDetectBuildProject(t *testing.T) {
	cases := []struct {
		name      string
		files     []string
		wantKind  string
		wantIndic bool
	}{
		{"go", []string{"README.md", "backend/go.mod", "backend/main.go"}, BuildProjectGo, true},
		{"cargo", []string{"Cargo.toml", "src/main.rs"}, BuildProjectCargo, true},
		{"npm", []string{"package.json", "src/index.ts"}, BuildProjectNPM, true},
		{"python pyproject", []string{"pyproject.toml", "app/main.py"}, BuildProjectPython, true},
		{"python requirements", []string{"requirements.txt"}, BuildProjectPython, true},
		{"unknown", []string{"README.md", "LICENSE"}, BuildProjectUnknown, false},
		{"empty", nil, BuildProjectUnknown, false},
		{"go wins over npm by precedence", []string{"package.json", "go.mod"}, BuildProjectGo, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectBuildProject(tc.files)
			if got.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if tc.wantIndic != (got.Indicator != "") {
				t.Fatalf("indicator = %q, want present=%v", got.Indicator, tc.wantIndic)
			}
		})
	}
}

func TestIsBuildTestRunShellCommand(t *testing.T) {
	yes := []string{
		"go build ./...",
		"  go test ./backend/nativecoder/...",
		"sudo gofmt -l backend",
		"GOFLAGS=-mod=mod go build ./...",
		"npm run lint -- --quiet",
		"cargo test --quiet",
		"python3 cap_scenario/main.py",
		"pytest -q",
	}
	for _, cmd := range yes {
		if !IsBuildTestRunShellCommand(cmd) {
			t.Errorf("IsBuildTestRunShellCommand(%q) = false, want true", cmd)
		}
	}
	no := []string{"git status", "gh pr view", "ls -la", "", "kubectl get pods"}
	for _, cmd := range no {
		if IsBuildTestRunShellCommand(cmd) {
			t.Errorf("IsBuildTestRunShellCommand(%q) = true, want false", cmd)
		}
	}
}

func TestTranslateBuildShellCommand(t *testing.T) {
	goProject := BuildProject{Kind: BuildProjectGo}
	cases := []struct {
		cmd     string
		project BuildProject
		tool    string
		guarded bool
	}{
		{"go build ./...", goProject, BuildShellToolBuild, false},
		{"go test ./backend/...", goProject, BuildShellToolTest, false},
		{"go vet ./...", goProject, BuildShellToolLint, false},
		{"gofmt -l backend/nativecoder", goProject, BuildShellToolLint, false},
		{"go run ./cmd/x", goProject, BuildShellToolRun, false},
		{"golangci-lint run", goProject, BuildShellToolLint, false},
		{"npm ci", BuildProject{Kind: BuildProjectNPM}, BuildShellToolBuild, false},
		{"npm test", BuildProject{Kind: BuildProjectNPM}, BuildShellToolTest, false},
		{"npm run build", BuildProject{Kind: BuildProjectNPM}, BuildShellToolBuild, false},
		{"npm run lint -- --quiet", BuildProject{Kind: BuildProjectNPM}, BuildShellToolLint, false},
		{"npm run dev", BuildProject{Kind: BuildProjectNPM}, BuildShellToolRun, false},
		{"npx tsc --noEmit", BuildProject{Kind: BuildProjectNPM}, BuildShellToolLint, false},
		{"jest --runInBand", BuildProject{Kind: BuildProjectNPM}, BuildShellToolTest, false},
		{"cargo build", BuildProject{Kind: BuildProjectCargo}, BuildShellToolBuild, false},
		{"cargo test --quiet", BuildProject{Kind: BuildProjectCargo}, BuildShellToolTest, false},
		{"cargo clippy", BuildProject{Kind: BuildProjectCargo}, BuildShellToolLint, false},
		{"pip install -r requirements.txt", BuildProject{Kind: BuildProjectPython}, BuildShellToolBuild, false},
		{"python3 -m pytest -q", BuildProject{Kind: BuildProjectPython}, BuildShellToolTest, false},
		{"python3 main.py", BuildProject{Kind: BuildProjectPython}, BuildShellToolRun, false},
		{"ruff check .", BuildProject{Kind: BuildProjectPython}, BuildShellToolLint, false},
		{"pytest -q", BuildProject{Kind: BuildProjectPython}, BuildShellToolTest, false},
		{"go mod tidy", goProject, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			got, ok := TranslateBuildShellCommand(tc.cmd, tc.project)
			if !ok {
				t.Fatalf("TranslateBuildShellCommand(%q) ok=false", tc.cmd)
			}
			if got.Guarded != tc.guarded {
				t.Fatalf("guarded = %v, want %v (reason %q)", got.Guarded, tc.guarded, got.Reason)
			}
			if !tc.guarded && got.Tool != tc.tool {
				t.Fatalf("tool = %q, want %q", got.Tool, tc.tool)
			}
			if tc.guarded && got.Reason == "" {
				t.Fatal("guarded translation must carry a reason")
			}
			if !got.Write {
				t.Fatal("phase-2 translations are always writes")
			}
		})
	}
}

func TestTranslateBuildShellCommandRejectsNonToolchain(t *testing.T) {
	for _, cmd := range []string{"git status", "ls", "", "echo hi"} {
		if _, ok := TranslateBuildShellCommand(cmd, BuildProject{Kind: BuildProjectGo}); ok {
			t.Errorf("TranslateBuildShellCommand(%q) ok=true, want false", cmd)
		}
	}
}

func TestBuildShellTranslationReportCovered(t *testing.T) {
	report := BuildShellTranslationReportFor(
		[]string{"go build ./...", "go test ./...", "go mod tidy", "ls -la"},
		BuildProject{Kind: BuildProjectGo},
	)
	if report.Total != 4 {
		t.Fatalf("total = %d, want 4", report.Total)
	}
	if report.Translated != 2 {
		t.Fatalf("translated = %d, want 2", report.Translated)
	}
	if report.Guarded != 1 {
		t.Fatalf("guarded = %d, want 1", report.Guarded)
	}
	if len(report.Untranslated) != 1 || report.Untranslated[0] != "ls -la" {
		t.Fatalf("untranslated = %v, want [ls -la]", report.Untranslated)
	}
	if got := report.Covered(); got != 0.75 {
		t.Fatalf("covered = %v, want 0.75", got)
	}
	if empty := (BuildShellTranslationReport{}).Covered(); empty != 1 {
		t.Fatalf("empty covered = %v, want 1", empty)
	}
}

func TestParseBuildDiagnosticsGo(t *testing.T) {
	output := "backend/nativecoder/buildshell.go:12:5: undefined: foo\n" +
		"# backend/nativecoder\n" +
		"backend/api/user/PermissionsAndTokens.go:9:2: imported and not used: \"crypto/rand\"\n"
	got := ParseBuildDiagnostics(BuildProjectGo, output)
	want := []BuildDiagnostic{
		{File: "backend/nativecoder/buildshell.go", Line: 12, Column: 5, Message: "undefined: foo"},
		{File: "backend/api/user/PermissionsAndTokens.go", Line: 9, Column: 2, Message: "imported and not used: \"crypto/rand\""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostics = %#v, want %#v", got, want)
	}
}

func TestParseBuildDiagnosticsTypeScript(t *testing.T) {
	output := "src/index.ts(12,5): error TS1005: ';' expected.\n" +
		"src/app.ts(3,1): warning TS6133: 'x' is declared but never read.\n"
	got := ParseBuildDiagnostics(BuildProjectNPM, output)
	if len(got) != 2 {
		t.Fatalf("got %d diagnostics, want 2: %#v", len(got), got)
	}
	if got[0].File != "src/index.ts" || got[0].Line != 12 || got[0].Column != 5 || got[0].Severity != "error" {
		t.Fatalf("first diagnostic = %#v", got[0])
	}
	if got[1].Severity != "warning" {
		t.Fatalf("second severity = %q, want warning", got[1].Severity)
	}
}

func TestParseFailingTestsGoCargoPythonJest(t *testing.T) {
	goOut := "--- FAIL: TestFoo (0.00s)\n--- FAIL: TestBar (0.01s)\nPASS\nok pkg 0.1s\n--- FAIL: TestFoo (0.00s)\n"
	if got := ParseFailingTests(BuildProjectGo, goOut); !reflect.DeepEqual(got, []string{"TestBar", "TestFoo"}) {
		t.Fatalf("go failing tests = %v", got)
	}

	cargoOut := "test tests::works ... ok\ntest tests::broken ... FAILED\ntest tests::also_broken ... FAILED\n"
	if got := ParseFailingTests(BuildProjectCargo, cargoOut); !reflect.DeepEqual(got, []string{"tests::also_broken", "tests::broken"}) {
		t.Fatalf("cargo failing tests = %v", got)
	}

	pytestOut := "FAILED tests/test_x.py::test_one - AssertionError\nPASSED tests/test_x.py::test_two\n"
	if got := ParseFailingTests(BuildProjectPython, pytestOut); !reflect.DeepEqual(got, []string{"tests/test_x.py::test_one"}) {
		t.Fatalf("pytest failing tests = %v", got)
	}

	jestOut := "  ✕ adds two numbers (5 ms)\n  ✓ subtracts (1 ms)\n"
	if got := ParseFailingTests(BuildProjectNPM, jestOut); !reflect.DeepEqual(got, []string{"adds two numbers"}) {
		t.Fatalf("jest failing tests = %v", got)
	}
}

func TestBuildRunResultSuccess(t *testing.T) {
	green := BuildRunResult{Tool: BuildShellToolTest, ExitCode: 0}
	if !green.Success() {
		t.Fatal("exit 0 with no failing tests must be success")
	}
	red := BuildRunResult{Tool: BuildShellToolTest, ExitCode: 1, FailingTests: []string{"TestX"}}
	if red.Success() {
		t.Fatal("failing tests must not be success")
	}
	warnOnly := BuildRunResult{Tool: BuildShellToolLint, ExitCode: 0, Diagnostics: []BuildDiagnostic{{Message: "unused"}}}
	if !warnOnly.Success() {
		t.Fatal("diagnostics alone must not fail a run")
	}
}
