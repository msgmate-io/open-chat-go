package nativecoder

// buildshell.go is the phase-2 (ci#514) counterpart of githubshell.go and
// gitshell.go. The phase retires the raw toolchain shell that still reaches
// coder_bash for the build_test_run and scaffolding_fixtures categories and
// replaces it with the typed coder_build / coder_test / coder_lint / coder_run
// tools, which detect the project (go/npm/cargo/python) and return structured
// diagnostics instead of raw stdout.
//
// This file is the core-side, pure/advisory half of that contract: it detects
// the project kind, classifies and translates a raw toolchain command onto its
// typed counterpart, and parses tool output into structured diagnostics and
// failing-test names. It never executes anything, so the retirement metric and
// the tool result shapes can be unit-tested without an LLM runtime or a
// toolchain; the coding integration owns the actual exec and tool schemas.

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The typed phase-2 tool names, exactly as the capability plan declares them.
const (
	BuildShellToolBuild = "coder_build"
	BuildShellToolTest  = "coder_test"
	BuildShellToolLint  = "coder_lint"
	BuildShellToolRun   = "coder_run"
)

// BuildProject* are the project kinds the four tools detect.
const (
	BuildProjectGo      = "go"
	BuildProjectNPM     = "npm"
	BuildProjectCargo   = "cargo"
	BuildProjectPython  = "python"
	BuildProjectUnknown = "unknown"
)

// BuildProject is the project a workspace holds, as detected from its file
// listing. Kind is one of the BuildProject* constants; Indicator is the
// relative path that identified it (empty for unknown).
type BuildProject struct {
	Kind      string `json:"kind"`
	Indicator string `json:"indicator,omitempty"`
}

// buildProjectIndicators is the detection order, most specific first, so a
// workspace with several manifests resolves deterministically.
var buildProjectIndicators = []struct {
	File string
	Kind string
}{
	{"go.mod", BuildProjectGo},
	{"Cargo.toml", BuildProjectCargo},
	{"package.json", BuildProjectNPM},
	{"pyproject.toml", BuildProjectPython},
	{"setup.py", BuildProjectPython},
	{"setup.cfg", BuildProjectPython},
	{"requirements.txt", BuildProjectPython},
	{"Pipfile", BuildProjectPython},
}

// DetectBuildProject returns the project kind of a workspace from its file
// listing (workspace-relative paths). An unrecognised or empty listing is
// BuildProjectUnknown, which keeps every command in the residual bucket rather
// than guessing.
func DetectBuildProject(files []string) BuildProject {
	base := make(map[string]string, len(files))
	for _, path := range files {
		name := path
		if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
			name = path[idx+1:]
		}
		if _, ok := base[name]; !ok {
			base[name] = path
		}
	}
	for _, indicator := range buildProjectIndicators {
		if path, ok := base[indicator.File]; ok {
			return BuildProject{Kind: indicator.Kind, Indicator: path}
		}
	}
	return BuildProject{Kind: BuildProjectUnknown}
}

// buildToolchainBinaries is the set of shell binaries that belong to the
// phase-2 build/test/lint/run family. It mirrors the build_test_run entries of
// bashBinaryCategories plus the common linters, so a command the typed surface
// cannot express yet still classifies as phase-2 and shows up as guarded.
var buildToolchainBinaries = map[string]bool{
	"go": true, "gofmt": true, "golangci-lint": true,
	"npm": true, "npx": true, "pnpm": true, "yarn": true, "node": true,
	"tsc": true, "eslint": true, "prettier": true, "jest": true, "vitest": true,
	"vite": true, "mocha": true, "ts-node": true, "bun": true, "deno": true,
	"cargo": true, "rustc": true, "make": true, "cmake": true,
	"gradle": true, "mvn": true,
	"python": true, "python3": true, "pip": true, "pip3": true,
	"pytest": true, "poetry": true, "uv": true,
	"ruff": true, "flake8": true, "black": true, "mypy": true, "isort": true, "pylint": true,
}

// BuildShellTranslation is the typed call a raw toolchain command maps onto.
// Write is always true for this family (builds/tests write artifacts and
// output); Guarded marks a phase-2 command the typed surface cannot express
// yet, with Reason explaining the gap.
type BuildShellTranslation struct {
	Command string            `json:"command"`
	Tool    string            `json:"tool,omitempty"`
	Kind    string            `json:"project_kind"`
	Args    map[string]string `json:"args,omitempty"`
	Write   bool              `json:"write"`
	Guarded bool              `json:"guarded,omitempty"`
	Reason  string            `json:"guarded_reason,omitempty"`
}

