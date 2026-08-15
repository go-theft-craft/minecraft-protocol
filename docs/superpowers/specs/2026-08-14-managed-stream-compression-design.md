# Managed stream and compression design

- Status: Draft for review
- Date: 2026-08-14
- Repository: `minecraft-protocol`
- Milestone: First P3 slice

## Context

The protocol 47 implementation currently combines packet coding with uncompressed Java framing in `protocol.Codec`. Callers set protocol state themselves. The repository has no managed connection, compression support, runtime transition policy, or bounded asynchronous queues.

This slice proves the protocol API through a real duplex connection before the project generates Java 26.1 protocol 775. It also creates the boundaries that later encryption, routing, capture, replay, and consumer migrations need.

## Goals

This slice adds:

- An edition-neutral asynchronous stream over an `io.Reader` and an `io.Writer`.
- Packet framing through a protocol-family pipeline.
- Automatic protocol-state and compression transitions.
- Developer-defined transition and compression policies.
- Runtime state and compression controls at frame boundaries.
- Bounded item counts and buffered bytes.
- Graceful disconnect and abortive close.
- An opt-in pre-frame hook for the Java `FE 01` legacy ping.
- Stable observation points for later capture and replay.
- Unit, integration, interoperability, malformed-input, and race tests.

## Non-goals

This slice does not add:

- Encryption.
- Authentication or an automatic login and configuration lifecycle.
- Packet routing or middleware execution.
- Capture storage, a capture file format, or a replay engine.
- The `mcproto` command.
- A migration of `server`, `proxy`, or `headless-minecraft`.
- Java 26.1 protocol 775 generation.
- Movement, combat, inventory, or crafting behavior.

## Replace the combined codec

The project can break the current pre-alpha `protocol.Codec` API. Compatibility adapters would preserve a boundary that puts Java framing inside packet coding and makes compression and raw-frame observation hard to compose.

`protocol.Protocol` creates an isolated per-connection session. The session supplies these protocol-specific parts:

- A packet codec converts packet-body bytes to and from typed `protocol.Packet` values.
- A frame pipeline reads and writes complete frames and applies protocol-family transforms.
- Transition rules propose state and pipeline changes after packets.
- An optional disconnect capability creates the outbound disconnect packet for a role and state.

The generated Java 1.8 implementation supplies all four parts. The core package does not know about VarInt framing, zlib, or Java packet types.

## Stream architecture

`protocol.Stream` owns the asynchronous runtime. It has one read pump, one write pump, and one coordinator.

The read pump reads complete bounded raw frames. It does not decode packets or change protocol state. The write pump performs complete ordered writes and reports their result. The coordinator owns:

- The inbound and outbound protocol snapshots.
- The inbound and outbound queues.
- State and pipeline transitions.
- Developer controls.
- Shared item and byte budgets.
- Observation ordering.
- Shutdown state and the terminal error.

This structure keeps blocking reads and writes independent. It gives state and compression changes one owner.

## Public lifecycle

Construction validates configuration. It does not start goroutines or touch the transport.

The public shape is:

```go
stream, err := protocol.NewStream(
	session,
	protocol.Transport{
		Reader:    reader,
		Writer:    writer,
		Interrupt: interrupt,
	},
	options...,
)

err = stream.Start(ctx)
packet, err := stream.Read(ctx)
err = stream.Write(ctx, packet)
err = stream.SetState(ctx, state)
err = stream.Control(ctx, java.CompressionControl{
	Enabled:   true,
	Threshold: 256,
	Policy:    java.StrictCompression,
})
err = stream.Shutdown(ctx, reason)
err = stream.Close()
err = stream.Wait()
```

The names in this example are the intended contract. The implementation plan may refine parameter types without changing the behavior in this document.

`Start` starts the pumps once. Calling `Start` again returns an error. Cancelling the start context aborts the stream and interrupts blocked transport operations.

`Read` waits for the next decoded packet in the inbound queue. Cancelling one `Read` stops only that wait. It does not discard a queued packet or stop the stream.

`Write` accepts concurrent callers. Queue acceptance order defines wire order. `Write` returns success only after the write pump writes the complete frame.

`SetState` and `Control` pass through the coordinator. A control takes effect at a complete frame boundary. A control does not reinterpret packets that the coordinator has already decoded.

