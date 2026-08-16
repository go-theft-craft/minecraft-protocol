package protocols_test

import (
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/protocols"
	"github.com/go-theft-craft/minecraft-protocol/replay"
)

func TestResolveFindsEveryListedProtocol(t *testing.T) {
	t.Parallel()

	ids := protocols.IDs()
	if len(ids) < 2 {
		t.Fatalf("protocols lists %d versions, want both supported ones", len(ids))
	}

	for _, id := range ids {
		descriptor, known := protocols.Resolve(id)
		if !known {
			t.Fatalf("Resolve(%q) reported unknown, but IDs lists it", id)
		}
		if descriptor.ID() != id {
			t.Fatalf("Resolve(%q) returned %q", id, descriptor.ID())
		}
	}
}

func TestResolveRejectsAnUnknownID(t *testing.T) {
	t.Parallel()

	if _, known := protocols.Resolve("java/9999"); known {
		t.Fatal("Resolve accepted a version that does not exist")
	}
}

func TestDefaultIsTheNewest(t *testing.T) {
	t.Parallel()

	if got, want := protocols.Default().ID(), protocols.IDs()[0]; got != want {
		t.Fatalf("Default is %q, want the newest %q", got, want)
	}
}

// TestResolveSatisfiesTheReplayResolver is the seam this package exists for.
func TestResolveSatisfiesTheReplayResolver(t *testing.T) {
	t.Parallel()

	var resolver replay.Resolver = replay.ResolverFunc(protocols.Resolve)
	if _, known := resolver.Resolve(protocols.Default().ID()); !known {
		t.Fatal("the default protocol did not resolve through the replay seam")
	}
}
