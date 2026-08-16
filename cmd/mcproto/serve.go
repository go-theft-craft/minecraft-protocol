package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	capturepkg "github.com/go-theft-craft/minecraft-protocol/capture"
	"github.com/go-theft-craft/minecraft-protocol/protocols"
)

const serveUsage = `mcproto serve replays a captured server at a real client.

Usage:
  mcproto serve --script <capture> [--address 127.0.0.1:25565]
                [--output <capture>] [--idle 60s] [--keep-listening]

This is a verification harness, not a server. It plays back what a real server
said, in order, and reads what the client says back through this repository's
own decoders. Every packet the client sends is decoded and counted, and a
decode failure is the result the harness exists to produce: it names a packet a
real client sent that this code could not read.

The script is a capture taken from a client's side of a real connection, so
its clientbound records are the server's half. Record one with:

  mcproto capture --address <real server> --output script.mcpcap \
    --username tester --offline --play-for 10s

Examples:
  mcproto serve --script script.mcpcap
  mcproto serve --script script.mcpcap --address 0.0.0.0:25565 --output session.mcpcap
`

// serveSummary is what the harness reports when a session ends. The counts are
// the point: a client sends the same packet hundreds of times, and a list of
// distinct names with counts is what says which codecs were exercised.
type serveSummary struct {
	scripted   int
	written    int
	read       int
	byName     map[string]int
	mismatches []string
	failures   []string
	reachedTo  protocol.State
}

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	script := flags.String("script", "", "capture whose clientbound records are replayed")
	address := flags.String("address", "127.0.0.1:25565", "address to listen on")
	output := flags.String("output", "", "write a capture of the session this harness serves")
	idle := flags.Duration("idle", 60*time.Second, "give up after this long with nothing from the client")
	step := flags.Duration("step-timeout", 5*time.Second, "how long to wait at each point the script expects the client to speak")
	keepListening := flags.Bool("keep-listening", false, "serve more than one client, until interrupted")
	dryRun := flags.Bool("dry-run", false, "print what the script would send and wait for, then exit")

	if err := parseFlags(flags, args, serveUsage); err != nil {
		return err
	}
	if *script == "" {
		return usagef("--script is required\n\n%s", serveUsage)
	}

	// Read the header once, before listening, so a bad script fails now rather
	// than after somebody has walked to their client and typed an address.
	header, err := readScriptHeader(*script)
	if err != nil {
		return err
	}
	descriptor, known := protocols.Resolve(header.Protocol)
	if !known {
		return usagef("script names protocol %q, which this build does not speak", header.Protocol)
	}

	if *dryRun {
		return describeScript(*script, descriptor, stdout)
	}

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *address, err)
	}
	defer func() { _ = listener.Close() }()

	_, _ = fmt.Fprintf(stderr, "mcproto: serving %s from %s on %s\n", descriptor.ID(), *script, listener.Addr())
	_, _ = fmt.Fprintf(stderr, "mcproto: connect a %s client, then press Ctrl-C\n", descriptor.Version().Name)

	// Closing the listener is what unblocks Accept when the context ends.
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	var sessions int
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return fmt.Errorf("accept: %w", err)
		}

		sessions++
		served, err := serveOne(
			ctx, conn, descriptor, *script, outputFor(*output, sessions), *idle, *step, stdout, stderr,
		)
		_ = conn.Close()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "mcproto: session ended: %v\n", err)
		}
		if served && !*keepListening {
			return nil
		}
	}
}

// describeScript prints the script without serving it.
//
// A script is a recording of one connection being used to drive another, and
// the two differ: what a script waits for is the question worth being able to
// answer without a client in front of you.
func describeScript(path string, descriptor protocol.Protocol, stdout io.Writer) error {
	script, err := openScript(path, descriptor)
	if err != nil {
		return err
	}
	defer script.close()

	var sent, awaited int
	for {
		instruction, err := script.next()
		if errors.Is(err, capturepkg.ErrEndOfCapture) {
			break
		}
		if err != nil {
			return err
		}

		if instruction.direction == protocol.DirectionClientbound {
			sent++
			_, _ = fmt.Fprintf(stdout, "send\t%s\n", instruction.describe())

			continue
		}
		awaited++
		_, _ = fmt.Fprintf(stdout, "await\t%s\n", instruction.describe())
	}

	_, _ = fmt.Fprintf(stdout, "total\tsend %d\tawait %d\n", sent, awaited)

	return nil
}

// outputFor numbers a capture per session.
//
// A client pings the server list before it joins and again when it leaves, and
// each of those is a session. One path for all of them means the recording of
// the session somebody cared about is overwritten by the ping that followed
// it — which is exactly what happened the first time this was used.
func outputFor(path string, session int) string {
	if path == "" || session <= 1 {
		return path
	}

	extension := filepath.Ext(path)
	stem := strings.TrimSuffix(path, extension)

	return fmt.Sprintf("%s-%d%s", stem, session, extension)
}

