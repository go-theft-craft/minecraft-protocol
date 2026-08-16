// Package replay drives a capture back through a session or a connection.
//
// Replay answers two different questions with one mechanism. Offline, it asks
// whether this code still reads what a real server sent: every recorded frame
// is decoded again, and a digest over the result says whether anything moved.
// Connected, it asks what a peer does when it is sent exactly what a peer sent
// before.
//
// A replay never invents timing. Fast mode has none, recorded mode honours
// what the capture measured, and every sleep is a select on the context, so a
// cancelled replay stops at once rather than after the next gap.
package replay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/capture"
)

var (
	// ErrInvalidPlayer reports a player that cannot be built.
	ErrInvalidPlayer = errors.New("invalid replay player")
	// ErrUnknownProtocol reports a capture naming a protocol the resolver
	// does not know.
	ErrUnknownProtocol = errors.New("unknown protocol")
	// ErrRedactedRecord reports a redacted record on a path that needs its
	// bytes. A capture that withheld a body cannot be sent to a peer, and
	// pretending otherwise would put a truncated frame on the wire.
	ErrRedactedRecord = errors.New("record was redacted and cannot be replayed")
	// ErrReplayFailed reports a record the destination could not accept.
	ErrReplayFailed = errors.New("replay failed")
)

// Mode selects how a replay spends time between records.
type Mode string

const (
	// ModeFast replays with no delay. It is the default, and it is what a
	// verification run wants: the timing of a capture is not what is being
	// checked.
	ModeFast Mode = "fast"
	// ModeRecorded honours the gaps the capture measured.
	ModeRecorded Mode = "recorded"
	// ModeScaled multiplies the recorded gaps. A scale of 0 is fast mode,
	// which is what makes "scaled" a single knob rather than a second mode
	// somebody has to remember to turn off.
	ModeScaled Mode = "scaled"
	// ModeStep hands out one record per Next and never sleeps.
	ModeStep Mode = "step"
)

// Resolver turns a capture header's protocol ID into a protocol.
//
// It is injected rather than looked up in a global registry. A registry
// populated by package initialization would link every generated version into
// every consumer, and a program that only speaks one of them would carry the
// other's megabyte of tables for nothing.
type Resolver interface {
	Resolve(id string) (protocol.Protocol, bool)
}

// ResolverFunc adapts a function to Resolver.
type ResolverFunc func(id string) (protocol.Protocol, bool)

// Resolve implements Resolver.
func (f ResolverFunc) Resolve(id string) (protocol.Protocol, bool) { return f(id) }

// Result is what a finished replay learned.
type Result struct {
	// Records is how many records were replayed, excluding the ones that
	// describe the consumer rather than the wire.
	Records int
	// Digest is computed over the replayed records, so it can be compared
	// with the one the capture's trailer carries.
	Digest string
	// Drift is how far behind the recorded timing the replay ran. It is zero
	// in fast and step modes, which have no timing to be behind.
	Drift time.Duration
	// Divergences are the places where the session proposed a different state
	// than the capture recorded. This is the regression signal the offline
	// destination exists to produce: a codec change that moves a connection
	// somewhere else shows up here rather than as a decode error much later.
	Divergences []Divergence
}

// Divergence is one disagreement between a capture and this code.
type Divergence struct {
	Sequence uint64
	Recorded protocol.State
	Proposed protocol.State
}

func (d Divergence) String() string {
	return fmt.Sprintf("sequence %d: capture recorded %q, this session proposed %q",
		d.Sequence, d.Recorded, d.Proposed)
}

// Player replays one capture.
type Player struct {
	reader   *capture.Reader
	mode     Mode
	scale    float64
	resolver Resolver

	session   protocol.Session
	transport *protocol.Transport
	direction protocol.Direction

	digester  *capture.Digester
	started   time.Time
	records   int
	drift     time.Duration
	diverged  []Divergence
	primed    bool
	exhausted bool
}

// Option configures a player.
type Option func(*Player) error

// WithMode selects the timing mode.
func WithMode(mode Mode) Option {
	return func(p *Player) error {
		switch mode {
		case ModeFast, ModeRecorded, ModeScaled, ModeStep:
			p.mode = mode

			return nil
		default:
			return fmt.Errorf("%w: unknown mode %q", ErrInvalidPlayer, mode)
		}
	}
}

// WithScale sets the multiplier used by ModeScaled. Zero replays as fast as
// the destination accepts records.
func WithScale(scale float64) Option {
	return func(p *Player) error {
		if scale < 0 {
			return fmt.Errorf("%w: scale must not be negative, got %v", ErrInvalidPlayer, scale)
		}
		p.scale = scale

		return nil
	}
}

