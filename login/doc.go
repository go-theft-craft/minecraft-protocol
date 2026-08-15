// Package login runs the Java Edition login sequence over a managed stream.
//
// It holds both halves. Negotiator is the client and Acceptor is the server,
// and they are tested against each other over one connection, which is the
// reason they live in the same package rather than one of them living in the
// consumer that needs it.
//
// Neither is a stream mode. Nothing in protocol.Stream knows they exist: they
// write packets, read packets, and apply one transport control, all through
// the public API. A developer who wants control over any step uses the
// primitives in wire/java and the stream's TransitionPolicy instead, and never
// constructs either.
//
// Authentication is somebody else's. The client half calls an Authenticator
// and the server half calls a Verifier; this package defines both interfaces,
// implements only the offline authenticator, and makes no network request of
// its own.
//
// This package is protocol 47 only. Protocol 775 changes the login packets
// themselves, so generalizing it belongs with the milestone that generates
// them.
package login
