# Roadmap

The roadmap records dependency order, not release dates. Completed work remains in the chart so later decisions retain context.

```mermaid
flowchart LR
    P0["P0: repository foundation<br/>in progress"]
    P1["P1: extract Java 1.8<br/>wire, data, and generator<br/>complete"]
    P2["P2: generate Java 26.1<br/>and all PrismarineJS data"]
    P3a["P3a: managed stream<br/>and compression<br/>complete"]
    P3b["P3b: encryption and<br/>login lifecycle<br/>complete"]
    P3c["P3c: router, capture,<br/>replay, and mcproto CLI"]
    P4["P4: migrate server<br/>and proxy consumers"]
    P5["P5: stable v1 contracts"]
    PX["Later: Bedrock family"]

    P0 --> P1
    P1 --> P2
    P1 --> P3a
    P3a --> P3b
    P3b --> P4
    P2 --> P3c
    P3c --> P5
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

Status: complete, except for verification against a live server.

- Generated protocol 775 for the Java 26.1 data family: 256 framed packets
  across five states, with a checked-in coverage report.
- Imported every dataset the pinned PrismarineJS manifest exposes, including
  the seven with no Java 1.8 equivalent.
- Kept every dataset as the bytes upstream published, reachable through
  `v26_1.Raw()`.
- Verified the codecs against pinned Node ProtoDef in both directions.
- Ran the opt-in live check against Paper 26.1.2 build 74. It found that the
  negotiator never answered `select_known_packs`, which stalls a real login in
  configuration; that is fixed. The resource limits now stand on traffic
  through login: 12,564 bytes largest raw frame against a 2 MiB limit, 32,316
  bytes largest decoded body against 8 MiB. Play is not measured.

## P3a: managed stream and compression

Status: complete.

- Replaced `protocol.Codec` with session, packet, frame, and compression
  boundaries.
- Added the asynchronous managed stream with a read pump, a write pump, and one
  coordinator that orders every state change, control, observation, and
  shutdown step.
- Added bounded Java compression envelopes with strict and compatible policies.
- Added protocol 47 automatic transitions, developer-controllable transition
  policy, runtime controls, and state-appropriate graceful disconnect.
- Added the opt-in legacy `FE 01` pre-frame hook.
- Added lossless observation points that later capture work can subscribe to.
- Added loopback TCP scenarios and pinned Node `minecraft-protocol` 1.66.2
  interoperability tests as a required gate.

## P3b: encryption and login lifecycle

Status: complete, except for the modern-login transitions, which need the
protocol 775 codecs that P2 generates.

- Added AES-CFB8 at the transport boundary, in the correct pipeline order,
  through a conduit that transforms bytes as it hands them out.
- Added the key-exchange primitives, the Java server hash, and strict identity
  types for every peer-supplied login field.
- Added the opt-in `login` negotiator, which drives the client sequence for an
  offline or externally authenticated identity without coupling authentication
  to the stream.
- Added secret redaction in observations, with disclosure behind an explicit,
  reasoned opt-in.
- Add configuration and play transitions for modern Java login, keeping every
  automatic transition optional.

## P3b.5: schema-first code generation

Status: complete.

- Compile every schema-defined type from its own schema; a hand-written codec
  backs only a name the schema declares native.
- Share named types that are recursive or used by more than one packet, and
  bound decode recursion against the configured depth.
- Delete the hand-written `Position`, `Slot`, and `EntityMetadata` value types.

## P3c: routing, capture, replay, and CLI

Depends on the protocol 775 datasets from P2.

- Add packet routing and ordered middleware outside framing.
- Add bounded in-memory history and durable capture sinks on top of the
  observation points P3a already publishes.
- Add deterministic replay with explicit timing modes.
- Add the non-interactive `mcproto` command.

## P4: shared consumers

Server and `headless-minecraft` migration comes before protocol 775.

- Migrate `server` to the shared Java 1.8 packages.
- Migrate `proxy` imports while keeping the legacy protocol internal.
- Connect `headless-minecraft` to the current Java profile.

## P5: stable contracts

- Publish `v1.0.0` after public APIs have compatibility tests.
- Document support windows for built-in protocol versions.
- Require migration notes for every later breaking change.

## Deferred conformance work

The P3a gate runs the pinned Node `minecraft-protocol` implementation on
loopback. These wider conformance lanes are deliberately deferred, with the
repository that owns each one named:

| Lane | Owner |
| --- | --- |
| Paper 26.1 compatibility matrix | `minecraft-protocol` |
| MCProtocolLib compatibility matrix | `minecraft-protocol` |
| Instrumented vanilla-client scenarios | `headless-minecraft` |
| Movement, combat, and crafting scenario matrix | `minecraft-simulation`, `server` |

An archived Paper 1.8 run is optional and is not a P3 gate.

## Deferred work

Bedrock transport, authentication, codecs, and client behavior require a separate design. The shared edition contract must remain able to host that work without applying Java transport assumptions.
