package main

import (
	"bytes"
	"fmt"
	"os"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	capturepkg "github.com/go-theft-craft/minecraft-protocol/capture"
)

// scriptStep is one thing the recorded connection did.
type scriptStep struct {
	direction protocol.Direction
	name      string
	// packet is present on clientbound steps: it is the server's packet,
	// decoded from the recorded frame and ready to be written again.
	packet protocol.Packet
}

// script walks a capture as a pair of instructions: what the server said, and
// where the client spoke.
//
// The clientbound frames are decoded and re-encoded rather than replayed as
// bytes. That is the more demanding path on purpose: it puts this
// repository's decoder and encoder both between a real server's recording and
// a real client, so a codec that reads a packet but writes it back differently
// is caught by the client rather than by a test that only ever talks to
// itself.
type script struct {
	file    *os.File
	reader  *capturepkg.Reader
	session protocol.Session
	pending *capturepkg.Record
	primed  bool
}

func openScript(path string, descriptor protocol.Protocol) (*script, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open script: %w", err)
	}

	reader, err := capturepkg.NewReader(file)
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("read script: %w", err)
	}

	// A client-role session, because the script is a client's recording and
	// its clientbound frames are the ones that have to be decoded.
	limits, err := protocol.NewLimits()
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("limits: %w", err)
	}
	session, err := descriptor.NewSession(protocol.RoleClient, limits)
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("script session: %w", err)
	}

	return &script{file: file, reader: reader, session: session}, nil
}

func (s *script) close() {
	_ = s.file.Close()
}

// next returns the next step, pairing each raw frame record with the packet
// record that names it.
//
// The handshake is skipped: the client has already sent its own, and replaying
// the recorded one would be telling the client what it just told us.
func (s *script) next() (scriptStep, error) {
	for {
		record, err := s.nextRecord()
		if err != nil {
			return scriptStep{}, err
		}

		if !s.primed {
			s.primed = true
			s.alignTo(record.BeforeState)
		}

		if record.Kind != capturepkg.KindRawFrame {
			// A packet record carries the state after its own transition,
			// which is the capture's statement about where the connection
			// went. Raw records are stamped before it commits.
			s.alignTo(record.State)

			continue
		}

		name, err := s.nameOf(record)
		if err != nil {
			return scriptStep{}, err
		}

		if record.Direction != s.session.Inbound() {
			// The client's half. The harness reads it live instead.
			if name == "set_protocol" {
				continue
			}

			return scriptStep{direction: record.Direction, name: name}, nil
		}

		packet, err := s.decode(record)
		if err != nil {
			return scriptStep{}, err
		}

		return scriptStep{direction: record.Direction, name: name, packet: packet}, nil
	}
}

// nextRecord reads one record, using the one held back by a previous lookahead
// when there is one.
func (s *script) nextRecord() (capturepkg.Record, error) {
	if s.pending != nil {
		record := *s.pending
		s.pending = nil

		return record, nil
	}

	return s.reader.Next()
}

// nameOf looks one record ahead for the packet record that names this frame.
// A raw frame with no packet record after it is one the capturing session
// could not decode, and it has no name to give.
func (s *script) nameOf(raw capturepkg.Record) (string, error) {
	record, err := s.reader.Next()
	if err != nil {
		return "", nil //nolint:nilerr // the frame is still usable without a name
	}
	if record.Kind == capturepkg.KindPacket && record.Frame == raw.Frame {
		s.alignTo(record.State)

		return record.Name, nil
	}

	s.pending = &record

	return "", nil
}

// decode turns one recorded frame back into a packet.
func (s *script) decode(record capturepkg.Record) (protocol.Packet, error) {
	if record.Redacted {
		return protocol.Packet{}, fmt.Errorf(
			"script record %d was redacted: a capture that withheld a body cannot be replayed at a client",
			record.Sequence,
		)
	}

	frame, err := s.session.Framer().ReadFrame(bytes.NewReader(record.Payload))
	if err != nil {
		return protocol.Packet{}, fmt.Errorf("script record %d: read frame: %w", record.Sequence, err)
	}

	packet, err := s.session.DecodeFrame(frame.Payload())
	if err != nil {
		return protocol.Packet{}, fmt.Errorf("script record %d: decode: %w", record.Sequence, err)
	}

	// The script session follows the recorded connection so that later frames
	// decode under the state and compression they were written under.
	transition, proposed, err := s.session.ProposeTransition(packet)
	if err != nil {
		return protocol.Packet{}, fmt.Errorf("script record %d: transition: %w", record.Sequence, err)
	}
	if proposed {
		if err := s.session.ValidateTransition(transition); err == nil {
			s.session.ApplyTransition(transition)
		}
	}

	return packet, nil
}

// alignTo moves the script session to a state the capture recorded.
func (s *script) alignTo(state protocol.State) {
	if state == "" || s.session.Snapshot().State == state {
		return
	}
	s.session.ApplyTransition(protocol.Transition{Control: protocol.StateControl{State: state}})
}
