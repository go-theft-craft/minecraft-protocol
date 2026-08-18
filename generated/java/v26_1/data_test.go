package v26_1

import (
	"encoding/json"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/data"
)

// TestMeasuredBlockMovementAnswersByState pins what the 26.1.2 measurement
// says, in the terms the wire uses.
//
// The states below were read back out of the game rather than reasoned about,
// which is the only way this table can be checked at all. Two of them are worth
// keeping for their own sake: cobweb and torch do not stop movement despite
// filling their cell to a reader's eye, and the game excludes cobweb by name in
// its own code.
func TestMeasuredBlockMovementAnswersByState(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}

	movement := set.BlockMovement()
	if movement == nil {
		t.Fatal("BlockMovement() is nil, want the 26.1.2 measurement")
	}

	for _, test := range []struct {
		name  string
		state data.BlockStateID
		want  bool
	}{
		{"air", 0, false},
		{"stone", 1, true},
		{"water", 86, false},
		{"oak leaves", 279, true},
		{"cobweb", 2247, false},
		{"torch", 3370, false},
		{"oak slab", 13333, true},
	} {
		blocks, known := movement.ByState(test.state)
		if !known {
			t.Errorf("%s (state %d) is unmeasured", test.name, test.state)
			continue
		}
		if blocks != test.want {
			t.Errorf("%s (state %d) blocks movement = %v, want %v", test.name, test.state, blocks, test.want)
		}
	}
}

// TestAStateThatDisagreesWithItsBlockIsAnswered is the case that decides how
// this version has to be keyed.
//
// Every wall in the game is registered so that all of its states answer alike,
// except resin_brick_wall. Its unconnected states have no collision shape and
// do not stop movement, while the rest of its states do. A registry keyed by
// block would report one answer for all 324 of them and be wrong for two, with
// nothing to signal it.
func TestAStateThatDisagreesWithItsBlockIsAnswered(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	movement := set.BlockMovement()

	for _, state := range []data.BlockStateID{9015, 9018} {
		blocks, known := movement.ByState(state)
		if !known {
			t.Errorf("resin_brick_wall state %d is unmeasured", state)
			continue
		}
		if blocks {
			t.Errorf("resin_brick_wall state %d blocks movement, want it not to", state)
		}
	}

	// The states either side of an exception belong to the same block and must
	// still give the block's own answer, or the exception has overwritten more
	// than the state it names.
	for _, state := range []data.BlockStateID{9009, 9014, 9016, 9017, 9019, 9332} {
		blocks, known := movement.ByState(state)
		if !known || !blocks {
			t.Errorf("resin_brick_wall state %d blocks movement = %v (known %v), want true", state, blocks, known)
		}
	}
}

// TestABlockWhoseStatesDisagreeHasNoBlockAnswer pins the refusal.
//
// ByID has no honest answer for that block: reporting what most of its states
// say would be wrong for the rest, and a caller cannot tell it was rounded.
// Reporting unknown sends the caller to ByState, which always has an answer.
func TestABlockWhoseStatesDisagreeHasNoBlockAnswer(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	movement := set.BlockMovement()

	const resinBrickWall data.BlockID = 379
	if _, known := movement.ByID(resinBrickWall); known {
		t.Error("ByID answered for a block whose states disagree")
	}
	if _, present := movement.All()[resinBrickWall]; present {
		t.Error("All() carries a block whose states disagree")
	}

	if blocks, known := movement.ByID(1); !known || !blocks {
		t.Errorf("ByID(stone) = %v, %v, want true, true", blocks, known)
	}
}

// TestAStateBeyondTheMeasurementIsUnknown keeps the distinction the whole
// dataset rests on.
//
// A state nobody measured is not a state nothing blocks. The measurement
// describes states 0 through 29872; anything above that is a block this version
// did not have, and a caller must refuse to walk into it rather than through
// it.
func TestAStateBeyondTheMeasurementIsUnknown(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	movement := set.BlockMovement()

	if _, known := movement.ByState(29873); known {
		t.Error("a state past the end of the measurement reported an answer")
	}
	if blocks, known := movement.ByState(29872); !known {
		t.Errorf("the last measured state reported unknown (blocks %v)", blocks)
	}
}

