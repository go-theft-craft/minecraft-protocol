# P4 Shared Consumers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close P4 — put every repository that consumes this module on the released version, fix the gap that let one of them ship a corrected defect anyway, and restate P4 in the roadmap as what the migration turned out to be.

**Architecture:** P4 was written when the consumers were expected to migrate onto extracted packages. Three of the four did that months of commits ago; the work left is not migration but uptake. One consumer is a release behind while a local Go workspace makes it look current, so this plan bumps that pin, pins the corrected behaviour with a test that reads bytes rather than a struct literal, makes the local gate resolve the same dependency CI resolves, and writes the uptake step into the release flow so the next fix reaches consumers by process rather than by memory.

**Tech Stack:** Go 1.26.6 via `openserbia/go-flake`, Devbox, Task, golangci-lint. No new dependency in any repository.

## Before executing this plan: what P4 turned out to be

`ROADMAP.md` states P4 as three bullets. Each was checked against the working
trees on 2026-08-18, and the state below is what the check found. Read this
before the tasks: two of the three bullets are already satisfied, and the plan
records that rather than redoing it.

| P4 bullet | State | Evidence |
| --- | --- | --- |
| Migrate `server` to the shared Java 1.8 packages | Done | `pkg/gamedata/`, `cmd/codegen/`, and `cmd/dmd/` are deleted; `NewPlayer` takes a `func(java.PacketValue) error`; 57 non-vendor files import this module; `go.mod` requires `v0.6.0`; the repository's own `2026-08-15-shared-protocol-migration.md` is 55 steps of 55 |
| Migrate `proxy` imports while keeping the legacy protocol internal | Superseded | The legacy proxy requires the relay framework and `minecraft-simulation`, and requires this module nowhere. Its codec owns its byte primitives by a recorded decision: the legacy protocol shares nothing with modern Java Edition beyond the byte order of its fixed-width numbers, so eight readers are a smaller thing to own than a dependency on a codec for another protocol |
| Connect `headless-minecraft` to the current Java profile | Done, on a stale pin | `version/java` carries `Java1_8` and `Current`; `internal/adapter/v26_1` reduces protocol 775; the vanilla lane runs six scenarios on 1.8.9 and six on 26.1.2. Its `go.mod` and its examples module both require `minecraft-protocol v0.5.0` |

Two facts the bullets do not cover:

- `minecraft-simulation` became a consumer after P4 was written. It requires
  `v0.6.0` and reads `data`, `generated/java/v1_8`, and `generated/java/v26_1`.
- `relay`'s examples module is a consumer and requires `v0.6.0`. Its core module
  requires nothing and must stay that way.

So every consumer runs `v0.6.0` except `headless-minecraft`, which runs `v0.5.0`
and therefore still decodes every entity velocity a 26.1 server sends into a
number that is not the velocity. That defect is what 0.6.0's `LPVec3` fix
corrected. It does not show up locally, because a developer's `go.work` — which
is gitignored, so it exists on the machine and not in CI — points the build at
`../minecraft-protocol` and the build silently tests the working tree. CI tests
the tag. That divergence is the thing worth fixing beyond the bump itself.

## Global Constraints

- Work in the repository each task names. Tasks 1 and 2 land in
  `/home/ocharnyshevich/pet.projects/go-theft-craft/headless-minecraft`, Tasks 3
  and 4 in `/home/ocharnyshevich/pet.projects/go-theft-craft/minecraft-protocol`.
- Run project commands as `devbox run -- task <name>`. Where a task does not
  exist for the command, run it as `devbox run -- go ...` so it uses the pinned
  toolchain.
- Tests run with `-race` wherever the repository's own task does.
- Each task ends with a commit. Never add a `Co-Authored-By` or
  `Claude-Session` trailer to a commit message.
- Start each task from a clean tree. Both repositories were clean at 2026-08-18;
  if `git status --porcelain` is not empty, stop and ask rather than committing
  someone else's work in progress.
