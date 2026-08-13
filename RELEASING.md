# Release and versioning rules

This repository uses [Semantic Versioning 2.0.0](https://semver.org/) and Go module tags in `vMAJOR.MINOR.PATCH` form. Minecraft versions and protocol numbers do not determine the module version.

## Version stability

Releases start in the `v0.x.y` series. A `v0` minor release may break an API while the design settles. Every breaking `v0` change requires a changelog entry, a `**Breaking:**` marker, and migration instructions.

The `v1.0.0` release starts compatibility guarantees for the public API. Starting with `v2.0.0`, the module path must include the matching major suffix, such as `github.com/go-theft-craft/minecraft-protocol/v2`.

## Public compatibility contract

The compatibility contract includes:

- exported Go names, signatures, interfaces, constants, and documented behavior;
- generated packet and data types intended for callers;
- built-in protocol profile identifiers;
- capture and manifest formats;
- command names, flags, exit behavior, and documented structured output; and
- default limit behavior for inputs that earlier releases accepted.

Internal packages, test fixtures, and generated files marked internal do not form part of the public contract.

## Pick the version change

```mermaid
flowchart TD
    A[Release candidate] --> B{Does it break the public contract?}
    B -->|Yes, after v1| C[Major release]
    B -->|Yes, before v1| D[Minor release with migration notes]
    B -->|No| E{Does it add public capability?}
    E -->|Yes| F[Minor release]
    E -->|No| G[Patch release]
```

Use these project rules:

| Change | Version |
| --- | --- |
| Add a protocol version, dataset, lookup, codec, or optional CLI command | Minor |
| Add fields to a struct that callers construct with literals | Major after `v1` |
| Remove a built-in protocol version | Major after `v1` |
| Rename or change the type of a public generated field | Major after `v1` |
| Change a capture or manifest format without backward reading support | Major after `v1` |
| Correct packet encoding, data, or lookup behavior without breaking valid callers | Patch |
| Tighten a bound to fix a security issue | Patch with a `Security` entry and impact note |
| Change documentation, tests, or internal generation code only | Patch |

## Pre-releases

Use `-alpha.N`, `-beta.N`, and `-rc.N` in that order. Do not use a pre-release tag as the latest stable release. Publish an `rc` before `v1.0.0` and before every later major release.

## Changelog rules

Maintain [CHANGELOG.md](CHANGELOG.md) during development. Put each user-visible change under `Unreleased` in one of these sections:

- `Added`
- `Changed`
- `Deprecated`
- `Removed`
- `Fixed`
- `Security`

Write entries for users, not for commit history. Mark a breaking entry with `**Breaking:**` within its category. Include a migration instruction or a link to one.

At release time, rename `Unreleased` to the version and UTC date in `YYYY-MM-DD` form. Add a fresh empty `Unreleased` section above it.

## Release flow

```mermaid
stateDiagram-v2
    [*] --> Unreleased
    Unreleased --> Candidate: choose version and update changelog
    Candidate --> Committed: commit release preparation
    Committed --> Verified: release and generation checks pass
    Verified --> Candidate: fix a failed check
    Verified --> Tagged: create annotated tag
    Tagged --> Published: push tag and create GitHub release
    Published --> Confirmed: Go proxy resolves the tag
    Confirmed --> [*]
```

Release only from a clean `main` branch whose CI checks pass.

1. Review `CHANGELOG.md` and choose the version from the rules above.
2. Replace `Unreleased` with the version and current UTC date.
3. Add a new empty `Unreleased` section.
4. Commit the release preparation.
5. Run `devbox run -- task release:check VERSION=vMAJOR.MINOR.PATCH`.
6. Run generation checks when generated protocol or data files changed.
7. Run the server, proxy, and headless-client compatibility suites when their shared contracts changed.
8. Create an annotated `vMAJOR.MINOR.PATCH` tag on the verified commit.
9. Push the commit and the tag.
10. Create a GitHub release from the matching changelog section.
11. Confirm that `go list -m github.com/go-theft-craft/minecraft-protocol@vMAJOR.MINOR.PATCH` resolves the release.

`release:check` rejects a dirty tree, a local `replace` directive, and an invalid version. Do not move or reuse a published tag. Publish a new patch release for a correction.

## Release tooling

Library releases use annotated Git tags and GitHub releases directly. Do not add
GoReleaser merely to publish the module or generate its changelog; the reviewed
`CHANGELOG.md` remains the release-note source.

Adopt GoReleaser when this repository ships the planned `mcproto` command. Its
scope will be reproducible cross-platform binaries, checksums, provenance, and
GitHub release assets. Go module tags remain the source of module versions.

## Dependency policy

Tag `minecraft-protocol` before a dependent `headless-minecraft` release. The headless repository must use a released module version and must not contain a local `replace` directive in a published tag.

The [Go module version documentation](https://go.dev/doc/modules/version-numbers) defines Go's stability meaning. The [Go module reference](https://go.dev/ref/mod#major-version-suffixes) defines major-version suffixes.
