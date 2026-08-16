package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/protocols"
)

const packetUsage = `mcproto packet decodes and encodes one packet body.

Usage:
  mcproto packet decode --protocol <id> --state <state> --direction <dir> [--input -|<file>] [--hex]
  mcproto packet encode --protocol <id> --state <state> --direction <dir> [--input -|<file>] [--hex]

decode reads a packet body — the packet ID varint followed by its fields, with
no frame length and no compression envelope — and writes one JSON object.
encode is its inverse.

Examples:
  printf '\\x00\\x01' | mcproto packet decode --protocol java/1.8.9 --state play --direction clientbound
  mcproto packet decode --protocol java/1.8.9 --state play --direction clientbound --input frame.bin --hex
  echo '{"id":0,"name":"keep_alive","fields":{"KeepAliveID":42}}' | \\
    mcproto packet encode --protocol java/1.8.9 --state play --direction clientbound --hex
`

// packetReport is what decode writes and encode reads.
type packetReport struct {
	Protocol  string          `json:"protocol"`
	State     string          `json:"state"`
	Direction string          `json:"direction"`
	ID        int32           `json:"id"`
	Name      string          `json:"name"`
	Fields    json.RawMessage `json:"fields"`
	// Body is the encoded packet body, hex, present only on encode output.
	Body string `json:"body,omitempty"`
}

type packetFlags struct {
	protocol  string
	state     string
	direction string
	input     string
	asHex     bool
}

func (p *packetFlags) bind(flags *flag.FlagSet) {
	flags.StringVar(&p.protocol, "protocol", "", "protocol ID, such as java/26.1")
	flags.StringVar(&p.state, "state", "", "connection state: handshaking, status, login, configuration, play")
	flags.StringVar(&p.direction, "direction", "", "clientbound or serverbound")
	flags.StringVar(&p.input, "input", "-", "input file, or - for stdin")
	flags.BoolVar(&p.asHex, "hex", false, "read and write bodies as hex text rather than raw bytes")
}

func runPacket(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return usagef("packet needs a subcommand\n\n%s", packetUsage)
	}

	switch args[0] {
	case "decode":
		return runPacketDecode(args[1:], stdin, stdout)
	case "encode":
		return runPacketEncode(args[1:], stdin, stdout)
	case "-h", "--help", "help":
		return helpError{usage: packetUsage}
	default:
		return usagef("unknown packet subcommand %q\n\n%s", args[0], packetUsage)
	}
}