- `minecraft-protocol` and `headless-minecraft` are public. Do not name the
  private proxy project, its protocol, its repository directory, or any path
  inside it, in source, comments, docs, or commit messages. Refer to it by role:
  "the legacy proxy", "the legacy protocol", "the legacy codec". Task 4 records a
  conclusion about it and names none of those things.
- Add no dependency to any repository. Every version this plan touches already
  exists in the module cache.

## Design decisions this plan settles

**The pin is the contract, and the gate must prove the pin.** A Go workspace is
the right tool for editing two modules together, and it is also a way to run a
whole verify against code no release contains. The two uses are separated by
lane rather than by discipline: the edit loop (`test:fast`) keeps the workspace,
and every task `verify` runs resolves modules with `GOWORK=off`, so a local pass
means what a CI pass means. This is Task 2.

**The velocity regression is pinned by bytes, not by a value.** Every existing
adapter test builds `protocol.Packet{Value: &gen.PlayClientbound...{}}`, which
never runs a decoder — the exact shape of test that let a byte order be wrong in
both directions for as long as it was. The new test decodes
`minecraft-protocol`'s own captured fixture and asserts what reaches the observed
world. It fails on `v0.5.0` and passes on `v0.6.0`, which is the only way to
know the bump did something.

**The legacy proxy's exception is recorded, not undone.** The P4 bullet asks for
an import migration that the consumer has since had a reason not to want, and
the reason is written down where the code is. Forcing a shared dependency on a
codec for a different protocol would add coupling and remove nothing. Task 4
verifies that no shared package is being shadowed there and records the decision
in the roadmap; it changes no line in a private repository.

**A release is finished when consumers have it.** 0.6.0 fixed a decode defect and
one of five consumers still ships it. The fix for the class, not the instance, is
that the release flow names its consumers and does not call a release done until
each has taken it or recorded why not. This is Task 3.

## Not in this plan

- Tagging `minecraft-simulation` and moving `headless-minecraft` off
  `minecraft-simulation v0.1.0`. That pin blocks nothing today —
  `headless-minecraft` compiles and tests against `v0.1.0` — and the item and
  arrow families on its HEAD are M9.2's to release, in M9.2's plan.
- Ticking the stale checkboxes in the `server` repository's
  `2026-08-15-server-play-migration.md`. The work is done and verified by
  inspection above; editing another repository's plan file to match is
  bookkeeping this plan does not need, and doing it would put a commit in a
  repository no task here otherwise touches.
- Public API compatibility tests. Those are P5's, and P5 says so.
- The truncation in `headless-minecraft`'s `velocity775` — `int16(v * 8000)`
  turns 0.0999816883 into 799 where rounding would give 800. It is one unit in
  8000 on a value the encoding already quantised, nothing reads it as anything
  but a report, and no evidence in hand says which the game would prefer. Task 1
  asserts what the code does and says why in a comment rather than changing it.

---

### Task 1: `headless-minecraft` takes `v0.6.0`, and the velocity fix is pinned to bytes

**Files:**
- Modify: `go.mod`, `go.sum` (root module)
- Modify: `examples/go.mod`, `examples/go.sum`
- Create: `internal/adapter/v26_1/velocity_test.go`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: `protocol.NewLimits() (protocol.Limits, error)`,
  `java.NewReadBuffer(payload []byte, limits protocol.Limits) (*java.Buffer, error)`,
  `(*java.Buffer).ReadLPVec3(path string) (java.LPVec3, error)`,
  `gen.PlayClientboundSpawnEntity{EntityID int32; ObjectUUID java.UUID; Type int32; X, Y, Z float64; Velocity java.LPVec3; Pitch, Yaw, HeadPitch int8; ObjectData int32}` — all from `minecraft-protocol v0.6.0`.
- Consumes from the package under test: `script(t *testing.T, batches ...[]protocol.Packet) (*world.World, []event.Event)`, `play(value any) protocol.Packet`, and `playLogin(entityID int32) protocol.Packet`, all defined in `internal/adapter/v26_1/reduce_test.go` in package `v26_1_test`.
- Produces: nothing other tasks import. Task 2 runs the test this task writes.

