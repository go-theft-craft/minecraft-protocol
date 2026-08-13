package data

import "slices"

// EnchantmentID identifies a Minecraft enchantment.
type EnchantmentID int

// Enchantment describes a Minecraft enchantment.
type Enchantment struct {
	ID           EnchantmentID
	Name         string
	DisplayName  string
	MaxLevel     int
	MinCost      EnchantCost
	MaxCost      EnchantCost
	Exclude      []string
	Category     string
	Weight       int
	TreasureOnly bool
	Curse        bool
	Tradeable    bool
	Discoverable bool
}

// Enchantments is a collection of Minecraft enchantments.
type Enchantments []Enchantment

// Clone returns enchantments whose mutable fields do not alias the source.
func (e Enchantments) Clone() Enchantments {
	if e == nil {
		return nil
	}

	clone := make(Enchantments, len(e))
	for index := range clone {
		clone[index] = e[index].Clone()
	}

	return clone
}

// EnchantCost describes the linear cost calculation for an enchantment.
type EnchantCost struct {
	A int
	B int
}

// Clone returns an Enchantment whose mutable fields do not alias the source.
func (e Enchantment) Clone() Enchantment {
	clone := e
	clone.Exclude = slices.Clone(e.Exclude)

	return clone
}
