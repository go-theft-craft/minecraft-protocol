package protocol

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

// scriptedReader hands out bytes only when a test releases them, so tests can
// control scheduling exactly instead of guessing with sleeps.
type scriptedReader struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	release   chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
	err       error
}

func newScriptedReader() *scriptedReader {
	return &scriptedReader{
		release: make(chan struct{}, 1024),
		closed:  make(chan struct{}),
	}
}

// deliver queues bytes and lets one Read call consume them.
func (r *scriptedReader) deliver(data []byte) {
	r.mu.Lock()
	r.buffer.Write(data)
	r.mu.Unlock()

	r.release <- struct{}{}
}

// fail makes the next read return err.
func (r *scriptedReader) fail(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()

	r.release <- struct{}{}
}

func (r *scriptedReader) interrupt() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func (r *scriptedReader) Read(data []byte) (int, error) {
	for {
		r.mu.Lock()
		if r.buffer.Len() > 0 {
			n, err := r.buffer.Read(data)
			r.mu.Unlock()
			return n, err
		}
		failure := r.err
		r.mu.Unlock()

		if failure != nil {
			return 0, failure
		}

		select {
		case <-r.release:
		case <-r.closed:
			return 0, io.EOF
		}
	}
}

// blockingWriter blocks every write until a test releases it.
type blockingWriter struct {
	mu        sync.Mutex
	written   bytes.Buffer
	entered   chan struct{}
	release   chan error
	closed    chan struct{}
	closeOnce sync.Once
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{
		entered: make(chan struct{}, 1024),
		release: make(chan error, 1024),
		closed:  make(chan struct{}),
	}
}

func (w *blockingWriter) interrupt() error {
	w.closeOnce.Do(func() { close(w.closed) })
	return nil
}

func (w *blockingWriter) Write(data []byte) (int, error) {
	select {
	case w.entered <- struct{}{}:
	default:
	}

	select {
	case err := <-w.release:
		if err != nil {
			return 0, err
		}
	case <-w.closed:
		return 0, errors.New("transport interrupted")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	return w.written.Write(data)
}

func (w *blockingWriter) bytesWritten() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()

	return bytes.Clone(w.written.Bytes())
}

// syncWriter records complete writes without blocking.
type syncWriter struct {
	mu      sync.Mutex
	written bytes.Buffer
	err     error
	writes  int
}

func (w *syncWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.writes++
	if w.err != nil {
		return 0, w.err
	}

	return w.written.Write(data)
}

func (w *syncWriter) bytesWritten() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()

	return bytes.Clone(w.written.Bytes())
}

func (w *syncWriter) writeCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.writes
}

// truncatingWriter accepts only part of a frame and then fails, modelling a
// transport that dies mid-write.
type truncatingWriter struct {
	limit   int
	written int
	err     error
	mu      sync.Mutex
}

func (w *truncatingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	remaining := w.limit - w.written
	if remaining <= 0 {
		return 0, w.err
	}
	if len(data) > remaining {
		data = data[:remaining]
	}
	w.written += len(data)

	return len(data), nil
}

// testFramer is an edition-neutral framer for stream tests: one length byte
// followed by that many payload bytes.
type testFramer struct {
	limits Limits
}

func (f testFramer) ReadFrame(reader io.Reader) (Frame, error) {
	var header [1]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return Frame{}, err
	}
	length := int(header[0])
	if length == 0 {
		return Frame{}, errors.New("empty test frame")
	}
	if length > f.limits.FrameBytes() {
		return Frame{}, errors.New("test frame exceeds the frame limit")
	}

	wire := make([]byte, 1+length)
	wire[0] = header[0]
	if _, err := io.ReadFull(reader, wire[1:]); err != nil {
		return Frame{}, err
	}

	return NewFrame(wire, 1)
}

func (f testFramer) BuildFrame(payload []byte) (Frame, error) {
	if len(payload) == 0 || len(payload) > 255 {
		return Frame{}, errors.New("test frame payload is unrepresentable")
	}

	wire := make([]byte, 1+len(payload))
	wire[0] = byte(len(payload))
	copy(wire[1:], payload)

	return NewFrame(wire, 1)
}

func (f testFramer) WriteFrame(writer io.Writer, frame Frame) error {
	wire := frame.WireBytes()
	written := 0
	for written < len(wire) {
		n, err := writer.Write(wire[written:])
		written += n
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}

	return nil
}

// testSession is a controllable Session for stream tests. Its packets are one
// byte of identity plus an opaque payload.
//
// Only the coordinator calls the Session methods, but tests inspect the
// recorded history while the stream runs, so everything mutable is guarded.
type testSession struct {
	limits   Limits
	framer   Framer
	inbound  Direction
	outbound Direction

	mu       sync.Mutex
	state    State
	pipeline map[string]string

	decodeErr  error
	encodeErr  error
	decodeHook func(payload []byte) (Packet, error)

	proposeHook           func(Packet) (Transition, bool, error)
	validateTransitionErr error
	validateControlErr    error

	disconnectPacket *Packet
	disconnectErr    error

	appliedTransitions []Transition
	appliedControls    []Control
	decodeStates       []State
	encodeStates       []State
}

