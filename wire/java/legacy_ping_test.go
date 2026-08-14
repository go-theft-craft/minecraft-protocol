package java

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/go-theft-craft/minecraft-protocol"
)

func testStatus() LegacyStatus {
	return LegacyStatus{
		ProtocolVersion: 47,
		Version:         "1.8.9",
		MOTD:            "A Minecraft Server",
		OnlinePlayers:   3,
		MaxPlayers:      20,
	}
}

func newTestLegacyHook(t *testing.T, handler LegacyStatusHandler) protocol.PreFrameHook {
	t.Helper()

	hook, err := NewLegacyPingHook(handler)
	if err != nil {
		t.Fatalf("NewLegacyPingHook() error = %v", err)
	}
	return hook
}

func TestNewLegacyPingHookRejectsNilHandler(t *testing.T) {
	t.Parallel()

	if _, err := NewLegacyPingHook(nil); !errors.Is(err, ErrInvalidLegacyStatus) {
		t.Fatalf("NewLegacyPingHook(nil) error = %v, want ErrInvalidLegacyStatus", err)
	}
}

func TestLegacyPingHookClaimsRequest(t *testing.T) {
	t.Parallel()

	hook := newTestLegacyHook(t, func(context.Context, LegacyPing) (LegacyStatus, error) {
		return testStatus(), nil
	})

	reader := bufio.NewReader(bytes.NewReader([]byte{0xfe, 0x01}))
	var response bytes.Buffer

	claimed, err := hook.HandlePreFrame(context.Background(), reader, &response)
	if err != nil {
		t.Fatalf("HandlePreFrame() error = %v", err)
	}
	if !claimed {
		t.Fatal("HandlePreFrame() claimed = false, want true")
	}
	if reader.Buffered() != 0 {
		t.Fatalf("HandlePreFrame() left %d buffered bytes after claiming", reader.Buffered())
	}

	want, err := EncodeLegacyStatus(testStatus())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response.Bytes(), want) {
		t.Fatalf("response = %x, want %x", response.Bytes(), want)
	}
}

func TestLegacyPingHookDeclinesWithoutConsuming(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"modern handshake":     {0x0f, 0x00, 0x2f},
		"FE with wrong second": {0xfe, 0x02, 0x03},
		"EOF after FE":         {0xfe},
		"no bytes at all":      nil,
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			hook := newTestLegacyHook(t, func(context.Context, LegacyPing) (LegacyStatus, error) {
				t.Error("handler ran for a declined connection")
				return LegacyStatus{}, nil
			})

			reader := bufio.NewReader(bytes.NewReader(input))
			var response bytes.Buffer

			claimed, err := hook.HandlePreFrame(context.Background(), reader, &response)
			if err != nil {
				t.Fatalf("HandlePreFrame() error = %v", err)
			}
			if claimed {
				t.Fatal("HandlePreFrame() claimed = true, want false")
			}
			if response.Len() != 0 {
				t.Fatalf("HandlePreFrame() wrote %d bytes while declining", response.Len())
			}

			// Every inspected byte must still be available to the framer.
			remaining, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(remaining, input) {
				t.Fatalf("remaining bytes = %x, want %x", remaining, input)
			}
		})
	}
}

func TestLegacyPingHookOneByteReader(t *testing.T) {
	t.Parallel()

	hook := newTestLegacyHook(t, func(context.Context, LegacyPing) (LegacyStatus, error) {
		return testStatus(), nil
	})

	reader := bufio.NewReader(oneByteReader{Reader: bytes.NewReader([]byte{0xfe, 0x01})})
	var response bytes.Buffer

	claimed, err := hook.HandlePreFrame(context.Background(), reader, &response)
	if err != nil {
		t.Fatalf("HandlePreFrame() error = %v", err)
	}
	if !claimed {
		t.Fatal("HandlePreFrame() claimed = false, want true")
	}
	if response.Len() == 0 {
		t.Fatal("HandlePreFrame() wrote no response")
	}
}

func TestLegacyPingHookOneByteWriter(t *testing.T) {
	t.Parallel()

	hook := newTestLegacyHook(t, func(context.Context, LegacyPing) (LegacyStatus, error) {
		return testStatus(), nil
	})

	reader := bufio.NewReader(bytes.NewReader([]byte{0xfe, 0x01}))
	var writer oneByteWriter

	if _, err := hook.HandlePreFrame(context.Background(), reader, &writer); err != nil {
		t.Fatalf("HandlePreFrame() error = %v", err)
	}

	want, err := EncodeLegacyStatus(testStatus())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(writer.Bytes(), want) {
		t.Fatalf("response = %x, want %x", writer.Bytes(), want)
	}
}

func TestLegacyPingHookReportsHandlerFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("no status available")
	hook := newTestLegacyHook(t, func(context.Context, LegacyPing) (LegacyStatus, error) {
		return LegacyStatus{}, sentinel
	})

	reader := bufio.NewReader(bytes.NewReader([]byte{0xfe, 0x01}))
	var response bytes.Buffer

	claimed, err := hook.HandlePreFrame(context.Background(), reader, &response)
	if !errors.Is(err, sentinel) {
		t.Fatalf("HandlePreFrame() error = %v, want the handler error", err)
	}
	if !claimed {
		t.Fatal("HandlePreFrame() claimed = false after consuming the request")
	}
	if response.Len() != 0 {
		t.Fatalf("HandlePreFrame() wrote %d bytes despite the handler failing", response.Len())
	}
}

func TestLegacyPingHookReportsWriteFailure(t *testing.T) {
	t.Parallel()

	hook := newTestLegacyHook(t, func(context.Context, LegacyPing) (LegacyStatus, error) {
		return testStatus(), nil
	})

	reader := bufio.NewReader(bytes.NewReader([]byte{0xfe, 0x01}))
	if _, err := hook.HandlePreFrame(context.Background(), reader, shortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("HandlePreFrame() error = %v, want io.ErrShortWrite", err)
	}
}

func TestEncodeLegacyStatusBytes(t *testing.T) {
	t.Parallel()

	response, err := EncodeLegacyStatus(testStatus())
	if err != nil {
		t.Fatalf("EncodeLegacyStatus() error = %v", err)
	}

	if response[0] != 0xff {
		t.Fatalf("packet ID = %#x, want 0xff", response[0])
	}
	units := binary.BigEndian.Uint16(response[1:3])
	if int(units)*2+3 != len(response) {
		t.Fatalf("declared %d units but response is %d bytes", units, len(response))
	}

	decoded := make([]uint16, units)
	for index := range decoded {
		decoded[index] = binary.BigEndian.Uint16(response[3+2*index:])
	}
	text := string(utf16.Decode(decoded))

	fields := strings.Split(text, "\x00")
	want := []string{"§1", "47", "1.8.9", "A Minecraft Server", "3", "20"}
	if len(fields) != len(want) {
		t.Fatalf("fields = %q, want %d fields", fields, len(want))
	}
	for index := range want {
		if fields[index] != want[index] {
			t.Errorf("field %d = %q, want %q", index, fields[index], want[index])
		}
	}
}

func TestEncodeLegacyStatusRejectsUnrepresentableResponses(t *testing.T) {
	t.Parallel()

	t.Run("NUL in a field", func(t *testing.T) {
		t.Parallel()

		status := testStatus()
		status.MOTD = "before\x00after"
		if _, err := EncodeLegacyStatus(status); !errors.Is(err, ErrInvalidLegacyStatus) {
			t.Fatalf("EncodeLegacyStatus() error = %v, want ErrInvalidLegacyStatus", err)
		}
	})

	t.Run("response above the length field", func(t *testing.T) {
		t.Parallel()

		status := testStatus()
		status.MOTD = strings.Repeat("x", 1<<16)
		if _, err := EncodeLegacyStatus(status); !errors.Is(err, ErrInvalidLegacyStatus) {
			t.Fatalf("EncodeLegacyStatus() error = %v, want ErrInvalidLegacyStatus", err)
		}
	})
}

func TestEncodeLegacyStatusHandlesSupplementaryRunes(t *testing.T) {
	t.Parallel()

	status := testStatus()
	status.MOTD = "server 🧱"

	response, err := EncodeLegacyStatus(status)
	if err != nil {
		t.Fatalf("EncodeLegacyStatus() error = %v", err)
	}

	units := binary.BigEndian.Uint16(response[1:3])
	decoded := make([]uint16, units)
	for index := range decoded {
		decoded[index] = binary.BigEndian.Uint16(response[3+2*index:])
	}
	if !strings.Contains(string(utf16.Decode(decoded)), "🧱") {
		t.Fatal("EncodeLegacyStatus() lost a surrogate pair")
	}
}
