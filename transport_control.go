package protocol

// TransportControl is a control that reconfigures the byte stream below
// framing, such as enabling encryption.
//
// A stream applies it to its conduit instead of its session, which keeps
// transport-level changes out of a type documented as performing no I/O. The
// stream matches on this interface and never on a concrete type, so it still
// does not interpret control contents.
//
// ApplyTransport runs on the coordinator, at a frame boundary, after any
// preceding write has fully reached the transport. Returning an error rejects
// the control and fails the caller without terminating the stream.
type TransportControl interface {
	Control
	ApplyTransport(*Conduit) error
}
