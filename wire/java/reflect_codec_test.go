package java

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

type testPacket struct {
	EntityID int32  `mc:"i32"`
	Name     string `mc:"string"`
	Grounded bool   `mc:"bool"`
}

func (testPacket) PacketID() int32 { return 0x01 }

type testVarIntPacket struct {
	ProtocolVersion int32  `mc:"varint"`
	ServerAddress   string `mc:"string"`
	ServerPort      uint16 `mc:"u16"`
	NextState       int32  `mc:"varint"`
}

func (testVarIntPacket) PacketID() int32 { return 0x00 }

type testRestPacket struct {
	ID   int32  `mc:"varint"`
	Data []byte `mc:"rest"`
}

func (testRestPacket) PacketID() int32 { return 0x7f }

type scalarPacket int

func (scalarPacket) PacketID() int32 { return 0x02 }

type unknownTagPacket struct {
	Value int32 `mc:"unknown"`
}

func (unknownTagPacket) PacketID() int32 { return 0x03 }

type marshalTypeMismatchPacket struct {
	Value string `mc:"i32"`
}

func (marshalTypeMismatchPacket) PacketID() int32 { return 0x04 }

type unmarshalTypeMismatchPacket struct {
	Value bool `mc:"i32"`
}

func (unmarshalTypeMismatchPacket) PacketID() int32 { return 0x05 }

type stringPacket struct {
	Value string `mc:"string"`
}

func (stringPacket) PacketID() int32 { return 0x06 }

type byteArrayPacket struct {
	Value []byte `mc:"bytearray"`
}

func (byteArrayPacket) PacketID() int32 { return 0x07 }

type invalidRestPacket struct {
	Data []byte `mc:"rest"`
	Tail uint8  `mc:"u8"`
}

func (invalidRestPacket) PacketID() int32 { return 0x08 }

type onlyRestPacket struct {
	Data []byte `mc:"rest"`
}

func (onlyRestPacket) PacketID() int32 { return 0x09 }

func TestMarshalRoundTrip(t *testing.T) {
	t.Parallel()

	limits := testLimits(t)
	original := &testPacket{EntityID: 42, Name: "Alex", Grounded: true}

	data, err := Marshal(original, limits)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	decoded := &testPacket{}
	if err := Unmarshal(data, decoded, limits); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if *decoded != *original {
		t.Errorf("round trip = %+v, want %+v", decoded, original)
	}
}

func TestMarshalVarInt(t *testing.T) {
	t.Parallel()

	limits := testLimits(t)
	original := &testVarIntPacket{
		ProtocolVersion: 47,
		ServerAddress:   "host",
		ServerPort:      25565,
		NextState:       2,
	}

	data, err := Marshal(original, limits)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	decoded := &testVarIntPacket{}
	if err := Unmarshal(data, decoded, limits); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if *decoded != *original {
		t.Errorf("round trip = %+v, want %+v", decoded, original)
	}
}

func TestMarshalRest(t *testing.T) {
	t.Parallel()

	limits := testLimits(t)
	original := &testRestPacket{ID: 5, Data: []byte{0xde, 0xad, 0xbe, 0xef}}

	data, err := Marshal(original, limits)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	decoded := &testRestPacket{}
	if err := Unmarshal(data, decoded, limits); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.ID != original.ID || !bytes.Equal(decoded.Data, original.Data) {
		t.Errorf("round trip = %+v, want %+v", decoded, original)
	}
}

func TestMarshalRejectsNonStruct(t *testing.T) {
	t.Parallel()

	_, err := Marshal(scalarPacket(1), testLimits(t))
	if err == nil || !strings.Contains(err.Error(), "expected struct") {
		t.Fatalf("Marshal() error = %v, want expected struct error", err)
	}
}

