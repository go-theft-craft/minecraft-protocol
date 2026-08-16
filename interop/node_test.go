// Package interop verifies the Go managed stream against the pinned Node
// minecraft-protocol implementation over loopback TCP.
//
// Every process started here is bound to a timeout and cleaned up, and no
// scenario contacts a host outside 127.0.0.1.
package interop

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/login"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

const (
	loopback       = "127.0.0.1"
	scenarioBudget = 60 * time.Second
	stepBudget     = 20 * time.Second
)

// runnerEvent is one newline-delimited JSON message from the Node runner.
type runnerEvent struct {
	Event         string  `json:"event"`
	Message       string  `json:"message"`
	State         string  `json:"state"`
	Name          string  `json:"name"`
	Reason        string  `json:"reason"`
	Username      string  `json:"username"`
	UUID          string  `json:"uuid"`
	Version       *string `json:"version"`
	Protocol      *int    `json:"protocol"`
	MaxPlayers    *int    `json:"maxPlayers"`
	OnlinePlayers *int    `json:"onlinePlayers"`
	Threshold     *int    `json:"threshold"`
	KeepAliveID   *int    `json:"keepAliveId"`
	Length        *int    `json:"length"`
	Port          *int    `json:"port"`
}

// runner is a Node process plus its decoded transcript.
type runner struct {
	cmd    *exec.Cmd
	mu     sync.Mutex
	events []runnerEvent
	lines  chan runnerEvent
	stderr strings.Builder
	done   chan struct{}
}

func nodeAvailable(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH; run through devbox to include it")
	}
	if _, err := os.Stat(filepath.Join("node", "node_modules", "minecraft-protocol")); err != nil {
		t.Skip("interop dependencies are not installed; run `task test:interop`")
	}
}

func startRunner(t *testing.T, args ...string) *runner {
	t.Helper()

	nodeAvailable(t)

	ctx, cancel := context.WithTimeout(context.Background(), scenarioBudget)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, "node", append([]string{filepath.Join("node", "runner.mjs")}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	instance := &runner{
		cmd:   cmd,
		lines: make(chan runnerEvent, 64),
		done:  make(chan struct{}),
	}
	cmd.Stderr = &instance.stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}

	go func() {
		defer close(instance.done)

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			var event runnerEvent
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				continue
			}

			instance.mu.Lock()
			instance.events = append(instance.events, event)
			instance.mu.Unlock()

			select {
			case instance.lines <- event:
			default:
			}
		}
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			instance.mu.Lock()
			t.Logf("node transcript: %+v", instance.events)
			instance.mu.Unlock()
			if stderr := instance.stderr.String(); stderr != "" {
				t.Logf("node stderr: %s", stderr)
			}
		}
	})

	return instance
}

// await waits for the next event with the given name.
func (r *runner) await(t *testing.T, name string) runnerEvent {
	t.Helper()

	deadline := time.After(stepBudget)
	for {
		select {
		case event := <-r.lines:
			if event.Event == "error" {
				t.Fatalf("node runner failed: %s", event.Message)
			}
			if event.Event == name {
				return event
			}
		case <-r.done:
			// Exiting does not unsay what was already said. The runner
			// reports an event and then ends, which leaves both cases ready
			// at once, and a select picks between ready cases at random — so
			// the queue is drained before the exit is treated as terminal.
			select {
			case event := <-r.lines:
				if event.Event == "error" {
					t.Fatalf("node runner failed: %s", event.Message)
				}
				if event.Event == name {
					return event
				}

				continue
			default:
			}

			r.mu.Lock()
			transcript := append([]runnerEvent(nil), r.events...)
			r.mu.Unlock()
			t.Fatalf("node runner exited before %q; transcript: %+v", name, transcript)
		case <-deadline:
			t.Fatalf("timed out waiting for node event %q", name)
		}
	}
}

// transcript returns everything the runner reported so far.
func (r *runner) transcript() []runnerEvent {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]runnerEvent(nil), r.events...)
}

func (r *runner) waitForExit(t *testing.T) {
	t.Helper()

	select {
	case <-r.done:
	case <-time.After(stepBudget):
		t.Fatal("node runner did not exit")
	}
}

