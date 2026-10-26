// Package benchmark implements the reproducible, machine-readable harness for
// the "free OpenRouter model x private sandbox test matrix" capability report
// (msgmate-io/open-chat-go-ci#893).
//
// Design rules enforced here:
//   - The free-model input list is derived from the model registry files on
//     disk (LoadFreeModels), never hardcoded and never fetched from a live
//     API. See registry.go.
//   - Result aggregation and table rendering are pure functions over local
//     fixtures/TSVs; they never touch the network.
//   - The expensive full-matrix sweep is NOT a unit test. It is launched only
//     by the explicitly-triggered `--dispatch` mode of
//     development/free_model_capability_sweep.sh, which hands off to the
//     private workflow `private-sandbox-tests.yaml` in
//     msgmate-io/open-chat-go-ci (ci#893).
package benchmark

import (
	"fmt"
	"time"
)

// StatusCategory is the coarse classification the capability table renders.
// provider_* and timeout are deliberately kept distinct from genuine
// capability failures, matching the ci#479 provider-failure classification used
// by the private runner.
type StatusCategory string

const (
	CategoryPass           StatusCategory = "pass"
	CategoryFail           StatusCategory = "fail"
	CategoryFailAfterRetry StatusCategory = "fail_after_retry"
	CategoryProviderError  StatusCategory = "provider_error"
	CategoryTimeout        StatusCategory = "timeout"
	CategorySkipped        StatusCategory = "skipped"
	CategoryNotApplicable  StatusCategory = "n/a"
	CategoryNotRun         StatusCategory = "not_run"
)

// SchemaVersion is the version of the emitted machine-readable artifact
// (benchmark-results/free-models-<date>.json).
const SchemaVersion = 1

// FreeModel is one model from the registry that the sweep should target.
// Only the model identifier is acted upon; everything else is provenance.
type FreeModel struct {
	ID     string `json:"model_id"`
	Title  string `json:"title,omitempty"`
	Source string `json:"registry_source,omitempty"` // file the entry came from
}

// TestResult is the aggregated final outcome of one (model, test) pair.
type TestResult struct {
	TestID   string         `json:"test_id"`
	Tag      string         `json:"tag,omitempty"`
	Status   string         `json:"status"`   // raw final status as recorded (e.g. "provider_rate_limit")
	Category StatusCategory `json:"category"` // coarse class for the table
	Reason   string         `json:"reason,omitempty"`
	Attempts int            `json:"attempts"`
}

// Counts holds the per-model / per-test tally used for the pass-rate column.
type Counts struct {
	Passed         int `json:"passed"`
	Failed         int `json:"failed"`
	FailAfterRetry int `json:"fail_after_retry"`
	ProviderError  int `json:"provider_error"`
	Timeout        int `json:"timeout"`
	Skipped        int `json:"skipped"`
	NotApplicable  int `json:"not_applicable"`
	NotRun         int `json:"not_run"`
}

// Total counts the outcomes that participate in the pass-rate denominator:
// pass, fail, provider-error and timeout are real results; skipped / n/a /
// not-run are documented as absences, not capabilities.
func (c Counts) Total() int {
	return c.Passed + c.Failed + c.FailAfterRetry + c.ProviderError + c.Timeout
}

// PassRate returns "NN.N%" over real outcomes, or "n/a" when there are none.
func (c Counts) PassRate() string {
	if c.Total() == 0 {
		return "n/a"
	}
	pct := float64(c.Passed) / float64(c.Total()) * 100
	return formatRate(pct)
}

// ModelReport is one row of the capability table.
type ModelReport struct {
	ModelID  string       `json:"model_id"`
	Title    string       `json:"title,omitempty"`
	Registry string       `json:"registry_source,omitempty"`
	Counts   Counts       `json:"counts"`
	PassRate string       `json:"pass_rate"`
	Results  []TestResult `json:"results"`
}

// TestSummary is one column of the capability table (per-test pass rate).
type TestSummary struct {
	TestID   string `json:"test_id"`
	Tag      string `json:"tag,omitempty"`
	Counts   Counts `json:"counts"`
	PassRate string `json:"pass_rate"`
}

// MatrixReport is the machine-readable artifact the landing page consumes.
type MatrixReport struct {
	SchemaVersion  int           `json:"schema_version"`
	GeneratedAt    string        `json:"generated_at"`
	TargetRef      string        `json:"target_ref,omitempty"`
	SourceRegistry []string      `json:"source_registry"`
	Models         []ModelReport `json:"models"`
	Tests          []TestSummary `json:"tests"`
}

// NewMatrixReport seeds a report with the current timestamp (UTC RFC3339).
func NewMatrixReport(targetRef string, sources []string) *MatrixReport {
	return &MatrixReport{
		SchemaVersion:  SchemaVersion,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		TargetRef:      targetRef,
		SourceRegistry: sources,
		Models:         []ModelReport{},
		Tests:          []TestSummary{},
	}
}

func formatRate(pct float64) string {
	return fmt.Sprintf("%.1f%%", pct)
}
