# Changelog

This file records notable user-visible changes. It follows [Keep a Changelog](https://keepachangelog.com/en/2.0.0/) and [Semantic Versioning](https://semver.org/).

## Unreleased

## 0.7.1 - 2026-08-18

### Fixed

- A write the peer already has is reported as written. `Write` returned
  `ErrStreamClosed` for a frame the transport had taken when the connection
  ended before the call returned — because the write pump's result and the stop
  become ready in the same instant, where a select picks between ready cases at
  random, and because the frame's observation could not be charged to a budget
  that closes with the transport. Both told a caller to send again something the
  peer already had. The outbound side now follows the rule the inbound side
  does: what a closing stream cannot record is the record.

## 0.7.0 - 2026-08-18

### Added

- `login`: `Acceptor` serves a protocol 775 login. It named `generated/java/v1_8`
  at ten call sites and now names no version at all, driving both protocols
  through `protocol.LoginExchange` the way `Negotiator` already did. A 775 login
  does not end at success: the client's acknowledgement moves both sides to
  configuration, and the finish handshake and its answer are what reach play.
  The two halves are now tested against each other on both protocols over one
  connection, offline, encrypted, and compressed.

- `login`: `WithConfiguration` sets the step an acceptor runs while a
  connection is in a configuration state. A real client will not leave that
  state until it has registries, tags, and data packs, none of which is
  protocol, so the acceptor opens the state and hands it to whoever owns the
  content. The default sends nothing, which is enough for a client that answers
  what it is sent and is not enough for a vanilla one.

- `protocol.LoginExchange` gains the server half of the sequence:
  `ReadLoginStart`, `WriteEncryptionRequest`, `ReadEncryptionResponse`,
  `WriteSetCompression`, `WriteLoginSuccess`, `WriteLoginDisconnect`, and
  `Announce`. The interface previously declared only a client's methods, which
  is why the server half had to reach past it. Both generated protocols
  implement all seven.

  This is a breaking change for anything outside this module that implements
  `protocol.LoginExchange`; a version package generated from these templates
  gets the methods for free.

- `data.BlockMovementRegistry` gains `FallsByState`, `FallsByID`,
  `ClimbableByState`, and `ClimbableByID`, and both built-in Java profiles
  answer them. Whether a block is pulled down when undermined and whether a body
  can climb its column are two facts nothing else in the stack supplies: a route
  that digs has to know the first before it digs, and no collision shape carries
  the second, because a ladder's box is empty and reads as air.

  Both are measured out of the same Mojang jars the movement fact comes from,
  in the same extraction pass, and ride in the same `blockMovement.json` records
  rather than on `data.Block` — upstream publishes neither, and a fact measured
  from a jar belongs with the measurement whose provenance the manifest records.
  Neither is derivable from what is already published: soul sand shares
  `Material.sand` with gravel and does not fall.

  The two versions state the facts in different places and each is read where
  its own game reads it. 1.8.9 names the falling class and the two climbable
  blocks directly; 26.1.2 has a falling class and a climbable block tag, and
  lists nine blocks rather than two. Both are per block, so unlike `ByID` the
  new accessors never decline because a block's states disagree.

  This is a breaking change for anything outside this module that implements
  `data.BlockMovementRegistry`; a version package generated from these templates
  gets the methods for free.

### Fixed

- A packet that arrives as the connection ends reaches the reader. A peer that
  kicks writes its disconnect and closes, so the frame and the EOF behind it
  land together: the read pump hands the frame over and then reads the EOF,
  which stops the stream and closes the shared budget while the coordinator is
  still holding the decoded packet. Neither its observation record nor its queue
  slot could be charged to a closed budget, and the packet was discarded — so a
  reader saw a bare EOF and lost the one packet that says why the connection
  ended. Teardown now drops the observation and keeps the packet: the reader
  gets it, and `Read` drains what is queued on the termination path as well,
  because a delivery and a termination can become ready in the same instant and
  a select picks between ready cases at random. A reservation still waiting for
  capacity when the budget closes no longer costs its packet either.

## 0.6.0 - 2026-08-18

### Fixed

- `wire/java`: `LPVec3` now reads and writes the byte order vanilla uses, so
  every velocity a real 26.1.2 server sends decodes to the velocity it sent.
  The upper thirty-two bits of the packed vector are big endian on the wire --
  what Netty's `writeByte, writeByte, writeInt` produces -- and this package
  wrote and read them little endian. Because it was wrong in both directions
  the round-trip tests passed, while an entity's spawn or velocity packet
  yielded a plausible number unrelated to its motion: an arrow summoned with
  `Motion:[0.1d,0.0d,0.0d]` decoded as `{0.600, 0.994, 0.992}` and now decodes
  as `{0.100, 0, 0}`. A new test reads velocity fields captured from a pinned
  vanilla server, which is the only shape of test that could have caught it.

- `login`: an online login refused by the server now reports the reason it was
  given rather than a corrupt stream. The client wrote its encryption response
  and only then installed its cipher, and the server begins encrypting the moment
  it reads that response -- so a server that replies immediately, which is what
  one refusing a login does, landed ciphertext in the window between. The read
  pump handed those bytes out as plaintext and the stream failed on a nonsense
  frame length; the peer's close then failed the cipher switch on the way out,
  replacing the reason with the noise that followed it. Measured at 19 failures
  per 300 runs against a real server before, and 0 after.

### Added

- `java/26.1`: the physics dataset now carries item and arrow motion constants
  beside the player's, so a consumer can move all three families. They are
  transcribed from `ItemEntity` and `AbstractArrow` and confirmed in bytecode,
  the way 1.8.9's twelve were. Three of them differ from 1.8.9 in a way a shared
  number would get wrong: both gravities are `double` literals here and `float`
  literals there (0.04 against 0.03999999910593033, 0.05 against
  0.05000000074505806), and the item's vertical drag became a `double` 0.98
  while the horizontal drag in the same statement stayed the `float`
  0.9800000190734863.
- `Conduit.EnableReadEncryption` and `Conduit.EnableWriteEncryption` install one
  half of a cipher switch, and `java.EncryptionControl` takes a `Half` field
  selecting `EncryptionInbound`, `EncryptionOutbound`, or -- as the zero value --
  both. The two halves of a Java login fall due at different moments: the inbound
  one before the encryption response is written, because the server starts
  encrypting as soon as it reads it, and the outbound one after, because that
  response frame is the last thing sent in the clear. `EnableEncryption` still
  installs both at once and is unchanged for callers that can switch together.
  The inbound half keeps the unread-bytes check that guards a part-read frame;
  the outbound half has no read buffer to check.

## 0.5.0 - 2026-08-18

### Added

- `generated/java/v26_1`: `Physics()` answers for Java 26.1.2 —
  `defaultSlipperiness`, all 1,168 blocks' slipperiness, the 65,536-entry
  trigonometry table, and the player's four motion constants — measured from a
  verified Mojang server jar and pinned in the `extracted` block of
  `source/java/26.1/manifest.json` beside that version's block measurement.
  Until now only Java 1.8.9 carried physics, so a consumer simulating the modern
  protocol had a world it could see and no constants to move through it with.
  Three of the four constants are `float` values Java widens where it applies
  them, and they are stored widened: the step height is `0.6000000238418579`
  rather than the attribute's round `0.6`, because the game narrows the attribute
  to a `float` where the step-up reads it. A new round-trip test compares every
  generated constant against the pinned document at full precision, so a
  generator that shortened one would fail rather than drift.
  Only the player is recorded. The item and arrow families 1.8.9 carries are not
  measured for this version, because nothing uses them yet.
  The version's trigonometry table is bit for bit identical to 1.8.9's, and it
  is stored again rather than shared: an identical measurement of two versions is
  a fact about both, and sharing one table would be a claim about every version
  after them.

## 0.4.0 - 2026-08-17

### Added

- `data`: `BlockMovementRegistry` answers whether a block stops something
  walking into it, and `Set.BlockMovement` publishes it. Upstream's block data
  says what a block is called, how hard it is, and what it drops, and never
  says whether an entity can occupy its cell — so a consumer holding the state
  identifiers a protocol carries could see a whole world and still not tell a
  wall from a flower. The fact is the game's own material, measured out of a
  verified Mojang server jar and pinned in the `extracted` block of
  `source/java/1.8/manifest.json` beside the physics constants.
  `generated/java/v1_8` publishes it for all 198 blocks; `generated/java/v26_1`
  returns nil, because nobody has measured that jar yet. Nil means the
  measurement is absent, which is not the same as "nothing blocks movement":
  `ByState` and `ByID` return a second result that separates an unknown block
  from a passable one, and a caller must refuse an unknown one rather than walk
  through it.
- `mcproto data validate` reports which datasets were measured from a game jar
  rather than fetched from upstream, alongside the aliases it already reports.

## 0.3.0 - 2026-08-17

### Added

- `wire/java`: `NetworkNBT.Int` and `NBT.Int` read one `TAG_Int` by a path of
  compound keys, returning a second result that separates an absent key from a
  real zero. Both NBT types are retained losslessly and exposed only as bytes,
  which left a caller needing one scalar out of server-sent data with a choice
  between guessing it and writing its own NBT walker — and a chunk column's
  sections cannot be placed in the world without the dimension type's `min_y`,
  which is legitimately zero in the nether. Names are matched in the encoding
  they arrived in, so a key is never decoded to compare it. Only integers are
  readable: a string accessor needs a decoder from Java's modified UTF-8 to a
  Go string, which this package does not have.

## 0.2.0 - 2026-08-16

This release publishes Java Edition 26.1 and protocol 775, the routing,
capture, replay, and history packages, and the `mcproto` command set. Both
halves have been measured against real traffic: a Paper 26.1.2 server and a
vanilla Java 26.1 client.

### Changed

- `protocol.Observation` gained `Elapsed`, `OriginalLen`, and `Rejected`.
  Keyed construction is unaffected; a caller building one with an unkeyed
  composite literal must switch to keyed fields.
- `protocol.Session` implementations may now implement
  `protocol.SensitiveFrames`, `protocol.PacketDescriptor`, and
  `protocol.PacketFactory`. All three are optional: a session that implements
  none behaves as before.

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
  real client and decodes everything the client sends back. A real Java 26.1
  client was driven through it on 2026-08-16: 3,612 packets, none of which
  failed to decode. The record is in
  `docs/verification/2026-08-16-vanilla-client-check.md`. Also
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
