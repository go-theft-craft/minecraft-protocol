# Encryption and login lifecycle design

- Status: Draft for review
- Date: 2026-08-15
- Repository: `minecraft-protocol`
- Milestone: M2

## Context

M1 delivered the managed stream: bounded framing, compression, ordered runtime
transitions, graceful disconnect, and lossless observation points. Every byte
still crosses the transport in the clear, and nothing in the repository knows
how to complete a Java login.

Compression lives inside the session, because the compression envelope sits
inside the frame. Encryption cannot follow that pattern. AES-CFB8 wraps the
whole byte stream, including the frame length prefix, so it belongs below
framing and outside anything the session touches.

This slice also decides where authentication stops. The protocol library gets
the cryptography and the packet mechanics. Identity acquisition stays in the
consumer, which matches the existing headless client plan.

## Goals

This slice adds:

- AES-128/CFB8 encryption at the transport boundary, switchable at runtime.
- A stream-owned transport stage that owns the cipher and the read buffer.
- A `TransportControl` routing rule, so the stream still never interprets
  control contents.
- Java key-exchange primitives: server key parsing, secret generation, PKCS#1
  v1.5 encryption to the server key, and the Minecraft server hash.
- A `SharedSecret` type that cannot reach a log or an error by accident.
- An opt-in `LoginNegotiator` that runs the client login sequence, and the
  narrow `Authenticator` interface it calls.
- Redacted observations by default, with an explicit, recorded opt-out.
- Tests for the cipher, the switch point, the server hash, every login failure
  mode, and an encrypted lane in the pinned Node interoperability suite.

## Non-goals

This slice does not add:

- Microsoft, Xbox Live, or XSTS authentication.
- Mojang session server calls.
- Token storage or credential persistence.
- Protocol 775 codecs or the modern configuration state.
- Packet routing, capture storage, or replay.
- A migration of `server`, `proxy`, or `headless-minecraft`.

## Scope of authentication

`minecraft-protocol` implements cryptography and packet mechanics only. It
defines an `Authenticator` interface and calls it at the correct moment. It
performs no HTTP request and holds no credential.

Offline identity, Microsoft device authorization, Xbox Live, XSTS, and the
Minecraft services join and verify calls stay in `headless-minecraft`, as the
headless client and authentication plan already specifies. `server` and `proxy`
supply their own verifier when they need online mode.

This keeps the wire library free of network side effects it cannot test
hermetically, and it keeps one implementation of the account flow rather than
three.

## Protocol version scope

M2 implements and tests encryption and login on protocol 47, where generated
codecs, byte fixtures, and the Node interoperability lane already exist.

The contracts are version neutral. `State` is a string, transitions are session
defined, and the transport stage knows nothing about packets. A protocol 775
session adds a configuration state and its own login packets without touching
the stream, the transport stage, or the cryptography.

`MASTER_PLAN.md` moves the configuration and play transition bullet from M2 to
M4 and records the reason: hand-written 775 login packets would be discarded
when M4 generates them, and there is no generated 775 data to validate them
against today.

## Transport stage

`NewStream` wraps the validated `Transport` in a stream-owned conduit. The
conduit holds:

- A `bufio.Reader` over the raw transport reader.
- An optional decrypt `cipher.Stream`.
- An optional encrypt `cipher.Stream`.
- One mutex covering all three.

`readPump` no longer creates its own `bufio.Reader`. It reads frames from the
conduit. `writePump` writes frames through the conduit. The pre-frame hook
receives the conduit reader, so the legacy `FE 01` path is unchanged.

The conduit transforms bytes as it hands them out, not as it buffers them. A
read pump blocked waiting for bytes therefore picks up a cipher installed while
it was blocked, which is the ordinary client case: the pump is parked on the
next frame length while the coordinator applies the switch.

Enabling the cipher additionally asserts that the buffer holds no unread
ciphertext. A peer that sent bytes past the switch point produces
`ErrEncryptionOverrun` and terminates the stream. Corruption becomes a named
error at its cause instead of an unexplained framing failure one frame later.

