package java

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"errors"
	"testing"
)

// The three canonical server-hash vectors. Java renders a negative SHA-1
// digest as the negation of its twos complement, with no zero padding, which
// is the only unusual part of this function.
func TestComputeServerHashMatchesCanonicalVectors(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	cases := []struct {
		name   string
		digest string
	}{
		{name: "Notch", digest: "4ed1f46bbe04bc756bcb17c0c7ce3e4632f06a48"},
		{name: "jeb_", digest: "-7c9d5b0044c130109a5d7b5fb5c317c02b4e28c1"},
		{name: "simon", digest: "88e16a1019277b15d58faf0541e11910eb756f6"},
	}

	// Each vector is the Java rendering of SHA-1 over the name's bytes, so
	// they pin the rendering rather than the concatenation. "jeb_" is the one
	// that matters: its digest has the high bit set, so a naive
	// implementation renders it as a large positive number instead of a
	// negative one.
	for _, testCase := range cases {
		sum := sha1.Sum([]byte(testCase.name))
		got := javaDigest(sum[:])
		if got != testCase.digest {
			t.Fatalf("javaDigest(sha1(%q)) = %q, want %q", testCase.name, got, testCase.digest)
		}
	}

	// ComputeServerHash must be deterministic for the same inputs and differ when
	// any input differs.
	secret, err := SharedSecretFrom([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatalf("SharedSecretFrom: %v", err)
	}
	first, err := ComputeServerHash("serverid", secret, &key.PublicKey)
	if err != nil {
		t.Fatalf("ComputeServerHash: %v", err)
	}
	again, err := ComputeServerHash("serverid", secret, &key.PublicKey)
	if err != nil {
		t.Fatalf("ComputeServerHash: %v", err)
	}
	if first != again {
		t.Fatalf("ComputeServerHash is not deterministic: %q then %q", first, again)
	}
	other, err := ComputeServerHash("different", secret, &key.PublicKey)
	if err != nil {
		t.Fatalf("ComputeServerHash: %v", err)
	}
	if first == other {
		t.Fatal("ComputeServerHash ignored the server ID")
	}
}

func TestServerPublicKeyRoundTrips(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	encoded, err := EncodeServerPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("EncodeServerPublicKey: %v", err)
	}
	parsed, err := ParseServerPublicKey(encoded)
	if err != nil {
		t.Fatalf("ParseServerPublicKey: %v", err)
	}
	if !parsed.Equal(&key.PublicKey) {
		t.Fatal("round trip changed the key")
	}
}

func TestParseServerPublicKeyRejectsGarbage(t *testing.T) {
	if _, err := ParseServerPublicKey([]byte("not a key")); !errors.Is(err, ErrInvalidServerKey) {
		t.Fatalf("error = %v, want ErrInvalidServerKey", err)
	}
	if _, err := ParseServerPublicKey(nil); !errors.Is(err, ErrInvalidServerKey) {
		t.Fatalf("error = %v, want ErrInvalidServerKey", err)
	}
}

func TestServerKeyEncryptionRoundTrips(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	plaintext := []byte("0123456789abcdef")
	ciphertext, err := EncryptToServerKey(&key.PublicKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptToServerKey: %v", err)
	}
	if string(ciphertext) == string(plaintext) {
		t.Fatal("ciphertext equals plaintext")
	}

	recovered, err := DecryptFromServerKey(key, ciphertext)
	if err != nil {
		t.Fatalf("DecryptFromServerKey: %v", err)
	}
	if string(recovered) != string(plaintext) {
		t.Fatalf("recovered %q, want %q", recovered, plaintext)
	}
}