func readScriptHeader(path string) (capturepkg.Header, error) {
	file, err := os.Open(path)
	if err != nil {
		return capturepkg.Header{}, fmt.Errorf("open script: %w", err)
	}
	defer func() { _ = file.Close() }()

	reader, err := capturepkg.NewReader(file)
	if err != nil {
		return capturepkg.Header{}, fmt.Errorf("read script: %w", err)
	}

	return reader.Header(), nil
}

// serveOne handles one connection. It reports whether the connection was a
// login rather than a status ping, because a client pings before it joins and
// the harness should still be listening when it does.
func serveOne(
	ctx context.Context,
	conn net.Conn,
	descriptor protocol.Protocol,
	scriptPath string,
	outputPath string,
	idle time.Duration,
	step time.Duration,
	stdout, stderr io.Writer,
) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	options, err := serveStreamOptions(outputPath, descriptor)
	if err != nil {
		return false, err
	}

	stream, err := startServerStream(ctx, descriptor, conn, options.stream...)
	if err != nil {
		return false, err
	}
	defer func() { _ = stream.Close() }()
	if options.sink != nil {
		defer func() { _ = options.sink.Close() }()
	}

	handshake, err := readHandshake(ctx, stream, idle)
	if err != nil {
		return false, err
	}
	_, _ = fmt.Fprintf(
		stderr,
		"mcproto: client speaks protocol %d and asked for state %d\n",
		handshake.ProtocolVersion, handshake.NextState,
	)

	// A ping is answered whatever version asked. That is what a real server
	// does, and it is how a client shows "outdated client" rather than a
	// connection that failed for no stated reason.
	if handshake.NextState == statusNextState {
		return false, serveStatus(ctx, stream, descriptor, idle)
	}

	// A login is refused, because a script for one protocol played at a
	// client speaking another produces nonsense rather than a finding.
	if handshake.ProtocolVersion != descriptor.Version().Protocol {
		reason := fmt.Sprintf(
			"This harness speaks protocol %d (%s) and your client speaks %d.",
			descriptor.Version().Protocol, descriptor.Version().Name, handshake.ProtocolVersion,
		)
		_, _ = fmt.Fprintf(stderr, "mcproto: %s\n", reason)
		_ = stream.Shutdown(ctx, reason)

		return false, nil
	}

	summary, err := servePlayback(ctx, stream, descriptor, scriptPath, idle, step, stderr)
	printServeSummary(stdout, summary)

	return true, err
}

type serveOptions struct {
	stream []protocol.StreamOption
	sink   *capturepkg.FileSink
}

func serveStreamOptions(outputPath string, descriptor protocol.Protocol) (serveOptions, error) {
	if outputPath == "" {
		return serveOptions{}, nil
	}

	limits, err := protocol.NewLimits()
	if err != nil {
		return serveOptions{}, fmt.Errorf("limits: %w", err)
	}
	sink, err := capturepkg.NewFileSink(outputPath, capturepkg.Header{
		Protocol:          descriptor.ID(),
		Role:              "server",
		FrameBytes:        limits.FrameBytes(),
		DecompressedBytes: limits.DecompressedBytes(),
		Created:           time.Now().UTC().Format(time.RFC3339),
		Note:              "recorded by mcproto serve",
	}, capturepkg.WithOverwrite())
	if err != nil {
		return serveOptions{}, fmt.Errorf("create output capture: %w", err)
	}

	return serveOptions{
		stream: []protocol.StreamOption{protocol.WithObservationSink(sink)},
		sink:   sink,
	}, nil
}

// startServerStream is startStream's server half. The role decides which
// direction the session decodes, and here the client is the peer.
func startServerStream(
	ctx context.Context,
	descriptor protocol.Protocol,
	conn net.Conn,
	options ...protocol.StreamOption,
) (*protocol.Stream, error) {
	limits, err := protocol.NewLimits()
	if err != nil {
		return nil, fmt.Errorf("limits: %w", err)
	}
	session, err := descriptor.NewSession(protocol.RoleServer, limits)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}

	stream, err := protocol.NewStream(session, protocol.Transport{
		Reader:    conn,
		Writer:    conn,
		Interrupt: conn.Close,
	}, options...)
	if err != nil {
		return nil, fmt.Errorf("stream: %w", err)
	}
	if err := stream.Start(ctx); err != nil {
		return nil, fmt.Errorf("start stream: %w", err)
	}

	return stream, nil
}

