# Changelog

This file records notable user-visible changes. It follows [Keep a Changelog](https://keepachangelog.com/en/2.0.0/) and [Semantic Versioning](https://semver.org/).

## Unreleased

### Added

- Initial repository structure, bounded protocol contracts, immutable raw datasets, Devbox tooling, CI, and a tracked pre-commit hook for lint and secret scanning.
- Exported the `wire/java` `PacketValue` compatibility interface; VarInt, VarLong, fixed-width field, UUID, packed-position, bounded string, and bounded byte-array helpers; reflection-based `Marshal` and `Unmarshal`; packet-level `ReadPacket` and `WritePacket`; and uncompressed `protocol.Packet` frame I/O. Variable-length values require validated `protocol.Limits`. This API does not include compression, encryption, login, generated codecs, a built-in protocol 47 descriptor, or a complete server session.
- Added typed game-data values, caller-owned registry lookup contracts, immutable `Set` construction, raw-dataset lookup, and concurrent version registration and loading.
- Added pinned Java 1.8 source data with exact upstream revision and license provenance, the atomic `mcdata-gen` generator, generated registries and packet values, and registration under `java/1.8.9`. The data model preserves entity and biome collisions, nullable recipe output shapes, and optional fractional drop counts. Legacy server-list ping remains in the protocol summary but is not exposed through the normal framed packet API.
