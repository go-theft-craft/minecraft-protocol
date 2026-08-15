# Java 1.8 Protocol Codecs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or execute this plan inline one task at a time. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish a complete built-in `java/1.8.9` protocol 47 descriptor whose generated packet codecs do not use reflection.

**Architecture:** Parse the pinned PrismarineJS `protocol.json` into a validated internal type graph, then generate concrete packet values and encode/decode methods backed by bounded Java wire helpers. The immutable built-in descriptor creates per-connection codecs by role and resolves packets by state, direction, and ID. The existing reflection bridge remains as a compatibility API, but the built-in does not call it.

**Tech Stack:** Go 1.26.5, the standard library, pinned PrismarineJS Java 1.8 data, `text/template`, Devbox, Task, gofumpt, gci, golangci-lint, the race detector, govulncheck, and gitleaks.

## Global Constraints

- Work in `/home/ocharnyshevich/pet.projects/go-theft-craft/minecraft-protocol`.
- Leave all changes uncommitted unless the user explicitly asks for a commit.
- Keep `mcdata-gen`, its templates, the pinned source snapshot, and generated output in `minecraft-protocol`.
- Generate exclusively from `source/java/1.8/protocol.json`; do not download or copy new upstream artifacts.
- Preserve the public `wire/java` reflection compatibility functions, but do not call them from the generated built-in codec.
- Bound every string, collection, NBT value, plugin payload, recursion step, and frame through a valid `protocol.Limits` value.
- Decode unknown packet IDs to an owned raw payload rather than guessing a packet type.
- Reject duplicate packet IDs, duplicate packet names, invalid state or direction combinations, unresolved named types, unclassified ProtoDef operators, and trailing bytes after a known packet.
- Generated output must be deterministic and must contain no `reflect` import.
- Preserve the existing caller-owned game-data behavior and `data.Register("java/1.8.9", Data)` registration.
- Run focused tests during each task and finish with `devbox run -- task verify` plus server protocol compatibility tests.

---

### Task 1: Parse the complete Java 1.8 ProtoDef graph

**Files:**

- Create: `internal/codegen/protodef/ast.go`
- Create: `internal/codegen/protodef/parser.go`
- Create: `internal/codegen/protodef/parser_test.go`
- Create: `internal/codegen/protodef/testdata/`

**Interfaces:**

- Consumes: the complete bytes of `source/java/1.8/protocol.json`.
- Produces: `protodef.Parse([]byte) (*Schema, error)`, a deterministic state/direction packet inventory, named type definitions, and recursive type nodes for aliases, primitives, containers, arrays, switches, options, buffers, mappers, bitfields, bitflags, and protocol-native types.

- [ ] **Step 1: Write failing parser tests**

  Add minimal fixtures for every operator used by the pinned source. Assert exact decoded nodes and JSON-path errors for malformed nodes, missing switch fields, invalid count references, duplicate fields, unresolved named types, alias cycles, duplicate packet IDs, and duplicate packet names.

- [ ] **Step 2: Prove the parser tests fail**

  Run `devbox run -- task test -- ./internal/codegen/protodef` and retain the expected missing-package or missing-symbol failure in the task report.

- [ ] **Step 3: Implement the recursive AST and parser**

  Keep source ordering for container fields and packet inventories. Sort only map-derived names used for validation or generated output. Preserve switch keys as source strings because ProtoDef uses numeric and symbolic discriminators.

- [ ] **Step 4: Validate the pinned protocol**

  Add a test that parses `../../../source/java/1.8/protocol.json`, resolves every reachable named type, and asserts all four states and both wire directions have a packet map and packet switch.

- [ ] **Step 5: Run focused checks**

  Run `devbox run -- task fmt`, `devbox run -- task test -- ./internal/codegen/protodef`, and `devbox run -- task lint`.

### Task 2: Add bounded values and payload I/O

**Files:**

- Create: `wire/java/value_types.go`
- Create: `wire/java/buffer.go`
- Create: `wire/java/buffer_test.go`
- Create: `wire/java/nbt.go`
- Create: `wire/java/nbt_test.go`
- Modify: `wire/java/errors.go`

**Interfaces:**

