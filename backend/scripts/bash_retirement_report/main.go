// Command bash_retirement_report is the measurement job of the coder_bash
// retirement plan (ci#514): the phase 0 coverage metric, the phase 1 GitHub
// exit criterion and the per-phase exit gates derived from them.
//
// It reads historical native-coder tool calls and buckets every `coder_bash` /
// `coder_git` call by category and by repository, then prints the coverage
// metric: the fraction of historical shell calls the typed tool surface can
// express. The typed plan itself lives in `backend/nativecoder`
// (`BashRetirementPlanSnapshot`), so the metric can never drift from the plan
// the docs describe.
//
// Because phase 1 is GitHub-first, the very same samples are also run through
// the phase 1 bridge (`MeasureGitHubShellTranslation`) and its exit criterion is
// emitted next to the coverage report under `github_phase1`: GitHub share,
// translation ratio, residual reasons and phase readiness. One export therefore
// answers both "how much of the shell is expressible" and "can the GitHub phase
// be closed", without a second job or a second input format.
//
// Input is JSONL, one call per line, so it can be produced from any transcript
// export without a schema dependency:
//
//	{"tool":"coder_bash","arguments":{"command":"gh pr view 873 --json files"},"repo":"msgmate-io/open-chat-go"}
//	{"tool":"coder_git","arguments":{"subcommand":"checkout","args":["-b","fix/514"]},"chat":"…","date":"2026-01-02"}
//
// Arguments that are already a JSON string (an older transcript shape) are
// accepted as-is. Lines that are blank or start with `#` are ignored; malformed
// lines are reported in `errored_calls` instead of failing the run, so a partial
// export still produces evidence.
//
// Usage:
//
//	go run ./backend/scripts/bash_retirement_report < calls.jsonl
//	go run ./backend/scripts/bash_retirement_report calls.jsonl --format=json
//	go run ./backend/scripts/bash_retirement_report calls.jsonl --format=text
//
// Output defaults to pretty JSON on stdout: the coverage report plus the
// per-phase exit gates derived from it (`phases`, `ready_to_close`); the phase 1
// readiness metric rides along under `github_phase1`, so a reader can consume
// the coverage numbers exactly as before. `--format=text` prints a short human
// summary for pasting into a ticket comment. The job is read-only and performs
// no network calls: it only classifies the command shapes it is given.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"backend/nativecoder"
)

// reportLine is one historical tool call as it appears in an export.
type reportLine struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Repo      string          `json:"repo,omitempty"`
	Chat      string          `json:"chat,omitempty"`
	Date      string          `json:"date,omitempty"`
}

// retirementReport is the job's output: the phase 0 coverage metric, the
// phase 1 GitHub exit criterion measured on the same samples, the per-phase
// exit gates (ci#514) that turn the metric into "may this phase close?", and
// the machine-checked drive record that states the target surface and the
// phase that retires each category.
type retirementReport struct {
	nativecoder.BashCoverageReport
	// GitHubPhase1 is the phase 1 readiness metric, emitted as its own object
	// so a reader can consume the coverage numbers exactly as before.
	GitHubPhase1 nativecoder.GitHubShellTranslationReport `json:"github_phase1"`
	// Phases carries one machine-checked exit gate per plan phase.
	Phases []nativecoder.BashRetirementPhaseGate `json:"phases"`
	// ReadyToClose is true only when every phase that retires a category has
	// met its exit criteria against this sample.
	ReadyToClose bool `json:"ready_to_close"`
	// Criteria is the per-phase exit-criteria checklist: every declared
	// criterion of every phase with its machine-checked status, so a phase can
	// never be declared done while one of its criteria was simply never
	// checked.
	Criteria []nativecoder.BashRetirementPhaseCriteria `json:"criteria"`
	// CriteriaReadyToClose is the checklist's verdict, which is strictly
	// harder than ReadyToClose: it also requires that every criterion has
	// evidence at all, not just that the categories passed their threshold.
	CriteriaReadyToClose bool `json:"criteria_ready_to_close"`
	// Drive is the machine-checked ci#514 drive record: the target typed-tool
	// surface, the per-phase status and the phase that retires each category,
	// hardened against the classifier that produces this report.
	Drive nativecoder.BashRetirementDriveRecord `json:"drive"`
}

