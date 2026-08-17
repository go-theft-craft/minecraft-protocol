package v26_1

import "testing"

// TestUnmeasuredVersionPublishesNoBlockMovement pins the shape of an absent
// measurement.
//
// Whether a block stops movement is read out of a Mojang jar, and nobody has
// run that extraction against this version yet. The registry is nil rather than
// empty because the two say different things: an empty registry answers "no
// block stops you" for every block, which is the failure mode this dataset
// exists to prevent. A caller that finds nil must refuse to walk rather than
// walk freely.
func TestUnmeasuredVersionPublishesNoBlockMovement(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}

	if movement := set.BlockMovement(); movement != nil {
		t.Fatalf("BlockMovement() = %T, want nil for a version nobody has measured", movement)
	}
}