- Consumes: a valid `protocol.Limits` and one packet payload.
- Produces: `java.Buffer`, owned `java.NBT`, `java.Slot`, `java.EntityMetadata`, and methods needed by generated code for all Java 1.8 ProtoDef primitives and native aliases.

- [ ] **Step 1: Write failing boundary tests**

  Cover truncated values at every byte, negative lengths, collection and string limits, plugin payload limits, recursion depth, all NBT tag shapes, optional NBT, absent and present slots, entity metadata terminators, mapper failures, bitfields, remaining bytes, and trailing bytes.

- [ ] **Step 2: Prove the boundary tests fail**

  Run `devbox run -- task test -- ./wire/java` and retain the expected missing-symbol failures in the task report.

- [ ] **Step 3: Implement one bounded payload buffer**

  `NewReadBuffer(payload []byte, limits protocol.Limits)` must own its input. `NewWriteBuffer(limits protocol.Limits)` accumulates at most `limits.FrameBytes()`. Every read and write error includes the logical field path supplied by generated code. Expose `Bytes()` as an owned copy and `Remaining()` for exact-consumption checks.

- [ ] **Step 4: Implement NBT, slot, and entity metadata values**

  Preserve semantic values and enough type information for lossless re-encoding. Reject invalid tag IDs, negative array lengths, duplicate compound keys, over-limit payloads, and nesting deeper than `limits.RecursionDepth()`.

- [ ] **Step 5: Run focused and race checks**

  Run `devbox run -- task fmt`, `devbox run -- task test -- ./wire/java`, and `devbox run -- task lint`.

### Task 3: Build the deterministic packet generation model

**Files:**

- Create: `internal/codegen/packetgen/model.go`
- Create: `internal/codegen/packetgen/model_test.go`

**Interfaces:**

- Consumes: `*protodef.Schema`.
- Produces: `packetgen.Build(*protodef.Schema, Options) (*Model, error)` with ordered states, directions, packet declarations, nested Go type declarations, encode/decode operations, factories, mapper tables, and stable field paths.

- [ ] **Step 1: Write failing model tests**

  Cover a fixed packet, nested and anonymous containers, fixed and counted arrays, switches with numeric and mapper cases, options, buffers, bitfields, NBT slots, entity metadata, colliding exported names, and unsupported source nodes.

- [ ] **Step 2: Prove model tests fail**

  Run `devbox run -- task test -- ./internal/codegen/packetgen` and retain the expected missing-symbol failures in the report.

- [ ] **Step 3: Build one deterministic model**

  Derive exported Go names without collisions, preserve source field and packet order, and give anonymous switch/container fields stable generated names. Represent every read and write as an explicit typed operation that names the required `java.Buffer` method and logical field path. Fail when a source construct has no rule; never silently replace a classified construct with `[]byte`.

- [ ] **Step 4: Run focused checks**

  Run `devbox run -- task fmt`, `devbox run -- task test -- ./internal/codegen/packetgen`, and `devbox run -- task lint`.

### Task 4: Render reflection-free packet source

**Files:**

- Create: `internal/codegen/packetgen/generator.go`
- Create: `internal/codegen/packetgen/generator_test.go`
- Create: `internal/codegen/packetgen/templates/packets.go.tmpl`
- Create: `internal/codegen/packetgen/templates/codec.go.tmpl`
- Create: `internal/codegen/packetgen/templates/descriptor.go.tmpl`

**Interfaces:**

- Consumes: `*packetgen.Model` and a generated package name.
- Produces: `packetgen.Generate(*Model, Options) (map[string][]byte, error)` with formatted `packets.go`, `codec.go`, and `descriptor.go` source.

- [ ] **Step 1: Write failing golden-source tests**

  Assert exact formatted output for each model construct and compile one representative generated package in a temporary module. Assert output contains no `reflect`, `java.Marshal`, or `java.Unmarshal` import or call.

- [ ] **Step 2: Prove rendering tests fail**

  Run `devbox run -- task test -- ./internal/codegen/packetgen` and retain the expected failures.

- [ ] **Step 3: Render direct methods and registries**

  Generated packet methods call typed `java.Buffer` operations directly. Generated decode methods assign only after each operation succeeds. Mapper and bitfield failures include the generated field path. Factory tables allocate exact concrete packet pointer types and reject duplicate keys during generation.

