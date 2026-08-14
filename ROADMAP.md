# Roadmap

The roadmap records dependency order, not release dates. Completed work remains in the chart so later decisions retain context.

```mermaid
flowchart LR
    P0["P0: repository foundation<br/>in progress"]
    P1["P1: extract Java 1.8<br/>wire, data, and generator<br/>complete"]
    P2["P2: generate Java 26.1<br/>and all PrismarineJS data"]
    P3["P3: stream, router,<br/>capture, and mcproto CLI"]
    P4["P4: migrate server<br/>and proxy consumers"]
    P5["P5: stable v1 contracts"]
    PX["Later: Bedrock family"]

    P0 --> P1
    P1 --> P2
    P1 --> P3
    P2 --> P4
    P3 --> P4
    P4 --> P5
    P2 -. protocol-family contract .-> PX
```

## P0: repository foundation

- Define edition-neutral protocol contracts.
- Enforce finite resource limits with hard ceilings.
- Pin the Go toolchain through `openserbia/go-flake`.
- Establish release, changelog, and roadmap rules.

## P1: Java 1.8 extraction

Status: complete.

- Extract reusable wire and game-data code from `server`.
- Preserve protocol 47 behavior with checked-in byte fixtures.
- Generate the built-in protocol 47 descriptor and direct packet codecs without
  reflection in generated runtime paths.

## P2: current Java data

- Generate protocol 775 for the Java 26.1 data family.
- Import every dataset exposed by the pinned PrismarineJS manifest.
- Preserve unknown JSON, YAML, and future source formats as raw datasets.

## P3: reusable protocol tools

- Add the bounded managed stream.
- Add compression, encryption, routing, middleware, capture, replay, status,
  and complete login helpers.
- Add the non-interactive `mcproto` command.

## P4: shared consumers

- Migrate `server` to the shared Java 1.8 packages.
- Migrate `proxy` imports while keeping legacy internal.
- Connect `headless-minecraft` to the current Java profile.

## P5: stable contracts

- Publish `v1.0.0` after public APIs have compatibility tests.
- Document support windows for built-in protocol versions.
- Require migration notes for every later breaking change.

## Deferred work

Bedrock transport, authentication, codecs, and client behavior require a separate design. The shared edition contract must remain able to host that work without applying Java transport assumptions.
