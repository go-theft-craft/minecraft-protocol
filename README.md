# Minecraft protocol and game data for Go

`minecraft-protocol` is a Go toolkit for building Minecraft clients, servers,
proxies, packet analyzers, and custom protocol implementations. It provides
bounded Java wire primitives and typed game-data contracts behind small,
injectable interfaces.

> [!IMPORTANT]
> This project is pre-alpha and has no published release. The `wire/java`
> package implements bounded Java wire primitives, reflection-based `mc` struct
> tags, and uncompressed packet frames. The generated Java 1.8 package provides
> a built-in protocol 47 descriptor and generated packet codecs. The project
> does not provide compression, encryption, an automatic login lifecycle,
> managed streams, or a complete server session.

## Why this repository exists

- Share protocol and game-data code across clients, servers, and proxies.
- Keep Java Edition, Bedrock Edition, and custom protocols from inheriting one
  another's transport assumptions.
- Generate reproducible built-ins from pinned upstream data while allowing
  applications to inject their own implementations.
- Preserve raw packet access and unknown upstream fields for modded servers and
  future protocol changes.

## Support status

| Protocol or data | Status |
| --- | --- |
| Edition-neutral contracts, resource limits, and game-data registry | Implemented |
| Java Edition 1.8, protocol 47 | Built-in descriptor, generated packet sessions, managed asynchronous stream, compression, automatic transitions, graceful disconnect, generated game data, and physics constants implemented |
| Java Edition 26.1, protocol 775 | Generated built-in descriptor, packet sessions, the modern login sequence through configuration, generated game data, and the raw dataset set implemented; not yet verified against a live server |
| Additional PrismarineJS versions and datasets beyond the Java 1.8 bundle | Planned generated built-ins |
| Application-provided protocols and datasets | Supported by the core contracts; adapters remain application code |
| Bedrock Edition | Deferred; it will use a separate transport and authentication implementation |

The detailed dependency order is in the [roadmap](ROADMAP.md). A roadmap entry
is not a compatibility promise or release date.

## Current API

Constructing limits validates every override against a process hard ceiling:

```go
limits, err := protocol.NewLimits(
	protocol.MaxFrameBytes(4<<20),
	protocol.MaxQueueItems(1024),
)
if err != nil {
	return err
}

session, err := selectedProtocol.NewSession(protocol.RoleClient, limits)
```

`selectedProtocol` can be `v1_8.Protocol()` or any application implementation of
`protocol.Protocol`. A session owns packet coding and pipeline state for one
connection and performs no I/O of its own.

A `protocol.Stream` drives a session over a transport. Construction performs no
I/O and starts no goroutine; `Start` does both:

```go
stream, err := protocol.NewStream(session, protocol.Transport{
	Reader:    conn,
	Writer:    conn,
	Interrupt: conn.Close,
})
if err != nil {
	return err
}

if err := stream.Start(ctx); err != nil {
	return err
}

packet, err := stream.Read(ctx)     // waits for the next decoded packet
err = stream.Write(ctx, packet)     // returns after the complete frame is written
err = stream.SetState(ctx, state)   // applied at a frame boundary
err = stream.Control(ctx, java.CompressionControl{
	Enabled:   true,
	Threshold: 256,
	Policy:    java.StrictCompression,
})
snapshot, err := stream.Snapshot(ctx) // current state and pipeline settings

err = stream.Shutdown(ctx, reason)  // drains, sends disconnect, then closes
err = stream.Close()                // abortive close, safe to call repeatedly
err = stream.Wait()                 // first fatal cause, or nil after Shutdown
```

`Transport.Interrupt` must unblock a blocked reader and writer from any
goroutine; `net.Conn.Close` satisfies that. A running stream owns its session
exclusively, so use `Stream.Snapshot` rather than reading the session directly.

## Chunk columns

The generated codecs stop at a chunk packet's column blob, because ProtoDef
describes it as a buffer and the layout inside it belongs to the game version
rather than to the schema. `wire/java/chunk` reads that layout, so a client and
a server do not each keep their own copy of arithmetic that fails silently when
it is wrong.

Splitting is separate from decoding. A joining player receives hundreds of
columns and reads blocks out of very few, so a split walks a column into
per-section byte ranges that alias it, and a caller decodes only the sections it
is asked about:

