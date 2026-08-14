package protocol_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

const tcpTimeout = 10 * time.Second

func tcpLimits(t *testing.T) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatal(err)
	}
	return limits
}

// loopbackPair returns two connected TCP endpoints on the loopback interface.
// Nothing here ever binds a routable address.
func loopbackPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	type accepted struct {
		conn net.Conn
		err  error
	}
	incoming := make(chan accepted, 1)
	go func() {
		conn, err := listener.Accept()
		incoming <- accepted{conn: conn, err: err}
	}()

	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	result := <-incoming
	if result.err != nil {
		t.Fatalf("accept: %v", result.err)
	}

	t.Cleanup(func() {
		_ = client.Close()
		_ = result.conn.Close()
	})

	return client, result.conn
}

// oneByteConn forces single-byte reads and writes so a test exercises the
// fragmentation a real network can produce.
type oneByteConn struct {
	net.Conn
}

func (c oneByteConn) Read(data []byte) (int, error) {
	if len(data) > 1 {
		data = data[:1]
	}
	return c.Conn.Read(data)
}

func (c oneByteConn) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	return c.Conn.Write(data[:1])
}

// endpoint bundles a stream with the session it drives.
//
// Once the stream starts, the session belongs to it, so tests ask the stream
// for a snapshot rather than reading the session directly.
type endpoint struct {
	stream  *protocol.Stream
	session protocol.Session
}

func (e *endpoint) snapshot(t *testing.T) protocol.Snapshot {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), tcpTimeout)
	defer cancel()

	snapshot, err := e.stream.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	return snapshot
}

func newEndpoint(
	t *testing.T,
	role protocol.Role,
	conn net.Conn,
	fragment bool,
	options ...protocol.StreamOption,
) *endpoint {
	t.Helper()

	session, err := v1_8.Protocol().NewSession(role, tcpLimits(t))
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	var reader io.Reader = conn
	var writer io.Writer = conn
	if fragment {
		wrapped := oneByteConn{Conn: conn}
		reader, writer = wrapped, wrapped
	}

	stream, err := protocol.NewStream(session, protocol.Transport{
		Reader:    reader,
		Writer:    writer,
		Interrupt: conn.Close,
	}, options...)
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	return &endpoint{stream: stream, session: session}
}

func (e *endpoint) write(t *testing.T, state protocol.State, direction protocol.Direction, id int32, value any) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), tcpTimeout)
	defer cancel()

	if err := e.stream.Write(ctx, protocol.Packet{
		State: state, Direction: direction, ID: id, Value: value,
	}); err != nil {
		t.Fatalf("Write(%T) error = %v", value, err)
	}
}

func (e *endpoint) read(t *testing.T) protocol.Packet {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), tcpTimeout)
	defer cancel()

	packet, err := e.stream.Read(ctx)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	return packet
}