func TestMarshalRejectsNilPointer(t *testing.T) {
	t.Parallel()

	var value *testPacket
	_, err := Marshal(value, testLimits(t))
	if err == nil || !strings.Contains(err.Error(), "expected struct") {
		t.Fatalf("Marshal() error = %v, want expected struct error", err)
	}
}

func TestUnmarshalRejectsInvalidTargets(t *testing.T) {
	t.Parallel()

	limits := testLimits(t)
	var nilPacket *testPacket
	tests := []struct {
		name  string
		value PacketValue
	}{
		{name: "nil interface", value: nil},
		{name: "nil pointer", value: nilPacket},
		{name: "non pointer", value: testPacket{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Unmarshal(nil, test.value, limits)
			if err == nil {
				t.Fatal("Unmarshal() error = nil, want invalid target error")
			}
		})
	}
}

func TestMarshalRejectsUnknownTag(t *testing.T) {
	t.Parallel()

	_, err := Marshal(unknownTagPacket{}, testLimits(t))
	assertErrorContains(t, err, "Value", `tag "unknown"`, "unknown field tag")
}

func TestUnmarshalRejectsUnknownTag(t *testing.T) {
	t.Parallel()

	err := Unmarshal(nil, &unknownTagPacket{}, testLimits(t))
	assertErrorContains(t, err, "Value", `tag "unknown"`, "unknown field tag")
}

func TestMarshalRejectsTypeMismatch(t *testing.T) {
	t.Parallel()

	_, err := Marshal(marshalTypeMismatchPacket{Value: "wrong"}, testLimits(t))
	assertErrorContains(t, err, "Value", `tag "i32"`, "expected int32", "got string")
}

func TestUnmarshalRejectsTypeMismatch(t *testing.T) {
	t.Parallel()

	err := Unmarshal([]byte{0, 0, 0, 1}, &unmarshalTypeMismatchPacket{}, testLimits(t))
	assertErrorContains(t, err, "Value", `tag "i32"`, "expected bool", "got int32")
}

func TestMarshalEnforcesSelectedLimits(t *testing.T) {
	t.Parallel()

	limits := testLimits(t)
	tests := []struct {
		name  string
		value PacketValue
	}{
		{name: "string", value: stringPacket{Value: "five!"}},
		{name: "byte array", value: byteArrayPacket{Value: []byte{1, 2, 3, 4, 5}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Marshal(test.value, limits)
			if !errors.Is(err, ErrValueTooLarge) {
				t.Fatalf("Marshal() error = %v, want ErrValueTooLarge", err)
			}
		})
	}
}

func TestMarshalRejectsOversizedAggregatePayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		frameBytes int
		value      PacketValue
	}{
		{
			name:       "rest",
			frameBytes: 4,
			value:      onlyRestPacket{Data: []byte{1, 2, 3, 4}},
		},
		{
			name:       "multiple fields",
			frameBytes: 10,
			value:      testPacket{EntityID: 42, Name: "Alex", Grounded: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := frameLimits(t, test.frameBytes)
			_, err := Marshal(test.value, limits)
			if !errors.Is(err, ErrFrameTooLarge) {
				t.Fatalf("Marshal() error = %v, want ErrFrameTooLarge", err)
			}
		})
	}
}

func TestUnmarshalRejectsOversizedAggregatePayload(t *testing.T) {
	t.Parallel()

	t.Run("rest", func(t *testing.T) {
		decoded := &onlyRestPacket{Data: []byte{0xaa}}
		err := Unmarshal([]byte{1, 2, 3, 4}, decoded, frameLimits(t, 4))
		if !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("Unmarshal() error = %v, want ErrFrameTooLarge", err)
		}
		if got, want := decoded.Data, []byte{0xaa}; !bytes.Equal(got, want) {
			t.Errorf("Unmarshal() changed target to %x, want %x", got, want)
		}
	})

	t.Run("multiple fields", func(t *testing.T) {
		data := []byte{0, 0, 0, 42, 4, 'A', 'l', 'e', 'x', 1}
		decoded := &testPacket{EntityID: -1, Name: "keep"}
		err := Unmarshal(data, decoded, frameLimits(t, 10))
		if !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("Unmarshal() error = %v, want ErrFrameTooLarge", err)
		}
		if want := (&testPacket{EntityID: -1, Name: "keep"}); *decoded != *want {
			t.Errorf("Unmarshal() changed target to %+v, want %+v", decoded, want)
		}
	})
}

