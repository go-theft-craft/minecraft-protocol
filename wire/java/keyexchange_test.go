package java

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// serverKey generates a key pair for one test. A key is never loaded from
// disk, so no fixture in this repository can contain private material.
func serverKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return key
}

func TestDecryptSharedSecretRecoversTheClientKey(t *testing.T) {
	key := serverKey(t)

	// The client half from M2 produces the ciphertext this function reads.
	sent, err := NewSharedSecret()
	if err != nil {
		t.Fatalf("NewSharedSecret: %v", err)
	}
	ciphertext, err := EncryptToServerKey(&key.PublicKey, sent.Reveal())
	if err != nil {
		t.Fatalf("EncryptToServerKey: %v", err)
	}

	got, err := DecryptSharedSecret(key, ciphertext)
	if err != nil {
		t.Fatalf("DecryptSharedSecret: %v", err)
	}
	if !bytes.Equal(got.Reveal(), sent.Reveal()) {
		t.Fatal("DecryptSharedSecret recovered a different key")
	}
	if got.Len() != SharedSecretBytes {
		t.Fatalf("Len = %d, want %d", got.Len(), SharedSecretBytes)
	}
}

func TestDecryptSharedSecretRejectsTheWrongLength(t *testing.T) {
	key := serverKey(t)

	for _, length := range []int{0, 15, 17, 32} {
		ciphertext, err := EncryptToServerKey(&key.PublicKey, make([]byte, length))
		if err != nil {
			t.Fatalf("EncryptToServerKey: %v", err)
		}

		secret, err := DecryptSharedSecret(key, ciphertext)
		if !errors.Is(err, ErrInvalidSharedSecret) {
			t.Fatalf("length %d: error = %v, want ErrInvalidSharedSecret", length, err)
		}
		if !secret.IsZero() {
			t.Fatalf("length %d: a rejected secret must stay zero", length)
		}
	}
}

func TestDecryptSharedSecretRejectsAnotherKeysCiphertext(t *testing.T) {
	key := serverKey(t)
	other := serverKey(t)

	ciphertext, err := EncryptToServerKey(&other.PublicKey, make([]byte, SharedSecretBytes))
	if err != nil {
		t.Fatalf("EncryptToServerKey: %v", err)
	}

	if _, err := DecryptSharedSecret(key, ciphertext); err == nil {
		t.Fatal("DecryptSharedSecret accepted ciphertext encrypted under another key")
	}
}

func TestDecryptSharedSecretRejectsANilKey(t *testing.T) {
	if _, err := DecryptSharedSecret(nil, []byte{1}); !errors.Is(err, ErrInvalidServerKey) {
		t.Fatalf("error = %v, want ErrInvalidServerKey", err)
	}
}

func TestVerifyTokenAcceptsTheReturnedToken(t *testing.T) {
	key := serverKey(t)

	expected := []byte{0x01, 0x02, 0x03, 0x04}
	encrypted, err := EncryptToServerKey(&key.PublicKey, expected)
	if err != nil {
		t.Fatalf("EncryptToServerKey: %v", err)
	}

	if err := VerifyToken(key, expected, encrypted); err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
}

func TestVerifyTokenRejectsAOneByteDifference(t *testing.T) {
	key := serverKey(t)

	expected := []byte{0x01, 0x02, 0x03, 0x04}
	returned := []byte{0x01, 0x02, 0x03, 0x05}
	encrypted, err := EncryptToServerKey(&key.PublicKey, returned)
	if err != nil {
		t.Fatalf("EncryptToServerKey: %v", err)
	}

	if err := VerifyToken(key, expected, encrypted); !errors.Is(err, ErrVerifyTokenMismatch) {
		t.Fatalf("error = %v, want ErrVerifyTokenMismatch", err)
	}
}

func TestVerifyTokenRejectsTheWrongLength(t *testing.T) {
	key := serverKey(t)

	expected := []byte{0x01, 0x02, 0x03, 0x04}

	// A token that is a prefix of the expected one, and one that extends it.
	// Both must fail with the same error as a differing token, so the length
	// is not an oracle that a mismatch is not.
	for _, returned := range [][]byte{{0x01, 0x02, 0x03}, {0x01, 0x02, 0x03, 0x04, 0x05}, {}} {
		encrypted, err := EncryptToServerKey(&key.PublicKey, returned)
		if err != nil {
			t.Fatalf("EncryptToServerKey: %v", err)
		}

		if err := VerifyToken(key, expected, encrypted); !errors.Is(err, ErrVerifyTokenMismatch) {
			t.Fatalf("length %d: error = %v, want ErrVerifyTokenMismatch", len(returned), err)
		}
	}
}

func TestVerifyTokenRejectsAnEmptyExpectedToken(t *testing.T) {
	key := serverKey(t)

	encrypted, err := EncryptToServerKey(&key.PublicKey, []byte{0x01})
	if err != nil {
		t.Fatalf("EncryptToServerKey: %v", err)
	}

	if err := VerifyToken(key, nil, encrypted); !errors.Is(err, ErrVerifyTokenMismatch) {
		t.Fatalf("error = %v, want ErrVerifyTokenMismatch", err)
	}
}

func TestVerifyTokenRejectsUndecryptableCiphertext(t *testing.T) {
	key := serverKey(t)
	other := serverKey(t)

	encrypted, err := EncryptToServerKey(&other.PublicKey, []byte{0x01})
	if err != nil {
		t.Fatalf("EncryptToServerKey: %v", err)
	}

	if err := VerifyToken(key, []byte{0x01}, encrypted); err == nil {
		t.Fatal("VerifyToken accepted ciphertext encrypted under another key")
	}
	if err := VerifyToken(nil, []byte{0x01}, encrypted); !errors.Is(err, ErrInvalidServerKey) {
		t.Fatalf("error = %v, want ErrInvalidServerKey", err)
	}
}

// The comparison has to be constant time, and a constant-time comparison is
// not observable from its results. The source is the only place the property
// is visible, so the test reads it.
func TestVerifyTokenComparesInConstantTime(t *testing.T) {
	source, err := os.ReadFile("keyexchange.go")
	if err != nil {
		t.Fatalf("read keyexchange.go: %v", err)
	}
	if !strings.Contains(string(source), "subtle.ConstantTimeCompare") {
		t.Fatal("VerifyToken must compare with crypto/subtle")
	}
	if strings.Contains(string(source), "bytes.Equal") {
		t.Fatal("keyexchange.go must not compare secret material with bytes.Equal")
	}
}

// The server half must redact what the client half redacts. A secret that
// prints itself here reaches a server log the moment a login fails.
func TestDecryptedSharedSecretStillRedacts(t *testing.T) {
	key := serverKey(t)

	sent, err := NewSharedSecret()
	if err != nil {
		t.Fatalf("NewSharedSecret: %v", err)
	}
	ciphertext, err := EncryptToServerKey(&key.PublicKey, sent.Reveal())
	if err != nil {
		t.Fatalf("EncryptToServerKey: %v", err)
	}
	secret, err := DecryptSharedSecret(key, ciphertext)
	if err != nil {
		t.Fatalf("DecryptSharedSecret: %v", err)
	}

	for _, rendered := range []string{
		fmt.Sprintf("%v", secret),
		fmt.Sprintf("%s", secret),
		fmt.Sprintf("%#v", secret),
		fmt.Sprintf("%x", secret),
		secret.String(),
	} {
		if rendered != redacted {
			t.Fatalf("rendered %q, want %q", rendered, redacted)
		}
	}
}
