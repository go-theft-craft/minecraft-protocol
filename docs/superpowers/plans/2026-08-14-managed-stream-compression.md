# Managed Stream and Compression Implementation Plan

> **Status: complete, 2026-08-18.** Shipped as M1 (`8625ea7`), including the
> pinned Node `minecraft-protocol` 1.66.2 interoperability lane that is now a
> required gate. The checkboxes below were never ticked and are not evidence; do
> not re-run this plan. `ROADMAP.md`'s P3a section is the summary of what landed.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or execute this plan inline one task at a time. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the framed `protocol.Codec` with an edition-neutral asynchronous stream that supports protocol 47 framing, compression, automatic transitions, developer controls, bounded resources, graceful disconnect, legacy ping, and conformance tests.

**Architecture:** A protocol creates one mutable session for each connection. The session owns packet coding, pipeline configuration, and transition rules. `protocol.Stream` runs separate read and write pumps while one coordinator orders state changes, controls, observations, budgets, and shutdown at complete frame boundaries.

**Tech Stack:** Go 1.26.5, the standard library, zlib, pinned PrismarineJS Java 1.8 data, generated protocol 47 codecs, Node.js 24, `minecraft-protocol` 1.66.2, Devbox, Task, gofumpt, gci, golangci-lint, the race detector, govulncheck, and gitleaks.

## Global Constraints

- Work in `/home/ocharnyshevich/pet.projects/go-theft-craft/minecraft-protocol`.
- Leave all changes uncommitted unless the user explicitly asks for a commit.
- Keep the public stream edition-neutral. Java VarInt framing, zlib, and `FE 01` handling stay in `wire/java` or the generated Java package.
- Replace `protocol.Codec`. Do not preserve it through a compatibility adapter.
- Keep encryption, authentication, automatic login, routing, middleware, capture storage, replay engines, `mcproto`, consumer migrations, and protocol 775 out of this plan.
- Start no goroutine and touch no transport in `NewStream`.
- Require one interrupt function that unblocks both transport directions.
- Use one shared `MaxQueueItems` budget and one shared `MaxBufferedBytes` budget for every stream-owned queue and buffer.
- Set `MaxBufferedBytes` to 32 MiB by default and 1 GiB at the hard ceiling.
- Never let a compression policy relax frame limits, decompressed limits, exact decompressed length, zlib validity, trailing-data rejection, integer safety, or allocation safety.
- Apply automatic and developer-defined changes only at complete frame boundaries.
- A successful `Write` means that the write pump wrote the complete frame.
- Keep observation delivery lossless and bounded. An observation failure terminates the stream.
- Do not commit Minecraft clients, server JARs, libraries, assets, natives, decompiled sources, or generated reference artifacts.
- Test community implementations only on loopback or in isolated CI jobs. Do not target public servers.
- Run focused tests after each task. Finish with `devbox run -- task verify` and `git diff --check`.
- At each review checkpoint, report the intended files and suggested commit message. Do not run `git commit` without explicit authorization.

---

### Task 1: Add a shared buffered-byte limit

**Files:**

- Modify: `limits.go`
- Modify: `limits_test.go`

**Interfaces:**

- Consumes: existing `NewLimits`, `LimitOption`, `MaxFrameBytes`, `MaxDecompressedBytes`, and `MaxQueueItems`.
- Produces: `MaxBufferedBytes(int) LimitOption` and `Limits.BufferedBytes() int`.

- [ ] **Step 1: Write failing default and ceiling tests**

  Add tests that assert a default of `32 << 20`, accept `MaxBufferedBytes(64<<20)`, reject zero, reject `(1<<30)+1`, and leave an invalid `Limits{}` value invalid.

- [ ] **Step 2: Write the cross-limit test**

  Construct limits where `BufferedBytes() < FrameBytes()+DecompressedBytes()`. Assert that `NewLimits` returns `ErrLimitExceeded` after all options run, regardless of option order.

- [ ] **Step 3: Prove the tests fail**

  Run `devbox run -- task test -- .`.

  Expected result: compilation fails because `MaxBufferedBytes` and `BufferedBytes` do not exist.

- [ ] **Step 4: Implement the limit**

  Add these constants and accessors:

  ```go
  const (
	defaultBufferedBytes = 32 << 20
	hardBufferedBytes    = 1 << 30
  )

  func MaxBufferedBytes(value int) LimitOption
  func (l Limits) BufferedBytes() int
  ```

  After applying every option, reject a configuration when `bufferedBytes < frameBytes+decompressedBytes`. Return an error that wraps `ErrLimitExceeded` and names all three values.

- [ ] **Step 5: Run focused checks**

  Run `devbox run -- task fmt`, `devbox run -- task test -- .`, and `devbox run -- task lint`.

- [ ] **Step 6: Review checkpoint**

  Inspect `git diff -- limits.go limits_test.go`. Suggested commit message if the user authorizes one: `feat(protocol): bound managed stream buffers`.

