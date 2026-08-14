package protocol

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestTransportValidate(t *testing.T) {
	t.Parallel()

	complete := Transport{
		Reader:    bytes.NewReader(nil),
		Writer:    io.Discard,
		Interrupt: func() error { return nil },
	}
	if err := complete.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}

	cases := map[string]Transport{
		"nil reader":    {Writer: io.Discard, Interrupt: complete.Interrupt},
		"nil writer":    {Reader: complete.Reader, Interrupt: complete.Interrupt},
		"nil interrupt": {Reader: complete.Reader, Writer: io.Discard},
		"zero value":    {},
	}

	for name, transport := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := transport.validate(); !errors.Is(err, ErrInvalidTransport) {
				t.Fatalf("validate() error = %v, want ErrInvalidTransport", err)
			}
		})
	}
}
