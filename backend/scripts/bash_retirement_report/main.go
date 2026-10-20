// Command bash_retirement_report is the measurement job of the coder_bash
// retirement plan (ci#514): the phase 0 coverage metric and the phase 1 GitHub
// exit criterion.
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
// Output defaults to pretty JSON on stdout (`github_phase1` carries the phase 1
// readiness metric); `--format=text` prints a short human summary for pasting
// into a ticket comment. The job is read-only and performs no network calls: it
// only classifies the command shapes it is given.
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

// retirementReport is the job's output: the phase 0 coverage metric plus the
// phase 1 GitHub exit criterion measured on the same samples.
type retirementReport struct {
	nativecoder.BashCoverageReport
	// GitHubPhase1 is the phase 1 readiness metric, emitted as its own object
	// so a reader can consume the coverage numbers exactly as before.
	GitHubPhase1 nativecoder.GitHubShellTranslationReport `json:"github_phase1"`
}

func main() {
	format := flag.String("format", "json", "output format: json or text")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [calls.jsonl] [--format=json|text]\n", os.Args[0])
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
	report := buildReport(samples)

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

// flagsFirst reorders `args` so that flags precede positional arguments,
// letting the job accept `calls.jsonl --format=text` as documented.
func flagsFirst(args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			// Carry an explicit `--format text` value along with the flag.
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
	return arg == "--format" || arg == "-format"
}

// readSamples consumes a JSONL export into the sample slice both metrics are
// computed from, so phase 0 and phase 1 always see exactly the same evidence.
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

// buildReport computes both plan metrics over one sample set.
func buildReport(samples []nativecoder.BashCallSample) retirementReport {
	return retirementReport{
		BashCoverageReport: nativecoder.ComputeBashCoverage(samples),
		GitHubPhase1:       nativecoder.MeasureGitHubShellTranslation(samples),
	}
}

// renderText renders the report as a short ticket-comment summary: the phase 0
// coverage table followed by the phase 1 GitHub exit criterion.
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

// phaseState renders the phase readiness as the ticket-facing word.
func phaseState(ready bool) string {
	if ready {
		return "READY"
	}
	return "OPEN"
}
