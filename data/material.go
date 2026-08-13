package data

import "maps"

// Material describes a Minecraft tool material.
type Material struct {
	Name       string
	ToolSpeeds ToolSpeedIndex
}

// Clone returns a Material whose mutable fields do not alias the source.
func (m Material) Clone() Material {
	clone := m
	clone.ToolSpeeds = m.ToolSpeeds.Clone()

	return clone
}

// ToolSpeedIndex maps item IDs to tool speed multipliers.
type ToolSpeedIndex map[ItemID]float64

// Clone returns a tool-speed index that does not alias the source.
func (t ToolSpeedIndex) Clone() ToolSpeedIndex { return maps.Clone(t) }

// Materials is a collection of Minecraft tool materials.
type Materials []Material

// Clone returns materials whose mutable fields do not alias the source.
func (m Materials) Clone() Materials {
	if m == nil {
		return nil
	}

	clone := make(Materials, len(m))
	for index := range clone {
		clone[index] = m[index].Clone()
	}

	return clone
}