func main() {
	format := flag.String("format", "json", "output format: json or text")
	coderPrompt := flag.String("coder-prompt", "", "path to the rendered coder system prompt (evidence for the typed-first criterion)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [calls.jsonl] [--format=json|text] [--coder-prompt=prompt.txt]\n", os.Args[0])
		flag.PrintDefaults()
	}
	// The stdlib flag package stops at the first positional argument, so
	// reorder flags ahead of the file to accept the documented
	// `job calls.jsonl --format=text` invocation.
	flag.CommandLine.Parse(flagsFirst(os.Args[1:]))

	input := io.Reader(os.Stdin)
	if path := flag.Arg(0); path != "" && path != "-" {
		file, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open %s: %v\n", path, err)
			os.Exit(1)
		}
		defer file.Close()
		input = file
	}

	samples, err := readSamples(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read transcript export: %v\n", err)
		os.Exit(1)
	}
	report := buildReport(samples, readCoderPrompt(*coderPrompt))

	switch strings.ToLower(*format) {
	case "json", "":
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
			os.Exit(1)
		}
	case "text":
		fmt.Print(renderText(report))
	default:
		fmt.Fprintf(os.Stderr, "unknown --format %q (want json or text)\n", *format)
		os.Exit(2)
	}
}

// readCoderPrompt loads the rendered coder system prompt used as evidence for
// the phase 1 typed-first criterion. A missing path yields an empty prompt,
// which leaves the criterion unattested (never falsely satisfied); an unreadable
// file is a hard error, because silently dropping supplied evidence would make
// the report lie.
func readCoderPrompt(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read coder prompt %s: %v\n", path, err)
		os.Exit(1)
	}
	return string(data)
}

// flagsFirst moves flag arguments ahead of positional ones so the documented
// `job calls.jsonl --format=text` form works with the stdlib flag package.
func flagsFirst(args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && isValueFlag(arg) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, arg)
	}
	return append(flags, positional...)
}

// isValueFlag reports whether the flag needs its value in the next argument
// rather than after an `=`.
func isValueFlag(arg string) bool {
	return arg == "--format" || arg == "-format" || arg == "--coder-prompt" || arg == "-coder-prompt"
}