// WithSession replays into a session the caller built. It is how a replay is
// driven without a resolver, and how a test supplies a session of its own.
func WithSession(session protocol.Session) Option {
	return func(p *Player) error {
		if session == nil {
			return fmt.Errorf("%w: nil session", ErrInvalidPlayer)
		}
		p.session = session

		return nil
	}
}

// WithResolver supplies the protocol lookup used to build a session from the
// capture's own header.
func WithResolver(resolver Resolver) Option {
	return func(p *Player) error {
		if resolver == nil {
			return fmt.Errorf("%w: nil resolver", ErrInvalidPlayer)
		}
		p.resolver = resolver

		return nil
	}
}

// WithTransport replays the given direction's frames to a peer.
//
// The direction is required rather than defaulted: sending a server's own
// frames back to it is a different exercise from sending it a client's, and
// guessing which one somebody meant is not a service.
func WithTransport(transport protocol.Transport, direction protocol.Direction) Option {
	return func(p *Player) error {
		if transport.Writer == nil {
			return fmt.Errorf("%w: transport has no writer", ErrInvalidPlayer)
		}
		if direction != protocol.DirectionClientbound && direction != protocol.DirectionServerbound {
			return fmt.Errorf("%w: replay needs a direction to send", ErrInvalidPlayer)
		}
		p.transport = &transport
		p.direction = direction

		return nil
	}
}

// New returns a player over one capture.
func New(reader *capture.Reader, options ...Option) (*Player, error) {
	if reader == nil {
		return nil, fmt.Errorf("%w: nil capture reader", ErrInvalidPlayer)
	}

	player := &Player{
		reader:   reader,
		mode:     ModeFast,
		digester: capture.NewDigester(),
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidPlayer)
		}
		if err := option(player); err != nil {
			return nil, err
		}
	}

	if player.session == nil && player.transport == nil {
		if err := player.buildSession(); err != nil {
			return nil, err
		}
	}

	return player, nil
}

// buildSession creates the offline destination from the capture's header.
func (p *Player) buildSession() error {
	header := p.reader.Header()
	if p.resolver == nil {
		return fmt.Errorf(
			"%w: capture names protocol %q and no session or resolver was supplied",
			ErrInvalidPlayer, header.Protocol,
		)
	}

	descriptor, known := p.resolver.Resolve(header.Protocol)
	if !known {
		return fmt.Errorf("%w: %q", ErrUnknownProtocol, header.Protocol)
	}

	limits, err := protocol.NewLimits(
		protocol.MaxFrameBytes(header.FrameBytes),
		protocol.MaxDecompressedBytes(header.DecompressedBytes),
	)
	if err != nil {
		return fmt.Errorf("replay limits: %w", err)
	}

	// The capture's own role: a capture taken from a client is replayed into
	// a client session, so the inbound direction matches what was recorded.
	role := protocol.RoleClient
	if header.Role == "server" {
		role = protocol.RoleServer
	}

	session, err := descriptor.NewSession(role, limits)
	if err != nil {
		return fmt.Errorf("replay session: %w", err)
	}
	p.session = session

	return nil
}

// Next returns the next replayable record, having applied it to the
// destination. It is the whole of step mode and the body of Run.
func (p *Player) Next(ctx context.Context) (capture.Record, error) {
	if p.started.IsZero() {
		p.started = time.Now()
	}

	for {
		if err := ctx.Err(); err != nil {
			return capture.Record{}, err
		}

		record, err := p.reader.Next()
		if err != nil {
			if errors.Is(err, capture.ErrEndOfCapture) {
				p.exhausted = true
			}

			return capture.Record{}, err
		}
		if !record.Replayable() {
			continue
		}

		if err := p.wait(ctx, record); err != nil {
			return capture.Record{}, err
		}
		if err := p.apply(ctx, record); err != nil {
			return capture.Record{}, err
		}

		p.digester.Add(record)
		p.records++

		return record, nil
	}
}

// Run replays every record and returns what it learned.
func (p *Player) Run(ctx context.Context) (Result, error) {
	for {
		_, err := p.Next(ctx)
		if err == nil {
			continue
		}
		if errors.Is(err, capture.ErrEndOfCapture) {
			return p.result(), nil
		}

		return p.result(), err
	}
}

func (p *Player) result() Result {
	return Result{
		Records:     p.records,
		Digest:      p.digester.Sum(),
		Drift:       p.drift,
		Divergences: p.diverged,
	}
}