The caller installs the transition policy, pre-frame hook, compression policy, and observation sink before `Start`.

## Transport interruption

An asynchronous stream cannot stop a blocked `io.Reader` through a context alone. `protocol.Transport` therefore requires an interrupt function. A network caller normally passes `net.Conn.Close`. An in-memory test passes an equivalent close function.

The interrupt function must unblock both the reader and the writer. The stream calls it during abortive close and after graceful shutdown finishes its final write.

## Inbound pipeline

The Java inbound order is:

```text
transport
  -> pre-frame hook
  -> future decryption
  -> outer frame
  -> compression envelope
  -> bounded decompression
  -> packet ID and body decode
  -> transition policy
  -> transition commit
  -> observations
  -> Read queue
```

The triggering packet uses the old state and compression settings. The coordinator commits accepted changes before it publishes that packet to `Read`. The read pump may read later raw frames while a transition-causing write is in progress, subject to the shared budgets. The coordinator does not decode those frames until the write succeeds and commits its transition.

## Outbound pipeline

The Java outbound order is:

```text
Write queue
  -> packet validation and body encoding
  -> compression envelope
  -> outer frame
  -> future encryption
  -> complete transport write
  -> transition commit
  -> observations
  -> Write acknowledgement
```

The coordinator evaluates a proposed transition before it starts the transport write. A rejected proposal therefore cannot fail after bytes leave the process. The coordinator commits an accepted transition only after the complete write succeeds. It does not process another frame while that commit is pending.

Future encryption wraps the framed byte stream. Inbound code decrypts before it reads the outer frame. Outbound code encrypts after it creates the outer frame.

## State and transition policy

Generated protocol rules propose automatic transitions. Protocol 47 rules include:

- Handshaking to status or login after the handshake packet.
- Login to play after login success.
- Compression activation after the set-compression packet.

The same rules apply from both roles. The packet direction determines whether the transition follows an inbound decode or an outbound write.

A developer can install a transition policy that accepts, replaces, or suppresses each proposal. The developer installs the policy before `Start`, and the stream invokes it for each proposal. The policy remains fixed for the stream lifetime. It cannot select a state that the session rejects.

Developers can also call `SetState` and family-specific `Control` methods at runtime. These calls enter the coordinator order. They cannot change a partial frame or a packet that is already in the `Read` queue. Cancellation before coordinator acceptance guarantees no change. After acceptance, the method waits for the apply result so it cannot report cancellation for a control that took effect.

## Java compression

Compression is disabled when a Java session starts. A set-compression packet uses the uncompressed pipeline. Its accepted transition affects subsequent packets.

A nonnegative set-compression threshold enables compression. A negative threshold disables compression. Threshold `0` compresses every subsequent packet.

The Java package provides strict and compatible policies. It also accepts a custom policy for modded protocols.

Strict policy enforces these rules:

- A compressed packet declares a decompressed length at or above the active threshold.
- An uncompressed packet is smaller than the active threshold.
- The zlib stream produces exactly the declared byte count.
- The zlib stream has no trailing compressed data.

Compatible policy may relax only the relationship between packet size and threshold. A custom policy has the same limit.

No policy can relax:

- The encoded frame limit.
- The decompressed byte limit.
- Exact decompressed length.
- Valid zlib structure.
- Trailing-data rejection.
- Integer and allocation safety checks.

A developer can change the threshold, disable compression, or change the validation policy through a Java compression control. The coordinator applies the change at a frame boundary.

## Legacy pre-frame hook

The pre-frame hook runs once before normal frame parsing. The hook may inspect buffered bytes without consuming them when it declines the connection.

The Java package provides an opt-in parser for the `FE 01` legacy status ping. A server supplies the status response callback. If the hook claims the connection, it owns the raw exchange, bypasses normal framing, and ends the stream cleanly. If the hook declines, the frame reader receives every inspected byte.

Java server-role streams do not enable this hook by default.

## Resource budgets

`Limits` adds `MaxBufferedBytes`. Its default is 32 MiB. Its hard ceiling is 1 GiB.

The stream uses one shared byte budget for:

- Inbound raw frames.
- Decompressed buffers.
- Packet-body backing buffers retained by the inbound queue.
- Outbound encoded frames.
- Pending observation records.