// BuildShellTranslationReport summarises how much of a sample of toolchain
// commands the typed phase-2 surface already covers.
type BuildShellTranslationReport struct {
	Total        int                     `json:"total"`
	Translated   int                     `json:"translated"`
	Guarded      int                     `json:"guarded"`
	Untranslated []string                `json:"untranslated,omitempty"`
	Translations []BuildShellTranslation `json:"translations,omitempty"`
}

// Covered reports the covered share in [0,1]; an empty sample counts as fully
// covered so it never blocks a phase gate.
func (r BuildShellTranslationReport) Covered() float64 {
	if r.Total == 0 {
		return 1
	}
	return float64(r.Translated+r.Guarded) / float64(r.Total)
}

// buildShellFields splits a shell command into fields, stripping wrapper
// binaries (sudo/time/…) and leading env assignments the way the phase-0
// classifier does. Quoting is not interpreted, which is safe for a metric.
func buildShellFields(cmd string) []string {
	fields := strings.Fields(strings.TrimSpace(cmd))
	for len(fields) > 0 {
		if bashWrapperBinaries[fields[0]] {
			fields = fields[1:]
			continue
		}
		if strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "=") {
			fields = fields[1:] // VAR=value prefix
			continue
		}
		break
	}
	return fields
}

// IsBuildTestRunShellCommand reports whether cmd is a phase-2 toolchain
// invocation (its first segment binary is in the family).
func IsBuildTestRunShellCommand(cmd string) bool {
	fields := buildShellFields(cmd)
	return len(fields) >= 1 && buildToolchainBinaries[fields[0]]
}

// TranslateBuildShellCommand maps one raw toolchain command onto its typed
// counterpart. ok is false only when the command is not a phase-2 toolchain
// call at all; a phase-2 call the typed surface cannot express yet is returned
// with Guarded=true so it stays visible in the report.
func TranslateBuildShellCommand(cmd string, project BuildProject) (BuildShellTranslation, bool) {
	fields := buildShellFields(cmd)
	if len(fields) == 0 || !buildToolchainBinaries[fields[0]] {
		return BuildShellTranslation{}, false
	}
	kind := project.Kind
	if kind == "" {
		kind = BuildProjectUnknown
	}
	t := BuildShellTranslation{Command: strings.TrimSpace(cmd), Kind: kind, Write: true, Args: map[string]string{}}
	bin := fields[0]
	sub := ""
	if len(fields) > 1 {
		sub = fields[1]
	}
	rest := fields[1:]
	if len(rest) > 0 {
		rest = rest[1:]
	}

	switch bin {
	case "go":
		switch sub {
		case "build":
			t.Tool, t.Args["target"] = BuildShellToolBuild, defaultTarget(rest)
		case "test":
			t.Tool, t.Args["packages"] = BuildShellToolTest, defaultTarget(rest)
		case "run":
			t.Tool, t.Args["program"] = BuildShellToolRun, strings.Join(rest, " ")
		case "vet", "fmt":
			t.Tool, t.Args["target"] = BuildShellToolLint, defaultTarget(rest)
		default:
			return guard(t, "go "+sub+" has no typed build/test/lint/run form yet")
		}
	case "gofmt", "golangci-lint":
		t.Tool, t.Args["target"] = BuildShellToolLint, defaultTarget(rest)
	case "npm", "pnpm", "yarn":
		mapped, ok := translateNodePackageManager(bin, sub, rest, &t)
		if !ok {
			return guard(t, bin+" "+sub+" has no typed build/test/lint/run form yet")
		}
		_ = mapped
	case "npx", "ts-node", "bun", "deno":
		switch sub {
		case "tsc", "eslint", "prettier":
			t.Tool, t.Args["target"] = BuildShellToolLint, defaultTarget(rest)
		case "jest", "vitest", "mocha":
			t.Tool, t.Args["filter"] = BuildShellToolTest, strings.Join(rest, " ")
		default:
			t.Tool, t.Args["program"] = BuildShellToolRun, strings.Join(fields[1:], " ")
		}
	case "tsc", "eslint", "prettier":
		t.Tool, t.Args["target"] = BuildShellToolLint, defaultTarget(rest)
	case "jest", "vitest", "mocha":
		t.Tool, t.Args["filter"] = BuildShellToolTest, strings.Join(rest, " ")
	case "node", "vite":
		t.Tool, t.Args["program"] = BuildShellToolRun, strings.Join(fields[1:], " ")
	case "cargo":
		switch sub {
		case "build":
			t.Tool = BuildShellToolBuild
		case "test":
			t.Tool, t.Args["filter"] = BuildShellToolTest, strings.Join(rest, " ")
		case "run":
			t.Tool, t.Args["program"] = BuildShellToolRun, strings.Join(rest, " ")
		case "clippy", "fmt":
			t.Tool = BuildShellToolLint
		default:
			return guard(t, "cargo "+sub+" has no typed build/test/lint/run form yet")
		}
	case "rustc", "make", "cmake", "gradle", "mvn":
		t.Tool, t.Args["target"] = BuildShellToolBuild, strings.Join(rest, " ")
	case "pip", "pip3", "poetry", "uv":
		t.Tool = BuildShellToolBuild
		t.Args["dependencies"] = strings.Join(rest, " ")
	case "python", "python3":
		switch {
		case sub == "-m" && len(rest) > 0 && (rest[0] == "pytest" || rest[0] == "unittest"):
			t.Tool, t.Args["filter"] = BuildShellToolTest, strings.Join(rest[1:], " ")
		case sub == "-m" && len(rest) > 0 && (rest[0] == "ruff" || rest[0] == "flake8" || rest[0] == "mypy" || rest[0] == "black" || rest[0] == "pylint"):
			t.Tool, t.Args["target"] = BuildShellToolLint, strings.Join(rest[1:], " ")
		case strings.HasSuffix(sub, ".py"):
			t.Tool, t.Args["program"] = BuildShellToolRun, strings.Join(fields[1:], " ")
		default:
			return guard(t, "python "+sub+" has no typed build/test/lint/run form yet")
		}
	case "pytest":
		t.Tool, t.Args["filter"] = BuildShellToolTest, strings.Join(rest, " ")
	case "ruff", "flake8", "black", "mypy", "isort", "pylint":
		t.Tool, t.Args["target"] = BuildShellToolLint, defaultTarget(rest)
	default:
		return guard(t, bin+" has no typed build/test/lint/run form yet")
	}
	if len(t.Args) == 0 {
		t.Args = nil
	}
	return t, true
}