### Task 2: Replace `protocol.Codec` with session contracts

**Files:**

- Modify: `protocol.go`
- Create: `session.go`
- Create: `session_test.go`
- Create: `transport.go`

**Interfaces:**

- Consumes: `Protocol`, `Packet`, `Role`, `State`, `Direction`, and `Limits`.
- Produces: `Frame`, `Framer`, `Control`, `Transition`, `Snapshot`, `Session`, `Transport`, `TransitionPolicy`, and `Protocol.NewSession`.

- [ ] **Step 1: Write compile-time contract tests**

  Add test doubles that implement the new contracts. Assert that `NewFrame` rejects an empty wire buffer, a negative payload offset, and an offset beyond the wire length. Assert that `NewFrame` owns its input and that the payload view starts at the declared offset.

- [ ] **Step 2: Define the frame and transport contracts**

  Add these public shapes:

  ```go
  type Frame struct {
	wire          []byte
	payloadOffset int
  }

  func NewFrame(wire []byte, payloadOffset int) (Frame, error)
  func (f Frame) WireBytes() []byte
  func (f Frame) Payload() []byte

  type Framer interface {
	ReadFrame(io.Reader) (Frame, error)
	BuildFrame([]byte) (Frame, error)
	WriteFrame(io.Writer, Frame) error
  }

  type Transport struct {
	Reader    io.Reader
	Writer    io.Writer
	Interrupt func() error
  }
  ```

  `NewFrame` owns `wire`. Both accessors return read-only borrowed views for protocol implementations. Document that callers must not retain or mutate the views. Stream observations make owned copies before they leave the runtime.

- [ ] **Step 3: Define controls, transitions, and snapshots**

  Add these contracts:

  ```go
  type Control interface {
	ControlName() string
  }

  type StateControl struct {
	State State
  }

  type Transition struct {
	State   *State
	Control Control
  }

  type Snapshot struct {
	State    State
	Pipeline map[string]string
  }
  ```

  `StateControl.ControlName` returns `"state"`. Snapshot construction and access must clone the pipeline map.

- [ ] **Step 4: Define the session contract**

  Replace `Protocol.NewCodec` with:

  ```go
  type Protocol interface {
	ID() string
	Edition() Edition
	Version() Version
	NewSession(Role, Limits) (Session, error)
  }

  type Session interface {
	Framer() Framer
	Role() Role
	Limits() Limits
	State() State
	Inbound() Direction
	Outbound() Direction
	Snapshot() Snapshot
	ValidateState(State) error
	SetState(State)
	DecodeFrame([]byte) (Packet, error)
	EncodeFrame(Packet) ([]byte, error)
	ProposeTransition(Packet) (Transition, bool, error)
	ValidateTransition(Transition) error
	ApplyTransition(Transition)
	ValidateControl(Control) error
	ApplyControl(Control)
	Disconnect(string) (Packet, bool, error)
  }
  ```

  `ValidateTransition` and `ValidateControl` perform every check that can fail. Their matching apply methods must not fail. This split prevents an outbound transition from failing after bytes leave the process.

- [ ] **Step 5: Define transition policy**

  Add `TransitionContext`, `TransitionPolicy`, `TransitionPolicyFunc`, and `AcceptTransitions`. The policy signature is:

  ```go
  Resolve(context.Context, TransitionContext, Transition) (Transition, bool, error)
  ```

  `TransitionContext` includes the packet, whether the packet is inbound, and the session snapshot before the packet.

- [ ] **Step 6: Remove `Codec` and update root tests**

  Delete the `Codec` interface and every root-package reference to `NewCodec`. Do not change generated files by hand in this task.

- [ ] **Step 7: Run focused checks**

  Run `devbox run -- task fmt` and `devbox run -- task test -- .`.

  Expected result: root tests pass. Generated packages fail to compile until Task 5 replaces `NewCodec`.

- [ ] **Step 8: Review checkpoint**

  Inspect the new public names together. Suggested commit message if authorized: `feat(protocol): define connection session contracts`.

### Task 3: Split Java framing from packet envelopes

**Files:**

- Modify: `wire/java/frame.go`
- Modify: `wire/java/frame_test.go`
- Modify: `wire/java/errors.go`
- Create: `wire/java/framer.go`
- Create: `wire/java/framer_test.go`

**Interfaces:**

- Consumes: `protocol.Frame`, `protocol.Framer`, `protocol.Limits`, `ReadVarInt`, `PutVarInt`, and `writeFull`.
- Produces: `java.NewFramer(protocol.Limits) (protocol.Framer, error)`, `SplitPacketBody`, and `JoinPacketBody`.

- [ ] **Step 1: Write failing framer ownership tests**

  Cover empty frames, negative lengths, oversized frames before payload reads, overlong length VarInts, truncated payloads, one-byte readers, one-byte writers, and partial writes. Assert that raw observations include the length prefix while `Frame.Payload()` excludes it.