```go
sections, err := chunk.Split775(packet.ChunkData, dimensionMinY/16)
if err != nil {
	return err
}

states, err := chunk.DecodeSection775(sections[0].Blocks)  // 4096 block states
```

Protocol 47 splits from the packet's bitmask alone, because the block data comes
first. Reading the light and biomes behind it needs two conditions the blob does
not carry — whether the dimension sends sky light, and whether the packet is
ground-up — which is what `Layout47` names:

```go
sections, rest, err := chunk.Split47(packet.BitMap, packet.ChunkData)

layout := chunk.Layout47{Bitmap: packet.BitMap, SkyLight: true, GroundUp: packet.GroundUp}
column, err := chunk.Decode47(layout, packet.ChunkData)
```

`Layout47.Bytes` is also the stride the bulk packet needs: it concatenates
columns with no lengths of their own.

A protocol 775 column does not carry where it starts either. The bottom section
index is the dimension's minimum build height divided by sixteen, which comes
from the `dimension_type` registry sent in configuration — minus four in a 26.1
overworld, and a caller that assumes zero puts every block sixty-four blocks too
high.

## Game-data contracts

The `data` package provides typed game-data values, read-only lookup
interfaces, immutable `Set` values, raw-dataset lookup, and a version registry.
`NewSet` copies raw dataset bytes and mutable schema values. Registry lookups
return caller-owned values and collections, so callers can modify a result
without changing the stored data. `DatasetNames` and `RegisteredVersions`
return sorted names.

Use `NewSet` in a factory, register that factory, then load a version by name:

```go
package main

import (
	"fmt"

	"github.com/go-theft-craft/minecraft-protocol/data"
)

func newExampleSet() (*data.Set, error) {
	set, err := data.NewSet(data.SetOptions{
		Raw: []data.RawDataset{{
			Name:      "example.json",
			MediaType: "application/json",
			Data:      []byte(`{}`),
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("create example data: %w", err)
	}

	return set, nil
}

func main() {
	if err := data.Register("example", newExampleSet); err != nil {
		panic(fmt.Errorf("register example data: %w", err))
	}

	set, err := data.Load("example")
	if err != nil {
		panic(fmt.Errorf("load example data: %w", err))
	}

	fmt.Println(set.DatasetNames()) // [example.json]
}
```

The example registers a package-level version once. Use `NewRegistry` when an
application needs an isolated registry, such as a test or a plugin host.

## Supported versions

| Package | Version | Protocol | Notes |
| --- | --- | --- | --- |
| `generated/java/v1_8` | Java 1.8.9 | 47 | Physics constants and block movement measured from a Mojang jar |
| `generated/java/v26_1` | Java 26.1 | 775 | Block movement measured from a Mojang jar, configuration state, raw datasets, checked-in packet coverage report |
| `generated/java/current` | follows the newest | — | An alias, not a compatibility promise |

`current` follows whichever version is newest here and will move when a newer
one lands. A program that must keep speaking one protocol imports that
version's package by name.

One caveat carries over from upstream. Java 26.1 publishes no `windows`
dataset, and the pinned tree resolves it to Java 1.16.1, so
`v26_1` window records describe a version ten releases older than the protocol
beside them. Five other datasets are aliases in the same way — `blockLoot` and
`entityLoot` at 1.20, `commands` at 1.20.3, `mapIcons` at 1.20.2, and `proto`
at `latest`. The manifest records each one, and `mcproto data validate` reports
them.

## Pinned source data

Each version's source tree is fetched once, by commit, and verified by digest
before anything is generated from it:

```bash
# Re-fetch a pinned tree. The revision is a full commit, never a branch.
devbox run -- go run ./cmd/mcproto data fetch   --edition java --version 26.1 --protocol 775   --revision 8a80816cbfb3fe2b609f2cde4e57796c8033af61   --output ./source/java/26.1

# Check every tree against its own manifest, and report aliased datasets.
devbox run -- task data:validate
devbox run -- go run ./cmd/mcproto data validate --source ./source/java/26.1 --format json
```

Fetching twice produces byte-identical output. `data validate` exits 0 on
success, 1 on a runtime failure, and 2 on a usage error, and prompts for
nothing.

