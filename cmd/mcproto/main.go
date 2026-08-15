// Command mcproto is the repository's non-interactive tool. Every command
// takes its input from flags, writes machine-readable output on request, and
// reports what happened through its exit code. It never prompts.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
)

// Exit codes. They are part of the interface: a script distinguishes a bad
// invocation from a failed run without reading the message.
const (
	exitSuccess = 0
	exitFailure = 1
	exitUsage   = 2
)

// usageError marks a failure caused by how the command was invoked rather than
// by what it tried to do.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func usagef(format string, args ...any) error {
	return usageError{err: fmt.Errorf(format, args...)}
}

const rootUsage = `mcproto manages pinned Minecraft data and generated protocol code.

Usage:
  mcproto <command> [flags]

Commands:
  data    Fetch and validate pinned upstream data trees

Run "mcproto <command> -h" for a command's flags.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	// Release the signal handler before exiting rather than deferring it,
	// because os.Exit runs no deferred call.
	stop()

	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, rootUsage)

		return exitUsage
	}

	var err error
	switch args[0] {
	case "data":
		err = runData(ctx, args[1:], stdout)
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(stdout, rootUsage)

		return exitSuccess
	default:
		_, _ = fmt.Fprintf(stderr, "mcproto: unknown command %q\n\n%s", args[0], rootUsage)

		return exitUsage
	}

	if err == nil {
		return exitSuccess
	}

	_, _ = fmt.Fprintf(stderr, "mcproto: %v\n", err)

	var usage usageError
	if errors.As(err, &usage) {
		return exitUsage
	}

	return exitFailure
}
