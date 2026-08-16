package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/capture"
)

const inspectUsage = `mcproto inspect prints the records of a capture.

Usage:
  mcproto inspect --input <file> [--format text|json] [--filter <expression>]
                  [--payload none|hex] [--limit N]

A filter is a space-separated conjunction of terms, each field<op>value:

  fields    kind state name direction (text)   id sequence frame bytes (numbers)
  text      = != ~=            (~= is substring)
  numbers   = != > >= < <=     (0x prefixes hex)

Examples:
  mcproto inspect --input session.mcpcap
  mcproto inspect --input session.mcpcap --filter 'state=play kind=packet'
  mcproto inspect --input session.mcpcap --filter 'name~=chunk bytes>1024' --format json
`

// inspectRecord is one line of machine-readable output.
type inspectRecord struct {
	Sequence    uint64 `json:"sequence"`
	Frame       uint64 `json:"frame"`
	Kind        string `json:"kind"`
	Direction   string `json:"direction"`
	Elapsed     string `json:"elapsed"`
	State       string `json:"state"`
	ID          int32  `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Bytes       int    `json:"bytes"`
	Redacted    bool   `json:"redacted,omitempty"`
	SecretLabel string `json:"secretLabel,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Payload     string `json:"payload,omitempty"`
}

func runInspect(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	input := flags.String("input", "", "capture file to read")
	format := flags.String("format", "text", "output format: text or json")
	expression := flags.String("filter", "", "filter expression; empty matches everything")
	payload := flags.String("payload", "none", "include payload bytes: none or hex")
	limit := flags.Int("limit", 0, "stop after this many matching records; 0 means all")

	if err := parseFlags(flags, args, inspectUsage); err != nil {
		return err
	}
	if *input == "" {
		return usagef("--input is required\n\n%s", inspectUsage)
	}
	if *payload != "none" && *payload != "hex" {
		return usagef("unknown --payload %q; use none or hex", *payload)
	}
	if *format != "text" && *format != "json" {
		return usagef("unknown format %q; use text or json", *format)
	}

	selector, err := parseFilter(*expression)
	if err != nil {
		return err
	}

	file, err := os.Open(*input)
	if err != nil {
		return fmt.Errorf("open capture: %w", err)
	}
	defer func() { _ = file.Close() }()

	reader, err := capture.NewReader(file)
	if err != nil {
		return fmt.Errorf("read capture: %w", err)
	}

	encoder := json.NewEncoder(stdout)
	var shown int

	for {
		record, err := reader.Next()
		if err != nil {
			if errors.Is(err, capture.ErrEndOfCapture) {
				break
			}

			// A truncated capture is a normal thing to inspect: it is what a
			// killed process leaves. Everything before the cut has already
			// been printed, so the cut is reported and the exit is clean.
			if errors.Is(err, capture.ErrTruncated) {
				_, _ = fmt.Fprintf(stdout, "# capture is truncated after %d records\n", shown)

				break
			}

			return fmt.Errorf("read capture: %w", err)
		}
		if !selector.matches(record) {
			continue
		}

		line := inspectLine(record, *payload == "hex")
		if *format == "json" {
			if err := encoder.Encode(line); err != nil {
				return err
			}
		} else {
			printInspectLine(stdout, line)
		}

		shown++
		if *limit > 0 && shown >= *limit {
			break
		}
	}

	return nil
}

func inspectLine(record capture.Record, withPayload bool) inspectRecord {
	line := inspectRecord{
		Sequence:    record.Sequence,
		Frame:       record.Frame,
		Kind:        kindName(record.Kind),
		Direction:   directionName(record.Direction),
		Elapsed:     record.Elapsed.Round(time.Microsecond).String(),
		State:       string(record.State),
		ID:          record.PacketID,
		Name:        record.Name,
		Bytes:       len(record.Payload),
		Redacted:    record.Redacted,
		SecretLabel: record.SecretLabel,
		Reason:      record.Reason,
	}
	if record.Redacted {
		line.Bytes = record.OriginalLen
	}
	if withPayload && !record.Redacted {
		line.Payload = hex.EncodeToString(record.Payload)
	}

	return line
}

func printInspectLine(stdout io.Writer, line inspectRecord) {
	name := line.Name
	if name == "" {
		name = "-"
	}
	_, _ = fmt.Fprintf(
		stdout,
		"%6d %8s %-9s %-11s %-13s %#04x %-24s %6d%s\n",
		line.Sequence, line.Elapsed, line.Kind, line.Direction, line.State,
		line.ID, name, line.Bytes, redactionMark(line),
	)
	if line.Payload != "" {
		_, _ = fmt.Fprintf(stdout, "       %s\n", line.Payload)
	}
}

func redactionMark(line inspectRecord) string {
	switch {
	case line.Redacted:
		return " withheld"
	case line.Reason != "":
		return " " + line.Reason
	case line.SecretLabel != "":
		return " " + line.SecretLabel
	default:
		return ""
	}
}

func kindName(kind capture.Kind) string {
	switch kind {
	case capture.KindRawFrame:
		return "raw"
	case capture.KindPacket:
		return "packet"
	case capture.KindSecret:
		return "secret"
	case capture.KindRejected:
		return "rejected"
	case capture.KindTrailer:
		return "trailer"
	default:
		return fmt.Sprintf("kind%d", kind)
	}
}

func directionName(direction protocol.Direction) string {
	switch direction {
	case protocol.DirectionClientbound:
		return "clientbound"
	case protocol.DirectionServerbound:
		return "serverbound"
	default:
		return "-"
	}
}
