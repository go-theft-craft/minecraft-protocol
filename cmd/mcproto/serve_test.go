package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	capturepkg "github.com/go-theft-craft/minecraft-protocol/capture"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/login"
)

// writeLoginScript records a protocol 47 login the way a client would have
// seen it, so the harness has a real script to play back.
//
// The frames are built by the real encoder rather than written as literals:
// the point of the harness is that a recorded connection can be played again
// through this code, and a script of hand-made bytes would not test that.
func writeLoginScript(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "script.mcpcap")
	sink, err := capturepkg.NewFileSink(path, capturepkg.Header{
		Protocol:          v1_8.Protocol().ID(),
		Role:              "client",
		FrameBytes:        1 << 20,
		DecompressedBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("NewLimits: %v", err)
	}
	// The server half encodes what the server said.
	server, err := v1_8.Protocol().NewSession(protocol.RoleServer, limits)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	server.ApplyTransition(protocol.Transition{Control: protocol.StateControl{State: v1_8.StateLogin}})

	var sequence uint64
	record := func(direction protocol.Direction, state protocol.State, id int32, name string, value any) {
		sequence++
		frameID := sequence

		before := protocol.NewSnapshot(state, nil)
		after := before

		var wire []byte
		if direction == protocol.DirectionClientbound {
			body, err := server.EncodeFrame(protocol.Packet{
				State: state, Direction: direction, ID: id, Value: value,
			})
			if err != nil {
				t.Fatalf("EncodeFrame(%s): %v", name, err)
			}
			frame, err := server.Framer().BuildFrame(body)
			if err != nil {
				t.Fatalf("BuildFrame(%s): %v", name, err)
			}
			wire = frame.WireBytes()
		}

		if err := sink.Observe(t.Context(), protocol.Observation{
			Sequence: sequence, Frame: frameID, Direction: direction,
			Stage: protocol.ObservationRawFrame, Before: before, After: after,
			Bytes: wire, OriginalLen: len(wire),
		}); err != nil {
			t.Fatalf("Observe: %v", err)
		}

		sequence++
		if err := sink.Observe(t.Context(), protocol.Observation{
			Sequence: sequence, Frame: frameID, Direction: direction,
			Stage: protocol.ObservationPacket, Before: before, After: after,
			Packet: &protocol.PacketMetadata{State: state, Direction: direction, ID: id, Name: name},
		}); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}

	// The client opens with a handshake and a login start; the server answers
	// with a success, which is the whole of a protocol 47 login.
	record(protocol.DirectionServerbound, v1_8.StateHandshaking, 0x00, "set_protocol", nil)
	record(protocol.DirectionServerbound, v1_8.StateLogin, 0x00, "login_start", nil)
	record(protocol.DirectionClientbound, v1_8.StateLogin, 0x02, "success", &v1_8.LoginClientboundSuccess{
		UUID:     "069a79f4-44e9-4726-a5be-fca90e38aaf5",
		Username: "scripted",
	})

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	return path
}

// TestServeDrivesARealLoginFromAScript is the harness's contract: a recording
// of one connection is enough to serve another, and the client on the far end
// is a real client rather than a fixture.
func TestServeDrivesARealLoginFromAScript(t *testing.T) {
	script := writeLoginScript(t)
	address := freeAddress(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	done := make(chan int, 1)
	go func() {
		done <- serveInBackground(ctx, "--script", script, "--address", address,
			"--idle", "3s", "--step-timeout", "3s")
	}()

	profile := loginThroughHarness(ctx, t, address)
	if profile.Name.String() != "scripted" {
		t.Fatalf("logged in as %q, want the name the script recorded", profile.Name)
	}

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("the harness did not finish after the client left")
	}
}

