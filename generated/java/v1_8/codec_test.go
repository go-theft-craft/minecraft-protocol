package v1_8

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

func TestProtocol47CodecByteVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		state     protocol.State
		direction protocol.Direction
		id        int32
		packet    string
		wire      []byte
		payload   []byte
		value     any
	}{
		{
			name:      "handshake",
			state:     StateHandshaking,
			direction: protocol.DirectionServerbound,
			id:        0x00,
			packet:    "set_protocol",
			wire:      []byte{0x0f, 0x00, 0x2f, 0x09, 'l', 'o', 'c', 'a', 'l', 'h', 'o', 's', 't', 0x63, 0xdd, 0x02},
			payload:   []byte{0x2f, 0x09, 'l', 'o', 'c', 'a', 'l', 'h', 'o', 's', 't', 0x63, 0xdd, 0x02},
			value: &HandshakingServerboundSetProtocol{
				ProtocolVersion: 47,
				ServerHost:      "localhost",
				ServerPort:      25565,
				NextState:       2,
			},
		},
		{
			name:      "status request",
			state:     StateStatus,
			direction: protocol.DirectionServerbound,
			id:        0x00,
			packet:    "ping_start",
			wire:      []byte{0x01, 0x00},
			payload:   []byte{},
			value:     &StatusServerboundPingStart{},
		},
		{
			name:      "status ping response",
			state:     StateStatus,
			direction: protocol.DirectionClientbound,
			id:        0x01,
			packet:    "ping",
			wire:      []byte{0x09, 0x01, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			payload:   []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			value:     &StatusClientboundPing{Time: 0x0102030405060708},
		},
		{
			name:      "login start",
			state:     StateLogin,
			direction: protocol.DirectionServerbound,
			id:        0x00,
			packet:    "login_start",
			wire:      []byte{0x06, 0x00, 0x04, 'A', 'l', 'e', 'x'},
			payload:   []byte{0x04, 'A', 'l', 'e', 'x'},
			value:     &LoginServerboundLoginStart{Username: "Alex"},
		},
		{
			name:      "play clientbound login",
			state:     StatePlay,
			direction: protocol.DirectionClientbound,
			id:        0x01,
			packet:    "login",
			wire:      []byte{0x0f, 0x01, 0x00, 0x00, 0x00, 0x2a, 0x01, 0x00, 0x01, 0x14, 0x04, 'f', 'l', 'a', 't', 0x00},
			payload:   []byte{0x00, 0x00, 0x00, 0x2a, 0x01, 0x00, 0x01, 0x14, 0x04, 'f', 'l', 'a', 't', 0x00},
			value: &PlayClientboundLogin{
				EntityID:         42,
				GameMode:         1,
				Dimension:        0,
				Difficulty:       1,
				MaxPlayers:       20,
				LevelType:        "flat",
				ReducedDebugInfo: false,
			},
		},
		{
			name:      "play serverbound chat",
			state:     StatePlay,
			direction: protocol.DirectionServerbound,
			id:        0x01,
			packet:    "chat",
			wire:      []byte{0x07, 0x01, 0x05, 'h', 'e', 'l', 'l', 'o'},
			payload:   []byte{0x05, 'h', 'e', 'l', 'l', 'o'},
			value:     &PlayServerboundChat{Message: "hello"},
		},
	}

	limits := protocol47SessionLimits(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			decoderRole, encoderRole := protocol.RoleClient, protocol.RoleServer
			if test.direction == protocol.DirectionServerbound {
				decoderRole, encoderRole = protocol.RoleServer, protocol.RoleClient
			}
			decoder := protocol47Session(t, decoderRole, test.state, limits)
			encoder := protocol47Session(t, encoderRole, test.state, limits)

			packet, err := readWire(t, decoder, test.wire)
			if err != nil {
				t.Fatalf("DecodeFrame() error = %v", err)
			}
			if packet.State != test.state || packet.Direction != test.direction || packet.ID != test.id || packet.Name != test.packet {
				t.Fatalf("DecodeFrame() envelope = {%q %d %#x %q}, want {%q %d %#x %q}", packet.State, packet.Direction, packet.ID, packet.Name, test.state, test.direction, test.id, test.packet)
			}
			if !bytes.Equal(packet.Payload, test.payload) {
				t.Fatalf("DecodeFrame() payload = %x, want %x", packet.Payload, test.payload)
			}
			if gotType, wantType := reflect.TypeOf(packet.Value), reflect.TypeOf(test.value); gotType != wantType {
				t.Fatalf("DecodeFrame() concrete value type = %v, want %v", gotType, wantType)
			}
			if !reflect.DeepEqual(packet.Value, test.value) {
				t.Fatalf("DecodeFrame() value = %#v, want %#v", packet.Value, test.value)
			}

			encoded, err := writeWire(t, encoder, packet)
			if err != nil {
				t.Fatalf("EncodeFrame() error = %v", err)
			}
			if !bytes.Equal(encoded, test.wire) {
				t.Fatalf("EncodeFrame() = %x, want %x", encoded, test.wire)
			}
		})
	}
}

