package java

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"testing"
)

// TestCFB8MatchesItsDefinition pins the construction rather than trusting the
// implementation to be self-consistent. Each ciphertext byte is the plaintext
// byte XORed with the first byte of the encrypted register, and the register
// shifts in the ciphertext byte. Deriving the expectation independently here
// is what distinguishes CFB8 from the block-wide CFB in the standard library.
func TestCFB8MatchesItsDefinition(t *testing.T) {
	key := []byte("0123456789abcdef")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}

	plaintext := []byte("the quick brown fox jumps over the lazy dog")

	// The independent derivation, written straight from the definition.
	register := make([]byte, aes.BlockSize)
	copy(register, key)
	want := make([]byte, len(plaintext))
	for index, input := range plaintext {
		encrypted := make([]byte, aes.BlockSize)
		block.Encrypt(encrypted, register)
		output := input ^ encrypted[0]
		copy(register, register[1:])
		register[aes.BlockSize-1] = output
		want[index] = output
	}

	got := make([]byte, len(plaintext))
	newCFB8Encrypter(block, key).XORKeyStream(got, plaintext)

	if !bytes.Equal(got, want) {
		t.Fatalf("ciphertext = %x, want %x", got, want)
	}
}

func TestCFB8RoundTrips(t *testing.T) {
	key := []byte("0123456789abcdef")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}

	plaintext := bytes.Repeat([]byte("java edition wire bytes "), 20)

	ciphertext := make([]byte, len(plaintext))
	newCFB8Encrypter(block, key).XORKeyStream(ciphertext, plaintext)
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext equals plaintext")
	}

	recovered := make([]byte, len(ciphertext))
	newCFB8Decrypter(block, key).XORKeyStream(recovered, ciphertext)
	if !bytes.Equal(recovered, plaintext) {
		t.Fatalf("recovered %q, want %q", recovered, plaintext)
	}
}

// TestCFB8IsStatefulAcrossCalls proves the register survives between calls,
// which is what a framed stream depends on: a frame handed out in two reads
// must decrypt the same as one read.
func TestCFB8IsStatefulAcrossCalls(t *testing.T) {
	key := []byte("0123456789abcdef")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}

	plaintext := []byte("split across two calls")

	whole := make([]byte, len(plaintext))
	newCFB8Encrypter(block, key).XORKeyStream(whole, plaintext)

	split := make([]byte, len(plaintext))
	stream := newCFB8Encrypter(block, key)
	stream.XORKeyStream(split[:5], plaintext[:5])
	stream.XORKeyStream(split[5:], plaintext[5:])

	if !bytes.Equal(split, whole) {
		t.Fatalf("split encryption = %x, want %x", split, whole)
	}
}

// TestCFB8DiffersFromBlockWideCFB is the regression guard. The two modes are
// interchangeable in any test where both peers use the same one, so this
// states outright that they are not the same cipher.
func TestCFB8DiffersFromBlockWideCFB(t *testing.T) {
	key := []byte("0123456789abcdef")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}

	plaintext := []byte("this is more than one AES block long, by a wide margin")

	segment := make([]byte, len(plaintext))
	newCFB8Encrypter(block, key).XORKeyStream(segment, plaintext)

	blockWide := make([]byte, len(plaintext))
	//nolint:staticcheck // SA1019: named here only to prove it is the wrong mode.
	cipher.NewCFBEncrypter(block, key).XORKeyStream(blockWide, plaintext)

	if bytes.Equal(segment, blockWide) {
		t.Fatal("CFB8 must not agree with the standard library's block-wide CFB")
	}
}
