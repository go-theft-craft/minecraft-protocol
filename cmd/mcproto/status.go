package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/protocols"
)

const statusUsage = `mcproto status queries a server's status.

Usage:
  mcproto status --address <host:port> [--protocol <id>] [--timeout 10s] [--format text|json]

There is no default address. Pointing a tool at a server is a decision, and one
made by typing it.

Examples:
  mcproto status --address 127.0.0.1:25565
  mcproto status --address play.example.com:25565 --protocol java/1.8.9 --format json
`

// connectionFlags are the flags every command that dials a server shares.
type connectionFlags struct {
	address  string
	protocol string
	timeout  time.Duration
}

func (c *connectionFlags) bind(flags *flag.FlagSet) {
	flags.StringVar(&c.address, "address", "", "server address as host:port")
	flags.StringVar(&c.protocol, "protocol", protocols.Default().ID(), "protocol ID to speak")
	flags.DurationVar(&c.timeout, "timeout", 10*time.Second, "how long to wait for the whole exchange")
}

func (c *connectionFlags) resolve(usage string) (protocol.Protocol, string, uint16, error) {
	if c.address == "" {
		return nil, "", 0, usagef("--address is required\n\n%s", usage)
	}
	host, portText, err := net.SplitHostPort(c.address)
	if err != nil {
		return nil, "", 0, usagef("--address must be host:port: %v", err)
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		return nil, "", 0, usagef("--address port: %v", err)
	}

	descriptor, known := protocols.Resolve(c.protocol)
	if !known {
		return nil, "", 0, usagef(
			"unknown protocol %q; known protocols: %s",
			c.protocol, strings.Join(protocols.IDs(), ", "),
		)
	}

	return descriptor, host, uint16(port), nil
}

// dial opens a connection, reporting a refusal or a timeout as the peer's
// failure rather than this program's.
func (c *connectionFlags) dial() (net.Conn, error) {
	started := time.Now()
	conn, err := net.DialTimeout("tcp", c.address, c.timeout)
	if err != nil {
		return nil, peerf("dial %s after %v: %w", c.address, time.Since(started).Round(time.Millisecond), err)
	}

	return conn, nil
}

// statusReport is the machine-readable result.
type statusReport struct {
	Address  string          `json:"address"`
	Protocol string          `json:"protocol"`
	Latency  string          `json:"latency"`
	Response json.RawMessage `json:"response"`
}

func runStatus(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	var options connectionFlags
	options.bind(flags)
	format := flags.String("format", "text", "output format: text or json")

	if err := parseFlags(flags, args, statusUsage); err != nil {
		return err
	}
	descriptor, host, port, err := options.resolve(statusUsage)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, options.timeout)
	defer cancel()

	conn, err := options.dial()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	started := time.Now()
	response, err := requestStatus(ctx, descriptor, conn, host, port)
	if err != nil {
		return err
	}
	latency := time.Since(started).Round(time.Millisecond)

	report := statusReport{
		Address:  options.address,
		Protocol: descriptor.ID(),
		Latency:  latency.String(),
		Response: json.RawMessage(response),
	}

	switch *format {
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")

		return encoder.Encode(report)
	case "text":
		_, _ = fmt.Fprintf(stdout, "address\t%s\n", report.Address)
		_, _ = fmt.Fprintf(stdout, "protocol\t%s\n", report.Protocol)
		_, _ = fmt.Fprintf(stdout, "latency\t%s\n", report.Latency)
		_, _ = fmt.Fprintf(stdout, "response\t%s\n", strings.TrimSpace(string(report.Response)))

		return nil
	default:
		return usagef("unknown format %q; use text or json", *format)
	}
}

// requestStatus performs handshake, request, and response on one connection.
func requestStatus(
	ctx context.Context,
	descriptor protocol.Protocol,
	conn net.Conn,
	host string,
	port uint16,
) (string, error) {
	stream, _, err := startStream(ctx, descriptor, conn)
	if err != nil {
		return "", err
	}
	defer func() { _ = stream.Close() }()

	if err := writeHandshake(ctx, stream, descriptor, host, port, statusNextState); err != nil {
		return "", err
	}

	// The status request is empty and its ID is zero in both protocols, which
	// is why this needs no per-version exchange the way login does.
	factory, ok := descriptor.(protocol.PacketFactory)
	if !ok {
		return "", fmt.Errorf("protocol %s cannot build packet values", descriptor.ID())
	}
	value, known := factory.NewPacketValue(stateStatus, protocol.DirectionServerbound, 0)
	if !known {
		return "", fmt.Errorf("protocol %s has no status request", descriptor.ID())
	}
	if err := stream.Write(ctx, protocol.Packet{
		State:     stateStatus,
		Direction: protocol.DirectionServerbound,
		ID:        0,
		Value:     value,
	}); err != nil {
		return "", peerf("write status request: %w", err)
	}

	packet, err := stream.Read(ctx)
	if err != nil {
		return "", peerf("read status response: %w", err)
	}

	response, err := statusResponseText(packet)
	if err != nil {
		return "", err
	}

	return response, nil
}

// statusResponseText pulls the JSON document out of a status response without
// naming a version's packet type.
//
// Both protocols answer with one string field. Reflecting over the decoded
// value rather than importing two generated types is what keeps this command
// version-neutral, and the failure is explicit when a protocol ever answers
// with something else.
func statusResponseText(packet protocol.Packet) (string, error) {
	encoded, err := json.Marshal(packet.Value)
	if err != nil {
		return "", fmt.Errorf("read status response: %w", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return "", fmt.Errorf("read status response: %w", err)
	}
	for _, raw := range fields {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil && strings.HasPrefix(strings.TrimSpace(text), "{") {
			return text, nil
		}
	}

	return "", peerf("status response carried no document: %s", encoded)
}
