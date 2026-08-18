# Schema-First Code Generation Implementation Plan

> **Status: complete, 2026-08-18.** Shipped as M2.5, before M3 migrated the
> server, so the consumer migrated once. Every schema-defined type is compiled
> from its own schema, named types are shared, decode recursion is bounded, and
> the hand-written `Position`, `Slot`, and `EntityMetadata` are gone. The
> boxes below are ticked by outcome, checked against this repository on
> 2026-08-18. Do not re-run this plan.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Compile every schema-defined type from the schema, keep hand-written codecs only for names the schema declares native, share named types instead of inlining them, and change no protocol 47 wire byte.

**Architecture:** `specialScalarRule`'s global name table is replaced by a lookup against the parsed schema's native set, so an override is impossible for a type the schema defines. Native invocations carry their schema arguments, which removes the hardcoded `endVal`. A per-protocol shared declaration space generates recursive and multi-packet types once, and generated decoders count nesting depth against `Limits.recursionDepth`. `java.Position`, `java.Slot`, and `java.EntityMetadata` are deleted once nothing references them.

**Tech Stack:** Go 1.26.5, Devbox, Task, standard library only, pinned Node `minecraft-protocol` 1.66.2 for the existing interoperability lane.

## Global Constraints

- Work in `/home/ocharnyshevich/pet.projects/go-theft-craft/minecraft-protocol`.
- Run every command as `devbox run -- task <name>`. Never call `go` directly.
- The module has **no external dependencies** and must still have none when this plan is finished.
- **Protocol 47 must encode and decode identical bytes at every task boundary.** The existing byte fixtures and the Node interoperability lane are the contract, and both run in every task's verification step.
- Do not add protocol 775, any new native, or any dataset. Those are M4.
- Do not touch `server`, `proxy`, or `headless-minecraft`. M3 owns the consumer migration and exists to happen after this plan.
- No generated runtime package may import `reflect`.
- Bound every allocation before making it.
- Leave changes uncommitted only when told to. Each task ends with a commit.
- Never add the `Co-Authored-By` or `Claude-Session` trailer to a commit message.
- Run `devbox run -- task precommit` before every commit.

## Dependencies

Sequenced after M2 so that two milestones do not regenerate `generated/java/v1_8`
at the same time. There is no code dependency: this plan touches
`internal/codegen`, `wire/java`, and generated output, while M2 touches the
stream, the conduit, and login.

Sequenced before M3 so that `server` migrates onto the final generated shape once.

## File Structure

**New files:**

| File | Responsibility |
| --- | --- |
| `internal/codegen/packetgen/native.go` | Schema-scoped native resolution and argument passing |
| `internal/codegen/packetgen/native_test.go` | Native-set scoping, argument mismatch, dead overrides |
| `internal/codegen/packetgen/shared.go` | Shared declaration space and the sharing rule |
| `internal/codegen/packetgen/shared_test.go` | Recursion, reuse threshold, naming determinism |
| `internal/codegen/packetgen/templates/types.go.tmpl` | Shared type declarations |
| `wire/java/depth.go` | Per-packet recursion counter on the buffer |
| `wire/java/depth_test.go` | Limit, reset, sequential packets |
| `generated/java/v1_8/types.go` | Generated shared types for protocol 47 |
| `generated/java/v1_8/roundtrip_test.go` | Decode and re-encode every fixture packet |

**Modified files:**

| File | Change |
| --- | --- |
| `internal/codegen/packetgen/model.go` | Native lookup, arguments, shared references, depth calls |
| `internal/codegen/packetgen/generator.go` | Emit shared declarations |
| `wire/java/value_types.go` | Remove `Position`, `Slot`, `EntityMetadata`, `EntityMetadataEntry`, `EntityMetadataType` |
| `wire/java/nbt.go` | Remove `ReadSlot`, `WriteSlot`, `ReadEntityMetadata`, `WriteEntityMetadata`; keep the NBT codec |
| `wire/java/buffer.go` | Remove `ReadPosition` and `WritePosition`; add the depth counter and the parameterized metadata loop |
| `generated/java/v1_8/*.go` | Regenerated output |
| `README.md`, `CHANGELOG.md`, `ROADMAP.md` | Documentation |
| `../headless-minecraft/MASTER_PLAN.md` | Milestone records |

