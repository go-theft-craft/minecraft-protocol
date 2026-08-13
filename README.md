# Minecraft protocol and game data for Go

`minecraft-protocol` is a Go toolkit for building Minecraft clients, servers,
proxies, packet analyzers, and custom protocol implementations. It provides
bounded Java wire primitives and typed game-data contracts behind small,
injectable interfaces.

> [!IMPORTANT]
> This project is pre-alpha and has no published release or built-in protocol
> descriptor. The `wire/java` package implements bounded Java wire primitives,
> reflection-based `mc` struct tags, and uncompressed packet frames. It does not
> provide compression, encryption, login, generated codecs, or a complete
> server session.

## Why this repository exists

- Share protocol and game-data code across clients, servers, and proxies.
- Keep Java Edition, Bedrock Edition, and custom protocols from inheriting one
  another's transport assumptions.
- Generate reproducible built-ins from pinned upstream data while allowing
  applications to inject their own implementations.
- Preserve raw packet access and unknown upstream fields for modded servers and
  future protocol changes.

## Planned support

| Protocol or data | Status |
| --- | --- |
| Edition-neutral contracts, resource limits, and game-data registry | Implemented |
| Java Edition 1.8, protocol 47 | Uncompressed primitives and frames implemented; generated built-in descriptor and dataset planned |
| Java Edition 26.1, protocol 775 | Generated built-in descriptor and dataset planned |
| PrismarineJS blocks, items, entities, recipes, registries, and other datasets | Planned generated built-ins |
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

`selectedProtocol` can be a future built-in or any application implementation
of `protocol.Protocol`. A codec works with `io.Reader` and `io.Writer`, so the
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

## Planned game-data sources

Built-in datasets will be generated from a pinned
[PrismarineJS minecraft-data](https://github.com/PrismarineJS/minecraft-data)
revision. Generated artifacts will record the source revision and digest.
Unknown source files and fields will remain available as raw datasets instead
of being silently discarded.

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

The public API is still changing. Before starting a substantial contribution,
[open an issue](https://github.com/go-theft-craft/minecraft-protocol/issues) to
agree on the contract and compatibility fixtures.

## Project information

- [Roadmap](ROADMAP.md)
- [Release and versioning rules](RELEASING.md)
- [Changelog](CHANGELOG.md)
- [Apache-2.0 license](LICENSE)
- [Headless client](https://github.com/go-theft-craft/headless-minecraft)

This project is not an official Minecraft product. It is not approved by or
associated with Mojang or Microsoft.