- [ ] **Step 4: Run focused checks**

  Run `devbox run -- task fmt`, `devbox run -- task test -- ./internal/codegen/packetgen`, and `devbox run -- task lint`.

### Task 5: Integrate generation and expose the protocol 47 descriptor

**Files:**

- Modify: `protocol.go`
- Modify: `internal/codegen/generator/generator.go`
- Modify: `internal/codegen/generator/generator_test.go`
- Modify: `internal/codegen/generator/templates/packets.go.tmpl`
- Modify: `internal/codegen/generator/templates/protocol.go.tmpl`
- Modify: `cmd/mcdata-gen/main.go`
- Regenerate: `generated/java/v1_8/`

**Interfaces:**

- Consumes: `*protodef.Schema`, `java.Buffer`, `protocol.Packet`, `protocol.Role`, `protocol.State`, and valid `protocol.Limits`.
- Produces: concrete generated packet types, reflection-free `Encode` and `Decode` methods, factories keyed by state/direction/ID, `protocol.UnknownPacket`, state access on `protocol.Codec`, `v1_8.Protocol() protocol.Protocol`, and per-connection codecs returned by `NewCodec`.

- [ ] **Step 1: Write failing integration and descriptor tests**

  Cover unknown packet ownership, wrong direction, wrong state, invalid role, explicit state changes, duplicate registry keys, and known-packet trailing bytes. Assert checked-in generated files contain no import or call involving `reflect`, `java.Marshal`, or `java.Unmarshal`.

- [ ] **Step 2: Prove generation tests fail**

  Run `devbox run -- task test -- ./internal/codegen/generator ./generated/java/v1_8` and retain the expected failures in the task report.

- [ ] **Step 3: Add stateful codec contracts and generated factories**

  Generated packet methods call typed `java.Buffer` operations directly. Packet factories allocate the exact concrete type. Extend `protocol.Codec` with `State() protocol.State` and `SetState(protocol.State) error`; the Java 1.8 codec starts in `handshaking`, validates the four supported states, derives inbound and outbound directions from its role, and rejects envelopes that do not match its role or current state. Export the four state constants from `v1_8`. Unknown IDs return a `protocol.Packet` whose `Value` is an owned `protocol.UnknownPacket` and whose `Payload` is a separate owned copy.

- [ ] **Step 4: Integrate generation atomically**

  Keep `mcdata-gen -check` as the source of truth. Extend its explicit generated-file inventory and preserve the existing atomic replacement and rollback behavior.

- [ ] **Step 5: Regenerate and verify stability**

  Run `devbox run -- task generate`, `devbox run -- task generate:check`, and the focused tests. A second generation must produce no diff.

### Task 6: Prove protocol 47 compatibility and finish P1 documentation

**Files:**

- Create: `generated/java/v1_8/codec_test.go`
- Modify: `wire/java/compat_test.go`
- Modify: `README.md`
- Modify: `ROADMAP.md`
- Modify: `CHANGELOG.md`

**Interfaces:**

- Consumes: the generated `v1_8.Protocol()` and checked-in protocol 47 fixtures.
- Produces: compatibility evidence for handshake, status, login, and representative play packets, plus accurate support and roadmap documentation.

- [ ] **Step 1: Add wire compatibility fixtures**

  Compare generated encoding with checked-in protocol 47 byte vectors. Decode the same vectors through the generated descriptor and assert state, direction, ID, name, concrete value, and exact re-encoding. Include unknown packet payload ownership.

- [ ] **Step 2: Run protocol and server compatibility tests**

  Run `devbox run -- task test -- ./generated/java/v1_8 ./wire/java`, then run the server's protocol-focused tests through its Devbox Task wrapper. Record the exact server packages and results.

- [ ] **Step 3: Update project status**

  Document the built-in protocol 47 descriptor and generated codecs. Mark Java 1.8 extraction complete while leaving compression, encryption, complete login, Java 26.1, and managed streams in their existing later milestones.

- [ ] **Step 4: Run the complete verification gate**

  Run `devbox run -- task verify`, `git diff --check`, and `git status --short`. Confirm generated output is current and no normal generated runtime path imports `reflect`.