func readHandshake(
	ctx context.Context,
	stream *protocol.Stream,
	idle time.Duration,
) (protocols.HandshakeFields, error) {
	ctx, cancel := context.WithTimeout(ctx, idle)
	defer cancel()

	packet, err := stream.Read(ctx)
	if err != nil {
		return protocols.HandshakeFields{}, peerf("read handshake: %w", err)
	}
	fields, ok := protocols.ReadHandshake(packet)
	if !ok {
		return protocols.HandshakeFields{}, peerf("first packet was %s, not a handshake", packet.Name)
	}

	return fields, nil
}

// serveStatus answers the ping a client sends before it joins.
func serveStatus(
	ctx context.Context,
	stream *protocol.Stream,
	descriptor protocol.Protocol,
	idle time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, idle)
	defer cancel()

	// The request carries nothing; reading it is what says the client is
	// ready for the response.
	if _, err := stream.Read(ctx); err != nil {
		return peerf("read status request: %w", err)
	}

	document := fmt.Sprintf(
		`{"version":{"name":"mcproto %s","protocol":%d},`+
			`"players":{"max":1,"online":0},`+
			`"description":{"text":"mcproto verification harness"}}`,
		descriptor.Version().Name, descriptor.Version().Protocol,
	)
	response, err := protocols.StatusResponse(descriptor, document)
	if err != nil {
		return err
	}
	if err := stream.Write(ctx, response); err != nil {
		return peerf("write status response: %w", err)
	}

	// The ping that follows is echoed back unchanged. Reading it and writing
	// it back is what makes the client show a latency rather than a question
	// mark. The reply is built rather than the request turned around: a
	// packet carries the direction it was decoded for, and a serverbound
	// value written clientbound is a different packet.
	ping, err := stream.Read(ctx)
	if err != nil {
		// A client that closes after the response is normal: it has what it
		// came for.
		return nil
	}
	reply, ok := protocols.PingResponse(descriptor, ping)
	if !ok {
		return nil
	}

	return stream.Write(ctx, reply)
}

// servePlayback walks the script, writing the server's half and reading the
// client's.
func servePlayback(
	ctx context.Context,
	stream *protocol.Stream,
	descriptor protocol.Protocol,
	scriptPath string,
	idle time.Duration,
	step time.Duration,
	stderr io.Writer,
) (serveSummary, error) {
	summary := serveSummary{byName: map[string]int{}}

	script, err := openScript(scriptPath, descriptor)
	if err != nil {
		return summary, err
	}
	defer script.close()

	for {
		instruction, err := script.next()
		if errors.Is(err, capturepkg.ErrEndOfCapture) {
			break
		}
		if err != nil {
			return summary, err
		}
		summary.scripted++

		if instruction.direction == protocol.DirectionClientbound {
			if err := writeScripted(ctx, stream, instruction, idle); err != nil {
				return summary, err
			}
			summary.written++

			continue
		}

		// The script says the client speaks here, so wait until it does.
		if err := awaitFromClient(ctx, stream, instruction, step, &summary, stderr); err != nil {
			return summary, err
		}
	}

	// The script has run out. Everything the client keeps sending is still
	// decoded, and that is where the play-state coverage comes from: a client
	// in the world sends position, rotation, and input packets continuously.
	summary.reachedTo = currentState(ctx, stream)
	_, _ = fmt.Fprintf(stderr, "mcproto: script finished with the connection in %q\n", summary.reachedTo)

	// The script has nothing left to say, and a client disconnects itself
	// from a server that goes quiet. Keepalives are the least this can send
	// to keep a real client in the world sending packets to decode.
	stopKeepAlive := startKeepAlive(ctx, stream, descriptor, summary.reachedTo)
	defer stopKeepAlive()

	drainClient(ctx, stream, idle, &summary, stderr)

	return summary, nil
}

// awaitFromClient reads until the client sends the packet the script is
// waiting for, decoding and counting everything that arrives on the way.
//
// It is not a strict pairing, and it must not be. A real client is not the
// client that was recorded: a vanilla one sends its brand and its settings in
// configuration where a headless one sends neither. Consuming those in the
// slots reserved for the packets that actually advance the connection is what
// left the session in configuration while the script moved on to play — the
// packets arrived, and the harness was looking at the wrong ones.
//
// Waiting is bounded, and running out is a mismatch rather than a failure: the
// script describes one connection and the client is having another.
func awaitFromClient(
	ctx context.Context,
	stream *protocol.Stream,
	want scriptStep,
	step time.Duration,
	summary *serveSummary,
	stderr io.Writer,
) error {
	deadline := time.Now().Add(step)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			summary.mismatches = append(summary.mismatches, fmt.Sprintf(
				"script expected %s and the client did not send it", want.describe(),
			))

			return nil
		}

		packet, err := readFromClient(ctx, stream, remaining)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			if errors.Is(err, context.DeadlineExceeded) {
				summary.mismatches = append(summary.mismatches, fmt.Sprintf(
					"script expected %s and the client did not send it", want.describe(),
				))

				return nil
			}
			if isSessionEnd(ctx, err) {
				return err
			}

			summary.failures = append(summary.failures, fmt.Sprintf("waiting for %s: %v", want.describe(), err))
			_, _ = fmt.Fprintf(stderr, "mcproto: DECODE FAILURE: %v\n", err)

			return err
		}

		summary.read++
		summary.byName[packetLabel(packet)]++
		_, _ = fmt.Fprintf(stderr, "mcproto: client sent %s\n", packetLabel(packet))

		if want.matches(packet) {
			return nil
		}
	}
}