- [ ] **Step 2: Prove the tests fail**

  Run `devbox run -- task test -- ./wire/java`.

- [ ] **Step 3: Implement the Java framer**

  `ReadFrame` reads the VarInt length and payload into one owned wire buffer. `BuildFrame` prefixes one frame payload. `WriteFrame` uses `writeFull`. All three methods enforce `limits.FrameBytes()` before large allocation or transport writes. Add these helpers:

  ```go
  func NewFramer(limits protocol.Limits) (protocol.Framer, error)
  func SplitPacketBody(body []byte) (protocol.Packet, error)
  func JoinPacketBody(packet protocol.Packet, limits protocol.Limits) ([]byte, error)
  ```

- [ ] **Step 4: Keep raw packet helpers as low-level functions**

  Refactor `ReadRawPacket` and `WriteRawPacket` to use `SplitPacketBody` and `JoinPacketBody`. Preserve their existing exported behavior and compatibility tests. They no longer implement the generated protocol session.

- [ ] **Step 5: Run focused checks**

  Run `devbox run -- task fmt` and `devbox run -- task test -- ./wire/java`. Do not run the repository-wide linter until Task 5 regenerates the package against `Protocol.NewSession`.

- [ ] **Step 6: Review checkpoint**

  Inspect framing fixtures byte for byte. Suggested commit message if authorized: `refactor(java): separate frames from packet bodies`.

### Task 4: Implement bounded Java compression envelopes

**Files:**

- Modify: `wire/java/errors.go`
- Create: `wire/java/compression.go`
- Create: `wire/java/compression_test.go`
- Create: `wire/java/compression_fuzz_test.go`

**Interfaces:**

- Consumes: an unframed Java packet body, `protocol.Limits`, and one compression configuration.
- Produces: `CompressionControl`, `CompressionPolicy`, `StrictCompression`, `CompatibleCompression`, `DecodeCompression`, and `EncodeCompression`.

- [ ] **Step 1: Write the policy and boundary tests**

  Cover disabled compression, threshold `0`, threshold `1`, packets below, at, and above the threshold, compressed data below threshold, uncompressed data at threshold, corrupt zlib data, declared size zero, negative size, declared size above `DecompressedBytes`, exact output mismatch, concatenated zlib streams, and trailing bytes.

- [ ] **Step 2: Define compression controls**

  Add:

  ```go
  type CompressionControl struct {
	Enabled   bool
	Threshold int32
	Policy    CompressionPolicy
  }

  func (CompressionControl) ControlName() string

  type CompressionValidation struct {
	Compressed       bool
	EncodedBytes     int
	DecompressedBytes int
	Threshold        int32
  }

  type CompressionPolicy interface {
	Name() string
	ValidateThreshold(CompressionValidation) error
  }
  ```

  Export `StrictCompression` and `CompatibleCompression` as immutable policy values whose names are `"strict"` and `"compatible"`. `CompressionControl.ControlName` returns `"java.compression"`. Reject an enabled control with a negative threshold or nil policy.

- [ ] **Step 3: Implement mandatory validation outside policies**

  Parse the data-length VarInt before allocation. Enforce the frame and decompressed limits. Use `io.LimitedReader` and require one zlib stream, exact decompressed size, zlib EOF, and no trailing envelope bytes. Run policy validation only after mandatory checks pass. Export:

  ```go
  func DecodeCompression(framePayload []byte, control CompressionControl, limits protocol.Limits) ([]byte, error)
  func EncodeCompression(packetBody []byte, control CompressionControl, limits protocol.Limits) ([]byte, error)
  ```

- [ ] **Step 4: Implement outbound threshold behavior**

  When compression is enabled, emit data length `0` plus the original packet body below threshold. At or above threshold, emit the uncompressed length and one zlib stream. Reject output that exceeds `FrameBytes()` before framing.

- [ ] **Step 5: Add fuzz invariants**

  Fuzz `DecodeCompression` with arbitrary envelopes and small limits. Assert no panic and no successful output larger than `DecompressedBytes()`.

- [ ] **Step 6: Run focused checks**

  Run `devbox run -- task fmt` and `devbox run -- task test -- ./wire/java`. Do not run the repository-wide linter until Task 5 restores generated-package compilation.

- [ ] **Step 7: Review checkpoint**

  Inspect every allocation path. Suggested commit message if authorized: `feat(java): add bounded packet compression`.

### Task 5: Generate protocol 47 sessions instead of framed codecs

**Files:**

- Modify: `internal/codegen/generator/templates/protocol.go.tmpl`
- Modify: `internal/codegen/generator/generator_test.go`
- Modify: `internal/codegen/packetgen/generator_test.go`
- Regenerate: `generated/java/v1_8/protocol.go`
- Modify preserved test: `generated/java/v1_8/data_test.go`
- Modify preserved test: `generated/java/v1_8/codec_test.go`

