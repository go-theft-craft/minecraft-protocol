# Correcting the Java 26.1 Block Material Regression

- Status: Approved 2026-08-17
- Repository: `minecraft-protocol`
- Milestone: prerequisite for M9.4; no milestone currently owns it

## The problem

Java 26.1's block dataset assigns 108 blocks a material that describes a tool
*penalty* rather than the tool that mines them. Every ore, every metal block,
and obsidian carry `material: "incorrect_for_wooden_tool"`, whose tool-speed
table covers four wooden-tier item IDs and nothing else:

```
incorrect_for_wooden_tool   ToolSpeeds{913: 2, 914: 2, 915: 2, 916: 2}
mineable/pickaxe            ToolSpeeds{914: 2, 919: 1, 924: 4, 929: 12,
                                       934: 6, 939: 8, 944: 9}
```

Obsidian's `harvestTools` is `{939, 944}` — diamond and netherite pickaxes —
and neither ID appears in its material's speed table. A break-time rule looking
up the held tool finds nothing, falls back to a speed of 1, and computes
obsidian at roughly 250 seconds instead of 9.4. The error is a factor of about
26.

The affected set is not marginal. It is all 14 ores and their deepslate
variants, every metal block, `obsidian`, `crying_obsidian`, `ancient_debris`,
`respawn_anchor`, and the copper oxidation chain — the whole mining game. Any
client that digs on 26.1.2 hits it immediately.

**This is an upstream regression, not a longstanding modelling choice.**
`minecraft-data` gave obsidian `mineable/pickaxe` through 1.20.2 and
`incorrect_for_wooden_tool` from 1.21.5 onward. The dataset's compound
machinery is intact in the same file — `plant;mineable/axe`,
`leaves;mineable/axe;mineable/hoe`, `vine_or_glow_lichen;plant;mineable/axe`
are all present as composed entries — but it never composes an `incorrect_*`
tag with a `mineable/*` one. It substitutes. Confirming that: **no** compound
material anywhere contains an `incorrect_` part, and six of the seven
`incorrect_for_*_tool` entries the registry defines are referenced by no block.

The pinned source tree already holds the defect, so nothing was lost in
generation:

```
source/java/26.1/data/blocks.json
  gold_ore   material="incorrect_for_wooden_tool"  harvestTools={934,939,944}
  obsidian   material="incorrect_for_wooden_tool"  harvestTools={939,944}
```

### This is not a two-version problem

The defect is in the 26.1 dataset alone; 1.8.9's block data is correct.
Narrowing the project's version support would not remove the need for this fix
— a 26.1.2-only product hits it on every block that matters. The correction is
the price of supporting the modern version at all, not the price of supporting
two.

### What is also not the problem

An earlier reading of this — recorded in the M9.4 plan and in `MASTER_PLAN.md`
on 2026-08-17 — held that the two versions' material vocabularies are
incompatible and that a shared lookup would miss on 26.1 blocks. **That reading
is wrong and both documents need correcting.** The lookup
`materials.ByName(block.Material).ToolSpeeds[itemID]` is uniform across
versions: material names are opaque keys and nothing interprets them. All 13
distinct 26.1 material values and all 7 of 1.8.9's resolve against their own
registries, compounds included, because compounds are pre-composed registry
entries rather than strings to split. The vocabularies differ; the algorithm
does not. The real defect is narrower, is 26.1-only, and is a wrong value rather
than an unreadable one.

## What is being decided

Whether `minecraft-protocol`'s generated tree may deviate from upstream when
upstream is provably wrong, and how much machinery that deserves.

**Decision: yes, once, narrowly.** The block generator corrects this one defect,
the deviation is visible in the manifest, and the correction expires by itself
when upstream fixes it. No general corrections facility.

### Alternatives considered

- **Wait for upstream.** Correct in the abstract and worth doing anyway, but not
  a schedule this project controls. M4 already parked the Node differential
  suite "until upstream ships 26.1 support"; adding M9.4 to that queue trades a
  known fix for an unknown wait.
- **Correct downstream in `minecraft-simulation`'s v26_1 profile.** Keeps
  `generated/` a pure transcription, but puts block-data knowledge in the
  physics repository where the next consumer to compute a break time will not
  find it. M9.4's own constraint — break times come from generated data, and "a
  number in the formula that is not a game rule is a bug waiting" — argues
  against it. The defect is in block data, so the fix belongs in the repository
  that owns block data.
- **A general corrections facility.** Considered and rejected on cost. It was
  first proposed on the argument that a second consumer was already visible in
  the 1.16.1 window dataset; on inspection that is an *alias*, which
  `sourcePath` and `Dataset.Aliased` already handle, not a wrong value. The
  facility therefore has one user and no evidenced second, and would cost a new
  package, a manifest schema with per-correction records, and a new reporting
  path in `mcproto data validate` — for correctness identical to the narrow
  fix's. Revisit if a second defect appears; the reasoning here is written down
  so that discussion starts from evidence rather than from scratch.

## Design

### The correction, in the block generator

Applied to the decoded upstream JSON before the block generator emits Go, so
generated code never knows a correction happened — it sees a corrected material.

