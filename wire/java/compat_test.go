package java

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

type protocol47Handshake struct {
	ProtocolVersion int32  `mc:"varint"`
	ServerAddress   string `mc:"string"`
	ServerPort      uint16 `mc:"u16"`
	NextState       int32  `mc:"varint"`
}

func (protocol47Handshake) PacketID() int32 { return 0x00 }

type protocol47Rest struct {
	ID   int32  `mc:"varint"`
	Data []byte `mc:"rest"`
}

func (protocol47Rest) PacketID() int32 { return 0xff }

type protocol47StatusRequest struct{}

func (protocol47StatusRequest) PacketID() int32 { return 0x00 }

type protocol47StatusPing struct {
	Time int64 `mc:"i64"`
}

func (protocol47StatusPing) PacketID() int32 { return 0x01 }

type protocol47LoginStart struct {
	Username string `mc:"string"`
}

func (protocol47LoginStart) PacketID() int32 { return 0x00 }

type protocol47PlayLogin struct {
	EntityID         int32  `mc:"i32"`
	GameMode         uint8  `mc:"u8"`
	Dimension        int8   `mc:"i8"`
	Difficulty       uint8  `mc:"u8"`
	MaxPlayers       uint8  `mc:"u8"`
	LevelType        string `mc:"string"`
	ReducedDebugInfo bool   `mc:"bool"`
}

func (protocol47PlayLogin) PacketID() int32 { return 0x01 }

type protocol47PlayChat struct {
	Message string `mc:"string"`
}

func (protocol47PlayChat) PacketID() int32 { return 0x01 }

func protocol47Limits(t *testing.T) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatal(err)
	}
	return limits
}

func TestProtocol47VarIntParity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value int32
		read  []byte
	}{
		{
			name:  "protocol version",
			value: 47,
			read:  []byte{0x2f},
		},
		{
			name:  "negative one",
			value: -1,
			read:  []byte{0xff, 0xff, 0xff, 0xff, 0x0f},
		},
		{
			name:  "maximum",
			value: math.MaxInt32,
			read:  []byte{0xff, 0xff, 0xff, 0xff, 0x07},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			value, read, err := ReadVarInt(bytes.NewReader(test.read))
			if err != nil {
				t.Fatalf("ReadVarInt() error = %v", err)
			}
			if value != test.value || read != len(test.read) {
				t.Errorf("ReadVarInt() = (%d, %d), want (%d, %d)", value, read, test.value, len(test.read))
			}

			var encoded bytes.Buffer
			written, err := WriteVarInt(&encoded, test.value)
			if err != nil {
				t.Fatalf("WriteVarInt() error = %v", err)
			}
			if got := encoded.Bytes(); written != len(test.read) || !bytes.Equal(got, test.read) {
				t.Errorf("WriteVarInt() = (%x, %d), want (%x, %d)", got, written, test.read, len(test.read))
			}
		})
	}
}

func TestProtocol47PositionParity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		x, y, z int
		read    []byte
	}{
		{
			name: "origin",
			read: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name: "negative coordinates",
			x:    -100,
			z:    -200,
			read: []byte{0xff, 0xff, 0xe7, 0x00, 0x03, 0xff, 0xff, 0x38},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			x, y, z, err := ReadPosition(bytes.NewReader(test.read))
			if err != nil {
				t.Fatalf("ReadPosition() error = %v", err)
			}
			if x != test.x || y != test.y || z != test.z {
				t.Errorf("ReadPosition() = (%d, %d, %d), want (%d, %d, %d)", x, y, z, test.x, test.y, test.z)
			}

			var encoded bytes.Buffer
			written, err := WritePosition(&encoded, test.x, test.y, test.z)
			if err != nil {
				t.Fatalf("WritePosition() error = %v", err)
			}
			if got := encoded.Bytes(); written != len(test.read) || !bytes.Equal(got, test.read) {
				t.Errorf("WritePosition() = (%x, %d), want (%x, %d)", got, written, test.read, len(test.read))
			}
		})
	}
}