// TestPhysicsMatchesTheExtractedDocument compares the generated constants
// against the pinned bytes they were generated from, at full precision.
//
// It exists for the widths. Three of the player's four motion constants are
// `float` values that Java widens where it applies them, so their decimal form
// is long and unmemorable: 0.91F is 0.9100000262260437 and not 0.91, and the
// step height is 0.6000000238418579 and not 0.6. A generator that formatted a
// constant to fewer digits, or a hand edit that rounded one, would produce a
// kernel that drifts from vanilla in a way no range check notices. Comparing
// the typed value against the document's own text is what catches it.
//
// The document is the embedded raw dataset rather than a file path, so this test
// checks the bytes this package was built from rather than whatever is on disk
// beside it.
func TestPhysicsMatchesTheExtractedDocument(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}

	dataset, ok := Raw().Get("physics")
	if !ok {
		t.Fatal("the raw set holds no physics document")
	}

	var document struct {
		DefaultSlipperiness float64            `json:"defaultSlipperiness"`
		BlockSlipperiness   map[string]float64 `json:"blockSlipperiness"`
		EntityMotion        map[string]struct {
			Gravity        float64 `json:"gravity"`
			HorizontalDrag float64 `json:"horizontalDrag"`
			VerticalDrag   float64 `json:"verticalDrag"`
			StepHeight     float64 `json:"stepHeight"`
		} `json:"entityMotion"`
	}
	if err := json.Unmarshal(dataset.Data, &document); err != nil {
		t.Fatalf("unmarshal the physics document: %v", err)
	}

	physics := set.Physics()
	if physics.DefaultSlipperiness != document.DefaultSlipperiness {
		t.Errorf("default slipperiness = %v, the document says %v",
			physics.DefaultSlipperiness, document.DefaultSlipperiness)
	}
	if len(physics.BlockSlipperiness) != len(document.BlockSlipperiness) {
		t.Errorf("generated %d slipperiness entries, the document has %d",
			len(physics.BlockSlipperiness), len(document.BlockSlipperiness))
	}
	for name, want := range document.BlockSlipperiness {
		if got := physics.BlockSlipperiness[name]; got != want {
			t.Errorf("slipperiness for %s = %v, the document says %v", name, got, want)
		}
	}

	if len(physics.EntityMotion) != len(document.EntityMotion) {
		t.Errorf("generated %d entity families, the document has %d",
			len(physics.EntityMotion), len(document.EntityMotion))
	}
	for name, want := range document.EntityMotion {
		got, ok := physics.EntityMotion[name]
		if !ok {
			t.Errorf("entity motion is missing %s", name)

			continue
		}
		if got.Gravity != want.Gravity {
			t.Errorf("%s gravity = %v, the document says %v", name, got.Gravity, want.Gravity)
		}
		if got.HorizontalDrag != want.HorizontalDrag {
			t.Errorf("%s horizontal drag = %v, the document says %v",
				name, got.HorizontalDrag, want.HorizontalDrag)
		}
		if got.VerticalDrag != want.VerticalDrag {
			t.Errorf("%s vertical drag = %v, the document says %v",
				name, got.VerticalDrag, want.VerticalDrag)
		}
		if got.StepHeight != want.StepHeight {
			t.Errorf("%s step height = %v, the document says %v", name, got.StepHeight, want.StepHeight)
		}
	}
}