---

### Task 1: Pin protocol 47's bytes before changing anything

**Files:**
- Create: `generated/java/v1_8/roundtrip_test.go`
- Create: `wire/java/position_vector_test.go`

**Interfaces:**
- Produces: no production code. This task exists to make the rest of the plan verifiable.

- [x] **Step 1: Write the round-trip test**

For every packet with a checked-in byte fixture: decode it, re-encode it, and
compare bytes. For packets without a fixture, generate a deterministic value from
a fixed seed, encode it, decode it, and compare fields. The test must fail
loudly if a packet is added later with neither a fixture nor a generated value,
so coverage cannot silently shrink.

- [x] **Step 2: Pin the bit layouts by value, not by agreement**

Assert the current `position` encoding against hand-computed bytes for at least:
`(0, 0, 0)`, `(1, 2, 3)`, a negative coordinate on each axis, and the extremes of
each field's range. Do the same for one entity-metadata sequence including its
`127` terminator.

These assertions are the ones that will catch a wrong bit order after
Task 3, and they must be written while the old codec is still in place so they
describe observed behavior rather than intended behavior.

- [x] **Step 3: Run and verify they pass**

`devbox run -- task test -- ./generated/java/v1_8 ./wire/java`. Expected: green
against the current generator. A failure here means the current codec disagrees
with hand-computed bytes, which is a finding to resolve before proceeding.

- [x] **Step 4: Commit** as `test(java): pin protocol 47 wire bytes`.

### Task 2: Recursion depth on the buffer

**Files:**
- Create: `wire/java/depth.go`, `wire/java/depth_test.go`
- Modify: `wire/java/buffer.go`

**Interfaces:**
- Produces: `(*Buffer).EnterNested() error`, `(*Buffer).LeaveNested()`, `ErrRecursionDepth`.

- [x] **Step 1: Write the failing test**

Nesting one past `Limits.recursionDepth` returns `ErrRecursionDepth` naming the
path; nesting exactly to the limit succeeds; the counter returns to zero after a
failed decode; two sequential packets do not accumulate depth.

- [x] **Step 2: Run and verify failure**

`devbox run -- task test -- ./wire/java`.

- [x] **Step 3: Implement**

An `int` on `Buffer`, reset wherever the buffer is reset for a new packet. No
synchronisation: a buffer belongs to one session.

- [x] **Step 4: Commit** as `feat(java): bound decode recursion depth`.

### Task 3: Native means native in this schema

**Files:**
- Create: `internal/codegen/packetgen/native.go`, `internal/codegen/packetgen/native_test.go`
- Modify: `internal/codegen/packetgen/model.go`
- Modify: `wire/java/buffer.go` (parameterized metadata loop)

**Interfaces:**
- Produces: `nativeRule(schema *protodef.Schema, name string) (scalarRule, bool)`, native argument binding, and `ErrUnknownNativeArgument`.

- [x] **Step 1: Write the failing test**

Against small hand-written schemas in `testdata`, not the real protocol files:

- a schema that declares `position` native gets the hand-written codec;
- a schema that defines `position` as a bitfield gets a generated type, even
  though a hand-written codec of that name exists;
- a hand-written codec whose name no schema declares native is reported as dead
  configuration at generation time;
- `entityMetadataLoop` invoked with `endVal: 255` generates a call carrying 255,
  and one invoked with `endVal: 127` carries 127;
- a native invoked with an argument the codec does not accept is a generation
  error naming the JSON path.

- [x] **Step 2: Run and verify failure**

`devbox run -- task test -- ./internal/codegen/packetgen`.

- [x] **Step 3: Implement the lookup and the arguments**

Thread the parsed schema's native set into the model builder and consult it
before the hand-written table. Make the metadata loop codec take `endVal` as a
parameter, and pass every native's schema arguments through to its call site.

- [x] **Step 4: Regenerate and expect the fixtures to speak**

`devbox run -- task generate` then `devbox run -- task test`.

Protocol 47 now compiles `position`, `slot`, and `entityMetadata` from the
schema. Task 1's byte assertions are what prove the result is identical. If
`slot` fails, the hand-written codec encoded something the schema does not
express: record exactly what, in the commit message and in
`MASTER_PLAN.md`, because the same gap will exist for protocol 775. Do not
restore the override to make the test pass.