// translateNodePackageManager maps `npm|pnpm|yarn <sub> ...` onto a typed tool.
func translateNodePackageManager(bin, sub string, rest []string, t *BuildShellTranslation) (bool, bool) {
	switch sub {
	case "install", "ci", "add", "i":
		t.Tool = BuildShellToolBuild
		t.Args["dependencies"] = strings.Join(rest, " ")
		return true, true
	case "test", "t":
		t.Tool = BuildShellToolTest
		return true, true
	case "run", "run-script":
		if len(rest) == 0 {
			return false, false
		}
		script := rest[0]
		switch script {
		case "build", "compile", "bundle":
			t.Tool = BuildShellToolBuild
		case "lint", "typecheck", "type-check", "fmt", "format":
			t.Tool = BuildShellToolLint
		case "test":
			t.Tool = BuildShellToolTest
		default:
			t.Tool = BuildShellToolRun
			t.Args["script"] = script
		}
		return true, true
	default:
		return false, false
	}
}

func defaultTarget(rest []string) string {
	if len(rest) == 0 {
		return "."
	}
	return strings.Join(rest, " ")
}

func guard(t BuildShellTranslation, reason string) (BuildShellTranslation, bool) {
	t.Guarded = true
	t.Reason = reason
	t.Args = nil
	return t, true
}

// BuildShellTranslationReportFor translates a whole sample of commands and
// returns the coverage report the phase gate reads.
func BuildShellTranslationReportFor(commands []string, project BuildProject) BuildShellTranslationReport {
	report := BuildShellTranslationReport{}
	for _, cmd := range commands {
		report.Total++
		translation, ok := TranslateBuildShellCommand(cmd, project)
		if !ok {
			report.Untranslated = append(report.Untranslated, strings.TrimSpace(cmd))
			continue
		}
		if translation.Guarded {
			report.Guarded++
		} else {
			report.Translated++
		}
		report.Translations = append(report.Translations, translation)
	}
	return report
}

// BuildDiagnostic is one structured compiler/linter message: a file:line(:col)
// location plus a severity and message, exactly the shape a typed tool result
// carries instead of raw stdout.
type BuildDiagnostic struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity,omitempty"`
	Message  string `json:"message"`
}

