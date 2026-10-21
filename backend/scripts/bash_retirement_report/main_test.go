package main

import (
	"encoding/json"
	"strings"
	"testing"

	"backend/nativecoder"
)

// The job is the executable evidence for ci#514, so the coverage metric, the
// phase 1 GitHub exit criterion and the per-phase exit gates must all be
// computed from the same export. These tests pin the export parsing, the
// combined report shape and the ticket-comment rendering.

const sampleExport = "{\"tool\":\"coder_bash\",\"arguments\":{\"command\":\"gh pr view 873 --json files\"},\"repo\":\"msgmate-io/open-chat-go\"}\n" +
	"{\"tool\":\"coder_bash\",\"arguments\":{\"command\":\"gh issue list --state open --json number\"},\"repo\":\"msgmate-io/open-chat-go\"}\n"

func TestReadSamplesParsesJSONL(t *testing.T) {
	export := strings.Join([]string{
		`# a comment line`,
		``,
		`{"tool":"coder_bash","arguments":{"command":"gh pr view 873 --json files"},"repo":"msgmate-io/open-chat-go"}`,
		`{"tool":"coder_git","arguments":{"subcommand":"log","args":["-5"]}}`,
		`{"tool":"coder_bash"}`,
		`not json at all`,
		`{"tool":"coder_bash","arguments":{"command":"go build ./..."}}`,
	}, "\n")

	samples, err := readSamples(strings.NewReader(export))
	if err != nil {
		t.Fatalf("readSamples: %v", err)
	}
	if len(samples) != 5 {
		t.Fatalf("samples = %d, want 5 (comments and blank lines skipped)", len(samples))
	}
	if samples[0].Tool != "coder_bash" || samples[0].Repo != "msgmate-io/open-chat-go" {
		t.Fatalf("first sample = %+v", samples[0])
	}
	if samples[1].Arguments != `{"subcommand":"log","args":["-5"]}` {
		t.Fatalf("a structured arguments payload must pass through as raw JSON: %q", samples[1].Arguments)
	}
	if samples[2].Arguments != "{}" {
		t.Fatalf("a missing arguments payload must default to {}: %q", samples[2].Arguments)
	}
	if samples[3].Tool != "unparsed" {
		t.Fatalf("a malformed line must stay measurable as unparsed, got %+v", samples[3])
	}
}

func TestBuildReportMeasuresBothPhases(t *testing.T) {
	samples := []nativecoder.BashCallSample{
		{Tool: "coder_bash", Arguments: `{"command":"gh pr view 873 --json files"}`},
		{Tool: "coder_bash", Arguments: `{"command":"gh auth token"}`},
		{Tool: "coder_bash", Arguments: `{"command":"go build ./..."}`},
	}

	report := buildReport(samples)
	if report.ClassifiedCalls != 3 {
		t.Fatalf("coverage classified %d calls, want 3", report.ClassifiedCalls)
	}
	phase1 := report.GitHubPhase1
	if phase1.Commands != 3 || phase1.GitHubCommands != 2 {
		t.Fatalf("phase 1 commands = %d/%d, want 2 gh of 3", phase1.GitHubCommands, phase1.Commands)
	}
	if phase1.TranslatedCommands != 1 || phase1.ResidualCommands != 1 {
		t.Fatalf("phase 1 translation = %d translated, %d residual",
			phase1.TranslatedCommands, phase1.ResidualCommands)
	}
	// A credential-printing command is residual, so the phase stays open.
	if phase1.PhaseReady {
		t.Fatal("phase 1 must not be ready while `gh auth token` still needs the shell")
	}
	if phase1.PlanVersion != nativecoder.BashRetirementPlanVersion {
		t.Fatalf("phase 1 plan version = %d", phase1.PlanVersion)
	}
}

// The job must emit the phase exit gates next to the coverage report, so one
// run answers both "how covered are we" and "may this phase close".
func TestBuildReportCarriesPhaseGates(t *testing.T) {
	samples, err := readSamples(strings.NewReader(sampleExport))
	if err != nil {
		t.Fatalf("readSamples: %v", err)
	}
	report := buildReport(samples)
	if len(report.Phases) == 0 {
		t.Fatal("the report must carry the plan's phase gates")
	}
	if report.Phases[0].ID == "" {
		t.Fatal("phase gates must carry their plan id")
	}
	github := false
	for _, phase := range report.Phases {
		if phase.ID == "phase_1_github" {
			github = true
		}
	}
	if !github {
		t.Fatal("the GitHub phase must be present among the gates")
	}
	if report.ReadyToClose {
		t.Fatal("the retirement must not be closable from a two-call sample")
	}
}

