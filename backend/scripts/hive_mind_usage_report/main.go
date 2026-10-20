// Command hive_mind_usage_report answers the ci#757 question "how well are the
// hive-mind dispatch/recall tools actually used in past runs?" from a
// read-only transcript export.
//
// It classifies every historic tool call as native hive-mind dispatch,
// observe, interaction recall, the legacy `trigger-open-chat-coding-interaction`
// workflow call, or unrelated, then prints a share/verdict summary that can be
// pasted into the ticket.
//
// The job performs no network calls and prints nothing but tool names, roles
// and chat identifiers: no prompt bodies, tokens or user data are echoed.
//
// Usage:
//
//	go run ./backend/scripts/hive_mind_usage_report < calls.jsonl
//	go run ./backend/scripts/hive_mind_usage_report calls.jsonl --format=text
//	go run ./backend/scripts/hive_mind_usage_report calls.jsonl --evidence evidence.json
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
	format := flag.String("format", "json", "output format: json or text")
	evidencePath := flag.String("evidence", "", "optional dispatch-route evidence JSON to attach")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [calls.jsonl] [--format=json|text] [--evidence evidence.json]\n", os.Args[0])
		flag.PrintDefaults()
	}
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
	report := nativecoder.ComputeHiveMindUsage(samples)

	evidence := nativecoder.DispatchRouteEvidence{}
	if *evidencePath != "" {
		raw, err := os.ReadFile(*evidencePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open %s: %v\n", *evidencePath, err)
			os.Exit(1)
		}
		if err := json.Unmarshal(raw, &evidence); err != nil {
			fmt.Fprintf(os.Stderr, "decode %s: %v\n", *evidencePath, err)
			os.Exit(1)
		}
	}

	switch strings.ToLower(*format) {
	case "json", "":
		fmt.Println(nativecoder.DispatchRouteJSON(evidence, &report))
	case "text":
		fmt.Print(nativecoder.RenderDispatchRouteMarkdown(evidence, &report))
	default:
		fmt.Fprintf(os.Stderr, "unknown --format %q (want json or text)\n", *format)
		os.Exit(2)
	}
}

// readSamples consumes a JSONL export of historic tool calls.
func readSamples(input io.Reader) ([]nativecoder.HiveMindCallSample, error) {
	samples := []nativecoder.HiveMindCallSample{}
	scanner := bufio.NewScanner(input)
	// Transcript lines carry long inline payloads; 4MiB bounds memory while
	// still fitting one tool call.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var sample nativecoder.HiveMindCallSample
		if err := json.Unmarshal([]byte(line), &sample); err != nil {
			return nil, fmt.Errorf("parse %q: %w", truncate(line, 80), err)
		}
		samples = append(samples, sample)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return samples, nil
}

// flagsFirst reorders `args` so flags precede positional arguments, letting the
// job accept the documented `calls.jsonl --format=text` invocation.
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

func isValueFlag(arg string) bool {
	switch arg {
	case "--format", "-format", "--evidence", "-evidence":
		return true
	}
	return false
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