func TestProtocol47UnknownPacketOwnershipAndReencoding(t *testing.T) {
	t.Parallel()

	limits := protocol47SessionLimits(t)
	decoder := protocol47Session(t, protocol.RoleClient, StateStatus, limits)
	encoder := protocol47Session(t, protocol.RoleServer, StateStatus, limits)
	wire := []byte{0x04, 0x7f, 0x01, 0x02, 0x03}

	packet, err := readWire(t, decoder, wire)
	if err != nil {
		t.Fatalf("DecodeFrame() error = %v", err)
	}
	unknown, ok := packet.Value.(protocol.UnknownPacket)
	if !ok {
		t.Fatalf("DecodeFrame() concrete value type = %T, want protocol.UnknownPacket", packet.Value)
	}
	if packet.State != StateStatus || packet.Direction != protocol.DirectionClientbound || packet.ID != 0x7f || packet.Name != "" {
		t.Fatalf("DecodeFrame() envelope = {%q %d %#x %q}", packet.State, packet.Direction, packet.ID, packet.Name)
	}
	if !bytes.Equal(packet.Payload, []byte{0x01, 0x02, 0x03}) || !bytes.Equal(unknown.Payload, []byte{0x01, 0x02, 0x03}) {
		t.Fatalf("DecodeFrame() payloads = %x and %x", packet.Payload, unknown.Payload)
	}

	encoded, err := writeWire(t, encoder, packet)
	if err != nil {
		t.Fatalf("EncodeFrame() error = %v", err)
	}
	if !bytes.Equal(encoded, wire) {
		t.Fatalf("EncodeFrame() = %x, want %x", encoded, wire)
	}

	packet.Payload[0] = 0xff
	if unknown.Payload[0] != 0x01 {
		t.Fatal("protocol.UnknownPacket.Payload aliases protocol.Packet.Payload")
	}
	unknown.Payload[1] = 0xff
	if packet.Payload[1] != 0x02 {
		t.Fatal("protocol.Packet.Payload aliases protocol.UnknownPacket.Payload")
	}
}

func TestProtocol47LegacyPingIsNotAFramedPacket(t *testing.T) {
	t.Parallel()

	limits := protocol47SessionLimits(t)
	decoder := protocol47Session(t, protocol.RoleServer, StateHandshaking, limits)
	wire := []byte{0x03, 0xfe, 0x01, 0x01}

	packet, err := readWire(t, decoder, wire)
	if err != nil {
		t.Fatalf("DecodeFrame() error = %v", err)
	}
	if packet.ID != 0xfe || packet.Name != "" {
		t.Fatalf("DecodeFrame() envelope = {%#x %q}, want unknown packet ID 0xfe", packet.ID, packet.Name)
	}
	if _, ok := packet.Value.(protocol.UnknownPacket); !ok {
		t.Fatalf("DecodeFrame() concrete value type = %T, want protocol.UnknownPacket", packet.Value)
	}
}

func protocol47SessionLimits(t *testing.T) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatal(err)
	}
	return limits
}

func protocol47Session(t *testing.T, role protocol.Role, state protocol.State, limits protocol.Limits) protocol.Session {
	t.Helper()

	session, err := Protocol().NewSession(role, limits)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if err := session.ValidateState(state); err != nil {
		t.Fatalf("ValidateState(%q) error = %v", state, err)
	}
	session.SetState(state)
	return session
}

// readWire runs the complete inbound path for one frame, so the byte vectors
// below stay expressed as exactly what crosses the transport.
func readWire(t *testing.T, session protocol.Session, wire []byte) (protocol.Packet, error) {
	t.Helper()

	frame, err := session.Framer().ReadFrame(bytes.NewReader(wire))
	if err != nil {
		return protocol.Packet{}, err
	}
	return session.DecodeFrame(frame.Payload())
}

// writeWire runs the complete outbound path for one packet.
func writeWire(t *testing.T, session protocol.Session, packet protocol.Packet) ([]byte, error) {
	t.Helper()

	payload, err := session.EncodeFrame(packet)
	if err != nil {
		return nil, err
	}
	frame, err := session.Framer().BuildFrame(payload)
	if err != nil {
		return nil, err
	}
	var encoded bytes.Buffer
	if err := session.Framer().WriteFrame(&encoded, frame); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