The Java 26.1 package also carries every dataset it was generated from, as the
bytes upstream published:

```go
import v26_1 "github.com/go-theft-craft/minecraft-protocol/generated/java/v26_1"

raw := v26_1.Raw()          // every dataset name, sorted
blocks, ok := raw.Get("blocks")
```

The typed registries are an interpretation — they keep what this repository
decided to model. The raw set is what that interpretation was made from, so a
consumer needing a detail no accessor exposes can read it, and a generated
value can be checked against its source.

## Built-in game-data sources

The Java 1.8 game-data package is generated from a pinned
[PrismarineJS minecraft-data](https://github.com/PrismarineJS/minecraft-data)
snapshot. `source/java/1.8/manifest.json` records the source repository and a
verified revision, the upstream path and license identifier, and a SHA-256
digest for every required source file. The generator renders typed registries
and packet values from that fixed input set. See
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md) for attribution and license
details.

Two datasets come from a different source than the rest of the bundle.
`physics.json` holds block slipperiness and the trigonometry table measured from
a verified Mojang server jar, plus entity motion constants transcribed from a
local research workspace. `blockMovement.json` holds three facts about each block, read
from the game's own answers — upstream publishes what a block is called and what
it drops, and never says whether an entity can occupy its cell, whether the
block falls when undermined, or whether it can be climbed. The last two are
there because a route that digs has to know the first before it digs, and
because no collision shape carries the second: a ladder's box is empty, so a
caller reading shapes alone cannot tell one from air. Both versions carry one, and they are not keyed alike: 1.8.9
hangs the fact off a block's material, so every state of a block answers
together, and 26.1.2 computes it per state from that state's own collision
shape, so its measurement is keyed by state range. The document declares which
encoding it holds, and the generator refuses one it has not been taught to
read. Their digests and provenance live in the
`extracted` block of the same manifest. Regenerating them requires a JDK and
the `mcreference dump` and `mcreference blocks` commands; verifying the
checked-in output needs neither.

`Set.BlockMovement` answers that measurement, and it is nil for a version
nobody has measured. Nil is not "nothing blocks movement": a caller that reads
an absent measurement as open ground walks into walls it cannot see, so an
unknown block is a block to refuse. The same reasoning applies within a
version: `ByState` reports whether it knows, and `ByID` declines to answer for
a block whose states disagree with each other rather than rounding to whichever
answer most of them give.

Falling and climbing are read through `FallsByState`, `FallsByID`,
`ClimbableByState`, and `ClimbableByID`, under the same rule — an unmeasured
block reports that it is not described, and a caller that reads that as "does
not fall" undermines a column the measurement never mentioned. They differ from
the movement fact in one way worth knowing: both hang off the block in both
measured versions, so neither `ByID` form has to decline. The two versions do
not agree on the answers, and are not meant to. 1.8.9 has two climbable blocks
and 26.1.2 has nine; the dragon egg falls in 26.1.2 and does not in 1.8.9.

Use the generated Java 1.8 data directly:

```go
import v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"

set, err := v1_8.Data()
```

The same package exposes the built-in protocol 47 descriptor:

```go
session, err := v1_8.Protocol().NewSession(protocol.RoleClient, limits)
```

Each session starts in `v1_8.StateHandshaking`. Reads return concrete generated
packet pointers. Writes validate the packet state, direction, and ID before
encoding.

Protocol 47 proposes its own transitions, which a stream commits at frame
boundaries: handshaking moves to status or login according to the handshake,
login success moves to play, and a set-compression packet enables or disables
compression. A nonnegative threshold enables compression, a negative one
disables it, and the active validation policy survives both. Install a
`protocol.TransitionPolicy` with `protocol.WithTransitionPolicy` to inspect,
replace, or suppress any proposal before it takes effect.

Compression validation comes in two policies. `java.StrictCompression` requires
a peer to compress exactly the packets the threshold selects.
`java.CompatibleCompression` relaxes only that size-to-threshold relationship.
Neither can relax the frame limit, the decompressed limit, exact decompressed
length, zlib validity, trailing-data rejection, or allocation safety.