**Interfaces:**

- Consumes: `protocol.Session`, `java.NewFramer`, generated packet factories, direct packet encoders, and Java compression functions.
- Produces: `v1_8.Protocol().NewSession(role, limits)` and a generated `protocolSession`.

- [ ] **Step 1: Replace descriptor tests**

  Update generated-package tests to call `NewSession`. Assert the initial handshaking state, role-derived directions, valid and invalid states, invalid roles, invalid limits, known packet round trips, unknown packet ownership, wrong envelopes, and trailing bytes.

- [ ] **Step 2: Prove generator tests fail**

  Run `devbox run -- task test -- ./internal/codegen/generator ./internal/codegen/packetgen ./generated/java/v1_8`.

- [ ] **Step 3: Generate the session**

  Replace the generated `protocolCodec` with `protocolSession`. `DecodeFrame` performs Java compression decoding, splits the packet ID, and invokes the generated value decoder. `EncodeFrame` validates the envelope, invokes the generated value encoder, joins the packet ID and body, and applies the current compression configuration. Generate a `ProposeTransition` method that returns `proposed=false`, a validator that accepts only an empty transition, a no-op apply method for that empty transition, and an unsupported `Disconnect` method. These temporary methods make `protocolSession` satisfy the complete interface. Task 6 replaces them with protocol 47 rules.

- [ ] **Step 4: Implement state and control validation**

  `ValidateState` accepts only handshaking, status, login, and play. `ValidateControl` accepts `protocol.StateControl` and `java.CompressionControl`. `ApplyControl` mutates state or compression only after validation.

- [ ] **Step 5: Expose snapshots**

  `Limits` returns the validated immutable limits used to construct the session. `Snapshot` returns state plus Java pipeline metadata with keys `compression.enabled`, `compression.threshold`, and `compression.policy`. Return a new map on each call.

- [ ] **Step 6: Regenerate deterministically**

  Run `devbox run -- task generate`, then `devbox run -- task generate:check`. A second generation must produce no diff.

- [ ] **Step 7: Run focused checks**

  Run `devbox run -- task fmt`, `devbox run -- task test -- ./internal/codegen/generator ./internal/codegen/packetgen ./generated/java/v1_8`, and `devbox run -- task lint`.

- [ ] **Step 8: Review checkpoint**

  Confirm generated runtime code has no `reflect`, `java.Marshal`, or `java.Unmarshal` call. Suggested commit message if authorized: `feat(java): generate protocol 47 sessions`.

### Task 6: Add protocol 47 transitions and disconnect packets

**Files:**

- Modify: `internal/codegen/generator/templates/protocol.go.tmpl`
- Modify: `internal/codegen/generator/generator_test.go`
- Regenerate: `generated/java/v1_8/protocol.go`
- Modify preserved test: `generated/java/v1_8/data_test.go`

**Interfaces:**

- Consumes: decoded generated packet values and current session role and state.
- Produces: `ProposeTransition`, `ValidateTransition`, `ApplyTransition`, and `Disconnect` for protocol 47.

- [ ] **Step 1: Write transition table tests for both roles**

  Cover handshake `NextState` values `1` and `2`, invalid values, login compression, play compression, negative set-compression thresholds that disable compression, login success, wrong-state values, inbound triggers, and outbound triggers. Assert that proposing a transition does not mutate the session.

- [ ] **Step 2: Write disconnect capability tests**

  Assert that a server-role login session creates `LoginClientboundDisconnect`, a server-role play session creates `PlayClientboundKickDisconnect`, and client-role, handshaking, and status sessions return `supported=false` without error.

- [ ] **Step 3: Implement generated transition matching**

  Match concrete generated packet types, not packet names alone. Return `protocol.StateControl` for state changes and `java.CompressionControl` for set-compression packets. A nonnegative threshold enables compression. A negative threshold disables it. Preserve the current compression policy in both cases.

- [ ] **Step 4: Make transition application atomic**

  Validate both the target state and the control before `ApplyTransition` changes either field. Store the complete validated transition so the apply method cannot fail.

- [ ] **Step 5: Implement disconnect construction**

  Encode the supplied reason through the generated string field. Let normal string limits reject an oversized reason before any write.

- [ ] **Step 6: Regenerate and run focused checks**

  Run `devbox run -- task generate`, `devbox run -- task generate:check`, `devbox run -- task fmt`, and `devbox run -- task test -- ./internal/codegen/generator ./generated/java/v1_8`.

- [ ] **Step 7: Review checkpoint**

  Inspect transition timing tests for both roles. Suggested commit message if authorized: `feat(java): define protocol 47 runtime transitions`.

### Task 7: Add the opt-in legacy `FE 01` hook

**Files:**

- Create: `preframe.go`
- Create: `preframe_test.go`
- Create: `wire/java/legacy_ping.go`
- Create: `wire/java/legacy_ping_test.go`