// BuildRunResult is the structured result of a typed build/test/lint/run call.
type BuildRunResult struct {
	Tool         string            `json:"tool"`
	ProjectKind  string            `json:"project_kind"`
	ExitCode     int               `json:"exit_code"`
	Diagnostics  []BuildDiagnostic `json:"diagnostics,omitempty"`
	FailingTests []string          `json:"failing_tests,omitempty"`
}

// Success reports whether the call produced a green result: exit 0 and no
// failing tests. Diagnostics alone (warnings) do not fail a run.
func (r BuildRunResult) Success() bool {
	return r.ExitCode == 0 && len(r.FailingTests) == 0
}

var (
	// file:line(:col): message — Go, ruff, mypy, most Unix toolchains.
	buildDiagnosticRe = regexp.MustCompile(`^(?P<file>[^\s:][^:]*):(?P<line>\d+):(?:(?P<col>\d+):)?\s*(?P<msg>.+)$`)
	// file(line,col): message — TypeScript / ESLint stylish.
	buildTSDiagnosticRe = regexp.MustCompile(`^(?P<file>[^\s(][^()]*)\((?P<line>\d+),(?P<col>\d+)\):\s*(?P<msg>.+)$`)
	buildSeverityRe     = regexp.MustCompile(`^(?i)(error|warning|fatal|note)\b[:\s]+(.*)$`)
)

// ParseBuildDiagnostics extracts structured file:line diagnostics from raw tool
// output. kind is accepted for symmetry with the tool that produced the output
// and to allow kind-specific extensions; the generic parsers cover the four
// detected projects today.
func ParseBuildDiagnostics(kind, output string) []BuildDiagnostic {
	_ = kind
	var out []BuildDiagnostic
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--- FAIL:") || strings.HasPrefix(trimmed, "===") {
			continue
		}
		if d, ok := parseBuildDiagnosticLine(trimmed); ok {
			out = append(out, d)
		}
	}
	return out
}

func parseBuildDiagnosticLine(line string) (BuildDiagnostic, bool) {
	for _, re := range []*regexp.Regexp{buildTSDiagnosticRe, buildDiagnosticRe} {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		file := m[re.SubexpIndex("file")]
		lineNo, _ := strconv.Atoi(m[re.SubexpIndex("line")])
		col := 0
		if idx := re.SubexpIndex("col"); idx >= 0 {
			col, _ = strconv.Atoi(m[idx])
		}
		msg := strings.TrimSpace(m[re.SubexpIndex("msg")])
		severity := ""
		if sm := buildSeverityRe.FindStringSubmatch(msg); sm != nil {
			severity = strings.ToLower(sm[1])
			msg = strings.TrimSpace(sm[2])
		}
		return BuildDiagnostic{File: file, Line: lineNo, Column: col, Severity: severity, Message: msg}, true
	}
	return BuildDiagnostic{}, false
}

var (
	buildGoFailRe     = regexp.MustCompile(`^\s*--- FAIL: (?P<name>[^\s(]+)`)
	buildPytestFailRe = regexp.MustCompile(`^(?:FAILED|ERROR)\s+(?P<name>\S+)`)
	buildCargoFailRe  = regexp.MustCompile(`^\s*test\s+(?P<name>\S+)\s+\.\.\.\s+FAILED`)
	buildJestFailRe   = regexp.MustCompile(`^\s*[✕✗×]\s+(?P<name>.+?)\s*(?:\(\d|$)`)
	buildJestFileRe   = regexp.MustCompile(`^(?:FAIL|✕)\s+(?P<name>\S+)`)
)

// ParseFailingTests extracts the names of failing tests from raw tool output
// for the detected project kinds (go/cargo/pytest/jest-style). Names are
// trimmed, de-duplicated and sorted so a result is deterministic.
func ParseFailingTests(kind, output string) []string {
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" {
			seen[name] = true
		}
	}
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := buildGoFailRe.FindStringSubmatch(line); m != nil {
			add(m[buildGoFailRe.SubexpIndex("name")])
		}
		if m := buildCargoFailRe.FindStringSubmatch(line); m != nil {
			add(m[buildCargoFailRe.SubexpIndex("name")])
		}
		if m := buildPytestFailRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			add(m[buildPytestFailRe.SubexpIndex("name")])
		}
		if m := buildJestFailRe.FindStringSubmatch(line); m != nil {
			add(m[buildJestFailRe.SubexpIndex("name")])
		} else if m := buildJestFileRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			add(m[buildJestFileRe.SubexpIndex("name")])
		}
	}
	_ = kind
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
