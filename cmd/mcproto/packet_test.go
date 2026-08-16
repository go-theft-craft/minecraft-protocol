package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPacketDecodeAndEncodeRoundTrip is the pair's contract: what decode says
// about a body, encode turns back into that body.
func TestPacketDecodeAndEncodeRoundTrip(t *testing.T) {
	t.Parallel()

	// A protocol 47 clientbound keepalive: packet ID 0x00, one varint field.
	const body = "002a"

	code, decoded, stderr := runCLIWithInput(t, body+"\n",
		"packet", "decode",
		"--protocol", "java/1.8.9", "--state", "play", "--direction", "clientbound", "--hex",
	)
	if code != exitSuccess {
		t.Fatalf("decode exit code = %d (stderr: %s)", code, stderr)
	}

	var report packetReport
	if err := json.Unmarshal([]byte(decoded), &report); err != nil {
		t.Fatalf("decode output is not one object: %v\n%s", err, decoded)
	}
	if report.Name != "keep_alive" {
		t.Fatalf("decoded packet is %q, want keep_alive", report.Name)
	}
	if !strings.Contains(string(report.Fields), "42") {
		t.Fatalf("decoded fields %s do not carry the value", report.Fields)
	}

	code, encoded, stderr := runCLIWithInput(t, decoded,
		"packet", "encode",
		"--protocol", "java/1.8.9", "--state", "play", "--direction", "clientbound", "--hex",
	)
	if code != exitSuccess {
		t.Fatalf("encode exit code = %d (stderr: %s)", code, stderr)
	}
	if got := strings.TrimSpace(encoded); got != body {
		t.Fatalf("encode produced %q, want the body decode was given (%q)", got, body)
	}
}

func TestPacketDecodeRejectsAMalformedBody(t *testing.T) {
	t.Parallel()

	// Packet ID 0x00 with no field after it: the keepalive's varint is
	// missing, so the decode path names where it ran out.
	code, _, stderr := runCLIWithInput(t, "00\n",
		"packet", "decode",
		"--protocol", "java/1.8.9", "--state", "play", "--direction", "clientbound", "--hex",
	)
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr, "keep_alive") {
		t.Fatalf("the failure does not name the decode path: %q", stderr)
	}
}

func TestPacketRequiresItsIdentityFlags(t *testing.T) {
	t.Parallel()

	cases := [][]string{
		{"packet", "decode"},
		{"packet", "decode", "--protocol", "java/1.8.9"},
		{"packet", "decode", "--protocol", "java/1.8.9", "--state", "play"},
		{"packet", "decode", "--protocol", "java/9999", "--state", "play", "--direction", "clientbound"},
	}

	for _, args := range cases {
		code, _, stderr := runCLIWithInput(t, "00\n", args...)
		if code != exitUsage {
			t.Errorf("%v exit code = %d, want %d (stderr: %s)", args, code, exitUsage, stderr)
		}
	}
}

func TestPacketDecodeRejectsNonHexUnderTheHexFlag(t *testing.T) {
	t.Parallel()

	code, _, stderr := runCLIWithInput(t, "not hex at all\n",
		"packet", "decode",
		"--protocol", "java/1.8.9", "--state", "play", "--direction", "clientbound", "--hex",
	)
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitUsage, stderr)
	}
}