**Interfaces:**

- Consumes: a buffered transport reader and raw transport writer before normal framing starts.
- Produces: `protocol.PreFrameHook`, `java.LegacyPing`, `java.LegacyStatus`, and `java.NewLegacyPingHook`.

- [ ] **Step 1: Define the pre-frame interface**

  Add:

  ```go
  type PreFrameHook interface {
	HandlePreFrame(context.Context, *bufio.Reader, io.Writer) (bool, error)
  }
  ```

  Document that `false, nil` must leave every inspected byte buffered for the framer.

  Define the Java callback and values as:

  ```go
  type LegacyPing struct{}

  type LegacyStatus struct {
	ProtocolVersion int32
	Version         string
	MOTD            string
	OnlinePlayers   int
	MaxPlayers      int
  }

  type LegacyStatusHandler func(context.Context, LegacyPing) (LegacyStatus, error)

  func NewLegacyPingHook(LegacyStatusHandler) (protocol.PreFrameHook, error)
  ```

- [ ] **Step 2: Write claim and decline tests**

  Cover a complete `FE 01` request, a non-`FE` first byte, `FE` followed by a different byte, EOF after `FE`, one-byte readers, callback failure, oversized response strings, and exact byte preservation after decline.

- [ ] **Step 3: Implement the Java hook**

  Use `bufio.Reader.Peek`. Do not call `Read` before the hook claims `FE 01`. On claim, consume exactly two bytes, obtain `LegacyStatus` from the callback, and write the protocol 47 legacy response with UTF-16BE text and a full-write loop.

- [ ] **Step 4: Test one-shot stream integration with a stub stream start**

  Add a root test that proves a claimed hook prevents the normal framer from running and a declined hook passes the same buffered reader to the read pump.

- [ ] **Step 5: Run focused checks**

  Run `devbox run -- task fmt`, `devbox run -- task test -- . ./wire/java`, and `devbox run -- task lint`.

- [ ] **Step 6: Review checkpoint**

  Compare response bytes with a checked-in protocol 47 fixture. Suggested commit message if authorized: `feat(java): add legacy ping pre-frame hook`.

### Task 8: Implement shared stream budgets and lifecycle state

**Files:**

- Create: `stream_errors.go`
- Create: `stream_budget.go`
- Create: `stream_budget_test.go`
- Create: `stream.go`
- Create: `stream_test.go`

**Interfaces:**

- Consumes: a valid `Session`, `Transport`, and `Limits` already held by the session.
- Produces: `NewStream`, `StreamOption`, `WithTransitionPolicy`, `WithPreFrameHook`, `Start`, `Close`, and `Wait` scaffolding.

- [ ] **Step 1: Define stable stream errors**

  Add `ErrInvalidStream`, `ErrStreamNotStarted`, `ErrStreamStarted`, `ErrStreamClosing`, `ErrStreamClosed`, `ErrMalformedInbound`, `ErrAmbiguousWrite`, and `ErrObservation`. Preserve underlying I/O and codec errors through wrapping.

- [ ] **Step 2: Write configuration tests**

  Reject a nil session, nil reader, nil writer, nil interrupt function, nil options, nil configured policies, and a second `Start`. Assert that `NewStream` does not call reader, writer, interrupt, hook, or session methods that perform I/O.

- [ ] **Step 3: Implement the shared budget**

  Use one context-aware weighted budget for items and bytes. Reserve `FrameBytes()+DecompressedBytes()` of the byte limit as coordinator processing headroom. Queued packets and observations can use only the remainder. An acquisition succeeds only when both units fit. Cancellation removes the waiter without leaking capacity. Release wakes waiters in FIFO order. Add tests with blocked waiters, cancellation, full inbound queues, outbound progress, and concurrent release under the race detector.

- [ ] **Step 4: Reserve processing memory before codec calls**

  Serialize codec processing against the reserved headroom. Before `Framer.ReadFrame`, claim `FrameBytes()` of that headroom. Before inbound decompression, grow the claim by `DecompressedBytes()`. Outbound encode claims the complete headroom. After each operation, move only retained bytes into the queue budget. This prevents framing and decompression buffers from escaping the shared ceiling.

- [ ] **Step 5: Implement lifecycle state and first-cause storage**

  Store the first fatal cause once. `Wait` blocks until termination and then returns that cause. Graceful shutdown makes `Wait` return nil. Abortive local close makes `Wait` return `ErrStreamClosed`. `Close` is idempotent, calls the interrupt function once, and unblocks `Wait`. Expose:

  ```go
  func NewStream(session Session, transport Transport, options ...StreamOption) (*Stream, error)
  func WithTransitionPolicy(TransitionPolicy) StreamOption
  func WithPreFrameHook(PreFrameHook) StreamOption
  func (s *Stream) Start(context.Context) error
  func (s *Stream) Close() error
  func (s *Stream) Wait() error
  ```

