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
// Neither half names a version. Both are written against
// protocol.LoginExchange and protocol.LoginRole, so one negotiator and one
// acceptor drive protocol 47, whose login ends at success, and protocol 775,
// whose login continues through a configuration state before reaching play.
//
// What a configuration state should contain is somebody else's too. A real
// client will not leave it until it has registries, tags, and data packs, and
// none of that is protocol. The acceptor opens the state and runs the step
// given to it by WithConfiguration; with no step it sends nothing, which is
// enough for a client that answers what it is sent and is not enough for a
// vanilla one.
package login