func interopLimits(t *testing.T) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatal(err)
	}
	return limits
}

// goEndpoint is a Go stream over one loopback connection.
type goEndpoint struct {
	stream  *protocol.Stream
	session protocol.Session
	sink    *transcriptSink
}

func newGoEndpoint(t *testing.T, role protocol.Role, conn net.Conn) *goEndpoint {
	t.Helper()

	session, err := v1_8.Protocol().NewSession(role, interopLimits(t))
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	sink := newTranscriptSink()
	stream, err := protocol.NewStream(session, protocol.Transport{
		Reader:    conn,
		Writer:    conn,
		Interrupt: conn.Close,
	}, protocol.WithObservationSink(sink))
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	return &goEndpoint{stream: stream, session: session, sink: sink}
}

func (e *goEndpoint) write(t *testing.T, state protocol.State, direction protocol.Direction, id int32, value any) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), stepBudget)
	defer cancel()

	if err := e.stream.Write(ctx, protocol.Packet{
		State: state, Direction: direction, ID: id, Value: value,
	}); err != nil {
		t.Fatalf("Write(%T) error = %v", value, err)
	}
}

// snapshot asks the stream for the session configuration. A running stream
// owns its session, so reading it directly would race the coordinator.
func (e *goEndpoint) snapshot(t *testing.T) protocol.Snapshot {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), stepBudget)
	defer cancel()

	snapshot, err := e.stream.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	return snapshot
}

func (e *goEndpoint) read(t *testing.T) protocol.Packet {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), stepBudget)
	defer cancel()

	packet, err := e.stream.Read(ctx)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	return packet
}

// transcriptSink records the packet observations a scenario compares against
// the Node transcript.
type transcriptSink struct {
	mu      sync.Mutex
	records []protocol.Observation
}

func newTranscriptSink() *transcriptSink { return &transcriptSink{} }

func (s *transcriptSink) Observe(_ context.Context, observation protocol.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.records = append(s.records, observation)

	return nil
}

func (s *transcriptSink) packetNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var names []string
	for _, record := range s.records {
		if record.Stage == protocol.ObservationPacket && record.Packet != nil {
			names = append(names, record.Packet.Name)
		}
	}
	return names
}

// TestGoClientAgainstNodeServerStatus drives the Go client-role stream through
// a status exchange with the pinned Node server.
func TestGoClientAgainstNodeServerStatus(t *testing.T) {
	runner := startRunner(t, "--mode", "server", "--scenario", "status", "--port", "0")
	listening := runner.await(t, "listening")
	if listening.Port == nil {
		t.Fatal("node server did not report a port")
	}

	conn := dialLoopback(t, *listening.Port)
	client := newGoEndpoint(t, protocol.RoleClient, conn)

	client.write(t, v1_8.StateHandshaking, protocol.DirectionServerbound, 0x00,
		&v1_8.HandshakingServerboundSetProtocol{
			ProtocolVersion: 47,
			ServerHost:      loopback,
			ServerPort:      uint16(*listening.Port),
			NextState:       1,
		})
	if got := client.snapshot(t).State; got != v1_8.StateStatus {
		t.Fatalf("client state = %q, want %q", got, v1_8.StateStatus)
	}

	client.write(t, v1_8.StateStatus, protocol.DirectionServerbound, 0x00, &v1_8.StatusServerboundPingStart{})

	info := client.read(t)
	value, ok := info.Value.(*v1_8.StatusClientboundServerInfo)
	if !ok {
		t.Fatalf("client read %T, want the status response", info.Value)
	}

	var status struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int    `json:"protocol"`
		} `json:"version"`
		Players struct {
			Max int `json:"max"`
		} `json:"players"`
	}
	if err := json.Unmarshal([]byte(value.Response), &status); err != nil {
		t.Fatalf("status response is not JSON: %v", err)
	}
	if status.Version.Protocol != 47 {
		t.Fatalf("status protocol = %d, want 47", status.Version.Protocol)
	}
	if status.Players.Max != 20 {
		t.Fatalf("status max players = %d, want 20", status.Players.Max)
	}

	const nonce = int64(0x1122334455667788)
	client.write(t, v1_8.StateStatus, protocol.DirectionServerbound, 0x01, &v1_8.StatusServerboundPing{Time: nonce})

	pong := client.read(t)
	pongValue, ok := pong.Value.(*v1_8.StatusClientboundPing)
	if !ok || pongValue.Time != nonce {
		t.Fatalf("client read %#v, want the pong nonce", pong.Value)
	}

	if err := client.stream.Shutdown(context.Background(), ""); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	runner.await(t, "status_served")
}