func TestReportJSONKeepsCoverageFlatAndNestsPhase1(t *testing.T) {
	encoded, err := json.Marshal(buildReport([]nativecoder.BashCallSample{
		{Tool: "coder_bash", Arguments: `{"command":"gh issue list --state open"}`},
	}))
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	// The phase 0 coverage fields stay at the top level, so existing consumers
	// of the job's output keep working when phase 1 is added.
	if _, ok := decoded["coverage_ratio"]; !ok {
		t.Fatalf("coverage fields must stay top level, got keys %v", keysOf(decoded))
	}
	if _, ok := decoded["github_phase1"]; !ok {
		t.Fatalf("github_phase1 missing, got keys %v", keysOf(decoded))
	}
	if _, ok := decoded["phases"]; !ok {
		t.Fatalf("phases missing, got keys %v", keysOf(decoded))
	}
	if _, ok := decoded["ready_to_close"]; !ok {
		t.Fatalf("ready_to_close missing, got keys %v", keysOf(decoded))
	}
}

// The report's JSON shape is the job's contract for downstream consumers.
func TestReportJSONShape(t *testing.T) {
	samples, err := readSamples(strings.NewReader(sampleExport))
	if err != nil {
		t.Fatalf("readSamples: %v", err)
	}
	encoded, err := json.Marshal(buildReport(samples))
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	var decoded struct {
		Report map[string]any `json:"report"`
		Phases []struct {
			ID       string `json:"id"`
			Order    int    `json:"order"`
			Ready    bool   `json:"ready"`
			Measured bool   `json:"measured"`
		} `json:"phases"`
		ReadyToClose bool `json:"ready_to_close"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(decoded.Phases) == 0 {
		t.Fatalf("the report must expose the phase gates: %s", encoded)
	}
	if len(decoded.Phases) > 1 && decoded.Phases[1].Order <= decoded.Phases[0].Order {
		t.Fatal("phases must be emitted in plan order")
	}
	if decoded.Phases[0].ID == "phase_0_measure" && decoded.Phases[0].Ready {
		t.Fatal("the measure phase retires nothing and must not report ready")
	}
}

func TestRenderTextReportsGitHubPhase(t *testing.T) {
	text := renderText(buildReport([]nativecoder.BashCallSample{
		{Tool: "coder_bash", Arguments: `{"command":"gh pr diff 8"}`},
		{Tool: "coder_bash", Arguments: `{"command":"gh auth token"}`},
	}))
	// Both calls are GitHub commands (a diff and a credential read); the
	// credential one is residual, so the phase cannot close here.
	for _, want := range []string{
		"phase 1 (GitHub) OPEN",
		"2/2 gh calls",
		"share 100.0%",
		"translation 50.0%",
		"residual:",
		"keeps shell: gh auth token",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered text is missing %q:\n%s", want, text)
		}
	}
}

// The text rendering must also surface the phase gates and the close verdict,
// so a ticket comment can be pasted straight from `--format=text`.
func TestRenderTextIncludesPhaseGates(t *testing.T) {
	samples, err := readSamples(strings.NewReader(sampleExport))
	if err != nil {
		t.Fatalf("readSamples: %v", err)
	}
	text := renderText(buildReport(samples))
	for _, want := range []string{"phase exit gates:", "phase_1_github", "ready to close the retirement:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text report missing %q:\n%s", want, text)
		}
	}
}

// An unparsable line is evidence, not a failure: the run must still produce a
// report and error count so a partial export is measurable.
func TestReadSamplesCountsMalformedLines(t *testing.T) {
	samples, err := readSamples(strings.NewReader("not json\n" + sampleExport))
	if err != nil {
		t.Fatalf("readSamples must tolerate malformed lines: %v", err)
	}
	report := buildReport(samples)
	if report.ErroredCalls == 0 {
		t.Fatal("a malformed line must be counted as an errored call")
	}
	if report.ClassifiedCalls == 0 {
		t.Fatal("the valid line must still be classified")
	}
}

func TestPhaseState(t *testing.T) {
	if phaseState(true) != "READY" || phaseState(false) != "OPEN" {
		t.Fatal("phaseState must render READY/OPEN")
	}
}

func keysOf(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
