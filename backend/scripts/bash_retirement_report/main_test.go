package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const sampleExport = "{\"tool\":\"coder_bash\",\"arguments\":{\"command\":\"gh pr view 873 --json files\"},\"repo\":\"msgmate-io/open-chat-go\"}\n" +
	"{\"tool\":\"coder_bash\",\"arguments\":{\"command\":\"gh issue list --state open --json number\"},\"repo\":\"msgmate-io/open-chat-go\"}\n"

// The job must emit the phase exit gates next to the coverage report, so one
// run answers both "how covered are we" and "may this phase close".
func TestBuildEnvelopeCarriesPhaseGates(t *testing.T) {
	report, err := readReport(strings.NewReader(sampleExport))
	if err != nil {
		t.Fatalf("readReport: %v", err)
	}
	envelope := buildEnvelope(report)
	if len(envelope.Phases) == 0 {
		t.Fatalf("envelope must carry the plan's phase gates")
	}
	if envelope.Phases[0].ID == "" {
		t.Fatalf("phase gates must carry their plan id")
	}
	github := false
	for _, phase := range envelope.Phases {
		if phase.ID == "phase_1_github" {
			github = true
		}
	}
	if !github {
		t.Fatalf("the GitHub phase must be present among the gates")
	}
}

// The envelope's JSON shape is the job's contract for downstream consumers.
func TestBuildEnvelopeJSONShape(t *testing.T) {
	report, err := readReport(strings.NewReader(sampleExport))
	if err != nil {
		t.Fatalf("readReport: %v", err)
	}
	encoded, err := json.Marshal(buildEnvelope(report))
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
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
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if decoded.Report == nil || len(decoded.Phases) == 0 {
		t.Fatalf("envelope JSON must expose both report and phases: %s", encoded)
	}
	if len(decoded.Phases) > 1 && decoded.Phases[1].Order <= decoded.Phases[0].Order {
		t.Fatalf("phases must be emitted in plan order")
	}
	if decoded.Phases[0].ID == "phase_0_measure" && decoded.Phases[0].Ready {
		t.Fatalf("the measure phase retires nothing and must not report ready")
	}
}

// The text rendering must surface the phase gates and the close verdict, so a
// ticket comment can be pasted straight from `--format=text`.
func TestRenderTextIncludesPhaseGates(t *testing.T) {
	report, err := readReport(strings.NewReader(sampleExport))
	if err != nil {
		t.Fatalf("readReport: %v", err)
	}
	envelope := buildEnvelope(report)
	text := renderText(envelope)
	for _, want := range []string{"phase exit gates:", "phase_1_github", "ready to close the retirement:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text report missing %q:\n%s", want, text)
		}
	}
}

// An unparsable line is evidence, not a failure: the run must still produce a
// report and error count so a partial export is measurable.
func TestReadReportCountsMalformedLines(t *testing.T) {
	report, err := readReport(strings.NewReader("not json\n" + sampleExport))
	if err != nil {
		t.Fatalf("readReport must tolerate malformed lines: %v", err)
	}
	if report.ErroredCalls == 0 {
		t.Fatalf("a malformed line must be counted as an errored call")
	}
	if report.ClassifiedCalls == 0 {
		t.Fatalf("the valid line must still be classified")
	}
}
