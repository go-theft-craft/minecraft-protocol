package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/login"
)

const loginUsage = `mcproto login logs in to a server and reports the profile.

Usage:
  mcproto login --address <host:port> --username <name> --offline
                [--protocol <id>] [--stop-at login|configuration]
                [--timeout 30s] [--format text|json]

--offline is required and is not a default. This tool has no account: an online
login proves ownership to the session server, which needs credentials it will
not hold. Use headless-minecraft for an authenticated client.

Examples:
  mcproto login --address 127.0.0.1:25565 --username tester --offline
  mcproto login --address 127.0.0.1:25565 --username tester --offline --format json
`

// loginReport is the machine-readable result.
type loginReport struct {
	Address  string `json:"address"`
	Protocol string `json:"protocol"`
	Username string `json:"username"`
	UUID     string `json:"uuid"`
	State    string `json:"state"`
	Elapsed  string `json:"elapsed"`
}

func runLogin(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	var options connectionFlags
	options.bind(flags)
	username := flags.String("username", "", "account name to present")
	offline := flags.Bool("offline", false, "log in without proving account ownership")
	stopAt := flags.String("stop-at", "", "stop the sequence at login or configuration instead of play")
	format := flags.String("format", "text", "output format: text or json")

	// A login passes through configuration and can wait on a server's
	// registry data, so it gets more room than a status query by default.
	options.timeout = 30 * time.Second

	if err := parseFlags(flags, args, loginUsage); err != nil {
		return err
	}
	descriptor, host, port, err := options.resolve(loginUsage)
	if err != nil {
		return err
	}
	if *username == "" {
		return usagef("--username is required\n\n%s", loginUsage)
	}
	if !*offline {
		return usagef(
			"--offline is required: this tool holds no account and cannot prove ownership "+
				"to the session server. Use headless-minecraft for an authenticated login.\n\n%s",
			loginUsage,
		)
	}

	authenticator, err := login.NewOffline(*username)
	if err != nil {
		return usagef("--username: %v", err)
	}

	negotiatorOptions := []login.NegotiatorOption{}
	switch *stopAt {
	case "":
	case "login", "configuration":
		negotiatorOptions = append(negotiatorOptions, login.WithTerminalState(protocol.State(*stopAt)))
	default:
		return usagef("unknown --stop-at %q; use login or configuration", *stopAt)
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

	started := time.Now()
	stream, _, err := startStream(ctx, descriptor, conn)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()

	if err := writeHandshake(ctx, stream, descriptor, host, port, loginNextState); err != nil {
		return err
	}

	profile, err := negotiator.Negotiate(ctx, stream)
	if err != nil {
		return peerf("login to %s: %w", options.address, err)
	}

	snapshot, err := stream.Snapshot(ctx)
	if err != nil {
		return peerf("read connection state: %w", err)
	}

	report := loginReport{
		Address:  options.address,
		Protocol: descriptor.ID(),
		Username: profile.Name.String(),
		UUID:     profile.UUID.String(),
		State:    string(snapshot.State),
		Elapsed:  time.Since(started).Round(time.Millisecond).String(),
	}

	switch *format {
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")

		return encoder.Encode(report)
	case "text":
		_, _ = fmt.Fprintf(stdout, "address\t%s\n", report.Address)
		_, _ = fmt.Fprintf(stdout, "protocol\t%s\n", report.Protocol)
		_, _ = fmt.Fprintf(stdout, "username\t%s\n", report.Username)
		_, _ = fmt.Fprintf(stdout, "uuid\t%s\n", report.UUID)
		_, _ = fmt.Fprintf(stdout, "state\t%s\n", report.State)
		_, _ = fmt.Fprintf(stdout, "elapsed\t%s\n", report.Elapsed)

		return nil
	default:
		return usagef("unknown format %q; use text or json", *format)
	}
}