// TestThePlayerConstantsAreTheWidenedFloats states the four numbers, because a
// round trip against the document proves the generator faithful and not the
// document right.
//
// Every one of them was confirmed twice against the 26.1.2 server jar, once from
// decompiled source and once from bytecode, and the record is in
// minecraft-reference's physics-motion-26.1.2 note. Two are worth knowing by
// sight: the step height is the attribute's 0.6 narrowed to a float where
// LivingEntity reads it, and gravity is a double the whole way and therefore
// round.
func TestThePlayerConstantsAreTheWidenedFloats(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}

	player, ok := set.Physics().EntityMotion["player"]
	if !ok {
		t.Fatal("physics has no player motion")
	}

	for _, test := range []struct {
		name string
		got  float64
		want float64
	}{
		{"gravity", player.Gravity, 0.08},
		{"horizontal drag", player.HorizontalDrag, float64(float32(0.91))},
		{"vertical drag", player.VerticalDrag, float64(float32(0.98))},
		{"step height", player.StepHeight, float64(float32(0.6))},
	} {
		if test.got != test.want {
			t.Errorf("player %s = %v, want %v", test.name, test.got, test.want)
		}
	}
}

// TestSlipperinessMatchesVanilla pins the blocks that differ from the default,
// which is the whole of what a movement kernel is sensitive to here.
//
// Five blocks differ in this version where three do in 1.8.9, and one of the
// three was renamed: `slime` there is `slime_block` here. A kernel carried over
// by name would silently walk on ordinary friction over the modern block.
//
// The values are the round decimals the document stores, not the widened floats
// the game computes with. That asymmetry with the entity constants is
// deliberate and belongs to the consumer: the game holds a block's friction in a
// `float` field, so a consumer narrows this to `float32` at its own boundary,
// which is what recovers the width vanilla uses.
func TestSlipperinessMatchesVanilla(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	physics := set.Physics()

	for _, test := range []struct {
		block string
		want  float64
	}{
		{"blue_ice", 0.989},
		{"frosted_ice", 0.98},
		{"ice", 0.98},
		{"packed_ice", 0.98},
		{"slime_block", 0.8},
		{"stone", 0.6},
		{"soul_sand", 0.6},
	} {
		if got := physics.Slipperiness(test.block); got != test.want {
			t.Errorf("slipperiness for %s = %v, want %v", test.block, got, test.want)
		}
	}

	// Stated as a count as well as by name, so a version that adds a slippery
	// block fails here rather than being simulated on the wrong friction.
	slippery := 0
	for _, value := range physics.BlockSlipperiness {
		if value != physics.DefaultSlipperiness {
			slippery++
		}
	}
	if slippery != 5 {
		t.Errorf("%d blocks differ from the default slipperiness, want 5", slippery)
	}
}

// TestMeasuredFallingAnswersByBlock pins the fact a route that digs depends on,
// and the difference between it and the movement fact beside it.
//
// This version computes stopping movement per state, so that measurement is
// keyed by state range. Falling is not: a block either extends FallingBlock or
// does not, and every state of it answers alike. Soul sand is the case that
// says why this is measured rather than derived — it shares its material with
// gravel in the version this project also supports, and it stays put in both.
func TestMeasuredFallingAnswersByBlock(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	movement := set.BlockMovement()
	if movement == nil {
		t.Fatal("the measured version publishes no block movement registry")
	}

	for _, test := range []struct {
		name  string
		id    data.BlockID
		want  bool
		known bool
	}{
		{name: "gravel", id: 40, want: true, known: true},
		{name: "sand", id: 37, want: true, known: true},
		{name: "anvil", id: 467, want: true, known: true},
		// The dragon egg does extend the falling class in this version, where
		// it did not in 1.8.9. It is asserted in both so the divergence is
		// recorded rather than discovered.
		{name: "dragon egg", id: 394, want: true, known: true},
		{name: "soul sand", id: 286, want: false, known: true},
		{name: "stone", id: 1, want: false, known: true},
		{name: "a block this version does not have", id: 60000, want: false, known: false},
	} {
		got, known := movement.FallsByID(test.id)
		if got != test.want || known != test.known {
			t.Errorf("FallsByID(%s) = %v, %v, want %v, %v", test.name, got, known, test.want, test.known)
		}
	}
}