func TestStreamTCPStatusExchange(t *testing.T) {
	t.Parallel()

	for _, fragment := range []bool{false, true} {
		t.Run(fragmentName(fragment), func(t *testing.T) {
			t.Parallel()

			clientConn, serverConn := loopbackPair(t)
			client := newEndpoint(t, protocol.RoleClient, clientConn, fragment)
			server := newEndpoint(t, protocol.RoleServer, serverConn, fragment)

			const response = `{"version":{"name":"1.8.9","protocol":47},"players":{"max":20,"online":0}}`

			client.write(t, v1_8.StateHandshaking, protocol.DirectionServerbound, 0x00,
				&v1_8.HandshakingServerboundSetProtocol{
					ProtocolVersion: 47,
					ServerHost:      "127.0.0.1",
					ServerPort:      25565,
					NextState:       1,
				})

			handshake := server.read(t)
			if handshake.Name != "set_protocol" {
				t.Fatalf("server read %q, want set_protocol", handshake.Name)
			}

			// Both ends move to status automatically: the server on the
			// inbound decode, the client on the completed write.
			if got := server.snapshot(t).State; got != v1_8.StateStatus {
				t.Fatalf("server state = %q, want %q", got, v1_8.StateStatus)
			}
			if got := client.snapshot(t).State; got != v1_8.StateStatus {
				t.Fatalf("client state = %q, want %q", got, v1_8.StateStatus)
			}

			client.write(t, v1_8.StateStatus, protocol.DirectionServerbound, 0x00, &v1_8.StatusServerboundPingStart{})
			if got := server.read(t).Name; got != "ping_start" {
				t.Fatalf("server read %q, want ping_start", got)
			}

			server.write(t, v1_8.StateStatus, protocol.DirectionClientbound, 0x00,
				&v1_8.StatusClientboundServerInfo{Response: response})

			info := client.read(t)
			value, ok := info.Value.(*v1_8.StatusClientboundServerInfo)
			if !ok || value.Response != response {
				t.Fatalf("client read %#v, want the status response", info.Value)
			}

			const nonce = int64(0x0102030405060708)
			client.write(t, v1_8.StateStatus, protocol.DirectionServerbound, 0x01, &v1_8.StatusServerboundPing{Time: nonce})

			ping := server.read(t)
			pingValue, ok := ping.Value.(*v1_8.StatusServerboundPing)
			if !ok || pingValue.Time != nonce {
				t.Fatalf("server read %#v, want the ping nonce", ping.Value)
			}

			server.write(t, v1_8.StateStatus, protocol.DirectionClientbound, 0x01, &v1_8.StatusClientboundPing{Time: nonce})

			pong := client.read(t)
			pongValue, ok := pong.Value.(*v1_8.StatusClientboundPing)
			if !ok || pongValue.Time != nonce {
				t.Fatalf("client read %#v, want the pong nonce", pong.Value)
			}
		})
	}
}

func fragmentName(fragment bool) string {
	if fragment {
		return "one byte at a time"
	}
	return "whole frames"
}