- [ ] **Step 6: Run focused checks**

  Run `devbox run -- task fmt`, `devbox run -- task test -- .`, and `devbox run -- task lint`.

- [ ] **Step 7: Review checkpoint**

  Run the budget tests ten times with `go test -race -count=10 .`. Suggested commit message if authorized: `feat(protocol): add bounded stream lifecycle`.

### Task 9: Implement asynchronous `Read` and completed `Write`

**Files:**

- Create: `stream_runtime.go`
- Create: `stream_runtime_test.go`
- Create: `stream_test_helpers_test.go`
- Modify: `stream.go`

**Interfaces:**

- Consumes: session framing and packet methods, the shared budget, the transport, and lifecycle state.
- Produces: `Stream.Read(context.Context) (Packet, error)` and `Stream.Write(context.Context, Packet) error`.

- [ ] **Step 1: Build adversarial test transports**

  Add a one-byte reader, one-byte writer, blocking reader, blocking writer, partial writer, counting interrupt function, and ordered duplex transport. Each helper exposes channels so tests control exact scheduling without `time.Sleep`.

- [ ] **Step 2: Write failing read-pump tests**

  Assert frame order, bounded backpressure, packet ownership after `Read`, caller cancellation that leaves the packet queued, peer EOF as a fatal cause, malformed decode as a fatal cause, and transport interruption on stream-context cancellation.

- [ ] **Step 3: Write failing write-pump tests**

  Assert concurrent acceptance order, no success before the complete write, queue backpressure, invalid packets that do not terminate the stream, cancellation before dequeue with no bytes written, cancellation after write start that aborts the stream, partial-write termination, and stable first cause.

- [ ] **Step 4: Implement the pumps**

  The read pump performs pre-frame handling and `Framer.ReadFrame`, then submits owned frames to the coordinator. The write pump accepts only complete built frames, calls `Framer.WriteFrame`, and returns one result for each request.

- [ ] **Step 5: Implement coordinator packet processing**

  Decode inbound frame payloads and publish packets through the bounded inbound queue. Encode outbound packets, build frames, send one at a time to the write pump, and acknowledge the waiting caller after the write result.

  Expose:

  ```go
  func (s *Stream) Read(context.Context) (Packet, error)
  func (s *Stream) Write(context.Context, Packet) error
  ```

- [ ] **Step 6: Run focused race checks**

  Run `devbox run -- task fmt`, `devbox run -- task test -- .`, and `go test -race -count=20 .`.

- [ ] **Step 7: Review checkpoint**

  Confirm that tests use scheduler channels instead of timing guesses. Suggested commit message if authorized: `feat(protocol): add asynchronous managed stream`.

### Task 10: Serialize transitions and runtime controls

**Files:**

- Create: `stream_transition_test.go`
- Modify: `stream_runtime.go`
- Modify: `stream.go`

**Interfaces:**

- Consumes: `Session.ProposeTransition`, `TransitionPolicy.Resolve`, session validation and apply methods, and coordinator requests.
- Produces: automatic transition commits, `Stream.SetState`, and `Stream.Control`.

- [ ] **Step 1: Write inbound transition tests**

  Assert decode under the old snapshot, policy invocation, validation, commit before `Read` publication, replacement, suppression, policy error as a fatal cause, invalid replacement as a fatal cause, and the next queued raw frame decoding under the new snapshot.

- [ ] **Step 2: Write outbound transition tests**

  Assert encoding under the old snapshot, policy and validation before transport write, policy rejection as a nonfatal `Write` error, commit after successful full write, no commit after write failure, and no inbound decode while a transition-causing write remains unresolved.

- [ ] **Step 3: Write runtime control tests**

  Assert control ordering with concurrent writes, invalid controls that leave the stream running, state controls that affect only later coordinator work, and controls that do not reinterpret packets already in the `Read` queue.

- [ ] **Step 4: Implement transition resolution**

  Resolve and validate a proposal before an outbound write. Store the validated decision with the write request. Commit it after the write result. For inbound packets, resolve, validate, and commit before queue publication.

- [ ] **Step 5: Implement control requests**

  `SetState` submits `StateControl`. `Control` validates through the session, then applies on the coordinator goroutine. Cancellation before coordinator acceptance guarantees no change. After acceptance, the method waits for the local apply result because returning a context error for an applied control would be ambiguous.

  ```go
  func (s *Stream) SetState(context.Context, State) error
  func (s *Stream) Control(context.Context, Control) error
  ```

- [ ] **Step 6: Run protocol 47 transition tests**

  Run `devbox run -- task fmt`, `devbox run -- task test -- . ./generated/java/v1_8`, and `go test -race -count=20 .`.

- [ ] **Step 7: Review checkpoint**

  Trace the set-compression packet in both roles. Suggested commit message if authorized: `feat(protocol): order runtime stream transitions`.