The legacy server-list ping is not a VarInt-framed packet. A server that wants
to answer it installs the opt-in hook, which claims a connection only when it
begins with `FE 01` and otherwise leaves every inspected byte for the framer:

```go
hook, err := java.NewLegacyPingHook(func(ctx context.Context, _ java.LegacyPing) (java.LegacyStatus, error) {
	return java.LegacyStatus{
		ProtocolVersion: 47,
		Version:         "1.8.9",
		MOTD:            "A Minecraft Server",
		OnlinePlayers:   0,
		MaxPlayers:      20,
	}, nil
})

stream, err := protocol.NewStream(session, transport, protocol.WithPreFrameHook(hook))
```

## Encryption and login

A stream enables AES-128/CFB8 through `Stream.Control`, not through a session
transition: no packet carries the plaintext session key, so the session never
sees it. The `login` package drives the client sequence for protocol 47:

```go
authenticator, err := login.NewOffline("player")
if err != nil {
	return err
}
negotiator, err := login.NewNegotiator(authenticator)
if err != nil {
	return err
}

profile, err := negotiator.Negotiate(ctx, stream)
if err != nil {
	return err
}
// The stream is now in the play state, encrypted if the server asked for it.
```

Four rules matter:

- Encryption is applied, not proposed. A consumer that runs its own login calls
  `Stream.Control` with a `java.EncryptionControl` after the key exchange.
  `Stream.Write` returns only once the frame has reached the transport and
  `Control` queues behind it, so the response itself goes out in plaintext and
  every later byte is encrypted.
- The negotiator owns `Read` while it runs. A caller that reads concurrently
  steals packets the sequence needs.
- Captures are redacted by default. The key-exchange packet bodies and the
  session key are withheld unless the stream was built with
  `protocol.WithSecretDisclosure`, which requires a stated reason. A disclosed
  capture is a credential; treat it as one.
- Every peer-supplied identity crosses the boundary as a parsed type. A
  `login.Profile` holds a `java.Username` and a `java.UUID`, so holding one is
  itself proof that validation ran.

`Stream.Snapshot` reports `encryption.enabled` alongside the compression
settings, and an `ObservationSecret` record marks the switch point even when
the material itself is withheld.

## How generated codecs are built

Every type a protocol schema defines is compiled from that schema. A
hand-written codec backs a name only when the schema declares that name
`native`, and a codec whose name the schema defines as something else fails
generation rather than quietly winning. The rule exists because names repeat
across protocol versions with different meanings: protocol 47 and protocol 775
both define `position` and `entityMetadata`, but 47 packs x, y, z and 775 packs
x, z, y, and 47 ends metadata at 127 where 775 ends it at 255. Binding a codec
by bare name gives one of those versions the other's wire format, and a
per-version round-trip test cannot see it, because both directions are wrong
together.

A named type is generated once, rather than inlined into each packet, when it
is recursive, participates in a cycle, or is used by two or more packets. Shared
decoders count nesting depth against `MaxRecursionDepth`, which is what keeps a
recursive schema -- where a peer chooses the nesting -- a decode error instead
of a stack overflow.

## Observation points

A stream can publish a lossless record of everything it moves. Install a sink
with `protocol.WithObservationSink`:

```go
stream, err := protocol.NewStream(session, transport, protocol.WithObservationSink(sink))
```

Each `protocol.Observation` carries a stream-wide sequence number, a frame
correlation ID, a direction, a stage, the session snapshots either side of the
commit, packet metadata where available, and owned bytes. Raw-frame records
hold the complete frame including its length prefix; packet records hold the
decoded body. Delivery is lossless and bounded by the shared budget, so a slow
sink applies backpressure and a failing sink terminates the stream. Mutable
generated packet values are deliberately not exposed to observers.

Bodies that carry secret material are withheld. Redaction is decided per record
and covers both the decoded body and the raw frame it arrived in — the raw
record is written before the frame is decoded, so the session answers a
frame-level question about the packet ID at the front of it. `Observation.
OriginalLen` reports the size a withheld body had, which is the one thing about
it that is safe to state. `protocol.WithSecretDisclosure` turns redaction off
for a stated reason.

## Routing and middleware

