package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	capturepkg "github.com/go-theft-craft/minecraft-protocol/capture"
	"github.com/go-theft-craft/minecraft-protocol/login"
	"github.com/go-theft-craft/minecraft-protocol/protocols"
)

const captureUsage = `mcproto capture records a connection to a capture file.

Usage:
  mcproto capture --address <host:port> --output <file> --username <name> --offline
                  [--protocol <id>] [--stop-at login|configuration]
                  [--timeout 30s] [--overwrite] [--disclose <reason>]

A capture holds session content and is not encrypted. Secret material is
withheld unless --disclose states a reason, and a disclosed capture says so in
its own header.

Examples:
  mcproto capture --address 127.0.0.1:25565 --output login.mcpcap --username tester --offline
  mcproto capture --address 127.0.0.1:25565 --output keys.mcpcap --username tester --offline \\
    --disclose 'interoperability debugging'
`

func runCapture(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	var options connectionFlags
	options.bind(flags)
	output := flags.String("output", "", "capture file to write")
	username := flags.String("username", "", "account name to present")
	offline := flags.Bool("offline", false, "log in without proving account ownership")
	stopAt := flags.String("stop-at", "", "stop the sequence at login or configuration instead of play")
	overwrite := flags.Bool("overwrite", false, "replace an existing capture file")
	disclose := flags.String("disclose", "", "record secret material, for the stated reason")
	playFor := flags.Duration("play-for", 0, "after reaching play, keep reading for this long")
	note := flags.String("note", "", "free text stored in the capture header")

	options.timeout = 30 * time.Second

	if err := parseFlags(flags, args, captureUsage); err != nil {
		return err
	}
	descriptor, host, port, err := options.resolve(captureUsage)
	if err != nil {
		return err
	}
	if *output == "" {
		return usagef("--output is required\n\n%s", captureUsage)
	}
	if *username == "" {
		return usagef("--username is required\n\n%s", captureUsage)
	}
	if !*offline {
		return usagef(
			"--offline is required: this tool holds no account. Use headless-minecraft "+
				"for an authenticated login.\n\n%s",
			captureUsage,
		)
	}

	limits, err := protocol.NewLimits()
	if err != nil {
		return fmt.Errorf("limits: %w", err)
	}

	fileOptions := []capturepkg.FileOption{}
	if *overwrite {
		fileOptions = append(fileOptions, capturepkg.WithOverwrite())
	}
	if *disclose != "" {
		fileOptions = append(fileOptions, capturepkg.WithWriterOptions(capturepkg.WithDisclosure(*disclose)))
	}

	sink, err := capturepkg.NewFileSink(*output, capturepkg.Header{
		Protocol:          descriptor.ID(),
		Role:              "client",
		FrameBytes:        limits.FrameBytes(),
		DecompressedBytes: limits.DecompressedBytes(),
		Created:           time.Now().UTC().Format(time.RFC3339),
		Note:              *note,
	}, fileOptions...)
	if err != nil {
		return fmt.Errorf("create capture: %w", err)
	}
	defer func() { _ = sink.Close() }()

	if *disclose != "" {
		_, _ = fmt.Fprintf(
			stderr,
			"mcproto: %s will contain secret material, disclosed for: %s\n",
			*output, *disclose,
		)
	}

	streamOptions := []protocol.StreamOption{protocol.WithObservationSink(sink)}
	if *disclose != "" {
		streamOptions = append(streamOptions, protocol.WithSecretDisclosure(*disclose))
	}

	if err := captureLogin(
		ctx, descriptor, options, host, port, *username, *stopAt, *playFor, streamOptions,
	); err != nil {
		return err
	}

	if err := sink.Close(); err != nil {
		return fmt.Errorf("close capture: %w", err)
	}

	_, _ = fmt.Fprintf(stdout, "path\t%s\n", *output)
	_, _ = fmt.Fprintf(stdout, "protocol\t%s\n", descriptor.ID())
	_, _ = fmt.Fprintf(stdout, "redaction\t%s\n", sink.Header().Redaction)

	return nil
}

// captureLogin runs the connection whose observations the sink records.
func captureLogin(
	ctx context.Context,
	descriptor protocol.Protocol,
	options connectionFlags,
	host string,
	port uint16,
	username string,
	stopAt string,
	playFor time.Duration,
	streamOptions []protocol.StreamOption,
) error {
	authenticator, err := login.NewOffline(username)
	if err != nil {
		return usagef("--username: %v", err)
	}

	negotiatorOptions := []login.NegotiatorOption{}
	switch stopAt {
	case "":
	case "login", "configuration":
		negotiatorOptions = append(negotiatorOptions, login.WithTerminalState(protocol.State(stopAt)))
	default:
		return usagef("unknown --stop-at %q; use login or configuration", stopAt)
	}

	negotiator, err := login.NewNegotiator(authenticator, negotiatorOptions...)
	if err != nil {
		return fmt.Errorf("negotiator: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, options.timeout)
	defer cancel()

	conn, err := options.dial()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	stream, _, err := startStream(ctx, descriptor, conn, streamOptions...)
	if err != nil {
		return err
	}
	// Closing the stream drains the observations still queued, which is what
	// makes the capture end where the connection did rather than a few
	// records short of it.
	defer func() { _ = stream.Close() }()

	if err := writeHandshake(ctx, stream, descriptor, host, port, loginNextState); err != nil {
		return err
	}
	if _, err := negotiator.Negotiate(ctx, stream); err != nil {
		return peerf("login to %s: %w", options.address, err)
	}

	if playFor > 0 {
		readPlay(ctx, stream, descriptor, playFor)
	}

	return stream.Close()
}

// readPlay keeps reading after the login so the capture holds play traffic.
//
// A capture that stops at the moment play begins holds a connection that never
// played, which is exactly the part a consumer of the capture most wants: the
// join packet, the registries already applied, and the world the server sent.
// Reading stops at the deadline or at the first read failure, and neither is
// an error: what was read is in the capture either way.
func readPlay(
	ctx context.Context,
	stream *protocol.Stream,
	descriptor protocol.Protocol,
	playFor time.Duration,
) {
	ctx, cancel := context.WithTimeout(ctx, playFor)
	defer cancel()

	for {
		packet, err := stream.Read(ctx)
		if err != nil {
			return
		}

		// Answer the two packets that gate progress. A server sends no world
		// data until its teleport is confirmed, so a capture taken without
		// this holds a login and an empty world.
		reply, needed := protocols.PlayReply(descriptor, packet)
		if !needed {
			continue
		}
		if err := stream.Write(ctx, reply); err != nil {
			return
		}
	}
}
