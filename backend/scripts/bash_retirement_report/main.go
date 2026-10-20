// Command bash_retirement_report is the Phase 0 "measure" job of the
// coder_bash retirement plan (ci#514).
//
// It reads historical native-coder tool calls and buckets every `coder_bash` /
// `coder_git` call by category and by repository, then prints the coverage
// metric: the fraction of historical shell calls the typed tool surface can
// express. The typed plan itself lives in `backend/nativecoder`
// (`BashRetirementPlanSnapshot`), so the metric can never drift from the plan
// the docs describe.
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
// per-phase exit gates derived from it (`phases`, `ready_to_close`), so one run
// answers both "how covered are we" and "may this phase close".
// `--format=text` prints a short human summary for pasting into a ticket
// comment. The job is read-only and performs no network calls: it only
// classifies the command shapes it is given.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
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

	report, err := readReport(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read transcript export: %v\n", err)
		os.Exit(1)
	}

	envelope := buildEnvelope(report)

	switch strings.ToLower(*format) {
	case "json", "":
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(envelope); err != nil {
			fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
			os.Exit(1)
		}
	case "text":
		fmt.Print(renderText(envelope))
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

// reportEnvelope is the program's output shape: the coverage report plus the
// per-phase exit gates derived from it (ci#514).
type reportEnvelope struct {
	Report nativecoder.BashCoverageReport        `json:"report"`
	Phases []nativecoder.BashRetirementPhaseGate `json:"phases"`
	// ReadyToClose is true only when every phase that retires a category has
	// met its exit criteria against this sample.
	ReadyToClose bool `json:"ready_to_close"`
}

// buildEnvelope pairs a coverage report with the phase exit gates derived from
// it, so JSON and text output always agree on `phases` and `ready_to_close`.
func buildEnvelope(report nativecoder.BashCoverageReport) reportEnvelope {
	phases := nativecoder.EvaluateBashRetirementPhases(report)
	return reportEnvelope{
		Report:       report,
		Phases:       phases,
		ReadyToClose: nativecoder.BashRetirementReadyToClose(phases),
	}
}

// readReport consumes a JSONL export and computes the coverage report.
func readReport(input io.Reader) (nativecoder.BashCoverageReport, error) {
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
		return nativecoder.BashCoverageReport{}, err
	}
	return nativecoder.ComputeBashCoverage(samples), nil
}

// renderText renders the report as a short ticket-comment summary.
func renderText(envelope reportEnvelope) string {
	report := envelope.Report
	var b strings.Builder
	fmt.Fprintf(&b, "coder_bash retirement coverage (plan v%d, threshold %.0f%%)\n",
		report.Version, report.Threshold*100)
	fmt.Fprintf(&b, "shell calls: %d classified (%d errored), coverage %.1f%% (%d/%d expressible)\n",
		report.ClassifiedCalls, report.ErroredCalls, report.CoverageRatio*100,
		report.CoveredCalls, report.ClassifiedCalls)
	fmt.Fprintf(&b, "shipped typed tools cover %d calls; %d stay on the residual shell\n\n",
		report.ShippedCalls, report.ResidualCalls)
	for _, bucket := range report.Buckets {
		fmt.Fprintf(&b, "%-22s %6d calls  %6.1f%%  %s",
			bucket.Category, bucket.Calls, bucket.Share*100, strings.Join(bucket.TargetTools, ","))
		if bucket.Retired {
			b.WriteString("  [retired]")
		}
		b.WriteString("\n")
	}
	if len(report.OutstandingCategories) > 0 {
		fmt.Fprintf(&b, "\nstill outstanding: %s\n", strings.Join(report.OutstandingCategories, ", "))
	}
	fmt.Fprintf(&b, "\nphase exit gates:\n")
	for _, phase := range envelope.Phases {
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
	fmt.Fprintf(&b, "\nready to close the retirement: %t\n", envelope.ReadyToClose)
	return b.String()
}