// TestGoClientAgainstNodeServerCompressedLogin drives an offline login with
// compression enabled by the Node server.
func TestGoClientAgainstNodeServerCompressedLogin(t *testing.T) {
	runner := startRunner(t, "--mode", "server", "--scenario", "login", "--port", "0")
	listening := runner.await(t, "listening")
	if listening.Port == nil {
		t.Fatal("node server did not report a port")
	}

	conn := dialLoopback(t, *listening.Port)
	client := newGoEndpoint(t, protocol.RoleClient, conn)

	client.write(t, v1_8.StateHandshaking, protocol.DirectionServerbound, 0x00,
		&v1_8.HandshakingServerboundSetProtocol{
			ProtocolVersion: 47,
			ServerHost:      loopback,
			ServerPort:      uint16(*listening.Port),
			NextState:       2,
		})
	client.write(t, v1_8.StateLogin, protocol.DirectionServerbound, 0x00,
		&v1_8.LoginServerboundLoginStart{Username: "Alex"})

	var (
		sawCompress bool
		sawSuccess  bool
		sawKeep     bool
		sawChat     bool
		threshold   int32
	)

	for !sawChat {
		packet := client.read(t)
		switch value := packet.Value.(type) {
		case *v1_8.LoginClientboundCompress:
			sawCompress = true
			threshold = value.Threshold

			// The pinned server picks its own threshold. What matters is that
			// the Go session adopted exactly the value it announced.
			if threshold <= 0 {
				t.Fatalf("compress threshold = %d, want a positive value", threshold)
			}
			pipeline := client.snapshot(t).Pipeline
			if pipeline["compression.enabled"] != "true" {
				t.Fatalf("pipeline = %v, want compression enabled", pipeline)
			}
			if got, want := pipeline["compression.threshold"], strconv.FormatInt(int64(threshold), 10); got != want {
				t.Fatalf("pipeline threshold = %q, want %q", got, want)
			}
		case *v1_8.LoginClientboundSuccess:
			sawSuccess = true
			if value.Username != "Alex" {
				t.Fatalf("login success username = %q, want Alex", value.Username)
			}
			if got := client.snapshot(t).State; got != v1_8.StatePlay {
				t.Fatalf("client state = %q, want %q", got, v1_8.StatePlay)
			}
		case *v1_8.PlayClientboundKeepAlive:
			sawKeep = true
			if value.KeepAliveID != 7 {
				t.Fatalf("keep alive ID = %d, want 7", value.KeepAliveID)
			}
		case *v1_8.PlayClientboundChat:
			sawChat = true
			if int32(len(value.Message)) < threshold {
				t.Fatalf("chat message is %d bytes, want more than the threshold %d", len(value.Message), threshold)
			}
		case *v1_8.LoginClientboundDisconnect:
			t.Fatalf("node rejected the login: %s", value.Reason)
		}
	}

	if !sawCompress || !sawSuccess || !sawKeep {
		t.Fatalf("missing packets: compress=%t success=%t keep_alive=%t", sawCompress, sawSuccess, sawKeep)
	}

	// Reply above the threshold, which makes the Node server disconnect us.
	client.write(t, v1_8.StatePlay, protocol.DirectionServerbound, 0x01,
		&v1_8.PlayServerboundChat{Message: strings.Repeat("y", 100)})

	disconnect := runner.await(t, "disconnect")
	if disconnect.Reason != "goodbye" {
		t.Fatalf("node disconnect reason = %q, want goodbye", disconnect.Reason)
	}

	kick := client.read(t)
	kickValue, ok := kick.Value.(*v1_8.PlayClientboundKickDisconnect)
	if !ok {
		t.Fatalf("client read %T, want kick_disconnect", kick.Value)
	}
	if !strings.Contains(kickValue.Reason, "goodbye") {
		t.Fatalf("kick reason = %q, want it to mention goodbye", kickValue.Reason)
	}

	// The Go observations and the Node transcript agree on what happened.
	names := client.sink.packetNames()
	for _, want := range []string{"compress", "success", "keep_alive", "chat", "kick_disconnect"} {
		if !contains(names, want) {
			t.Errorf("Go observations %v are missing %q", names, want)
		}
	}
	if !hasEvent(runner.transcript(), "login_success") {
		t.Error("node transcript is missing login_success")
	}

	runner.waitForExit(t)
}

