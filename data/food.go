package data

// Food describes a Minecraft food item.
type Food struct {
	ID               ItemID
	Name             string
	DisplayName      string
	StackSize        int
	FoodPoints       float64
	Saturation       float64
	EffectiveQuality float64
	SaturationRatio  float64
	Variations       Variations
}

// Clone returns a Food whose mutable fields do not alias the source.
func (f Food) Clone() Food {
	clone := f
	clone.Variations = f.Variations.Clone()

	return clone
}

// Foods is a collection of Minecraft food items.
type Foods []Food

// Clone returns foods whose mutable fields do not alias the source.
func (f Foods) Clone() Foods {
	if f == nil {
		return nil
	}

	clone := make(Foods, len(f))
	for index := range clone {
		clone[index] = f[index].Clone()
	}

	return clone
}
