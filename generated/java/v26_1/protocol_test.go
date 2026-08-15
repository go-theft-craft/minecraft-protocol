package v26_1

import (
	"slices"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

func TestProtocol775Descriptor(t *testing.T) {
	t.Parallel()

	descriptor := Protocol()
	if got := descriptor.ID(); got != "java/26.1" {
		t.Errorf("ID = %q, want java/26.1", got)
	}
	if got := descriptor.Edition(); got != protocol.EditionJava {
		t.Errorf("Edition = %q, want java", got)
	}
	version := descriptor.Version()
	if version.Name != "26.1" || version.Protocol != 775 {
		t.Errorf("Version = %+v, want {Name:26.1 Protocol:775}", version)
	}
}

// TestProtocol775PacketFactoriesCoverEveryState is the check that
// configuration is a real state here rather than a name in a list: a protocol
// that generated no configuration packets would still report the state and
// fail to resolve anything in it.
func TestProtocol775PacketFactoriesCoverEveryState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		state     protocol.State
		direction protocol.Direction
		id        int32
	}{
		{"handshake", StateHandshaking, protocol.DirectionServerbound, 0x00},
		{"status request", StateStatus, protocol.DirectionServerbound, 0x00},
		{"login start", StateLogin, protocol.DirectionServerbound, 0x00},
		{"login success", StateLogin, protocol.DirectionClientbound, 0x02},
		{"configuration finish", StateConfiguration, protocol.DirectionClientbound, 0x03},
		{"configuration acknowledgement", StateConfiguration, protocol.DirectionServerbound, 0x03},
		{"play login", StatePlay, protocol.DirectionClientbound, 0x31},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, ok := newPacket(test.state, test.direction, test.id); !ok {
				t.Errorf("no packet for state %q direction %d ID %#x", test.state, test.direction, test.id)
			}
		})
	}
}

func TestProtocol775SessionAcceptsEveryState(t *testing.T) {
	t.Parallel()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("NewLimits: %v", err)
	}
	session, err := Protocol().NewSession(protocol.RoleClient, limits)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for _, state := range []protocol.State{StateHandshaking, StateStatus, StateLogin, StateConfiguration, StatePlay} {
		if err := session.ValidateState(state); err != nil {
			t.Errorf("ValidateState(%q): %v", state, err)
		}
	}
	if err := session.ValidateState("nonesuch"); err == nil {
		t.Error("ValidateState accepted a state the protocol does not have")
	}
}

// TestRawHoldsEveryDataset pins that the raw set is the whole inventory the
// package was generated from, not a selection of it.
func TestRawHoldsEveryDataset(t *testing.T) {
	t.Parallel()

	names := Raw().Names()
	want := []string{
		"attributes", "biomes", "blockCollisionShapes", "blockLoot", "blocks",
		"commands", "effects", "enchantments", "entities", "entityLoot",
		"foods", "instruments", "items", "language", "loginPacket",
		"mapIcons", "materials", "particles", "proto", "protocol",
		"recipes", "sounds", "tints", "version", "windows",
	}
	if !slices.Equal(names, want) {
		t.Errorf("Raw().Names() = %v, want %v", names, want)
	}

	dataset, ok := Raw().Get("blocks")
	if !ok {
		t.Fatal("the raw set has no blocks dataset")
	}
	if dataset.Path != "data/pc/26.1/blocks.json" || len(dataset.Data) == 0 {
		t.Errorf("blocks dataset = {Path:%q bytes:%d}, want the upstream path and its bytes", dataset.Path, len(dataset.Data))
	}
	if Raw().Version().Protocol != 775 {
		t.Errorf("raw set protocol = %d, want 775", Raw().Version().Protocol)
	}
}