// loginThroughHarness runs a real client against the harness.
func loginThroughHarness(ctx context.Context, t *testing.T, address string) login.Profile {
	t.Helper()

	var conn net.Conn
	var err error
	// The harness needs a moment to bind, and a retry loop is more honest
	// than a sleep chosen to be long enough.
	for range 100 {
		conn, err = net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial the harness: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	stream, _, err := startStream(ctx, v1_8.Protocol(), conn)
	if err != nil {
		t.Fatalf("start stream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	if err := writeHandshake(ctx, stream, v1_8.Protocol(), "127.0.0.1", 25565, loginNextState); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	authenticator, err := login.NewOffline("tester")
	if err != nil {
		t.Fatalf("NewOffline: %v", err)
	}
	negotiator, err := login.NewNegotiator(authenticator)
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	profile, err := negotiator.Negotiate(ctx, stream)
	if err != nil {
		t.Fatalf("Negotiate against the harness: %v", err)
	}

	return profile
}

func TestServeAnswersAStatusPing(t *testing.T) {
	script := writeLoginScript(t)
	address := freeAddress(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	go func() {
		_ = serveInBackground(ctx, "--script", script, "--address", address,
			"--idle", "3s", "--keep-listening")
	}()

	// The harness must still be listening after a ping, because a client
	// pings before it joins.
	var response string
	for range 100 {
		code, stdout, _ := runCLI(t, "status", "--address", address, "--timeout", "2s")
		if code == exitSuccess {
			response = stdout

			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(response, "mcproto verification harness") {
		t.Fatalf("status did not reach the harness: %q", response)
	}

	profile := loginThroughHarness(ctx, t, address)
	if profile.Name.String() != "scripted" {
		t.Fatalf("logged in as %q after a ping, want the harness still serving", profile.Name)
	}
}

func TestServeRefusesAClientOnAnotherProtocol(t *testing.T) {
	script := writeLoginScript(t)
	address := freeAddress(t)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	go func() {
		_ = serveInBackground(ctx, "--script", script, "--address", address,
			"--idle", "3s", "--keep-listening")
	}()

	// The script is protocol 47 and this client speaks 775, which is exactly
	// the mistake somebody makes with the wrong client open.
	var conn net.Conn
	var err error
	for range 100 {
		conn, err = net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial the harness: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// A protocol 775 handshake asking for login, by hand: the harness must
	// refuse it rather than replay a protocol 47 script at it.
	handshake := []byte{
		0x10, 0x00, 0x87, 0x06, 0x09,
		'1', '2', '7', '.', '0', '.', '0', '.', '1',
		0x63, 0xdd, 0x02,
	}
	if _, err := conn.Write(handshake); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buffer := make([]byte, 512)
	n, _ := conn.Read(buffer)
	if n == 0 {
		t.Fatal("the harness closed without saying why")
	}
	if !strings.Contains(string(buffer[:n]), "protocol") {
		t.Fatalf("the refusal does not name the mismatch: %q", buffer[:n])
	}
}

func TestServeRequiresAScript(t *testing.T) {
	t.Parallel()

	code, _, stderr := runCLI(t, "serve")
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "--script") {
		t.Fatalf("stderr does not name the missing flag: %q", stderr)
	}
}

func TestServeRejectsAScriptItCannotSpeak(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "alien.mcpcap")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writer, err := capturepkg.NewWriter(file, capturepkg.Header{
		Protocol:          "java/9999",
		FrameBytes:        1 << 20,
		DecompressedBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close file: %v", err)
	}

	code, _, stderr := runCLI(t, "serve", "--script", path)
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "java/9999") {
		t.Fatalf("stderr does not name the protocol: %q", stderr)
	}
}

// serveInBackground runs the harness on its own goroutine.
//
// It calls the entry point directly rather than through the test helper,
// because a helper that fails with t.Fatal may only be used from the test's
// own goroutine.
func serveInBackground(ctx context.Context, args ...string) int {
	return run(ctx, append([]string{"serve"}, args...), strings.NewReader(""), io.Discard, io.Discard)
}

// freeAddress reserves a loopback port and releases it, so a test binds
// somewhere nothing else is rather than a port chosen by hope.
func freeAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}

	return address
}
