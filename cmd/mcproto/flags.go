package main

import (
	"errors"
	"flag"
	"io"
)

// parseFlags parses one command's flags and turns every failure into a usage
// error carrying that command's own help.
//
// It exists so no command has to remember to print help on -h, and so a
// missing or misspelled flag reports the same way everywhere. flag's own
// output is discarded because it writes to stderr on its own schedule, and a
// command's diagnostics belong in one place.
func parseFlags(flags *flag.FlagSet, args []string, usage string) error {
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return helpError{usage: usage}
		}

		return usagef("%v\n\n%s", err, usage)
	}
	if flags.NArg() > 0 {
		return usagef("unexpected argument %q\n\n%s", flags.Arg(0), usage)
	}

	return nil
}

// helpError is -h or --help: not a failure, and not something a command
// should have to handle itself.
type helpError struct{ usage string }

func (helpError) Error() string { return "help requested" }
