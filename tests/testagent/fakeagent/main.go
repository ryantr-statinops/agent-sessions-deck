//go:build linux

// Command fakeagent is the Stage 01 deterministic fixture that stands in for a
// coding agent CLI.
//
// It is started by a test with --mode=<mode> and inherits a full-duplex
// control channel on fd 3 (see the control package). The fixture writes
// newline-delimited JSON events to that channel and reads commands from it.
// Terminal bytes go only to stdout/stderr, which are bound to the PTY.
//
// The fixture never sleeps to make progress: every mode advances on an
// explicit control command, an explicit signal, or explicit terminal input.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
)

const modeHelper = "helper"

// options holds the resolved command line.
type options struct {
	mode       string
	label      string
	exitCode   int
	floodBytes int
	floodLines int
	role       string
	spawn      string
}

func main() {
	var opt options
	fs := flag.NewFlagSet("testagent", flag.ContinueOnError)
	fs.StringVar(&opt.mode, "mode", "", "fixture mode (required)")
	fs.StringVar(&opt.label, "label", "", "label echoed into terminal output and events")
	fs.IntVar(&opt.exitCode, "exit-code", 0, "exit status for exit-code mode")
	fs.IntVar(&opt.floodBytes, "flood-bytes", 0, "total bytes to emit in flood mode")
	fs.IntVar(&opt.floodLines, "flood-lines", 0, "record count to emit in flood mode")
	fs.StringVar(&opt.role, "role", "", "process role reported by helper mode")
	fs.StringVar(&opt.spawn, "spawn", "", "role for a helper to spawn in turn")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if opt.mode == "" {
		fmt.Fprintln(os.Stderr, "fakeagent: --mode is required")
		os.Exit(2)
	}

	if opt.mode == modeHelper {
		runHelper(opt)
		return
	}

	f, err := newFixture(opt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeagent: %v\n", err)
		os.Exit(2)
	}
	f.run()
}

func describe(opt options) string {
	switch opt.mode {
	case modeExitCode:
		return "exit-code=" + strconv.Itoa(opt.exitCode)
	case modeFlood:
		return "flood-bytes=" + strconv.Itoa(opt.floodBytes)
	default:
		return ""
	}
}
