package data

import (
	"maps"
	"slices"
)

// EntityMotion records the per-tick motion constants for one entity family.
type EntityMotion struct {
	Gravity        float64
	HorizontalDrag float64
	VerticalDrag   float64
	StepHeight     float64
}

// BlockSlipperinessIndex maps block names to their horizontal friction multiplier.
type BlockSlipperinessIndex map[string]float64

// Clone returns an index that does not alias the source.
func (b BlockSlipperinessIndex) Clone() BlockSlipperinessIndex { return maps.Clone(b) }

// EntityMotionIndex maps entity family names to motion constants.
type EntityMotionIndex map[string]EntityMotion

// Clone returns an index that does not alias the source.
func (e EntityMotionIndex) Clone() EntityMotionIndex { return maps.Clone(e) }

// Physics describes version-specific movement constants.
type Physics struct {
	DefaultSlipperiness float64
	BlockSlipperiness   BlockSlipperinessIndex
	SinTable            []float32
	EntityMotion        EntityMotionIndex
}

// Slipperiness returns the friction multiplier for a block, or the default.
func (p Physics) Slipperiness(block string) float64 {
	if value, ok := p.BlockSlipperiness[block]; ok {
		return value
	}

	return p.DefaultSlipperiness
}

// Clone returns Physics whose maps and slices do not alias the source.
func (p Physics) Clone() Physics {
	clone := p
	clone.BlockSlipperiness = p.BlockSlipperiness.Clone()
	clone.EntityMotion = p.EntityMotion.Clone()
	clone.SinTable = slices.Clone(p.SinTable)

	return clone
}