`router` dispatches decoded packets to handlers registered by packet name or by
ID, and `middleware` composes ordered wrappers around sending and handling.
Neither imports the stream: they are written against one-method `Sender`,
`Handler`, and `Receiver` interfaces, so a test drives them over a slice.

```go
dispatcher, err := router.New(v26_1.Protocol())
if err != nil {
	return err
}
err = dispatcher.Handle(v26_1.StatePlay, protocol.DirectionClientbound, "keep_alive", handler)

adapter, err := router.FromStream(stream)
if err != nil {
	return err
}
err = dispatcher.Run(ctx, adapter)          // loop until the stream ends
err = dispatcher.Dispatch(ctx, onePacket)   // or drive it a packet at a time
```

Handlers on one key run in registration order and the first error stops the
rest. An unregistered packet is skipped without error — a connection carries
packets a consumer did not ask for — and a fallback handler receives exactly
those. Registration by name resolves through the protocol at registration time,
so a misspelled name fails where the mistake is rather than as silence at
dispatch. Handler panics are not recovered.

## Capture, history, and replay

A capture is a durable record of one connection: a JSON header, then
length-prefixed, CRC-checked binary records with an inline string table. It is
written straight from the observation path, so a process killed mid-capture
leaves a file readable up to its last complete record.

**A capture holds session content and is not encrypted.** Everything the peers
exchanged is in it — chat, positions, plugin messages. Secret material is
withheld unless the writer was explicitly constructed to disclose it, and a
disclosing capture records in its own header that it did and why.

```go
sink, err := capture.NewFileSink("session.mcpcap", capture.Header{
	Protocol:          v26_1.Protocol().ID(),
	Role:              "client",
	FrameBytes:        limits.FrameBytes(),
	DecompressedBytes: limits.DecompressedBytes(),
})
if err != nil {
	return err
}
defer sink.Close()   // writes the trailer, flushes, and syncs

stream, err := protocol.NewStream(session, transport, protocol.WithObservationSink(sink))
```

`history.NewRing` is the in-memory counterpart, bounded by record count and by
bytes. It is the one sink allowed to lose data: a capture with holes looks
complete and is not, but a ring exists so somebody can ask what just happened,
and forgetting the distant past is what makes that possible in bounded memory.
`capture.MultiSink` composes the two.

`replay` drives a capture back through a decoder or a peer. Offline it decodes
every recorded frame again and produces a digest to compare against the one the
capture recorded; where this code proposes a different state than the capture
recorded, the capture wins and the disagreement is returned as a divergence.

### A worked example

Capture a login against a local server, look at it, and check that it still
decodes the same way:

```console
$ mcproto capture --address 127.0.0.1:25565 --output login.mcpcap \
    --username tester --offline
path        login.mcpcap
protocol    java/26.1
redaction   enforced

$ mcproto inspect --input login.mcpcap --filter 'kind=packet bytes>1024'
    58 14.695ms packet    clientbound configuration 0x0007 registry_data      4945
    60 15.853ms packet    clientbound configuration 0x0007 registry_data     31715
    76 17.952ms packet    clientbound configuration 0x000d tags              32316

$ mcproto replay --input login.mcpcap --verify
input       login.mcpcap
protocol    java/26.1
records     77
digest      fee4542d4861e5b27bda9ee8893620032107c91fb49331b87ba697cddad1d4f2
recorded    fee4542d4861e5b27bda9ee8893620032107c91fb49331b87ba697cddad1d4f2
drift       0s
```

`replay --verify` exits 4 when the digests differ, which is what makes it
usable as a check rather than a report.

### Exit codes

`mcproto` reports through its exit code so a script never has to read a
message:

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | The command failed at what it was asked |
| 2 | The command was asked wrongly; nothing was attempted |
| 3 | The peer or the network failed |
| 4 | A check ran and did not match |

## Resource budgets

`MaxBufferedBytes` bounds everything a stream keeps in memory. It defaults to
32 MiB with a 1 GiB hard ceiling, and construction rejects a configuration that
cannot hold one maximum frame plus one maximum decompressed body.
`MaxQueueItems` is a stream-wide count budget rather than a per-queue one, so
unread inbound packets eventually apply backpressure to writes. Part of the
byte budget stays reserved as processing headroom for the frame in flight in
each direction, which is why an accepted write and the final disconnect can
still make progress while the inbound queue is full.

