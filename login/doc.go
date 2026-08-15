// Package login runs the Java Edition login sequence over a managed stream.
//
// The negotiator is a helper, not a stream mode. Nothing in protocol.Stream
// knows it exists: it writes packets, reads packets, and applies one transport
// control, all through the public API. A developer who wants control over any
// step uses the primitives in wire/java and the stream's TransitionPolicy
// instead, and never constructs a negotiator.
//
// This package is protocol 47 only. Protocol 775 changes the login packets
// themselves, so generalizing it belongs with the milestone that generates
// them.
package login