func (s *testSession) setDecodeErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.decodeErr = err
}

func (s *testSession) setEncodeErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.encodeErr = err
}

func (s *testSession) setProposeHook(hook func(Packet) (Transition, bool, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.proposeHook = hook
}

func (s *testSession) setValidateTransitionErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.validateTransitionErr = err
}

func (s *testSession) setValidateControlErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.validateControlErr = err
}

func (s *testSession) history() ([]Transition, []Control, []State, []State) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]Transition(nil), s.appliedTransitions...),
		append([]Control(nil), s.appliedControls...),
		append([]State(nil), s.decodeStates...),
		append([]State(nil), s.encodeStates...)
}

func newTestSession(t *testing.T, limits Limits) *testSession {
	t.Helper()

	return &testSession{
		limits:   limits,
		framer:   testFramer{limits: limits},
		state:    State("play"),
		pipeline: map[string]string{"stage": "initial"},
		inbound:  DirectionClientbound,
		outbound: DirectionServerbound,
	}
}

func (s *testSession) Framer() Framer      { return s.framer }
func (s *testSession) Role() Role          { return RoleClient }
func (s *testSession) Limits() Limits      { return s.limits }
func (s *testSession) Inbound() Direction  { return s.inbound }
func (s *testSession) Outbound() Direction { return s.outbound }

func (s *testSession) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.state
}

func (s *testSession) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	return NewSnapshot(s.state, s.pipeline)
}

func (s *testSession) ValidateState(State) error { return nil }

func (s *testSession) SetState(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = state
}

func (s *testSession) DecodeFrame(payload []byte) (Packet, error) {
	s.mu.Lock()
	state := s.state
	s.decodeStates = append(s.decodeStates, state)
	hook, decodeErr := s.decodeHook, s.decodeErr
	s.mu.Unlock()

	if hook != nil {
		return hook(payload)
	}
	if decodeErr != nil {
		return Packet{}, decodeErr
	}
	if len(payload) == 0 {
		return Packet{}, errors.New("empty test packet body")
	}

	return Packet{
		State:     state,
		Direction: s.inbound,
		ID:        int32(payload[0]),
		Payload:   bytes.Clone(payload[1:]),
	}, nil
}

func (s *testSession) EncodeFrame(packet Packet) ([]byte, error) {
	s.mu.Lock()
	s.encodeStates = append(s.encodeStates, s.state)
	encodeErr := s.encodeErr
	s.mu.Unlock()

	if encodeErr != nil {
		return nil, encodeErr
	}

	body := make([]byte, 1+len(packet.Payload))
	body[0] = byte(packet.ID)
	copy(body[1:], packet.Payload)

	return body, nil
}

func (s *testSession) ProposeTransition(packet Packet) (Transition, bool, error) {
	s.mu.Lock()
	hook := s.proposeHook
	s.mu.Unlock()

	if hook == nil {
		return Transition{}, false, nil
	}

	return hook(packet)
}

func (s *testSession) ValidateTransition(Transition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.validateTransitionErr
}

func (s *testSession) ApplyTransition(transition Transition) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.appliedTransitions = append(s.appliedTransitions, transition)
	if transition.State != nil {
		s.state = *transition.State
	}
	if control, ok := transition.Control.(testControl); ok {
		s.pipeline["stage"] = control.stage
	}
}

func (s *testSession) ValidateControl(Control) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.validateControlErr
}

func (s *testSession) ApplyControl(control Control) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.appliedControls = append(s.appliedControls, control)
	switch typed := control.(type) {
	case StateControl:
		s.state = typed.State
	case testControl:
		s.pipeline["stage"] = typed.stage
	}
}

// testControl is an edition-specific control for stream tests.
type testControl struct {
	stage string
}

func (testControl) ControlName() string { return "test.stage" }

// setDisconnect makes the session offer a disconnect packet during shutdown.
func (s *testSession) setDisconnect(packet *Packet, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.disconnectPacket = packet
	s.disconnectErr = err
}

func (s *testSession) Disconnect(reason string) (Packet, bool, error) {
	s.mu.Lock()
	packet, err := s.disconnectPacket, s.disconnectErr
	s.mu.Unlock()

	if err != nil {
		return Packet{}, false, err
	}
	if packet == nil {
		return Packet{}, false, nil
	}

	disconnect := *packet
	disconnect.Payload = append(bytes.Clone(disconnect.Payload), []byte(reason)...)

	return disconnect, true, nil
}

// testFrameBytes builds the wire form of one test packet.
func testFrameBytes(id byte, payload ...byte) []byte {
	body := append([]byte{id}, payload...)
	return append([]byte{byte(len(body))}, body...)
}