## Error model

A stream keeps running after an operation that fails before any byte is
written: an invalid outbound packet, an unsupported control, or a transition
policy rejection fails only that call. These failures terminate the stream and
become the cause `Wait` reports: a malformed inbound frame, a compression or
decode failure, an impossible inbound transition, a partial or failed transport
write, a peer EOF outside local shutdown, a pre-frame hook failure, and an
observation sink failure. The first cause is stable; later ones do not replace
it.

Cancelling a `Write` before the transport write starts guarantees no byte was
sent. Cancelling it afterwards reports `protocol.ErrAmbiguousWrite` and aborts
the stream, because the caller cannot know how much the peer received.

To activate the `java/1.8.9` registry entry, blank-import the generated package
before calling `data.Load`:

```go
import (
	"github.com/go-theft-craft/minecraft-protocol/data"
	_ "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
)

set, err := data.Load("java/1.8.9")
```

`data.Protocol` is a lossy protocol summary in this milestone. It keeps packet
names, IDs, and field names, but represents arrays, switches, containers, and
other structured definitions as `complex`. Use the pinned `protocol.json` when
you need the complete upstream schema.

Minecraft data changes independently of the wire protocol. Applications can
therefore select or replace a protocol implementation and a data bundle
separately.

## Development

Install [Devbox](https://www.jetify.com/devbox), enter this directory, and run:

```bash
devbox run -- task verify
```

Devbox configures the tracked pre-commit hook for the clone. The hook checks
formatting, runs the Go linters, and scans staged content for secrets and private
keys. CI scans the complete committed tree. Run `devbox run -- task --list` to
see the individual tasks.

### Generate the version packages

To regenerate every checked-in generated package, run:

```bash
devbox run -- task generate
```

To verify that the checked-in generated files match the pinned source, run:

```bash
devbox run -- task generate:check
```

`task generate:check` compares an explicit inventory of generated files, the
Java 26.1 raw dataset directory, and the checked-in packet coverage report. A
handful of hand-written test files are preserved rather than created: the
generator keeps them and never writes them.

`task generate:v1_8` and `task generate:v26_1` regenerate one version each. The
raw dataset set and the coverage report are generated for Java 26.1 only, with
`-raw` and `-coverage`: the bytes are megabytes, every binary importing the
package carries them, and Java 1.8's package is consumed by services that read
only the typed registries.

### Command tests and the capture fuzz target

`task test:cli` runs the black-box `mcproto` tests. They call the command's
entry point directly rather than building a binary, so the exit codes are
covered rather than inferred.

`task test:fuzz` smokes the capture reader against arbitrary bytes for thirty
seconds. A capture is read off a disk that may hold a truncated write or a
corrupt sector, so every input has to produce an error rather than a panic or
an unbounded allocation.

### Differential verification against ProtoDef

`task test:protodef` compares the generated protocol 775 codecs against the
pinned Node ProtoDef, packet by packet: Go encodes a packet, ProtoDef reads and
re-encodes it, and the bytes must match; then Go reads ProtoDef's bytes back
and must recover the value it started from. The test first checks that both
sides are reading the same `protocol.json`, by hash, so an agreement between
two different schemas cannot pass for a result.

This is codec-level and not session-level, because the pinned Node
`minecraft-protocol` 1.66.2 supports up to Minecraft 1.21.11 and cannot speak
26.1 at all. Protocol 47 keeps its loopback session lane in `task test:interop`;
protocol 775 has no session-level oracle until upstream adds the version.

The public API is still changing. Before starting a substantial contribution,
[open an issue](https://github.com/go-theft-craft/minecraft-protocol/issues) to
agree on the contract and compatibility fixtures.

## Project information

- [Roadmap](ROADMAP.md)
- [Release and versioning rules](RELEASING.md)
- [Changelog](CHANGELOG.md)
- [Apache-2.0 license](LICENSE)
- [Third-party notices](THIRD_PARTY_NOTICES.md)
- [Headless client](https://github.com/go-theft-craft/headless-minecraft)

This project is not an official Minecraft product. It is not approved by or
associated with Mojang or Microsoft.
