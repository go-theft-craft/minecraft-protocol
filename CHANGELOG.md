# Changelog

This file records notable user-visible changes. It follows [Keep a Changelog](https://keepachangelog.com/en/2.0.0/) and [Semantic Versioning](https://semver.org/).

## Unreleased

### Added

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
