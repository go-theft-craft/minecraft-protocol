package java

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewSharedSecretIsSixteenBytes(t *testing.T) {
	secret, err := NewSharedSecret()
	if err != nil {
		t.Fatalf("NewSharedSecret: %v", err)
	}
	if secret.Len() != 16 {
		t.Fatalf("length %d, want 16", secret.Len())
	}
	if secret.IsZero() {
		t.Fatal("a generated secret must not be zero")
	}
}

func TestSharedSecretRevealsAnIndependentCopy(t *testing.T) {
	secret, err := SharedSecretFrom(make([]byte, 16))
	if err != nil {
		t.Fatalf("SharedSecretFrom: %v", err)
	}

	revealed := secret.Reveal()
	revealed[0] = 0xff
	if secret.Reveal()[0] != 0x00 {
		t.Fatal("Reveal must return a copy, not the stored bytes")
	}
}

func TestSharedSecretRejectsWrongLength(t *testing.T) {
	if _, err := SharedSecretFrom(make([]byte, 8)); !errors.Is(err, ErrInvalidSharedSecret) {
		t.Fatalf("error = %v, want ErrInvalidSharedSecret", err)
	}
}

// TestSharedSecretRedactsEveryFormatting is the test that matters. A secret
// that reaches a log through any verb is the failure this type exists to
// prevent.
func TestSharedSecretRedactsEveryFormatting(t *testing.T) {
	raw := []byte("0123456789abcdef")
	secret, err := SharedSecretFrom(raw)
	if err != nil {
		t.Fatalf("SharedSecretFrom: %v", err)
	}

	rendered := []string{
		secret.String(),
		secret.GoString(),
		fmt.Sprintf("%v", secret),
		fmt.Sprintf("%s", secret),
		fmt.Sprintf("%q", secret),
		fmt.Sprintf("%d", secret),
		fmt.Sprintf("%x", secret),
		fmt.Sprintf("%X", secret),
		fmt.Sprintf("%#v", secret),
		fmt.Sprintf("%+v", secret),
		fmt.Sprintf("%v", struct{ Secret SharedSecret }{secret}),
		fmt.Sprintf("%v", &secret),
		fmt.Errorf("wrapped: %w", fmt.Errorf("secret %v", secret)).Error(),
	}

	for index, text := range rendered {
		if strings.Contains(text, "0123456789abcdef") {
			t.Fatalf("rendering %d leaked the secret: %s", index, text)
		}
		if !strings.Contains(text, "redacted") {
			t.Fatalf("rendering %d is not marked redacted: %s", index, text)
		}
	}
}
