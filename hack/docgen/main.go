// docgen regenerates and verifies the project's auto-generated documentation.
//
// It is invoked from the Makefile via:
//
//	go run ./hack/docgen <subcommand> [flags]
//
// Subcommands:
//
//	flags              — emit docs/flags.md from cmd/main.go flag declarations.
//	metrics            — emit docs/metrics.md from prometheus.New*Vec calls.
//	events             — emit docs/events.md from controller event Recorder calls.
//	samples            — emit docs/samples.md from config/samples/*.yaml.
//	verify-conditions  — lint docs/conditions.md against meta.SetStatusCondition calls.
//
// Each generator subcommand accepts:
//
//	--out PATH    — output file (required for the four generators).
//	--check       — render to a temp file, diff against PATH, exit non-zero on drift.
//	--root PATH   — repo root override (defaults to the working directory).
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	fs := flag.NewFlagSet(sub, flag.ExitOnError)
	out := fs.String("out", "", "output file path")
	check := fs.Bool("check", false, "verify mode: diff generated output against --out, do not write")
	root := fs.String("root", ".", "repo root (defaults to working directory)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	var err error
	switch sub {
	case "flags":
		err = runGenerator(*root, *out, *check, generateFlags)
	case "metrics":
		err = runGenerator(*root, *out, *check, generateMetrics)
	case "events":
		err = runGenerator(*root, *out, *check, generateEvents)
	case "samples":
		err = runGenerator(*root, *out, *check, generateSamples)
	case "verify-conditions":
		// verify-conditions takes the doc path as the first positional arg.
		args := fs.Args()
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "verify-conditions: doc path argument required")
			os.Exit(2)
		}
		err = verifyConditions(*root, args[0])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", sub)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "docgen %s: %v\n", sub, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: docgen <subcommand> [--out PATH] [--check] [--root PATH]

Subcommands:
  flags              emit flag reference from cmd/main.go
  metrics            emit metrics reference from prometheus.New*Vec calls
  events             emit event-reason reference from Recorder.Event* calls
  samples            emit sample-CR gallery from config/samples/*.yaml
  verify-conditions  lint conditions doc against meta.SetStatusCondition calls`)
}
