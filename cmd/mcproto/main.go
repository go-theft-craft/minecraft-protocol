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
	// exitSuccess is a command that did what it was asked.
	exitSuccess = 0
	// exitFailure is a command that failed at what it was asked: a file it
	// could not read, a capture it could not write.
	exitFailure = 1
	// exitUsage is a command that was asked wrongly. Nothing was attempted.
	exitUsage = 2
	// exitPeer is a failure that belongs to the other end: a refused
	// connection, a timeout, a peer that disconnected or spoke wrongly.
	exitPeer = 3
	// exitVerify is a check that ran and did not match. It is separate from
	// exitFailure because a mismatch is a result, not a malfunction.
	exitVerify = 4
)

// usageError marks a failure caused by how the command was invoked rather than
// by what it tried to do.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func usagef(format string, args ...any) error {
	return usageError{err: fmt.Errorf(format, args...)}
}

// peerError marks a failure that belongs to the peer or the network rather
// than to this program.
type peerError struct{ err error }

func (e peerError) Error() string { return e.err.Error() }
func (e peerError) Unwrap() error { return e.err }

func peerf(format string, args ...any) error {
	return peerError{err: fmt.Errorf(format, args...)}
}

// verifyError marks a check that ran to completion and did not match.
type verifyError struct{ err error }

func (e verifyError) Error() string { return e.err.Error() }
func (e verifyError) Unwrap() error { return e.err }

func verifyf(format string, args ...any) error {
	return verifyError{err: fmt.Errorf(format, args...)}
}

// exitCodeFor maps an error to its documented code. Every command reports
// through this one function, so a new command cannot invent a code.
func exitCodeFor(err error) int {
	if err == nil {
		return exitSuccess
	}

	var (
		usage  usageError
		peer   peerError
		verify verifyError
	)
	switch {
	case errors.As(err, &usage):
		return exitUsage
	case errors.As(err, &peer):
		return exitPeer
	case errors.As(err, &verify):
		return exitVerify
	default:
		return exitFailure
	}
}

const rootUsage = `mcproto inspects Minecraft protocol traffic and the data behind it.

Usage:
  mcproto <command> [flags]

Commands:
  version   Print the version of this tool and the protocols it speaks
  data      Fetch and validate pinned upstream data trees
  packet    Decode and encode one packet body
  status    Query a server's status
  login     Log in to a server and report the profile
  capture   Record a connection to a capture file
  inspect   Print the records of a capture
  replay    Replay a capture, offline or to a peer

Exit codes:
  0  success
  1  the command failed at what it was asked
  2  the command was asked wrongly; nothing was attempted
  3  the peer or the network failed
  4  a check ran and did not match

Run "mcproto <command> -h" for a command's flags.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	// Release the signal handler before exiting rather than deferring it,
	// because os.Exit runs no deferred call.
	stop()

	os.Exit(code)
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, rootUsage)

		return exitUsage
	}

	var err error
	switch args[0] {
	case "version":
		err = runVersion(args[1:], stdout)
	case "data":
		err = runData(ctx, args[1:], stdout)
	case "packet":
		err = runPacket(args[1:], stdin, stdout)
	case "status":
		err = runStatus(ctx, args[1:], stdout)
	case "login":
		err = runLogin(ctx, args[1:], stdout)
	case "capture":
		err = runCapture(ctx, args[1:], stdout, stderr)
	case "inspect":
		err = runInspect(args[1:], stdout)
	case "replay":
		err = runReplay(ctx, args[1:], stdout)
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

	// Help is a request, not a failure: it goes to stdout and exits zero, so
	// `mcproto replay -h | less` works the way anyone would expect.
	var help helpError
	if errors.As(err, &help) {
		_, _ = fmt.Fprint(stdout, help.usage)

		return exitSuccess
	}

	_, _ = fmt.Fprintf(stderr, "mcproto: %v\n", err)

	return exitCodeFor(err)
}
