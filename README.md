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
| Java Edition 1.8, protocol 47 | Built-in descriptor, generated packet codecs, uncompressed primitives and frames, and generated game data implemented |
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

codec, err := selectedProtocol.NewCodec(protocol.RoleClient, limits)
```

`selectedProtocol` can be `v1_8.Protocol()` or any application implementation of
`protocol.Protocol`. A codec works with `io.Reader` and `io.Writer`, so the
caller retains ownership of connections, buffering, deadlines, and capture.

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
codec, err := v1_8.Protocol().NewCodec(protocol.RoleClient, limits)
```

Each codec starts in `v1_8.StateHandshaking`. Call `SetState` to select the
status, login, or play packet set. Reads return concrete generated packet
pointers. Writes validate the packet state, direction, and ID before encoding.
State transitions, compression, encryption, and login lifecycle remain the
caller's responsibility.

The legacy server-list ping remains available in the protocol data inventory,
but it is not part of the normal VarInt-framed codec. A transport that supports
legacy ping must detect and handle its `FE 01` prefix before frame decoding.

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