// TestNodeClientAgainstGoServerStatus serves a status response to the pinned
// Node client.
func TestNodeClientAgainstGoServerStatus(t *testing.T) {
	nodeAvailable(t)

	listener, port := listenLoopback(t)

	accepted := acceptOne(t, listener)
	runner := startRunner(t, "--mode", "client", "--scenario", "status",
		"--host", loopback, "--port", fmt.Sprint(port))

	conn := <-accepted
	server := newGoEndpoint(t, protocol.RoleServer, conn)

	handshake := server.read(t)
	handshakeValue, ok := handshake.Value.(*v1_8.HandshakingServerboundSetProtocol)
	if !ok {
		t.Fatalf("server read %T, want set_protocol", handshake.Value)
	}
	if handshakeValue.ProtocolVersion != 47 || handshakeValue.NextState != 1 {
		t.Fatalf("handshake = %+v, want protocol 47 into status", handshakeValue)
	}
	if got := server.snapshot(t).State; got != v1_8.StateStatus {
		t.Fatalf("server state = %q, want %q", got, v1_8.StateStatus)
	}

	if got := server.read(t).Name; got != "ping_start" {
		t.Fatalf("server read %q, want ping_start", got)
	}

	const response = `{"version":{"name":"1.8.9","protocol":47},"players":{"max":20,"online":0},"description":{"text":"interop"}}`
	server.write(t, v1_8.StateStatus, protocol.DirectionClientbound, 0x00,
		&v1_8.StatusClientboundServerInfo{Response: response})

	ping := server.read(t)
	pingValue, ok := ping.Value.(*v1_8.StatusServerboundPing)
	if !ok {
		t.Fatalf("server read %T, want ping", ping.Value)
	}
	server.write(t, v1_8.StateStatus, protocol.DirectionClientbound, 0x01,
		&v1_8.StatusClientboundPing{Time: pingValue.Time})

	status := runner.await(t, "status")
	if status.Protocol == nil || *status.Protocol != 47 {
		t.Fatalf("node saw protocol %v, want 47", status.Protocol)
	}
	if status.MaxPlayers == nil || *status.MaxPlayers != 20 {
		t.Fatalf("node saw max players %v, want 20", status.MaxPlayers)
	}

	runner.waitForExit(t)
}