```go
// Upstream regressed between minecraft-data 1.20.2 and 1.21.5: the
// incorrect_for_* tag replaced the mineable/* tag rather than compounding with
// it, as it does for plant;mineable/axe and the other composed entries. The
// result is that 108 blocks — every ore, every metal block, obsidian — carry a
// material whose tool-speed table covers only wooden tools. Obsidian with a
// diamond pickaxe computes about 26 times too slow.
//
// Recover it: an incorrect_* material whose harvest tools are all pickaxes is
// mineable/pickaxe. That covers 107 of the 108. The exception is crafter, which
// upstream gives no harvestTools at all, so no derived rule reaches it.
//
// Unverified against the game. The derivation is sound and it is still an
// inference; M9.4's captured corpus is what settles it. See "What this design
// does not settle".
func correctMaterial(block rawBlock) (string, bool) {
    if !strings.HasPrefix(block.Material, incorrectPrefix) {
        return block.Material, false
    }
    if block.Name == "crafter" || allPickaxes(block.HarvestTools) {
        return "mineable/pickaxe", true
    }
    return block.Material, false
}
```

The rule states the reasoning; a 108-row table would state 108 conclusions. Only
the rule notices when an upstream bump adds a 109th affected block.

`crafter` is named rather than derived because the data forces it: 107 of the
108 have harvest tools that are exclusively pickaxes, and `crafter` has none at
all. Naming one exception beside a mechanical rule is honest; folding it into
the rule by weakening the precondition would not be.

### The deviation is visible in the manifest

`Dataset` gains one optional field:

```go
// Corrected explains why this dataset's generated form deviates from the bytes
// SHA256 names, and is empty for the datasets — almost all of them — that do
// not deviate at all.
//
// It exists because SHA256 keeps recording the upstream file's digest rather
// than the corrected form: provenance stays auditable, so a reader can fetch
// exactly what upstream said and diff it. A digest of corrected bytes would
// make the manifest self-consistent and useless for the one question it exists
// to answer. That leaves the deviation invisible unless something says so, and
// this is that something.
Corrected string `json:"corrected,omitempty"`
```

`manifestVersion` goes to 3, because the manifest package validates strictly and
a silently-added field would be a schema change pretending not to be one.

This is the third kind of provenance the manifest carries, and they differ in
kind rather than degree:

- **`sourcePath`** — an alias. Upstream's decision, not a bug. `Dataset.Aliased`
  already says so: "consumers must be able to see it: a window layout resolved
  from 1.16.1 is nine years old however current the version asking for it."
- **`extracted`** — a measurement from a licensed binary rather than a public
  repository.
- **`corrected`** — upstream is wrong and this tree says so.

`mcproto data validate` reports it alongside the aliases it already reports.

## Testing

**The sweep is the real test.** After correction, no block in `java/26.1` may
carry a material beginning with `incorrect_`. One assertion covers all 108
without enumerating any, and it fails if a later data bump introduces a 109th —
which a hand-written table would have absorbed in silence.

Alongside it:

- **The count is asserted exactly: 107 derived plus 1 named.** If an upstream
  bump makes the rule match a different number, that is a build failure
  demanding a look, not a silent change in what the correction does.
- **The count assertion is also the expiry.** When upstream lands a fix, gold
  ore's material becomes `mineable/pickaxe`, the rule stops matching, the count
  falls to zero, and the build fails. Someone deletes the correction, which is
  the right outcome. `crafter` is covered by the same assertion because it is
  counted separately — a name-matched correction with no count would apply
  forever.
- **`java/1.8` is asserted uncorrected**, so a correction cannot leak across
  versions.
- **Manifest round-trip at version 3, and a version 2 manifest still loads.**
- **A regression test on the symptom**: obsidian's corrected material resolves a
  non-1 tool speed for a diamond pickaxe.

## What this design does not settle

**The correction is a belief until the game confirms it.** Deriving
`mineable/pickaxe` from "every harvest tool is a pickaxe" is well-reasoned and
still an inference. What settles it is a measurement against a pinned vanilla
26.1.2 server: dig obsidian with a diamond pickaxe and compare the elapsed time.

Until that runs, the correction's doc comment says it is unverified against the
game — the same requirement M8.7's plan places on fixtures built from
behavioural statements, and for the same reason: a value that records a belief
without saying so is what makes a later failure hard to diagnose.

That measurement belongs to M9.4's captured corpus, and the obsidian case must
be a **named, non-optional** entry in that matrix rather than one of the
combinations the cross-product sampling may drop. It is the case that proves the
correction, so it cannot be a casualty of the trim.

## Follow-on work

1. **Report the regression upstream.** Worth doing independently of this design.
2. **Correct the M9.4 plan.**
   `docs/superpowers/plans/2026-08-17-m9-4-digging-block-breaking.md` in
   `headless-minecraft` describes this as incompatible vocabularies and
   prescribes splitting compound material names on `;`. Both are wrong: the
   compounds are pre-composed registry entries and the lookup is uniform. The
   version-owned classification it specifies is still correct, for the narrower
   reason that harvest legality and tool speed differ in shape between versions.
   It also needs the obsidian case pinned as non-optional in its matrix.
3. **Correct `MASTER_PLAN.md`.** It carries the same mischaracterisation in the
   M9 findings recorded on 2026-08-17.
4. **Revisit the general facility if a second defect appears.** Not before. The
   rejected-alternatives section above is the record of why, so that discussion
   starts from evidence.