func TestStreamTCPCompressedLogin(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := loopbackPair(t)

	sink := newCapturingSink()
	client := newEndpoint(t, protocol.RoleClient, clientConn, false, protocol.WithObservationSink(sink))
	server := newEndpoint(t, protocol.RoleServer, serverConn, false)

	const threshold = 64

	client.write(t, v1_8.StateHandshaking, protocol.DirectionServerbound, 0x00,
		&v1_8.HandshakingServerboundSetProtocol{
			ProtocolVersion: 47,
			ServerHost:      "127.0.0.1",
			ServerPort:      25565,
			NextState:       2,
		})
	if got := server.read(t).Name; got != "set_protocol" {
		t.Fatalf("server read %q, want set_protocol", got)
	}
	if got := server.snapshot(t).State; got != v1_8.StateLogin {
		t.Fatalf("server state = %q, want %q", got, v1_8.StateLogin)
	}

	client.write(t, v1_8.StateLogin, protocol.DirectionServerbound, 0x00,
		&v1_8.LoginServerboundLoginStart{Username: "Alex"})
	if got := server.read(t).Name; got != "login_start" {
		t.Fatalf("server read %q, want login_start", got)
	}

	// Set compression travels uncompressed. Its own transition applies to
	// everything after it.
	server.write(t, v1_8.StateLogin, protocol.DirectionClientbound, 0x03,
		&v1_8.LoginClientboundCompress{Threshold: threshold})

	compress := client.read(t)
	if compress.Name != "compress" {
		t.Fatalf("client read %q, want compress", compress.Name)
	}
	for _, endpoint := range []*endpoint{client, server} {
		pipeline := endpoint.snapshot(t).Pipeline
		if pipeline["compression.enabled"] != "true" || pipeline["compression.threshold"] != "64" {
			t.Fatalf("pipeline = %v, want compression enabled at 64", pipeline)
		}
	}

	// The set-compression frame itself must still be uncompressed: its body
	// is the packet ID followed directly by the threshold.
	compressBody := frameBody(t, sink.rawFrameFor(t, "compress"))
	rawCompress, err := java.SplitPacketBody(compressBody)
	if err != nil {
		t.Fatalf("the set-compression frame is not a plain packet body: %v", err)
	}
	if rawCompress.ID != 0x03 || !bytes.Equal(rawCompress.Payload, []byte{threshold}) {
		t.Fatalf("set-compression frame body = %x, want an uncompressed compress packet", compressBody)
	}

	server.write(t, v1_8.StateLogin, protocol.DirectionClientbound, 0x02,
		&v1_8.LoginClientboundSuccess{UUID: "00000000-0000-0000-0000-000000000001", Username: "Alex"})
	if got := client.read(t).Name; got != "success" {
		t.Fatalf("client read %q, want success", got)
	}
	for _, endpoint := range []*endpoint{client, server} {
		if got := endpoint.snapshot(t).State; got != v1_8.StatePlay {
			t.Fatalf("state = %q, want %q after login success", got, v1_8.StatePlay)
		}
	}

	// A small play packet stays uncompressed inside the envelope.
	server.write(t, v1_8.StatePlay, protocol.DirectionClientbound, 0x00,
		&v1_8.PlayClientboundKeepAlive{KeepAliveID: 42})
	small := client.read(t)
	smallValue, ok := small.Value.(*v1_8.PlayClientboundKeepAlive)
	if !ok || smallValue.KeepAliveID != 42 {
		t.Fatalf("client read %#v, want the keep alive", small.Value)
	}
	if declared := declaredDataLength(t, sink.rawFrameFor(t, "keep_alive")); declared != 0 {
		t.Fatalf("a packet below the threshold declared data length %d, want 0", declared)
	}

	// A large play packet is compressed.
	message := `{"text":"` + strings.Repeat("x", 256) + `"}`
	server.write(t, v1_8.StatePlay, protocol.DirectionClientbound, 0x02,
		&v1_8.PlayClientboundChat{Message: message, Position: 0})
	large := client.read(t)
	largeValue, ok := large.Value.(*v1_8.PlayClientboundChat)
	if !ok || largeValue.Message != message {
		t.Fatalf("client read %#v, want the chat message", large.Value)
	}
	if declared := declaredDataLength(t, sink.rawFrameFor(t, "chat")); declared <= 0 {
		t.Fatalf("a packet above the threshold declared data length %d, want a positive size", declared)
	}

	// The server closes politely with a play-state disconnect.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), tcpTimeout)
	defer cancel()
	if err := server.stream.Shutdown(shutdownCtx, `{"text":"goodbye"}`); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	disconnect := client.read(t)
	if disconnect.Name != "kick_disconnect" {
		t.Fatalf("client read %q, want kick_disconnect", disconnect.Name)
	}
	if err := server.stream.Wait(); err != nil {
		t.Fatalf("server Wait() error = %v, want a clean shutdown", err)
	}
}

func TestStreamTCPClientCleanClose(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := loopbackPair(t)
	client := newEndpoint(t, protocol.RoleClient, clientConn, false)
	server := newEndpoint(t, protocol.RoleServer, serverConn, false)

	client.write(t, v1_8.StateHandshaking, protocol.DirectionServerbound, 0x00,
		&v1_8.HandshakingServerboundSetProtocol{
			ProtocolVersion: 47, ServerHost: "127.0.0.1", ServerPort: 25565, NextState: 1,
		})
	server.read(t)

	// A client role has no disconnect packet, so shutdown just closes.
	ctx, cancel := context.WithTimeout(context.Background(), tcpTimeout)
	defer cancel()
	if err := client.stream.Shutdown(ctx, "bye"); err != nil {
		t.Fatalf("client Shutdown() error = %v", err)
	}
	if err := client.stream.Wait(); err != nil {
		t.Fatalf("client Wait() error = %v, want a clean shutdown", err)
	}

	// The server sees the peer go away.
	if err := server.stream.Wait(); !errors.Is(err, io.EOF) {
		t.Fatalf("server Wait() error = %v, want the peer close", err)
	}
}

