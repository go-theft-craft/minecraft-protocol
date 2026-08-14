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

	limits := protocol47CodecLimits(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			decoderRole, encoderRole := protocol.RoleClient, protocol.RoleServer
			if test.direction == protocol.DirectionServerbound {
				decoderRole, encoderRole = protocol.RoleServer, protocol.RoleClient
			}
			decoder := protocol47Codec(t, decoderRole, test.state, limits)
			encoder := protocol47Codec(t, encoderRole, test.state, limits)

			packet, err := decoder.Read(bytes.NewReader(test.wire))
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			if packet.State != test.state || packet.Direction != test.direction || packet.ID != test.id || packet.Name != test.packet {
				t.Fatalf("Read() envelope = {%q %d %#x %q}, want {%q %d %#x %q}", packet.State, packet.Direction, packet.ID, packet.Name, test.state, test.direction, test.id, test.packet)
			}
			if !bytes.Equal(packet.Payload, test.payload) {
				t.Fatalf("Read() payload = %x, want %x", packet.Payload, test.payload)
			}
			if gotType, wantType := reflect.TypeOf(packet.Value), reflect.TypeOf(test.value); gotType != wantType {
				t.Fatalf("Read() concrete value type = %v, want %v", gotType, wantType)
			}
			if !reflect.DeepEqual(packet.Value, test.value) {
				t.Fatalf("Read() value = %#v, want %#v", packet.Value, test.value)
			}

			var encoded bytes.Buffer
			if err := encoder.Write(&encoded, packet); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			if got := encoded.Bytes(); !bytes.Equal(got, test.wire) {
				t.Fatalf("Write() = %x, want %x", got, test.wire)
			}
		})
	}
}

func TestProtocol47UnknownPacketOwnershipAndReencoding(t *testing.T) {
	t.Parallel()

	limits := protocol47CodecLimits(t)
	decoder := protocol47Codec(t, protocol.RoleClient, StateStatus, limits)
	encoder := protocol47Codec(t, protocol.RoleServer, StateStatus, limits)
	wire := []byte{0x04, 0x7f, 0x01, 0x02, 0x03}

	packet, err := decoder.Read(bytes.NewReader(wire))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	unknown, ok := packet.Value.(protocol.UnknownPacket)
	if !ok {
		t.Fatalf("Read() concrete value type = %T, want protocol.UnknownPacket", packet.Value)
	}
	if packet.State != StateStatus || packet.Direction != protocol.DirectionClientbound || packet.ID != 0x7f || packet.Name != "" {
		t.Fatalf("Read() envelope = {%q %d %#x %q}", packet.State, packet.Direction, packet.ID, packet.Name)
	}
	if !bytes.Equal(packet.Payload, []byte{0x01, 0x02, 0x03}) || !bytes.Equal(unknown.Payload, []byte{0x01, 0x02, 0x03}) {
		t.Fatalf("Read() payloads = %x and %x", packet.Payload, unknown.Payload)
	}

	var encoded bytes.Buffer
	if err := encoder.Write(&encoded, packet); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got := encoded.Bytes(); !bytes.Equal(got, wire) {
		t.Fatalf("Write() = %x, want %x", got, wire)
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

	limits := protocol47CodecLimits(t)
	decoder := protocol47Codec(t, protocol.RoleServer, StateHandshaking, limits)
	wire := []byte{0x03, 0xfe, 0x01, 0x01}

	packet, err := decoder.Read(bytes.NewReader(wire))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if packet.ID != 0xfe || packet.Name != "" {
		t.Fatalf("Read() envelope = {%#x %q}, want unknown packet ID 0xfe", packet.ID, packet.Name)
	}
	if _, ok := packet.Value.(protocol.UnknownPacket); !ok {
		t.Fatalf("Read() concrete value type = %T, want protocol.UnknownPacket", packet.Value)
	}
}

func protocol47CodecLimits(t *testing.T) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatal(err)
	}
	return limits
}

func protocol47Codec(t *testing.T, role protocol.Role, state protocol.State, limits protocol.Limits) protocol.Codec {
	t.Helper()

	codec, err := Protocol().NewCodec(role, limits)
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	if err := codec.SetState(state); err != nil {
		t.Fatalf("SetState(%q) error = %v", state, err)
	}
	return codec
}