func TestProtocol47RawPacketParity(t *testing.T) {
	t.Parallel()

	limits := protocol47Limits(t)
	tests := []struct {
		name string
		read []byte
		want protocol.Packet
	}{
		{
			name: "handshake",
			read: []byte{0x0f, 0x00, 0x2f, 0x09, 0x6c, 0x6f, 0x63, 0x61, 0x6c, 0x68, 0x6f, 0x73, 0x74, 0x63, 0xdd, 0x02},
			want: protocol.Packet{
				ID:      0x00,
				Payload: []byte{0x2f, 0x09, 0x6c, 0x6f, 0x63, 0x61, 0x6c, 0x68, 0x6f, 0x73, 0x74, 0x63, 0xdd, 0x02},
			},
		},
		{
			name: "rest payload",
			read: []byte{0x07, 0xff, 0x01, 0x05, 0xde, 0xad, 0xbe, 0xef},
			want: protocol.Packet{
				ID:      0xff,
				Payload: []byte{0x05, 0xde, 0xad, 0xbe, 0xef},
			},
		},
		{
			name: "status request",
			read: []byte{0x01, 0x00},
			want: protocol.Packet{ID: 0x00, Payload: []byte{}},
		},
		{
			name: "status ping",
			read: []byte{0x09, 0x01, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			want: protocol.Packet{ID: 0x01, Payload: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}},
		},
		{
			name: "login start",
			read: []byte{0x06, 0x00, 0x04, 'A', 'l', 'e', 'x'},
			want: protocol.Packet{ID: 0x00, Payload: []byte{0x04, 'A', 'l', 'e', 'x'}},
		},
		{
			name: "play clientbound login",
			read: []byte{0x0f, 0x01, 0x00, 0x00, 0x00, 0x2a, 0x01, 0x00, 0x01, 0x14, 0x04, 'f', 'l', 'a', 't', 0x00},
			want: protocol.Packet{
				ID:      0x01,
				Payload: []byte{0x00, 0x00, 0x00, 0x2a, 0x01, 0x00, 0x01, 0x14, 0x04, 'f', 'l', 'a', 't', 0x00},
			},
		},
		{
			name: "play serverbound chat",
			read: []byte{0x07, 0x01, 0x05, 'h', 'e', 'l', 'l', 'o'},
			want: protocol.Packet{ID: 0x01, Payload: []byte{0x05, 'h', 'e', 'l', 'l', 'o'}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ReadRawPacket(bytes.NewReader(test.read), limits)
			if err != nil {
				t.Fatalf("ReadRawPacket() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("ReadRawPacket() = %+v, want %+v", got, test.want)
			}

			var encoded bytes.Buffer
			if err := WriteRawPacket(&encoded, limits, test.want); err != nil {
				t.Fatalf("WriteRawPacket() error = %v", err)
			}
			if got := encoded.Bytes(); !bytes.Equal(got, test.read) {
				t.Errorf("WriteRawPacket() = %x, want %x", got, test.read)
			}
		})
	}
}

func TestProtocol47TaggedPacketParity(t *testing.T) {
	t.Parallel()

	limits := protocol47Limits(t)
	tests := []struct {
		name    string
		read    []byte
		payload []byte
		want    PacketValue
		new     func() PacketValue
	}{
		{
			name:    "handshake",
			read:    []byte{0x0f, 0x00, 0x2f, 0x09, 0x6c, 0x6f, 0x63, 0x61, 0x6c, 0x68, 0x6f, 0x73, 0x74, 0x63, 0xdd, 0x02},
			payload: []byte{0x2f, 0x09, 0x6c, 0x6f, 0x63, 0x61, 0x6c, 0x68, 0x6f, 0x73, 0x74, 0x63, 0xdd, 0x02},
			want: &protocol47Handshake{
				ProtocolVersion: 47,
				ServerAddress:   "localhost",
				ServerPort:      25565,
				NextState:       2,
			},
			new: func() PacketValue { return &protocol47Handshake{} },
		},
		{
			name:    "rest payload",
			read:    []byte{0x07, 0xff, 0x01, 0x05, 0xde, 0xad, 0xbe, 0xef},
			payload: []byte{0x05, 0xde, 0xad, 0xbe, 0xef},
			want: &protocol47Rest{
				ID:   5,
				Data: []byte{0xde, 0xad, 0xbe, 0xef},
			},
			new: func() PacketValue { return &protocol47Rest{} },
		},
		{
			name:    "status request",
			read:    []byte{0x01, 0x00},
			payload: []byte{},
			want:    &protocol47StatusRequest{},
			new:     func() PacketValue { return &protocol47StatusRequest{} },
		},
		{
			name:    "status ping",
			read:    []byte{0x09, 0x01, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			payload: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			want:    &protocol47StatusPing{Time: 0x0102030405060708},
			new:     func() PacketValue { return &protocol47StatusPing{} },
		},
		{
			name:    "login start",
			read:    []byte{0x06, 0x00, 0x04, 'A', 'l', 'e', 'x'},
			payload: []byte{0x04, 'A', 'l', 'e', 'x'},
			want:    &protocol47LoginStart{Username: "Alex"},
			new:     func() PacketValue { return &protocol47LoginStart{} },
		},
		{
			name:    "play clientbound login",
			read:    []byte{0x0f, 0x01, 0x00, 0x00, 0x00, 0x2a, 0x01, 0x00, 0x01, 0x14, 0x04, 'f', 'l', 'a', 't', 0x00},
			payload: []byte{0x00, 0x00, 0x00, 0x2a, 0x01, 0x00, 0x01, 0x14, 0x04, 'f', 'l', 'a', 't', 0x00},
			want: &protocol47PlayLogin{
				EntityID:         42,
				GameMode:         1,
				Dimension:        0,
				Difficulty:       1,
				MaxPlayers:       20,
				LevelType:        "flat",
				ReducedDebugInfo: false,
			},
			new: func() PacketValue { return &protocol47PlayLogin{} },
		},
		{
			name:    "play serverbound chat",
			read:    []byte{0x07, 0x01, 0x05, 'h', 'e', 'l', 'l', 'o'},
			payload: []byte{0x05, 'h', 'e', 'l', 'l', 'o'},
			want:    &protocol47PlayChat{Message: "hello"},
			new:     func() PacketValue { return &protocol47PlayChat{} },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			payload, err := Marshal(test.want, limits)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if !bytes.Equal(payload, test.payload) {
				t.Errorf("Marshal() = %x, want %x", payload, test.payload)
			}

			decodedPayload := test.new()
			if err := Unmarshal(test.payload, decodedPayload, limits); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if !reflect.DeepEqual(decodedPayload, test.want) {
				t.Errorf("Unmarshal() = %+v, want %+v", decodedPayload, test.want)
			}

			var encoded bytes.Buffer
			if err := WritePacket(&encoded, limits, test.want); err != nil {
				t.Fatalf("WritePacket() error = %v", err)
			}
			if got := encoded.Bytes(); !bytes.Equal(got, test.read) {
				t.Errorf("WritePacket() = %x, want %x", got, test.read)
			}

			decodedPacket := test.new()
			if err := ReadPacket(bytes.NewReader(test.read), limits, decodedPacket); err != nil {
				t.Fatalf("ReadPacket() error = %v", err)
			}
			if !reflect.DeepEqual(decodedPacket, test.want) {
				t.Errorf("ReadPacket() = %+v, want %+v", decodedPacket, test.want)
			}
		})
	}
}