Buffered-byte accounting is unchanged. The conduit buffer replaces the read
pump buffer, and CFB8 transforms in place, so encryption adds no new buffer.
This is what the M1 note anticipated.

## Control routing

```go
// TransportControl is a control that reconfigures the byte stream below
// framing. The stream routes it to its conduit instead of its session.
type TransportControl interface {
	Control
	ApplyTransport(*Conduit) error
}
```

`processControl` validates and applies a `TransportControl` against the conduit
and everything else against the session, as today. The stream matches on the
interface, never on the concrete type, so it still does not interpret control
contents.

`Stream.Snapshot` merges the conduit's pipeline entries into the session
snapshot. It reports `encryption.enabled` and `observations.disclosure`
alongside the existing compression entries.

## Why the session does not propose encryption

Compression is proposed by the session, because the set-compression packet
carries everything the control needs. Encryption is not, because no packet
carries the plaintext shared secret. The clientbound packet carries the server
key and a verify token. The serverbound packet carries the secret encrypted to
that key. The plaintext exists only in the client that generated it and in the
server that holds the private key.

Encryption therefore enters through `Stream.Control`, called explicitly by the
negotiator or by the developer. Ordering is already guaranteed: `Write` returns
only after `writePump` reports the frame written, and `Control` queues to the
same coordinator, so the control cannot apply before the response bytes are on
the wire.

This also means M2 adds no transition rule and no control case to the code
generator. The only generated change is the sensitivity marker described below.

## Java cryptography

`wire/java` gains:

```go
// SharedSecret is a Java Edition session key. Its formatting methods always
// redact, so it cannot reach a log or an error by accident. Reveal is the
// only way to read the bytes.
type SharedSecret struct{ /* unexported */ }

func NewSharedSecret() (SharedSecret, error)
func (SharedSecret) Reveal() []byte
func (SharedSecret) String() string
func (SharedSecret) GoString() string
func (SharedSecret) Format(fmt.State, rune)

func ParseServerPublicKey([]byte) (*rsa.PublicKey, error)
func EncryptToServerKey(*rsa.PublicKey, []byte) ([]byte, error)
func ComputeServerHash(serverID string, secret SharedSecret, key *rsa.PublicKey) (ServerHash, error)

type EncryptionControl struct{ Secret SharedSecret }
```

`NewSharedSecret` draws sixteen bytes from `crypto/rand`.
`ParseServerPublicKey` accepts the DER `SubjectPublicKeyInfo` encoding the
server sends. `EncryptToServerKey` uses PKCS#1 v1.5, which is what Java uses.
`ComputeServerHash` is SHA-1 over the server ID, the secret, and the encoded public
key, rendered as Java renders it: a negative digest is printed as the negation
of its twos complement, with no zero padding.

`EncryptionControl` implements `TransportControl`. `ApplyTransport` builds one
AES-128 CFB8 stream per direction with the initialization vector set to the key
itself, as the Java client does, and installs both under the conduit mutex.

## Inbound validation

Every value the login sequence takes from the peer is validated before it is
used, and validation failures are typed. A login is the one exchange where the
peer is entirely unauthenticated, so it is the wrong place to trust a field
because it usually looks right.

`java.UUID` already exists as a sixteen-byte array, but nothing parses or
renders one. Protocol 47 carries the login success UUID as a dashed string,
while the Mojang session server returns it undashed, so a string is the wrong
type to hold across that boundary. This slice adds:

```go
func ParseUUID(text string) (UUID, error)
func (UUID) String() string   // dashed, lowercase
func (UUID) IsZero() bool
```

`ParseUUID` accepts the dashed thirty-six character form and the undashed
thirty-two character form, rejects everything else with `ErrInvalidUUID`, and is
case insensitive. `Profile.UUID` is a `java.UUID`, so a malformed UUID fails at
the packet that carried it rather than somewhere downstream in `headless-
minecraft`.

Two more values get types for the same reason:

```go
type Username struct{ /* unexported */ }
func ParseUsername(text string) (Username, error)
func (Username) String() string
func (Username) IsZero() bool

type ServerHash struct{ /* unexported */ }
func ComputeServerHash(serverID string, secret SharedSecret, key *rsa.PublicKey) (ServerHash, error)
func (ServerHash) String() string
func (ServerHash) IsZero() bool
```

Both hold an unexported field, following `SharedSecret`. A defined string type
would be convertible, and `Username("bad\nname")` compiling anywhere would make
the validation a convention rather than a guarantee. A one-field struct is
still comparable and still usable as a map key, so nothing is lost.

`Username` enforces the rules that hold everywhere: non-empty, at most sixteen
bytes, valid UTF-8, and no control characters. It deliberately does not enforce
the `[a-zA-Z0-9_]` charset that Mojang applies to new accounts. Offline-mode and
modded servers legitimately issue names outside it, and rejecting them breaks
real connections while preventing nothing.

`ServerHash` is not validated inbound at all, because it is an output rather
than an input. It gets a type because of the signature it appears in:

```go
Verify(ctx context.Context, username Username, hash ServerHash) (Profile, error)
```

As two adjacent strings, swapping the arguments compiles, survives review, and
fails at runtime as an authentication error that looks like a rejected account.
As two types, it does not compile. The value already comes from one
constructor, so the type costs nothing.

The line between a type and an inline check: a value gets a type when it
crosses an API boundary or is stored, and an inline check when it is consumed
in the function that received it. That keeps `serverID` a bounded string, since
it is read from the encryption request and folded into the hash three lines
later without ever escaping, and it keeps the disconnect reason untyped, since
nothing parses it.

The negotiator applies the same rule to the rest of the login state:

| Field | Rule | Failure |
| --- | --- | --- |
| Encryption request public key | Non-empty, parses as RSA | `ErrInvalidServerKey` |
| Encryption request server ID | At most twenty characters | `ErrInvalidLoginField` |
| Encryption request verify token | Non-empty, at most the frame limit | `ErrInvalidLoginField` |
| Login success username | Parses through `ParseUsername` | `ErrInvalidUsername` |
| Login success UUID | Parses through `ParseUUID` | `ErrInvalidUUID` |
| Disconnect reason | Used only in an error string, never parsed | none |

The username bound is the protocol's own, not a guess. A server that sends a
longer one is out of spec, and accepting it would put an unbounded
peer-controlled string into a profile that consumers treat as identity.

Server-side, the same rules apply to the client's login start username and to
the decrypted verify token, which must equal the token the server sent.

## Login negotiator

```go
// Profile identifies the account a login presents. Both fields are types that
// cannot hold an invalid value, so a profile is proof that validation ran.
type Profile struct {
	Name java.Username
	UUID java.UUID
}

// Authenticator proves account ownership during login. An offline
// authenticator returns its profile and does nothing in Join.
type Authenticator interface {
	Profile() Profile
	Join(ctx context.Context, hash java.ServerHash) error
}
```

The negotiator needs concrete login packet types, so it lives in its own
package, `login`, which imports `wire/java` and `generated/java/v1_8`. Putting
it in `wire/java` would invert the dependency, because the generated package
imports `wire/java`.

For M2 the negotiator is protocol 47 only. Protocol 775 changes the login
packets themselves, adding a UUID to login start and an acknowledgement before
the configuration state, so generalizing it now would mean designing against
packets that do not exist yet. M4 either parameterizes it or adds a second
constructor. Nothing else in this slice depends on that choice: the conduit,
the controls, and the cryptography never see a packet.

`LoginNegotiator` is a helper, not a stream mode. Given a running stream and an
authenticator it writes `LoginStart`, reads until it sees `EncryptionBegin` or
`Success`, and on `EncryptionBegin` it parses the server key, generates a
secret, computes the server hash, calls `Join`, writes the encrypted secret and
verify token, and then calls `Stream.Control` with the `EncryptionControl`.

It handles the compression packet by doing nothing: the session already
proposes that transition and the stream already commits it.