func runPacketDecode(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("packet decode", flag.ContinueOnError)
	var options packetFlags
	options.bind(flags)

	if err := parseFlags(flags, args, packetUsage); err != nil {
		return err
	}
	descriptor, state, direction, err := options.resolve()
	if err != nil {
		return err
	}

	body, err := readInput(options.input, stdin, options.asHex)
	if err != nil {
		return err
	}

	session, err := newToolSession(descriptor, direction, state, true)
	if err != nil {
		return err
	}

	packet, err := session.DecodeFrame(body)
	if err != nil {
		return fmt.Errorf("decode %s %s packet: %w", descriptor.ID(), state, err)
	}

	fields, err := json.Marshal(packet.Value)
	if err != nil {
		return fmt.Errorf("render decoded packet: %w", err)
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")

	return encoder.Encode(packetReport{
		Protocol:  descriptor.ID(),
		State:     string(state),
		Direction: options.direction,
		ID:        packet.ID,
		Name:      packet.Name,
		Fields:    fields,
	})
}

func runPacketEncode(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("packet encode", flag.ContinueOnError)
	var options packetFlags
	options.bind(flags)

	if err := parseFlags(flags, args, packetUsage); err != nil {
		return err
	}
	descriptor, state, direction, err := options.resolve()
	if err != nil {
		return err
	}

	source, err := readSource(options.input, stdin)
	if err != nil {
		return fmt.Errorf("read packet JSON: %w", err)
	}

	var report packetReport
	if err := json.Unmarshal(source, &report); err != nil {
		return usagef("input is not a packet object: %v\n\n%s", err, packetUsage)
	}

	factory, ok := descriptor.(protocol.PacketFactory)
	if !ok {
		return fmt.Errorf("protocol %s cannot build packet values", descriptor.ID())
	}
	value, known := factory.NewPacketValue(state, direction, report.ID)
	if !known {
		return usagef(
			"protocol %s has no packet %#x in state %s direction %s",
			descriptor.ID(), report.ID, state, options.direction,
		)
	}
	if len(report.Fields) > 0 {
		if err := json.Unmarshal(report.Fields, value); err != nil {
			return usagef("fields do not fit packet %#x: %v", report.ID, err)
		}
	}

	// Encoding is the writing half, so the session takes the opposite role
	// from the one that would read this packet.
	session, err := newToolSession(descriptor, direction, state, false)
	if err != nil {
		return err
	}

	body, err := session.EncodeFrame(protocol.Packet{
		State:     state,
		Direction: direction,
		ID:        report.ID,
		Value:     value,
	})
	if err != nil {
		return fmt.Errorf("encode packet %#x: %w", report.ID, err)
	}

	if options.asHex {
		_, _ = fmt.Fprintln(stdout, hex.EncodeToString(body))

		return nil
	}
	_, err = stdout.Write(body)

	return err
}

// resolve turns the three identity flags into the values a session needs.
func (p *packetFlags) resolve() (protocol.Protocol, protocol.State, protocol.Direction, error) {
	if p.protocol == "" {
		return nil, "", 0, usagef(
			"--protocol is required; known protocols: %s\n\n%s",
			strings.Join(protocols.IDs(), ", "), packetUsage,
		)
	}
	descriptor, known := protocols.Resolve(p.protocol)
	if !known {
		return nil, "", 0, usagef(
			"unknown protocol %q; known protocols: %s",
			p.protocol, strings.Join(protocols.IDs(), ", "),
		)
	}
	if p.state == "" {
		return nil, "", 0, usagef("--state is required\n\n%s", packetUsage)
	}

	direction, err := parseDirection(p.direction)
	if err != nil {
		return nil, "", 0, err
	}

	return descriptor, protocol.State(p.state), direction, nil
}

func parseDirection(value string) (protocol.Direction, error) {
	switch value {
	case "clientbound":
		return protocol.DirectionClientbound, nil
	case "serverbound":
		return protocol.DirectionServerbound, nil
	case "":
		return 0, usagef("--direction is required: clientbound or serverbound")
	default:
		return 0, usagef("unknown direction %q; use clientbound or serverbound", value)
	}
}

// newToolSession builds a session that can read or write packets of one
// direction.
//
// A session only decodes its inbound direction and only encodes its outbound
// one, so the role follows from what the caller is doing rather than from
// anything they should have to state.
func newToolSession(
	descriptor protocol.Protocol,
	direction protocol.Direction,
	state protocol.State,
	reading bool,
) (protocol.Session, error) {
	role := protocol.RoleClient
	if (direction == protocol.DirectionServerbound) == reading {
		role = protocol.RoleServer
	}

	limits, err := protocol.NewLimits()
	if err != nil {
		return nil, fmt.Errorf("limits: %w", err)
	}
	session, err := descriptor.NewSession(role, limits)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	session.ApplyTransition(protocol.Transition{Control: protocol.StateControl{State: state}})

	if got := session.Snapshot().State; got != state {
		return nil, usagef("protocol %s has no state %q", descriptor.ID(), state)
	}

	return session, nil
}

// readSource reads all of stdin or all of a named file. "-" means stdin,
// which is the convention every other tool in a pipeline uses.
func readSource(path string, stdin io.Reader) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(stdin)
	}

	return os.ReadFile(path)
}

// readInput reads a packet body from stdin or a file, as raw bytes or hex.
func readInput(path string, stdin io.Reader, asHex bool) ([]byte, error) {
	source, err := readSource(path, stdin)
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}
	if len(source) == 0 {
		return nil, usagef("input is empty")
	}

	if !asHex {
		return source, nil
	}

	decoded, err := hex.DecodeString(strings.TrimSpace(string(bytes.TrimSpace(source))))
	if err != nil {
		return nil, usagef("input is not hex: %v", err)
	}

	return decoded, nil
}
