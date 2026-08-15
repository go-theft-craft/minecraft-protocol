package java

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// uuidDashPositions are the indices a dashed UUID puts its separators at.
var uuidDashPositions = [4]int{8, 13, 18, 23}

// ParseUUID reads the two forms a Java Edition login can carry: the dashed
// thirty-six character form that the login success packet sends, and the
// undashed thirty-two character form that the session server returns.
//
// It is deliberately strict. Braced and URN forms, surrounding whitespace, and
// partial dashing are rejected, because a login is the one exchange where the
// peer is entirely unauthenticated and a permissive parser there turns a
// malformed identity into a valid-looking one.
func ParseUUID(text string) (UUID, error) {
	var compact string

	switch len(text) {
	case 32:
		compact = text
	case 36:
		for _, position := range uuidDashPositions {
			if text[position] != '-' {
				return UUID{}, fmt.Errorf(
					"%w: expected a separator at index %d",
					ErrInvalidUUID,
					position,
				)
			}
		}
		compact = strings.ReplaceAll(text, "-", "")
		if len(compact) != 32 {
			return UUID{}, fmt.Errorf("%w: misplaced separators", ErrInvalidUUID)
		}
	default:
		return UUID{}, fmt.Errorf("%w: length %d, want 32 or 36", ErrInvalidUUID, len(text))
	}

	var value UUID
	if _, err := hex.Decode(value[:], []byte(strings.ToLower(compact))); err != nil {
		return UUID{}, fmt.Errorf("%w: %w", ErrInvalidUUID, err)
	}

	return value, nil
}

// String renders the dashed, lowercase form.
func (u UUID) String() string {
	encoded := hex.EncodeToString(u[:])

	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] +
		"-" + encoded[16:20] + "-" + encoded[20:32]
}

// IsZero reports whether the UUID is the nil UUID.
func (u UUID) IsZero() bool { return u == UUID{} }

// MaxUsernameBytes is the longest username the protocol allows.
const MaxUsernameBytes = 16

// Username is a validated Java Edition account name.
//
// The field is unexported so ParseUsername is the only way to build a
// non-zero one. A defined string type would be convertible, and
// Username("bad\nname") compiling anywhere would make the validation a
// convention rather than a guarantee. The struct is still comparable and
// still usable as a map key.
type Username struct {
	name string
}

// ParseUsername validates a peer-supplied account name.
//
// It enforces the rules that hold everywhere: non-empty, at most
// MaxUsernameBytes, valid UTF-8, and no control characters. It deliberately
// does not enforce the [a-zA-Z0-9_] charset that Mojang applies to new
// accounts, because offline-mode and modded servers legitimately issue names
// outside it and rejecting those breaks real connections while preventing
// nothing.
func ParseUsername(text string) (Username, error) {
	if text == "" {
		return Username{}, fmt.Errorf("%w: empty", ErrInvalidUsername)
	}
	if len(text) > MaxUsernameBytes {
		return Username{}, fmt.Errorf(
			"%w: %d bytes, limit %d",
			ErrInvalidUsername,
			len(text),
			MaxUsernameBytes,
		)
	}
	if !utf8.ValidString(text) {
		return Username{}, fmt.Errorf("%w: not valid UTF-8", ErrInvalidUsername)
	}
	for _, character := range text {
		if unicode.IsControl(character) {
			return Username{}, fmt.Errorf("%w: contains a control character", ErrInvalidUsername)
		}
	}

	return Username{name: text}, nil
}

// String returns the account name.
func (u Username) String() string { return u.name }

// IsZero reports whether the username was never parsed.
func (u Username) IsZero() bool { return u.name == "" }

// ServerHash is the login hash a client presents to the session server and a
// server verifies. ComputeServerHash is the only way to obtain one.
//
// It is a type rather than a string because of the signature it appears in:
// Verify takes a username and a hash, and as two adjacent strings, swapping
// them compiles, survives review, and fails at runtime as an authentication
// error that looks like a rejected account.
type ServerHash struct {
	hash string
}

// String returns the hash in the form the session server expects.
func (h ServerHash) String() string { return h.hash }

// IsZero reports whether the hash was never computed.
func (h ServerHash) IsZero() bool { return h.hash == "" }