### Task 11: Add lossless observations and graceful shutdown

**Files:**

- Create: `observation.go`
- Create: `observation_test.go`
- Create: `stream_shutdown_test.go`
- Modify: `stream.go`
- Modify: `stream_runtime.go`

**Interfaces:**

- Consumes: frame and packet commits, session snapshots, shared budgets, `Session.Disconnect`, and the transport interrupt function.
- Produces: `Observation`, `ObservationSink`, `WithObservationSink`, `Shutdown`, and final queue-drain behavior.

- [ ] **Step 1: Define immutable observations**

  Add stages `ObservationRawFrame` and `ObservationPacket`. Each record contains a monotonic sequence, frame correlation ID, direction, stage, before and after snapshots, packet metadata when available, and owned bytes. Do not expose generated `Packet.Value` through observations. Use these fields:

  ```go
  type PacketMetadata struct {
	State     State
	Direction Direction
	ID        int32
	Name      string
  }

  type Observation struct {
	Sequence    uint64
	Frame       uint64
	Direction   Direction
	Stage       ObservationStage
	Before      Snapshot
	After       Snapshot
	Packet      *PacketMetadata
	Bytes       []byte
  }
  ```

- [ ] **Step 2: Write observation tests**

  Assert global sequence order, raw and semantic correlation, bytes that do not alias pump buffers, snapshots that do not share maps, sink backpressure covered by shared budgets, sink context cancellation, and sink failure as the stable fatal cause.

- [ ] **Step 3: Implement a dedicated observation dispatcher**

  The coordinator submits lossless records to one bounded queue. The dispatcher calls:

  ```go
  type ObservationSink interface {
	Observe(context.Context, Observation) error
  }

  func WithObservationSink(ObservationSink) StreamOption
  ```

  Release each record's budget after `Observe` returns. Do not add best-effort dropping in this milestone.

- [ ] **Step 4: Write graceful shutdown tests**

  Cover rejection of new writes, draining accepted writes, disconnect as the final server frame in login and play, clean close for unsupported role and state, exactly one interrupt call, inbound queue draining, peer failure during drain, disconnect encoding failure, disconnect write failure, timeout changing to abortive close, and repeated `Shutdown` and `Close` calls.

- [ ] **Step 5: Implement `Shutdown`**

  Submit one shutdown request to the coordinator. Stop outbound acceptance, drain accepted work, request the optional disconnect packet, write it through the normal old-state pipeline, interrupt the transport, close delivery queues, and wait for all pumps and the observation dispatcher.

  ```go
  func (s *Stream) Shutdown(context.Context, string) error
  ```

- [ ] **Step 6: Run focused race checks**

  Run `devbox run -- task fmt`, `devbox run -- task test -- .`, and `go test -race -count=20 .`.

- [ ] **Step 7: Review checkpoint**

  Verify that no shutdown test sleeps. Suggested commit message if authorized: `feat(protocol): observe and gracefully stop streams`.

### Task 12: Add malformed-input fuzzing and localhost TCP scenarios

**Files:**

- Create: `stream_fuzz_test.go`
- Create: `stream_tcp_test.go`
- Modify: `wire/java/frame_test.go`
- Modify: `wire/java/compression_fuzz_test.go`
- Modify: `generated/java/v1_8/codec_test.go`

**Interfaces:**

- Consumes: the completed stream, protocol 47 sessions, Java framing, compression, and transition rules.
- Produces: local real-connection evidence and reproducible fuzz seeds.

- [ ] **Step 1: Add fuzz seeds from every malformed unit case**

  Seed negative and oversized lengths, overlong VarInts, truncated frames, corrupt zlib streams, decompression bombs, size mismatches, trailing data, and invalid transition sequences. Assert no panic and no output beyond configured limits.

- [ ] **Step 2: Implement the status TCP scenario**

  Listen on `127.0.0.1:0`. Connect one client-role stream and one server-role stream. Exchange handshake, status request, JSON response, ping, and pong. Assert automatic handshaking-to-status transitions and exact packet order.

- [ ] **Step 3: Implement the compressed login TCP scenario**

  Manually exchange login start, set compression, login success, representative play packets below and above threshold, and a server disconnect. Assert that set compression itself is uncompressed and every later packet uses the configured envelope.

- [ ] **Step 4: Add TCP fragmentation and shutdown variants**

  Wrap each `net.Conn` with one-byte reads and writes for one table case. Add a server shutdown timeout case and a client clean-close case.

- [ ] **Step 5: Run focused and repeated checks**

  Run `devbox run -- task test -- . ./wire/java ./generated/java/v1_8`, `go test -race -count=10 .`, and one bounded fuzz run for each target.

- [ ] **Step 6: Review checkpoint**

  Record exact test names and runtime. Suggested commit message if authorized: `test(protocol): cover managed stream TCP behavior`.

### Task 13: Add pinned Node interoperability tests

**Files:**

