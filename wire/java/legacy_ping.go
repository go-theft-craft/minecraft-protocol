package java

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/go-theft-craft/minecraft-protocol"
)

const (
	legacyPingFirstByte  byte = 0xfe
	legacyPingSecondByte byte = 0x01
	legacyKickPacketID   byte = 0xff
	// legacyStatusSeparator separates the fields of a legacy status response.
	legacyStatusSeparator = "\x00"
	// legacyStatusMarker introduces the protocol 47 response layout.
	legacyStatusMarker = "§" + "1"
)

// ErrInvalidLegacyStatus reports a legacy status response that cannot be
// represented on the wire.
var ErrInvalidLegacyStatus = errors.New("invalid legacy status response")

// LegacyPing is the request a legacy client sends. The `FE 01` ping carries no
// fields of its own, so this type exists to give handlers a stable signature.
type LegacyPing struct{}

// LegacyStatus is the response to a legacy `FE 01` ping.
type LegacyStatus struct {
	ProtocolVersion int32
	Version         string
	MOTD            string
	OnlinePlayers   int
	MaxPlayers      int
}

// LegacyStatusHandler supplies the response for one legacy ping.
type LegacyStatusHandler func(context.Context, LegacyPing) (LegacyStatus, error)

type legacyPingHook struct {
	handler LegacyStatusHandler
}

var _ protocol.PreFrameHook = legacyPingHook{}

// NewLegacyPingHook returns an opt-in hook that answers the legacy `FE 01`
// server-list ping. It declines every other connection without consuming a
// byte, so normal framing continues untouched.
func NewLegacyPingHook(handler LegacyStatusHandler) (protocol.PreFrameHook, error) {
	if handler == nil {
		return nil, fmt.Errorf("%w: nil status handler", ErrInvalidLegacyStatus)
	}

	return legacyPingHook{handler: handler}, nil
}

// HandlePreFrame implements protocol.PreFrameHook.
func (h legacyPingHook) HandlePreFrame(
	ctx context.Context,
	reader *bufio.Reader,
	writer io.Writer,
) (bool, error) {
	if !isLegacyPingRequest(reader) {
		return false, nil
	}

	if _, err := reader.Discard(2); err != nil {
		return true, fmt.Errorf("consume legacy ping request: %w", err)
	}

	status, err := h.handler(ctx, LegacyPing{})
	if err != nil {
		return true, fmt.Errorf("build legacy status response: %w", err)
	}

	response, err := EncodeLegacyStatus(status)
	if err != nil {
		return true, err
	}
	if _, err := writeFull(writer, response); err != nil {
		return true, fmt.Errorf("write legacy status response: %w", err)
	}

	return true, nil
}

// isLegacyPingRequest reports whether the next two buffered bytes are a legacy
// ping. Peek never consumes, so a false answer leaves the bytes exactly as the
// framer expects to find them.
//
// A peek failure is not this hook's problem to report: too few bytes, a closed
// connection, or a transport error all mean "not a legacy ping", and the read
// pump surfaces the same error when it tries to frame.
func isLegacyPingRequest(reader *bufio.Reader) bool {
	prefix, err := reader.Peek(2)
	if err != nil {
		return false
	}

	return prefix[0] == legacyPingFirstByte && prefix[1] == legacyPingSecondByte
}

// EncodeLegacyStatus encodes one protocol 47 legacy status response, including
// its `FF` kick packet ID and UTF-16BE payload.
func EncodeLegacyStatus(status LegacyStatus) ([]byte, error) {
	fields := []string{
		legacyStatusMarker,
		strconv.FormatInt(int64(status.ProtocolVersion), 10),
		status.Version,
		status.MOTD,
		strconv.Itoa(status.OnlinePlayers),
		strconv.Itoa(status.MaxPlayers),
	}
	for _, field := range fields {
		// A NUL inside a field would split the response into different fields
		// than the server intended.
		if strings.Contains(field, legacyStatusSeparator) {
			return nil, fmt.Errorf("%w: field contains a NUL separator", ErrInvalidLegacyStatus)
		}
	}

	units := utf16.Encode([]rune(strings.Join(fields, legacyStatusSeparator)))
	if len(units) > math.MaxUint16 {
		return nil, fmt.Errorf(
			"%w: response of %d UTF-16 units exceeds %d",
			ErrInvalidLegacyStatus,
			len(units),
			math.MaxUint16,
		)
	}

	response := make([]byte, 3+2*len(units))
	response[0] = legacyKickPacketID
	binary.BigEndian.PutUint16(response[1:3], uint16(len(units)))
	for index, unit := range units {
		binary.BigEndian.PutUint16(response[3+2*index:], unit)
	}

	return response, nil
}
