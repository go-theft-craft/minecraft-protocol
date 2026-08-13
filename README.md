# Minecraft protocol and game data for Go

`minecraft-protocol` is a Go toolkit for building Minecraft clients, servers,
proxies, packet analyzers, and custom protocol implementations. It provides
versioned wire codecs and generated game data behind small, injectable
interfaces.

> [!IMPORTANT]
> This project is pre-alpha. It has no published release or built-in wire codec
> yet. The current code defines the protocol contracts and bounded resource
> limits on which those implementations will depend.

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
| Edition-neutral contracts and resource limits | In progress |
| Java Edition 1.8, protocol 47 | Planned extraction from the sibling server |
| Java Edition 26.1, protocol 775 | Planned generated built-in |
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

## Game-data provenance

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