- [x] **Step 5: Run the interoperability lane**

`devbox run -- task test:interop`. Expected: unchanged.

- [x] **Step 6: Commit** as `fix(codegen): scope native codecs to their schema`.

### Task 4: Shared named types

**Files:**
- Create: `internal/codegen/packetgen/shared.go`, `internal/codegen/packetgen/shared_test.go`
- Create: `internal/codegen/packetgen/templates/types.go.tmpl`
- Modify: `internal/codegen/packetgen/model.go`, `internal/codegen/packetgen/generator.go`

**Interfaces:**
- Produces: `SharedType{SchemaName, GoName string; Declaration Declaration; Recursive bool}`, `Protocol.SharedTypes []SharedType`.

- [x] **Step 1: Write the failing test**

Against `testdata` schemas: a self-recursive type generates one Go type whose
recursive edge is a slice or pointer; a mutually recursive triple generates three
and terminates; a type used by two packets is shared and one used by a single
packet is inlined; names are deterministic across runs and stable under packet
reordering; a collision between a schema type name and a packet type name
resolves deterministically; generated shared decoders call `EnterNested` and
`LeaveNested` on every exit path.

- [x] **Step 2: Run and verify failure**

- [x] **Step 3: Implement the sharing rule**

Two passes before compilation: count referring packets per named type and find
strongly connected components; then mark a type shared when it is in a
non-trivial component, is self-referential, or has two or more referring packets.
Compile shared types first, in sorted schema-name order, into a package-level
declaration list.

- [x] **Step 4: Emit `types.go` and regenerate**

Protocol 47 produces shared declarations for `position` (16 packets), `slot`
(7), and `entityMetadata` (3). `string` resolves to a scalar and is not
declared.

`devbox run -- task generate`, `devbox run -- task test`,
`devbox run -- task test:interop`. Expected: identical bytes, identical interop
results, a large mechanical diff in `generated/java/v1_8`.

- [x] **Step 5: Commit** as `feat(codegen): share named protocol types`.

### Task 5: Delete the hand-written value types

**Files:**
- Modify: `wire/java/value_types.go` (`Position`, `Slot`, `EntityMetadata`, `EntityMetadataEntry`, `EntityMetadataType`)
- Modify: `wire/java/nbt.go` (`ReadSlot`, `WriteSlot`, `ReadEntityMetadata`, `WriteEntityMetadata`)
- Modify: `wire/java/buffer.go` (`ReadPosition`, `WritePosition`)
- Modify: their tests

**Interfaces:**
- Removes: `java.Position`, `java.Slot`, `java.EntityMetadata`, and their read and write methods.

- [x] **Step 1: Prove they are unreferenced**

`grep -rn 'java\.Position\|java\.Slot\|java\.EntityMetadata' --include='*.go' .`
Expected: matches only in the files being deleted and their tests.

- [x] **Step 2: Delete**

Remove the types, their codecs, and the tests that only exercised them. Keep any
test whose subject is a buffer primitive rather than the value type.

- [x] **Step 3: Verify**

`devbox run -- task verify`. Expected: generate check, lint, tests with race,
interop, vulnerability check, and build all pass.

- [x] **Step 4: Commit** as `refactor(java): remove superseded hand-written value types`.

### Task 6: Documentation and milestone records

**Files:**
- Modify: `README.md`, `CHANGELOG.md`, `ROADMAP.md`, `../headless-minecraft/MASTER_PLAN.md`

- [x] **Step 1: Document the rule**

README states the generator's contract in one paragraph: a hand-written codec
backs a name only when the schema declares it native, and every other type is
compiled. CHANGELOG records the removal of the three value types as a breaking
change to the generated API, with the reason.

- [x] **Step 2: Update the records**

`MASTER_PLAN.md`: M2.5 complete, with any gap Task 3 Step 4 discovered between
the hand-written `slot` codec and the schema.

- [x] **Step 3: Inspect final scope**

`git status --short` and `git diff --check`. Confirm: no protocol 775 anywhere;
no new dataset; no consumer repository touched; `go.mod` still has no `require`
block.

- [x] **Step 4: Commit** as `docs: record schema-first code generation`.
