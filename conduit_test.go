package protocol

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"io"
	"testing"
)

func TestConduitPassesBytesThroughUnencrypted(t *testing.T) {
	source := bytes.NewReader([]byte("hello wire"))
	var sink bytes.Buffer
	conduit := newConduit(Transport{
		Reader:    source,
		Writer:    &sink,
		Interrupt: func() error { return nil },
	})

	read := make([]byte, 10)
	if _, err := io.ReadFull(conduit, read); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(read) != "hello wire" {
		t.Fatalf("read %q, want %q", read, "hello wire")
	}

	if _, err := conduit.Write([]byte("goodbye")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if sink.String() != "goodbye" {
		t.Fatalf("wrote %q, want %q", sink.String(), "goodbye")
	}
}

func TestConduitReportsDisabledEncryptionInPipeline(t *testing.T) {
	conduit := newConduit(Transport{
		Reader:    bytes.NewReader(nil),
		Writer:    io.Discard,
		Interrupt: func() error { return nil },
	})

	if got := conduit.pipeline()["encryption.enabled"]; got != "false" {
		t.Fatalf("encryption.enabled = %q, want %q", got, "false")
	}
}

// testCiphers builds the same CFB8 pair on both sides of a loopback pipe.
func testCiphers(t *testing.T, key []byte) (cipher.Stream, cipher.Stream) {
	t.Helper()

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}

	//nolint:staticcheck // SA1019: the wire format mandates AES-CFB8.
	return cipher.NewCFBDecrypter(block, key), cipher.NewCFBEncrypter(block, key)
}

func TestConduitEncryptsAfterTheSwitch(t *testing.T) {
	key := []byte("0123456789abcdef")
	var sink bytes.Buffer
	conduit := newConduit(Transport{
		Reader:    bytes.NewReader(nil),
		Writer:    &sink,
		Interrupt: func() error { return nil },
	})

	if _, err := conduit.Write([]byte("clear")); err != nil {
		t.Fatalf("write before switch: %v", err)
	}

	decrypt, encrypt := testCiphers(t, key)
	if err := conduit.EnableEncryption(decrypt, encrypt); err != nil {
		t.Fatalf("EnableEncryption: %v", err)
	}
	if _, err := conduit.Write([]byte("secret")); err != nil {
		t.Fatalf("write after switch: %v", err)
	}

	written := sink.Bytes()
	if string(written[:5]) != "clear" {
		t.Fatalf("bytes before the switch were transformed: %q", written[:5])
	}
	if string(written[5:]) == "secret" {
		t.Fatal("bytes after the switch were not encrypted")
	}

	// Decrypt with the matching direction to prove the ciphertext is real.
	peerDecrypt, _ := testCiphers(t, key)
	recovered := make([]byte, len(written)-5)
	peerDecrypt.XORKeyStream(recovered, written[5:])
	if string(recovered) != "secret" {
		t.Fatalf("recovered %q, want %q", recovered, "secret")
	}

	if got := conduit.pipeline()["encryption.enabled"]; got != "true" {
		t.Fatalf("encryption.enabled = %q, want %q", got, "true")
	}
}

func TestConduitRejectsSwitchWithBufferedCiphertext(t *testing.T) {
	conduit := newConduit(Transport{
		Reader:    bytes.NewReader([]byte("bytes the peer sent too early")),
		Writer:    io.Discard,
		Interrupt: func() error { return nil },
	})

	// Force the buffer to fill, which is what a peer writing past the switch
	// point causes in practice.
	if _, err := conduit.buffered.Peek(1); err != nil {
		t.Fatalf("peek: %v", err)
	}

	decrypt, encrypt := testCiphers(t, []byte("0123456789abcdef"))
	err := conduit.EnableEncryption(decrypt, encrypt)
	if !errors.Is(err, ErrEncryptionOverrun) {
		t.Fatalf("error = %v, want ErrEncryptionOverrun", err)
	}
	if got := conduit.pipeline()["encryption.enabled"]; got != "false" {
		t.Fatal("a rejected switch must leave encryption disabled")
	}
}

func TestConduitRejectsASecondSwitch(t *testing.T) {
	conduit := newConduit(Transport{
		Reader:    bytes.NewReader(nil),
		Writer:    io.Discard,
		Interrupt: func() error { return nil },
	})

	decrypt, encrypt := testCiphers(t, []byte("0123456789abcdef"))
	if err := conduit.EnableEncryption(decrypt, encrypt); err != nil {
		t.Fatalf("first EnableEncryption: %v", err)
	}

	again, alsoAgain := testCiphers(t, []byte("fedcba9876543210"))
	if err := conduit.EnableEncryption(again, alsoAgain); !errors.Is(err, ErrEncryptionEnabled) {
		t.Fatalf("error = %v, want ErrEncryptionEnabled", err)
	}
}