- [x] **Step 1: Write the failing test**

Create `internal/adapter/v26_1/velocity_test.go`:

```go
package v26_1_test

import (
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	gen "github.com/go-theft-craft/minecraft-protocol/generated/java/v26_1"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

// TestSpawnVelocityDecodesWhatAVanillaServerSent pins an observed velocity to
// bytes rather than to a struct literal.
//
// Every other test in this package builds a packet value, so no decoder runs in
// them — and a byte order that is wrong in both directions round-trips
// perfectly. One was: minecraft-protocol before v0.6.0 read the upper
// thirty-two bits of a quantised vector little endian where vanilla writes them
// big endian, so the six bytes below decoded as {0.600, 0.994, 0.992} and
// reached the observed world as a velocity no entity in the game had.
//
// The bytes are minecraft-protocol's own fixture: the velocity field of the
// spawn packets of two arrows, captured from a pinned vanilla 26.1.2 server on
// 2026-08-18 and summoned with the motion the operator stated —
// Motion:[0.10d,0.0d,0.0d] and Motion:[0.0d,0.0d,0.05d].
func TestSpawnVelocityDecodesWhatAVanillaServerSent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		encoded []byte
		want    [3]int16
	}{
		// The encoding keeps fifteen bits per component, so 0.1 comes back as
		// 0.0999816883 — 799.85 in the 1/8000 units the world records, and
		// velocity775 truncates rather than rounds.
		{
			name:    "an arrow summoned with 0.1 on X",
			encoded: []byte{0x29, 0x33, 0x7f, 0xfe, 0xff, 0xfe},
			want:    [3]int16{799, 0, 0},
		},
		{
			name:    "an arrow summoned with 0.05 on Z",
			encoded: []byte{0xf9, 0xff, 0x86, 0x64, 0xff, 0xfd},
			want:    [3]int16{0, 0, 399},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limits, err := protocol.NewLimits()
			if err != nil {
				t.Fatal(err)
			}
			buffer, err := java.NewReadBuffer(tc.encoded, limits)
			if err != nil {
				t.Fatal(err)
			}
			velocity, err := buffer.ReadLPVec3("velocity")
			if err != nil {
				t.Fatalf("ReadLPVec3: %v", err)
			}

			w, _ := script(t, []protocol.Packet{
				playLogin(1),
				play(&gen.PlayClientboundSpawnEntity{
					EntityID: 7, Type: 3,
					X: -4.5, Y: -55, Z: 9.5,
					Velocity: velocity,
				}),
			})

			entity, ok := w.Snapshot().Entities.Get(7)
			if !ok {
				t.Fatal("the arrow is not tracked")
			}
			if entity.Velocity != tc.want {
				t.Errorf("velocity is %v, want %v — the motion the server was told to give it", entity.Velocity, tc.want)
			}
		})
	}
}
```

- [x] **Step 2: Run it against the pinned release and watch it fail**

The point of this step is to see the defect this repository ships today, which
means resolving `minecraft-protocol` from `go.mod` and not from the workspace.

Run:

```bash
cd /home/ocharnyshevich/pet.projects/go-theft-craft/headless-minecraft
devbox run -- env GOWORK=off go test ./internal/adapter/v26_1/ -run TestSpawnVelocityDecodesWhatAVanillaServerSent -v
```

Expected: both subtests FAIL. `v0.5.0` decodes the first case as
`{4800 7953 7937}` where the server sent `{799 0 0}`, and the second as
`{4000 3141 7875}` where it sent `{0 0 399}` — the arrows moving on three axes
at speeds nothing summoned them with.

If it passes here, stop: either the pin is no longer `v0.5.0` or the workspace is
still in play. Check with `devbox run -- env GOWORK=off go list -m github.com/go-theft-craft/minecraft-protocol`.

- [x] **Step 3: Take the release in both modules**

Run:

```bash
cd /home/ocharnyshevich/pet.projects/go-theft-craft/headless-minecraft
devbox run -- env GOWORK=off go get github.com/go-theft-craft/minecraft-protocol@v0.6.0
devbox run -- env GOWORK=off go mod tidy
cd examples
devbox run -- env GOWORK=off go get github.com/go-theft-craft/minecraft-protocol@v0.6.0
devbox run -- env GOWORK=off go mod tidy
```

Both modules require the old version and both must move; the examples module is
this repository's integration surface and pins its own copy.

Confirm the result:

```bash
cd /home/ocharnyshevich/pet.projects/go-theft-craft/headless-minecraft
grep -n 'minecraft-protocol' go.mod examples/go.mod
```

Expected: `v0.6.0` in both, and no `replace` for `minecraft-protocol` in either.

- [x] **Step 4: Run the test and verify it passes**

Run:

```bash
devbox run -- env GOWORK=off go test ./internal/adapter/v26_1/ -run TestSpawnVelocityDecodesWhatAVanillaServerSent -v
```

Expected: PASS, both subtests.

- [x] **Step 5: Run the whole gate**

Run:

```bash
devbox run -- task verify
```

Expected: green. `v0.6.0` adds `Conduit.EnableReadEncryption`,
`Conduit.EnableWriteEncryption`, and `java.EncryptionControl.Half`, all additive,
and corrects the login sequence inside this module's `login` package, so no
caller here changes. If something fails to compile, that is a finding worth
recording rather than working around.

- [x] **Step 6: Record it in the changelog**

Append to `CHANGELOG.md`, after the last entry of the `### Added` list, so the
file reads `Added` then `Fixed` the way Keep a Changelog orders them:

```markdown
### Fixed

- `internal/adapter/v26_1`: an entity's velocity is the velocity the server sent.
  This client pinned `minecraft-protocol v0.5.0`, whose quantised-vector reader
  took the upper thirty-two bits little endian where vanilla writes them big
  endian, so every spawn and velocity packet on protocol 775 reached the observed
  world as a plausible number unrelated to the entity's motion — an arrow
  summoned with `Motion:[0.1d,0.0d,0.0d]` was recorded moving on all three axes.
  Taking `v0.6.0` fixes it, and a new test decodes the bytes a real 26.1.2 server
  sent rather than building the packet as a value, because a value-built test
  cannot see a byte order at all.
```

- [x] **Step 7: Commit**

```bash
cd /home/ocharnyshevich/pet.projects/go-theft-craft/headless-minecraft
devbox run -- task precommit
git add go.mod go.sum examples/go.mod examples/go.sum internal/adapter/v26_1/velocity_test.go CHANGELOG.md
git commit -m "build: take minecraft-protocol v0.6.0, and pin the velocity it fixed"
```

---

### Task 2: The gate resolves what a release pins

**Files:**
- Modify: `Taskfile.yml` (tasks `deps`, `test`, `test:e2e`, `examples`, `vuln`, `build`)

**Interfaces:**
- Consumes: Task 1's test, which is the only test in the repository whose result
  differs between `v0.5.0` and `v0.6.0` and therefore the only one that can prove
  this change works.
- Produces: nothing other tasks import.

The defect this closes: `go.work` is gitignored, so it exists on a developer
machine and never in CI. With it, `task verify` builds against
`../minecraft-protocol`'s working tree; without it, CI builds against the tag in
`go.mod`. A stale pin passes locally and ships. The fix is that every lane inside
`verify` resolves modules the way CI does, while `test:fast` — the edit loop, and
the reason the workspace exists — keeps it.

- [x] **Step 1: Reproduce the divergence**

Run, with the workspace in place (`ls go.work` should find it; if there is no
`go.work` on this machine, create one for this step with
`devbox run -- go work init . ../minecraft-protocol ./examples`, and delete it
after Step 4 if you created it — it is gitignored either way, so it never
reaches a commit):

```bash
cd /home/ocharnyshevich/pet.projects/go-theft-craft/headless-minecraft
devbox run -- go mod edit -require=github.com/go-theft-craft/minecraft-protocol@v0.5.0
devbox run -- task test
```

