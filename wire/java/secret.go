package java

import (
	"crypto/rand"
	"fmt"
)

// SharedSecretBytes is the length of a Java Edition session key.
const SharedSecretBytes = 16

// SharedSecret is a Java Edition session key.
//
// Every formatting method redacts, so the key cannot reach a log line or an
// error by accident. Reveal is the only way to read the bytes, and calling it
// is a deliberate act that shows up in review. The field is an array rather
// than a slice so a copy of the value is a copy of the key, with no aliasing
// back to the caller's buffer.
type SharedSecret struct {
	key [SharedSecretBytes]byte
}

// redacted is what every formatting method renders.
const redacted = "java.SharedSecret(redacted)"

// NewSharedSecret draws a fresh session key from the system source.
func NewSharedSecret() (SharedSecret, error) {
	var secret SharedSecret
	if _, err := rand.Read(secret.key[:]); err != nil {
		return SharedSecret{}, fmt.Errorf("generate shared secret: %w", err)
	}

	return secret, nil
}

// SharedSecretFrom adopts key, which must be exactly SharedSecretBytes long.
// It copies key, so the caller may reuse the buffer.
func SharedSecretFrom(key []byte) (SharedSecret, error) {
	if len(key) != SharedSecretBytes {
		return SharedSecret{}, fmt.Errorf(
			"%w: length %d, want %d",
			ErrInvalidSharedSecret,
			len(key),
			SharedSecretBytes,
		)
	}

	var secret SharedSecret
	copy(secret.key[:], key)

	return secret, nil
}

// Reveal returns an independent copy of the key.
func (s SharedSecret) Reveal() []byte {
	key := make([]byte, SharedSecretBytes)
	copy(key, s.key[:])

	return key
}

// Len returns the key length in bytes.
func (SharedSecret) Len() int { return SharedSecretBytes }

// IsZero reports whether the secret was never populated.
func (s SharedSecret) IsZero() bool {
	return s.key == [SharedSecretBytes]byte{}
}

// String implements fmt.Stringer and redacts.
func (SharedSecret) String() string { return redacted }

// GoString implements fmt.GoStringer and redacts.
func (SharedSecret) GoString() string { return redacted }

// Format implements fmt.Formatter and redacts under every verb, including the
// numeric and hexadecimal verbs that would otherwise render the array.
func (SharedSecret) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte(redacted))
}

var (
	_ fmt.Stringer   = SharedSecret{}
	_ fmt.GoStringer = SharedSecret{}
	_ fmt.Formatter  = SharedSecret{}
)