`MaxQueueItems` becomes a stream-wide count budget. The implementation does not multiply either budget by the number of internal queues.

Construction rejects a configuration whose buffered-byte budget cannot hold one maximum encoded frame and one maximum decompressed result. A producer waits for both count and byte capacity. The producer's context can cancel that wait.

The stream reserves that frame-plus-decompression amount as processing headroom. Queued packets and observations can use only the remaining bytes. The coordinator serializes frame processing against this headroom, so a full inbound queue cannot prevent an accepted outbound write or final disconnect from making progress.

The byte budget covers buffers that the stream owns. Existing string, collection, NBT, plugin, and recursion limits bound allocations made by generated packet decoding. A custom packet codec or policy is trusted code and must enforce its own allocations beyond those limits.

## Observation points

This slice defines two optional, ordered observation stages:

- A raw-frame observation after the stream owns one complete frame and before decompression.
- A semantic-packet observation after decode and transition commit.

Each observation has a monotonic stream sequence, a frame correlation ID, a direction, a stage, state and compression metadata, packet metadata when available, and owned bytes. Semantic observations contain packet metadata and packet-body bytes. They do not expose mutable generated packet values.

An observation sink runs through a bounded queue covered by the shared budgets. Delivery is lossless. Backpressure stops more stream work until the sink consumes a record or the stream context ends. A sink error is fatal because a lossless capture would otherwise be incomplete.

This slice does not define capture storage or replay injection. Later work can feed captured raw frames before decompression or semantic packet records after decode. Both paths reuse the coordinator's ordering rules.

## Graceful and abortive shutdown

`Shutdown` is idempotent. It performs these steps:

1. Stop accepting outbound packets.
2. Drain accepted outbound packets in order.
3. Send the protocol-specific disconnect packet as the final frame when the current role and state support one.
4. Interrupt the transport.
5. Wait for both pumps to exit.

A Java client has no serverbound disconnect packet. Handshaking and status states also have no disconnect packet. In these cases, `Shutdown` drains and closes without an error.

If the shutdown context expires, the stream changes to abortive close. `Close` interrupts the transport immediately and fails queued writes. Both methods are safe to call more than once.

`Wait` returns the first fatal cause. That cause remains stable for later operations.

## Error model

The stream keeps running after an operation fails before any bytes are written. Examples include an invalid outbound packet, an unsupported control, and a transition policy rejection.

These failures terminate the stream:

- A malformed inbound frame.
- A compression or decode failure.
- An impossible inbound transition.
- A partial transport write.
- A transport read or write failure.
- A peer EOF outside local shutdown.
- A pre-frame hook failure.
- A lossless observation sink failure.

Cancelling a `Write` before the write pump starts guarantees that the stream sent no bytes. Cancelling after the transport write starts aborts the stream because the caller cannot know how many bytes reached the peer.

If graceful shutdown cannot encode or write its disconnect packet, the error becomes the terminal cause. If the shutdown context also expires, `Shutdown` reports both errors.

## Verification

### Deterministic Go tests

Unit tests cover:

- Exact Java frame and compression fixtures.
- One-byte readers and writers.
- Short and partial writes.
- Negative, zero, oversized, truncated, and overlong lengths.
- Compression thresholds at zero, below, at, and above packet size.
- Corrupt zlib data, decompression bombs, size mismatches, and trailing data.
- Both protocol roles and every protocol 47 state transition.
- Transition acceptance, replacement, and suppression.
- Runtime controls at frame boundaries.
- Shared count and byte budget exhaustion.
- Concurrent writers and stable queue order.
- Read and write cancellation.
- Graceful shutdown, unsupported disconnect states, and shutdown timeout.
- Legacy ping claim, decline, malformed input, and one-shot behavior.
- Observation sequence, ownership, correlation, and backpressure.

Fuzz tests target frame parsing, compression envelopes, and transition and control sequences. Tests assert that malformed input does not panic or exceed configured stream-owned allocations.

All asynchronous tests run under `go test -race`.

### Local TCP tests

A localhost TCP test connects protocol 47 client-role and server-role streams. It performs a status request and ping exchange.

A second test manually drives offline login. It enables compression, moves both sessions to play, exchanges packets below and above the threshold, and performs a graceful server disconnect. The test uses application code to drive login. It does not add an automatic login lifecycle.