The negotiator calls `Stream.Read`, so it owns inbound delivery for the
duration of the login. A caller that reads concurrently would steal packets the
negotiator needs. `Negotiate` therefore takes the stream, blocks until login
finishes or fails, and returns the profile the server confirmed. The caller
resumes reading afterwards. This is documented on the method, and it is the
main reason the negotiator is a helper the caller drives rather than a
background goroutine.

A developer who wants control over any step uses the exported primitives and
the existing `TransitionPolicy` hook instead, and never constructs a
negotiator. Nothing in `Stream` knows the negotiator exists.

Server-side verification is the mirror interface. The consumer supplies it,
because it makes the network call:

```go
type Verifier interface {
	Verify(ctx context.Context, username java.Username, hash java.ServerHash) (Profile, error)
}
```

## Secret containment and disclosure

Two mechanisms, because they fail differently.

Type-level containment is absolute and not configurable. `SharedSecret` always
redacts when formatted. The conduit reads the bytes through `Reveal`. Leaks
come from formatting a value into a log line or an error, not from a deliberate
`Reveal` call, so making the formatting methods conditional would make the
dangerous path unpredictable without helping a debugger.

Observation redaction is the part that toggles.

```go
// WithSecretDisclosure turns off redaction of secret material in
// observations. A capture then contains the session key and the full
// key-exchange bodies, which makes it as sensitive as the account itself.
// reason must be non-empty and is recorded, so a capture states why it is
// unredacted.
func WithSecretDisclosure(reason string) StreamOption
```

Sensitivity is reported by an optional session interface, because the session
is the only component that knows what a packet is, and because the outbound
path observes a packet the caller constructed rather than one the session
returned:

```go
// SensitivePackets reports packets whose bodies must be redacted in
// observations. A session that does not implement it has no sensitive
// packets.
type SensitivePackets interface {
	Sensitive(Packet) bool
}
```

The stream matches on the interface, never on a packet type, so the rule stays
version neutral: a 775 session marks its own packets with no change here. The
v1_8 template gains one method listing `LoginClientboundEncryptionBegin` and
`LoginServerboundEncryptionBegin`. `protocol.Packet` and `protocol.Session` are
unchanged.

By default, packet records for a sensitive packet carry a length and a marker
instead of a body. Raw frame records are never redacted in either mode: they are
exactly what crossed the wire, and at the key-exchange step that is RSA
ciphertext, not a key. Preserving them keeps framing analysis and replay
possible without exposing anything.

Under disclosure, sensitive packet records carry their real bodies, and
applying an `EncryptionControl` emits one additional record carrying the
session key. That record is what makes an encrypted capture decryptable, which
is the reason the escape hatch exists at all.

The disclosure hook is two methods, because the stream needs them at different
times:

```go
type SecretDisclosure interface {
	SecretLabel() string
	DisclosedSecret() []byte
}
```

`SecretLabel` is called on every switch, so a redacted capture still states
what kind of secret was installed and when. `EncryptionControl` returns
`"java.session-key"`. `DisclosedSecret` is called only under disclosure, so the
default path never materializes a key it would immediately discard, and it
returns a copy the caller may retain.

A single method returning both would be smaller, but it would read the key on
every connection in order to throw it away. Splitting keeps the default path
free of secret material entirely, which is the property this section exists to
guarantee. The stream interprets neither value: it copies both into the record.

The label is not a wrapper type, and deliberately so. `Username` and `UUID`
earn types because they have validity rules and a confusion hazard; disclosed
material has neither, and the stream is specifically forbidden from
understanding it, so an opaque `[]byte` is the honest representation. What the
bytes did lack was a discriminator. `ObservationSecret` records reach durable
capture files in M5, and a discriminator cannot be added retroactively to
captures already written, so it goes in now while there is still only one kind
of secret to name.

`Observation` gains a `Redacted bool` field, set per record rather than
inferred from stream configuration. A sink or a capture file never has to guess
whether it holds a real body or a placeholder.

## Error model

New sentinel errors, following the existing convention:

- `ErrEncryptionOverrun`: a peer sent bytes past the encryption switch point.
- `ErrEncryptionEnabled`: a second `EncryptionControl` reached an already
  encrypted conduit. Java rekeying does not exist, so this is a bug in the
  caller.
- `ErrInvalidServerKey`: the server key is unparseable or is not RSA.
- `ErrVerifyTokenMismatch`: the server-side verify token did not match.
- `ErrAuthenticationRejected`: the authenticator refused, wrapping its cause.

An error that carries a `SharedSecret` prints it redacted, because the type
formats itself.

## Verification

### Deterministic Go tests

- CFB8 encrypt and decrypt round-trips, byte at a time and in large writes, and
  cross-checks against fixtures captured from a Java implementation.
- `ComputeServerHash` against the three canonical vectors: `Notch`, `jeb_`, `simon`.
- Server key parsing, including malformed DER and non-RSA keys.
- Enabling encryption mid-stream: frames before the switch decode in the clear,
  frames after decode through the cipher.
- `ErrEncryptionOverrun` when a peer writes past the switch point.
- Redaction: `SharedSecret` formats redacted through `%v`, `%s`, `%#v`, and
  inside a wrapped error; sensitive packet records carry markers by default and
  bodies under disclosure; `Redacted` is truthful in both modes.
- Login failure modes: authenticator rejection, authenticator timeout, context
  cancellation at each step, a login-state disconnect packet, and a peer that
  closes mid-exchange.
- Race detector across the switch point, with a read pump blocked when the
  cipher is installed.

### Local TCP tests

A loopback pair completes an encrypted login and exchanges play packets,
including a compressed and encrypted frame, which is where envelope ordering
breaks if it is wrong.

### Community interoperability

The pinned Node `minecraft-protocol` lane gains encrypted sessions in both
directions. Neither contacts a host outside 127.0.0.1.

A Node client against a Go server needs nothing new. Version 1.66.2 answers
`encryption_begin` whether or not it holds credentials; without them it skips
the join call and sends the response anyway. The Go server supplies a test
verifier that accepts.

A Node server against a Go client needs one stub. In 1.66.2, `server/login.js`
gates the encryption request and the `hasJoined` call on the same flag, so
there is no configuration that encrypts without verifying. The interop runner,
which is our own file, replaces `yggdrasil.server` before creating the server so
`hasJoined` returns a fixed profile. `server/login.js` calls it through the
module object at connection time, so replacing the export is enough. This is
confined to the interop runner and never ships.

Both lanes validate the cipher and the packet mechanics, which is what M2 owns.
Real account flows belong to the consumer that implements them, and reach the
test suite in M6.

## Alternatives considered

### Encryption inside the session

Rejected. The session is documented as performing no I/O, and the cipher covers
the frame length prefix, which the session never sees. Placing it there would
require the stream to re-read a transport codec from the session after every
control.

### Caller-installed encryption unit

Rejected. It gives maximum control but pushes the same sequencing into the
headless client, the server, and the proxy, where it would drift.

### Automatic login inside the stream

Rejected. It would make the stream interpret packets, which M1 deliberately
avoided, and it hides the failure modes this milestone exists to test.

### Unbuffered reads through the cipher

Rejected. It removes the read-ahead question by construction, but frame length
VarInts would cost several syscalls per frame on the play state hot path. The
conduit reaches the same correctness by buffering ciphertext instead of
plaintext.

### Redaction at the sink

Rejected. It keeps maximum fidelity, but every consumer must remember to
redact, and the durable capture sinks planned for M5 would write session keys
to disk by default.

## Completion criteria

- A client stream completes an encrypted protocol 47 login against a local
  server and exchanges play packets.
- A server stream completes the same exchange with a supplied verifier.
- Compression and encryption compose in both directions.
- Secrets are redacted by default, disclosed only through
  `WithSecretDisclosure`, and every observation states which it is.
- The full release gate passes, including the encrypted Node interoperability
  lane and the race detector.
- `MASTER_PLAN.md` records M2 complete and moves the configuration-state bullet
  to M4.
