// Command native_dispatch_report is the evidence job of ci#757 (retire the
// `trigger-open-chat-coding-interaction.yaml` workflow-only dispatch route in
// favour of the native hive-mind agent dispatch).
//
// It reads the observed dispatch state of the development-agent fleet - chat
// metadata only, never a transcript, token or credential - and reports the
// migration checklist plus the blockers. The gates live in `backend/nativecoder`
// (`NativeDispatchBlockers` / `NativeDispatchReady`), so this report can never
// drift from what the rollout checks.
//
// Input is the JSON document `NativeDispatchStateJSON` produces, for example:
//
//	{"chats":[{"name":"lead","repo":"msgmate-io/open-chat-go","role":"lead","tools":["hive_mind_agent_dispatch","mcp:open-chat-opencode-mcp:openchat_search_interactions"],"dispatch_round_trip":true,"context_recall_round_trip":true}]}
//
// With no input file the job prints the empty state: every step is blocked,
// which is exactly where ci#757 starts.
//
// Usage:
//
//	go run ./backend/scripts/native_dispatch_report                        # empty state (markdown)
//	go run ./backend/scripts/native_dispatch_report fleet.json             # observed fleet
//	go run ./backend/scripts/native_dispatch_report fleet.json --format=json
//	go run ./backend/scripts/native_dispatch_report fleet.json --check     # non-zero exit while blocked
//	go run ./backend/scripts/native_dispatch_report --parity               # workflow -> native input map
//
// The job is read-only and performs no network calls: it never dispatches a bot
// and never touches the deployment.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"backend/nativecoder"
)

func main() {
	format := flag.String("format", "markdown", "output format: markdown or json")
	check := flag.Bool("check", false, "exit non-zero while a blocker remains")
	parity := flag.Bool("parity", false, "print the legacy-workflow to native-input parity map instead")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: %s [fleet.json] [--format=markdown|json] [--check] [--parity]\n", os.Args[0])
		flag.PrintDefaults()
	}
	// The stdlib flag package stops at the first positional argument, so move
	// flags ahead of the file to accept the documented
	// `job fleet.json --format=json` invocation.
	flag.CommandLine.Parse(flagsFirst(os.Args[1:]))

	if *parity {
		switch strings.ToLower(*format) {
		case "json":
			raw, err := json.MarshalIndent(nativecoder.LegacyDispatchInputParity(), "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "encode parity map: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(string(raw))
		default:
			fmt.Print(renderParity())
		}
		if gaps := nativecoder.LegacyDispatchParityGaps(); len(gaps) > 0 {
			fmt.Printf("\nnative parity incomplete: %d input(s) without an equivalent\n", len(gaps))
			if *check {
				os.Exit(1)
			}
		}
		return
	}

	state := nativecoder.NativeDispatchState{}
	if path := flag.Arg(0); path != "" && path != "-" {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read fleet state: %v\n", err)
			os.Exit(1)
		}
		if err := json.Unmarshal(raw, &state); err != nil {
			fmt.Fprintf(os.Stderr, "parse fleet state: %v\n", err)
			os.Exit(1)
		}
	}

	switch strings.ToLower(*format) {
	case "json":
		raw, err := json.MarshalIndent(nativecoder.NativeDispatchEvidence(state), "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode evidence: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(raw))
	case "markdown", "":
		fmt.Print(nativecoder.RenderNativeDispatchEvidenceMarkdown(state))
	default:
		fmt.Fprintf(os.Stderr, "unknown --format %q (want markdown or json)\n", *format)
		os.Exit(2)
	}
	fmt.Printf("\nready to retire the workflow-only dispatch route: %t\n", nativecoder.NativeDispatchReady(state))
	if *check && !nativecoder.NativeDispatchReady(state) {
		os.Exit(1)
	}
}

// flagsFirst reorders `args` so that flags precede positional arguments, letting
// the job accept `fleet.json --format=json` as documented.
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

// renderParity renders the workflow-input to native-equivalent map as a table.
func renderParity() string {
	var b strings.Builder
	fmt.Fprintf(&b, "legacy %s/%s -> native %s\n\n",
		nativecoder.LegacyDispatchWorkflowRepo, nativecoder.LegacyDispatchWorkflowName, nativecoder.NativeDispatchRoute)
	b.WriteString("| workflow input | purpose | native equivalent |\n| --- | --- | --- |\n")
	for _, input := range nativecoder.LegacyDispatchInputParity() {
		equivalent := input.Equivalent
		if input.Gap != "" {
			equivalent = "GAP: " + input.Gap
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", input.Name, input.Purpose, equivalent)
	}
	return b.String()
}