### Community interoperability

Every pull request runs a pinned `node-minecraft-protocol` 1.8.8 interoperability test in both directions. The test records the upstream revision and license. It tests status, offline login, compression, play transition, and disconnect behavior.

P3 does not download or require an archived Paper 1.8 build. A developer may run a pinned Paper 1.8 server as an optional manual external target. The repository does not copy Paper test code or commit server JARs.

After protocol 775 exists, a separate conformance task adds pinned Paper 26.1 as the primary external server target. MCProtocolLib can provide another independent implementation when its supported version matches the generated protocol.

### Instrumented vanilla client

An instrumented original client is the strongest server-role reference. The client runs under Xvfb with software rendering and a test-only mod. The mod selects a scenario, connects to the local Go server, and writes a structured result.

The test workflow binds to loopback, uses an isolated temporary game directory, avoids account secrets where offline login permits it, and applies process timeouts and memory limits. It does not commit Mojang client files, libraries, assets, natives, or derived sources.

A manually triggered protocol 47 smoke test is optional and does not block P3 completion. After protocol 775 exists, a Fabric Client GameTest for Java 26.1 becomes a release conformance target.

The Fabric test configuration and CI environment must record explicit Minecraft EULA acceptance. See the [Fabric automated testing guide](https://docs.fabricmc.net/develop/automatic-testing) and [Fabric Loom test configuration](https://docs.fabricmc.net/develop/loom/fabric-api).

## Deferred gameplay scenarios

Movement, combat, and crafting scenarios use the same reference approach in later milestones. Each scenario records the exact game version, world fixture, initial state, tick-indexed actions, packet transcript, server-authoritative result, client observations, and comparison rules.

The first deferred matrix includes:

- Movement across walking, sprinting, sneaking, jumping, falling, swimming, ladders, slabs, stairs, collision corners, chunk boundaries, knockback, and server corrections.
- Combat across reach boundaries, line of sight, moving targets, attack cadence, critical hits, knockback, armor, effects, death, rejected attacks, and version-specific cooldown behavior.
- Crafting across shaped, shapeless, and mirrored recipes; stack splitting; shift-click; remainder items; full inventories; rejected transactions; recovery; and unknown recipes.

Owned local servers and controlled worlds run these tests. The tests do not target public servers or anti-cheat evasion.

Repository ownership remains narrow:

- `minecraft-protocol` owns connection transcripts and replay records.
- `minecraft-simulation` owns protocol-independent tick behavior fixtures.
- `headless-minecraft` owns command and packet conversion plus end-to-end client scenarios.
- `server` owns authoritative validation scenarios.
- The instrumented vanilla client produces reference results and is not a runtime dependency.

## Alternatives considered

### Independent full read and write pipelines

This design puts framing, compression, packet coding, and transitions in both pumps. It uses fewer internal messages, but cross-direction transitions require shared mutable state and hard-to-test barriers. The design rejects this option.

### One protocol event loop with blocking I/O

One event loop gives state a single owner, but a blocked read prevents writes and shutdown. Adding helper pumps turns this design into the selected coordinator structure with less explicit boundaries.

### Synchronous stream

A synchronous stream is smaller, but it does not provide the bounded asynchronous ownership required by later clients, servers, and proxies. The design rejects this option.

### Preserve `protocol.Codec`

Adapters around the current framed codec would reconstruct uncompressed frames around compression and obscure raw capture boundaries. The project is pre-alpha, so a direct API correction costs less than preserving this split.

## Completion criteria

The slice is complete when:

- The new session and stream contracts replace `protocol.Codec`.
- Protocol 47 supports asynchronous framed traffic and compression in both roles.
- Generated rules apply state and compression changes at the defined boundaries.
- Developer policies and runtime controls pass race and ordering tests.
- Shared resource budgets cover all stream-owned queues and buffers.
- Graceful and abortive shutdown satisfy the lifecycle tests.
- The opt-in legacy hook handles and declines `FE 01` without losing bytes.
- Observation records preserve order and ownership for later capture work.
- Local TCP and pinned `node-minecraft-protocol` interoperability tests pass.
- `go test -race ./...`, the formatter, the linter, the build, and the repository security scan pass.
- README, changelog, and roadmap text match the implemented boundary and revised milestone order.
