package main

import (
	"encoding/json"
	"strings"
	"testing"

	"backend/nativecoder"
)

// The job is the executable evidence for ci#514, so its two reported metrics
// must both be computed from the same export. These tests pin the export
// parsing, the combined report shape and the ticket-comment rendering.

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