// wait spends the gap between the previous record and this one.
func (p *Player) wait(ctx context.Context, record capture.Record) error {
	scale := p.scale
	switch p.mode {
	case ModeFast, ModeStep:
		return nil
	case ModeRecorded:
		scale = 1
	case ModeScaled:
		if scale == 0 {
			return nil
		}
	}

	target := time.Duration(float64(record.Elapsed) * scale)
	behind := time.Since(p.started) - target
	if behind > p.drift {
		p.drift = behind
	}
	if behind >= 0 {
		return nil
	}

	timer := time.NewTimer(-behind)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// apply hands one record to whichever destination this player has.
func (p *Player) apply(ctx context.Context, record capture.Record) error {
	if p.transport != nil {
		return p.send(ctx, record)
	}

	return p.decode(record)
}

// decode replays one raw frame through the session.
//
// Packet records are counted and digested but not decoded again: they hold the
// decoded body, not the frame it came in, and re-decoding a body that a
// compression envelope has already been stripped from would be decoding
// something that never crossed the wire.
func (p *Player) decode(record capture.Record) error {
	if record.Kind != capture.KindRawFrame {
		return nil
	}
	if record.Redacted {
		// A withheld body is not a failure offline: the capture said so, and
		// skipping it is honest. It is a failure only when sending.
		return nil
	}

	p.prime(record)

	frame, err := p.session.Framer().ReadFrame(bytes.NewReader(record.Payload))
	if err != nil {
		return fmt.Errorf("%w: sequence %d: read frame: %w", ErrReplayFailed, record.Sequence, err)
	}

	packet, err := p.session.DecodeFrame(frame.Payload())
	if err != nil {
		return fmt.Errorf("%w: sequence %d: decode: %w", ErrReplayFailed, record.Sequence, err)
	}

	return p.follow(record, packet)
}

// prime puts the session where the capture says the connection was when its
// first record was taken.
//
// A capture does not have to begin at a handshake. One taken from a proxy, or
// started against a connection already in play, begins wherever it begins, and
// a replay that insisted on starting at handshaking would fail to decode its
// first frame. Only the first record primes: after that the recorded
// transitions carry the session, and forcing a state per record would hide
// exactly the divergence this replay exists to find.
func (p *Player) prime(record capture.Record) {
	if p.primed {
		return
	}
	p.primed = true

	if record.BeforeState == "" || p.session.Snapshot().State == record.BeforeState {
		return
	}
	p.session.ApplyTransition(protocol.Transition{
		Control: protocol.StateControl{State: record.BeforeState},
	})
}

// follow applies the transition this packet implies and reports where the
// session disagreed with the capture.
//
// The session's own proposal is applied rather than the recorded state alone,
// because a state is not the whole of a transition: compression is installed
// by one, and a replay that ignored it would fail to read the very next frame.
// Where the resulting state differs from the recorded one, the capture wins
// and the disagreement is reported.
func (p *Player) follow(record capture.Record, packet protocol.Packet) error {
	transition, proposed, err := p.session.ProposeTransition(packet)
	if err != nil {
		return fmt.Errorf("%w: sequence %d: transition: %w", ErrReplayFailed, record.Sequence, err)
	}
	if proposed {
		if err := p.session.ValidateTransition(transition); err != nil {
			return fmt.Errorf("%w: sequence %d: transition: %w", ErrReplayFailed, record.Sequence, err)
		}
		p.session.ApplyTransition(transition)
	}

	if record.State == "" {
		return nil
	}
	if current := p.session.Snapshot().State; current != record.State {
		p.diverged = append(p.diverged, Divergence{
			Sequence: record.Sequence,
			Recorded: record.State,
			Proposed: current,
		})
		p.session.ApplyTransition(protocol.Transition{Control: protocol.StateControl{State: record.State}})
	}

	return nil
}

// send writes one recorded frame to the peer.
func (p *Player) send(_ context.Context, record capture.Record) error {
	if record.Kind != capture.KindRawFrame || record.Direction != p.direction {
		return nil
	}
	if record.Redacted {
		return fmt.Errorf("%w: sequence %d", ErrRedactedRecord, record.Sequence)
	}

	if _, err := p.transport.Writer.Write(record.Payload); err != nil {
		return fmt.Errorf("%w: sequence %d: write: %w", ErrReplayFailed, record.Sequence, err)
	}

	return nil
}

// Exhausted reports whether the capture has been read to its end.
func (p *Player) Exhausted() bool { return p.exhausted }