func TestStreamTCPMalformedFrameTerminatesTheStream(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := loopbackPair(t)
	server := newEndpoint(t, protocol.RoleServer, serverConn, false)

	// A frame length beyond the configured limit must be refused before the
	// payload is read.
	oversized := []byte{0xff, 0xff, 0xff, 0x7f}
	if _, err := clientConn.Write(oversized); err != nil {
		t.Fatalf("write oversized length: %v", err)
	}

	if err := server.stream.Wait(); !errors.Is(err, java.ErrFrameTooLarge) {
		t.Fatalf("server Wait() error = %v, want ErrFrameTooLarge", err)
	}
}

func TestStreamTCPUnknownPacketSurvivesRoundTrip(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := loopbackPair(t)
	client := newEndpoint(t, protocol.RoleClient, clientConn, false)
	server := newEndpoint(t, protocol.RoleServer, serverConn, false)

	client.write(t, v1_8.StateHandshaking, protocol.DirectionServerbound, 0x00,
		&v1_8.HandshakingServerboundSetProtocol{
			ProtocolVersion: 47, ServerHost: "127.0.0.1", ServerPort: 25565, NextState: 1,
		})
	server.read(t)

	// An ID the descriptor does not know must survive as raw bytes.
	client.write(t, v1_8.StateStatus, protocol.DirectionServerbound, 0x7f, protocol.UnknownPacket{Payload: []byte{1, 2, 3}})

	unknown := server.read(t)
	if unknown.ID != 0x7f || unknown.Name != "" {
		t.Fatalf("server read %+v, want an unknown packet", unknown)
	}
	value, ok := unknown.Value.(protocol.UnknownPacket)
	if !ok || !bytes.Equal(value.Payload, []byte{1, 2, 3}) {
		t.Fatalf("server read value %#v, want the raw payload", unknown.Value)
	}
}

// capturingSink keeps raw frame records so a test can inspect exact bytes.
type capturingSink struct {
	mu      sync.Mutex
	records []protocol.Observation
	updated chan struct{}
}

func newCapturingSink() *capturingSink {
	return &capturingSink{updated: make(chan struct{}, 1024)}
}

func (s *capturingSink) Observe(_ context.Context, observation protocol.Observation) error {
	s.mu.Lock()
	s.records = append(s.records, observation)
	s.mu.Unlock()

	select {
	case s.updated <- struct{}{}:
	default:
	}

	return nil
}

// rawFrameFor returns the raw frame bytes that belong to the named packet.
//
// Observations reach the sink from a dispatcher goroutine, so a record can
// still be in flight when Read has already returned its packet. The wait here
// is for that handoff, not for the stream to make progress.
func (s *capturingSink) rawFrameFor(t *testing.T, name string) []byte {
	t.Helper()

	deadline := time.After(tcpTimeout)
	for {
		if wire, ok := s.lookup(name); ok {
			return wire
		}

		select {
		case <-s.updated:
		case <-deadline:
			t.Fatalf("no raw frame observation for %q", name)
		}
	}
}

func (s *capturingSink) lookup(name string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var frameID uint64
	for _, record := range s.records {
		if record.Stage == protocol.ObservationPacket && record.Packet != nil && record.Packet.Name == name {
			frameID = record.Frame
		}
	}
	if frameID == 0 {
		return nil, false
	}

	for _, record := range s.records {
		if record.Stage == protocol.ObservationRawFrame && record.Frame == frameID {
			return record.Bytes, true
		}
	}

	return nil, false
}

// frameBody strips the length prefix and checks it against the frame size.
func frameBody(t *testing.T, wire []byte) []byte {
	t.Helper()

	reader := bytes.NewReader(wire)
	length, prefix, err := java.ReadVarInt(reader)
	if err != nil {
		t.Fatalf("read frame length: %v", err)
	}
	if int(length) != len(wire)-prefix {
		t.Fatalf("frame declares %d bytes but carries %d", length, len(wire)-prefix)
	}

	return wire[prefix:]
}

// declaredDataLength reports the compression envelope's data length. It is
// only meaningful once compression is active.
func declaredDataLength(t *testing.T, wire []byte) int32 {
	t.Helper()

	declared, _, err := java.ReadVarInt(bytes.NewReader(frameBody(t, wire)))
	if err != nil {
		t.Fatalf("read data length: %v", err)
	}

	return declared
}
