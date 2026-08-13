package data

import "slices"

// Item describes a Minecraft item.
type Item struct {
	ID                ItemID
	Name              string
	DisplayName       string
	StackSize         int
	MaxDurability     int
	EnchantCategories []string
	RepairWith        []string
	Variations        Variations
}

// Clone returns an Item whose mutable fields do not alias the source.
func (i Item) Clone() Item {
	clone := i
	clone.EnchantCategories = slices.Clone(i.EnchantCategories)
	clone.RepairWith = slices.Clone(i.RepairWith)
	clone.Variations = i.Variations.Clone()

	return clone
}

// Items is a collection of Minecraft items.
type Items []Item

// Clone returns items whose mutable fields do not alias the source.
func (i Items) Clone() Items {
	if i == nil {
		return nil
	}

	clone := make(Items, len(i))
	for index := range clone {
		clone[index] = i[index].Clone()
	}

	return clone
}