// TestNodeClientAgainstGoServerCompressedLogin drives set compression, login
// success, play packets either side of the threshold, and a graceful
// disconnect from the Go server-role stream.
func TestNodeClientAgainstGoServerCompressedLogin(t *testing.T) {
	nodeAvailable(t)

	const threshold = 64

	listener, port := listenLoopback(t)

	accepted := acceptOne(t, listener)
	runner := startRunner(t, "--mode", "client", "--scenario", "login",
		"--host", loopback, "--port", fmt.Sprint(port))

	conn := <-accepted
	server := newGoEndpoint(t, protocol.RoleServer, conn)

	handshake := server.read(t)
	handshakeValue, ok := handshake.Value.(*v1_8.HandshakingServerboundSetProtocol)
	if !ok || handshakeValue.NextState != 2 {
		t.Fatalf("server read %#v, want a handshake into login", handshake.Value)
	}

	start := server.read(t)
	startValue, ok := start.Value.(*v1_8.LoginServerboundLoginStart)
	if !ok || startValue.Username != "Alex" {
		t.Fatalf("server read %#v, want login_start from Alex", start.Value)
	}

	// Set compression, then log in. The set-compression packet itself travels
	// uncompressed; everything after it uses the envelope.
	server.write(t, v1_8.StateLogin, protocol.DirectionClientbound, 0x03,
		&v1_8.LoginClientboundCompress{Threshold: threshold})

	compress := runner.await(t, "compress")
	if compress.Threshold == nil || *compress.Threshold != threshold {
		t.Fatalf("node saw threshold %v, want %d", compress.Threshold, threshold)
	}

	server.write(t, v1_8.StateLogin, protocol.DirectionClientbound, 0x02,
		&v1_8.LoginClientboundSuccess{
			UUID:     "00000000-0000-0000-0000-000000000001",
			Username: "Alex",
		})
	success := runner.await(t, "login_success")
	if success.Username != "Alex" {
		t.Fatalf("node saw username %q, want Alex", success.Username)
	}
	if got := server.snapshot(t).State; got != v1_8.StatePlay {
		t.Fatalf("server state = %q, want %q", got, v1_8.StatePlay)
	}

	server.write(t, v1_8.StatePlay, protocol.DirectionClientbound, 0x01,
		&v1_8.PlayClientboundLogin{
			EntityID:   1,
			GameMode:   0,
			Dimension:  0,
			Difficulty: 1,
			MaxPlayers: 20,
			LevelType:  "default",
		})
	runner.await(t, "state")

	// Below the threshold.
	server.write(t, v1_8.StatePlay, protocol.DirectionClientbound, 0x00,
		&v1_8.PlayClientboundKeepAlive{KeepAliveID: 7})

	// Above the threshold.
	message := `{"text":"` + strings.Repeat("z", 256) + `"}`
	server.write(t, v1_8.StatePlay, protocol.DirectionClientbound, 0x02,
		&v1_8.PlayClientboundChat{Message: message, Position: 0})

	var sawKeep, sawChat bool
	for !sawKeep || !sawChat {
		packet := runner.await(t, "packet")
		switch packet.Name {
		case "keep_alive":
			sawKeep = true
			if packet.KeepAliveID == nil || *packet.KeepAliveID != 7 {
				t.Fatalf("node saw keep alive %v, want 7", packet.KeepAliveID)
			}
		case "chat":
			sawChat = true
			if packet.Length == nil || *packet.Length != len(message) {
				t.Fatalf("node saw a chat of %v characters, want %d", packet.Length, len(message))
			}
		}
	}

	// A graceful shutdown sends the play-state disconnect as the final frame.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), stepBudget)
	defer cancel()
	if err := server.stream.Shutdown(shutdownCtx, `{"text":"goodbye"}`); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	disconnect := runner.await(t, "disconnect")
	if !strings.Contains(disconnect.Reason, "goodbye") {
		t.Fatalf("node disconnect reason = %q, want it to mention goodbye", disconnect.Reason)
	}
	if disconnect.State != "play" {
		t.Fatalf("node saw a %s-state disconnect, want play", disconnect.State)
	}

	if err := server.stream.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, want a clean shutdown", err)
	}
	runner.waitForExit(t)
}