func writeScripted(ctx context.Context, stream *protocol.Stream, step scriptStep, idle time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, idle)
	defer cancel()

	if err := stream.Write(ctx, step.packet); err != nil {
		return fmt.Errorf("write scripted %s: %w", step.name, err)
	}

	return nil
}

func readFromClient(ctx context.Context, stream *protocol.Stream, idle time.Duration) (protocol.Packet, error) {
	ctx, cancel := context.WithTimeout(ctx, idle)
	defer cancel()

	return stream.Read(ctx)
}

// drainClient keeps decoding until the client stops or the idle deadline
// passes.
func drainClient(
	ctx context.Context,
	stream *protocol.Stream,
	idle time.Duration,
	summary *serveSummary,
	stderr io.Writer,
) {
	for {
		packet, err := readFromClient(ctx, stream, idle)
		if err != nil {
			// A client that leaves is how a session ends, not a defect. What
			// counts as a finding is a frame that arrived and could not be
			// read, which is what is left once the ordinary endings are named.
			if isSessionEnd(ctx, err) {
				_, _ = fmt.Fprintf(stderr, "mcproto: client disconnected\n")

				return
			}

			summary.failures = append(summary.failures, err.Error())
			_, _ = fmt.Fprintf(stderr, "mcproto: DECODE FAILURE: %v\n", err)

			return
		}
		summary.read++
		summary.byName[packetLabel(packet)]++
	}
}

// isSessionEnd reports whether an error is one of the ordinary ways a session
// stops: the harness giving up, the client hanging up, or the process being
// interrupted.
func isSessionEnd(ctx context.Context, err error) bool {
	switch {
	case ctx.Err() != nil:
		return true
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return true
	case errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return true
	case errors.Is(err, protocol.ErrStreamClosed):
		return true
	default:
		return false
	}
}

// startKeepAlive sends a keepalive every ten seconds until the returned
// function is called. Ten seconds is well inside the timeout a client applies
// and well outside anything that would look like traffic.
func startKeepAlive(
	ctx context.Context,
	stream *protocol.Stream,
	descriptor protocol.Protocol,
	state protocol.State,
) func() {
	if state != protocol.State("play") {
		return func() {}
	}

	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		var id int64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				id++
				packet, err := protocols.KeepAlive(descriptor, id)
				if err != nil {
					return
				}
				if err := stream.Write(ctx, packet); err != nil {
					return
				}
			}
		}
	}()

	return cancel
}

func currentState(ctx context.Context, stream *protocol.Stream) protocol.State {
	snapshot, err := stream.Snapshot(ctx)
	if err != nil {
		return ""
	}

	return snapshot.State
}

func packetLabel(packet protocol.Packet) string {
	name := packet.Name
	if name == "" {
		name = fmt.Sprintf("%#x", packet.ID)
	}

	return string(packet.State) + "/" + name
}

func printServeSummary(stdout io.Writer, summary serveSummary) {
	_, _ = fmt.Fprintf(stdout, "scripted\t%d\n", summary.scripted)
	_, _ = fmt.Fprintf(stdout, "written\t%d\n", summary.written)
	_, _ = fmt.Fprintf(stdout, "read\t%d\n", summary.read)
	_, _ = fmt.Fprintf(stdout, "state\t%s\n", summary.reachedTo)

	names := make([]string, 0, len(summary.byName))
	for name := range summary.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_, _ = fmt.Fprintf(stdout, "decoded\t%s\t%d\n", name, summary.byName[name])
	}

	for _, mismatch := range summary.mismatches {
		_, _ = fmt.Fprintf(stdout, "mismatch\t%s\n", mismatch)
	}
	for _, failure := range summary.failures {
		_, _ = fmt.Fprintf(stdout, "failure\t%s\n", failure)
	}
	if len(summary.failures) == 0 {
		_, _ = fmt.Fprintf(stdout, "result\tno packet the client sent failed to decode\n")
	}
}