Expected: PASS — the pin says `v0.5.0` and the gate does not notice, which is the
bug in the gate. Leave the reverted pin in place for Step 3.

- [x] **Step 2: Change the lanes**

In `Taskfile.yml`, prefix every command in the tasks `verify` runs with
`env GOWORK=off`, and say why once. `deps` is included because `go mod tidy`
writes `go.sum`, and a workspace-resolved tidy writes sums for a build nobody
ships.

`deps`:

```yaml
  deps:
    desc: Download and normalize Go dependencies
    # Every lane verify runs resolves modules with the workspace off, so a local
    # pass and a CI pass mean the same thing. go.work is gitignored — it is on
    # this machine and never on the runner — so with it in play the gate builds
    # ../minecraft-protocol's working tree while CI builds the tag in go.mod, and
    # a pin left a release behind passes here and ships. test:fast keeps the
    # workspace; it is the edit loop, which is what the workspace is for.
    sources:
      - go.mod
      - go.sum
    cmds:
      - env GOWORK=off go mod download
      - env GOWORK=off go mod tidy
```

`test`:

```yaml
    cmds:
      - env GOWORK=off go test -race -covermode=atomic -coverprofile=coverage.out ./...
```

`test:e2e`:

```yaml
    cmds:
      - env GOWORK=off go test -race -run 'TestEndToEnd' ./client/...
```

`examples` — the module has its own `replace` to `../`, so it builds against the
working tree either way, and the workspace only changes which
`minecraft-protocol` it takes:

```yaml
    dir: examples
    cmds:
      - env GOWORK=off go mod tidy
      - env GOWORK=off golangci-lint run ./...
      - env GOWORK=off go test -race ./...
      - env GOWORK=off go vet ./...
```

`vuln`:

```yaml
    cmds:
      - env GOWORK=off govulncheck ./...
```

`build`:

```yaml
    cmds:
      - env GOWORK=off go build ./...
```

Leave `test:fast`, `test:vanilla`, `lint`, `fmt`, `fmt:check`, `secrets`, and
`server:vanilla` alone. `test:vanilla` is a live lane run on request, and the
formatting and secret lanes do not resolve modules.

- [x] **Step 3: Run the same lane and watch it fail**

The pin is still reverted from Step 1.

Run:

```bash
devbox run -- task test
```

Expected: FAIL, in `TestSpawnVelocityDecodesWhatAVanillaServerSent`, reporting a
`{4800 7953 7937}` for the first case. That failure is the whole point: the gate
now sees what CI sees.

- [x] **Step 4: Restore the pin and verify green**

```bash
devbox run -- go mod edit -require=github.com/go-theft-craft/minecraft-protocol@v0.6.0
devbox run -- task deps
git diff --exit-code go.mod go.sum && echo "pin restored exactly"
devbox run -- task verify
```

Expected: `pin restored exactly` prints, and `verify` is green. If `git diff`
reports a change, the Step 1 revert has leaked into the commit — fix it before
going on.

- [x] **Step 5: Record it in the changelog**

Append to the `### Fixed` section Task 1 created in `CHANGELOG.md`:

```markdown
- Taskfile: every lane `verify` runs resolves modules with the workspace off. A
  `go.work` is gitignored, so it is present on a developer machine and absent in
  CI, and the gate was building the neighbouring working tree while CI built the
  version `go.mod` pins — which is how this repository ran a release behind
  without a red check anywhere. `test:fast` keeps the workspace, because editing
  two modules together is what it is for.
```

- [x] **Step 6: Commit**

```bash
devbox run -- task precommit
git add Taskfile.yml CHANGELOG.md
git commit -m "build: make verify resolve the modules go.mod pins"
```

---

### Task 3: The release flow names its consumers

**Files:**
- Modify: `RELEASING.md` (the `Release flow` list and the `Dependency policy` section)

**Interfaces:**
- Consumes: the consumer set established in this plan's findings — `server`,
  `minecraft-simulation`, `headless-minecraft` (root and examples modules), and
  `relay`'s examples module.