// TestGoServerRejectsOversizedFrameFromLoopback proves the frame limit holds
// against a real socket rather than an in-memory reader.
func TestGoServerRejectsOversizedFrameFromLoopback(t *testing.T) {
	listener, port := listenLoopback(t)
	accepted := acceptOne(t, listener)

	client, err := net.Dial("tcp", net.JoinHostPort(loopback, strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	conn := <-accepted
	server := newGoEndpoint(t, protocol.RoleServer, conn)

	if _, err := client.Write([]byte{0xff, 0xff, 0xff, 0x7f}); err != nil {
		t.Fatalf("write oversized length: %v", err)
	}

	if err := server.stream.Wait(); !errors.Is(err, java.ErrFrameTooLarge) {
		t.Fatalf("Wait() error = %v, want ErrFrameTooLarge", err)
	}
}

func dialLoopback(t *testing.T, port int) net.Conn {
	t.Helper()

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(loopback, strconv.Itoa(port)), stepBudget)
	if err != nil {
		t.Fatalf("dial loopback: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

func listenLoopback(t *testing.T) (net.Listener, int) {
	t.Helper()

	listener, err := net.Listen("tcp", loopback+":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	return listener, listener.Addr().(*net.TCPAddr).Port
}

func acceptOne(t *testing.T, listener net.Listener) <-chan net.Conn {
	t.Helper()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- conn
	}()

	t.Cleanup(func() {
		select {
		case conn, ok := <-accepted:
			if ok && conn != nil {
				_ = conn.Close()
			}
		default:
		}
	})

	return accepted
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasEvent(events []runnerEvent, name string) bool {
	for _, event := range events {
		if event.Event == name {
			return true
		}
	}
	return false
}

// TestGoClientAgainstNodeServerEncryptedLogin runs the Go negotiator against
// the pinned Node server in online mode, which is the only configuration that
// makes it send an encryption request.
func TestGoClientAgainstNodeServerEncryptedLogin(t *testing.T) {
	runner := startRunner(t, "--mode", "server", "--scenario", "encrypted-login", "--port", "0")
	listening := runner.await(t, "listening")
	if listening.Port == nil {
		t.Fatal("node server did not report a port")
	}

	conn := dialLoopback(t, *listening.Port)
	client := newGoEndpoint(t, protocol.RoleClient, conn)

	client.write(t, v1_8.StateHandshaking, protocol.DirectionServerbound, 0x00,
		&v1_8.HandshakingServerboundSetProtocol{
			ProtocolVersion: 47,
			ServerHost:      loopback,
			ServerPort:      uint16(*listening.Port),
			NextState:       2,
		})

	authenticator, err := login.NewOffline("interop")
	if err != nil {
		t.Fatalf("NewOffline() error = %v", err)
	}
	negotiator, err := login.NewNegotiator(authenticator)
	if err != nil {
		t.Fatalf("NewNegotiator() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), scenarioBudget)
	defer cancel()

	profile, err := negotiator.Negotiate(ctx, client.stream)
	if err != nil {
		t.Fatalf("Negotiate() error = %v", err)
	}
	if profile.Name.String() != "interop" {
		t.Fatalf("profile name = %q, want interop", profile.Name)
	}
	if profile.UUID.IsZero() {
		t.Fatal("a successful login must carry a UUID")
	}

	snapshot := client.snapshot(t)
	if snapshot.Pipeline["encryption.enabled"] != "true" {
		t.Fatalf("pipeline = %v, want encryption enabled", snapshot.Pipeline)
	}
	if snapshot.State != v1_8.StatePlay {
		t.Fatalf("client state = %q, want %q", snapshot.State, v1_8.StatePlay)
	}

	success := runner.await(t, "login_success")
	if success.Username != "interop" {
		t.Fatalf("node saw username %q, want interop", success.Username)
	}

	// A play packet after the switch proves the cipher survives the state
	// change rather than only the login exchange.
	client.write(t, v1_8.StatePlay, protocol.DirectionServerbound, 0x01,
		&v1_8.PlayServerboundChat{Message: strings.Repeat("y", 100)})

	chat := runner.await(t, "packet")
	if chat.Name != "chat" {
		t.Fatalf("node saw packet %q, want chat", chat.Name)
	}

	disconnect := runner.await(t, "disconnect")
	if disconnect.Reason != "goodbye" {
		t.Fatalf("node disconnect reason = %q, want goodbye", disconnect.Reason)
	}

	// The encryption switch is recorded even though the key is not.
	if !contains(client.sink.packetNames(), "success") {
		t.Errorf("Go observations %v are missing the login success", client.sink.packetNames())
	}

	runner.waitForExit(t)
}

// TestNodeClientAgainstGoServerEncryptedLogin drives the pinned Node client
// through a Go-served encryption handshake.
func TestNodeClientAgainstGoServerEncryptedLogin(t *testing.T) {
	nodeAvailable(t)

	listener, port := listenLoopback(t)

	accepted := acceptOne(t, listener)
	runner := startRunner(t, "--mode", "client", "--scenario", "encrypted-login",
		"--host", loopback, "--port", fmt.Sprint(port))

	conn := <-accepted
	server := newGoEndpoint(t, protocol.RoleServer, conn)

	handshake := server.read(t)
	if handshakeValue, ok := handshake.Value.(*v1_8.HandshakingServerboundSetProtocol); !ok || handshakeValue.NextState != 2 {
		t.Fatalf("server read %#v, want a handshake into login", handshake.Value)
	}

	start := server.read(t)
	startValue, ok := start.Value.(*v1_8.LoginServerboundLoginStart)
	if !ok || startValue.Username != "interop" {
		t.Fatalf("server read %#v, want login_start from interop", start.Value)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encoded, err := java.EncodeServerPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("encode key: %v", err)
	}

	token := []byte{0x0a, 0x0b, 0x0c, 0x0d}
	server.write(t, v1_8.StateLogin, protocol.DirectionClientbound, 0x01,
		&v1_8.LoginClientboundEncryptionBegin{
			ServerID:    "",
			PublicKey:   encoded,
			VerifyToken: token,
		})

	response := server.read(t)
	responseValue, ok := response.Value.(*v1_8.LoginServerboundEncryptionBegin)
	if !ok {
		t.Fatalf("server read %T, want the encryption response", response.Value)
	}

	if err := java.VerifyToken(key, token, responseValue.VerifyToken); err != nil {
		t.Fatalf("verify token: %v", err)
	}

	secret, err := java.DecryptSharedSecret(key, responseValue.SharedSecret)
	if err != nil {
		t.Fatalf("decrypt session key: %v", err)
	}

	controlCtx, cancel := context.WithTimeout(context.Background(), stepBudget)
	defer cancel()
	if err := server.stream.Control(controlCtx, java.EncryptionControl{Secret: secret}); err != nil {
		t.Fatalf("enable encryption: %v", err)
	}
	if got := server.snapshot(t).Pipeline["encryption.enabled"]; got != "true" {
		t.Fatalf("encryption.enabled = %q, want true", got)
	}

	server.write(t, v1_8.StateLogin, protocol.DirectionClientbound, 0x02,
		&v1_8.LoginClientboundSuccess{
			UUID:     "00000000-0000-0000-0000-000000000001",
			Username: "interop",
		})

	success := runner.await(t, "login_success")
	if success.Username != "interop" {
		t.Fatalf("node saw username %q, want interop", success.Username)
	}

	// Everything from here is encrypted, so a keep-alive that arrives intact
	// proves the cipher is composed correctly in both directions.
	server.write(t, v1_8.StatePlay, protocol.DirectionClientbound, 0x01,
		&v1_8.PlayClientboundLogin{
			EntityID:   1,
			GameMode:   0,
			Dimension:  0,
			Difficulty: 1,
			MaxPlayers: 20,
			LevelType:  "default",
		})
	server.write(t, v1_8.StatePlay, protocol.DirectionClientbound, 0x00,
		&v1_8.PlayClientboundKeepAlive{KeepAliveID: 7})

	keepAlive := runner.await(t, "packet")
	if keepAlive.Name != "keep_alive" {
		t.Fatalf("node saw packet %q, want keep_alive", keepAlive.Name)
	}
	if keepAlive.KeepAliveID == nil || *keepAlive.KeepAliveID != 7 {
		t.Fatalf("node saw keep alive %v, want 7", keepAlive.KeepAliveID)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), stepBudget)
	defer cancelShutdown()
	if err := server.stream.Shutdown(shutdownCtx, `{"text":"goodbye"}`); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	disconnect := runner.await(t, "disconnect")
	if !strings.Contains(disconnect.Reason, "goodbye") {
		t.Fatalf("node disconnect reason = %q, want it to mention goodbye", disconnect.Reason)
	}

	if err := server.stream.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, want a clean shutdown", err)
	}
	runner.waitForExit(t)
}
