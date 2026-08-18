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

// testCiphers builds a matched pair for the conduit tests.
//
// The conduit is agnostic about which stream cipher it is handed, so this uses
// the standard library's CFB rather than reaching into wire/java, which would
// be an import cycle. The real mode is CFB8 and lives in wire/java.
func testCiphers(t *testing.T, key []byte) (cipher.Stream, cipher.Stream) {
	t.Helper()

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}

	//nolint:staticcheck // SA1019: any matched stream pair exercises the conduit.
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

	// Read less than the peer sent, which leaves the rest buffered. That is
	// what a peer writing past the switch point causes in practice.
	if _, err := conduit.Read(make([]byte, 5)); err != nil {
		t.Fatalf("read: %v", err)
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

// The two halves installed separately must leave the conduit exactly where one
// combined switch would, because that is the only reason to allow the split.
func TestConduitHalvesComposeAsOneSwitch(t *testing.T) {
	key := []byte("0123456789abcdef")
	var sink bytes.Buffer

	_, peerEncrypt := testCiphers(t, key)
	inbound := make([]byte, len("from the peer"))
	peerEncrypt.XORKeyStream(inbound, []byte("from the peer"))

	conduit := newConduit(Transport{
		Reader:    bytes.NewReader(inbound),
		Writer:    &sink,
		Interrupt: func() error { return nil },
	})

	decrypt, encrypt := testCiphers(t, key)
	if err := conduit.EnableReadEncryption(decrypt); err != nil {
		t.Fatalf("EnableReadEncryption: %v", err)
	}
	if err := conduit.EnableWriteEncryption(encrypt); err != nil {
		t.Fatalf("EnableWriteEncryption: %v", err)
	}

	got := make([]byte, len(inbound))
	if _, err := io.ReadFull(conduit, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "from the peer" {
		t.Fatalf("decrypted %q, want %q", got, "from the peer")
	}

	if _, err := conduit.Write([]byte("to the peer")); err != nil {
		t.Fatalf("write: %v", err)
	}
	peerDecrypt, _ := testCiphers(t, key)
	recovered := make([]byte, sink.Len())
	peerDecrypt.XORKeyStream(recovered, sink.Bytes())
	if string(recovered) != "to the peer" {
		t.Fatalf("peer recovered %q, want %q", recovered, "to the peer")
	}

	if got := conduit.pipeline()["encryption.enabled"]; got != "true" {
		t.Fatalf("encryption.enabled = %q, want %q", got, "true")
	}
}

// The inbound half keeps the guard: unread bytes mean the pump is part-way
// through a frame it began in the clear, and decrypting its tail would hand back
// a packet that is half one thing and half another.
func TestConduitReadHalfStillRefusesAPartFrame(t *testing.T) {
	conduit := newConduit(Transport{
		Reader:    bytes.NewReader([]byte("a frame the pump began in the clear")),
		Writer:    io.Discard,
		Interrupt: func() error { return nil },
	})
	if _, err := conduit.Read(make([]byte, 5)); err != nil {
		t.Fatalf("read: %v", err)
	}

	decrypt, _ := testCiphers(t, []byte("0123456789abcdef"))
	if err := conduit.EnableReadEncryption(decrypt); !errors.Is(err, ErrEncryptionOverrun) {
		t.Fatalf("error = %v, want ErrEncryptionOverrun", err)
	}
	if got := conduit.pipeline()["encryption.enabled"]; got != "false" {
		t.Fatal("a refused switch must leave the inbound half off")
	}
}

// The outbound half has no read buffer to care about. A negotiator installs it
// while the pump still holds whatever arrived during the response write, and
// that must not be read as a reason to refuse.
func TestConduitWriteHalfIgnoresTheReadBuffer(t *testing.T) {
	var sink bytes.Buffer
	conduit := newConduit(Transport{
		Reader:    bytes.NewReader([]byte("bytes that arrived meanwhile")),
		Writer:    &sink,
		Interrupt: func() error { return nil },
	})
	if _, err := conduit.Read(make([]byte, 5)); err != nil {
		t.Fatalf("read: %v", err)
	}

	_, encrypt := testCiphers(t, []byte("0123456789abcdef"))
	if err := conduit.EnableWriteEncryption(encrypt); err != nil {
		t.Fatalf("EnableWriteEncryption with a full read buffer: %v", err)
	}
	if _, err := conduit.Write([]byte("out")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if sink.String() == "out" {
		t.Fatal("the outbound half was not installed")
	}
}

// Each half refuses its own second install, so a caller that switches twice is
// told which one it repeated rather than silently rekeying.
func TestConduitRefusesASecondSwitchPerHalf(t *testing.T) {
	key := []byte("0123456789abcdef")
	conduit := newConduit(Transport{
		Reader:    bytes.NewReader(nil),
		Writer:    io.Discard,
		Interrupt: func() error { return nil },
	})

	decrypt, encrypt := testCiphers(t, key)
	if err := conduit.EnableReadEncryption(decrypt); err != nil {
		t.Fatalf("EnableReadEncryption: %v", err)
	}

	again, _ := testCiphers(t, key)
	if err := conduit.EnableReadEncryption(again); !errors.Is(err, ErrEncryptionEnabled) {
		t.Fatalf("second inbound switch = %v, want ErrEncryptionEnabled", err)
	}

	// The outbound half is still free, because the halves are independent.
	if err := conduit.EnableWriteEncryption(encrypt); err != nil {
		t.Fatalf("EnableWriteEncryption after an inbound switch: %v", err)
	}
	_, twice := testCiphers(t, key)
	if err := conduit.EnableWriteEncryption(twice); !errors.Is(err, ErrEncryptionEnabled) {
		t.Fatalf("second outbound switch = %v, want ErrEncryptionEnabled", err)
	}
}