- Produces: a `Consumers` section other repositories' release rules can point at.

Work in `/home/ocharnyshevich/pet.projects/go-theft-craft/minecraft-protocol`.

- [x] **Step 1: Check the consumer list against the trees**

The list is only worth writing if it is complete on the day it is written.

Run:

```bash
cd /home/ocharnyshevich/pet.projects/go-theft-craft
for d in */; do
  for m in $(find "$d" -name go.mod -not -path '*/vendor/*' -not -path '*/.git/*' 2>/dev/null); do
    grep -q 'go-theft-craft/minecraft-protocol' "$m" && printf '%s: %s\n' "$m" "$(grep 'go-theft-craft/minecraft-protocol' "$m" | tr -s ' ')"
  done
done
```

Expected, as of 2026-08-18: `server/go.mod` at `v0.6.0`,
`minecraft-simulation/go.mod` at `v0.6.0`, `headless-minecraft/go.mod` and
`headless-minecraft/examples/go.mod` at `v0.6.0` after Task 1, and
`relay/examples/go.mod` at `v0.6.0`. If a repository appears that is not in that
list, add it to the table in Step 2 rather than dropping it.

- [x] **Step 2: Write the consumer section**

In `RELEASING.md`, replace the `## Dependency policy` section — the whole
section, whose current text names a headless release rule and a `proxy`
compatibility suite that no longer describes anything — with:

```markdown
## Consumers

These repositories require this module:

| Repository | What it takes |
| --- | --- |
| `server` | the root package, `wire/java`, `generated/java/v1_8`, `generated/java/v26_1`, `data`, and `login`, vendored |
| `minecraft-simulation` | `data`, `generated/java/v1_8`, and `generated/java/v26_1` |
| `headless-minecraft` | the root package, both generated Java profiles, `data`, and `login` — in its root module and again in its examples module, which pins its own copy |
| `relay` | the examples module only. The core module requires nothing, and a release must never be the reason that changes |

A release is not finished when the tag is pushed. It is finished when every
repository above requires it, or has recorded why it does not.

0.6.0 is why this rule is written down. It corrected a quantised-vector byte
order, and `headless-minecraft` — the one consumer whose read path that defect
reached — stayed on 0.5.0 and kept decoding every entity velocity a 26.1 server
sent into a number that was not the velocity. Nothing was red: its local gate
resolved a Go workspace pointing at this working tree, so the fix looked present
there and was absent in everything it built.

Tag this module before a dependent release. A dependent's published tag must use
a released version of this module and must not carry a local `replace`
directive for it.

The [Go module version documentation](https://go.dev/doc/modules/version-numbers) defines Go's stability meaning. The [Go module reference](https://go.dev/ref/mod#major-version-suffixes) defines major-version suffixes.
```

- [x] **Step 3: Put uptake in the release flow**

In the numbered list under `## Release flow`, replace step 7:

```markdown
7. Run the server, proxy, and headless-client compatibility suites when their shared contracts changed.
```

with:

```markdown
7. Run the verify task of each repository under [Consumers](#consumers) whose shared contracts changed.
```

and append after the current step 11:

```markdown
12. Open the version bump in every repository under [Consumers](#consumers), and run each one's verify against it. A consumer that should not take this release records why, in its own changelog.
```

- [x] **Step 4: Check the document still reads straight through**

Run:

```bash
cd /home/ocharnyshevich/pet.projects/go-theft-craft/minecraft-protocol
grep -n 'proxy\|Dependency policy\|Consumers' RELEASING.md
```

Expected: no `Dependency policy`; one `## Consumers` heading; two
`[Consumers](#consumers)` links in the release flow; and exactly one `proxy` —
the `Go proxy resolves the tag` transition in the release-flow diagram, which is
Go's module proxy and stays.

There is no changelog entry for this task. `CHANGELOG.md` records user-visible
changes to the module, and this changes how this repository releases, not what a
caller compiles against.

- [x] **Step 5: Commit**

