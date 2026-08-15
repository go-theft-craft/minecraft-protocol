package data

import "slices"

// TintKey is what a tint entry is keyed by. Every category keys by biome name
// except redstone, which keys by redstone power level and whose keys are the
// decimal levels as text.
type TintKey string

// Tint is one colour and the keys that take it.
type Tint struct {
	Keys []TintKey
	// Color is upstream's signed packed 0xAARRGGBB value.
	Color int
}

// Clone returns a Tint whose mutable fields do not alias the source.
func (t Tint) Clone() Tint {
	clone := t
	clone.Keys = slices.Clone(t.Keys)

	return clone
}

// TintCategory is one named group of tints, such as grass or foliage.
type TintCategory struct {
	Name string
	Data []Tint
}

// Clone returns a TintCategory whose mutable fields do not alias the source.
func (t TintCategory) Clone() TintCategory {
	clone := t
	if t.Data == nil {
		return clone
	}
	clone.Data = make([]Tint, len(t.Data))
	for index := range clone.Data {
		clone.Data[index] = t.Data[index].Clone()
	}

	return clone
}

// Tints is every tint category a version publishes, sorted by name.
type Tints []TintCategory

// Clone returns tints whose mutable fields do not alias the source.
func (t Tints) Clone() Tints {
	if t == nil {
		return nil
	}

	clone := make(Tints, len(t))
	for index := range clone {
		clone[index] = t[index].Clone()
	}

	return clone
}
