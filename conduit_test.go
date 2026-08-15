package protocol

import (
	"bytes"
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
