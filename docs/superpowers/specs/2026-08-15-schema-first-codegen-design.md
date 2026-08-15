# Schema-first code generation design

- Status: Draft for review
- Date: 2026-08-15
- Repository: `minecraft-protocol`
- Milestone: M2.5

## Context

The generator overrides six schema type names with hand-written codecs:
`nbt`, `optionalNbt`, `slot`, `entityMetadata`, `entityMetadataLoop`, and
`position`. The override is keyed on the bare name, in `specialScalarRule`
(`internal/codegen/packetgen/model.go:885`), with no reference to which schema
is being compiled.

For protocol 47 that is invisible, because there is one schema. For protocol 775
it is a wire bug waiting to happen, and it was found by comparing the two
schemas rather than by running anything:

| Type | Protocol 47 | Protocol 775 |
| --- | --- | --- |
| `position` | bitfield x(26), **y(12)**, z(26) | bitfield x(26), **z(26)**, y(12) |
| `entityMetadata` | `entityMetadataLoop` with `endVal: 127` | `entityMetadataLoop` with `endVal: 255` |

Both names exist in both schemas. Under name-keyed overrides, protocol 775 would
be handed the 1.8 codec: every coordinate encoded with the wrong bit order, and
entity metadata terminated on the wrong sentinel. Each protocol's own round-trip
tests would pass, because each would be self-consistent and wrong in the same
way in both directions.

Three of those six names — `position`, `slot`, `entityMetadata` — are not
declared `native` in either schema. They are ordinary schema definitions that the
generator declines to compile.

## Goals

- A hand-written codec may override a schema type only when that schema declares
  the name `native`. Everything else compiles from the schema.
- `entityMetadataLoop` stays native in both schemas and takes `endVal` as a
  schema argument rather than a constant.
- Named types that are recursive, or used by more than one packet, are generated
  once and referenced, instead of inlined per packet.
- Decoding counts nesting depth against the existing `Limits.recursionDepth`.
- Protocol 47 encodes and decodes exactly the same bytes afterwards.

## Non-goals

- Protocol 775. This milestone compiles one schema and changes no dataset.
- Consumer migration. M3 owns that, and this milestone exists so that M3 has one
  stable target.
- New natives. The six protocol 775 natives arrive in M4.

## Why this lands before M3

M3 migrates `server` onto the generated protocol 47 codecs. This milestone
changes what those codecs look like: `java.Position` becomes a generated bitfield
struct, `java.Slot` a generated container, and `java.EntityMetadata` a generated
loop over a generated entry type. Those three types appear in most gameplay
packets.

Landing this after M3 would migrate every call site in `server` twice. Landing it
inside M3 would mix a code-generation change into a consumer milestone. Landing
it before M3 costs one small milestone and gives the migration a final target.

It also means protocol 47 exercises the shared-type path for the whole of M3,
M4, and beyond. A code path that only 775 uses would have no coverage until 775
exists, which is the wrong order for the piece of the generator most likely to be
subtly wrong.

This milestone depends on M2 only for sequencing, not for code. It touches
`internal/codegen`, `wire/java`, and `generated/java/v1_8`; M2 touches the
stream, the conduit, and login. Running it after M2 avoids two milestones
regenerating `v1_8` at once.

## Decision 1: native means native in this schema

`specialScalarRule` is replaced by a lookup that consults the parsed schema's
native set first. A name is eligible for a hand-written codec only if that
schema declares it `native`. A name the schema defines is compiled, whatever it
is called.

For protocol 47 this promotes `position`, `slot`, and `entityMetadata` from
hand-written to generated. `nbt` and `optionalNbt` stay hand-written, because 1.8
declares them native. For protocol 775 the same rule keeps `anonymousNbt`,
`anonOptionalNbt`, `registryEntryHolder`, `registryEntryHolderSet`, `lpVec3`, and
`topBitSetTerminatedArray` hand-written, and compiles `Slot`, `position`, and
`entityMetadata` from the schema.

The rule needs no per-version list, so a new version cannot be broken by someone
forgetting to update one. A hand-written codec for a name a schema does not
declare native is now unreachable, and generation reports it as dead
configuration rather than silently preferring it.

## Decision 2: native invocations take their schema arguments

`entityMetadataLoop` is native in both schemas and is invoked with an `endVal`
and an entry type. The hand-written codec currently hardcodes 127.

Native codecs become parameterized at the call site: the generator passes the
schema's arguments through, and a native that receives an argument it does not
understand is a generation error. `endVal` stops being a constant in Go and
becomes what it already is in the schema — an argument.

This is the general form of the bug in the context section. A native that ignores
its arguments is a codec that ignores the schema.

## Decision 3: shared named types

A named type is generated once, as an exported type in the version package, when
it is recursive or referenced by two or more packets. Everything else stays
inline, as today. The rule is a pure function of the schema, so output stays
reproducible.

Protocol 47 has four such types after Decision 1: `string`, `position`, `slot`,
and `entityMetadata`. `string` resolves to a scalar, and the other three become
shared declarations used by 16, 7, and 3 packets. Without sharing, schema-first
compilation would inline a full slot container into every packet carrying one.

Recursion terminates in Go because every recursive edge in a Minecraft schema
crosses a slice or a pointer. It terminates at decode time because a shared
decoder increments a depth counter on the buffer, checked against
`Limits.recursionDepth`. Protocol 47 has no recursive type; the counter is built
here because it belongs with the shared-type machinery, and 775 needs it.

## Decision 4: the hand-written value types are deleted

`java.Position`, `java.Slot`, and `java.EntityMetadata` have no remaining
callers once the generator stops emitting references to them. They are removed
rather than kept as conversions.

Keeping them would mean a mapping layer per version per type, and would suggest
that a 47 slot and a 775 slot are the same value, which they are not: one carries
a block ID, a count, damage, and gzip NBT; the other carries a count, an item ID,
and a list of data components. A shared type across that gap is a lie with a
conversion function attached.

`java.UUID`, `java.NBT`, and the buffer primitives stay. They back names the
schemas genuinely declare native.

## Verification

The exit criterion is byte equality, not review:

- The existing protocol 47 byte fixtures pass unchanged. They are the contract.
- The pinned Node `minecraft-protocol` interoperability lane passes unchanged,
  including the encrypted scenarios M2 adds.
- A round-trip test over every generated 47 packet: decode a fixture, re-encode,
  compare bytes.
- `grep` proves `java.Position`, `java.Slot`, and `java.EntityMetadata` are gone
  from the tree, and that no generated package imports `reflect`.
- A test asserts the new position codec against a known coordinate encoded by
  hand, so the bit order is pinned by a value rather than by the generator
  agreeing with itself.

The generated diff will be large and mostly mechanical. The fixtures and the
interop lane are what make it reviewable.

## Risks

**The diff hides a real change.** A thousand-line generated diff is where a
genuine regression goes unnoticed. Byte fixtures and the interop lane are the
mitigation, and the round-trip test over every packet is the backstop. Reviewing
the diff by reading it is not the plan.

**`slot` in 1.8 carries NBT with its own quirks.** The hand-written codec may
encode knowledge the schema does not express. If the fixtures fail after
compiling `slot` from the schema, that difference is a finding worth writing
down, not a reason to restore the override — it means the schema is
insufficient, and that needs to be true for 775 as well.

**Scope creep into M4.** The temptation is to add the six 775 natives while the
native machinery is open. They stay in M4, because this milestone's exit
criterion is that protocol 47 is unchanged on the wire, and 775 natives cannot
be tested against that.
