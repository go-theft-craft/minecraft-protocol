package main

import (
	"errors"
	"strings"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	capturepkg "github.com/go-theft-craft/minecraft-protocol/capture"
)

func filterSubject() capturepkg.Record {
	return capturepkg.Record{
		Kind:      capturepkg.KindPacket,
		Sequence:  42,
		Frame:     21,
		Direction: protocol.DirectionClientbound,
		State:     protocol.State("play"),
		PacketID:  0x21,
		Name:      "map_chunk",
		Payload:   make([]byte, 2048),
	}
}

func TestFilterTerms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expression string
		matches    bool
	}{
		{expression: "", matches: true},
		{expression: "state=play", matches: true},
		{expression: "state=login", matches: false},
		{expression: "state!=login", matches: true},
		{expression: "name~=chunk", matches: true},
		{expression: "name~=entity", matches: false},
		{expression: "kind=packet", matches: true},
		{expression: "kind=raw", matches: false},
		{expression: "direction=clientbound", matches: true},
		{expression: "id=33", matches: true},
		{expression: "id=0x21", matches: true},
		{expression: "id>0x20", matches: true},
		{expression: "id<0x20", matches: false},
		{expression: "sequence>=42", matches: true},
		{expression: "sequence<=41", matches: false},
		{expression: "frame=21", matches: true},
		{expression: "bytes>1024", matches: true},
		{expression: "bytes<1024", matches: false},
		// Three terms conjoin: all must hold.
		{expression: "state=play kind=packet bytes>1024", matches: true},
		{expression: "state=play kind=packet bytes<1024", matches: false},
	}

	subject := filterSubject()
	for _, test := range tests {
		parsed, err := parseFilter(test.expression)
		if err != nil {
			t.Errorf("parseFilter(%q): %v", test.expression, err)

			continue
		}
		if got := parsed.matches(subject); got != test.matches {
			t.Errorf("filter %q matched %v, want %v", test.expression, got, test.matches)
		}
	}
}

func TestFilterUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expression string
		mentions   string
	}{
		{expression: "nosuchfield=1", mentions: "nosuchfield"},
		{expression: "id~=21", mentions: "substring"},
		{expression: "state>play", mentions: "ordered"},
		{expression: "id=notanumber", mentions: "number"},
		{expression: "stateplay", mentions: "operator"},
	}

	for _, test := range tests {
		_, err := parseFilter(test.expression)
		if err == nil {
			t.Errorf("parseFilter(%q) accepted it", test.expression)

			continue
		}

		var usage usageError
		if !errors.As(err, &usage) {
			t.Errorf("parseFilter(%q) error = %v, want a usage error", test.expression, err)
		}
		if !strings.Contains(err.Error(), test.mentions) {
			t.Errorf("parseFilter(%q) error %q does not mention %q", test.expression, err, test.mentions)
		}
	}
}

// TestARedactedRecordFiltersOnWhatItWithheld keeps `bytes>` honest: a record
// that carries nothing still describes a body of a size.
func TestARedactedRecordFiltersOnWhatItWithheld(t *testing.T) {
	t.Parallel()

	record := capturepkg.Record{
		Kind:        capturepkg.KindRawFrame,
		Redacted:    true,
		OriginalLen: 4096,
	}

	parsed, err := parseFilter("bytes>1024")
	if err != nil {
		t.Fatalf("parseFilter: %v", err)
	}
	if !parsed.matches(record) {
		t.Fatal("a redacted 4096-byte record did not match bytes>1024")
	}
}
