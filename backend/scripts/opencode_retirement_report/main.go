// Command opencode_retirement_report renders the ci#585 umbrella evidence.
//
// The opencode retirement gate lives in `backend/nativecoder`
// (`OpencodeRetirementEvidence`); this job only feeds it the observed fleet
// state and prints the per-step checklist the ticket comments are written from,
// so the report and the gate cannot drift apart.
//
// Input is a JSON state file (or stdin) mirroring
// `nativecoder.OpencodeRetirementState`:
//
//	{
//	  "configured_runtimes": {"msgmate-coder-plan-bot": "native-coder"},
//	  "rolled_out_through": "repl",
//	  "rollback_proven_on": "repl",
//	  "native_stable_days": 14,
//	  "parity_signed_off": true,
//	  "ab_validation_done": true,
//	  "retirement_delta_ok": true
//	}
//
// Usage:
//
//	go run ./backend/scripts/opencode_retirement_report state.json
//	go run ./backend/scripts/opencode_retirement_report state.json --format=json
//	go run ./backend/scripts/opencode_retirement_report state.json --format=delta
//	go run ./backend/scripts/opencode_retirement_report state.json --require-ready
//
// `--format=delta` prints the flip config delta of ci#582 (rendered from the
// same flip registry) plus how many of its entries are still outstanding for the
// observed fleet; it is applied through `publish-open-chat-config.yaml`
// (vault-sync), `DRY_RUN=true` first.
//
// The job is read-only and performs no network calls.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"backend/nativecoder"
)

// retirementState mirrors the JSON shape of the gate state.
type retirementState struct {
	ConfiguredRuntimes map[string]string `json:"configured_runtimes"`
	RolledOutThrough   string            `json:"rolled_out_through"`
	RollbackProvenOn   string            `json:"rollback_proven_on"`
	NativeStableDays   int               `json:"native_stable_days"`
	ParitySignedOff    bool              `json:"parity_signed_off"`
	ABValidationDone   bool              `json:"ab_validation_done"`
	RetirementDeltaOK  bool              `json:"retirement_delta_ok"`
}

// report is the machine-readable output.
type report struct {
	Ready    bool                             `json:"ready"`
	Blockers []string                         `json:"blockers"`
	Steps    []nativecoder.RetirementEvidence `json:"steps"`
	// PendingFlip are the ci#582 flip entries still outstanding for the
	// observed fleet, so the config delta and the gate are read together.
	PendingFlip []nativecoder.OpencodeRetirementDeltaEntry `json:"pending_flip"`
}

func main() {
	format := flag.String("format", "markdown", "output format: markdown or json")
	requireReady := flag.Bool("require-ready", false, "exit non-zero while the umbrella gate is blocked")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [state.json] [--format=markdown|json] [--require-ready]\n", os.Args[0])
		flag.PrintDefaults()
	}
	// The stdlib flag package stops at the first positional argument, so
	// reorder flags ahead of the file to accept the documented
	// `job state.json --format=json` invocation.
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

	var raw retirementState
	if err := json.NewDecoder(input).Decode(&raw); err != nil {
		fmt.Fprintf(os.Stderr, "decode state: %v\n", err)
		os.Exit(1)
	}
	state := nativecoder.OpencodeRetirementState{
		ConfiguredRuntimes: raw.ConfiguredRuntimes,
		RolledOutThrough:   raw.RolledOutThrough,
		RollbackProvenOn:   raw.RollbackProvenOn,
		NativeStableDays:   raw.NativeStableDays,
		ParitySignedOff:    raw.ParitySignedOff,
		ABValidationDone:   raw.ABValidationDone,
		RetirementDeltaOK:  raw.RetirementDeltaOK,
	}
	rep := report{
		Ready:       nativecoder.OpencodeRetirementReady(state),
		Blockers:    nativecoder.OpencodeRetirementBlockers(state),
		Steps:       nativecoder.OpencodeRetirementEvidence(state),
		PendingFlip: nativecoder.PendingOpencodeRetirementFlipDelta(state),
	}

	switch strings.ToLower(*format) {
	case "markdown", "":
		fmt.Print(nativecoder.RenderOpencodeRetirementEvidenceMarkdown(state))
	case "delta":
		fmt.Print(nativecoder.RenderOpencodeRetirementDeltaMarkdown(nativecoder.OpencodeRetirementFlipDelta()))
		fmt.Fprintf(os.Stdout, "\nstill outstanding for the observed fleet: %d of %d flip entries\n",
			len(rep.PendingFlip), len(nativecoder.OpencodeRetirementFlipDelta()))
	case "json":
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(rep); err != nil {
			fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown --format %q (want markdown or json)\n", *format)
		os.Exit(2)
	}

	if *requireReady && !rep.Ready {
		fmt.Fprintf(os.Stderr, "opencode retirement gate is blocked: %s\n", strings.Join(rep.Blockers, "; "))
		os.Exit(3)
	}
}

// flagsFirst reorders `args` so that flags precede positional arguments,
// letting the job accept `state.json --format=json` as documented.
func flagsFirst(args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			continue
		}
		positional = append(positional, arg)
	}
	return append(flags, positional...)
}
