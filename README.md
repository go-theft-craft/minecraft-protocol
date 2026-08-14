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
| Java Edition 1.8, protocol 47 | Built-in descriptor, generated packet sessions, managed asynchronous stream, compression, automatic transitions, graceful disconnect, and generated game data implemented |
| Java Edition 26.1, protocol 775 | Generated built-in descriptor and dataset planned |
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

## Built-in game-data sources

The Java 1.8 game-data package is generated from a pinned
[PrismarineJS minecraft-data](https://github.com/PrismarineJS/minecraft-data)
snapshot. `source/java/1.8/manifest.json` records the source repository and a
verified revision, the upstream path and license identifier, and a SHA-256
digest for every required source file. The generator renders typed registries
and packet values from that fixed input set. See
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md) for attribution and license
details.

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

### Generate Java 1.8 game data

To regenerate the checked-in Java 1.8 package, run:

```bash
devbox run -- task generate
```

To verify that the checked-in generated files match the pinned source, run:

```bash
devbox run -- task generate:check
```

`task generate:check` compares an explicit inventory of generated files and
allows only `data_test.go` and `codec_test.go` as hand-written exceptions. The
generator preserves these files without creating them.

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
