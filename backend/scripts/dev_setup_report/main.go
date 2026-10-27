// Command dev_setup_report is the audit/evidence job of ci#756 (the
// development set-up of the msgmate coder fleet).
//
// It answers the operator question the ticket starts from: "which bot, for
// which repository, gets which development set-up before the model's first
// turn?" — and names the bots that would still start half-set-up. The gates live
// in `backend/nativecoder` (devsetup.go / devsetupmatrix.go / devsetupfleet.go),
// so this report can never drift from what the sandbox actually runs.
//
// The output is data-only: bot names, repository slugs and profile names. It
// never prints a token, kubeconfig, private module path or user data.
//
// Usage:
//
//	go run ./backend/scripts/dev_setup_report                    # fleet table (markdown)
//	go run ./backend/scripts/dev_setup_report --matrix           # per-repo matrix
//	go run ./backend/scripts/dev_setup_report --config           # ci delta fragment
//	go run ./backend/scripts/dev_setup_report --format=json      # fleet plans as JSON
//	go run ./backend/scripts/dev_setup_report --check            # non-zero on a coverage gap
//
// The job is read-only and performs no network calls: it never provisions a
// sandbox and never touches the deployment.
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
	check := flag.Bool("check", false, "exit non-zero when a coder bot has a coverage gap")
	matrix := flag.Bool("matrix", false, "print the per-repository matrix instead of the fleet table")
	config := flag.Bool("config", false, "print the non-secret coder_dev_setup fragment instead of the fleet table")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: %s [--format=markdown|json] [--check] [--matrix] [--config]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	plans := nativecoder.DevSetupFleetPlans(nativecoder.CoderDevSetupFleet)
	gaps := nativecoder.DevSetupFleetGaps(plans)

	switch {
	case *config:
		fmt.Print(nativecoder.RenderDevSetupConfigFragment())
	case *matrix:
		repos := make([]string, 0, len(plans))
		for _, plan := range plans {
			for _, repo := range plan.Repos {
				repos = append(repos, repo.Repo)
			}
		}
		plans := nativecoder.DevSetupMatrixPlan(repos)
		if *format == "json" {
			printJSON(plans)
		} else {
			fmt.Print(nativecoder.RenderDevSetupMatrixMarkdown(plans))
		}
		if problems := nativecoder.ValidateDevSetupMatrix(plans); len(problems) > 0 {
			fmt.Printf("\nmatrix defects: %d\n", len(problems))
			for _, problem := range problems {
				fmt.Printf("- %s\n", problem)
			}
			os.Exit(1)
		}
	default:
		if *format == "json" {
			printJSON(plans)
		} else {
			fmt.Print(nativecoder.RenderDevSetupFleetMarkdown(plans))
		}
	}

	fmt.Printf("\nci#756 coder development set-up: %d bot(s), %d coverage gap(s)\n", len(plans), len(gaps))
	if *check && len(gaps) > 0 {
		os.Exit(1)
	}
}

func printJSON(value interface{}) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(strings.TrimRight(string(raw), "\n"))
}