// TestMeasuredClimbableAnswersTheGamesTag pins the nine blocks this version
// calls climbable.
//
// The list is the game's own climbable block tag rather than a reading of what
// looks climbable, and it is longer than 1.8.9's two. Scaffolding and the cave
// vines are the entries a caller porting from the older version would miss.
func TestMeasuredClimbableAnswersTheGamesTag(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	movement := set.BlockMovement()

	for _, test := range []struct {
		name  string
		id    data.BlockID
		want  bool
		known bool
	}{
		{name: "ladder", id: 221, want: true, known: true},
		{name: "vine", id: 366, want: true, known: true},
		{name: "scaffolding", id: 837, want: true, known: true},
		{name: "cave vines", id: 1107, want: true, known: true},
		{name: "stone", id: 1, want: false, known: true},
		{name: "air", id: 0, want: false, known: true},
		{name: "a block this version does not have", id: 60000, want: false, known: false},
	} {
		got, known := movement.ClimbableByID(test.id)
		if got != test.want || known != test.known {
			t.Errorf("ClimbableByID(%s) = %v, %v, want %v, %v", test.name, got, known, test.want, test.known)
		}
	}
}

// TestFallingAndClimbingResolveAStateToItsBlock pins the lookup that has no
// arithmetic behind it.
//
// A state identifier here is a registry index, so nothing shifts it back to a
// block; the registry has to search its ranges. A ladder owns eight states and
// a vine thirty-two, and every one of them has to come back climbable — a
// lookup that searched wrongly would answer for a neighbouring block and look
// entirely plausible doing it.
func TestFallingAndClimbingResolveAStateToItsBlock(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	movement := set.BlockMovement()

	for state := data.BlockStateID(5719); state <= 5726; state++ {
		if climbable, known := movement.ClimbableByState(state); !climbable || !known {
			t.Errorf("ladder state %d climbable = %v, known = %v; want true, true", state, climbable, known)
		}
	}
	for _, state := range []data.BlockStateID{8358, 8370, 8389} {
		if climbable, known := movement.ClimbableByState(state); !climbable || !known {
			t.Errorf("vine state %d climbable = %v, known = %v; want true, true", state, climbable, known)
		}
	}
	// One either side of the ladder's span, to catch a search that is off by
	// one and would otherwise pass every case above.
	for _, state := range []data.BlockStateID{5718, 5727} {
		if climbable, _ := movement.ClimbableByState(state); climbable {
			t.Errorf("state %d, which is not a ladder, reported climbable", state)
		}
	}
	if falls, known := movement.FallsByState(124); !falls || !known {
		t.Errorf("gravel falls = %v, known = %v; want true, true", falls, known)
	}
	if _, known := movement.FallsByState(60000); known {
		t.Error("a state past the end of the registry claimed a falling answer")
	}
}

// TestFallingIsSettledEvenWhereMovementIsNot pins the one block whose states
// disagree about stopping movement, and records that it agrees about falling.
//
// resin_brick_wall is the reason this version's movement measurement cannot be
// keyed by block: it is the one wall not registered forceSolidOn, so its
// unconnected states have no collision shape. ByID declines for it. The two
// new facts do not, because neither is computed from a shape.
func TestFallingIsSettledEvenWhereMovementIsNot(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	movement := set.BlockMovement()

	const resinBrickWall data.BlockID = 379

	if _, known := movement.ByID(resinBrickWall); known {
		t.Fatal("ByID answered for the block whose states disagree; the fixture this test rests on has moved")
	}
	falls, known := movement.FallsByID(resinBrickWall)
	if !known || falls {
		t.Errorf("resin brick wall falls = %v, known = %v; want false, true", falls, known)
	}
	climbable, known := movement.ClimbableByID(resinBrickWall)
	if !known || climbable {
		t.Errorf("resin brick wall climbable = %v, known = %v; want false, true", climbable, known)
	}
}
