# Changelog

This file records notable user-visible changes. It follows [Keep a Changelog](https://keepachangelog.com/en/2.0.0/) and [Semantic Versioning](https://semver.org/).

## Unreleased

### Fixed

- Network NBT no longer requires a compound root. The plain-text form of a text
  component is a bare `TAG_String`, and a real server sends it that way — Paper
  26.1 sends its MOTD in `server_data` as a root string. The old rule rejected
  that packet, and would have rejected every chat message, kick reason, and
  title whose component was plain text. Found by capturing ten seconds of play
  against a live server, which the reader could not get past.
- Raw frame observations no longer carry the bytes of a packet whose decoded
  body is redacted. The raw record is written before the frame is decoded, so
  the packet-level check could not answer for it, and a capture taken with the
  default settings would have held the login key exchange in the clear beside
  a record marked redacted. `protocol.SensitiveFrames` is the session-side
  answer, decided from the frame's own packet ID.

### Added

- Java Edition 26.1, protocol 775. `generated/java/v26_1` carries 256 framed
  packets across five states — handshaking, status, login, configuration, and
  play — the typed game data, every source dataset as the bytes upstream
  published through `v26_1.Raw()`, and a checked-in `coverage.json` naming
  every packet a codec exists for. `generated/java/current` aliases the newest
  supported version and promises nothing across releases.
- The modern login sequence. `protocol.LoginExchange` is the per-version
  exchange that builds and reads a login's packets, and `login.Negotiator` now
  drives both protocol 47, whose login ends at success, and protocol 775, whose
  login passes through configuration — with no version named in `login/`.
  `login.WithTerminalState` stops the sequence early, which is how a caller
  reads what a server sends in configuration.
- `java.NewNetworkNBTText`, which builds a literal text component as network
  NBT. Disconnect reasons after login are components rather than JSON strings.
- `mcdata-gen -raw` and `-coverage`, and the `generate:v1_8`,
  `generate:v26_1`, `test:protodef`, and `check:live` tasks.
- A codec-level differential suite against pinned Node ProtoDef, and an opt-in
  live check against a real Java 26.1 server. The check has been run against
  Paper 26.1.2 build 74: the largest raw frame was 12,564 bytes and the largest
  decoded body 32,316 bytes, against limits of 2 MiB and 8 MiB.
- `protocol.Observation.Elapsed`, stamped where the record is made rather than
  where it is delivered, so a sink that falls behind cannot rewrite the timing
  of the connection it was watching.
- `protocol.ObservationRejected`, the stage for a write the stream accepted and
  then refused before any byte left the process. It is the one stage that
  describes the consumer rather than the connection.
- `protocol.PacketDescriptor`, which resolves packet names and IDs for a
  protocol without creating a session or loading game data. The generated
  descriptors implement it.
- `middleware` and `router`: ordered send and receive chains, and a dispatch
  table registered by packet name or ID. Neither imports the stream — the one
  file that names it is `router/adapter.go`.
- `capture`: a versioned capture format, a durable file sink, sink composition,
  and a replay digest. A capture is a JSON header followed by CRC-checked,
  length-prefixed binary records with an inline string table, written straight
  from the observation path so a killed process leaves a readable file.
- `history.Ring`: bounded in-memory observations, the one sink allowed to lose
  data.
- `replay`: deterministic replay of a capture, offline through a session or
  connected to a peer, with timing modes and a reported digest, drift, and any
  divergence between the recorded connection and what this code proposes.
- `protocols`: resolves a protocol ID to a protocol, for consumers that
  deliberately want every version.
- `protocol.PacketFactory`, which builds an empty packet value for one identity
  so a tool can decode text into it.
- `mcproto serve`, a verification harness that replays a captured server at a
  real client and decodes everything the client sends back, and
  `mcproto capture --play-for`, which keeps reading after the login so a
  capture holds play traffic rather than stopping where play begins.
- `mcproto version`, `packet`, `status`, `login`, `capture`, `inspect`, and
  `replay`, with documented exit codes: 3 for a failure that belongs to the
  peer, 4 for a check that ran and did not match.
- `protocol.Observation.OriginalLen`, the size a record describes whether or
  not it carries the bytes, so a redacted record can state what it withheld.
- `protocol.RoleKnownPacks`, the data-pack negotiation a modern configuration
  opens with. A 26.1 server sends no registry data and never finishes
  configuration until the client answers it, so `login.Negotiator` answers it
  with an empty pack list — which is also what makes the server send every
  registry entry rather than assume the client shipped with a copy.
- Java 1.8 physics constants: block slipperiness, the trigonometry table, and
  entity motion constants for the player, dropped item, and arrow families,
  reachable through `data.Set.Physics`.

## 0.1.0 - 2026-08-15

First tagged release. It publishes the managed stream, transport encryption,
both halves of the Java Edition login sequence, schema-first code generation,
and the protocol 47 packet and game-data packages that `server` consumes from
M3 onward.

Nothing in this repository was published before this tag, so the `Changed` and
`Removed` entries below record decisions taken during development rather than
migrations a caller has to perform.

### Added

- Added `Buffer.EnterNested`, `Buffer.LeaveNested`, and `Buffer.NestingDepth`,
  which bound decode recursion against `MaxRecursionDepth`. Generated decoders
  for shared named types count depth, so a recursive schema cannot be driven
  into a stack overflow by a peer.
- Added `Buffer.ReadTerminator` and `Buffer.WriteTerminator` for loops that end
  at a sentinel byte. The sentinel is a parameter taken from the schema rather
  than a constant, because protocol 47 ends entity metadata at 127 and protocol
  775 ends it at 255.
- Added AES-128/CFB8 transport encryption. `protocol.Conduit` sits between the
  transport and both pumps, buffering raw bytes and transforming them as it
  hands them out, which is what makes a mid-stream cipher switch safe. It
  refuses a switch while the read buffer still holds bytes the peer sent early,
  reporting `protocol.ErrEncryptionOverrun` rather than corrupting the next
  frame. The mode is CFB8, with an eight-bit segment, not the block-wide CFB in
  the standard library.
- Added `protocol.TransportControl`, a control a stream applies to its conduit
  instead of its session, and `java.EncryptionControl`, which implements it.
  Encryption is applied through `Stream.Control` rather than proposed by a
  session, because no packet carries the plaintext key.
- Added the Java key-exchange primitives: `java.ParseServerPublicKey`,
  `java.EncodeServerPublicKey`, `java.EncryptToServerKey`,
  `java.DecryptFromServerKey`, and `java.ComputeServerHash`, which renders the
  digest the way Java renders a signed BigInteger.
- Added the server half of the key exchange: `java.DecryptSharedSecret`
  recovers a client's session key as a redacting `java.SharedSecret`, refusing
  a key of the wrong length before it can become one, and `java.VerifyToken`
  decrypts the returned verify token and compares it with `crypto/subtle`. A
  token of the wrong length fails with `java.ErrVerifyTokenMismatch` before any
  content is compared, so the length is not an oracle that a mismatch is not.
- Added strict identity types for login. `java.ParseUUID` accepts the dashed
  and undashed wire forms and nothing else, `java.ParseUsername` bounds bytes
  rather than runes and rejects control characters, and `java.ServerHash` is a
  distinct type so it cannot be transposed with a username.
- Added `java.SharedSecret`, which redacts under every formatting verb.
  `Reveal` is the only way to read the key.
- Added observation redaction. `Observation.Redacted` reports a withheld body,
  the new `ObservationSecret` stage marks the encryption switch point,
  `Observation.Secret` names the kind of material, and
  `protocol.WithSecretDisclosure` turns redaction off for a stated reason. The
  generated protocol 47 session marks both key-exchange packets sensitive
  through the optional `protocol.SensitivePackets` interface.
- Added `protocol.LoginRole` and the optional `protocol.LoginRoles` interface.
  The generated session reports which part of a login each packet plays, so a
  later protocol is tagged rather than special-cased.
- Added the `login` package: `login.Negotiator` drives the client half of the
  protocol 47 login sequence, `login.Offline` is the authenticator for a server
  that does not verify accounts, `login.Authenticator` and `login.Verifier` are
  the two halves a consumer implements, and `login.Profile` holds only parsed
  identity types.
- Added `login.Acceptor`, the server half of the login sequence, with
  `login.WithVerifier`, `login.WithCompressionThreshold`, and
  `login.WithServerID`. Without a verifier the login is offline and
  `login.OfflineUUID` derives the same identity vanilla does. With one, the
  acceptor runs the key exchange, installs the cipher before it calls the
  verifier so no session-server call happens over plaintext, and disconnects
  with a readable reason when the verifier refuses. Both halves are tested
  against each other over one connection, which is why they share a package.
- Added the sentinel errors `protocol.ErrEncryptionOverrun`,
  `protocol.ErrEncryptionEnabled`, `protocol.ErrEncryptionUnavailable`,
  `java.ErrInvalidSharedSecret`, `java.ErrInvalidUUID`,
  `java.ErrInvalidUsername`, `java.ErrInvalidServerKey`,
  `java.ErrVerifyTokenMismatch`, and the `login` package's own set.
- Added encrypted interoperability scenarios in both directions against the
  pinned Node `minecraft-protocol` 1.66.2.
- Added the asynchronous managed stream. `protocol.Stream` runs a read pump and
  a write pump over `io.Reader` and `io.Writer` while one coordinator orders
  every state change, control, observation, and shutdown step at complete frame
  boundaries. Construction performs no I/O and starts no goroutine. `Read`
  waits for the next decoded packet, `Write` returns only after the write pump
  writes the complete frame, and `Wait` reports a stable first fatal cause.
- Added bounded Java Edition compression. `java.CompressionControl` configures
  the envelope, and `java.StrictCompression` and `java.CompatibleCompression`
  differ only in how strictly they hold a peer to the threshold. No policy can
  relax the frame limit, the decompressed limit, exact decompressed length,
  zlib validity, trailing-data rejection, or allocation safety.
- Added protocol 47 runtime transitions for handshake next-state, login
  success, and set compression, together with state-appropriate disconnect
  packets for server-role login and play sessions. A
  `protocol.TransitionPolicy` can accept, replace, or suppress any proposal.
- Added runtime controls: `Stream.SetState`, `Stream.Control`, and
  `Stream.Snapshot`. Cancelling before the coordinator accepts a control
  guarantees nothing changed.
- Added graceful shutdown. `Stream.Shutdown` stops accepting writes, drains the
  write in flight, sends the disconnect packet where the role and state have
  one, and then interrupts the transport. `Stream.Close` remains an immediate
  abortive close. Both are idempotent.
- Added the opt-in legacy `FE 01` pre-frame hook through
  `protocol.WithPreFrameHook` and `java.NewLegacyPingHook`. A declining hook
  leaves every inspected byte buffered for the framer.
- Added lossless observation points through `protocol.WithObservationSink`,
  with raw-frame and packet stages, a stream-wide sequence, frame correlation,
  before and after snapshots, and owned bytes.
- Added `protocol.MaxBufferedBytes`, a shared byte budget for everything a
  stream retains. It defaults to 32 MiB with a 1 GiB hard ceiling, and
  construction rejects a configuration that cannot hold one maximum frame plus
  one maximum decompressed body.
- Added `java.NewFramer`, `java.SplitPacketBody`, and `java.JoinPacketBody`,
  which separate Java framing from packet envelopes.
- Added loopback TCP scenarios and interoperability tests against pinned Node
  `minecraft-protocol` 1.66.2, wired into a required `task test:interop` gate.

### Changed

- **Breaking.** The generator now compiles every type a schema defines and
  reserves hand-written codecs for names the schema declares `native`. Protocol
  47's `position`, `slot`, and `entityMetadata` were schema-defined types being
  resolved to hand-written codecs by name, which would have given protocol 775
  protocol 47's bit order and metadata terminator. Every protocol 47 wire byte
  is unchanged; the generated Go API is not.
- **Breaking.** Removed `java.Position`, `java.Slot`, `java.EntityMetadata`,
  `java.EntityMetadataEntry`, `java.EntityMetadataType`, their buffer methods,
  and the `ErrInvalidSlot`, `ErrInvalidMetadata`, and
  `ErrDuplicateMetadataIndex` sentinels. The generated package declares these
  types itself, compiled from the schema. A consumer that held a `java.Slot`
  now holds the generated `Slot` for its protocol version.
- A named type used by two or more packets, or that is recursive, is generated
  once and shared instead of being inlined per packet. Protocol 47 shares
  `Position`, `Slot`, and `EntityMetadata`.
- `Stream.Snapshot` now reports `encryption.enabled` alongside the compression
  settings. It merges the conduit's view with the session's, so one snapshot
  describes everything a caller can configure at runtime.
- Bumped the pinned Go toolchain to 1.26.6. Parsing an untrusted server public
  key reaches `encoding/asn1`, whose recursion-depth fix landed in that patch
  release.
- Replaced `protocol.Codec` with `protocol.Session`, and `Protocol.NewCodec`
  with `Protocol.NewSession`. There is no compatibility adapter. A session owns
  packet coding and pipeline state and performs no I/O; a stream owns the
  transport. A running stream owns its session exclusively, so callers use
  `Stream.Snapshot` instead of reading the session directly.
- `java.ReadRawPacket` and `java.WriteRawPacket` keep their exported behavior
  but are now thin helpers over the framer and packet-body functions. A frame
  length encoded in more bytes than it needs is now rejected, and a length
  VarInt truncated after its first byte now reports `io.ErrUnexpectedEOF`
  rather than `io.EOF`, so a truncated frame cannot look like a clean close.

- Initial repository structure, bounded protocol contracts, immutable raw datasets, Devbox tooling, CI, and a tracked pre-commit hook for lint and secret scanning.
- Exported the `wire/java` `PacketValue` compatibility interface; VarInt,
  VarLong, fixed-width field, UUID, packed-position, bounded string, and bounded
  byte-array helpers; reflection-based `Marshal` and `Unmarshal`; packet-level
  `ReadPacket` and `WritePacket`; and uncompressed `protocol.Packet` frame I/O.
  Variable-length values require validated `protocol.Limits`.
- Added typed game-data values, caller-owned registry lookup contracts, immutable `Set` construction, raw-dataset lookup, and concurrent version registration and loading.
- Added pinned Java 1.8 source data with exact upstream revision and license
  provenance, the atomic `mcdata-gen` generator, generated registries and packet
  values, and registration under `java/1.8.9`. The data model preserves entity
  and biome collisions, nullable recipe output shapes, and optional fractional
  drop counts. Legacy server-list ping remains in the protocol summary but is
  not exposed through the normal framed packet API; supporting it requires a
  separate pre-frame transport path.
- Added the built-in `java/1.8.9` protocol 47 descriptor, explicit connection
  states, generated reflection-free packet codecs, concrete packet factories,
  and checked-in compatibility bytes for handshake, status, login, and play.
  Unknown packet bodies remain caller-owned. Compression, encryption, managed
  streams, and the complete login lifecycle remain later work.
