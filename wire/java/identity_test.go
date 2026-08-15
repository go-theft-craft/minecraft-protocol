package java

import (
	"errors"
	"strings"
	"testing"
)

func TestParseUUIDAcceptsBothWireForms(t *testing.T) {
	const dashed = "069a79f4-44e9-4726-a5be-fca90e38aaf5"
	const undashed = "069a79f444e94726a5befca90e38aaf5"

	fromDashed, err := ParseUUID(dashed)
	if err != nil {
		t.Fatalf("ParseUUID(dashed): %v", err)
	}
	fromUndashed, err := ParseUUID(undashed)
	if err != nil {
		t.Fatalf("ParseUUID(undashed): %v", err)
	}
	if fromDashed != fromUndashed {
		t.Fatal("the two wire forms must parse to the same value")
	}
	if fromDashed.String() != dashed {
		t.Fatalf("String() = %q, want %q", fromDashed.String(), dashed)
	}
	if fromDashed.IsZero() {
		t.Fatal("a populated UUID must not report zero")
	}
}

func TestParseUUIDIsCaseInsensitive(t *testing.T) {
	upper, err := ParseUUID("069A79F4-44E9-4726-A5BE-FCA90E38AAF5")
	if err != nil {
		t.Fatalf("ParseUUID: %v", err)
	}
	lower, err := ParseUUID("069a79f4-44e9-4726-a5be-fca90e38aaf5")
	if err != nil {
		t.Fatalf("ParseUUID: %v", err)
	}
	if upper != lower {
		t.Fatal("parsing must be case insensitive")
	}
	if upper.String() != "069a79f4-44e9-4726-a5be-fca90e38aaf5" {
		t.Fatalf("String() must render lowercase, got %q", upper.String())
	}
}

func TestParseUUIDRejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "empty", text: ""},
		{name: "too short", text: "069a79f4"},
		{name: "too long", text: "069a79f4-44e9-4726-a5be-fca90e38aaf5a"},
		{name: "non-hex", text: "069a79f4-44e9-4726-a5be-fca90e38aazz"},
		{name: "dashes in the wrong places", text: "069a79f4-44e94-726-a5be-fca90e38aaf5"},
		{name: "leading space", text: " 069a79f4-44e9-4726-a5be-fca90e38aaf5"},
		{name: "braced", text: "{069a79f4-44e9-4726-a5be-fca90e38aaf5}"},
		{name: "urn form", text: "urn:uuid:069a79f4-44e9-4726-a5be-fca90e38aaf5"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ParseUUID(testCase.text); !errors.Is(err, ErrInvalidUUID) {
				t.Fatalf("ParseUUID(%q) error = %v, want ErrInvalidUUID", testCase.text, err)
			}
		})
	}
}

func TestParseUsernameAcceptsRealNames(t *testing.T) {
	cases := []string{
		"Notch",
		"jeb_",
		"a",
		"sixteencharacter",
		"Ünïcödé", // offline and modded servers issue these
		"player.one",
	}

	for _, text := range cases {
		name, err := ParseUsername(text)
		if err != nil {
			t.Fatalf("ParseUsername(%q): %v", text, err)
		}
		if name.String() != text {
			t.Fatalf("String() = %q, want %q", name.String(), text)
		}
	}
}

func TestParseUsernameRejectsInvalidNames(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "empty", text: ""},
		{name: "too long", text: "seventeencharact"[:16] + "x"},
		{name: "newline", text: "player\nname"},
		{name: "null byte", text: "player\x00"},
		{name: "invalid UTF-8", text: "player\xff"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ParseUsername(testCase.text); !errors.Is(err, ErrInvalidUsername) {
				t.Fatalf("ParseUsername(%q) error = %v, want ErrInvalidUsername", testCase.text, err)
			}
		})
	}
}

func TestParseUsernameBoundsBytesNotRunes(t *testing.T) {
	// Nine two-byte runes are nine characters but eighteen bytes, and the
	// wire format bounds bytes.
	if _, err := ParseUsername(strings.Repeat("ü", 9)); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("error = %v, want ErrInvalidUsername", err)
	}
}

func TestZeroUUIDReportsZero(t *testing.T) {
	var zero UUID
	if !zero.IsZero() {
		t.Fatal("the zero value must report zero")
	}
	if zero.String() != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("String() = %q", zero.String())
	}
}