func TestUnmarshalRestOwnsBytes(t *testing.T) {
	t.Parallel()

	data := []byte{0x05, 0xde, 0xad, 0xbe, 0xef}
	decoded := &testRestPacket{}
	if err := Unmarshal(data, decoded, testLimits(t)); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	data[1] = 0
	if got, want := decoded.Data, []byte{0xde, 0xad, 0xbe, 0xef}; !bytes.Equal(got, want) {
		t.Errorf("decoded rest after input mutation = %x, want %x", got, want)
	}
}

func TestMarshalRejectsFieldAfterRest(t *testing.T) {
	t.Parallel()

	_, err := Marshal(invalidRestPacket{}, testLimits(t))
	assertErrorContains(t, err, "Data", `tag "rest"`, "must be last")
}

func TestUnmarshalRejectsFieldAfterRest(t *testing.T) {
	t.Parallel()

	err := Unmarshal(nil, &invalidRestPacket{}, testLimits(t))
	assertErrorContains(t, err, "Data", `tag "rest"`, "must be last")
}

func TestReadPacket(t *testing.T) {
	t.Parallel()

	limits := testLimits(t)
	want := &testPacket{EntityID: 42, Name: "Alex", Grounded: true}
	var wire bytes.Buffer
	if err := WritePacket(&wire, limits, want); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}

	got := &testPacket{}
	if err := ReadPacket(&wire, limits, got); err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	if *got != *want {
		t.Errorf("ReadPacket() = %+v, want %+v", got, want)
	}
}

func TestReadPacketRejectsMismatchedID(t *testing.T) {
	t.Parallel()

	limits := testLimits(t)
	payload, err := Marshal(testPacket{EntityID: 42, Name: "Alex", Grounded: true}, limits)
	if err != nil {
		t.Fatal(err)
	}

	var wire bytes.Buffer
	if err := WriteRawPacket(&wire, limits, protocol.Packet{ID: 0x02, Payload: payload}); err != nil {
		t.Fatal(err)
	}

	got := &testPacket{EntityID: -1, Name: "keep", Grounded: false}
	err = ReadPacket(&wire, limits, got)
	assertErrorContains(t, err, "expected packet 0x01", "got 0x02")
	if want := (&testPacket{EntityID: -1, Name: "keep", Grounded: false}); *got != *want {
		t.Errorf("ReadPacket() changed target to %+v after ID mismatch", got)
	}
}

func TestWritePacketRejectsShortWriter(t *testing.T) {
	t.Parallel()

	err := WritePacket(shortWriter{}, testLimits(t), testPacket{Name: "Alex"})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("WritePacket() error = %v, want io.ErrShortWrite", err)
	}
}

func TestWritePacketRejectsOversizedAggregateBeforeWrite(t *testing.T) {
	t.Parallel()

	writer := &countingWriter{}
	err := WritePacket(writer, frameLimits(t, 4), onlyRestPacket{Data: []byte{1, 2, 3, 4}})
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("WritePacket() error = %v, want ErrFrameTooLarge", err)
	}
	if writer.writes != 0 {
		t.Errorf("WritePacket() wrote %d times before rejecting the payload", writer.writes)
	}
}

func assertErrorContains(t *testing.T, err error, fragments ...string) {
	t.Helper()

	if err == nil {
		t.Fatal("error = nil, want error")
	}
	for _, fragment := range fragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q does not contain %q", err, fragment)
		}
	}
}
