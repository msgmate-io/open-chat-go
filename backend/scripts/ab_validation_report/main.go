// Command ab_validation_report is the evidence job of the opencode -> native
// coder migration (umbrella ci#585, A/B validation ci#583).
//
// It reads the paired-run evidence recorded while an opencode-era bot and its
// native msgmate-coder replacement worked the same ticket, then reports which
// bot families may be flipped. The gates live in `backend/nativecoder`
// (`ABValidationBlockers` / `ABValidationFlipAllowed`), so the report can never
// drift from the gate the rollout checks.
//
// Input is JSONL, one paired run per line, so it can be produced from any
// ticket comment or transcript export without a schema dependency:
//
//	{"stage":"plan","legacy_bot":"issue-plan-bot","native_runtime":"msgmate-coder-plan-bot","ticket":"ci#812","legacy":{"task_completed":true,"pr_opened":true},"native":{"task_completed":true,"pr_opened":true}}
//
// Lines that are blank or start with `#` are ignored. With no input file the job
// prints the empty plan: every family is listed with 0 paired runs and blocked,
// which is exactly the state ci#583 starts from.
//
// Usage:
//
//	go run ./backend/scripts/ab_validation_report                      # empty plan (markdown)
//	go run ./backend/scripts/ab_validation_report runs.jsonl           # recorded runs (markdown)
//	go run ./backend/scripts/ab_validation_report runs.jsonl --format=json
//	go run ./backend/scripts/ab_validation_report --umbrella           # ci#585 checklist
//
// The job is read-only and performs no network calls: it never dispatches a bot
// and never touches the deployment.
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

func main() {
	format := flag.String("format", "markdown", "output format: markdown or json")
	umbrella := flag.Bool("umbrella", false, "render the ci#585 migration umbrella checklist instead of the A/B table")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: %s [runs.jsonl] [--format=markdown|json] [--umbrella]\n", os.Args[0])
		flag.PrintDefaults()
	}
	// The stdlib flag package stops at the first positional argument, so move
	// flags ahead of the file to accept the documented
	// `job runs.jsonl --format=json` invocation.
	flag.CommandLine.Parse(flagsFirst(os.Args[1:]))

	cases := nativecoder.ABValidationCases()
	if path := flag.Arg(0); path != "" && path != "-" {
		file, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open %s: %v\n", path, err)
			os.Exit(1)
		}
		defer file.Close()
		cases, err = readCases(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read paired runs: %v\n", err)
			os.Exit(1)
		}
	}

	if *umbrella {
		steps := nativecoder.MigrationUmbrellaSteps(cases)
		switch strings.ToLower(*format) {
		case "json":
			raw, err := json.MarshalIndent(steps, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "encode umbrella: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(string(raw))
		case "markdown", "text", "":
			fmt.Print(nativecoder.RenderMigrationUmbrellaMarkdown(steps))
		default:
			fmt.Fprintf(os.Stderr, "unknown --format %q (want markdown, json or text)\n", *format)
			os.Exit(2)
		}
		fmt.Printf("\nready for opencode retirement: %t\n", nativecoder.MigrationUmbrellaReady(steps))
		return
	}

	switch strings.ToLower(*format) {
	case "markdown", "":
		fmt.Print(nativecoder.RenderABValidationMarkdown(cases))
	case "json":
		out, err := nativecoder.ABValidationJSON(cases)
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(out)
	case "text":
		fmt.Print(renderText(cases))
	default:
		fmt.Fprintf(os.Stderr, "unknown --format %q (want markdown, json or text)\n", *format)
		os.Exit(2)
	}
}

// flagsFirst reorders `args` so that flags precede positional arguments,
// letting the job accept `runs.jsonl --format=json` as documented.
func flagsFirst(args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && arg == "--format" && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, arg)
	}
	return append(flags, positional...)
}

// readCases consumes a JSONL export of paired runs.
func readCases(input io.Reader) ([]nativecoder.ABValidationCase, error) {
	cases := []nativecoder.ABValidationCase{}
	scanner := bufio.NewScanner(input)
	// Ticket comments with inline payloads exceed the 64KiB default; 1MiB
	// covers one paired run while still bounding memory.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var entry nativecoder.ABValidationCase
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("line %d: %w", len(cases)+1, err)
		}
		cases = append(cases, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return cases, nil
}

// renderText renders the report as a short ticket-comment summary.
func renderText(cases []nativecoder.ABValidationCase) string {
	report := nativecoder.ABValidationReportFor(cases)
	var b strings.Builder
	fmt.Fprintf(&b, "opencode/native A/B validation (%s, umbrella %s), plan v%d/%d cases per family\n",
		report.Ticket, report.Umbrella, report.MinCasesPerFamily, report.MinCasesPerFamily)
	fmt.Fprintf(&b, "paired runs: %d; families validated: %d/%d; ci#583 sign-off: %t\n\n",
		len(report.Cases), len(report.ValidatedFamilies), len(report.FlipAllowed), report.SignOff)
	for _, stage := range nativecoder.NativeBotStages() {
		state := "blocked"
		if report.FlipAllowed[stage.Stage] {
			state = "flip allowed"
		}
		fmt.Fprintf(&b, "%-28s %-12s %s\n", stage.Stage, stage.NativeRuntime, state)
	}
	if len(report.Blockers) > 0 {
		fmt.Fprintf(&b, "\nblockers:\n")
		for _, blocker := range report.Blockers {
			fmt.Fprintf(&b, "- %s\n", blocker)
		}
	}
	return b.String()
}
