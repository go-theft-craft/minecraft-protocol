package data

import "testing"

func TestPhysicsCloneDoesNotAlias(t *testing.T) {
	source := Physics{
		DefaultSlipperiness: 0.6,
		BlockSlipperiness:   BlockSlipperinessIndex{"ice": 0.98},
		SinTable:            []float32{0, 1},
		EntityMotion:        EntityMotionIndex{"player": {Gravity: 0.08}},
	}

	clone := source.Clone()
	clone.BlockSlipperiness["slime"] = 0.8
	clone.SinTable[0] = 5
	clone.EntityMotion["arrow"] = EntityMotion{Gravity: 0.05}

	if _, ok := source.BlockSlipperiness["slime"]; ok {
		t.Fatal("Physics clone modified source block slipperiness")
	}
	if source.SinTable[0] != 0 {
		t.Fatal("Physics clone modified source sin table")
	}
	if _, ok := source.EntityMotion["arrow"]; ok {
		t.Fatal("Physics clone modified source entity motion")
	}
}

func TestPhysicsSlipperinessFallsBackToDefault(t *testing.T) {
	physics := Physics{
		DefaultSlipperiness: 0.6,
		BlockSlipperiness:   BlockSlipperinessIndex{"ice": 0.98},
	}

	if got := physics.Slipperiness("ice"); got != 0.98 {
		t.Fatalf("Slipperiness(ice) = %v, want 0.98", got)
	}
	if got := physics.Slipperiness("stone"); got != 0.6 {
		t.Fatalf("Slipperiness(stone) = %v, want the default 0.6", got)
	}
}

func TestSetPhysicsReturnsCallerOwnedValue(t *testing.T) {
	set, err := NewSet(SetOptions{
		Physics: Physics{
			DefaultSlipperiness: 0.6,
			BlockSlipperiness:   BlockSlipperinessIndex{"ice": 0.98},
			SinTable:            []float32{0, 1},
			EntityMotion:        EntityMotionIndex{"player": {Gravity: 0.08}},
		},
	})
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}

	first := set.Physics()
	first.BlockSlipperiness["ice"] = 0
	first.SinTable[0] = 9

	second := set.Physics()
	if second.BlockSlipperiness["ice"] != 0.98 {
		t.Fatal("Set.Physics returned an aliased slipperiness index")
	}
	if second.SinTable[0] != 0 {
		t.Fatal("Set.Physics returned an aliased sin table")
	}
}
