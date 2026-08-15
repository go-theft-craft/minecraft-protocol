package current

import (
	"testing"

	v26_1 "github.com/go-theft-craft/minecraft-protocol/generated/java/v26_1"
)

// TestCurrentDelegates checks the one property this package has: it is the
// version package under another name, not a copy of it that could drift.
func TestCurrentDelegates(t *testing.T) {
	t.Parallel()

	if Protocol().ID() != v26_1.Protocol().ID() {
		t.Errorf("Protocol().ID() = %q, want %q", Protocol().ID(), v26_1.Protocol().ID())
	}
	if Protocol().ID() != Version {
		t.Errorf("Protocol().ID() = %q, want the declared version %q", Protocol().ID(), Version)
	}
	if Raw().Len() != v26_1.Raw().Len() {
		t.Errorf("Raw() holds %d datasets, want %d", Raw().Len(), v26_1.Raw().Len())
	}

	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	if set.Version().Protocol != 775 {
		t.Errorf("Data().Version().Protocol = %d, want 775", set.Version().Protocol)
	}
}