- Modify: `devbox.json`
- Modify: `devbox.lock`
- Modify: `Taskfile.yml`
- Create: `interop/node/package.json`
- Create: `interop/node/package-lock.json`
- Create: `interop/node/runner.mjs`
- Create: `interop/node_test.go`
- Modify: `THIRD_PARTY_NOTICES.md`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**

- Consumes: `minecraft-protocol` npm package version `1.66.2`, protocol 47 Go streams, loopback TCP, and newline-delimited JSON control messages.
- Produces: bidirectional 1.8.8 status, offline-login, compression, play-transition, and disconnect conformance tests.

- [ ] **Step 1: Add pinned Node tooling**

  Add Node.js 24 to Devbox. Pin `minecraft-protocol` to `1.66.2` in `package.json` and commit the exact npm lock file only when commits are authorized. Record BSD-3-Clause attribution in `THIRD_PARTY_NOTICES.md`.

- [ ] **Step 2: Define the runner protocol**

  `runner.mjs` accepts `--mode client` or `--mode server`, `--host`, `--port`, and `--scenario`. It writes one JSON object per state change and exits nonzero on unexpected packets, timeout, or disconnect reason. It never binds a non-loopback address.

- [ ] **Step 3: Test Go client against Node server**

  Start the Node offline server on an allocated loopback port. Drive the Go client through status and compressed offline login. Assert the Node transcript and Go observations agree on state, packet name, threshold, and disconnect reason.

- [ ] **Step 4: Test Node client against Go server**

  Start the Go server-role stream and run the Node client scenario. Drive set compression, login success, one play packet below threshold, one above threshold, and graceful disconnect.

- [ ] **Step 5: Add the required Task target**

  Add `test:interop` that runs `npm ci --prefix interop/node` and the Go interoperability package. Make `verify` depend on `test:interop`. Keep the existing Go race test target unchanged.

- [ ] **Step 6: Update CI**

  Keep one `verify` job. Devbox supplies Node. Cache the npm download directory by the lock-file hash without caching `node_modules` in the repository.

- [ ] **Step 7: Run the interoperability gate**

  Run `devbox run -- task test:interop` twice, then `devbox run -- task verify`.

- [ ] **Step 8: Review checkpoint**

  Confirm that every external process has a timeout and cleanup path. Suggested commit message if authorized: `test(protocol): verify protocol 47 interoperability`.

### Task 14: Update support status and complete the milestone

**Files:**

- Modify: `README.md`
- Modify: `ROADMAP.md`
- Modify: `CHANGELOG.md`
- Modify: `RELEASING.md` only if the new required interoperability gate changes release commands
- Review only: `docs/superpowers/specs/2026-08-14-managed-stream-compression-design.md`

**Interfaces:**

- Consumes: the implemented public API, passing tests, and approved design.
- Produces: accurate user documentation and final verification evidence.

- [ ] **Step 1: Update the API example**

  Replace `NewCodec` and manual `SetState` examples with `NewSession`, `NewStream`, `Start`, `Read`, `Write`, runtime controls, `Shutdown`, and `Wait`. State that construction performs no I/O.

- [ ] **Step 2: Update support boundaries**

  Document asynchronous framing, compression policies, automatic transitions, manual controls, graceful disconnect, the opt-in legacy hook, observation points, and resource budgets. Keep encryption and automatic login marked as planned.

- [ ] **Step 3: Revise the roadmap order**

  Split the current P3 entry so managed stream plus compression precedes encryption and login lifecycle. Keep server and `headless-minecraft` migration before protocol 775. Keep routing, capture storage, replay, and `mcproto` after protocol 775.

- [ ] **Step 4: Record deferred conformance work**

  Add Paper 26.1, MCProtocolLib, the instrumented vanilla-client lane, and the movement, combat, and crafting scenario matrix as deferred work. Name `minecraft-protocol`, `minecraft-simulation`, `headless-minecraft`, and `server` as their respective owners. State that an archived Paper 1.8 run is optional and not a P3 gate.

- [ ] **Step 5: Run the complete verification gate**

  Run:

  ```bash
  devbox run -- task generate:check
  devbox run -- task fmt
  devbox run -- task lint
  devbox run -- task secrets
  devbox run -- task test
  devbox run -- task test:interop
  devbox run -- task vuln
  devbox run -- task build
  git diff --check
  git status --short
  ```

  Expected result: every command passes. Only intended P3 files and pre-existing local design files remain changed or untracked.

- [ ] **Step 6: Inspect generated and public API drift**

  Run `rg -n "NewCodec|protocol\.Codec" --glob '*.go' --glob '*.md' .`. Any remaining reference must be an intentional migration note. Run generation a second time and confirm no diff.

- [ ] **Step 7: Final review checkpoint**

  Compare every completion criterion in the design with test evidence. Suggested commit message if authorized: `feat(protocol): add managed streams and compression`.
