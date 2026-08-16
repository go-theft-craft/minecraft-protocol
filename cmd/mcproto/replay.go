package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/capture"
	"github.com/go-theft-craft/minecraft-protocol/protocols"
	"github.com/go-theft-craft/minecraft-protocol/replay"
)

const replayUsage = `mcproto replay drives a capture back through a decoder or a peer.

Usage:
  mcproto replay --input <file> [--verify] [--mode fast|recorded|scaled] [--scale 1.0]
                 [--format text|json]
  mcproto replay --input <file> --connect <host:port> --direction clientbound|serverbound

Without --connect, every recorded frame is decoded again by this code. With
--verify the result is compared against the digest the capture recorded, and a
mismatch exits 4.

Examples:
  mcproto replay --input session.mcpcap --verify
  mcproto replay --input session.mcpcap --mode recorded
  mcproto replay --input session.mcpcap --connect 127.0.0.1:25565 --direction serverbound
`

// replayReport is the machine-readable result.
type replayReport struct {
	Input       string   `json:"input"`
	Protocol    string   `json:"protocol"`
	Records     int      `json:"records"`
	Digest      string   `json:"digest"`
	Recorded    string   `json:"recordedDigest,omitempty"`
	Drift       string   `json:"drift"`
	Divergences []string `json:"divergences,omitempty"`
}

func runReplay(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("replay", flag.ContinueOnError)
	input := flags.String("input", "", "capture file to replay")
	verify := flags.Bool("verify", false, "compare the result against the capture's own digest")
	mode := flags.String("mode", "fast", "timing: fast, recorded, or scaled")
	scale := flags.Float64("scale", 1, "multiplier for scaled mode; 0 replays as fast as possible")
	connect := flags.String("connect", "", "send the capture's frames to this host:port")
	direction := flags.String("direction", "", "which direction's frames to send when connecting")
	timeout := flags.Duration("timeout", 30*time.Second, "how long a connected replay may take")
	format := flags.String("format", "text", "output format: text or json")

	if err := parseFlags(flags, args, replayUsage); err != nil {
		return err
	}
	if *input == "" {
		return usagef("--input is required\n\n%s", replayUsage)
	}
	if *format != "text" && *format != "json" {
		return usagef("unknown format %q; use text or json", *format)
	}
	if *connect != "" && *direction == "" {
		return usagef(
			"--connect needs --direction: sending a server its own frames is a different "+
				"exercise from sending it a client's\n\n%s",
			replayUsage,
		)
	}
	if *connect == "" && *direction != "" {
		return usagef("--direction only applies with --connect")
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

	options := []replay.Option{
		replay.WithMode(replay.Mode(*mode)),
		replay.WithScale(*scale),
	}

	if *connect != "" {
		conn, err := net.DialTimeout("tcp", *connect, *timeout)
		if err != nil {
			return peerf("dial %s: %w", *connect, err)
		}
		defer func() { _ = conn.Close() }()

		parsed, err := parseDirection(*direction)
		if err != nil {
			return err
		}
		options = append(options, replay.WithTransport(protocol.Transport{
			Reader:    conn,
			Writer:    conn,
			Interrupt: conn.Close,
		}, parsed))

		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	} else {
		options = append(options, replay.WithResolver(replay.ResolverFunc(protocols.Resolve)))
	}

	player, err := replay.New(reader, options...)
	if err != nil {
		return usagef("%v", err)
	}

	result, err := player.Run(ctx)
	if err != nil {
		return fmt.Errorf("replay %s: %w", *input, err)
	}

	report := replayReport{
		Input:    *input,
		Protocol: reader.Header().Protocol,
		Records:  result.Records,
		Digest:   result.Digest,
		Drift:    result.Drift.Round(time.Millisecond).String(),
	}
	for _, divergence := range result.Divergences {
		report.Divergences = append(report.Divergences, divergence.String())
	}

	trailer, complete := reader.Trailer()
	if complete {
		report.Recorded = trailer.Digest
	}

	if err := writeReplayReport(stdout, *format, report); err != nil {
		return err
	}

	if !*verify {
		return nil
	}

	return verifyReplay(reader, result)
}

// verifyReplay compares a replay against what the capture recorded.
func verifyReplay(reader *capture.Reader, result replay.Result) error {
	trailer, complete := reader.Trailer()
	if !complete {
		return verifyf(
			"capture has no trailer, so there is no recorded digest to compare against: " +
				"the process that wrote it did not finish",
		)
	}
	if !trailer.Comparable() {
		return verifyf(
			"capture's digest was computed under rule %d and this tool computes rule %d",
			trailer.DigestAlgorithm, capture.DigestVersion,
		)
	}
	if trailer.Digest != result.Digest {
		return verifyf("digest mismatch: capture recorded %s, replay produced %s", trailer.Digest, result.Digest)
	}

	return nil
}

func writeReplayReport(stdout io.Writer, format string, report replayReport) error {
	if format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")

		return encoder.Encode(report)
	}

	_, _ = fmt.Fprintf(stdout, "input\t%s\n", report.Input)
	_, _ = fmt.Fprintf(stdout, "protocol\t%s\n", report.Protocol)
	_, _ = fmt.Fprintf(stdout, "records\t%d\n", report.Records)
	_, _ = fmt.Fprintf(stdout, "digest\t%s\n", report.Digest)
	if report.Recorded != "" {
		_, _ = fmt.Fprintf(stdout, "recorded\t%s\n", report.Recorded)
	}
	_, _ = fmt.Fprintf(stdout, "drift\t%s\n", report.Drift)
	for _, divergence := range report.Divergences {
		_, _ = fmt.Fprintf(stdout, "divergence\t%s\n", divergence)
	}

	return nil
}