```bash
devbox run -- task precommit
git add RELEASING.md
git commit -m "docs: name this module's consumers, and make uptake part of a release"
```

---

### Task 4: Restate P4 in the roadmap and close it

**Files:**
- Modify: `ROADMAP.md` (the mermaid chart's `P4` node and the `## P4: shared consumers` section)

**Interfaces:**
- Consumes: this plan's findings table, and Step 1's check.
- Produces: nothing other tasks import. This is the last task.

Work in `/home/ocharnyshevich/pet.projects/go-theft-craft/minecraft-protocol`.

- [x] **Step 1: Verify the claim about the legacy consumers before writing it**

The roadmap is about to say that no shared package is being shadowed in the
private repositories. Check it rather than assert it. Run this in each private
repository's working tree, on the machine that has them:

```bash
grep -rn 'go-theft-craft/minecraft-protocol' --include='*.go' --include='go.mod' . | head
grep -rln 'func ReadVarInt\|func WriteVarInt' --include='*.go' . | head
```

Expected: no requirement on this module, and no VarInt codec — the legacy
protocol has none, and a hit would mean a Java-Edition codec had been copied in
rather than imported.

Record only the conclusion. Do not paste paths, package names, or file listings
from those repositories into this public repository, here or in the commit
message.

- [x] **Step 2: Update the chart node**

In the mermaid chart at the top of `ROADMAP.md`, replace:

```
    P4["P4: migrate server<br/>and proxy consumers"]
```

with:

```
    P4["P4: shared consumers<br/>complete"]
```

- [x] **Step 3: Rewrite the section**

Replace the whole `## P4: shared consumers` section — heading line included —
with:

```markdown
## P4: shared consumers

Status: complete.

- `server` runs on the shared Java 1.8 packages. Its own packet types, its
  packet code generation, and the schema fetcher that fed them are deleted; the
  connection writes generated values through the managed stream, and the
  byte-parity fixtures it captured before the migration still pass unchanged.
  The two constants it kept local are kept deliberately: it advertises `1.8.8`
  where this module says `1.8.9`, and reconciling those names is a decision on
  its own rather than a side effect of a migration.
- `minecraft-simulation` reads the generated datasets for both Java profiles and
  states no game value of its own. It became a consumer after this phase was
  written, which is why the phase's original wording does not mention it.
- `headless-minecraft` connects on both built-in Java profiles — protocol 47 and
  protocol 775 — with an adapter each, and its conformance lane runs the same six
  scenarios against a real 1.8.9 server and a real 26.1.2 server.
- The legacy proxy does not consume this module, and the migration this phase
  originally planned for it is not the right change. It consumes the proxy
  framework and the simulation, and its codec owns its own fixed-width readers
  on a recorded decision: the legacy protocol shares nothing with modern Java
  Edition beyond the byte order of those numbers, so depending on a codec for
  another protocol would add coupling and remove nothing. Checked again when this
  phase closed: nothing there shadows a package this module publishes.
- Uptake, not migration, turned out to be the risk. 0.6.0 corrected a
  quantised-vector byte order, and the one consumer that read those vectors
  stayed a release behind with every check green, because its local gate resolved
  a Go workspace pointing at this working tree. Its gate now resolves what its
  `go.mod` pins, and [RELEASING.md](RELEASING.md) names the consumers a release
  is not finished without.
```

- [x] **Step 4: Check the surrounding text for the same staleness**

The old section opened with "Server and `headless-minecraft` migration comes
before protocol 775", which stopped being true when P2 shipped protocol 775.
Step 3 removes that line. Confirm nothing else in the file still orders the two:

```bash
grep -n 'before protocol 775\|migrate server\|proxy consumers' ROADMAP.md
```

Expected: no output.

Then read the P5 section immediately below and leave it alone — its dependency on
P4 is satisfied by this task, and its own contents are P5's work.

- [x] **Step 5: Commit**

```bash
devbox run -- task precommit
git add ROADMAP.md
git commit -m "docs: close P4, and say what the consumer migration turned out to be"
```
