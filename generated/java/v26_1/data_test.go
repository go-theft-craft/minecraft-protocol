package v26_1

import (
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