// readSamples consumes a JSONL export into the sample slice both metrics are
// computed from, so phase 0, phase 1 and the exit gates always see exactly the
// same evidence.
func readSamples(input io.Reader) ([]nativecoder.BashCallSample, error) {
	samples := []nativecoder.BashCallSample{}
	scanner := bufio.NewScanner(input)
	// Transcript lines with long inline payloads routinely exceed the 64KiB
	// default; 4MiB comfortably covers a tool call while still bounding memory.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var entry reportLine
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Keep unsupported lines measurable as parse errors rather than
			// dropping them: an unparsed call is evidence too.
			samples = append(samples, nativecoder.BashCallSample{Tool: "unparsed", Arguments: line})
			continue
		}
		arguments := string(entry.Arguments)
		if len(entry.Arguments) == 0 {
			arguments = "{}"
		}
		samples = append(samples, nativecoder.BashCallSample{
			Tool:      entry.Tool,
			Arguments: arguments,
			Repo:      entry.Repo,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return samples, nil
}

// buildReport computes both plan metrics and the phase exit gates over one
// sample set, so JSON and text output always agree on `phases` and
// `ready_to_close`.
//
// coderSystemPrompt is the rendered coder system prompt the run really uses; it
// is the evidence for the phase 1 "typed-first prompt" criterion. An empty
// prompt leaves that criterion unattested rather than passing it, so a report
// never claims a criterion it did not check.
func buildReport(samples []nativecoder.BashCallSample, coderSystemPrompt string) retirementReport {
	coverage := nativecoder.ComputeBashCoverage(samples)
	phases := nativecoder.EvaluateBashRetirementPhases(coverage)
	translation := nativecoder.MeasureGitHubShellTranslation(samples)
	criteria := nativecoder.EvaluateBashRetirementCriteriaWithEvidence(nativecoder.BashRetirementEvidence{
		Coverage:          coverage,
		GitHub:            &translation,
		CoderSystemPrompt: coderSystemPrompt,
	})
	return retirementReport{
		BashCoverageReport:   coverage,
		GitHubPhase1:         translation,
		Phases:               phases,
		ReadyToClose:         nativecoder.BashRetirementReadyToClose(phases),
		Criteria:             criteria,
		CriteriaReadyToClose: nativecoder.BashRetirementCriteriaReadyToClose(criteria),
		Drive: nativecoder.BashRetirementDriveRecord{
			Phase:  "ci514-drive",
			Ticket: "ci#514",
			Surface: []string{
				"github_issue_list", "github_issue_view", "github_issue_comment",
				"github_pr_view", "github_pr_create", "github_pr_comment", "github_pr_merge", "github_pr_review",
				"github_api", "github_auth_status",
				"coder_git status/log/diff/show/blame/remote", "coder_git add/commit/branch/checkout/push/reset/stash/fetch/tag",
				"coder_read", "coder_list", "coder_glob", "coder_grep", "coder_write", "coder_edit",
				"coder_webfetch", "coder_websearch",
			},
			Phases: []nativecoder.BashRetirementDrivePhase{
				{ID: "phase_0_measure", State: nativecoder.BashRetirementDriveStateShipped, Targets: "classifier + bash_retirement_report job", EvidenceAnchor: "bashretirement.go, scripts/bash_retirement_report"},
				{ID: "phase_1_github", Retires: nativecoder.BashCategoryGitHubCLI, State: nativecoder.BashRetirementDriveStateShipped, Targets: "github_* typed tools + githubshell.go bridge", EvidenceAnchor: "githubshell.go, githubapi.go"},
				{ID: "phase_2_git", Retires: nativecoder.BashCategoryGitMutation, State: nativecoder.BashRetirementDriveStateShipped, Targets: "coder_git typed family + gitshell.go guarded bridge", EvidenceAnchor: "gitshell.go"},
				{ID: "phase_3_shellmodes", State: nativecoder.BashRetirementDriveStateOpen, Targets: "shellmodes.go routing for interactive/repair niche", EvidenceAnchor: "shellmodes.go"},
				{ID: "phase_4_longtail", State: nativecoder.BashRetirementDriveStateOpen, Targets: "kubernetes/docker_sandbox/integration typed tools", EvidenceAnchor: "plan registry"},
				{ID: "phase_5_close", State: nativecoder.BashRetirementDriveStateOpen, Targets: "residual shell below threshold, incl. credentials", EvidenceAnchor: "retirementgate.go, retirementcriteria.go"},
			},
			Categories: []nativecoder.BashRetirementDriveCategory{
				{Category: nativecoder.BashCategoryGitHubCLI, RetiredBy: "phase_1_github", Shipped: true},
				{Category: nativecoder.BashCategoryGitMutation, RetiredBy: "phase_2_git", Shipped: true},
				{Category: nativecoder.BashCategoryRepoInspection, RetiredBy: "phase_2_git", Shipped: false},
				{Category: nativecoder.BashCategoryEnvProbing, Shipped: false},
				{Category: nativecoder.BashCategoryResidualShell, Shipped: false},
			},
		},
	}
}

// renderText renders the report as a short ticket-comment summary: the phase 0
// coverage table, the phase 1 GitHub exit criterion and the per-phase exit
// gates.
func renderText(report retirementReport) string {
	coverage := report.BashCoverageReport
	var b strings.Builder
	fmt.Fprintf(&b, "coder_bash retirement coverage (plan v%d, threshold %.0f%%)\n",
		coverage.Version, coverage.Threshold*100)
	fmt.Fprintf(&b, "shell calls: %d classified (%d errored), coverage %.1f%% (%d/%d expressible)\n",
		coverage.ClassifiedCalls, coverage.ErroredCalls, coverage.CoverageRatio*100,
		coverage.CoveredCalls, coverage.ClassifiedCalls)
	fmt.Fprintf(&b, "shipped typed tools cover %d calls; %d stay on the residual shell\n\n",
		coverage.ShippedCalls, coverage.ResidualCalls)
	for _, bucket := range coverage.Buckets {
		fmt.Fprintf(&b, "%-22s %6d calls  %6.1f%%  %s",
			bucket.Category, bucket.Calls, bucket.Share*100, strings.Join(bucket.TargetTools, ","))
		if bucket.Retired {
			b.WriteString("  [retired]")
		}
		b.WriteString("\n")
	}
	if len(coverage.OutstandingCategories) > 0 {
		fmt.Fprintf(&b, "\nstill outstanding: %s\n", strings.Join(coverage.OutstandingCategories, ", "))
	}
	b.WriteString(renderGitHubPhase1Text(report.GitHubPhase1))
	b.WriteString(renderPhaseGatesText(report.Phases, report.ReadyToClose))
	b.WriteString(renderCriteriaText(report.Criteria, report.CriteriaReadyToClose))
	return b.String()
}

// renderCriteriaText renders the exit-criteria checklist: every criterion of
// every phase with its machine-checked status and evidence, so a reader can see
// which criteria are actually checked and which are still unattested instead of
// inferring it from the category gate.
func renderCriteriaText(criteria []nativecoder.BashRetirementPhaseCriteria, readyToClose bool) string {
	var b strings.Builder
	b.WriteString("\nexit criteria:\n")
	for _, phase := range criteria {
		status := "open"
		if len(phase.Criteria) == 0 {
			status = "n/a (no declared criteria)"
		} else if phase.Ready {
			status = "ready"
		} else if !phase.Measured {
			status = "unmeasured"
		} else if !phase.Attested {
			status = "partly unattested"
		}
		fmt.Fprintf(&b, "  %-28s %s\n", phase.ID, status)
		for _, criterion := range phase.Criteria {
			fmt.Fprintf(&b, "      %-11s %s\n", criterion.Status, criterion.Text)
			for _, evidence := range criterion.Evidence {
				fmt.Fprintf(&b, "          evidence: %s\n", evidence)
			}
			for _, blocker := range criterion.Blockers {
				fmt.Fprintf(&b, "          blocked: %s\n", blocker)
			}
		}
	}
	fmt.Fprintf(&b, "\nready to close on criteria alone: %t\n", readyToClose)
	return b.String()
}

// renderGitHubPhase1Text renders the phase 1 exit criterion: the GitHub share of
// shell calls, how much of it the typed surface can express, why the remainder
// stays on the shell, and whether the phase can be closed.
func renderGitHubPhase1Text(report nativecoder.GitHubShellTranslationReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nphase 1 (GitHub) %s: %d/%d gh calls, share %.1f%%, translation %.1f%% (%d translated, %d residual)\n",
		phaseState(report.PhaseReady), report.GitHubCommands, report.Commands,
		report.GitHubShare*100, report.TranslationRatio*100,
		report.TranslatedCommands, report.ResidualCommands)
	if len(report.ResidualReasons) > 0 {
		reasons := make([]string, 0, len(report.ResidualReasons))
		for reason, count := range report.ResidualReasons {
			reasons = append(reasons, fmt.Sprintf("%s (%d)", reason, count))
		}
		sort.Strings(reasons)
		fmt.Fprintf(&b, "  residual: %s\n", strings.Join(reasons, "; "))
	}
	for _, example := range report.ResidualExamples {
		fmt.Fprintf(&b, "  keeps shell: %s\n", example)
	}
	return b.String()
}

// renderPhaseGatesText renders the machine-checked exit gate of every plan phase
// plus the overall close verdict, so a ticket comment can be pasted straight
// from `--format=text`.
func renderPhaseGatesText(phases []nativecoder.BashRetirementPhaseGate, readyToClose bool) string {
	var b strings.Builder
	b.WriteString("\nphase exit gates:\n")
	for _, phase := range phases {
		status := "open"
		if len(phase.Categories) == 0 {
			status = "n/a (definition phase)"
		} else if phase.Ready {
			status = "ready"
		} else if !phase.Measured {
			status = "unmeasured"
		}
		fmt.Fprintf(&b, "  %-28s %s\n", phase.ID, status)
		for _, blocker := range phase.Blockers {
			fmt.Fprintf(&b, "      blocked: %s\n", blocker)
		}
	}
	fmt.Fprintf(&b, "\nready to close the retirement: %t\n", readyToClose)
	return b.String()
}

// phaseState renders the phase readiness as the ticket-facing word.
func phaseState(ready bool) string {
	if ready {
		return "READY"
	}
	return "OPEN"
}
